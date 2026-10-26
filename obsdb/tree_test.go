package obsdb_test

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

func treeTestSpan(id, parentSpan string, attrs map[string]any) obsdb.Span {
	return obsdb.Span{TraceID: "t1", SpanID: id, ParentSpanID: parentSpan, Name: id, Attrs: attrs}
}

// TestCutRunTree (U9.1): the cut follows the stored parents first and
// the spans' weft.parent.run.id where a run has no row, takes derived
// runs (§9) by their derivation's parent, leaves out sibling runs and
// run-less spans, keeps the input order, and ends a parent loop.
func TestCutRunTree(t *testing.T) {
	spans := []obsdb.Span{
		treeTestSpan("s1", "", map[string]any{"weft.run.id": "root"}),
		treeTestSpan("s2", "s1", map[string]any{"weft.run.id": "kid", "weft.parent.run.id": "root"}),
		treeTestSpan("s3", "s2", map[string]any{"weft.run.id": "grand"}), // parent from the row only
		treeTestSpan("s4", "", map[string]any{"weft.run.id": "other"}),
		treeTestSpan("s5", "", map[string]any{"http.method": "GET"}),
		treeTestSpan("s6", "", map[string]any{"weft.run.id": "loop_a"}),
		treeTestSpan("s7", "", map[string]any{"weft.run.id": "loop_b"}),
	}
	parents := map[string]string{"grand": "kid", "loop_a": "loop_b", "loop_b": "loop_a", "other": ""}
	ids := func(tree []obsdb.TreeSpan) []string {
		var out []string
		for _, s := range tree {
			out = append(out, s.RunID+"/"+s.SpanID+"<"+s.ParentRunID)
		}
		return out
	}
	if got, want := ids(obsdb.CutRunTree(spans, "root", parents)), []string{"root/s1<", "kid/s2<root", "grand/s3<kid"}; !slices.Equal(got, want) {
		t.Errorf("CutRunTree(root) = %v, want %v", got, want)
	}
	if got, want := ids(obsdb.CutRunTree(spans, "kid", parents)), []string{"kid/s2<root", "grand/s3<kid"}; !slices.Equal(got, want) {
		t.Errorf("CutRunTree(kid) = %v, want %v", got, want)
	}
	if got, want := ids(obsdb.CutRunTree(spans, "loop_a", parents)), []string{"loop_a/s6<loop_b", "loop_b/s7<loop_a"}; !slices.Equal(got, want) {
		t.Errorf("CutRunTree(loop_a) = %v, want %v (a loop ends where it repeats)", got, want)
	}
	if got := obsdb.CutRunTree(nil, "root", nil); got == nil || len(got) != 0 {
		t.Errorf("CutRunTree(nil) = %#v, want empty, non-nil", got)
	}
	// The id rule (U9.1-fix): a run no row, span or derivation places —
	// a running child whose run_start was lost, its spans carrying no
	// weft.parent.run.id, a row with no parent — is in the tree when
	// core's id names it a descendant, under its nearest known ancestor
	// (a call id holding "/" included). A run whose span or row names
	// a parent outside the tree is that parent's, whatever its id says
	// (U9.1-fix2), and an id of no core child shape is never placed.
	idRule := []obsdb.Span{
		treeTestSpan("r1", "", map[string]any{"weft.run.id": "root"}),
		treeTestSpan("r2", "r1", map[string]any{"weft.run.id": "root/0/call_a"}),
		treeTestSpan("r3", "r2", map[string]any{"weft.run.id": "root/0/call_a/1/call_b"}),
		treeTestSpan("r4", "r1", map[string]any{"weft.run.id": "root/0/call/with/slash"}),
		treeTestSpan("r5", "r1", map[string]any{"weft.run.id": "root/resume/call_c", "weft.parent.run.id": "elsewhere"}),
		treeTestSpan("r6", "", map[string]any{"weft.run.id": "rootish/0/call_x"}),
		treeTestSpan("r7", "", map[string]any{"weft.run.id": "root/1"}),
		treeTestSpan("r8", "", map[string]any{"weft.run.id": "root/retry/2"}),
	}
	if got, want := ids(obsdb.CutRunTree(idRule, "root", nil)), []string{
		"root/r1<", "root/0/call_a/r2<root", "root/0/call_a/1/call_b/r3<root/0/call_a",
		"root/0/call/with/slash/r4<root",
	}; !slices.Equal(got, want) {
		t.Errorf("CutRunTree(id rule) = %v, want %v", got, want)
	}
	known := func(id string) bool { return id == "a/0/c" }
	for _, c := range []struct {
		id, parent string
		ok         bool
	}{
		{"a/0/c", "a", true}, {"a/0/c/1/d", "a/0/c", true}, {"a/0/c/resume/d", "a/0/c", true},
		{"a/0/c/x/1/d", "a", true}, // a/0/c's child would be a/0/c/<step>/…: the call id is "c/x/1/d"
		{"a/1/e", "a", true}, {"a/resume/e", "a", true}, {"a", "", false}, {"ab/0/c", "", false}, {"b/0/c", "", false},
		// U9.1-fix2: an app's own hierarchical ids are not core's shape.
		{"a/1", "", false}, {"a/retry/2", "", false}, {"a/0/", "", false}, {"a//c", "", false}, {"a/1x/c", "", false},
	} {
		if p, ok := obsdb.TreeIDRule("a", c.id, known); p != c.parent || ok != c.ok {
			t.Errorf("TreeIDRule(a, %s) = %q, %v; want %q, %v", c.id, p, ok, c.parent, c.ok)
		}
	}
	// "job/1" and "job/1/retry/2" (an app's ids, no core shape under
	// "job/1"), and "root/0/claimed" whose stored parent is outside the
	// tree: none is root's or job/1's.
	if got := obsdb.TreeMemberIDs("job/1", map[string]string{"job/1": "", "job/1/retry/2": ""}); !slices.Equal(got, []string{"job/1"}) {
		t.Errorf("TreeMemberIDs(job/1) = %v, want job/1 alone (retry/2 is no core child id)", got)
	}
	if got, want := obsdb.TreeMemberIDs("root", map[string]string{
		"root": "", "kid": "root", "grand": "kid", "root/0/c": "", "other": "", "loop_a": "loop_b", "loop_b": "loop_a",
		"root/0/claimed": "other", "root/7": "",
	}), []string{"grand", "kid", "root", "root/0/c"}; !slices.Equal(got, want) {
		t.Errorf("TreeMemberIDs = %v, want %v", got, want)
	}

	// CapTree: past the cap the first TreeSpanCap, Truncated, no
	// Missing; under it, Missing names each finished member row with no
	// span — a running one is not missing yet (U9.1-fix2).
	q := obsdb.TreeQuery{RunID: "root"}
	read := make([]obsdb.Span, obsdb.TreeSpanCap+1)
	for i := range read {
		read[i] = treeTestSpan(fmt.Sprintf("c%05d", i), "", map[string]any{"weft.run.id": "root"})
	}
	rows := []obsdb.TreeRun{{ID: "root"}, {ID: "root/0/a", PublicID: "pub", Status: obsdb.StatusSucceeded},
		{ID: "root/0/live", Status: obsdb.StatusRunning}, {ID: "stray", ParentRunID: "x"}}
	capParents := map[string]string{"root": "", "root/0/a": "root", "root/0/live": "root", "stray": "x"}
	if got := obsdb.CapTree(read, true, q, capParents, rows); !got.Truncated || len(got.Spans) != obsdb.TreeSpanCap || len(got.Missing) != 0 {
		t.Errorf("CapTree(cap+1) = truncated %v, %d spans, missing %v; want truncated at the cap", got.Truncated, len(got.Spans), got.Missing)
	}
	got := obsdb.CapTree(read[:2], false, q, capParents, rows)
	if want := []obsdb.TreeRun{{ID: "root/0/a", ParentRunID: "root", PublicID: "pub", Status: obsdb.StatusSucceeded}}; got.Truncated ||
		len(got.Spans) != 2 || !reflect.DeepEqual(got.Missing, want) || got.MissingCount != 1 {
		t.Errorf("CapTree(2) = truncated %v, %d spans, missing %+v (%d); want 2 spans, missing %+v", got.Truncated, len(got.Spans), got.Missing, got.MissingCount, want)
	}

	// Missing is bounded: the first TreeMissingCap by start, the count
	// whole (U9.1-fix2).
	manyRows := []obsdb.TreeRun{{ID: "root"}}
	manyParents := map[string]string{"root": ""}
	for i := range obsdb.TreeMissingCap + 5 {
		id := fmt.Sprintf("root/0/m%03d", i)
		manyRows = append(manyRows, obsdb.TreeRun{ID: id, ParentRunID: "root", Status: obsdb.StatusFailed})
		manyParents[id] = "root"
	}
	if got := obsdb.CapTree(read[:1], false, q, manyParents, manyRows); len(got.Missing) != obsdb.TreeMissingCap ||
		got.MissingCount != obsdb.TreeMissingCap+5 || got.Missing[0].ID != "root/0/m000" {
		t.Errorf("CapTree(105 missing) = %d named, count %d; want %d named, count %d", len(got.Missing), got.MissingCount, obsdb.TreeMissingCap, obsdb.TreeMissingCap+5)
	}

	// The scope cut comes before the cap (U9.1-fix2): a hidden run's
	// spans never take a visible one's place, nor make the tree read
	// truncated while the visible tree fits; a hidden run is not read.
	scoped := obsdb.TreeQuery{RunID: "root", PublicID: "pub"}
	scopeRows := []obsdb.TreeRun{{ID: "root", PublicID: "pub"}, {ID: "root/0/mine", PublicID: "pub", Status: obsdb.StatusSucceeded},
		{ID: "root/0/theirs", PublicID: "other", Status: obsdb.StatusSucceeded}, {ID: "root/0/theirs/0/kid", PublicID: "pub"}}
	scopeParents := map[string]string{"root": "", "root/0/mine": "root", "root/0/theirs": "root", "root/0/theirs/0/kid": "root/0/theirs"}
	if ids := obsdb.TreeReadIDs(scoped, scopeParents, scopeRows); !slices.Equal(ids, []string{"root", "root/0/mine"}) {
		t.Errorf("TreeReadIDs(scoped) = %v, want root and its own child (theirs and theirs' kid never read)", ids)
	}
	if ids := obsdb.TreeReadIDs(q, scopeParents, scopeRows); len(ids) != 4 {
		t.Errorf("TreeReadIDs(unscoped) = %v, want every member", ids)
	}
	mixed := []obsdb.Span{treeTestSpan("m0", "", map[string]any{"weft.run.id": "root"})}
	for i := range obsdb.TreeSpanCap {
		mixed = append(mixed, treeTestSpan(fmt.Sprintf("t%05d", i), "", map[string]any{"weft.run.id": "root/0/theirs", "weft.public_id": "other"}))
	}
	mixed = append(mixed, treeTestSpan("z1", "", map[string]any{"weft.run.id": "root/0/mine", "weft.public_id": "pub"}))
	sc := obsdb.CapTree(mixed, false, scoped, scopeParents, scopeRows)
	if sc.Truncated || len(sc.Spans) != 2 || sc.Spans[1].RunID != "root/0/mine" || !sc.Hidden || sc.MissingCount != 0 {
		t.Errorf("CapTree(scoped) = truncated %v, %d spans, hidden %v, missing %d; want root's and mine's 2 spans, hidden, none missing",
			sc.Truncated, len(sc.Spans), sc.Hidden, sc.MissingCount)
	}
	if sc.HiddenCount != 2 {
		t.Errorf("CapTree(scoped).HiddenCount = %d, want 2 (theirs and its kid)", sc.HiddenCount)
	}
	if un := obsdb.CapTree(mixed, false, q, scopeParents, scopeRows); !un.Truncated || un.Hidden || un.HiddenCount != 0 {
		t.Errorf("CapTree(unscoped) = truncated %v, hidden %v; want truncated, not hidden", un.Truncated, un.Hidden)
	}

	// A derived run (§9) nested under another: its spans are its own,
	// its parent its derivation's.
	stock := []obsdb.Span{
		treeTestSpan("a1", "", map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "c"}),
		treeTestSpan("a2", "a1", map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.call.id": "call_1"}),
		treeTestSpan("a3", "a2", map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "c"}),
		treeTestSpan("a4", "a3", map[string]any{"gen_ai.operation.name": "chat"}),
	}
	runs := obsdb.DeriveStockRuns(stock)
	if len(runs) != 2 {
		t.Fatalf("DeriveStockRuns = %d runs, want 2", len(runs))
	}
	outer, inner := runs[0].Row.ID, runs[1].Row.ID
	if runs[1].Row.ParentRunID != outer {
		outer, inner = inner, outer
	}
	if got, want := ids(obsdb.CutRunTree(stock, outer, nil)), []string{outer + "/a1<", outer + "/a2<", inner + "/a3<" + outer, inner + "/a4<" + outer}; !slices.Equal(got, want) {
		t.Errorf("CutRunTree(derived outer) = %v, want %v", got, want)
	}
}

// A run tree: the spans of a trace cut to one run's subtree, each keyed
// by its run and that run's parent. The run rows' parents come in
// beside the spans (a backend reads them in the same query); a run
// without a row takes its parent from its root span's
// weft.parent.run.id.
func ExampleCutRunTree() {
	spans := []obsdb.Span{
		{TraceID: "t1", SpanID: "01", Name: "invoke_agent director", Attrs: map[string]any{"weft.run.id": "d"}},
		{TraceID: "t1", SpanID: "02", ParentSpanID: "01", Name: "execute_tool ops", Attrs: map[string]any{"weft.run.id": "d"}},
		{TraceID: "t1", SpanID: "03", ParentSpanID: "02", Name: "invoke_agent ops",
			Attrs: map[string]any{"weft.run.id": "d/0/c1", "weft.parent.run.id": "d"}},
		{TraceID: "t1", SpanID: "04", ParentSpanID: "03", Name: "chat", Attrs: map[string]any{"weft.run.id": "d/0/c1/0/c2"}},
		{TraceID: "t1", SpanID: "05", Name: "invoke_agent sibling", Attrs: map[string]any{"weft.run.id": "s"}},
	}
	parents := map[string]string{"d/0/c1/0/c2": "d/0/c1"} // the worker's run row
	for _, s := range obsdb.CutRunTree(spans, "d", parents) {
		fmt.Printf("%s %s (run %s, parent %q)\n", s.SpanID, s.Name, s.RunID, s.ParentRunID)
	}
	// Output:
	// 01 invoke_agent director (run d, parent "")
	// 02 execute_tool ops (run d, parent "")
	// 03 invoke_agent ops (run d/0/c1, parent "d")
	// 04 chat (run d/0/c1/0/c2, parent "d/0/c1")
}

// A run tree's members from the rows alone: the parent chain first,
// then core's id rule for a run no chain places — here a grandchild
// whose run_start was lost (its row names no parent) and a nested turn
// whose parent chain leads elsewhere. A backend reads the members'
// spans by run id after this.
func ExampleTreeMembers() {
	parents := map[string]string{
		"d":             "",
		"d/0/c1":        "d",
		"d/0/c1/1/c2":   "", // a row with no parent: the id rule places it
		"nested_turn":   "d/0/c1",
		"sibling":       "",
		"elsewhere/0/x": "elsewhere",
	}
	for _, id := range obsdb.TreeMemberIDs("d", parents) {
		p, _ := obsdb.TreeIDRule("d", id, func(s string) bool { _, ok := parents[s]; return ok })
		fmt.Printf("%s (parent %q, id rule %q)\n", id, obsdb.TreeMembers("d", parents)[id], p)
	}
	// Output:
	// d (parent "", id rule "")
	// d/0/c1 (parent "d", id rule "d")
	// d/0/c1/1/c2 (parent "d/0/c1", id rule "d/0/c1")
	// nested_turn (parent "d/0/c1", id rule "")
}

// TestTreeMembersStopAtACutRun (P9-pre-go-fix4): a run a backend found
// but cut (mapped to obsdb.TreeCutParent) is no member, and neither is
// any descendant of it — one whose row names it, and one of core's id
// shape under it whose row names no parent, which the id rule would
// otherwise re-attach to the root above the cut run. A deeper id-shaped
// run under a stored-parent descendant stays out too. With the child
// kept (no sentinel: SQLite's read, which cuts nothing) all of them are
// members, the id-only one under the child.
func TestTreeMembersStopAtACutRun(t *testing.T) {
	const root, child = "tr_e", "tr_e/1/call_a"
	byRow, byID, deeper := child+"/0/call_b", child+"/0/call_c", child+"/0/call_b/2/call_d"
	sibling := root + "/2/call_z"
	parents := map[string]string{
		root: "", child: obsdb.TreeCutParent,
		byRow: child, byID: "", deeper: "", sibling: "",
	}
	got := obsdb.TreeMembers(root, parents)
	want := map[string]string{root: "", sibling: root}
	if !maps.Equal(got, want) {
		t.Errorf("members with the child cut = %v, want %v", got, want)
	}
	if ids := obsdb.TreeReadIDs(obsdb.TreeQuery{RunID: root}, parents, nil); !slices.Equal(ids, []string{root, sibling}) {
		t.Errorf("TreeReadIDs = %v, want the root and its kept sibling", ids)
	}
	parents[child] = root
	got = obsdb.TreeMembers(root, parents)
	want = map[string]string{root: "", child: root, byRow: child, byID: child, deeper: byRow, sibling: root}
	if !maps.Equal(got, want) {
		t.Errorf("members with the child kept = %v, want %v", got, want)
	}
}
