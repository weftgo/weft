package obsdb

import (
	"context"
	"slices"
	"strings"
	"time"
)

// TreeReader is the run tree's reads (U9.1), an optional capability of
// a DB: a run's spans with every descendant run's beside them, and the
// children of many runs at once. Both first-party backends implement
// it and the obsdbtest conformance table holds them to the same
// answers; a backend that does not is a database without the tree —
// Studio then leaves its spans_tree capability off, says why, and
// reads each run's children one run at a time.
type TreeReader interface {
	// RunTreeSpans returns the spans of the run and of every run that
	// descends from it (RunTree): the runs whose parent chain reaches
	// it — RunRow.ParentRunID, else a span's weft.parent.run.id for a
	// run with no row — and, whatever else is known of them, the runs
	// core's id rule names its descendants (TreeIDRule: a Subagent
	// child's id is <parent>/<step>/<call id>). The descendants are
	// found from the run rows first and their spans read by run id, in
	// any trace — the run's trace is consulted only for its runs that
	// have no row — so a leaf of a large app-level trace reads its own
	// subtree, never the whole trace; a derived run (§9 of
	// docs/record-contract.md), whose spans carry no run id, is cut
	// from its trace. At most TreeSpanCap spans, in start order then
	// span id; RunTree.Truncated says the tree holds more. Each span
	// names the run it belongs to and that run's parent: the key a
	// reader draws one run's spans under another's by is (RunID,
	// SpanID). ErrNotFound for an unknown run; empty, not an error, for
	// a run with no span stored.
	RunTreeSpans(ctx context.Context, runID string) (RunTree, error)
	// ChildRuns returns the direct children (RunRow.ParentRunID) of each
	// of the parent ids, in one read per call however many ids it names
	// (the backend may split a very long list into chunks): each
	// parent's children ordered by Started, then id; a parent with no
	// child (or no row) is absent from the map. RunDetail.Children's
	// rows, for a page of runs at once — the runs list prices a page's
	// subagent trees level by level instead of one read per row.
	ChildRuns(ctx context.Context, parentIDs []string) (map[string][]RunRow, error)
}

// TreeSpanCap bounds the spans one RunTreeSpans answer holds: 10,000,
// the first in start order. A tree with more is cut there and says so
// (RunTree.Truncated; Studio's truncated hole, cause response_cap):
// each child run's own tree is a read away.
const TreeSpanCap = 10_000

// RunTree is TreeReader.RunTreeSpans' answer.
type RunTree struct {
	// Spans is the tree's spans in start order, then span id: at most
	// TreeSpanCap, the first ones.
	Spans []TreeSpan
	// Truncated says the tree holds more than TreeSpanCap spans: Spans
	// is its first TreeSpanCap.
	Truncated bool
	// Missing is the descendant runs the tree knows from their rows
	// that have no span in Spans — a child that ran without a tracer,
	// whose spans a destination dropped, or that has not reported one
	// yet — ordered by Started, then id. Empty when Truncated:
	// a cut tree cannot tell a missing run from one past the cap.
	Missing []TreeRun
}

// TreeRun names one run of a tree: its id, its parent, its public id
// (weft.public_id, "" when none — what a scoped reader filters by) and
// when it started (RunRow.Started, the order Missing lists runs in).
type TreeRun struct {
	ID, ParentRunID, PublicID string
	Started                   time.Time
}

// TreeSpan is one span of a run tree (TreeReader.RunTreeSpans): the
// span, the run it belongs to — its weft.run.id, or the derived run
// (§9 of docs/record-contract.md) whose subtree holds it — and that
// run's parent run ("" for the tree's root run when it has none, and
// when no row, span or id names one).
type TreeSpan struct {
	Span
	RunID, ParentRunID string
}

// TreeIDRule is the parent core's id rule gives id inside runID's
// subtree: a Subagent child's id is <parent>/<step>/<call id> (and
// <parent>/resume/<call id> under Approve), so an id that starts with
// runID + "/" descends from runID. The parent is the nearest id above
// it that known holds — id trimmed at each "/" from the end, so a call
// id holding a "/" of its own still finds its run — else runID. ok is
// false for an id outside the subtree (runID itself included).
func TreeIDRule(runID, id string, known func(string) bool) (parent string, ok bool) {
	if !strings.HasPrefix(id, runID+"/") {
		return "", false
	}
	for p := id; ; {
		i := strings.LastIndexByte(p, '/')
		if i <= len(runID) {
			return runID, true
		}
		p = p[:i]
		if known(p) {
			return p, true
		}
	}
}

// TreeMembers is runID's subtree from what a backend knows before it
// reads a span: parents maps each run it found — the rows its
// parent-chain and id-rule reads returned, the trace's runs with no
// row (their spans' weft.parent.run.id, "" when none) — to its parent.
// A run is in the subtree when its parent chain reaches runID, or,
// when the chain does not, when the id rule names it a descendant
// (TreeIDRule; its parent then the rule's). The answer maps every
// member, runID included, to its parent; a parent chain that loops
// (never written by the core) ends where it repeats.
func TreeMembers(runID string, parents map[string]string) map[string]string {
	known := func(id string) bool { _, ok := parents[id]; return ok || id == runID }
	in := map[string]string{runID: parents[runID]}
	for grew := true; grew; {
		grew = false
		for id, p := range parents {
			if _, done := in[id]; done {
				continue
			}
			if _, up := in[p]; up && p != "" {
				in[id], grew = p, true
			}
		}
		if grew {
			continue
		}
		for id := range parents {
			if _, done := in[id]; done {
				continue
			}
			if p, ok := TreeIDRule(runID, id, known); ok {
				in[id], grew = p, true
			}
		}
	}
	return in
}

// CutRunTree cuts spans, in the order a backend read them, to runID's
// subtree: the spans of runID and of every run whose parent chain
// reaches it. parents maps run ids to their stored parent
// (RunRow.ParentRunID; a backend reads it beside the spans); a run
// absent from it, or mapped to "", takes its parent from its spans'
// weft.parent.run.id (the root span carries it), and a derived run
// from its derivation's ParentRunID; a run none of those places in the
// subtree is in it when the id rule names it a descendant
// (TreeIDRule) — a running child whose run_start was lost, a run with
// no row. A span belongs to the run its weft.run.id names, else to the
// derived run whose subtree holds it; a span of neither (an app's own
// span with no run) is left out. A parent chain that loops (never
// written by the core) ends where it repeats. The result keeps the
// input order; nil spans or a run with no span in them cut to an
// empty, non-nil slice.
func CutRunTree(spans []Span, runID string, parents map[string]string) []TreeSpan {
	owner := make(map[string]string, len(spans)) // span id → run id
	parent := make(map[string]string, len(parents))
	for id, p := range parents {
		parent[id] = p
	}
	note := func(id, p string) {
		if p != "" && parent[id] == "" {
			parent[id] = p
		} else if _, ok := parent[id]; !ok {
			parent[id] = ""
		}
	}
	for _, s := range spans {
		id, _ := s.Attrs[attrRunID].(string)
		if id == "" {
			continue
		}
		owner[s.SpanID] = id
		p, _ := s.Attrs[attrParentRunID].(string)
		note(id, p)
	}
	for _, r := range DeriveStockRuns(spans) {
		for _, s := range r.Spans {
			if owner[s.SpanID] == "" {
				owner[s.SpanID] = r.Row.ID
			}
		}
		note(r.Row.ID, r.Row.ParentRunID)
	}
	in := TreeMembers(runID, parent)
	out := []TreeSpan{}
	for _, s := range spans {
		id := owner[s.SpanID]
		if p, ok := in[id]; id != "" && ok {
			out = append(out, TreeSpan{Span: s, RunID: id, ParentRunID: p})
		}
	}
	return out
}

// CapTree is a backend's last step: read is the spans it read in start
// order, at most TreeSpanCap + 1 so that one more says the tree holds
// more; parents is CutRunTree's; rows is the member runs it read a row
// of. The answer is the read cut to runID's subtree and to TreeSpanCap,
// and Missing: each of rows but runID's that is a member (TreeMembers,
// its parent the members') with no span in the cut, ordered by
// Started, then id. Missing stays empty on a cut tree.
func CapTree(read []Span, runID string, parents map[string]string, rows []TreeRun) RunTree {
	more := len(read) > TreeSpanCap
	if more {
		read = read[:TreeSpanCap]
	}
	out := RunTree{Spans: CutRunTree(read, runID, parents), Truncated: more}
	if more || len(rows) == 0 {
		return out
	}
	members := TreeMembers(runID, parents)
	seen := make(map[string]bool, len(rows))
	for _, s := range out.Spans {
		seen[s.RunID] = true
	}
	for _, r := range rows {
		if p, ok := members[r.ID]; ok && r.ID != runID && !seen[r.ID] {
			r.ParentRunID = p
			out.Missing = append(out.Missing, r)
			seen[r.ID] = true
		}
	}
	slices.SortStableFunc(out.Missing, func(a, b TreeRun) int {
		if c := a.Started.Compare(b.Started); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// TreeMemberIDs is TreeMembers' run ids, sorted: the ids a backend
// reads the tree's spans by.
func TreeMemberIDs(runID string, parents map[string]string) []string {
	members := TreeMembers(runID, parents)
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
