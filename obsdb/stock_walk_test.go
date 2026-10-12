package obsdb

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// naiveWalkUp is the unmemoised walk the derivation used before: the
// reference the memoised walker must agree with, span for span.
func naiveWalkUp(idx map[string]*Span, s *Span) stockWalk {
	var w stockWalk
	cur := s
	for range len(idx) {
		if cur.ParentSpanID == "" {
			return stockWalk{}
		}
		p, ok := idx[cur.ParentSpanID]
		if !ok || attr(p.Attrs, attrRunID) != "" {
			return stockWalk{}
		}
		if IsStockRoot(*p) {
			w.root = p
			return w
		}
		if w.tool == nil && stockOp(*p) == opExecuteTool {
			w.tool = p
		}
		cur = p
	}
	return stockWalk{}
}

func walkSpan(tr string, i int, parent int, name string) Span {
	s := Span{TraceID: tr, SpanID: fmt.Sprintf("%016x", i), Name: name,
		Start: time.Unix(0, int64(i)), End: time.Unix(0, int64(i)+1), Attrs: map[string]any{}}
	if parent >= 0 {
		s.ParentSpanID = fmt.Sprintf("%016x", parent)
	}
	if name == "invoke_agent a" {
		s.Attrs["gen_ai.conversation.id"] = "c"
	}
	return s
}

// A deep chain is derived in O(n) parent hops, not O(n·depth): a
// 5,000-deep chain re-derived on every ingest batch held SQLite's
// single writer for seconds (the review's 22 s over 50 batches). The
// bound is on the hops counted, so it never flakes on a slow machine.
func TestDeriveDeepChainLinear(t *testing.T) {
	const tr, n = "000000000000000000000000000000dd", 5000
	idx := map[string]*Span{}
	add := func(s Span) { idx[s.SpanID] = &s }
	add(walkSpan(tr, 0, -1, "invoke_agent a"))
	for i := 1; i < n; i++ {
		name := "chat m"
		if i%10 == 0 {
			name = "execute_tool t"
		}
		add(walkSpan(tr, i, i-1, name))
	}
	add(walkSpan(tr, n, n-1, "invoke_agent a")) // a nested run at the bottom
	start := time.Now()
	runs, hops := deriveTraceCounted(idx)
	took := time.Since(start)
	if hops > 2*len(idx) {
		t.Errorf("%d parent hops for %d spans: the walk is not linear", hops, len(idx))
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want the root and the nested one", len(runs))
	}
	for _, r := range runs {
		switch r.Row.ID {
		case StockRunID(tr, fmt.Sprintf("%016x", 0)):
			if len(r.Spans) != n {
				t.Errorf("the top run holds %d spans, want %d", len(r.Spans), n)
			}
		default:
			if r.Row.ParentRunID != StockRunID(tr, fmt.Sprintf("%016x", 0)) {
				t.Errorf("nested parent = %q", r.Row.ParentRunID)
			}
		}
	}
	t.Logf("5,000-deep chain: %d hops, %s", hops, took)
}

// The memoised walker answers what the naive walk answered for every
// span of random forests — broken chains, weft spans, cycles, nested
// roots and execute_tool spans included.
func TestStockWalkerMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	names := []string{"chat m", "execute_tool t", "invoke_agent a", "other"}
	for round := range 200 {
		const tr = "000000000000000000000000000000ee"
		n := 1 + rng.IntN(60)
		idx := map[string]*Span{}
		for i := range n {
			parent := -1
			switch k := rng.IntN(10); {
			case k < 7:
				parent = rng.IntN(n + 3) // forward/back edges: cycles, and missing parents past n
			case k < 8:
				parent = i
			}
			s := walkSpan(tr, i, parent, names[rng.IntN(len(names))])
			if rng.IntN(15) == 0 {
				s.Attrs[attrRunID] = "r"
			}
			idx[s.SpanID] = &s
		}
		w := newStockWalker(idx)
		for _, s := range idx {
			got, want := w.walkUp(s), naiveWalkUp(idx, s)
			if got != want {
				t.Fatalf("round %d span %s: memoised %+v, naive %+v", round, s.SpanID, got, want)
			}
		}
		if len(w.busy) != 0 {
			t.Fatalf("round %d: %d spans left busy", round, len(w.busy))
		}
	}
}
