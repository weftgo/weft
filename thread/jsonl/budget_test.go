package jsonl_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// The jsonl half of the budget suite (plan §10, step 7.2): opening a
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
			e := thread.MessageEntry{Message: weft.User("tick " + strconv.Itoa(j))}
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

// budgetOpen100k is opening a 100k-entry session — a fresh Storage
// over the directory and its Load (the adopt is thread.Open's, and
// Memory's budgets cover it): measured 2026-09-30 at ~1.3s plain and
// ~6s under -race (BenchmarkOpen100k carries the number); the bound
// leaves CI hardware its margin.
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
	fresh, err := jsonl.Open(dir) // the next process's open (the page cache stays warm)
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
		e := thread.MessageEntry{Message: weft.User("tick")}
		e.ID = fmt.Sprintf("e_%06d", i)
		e.ParentID = parent
		e.Created = time.Now().UTC()
		if err := st.Append(ctx, "s_app", e); err != nil {
			b.Fatal(err)
		}
		parent = e.ID
	}
}
