package obsdb_test

import (
	"fmt"
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
	if got, want := obsdb.TreeRunIDs(spans), []string{"root", "kid", "grand", "other", "loop_a", "loop_b"}; !slices.Equal(got, want) {
		t.Errorf("TreeRunIDs = %v, want %v", got, want)
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
