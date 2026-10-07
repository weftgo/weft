package thread_test

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// The budget suite: the costs an operator feels —
// append latency, opening a long session (jsonl and sqlite carry their
// own files), building the context of a much-compacted one, listing a
// fleet — each with a Benchmark for the number and a TestBudget that
// fails over it, so CI enforces the budget the way it enforces
// correctness. The budgets are wall-clock bounds set at roughly an
// order of magnitude over the measured time (each constant's note says
// where it was measured); alloc counts, which pin a budget steadier
// than time on noisy CI hardware, ride along where they help.

// budgetAppend is the per-append ceiling on Memory (storage.Append of
// one entry, no session machinery): measured 2026-09-30 at ~5µs plain
// and a few hundred microseconds under -race on a fully loaded box
// (five fuzzing processes on every core — the CI worst case, then
// some); the bound covers that worst case. The alloc bound is asserted
// only without -race, whose bookkeeping is not the code's.
const budgetAppend = 1 * time.Millisecond

func TestBudgetAppend(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	if err := st.Create(ctx, thread.Header{ID: "s_bench", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	parent := ""
	appendOne := func(i int) {
		t.Helper()
		e := thread.MessageEntry{Message: core.User("tick")}
		e.ID, e.ParentID, e.Created = "e_"+strconv.Itoa(i), parent, time.Now().UTC()
		if err := st.Append(ctx, "s_bench", e); err != nil {
			t.Fatal(err)
		}
		parent = e.ID
	}
	allocs := testing.AllocsPerRun(100, func() { appendOne(1_000_000) })
	start := time.Now()
	for i := 0; i < 1000; i++ {
		appendOne(i)
	}
	if d := time.Since(start) / 1000; d > budgetAppend {
		t.Errorf("Memory append = %s, budget %s", d, budgetAppend)
	}
	// 15 measured on CI's stable Go (the runtime's allocs shift a
	// little between versions); the bound catches order-of-magnitude
	// drift, not a single allocation.
	if allocs > 24 && !raceEnabled {
		t.Errorf("Memory append allocs = %d, budget 24", int(allocs))
	}
}

// benchContextModel serves the compactions benchmark: every call
// answers with its index — the summarizer and the turns share it.
type benchContextModel struct{ calls int }

func (m *benchContextModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "threadtest", Name: "benchcontext"}
}

func (m *benchContextModel) Stream(_ context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.calls++
	n := m.calls
	return func(yield func(core.ModelEvent, error) bool) {
		for _, ev := range []core.ModelEvent{
			core.ModelTextDelta{Text: fmt.Sprintf("turn %d", n)},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// buildCompacted drives n Send+Compact cycles on one session — the
// much-compacted shape the context build must stay cheap over.
func buildCompacted(tb testing.TB, n int) *thread.Session {
	tb.Helper()
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), core.New(&benchContextModel{}), thread.KeepRecent(1))
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < n; i++ {
		turn, err := s.Send(ctx, core.User("tick"))
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			tb.Fatal(err)
		}
		if err := s.Compact(ctx); err != nil {
			tb.Fatal(err)
		}
	}
	return s
}

// budgetContext is the context build over 50 compactions: measured
// 2026-09-30 at tens of microseconds; the bound leaves CI hardware its
// margin.
const budgetContext = 5 * time.Millisecond

func TestBudgetContextAfterCompactions(t *testing.T) {
	s := buildCompacted(t, 50)
	start := time.Now()
	if got := len(s.Context()); got == 0 {
		t.Fatal("no context built")
	}
	if d := time.Since(start); d > budgetContext {
		t.Errorf("context after 50 compactions = %s, budget %s", d, budgetContext)
	}
}

func BenchmarkContextAfterCompactions(b *testing.B) {
	s := buildCompacted(b, 50)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Context()
	}
}

// budgetList10k is the full paged walk over 10k Memory sessions plus
// one metadata filter across them: measured 2026-09-30 at ~0.9s plain
// (the Memory backend scans the whole fleet per page — the durable
// backends carry their own list budgets in their modules) and ~7s
// under -race on a fully loaded box; the bound covers that worst case.
const budgetList10k = 20 * time.Second

func TestBudgetList10k(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	for i := 0; i < 10_000; i++ {
		h := thread.Header{ID: "s_l" + strconv.Itoa(i), Created: time.Now().UTC()}
		if i == 0 {
			h.Meta = map[string]string{"tier": "gold"}
		}
		if err := st.Create(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	seen := 0
	var cursor time.Time
	for {
		page, err := thread.List(ctx, st, thread.Query{Limit: 100, Before: cursor})
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Sessions)
		if len(page.Sessions) == 0 {
			break
		}
		// Sessions sharing a Created time page by id too; this walk's
		// headers each carry their own stamp, so the time cursor alone
		// walks the whole fleet.
		cursor = page.Sessions[len(page.Sessions)-1].Created
	}
	if _, err := thread.List(ctx, st, thread.Query{Limit: 100, Meta: map[string]string{"tier": "gold"}}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > budgetList10k {
		t.Errorf("list 10k (walk + filter) = %s, budget %s", d, budgetList10k)
	}
	if seen != 10_000 {
		t.Fatalf("walked %d sessions, want 10000", seen)
	}
}
