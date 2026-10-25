package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
