package clickhouse

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// The read side. Status is derived on read through obsdb.DeriveStatus —
// never stored — and every SQL condition that filters by status says
// the same thing the derivation does (statusCase). Lists read through
// GROUP BY RunId over weft_runs' aggregates (S3.6); single-run pages
// read FINAL, which is exact for ReplacingMergeTree duplicates and for
// unmerged AggregatingMergeTree parts alike.

// runColumns is weft_runs' column list as FINAL reads it.
const runColumns = `RunId, ParentRunID, ParentCallID, TraceID, Agent, Provider, Model,
	ManifestHash, WeftVersion, Service, SessionID, PublicID, Turn, Playground,
	ExperimentID, ForkedFrom, Meta, Started, Finished, LastSeen, FinishedOK,
	Failed, Err, Steps, Pending, StopReason, InputTokens, OutputTokens,
	CachedInputTokens, CacheWriteTokens, ReasoningTokens, DeltaCount,
	InstructionsHash, CatalogHash, RequestCount`

// runAggregates is the same list as a GROUP BY RunId select: max for
// identity strings, flags, usage and last-seen; min for started — the
// same aggregates the AggregatingMergeTree columns carry.
const runAggregates = `RunId, max(ParentRunID) AS ParentRunID, max(ParentCallID) AS ParentCallID,
	max(TraceID) AS TraceID, max(Agent) AS Agent, max(Provider) AS Provider, max(Model) AS Model,
	max(ManifestHash) AS ManifestHash, max(WeftVersion) AS WeftVersion, max(Service) AS Service,
	max(SessionID) AS SessionID, max(PublicID) AS PublicID, max(Turn) AS Turn,
	max(Playground) AS Playground, max(ExperimentID) AS ExperimentID, max(ForkedFrom) AS ForkedFrom,
	max(Meta) AS Meta, min(Started) AS Started, max(Finished) AS Finished, max(LastSeen) AS LastSeen,
	max(FinishedOK) AS FinishedOK, max(Failed) AS Failed, max(Err) AS Err,
	max(Steps) AS Steps, max(Pending) AS Pending, max(StopReason) AS StopReason,
	max(InputTokens) AS InputTokens, max(OutputTokens) AS OutputTokens,
	max(CachedInputTokens) AS CachedInputTokens, max(CacheWriteTokens) AS CacheWriteTokens,
	max(ReasoningTokens) AS ReasoningTokens, max(DeltaCount) AS DeltaCount,
	max(InstructionsHash) AS InstructionsHash, max(CatalogHash) AS CatalogHash,
	max(RequestCount) AS RequestCount`

// statusCase is the four-row table in SQL over weft_runs' columns: it
// must agree with obsdb.DeriveStatus, whose test pins the boundaries.
// One bind parameter precedes it: the interrupted cutoff
// (now - InterruptedAfter) as integer nanoseconds — a positional
// time.Time bind renders at Seconds scale (clickhouse-go's bind.go),
// which would widen the running band by up to a second.
const statusCase = `CASE WHEN Failed = 1 THEN 'failed'
	WHEN FinishedOK = 1 THEN 'succeeded'
	WHEN toUnixTimestamp64Nano(LastSeen) >= ? THEN 'running'
	ELSE 'interrupted' END`

func (d *DB) Runs(ctx context.Context, q obsdb.RunQuery) (_ obsdb.RunPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunPage{}, err
	}
	defer d.closedErr(&err)
	now := time.Now()
	inner, args := runsInner(q)
	var conds []string
	var outerArgs []any
	if q.Status != "" {
		outerArgs = append(outerArgs, now.Add(-obsdb.InterruptedAfter).UnixNano(), string(q.Status))
		conds = append(conds, "("+statusCase+") = ?")
	}
	total, err := d.countRows(ctx,
		"SELECT count() FROM ("+inner+") WHERE "+strings.Join(or1(conds), " AND "), args, outerArgs)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	pageArgs := append(append([]any{}, args...), outerArgs...)
	frag := "SELECT * FROM (" + inner + ") WHERE " + strings.Join(or1(conds), " AND ")
	if !q.Before.IsZero() {
		// Integer nanoseconds, not a time.Time bind: the driver renders
		// positional time.Time at Seconds scale, so `Started < ?`
		// compared against S.000000000 and every row in
		// [S.000, S.<nanos>) sorted right after the boundary row — no
		// later page ever returned it (rows silently skipped at
		// essentially every page boundary).
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			frag += " AND (toUnixTimestamp64Nano(Started) < ? OR (toUnixTimestamp64Nano(Started) = ? AND RunId < ?))"
			pageArgs = append(pageArgs, ns, ns, q.BeforeID)
		} else {
			frag += " AND toUnixTimestamp64Nano(Started) < ?"
			pageArgs = append(pageArgs, ns)
		}
	}
	// WITH TIES: a page does not end inside a group of runs sharing one
	// Started — the tied rows ride along, or a next page read on Before
	// alone (`Started < cursor`) would skip them for good. The outer
	// sort gives the page its total order and caps the ride-along at
	// MaxTies, so one page stays bounded however large the tie; past
	// the cap the (Before, BeforeID) cursor resumes inside the group.
	limit := obsdb.LimitOf(q.Limit)
	frag = "SELECT * FROM (" + frag + " ORDER BY Started DESC LIMIT ? WITH TIES) ORDER BY Started DESC, RunId DESC LIMIT ?"
	pageArgs = append(pageArgs, limit, limit+obsdb.MaxTies)
	rows, err := d.queryRunRows(ctx, now, frag, pageArgs...)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	if err := d.fillCounts(ctx, rowPtrs(rows)); err != nil {
		return obsdb.RunPage{}, err
	}
	page := obsdb.RunPage{Runs: rows, Total: total}
	if len(rows) >= limit {
		end := rows[len(rows)-1]
		page.NextBefore, page.NextBeforeID = &end.Started, end.ID
	}
	return page, nil
}

// runsInner builds the filtered run list — one row per run, the whole
// run's aggregates — with its bind arguments. The filters apply to the
// grouped rows, never to weft_runs' stored ones: until a background
// merge collapses them a run is several rows (one per insert), each
// holding only what its own records carried, so a row-level filter both
// leaks and truncates — a subagent's later records carry no parent id
// and passed the top-level filter, while a parent filter kept
// only the run_start row and aggregated a run that never finished.
//
// An equality on an identity column additionally narrows the scan to
// the runs that have a row with that value (a superset of the answer:
// max returns a value some row holds), so a session's or an
// experiment's list reads those runs by primary key instead of
// aggregating the table. The narrowing subquery names its columns
// through a table alias: ClickHouse aliases are query-global, and a
// bare `Agent` beside `max(Agent) AS Agent` would resolve to the
// aggregate.
func runsInner(q obsdb.RunQuery) (string, []any) {
	var narrow, conds []string
	var narrowArgs, args []any
	eq := func(col, v string) {
		narrow = append(narrow, "RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r."+col+" = ?)")
		narrowArgs = append(narrowArgs, v)
		conds = append(conds, col+" = ?")
		args = append(args, v)
	}
	if q.Agent != "" {
		eq("Agent", q.Agent)
	}
	if q.SessionID != "" {
		eq("SessionID", q.SessionID)
	}
	if q.PublicID != "" {
		eq("PublicID", q.PublicID)
	}
	switch q.ParentRunID {
	case "":
		conds = append(conds, "ParentRunID = ''")
	case "*":
	default:
		eq("ParentRunID", q.ParentRunID)
	}
	if q.Playground != nil {
		conds = append(conds, "Playground = ?")
		args = append(args, *q.Playground)
	}
	if q.ExperimentID != "" {
		eq("ExperimentID", q.ExperimentID)
	}
	for k, v := range q.Meta {
		// Meta holds the caller metadata (contract keys filtered at the
		// view); a subset match asks for the key's exact string.
		conds = append(conds, "JSONHas(Meta, ?) AND JSONExtractString(Meta, ?) = ?")
		args = append(args, k, k, v)
	}
	return "SELECT * FROM (SELECT " + runAggregates + " FROM weft_runs WHERE " +
		strings.Join(or1(narrow), " AND ") + " GROUP BY RunId) WHERE " +
		strings.Join(or1(conds), " AND "), append(narrowArgs, args...)
}

// or1 returns conds or ["1=1"], so WHERE clauses never dangle.
func or1(conds []string) []string {
	if len(conds) == 0 {
		return []string{"1=1"}
	}
	return conds
}

// countRows counts the grouped rows with the status filters applied; args
// are the inner binds, outerArgs the status binds.
func (d *DB) countRows(ctx context.Context, sql string, args, outerArgs []any) (int, error) {
	var n uint64
	all := append(append([]any{}, args...), outerArgs...)
	if err := d.conn.QueryRow(ctx, sql, all...).Scan(&n); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (d *DB) Run(ctx context.Context, id string) (_ obsdb.RunDetail, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunDetail{}, err
	}
	defer d.closedErr(&err)
	now := time.Now()
	rows, err := d.queryRunRows(ctx, now,
		"SELECT "+runColumns+" FROM weft_runs FINAL WHERE RunId = ?", id)
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	if len(rows) == 0 {
		return obsdb.RunDetail{}, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, id)
	}
	// The RunId IN (...) narrows FINAL to the candidate runs by primary
	// key (a superset: some row of each child names the parent); the
	// equality then reads their merged rows. Without it FINAL merges
	// the whole table for every run page.
	children, err := d.queryRunRows(ctx, now,
		"SELECT "+runColumns+" FROM weft_runs FINAL WHERE "+
			"RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.ParentRunID = ?) AND ParentRunID = ? "+
			"ORDER BY Started, RunId", id, id)
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	ptrs := rowPtrs(children)
	ptrs = append(ptrs, &rows[0])
	if err := d.fillCounts(ctx, ptrs); err != nil {
		return obsdb.RunDetail{}, err
	}
	return obsdb.RunDetail{RunRow: rows[0], Children: children}, nil
}

// queryRunRows selects run rows (FINAL or already-aggregated SQL) and
// derives status with one clock for the whole result — the caller's
// now, the same instant its status filter's cutoff was cut from, so a
// row the filter passed as running cannot derive interrupted a moment
// later.
func (d *DB) queryRunRows(ctx context.Context, now time.Time, sql string, args ...any) ([]obsdb.RunRow, error) {
	rs, err := d.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.RunRow
	for rs.Next() {
		var s runScan
		if err := rs.Scan(&s.ID, &s.ParentRunID, &s.ParentCallID, &s.TraceID, &s.Agent,
			&s.Provider, &s.Model, &s.ManifestHash, &s.WeftVersion, &s.Service,
			&s.SessionID, &s.PublicID, &s.Turn, &s.Playground, &s.ExperimentID, &s.ForkedFrom,
			&s.Meta, &s.Started, &s.Finished, &s.LastSeen, &s.FinishedOK, &s.Failed, &s.Err,
			&s.Steps, &s.Pending, &s.StopReason,
			&s.InTok, &s.OutTok, &s.CachedTok, &s.CacheWriteTok, &s.ReasonTok, &s.DeltaCount,
			&s.InstructionsHash, &s.CatalogHash, &s.RequestCount); err != nil {
			return nil, err
		}
		row, err := s.row(now)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

type runScan struct {
	ID, ParentRunID, ParentCallID, TraceID             string
	Agent, Provider, Model                             string
	ManifestHash, WeftVersion, Service                 string
	SessionID, PublicID                                string
	Turn                                               int32
	Playground, FinishedOK, Failed                     bool
	ExperimentID, ForkedFrom, Meta                     string
	Started, LastSeen                                  time.Time
	Finished                                           *time.Time
	Err, StopReason                                    string
	Steps, Pending                                     int32
	InTok, OutTok, CachedTok, CacheWriteTok, ReasonTok int64
	DeltaCount                                         int64
	InstructionsHash, CatalogHash                      string
	RequestCount                                       int64
}

func (s *runScan) row(now time.Time) (obsdb.RunRow, error) {
	row := obsdb.RunRow{
		ID: s.ID, ParentRunID: s.ParentRunID, ParentCallID: s.ParentCallID, TraceID: s.TraceID,
		Agent: s.Agent, Provider: s.Provider, Model: s.Model,
		ManifestHash: s.ManifestHash, WeftVersion: s.WeftVersion, Service: s.Service,
		SessionID: s.SessionID, PublicID: s.PublicID, Turn: int(s.Turn),
		Playground: s.Playground, ExperimentID: s.ExperimentID, ForkedFrom: s.ForkedFrom,
		Started: s.Started.UTC(), LastSeen: s.LastSeen.UTC(),
		Err: s.Err, Steps: int(s.Steps), Pending: int(s.Pending), StopReason: s.StopReason,
		Usage: core.Usage{
			InputTokens: s.InTok, OutputTokens: s.OutTok,
			CachedInputTokens: s.CachedTok, CacheWriteTokens: s.CacheWriteTok,
			ReasoningTokens: s.ReasonTok,
		},
		DeltaCount:       s.DeltaCount,
		InstructionsHash: s.InstructionsHash, CatalogHash: s.CatalogHash, RequestCount: s.RequestCount,
	}
	if s.Finished != nil {
		f := s.Finished.UTC()
		row.Finished = &f
	}
	if s.Meta != "" {
		var attrs map[string]any
		if err := unmarshalAttrs([]byte(s.Meta), &attrs); err != nil {
			return obsdb.RunRow{}, fmt.Errorf("clickhouse: run %s: meta column: %w", s.ID, err)
		}
		row.Meta = obsdb.MetaOf(attrs)
	}
	row.Status = obsdb.DeriveStatus(s.Failed, s.FinishedOK, row.LastSeen, now)
	return row, nil
}

// rowPtrs turns a slice into pointers to its elements, so fillCounts
// writes through to the caller's rows.
func rowPtrs(rows []obsdb.RunRow) []*obsdb.RunRow {
	ptrs := make([]*obsdb.RunRow, len(rows))
	for i := range rows {
		ptrs[i] = &rows[i]
	}
	return ptrs
}

// fillCounts loads EventCount/MessageCount for the rows' runs from
// weft_records: uniqExact over the (RunId, Kind, Pos) key is exact even
// before ReplacingMergeTree merges a retried batch's duplicates, so a
// transport retry can never inflate a count (the reason counts are
// derived here rather than summed in weft_runs).
func (d *DB) fillCounts(ctx context.Context, rows []*obsdb.RunRow) error {
	byID := make(map[string]*obsdb.RunRow, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if _, seen := byID[row.ID]; !seen {
			byID[row.ID] = row
			ids = append(ids, row.ID)
		}
	}
	// The ids bind as literals into the query text, which the server
	// caps (max_query_size, 256 KiB by default): an uncapped Session's
	// thousands of runs read in chunks, each well under the cap.
	for len(ids) > 0 {
		n, size := 0, 0
		for n < len(ids) && n < 1000 && (n == 0 || size+len(ids[n]) < 64<<10) {
			size += len(ids[n])
			n++
		}
		if err := d.fillCountsOf(ctx, ids[:n], byID); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

func (d *DB) fillCountsOf(ctx context.Context, ids []string, byID map[string]*obsdb.RunRow) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := d.conn.Query(ctx,
		"SELECT RunId, uniqExactIf(Pos, Kind = 'event'), uniqExactIf(Pos, Kind = 'messages') FROM weft_records WHERE RunId IN ("+placeholders+") GROUP BY RunId",
		args...)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var id string
		var events, messages uint64
		if err := rs.Scan(&id, &events, &messages); err != nil {
			return err
		}
		if row, ok := byID[id]; ok {
			row.EventCount, row.MessageCount = int64(events), int64(messages)
		}
	}
	return rs.Err()
}

func (d *DB) Events(ctx context.Context, runID string, after int64, limit int) (_ obsdb.EventPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.EventPage{}, err
	}
	defer d.closedErr(&err)
	// The terminal flags are read before the events: a run that
	// finishes between the two reads then answers Done false with its
	// tail still to come, never Done true short of its tail. Read in
	// this order, Done only ever says "the run was already terminal
	// when these events were read". The same row count is the
	// existence check.
	var finishedOK, failed bool
	var stored uint64
	if err := d.conn.QueryRow(ctx,
		`SELECT max(FinishedOK), max(Failed), count() FROM weft_runs WHERE RunId = ?`, runID).
		Scan(&finishedOK, &failed, &stored); err != nil {
		return obsdb.EventPage{}, err
	}
	if stored == 0 {
		return obsdb.EventPage{}, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, runID)
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	// One row past the limit answers "is there more" in the same read.
	rs, err := d.conn.Query(ctx,
		`SELECT Pos, Time, Body FROM weft_records FINAL
		WHERE RunId = ? AND Kind = 'event' AND Pos > ? ORDER BY Pos LIMIT ?`,
		runID, after, limit+1)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	var page obsdb.EventPage
	for rs.Next() {
		var ev obsdb.PosEvent
		if err := rs.Scan(&ev.Pos, &ev.Time, &ev.Event); err != nil {
			return obsdb.EventPage{}, err
		}
		ev.Time = timeOf(ev.Time)
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
	gaps, err := d.eventGaps(ctx, runID)
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
// The common, gapless run is answered from one aggregate: positions
// are unique per run, so n distinct positions inside 0..max with
// n = max+1 leave no room for a hole, and no position is read back.
func (d *DB) eventGaps(ctx context.Context, runID string) ([]int64, error) {
	var n uint64
	var hi int64
	if err := d.conn.QueryRow(ctx,
		`SELECT uniqExact(Pos), max(Pos) FROM weft_records WHERE RunId = ? AND Kind = 'event' AND Pos >= 0`,
		runID).Scan(&n, &hi); err != nil {
		return nil, err
	}
	if n == 0 || int64(n) == hi+1 {
		return nil, nil
	}
	rs, err := d.conn.Query(ctx,
		`SELECT DISTINCT Pos FROM weft_records WHERE RunId = ? AND Kind = 'event' AND Pos >= 0 ORDER BY Pos`, runID)
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
	// Step is weft_records.Step and Input weft_records.Input (0004;
	// -1 on rows written before it). A row without a stored input flag
	// has its index 0 inferred and marked so (obsdb.TranscriptBatch).
	rs, err := d.conn.Query(ctx,
		`SELECT Pos, Step, Input, Body FROM weft_records FINAL
		WHERE RunId = ? AND Kind = 'messages' ORDER BY Pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.TranscriptBatch
	for rs.Next() {
		var (
			pos   int64
			step  int32
			input int8
			body  string
		)
		if err := rs.Scan(&pos, &step, &input, &body); err != nil {
			return nil, err
		}
		b := obsdb.TranscriptBatch{Index: pos, Step: int(step), Input: input == 1, Messages: json.RawMessage(body)}
		if input < 0 && pos == 0 {
			// Written before 0004 stored the flag: inferred, and said so.
			b.Input, b.InputDerived = !loneAssistant(b.Messages), true
		}
		out = append(out, b)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	// The later record of a rebuilt tool message is the authoritative
	// one (the rule every backend reads through).
	return obsdb.DedupBatches(out), nil
}

// loneAssistant reports whether a messages body is exactly one
// assistant message: the core's step-0 batch of a run fed no messages,
// never an input record it writes for a run fed some.
func loneAssistant(body json.RawMessage) bool {
	var msgs []struct {
		Role string `json:"role"`
	}
	return json.Unmarshal(body, &msgs) == nil && len(msgs) == 1 && msgs[0].Role == "assistant"
}

const spanColumns = `Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName,
	ResourceAttributes, SpanAttributes, Duration, StatusCode, StatusMessage,
	Events.Timestamp, Events.Name, Events.Attributes,
	WeftAttrs, WeftEvents, WeftResource`

func (d *DB) RunSpans(ctx context.Context, runID string) (_ []obsdb.Span, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	return d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE RunId = ?"+
		spanDedup, runID)
}

// Trace returns any trace's spans — weft and non-weft alike (the
// polyglot promise). An unknown trace id returns no rows and no error,
// like the SQLite backend.
func (d *DB) Trace(ctx context.Context, traceID string) (_ []obsdb.Span, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	return d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE TraceId = ?"+
		spanDedup, traceID)
}

// spanDedup makes the span reads idempotent on (trace, span) — the
// promise doc.go makes and obsdb/sqlite keeps with INSERT OR IGNORE.
// otel_traces is a plain MergeTree (a replacing key would collapse
// non-weft spans), so a retried batch's duplicates are removed at
// read: span rows are immutable by construction, so LIMIT 1 BY on the
// identity pair is exact and never hides a real second version.
const spanDedup = " ORDER BY Timestamp, SpanId LIMIT 1 BY TraceId, SpanId"

func (d *DB) querySpans(ctx context.Context, sql string, arg any) ([]obsdb.Span, error) {
	rs, err := d.conn.Query(ctx, sql, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.Span
	for rs.Next() {
		var s obsdb.Span
		var res, attrs map[string]string
		var dur uint64
		var kind, code string
		var evT []time.Time
		var evN []string
		var evA []map[string]string
		var weftAttrs, weftEvents, weftRes string
		if err := rs.Scan(&s.Start, &s.TraceID, &s.SpanID, &s.ParentSpanID, &s.Name,
			&kind, &s.Service, &res, &attrs, &dur, &code, &s.StatusMessage,
			&evT, &evN, &evA, &weftAttrs, &weftEvents, &weftRes); err != nil {
			return nil, err
		}
		s.Kind = spanKindInt(kind)
		s.StatusCode = statusCodeInt(code)
		s.Start, s.End = timeOf(s.Start), timeOf(s.Start.Add(time.Duration(dur)))
		s.Attrs = attrsFromJSONOrMap(weftAttrs, attrs)
		s.Resource = attrsFromJSONOrMap(weftRes, res)
		s.Events = eventsFromJSONOrNested(weftEvents, evT, evN, evA)
		out = append(out, s)
	}
	return out, rs.Err()
}

// attrsFromJSONOrMap restores attributes: the weft JSON column when this
// package wrote the row (typed values survive), the collector's map
// columns otherwise (string values — what a stock collector stored).
func attrsFromJSONOrMap(js string, m map[string]string) map[string]any {
	if js != "" {
		var attrs map[string]any
		if err := unmarshalAttrs([]byte(js), &attrs); err == nil {
			return attrs
		}
	}
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// eventsFromJSONOrNested restores span events: the weft JSON column
// when this package wrote the row — decoded through unmarshalAttrs, so
// an int64 event attribute survives as int64 exactly as on the
// top-level Attrs (S3.1/S3.3; plain json.Unmarshal would produce
// float64, the bug T17's review proved) — and the collector's nested
// arrays otherwise (string values, what a stock collector stored).
func eventsFromJSONOrNested(js string, times []time.Time, names []string, attrs []map[string]string) []obsdb.SpanEvent {
	if js != "" {
		// obsdb.SpanEvent's fields carry the wire keys Time/Name/Attrs.
		var events []obsdb.SpanEvent
		if err := unmarshalAttrs([]byte(js), &events); err == nil && len(events) > 0 {
			for i := range events {
				events[i].Time = events[i].Time.UTC()
			}
			return events
		}
	}
	if len(times) == 0 {
		return nil
	}
	out := make([]obsdb.SpanEvent, len(times))
	for i := range times {
		var a map[string]any
		if i < len(attrs) && len(attrs[i]) > 0 {
			a = make(map[string]any, len(attrs[i]))
			for k, v := range attrs[i] {
				a[k] = v
			}
		}
		out[i] = obsdb.SpanEvent{Time: timeOf(times[i]), Name: names[i], Attrs: a}
	}
	return out
}

// timeOf reads a span or record time column: the Unix epoch is what a
// zero time.Time — OTLP's "unknown" — was stored as (DateTime64 cannot
// hold year 1), and reads back as the zero time, as the SQLite backend
// returns it.
func timeOf(t time.Time) time.Time {
	if t.UnixNano() == 0 {
		return time.Time{}
	}
	return t.UTC()
}

func (d *DB) runExists(ctx context.Context, runID string) error {
	var n uint64
	err := d.conn.QueryRow(ctx,
		`SELECT count() FROM weft_runs FINAL WHERE RunId = ?`, runID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: run %s", obsdb.ErrNotFound, runID)
	}
	return nil
}

// Sessions is a GROUP BY SessionId over weft_runs' top-level,
// non-playground runs (S3.6): turns, first/last seen, summed usage, and
// the newest turn's derived status — argMax on (Turn, Started) picks
// the same row SQLite's ORDER BY ... LIMIT 1 does.
//
// The runs collapse per RunId first (the same aggregates the Runs list
// uses) because unmerged AggregatingMergeTree parts would otherwise
// double-count usage in the outer sum; the session level's aliases are
// prefixed s* so no ClickHouse alias substitution can shadow the argMax
// arguments (aliases are query-global).
func (d *DB) Sessions(ctx context.Context, q obsdb.SessionQuery) (_ obsdb.SessionPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionPage{}, err
	}
	defer d.closedErr(&err)
	runs := sessionRuns("", "")
	var having []string
	var hargs []any
	if q.Agent != "" {
		having = append(having, "max(Agent) = ?")
		hargs = append(hargs, q.Agent)
	}
	if q.PublicID != "" {
		having = append(having, "max(PublicID) = ?")
		hargs = append(hargs, q.PublicID)
	}
	havingSQL := func(conds []string) string {
		if len(conds) == 0 {
			return ""
		}
		return " HAVING " + strings.Join(conds, " AND ")
	}
	// Total is the whole match: the cursor pages it, never narrows it.
	var total uint64
	if err := d.conn.QueryRow(ctx,
		"SELECT count() FROM (SELECT SessionID FROM "+runs+" GROUP BY SessionID"+havingSQL(having)+")", hargs...).Scan(&total); err != nil {
		return obsdb.SessionPage{}, err
	}
	pageArgs := append([]any{}, hargs...)
	if !q.Before.IsZero() {
		// Integer nanoseconds (the Runs cursor's rule): a time.Time
		// bind floors to whole seconds and drops the sub-second band.
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			having = append(having, "(toUnixTimestamp64Nano(max(LastSeen)) < ? OR (toUnixTimestamp64Nano(max(LastSeen)) = ? AND SessionID < ?))")
			pageArgs = append(pageArgs, ns, ns, q.BeforeID)
		} else {
			having = append(having, "toUnixTimestamp64Nano(max(LastSeen)) < ?")
			pageArgs = append(pageArgs, ns)
		}
	}
	limit := obsdb.LimitOf(q.Limit)
	// WITH TIES, then the total order capped at MaxTies past the limit —
	// the Runs page's rule: sessions sharing one LastSeen ride along
	// instead of being skipped by a next page read on Before alone.
	rs, err := d.conn.Query(ctx, `SELECT * FROM (SELECT SessionID, max(PublicID) AS sPublicID, max(Agent) AS sAgent,
		uniqExact(RunId) AS sTurns, min(Started) AS sFirstSeen, max(LastSeen) AS sLastSeen,
		sum(InputTokens) AS sInputTokens, sum(OutputTokens) AS sOutputTokens,
		sum(CachedInputTokens) AS sCachedInputTokens, sum(CacheWriteTokens) AS sCacheWriteTokens,
		sum(ReasoningTokens) AS sReasoningTokens,
		argMax(FinishedOK, (Turn, Started)) AS sNewestOK, argMax(Failed, (Turn, Started)) AS sNewestFailed,
		argMax(LastSeen, (Turn, Started)) AS sNewestLastSeen
		FROM `+runs+` GROUP BY SessionID`+havingSQL(having)+
		` ORDER BY sLastSeen DESC LIMIT ? WITH TIES) ORDER BY sLastSeen DESC, SessionID DESC LIMIT ?`, append(pageArgs, limit, limit+obsdb.MaxTies)...)
	if err != nil {
		return obsdb.SessionPage{}, err
	}
	defer func() { _ = rs.Close() }()
	now := time.Now()
	page := obsdb.SessionPage{Total: int(total)}
	for rs.Next() {
		var row obsdb.SessionRow
		var turns uint64
		var inTok, outTok, cached, cacheW, reason int64
		var ok, failed bool
		var newestSeen time.Time
		if err := rs.Scan(&row.ID, &row.PublicID, &row.Agent, &turns,
			&row.FirstSeen, &row.LastSeen, &inTok, &outTok, &cached, &cacheW, &reason,
			&ok, &failed, &newestSeen); err != nil {
			return obsdb.SessionPage{}, err
		}
		row.Turns = int(turns)
		row.FirstSeen, row.LastSeen = row.FirstSeen.UTC(), row.LastSeen.UTC()
		row.Usage = core.Usage{
			InputTokens: inTok, OutputTokens: outTok,
			CachedInputTokens: cached, CacheWriteTokens: cacheW,
			ReasoningTokens: reason,
		}
		row.Status = obsdb.DeriveStatus(failed, ok, newestSeen, now)
		page.Sessions = append(page.Sessions, row)
	}
	if err := rs.Err(); err != nil {
		return obsdb.SessionPage{}, err
	}
	if len(page.Sessions) >= limit {
		end := page.Sessions[len(page.Sessions)-1]
		page.NextBefore, page.NextBeforeID = &end.LastSeen, end.ID
	}
	return page, nil
}

// sessionRuns builds the per-run subquery both Sessions and Session
// group over: each run collapsed to its aggregates first, then scoped
// to top-level, non-playground runs that carry a session. The scope
// reads the collapsed row for the reason runsInner's filters do — a
// subagent run inherits its parent's session metadata and names its
// parent on run_start only, so a row-level top-level filter counted
// its later records as a turn of the session and summed its usage in a
// second time. narrow goes inside the collapse (Session narrows the
// scan to the one session's runs), cond onto the collapsed rows.
func sessionRuns(narrow, cond string) string {
	return `(SELECT * FROM (SELECT RunId, max(SessionID) AS SessionID, max(PublicID) AS PublicID,
				max(Agent) AS Agent, max(ParentRunID) AS ParentRunID, max(Playground) AS Playground,
				max(Turn) AS Turn, min(Started) AS Started, max(LastSeen) AS LastSeen,
				max(FinishedOK) AS FinishedOK, max(Failed) AS Failed,
				max(InputTokens) AS InputTokens, max(OutputTokens) AS OutputTokens,
				max(CachedInputTokens) AS CachedInputTokens, max(CacheWriteTokens) AS CacheWriteTokens,
				max(ReasoningTokens) AS ReasoningTokens
			FROM weft_runs` + narrow + `
			GROUP BY RunId)
		WHERE ParentRunID = '' AND Playground = 0 AND SessionID != ''` + cond + `)`
}

func (d *DB) Session(ctx context.Context, id string) (_ obsdb.SessionDetail, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionDetail{}, err
	}
	defer d.closedErr(&err)
	// The session's own grouped row, queried directly (the sqlite
	// backend's P2-1 fix): the old read scanned the newest 500
	// sessions, so an older session 404'd here while the list still
	// showed it.
	rs, err := d.conn.Query(ctx, `SELECT SessionID, max(PublicID) AS sPublicID, max(Agent) AS sAgent,
		uniqExact(RunId) AS sTurns, min(Started) AS sFirstSeen, max(LastSeen) AS sLastSeen,
		sum(InputTokens) AS sInputTokens, sum(OutputTokens) AS sOutputTokens,
		sum(CachedInputTokens) AS sCachedInputTokens, sum(CacheWriteTokens) AS sCacheWriteTokens,
		sum(ReasoningTokens) AS sReasoningTokens,
		argMax(FinishedOK, (Turn, Started)) AS sNewestOK, argMax(Failed, (Turn, Started)) AS sNewestFailed,
		argMax(LastSeen, (Turn, Started)) AS sNewestLastSeen
		FROM `+sessionRuns(
		" WHERE RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID = ?)",
		" AND SessionID = ?")+` GROUP BY SessionID`, id, id)
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	defer func() { _ = rs.Close() }()
	var row obsdb.SessionRow
	var turns uint64
	var inTok, outTok, cached, cacheW, reason int64
	var ok, failed bool
	var newestSeen time.Time
	if !rs.Next() {
		if err := rs.Err(); err != nil {
			return obsdb.SessionDetail{}, err
		}
		return obsdb.SessionDetail{}, fmt.Errorf("%w: session %s", obsdb.ErrNotFound, id)
	}
	if err := rs.Scan(&row.ID, &row.PublicID, &row.Agent, &turns,
		&row.FirstSeen, &row.LastSeen, &inTok, &outTok, &cached, &cacheW, &reason,
		&ok, &failed, &newestSeen); err != nil {
		return obsdb.SessionDetail{}, err
	}
	if err := rs.Err(); err != nil {
		return obsdb.SessionDetail{}, err
	}
	row.Turns = int(turns)
	row.FirstSeen, row.LastSeen = row.FirstSeen.UTC(), row.LastSeen.UTC()
	row.Usage = core.Usage{
		InputTokens: inTok, OutputTokens: outTok,
		CachedInputTokens: cached, CacheWriteTokens: cacheW,
		ReasoningTokens: reason,
	}
	now := time.Now()
	row.Status = obsdb.DeriveStatus(failed, ok, newestSeen, now)
	// Every turn (the SQLite backend's rule): a cap here kept the oldest
	// 500 and silently dropped the newest. A caller that wants pages
	// reads Runs with RunQuery.SessionID.
	runs, err := d.queryRunRows(ctx, now,
		`SELECT `+runColumns+` FROM weft_runs FINAL
		WHERE RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID = ?)
			AND SessionID = ? AND ParentRunID = '' AND Playground = 0
		ORDER BY Turn, Started, RunId`, id, id)
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	if err := d.fillCounts(ctx, rowPtrs(runs)); err != nil {
		return obsdb.SessionDetail{}, err
	}
	return obsdb.SessionDetail{SessionRow: row, Runs: runs}, nil
}

func (d *DB) ResolvePublicID(ctx context.Context, publicID string) (_ string, err error) {
	if err := d.checkOpen(); err != nil {
		return "", err
	}
	defer d.closedErr(&err)
	var sessionID string
	// The RunId IN (...) narrows FINAL to the runs that carry the id, by
	// primary key (Session's and Run's rule) — without it every
	// resolution merged the whole table.
	err = d.conn.QueryRow(ctx,
		`SELECT SessionID FROM weft_runs FINAL
		WHERE RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.PublicID = ?)
			AND PublicID = ? AND SessionID != '' ORDER BY Turn DESC, Started DESC, RunId DESC LIMIT 1`,
		publicID, publicID).Scan(&sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: public id %s", obsdb.ErrNotFound, publicID)
		}
		return "", err
	}
	return sessionID, nil
}

// contractKeys is the read-side copy of obsdb's record/span contract —
// the attribute keys obsdb.MetaOf excludes from a run row's metadata.
// The SQL filter needs them inside the database; MetaOf itself stays
// the one implementation (applied to every row on read), and
// TestContractKeys pins the copy to MetaOf's behaviour key by key.
var contractKeys = []string{
	"weft.run.id", "weft.parent.run.id", "weft.parent.call.id",
	"weft.session.id", "weft.public_id", "weft.turn", "gen_ai.agent.name",
	"weft.record", "weft.event.type", "weft.event.pos", "weft.delta.pos",
	"weft.messages.index", "weft.step.index", "weft.tool.seq",
	"weft.playground", "weft.experiment.id", "weft.forked_from",
	"weft.messages.count", "weft.messages.input", "weft.content",
	"weft.content.truncated_bytes", "weft.version", "weft.manifest.hash",
	"weft.run.steps", "weft.run.pending", "weft.run.stop_reason",
	"weft.stop.raw", "weft.model.tool_calls", "weft.tool.approved",
	"weft.tool.pending", "weft.tool.result_bytes", "weft.metadata.dropped",
	"weft.override.hash",
	"gen_ai.operation.name", "gen_ai.provider.name", "gen_ai.request.model",
	"gen_ai.response.finish_reasons", "gen_ai.usage.input_tokens",
	"gen_ai.usage.output_tokens", "gen_ai.usage.cache_read.input_tokens",
	"gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.reasoning.output_tokens",
	"gen_ai.tool.name", "gen_ai.tool.call.id", "gen_ai.conversation.id",
	"session.id", "user.id", "error.type",
	// ADR 0028 (migration 0004's tuple; 0001's and 0003's end above).
	"weft.request.index", "weft.prompt.index", "weft.tools.index",
	"weft.system.hash", "weft.catalog.hash", "weft.attempt.index",
	"weft.instructions.hash", "weft.messages.reason", "weft.messages.from_seq",
	"weft.messages.to_seq", "weft.compaction.hash", "weft.compaction.scope",
	// Plan A4 (ADR 0016's A4 note), the same restatement of 0004.
	"gen_ai.response.model", "weft.stream", "weft.ttft_ms", "weft.latency_ms",
}

// unmarshalAttrs decodes a JSON column keeping integral numbers as
// int64, so a stored attribute map equals the OTLP-decoded one (plain
// json.Unmarshal would turn every number into float64) — the same rule
// the SQLite backend applies.
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
