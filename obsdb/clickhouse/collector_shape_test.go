package clickhouse_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
)

// The column lists below are verbatim from the OTel Collector
// ClickHouse exporter's insert templates at the pinned version
// v0.162.0 (exporter/clickhouseexporter/internal/sqltemplates/
// traces_insert.sql and logs_insert.sql, verified identical to main on
// 2026-10-01). If the exporter's insert shape changes, these strings —
// and the tables — change with it, in a new migration that names the
// new pin.
//
// The traces template's header is `INSERT INTO %q.%q (...)`: the
// collector fmt-renders it with the database and table name, so this
// test renders it the same way. The logs template is
// `{{ident .Database}}.{{ident .TableName}}` with a
// {{.FeatureColumnNames}}/{{.FeatureColumnPositions}} pair the exporter
// fills after probing the table with DESC TABLE: EventName is probed
// (ours exists, so it is included — exporter_logs.go's
// logsColumnEventName), the *AttributesKeys columns are probed (ours
// do not exist, so a pinned collector omits them, exactly as it does
// for its own older tables). The values follow the append order of
// exporter_traces.go and exporter_logs.go at the same tag.

const collectorTracesInsert = `INSERT INTO %q.%q (
    Timestamp,
    TraceId,
    SpanId,
    ParentSpanId,
    TraceState,
    SpanName,
    SpanKind,
    ServiceName,
    ResourceAttributes,
    ScopeName,
    ScopeVersion,
    SpanAttributes,
    Duration,
    StatusCode,
    StatusMessage,
    Events.Timestamp,
    Events.Name,
    Events.Attributes,
    Links.TraceId,
    Links.SpanId,
    Links.TraceState,
    Links.Attributes
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?
)`

const collectorLogsInsert = `INSERT INTO ` + "`%s`" + `.` + "`%s`" + ` (
    Timestamp,
    TraceId,
    SpanId,
    TraceFlags,
    SeverityText,
    SeverityNumber,
    ServiceName,
    Body,
    ResourceSchemaUrl,
    ResourceAttributes,
    ScopeSchemaUrl,
    ScopeName,
    ScopeVersion,
    ScopeAttributes,
    LogAttributes,
    EventName
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?
)`

// TestCollectorShapeInserts pins S3.6's compatibility promise: a stock
// collector pinned to v0.162.0, pointed at this database with
// create_schema: false, writes spans and logs whose INSERTs land — and
// land in the weft tables too, because the views read the very columns
// the collector fills.
func TestCollectorShapeInserts(t *testing.T) {
	db, dsn := openFresh(t)
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	database := parseDB(t, dsn)

	at := time.Unix(0, 1790845923120000000).UTC()

	// A span shaped the way a collector forwards a weft run's
	// invoke_agent span (attribute values are strings — the collector's
	// map columns stringify, and the views parse them back).
	spanBatch, err := conn.PrepareBatch(ctx,
		fmt.Sprintf(collectorTracesInsert, database, "otel_traces"))
	if err != nil {
		t.Fatalf("collector-shaped traces insert rejected by our tables: %v", err)
	}
	if err := spanBatch.Append(
		at, "0102030405060708090a0b0c0d0e0f10", "0a0b0c0d0e0f0102", "", "",
		"invoke_agent conf", "SPAN_KIND_INTERNAL", "conf-svc",
		map[string]string{"service.name": "conf-svc"}, "", "",
		map[string]string{
			"gen_ai.operation.name": "invoke_agent", "weft.run.id": "collector",
			"gen_ai.agent.name": "conf", "weft.session.id": "s_col",
			"weft.turn": "1", "gen_ai.usage.input_tokens": "42",
		},
		uint64(9000000000), "STATUS_CODE_OK", "",
		[]time.Time{at, at.Add(time.Second)},
		[]string{"gen_ai.content.prompt", "exception"},
		[]map[string]string{{"gen_ai.system.prompt": "short"}, {"exception.type": "RuntimeError"}},
		[]string{}, []string{}, []string{}, []map[string]string{},
	); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := spanBatch.Send(); err != nil {
		t.Fatalf("collector-shaped traces insert failed: %v", err)
	}

	// A log record the same collector forwards: a weft run_start, with
	// EventName — the feature column the pinned exporter probes for and
	// fills because ours has it.
	logBatch, err := conn.PrepareBatch(ctx,
		fmt.Sprintf(collectorLogsInsert, database, "otel_logs"))
	if err != nil {
		t.Fatalf("collector-shaped logs insert rejected by our tables: %v", err)
	}
	if err := logBatch.Append(
		at, "0102030405060708090a0b0c0d0e0f10", "0102030405060708", uint8(0),
		"SEVERE", uint8(9), "conf-svc",
		`{"type":"run_start","id":"collector"}`, "",
		map[string]string{"service.name": "conf-svc"},
		"", "", "", map[string]string{},
		map[string]string{
			"weft.record": "event", "weft.run.id": "collector",
			"weft.event.type": "run_start", "weft.event.pos": "0",
			"weft.session.id": "s_col", "gen_ai.agent.name": "conf",
		},
		"weft.event",
	); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := logBatch.Send(); err != nil {
		t.Fatalf("collector-shaped logs insert failed: %v", err)
	}

	// The rows landed, and the materialized views turned the
	// collector-shaped rows into the weft tables' rows — a stock
	// collector feeds Studio.
	var spans, logs uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM otel_traces").Scan(&spans); err != nil || spans != 1 {
		t.Fatalf("otel_traces rows = %d, err %v", spans, err)
	}
	if err := conn.QueryRow(ctx, "SELECT count() FROM otel_logs").Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("otel_logs rows = %d, err %v", logs, err)
	}
	var started time.Time
	var inputTokens int64
	if err := conn.QueryRow(ctx,
		"SELECT Started, InputTokens FROM weft_runs FINAL WHERE RunId = 'collector'").
		Scan(&started, &inputTokens); err != nil {
		t.Fatalf("weft_runs from collector-shaped rows: %v", err)
	}
	if !started.Equal(at) || inputTokens != 42 {
		t.Errorf("collector-fed run: started %v usage %d, want %v / 42", started, inputTokens, at)
	}
	var body string
	if err := conn.QueryRow(ctx,
		"SELECT Body FROM weft_records FINAL WHERE RunId = 'collector' AND Kind = 'event'").
		Scan(&body); err != nil {
		t.Fatalf("weft_records from collector-shaped rows: %v", err)
	}
	if body != `{"type":"run_start","id":"collector"}` {
		t.Errorf("collector-fed record body = %q", body)
	}

	// The collector-written span reads back through the same API, its
	// events from the nested arrays (WeftEvents is '' for a collector
	// row, so the read takes the fallback path) with string attribute
	// values — what that schema actually stored.
	trace, err := db.Trace(ctx, "0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatalf("trace read of collector rows: %v", err)
	}
	if len(trace) != 1 || len(trace[0].Events) != 2 {
		t.Fatalf("collector span = %d spans / %d events, want 1/2", len(trace), len(trace[0].Events))
	}
	if p := trace[0].Events[0].Attrs["gen_ai.system.prompt"]; p != "short" {
		t.Errorf("collector event attr = %#v, want the stored string %q", p, "short")
	}
	if et := trace[0].Events[1].Attrs["exception.type"]; et != "RuntimeError" {
		t.Errorf("collector exception.type = %#v", et)
	}
}

func parseDB(t *testing.T, dsn string) string {
	t.Helper()
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Auth.Database == "" {
		t.Fatalf("dsn %q: no database", dsn)
	}
	return opts.Auth.Database
}
