package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// statSpans is one run's spans in two batches: the root, a chat span
// whose second attempt arrives later, a failed tool call, a policy
// DENIED call, a resume's denied call and a summarizer call (the two
// the rollup skips).
func statSpans(run string, t0 time.Time) (first, late []obsdb.Span) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(run))
	trace := fmt.Sprintf("%016x%016x", h.Sum64(), h.Sum64())
	base := func(op string) map[string]any {
		a := map[string]any{"weft.run.id": run}
		if op != "" {
			a["gen_ai.operation.name"] = op
		}
		return a
	}
	span := func(id, parent, name string, start, end time.Duration, status int, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: trace, SpanID: id, ParentSpanID: parent, Name: name, Kind: 1,
			Start: t0.Add(start), End: t0.Add(end), StatusCode: status, Attrs: attrs}
	}
	root := base("invoke_agent")
	root["gen_ai.agent.name"], root["weft.run.denied"] = "alpha", int64(1)
	chat := base("chat")
	chat["weft.step.index"], chat["gen_ai.provider.name"], chat["gen_ai.request.model"] = int64(0), "p", "m"
	chat["gen_ai.usage.input_tokens"], chat["gen_ai.usage.output_tokens"], chat["weft.ttft_ms"] = int64(12), int64(3), int64(40)
	chat["gen_ai.usage.cache_read.input_tokens"] = int64(4)
	att := func(i int64, status int) obsdb.Span {
		a := base("")
		a["weft.attempt.index"] = i
		return span(fmt.Sprintf("a%016d", i), "c000000000000001", "attempt", 10*time.Millisecond, 20*time.Millisecond, status, a)
	}
	tool := func(id, name, et string, extra map[string]any) obsdb.Span {
		a := base("execute_tool")
		a["gen_ai.tool.name"], a["weft.step.index"] = name, int64(0)
		status := 1
		if et != "" {
			a["error.type"], status = et, 2
		}
		for k, v := range extra {
			a[k] = v
		}
		return span(id, "r000000000000001", "execute_tool "+name, 300*time.Millisecond, 350*time.Millisecond, status, a)
	}
	sum := base("chat")
	sum["weft.chat.role"], sum["gen_ai.usage.input_tokens"] = "summarizer", int64(500)
	first = []obsdb.Span{
		att(1, 2),
		span("c000000000000001", "r000000000000001", "chat m", 5*time.Millisecond, 250*time.Millisecond, 1, chat),
		tool("t000000000000001", "lookup", "tool_error", nil),
		tool("t000000000000002", "pay", "DENIED", nil),
		tool("t000000000000003", "pay", "DENIED", map[string]any{"weft.tool.denied": true}),
		tool("t000000000000004", "retry_me", "RETRY", nil),
		span("s000000000000001", "r000000000000001", "chat summary", 400*time.Millisecond, 450*time.Millisecond, 1, sum),
		span("r000000000000001", "", "invoke_agent alpha", 0, time.Second, 1, root),
	}
	late = []obsdb.Span{att(3, 1)}
	return first, late
}

// spanStatRows dumps span_stats in key order, one line per row.
func spanStatRows(t *testing.T, db *sql.DB, where string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(`SELECT trace_id, span_id, run_id, op, name, provider, model, step, start_ns, dur_ms,
		COALESCE(ttft_ms, -1), attempts, failed, error_type, denied, input_tokens, output_tokens,
		cached_input_tokens, cache_write_tokens, reasoning_tokens FROM span_stats WHERE `+where+` ORDER BY trace_id, span_id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		vals := make([]any, 20)
		ptrs := make([]any, 20)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSpanStatsAtIngest pins what the writer derives into the rollup
// (migration 0023's rules): the root, the chat call with its attempts
// raised by an attempt stored after it, the failed and the DENIED tool
// calls — a RETRY result is no failure — and no row for the resume's
// denied call, the summarizer call or an attempt.
func TestSpanStatsAtIngest(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	db := d.(*DB)
	first, late := statSpans("r_ingest", time.Unix(1_800_000_000, 0))
	for _, b := range [][]obsdb.Span{first, late, first} { // the last a retried batch: a no-op
		if err := db.Write(context.Background(), obsdb.Batch{Spans: b}); err != nil {
			t.Fatal(err)
		}
	}
	got := spanStatRows(t, db.reads, "1 = 1")
	var ops []string
	for _, r := range got {
		f := strings.Split(r, "|")
		ops = append(ops, f[1]+":"+f[3]+":"+f[4])
	}
	want := []string{"c000000000000001:chat:m", "r000000000000001:invoke_agent:alpha",
		"t000000000000001:execute_tool:lookup", "t000000000000002:execute_tool:pay", "t000000000000004:execute_tool:retry_me"}
	if !slices.Equal(ops, want) {
		t.Fatalf("span_stats rows = %v, want %v", ops, want)
	}
	var attempts, failed, denied, in, cached int64
	var ttft, dur float64
	if err := db.reads.QueryRow(`SELECT attempts, failed, ttft_ms, dur_ms, input_tokens, cached_input_tokens FROM span_stats WHERE op = 'chat'`).
		Scan(&attempts, &failed, &ttft, &dur, &in, &cached); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || failed != 0 || ttft != 40 || dur != 245 || in != 12 || cached != 4 {
		t.Errorf("chat row = attempts %d failed %d ttft %v dur %v in %d cached %d; want 3 0 40 245 12 4", attempts, failed, ttft, dur, in, cached)
	}
	for _, c := range []struct {
		span           string
		failed, denied int64
	}{{"r000000000000001", 0, 1}, {"t000000000000001", 1, 0}, {"t000000000000002", 0, 1}, {"t000000000000004", 0, 0}} {
		if err := db.reads.QueryRow(`SELECT failed, denied FROM span_stats WHERE span_id = ?`, c.span).Scan(&failed, &denied); err != nil {
			t.Fatal(err)
		}
		if failed != c.failed || denied != c.denied {
			t.Errorf("%s: failed %d denied %d, want %d %d", c.span, failed, denied, c.failed, c.denied)
		}
	}
}

// rewindTo22 makes path's file a pre-0023 one: the rollup, its trigger,
// the pass predicate column and their migration rows gone.
func rewindTo22(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for _, stmt := range []string{
		`DROP TRIGGER span_stats_gone`, `DROP TABLE span_stats`,
		`ALTER TABLE experiments DROP COLUMN pass_predicate`,
		`DROP INDEX spans_attempt`,
		`DELETE FROM obsdb_migrations WHERE version >= 23`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// TestUpgradeTo0023BackfillsSpanStats: a file opened at 0023 derives the
// rollup of every span it already holds, row for row what the writer
// derives at ingest — over several backfill batches.
func TestUpgradeTo0023BackfillsSpanStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at22.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	for i := range 40 {
		first, late := statSpans("r_"+strconv.Itoa(i), t0.Add(time.Duration(i)*time.Minute))
		for _, b := range [][]obsdb.Span{first, late} {
			if err := d.Write(context.Background(), obsdb.Batch{Spans: b}); err != nil {
				t.Fatal(err)
			}
		}
	}
	ingested := spanStatRows(t, d.(*DB).reads, "1 = 1")
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	rewindTo22(t, path)
	old := spanStatsBatchSize
	spanStatsBatchSize = 7 // several batches, one ending on the last span
	defer func() { spanStatsBatchSize = old }()
	d, err = Open(path)
	if err != nil {
		t.Fatalf("open a 0022 file: %v", err)
	}
	defer func() { _ = d.Close() }()
	backfilled := spanStatRows(t, d.(*DB).reads, "1 = 1")
	if len(ingested) != 40*5 || !slices.Equal(backfilled, ingested) {
		t.Errorf("backfill = %d rows, ingest %d (want %d, equal):\n%s\nvs\n%s", len(backfilled), len(ingested), 40*5,
			strings.Join(backfilled[:min(3, len(backfilled))], "\n"), strings.Join(ingested[:min(3, len(ingested))], "\n"))
	}
}

// TestSpanStatsFollowDeletes: a deleted run's rollup rows go with its
// spans (the trigger), and a sweep leaves none.
func TestSpanStatsFollowDeletes(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "del.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	db := d.(*DB)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	for _, run := range []string{"r_keep", "r_gone"} {
		first, late := statSpans(run, t0)
		for _, b := range [][]obsdb.Span{first, late} {
			if err := db.Write(ctx, obsdb.Batch{Spans: b}); err != nil {
				t.Fatal(err)
			}
		}
		t0 = t0.Add(time.Minute)
	}
	count := func(run string) int {
		return len(spanStatRows(t, db.reads, "run_id = ?", run))
	}
	if count("r_keep") != 5 || count("r_gone") != 5 {
		t.Fatalf("rows before delete: %d, %d; want 5 each", count("r_keep"), count("r_gone"))
	}
	if err := db.DeleteRun(ctx, "r_gone"); err != nil {
		t.Fatal(err)
	}
	if count("r_gone") != 0 || count("r_keep") != 5 {
		t.Errorf("after DeleteRun: r_gone %d rows (want 0), r_keep %d (want 5)", count("r_gone"), count("r_keep"))
	}
	if _, err := db.Sweep(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := len(spanStatRows(t, db.reads, "1 = 1")); n != 0 {
		t.Errorf("after Sweep: %d span_stats rows, want 0", n)
	}
}

// TestSpanStatsBackfillTime measures migration 0023's backfill on a
// seeded file: WEFT_BACKFILL_RUNS runs (default 300; 50000 is the
// measurement CHANGELOG quotes, 450,000 spans in 2.8 s) of statSpans'
// eight spans each, rewound to 0022, then opened. The open is logged;
// it must stay within a generous bound so a Studio over a large file
// still starts.
func TestSpanStatsBackfillTime(t *testing.T) {
	n := 300
	if v := os.Getenv("WEFT_BACKFILL_RUNS"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	path := filepath.Join(t.TempDir(), "seeded.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	const per = 100
	for i := 0; i < n; i += per {
		var b obsdb.Batch
		for j := i; j < min(i+per, n); j++ {
			first, late := statSpans("r_"+strconv.Itoa(j), t0.Add(time.Duration(j)*time.Second))
			b.Spans = append(b.Spans, first...)
			b.Spans = append(b.Spans, late...)
		}
		if err := d.Write(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	var spans int
	if err := d.(*DB).reads.QueryRow(`SELECT COUNT(*) FROM spans`).Scan(&spans); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	rewindTo22(t, path)
	start := time.Now()
	d, err = Open(path)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var rows int
	if err := d.(*DB).reads.QueryRow(`SELECT COUNT(*) FROM span_stats`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	t.Logf("migration 0023 over %d spans (%d runs): %d rollup rows in %v (%.0f spans/s)", spans, n, rows, took, float64(spans)/took.Seconds())
	if rows != n*5 {
		t.Errorf("backfill = %d rows, want %d", rows, n*5)
	}
	if took > time.Minute {
		t.Errorf("migration 0023 over %d spans took %v: a Studio over this file would not start", spans, took)
	}
}

// longTrace is one run's trace of n spans: model calls, each with an
// attempt span, under one root — the shape whose attempts subquery
// once scanned the whole trace per chat span.
func longTrace(run string, n int, t0 time.Time) []obsdb.Span {
	trace := fmt.Sprintf("%032x", len(run)+n)
	out := make([]obsdb.Span, 0, n)
	root := obsdb.Span{TraceID: trace, SpanID: "f000000000000000", Name: "invoke_agent long", Start: t0, End: t0.Add(time.Hour),
		StatusCode: 1, Attrs: map[string]any{"weft.run.id": run, "gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "long"}}
	for i := 0; len(out) < n-1; i++ {
		chat := fmt.Sprintf("c%015x", i)
		out = append(out,
			obsdb.Span{TraceID: trace, SpanID: fmt.Sprintf("a%015x", i), ParentSpanID: chat, Name: "attempt",
				Start: t0.Add(time.Duration(i) * time.Millisecond), End: t0.Add(time.Duration(i+1) * time.Millisecond), StatusCode: 1,
				Attrs: map[string]any{"weft.run.id": run, "weft.attempt.index": int64(1)}},
			obsdb.Span{TraceID: trace, SpanID: chat, ParentSpanID: root.SpanID, Name: "chat m",
				Start: t0.Add(time.Duration(i) * time.Millisecond), End: t0.Add(time.Duration(i+1) * time.Millisecond), StatusCode: 1,
				Attrs: map[string]any{"weft.run.id": run, "gen_ai.operation.name": "chat", "weft.step.index": int64(i),
					"gen_ai.request.model": "m", "gen_ai.usage.input_tokens": int64(1)}})
	}
	return append(out[:n-1], root)
}

// TestSpanStatsLongTrace: a 30,000-span trace ingests and backfills in
// bounded time — the attempts subquery reads spans_attempt, never the
// whole trace per chat span (before: 41.7 s to ingest, 46.8 s blocking
// Open; now well under a second each).
func TestSpanStatsLongTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db := d.(*DB)
	var plan strings.Builder
	rows, err := db.reads.Query(`EXPLAIN QUERY PLAN `+spanStatsInsert(`trace_id = ? AND span_id = ?`), "t", "s")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	_ = rows.Close()
	if !strings.Contains(plan.String(), "spans_attempt") {
		t.Errorf("the derivation's plan does not read spans_attempt:\n%s", plan.String())
	}
	spans := longTrace("r_long", 30_000, time.Unix(1_800_000_000, 0))
	start := time.Now()
	for i := 0; i < len(spans); i += 500 {
		if err := db.Write(context.Background(), obsdb.Batch{Spans: spans[i:min(i+500, len(spans))]}); err != nil {
			t.Fatal(err)
		}
	}
	ingest := time.Since(start)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	rewindTo22(t, path)
	start = time.Now()
	d, err = Open(path)
	backfill := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var n int
	if err := d.(*DB).reads.QueryRow(`SELECT COUNT(*) FROM span_stats`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	t.Logf("a 30,000-span trace: ingest %v, backfill on open %v, %d rollup rows", ingest, backfill, n)
	if n != 15_000 {
		t.Errorf("rollup rows = %d, want 15000 (the chat spans and the root)", n)
	}
	if !raceEnabled && (ingest > 15*time.Second || backfill > 5*time.Second) {
		t.Errorf("a 30,000-span trace took %v to ingest and %v to backfill: the attempts subquery is scanning the trace", ingest, backfill)
	}
}

// BenchmarkSpanStatsIngest pins the rollup's per-span cost at ingest:
// the derivation is prepared once per Write (spanStatWriter), not
// parsed per span. Measured in 450-span batches: 37 µs/span with the
// rollup off, 54 µs prepared (the review measured 213 µs re-parsed).
// Reports µs/span; TestSpanStatsIngestCost holds the bound.
func BenchmarkSpanStatsIngest(b *testing.B) {
	d, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	t0 := time.Unix(1_800_000_000, 0)
	b.ResetTimer()
	spans := 0
	// Batches of 50 runs (450 spans): an exporter's batch, the shape
	// the prepared statement amortises over.
	var batch []obsdb.Span
	flush := func() {
		if err := d.Write(context.Background(), obsdb.Batch{Spans: batch}); err != nil {
			b.Fatal(err)
		}
		spans += len(batch)
		batch = batch[:0]
	}
	for i := range b.N {
		first, late := statSpans("r_bench_"+strconv.Itoa(i), t0.Add(time.Duration(i)*time.Second))
		batch = append(append(batch, first...), late...)
		if len(batch) >= 450 {
			flush()
		}
	}
	if len(batch) > 0 {
		flush()
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(spans), "µs/span")
}

// TestSpanStatsIngestCost runs the benchmark briefly and fails on a
// per-span cost that says the derivation is parsed per span again
// (213 µs/span then; ~54 now): the bound, 150 µs, leaves room for a
// loaded machine. The best of three runs counts (load only adds
// time). Not on CI: a shared runner measured 234 µs/span on the
// unchanged code (CI 40354621447), so a wall-clock bound there says
// nothing; the benchmark still runs with -bench.
func TestSpanStatsIngestCost(t *testing.T) {
	if testing.Short() || raceEnabled || os.Getenv("CI") != "" {
		t.Skip("a timing bound: not under -short, -race or CI")
	}
	us := math.Inf(1)
	for range 3 {
		us = min(us, testing.Benchmark(BenchmarkSpanStatsIngest).Extra["µs/span"])
	}
	if us > 150 {
		t.Errorf("span_stats ingest costs %.0f µs/span, want under 150: is the INSERT … SELECT prepared once per Write?", us)
	}
}

// TestUpgradeTo0025AddsSpansAttempt: a file that reached 0024 with
// 0023's first text (no spans_attempt) gets the index at 0025, and a
// span write — whose derivation names it — works again.
func TestUpgradeTo0025AddsSpansAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at24.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`DROP INDEX spans_attempt`, `DELETE FROM obsdb_migrations WHERE version >= 25`} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	first, late := statSpans("r_25", time.Unix(1_800_000_000, 0))
	if err := d.Write(context.Background(), obsdb.Batch{Spans: append(first, late...)}); err != nil {
		t.Fatalf("a span write after 0025: %v", err)
	}
	if n := len(spanStatRows(t, d.(*DB).reads, "run_id = 'r_25'")); n != 5 {
		t.Errorf("rollup rows after 0025 = %d, want 5", n)
	}
}

// TestStatsDelegatedScale: by=agent's delegated query drives from the
// runs and seeks each one's descendants on span_stats_run — its plan
// names the index with r first — and 4,000 runs with three model
// calls each (a subagent under every tenth) answer under a bound
// (before: 15.5 s, the planner re-scanning r per chat span).
func TestStatsDelegatedScale(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "scale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	db := d.(*DB)
	t0 := time.Unix(1_800_000_000, 0).UTC()
	q := obsdb.StatsQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"}, By: obsdb.StatsByAgent,
		Since: t0.Add(-time.Hour), Until: t0.Add(24 * time.Hour)}
	buckets, width, _ := obsdb.CheckStats(q)
	with, wargs := statsRuns(q, buckets, width, time.Now())
	rows, err := db.reads.Query(`EXPLAIN QUERY PLAN `+with+statsBelowSQL(""), append(wargs, t0.UnixNano())...)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	_ = rows.Close()
	joined := strings.Join(plan, "\n")
	runsAt, indexAt := -1, -1
	for i, p := range plan {
		if runsAt < 0 && strings.Contains(p, "runs") && !strings.Contains(p, "AS c") {
			runsAt = i
		}
		if strings.Contains(p, "span_stats_run") {
			indexAt = i
		}
	}
	if indexAt < 0 || runsAt < 0 || runsAt > indexAt {
		t.Errorf("the delegated query's plan must drive from the runs and seek span_stats_run:\n%s", joined)
	}
	if testing.Short() || raceEnabled {
		return
	}
	const n = 4000
	var b obsdb.Batch
	flush := func() {
		if err := d.Write(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		b = obsdb.Batch{}
	}
	span := func(run, trace, id, parent, name string, at time.Duration, attrs map[string]any) obsdb.Span {
		attrs["weft.run.id"] = run
		return obsdb.Span{TraceID: trace, SpanID: id, ParentSpanID: parent, Name: name, StatusCode: 1,
			Start: t0.Add(at), End: t0.Add(at + time.Second), Attrs: attrs}
	}
	for i := range n {
		run := fmt.Sprintf("%016x", i+1)
		agent := []string{"a", "b", "c", "d"}[i%4]
		trace := fmt.Sprintf("%032x", i+1)
		at := time.Duration(i) * time.Second
		for c := range 3 {
			b.Spans = append(b.Spans, span(run, trace, fmt.Sprintf("c%015x", i*4+c), "r000000000000000", "chat m", at,
				map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(c), "gen_ai.request.model": "m",
					"gen_ai.usage.input_tokens": int64(10)}))
		}
		b.Spans = append(b.Spans, span(run, trace, "r000000000000000", "", "invoke_agent "+agent, at,
			map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": agent}))
		if i%10 == 0 {
			child := run + "/0/call_1"
			b.Spans = append(b.Spans,
				span(child, trace, fmt.Sprintf("k%015x", i), "r000000000000000", "chat m", at,
					map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(0), "gen_ai.request.model": "m",
						"gen_ai.usage.input_tokens": int64(7)}),
				span(child, trace, fmt.Sprintf("q%015x", i), "r000000000000000", "invoke_agent helper", at,
					map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "helper", "weft.parent.run.id": run}))
		}
		if len(b.Spans) >= 2000 {
			flush()
		}
	}
	flush()
	start := time.Now()
	page, err := db.Stats(context.Background(), q)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	var delegated int64
	for _, g := range page.Groups {
		delegated += g.Delegated.InputTokens
	}
	t.Logf("by=agent over %d runs (+%d subagents, 3 calls each): %v", n, n/10, took)
	if delegated != int64(n/10)*7 {
		t.Errorf("delegated input tokens = %d, want %d", delegated, (n/10)*7)
	}
	if took > 3*time.Second {
		t.Errorf("by=agent over %d runs took %v: is the delegated query driving from the runs on span_stats_run?", n, took)
	}
}

// TestSpanStatsFailureKeepsTheBatch (P10a-pre-go-fix item 2): a
// span_stats write that fails (here: spans_attempt dropped, so the
// derivation's INDEXED BY cannot prepare) rolls back to its savepoint —
// the spans and the run are stored, the rollup skips them, and one WARN
// is logged for the process, not one per span.
func TestSpanStatsFailureKeepsTheBatch(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)
	spanStatsWarn = sync.Once{}
	defer func() { spanStatsWarn = sync.Once{} }()

	path := filepath.Join(t.TempDir(), "nostats.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP INDEX spans_attempt`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	first, late := statSpans("r_nostats", time.Unix(1_800_000_000, 0))
	if err := d.Write(context.Background(), obsdb.Batch{Spans: append(first, late...)}); err != nil {
		t.Fatalf("a span_stats failure failed the batch: %v", err)
	}
	var spans int
	if err := d.(*DB).reads.QueryRow(`SELECT COUNT(*) FROM spans WHERE run_id = 'r_nostats'`).Scan(&spans); err != nil {
		t.Fatal(err)
	}
	if spans != len(first)+len(late) {
		t.Errorf("stored spans = %d, want %d", spans, len(first)+len(late))
	}
	if _, err := d.Run(context.Background(), "r_nostats"); err != nil {
		t.Errorf("the run row: %v", err)
	}
	if n := len(spanStatRows(t, d.(*DB).reads, "run_id = 'r_nostats'")); n != 0 {
		t.Errorf("rollup rows = %d, want 0 (each write rolled back to its savepoint)", n)
	}
	if n := strings.Count(logged.String(), "span_stats rollup write failed"); n != 1 || !strings.Contains(logged.String(), "level=WARN") {
		t.Errorf("WARN lines = %d, want 1:\n%s", n, logged.String())
	}
}

// TestUpgradeTo0023LogsTheBackfill (P10a-pre-go-fix item 6): Open
// blocks for 0023's backfill, so it says so — one INFO line with the
// span count before, one with the duration after; a file with no spans
// logs neither.
func TestUpgradeTo0023LogsTheBackfill(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	path := filepath.Join(t.TempDir(), "at22.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logged.String(), "0023") {
		t.Errorf("a new file logged the backfill:\n%s", logged.String())
	}
	first, late := statSpans("r_log", time.Unix(1_800_000_000, 0))
	spans := append(first, late...)
	if err := d.Write(context.Background(), obsdb.Batch{Spans: spans}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	rewindTo22(t, path)
	logged.Reset()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	out := logged.String()
	if !strings.Contains(out, "level=INFO msg=\"weft/obsdb/sqlite: migration 0023 backfills the span_stats rollup; Open waits for it\" spans="+strconv.Itoa(len(spans))) ||
		!strings.Contains(out, "migration 0023 backfill done") || !strings.Contains(out, "duration=") {
		t.Errorf("the backfill's INFO lines:\n%s", out)
	}
}
