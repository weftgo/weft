package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
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
	event_count, delta_count, message_count,
	instructions_hash, catalog_hash, request_count`

// statusExpr is the four-row table in SQL: it must agree with
// obsdb.DeriveStatus, whose test pins the boundaries. One bind
// parameter: the interrupted cutoff (now - InterruptedAfter).
const statusExpr = `CASE WHEN failed = 1 THEN 'failed'
	WHEN finished_ok = 1 THEN 'succeeded'
	WHEN last_seen_ns >= ? THEN 'running'
	ELSE 'interrupted' END`

func (d *DB) Runs(ctx context.Context, q obsdb.RunQuery) (_ obsdb.RunPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunPage{}, err
	}
	defer d.closedErr(&err)
	now := time.Now()
	where, args := runWhere(q, now)
	var total int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE `+where, args...).Scan(&total); err != nil {
		return obsdb.RunPage{}, err
	}
	limit := obsdb.LimitOf(q.Limit)
	frag, qargs := where, append([]any{}, args...)
	if !q.Before.IsZero() {
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			frag += " AND (started_ns < ? OR (started_ns = ? AND run_id < ?))"
			qargs = append(qargs, ns, ns, q.BeforeID)
		} else {
			frag += " AND started_ns < ?"
			qargs = append(qargs, ns)
		}
	}
	frag += ` ORDER BY started_ns DESC, run_id DESC LIMIT ?`
	qargs = append(qargs, limit)
	rows, err := d.queryRunRows(ctx, frag, qargs, now)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	page := obsdb.RunPage{Runs: rows, Total: total}
	if len(rows) == limit {
		// A page does not end inside a group of runs sharing one
		// Started: a caller paging on Before alone reads strictly older
		// next, and the rest of the group would never be listed. The
		// tied rows ride along — at most MaxTies of them, so one page
		// stays bounded however many runs share a timestamp; past that
		// the (Before, BeforeID) cursor resumes inside the group.
		last := rows[len(rows)-1]
		ties, err := d.queryRunRows(ctx,
			where+` AND started_ns = ? AND run_id < ? ORDER BY run_id DESC LIMIT ?`,
			append(append([]any{}, args...), last.Started.UnixNano(), last.ID, obsdb.MaxTies), now)
		if err != nil {
			return obsdb.RunPage{}, err
		}
		page.Runs = append(page.Runs, ties...)
		end := page.Runs[len(page.Runs)-1]
		page.NextBefore, page.NextBeforeID = &end.Started, end.ID
	}
	return page, nil
}

// runWhere builds the Runs WHERE fragment with its bind arguments, in
// text order. The status filter's condition carries statusExpr, whose
// own cutoff parameter binds first, so it is added before everything
// else. now is the read's clock — the same one queryRunRows derives
// the rows' statuses with, so filtering and rows cannot disagree.
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
	if q.ExperimentID != "" {
		conds = append(conds, "experiment_id = ?")
		args = append(args, q.ExperimentID)
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

func (d *DB) Run(ctx context.Context, id string) (_ obsdb.RunDetail, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunDetail{}, err
	}
	defer d.closedErr(&err)
	now := time.Now()
	rows, err := d.queryRunRows(ctx, "run_id = ?", []any{id}, now)
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	if len(rows) == 0 {
		return obsdb.RunDetail{}, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, id)
	}
	children, err := d.queryRunRows(ctx,
		"parent_run_id = ? ORDER BY started_ns, run_id", []any{id}, now)
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	return obsdb.RunDetail{RunRow: rows[0], Children: children}, nil
}

// queryRunRows selects run rows for a WHERE fragment. The fragment's
// args come first; when the fragment references statusExpr the cutoff
// is already among the caller's args (runWhere puts status filtering in
// its own prelude — see Runs). limit is always last. now is the clock
// the rows' statuses derive from — the caller's, so a read that also
// filters or groups by status uses one clock for both.
func (d *DB) queryRunRows(ctx context.Context, where string, args []any, now time.Time) ([]obsdb.RunRow, error) {
	rows, err := d.runRowsQuery(ctx, where, args)
	if err != nil {
		return nil, err
	}
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
			&r.EventCount, &r.DeltaCount, &r.MessageCount,
			&r.InstructionsHash, &r.CatalogHash, &r.RequestCount); err != nil {
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
	InstructionsHash, CatalogHash                     string
	RequestCount                                      int64
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
		Usage: core.Usage{
			InputTokens: r.InTok, OutputTokens: r.OutTok,
			CachedInputTokens: r.CachedTok, CacheWriteTokens: r.CacheWrite,
			ReasoningTokens: r.ReasonTok,
		},
		EventCount: r.EventCount, DeltaCount: r.DeltaCount, MessageCount: r.MessageCount,
		InstructionsHash: r.InstructionsHash, CatalogHash: r.CatalogHash, RequestCount: r.RequestCount,
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

func (d *DB) Events(ctx context.Context, runID string, after int64, limit int) (_ obsdb.EventPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.EventPage{}, err
	}
	defer d.closedErr(&err)
	// The run's terminal flags are read before its events, never after:
	// a batch committing between the two reads (the last events plus
	// run_finish) would otherwise make a page that is Done yet misses
	// those events, and a reader following the run to Done would stop
	// short of its tail. Read in this order, Done only ever says "the
	// run was already terminal when these events were read".
	var finishedOK, failed bool
	var eventCount int64
	err = d.reads.QueryRowContext(ctx,
		`SELECT finished_ok, failed, event_count FROM runs WHERE run_id = ?`, runID).
		Scan(&finishedOK, &failed, &eventCount)
	if err == sql.ErrNoRows {
		return obsdb.EventPage{}, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, runID)
	}
	if err != nil {
		return obsdb.EventPage{}, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	// One row past the limit answers "is there more" in the same read.
	rs, err := d.reads.QueryContext(ctx,
		`SELECT pos, time_ns, body, attrs FROM records WHERE run_id = ? AND kind = 'event' AND pos > ? ORDER BY pos LIMIT ?`,
		runID, after, limit+1)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	var page obsdb.EventPage
	for rs.Next() {
		var ev obsdb.PosEvent
		var ns int64
		var body, attrs []byte
		if err := rs.Scan(&ev.Pos, &ns, &body, &attrs); err != nil {
			return obsdb.EventPage{}, err
		}
		ev.Time = timeOf(ns)
		ev.Event = json.RawMessage(body)
		var st obsdb.StoredRecord
		if err := storedAttrs(attrs, &st); err != nil {
			return obsdb.EventPage{}, err
		}
		ev.Content, ev.TruncatedBytes = st.Content, st.TruncatedBytes
		page.Events = append(page.Events, ev)
	}
	if err := rs.Err(); err != nil {
		return obsdb.EventPage{}, err
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		last := page.Events[limit-1].Pos
		page.NextAfter = &last
	} else {
		// Every stored event returned: done only when the run is
		// terminal — a running run may still emit more.
		page.Done = finishedOK || failed
	}
	gaps, err := d.eventGaps(ctx, runID, eventCount)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	page.Gaps = gaps
	return page, nil
}

// eventGaps returns the durable positions missing below the high-water
// mark: a lost batch, never a delta (deltas are on their own counter,
// so their absence cannot open a hole here). At most obsdb.MaxGaps are
// listed.
//
// count is the run's event_count, read before this call. Positions are
// unique per run, so count rows inside 0..max with count = max+1 leave
// no room for a hole: the common, gapless run is answered from the two
// ends of the key without reading every position on every page. A
// count read earlier than the positions can only be too small, which
// falls through to the scan.
func (d *DB) eventGaps(ctx context.Context, runID string, count int64) ([]int64, error) {
	var lo, hi sql.NullInt64
	if err := d.reads.QueryRowContext(ctx,
		`SELECT (SELECT MIN(pos) FROM records WHERE run_id = ? AND kind = 'event'),
		        (SELECT MAX(pos) FROM records WHERE run_id = ? AND kind = 'event')`,
		runID, runID).Scan(&lo, &hi); err != nil {
		return nil, err
	}
	if !hi.Valid || (lo.Int64 >= 0 && count == hi.Int64+1) {
		return nil, nil
	}
	rs, err := d.reads.QueryContext(ctx,
		`SELECT pos FROM records WHERE run_id = ? AND kind = 'event' AND pos >= 0 ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var gaps []int64
	next := int64(0)
	for rs.Next() && len(gaps) < obsdb.MaxGaps {
		var p int64
		if err := rs.Scan(&p); err != nil {
			return nil, err
		}
		// The hole below p, bounded: a stray position (a foreign
		// sender's weft.event.pos of 10^12) must not turn one page
		// read into a trillion-element list.
		for ; next < p && len(gaps) < obsdb.MaxGaps; next++ {
			gaps = append(gaps, next)
		}
		next = p + 1
	}
	return gaps, rs.Err()
}

func (d *DB) Transcript(ctx context.Context, runID string) ([]json.RawMessage, error) {
	batches, err := d.TranscriptBatches(ctx, runID)
	return obsdb.TranscriptBodies(batches), err
}

func (d *DB) TranscriptBatches(ctx context.Context, runID string) (_ []obsdb.TranscriptBatch, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	// step is the record's weft.step.index, stored by the write path
	// (-1 when absent); the input flag is read from the attribute column.
	rs, err := d.reads.QueryContext(ctx,
		`SELECT pos, step, body, attrs FROM records WHERE run_id = ? AND kind = 'messages' AND pos >= 0 ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.TranscriptBatch
	for rs.Next() {
		var (
			b     obsdb.TranscriptBatch
			body  []byte
			attrs []byte
		)
		if err := rs.Scan(&b.Index, &b.Step, &body, &attrs); err != nil {
			return nil, err
		}
		input, reason := messagesFlags(attrs)
		if reason != "" {
			continue // a compaction view (ADR 0028 §8) is not transcript: Compactions reads it
		}
		b.Messages = json.RawMessage(body)
		b.Input = input
		out = append(out, b)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	// A resume's rebuilt tool message supersedes the partial one its
	// input record carried (the later record is the authoritative one).
	return obsdb.DedupBatches(out), nil
}

// messagesFlags reads weft.messages.input (a bool, or the string an
// OTLP sender may have sent) and weft.messages.reason from a record's
// stored attributes.
func messagesFlags(attrs []byte) (input bool, reason string) {
	var a struct {
		Input  any `json:"weft.messages.input"`
		Reason any `json:"weft.messages.reason"`
	}
	if json.Unmarshal(attrs, &a) != nil {
		return false, ""
	}
	switch v := a.Input.(type) {
	case bool:
		input = v
	case string:
		input = v == "true"
	}
	// A reason of any type reads as "not growth", as ClickHouse's
	// stringified column does: a non-string reason must not demote a
	// view into the transcript on one backend only.
	switch v := a.Reason.(type) {
	case nil:
	case string:
		reason = v
	default:
		reason = fmt.Sprint(v)
	}
	return input, reason
}

func (d *DB) Compactions(ctx context.Context, runID string) (_ []obsdb.Compaction, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	// The views are messages records whose attributes name a reason
	// (the instr narrows the scan; CompactionOf decides), the marker a
	// record of thread's compaction kind.
	rs, err := d.reads.QueryContext(ctx,
		`SELECT kind, pos, step, body, attrs FROM records WHERE run_id = ?
		AND (kind = 'compaction' OR (kind = 'messages' AND instr(attrs, '"weft.messages.reason"') > 0))
		ORDER BY time_ns, pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []obsdb.Compaction{}
	for rs.Next() {
		var (
			kind        string
			pos         int64
			step        int
			body, attrs []byte
		)
		if err := rs.Scan(&kind, &pos, &step, &body, &attrs); err != nil {
			return nil, err
		}
		var m map[string]any
		if len(attrs) > 0 {
			if err := unmarshalAttrs(attrs, &m); err != nil {
				return nil, fmt.Errorf("sqlite: run %s record %d attrs: %w", runID, pos, err)
			}
		}
		if kind == obsdb.RecordCompaction {
			pos = -1 // a marker's stored position is derived from its hash, not an index
		}
		c, ok, err := obsdb.CompactionOf(kind, pos, step, m, body)
		if err != nil {
			return nil, fmt.Errorf("sqlite: run %s: %w", runID, err)
		}
		if ok {
			out = append(out, c)
		}
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	obsdb.SortCompactions(out)
	return out, nil
}

func (d *DB) RunSpans(ctx context.Context, runID string) (_ []obsdb.Span, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	return d.querySpans(ctx, `run_id = ? ORDER BY start_ns, span_id`, runID)
}

func (d *DB) Trace(ctx context.Context, traceID string) (_ []obsdb.Span, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
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
		s.Start, s.End = timeOf(start), timeOf(end)
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

// timeOf reads a span or record *_ns column: 0 is the zero time Write
// stored for it (unixNS), anything else the instant.
func timeOf(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
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

// sessionScope is what a session is grouped over (S3.4): its
// top-level, non-playground runs.
const sessionScope = `parent_run_id = '' AND playground = 0 AND session_id <> ''`

// Sessions is a GROUP BY session_id over top-level, non-playground
// runs (S3.4): turns, first/last seen, summed usage, and the newest
// turn's derived status.
func (d *DB) Sessions(ctx context.Context, q obsdb.SessionQuery) (_ obsdb.SessionPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionPage{}, err
	}
	defer d.closedErr(&err)
	cutoff := time.Now().Add(-obsdb.InterruptedAfter).UnixNano()
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
	// Total is the whole match, as Runs' is: the cursor pages through
	// it and never narrows it.
	var total int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT session_id FROM runs WHERE `+sessionScope+
			` GROUP BY session_id`+havingSQL(having)+`)`, hargs...).Scan(&total); err != nil {
		return obsdb.SessionPage{}, err
	}
	limit := obsdb.LimitOf(q.Limit)
	paged, pargs := having, hargs
	if !q.Before.IsZero() {
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			paged = append(append([]string{}, having...), "(MAX(last_seen_ns) < ? OR (MAX(last_seen_ns) = ? AND session_id < ?))")
			pargs = append(append([]any{}, hargs...), ns, ns, q.BeforeID)
		} else {
			paged = append(append([]string{}, having...), "MAX(last_seen_ns) < ?")
			pargs = append(append([]any{}, hargs...), ns)
		}
	}
	rows, err := d.sessionRows(ctx, cutoff, sessionScope, nil, paged,
		append(append([]any{}, pargs...), limit), ` ORDER BY MAX(last_seen_ns) DESC, session_id DESC LIMIT ?`)
	if err != nil {
		return obsdb.SessionPage{}, err
	}
	page := obsdb.SessionPage{Sessions: rows, Total: total}
	if len(rows) == limit {
		// Runs' rule: the sessions sharing the last row's LastSeen ride
		// along (at most MaxTies) instead of being skipped by a next
		// page that reads strictly older.
		last := rows[len(rows)-1]
		ties, err := d.sessionRows(ctx, cutoff, sessionScope, nil,
			append(append([]string{}, having...), "MAX(last_seen_ns) = ?", "session_id < ?"),
			append(append([]any{}, hargs...), last.LastSeen.UnixNano(), last.ID, obsdb.MaxTies),
			` ORDER BY session_id DESC LIMIT ?`)
		if err != nil {
			return obsdb.SessionPage{}, err
		}
		page.Sessions = append(page.Sessions, ties...)
		end := page.Sessions[len(page.Sessions)-1]
		page.NextBefore, page.NextBeforeID = &end.LastSeen, end.ID
	}
	return page, nil
}

func havingSQL(having []string) string {
	if len(having) == 0 {
		return ""
	}
	return " HAVING " + strings.Join(having, " AND ")
}

// sessionRows selects grouped session rows: where (with wargs) scopes
// the runs, having (with hargs, which also carry tail's parameters)
// filters the groups, tail orders and limits. cutoff is statusExpr's
// parameter, the first in text order.
func (d *DB) sessionRows(ctx context.Context, cutoff int64, where string, wargs []any,
	having []string, hargs []any, tail string) ([]obsdb.SessionRow, error) {
	args := append(append([]any{cutoff}, wargs...), hargs...)
	rs, err := d.reads.QueryContext(ctx, `SELECT session_id, MAX(public_id), MAX(agent), COUNT(*),
		MIN(started_ns), MAX(last_seen_ns),
		SUM(input_tokens), SUM(output_tokens), SUM(cached_input_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens),
		(SELECT `+statusExpr+` FROM runs r2 WHERE r2.session_id = runs.session_id
		 AND r2.parent_run_id = '' AND r2.playground = 0
		 ORDER BY r2.turn DESC, r2.started_ns DESC LIMIT 1)
		FROM runs WHERE `+where+` GROUP BY session_id`+havingSQL(having)+tail, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.SessionRow
	for rs.Next() {
		var row obsdb.SessionRow
		var first, last int64
		var inTok, outTok, cached, cacheW, reason sql.NullInt64
		var status string
		if err := rs.Scan(&row.ID, &row.PublicID, &row.Agent, &row.Turns,
			&first, &last, &inTok, &outTok, &cached, &cacheW, &reason, &status); err != nil {
			return nil, err
		}
		row.FirstSeen = time.Unix(0, first).UTC()
		row.LastSeen = time.Unix(0, last).UTC()
		row.Status = obsdb.Status(status)
		row.Usage = core.Usage{
			InputTokens: inTok.Int64, OutputTokens: outTok.Int64,
			CachedInputTokens: cached.Int64, CacheWriteTokens: cacheW.Int64,
			ReasoningTokens: reason.Int64,
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

func (d *DB) Session(ctx context.Context, id string) (_ obsdb.SessionDetail, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionDetail{}, err
	}
	defer d.closedErr(&err)
	// The session's own grouped row, queried directly: the old read
	// scanned the newest 500 sessions, so an older session 404'd here
	// while the list still showed it (the audit's P2-1). Same shape as
	// Sessions' select, scoped to the one session_id.
	now := time.Now()
	rows, err := d.sessionRows(ctx, now.Add(-obsdb.InterruptedAfter).UnixNano(),
		sessionScope+` AND session_id = ?`, []any{id}, nil, nil, ``)
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	if len(rows) == 0 {
		return obsdb.SessionDetail{}, fmt.Errorf("%w: session %s", obsdb.ErrNotFound, id)
	}
	// Every turn: a cap here kept the oldest 500 and silently dropped
	// the newest — the ones a session view is opened for. A caller that
	// wants pages reads Runs with RunQuery.SessionID.
	runs, err := d.queryRunRows(ctx,
		`session_id = ? AND parent_run_id = '' AND playground = 0 ORDER BY turn, started_ns, run_id`,
		[]any{id}, now)
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	return obsdb.SessionDetail{SessionRow: rows[0], Runs: runs}, nil
}

func (d *DB) ResolvePublicID(ctx context.Context, publicID string) (_ string, err error) {
	if err := d.checkOpen(); err != nil {
		return "", err
	}
	defer d.closedErr(&err)
	var sessionID string
	err = d.reads.QueryRowContext(ctx,
		`SELECT session_id FROM runs WHERE public_id = ? AND session_id <> '' ORDER BY turn DESC, started_ns DESC, run_id DESC LIMIT 1`,
		publicID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: public id %s", obsdb.ErrNotFound, publicID)
	}
	return sessionID, err
}
