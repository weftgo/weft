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
