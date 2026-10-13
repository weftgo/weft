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
	playground, experiment_id, forked_from, resumed_from, meta,
	started_ns, finished_ns, last_seen_ns, finished_ok, failed, err,
	steps, pending, stop_reason,
	input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens,
	event_count, delta_count, message_count,
	instructions_hash, catalog_hash, request_count, derived, root_ended,
	stopped_by, cancel_cause, events_emitted, messages_emitted, error_type, problems`

// promptPreviewColumn is the run row's prompt_preview (plan A7): the
// prompt migration 0004's run_search holds, cut to
// obsdb.PromptPreviewRunes characters (” for a run with none).
const promptPreviewColumn = `COALESCE((SELECT substr(run_search.prompt, 1, 200) FROM run_search
	WHERE run_search.run_id = runs.run_id), '')`

// resumedByColumn is the run row's resumed_by (contract §6, U13.1):
// the latest run by start time whose resumed_from names this run (a
// tie on started_ns goes to the larger id) — derived at read time, so a
// resumer stored before or after the parked run links it alike. Never
// the largest id alone: a core run id is random hex and a thread turn's
// <session>-t10 sorts before -t9. The partial index runs_resumed_from
// (0018) serves the equality — its condition is stated beside it, as
// sqlite needs — and the handful of resumers it finds are sorted
// (plan_internal_test.go pins the plan).
const resumedByColumn = `COALESCE((SELECT rb.run_id FROM runs AS rb
	WHERE rb.resumed_from = runs.run_id AND rb.resumed_from <> ''
	ORDER BY rb.started_ns DESC, rb.run_id DESC LIMIT 1), '')`

// statusExpr is the four-row table in SQL: it must agree with
// obsdb.DeriveStatus, whose test pins the boundaries — succeeded on a
// run_finish or on the stored invoke_agent span (root_ended, 0009).
// One bind parameter: the interrupted cutoff (now - InterruptedAfter).
const statusExpr = `CASE WHEN failed = 1 THEN 'failed'
	WHEN finished_ok = 1 OR root_ended = 1 THEN 'succeeded'
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
	frag += runOrder(q) + ` LIMIT ?`
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

// runOrder is the Runs page's ORDER BY. The order is always newest
// first, run id breaking ties; only its spelling changes. On the
// all-runs list (ParentRunID "*") narrowed by last_seen_ns — the
// running filter or LastSeenAfter, the live backfill — the matches are
// few and runs_last_seen finds them, but sqlite prefers runs_all's
// order and walks every row until the LIMIT fills. The unary plus
// makes started_ns an expression no index orders, so the plan is
// SEARCH runs USING INDEX runs_last_seen (last_seen_ns>?) and a temp
// B-tree over the matches (TestRunListsUseTheirIndexes pins both).
// Not under a public id or session equality (the panel's public-id live
// backfill, a session's running list): runs_public / runs_session_started
// lead with that equality and narrow better than runs_last_seen, so the
// plain spelling stays and the planner keeps them.
func runOrder(q obsdb.RunQuery) string {
	if q.ParentRunID == "*" && q.PublicID == "" && q.SessionID == "" &&
		(q.Status == obsdb.StatusRunning || !q.LastSeenAfter.IsZero()) {
		return ` ORDER BY +started_ns DESC, run_id DESC`
	}
	return ` ORDER BY started_ns DESC, run_id DESC`
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
		// Each condition below is implied by the CASE (redundant), and
		// is there for the planner: the CASE, with its bind parameter,
		// can be served by no index, while these name the columns of
		// runs_last_seen and runs_failed (0014), and runs_failed_all
		// (0015) for the all-runs failed page.
		switch q.Status {
		case obsdb.StatusRunning:
			conds = append(conds, "last_seen_ns >= ?")
			args = append(args, cutoff)
		case obsdb.StatusFailed:
			conds = append(conds, "failed = 1")
		}
	}
	conds = append(conds, "1=1")
	if q.Agent != "" {
		conds = append(conds, "agent = ?")
		args = append(args, q.Agent)
	}
	if q.SessionID != "" {
		// The literal <> '' (redundant: the value is not empty) lets
		// the planner use the partial index runs_session (0001), as
		// for runs_experiment below.
		conds = append(conds, "session_id = ? AND session_id <> ''")
		args = append(args, q.SessionID)
	}
	if q.ExperimentID != "" {
		// The literal <> '' is redundant (the value is not empty) but
		// lets the planner use the partial index runs_experiment
		// (0006), which only a WHERE implying its own condition may.
		conds = append(conds, "experiment_id = ? AND experiment_id <> ''")
		args = append(args, q.ExperimentID)
	}
	if q.PublicID != "" {
		conds = append(conds, "public_id = ? AND public_id <> ''") // runs_public (rebuilt in 0014)
		args = append(args, q.PublicID)
	}
	if q.ErrorType != "" {
		conds = append(conds, "error_type = ?")
		args = append(args, q.ErrorType)
	}
	if q.StoppedBy != "" {
		cond, cargs := stoppedByCond(q.StoppedBy)
		conds = append(conds, cond)
		args = append(args, cargs...)
	}
	switch q.ParentRunID {
	case "":
		conds = append(conds, "parent_run_id = ''")
	case "*":
	default:
		conds = append(conds, "parent_run_id = ?")
		args = append(args, q.ParentRunID)
	}
	if !q.LastSeenAfter.IsZero() {
		conds = append(conds, "last_seen_ns >= ?")
		args = append(args, q.LastSeenAfter.UnixNano())
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

// stoppedByCond is RunQuery.StoppedBy's condition with its binds: an
// exact match, or — for a filter ending in ':' (has_tool_call:) — a
// prefix, compared with substr so neither LIKE's wildcards nor its
// ASCII case folding apply.
func stoppedByCond(filter string) (string, []any) {
	if strings.HasSuffix(filter, ":") {
		return "substr(stopped_by, 1, length(?)) = ?", []any{filter, filter}
	}
	return "stopped_by = ?", []any{filter}
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
	rs, err := d.reads.QueryContext(ctx, `SELECT `+runColumns+`, `+promptPreviewColumn+`, `+resumedByColumn+` FROM runs WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []runRowScan
	for rs.Next() {
		var r runRowScan
		if err := rs.Scan(&r.ID, &r.ParentRunID, &r.ParentCallID, &r.TraceID, &r.Agent, &r.Provider,
			&r.Model, &r.ManifestHash, &r.WeftVersion, &r.Service, &r.SessionID, &r.PublicID, &r.Turn,
			&r.Playground, &r.ExperimentID, &r.ForkedFrom, &r.ResumedFrom, &r.Meta,
			&r.Started, &r.Finished, &r.LastSeen, &r.FinishedOK, &r.Failed, &r.Err,
			&r.Steps, &r.Pending, &r.StopReason,
			&r.InTok, &r.OutTok, &r.CachedTok, &r.CacheWrite, &r.ReasonTok,
			&r.EventCount, &r.DeltaCount, &r.MessageCount,
			&r.InstructionsHash, &r.CatalogHash, &r.RequestCount, &r.Derived, &r.RootEnded,
			&r.StoppedBy, &r.CancelCause, &r.EventsEmitted, &r.MessagesEmitted,
			&r.ErrorType, &r.Problems, &r.PromptPreview, &r.ResumedBy); err != nil {
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
	ResumedFrom, ResumedBy                            string
	Started, LastSeen                                 int64
	Finished                                          sql.NullInt64
	FinishedOK, Failed                                bool
	Err, StopReason                                   string
	Steps, Pending                                    int
	InTok, OutTok, CachedTok, CacheWrite, ReasonTok   int64
	EventCount, DeltaCount, MessageCount              int64
	InstructionsHash, CatalogHash                     string
	RequestCount                                      int64
	Derived, RootEnded                                bool
	StoppedBy, CancelCause                            string
	EventsEmitted, MessagesEmitted                    int64
	ErrorType, Problems                               string
	PromptPreview                                     string
}

func scanRunRow(r runRowScan, now time.Time) (obsdb.RunRow, error) {
	row := obsdb.RunRow{
		ID: r.ID, ParentRunID: r.ParentRunID, ParentCallID: r.ParentCallID, TraceID: r.TraceID,
		Agent: r.Agent, Provider: r.Provider, Model: r.Model,
		ManifestHash: r.ManifestHash, WeftVersion: r.WeftVersion, Service: r.Service,
		SessionID: r.SessionID, PublicID: r.PublicID, Turn: r.Turn,
		Playground: r.Playground, ExperimentID: r.ExperimentID, ForkedFrom: r.ForkedFrom,
		ResumedFrom: r.ResumedFrom, ResumedBy: r.ResumedBy,
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
		PromptPreview: r.PromptPreview, Derived: r.Derived, RootEnded: r.RootEnded,
		FinishRecorded: r.FinishedOK,
		StoppedBy:      r.StoppedBy, CancelCause: r.CancelCause,
		EventsEmitted: r.EventsEmitted, MessagesEmitted: r.MessagesEmitted,
		ErrorType: r.ErrorType, Problems: problemsOf(r.Problems),
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
	row.Status = obsdb.DeriveStatus(r.Failed, r.FinishedOK || r.RootEnded, row.LastSeen, now)
	// A failed root is terminal: nothing is coming, so it never settles
	// (the event page calls it done at once, the same rule).
	row.Settling = obsdb.DeriveSettling(r.RootEnded && !r.Derived && !r.Failed, r.FinishedOK, row.LastSeen, now)
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
	var finishedOK, failed, rootEnded, derived bool
	var eventCount, lastSeen int64
	err = d.reads.QueryRowContext(ctx,
		`SELECT finished_ok, failed, root_ended, derived, last_seen_ns, event_count FROM runs WHERE run_id = ?`, runID).
		Scan(&finishedOK, &failed, &rootEnded, &derived, &lastSeen, &eventCount)
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
		// terminal — a running run may still emit more, and a root-ended
		// one's trailing records are waited for while it settles
		// (obsdb.EventPage.Done).
		page.Done = finishedOK || failed ||
			(rootEnded && !obsdb.DeriveSettling(rootEnded && !derived && !failed, finishedOK, time.Unix(0, lastSeen), time.Now()))
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
	if trace, _, ok := obsdb.ParseStockRunID(runID); ok {
		// A derived run (docs/record-contract.md §9) names no span by
		// run id: its spans are its root's subtree, read from the
		// trace (obsdb.StockRunSpans).
		spans, err := d.querySpans(ctx, `trace_id = ? ORDER BY start_ns, span_id`, trace)
		if err != nil {
			return nil, err
		}
		return obsdb.StockRunSpans(spans, runID), nil
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

// Traces groups the spans table by trace id (obsdb.DB.Traces): the
// filters in HAVING over the group's aggregates — a trace's Start is
// MIN(start_ns), never one span's — and three keyed reads for the
// page's roots, services and run ids. With no window it is one scan of
// the primary key's order; a Since window narrows the groups first to
// the traces with a span at or after it (spans_start, migration 0019;
// a necessary condition of MIN(start_ns) >= since, so HAVING still
// decides — obsdb/clickhouse's traceInner narrows the same way). The
// scope filter reads weft.public_id off every span's attrs, the
// attribute /api/traces/{id}'s scope check reads.
func (d *DB) Traces(ctx context.Context, q obsdb.TraceQuery) (_ obsdb.TracePage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.TracePage{}, err
	}
	defer d.closedErr(&err)
	group, having, args := traceGroup(q)
	var total int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (`+group+having+`)`, args...).Scan(&total); err != nil {
		return obsdb.TracePage{}, err
	}
	limit := obsdb.LimitOf(q.Limit)
	frag, qargs := having, append([]any{}, args...)
	if !q.Before.IsZero() {
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			frag += " AND (MIN(start_ns) < ? OR (MIN(start_ns) = ? AND trace_id < ?))"
			qargs = append(qargs, ns, ns, q.BeforeID)
		} else {
			frag += " AND MIN(start_ns) < ?"
			qargs = append(qargs, ns)
		}
	}
	rows, err := d.queryTraceRows(ctx, group+frag+` ORDER BY MIN(start_ns) DESC, trace_id DESC LIMIT ?`,
		append(qargs, limit))
	if err != nil {
		return obsdb.TracePage{}, err
	}
	page := obsdb.TracePage{Traces: rows, Total: total}
	if len(rows) == limit {
		// The tied traces ride along (RunPage's rule), up to MaxTies.
		last := rows[len(rows)-1]
		ties, err := d.queryTraceRows(ctx,
			group+having+` AND MIN(start_ns) = ? AND trace_id < ? ORDER BY trace_id DESC LIMIT ?`,
			append(append([]any{}, args...), last.Start.UnixNano(), last.TraceID, obsdb.MaxTies))
		if err != nil {
			return obsdb.TracePage{}, err
		}
		page.Traces = append(page.Traces, ties...)
		end := page.Traces[len(page.Traces)-1]
		page.NextBefore, page.NextBeforeID = &end.Start, end.TraceID
	}
	if err := d.fillTraces(ctx, page.Traces); err != nil {
		return obsdb.TracePage{}, err
	}
	return page, nil
}

// traceGroup builds Traces' grouped select up to its HAVING keyword
// and the HAVING fragment over one trace's group of spans, with their
// bind arguments in text order.
func traceGroup(q obsdb.TraceQuery) (group, having string, args []any) {
	group = `SELECT trace_id, MIN(start_ns), MAX(end_ns), COUNT(*), MAX(status_code = 2)
		FROM spans `
	if !q.Since.IsZero() {
		group += `WHERE trace_id IN (SELECT trace_id FROM spans WHERE start_ns >= ?) `
		args = append(args, q.Since.UnixNano())
	}
	group += `GROUP BY trace_id HAVING `
	conds := []string{"1=1"}
	if q.Service != "" {
		conds = append(conds, "SUM(service = ?) > 0")
		args = append(args, q.Service)
	}
	if q.PublicID != "" {
		conds = append(conds, `SUM(COALESCE(json_extract(attrs, '$."weft.public_id"'), '') <> ?) = 0`)
		args = append(args, q.PublicID)
	}
	if !q.Since.IsZero() {
		conds = append(conds, "MIN(start_ns) >= ?")
		args = append(args, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		conds = append(conds, "MIN(start_ns) < ?")
		args = append(args, q.Until.UnixNano())
	}
	return group, strings.Join(conds, " AND "), args
}

// queryTraceRows reads the grouped columns of Traces' select.
func (d *DB) queryTraceRows(ctx context.Context, query string, args []any) ([]obsdb.TraceRow, error) {
	rs, err := d.reads.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.TraceRow
	for rs.Next() {
		var r obsdb.TraceRow
		var start, end int64
		var failed int
		if err := rs.Scan(&r.TraceID, &start, &end, &r.SpanCount, &failed); err != nil {
			return nil, err
		}
		r.Start, r.End, r.Error = timeOf(start), timeOf(end), failed == 1
		out = append(out, r)
	}
	return out, rs.Err()
}

// fillTraces completes a page's rows by trace id: the root (the
// earliest parentless span), the service (the root's, else the
// earliest span's) and the distinct run ids.
func (d *DB) fillTraces(ctx context.Context, rows []obsdb.TraceRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]any, len(rows))
	byID := make(map[string]*obsdb.TraceRow, len(rows))
	for i := range rows {
		ids[i] = rows[i].TraceID
		byID[rows[i].TraceID] = &rows[i]
	}
	in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	// The first span of each trace, a parentless one before any other:
	// the root when there is one, the earliest span otherwise.
	rs, err := d.reads.QueryContext(ctx, `SELECT trace_id, span_id, name, service, parent_span_id = '' FROM (
		SELECT trace_id, span_id, name, service, parent_span_id, ROW_NUMBER() OVER (
			PARTITION BY trace_id ORDER BY parent_span_id <> '', start_ns, span_id) AS rn
		FROM spans WHERE trace_id IN `+in+`) WHERE rn = 1`, ids...)
	if err != nil {
		return err
	}
	for rs.Next() {
		var id, spanID, name, service string
		var root int
		if err := rs.Scan(&id, &spanID, &name, &service, &root); err != nil {
			_ = rs.Close()
			return err
		}
		if r := byID[id]; r != nil {
			r.Service = service
			if root == 1 {
				r.Root = &obsdb.TraceRoot{SpanID: spanID, Name: name}
			}
		}
	}
	if err := rs.Close(); err != nil {
		return err
	}
	rs, err = d.reads.QueryContext(ctx, `SELECT DISTINCT trace_id, run_id FROM spans
		WHERE run_id <> '' AND trace_id IN `+in+` ORDER BY trace_id, run_id`, ids...)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var id, run string
		if err := rs.Scan(&id, &run); err != nil {
			return err
		}
		if r := byID[id]; r != nil {
			r.RunIDs = append(r.RunIDs, run)
		}
	}
	return rs.Err()
}

func (d *DB) querySpans(ctx context.Context, where string, arg any) ([]obsdb.Span, error) {
	return querySpansOn(ctx, d.reads, where, arg)
}

// spanQueryer is what querySpansOn reads through: the read pool, or the
// writer's transaction (Write re-derives a trace inside it).
type spanQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func querySpansOn(ctx context.Context, q spanQueryer, where string, arg any) ([]obsdb.Span, error) {
	rs, err := q.QueryContext(ctx, `SELECT trace_id, span_id, parent_span_id, name, kind,
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
// turn's derived status. A PublicID scopes the runs before the GROUP BY
// (RunQuery.PublicID's WHERE): every aggregate, the order, Total and the
// cursor are that public id's runs alone.
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
	if q.Q != "" {
		title, targs := sessionTitleExpr(q.PublicID)
		prompt, pargs := sessionPromptExpr(q.PublicID)
		for _, t := range strings.Fields(q.Q) {
			having = append(having, `(instr(lower(`+title+`), lower(?)) > 0
				OR instr(lower(`+prompt+`), lower(?)) > 0
				OR instr(lower(session_id), lower(?)) > 0
				OR instr(lower(MAX(public_id)), lower(?)) > 0
				OR instr(lower(MAX(agent)), lower(?)) > 0)`)
			hargs = append(append(append(append(hargs, targs...), t), pargs...), t, t, t, t)
		}
	}
	scope, sargs := sessionScope, []any(nil)
	if q.PublicID != "" {
		scope += ` AND public_id = ? AND public_id <> ''`
		sargs = []any{q.PublicID}
	}
	// Total is the whole match, as Runs' is: the cursor pages through
	// it and never narrows it.
	var total int
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT session_id FROM runs WHERE `+scope+
			` GROUP BY session_id`+havingSQL(having)+`)`, append(append([]any{}, sargs...), hargs...)...).Scan(&total); err != nil {
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
	rows, err := d.sessionRows(ctx, cutoff, q.PublicID, scope, sargs, paged,
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
		ties, err := d.sessionRows(ctx, cutoff, q.PublicID, scope, sargs,
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

// sessionTitleExpr is a grouped session's title (SessionRow.Title):
// the newest turn's (by turn, then start) non-empty
// weft.session.title, from the run's meta. pub, when set, scopes the
// turns read as the grouping's WHERE does; the args are its parameters.
func sessionTitleExpr(pub string) (string, []any) {
	scope, args := "", []any(nil)
	if pub != "" {
		scope, args = ` AND r3.public_id = ? AND r3.public_id <> ''`, []any{pub}
	}
	const title = `json_extract(r3.meta, '$."` + obsdb.SessionTitleKey + `"')`
	return `COALESCE((SELECT ` + title + ` FROM runs r3
		WHERE r3.session_id = runs.session_id AND r3.session_id <> '' AND r3.parent_run_id = '' AND r3.playground = 0` + scope + `
		AND COALESCE(` + title + `, '') <> ''
		ORDER BY r3.turn DESC, r3.started_ns DESC LIMIT 1), '')`, args
}

// sessionPromptExpr is a grouped session's first turn's prompt, whole
// (SessionRow.FirstPrompt is its first PromptPreviewRunes): the
// lowest turn's (then the earliest), "" when that turn has none —
// never a later turn's. pub scopes as in sessionTitleExpr.
func sessionPromptExpr(pub string) (string, []any) {
	scope, args := "", []any(nil)
	if pub != "" {
		scope, args = ` AND r4.public_id = ? AND r4.public_id <> ''`, []any{pub}
	}
	return `COALESCE((SELECT rs.prompt FROM run_search rs WHERE rs.run_id = (SELECT r4.run_id FROM runs r4
		WHERE r4.session_id = runs.session_id AND r4.session_id <> '' AND r4.parent_run_id = '' AND r4.playground = 0` + scope + `
		ORDER BY r4.turn, r4.started_ns, r4.run_id LIMIT 1)), '')`, args
}

// sessionFirstTurnExpr is a grouped session's first turn (the one
// sessionPromptExpr reads) as a JSON object of what
// SessionRow.ContentOff reads: its counts, Derived and the status
// inputs firstTurn derives Status and Settling from, as the run
// scanner does; ” when the session has none. pub scopes as in
// sessionTitleExpr.
func sessionFirstTurnExpr(pub string) (string, []any) {
	scope, args := "", []any(nil)
	if pub != "" {
		scope, args = ` AND r5.public_id = ? AND r5.public_id <> ''`, []any{pub}
	}
	return `COALESCE((SELECT json_object('derived', r5.derived, 'messages', r5.message_count, 'events', r5.event_count,
			'failed', r5.failed, 'finished_ok', r5.finished_ok, 'root_ended', r5.root_ended, 'last_seen', r5.last_seen_ns)
		FROM runs r5
		WHERE r5.session_id = runs.session_id AND r5.session_id <> '' AND r5.parent_run_id = '' AND r5.playground = 0` + scope + `
		ORDER BY r5.turn, r5.started_ns, r5.run_id LIMIT 1), '')`, args
}

// firstTurn is sessionFirstTurnExpr's object (sqlite's booleans are
// integers).
type firstTurn struct {
	Derived    int   `json:"derived"`
	Messages   int64 `json:"messages"`
	Events     int64 `json:"events"`
	Failed     int   `json:"failed"`
	FinishedOK int   `json:"finished_ok"`
	RootEnded  int   `json:"root_ended"`
	LastSeen   int64 `json:"last_seen"`
}

// contentOff decodes a firstTurn and applies obsdb.SessionContentOff
// to it, its Status and Settling derived as the run scanner derives
// them (DeriveStatus, DeriveSettling); false for none.
func (f *firstTurn) contentOff(stored string, now time.Time) (bool, error) {
	if stored == "" {
		return false, nil
	}
	if err := json.Unmarshal([]byte(stored), f); err != nil {
		return false, fmt.Errorf("sqlite: session first turn: %w", err)
	}
	derived, failed, finishedOK, rootEnded := f.Derived != 0, f.Failed != 0, f.FinishedOK != 0, f.RootEnded != 0
	seen := time.Unix(0, f.LastSeen)
	return obsdb.SessionContentOff(obsdb.RunRow{
		Derived: derived, MessageCount: f.Messages, EventCount: f.Events,
		Status:   obsdb.DeriveStatus(failed, finishedOK || rootEnded, seen, now),
		Settling: obsdb.DeriveSettling(rootEnded && !derived && !failed, finishedOK, seen, now),
	}), nil
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
// parameter, the first in text order; pub, when set, scopes the newest
// turn the status is read from as where scopes the rest.
func (d *DB) sessionRows(ctx context.Context, cutoff int64, pub, where string, wargs []any,
	having []string, hargs []any, tail string) ([]obsdb.SessionRow, error) {
	query, args := sessionRowsSQL(cutoff, pub, where, wargs, having, hargs, tail)
	rs, err := d.reads.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	now := time.Unix(0, cutoff).Add(obsdb.InterruptedAfter) // statusExpr's clock
	var out []obsdb.SessionRow
	for rs.Next() {
		var row obsdb.SessionRow
		var first, last int64
		var inTok, outTok, cached, cacheW, reason sql.NullInt64
		var status, firstStored string
		if err := rs.Scan(&row.ID, &row.PublicID, &row.Agent, &row.Turns,
			&first, &last, &inTok, &outTok, &cached, &cacheW, &reason, &status,
			&row.Title, &row.FirstPrompt, &firstStored); err != nil {
			return nil, err
		}
		var ft firstTurn
		if row.ContentOff, err = ft.contentOff(firstStored, now); err != nil {
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

// sessionRowsSQL is sessionRows' statement with its arguments in text
// order (a function of its own so a test can EXPLAIN it). Every
// correlated subquery states r.session_id <> ” beside its equality,
// which lets sqlite use the partial index runs_session (0001): without
// it each session's newest turn was looked up by walking every
// top-level run.
func sessionRowsSQL(cutoff int64, pub, where string, wargs []any,
	having []string, hargs []any, tail string) (string, []any) {
	args := []any{cutoff}
	statusScope := ""
	if pub != "" {
		statusScope = ` AND r2.public_id = ? AND r2.public_id <> ''`
		args = append(args, pub)
	}
	title, targs := sessionTitleExpr(pub)
	prompt, pargs := sessionPromptExpr(pub)
	off, oargs := sessionFirstTurnExpr(pub)
	args = append(append(append(args, targs...), pargs...), oargs...)
	args = append(append(args, wargs...), hargs...)
	return `SELECT session_id, MAX(public_id), MAX(agent), COUNT(*),
		MIN(started_ns), MAX(last_seen_ns),
		SUM(input_tokens), SUM(output_tokens), SUM(cached_input_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens),
		(SELECT ` + statusExpr + ` FROM runs r2 WHERE r2.session_id = runs.session_id AND r2.session_id <> ''
		 AND r2.parent_run_id = '' AND r2.playground = 0` + statusScope + `
		 ORDER BY r2.turn DESC, r2.started_ns DESC LIMIT 1),
		` + title + `, substr(` + prompt + `, 1, ` + fmt.Sprint(obsdb.PromptPreviewRunes) + `), ` + off + `
		FROM runs WHERE ` + where + ` GROUP BY session_id` + havingSQL(having) + tail, args
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
	rows, err := d.sessionRows(ctx, now.Add(-obsdb.InterruptedAfter).UnixNano(), "",
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
		`session_id = ? AND session_id <> '' AND parent_run_id = '' AND playground = 0 ORDER BY turn, started_ns, run_id`,
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
	if publicID == "" { // "" is no public id: the runs without one are nobody's
		return "", fmt.Errorf("%w: empty public id", obsdb.ErrNotFound)
	}
	defer d.closedErr(&err)
	var sessionID string
	err = d.reads.QueryRowContext(ctx,
		`SELECT session_id FROM runs WHERE public_id = ? AND public_id <> '' AND session_id <> '' ORDER BY turn DESC, started_ns DESC, run_id DESC LIMIT 1`,
		publicID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: public id %s", obsdb.ErrNotFound, publicID)
	}
	return sessionID, err
}

// problemsOf folds runs.problems — a JSON array of obsdb.SpanProblems
// tokens — into the row's counters; a malformed value reads none.
func problemsOf(stored string) obsdb.Problems {
	var tokens []string
	if stored != "" {
		_ = json.Unmarshal([]byte(stored), &tokens)
	}
	return obsdb.CountProblems(tokens)
}
