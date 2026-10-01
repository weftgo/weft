package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
)

// The read side. Status is derived on read through obsdb.DeriveStatus —
// never stored — and the SQL WHERE that filters by status must say the
// same thing the derivation does, so both are built from
// statusExpr.

const runColumns = `run_id, parent_run_id, parent_call_id, trace_id, agent, provider, model,
	manifest_hash, weft_version, service, session_id, public_id, turn,
	playground, experiment_id, forked_from, meta,
	started_ns, finished_ns, last_seen_ns, finished_ok, failed, err,
	steps, pending, stop_reason,
	input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens,
	event_count, delta_count, message_count`

// statusExpr is the four-row table in SQL: it must agree with
// obsdb.DeriveStatus, whose test pins the boundaries. One bind
// parameter: the interrupted cutoff (now - InterruptedAfter).
const statusExpr = `CASE WHEN failed = 1 THEN 'failed'
	WHEN finished_ok = 1 THEN 'succeeded'
	WHEN last_seen_ns >= ? THEN 'running'
	ELSE 'interrupted' END`

func (d *DB) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunPage{}, err
	}
	now := time.Now()
	where, args := runWhere(q, now)
	var total int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE `+where, args...).Scan(&total); err != nil {
		return obsdb.RunPage{}, err
	}
	limit := obsdb.LimitOf(q.Limit)
	frag, qargs := where, args
	if !q.Before.IsZero() {
		frag += " AND started_ns < ?"
		qargs = append(append([]any{}, args...), q.Before.UnixNano())
	}
	frag += ` ORDER BY started_ns DESC, run_id DESC LIMIT ?`
	qargs = append(append([]any{}, qargs...), limit)
	rows, err := d.queryRunRows(ctx, frag, qargs)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	page := obsdb.RunPage{Runs: rows, Total: total}
	if len(rows) == limit {
		last := rows[len(rows)-1].Started
		page.NextBefore = &last
	}
	return page, nil
}

// runWhere builds the Runs WHERE fragment with its bind arguments, in
// text order. The status filter's condition carries statusExpr, whose
// own cutoff parameter binds first, so it is added before everything
// else. now is the read's clock — the same one scanRunRow derives
// statuses with, so filtering and rows cannot disagree.
func runWhere(q obsdb.RunQuery, now time.Time) (string, []any) {
	var conds []string
	var args []any
	if q.Status != "" {
		cutoff := now.Add(-obsdb.InterruptedAfter).UnixNano()
		conds = append(conds, "("+statusExpr+") = ?")
		args = append(args, cutoff, string(q.Status))
	}
	conds = append(conds, "1=1")
	if q.Agent != "" {
		conds = append(conds, "agent = ?")
		args = append(args, q.Agent)
	}
	if q.SessionID != "" {
		conds = append(conds, "session_id = ?")
		args = append(args, q.SessionID)
	}
	if q.PublicID != "" {
		conds = append(conds, "public_id = ?")
		args = append(args, q.PublicID)
	}
	switch q.ParentRunID {
	case "":
		conds = append(conds, "parent_run_id = ''")
	case "*":
	default:
		conds = append(conds, "parent_run_id = ?")
		args = append(args, q.ParentRunID)
	}
	if q.Playground != nil {
		conds = append(conds, "playground = ?")
		args = append(args, boolInt(*q.Playground))
	}
	for k, v := range q.Meta {
		// Key and value are both bound parameters (the store's finding:
		// a JSON path would have to quote the key into SQL).
		conds = append(conds, "EXISTS (SELECT 1 FROM json_each(runs.meta) WHERE json_each.key = ? AND json_each.value = ?)")
		args = append(args, k, v)
	}
	return strings.Join(conds, " AND "), args
}

func (d *DB) Run(ctx context.Context, id string) (obsdb.RunDetail, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunDetail{}, err
	}
	rows, err := d.queryRunRows(ctx, "run_id = ?", []any{id})
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	if len(rows) == 0 {
		return obsdb.RunDetail{}, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, id)
	}
	children, err := d.queryRunRows(ctx,
		"parent_run_id = ? ORDER BY started_ns, run_id", []any{id})
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	return obsdb.RunDetail{RunRow: rows[0], Children: children}, nil
}

// queryRunRows selects run rows for a WHERE fragment. The fragment's
// args come first; when the fragment references statusExpr the cutoff
// is already among the caller's args (runWhere puts status filtering in
// its own prelude — see Runs). limit is always last.
func (d *DB) queryRunRows(ctx context.Context, where string, args []any) ([]obsdb.RunRow, error) {
	rows, err := d.runRowsQuery(ctx, where, args)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []obsdb.RunRow
	for _, r := range rows {
		row, err := scanRunRow(r, now)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (d *DB) runRowsQuery(ctx context.Context, where string, args []any) ([]runRowScan, error) {
	rs, err := d.reads.QueryContext(ctx, `SELECT `+runColumns+` FROM runs WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []runRowScan
	for rs.Next() {
		var r runRowScan
		if err := rs.Scan(&r.ID, &r.ParentRunID, &r.ParentCallID, &r.TraceID, &r.Agent, &r.Provider,
			&r.Model, &r.ManifestHash, &r.WeftVersion, &r.Service, &r.SessionID, &r.PublicID, &r.Turn,
			&r.Playground, &r.ExperimentID, &r.ForkedFrom, &r.Meta,
			&r.Started, &r.Finished, &r.LastSeen, &r.FinishedOK, &r.Failed, &r.Err,
			&r.Steps, &r.Pending, &r.StopReason,
			&r.InTok, &r.OutTok, &r.CachedTok, &r.CacheWrite, &r.ReasonTok,
			&r.EventCount, &r.DeltaCount, &r.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

type runRowScan struct {
	ID, ParentRunID, ParentCallID, TraceID            string
	Agent, Provider, Model, ManifestHash, WeftVersion string
	Service, SessionID, PublicID                      string
	Turn                                              int
	Playground                                        bool
	ExperimentID, ForkedFrom, Meta                    string
	Started, LastSeen                                 int64
	Finished                                          sql.NullInt64
	FinishedOK, Failed                                bool
	Err, StopReason                                   string
	Steps, Pending                                    int
	InTok, OutTok, CachedTok, CacheWrite, ReasonTok   int64
	EventCount, DeltaCount, MessageCount              int64
}

func scanRunRow(r runRowScan, now time.Time) (obsdb.RunRow, error) {
	row := obsdb.RunRow{
		ID: r.ID, ParentRunID: r.ParentRunID, ParentCallID: r.ParentCallID, TraceID: r.TraceID,
		Agent: r.Agent, Provider: r.Provider, Model: r.Model,
		ManifestHash: r.ManifestHash, WeftVersion: r.WeftVersion, Service: r.Service,
		SessionID: r.SessionID, PublicID: r.PublicID, Turn: r.Turn,
		Playground: r.Playground, ExperimentID: r.ExperimentID, ForkedFrom: r.ForkedFrom,
		Started:  time.Unix(0, r.Started).UTC(),
		LastSeen: time.Unix(0, r.LastSeen).UTC(),
		Err:      r.Err, Steps: r.Steps, Pending: r.Pending, StopReason: r.StopReason,
		Usage: weft.Usage{
			InputTokens: r.InTok, OutputTokens: r.OutTok,
			CachedInputTokens: r.CachedTok, CacheWriteTokens: r.CacheWrite,
			ReasoningTokens: r.ReasonTok,
		},
		EventCount: r.EventCount, DeltaCount: r.DeltaCount, MessageCount: r.MessageCount,
	}
	if r.Finished.Valid {
		f := time.Unix(0, r.Finished.Int64).UTC()
		row.Finished = &f
	}
	if r.Meta != "" && r.Meta != "{}" {
		if err := json.Unmarshal([]byte(r.Meta), &row.Meta); err != nil {
			return obsdb.RunRow{}, fmt.Errorf("sqlite: run %s: meta column: %w", r.ID, err)
		}
	}
	row.Status = obsdb.DeriveStatus(r.Failed, r.FinishedOK, row.LastSeen, now)
	return row, nil
}

func (d *DB) Events(ctx context.Context, runID string, after int64, limit int) (obsdb.EventPage, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.EventPage{}, err
	}
	if err := d.runExists(ctx, runID); err != nil {
		return obsdb.EventPage{}, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	page := obsdb.EventPage{Done: true}
	rs, err := d.reads.QueryContext(ctx,
		`SELECT pos, time_ns, body FROM records WHERE run_id = ? AND kind = 'event' AND pos > ? ORDER BY pos LIMIT ?`,
		runID, after, limit)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var ev obsdb.PosEvent
		var ns int64
		var body []byte
		if err := rs.Scan(&ev.Pos, &ns, &body); err != nil {
			return obsdb.EventPage{}, err
		}
		ev.Time = time.Unix(0, ns).UTC()
		ev.Event = json.RawMessage(body)
		page.Events = append(page.Events, ev)
	}
	if err := rs.Err(); err != nil {
		return obsdb.EventPage{}, err
	}
	var remaining int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM records WHERE run_id = ? AND kind = 'event' AND pos > ?`, runID, after).
		Scan(&remaining); err != nil {
		return obsdb.EventPage{}, err
	}
	if remaining > len(page.Events) {
		page.Done = false
		if n := len(page.Events); n > 0 {
			last := page.Events[n-1].Pos
			page.NextAfter = &last
		}
	} else {
		// Every stored event returned: done only when the run is
		// terminal — a running run may still emit more.
		var finishedOK, failed bool
		if err := d.reads.QueryRowContext(ctx,
			`SELECT finished_ok, failed FROM runs WHERE run_id = ?`, runID).Scan(&finishedOK, &failed); err != nil {
			return obsdb.EventPage{}, err
		}
		page.Done = finishedOK || failed
	}
	gaps, err := d.eventGaps(ctx, runID)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	page.Gaps = gaps
	return page, nil
}

// eventGaps returns the durable positions missing below the high-water
// mark: a lost batch, never a delta (deltas are on their own counter,
// so their absence cannot open a hole here).
func (d *DB) eventGaps(ctx context.Context, runID string) ([]int64, error) {
	rs, err := d.reads.QueryContext(ctx,
		`SELECT pos FROM records WHERE run_id = ? AND kind = 'event' ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var present []int64
	for rs.Next() {
		var p int64
		if err := rs.Scan(&p); err != nil {
			return nil, err
		}
		present = append(present, p)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	var gaps []int64
	next := int64(0)
	for _, p := range present {
		for p > next {
			gaps = append(gaps, next)
			next++
		}
		if p == next {
			next++
		}
	}
	return gaps, nil
}

func (d *DB) Transcript(ctx context.Context, runID string) ([]json.RawMessage, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	rs, err := d.reads.QueryContext(ctx,
		`SELECT body FROM records WHERE run_id = ? AND kind = 'messages' ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []json.RawMessage
	for rs.Next() {
		var body []byte
		if err := rs.Scan(&body); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(body))
	}
	return out, rs.Err()
}

func (d *DB) RunSpans(ctx context.Context, runID string) ([]obsdb.Span, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	return d.querySpans(ctx, `run_id = ? ORDER BY start_ns, span_id`, runID)
}

func (d *DB) Trace(ctx context.Context, traceID string) ([]obsdb.Span, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	return d.querySpans(ctx, `trace_id = ? ORDER BY start_ns, span_id`, traceID)
}

func (d *DB) querySpans(ctx context.Context, where string, arg any) ([]obsdb.Span, error) {
	rs, err := d.reads.QueryContext(ctx, `SELECT trace_id, span_id, parent_span_id, name, kind,
		start_ns, end_ns, status_code, status_message, service, attrs, resource, events
		FROM spans WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.Span
	for rs.Next() {
		var s obsdb.Span
		var attrs, resource, events string
		var start, end int64
		if err := rs.Scan(&s.TraceID, &s.SpanID, &s.ParentSpanID, &s.Name, &s.Kind,
			&start, &end, &s.StatusCode, &s.StatusMessage, &s.Service, &attrs, &resource, &events); err != nil {
			return nil, err
		}
		s.Start, s.End = time.Unix(0, start).UTC(), time.Unix(0, end).UTC()
		if attrs != "" && attrs != "null" {
			if err := unmarshalAttrs([]byte(attrs), &s.Attrs); err != nil {
				return nil, fmt.Errorf("sqlite: span %s attrs: %w", s.SpanID, err)
			}
		}
		if resource != "" && resource != "null" {
			if err := unmarshalAttrs([]byte(resource), &s.Resource); err != nil {
				return nil, fmt.Errorf("sqlite: span %s resource: %w", s.SpanID, err)
			}
		}
		if events != "" && events != "[]" && events != "null" {
			if err := unmarshalAttrs([]byte(events), &s.Events); err != nil {
				return nil, fmt.Errorf("sqlite: span %s events: %w", s.SpanID, err)
			}
		}
		out = append(out, s)
	}
	return out, rs.Err()
}

// unmarshalAttrs decodes a JSON column keeping integral numbers as
// int64, so a stored attribute map equals the OTLP-decoded one (plain
// json.Unmarshal would turn every number into float64).
func unmarshalAttrs(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	normalizeNumbers(v)
	return nil
}

func normalizeNumbers(v any) {
	switch x := v.(type) {
	case *map[string]any:
		for k, e := range *x {
			(*x)[k] = normalized(e)
		}
	case *[]obsdb.SpanEvent:
		for i := range *x {
			normalizeNumbers(&(*x)[i].Attrs)
		}
	}
}

func normalized(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = normalized(e)
		}
		return x
	case []any:
		for i := range x {
			x[i] = normalized(x[i])
		}
		return x
	}
	return v
}

func (d *DB) runExists(ctx context.Context, runID string) error {
	var one int
	err := d.reads.QueryRowContext(ctx, `SELECT 1 FROM runs WHERE run_id = ?`, runID).Scan(&one)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: run %s", obsdb.ErrNotFound, runID)
	}
	return err
}

// Sessions is a GROUP BY session_id over top-level, non-playground
// runs (S3.4): turns, first/last seen, summed usage, and the newest
// turn's derived status.
func (d *DB) Sessions(ctx context.Context, q obsdb.SessionQuery) (obsdb.SessionPage, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionPage{}, err
	}
	cutoff := time.Now().Add(-obsdb.InterruptedAfter).UnixNano()
	conds := []string{"parent_run_id = ''", "playground = 0", "session_id <> ''"}
	var having []string
	var hargs []any
	if q.Agent != "" {
		having = append(having, "MAX(agent) = ?")
		hargs = append(hargs, q.Agent)
	}
	if q.PublicID != "" {
		having = append(having, "MAX(public_id) = ?")
		hargs = append(hargs, q.PublicID)
	}
	if !q.Before.IsZero() {
		having = append(having, "MAX(last_seen_ns) < ?")
		hargs = append(hargs, q.Before.UnixNano())
	}
	havingSQL := ""
	if len(having) > 0 {
		havingSQL = " HAVING " + strings.Join(having, " AND ")
	}
	where := strings.Join(conds, " AND ")
	var total int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT session_id FROM runs WHERE `+where+
			` GROUP BY session_id`+havingSQL+`)`, hargs...).Scan(&total); err != nil {
		return obsdb.SessionPage{}, err
	}
	limit := obsdb.LimitOf(q.Limit)
	rs, err := d.reads.QueryContext(ctx, `SELECT session_id, MAX(public_id), MAX(agent), COUNT(*),
		MIN(started_ns), MAX(last_seen_ns),
		SUM(input_tokens), SUM(output_tokens), SUM(cached_input_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens),
		(SELECT `+statusExpr+` FROM runs r2 WHERE r2.session_id = runs.session_id
		 AND r2.parent_run_id = '' AND r2.playground = 0
		 ORDER BY r2.turn DESC, r2.started_ns DESC LIMIT 1)
		FROM runs WHERE `+where+` GROUP BY session_id`+havingSQL+
		` ORDER BY MAX(last_seen_ns) DESC LIMIT ?`, append([]any{cutoff}, append(hargs, limit)...)...)
	if err != nil {
		return obsdb.SessionPage{}, err
	}
	defer func() { _ = rs.Close() }()
	page := obsdb.SessionPage{Total: total}
	for rs.Next() {
		var row obsdb.SessionRow
		var first, last int64
		var inTok, outTok, cached, cacheW, reason sql.NullInt64
		var status string
		if err := rs.Scan(&row.ID, &row.PublicID, &row.Agent, &row.Turns,
			&first, &last, &inTok, &outTok, &cached, &cacheW, &reason, &status); err != nil {
			return obsdb.SessionPage{}, err
		}
		row.FirstSeen = time.Unix(0, first).UTC()
		row.LastSeen = time.Unix(0, last).UTC()
		row.Status = obsdb.Status(status)
		row.Usage = weft.Usage{
			InputTokens: inTok.Int64, OutputTokens: outTok.Int64,
			CachedInputTokens: cached.Int64, CacheWriteTokens: cacheW.Int64,
			ReasoningTokens: reason.Int64,
		}
		page.Sessions = append(page.Sessions, row)
	}
	if err := rs.Err(); err != nil {
		return obsdb.SessionPage{}, err
	}
	if len(page.Sessions) == limit {
		last := page.Sessions[len(page.Sessions)-1].LastSeen
		page.NextBefore = &last
	}
	return page, nil
}

func (d *DB) Session(ctx context.Context, id string) (obsdb.SessionDetail, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionDetail{}, err
	}
	page, err := d.Sessions(ctx, obsdb.SessionQuery{Limit: 500})
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	var row obsdb.SessionRow
	found := false
	for _, s := range page.Sessions {
		if s.ID == id {
			row, found = s, true
			break
		}
	}
	if !found {
		return obsdb.SessionDetail{}, fmt.Errorf("%w: session %s", obsdb.ErrNotFound, id)
	}
	runs, err := d.queryRunRows(ctx,
		`session_id = ? AND parent_run_id = '' AND playground = 0 ORDER BY turn, started_ns, run_id LIMIT ?`,
		[]any{id, 500})
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	return obsdb.SessionDetail{SessionRow: row, Runs: runs}, nil
}

func (d *DB) ResolvePublicID(ctx context.Context, publicID string) (string, error) {
	if err := d.checkOpen(); err != nil {
		return "", err
	}
	var sessionID string
	err := d.reads.QueryRowContext(ctx,
		`SELECT session_id FROM runs WHERE public_id = ? AND session_id <> '' ORDER BY turn DESC, started_ns DESC LIMIT 1`,
		publicID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: public id %s", obsdb.ErrNotFound, publicID)
	}
	return sessionID, err
}
