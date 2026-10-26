package obsdb

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb/internal/caps"
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
	// a run with no span stored. TreeQuery.PublicID and Meta scope the
	// answer (a panel token's): the cut, then the scope, then
	// the cap, so a run the scope leaves out never takes a visible
	// run's place under the cap (RunTree.Hidden).
	RunTreeSpans(ctx context.Context, q TreeQuery) (RunTree, error)
	// ChildRuns returns the direct children (RunRow.ParentRunID) of each
	// of the parent ids, in one read per call however many ids it names
	// (the backend may split a very long list into chunks): each
	// parent's children ordered by Started, then id; a parent with no
	// child (or no row) is absent from the map. RunDetail.Children's
	// rows, for a page of runs at once — the runs list prices a page's
	// subagent trees level by level instead of one read per row. At
	// most ChildRunsCap rows in all; past it ErrChildRunsCapped.
	ChildRuns(ctx context.Context, parentIDs []string) (map[string][]RunRow, error)
}

// TreeQuery names the run tree TreeReader.RunTreeSpans reads.
type TreeQuery struct {
	// RunID is the tree's root run.
	RunID string
	// PublicID, when not "", scopes the tree to one public id (a panel
	// token's, U9.1-fix): the root run's spans stay, and a descendant
	// run stays when its row and each of its spans carry PublicID and
	// its parent run stays; any other descendant is left out with its
	// whole subtree, unnamed but counted (RunTree.Hidden, HiddenCount). The
	// backend leaves the rows' outsiders out of its span read, and the
	// cap counts the visible spans alone.
	PublicID string
	// Meta, when not empty, scopes the tree to the runs carrying every
	// pair (a tag-scoped panel token's, I4) by PublicID's rule: a
	// descendant run stays when its row's metadata and each of its spans'
	// attributes carry every pair — run metadata rides every weft span
	// verbatim — and its parent run stays. With PublicID both apply.
	Meta map[string]string
}

// Scoped reports whether q scopes the tree (PublicID or Meta set).
func (q TreeQuery) Scoped() bool { return q.PublicID != "" || len(q.Meta) > 0 }

// admitsRow reports whether a member run's row lies in q's scope.
func (q TreeQuery) admitsRow(r TreeRun) bool {
	if q.PublicID != "" && r.PublicID != q.PublicID {
		return false
	}
	for k, v := range q.Meta {
		if got, ok := r.Meta[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// admitsSpan reports whether a span's attributes lie in q's scope.
func (q TreeQuery) admitsSpan(attrs map[string]any) bool {
	if q.PublicID != "" {
		if got, _ := attrs[attrPublicID].(string); got != q.PublicID {
			return false
		}
	}
	for k, v := range q.Meta {
		if got, ok := attrs[k].(string); !ok || got != v {
			return false
		}
	}
	return true
}

// TreeSpanCap bounds the spans one RunTreeSpans answer holds: 10,000,
// the first in start order. A tree with more is cut there and says so
// (RunTree.Truncated; Studio's truncated hole, cause response_cap):
// each child run's own tree is a read away.
const TreeSpanCap = 10_000

// TreeTraceCap bounds the spans a derived run's tree (§9 of
// docs/record-contract.md: its spans carry no run id) reads of its
// trace before it cuts the run's subtree out: 50,000, the first in
// start order. A trace with more is read that far and says so
// (RunTree.TraceTruncated). The subtree is cut from what was read,
// then capped at TreeSpanCap, so a run late in a large trace keeps its
// own spans while they fall inside the read.
const TreeTraceCap = 50_000

// TreeMissingCap bounds RunTree.Missing: the first 100 by start, the
// whole count in RunTree.MissingCount.
const TreeMissingCap = 100

// TreeRowCap bounds the run rows one RunTreeSpans read takes of a weft
// run's tree: 10,000, in RunRow order. A tree of more runs is read that
// far and says so (RunTree.Truncated, as past TreeSpanCap): the runs
// past it, and their spans, are not in the answer.
const TreeRowCap = 10_000

// ChildRunsCap bounds one TreeReader.ChildRuns call: 10,000 child rows
// in all, over every parent it names. A call whose parents hold more
// answers ErrChildRunsCapped and no map: the caller reads those
// parents' children as not known, never as a part.
const ChildRunsCap = 10_000

// ErrChildRunsCapped is ChildRuns' answer past ChildRunsCap.
var ErrChildRunsCapped = errors.New("obsdb: more child runs than ChildRunsCap")

// RunTree is TreeReader.RunTreeSpans' answer.
type RunTree struct {
	// Spans is the tree's spans in start order, then span id: at most
	// TreeSpanCap, the first ones.
	Spans []TreeSpan
	// Truncated says the tree holds more than TreeSpanCap spans: Spans
	// is its first TreeSpanCap.
	Truncated bool
	// TraceTruncated says a derived run's trace held more than
	// TreeTraceCap spans: the tree was cut from the trace's first
	// TreeTraceCap, so a span of it after them was not read. Never set
	// for a weft run (its spans are read by run id).
	TraceTruncated bool
	// Missing is the descendant runs the tree knows from their rows
	// that have no span in Spans and are not running — a child that
	// ran without a tracer, or whose spans a destination dropped —
	// ordered by Started, then id: at most TreeMissingCap, the first
	// ones. A running child (StatusRunning: spans export when they
	// end) is not missing yet. Empty when Truncated: a cut tree cannot
	// tell a missing run from one past the cap.
	Missing []TreeRun
	// MissingCount is how many runs are missing: Missing's length
	// before TreeMissingCap.
	MissingCount int
	// Hidden says TreeQuery's scope (PublicID, Meta) left runs out.
	Hidden bool
	// HiddenCount is how many runs it left out: each descendant run the
	// tree knows (from its rows' parents or its spans) that the scope
	// cut, its subtree's runs included. Never their ids. 0 unless Hidden.
	HiddenCount int
}

// TreeRun names one run of a tree: its id, its parent, its public id
// (weft.public_id, "" when none — what a scoped reader filters by),
// its metadata (RunRow.Meta: TreeQuery.Meta's scope reads it), when it
// started (RunRow.Started, the order Missing lists runs in) and its
// derived status (DeriveStatus with the read's clock: a running run is
// never Missing).
type TreeRun struct {
	ID, ParentRunID, PublicID string
	Meta                      map[string]string
	Started                   time.Time
	Status                    Status
}

// TreeCutParent is the parent a backend maps a run to in the parents
// it hands TreeMembers (and TreeReadIDs, CutRunTree, CapTree) when its
// read found the run but may not show it — a run past the backend's
// retention cut. No run has it, so the run is no member and neither is
// a run whose stored parent it is; and the id rule stops at it, so an
// id-shaped descendant with no stored parent stays out with it rather
// than joining the nearest member above (TreeMembers). A NUL byte
// leads it: no run id holds one.
const TreeCutParent = "\x00cut"

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
// <parent>/resume/<call id> under Approve), so an id of that shape
// under runID (TreeChildID: the step all digits or "resume", a
// non-empty call id) descends from runID; an app's own hierarchical
// id ("job/1", or "job/1/retry/2" under "job/1") is not of it. The
// parent is the nearest run above id that known holds and of which id
// has the shape — id trimmed at each "/" from the end, so a call id
// holding a "/" of its own still finds its run — else runID. ok is
// false for an id outside the subtree (runID itself included). The
// rule is a fallback for a run no row or span names a parent of
// (TreeMembers): a stored parent always wins over the id.
func TreeIDRule(runID, id string, known func(string) bool) (parent string, ok bool) {
	if !TreeChildID(runID, id) {
		return "", false
	}
	for p := id; ; {
		i := strings.LastIndexByte(p, '/')
		if i <= len(runID) {
			return runID, true
		}
		p = p[:i]
		if known(p) && TreeChildID(p, id) {
			return p, true
		}
	}
}

// TreeChildID reports whether id has the shape core gives a child run
// of parent (core's childRunID): parent + "/" + a step (one or more
// ASCII digits) or "resume", then "/" + a non-empty call id.
func TreeChildID(parent, id string) bool {
	rest, ok := strings.CutPrefix(id, parent+"/")
	if !ok {
		return false
	}
	seg, call, ok := strings.Cut(rest, "/")
	if !ok || call == "" || seg == "" {
		return false
	}
	if seg == "resume" {
		return true
	}
	for i := 0; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return false
		}
	}
	return true
}

// TreeMembers is runID's subtree from what a backend knows before it
// reads a span: parents maps each run it found — the rows its
// parent-chain and id-rule reads returned, the trace's runs with no
// row (their spans' weft.parent.run.id, "" when none) — to its parent.
// A run is in the subtree when its parent chain reaches runID, or,
// when no row or span names its parent (mapped to ""), when the id
// rule names it a descendant (TreeIDRule; its parent then the rule's):
// a stored parent outside the subtree wins over the id. The rule's
// parent is a member, never merely a run the backend found: the
// nearest run above the id that is (or becomes) a member, so an app's
// own "j/1" (no member: not of core's shape, no stored parent) leaves
// "j/1/0/c" to j — whichever superset of rows a backend read. The
// answer maps every member, runID included, to its parent; a parent
// chain that loops (never written by the core) ends where it repeats.
// A run mapped to TreeCutParent is found but cut: never a member, and
// the id rule stops at it — a descendant whose nearest run is a cut one
// stays out, never re-attached to a member above it.
func TreeMembers(runID string, parents map[string]string) map[string]string {
	known := func(id string) bool { _, ok := parents[id]; return ok || id == runID }
	in := map[string]string{runID: parents[runID]}
	member := func(id string) bool { _, ok := in[id]; return ok }
	// memberOrCut is the last pass's rule: the nearest member above an
	// id, unless a cut run sits nearer.
	memberOrCut := func(id string) bool { return member(id) || (id != runID && parents[id] == TreeCutParent) }
	// The id rule's candidates, shortest first: a parent's id is a
	// prefix of its child's, so it is placed before them.
	var rule []string
	for id, stored := range parents {
		if stored == "" && TreeChildID(runID, id) {
			rule = append(rule, id)
		}
	}
	slices.SortFunc(rule, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
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
		// A candidate whose nearest found run is a member takes it; one
		// whose nearest found run is not a member yet waits a round
		// (that run may still join by its stored parent).
		for _, id := range rule {
			if member(id) {
				continue
			}
			if p, ok := TreeIDRule(runID, id, known); ok && member(p) {
				in[id], grew = p, true
			}
		}
		if grew {
			continue
		}
		// What still waits has a non-member nearest run: its parent is
		// the nearest member above it instead — unless a cut run is
		// nearer, which holds it out.
		for _, id := range rule {
			if member(id) {
				continue
			}
			if p, ok := TreeIDRule(runID, id, memberOrCut); ok && member(p) {
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
// from its derivation's ParentRunID; a run none of those names a
// parent of is in the subtree when the id rule names it a descendant
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
// order — a weft run's by member id (TreeReadIDs), at most
// TreeSpanCap + 1, more true when it read that many; a derived run's
// from its trace, more false; parents is CutRunTree's; rows is the
// member runs it read a row of. The answer is the read cut to q's
// subtree, then to q's scope (TreeQuery.PublicID, Meta), then to TreeSpanCap
// — so a run the scope leaves out never takes a visible one's place —
// and Missing: each of the visible rows but the root's that is a
// member (TreeMembers, its parent the members'), not running, with no
// span in the answer, ordered by Started, then id, the first
// TreeMissingCap. Missing stays empty on a cut tree.
func CapTree(read []Span, more bool, q TreeQuery, parents map[string]string, rows []TreeRun) RunTree {
	spans := CutRunTree(read, q.RunID, parents)
	members := TreeMembers(q.RunID, parents)
	out := RunTree{}
	if q.Scoped() {
		spans, rows, out.HiddenCount = scopeTree(q, spans, members, rows)
		out.Hidden = out.HiddenCount > 0
	}
	if more || len(spans) > caps.TreeSpan {
		out.Spans, out.Truncated = spans[:min(len(spans), caps.TreeSpan)], true
		return out
	}
	out.Spans = spans
	seen := make(map[string]bool, len(rows))
	for _, s := range out.Spans {
		seen[s.RunID] = true
	}
	for _, r := range rows {
		if p, ok := members[r.ID]; ok && r.ID != q.RunID && !seen[r.ID] && r.Status != StatusRunning {
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
	out.MissingCount = len(out.Missing)
	out.Missing = out.Missing[:min(len(out.Missing), caps.TreeMissing)]
	return out
}

// TreeReadIDs is the run ids a backend reads a weft run tree's spans
// by: TreeMembers' ids, sorted, and under a scope (TreeQuery.PublicID,
// Meta) only the members the rows show visible — the root, and each
// run whose row carries the scope under a visible parent — so a run
// the scope leaves out is never read and never takes a visible run's
// place under the read's limit. A member with no row is read: its
// spans decide (CapTree).
func TreeReadIDs(q TreeQuery, parents map[string]string, rows []TreeRun) []string {
	members := TreeMembers(q.RunID, parents)
	byID := make(map[string]TreeRun, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	visible := treeVisible(q.RunID, members, func(id string) bool {
		r, ok := byID[id]
		return !ok || q.admitsRow(r)
	})
	ids := make([]string, 0, len(members))
	for id := range members {
		if !q.Scoped() || visible(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// treeVisible is the scope's walk: a run is visible when it is root,
// or when own holds for it and its parent (parent[id]) is visible; a
// loop never reaches the root.
func treeVisible(root string, parent map[string]string, own func(string) bool) func(string) bool {
	memo := map[string]bool{root: true}
	var visible func(id string) bool
	visible = func(id string) bool {
		if v, ok := memo[id]; ok {
			return v
		}
		memo[id] = false // a loop never reaches the root
		p, known := parent[id]
		v := known && own(id) && visible(p)
		memo[id] = v
		return v
	}
	return visible
}

// scopeTree cuts a run tree to q's scope (PublicID, Meta): the root's
// spans stay, and a descendant run stays when its row (when it has one)
// and each of its spans carry the scope and its parent run stays; any other
// run is left out with its whole subtree, counted: the int is how many
// runs (members or span runs) it left out. The rows answered are the
// visible members'.
func scopeTree(q TreeQuery, spans []TreeSpan, members map[string]string, rows []TreeRun) ([]TreeSpan, []TreeRun, int) {
	parent := make(map[string]string, len(members))
	for id, p := range members {
		parent[id] = p
	}
	foreign := map[string]bool{}
	for _, sp := range spans {
		parent[sp.RunID] = sp.ParentRunID
		if sp.RunID != q.RunID && !q.admitsSpan(sp.Attrs) {
			foreign[sp.RunID] = true
		}
	}
	for _, r := range rows {
		if _, ok := members[r.ID]; ok && r.ID != q.RunID && !q.admitsRow(r) {
			foreign[r.ID] = true
		}
	}
	visible := treeVisible(q.RunID, parent, func(id string) bool { return !foreign[id] })
	hidden := map[string]bool{}
	for id := range members {
		if id != q.RunID && !visible(id) {
			hidden[id] = true
		}
	}
	kept := make([]TreeSpan, 0, len(spans))
	for _, sp := range spans {
		if visible(sp.RunID) {
			kept = append(kept, sp)
		} else {
			hidden[sp.RunID] = true
		}
	}
	var runs []TreeRun
	for _, r := range rows {
		if _, ok := members[r.ID]; !ok {
			continue
		}
		if visible(r.ID) {
			runs = append(runs, r)
		} else {
			hidden[r.ID] = true
		}
	}
	return kept, runs, len(hidden)
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
