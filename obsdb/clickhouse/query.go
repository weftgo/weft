package clickhouse

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/caps"
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
	InstructionsHash, CatalogHash, RequestCount, Derived, RootEnded,
	StoppedBy, CancelCause, EventsEmitted, MessagesEmitted, ErrorType, Problems,
	ResumedFrom, CallerRunID, CallerCallID`

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
	max(RequestCount) AS RequestCount, max(Derived) AS Derived, max(RootEnded) AS RootEnded,
	max(StoppedBy) AS StoppedBy, max(CancelCause) AS CancelCause,
	max(EventsEmitted) AS EventsEmitted, max(MessagesEmitted) AS MessagesEmitted,
	max(ErrorType) AS ErrorType, groupUniqArrayArray(Problems) AS Problems,
	max(ResumedFrom) AS ResumedFrom, max(CallerRunID) AS CallerRunID, max(CallerCallID) AS CallerCallID`

// statusCase is the four-row table in SQL over weft_runs' columns: it
// must agree with obsdb.DeriveStatus, whose test pins the boundaries —
// succeeded on a run_finish or on the stored invoke_agent span
// (RootEnded, migration 0010).
// One bind parameter precedes it: the interrupted cutoff
// (now - InterruptedAfter) as integer nanoseconds — a positional
// time.Time bind renders at Seconds scale (clickhouse-go's bind.go),
// which would widen the running band by up to a second.
const statusCase = `CASE WHEN Failed = 1 THEN 'failed'
	WHEN FinishedOK = 1 OR RootEnded = 1 THEN 'succeeded'
	WHEN toUnixTimestamp64Nano(LastSeen) >= ? THEN 'running'
	ELSE 'interrupted' END`

// statusConds is a status filter over the grouped rows (Runs and
// Search alike): statusCase equal to the derived status, and for a
// read-time word (I3) its own condition — parked an open park (Pending
// above 0 and no run whose ResumedFrom names it: resumed_by's rule — the
// NOT IN set is built from every row's ResumedFrom, a column scan the
// bloom idx_resumed_from cannot narrow (it serves lookups of given ids;
// README.md states the cost)), cancelled a failed run whose ErrorType
// is cancelled. None for "".
func statusConds(st obsdb.Status, now time.Time) ([]string, []any) {
	if st == "" {
		return nil, nil
	}
	conds := []string{"(" + statusCase + ") = ?"}
	args := []any{now.Add(-obsdb.InterruptedAfter).UnixNano(), string(st.Stored())}
	switch st {
	case obsdb.StatusCancelled:
		conds = append(conds, "ErrorType = ?")
		args = append(args, obsdb.ErrorTypeCancelled)
	case obsdb.StatusParked:
		conds = append(conds, "Pending > 0 AND RunId NOT IN (SELECT rb.ResumedFrom FROM weft_runs AS rb WHERE rb.ResumedFrom <> '')")
	}
	return conds, args
}

func (d *DB) Runs(ctx context.Context, q obsdb.RunQuery) (_ obsdb.RunPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunPage{}, err
	}
	defer d.closedErr(&err)
	now := time.Now()
	inner, args := runsInner(q)
	conds, outerArgs := statusConds(q.Status, now)
	total, err := d.countRows(ctx,
		"SELECT count() FROM ("+inner+") WHERE "+strings.Join(or1(conds), " AND "), args, outerArgs)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	pageArgs := append(append([]any{}, args...), outerArgs...)
	frag := "SELECT * FROM (" + inner + ") WHERE " + strings.Join(or1(conds), " AND ")
	// past is the cursor's direction: older runs, or newer under Oldest.
	past, dir := "<", "DESC"
	if q.Oldest {
		past, dir = ">", "ASC"
	}
	if !q.Before.IsZero() {
		// Integer nanoseconds, not a time.Time bind: the driver renders
		// positional time.Time at Seconds scale, so `Started < ?`
		// compared against S.000000000 and every row in
		// [S.000, S.<nanos>) sorted right after the boundary row — no
		// later page ever returned it (rows silently skipped at
		// essentially every page boundary).
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			frag += " AND (toUnixTimestamp64Nano(Started) " + past + " ? OR (toUnixTimestamp64Nano(Started) = ? AND RunId " + past + " ?))"
			pageArgs = append(pageArgs, ns, ns, q.BeforeID)
		} else {
			frag += " AND toUnixTimestamp64Nano(Started) " + past + " ?"
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
	frag = "SELECT * FROM (" + frag + " ORDER BY Started " + dir + " LIMIT ? WITH TIES) ORDER BY Started " + dir + ", RunId " + dir + " LIMIT ?"
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
	if q.CallerRunID != "" {
		eq("CallerRunID", q.CallerRunID)
	}
	if q.TurnBelow > 0 {
		conds = append(conds, "Turn < ?")
		args = append(args, q.TurnBelow)
	}
	if q.TurnAbove > 0 {
		conds = append(conds, "Turn > ?")
		args = append(args, q.TurnAbove)
	}
	switch {
	case q.ParentRunID == "" && q.SessionID != "":
		// A session's top-level runs are its own turns (D3, the
		// sessions' rule): a delegated session's turns carry a caller
		// link and are its turns all the same.
		conds = append(conds, ownTurnCH)
	case q.ParentRunID == "":
		// Top-level: no Subagent parent, no caller link (O11).
		conds = append(conds, "ParentRunID = '' AND CallerRunID = ''")
	case q.ParentRunID == "*":
	default:
		eq("ParentRunID", q.ParentRunID)
	}
	if !q.LastSeenAfter.IsZero() {
		conds = append(conds, "toUnixTimestamp64Nano(LastSeen) >= ?")
		args = append(args, q.LastSeenAfter.UnixNano())
	}
	if q.Playground != nil {
		conds = append(conds, "Playground = ?")
		args = append(args, *q.Playground)
	}
	if q.ExperimentID != "" {
		eq("ExperimentID", q.ExperimentID)
	}
	if q.ErrorType != "" {
		eq("ErrorType", q.ErrorType)
	}
	if q.IDPrefix != "" {
		// RunId is the grouping key: narrowing on it is the whole filter.
		narrow = append(narrow, "startsWith(RunId, ?)")
		narrowArgs = append(narrowArgs, q.IDPrefix)
	}
	switch {
	case q.StoppedBy == "":
	case strings.HasSuffix(q.StoppedBy, ":"):
		// A prefix (has_tool_call: matches every has_tool_call:<tools>):
		// startsWith, so no LIKE wildcard in a tool name reads as one.
		conds = append(conds, "startsWith(StoppedBy, ?)")
		args = append(args, q.StoppedBy)
	default:
		eq("StoppedBy", q.StoppedBy)
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
			&s.InstructionsHash, &s.CatalogHash, &s.RequestCount, &s.Derived, &s.RootEnded,
			&s.StoppedBy, &s.CancelCause, &s.EventsEmitted, &s.MessagesEmitted,
			&s.ErrorType, &s.Problems, &s.ResumedFrom, &s.CallerRunID, &s.CallerCallID); err != nil {
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
	Derived, RootEnded                                 bool
	StoppedBy, CancelCause                             string
	EventsEmitted, MessagesEmitted                     int64
	ErrorType                                          string
	Problems                                           []string
	ResumedFrom                                        string
	CallerRunID, CallerCallID                          string
}

func (s *runScan) row(now time.Time) (obsdb.RunRow, error) {
	row := obsdb.RunRow{
		ID: s.ID, ParentRunID: s.ParentRunID, ParentCallID: s.ParentCallID, TraceID: s.TraceID,
		Agent: s.Agent, Provider: s.Provider, Model: s.Model,
		ManifestHash: s.ManifestHash, WeftVersion: s.WeftVersion, Service: s.Service,
		SessionID: s.SessionID, PublicID: s.PublicID, Turn: int(s.Turn),
		Playground: s.Playground, ExperimentID: s.ExperimentID, ForkedFrom: s.ForkedFrom,
		ResumedFrom: s.ResumedFrom, CallerRunID: s.CallerRunID, CallerCallID: s.CallerCallID,
		Started: s.Started.UTC(), LastSeen: s.LastSeen.UTC(),
		Err: s.Err, Steps: int(s.Steps), Pending: int(s.Pending), StopReason: s.StopReason,
		Usage: core.Usage{
			InputTokens: s.InTok, OutputTokens: s.OutTok,
			CachedInputTokens: s.CachedTok, CacheWriteTokens: s.CacheWriteTok,
			ReasoningTokens: s.ReasonTok,
		},
		DeltaCount:       s.DeltaCount,
		InstructionsHash: s.InstructionsHash, CatalogHash: s.CatalogHash, RequestCount: s.RequestCount,
		Derived: s.Derived, RootEnded: s.RootEnded, FinishRecorded: s.FinishedOK,
		StoppedBy: s.StoppedBy, CancelCause: s.CancelCause,
		EventsEmitted: s.EventsEmitted, MessagesEmitted: s.MessagesEmitted,
		ErrorType: s.ErrorType, Problems: obsdb.CountProblems(s.Problems),
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
	row.Status = obsdb.DeriveStatus(s.Failed, s.FinishedOK || s.RootEnded, row.LastSeen, now)
	// A failed root is terminal: nothing is coming, so it never settles
	// (the event page calls it done at once, the same rule).
	row.Settling = obsdb.DeriveSettling(s.RootEnded && !s.Derived && !s.Failed, s.FinishedOK, row.LastSeen, now)
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

// fillCounts loads EventCount/MessageCount (and PromptPreview,
// fillPreviewsOf) for the rows' runs from
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
		n := idChunk(ids)
		if err := d.fillCountsOf(ctx, ids[:n], byID); err != nil {
			return err
		}
		if err := d.fillPreviewsOf(ctx, ids[:n], byID); err != nil {
			return err
		}
		if err := d.fillResumedByOf(ctx, ids[:n], byID); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

// fillResumedByOf loads ResumedBy (contract §6, U13.1): for each of the
// rows' runs, the latest run by start time whose ResumedFrom names it
// (a tie on Started goes to the larger RunId; never the largest id
// alone — a core run id is random hex, a thread turn's <session>-t10
// sorts before -t9) — derived here rather than stored, so a resumer
// ingested before or after the parked run links it alike and no row is
// written for a parked run not stored (migration 0019). Each resumer's
// partial rows are merged first (min Started), the way every run read
// does; idx_resumed_from skips the granules no resumer names.
func (d *DB) fillResumedByOf(ctx context.Context, ids []string, byID map[string]*obsdb.RunRow) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := d.conn.Query(ctx,
		"SELECT ResumedFrom, argMax(RunId, (Started, RunId)) FROM ("+
			"SELECT RunId, max(ResumedFrom) AS ResumedFrom, min(Started) AS Started FROM weft_runs "+
			"WHERE RunId IN (SELECT RunId FROM weft_runs WHERE ResumedFrom IN ("+placeholders+")) GROUP BY RunId"+
			") WHERE ResumedFrom IN ("+placeholders+") GROUP BY ResumedFrom",
		append(args, args...)...)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var from, by string
		if err := rs.Scan(&from, &by); err != nil {
			return err
		}
		if row, ok := byID[from]; ok {
			row.ResumedBy = by
		}
	}
	return rs.Err()
}

func (d *DB) fillCountsOf(ctx context.Context, ids []string, byID map[string]*obsdb.RunRow) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := d.conn.Query(ctx,
		"SELECT RunId, uniqExactIf(Pos, Kind = 'event'), uniqExactIf(Pos, Kind = 'messages' AND Reason = '' AND Pos >= 0) FROM weft_records WHERE RunId IN ("+placeholders+") GROUP BY RunId",
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
	var finishedOK, failed, rootEnded, derived bool
	var lastSeen time.Time
	var stored uint64
	if err := d.conn.QueryRow(ctx,
		`SELECT max(FinishedOK), max(Failed), max(RootEnded), max(Derived), max(LastSeen), count() FROM weft_runs WHERE RunId = ?`, runID).
		Scan(&finishedOK, &failed, &rootEnded, &derived, &lastSeen, &stored); err != nil {
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
		`SELECT Pos, Time, Body, Content, TruncatedBytes FROM weft_records FINAL
		WHERE RunId = ? AND Kind = 'event' AND Pos > ? ORDER BY Pos LIMIT ?`,
		runID, after, limit+1)
	if err != nil {
		return obsdb.EventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	var page obsdb.EventPage
	for rs.Next() {
		var ev obsdb.PosEvent
		if err := rs.Scan(&ev.Pos, &ev.Time, &ev.Event, &ev.Content, &ev.TruncatedBytes); err != nil {
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
		// terminal — a running run may still emit more, and a root-ended
		// one's trailing records are waited for while it settles
		// (obsdb.EventPage.Done).
		page.Done = finishedOK || failed ||
			(rootEnded && !obsdb.DeriveSettling(rootEnded && !derived && !failed, finishedOK, lastSeen, time.Now()))
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
	// Growth records only (Reason = ''): a compaction view (ADR 0028
	// §8) is not transcript; Compactions reads it.
	// Step is weft_records.Step and Input weft_records.Input (0004;
	// -1 on rows written before it). A row without a stored input flag
	// has its index 0 inferred and marked so (obsdb.TranscriptBatch).
	rs, err := d.conn.Query(ctx,
		`SELECT Pos, Step, Input, Body FROM weft_records FINAL
		WHERE RunId = ? AND Kind = 'messages' AND Reason = '' AND Pos >= 0 ORDER BY Pos`, runID)
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

// Compactions reads the run's compaction records (ADR 0028 §8) from
// otel_logs, the one table that keeps their attributes: weft_records
// holds a view's Reason but not its range or hash, and thread's
// session marker (kind compaction) is outside weft_records_mv's kinds.
// The scan is bounded by the run's time window from weft_runs.
// otel_logs is a plain MergeTree: a retried batch's duplicate is
// dropped here, by (kind, index) — by hash for a marker.
func (d *DB) Compactions(ctx context.Context, runID string) (_ []obsdb.Compaction, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	// The run's time window bounds the otel_logs scan (its order key
	// leads with time): every record of the run — the marker included,
	// which moves LastSeen like any record — sits inside it. ε absorbs
	// clock skew between the emitters of one run.
	var (
		n             uint64
		started, last time.Time
	)
	if err := d.conn.QueryRow(ctx,
		`SELECT count(), min(Started), max(LastSeen) FROM weft_runs WHERE RunId = ?`, runID).Scan(&n, &started, &last); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, runID)
	}
	const skew = time.Minute
	// The views are messages records naming a reason, the marker a
	// record of thread's compaction kind — the selection SQLite makes
	// (CompactionOf decides; an unknown reason is its error).
	rs, err := d.conn.Query(ctx,
		`SELECT LogAttributes, Body FROM otel_logs
		WHERE Timestamp BETWEEN ? AND ?
		  AND LogAttributes['weft.run.id'] = ?
		  AND ((LogAttributes['weft.record'] = 'messages' AND mapContains(LogAttributes, 'weft.messages.reason'))
		       OR LogAttributes['weft.record'] = 'compaction')
		ORDER BY Timestamp`, started.Add(-skew), last.Add(skew), runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	type key struct {
		kind  string
		index int64
		hash  string // a marker's identity: markers carry no index
	}
	seen := map[key]bool{}
	out := []obsdb.Compaction{}
	for rs.Next() {
		var (
			la   map[string]string
			body string
		)
		if err := rs.Scan(&la, &body); err != nil {
			return nil, err
		}
		attrs := make(map[string]any, len(la))
		for k, v := range la {
			attrs[k] = v
		}
		kind := la["weft.record"]
		index := int64(-1)
		if kind == "messages" {
			idx, perr := strconv.ParseInt(la["weft.messages.index"], 10, 64)
			if perr != nil {
				// SQLite stores such a record at its derived position; a
				// view this backend cannot place is an error, never a
				// silent drop.
				return nil, fmt.Errorf("clickhouse: run %s: messages record with weft.messages.reason %q has no usable weft.messages.index (%q)",
					runID, la["weft.messages.reason"], la["weft.messages.index"])
			}
			index = idx
		}
		step := -1
		if n, perr := strconv.Atoi(la["weft.step.index"]); perr == nil {
			step = n
		}
		c, ok, cerr := obsdb.CompactionOf(kind, index, step, attrs, []byte(body))
		if cerr != nil {
			return nil, fmt.Errorf("clickhouse: run %s: %w", runID, cerr)
		}
		k := key{kind: kind, index: index}
		if kind == obsdb.RecordCompaction {
			k.hash = c.Hash
		}
		if !ok || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	obsdb.SortCompactions(out)
	return out, nil
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
	runTrace, err := d.runTrace(ctx, runID)
	if err != nil {
		return nil, err
	}
	if trace, _, ok := obsdb.ParseStockRunID(runID); ok {
		// A derived run (docs/record-contract.md §9) names no span by
		// run id: its spans are its root's subtree, read from the
		// trace (obsdb.StockRunSpans).
		spans, err := d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE TraceId = ?"+
			spanDedup, trace)
		if err != nil {
			return nil, err
		}
		return obsdb.StockRunSpans(spans, runID), nil
	}
	if runTrace != "" {
		// The run row names its trace: the bloom-indexed TraceId
		// (idx_trace_id) narrows the granules before RunId is tested.
		// A run whose spans are not under that trace (its records
		// carried a trace its spans did not) falls through to RunId
		// alone, which idx_weft_run_id (0016) serves. A non-empty
		// by-trace answer is trusted as the whole run: no writer path
		// splits one run id across traces, so the fallback fires only
		// on an empty result.
		spans, err := d.querySpans(ctx, runSpansByTrace, runTrace, runID)
		if err != nil || len(spans) > 0 {
			return spans, err
		}
	}
	return d.querySpans(ctx, runSpansByRun, runID)
}

// RunTreeSpans is obsdb.TreeReader's. A weft run's subtree is found
// from the run rows before a span is read: the run and the rows core's
// id rule names its descendants (treeIDRows: weft_runs' primary key,
// RunId = ? OR startsWith), the rows whose ParentRunID is one of them
// (treeChildRows: idx_parent_run, 0020, the same set as a subquery),
// and one more level per non-Subagent child found (a literal list);
// obsdb.TreeMembers places a row whose parent chain does not reach the
// run by the id rule. Then the members' spans are read by RunId
// (idx_weft_run_id, 0016), in any trace, capped (treeSpans,
// obsdb.CapTree): the run's trace is never read whole. Every span with
// a weft.run.id has its run's row (weft_runs_traces_mv), so the rows
// name every run a span of the tree can belong to. A derived run (no
// run id on its spans) is cut from its trace (traceSpans), as RunSpans
// reads it. A Subagent-only tree costs four statements: the run's row,
// the id-rule rows, their children, the spans; a deeper chain of
// non-Subagent children one statement more per level, bounded by the
// tree's own depth. A derived run reads its trace's first
// obsdb.TreeTraceCap spans (stockTreeSpans).
func (d *DB) RunTreeSpans(ctx context.Context, q obsdb.TreeQuery) (_ obsdb.RunTree, err error) {
	runID := q.RunID
	if err := d.checkOpen(); err != nil {
		return obsdb.RunTree{}, err
	}
	defer d.closedErr(&err)
	if _, err := d.runTrace(ctx, runID); err != nil {
		return obsdb.RunTree{}, err
	}
	if t, _, ok := obsdb.ParseStockRunID(runID); ok {
		spans, err := d.querySpans(ctx, stockTreeSpans, t, caps.TreeTrace+1)
		if err != nil {
			return obsdb.RunTree{}, err
		}
		cut := len(spans) > caps.TreeTrace
		tree := obsdb.CapTree(spans[:min(len(spans), caps.TreeTrace)], false, q, nil, nil)
		tree.TraceTruncated = cut
		return tree, nil
	}
	rows, err := d.treeRunRows(ctx, treeIDRows+treeRowLimit, append(treeIDArgs(runID), caps.TreeRow+1)...)
	if err != nil {
		return obsdb.RunTree{}, err
	}
	parents := make(map[string]string, len(rows))
	for _, r := range rows {
		parents[r.ID] = r.ParentRunID
	}
	level, err := d.treeRunRows(ctx, treeChildRows(treeIDCondition)+treeRowLimit, append(treeIDArgs(runID), caps.TreeRow+1)...)
	if err != nil {
		return obsdb.RunTree{}, err
	}
	// Past obsdb.TreeRowCap rows the walk stops: the tree is cut there
	// (Truncated), its rows the first read.
	for len(level) > 0 && len(rows) <= caps.TreeRow {
		var frontier []string
		for _, r := range level {
			if _, ok := parents[r.ID]; !ok {
				parents[r.ID] = r.ParentRunID
				rows = append(rows, r)
				frontier = append(frontier, r.ID)
			}
		}
		level = nil
		for len(frontier) > 0 {
			n := idChunk(frontier)
			more, err := d.treeRunRows(ctx, treeChildRows("RunId IN ("+placeholders(n)+")")+treeRowLimit, append(anys(frontier[:n]), caps.TreeRow+1)...)
			if err != nil {
				return obsdb.RunTree{}, err
			}
			level = append(level, more...)
			frontier = frontier[n:]
		}
	}
	rowsCut := len(rows) > caps.TreeRow
	if rowsCut {
		for _, r := range rows[caps.TreeRow:] {
			delete(parents, r.ID)
		}
		rows = rows[:caps.TreeRow]
	}
	spans, err := d.treeSpans(ctx, q, parents, rows)
	if err != nil {
		return obsdb.RunTree{}, err
	}
	return obsdb.CapTree(spans, rowsCut || len(spans) > caps.TreeSpan, q, parents, rows), nil
}

// The run tree's statements (RunTreeSpans; runspans_internal_test.go
// pins their indexes). A run's merged columns are read grouped by
// RunId, in RunRow order.
const (
	treeRowColumns = "SELECT RunId, max(ParentRunID), max(PublicID), min(Started) AS started, " +
		"max(Failed), max(FinishedOK), max(RootEnded), max(LastSeen) FROM weft_runs WHERE "
	treeRowOrder = " GROUP BY RunId ORDER BY started, RunId"
	// treeRowLimit bounds each row read: obsdb.TreeRowCap and one more.
	treeRowLimit = " LIMIT ?"
	// treeIDCondition: the run and the ids the id rule names its
	// descendants, on weft_runs' primary key (the run's id, then its
	// id + "/"), of core's child shape (match: obsdb.TreeChildID's
	// rule, treeIDArgs' pattern) — obsdb.TreeMembers keeps those whose
	// row names no parent.
	treeIDCondition = "RunId = ? OR (startsWith(RunId, ?) AND match(RunId, ?))"
	treeIDRows      = treeRowColumns + treeIDCondition + treeRowOrder
	// treeSpansByIDRule: the spans of the runs treeIDCondition selects,
	// by RunId; treeSpans closes the parenthesis, after the literal list
	// of the other members when there are any.
	treeSpansByIDRule = "SELECT " + spanColumns + " FROM otel_traces WHERE RunId != '' AND " +
		"(RunId IN (SELECT RunId FROM weft_runs WHERE " + treeIDCondition + ")"
	// treeSpanOrder: start order, one row per span, the cap and one more.
	treeSpanOrder = " ORDER BY Timestamp, SpanId LIMIT 1 BY TraceId, SpanId LIMIT ?"
)

// treeIDArgs is treeIDCondition's binds for runID: the id, its prefix,
// and the RE2 pattern of core's child shape under it (obsdb.TreeChildID:
// a step of digits or "resume", then a non-empty call id).
func treeIDArgs(runID string) []any {
	return []any{runID, runID + "/", "^" + regexp.QuoteMeta(runID) + "/([0-9]+|resume)/(?s:.+)$"}
}

// stockTreeSpans is a derived run's tree read: its trace in start
// order, one row per span, the first obsdb.TreeTraceCap and one more
// (the trace holds more: RunTree.TraceTruncated).
const stockTreeSpans = traceSpans + " LIMIT ?"

// treeChildRows is the rows whose ParentRunID is one of the runs the
// condition parents selects on weft_runs — idx_parent_run's narrowing,
// then the runs' merged columns.
func treeChildRows(parents string) string {
	return treeRowColumns + "RunId IN (" + treeChildIDs(parents) + ")" + treeRowOrder
}

// treeChildIDs is treeChildRows' narrowing: the ids of the rows whose
// ParentRunID is one of the runs parents selects, on idx_parent_run.
func treeChildIDs(parents string) string {
	return "SELECT RunId FROM weft_runs WHERE ParentRunID != '' AND ParentRunID IN (SELECT RunId FROM weft_runs WHERE " + parents + ")"
}

// placeholders is n comma-separated binds.
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// anys is ids as bind arguments.
func anys(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// treeRunRows reads one of the tree's row statements.
func (d *DB) treeRunRows(ctx context.Context, q string, args ...any) ([]obsdb.TreeRun, error) {
	rs, err := d.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.TreeRun
	now := time.Now()
	for rs.Next() {
		var r obsdb.TreeRun
		var failed, finished, rootEnded bool
		var seen time.Time
		if err := rs.Scan(&r.ID, &r.ParentRunID, &r.PublicID, &r.Started, &failed, &finished, &rootEnded, &seen); err != nil {
			return nil, err
		}
		r.Started = timeOf(r.Started)
		r.Status = obsdb.DeriveStatus(failed, finished || rootEnded, timeOf(seen), now)
		out = append(out, r)
	}
	return out, rs.Err()
}

// treeSpans reads the members' spans (obsdb.TreeReadIDs: under a scope
// the visible ones alone): the run's and its id-rule rows' by one
// subquery when the read takes every row that subquery names — no
// scope, and every row of the id rule's shape a member — else by
// literal lists alone; every other member by a literal list (idChunk
// ids a statement, the first chunk in the subquery's statement), each
// statement in start order and capped at obsdb.TreeSpanCap + 1, merged
// in that order when there is more than one.
func (d *DB) treeSpans(ctx context.Context, q obsdb.TreeQuery, parents map[string]string, rows []obsdb.TreeRun) ([]obsdb.Span, error) {
	ids := obsdb.TreeReadIDs(q, parents, rows)
	read := make(map[string]bool, len(ids))
	for _, id := range ids {
		read[id] = true
	}
	useRule := q.PublicID == ""
	byRule := map[string]bool{}
	if useRule {
		byRule[q.RunID] = true
		for _, r := range rows {
			if obsdb.TreeChildID(q.RunID, r.ID) {
				byRule[r.ID] = true
				useRule = useRule && read[r.ID]
			}
		}
	}
	if !useRule {
		byRule = map[string]bool{}
	}
	var others []string
	for _, id := range ids {
		if !byRule[id] {
			others = append(others, id)
		}
	}
	var out []obsdb.Span
	merged := false
	if useRule {
		n := 0
		if len(others) > 0 {
			n = idChunk(others)
		}
		stmt, args := treeSpansByIDRule, treeIDArgs(q.RunID)
		if n > 0 {
			stmt += " OR RunId IN (" + placeholders(n) + ")"
			args = append(args, anys(others[:n])...)
		}
		var err error
		if out, err = d.querySpans(ctx, stmt+")"+treeSpanOrder, append(args, caps.TreeSpan+1)...); err != nil {
			return nil, err
		}
		others = others[n:]
		merged = len(others) > 0
	} else {
		merged = len(others) > idChunk(others)
	}
	for rest := others; len(rest) > 0; {
		k := idChunk(rest)
		more, err := d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE RunId IN ("+placeholders(k)+")"+
			treeSpanOrder, append(anys(rest[:k]), caps.TreeSpan+1)...)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
		rest = rest[k:]
	}
	if merged {
		slices.SortStableFunc(out, func(a, b obsdb.Span) int {
			if c := a.Start.Compare(b.Start); c != 0 {
				return c
			}
			return strings.Compare(a.SpanID, b.SpanID)
		})
		out = out[:min(len(out), caps.TreeSpan+1)]
	}
	return out, nil
}

// traceSpans is a trace's spans in start order, one row per span.
const traceSpans = "SELECT " + spanColumns + " FROM otel_traces WHERE TraceId = ?" + spanDedup

// idChunk is how many of ids one statement binds: the ids bind as
// literals into the query text, which the server caps (max_query_size,
// 256 KiB by default) — fillCounts' rule, at most 1000 ids and 64 KiB.
func idChunk(ids []string) int {
	n, size := 0, 0
	for n < len(ids) && n < 1000 && (n == 0 || size+len(ids[n]) < 64<<10) {
		size += len(ids[n])
		n++
	}
	return n
}

// ChildRuns is obsdb.TreeReader's: the parents' children, merged rows
// (FINAL) narrowed by primary key to the runs some row of which names
// one of the parents — idx_parent_run (0020) skips the granules none
// does — one statement per idChunk of parent ids, grouped in Go; the
// counts filled as Run's children are. Each statement reads at most
// what is left of obsdb.ChildRunsCap and one more, the one more
// answering obsdb.ErrChildRunsCapped.
func (d *DB) ChildRuns(ctx context.Context, parentIDs []string) (_ map[string][]obsdb.RunRow, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	out := map[string][]obsdb.RunRow{}
	now := time.Now()
	left := caps.ChildRuns
	for ids := parentIDs; len(ids) > 0; {
		n := idChunk(ids)
		args := make([]any, 0, 2*n+1)
		for _, id := range ids[:n] {
			args = append(args, id)
		}
		args = append(args, args...)
		args = append(args, left+1)
		rows, err := d.queryRunRows(ctx, now, childRunsSQL(n)+" LIMIT ?", args...)
		if err != nil {
			return nil, err
		}
		if len(rows) > left {
			return nil, obsdb.ErrChildRunsCapped
		}
		left -= len(rows)
		if err := d.fillCounts(ctx, rowPtrs(rows)); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ParentRunID] = append(out[r.ParentRunID], r)
		}
		ids = ids[n:]
	}
	return out, nil
}

// childRunsSQL is ChildRuns' statement for n parent ids (bound twice:
// the narrowing, then the merged rows' equality), in
// RunDetail.Children's order.
func childRunsSQL(n int) string {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return "SELECT " + runColumns + " FROM weft_runs FINAL WHERE " +
		"RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.ParentRunID IN (" + placeholders + ")) " +
		"AND ParentRunID IN (" + placeholders + ") AND ParentRunID != '' ORDER BY Started, RunId"
}

// RunSpans' two statements: by the run row's trace and run id, and by
// run id alone.
const (
	runSpansByTrace = "SELECT " + spanColumns + " FROM otel_traces WHERE TraceId = ? AND RunId = ?" + spanDedup
	runSpansByRun   = "SELECT " + spanColumns + " FROM otel_traces WHERE RunId = ?" + spanDedup
)

// runTrace is runExists answering the run row's trace id too ("" when
// the row names none). ErrNotFound for a run with no row.
func (d *DB) runTrace(ctx context.Context, runID string) (string, error) {
	var n uint64
	var trace string
	if err := d.conn.QueryRow(ctx,
		`SELECT count(), max(TraceID) FROM weft_runs WHERE RunId = ?`, runID).Scan(&n, &trace); err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("%w: run %s", obsdb.ErrNotFound, runID)
	}
	return trace, nil
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

// traceColumns is Traces' outer select over traceInner's groups.
const traceColumns = `TraceId, StartNs, EndNs, SpanCount, Failed, HasRoot, RootSpanId, RootName, Svc, RunIds`

// Traces groups otel_traces by TraceId (obsdb.DB.Traces), every column
// an aggregate over the trace's span rows — idempotent under a retried
// batch's duplicate rows (uniqExact counts each SpanId once; argMin,
// min, max and groupUniqArray ignore a repeat) — and filters the groups,
// never the rows: a trace's Start is min(Timestamp) over all its spans.
// Times bind as integer nanoseconds (Runs' reason: a positional
// time.Time renders at Seconds scale).
func (d *DB) Traces(ctx context.Context, q obsdb.TraceQuery) (_ obsdb.TracePage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.TracePage{}, err
	}
	defer d.closedErr(&err)
	inner, args := traceInner(q)
	conds := []string{"1=1"}
	if q.Service != "" {
		conds = append(conds, "SvcHits > 0")
	}
	if q.PublicID != "" {
		conds = append(conds, "Foreign = 0")
	}
	if !q.Since.IsZero() {
		conds = append(conds, "StartNs >= ?")
		args = append(args, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		conds = append(conds, "StartNs < ?")
		args = append(args, q.Until.UnixNano())
	}
	where := strings.Join(conds, " AND ")
	var total uint64
	if err := d.conn.QueryRow(ctx, "SELECT count() FROM ("+inner+") WHERE "+where, args...).Scan(&total); err != nil {
		return obsdb.TracePage{}, err
	}
	pageArgs := append([]any{}, args...)
	if !q.Before.IsZero() {
		ns := q.Before.UnixNano()
		if q.BeforeID != "" {
			where += " AND (StartNs < ? OR (StartNs = ? AND TraceId < ?))"
			pageArgs = append(pageArgs, ns, ns, q.BeforeID)
		} else {
			where += " AND StartNs < ?"
			pageArgs = append(pageArgs, ns)
		}
	}
	// WITH TIES, capped at MaxTies past the limit: Runs' paging rule.
	limit := obsdb.LimitOf(q.Limit)
	sql := "SELECT " + traceColumns + " FROM (SELECT * FROM (" + inner + ") WHERE " + where +
		" ORDER BY StartNs DESC LIMIT ? WITH TIES) ORDER BY StartNs DESC, TraceId DESC LIMIT ?"
	pageArgs = append(pageArgs, limit, limit+obsdb.MaxTies)
	rs, err := d.conn.Query(ctx, sql, pageArgs...)
	if err != nil {
		return obsdb.TracePage{}, err
	}
	defer func() { _ = rs.Close() }()
	page := obsdb.TracePage{Total: int(total)}
	for rs.Next() {
		var r obsdb.TraceRow
		var start, end int64
		var count uint64
		var failed, hasRoot uint8
		var rootID, rootName string
		if err := rs.Scan(&r.TraceID, &start, &end, &count, &failed, &hasRoot,
			&rootID, &rootName, &r.Service, &r.RunIDs); err != nil {
			return obsdb.TracePage{}, err
		}
		r.Start, r.End = timeOf(time.Unix(0, start)), timeOf(time.Unix(0, end))
		r.SpanCount, r.Error = int(count), failed == 1
		if hasRoot == 1 {
			r.Root = &obsdb.TraceRoot{SpanID: rootID, Name: rootName}
		}
		if len(r.RunIDs) == 0 {
			r.RunIDs = nil
		}
		page.Traces = append(page.Traces, r)
	}
	if err := rs.Err(); err != nil {
		return obsdb.TracePage{}, err
	}
	if len(page.Traces) >= limit {
		end := page.Traces[len(page.Traces)-1]
		page.NextBefore, page.NextBeforeID = &end.Start, end.TraceID
	}
	return page, nil
}

// traceInner is the grouped trace list with its bind arguments, in text
// order: the select list's two filter counts first, then the narrowing.
// Each filter also narrows the scan to the traces holding a span that
// could satisfy it (a superset: a trace that starts at or after Since
// has a span that does; one every span of which carries the public id
// has one that does), so a filtered list reads those traces by the
// TraceId bloom index instead of grouping the table. The aliases differ
// from every column name: ClickHouse aliases are query-global
// (runsInner's note).
func traceInner(q obsdb.TraceQuery) (string, []any) {
	const first = "(Timestamp, SpanId)"
	cols := []string{
		"TraceId",
		"min(toUnixTimestamp64Nano(Timestamp)) AS StartNs",
		"max(toUnixTimestamp64Nano(Timestamp) + toInt64(Duration)) AS EndNs",
		"uniqExact(SpanId) AS SpanCount",
		"countIf(StatusCode IN ('Error', 'STATUS_CODE_ERROR')) > 0 AS Failed",
		"countIf(ParentSpanId = '') > 0 AS HasRoot",
		"argMinIf(SpanId, " + first + ", ParentSpanId = '') AS RootSpanId",
		"toString(argMinIf(SpanName, " + first + ", ParentSpanId = '')) AS RootName",
		"toString(if(HasRoot, argMinIf(ServiceName, " + first + ", ParentSpanId = ''), argMin(ServiceName, " + first + "))) AS Svc",
		"arraySort(groupUniqArrayIf(toString(RunId), RunId != '')) AS RunIds",
	}
	var args, narrowArgs []any
	var narrow []string
	if q.Service != "" {
		cols = append(cols, "countIf(ServiceName = ?) AS SvcHits")
		args = append(args, q.Service)
		narrow = append(narrow, "TraceId IN (SELECT t.TraceId FROM otel_traces AS t WHERE t.ServiceName = ?)")
		narrowArgs = append(narrowArgs, q.Service)
	}
	if q.PublicID != "" {
		cols = append(cols, "countIf(PublicId != ?) AS Foreign")
		args = append(args, q.PublicID)
		narrow = append(narrow, "TraceId IN (SELECT t.TraceId FROM otel_traces AS t WHERE t.PublicId = ?)")
		narrowArgs = append(narrowArgs, q.PublicID)
	}
	if !q.Since.IsZero() {
		narrow = append(narrow, "TraceId IN (SELECT t.TraceId FROM otel_traces AS t WHERE t.Timestamp >= fromUnixTimestamp64Nano(toInt64(?)))")
		narrowArgs = append(narrowArgs, q.Since.UnixNano())
	}
	sql := "SELECT " + strings.Join(cols, ", ") + " FROM otel_traces"
	if len(narrow) > 0 {
		sql += " WHERE " + strings.Join(narrow, " AND ")
	}
	return sql + " GROUP BY TraceId", append(args, narrowArgs...)
}

// spanDedup makes the span reads idempotent on (trace, span) — the
// promise doc.go makes and obsdb/sqlite keeps with INSERT OR IGNORE.
// otel_traces is a plain MergeTree (a replacing key would collapse
// non-weft spans), so a retried batch's duplicates are removed at
// read: span rows are immutable by construction, so LIMIT 1 BY on the
// identity pair is exact and never hides a real second version.
const spanDedup = " ORDER BY Timestamp, SpanId LIMIT 1 BY TraceId, SpanId"

func (d *DB) querySpans(ctx context.Context, sql string, args ...any) ([]obsdb.Span, error) {
	rs, err := d.conn.Query(ctx, sql, args...)
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
	var having []string
	var hargs, runArgs []any // runArgs: the runs subquery's parameters
	var narrows []string
	var narrowArgs, condArgs []any
	cond := ""
	if q.PublicID != "" {
		// The scope is on the runs, before the GROUP BY (the SQLite
		// backend's rule, RunQuery.PublicID's): every aggregate, the
		// order, Total and the cursor are the public id's runs alone.
		// The narrow prunes the collapse by primary key, the cond keeps
		// the collapsed rows whose public id is it.
		narrows = append(narrows, "RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.PublicID = ?)")
		cond += " AND PublicID = ?"
		narrowArgs, condArgs = append(narrowArgs, q.PublicID), append(condArgs, q.PublicID)
	}
	if len(q.Parents) > 0 {
		// A delegated session's every turn carries its lineage (thread's
		// identityMetadata; the SessionParent column, 0023): the narrow
		// reads those runs by idx_session_parent before the collapse —
		// eq()'s rule in runsInner — the cond keeps the collapsed turns.
		// One IN lists a whole level of a delegation tree.
		in := " IN (" + placeholders(len(q.Parents)) + ")"
		narrows = append(narrows, "RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionParent"+in+")")
		cond += " AND SessionParent" + in
		for _, p := range q.Parents {
			narrowArgs, condArgs = append(narrowArgs, p), append(condArgs, p)
		}
	}
	if len(q.IDs) > 0 {
		// A known set of sessions (U15.2-fix): the narrow reads their
		// runs by idx_weft_session_id before the collapse, the cond
		// keeps the collapsed turns.
		in := " IN (" + placeholders(len(q.IDs)) + ")"
		narrows = append(narrows, "RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID"+in+")")
		cond += " AND SessionID" + in
		for _, id := range q.IDs {
			narrowArgs, condArgs = append(narrowArgs, id), append(condArgs, id)
		}
	}
	if len(q.Meta) > 0 {
		// The tag filter (U12.2) selects sessions, never turns: the
		// sessions one of whose own turns carries every pair (RunQuery.Meta's
		// subset match over the collapsed rows), every turn of each then
		// grouped. The narrow reads those sessions' runs by
		// idx_weft_session_id before the collapse; the cond holds the
		// collapsed rows to them.
		tagged := "SELECT SessionID FROM (SELECT RunId, max(SessionID) AS SessionID, max(Meta) AS Meta, max(PublicID) AS PublicID," +
			" max(ParentRunID) AS ParentRunID, max(CallerRunID) AS CallerRunID, max(Turn) AS Turn, max(Playground) AS Playground" +
			" FROM weft_runs GROUP BY RunId) WHERE SessionID != '' AND Playground = 0 AND " + ownTurnCH
		var targs []any
		if q.PublicID != "" {
			tagged += " AND PublicID = ?"
			targs = append(targs, q.PublicID)
		}
		for _, k := range slices.Sorted(maps.Keys(q.Meta)) {
			tagged += " AND JSONHas(Meta, ?) AND JSONExtractString(Meta, ?) = ?"
			targs = append(targs, k, k, q.Meta[k])
		}
		narrows = append(narrows, "RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID IN ("+tagged+"))")
		cond += " AND SessionID IN (" + tagged + ")"
		narrowArgs, condArgs = append(narrowArgs, targs...), append(condArgs, targs...)
	}
	narrow := ""
	if len(narrows) > 0 {
		narrow = " WHERE " + strings.Join(narrows, " AND ")
	}
	runArgs = append(narrowArgs, condArgs...)
	runs := sessionRuns(narrow, cond)
	hargs = append(hargs, runArgs...)
	if q.Agent != "" {
		having = append(having, "max(Agent) = ?")
		hargs = append(hargs, q.Agent)
	}
	if q.Q != "" {
		// The first turn's whole prompt, derived as Search derives it,
		// for the scoped sessions' first turns only; one derivation per
		// term (a term may match another field instead).
		pubArgs := runArgs // the runs subquery's parameters, as the FROM's
		cand := " AND RunId IN (SELECT argMin(RunId, (Turn, Started, RunId)) FROM " + runs + " GROUP BY SessionID)"
		for _, t := range strings.Fields(q.Q) {
			having = append(having, "(positionCaseInsensitiveUTF8(sTitle, ?) > 0"+
				" OR sFirstRun IN (SELECT RunId FROM ("+searchWindowed(cand)+") GROUP BY RunId HAVING positionCaseInsensitiveUTF8("+promptExpr+", ?) > 0)"+
				" OR positionCaseInsensitiveUTF8(SessionID, ?) > 0 OR positionCaseInsensitiveUTF8(sPublicID, ?) > 0"+
				" OR positionCaseInsensitiveUTF8(sAgent, ?) > 0)")
			hargs = append(append(append(hargs, t), pubArgs...), t, t, t, t)
		}
	}
	// The turn-state filters (U16): obsdb.SessionTurnFacts' rule over
	// the scoped, collapsed turns — the SQLite backend's conditions.
	if q.Failed {
		having = append(having, "countIf(Failed AND CancelCause != ?) > 0")
		hargs = append(hargs, obsdb.BusyCancelCause)
	}
	if q.Awaiting {
		// The newest turn (by Turn, Started, RunId) an open park:
		// succeeded, pending calls, no resumer (the parked filter's rule).
		const newest = "(Turn, Started, RunId)"
		having = append(having, "(argMax(Failed, "+newest+") = 0 AND argMax(greatest(FinishedOK, RootEnded), "+newest+") = 1"+
			" AND argMax(Pending, "+newest+") > 0"+
			" AND argMax(RunId, "+newest+") NOT IN (SELECT rb.ResumedFrom FROM weft_runs AS rb WHERE rb.ResumedFrom != ''))")
	}
	switch q.Lineage {
	case "":
	case obsdb.LineageChild:
		having = append(having, "max(SessionParent) != ''")
	case obsdb.LineageFork:
		having = append(having, "max(ForkedSession) != ''")
	default:
		having = append(having, "0")
	}
	if !q.Since.IsZero() {
		having = append(having, "toUnixTimestamp64Nano(max(LastSeen)) >= ?")
		hargs = append(hargs, q.Since.UnixNano())
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
		"SELECT count() FROM (SELECT SessionID, "+sessionAggs+" FROM "+runs+" GROUP BY SessionID"+havingSQL(having)+")", hargs...).Scan(&total); err != nil {
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
	rs, err := d.conn.Query(ctx, `SELECT * FROM (SELECT SessionID, `+sessionAggs+`
		FROM `+runs+` GROUP BY SessionID`+havingSQL(having)+
		` ORDER BY sLastSeen DESC LIMIT ? WITH TIES) ORDER BY sLastSeen DESC, SessionID DESC LIMIT ?`, append(pageArgs, limit, limit+obsdb.MaxTies)...)
	if err != nil {
		return obsdb.SessionPage{}, err
	}
	defer func() { _ = rs.Close() }()
	now := time.Now()
	page := obsdb.SessionPage{Total: int(total)}
	var firstRuns []*obsdb.RunRow
	for rs.Next() {
		var row obsdb.SessionRow
		var turns uint64
		var inTok, outTok, cached, cacheW, reason int64
		var ok, failed bool
		var newestSeen time.Time
		var first firstTurn
		if err := rs.Scan(append([]any{&row.ID, &row.PublicID, &row.Agent, &turns,
			&row.FirstSeen, &row.LastSeen, &inTok, &outTok, &cached, &cacheW, &reason,
			&ok, &failed, &newestSeen, &row.Title}, append(first.dest(), &row.ParentSession, &row.ParentCall, &row.ParentRun)...)...); err != nil {
			return obsdb.SessionPage{}, err
		}
		if row.ParentSession == "" { // lineage is a delegated session's alone
			row.ParentCall, row.ParentRun = "", ""
		}
		firstRuns = append(firstRuns, first.row(now)) // fillFirstPrompts reads the counts
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
	if err := d.fillFirstPrompts(ctx, page.Sessions, firstRuns); err != nil {
		return obsdb.SessionPage{}, err
	}
	if len(page.Sessions) >= limit {
		end := page.Sessions[len(page.Sessions)-1]
		page.NextBefore, page.NextBeforeID = &end.LastSeen, end.ID
	}
	if err := d.fillSummarizerUsage(ctx, page.Sessions, q.PublicID); err != nil {
		return obsdb.SessionPage{}, err
	}
	if err := d.fillTurnFacts(ctx, page.Sessions, q.PublicID, now); err != nil {
		return obsdb.SessionPage{}, err
	}
	return page, nil
}

// fillTurnFacts sets each session row's turn facts
// (obsdb.SessionTurnFacts) from its own turns — sessionRuns' scope,
// pub's turns when set — read as merged run rows (FINAL, narrowed by
// primary key through idx_weft_session_id) in one query for the page,
// with their ResumedBy; statuses on the page's clock (now). The SQLite
// backend's rule.
func (d *DB) fillTurnFacts(ctx context.Context, rows []obsdb.SessionRow, pub string, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]any, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	in := " IN (" + placeholders(len(ids)) + ")"
	args := append(append([]any{}, ids...), ids...)
	cond := ""
	if pub != "" {
		cond = " AND PublicID = ?"
		args = append(args, pub)
	}
	turns, err := d.queryRunRows(ctx, now,
		`SELECT `+runColumns+` FROM weft_runs FINAL
		WHERE RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID`+in+`)
			AND SessionID`+in+` AND SessionID != '' AND `+ownTurnCH+` AND Playground = 0`+cond, args...)
	if err != nil {
		return err
	}
	byID := make(map[string]*obsdb.RunRow, len(turns))
	runIDs := make([]string, 0, len(turns))
	for i := range turns {
		if _, seen := byID[turns[i].ID]; !seen {
			byID[turns[i].ID] = &turns[i]
			runIDs = append(runIDs, turns[i].ID)
		}
	}
	for len(runIDs) > 0 {
		n := idChunk(runIDs)
		if err := d.fillResumedByOf(ctx, runIDs[:n], byID); err != nil {
			return err
		}
		runIDs = runIDs[n:]
	}
	by := map[string][]obsdb.RunRow{}
	for _, t := range turns {
		by[t.SessionID] = append(by[t.SessionID], t)
	}
	for i := range rows {
		obsdb.SessionTurnFacts(&rows[i], by[rows[i].ID])
	}
	return nil
}

// fillSummarizerUsage sets each session row's SummarizerUsage: the
// summed usage of the summarizer chat spans (weft.chat.role summarizer)
// under the session's runs — RunId narrowed through weft_runs by
// session (and pub's runs when set, the rows' own scope), the spans
// read once each (spanDedup's key) — the SQLite backend's rule. One
// read for the page.
func (d *DB) fillSummarizerUsage(ctx context.Context, rows []obsdb.SessionRow, pub string) error {
	if len(rows) == 0 {
		return nil
	}
	args := make([]any, 0, len(rows)+1)
	for _, r := range rows {
		args = append(args, r.ID)
	}
	scope := ""
	if pub != "" {
		scope = " AND r.PublicID = ? AND r.PublicID != ''"
		args = append(args, pub)
	}
	tok := func(k string) string { return "sum(toInt64OrZero(SpanAttributes['" + k + "']))" }
	rs, err := d.conn.Query(ctx, `SELECT SpanAttributes['weft.session.id'] AS sid,
		SpanAttributes['gen_ai.provider.name'] AS provider, SpanAttributes['gen_ai.request.model'] AS model, `+
		tok("gen_ai.usage.input_tokens")+`, `+tok("gen_ai.usage.output_tokens")+`, `+
		tok("gen_ai.usage.cache_read.input_tokens")+`, `+tok("gen_ai.usage.cache_creation.input_tokens")+`, `+
		tok("gen_ai.usage.reasoning.output_tokens")+`
		FROM (SELECT TraceId, SpanId, SpanAttributes FROM otel_traces
			WHERE RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID IN (?`+strings.Repeat(",?", len(rows)-1)+`) AND r.SessionID != ''`+scope+`)
				AND SpanAttributes['`+obsdb.AttrChatRole+`'] = '`+obsdb.ChatRoleSummarizer+`'
			LIMIT 1 BY TraceId, SpanId)
		GROUP BY sid, provider, model`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	by := map[string][]obsdb.ModelUsage{}
	for rs.Next() {
		var id string
		var m obsdb.ModelUsage
		u := &m.Usage
		if err := rs.Scan(&id, &m.Provider, &m.Model, &u.InputTokens, &u.OutputTokens, &u.CachedInputTokens, &u.CacheWriteTokens, &u.ReasoningTokens); err != nil {
			return err
		}
		by[id] = append(by[id], m)
	}
	if err := rs.Err(); err != nil {
		return err
	}
	for i := range rows {
		rows[i].SummarizerUsage, rows[i].SummarizerModels = obsdb.SumModelUsage(by[rows[i].ID])
	}
	return nil
}

// sessionAggs is a session row's aggregates over sessionRuns' rows,
// every alias s-prefixed: the newest turn's status inputs and title
// (argMax on (Turn, Started), SQLite's ORDER BY ... LIMIT 1), and the
// first turn's run id (argMin), whose prompt and counts
// fillFirstPrompts reads, and that turn's Derived and status inputs
// (firstTurn derives its Status and Settling).
const sessionAggs = `max(PublicID) AS sPublicID, max(Agent) AS sAgent,
		uniqExact(RunId) AS sTurns, min(Started) AS sFirstSeen, max(LastSeen) AS sLastSeen,
		sum(InputTokens) AS sInputTokens, sum(OutputTokens) AS sOutputTokens,
		sum(CachedInputTokens) AS sCachedInputTokens, sum(CacheWriteTokens) AS sCacheWriteTokens,
		sum(ReasoningTokens) AS sReasoningTokens,
		argMax(greatest(FinishedOK, RootEnded), (Turn, Started)) AS sNewestOK, argMax(Failed, (Turn, Started)) AS sNewestFailed,
		argMax(LastSeen, (Turn, Started)) AS sNewestLastSeen,
		argMaxIf(Title, (Turn, Started), Title != '') AS sTitle,
		argMin(RunId, (Turn, Started, RunId)) AS sFirstRun,
		argMin(Derived, (Turn, Started, RunId)) AS sFirstDerived,
		argMin(Failed, (Turn, Started, RunId)) AS sFirstFailed,
		argMin(FinishedOK, (Turn, Started, RunId)) AS sFirstFinishedOK,
		argMin(RootEnded, (Turn, Started, RunId)) AS sFirstRootEnded,
		argMin(LastSeen, (Turn, Started, RunId)) AS sFirstLastSeen,
		max(SessionParent) AS sParent, max(SessionParentCall) AS sParentCall,
		argMin(if(CallerRunID != '', CallerRunID, ParentRunID), (Turn, Started, RunId)) AS sParentRun`

// firstTurn is a session row's first-turn aggregates (sessionAggs'
// sFirst* columns, in order).
type firstTurn struct {
	id                                   string
	derived, failed, finishedOK, rootEnd bool
	lastSeen                             time.Time
}

func (f *firstTurn) dest() []any {
	return []any{&f.id, &f.derived, &f.failed, &f.finishedOK, &f.rootEnd, &f.lastSeen}
}

// row is the first turn as a run row with its Status and Settling
// derived as the run scanner derives them (DeriveStatus,
// DeriveSettling); fillCounts adds its counts.
func (f *firstTurn) row(now time.Time) *obsdb.RunRow {
	seen := f.lastSeen.UTC()
	return &obsdb.RunRow{
		ID: f.id, Derived: f.derived, RootEnded: f.rootEnd, FinishRecorded: f.finishedOK, LastSeen: seen,
		Status:   obsdb.DeriveStatus(f.failed, f.finishedOK || f.rootEnd, seen, now),
		Settling: obsdb.DeriveSettling(f.rootEnd && !f.derived && !f.failed, f.finishedOK, seen, now),
	}
}

// fillFirstPrompts sets each row's FirstPrompt to its first turn's
// prompt preview (firsts[i] is rows[i]'s first turn) and ContentOff
// from that turn (obsdb.SessionContentOff) once fillCounts has given it
// the run rows' own counts; "" for a turn that has none.
func (d *DB) fillFirstPrompts(ctx context.Context, rows []obsdb.SessionRow, firsts []*obsdb.RunRow) error {
	if len(firsts) == 0 {
		return nil
	}
	if err := d.fillCounts(ctx, firsts); err != nil {
		return err
	}
	// fillCounts fills the first row of each id: read every row's from it.
	byID := make(map[string]*obsdb.RunRow, len(firsts))
	for _, r := range firsts {
		if _, seen := byID[r.ID]; !seen {
			byID[r.ID] = r
		}
	}
	for i := range rows {
		r := byID[firsts[i].ID]
		rows[i].FirstPrompt = r.PromptPreview
		rows[i].ContentOff = obsdb.SessionContentOff(*r)
	}
	return nil
}

// sessionRuns builds the per-run subquery both Sessions and Session
// group over: each run collapsed to its aggregates first, then scoped
// to the non-playground own turns of a session (D3, U14; ownTurnCH). The scope
// reads the collapsed row for the reason runsInner's filters do — a
// subagent run inherits its parent's session metadata and names its
// parent on run_start only, so a row-level top-level filter counted
// its later records as a turn of the session and summed its usage in a
// second time. narrow goes inside the collapse (Session narrows the
// scan to the one session's runs), cond onto the collapsed rows.
func sessionRuns(narrow, cond string) string {
	return `(SELECT * FROM (SELECT RunId, max(SessionID) AS SessionID, max(PublicID) AS PublicID,
				max(Agent) AS Agent, max(ParentRunID) AS ParentRunID, max(CallerRunID) AS CallerRunID,
				max(Playground) AS Playground,
				max(Turn) AS Turn, min(Started) AS Started, max(LastSeen) AS LastSeen,
				max(FinishedOK) AS FinishedOK, max(Failed) AS Failed, max(RootEnded) AS RootEnded,
				max(InputTokens) AS InputTokens, max(OutputTokens) AS OutputTokens,
				max(CachedInputTokens) AS CachedInputTokens, max(CacheWriteTokens) AS CacheWriteTokens,
				max(ReasoningTokens) AS ReasoningTokens, max(Derived) AS Derived,
				max(JSONExtractString(Meta, '` + obsdb.SessionTitleKey + `')) AS Title,
				max(SessionParent) AS SessionParent,
				max(JSONExtractString(Meta, 'weft.session.parent_call')) AS SessionParentCall,
				max(Pending) AS Pending, max(CancelCause) AS CancelCause,
				max(JSONExtractString(Meta, '` + obsdb.SessionForkedFromKey + `')) AS ForkedSession
			FROM weft_runs` + narrow + `
			GROUP BY RunId)
		WHERE ` + ownTurnCH + ` AND Playground = 0 AND SessionID != ''` + cond + `)`
}

// ownTurnCH is the own-turn rule over a collapsed run row (D3; the
// SQLite backend's ownTurn): its id is <SessionID>-t<Turn> — thread's
// id rule, so a turn a tool handler started (a thread/pool child
// session's turn) counts and a Subagent child never does — or it is
// linked to no run at all (no parent, no caller). A Generate inside a
// handler inherits the session id through ctx metadata and is neither.
const ownTurnCH = `(RunId = concat(SessionID, '-t', toString(Turn)) OR (ParentRunID = '' AND CallerRunID = ''))`

func (d *DB) Session(ctx context.Context, id string) (_ obsdb.SessionDetail, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionDetail{}, err
	}
	defer d.closedErr(&err)
	// The session's own grouped row, queried directly (the sqlite
	// backend's P2-1 fix): the old read scanned the newest 500
	// sessions, so an older session 404'd here while the list still
	// showed it.
	rs, err := d.conn.Query(ctx, `SELECT SessionID, `+sessionAggs+`
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
	var first firstTurn
	if err := rs.Scan(append([]any{&row.ID, &row.PublicID, &row.Agent, &turns,
		&row.FirstSeen, &row.LastSeen, &inTok, &outTok, &cached, &cacheW, &reason,
		&ok, &failed, &newestSeen, &row.Title}, append(first.dest(), &row.ParentSession, &row.ParentCall, &row.ParentRun)...)...); err != nil {
		return obsdb.SessionDetail{}, err
	}
	if row.ParentSession == "" { // lineage is a delegated session's alone
		row.ParentCall, row.ParentRun = "", ""
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
	one := []obsdb.SessionRow{row}
	if err := d.fillSummarizerUsage(ctx, one, ""); err != nil {
		return obsdb.SessionDetail{}, err
	}
	row = one[0]
	// Every turn (the SQLite backend's rule): a cap here kept the oldest
	// 500 and silently dropped the newest. A caller that wants pages
	// reads Runs with RunQuery.SessionID.
	runs, err := d.queryRunRows(ctx, now,
		`SELECT `+runColumns+` FROM weft_runs FINAL
		WHERE RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.SessionID = ?)
			AND SessionID = ? AND `+ownTurnCH+` AND Playground = 0
		ORDER BY Turn, Started, RunId`, id, id)
	if err != nil {
		return obsdb.SessionDetail{}, err
	}
	if err := d.fillCounts(ctx, rowPtrs(runs)); err != nil {
		return obsdb.SessionDetail{}, err
	}
	for _, r := range runs { // the first turn's preview, filled with the counts
		if r.ID == first.id {
			row.FirstPrompt = r.PromptPreview
			row.ContentOff = obsdb.SessionContentOff(r)
		}
	}
	obsdb.SessionTurnFacts(&row, runs) // ResumedBy filled with the counts
	return obsdb.SessionDetail{SessionRow: row, Runs: runs}, nil
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
	// The attempt span's retry-after ask and the experiment
	// fingerprint's fields, the same restatement of 0004.
	"weft.attempt.retry_after_ms",
	"weft.override.instructions", "weft.override.max_steps", "weft.override.model",
	"weft.override.parallelism", "weft.override.params", "weft.override.park_all_except",
	"weft.override.park_on", "weft.override.thinking", "weft.override.tool_choice",
	"weft.override.tools",
	// ADR 0028 §12: the raw record's position (migration 0005's
	// restatement of the logs view).
	"weft.raw.index",
	// U3.2: what ended the run, the run row's stopped_by and
	// cancel_cause columns (migration 0011's restatement of both views).
	"weft.run.stopped_by", "weft.cancel.cause",
	// U5: what the core emitted, the run row's events_emitted and
	// messages_emitted columns (migration 0013's restatement of the
	// traces view; span attributes only, so the logs view keeps 0011's).
	"weft.run.events", "weft.run.messages",
	// U5-fix: a destination's loss count on run_start / run_finish
	// (ingest health; migration 0014's restatement of the logs view).
	"weft.records.dropped",
	// U4: the run row's problem counters and the call's badges
	// (migration 0015's restatement of both views).
	"weft.run.denied", "weft.tool.truncated_bytes", "weft.tool.input_error",
	"weft.panic.layer",
	// U4-fix: a fallback layer's attempt, the fallbacks counter's key
	// (migration 0017's restatement of the traces view; a span
	// attribute only, so the logs view keeps 0015's).
	"weft.attempt.layer",
	// U32.1: a RETRY's count and budget, the run's observer panics
	// (migration 0018's restatement of both views).
	"weft.tool.retry", "weft.tool.retry_budget", "weft.tap.panics",
	// U13.1: the approval link — the run row's resumed_from, a resume's
	// decisions per parked call, a denied call's span marks (migration
	// 0019's restatement of both views).
	"weft.resumed_from", "weft.resume.calls", "weft.tool.denied", "weft.tool.denied_kind",
	// U11.1: the slog bridge's count — on the heartbeat (the logs view)
	// and a run's invoke_agent span (the traces view) — the logs route's
	// hint, never metadata (migration 0021's restatement of both views).
	"weft.logs.bridged",
	// U25: the cards' facts — attempt, tool and run span attributes
	// (migration 0022's restatement of both views).
	"weft.attempt.wait_ms", "http.response.status_code", "weft.tool.pending.source",
	"weft.tool.timeout_ms", "weft.tool.queue_ms", "weft.subagent.pending", "weft.output.submitted",
	// U14 (O11): the caller link of a run started on a tool call that is
	// no Subagent child — the run row's CallerRunID / CallerCallID
	// (migration 0023's restatement of both views).
	"weft.caller.run.id", "weft.caller.call.id",
	// U12.1 (audit O15): weft/thread's summarizer chat spans' role, a span
	// attribute (migration 0024's restatement of the traces view).
	"weft.chat.role",
	// UX-B-14: an approved call's provenance (core.ApproveBy), span
	// attributes on execute_tool (the same restatement).
	"weft.tool.approved_by", "weft.grant.id",
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
