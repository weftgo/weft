package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// freshDB opens Open on a database of its own on the live server named
// by WEFT_CLICKHOUSE_DSN (skipping without one), dropped at cleanup.
func freshDB(t *testing.T) *DB {
	t.Helper()
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the one-line container recipe)")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_rs_" + hex.EncodeToString(suffix)
	admin := rawConn(t, base)
	if err := admin.Exec(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
		_ = admin.Close()
	})
	u.Path = "/" + name
	db, err := Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.(*DB)
}

// TestRunSpansUseTheTraceAndRunIndexes (U27; UX-E-35): a run's spans
// are read with `TraceId = ? AND RunId = ?` when the run row names its
// trace — idx_trace_id (0001) skips the granules — and with `RunId = ?`
// otherwise, which idx_weft_run_id (0016) serves; EXPLAIN indexes = 1
// names each index on its statement. A run row whose trace is not its
// spans' (a record carried another trace) falls back to RunId alone and
// still answers its spans.
func TestRunSpansUseTheTraceAndRunIndexes(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()

	var idx uint64
	if err := db.conn.QueryRow(ctx, `SELECT count() FROM system.data_skipping_indices
		WHERE database = currentDatabase() AND table = 'otel_traces' AND name = 'idx_weft_run_id'
		AND type = 'bloom_filter' AND expr = 'RunId'`).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatalf("otel_traces idx_weft_run_id = %d, want the bloom_filter skip index on RunId 0016 adds", idx)
	}

	at := time.Now().UTC().Add(-time.Minute)
	span := func(trace, spanID, runID string) obsdb.Span {
		return obsdb.Span{
			TraceID: trace, SpanID: spanID, Name: "invoke_agent a", Kind: 1,
			Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs:    map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": runID},
			Resource: map[string]any{"service.name": "svc"},
		}
	}
	const traceA, traceB = "0102030405060708090a0b0c0d0e0f10", "0102030405060708090a0b0c0d0e0f11"
	// rs_b's record carries a trace (ff…) greater than its span's: the
	// run row's max(TraceID) names a trace that holds none of its spans.
	rec := obsdb.Record{
		Time: at, TraceID: "ffffffffffffffffffffffffffffffff", Service: "svc",
		Body: `{"type":"run_start","run_id":"rs_b"}`,
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": "rs_b",
			"weft.event.type": "run_start", "weft.event.pos": int64(0)},
		Resource: map[string]any{"service.name": "svc"},
	}
	if err := db.Write(ctx, obsdb.Batch{
		Spans:   []obsdb.Span{span(traceA, "0a0b0c0d0e0f0401", "rs_a"), span(traceB, "0a0b0c0d0e0f0402", "rs_b")},
		Records: []obsdb.Record{rec},
	}); err != nil {
		t.Fatal(err)
	}

	if trace, err := db.runTrace(ctx, "rs_a"); err != nil || trace != traceA {
		t.Fatalf("runTrace(rs_a) = %q, %v; want %s", trace, err, traceA)
	}
	if trace, err := db.runTrace(ctx, "rs_b"); err != nil || trace != rec.TraceID {
		t.Fatalf("runTrace(rs_b) = %q, %v; want the record's %s (the fallback's fixture)", trace, err, rec.TraceID)
	}
	for _, id := range []string{"rs_a", "rs_b"} {
		spans, err := db.RunSpans(ctx, id)
		if err != nil || len(spans) != 1 || spans[0].Attrs["weft.run.id"] != id {
			t.Errorf("RunSpans(%s) = %+v, %v; want its one span", id, spans, err)
		}
	}

	explain := func(q string, args ...any) string {
		t.Helper()
		rows, err := db.conn.Query(ctx, "EXPLAIN indexes = 1 "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out = append(out, strings.TrimSpace(line))
		}
		return strings.Join(out, "\n")
	}
	if p := explain(runSpansByTrace, traceA, "rs_a"); !strings.Contains(p, "idx_trace_id") {
		t.Errorf("the by-trace statement's plan names no idx_trace_id:\n%s", p)
	}
	if p := explain(runSpansByRun, "rs_a"); !strings.Contains(p, "idx_weft_run_id") {
		t.Errorf("the by-run statement's plan names no idx_weft_run_id:\n%s", p)
	}
}

// TestRunTreeAndChildRunsUseTheirIndexes (U9.1; UX-E-34, UX-E-24): a
// run tree is found from the rows — the run and the id rule's on
// weft_runs' primary key, their children on idx_parent_run (0020) —
// and its spans read by RunId on idx_weft_run_id (0016), never by the
// whole trace; a derived run's trace is read with `TraceId = ?`
// (idx_trace_id, 0001); a page's children with `ParentRunID IN (…)`
// on idx_parent_run. EXPLAIN indexes = 1 names each. A run row naming
// another trace than its spans' still answers its tree.
func TestRunTreeAndChildRunsUseTheirIndexes(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()

	var idx uint64
	if err := db.conn.QueryRow(ctx, `SELECT count() FROM system.data_skipping_indices
		WHERE database = currentDatabase() AND table = 'weft_runs' AND name = 'idx_parent_run'
		AND type = 'bloom_filter' AND expr = 'ParentRunID'`).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatalf("weft_runs idx_parent_run = %d, want the bloom_filter skip index on ParentRunID 0020 adds", idx)
	}

	at := time.Now().UTC().Add(-time.Minute)
	const trace = "0102030405060708090a0b0c0d0e0f12"
	span := func(spanID, parentSpan, runID, parentRun string) obsdb.Span {
		attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": runID}
		if parentRun != "" {
			attrs["weft.parent.run.id"] = parentRun
		}
		return obsdb.Span{
			TraceID: trace, SpanID: spanID, ParentSpanID: parentSpan, Name: "invoke_agent a", Kind: 1,
			Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
		}
	}
	// The parent's record carries another trace than its spans (ff…):
	// the row's max(TraceID) holds none of them.
	rec := obsdb.Record{
		Time: at, TraceID: "ffffffffffffffffffffffffffffffff", Service: "svc",
		Body: `{"type":"run_start","run_id":"tr_p"}`,
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": "tr_p",
			"weft.event.type": "run_start", "weft.event.pos": int64(0)},
		Resource: map[string]any{"service.name": "svc"},
	}
	if err := db.Write(ctx, obsdb.Batch{
		Spans: []obsdb.Span{span("0a0b0c0d0e0f0501", "", "tr_p", ""),
			span("0a0b0c0d0e0f0502", "0a0b0c0d0e0f0501", "tr_p/0/c", "tr_p")},
		Records: []obsdb.Record{rec},
	}); err != nil {
		t.Fatal(err)
	}
	tree, err := db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: "tr_p"})
	if err != nil || len(tree.Spans) != 2 || tree.Spans[1].RunID != "tr_p/0/c" || tree.Spans[1].ParentRunID != "tr_p" {
		t.Errorf("RunTreeSpans(tr_p) = %+v, %v; want its span and its child's, read by run id", tree, err)
	}

	explain := func(q string, args ...any) string {
		t.Helper()
		rows, err := db.conn.Query(ctx, "EXPLAIN indexes = 1 "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out = append(out, strings.TrimSpace(line))
		}
		return strings.Join(out, "\n")
	}
	if p := explain(stockTreeSpans, trace, 10); !strings.Contains(p, "idx_trace_id") {
		t.Errorf("a derived tree's trace read names no idx_trace_id:\n%s", p)
	}
	// U9.1-fix: the tree is found from the rows — the id rule's on the
	// primary key, their children on idx_parent_run — and its spans read
	// by run id on idx_weft_run_id, never the whole trace.
	if p := explain(treeIDRows, treeIDArgs("tr_p")...); !strings.Contains(p, "Keys:\nRunId") || strings.Contains(p, "Condition: true") {
		t.Errorf("the id rule's rows are not read on weft_runs' primary key:\n%s", p)
	}
	if p := explain(treeChildIDs(treeIDCondition), treeIDArgs("tr_p")...); !strings.Contains(p, "idx_parent_run") {
		t.Errorf("the tree's children read names no idx_parent_run:\n%s", p)
	}
	if p := explain(treeSpansByIDRule+" OR RunId IN (?))"+treeSpanOrder, append(treeIDArgs("tr_p"), "x", 10)...); !strings.Contains(p, "idx_weft_run_id") || strings.Contains(p, "idx_trace_id") {
		t.Errorf("the tree's spans read names no idx_weft_run_id (or reads by trace):\n%s", p)
	}
	if p := explain(childRunsSQL(2, ""), "tr_p", "x", "tr_p", "x"); !strings.Contains(p, "idx_parent_run") {
		t.Errorf("ChildRuns' plan names no idx_parent_run:\n%s", p)
	}
}
