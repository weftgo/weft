package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// Write stores one batch: spans into otel_traces and records into
// otel_logs — both in the collector's own column shape, so the row a
// stock collector would have written and the row weft writes are the
// same shape (the materialized views cannot tell them apart). Deltas
// ride otel_logs (the runs view counts them; the records view filters
// them) and, with KeepDeltas, are additionally inserted into
// weft_deltas directly — the option lives in Write, not the schema,
// because a view cannot be option-gated.
//
// Idempotence (I4) is the engines' job: otel_logs tolerates duplicate
// rows, weft_records' ReplacingMergeTree collapses the (RunId, Kind,
// Pos) duplicates at merge (exact reads use FINAL), and weft_runs'
// min/max aggregates are unchanged by identical values, so a retried
// transport's duplicate is a no-op once merged and never inflates a
// count at read time (uniqExact over the key dedups pre-merge).
//
// A stock GenAI span (no weft.run.id, obsdb.StockCandidate) re-derives
// its trace's derived runs after the spans are stored (deriveStock,
// docs/record-contract.md §9): no view can, migration 0009 says why.
func (d *DB) Write(ctx context.Context, b obsdb.Batch) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if len(b.Spans) == 0 && len(b.Records) == 0 {
		return nil
	}
	// The tables partition by day, and the server refuses an insert
	// block that spans more than 100 partitions by default: one batch
	// carrying a backfill or a sender's broken clock failed whole, its
	// sound rows lost with it and a retry failing the same way. The
	// limit guards against a mis-chosen partition key; this one is
	// fixed, so the batch is taken as it is.
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"max_partitions_per_insert_block": 0}))
	if len(b.Spans) > 0 {
		if err := d.insertSpans(ctx, b.Spans); err != nil {
			return err
		}
		if err := d.deriveStock(ctx, b.Spans); err != nil {
			return err
		}
	}
	if len(b.Records) > 0 {
		if err := d.insertRecords(ctx, b.Records); err != nil {
			return err
		}
	}
	if d.keepDeltas {
		if err := d.insertDeltas(ctx, b.Records); err != nil {
			return err
		}
	}
	return nil
}

// deriveStock re-derives the runs of every trace the spans bring a
// stock GenAI span of (obsdb.StockCandidate): the trace's stored spans
// — the batch's own included, inserted just before — through
// obsdb.DeriveStockRuns, each run's row inserted into weft_runs under
// its id with Derived = 1. The table's max aggregates fold the
// derivations together; re-derived from more spans a row's steps,
// usage and last-seen only grow (DeriveStockRuns counts a span only
// through a complete chain of stored parents), so the merged row is
// the latest, most complete one. Provider and model are the exception
// a fold cannot order: a run whose root names none takes its first chat
// span's, and should an earlier chat span arrive in a later batch
// naming another model, max keeps the larger name, where SQLite's
// overwrite keeps the earlier span's.
func (d *DB) deriveStock(ctx context.Context, spans []obsdb.Span) error {
	var traces []string
	seen := map[string]bool{}
	for _, s := range spans {
		if obsdb.StockCandidate(s) && !seen[s.TraceID] {
			seen[s.TraceID] = true
			traces = append(traces, s.TraceID)
		}
	}
	_, err := d.rederiveTraces(ctx, traces)
	return err
}

// rederiveTraces re-derives each trace's runs from its stored spans
// (deriveStock's body, and migration 0031's Go step). Per trace, in this
// order — so the last writer of a trace sees everything (two batches, or
// two Studios over one database, racing on it):
//
//  1. read the spans, derive, insert every run's row (MessageCount into
//     DerivedMessages);
//  2. read the spans again — another writer's spans inserted before its
//     own derivation are visible now — and derive again;
//  3. reconcile (reconcileDerived): the rows that derivation no longer
//     makes are deleted, and a row whose stored parent is not the one it
//     derives (a parent dropped in 3, the max aggregate keeping its id)
//     is deleted and inserted again whole.
//
// A writer that derived before another's spans arrived thus drops its
// own interim rows after inserting them, and the other, deriving after
// its spans, sees them; 3 costs a key-range read per trace and a delete
// only when a row is stale. removed answers the dropped rows (the live
// frame's Removed rows).
func (d *DB) rederiveTraces(ctx context.Context, traces []string) (removed []obsdb.RunRow, err error) {
	for _, t := range traces {
		stored, err := d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE TraceId = ?"+spanDedup, t)
		if err != nil {
			return nil, err
		}
		runs := obsdb.DeriveStockRuns(stored)
		if deriveHook != nil {
			deriveHook(t)
		}
		if err := d.insertDerived(ctx, rowsOf(runs)); err != nil {
			return nil, err
		}
		fresh, err := d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE TraceId = ?"+spanDedup, t)
		if err != nil {
			return nil, err
		}
		if reconcileHook != nil {
			reconcileHook(t)
		}
		gone, err := d.reconcileDerived(ctx, t, fresh, obsdb.DeriveStockRuns(fresh))
		if err != nil {
			return nil, err
		}
		removed = append(removed, gone...)
	}
	return removed, nil
}

// deriveHook, when set (tests), runs between a trace's first derivation
// and its rows' insert: where another writer may interleave.
var deriveHook func(trace string)

// reconcileHook, when set (tests), runs between a trace's fresh read and
// its reconcile: the other place another writer may interleave.
var reconcileHook func(trace string)

func rowsOf(runs []obsdb.StockRun) []obsdb.RunRow {
	out := make([]obsdb.RunRow, len(runs))
	for i, r := range runs {
		out[i] = r.Row
	}
	return out
}

// insertDerived inserts derived rows into weft_runs (Derived = 1, the
// max aggregates folding them with the rows already there).
func (d *DB) insertDerived(ctx context.Context, rows []obsdb.RunRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := d.conn.PrepareBatch(ctx, `INSERT INTO weft_runs (
		RunId, ParentRunID, ParentCallID, TraceID, Agent, Provider, Model, Service, SessionID,
		Started, Finished, LastSeen, FinishedOK, Failed, Err, Steps,
		InputTokens, OutputTokens, CachedInputTokens, CacheWriteTokens, ReasoningTokens, Derived, RootEnded,
		DerivedMessages)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(
			r.ID, r.ParentRunID, r.ParentCallID, r.TraceID, r.Agent, r.Provider, r.Model, r.Service, r.SessionID,
			r.Started, r.Finished, r.LastSeen,
			boolUInt8(r.Status == obsdb.StatusSucceeded), boolUInt8(r.Status == obsdb.StatusFailed), r.Err, int32(r.Steps),
			r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CachedInputTokens,
			r.Usage.CacheWriteTokens, r.Usage.ReasoningTokens, uint8(1), uint8(1), r.MessageCount,
		); err != nil {
			return err
		}
	}
	return batch.Send()
}

// reconcileSelect is reconcileDerived's read of a trace's derived rows,
// by id prefix (a primary-key range).
const reconcileSelect = `SELECT RunId, max(ParentRunID), max(Agent), max(SessionID) FROM weft_runs
	WHERE startsWith(RunId, ?) AND Derived = 1 GROUP BY RunId`

// reconcileDerived brings the trace's stored derived rows to runs, the
// latest derivation (§9). The rows are read by their id prefix
// (obsdb.StockRunPrefix: weft_runs' primary key, a key range — never a
// scan for TraceID). A stored row runs no longer has is stale: an agent
// without a conversation id that was top-level while its parent was not
// stored, folded now — dropped only when its root span is in this read
// (a run whose root this read missed is another writer's, whose spans
// landed after the read: never this writer's to drop). What this read
// drops it drops rightly whatever came later: a span with an agent
// stored above it stays folded as more spans arrive. A row whose stored
// (max-aggregated) ParentRunID is a run this read knows folded (its root
// span here, not a root) — a child re-parented when its interim parent
// folded, whose max keeps the dropped id when it sorts higher — must be
// rewritten, which an insert cannot do under max: both are deleted (one
// lightweight DELETE, guarded by Derived = 1, only when one is due —
// never in the steady state), then the rewritten rows inserted whole from
// a read of the trace after the DELETE (so a larger row another writer
// inserted before it is derived again, not lost).
// The DELETE waits for its mutation (the server's default
// lightweight_deletes_sync): it applies to the parts that exist when it
// is issued, so the re-insert after it is never deleted either way, but
// a read between an asynchronous delete and its completion would fold
// the old row into the new one's max. The stale rows come back Removed.
func (d *DB) reconcileDerived(ctx context.Context, trace string, spans []obsdb.Span, runs []obsdb.StockRun) ([]obsdb.RunRow, error) {
	want := make(map[string]obsdb.RunRow, len(runs))
	for _, r := range runs {
		want[r.Row.ID] = r.Row
	}
	seen := make(map[string]bool, len(spans))
	for _, sp := range spans {
		seen[sp.SpanID] = true
	}
	// staleHere: a derived id whose root span this read holds and whose
	// run this derivation does not make.
	staleHere := func(id string) bool {
		_, root, ok := obsdb.ParseStockRunID(id)
		_, made := want[id]
		return ok && seen[root] && !made
	}
	rs, err := d.conn.Query(ctx, reconcileSelect, obsdb.StockRunPrefix(trace))
	if err != nil {
		return nil, err
	}
	var drop []string
	var gone, rewrite []obsdb.RunRow
	for rs.Next() {
		var id, parent, agent, session string
		if err := rs.Scan(&id, &parent, &agent, &session); err != nil {
			_ = rs.Close()
			return nil, err
		}
		r, ok := want[id]
		_, root, _ := obsdb.ParseStockRunID(id)
		switch {
		case !ok && !seen[root]:
			// Its root span is not in this read: another writer's run
			// whose spans landed after it — never this writer's to drop.
		case !ok:
			drop = append(drop, id)
			gone = append(gone, obsdb.RunRow{ID: id, TraceID: trace, Agent: agent, SessionID: session, Derived: true, Removed: true})
		case parent != r.ParentRunID && staleHere(parent):
			// Its stored parent is a run this read knows folded (its
			// root span seen, no longer a root): the max kept a dropped
			// id. A parent this read does not know is a newer writer's —
			// left alone.
			drop = append(drop, id)
			rewrite = append(rewrite, r)
		}
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	if len(drop) == 0 {
		return nil, nil
	}
	if err := d.conn.Exec(ctx, `DELETE FROM weft_runs WHERE RunId IN ? AND Derived = 1`, drop); err != nil {
		return nil, err
	}
	if len(rewrite) == 0 {
		return gone, nil
	}
	// The rewritten rows come from a read after the DELETE, never from
	// want: another writer may have inserted a larger row for one of them
	// between this writer's SELECT and its DELETE, which the DELETE
	// removed. That row's spans were stored before it — this read holds
	// them — and any row inserted after the DELETE survives it.
	after, err := d.querySpans(ctx, "SELECT "+spanColumns+" FROM otel_traces WHERE TraceId = ?"+spanDedup, trace)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(rewrite))
	for _, r := range rewrite {
		ids[r.ID] = true
	}
	var rows []obsdb.RunRow
	for _, r := range obsdb.DeriveStockRuns(after) {
		if ids[r.Row.ID] {
			rows = append(rows, r.Row)
		}
	}
	return gone, d.insertDerived(ctx, rows)
}

func boolUInt8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// insertSpans writes spans with the collector's traces_insert.sql
// column list plus the three weft JSON columns that keep attribute
// types exact (the map columns stringify values, which would lose the
// int64 the record contract pins).
func (d *DB) insertSpans(ctx context.Context, spans []obsdb.Span) error {
	batch, err := d.conn.PrepareBatch(ctx, `INSERT INTO otel_traces (
		Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind,
		ServiceName, ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes,
		Duration, StatusCode, StatusMessage,
		Events.Timestamp, Events.Name, Events.Attributes,
		Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes,
		WeftAttrs, WeftEvents, WeftResource)`)
	if err != nil {
		return err
	}
	for _, s := range spans {
		eventsT := make([]time.Time, len(s.Events))
		eventsN := make([]string, len(s.Events))
		eventsA := make([]map[string]string, len(s.Events))
		for i, e := range s.Events {
			eventsT[i] = e.Time
			eventsN[i] = e.Name
			eventsA[i] = stringAttrs(e.Attrs)
		}
		if err := batch.Append(
			s.Start, s.TraceID, s.SpanID, s.ParentSpanID, "",
			s.Name, spanKindName(s.Kind), s.Service,
			stringAttrs(s.Resource), "", "",
			stringAttrs(s.Attrs),
			spanDuration(s.Start, s.End),
			statusCodeName(s.StatusCode), s.StatusMessage,
			eventsT, eventsN, eventsA,
			[]string{}, []string{}, []string{}, []map[string]string{},
			jsonOrNull(s.Attrs), eventsJSON(s.Events), jsonOrNull(s.Resource),
		); err != nil {
			return err
		}
	}
	return batch.Send()
}

// insertRecords writes every record — weft and non-weft — with the
// collector's logs_insert.sql column list plus EventName, which the
// pinned exporter fills when the column exists (it detects it via
// DESC TABLE; ours does).
func (d *DB) insertRecords(ctx context.Context, records []obsdb.Record) error {
	batch, err := d.conn.PrepareBatch(ctx, `INSERT INTO otel_logs (
		Timestamp, TraceId, SpanId, TraceFlags, SeverityText, SeverityNumber,
		ServiceName, Body, ResourceSchemaUrl, ResourceAttributes,
		ScopeSchemaUrl, ScopeName, ScopeVersion, ScopeAttributes,
		LogAttributes, EventName)`)
	if err != nil {
		return err
	}
	for _, r := range records {
		if err := batch.Append(
			recordTime(r), r.TraceID, r.SpanID, uint8(0), "", severityNumber(r.Severity),
			r.Service, r.Body, "",
			stringAttrs(r.Resource),
			"", "", "", map[string]string{},
			stringAttrs(r.Attrs), r.EventName,
		); err != nil {
			return err
		}
	}
	return batch.Send()
}

// insertDeltas is the KeepDeltas path: delta rows straight into
// weft_deltas, keyed by their own counter (D3) so they can never touch
// the durable sequence.
func (d *DB) insertDeltas(ctx context.Context, records []obsdb.Record) error {
	var deltas []obsdb.Record
	for _, r := range records {
		if obsdb.DeriveRecord(r).Record == "delta" {
			deltas = append(deltas, r)
		}
	}
	if len(deltas) == 0 {
		return nil
	}
	batch, err := d.conn.PrepareBatch(ctx, `INSERT INTO weft_deltas (
		RunId, Pos, Time, TraceId, SpanId, EventType, Body)`)
	if err != nil {
		return err
	}
	for _, r := range deltas {
		w := obsdb.DeriveRecord(r)
		if err := batch.Append(w.RunID, w.Pos, recordTime(r), r.TraceID, r.SpanID, w.EventType, r.Body); err != nil {
			return err
		}
	}
	return batch.Send()
}

// recordTime is the time a record is stored at: its own, or — OTLP's
// rule for a zero ("unknown") Time — the observed one (the SQLite
// backend reads it the same way).
func recordTime(r obsdb.Record) time.Time {
	if r.Time.IsZero() {
		return r.Observed
	}
	return r.Time
}

// spanDuration returns end-start in nanoseconds, floored at zero for a
// malformed span (the collector writes the same UInt64).
func spanDuration(start, end time.Time) uint64 {
	if ns := end.Sub(start); ns > 0 {
		return uint64(ns)
	}
	return 0
}

// severityNumber clamps an OTLP severity (0-24) into the column's UInt8.
func severityNumber(sev int) uint8 {
	switch {
	case sev < 0:
		return 0
	case sev > 24:
		return 24
	default:
		return uint8(sev)
	}
}

// stringAttrs flattens an attribute map into the collector's
// Map(LowCardinality(String), String) shape: strings stay, scalars get
// their decimal or JSON spelling — the same lossy pass the collector
// itself applies. The materialized views read only weft's scalar keys,
// which are strings by contract.
func stringAttrs(m map[string]any) map[string]string {
	if len(m) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = attrString(v)
	}
	return out
}

func attrString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// jsonOrNull marshals the typed map for the weft JSON columns; a nil or
// empty map becomes ” (the default a stock collector's insert leaves),
// and reads treat ” as "fall back to the map columns".
func jsonOrNull(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, err := marshalColumn(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// marshalColumn encodes a weft JSON column. JSON has no spelling for NaN
// or an infinite double, yet both are legal OTLP attribute values: one
// of them must not cost the row its typed column (the read would fall
// back to the stringified map, every int64 attribute a string). They are
// stored under the names the protobuf JSON mapping gives them — the
// SQLite backend's rule, so both backends read the same values back.
func marshalColumn(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	var unsupported *json.UnsupportedValueError
	if errors.As(err, &unsupported) {
		return json.Marshal(finite(v))
	}
	return b, err
}

// finite returns v with every non-finite double replaced by its name,
// copied, never modified in place — the batch is the caller's.
func finite(v any) any {
	switch x := v.(type) {
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = finite(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = finite(e)
		}
		return out
	}
	return v
}

// eventsJSON marshals span events with typed attributes.
func eventsJSON(events []obsdb.SpanEvent) string {
	if len(events) == 0 {
		return ""
	}
	type wireEvent struct {
		Time  time.Time      `json:"Time"`
		Name  string         `json:"Name"`
		Attrs map[string]any `json:"Attrs,omitempty"`
	}
	wire := make([]wireEvent, len(events))
	for i, e := range events {
		wire[i] = wireEvent{Time: e.Time, Name: e.Name, Attrs: e.Attrs}
	}
	b, err := json.Marshal(wire)
	var unsupported *json.UnsupportedValueError
	if errors.As(err, &unsupported) {
		for i := range wire {
			wire[i].Attrs, _ = finite(wire[i].Attrs).(map[string]any)
		}
		b, err = json.Marshal(wire)
	}
	if err != nil {
		return ""
	}
	return string(b)
}

// The span kind and status spellings the pinned exporter writes:
// v0.162.0's exporter_traces.go appends span.Kind().String() and
// spanStatus.Code().String(), which are pdata's names — "Internal",
// "Error" — not the OTLP proto value names (SPAN_KIND_INTERNAL,
// STATUS_CODE_ERROR). Writes use the exporter's spelling, so a row is
// the same shape whoever wrote it; reads accept both, because rows
// written before migration 0003 (and by older exporters) carry the
// proto names. Tests pin the round trip and both read spellings.

func spanKindName(kind int) string {
	switch kind {
	case 1:
		return "Internal"
	case 2:
		return "Server"
	case 3:
		return "Client"
	case 4:
		return "Producer"
	case 5:
		return "Consumer"
	default:
		return "Unspecified"
	}
}

func spanKindInt(name string) int {
	switch name {
	case "Internal", "SPAN_KIND_INTERNAL":
		return 1
	case "Server", "SPAN_KIND_SERVER":
		return 2
	case "Client", "SPAN_KIND_CLIENT":
		return 3
	case "Producer", "SPAN_KIND_PRODUCER":
		return 4
	case "Consumer", "SPAN_KIND_CONSUMER":
		return 5
	default:
		return 0
	}
}

func statusCodeName(code int) string {
	switch code {
	case 1:
		return "Ok"
	case 2:
		return "Error"
	default:
		return "Unset"
	}
}

func statusCodeInt(name string) int {
	switch name {
	case "Ok", "STATUS_CODE_OK":
		return 1
	case "Error", "STATUS_CODE_ERROR":
		return 2
	default:
		return 0
	}
}
