package thread_test

import (
	"context"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// buildLong drives n plain turns on one Memory session — the long,
// uncompacted shape whose per-turn bookkeeping must not grow into the
// turn's cost.
func buildLong(tb testing.TB, n int) *thread.Session {
	tb.Helper()
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), weft.New(&benchContextModel{}))
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < n; i++ {
		turn, err := s.Send(ctx, weft.User("tick"))
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			tb.Fatal(err)
		}
	}
	return s
}

// budgetLongTurn bounds one whole turn — Send to Wait, a model that
// answers at once — on a session of 1000 turns: measured 2026-10-01 at
// ~1.3ms (most of it the one deep copy of the context the run is
// handed), ~12ms under -race; the bound leaves CI hardware its margin.
// What it guards is the shape, not the constant: the session's own
// bookkeeping reads the path several times a turn, and each read used
// to deep-copy the whole transcript.
const budgetLongTurn = 100 * time.Millisecond

func TestBudgetLongSessionTurn(t *testing.T) {
	ctx := context.Background()
	s := buildLong(t, 1000)
	const turns = 20
	start := time.Now()
	for i := 0; i < turns; i++ {
		turn, err := s.Send(ctx, weft.User("tick"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start) / turns; d > budgetLongTurn {
		t.Errorf("a turn on a 1000-turn session = %s, budget %s", d, budgetLongTurn)
	}
	// The steadier pin: asking an idle session what is pending walks
	// the path once and copies nothing — one allocation measured, the
	// path slice itself. A deep copy per entry would be thousands. The
	// race detector's bookkeeping is not the code's.
	if allocs := testing.AllocsPerRun(20, func() { _ = s.Pending() }); allocs > 8 && !raceEnabled {
		t.Errorf("Pending on a 1000-turn session allocates %d times, budget 8", int(allocs))
	}
}

func BenchmarkSendLongSession(b *testing.B) {
	ctx := context.Background()
	s := buildLong(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		turn, err := s.Send(ctx, weft.User("tick"))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPendingLongSession(b *testing.B) {
	s := buildLong(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Pending()
	}
}

func BenchmarkContextLongSession(b *testing.B) {
	s := buildLong(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Context()
	}
}
