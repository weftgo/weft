package jsonl_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// The jsonl half of the budget suite: opening a
// 100k-entry session — the decode-and-adopt cost a restart pays — and
// the per-append cost with the backend's own durability in the loop.

// buildBigSession writes n message entries in batches through the
// backend's public Append: the shape of a long-lived session, written
// the way the session layer writes it.
func buildBigSession(tb testing.TB, st thread.Storage, id string, n int) {
	tb.Helper()
	ctx := context.Background()
	if err := st.Create(ctx, thread.Header{ID: id, Created: time.Now().UTC()}); err != nil {
		tb.Fatal(err)
	}
	const batch = 500
	parent := ""
	for i := 0; i < n; i += batch {
		end := i + batch
		if end > n {
			end = n
		}
		entries := make([]thread.Entry, 0, end-i)
		for j := i; j < end; j++ {
			e := thread.MessageEntry{Message: core.User("tick " + strconv.Itoa(j))}
			e.ID = fmt.Sprintf("e_%06d", j)
			e.ParentID = parent
			e.Created = time.Now().UTC()
			entries = append(entries, e)
			parent = e.ID
		}
		if err := st.Append(ctx, id, entries...); err != nil {
			tb.Fatal(err)
		}
	}
}

// budgetOpen100k is opening (Load plus adopt) a 100k-entry session:
// measured 2026-09-30 at ~3.1s on jsonl (BenchmarkOpen100k carries
// the number); the bound leaves CI hardware its margin.
const budgetOpen100k = 30 * time.Second

func TestBudgetOpen100k(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	buildBigSession(t, st, "s_big", 100_000)

	start := time.Now()
	fresh, err := jsonl.Open(dir) // the next process's open, cold
	if err != nil {
		t.Fatal(err)
	}
	_, entries, _, err := fresh.Load(ctx, "s_big")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > budgetOpen100k {
		t.Errorf("opening a 100k-entry session = %s, budget %s", d, budgetOpen100k)
	}
	if len(entries) != 100_000 {
		t.Fatalf("loaded %d entries, want 100000", len(entries))
	}
}

func BenchmarkOpen100k(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		st, err := jsonl.Open(dir)
		if err != nil {
			b.Fatal(err)
		}
		buildBigSession(b, st, "s_big", 100_000)
		b.StartTimer()
		fresh, err := jsonl.Open(dir)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, _, err := fresh.Load(ctx, "s_big"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppendJSONL is the per-append number with the backend's
// durability in the loop — one entry per call, the flush policy the
// session layer leaves open (FsyncOnFlush by default). No wall-clock
// budget: fsync is the disk's, not the code's; the number is the
// report, and Memory's budget (thread's suite) pins the logic.
func BenchmarkAppendJSONL(b *testing.B) {
	ctx := context.Background()
	st, err := jsonl.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_app", Created: time.Now().UTC()}); err != nil {
		b.Fatal(err)
	}
	parent := ""
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := thread.MessageEntry{Message: core.User("tick")}
		e.ID = fmt.Sprintf("e_%06d", i)
		e.ParentID = parent
		e.Created = time.Now().UTC()
		if err := st.Append(ctx, "s_app", e); err != nil {
			b.Fatal(err)
		}
		parent = e.ID
	}
}

// buildFleet writes n session files — a header line each, the shape
// Create leaves — straight to the directory: the list budget measures
// reading a fleet, and creating one through the backend would spend
// the test on 2n fsyncs.
func buildFleet(tb testing.TB, dir string, n int) {
	tb.Helper()
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := "s_l" + strconv.Itoa(i)
		line := fmt.Sprintf(`{"type":"session","weft":1,"id":%q,"created":%q}`+"\n",
			id, base.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano))
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
}

// budgetList10k is one page plus the count over a 10k-session
// directory, a title search and a meta filter beside it — the sqlite
// budget's shape (sqlite/budget_test.go). A directory is this
// backend's index, so every call reads every header; the budget is
// what keeps that read proportional to the headers. Measured
// 2026-10-01 at ~70ms a call.
const budgetList10k = 15 * time.Second

// budgetListBytesPerSession bounds what one List allocates per session
// file it reads: a header's worth. The read buffer is shared across
// the directory — it used to be a megabyte per file per call, 10 GiB
// of garbage for this fleet.
const budgetListBytesPerSession = 8 << 10

func TestBudgetList10k(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	buildFleet(t, dir, 10_000)
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	page, err := thread.List(ctx, st, thread.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if len(page.Sessions) != 100 || page.Total != 10_000 {
		t.Fatalf("page = %d sessions, total %d; want 100 of 10000", len(page.Sessions), page.Total)
	}
	if per := (after.TotalAlloc - before.TotalAlloc) / 10_000; per > budgetListBytesPerSession {
		t.Errorf("List allocated %d bytes per session file, budget %d", per, budgetListBytesPerSession)
	}
	if _, err := thread.List(ctx, st, thread.Query{Limit: 100, TitleSearch: "s_l1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := thread.List(ctx, st, thread.Query{Limit: 100, Meta: map[string]string{"tier": "gold"}}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > budgetList10k {
		t.Errorf("list over 10k (page + count + title + meta) = %s, budget %s", d, budgetList10k)
	}
}

func BenchmarkList10k(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	buildFleet(b, dir, 10_000)
	st, err := jsonl.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := thread.List(ctx, st, thread.Query{Limit: 100}); err != nil {
			b.Fatal(err)
		}
	}
}
