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

	"github.com/weftgo/weft"
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
	CachedInputTokens, CacheWriteTokens, ReasoningTokens, DeltaCount`

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
	max(ReasoningTokens) AS ReasoningTokens, max(DeltaCount) AS DeltaCount`

// statusCase is the four-row table in SQL over weft_runs' columns: it
// must agree with obsdb.DeriveStatus, whose test pins the boundaries.
// One bind parameter precedes it: the interrupted cutoff
// (now - InterruptedAfter).
const statusCase = `CASE WHEN Failed = 1 THEN 'failed'
	WHEN FinishedOK = 1 THEN 'succeeded'
	WHEN LastSeen >= ? THEN 'running'
	ELSE 'interrupted' END`

func (d *DB) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunPage{}, err
	}
	now := time.Now()
	inner, args := runsInner(q)
	var conds []string
	var outerArgs []any
	if q.Status != "" {
		outerArgs = append(outerArgs, now.Add(-obsdb.InterruptedAfter), string(q.Status))
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
		frag += " AND Started < ?"
		pageArgs = append(pageArgs, q.Before)
	}
	frag += " ORDER BY Started DESC, RunId DESC LIMIT ?"
	pageArgs = append(pageArgs, obsdb.LimitOf(q.Limit))
	rows, err := d.queryRunRows(ctx, frag, pageArgs...)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	if err := d.fillCounts(ctx, rowPtrs(rows)); err != nil {
		return obsdb.RunPage{}, err
	}
	page := obsdb.RunPage{Runs: rows, Total: total}
	if len(rows) == obsdb.LimitOf(q.Limit) {
		last := rows[len(rows)-1].Started
		page.NextBefore = &last
	}
	return page, nil
}

// runsInner builds weft_runs' grouped subquery with its bind arguments.
// The row filters run inside their own SELECT: ClickHouse aliases are
// query-global, so a WHERE beside `max(ParentRunID) AS ParentRunID`
// would resolve the name to the aggregate and fail.
func runsInner(q obsdb.RunQuery) (string, []any) {
	var conds []string
	var args []any
	if q.Agent != "" {
		conds = append(conds, "Agent = ?")
		args = append(args, q.Agent)
	}
	if q.SessionID != "" {
		conds = append(conds, "SessionID = ?")
		args = append(args, q.SessionID)
	}
	if q.PublicID != "" {
		conds = append(conds, "PublicID = ?")
		args = append(args, q.PublicID)
	}
	switch q.ParentRunID {
	case "":
		conds = append(conds, "ParentRunID = ''")
	case "*":
	default:
		conds = append(conds, "ParentRunID = ?")
		args = append(args, q.ParentRunID)
	}
	if q.Playground != nil {
		conds = append(conds, "Playground = ?")
		args = append(args, *q.Playground)
	}
	for k, v := range q.Meta {
		// Meta holds the caller metadata (contract keys filtered at the
		// view); a subset match asks for the key's exact string.
		conds = append(conds, "JSONHas(Meta, ?) AND JSONExtractString(Meta, ?) = ?")
		args = append(args, k, k, v)
	}
	return "SELECT " + runAggregates + " FROM (SELECT * FROM weft_runs WHERE " +
		strings.Join(or1(conds), " AND ") + ") GROUP BY RunId", args
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

func (d *DB) Run(ctx context.Context, id string) (obsdb.RunDetail, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunDetail{}, err
	}
	rows, err := d.queryRunRows(ctx,
		"SELECT "+runColumns+" FROM weft_runs FINAL WHERE RunId = ?", id)
	if err != nil {
		return obsdb.RunDetail{}, err
	}
	if len(rows) == 0 {
		return obsdb.RunDetail{}, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, id)
	}
	children, err := d.queryRunRows(ctx,
		"SELECT "+runColumns+" FROM weft_runs FINAL WHERE ParentRunID = ? ORDER BY Started, RunId", id)
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
// derives status with one clock for the whole result.
func (d *DB) queryRunRows(ctx context.Context, sql string, args ...any) ([]obsdb.RunRow, error) {
	rs, err := d.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	now := time.Now()
	var out []obsdb.RunRow
	for rs.Next() {
		var s runScan
		if err := rs.Scan(&s.ID, &s.ParentRunID, &s.ParentCallID, &s.TraceID, &s.Agent,
			&s.Provider, &s.Model, &s.ManifestHash, &s.WeftVersion, &s.Service,
			&s.SessionID, &s.PublicID, &s.Turn, &s.Playground, &s.ExperimentID, &s.ForkedFrom,
			&s.Meta, &s.Started, &s.Finished, &s.LastSeen, &s.FinishedOK, &s.Failed, &s.Err,
			&s.Steps, &s.Pending, &s.StopReason,
			&s.InTok, &s.OutTok, &s.CachedTok, &s.CacheWriteTok, &s.ReasonTok, &s.DeltaCount); err != nil {
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
		Usage: weft.Usage{
			InputTokens: s.InTok, OutputTokens: s.OutTok,
			CachedInputTokens: s.CachedTok, CacheWriteTokens: s.CacheWriteTok,
			ReasoningTokens: s.ReasonTok,
		},
		DeltaCount: s.DeltaCount,
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
	if len(ids) == 0 {
		return nil
	}
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
	rs, err := d.conn.Query(ctx,
		`SELECT Pos, Time, Body FROM weft_records FINAL
		WHERE RunId = ? AND Kind = 'event' AND Pos > ? ORDER BY Pos LIMIT ?`,
		runID, after, limit)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var ev obsdb.PosEvent
		if err := rs.Scan(&ev.Pos, &ev.Time, &ev.Event); err != nil {
			return obsdb.EventPage{}, err
		}
		ev.Time = ev.Time.UTC()
		page.Events = append(page.Events, ev)
	}
	if err := rs.Err(); err != nil {
		return obsdb.EventPage{}, err
	}
	var remaining uint64
	if err := d.conn.QueryRow(ctx,
		`SELECT uniqExactIf(Pos, Kind = 'event') FROM weft_records WHERE RunId = ? AND Pos > ?`,
		runID, after).Scan(&remaining); err != nil {
		return obsdb.EventPage{}, err
	}
	if remaining > uint64(len(page.Events)) {
		page.Done = false
		if n := len(page.Events); n > 0 {
			last := page.Events[n-1].Pos
			page.NextAfter = &last
		}
	} else {
		// Every stored event returned: done only when the run is
		// terminal — a running run may still emit more.
		var finishedOK, failed bool
		if err := d.conn.QueryRow(ctx,
			`SELECT max(FinishedOK), max(Failed) FROM weft_runs WHERE RunId = ?`, runID).
			Scan(&finishedOK, &failed); err != nil {
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
	rs, err := d.conn.Query(ctx,
		`SELECT DISTINCT Pos FROM weft_records WHERE RunId = ? AND Kind = 'event' ORDER BY Pos`, runID)
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
	rs, err := d.conn.Query(ctx,
		`SELECT Body FROM weft_records FINAL
		WHERE RunId = ? AND Kind = 'messages' ORDER BY Pos`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []json.RawMessage
	for rs.Next() {
		var body string
		if err := rs.Scan(&body); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(body))
	}
	return out, rs.Err()
}

const spanColumns = `Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName,
	ResourceAttributes, SpanAttributes, Duration, StatusCode, StatusMessage,
	Events.Timestamp, Events.Name, Events.Attributes,
	WeftAttrs, WeftEvents, WeftResource`

func (d *DB) RunSpans(ctx context.Context, runID string) ([]obsdb.Span, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	return d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE RunId = ? ORDER BY Timestamp, SpanId", runID)
}

// Trace returns any trace's spans — weft and non-weft alike (the
// polyglot promise). An unknown trace id returns no rows and no error,
// like the SQLite backend.
func (d *DB) Trace(ctx context.Context, traceID string) ([]obsdb.Span, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	return d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE TraceId = ? ORDER BY Timestamp, SpanId", traceID)
}

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
		s.End = s.Start.Add(time.Duration(dur))
		s.Attrs = attrsFromJSONOrMap(weftAttrs, attrs)
		s.Resource = attrsFromJSONOrMap(weftRes, res)
		s.Events = eventsFromJSONOrNested(weftEvents, evT, evN, evA)
		s.Start, s.End = s.Start.UTC(), s.End.UTC()
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

func eventsFromJSONOrNested(js string, times []time.Time, names []string, attrs []map[string]string) []obsdb.SpanEvent {
	if js != "" {
		type wireEvent struct {
			Time  time.Time      `json:"Time"`
			Name  string         `json:"Name"`
			Attrs map[string]any `json:"Attrs"`
		}
		var events []wireEvent
		if err := json.Unmarshal([]byte(js), &events); err == nil && len(events) > 0 {
			out := make([]obsdb.SpanEvent, len(events))
			for i, e := range events {
				out[i] = obsdb.SpanEvent{Time: e.Time.UTC(), Name: e.Name, Attrs: e.Attrs}
			}
			return out
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
		out[i] = obsdb.SpanEvent{Time: times[i].UTC(), Name: names[i], Attrs: a}
	}
	return out
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
func (d *DB) Sessions(ctx context.Context, q obsdb.SessionQuery) (obsdb.SessionPage, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionPage{}, err
	}
	runs := `(SELECT RunId, SessionID, max(PublicID) AS PublicID, max(Agent) AS Agent,
			max(Turn) AS Turn, min(Started) AS Started, max(LastSeen) AS LastSeen,
			max(FinishedOK) AS FinishedOK, max(Failed) AS Failed,
			max(InputTokens) AS InputTokens, max(OutputTokens) AS OutputTokens,
			max(CachedInputTokens) AS CachedInputTokens, max(CacheWriteTokens) AS CacheWriteTokens,
			max(ReasoningTokens) AS ReasoningTokens
		FROM weft_runs
		WHERE ParentRunID = '' AND Playground = 0 AND SessionID != ''
		GROUP BY RunId, SessionID)`
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
	if !q.Before.IsZero() {
		having = append(having, "max(LastSeen) < ?")
		hargs = append(hargs, q.Before)
	}
	havingSQL := ""
	if len(having) > 0 {
		havingSQL = " HAVING " + strings.Join(having, " AND ")
	}
	var total uint64
	if err := d.conn.QueryRow(ctx,
		"SELECT count() FROM (SELECT SessionID FROM "+runs+" GROUP BY SessionID"+havingSQL+")", hargs...).Scan(&total); err != nil {
		return obsdb.SessionPage{}, err
	}
	limit := obsdb.LimitOf(q.Limit)
	rs, err := d.conn.Query(ctx, `SELECT SessionID, max(PublicID) AS sPublicID, max(Agent) AS sAgent,
		uniqExact(RunId) AS sTurns, min(Started) AS sFirstSeen, max(LastSeen) AS sLastSeen,
		sum(InputTokens) AS sInputTokens, sum(OutputTokens) AS sOutputTokens,
		sum(CachedInputTokens) AS sCachedInputTokens, sum(CacheWriteTokens) AS sCacheWriteTokens,
		sum(ReasoningTokens) AS sReasoningTokens,
		argMax(FinishedOK, (Turn, Started)) AS sNewestOK, argMax(Failed, (Turn, Started)) AS sNewestFailed,
		argMax(LastSeen, (Turn, Started)) AS sNewestLastSeen
		FROM `+runs+` GROUP BY SessionID`+havingSQL+
		` ORDER BY sLastSeen DESC LIMIT ?`, append(append([]any{}, hargs...), limit)...)
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
		row.Usage = weft.Usage{
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
		`SELECT `+runColumns+` FROM weft_runs FINAL
		WHERE SessionID = ? AND ParentRunID = '' AND Playground = 0
		ORDER BY Turn, Started, RunId LIMIT ?`, id, 500)
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	if err := d.fillCounts(ctx, rowPtrs(runs)); err != nil {
		return obsdb.SessionDetail{}, err
	}
	return obsdb.SessionDetail{SessionRow: row, Runs: runs}, nil
}

func (d *DB) ResolvePublicID(ctx context.Context, publicID string) (string, error) {
	if err := d.checkOpen(); err != nil {
		return "", err
	}
	var sessionID string
	err := d.conn.QueryRow(ctx,
		`SELECT SessionID FROM weft_runs FINAL
		WHERE PublicID = ? AND SessionID != '' ORDER BY Turn DESC, LastSeen DESC LIMIT 1`,
		publicID).Scan(&sessionID)
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
