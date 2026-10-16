package obsdb

import "context"

// TreeReader is the run tree's reads (U9.1), an optional capability of
// a DB: a run's spans with every descendant run's beside them, and the
// children of many runs at once. Both first-party backends implement
// it and the obsdbtest conformance table holds them to the same
// answers; a backend that does not is a database without the tree —
// Studio then leaves its spans_tree capability off, says why, and
// reads each run's children one run at a time.
type TreeReader interface {
	// RunTreeSpans returns the spans of the run and of every run that
	// descends from it — the runs whose parent chain (RunRow.ParentRunID,
	// else the span's weft.parent.run.id) reaches it — from the run's
	// trace, read in one query by trace id and cut to the subtree
	// (CutRunTree). Ordered by start, then span id. Each span names the
	// run it belongs to and that run's parent: the key a reader draws
	// one run's spans under another's by is (RunID, SpanID). A run in
	// another trace (a thread/pool child session's) is not in it.
	// ErrNotFound for an unknown run; empty, not an error, for a run
	// with no span stored.
	RunTreeSpans(ctx context.Context, runID string) ([]TreeSpan, error)
	// ChildRuns returns the direct children (RunRow.ParentRunID) of each
	// of the parent ids, in one read per call however many ids it names
	// (the backend may split a very long list into chunks): each
	// parent's children ordered by Started, then id; a parent with no
	// child (or no row) is absent from the map. RunDetail.Children's
	// rows, for a page of runs at once — the runs list prices a page's
	// subagent trees level by level instead of one read per row.
	ChildRuns(ctx context.Context, parentIDs []string) (map[string][]RunRow, error)
}

// TreeSpan is one span of a run tree (TreeReader.RunTreeSpans): the
// span, the run it belongs to — its weft.run.id, or the derived run
// (§9 of docs/record-contract.md) whose subtree holds it — and that
// run's parent run ("" for the tree's root run when it has none, and
// when no row or span names one).
type TreeSpan struct {
	Span
	RunID, ParentRunID string
}

// CutRunTree cuts one trace's spans, in the order a backend read them,
// to runID's subtree: the spans of runID and of every run whose parent
// chain reaches it. parents maps run ids to their stored parent
// (RunRow.ParentRunID; a backend reads it beside the spans); a run
// absent from it takes its parent from its spans' weft.parent.run.id
// (the root span carries it), and a derived run from its derivation's
// ParentRunID. A span belongs to the run its weft.run.id names, else
// to the derived run whose subtree holds it; a span of neither (an
// app's own span with no run) is left out. A parent chain that loops
// (never written by the core) ends where it repeats. The result keeps
// the input order; nil spans or a run with no span in the trace cut to
// an empty, non-nil slice.
func CutRunTree(spans []Span, runID string, parents map[string]string) []TreeSpan {
	owner := make(map[string]string, len(spans)) // span id → run id
	parent := make(map[string]string, len(parents))
	for id, p := range parents {
		if p != "" {
			parent[id] = p
		}
	}
	for _, s := range spans {
		id, _ := s.Attrs[attrRunID].(string)
		if id == "" {
			continue
		}
		owner[s.SpanID] = id
		if p, _ := s.Attrs[attrParentRunID].(string); p != "" && parent[id] == "" {
			parent[id] = p
		}
	}
	for _, r := range DeriveStockRuns(spans) {
		for _, s := range r.Spans {
			if owner[s.SpanID] == "" {
				owner[s.SpanID] = r.Row.ID
			}
		}
		if r.Row.ParentRunID != "" && parent[r.Row.ID] == "" {
			parent[r.Row.ID] = r.Row.ParentRunID
		}
	}
	in := map[string]bool{runID: true}
	for grew := true; grew; {
		grew = false
		for id, p := range parent {
			if in[p] && !in[id] {
				in[id], grew = true, true
			}
		}
	}
	out := []TreeSpan{}
	for _, s := range spans {
		if id := owner[s.SpanID]; id != "" && in[id] {
			out = append(out, TreeSpan{Span: s, RunID: id, ParentRunID: parent[id]})
		}
	}
	return out
}

// TreeRunIDs is the distinct run ids among a run tree's spans, each
// once, in the order its first span comes (a backend reads their
// parents by these).
func TreeRunIDs(spans []Span) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range spans {
		if id, _ := s.Attrs[attrRunID].(string); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
