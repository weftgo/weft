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
