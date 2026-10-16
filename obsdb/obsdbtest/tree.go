package obsdbtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The run tree (U9.1, obsdb.TreeReader): a run's spans with every
// descendant run's, keyed by run id and span id, cut from one trace by
// the parent chain; and the children of many runs in one read. A
// backend that does not implement it is skipped: the capability is
// optional.

// treeTrace is the fixture's one trace: an app-level trace holding a
// director's tree and an unrelated sibling run.
const treeTrace = "0102030405060708090a0b0c0d0e0f20"

// treeSpan is one span of the fixture's trace, at offset ms.
func treeSpan(spanID, parentSpan, name, runID, parentRun string, ms int) obsdb.Span {
	attrs := map[string]any{"gen_ai.operation.name": "chat"}
	if runID != "" {
		attrs["weft.run.id"] = runID
	}
	if parentRun != "" {
		attrs["gen_ai.operation.name"] = "invoke_agent"
		attrs["weft.parent.run.id"] = parentRun
	}
	return obsdb.Span{
		TraceID: treeTrace, SpanID: spanID, ParentSpanID: parentSpan, Name: name, Kind: 1,
		Start: at(time.Duration(ms) * time.Millisecond), End: at(time.Duration(ms+5) * time.Millisecond),
		StatusCode: 1, Service: "tree-svc", Attrs: attrs,
		Resource: map[string]any{"service.name": "tree-svc"},
	}
}

func runTree(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		tr, ok := db.(obsdb.TreeReader)
		if !ok {
			t.Skip("backend does not implement obsdb.TreeReader")
		}
		// director › ops (a Subagent child) › worker (a grandchild, still
		// running: only its chat span is stored, so its parent comes from
		// its run row's run_start); app (a sibling run in the same app
		// trace, never in the director's tree); a span of no run.
		start := func(id, parent string) obsdb.Record {
			r := record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`,
				map[string]any{"weft.session.id": ""})
			r.TraceID = treeTrace
			if parent != "" {
				r.Attrs["weft.parent.run.id"] = parent
				r.Attrs["weft.parent.call.id"] = "call_1"
			}
			return r
		}
		const (
			director = "tree_director"
			ops      = "tree_director/0/call_1"
			worker   = "tree_director/0/call_1/0/call_1"
			app      = "tree_app"
		)
		if err := db.Write(ctx(), obsdb.Batch{
			Spans: []obsdb.Span{
				treeSpan("0b00000000000001", "", "invoke_agent director", director, "", 0),
				treeSpan("0b00000000000002", "0b00000000000001", "chat", director, "", 1),
				treeSpan("0b00000000000003", "0b00000000000001", "execute_tool ops", director, "", 2),
				treeSpan("0b00000000000004", "0b00000000000003", "invoke_agent ops", ops, director, 3),
				treeSpan("0b00000000000005", "0b00000000000004", "chat", ops, "", 4),
				treeSpan("0b00000000000006", "0b00000000000004", "execute_tool worker", ops, "", 5),
				treeSpan("0b00000000000007", "0b00000000000006", "chat", worker, "", 6),
				treeSpan("0b00000000000008", "", "invoke_agent app", app, "", 7),
				treeSpan("0b00000000000009", "", "GET /orders", "", "", 8),
			},
			Records: []obsdb.Record{start(director, ""), start(ops, director), start(worker, ops), start(app, "")},
		}); err != nil {
			t.Fatal(err)
		}

		type key struct{ run, span, parent string }
		keys := func(tree []obsdb.TreeSpan) []key {
			out := []key{}
			for _, s := range tree {
				out = append(out, key{s.RunID, s.SpanID, s.ParentRunID})
			}
			return out
		}
		full, err := tr.RunTreeSpans(ctx(), director)
		if err != nil {
			t.Fatal(err)
		}
		tree := full.Spans
		if full.Truncated || len(full.Missing) != 0 {
			t.Errorf("RunTreeSpans(director) = truncated %v, missing %v; want neither", full.Truncated, full.Missing)
		}
		want := []key{
			{director, "0b00000000000001", ""}, {director, "0b00000000000002", ""}, {director, "0b00000000000003", ""},
			{ops, "0b00000000000004", director}, {ops, "0b00000000000005", director}, {ops, "0b00000000000006", director},
			{worker, "0b00000000000007", ops},
		}
		if got := keys(tree); !slices.Equal(got, want) {
			t.Errorf("RunTreeSpans(director) = %v,\nwant %v (the descendants in start order, no sibling run, no run-less span)", got, want)
		}
		if len(tree) > 0 && (tree[0].Name != "invoke_agent director" || tree[0].Attrs["weft.run.id"] != director || tree[0].TraceID != treeTrace) {
			t.Errorf("RunTreeSpans' span = %+v, want the stored span whole", tree[0].Span)
		}
		if tree, err := tr.RunTreeSpans(ctx(), ops); err != nil || !slices.Equal(keys(tree.Spans), want[3:]) {
			t.Errorf("RunTreeSpans(ops) = %v, %v; want ops and worker alone", keys(tree.Spans), err)
		}
		if tree, err := tr.RunTreeSpans(ctx(), app); err != nil || len(tree.Spans) != 1 || tree.Spans[0].RunID != app {
			t.Errorf("RunTreeSpans(app) = %v, %v; want its own span alone", keys(tree.Spans), err)
		}
		// The descendants the parent chain alone cannot place (U9.1-fix):
		// a Subagent child whose run_start was lost (only its chat span,
		// no weft.parent.run.id — its row names no parent; the id rule
		// places it), a nested Generate inside a tool handler (its own id,
		// its invoke_agent's weft.parent.run.id, another public id), that
		// run's child stored under another trace (its row's parent), and
		// a child with a row and no span (Missing, with its public id).
		const (
			deepTrace = "0102030405060708090a0b0c0d0e0f21"
			farTrace  = "0102030405060708090a0b0c0d0e0f22"
			deep      = "tree_deep"
			lost      = "tree_deep/0/call_a"
			nested    = "tree_nested_turn"
			far       = "tree_nested_turn/0/call_n"
			bare      = "tree_deep/1/call_b"
		)
		inTrace := func(sp obsdb.Span, trace, pub string) obsdb.Span {
			sp.TraceID = trace
			if pub != "" {
				sp.Attrs["weft.public_id"] = pub
			}
			return sp
		}
		deepStart := func(id, parent, pub string, pos int64) obsdb.Record {
			r := record(id, "event", "run_start", pos, `{"type":"run_start","id":"`+id+`"}`,
				map[string]any{"weft.session.id": "", "weft.public_id": pub})
			r.TraceID = deepTrace
			if parent != "" {
				r.Attrs["weft.parent.run.id"] = parent
			}
			return r
		}
		if err := db.Write(ctx(), obsdb.Batch{
			Spans: []obsdb.Span{
				inTrace(treeSpan("0c00000000000001", "", "invoke_agent deep", deep, "", 0), deepTrace, "pub_t"),
				inTrace(treeSpan("0c00000000000002", "0c00000000000001", "chat", lost, "", 1), deepTrace, "pub_t"),
				inTrace(treeSpan("0c00000000000003", "0c00000000000001", "invoke_agent nested", nested, deep, 2), deepTrace, "pub_other"),
				inTrace(treeSpan("0c00000000000004", "", "invoke_agent far", far, "", 3), farTrace, "pub_other"),
			},
			Records: []obsdb.Record{deepStart(deep, "", "pub_t", 0), deepStart(far, nested, "pub_other", 1),
				deepStart(bare, deep, "pub_t", 2)},
		}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.RunTreeSpans(ctx(), deep)
		if err != nil {
			t.Fatal(err)
		}
		wantDeep := []key{
			{deep, "0c00000000000001", ""}, {lost, "0c00000000000002", deep},
			{nested, "0c00000000000003", deep}, {far, "0c00000000000004", nested},
		}
		if !slices.Equal(keys(got.Spans), wantDeep) {
			t.Errorf("RunTreeSpans(deep) = %v,\nwant %v (the id rule's child, the nested turn, its child in another trace)", keys(got.Spans), wantDeep)
		}
		if want := []obsdb.TreeRun{{ID: bare, ParentRunID: deep, PublicID: "pub_t", Started: at(2 * time.Second)}}; got.Truncated ||
			len(got.Missing) != 1 || got.Missing[0].ID != want[0].ID || got.Missing[0].ParentRunID != want[0].ParentRunID ||
			got.Missing[0].PublicID != want[0].PublicID || !got.Missing[0].Started.Equal(want[0].Started) {
			t.Errorf("RunTreeSpans(deep) = truncated %v, missing %+v; want missing %+v", got.Truncated, got.Missing, want)
		}

		// The cap: a tree of more than obsdb.TreeSpanCap spans answers its
		// first TreeSpanCap in start order and says it holds more.
		const big = "tree_big"
		many := make([]obsdb.Span, 0, obsdb.TreeSpanCap+1)
		for i := range obsdb.TreeSpanCap + 1 {
			sp := treeSpan(fmt.Sprintf("0d%014x", i), "", "chat", big, "", 10+i)
			sp.TraceID = "0102030405060708090a0b0c0d0e0f23"
			many = append(many, sp)
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: many}); err != nil {
			t.Fatal(err)
		}
		capped, err := tr.RunTreeSpans(ctx(), big)
		if err != nil {
			t.Fatal(err)
		}
		if !capped.Truncated || len(capped.Spans) != obsdb.TreeSpanCap || capped.Spans[0].SpanID != many[0].SpanID ||
			capped.Spans[obsdb.TreeSpanCap-1].SpanID != many[obsdb.TreeSpanCap-1].SpanID || len(capped.Missing) != 0 {
			t.Errorf("RunTreeSpans(big) = truncated %v, %d spans, missing %v; want truncated, the first %d",
				capped.Truncated, len(capped.Spans), capped.Missing, obsdb.TreeSpanCap)
		}

		// A run with a row and no span: an empty tree, not an error.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{record("tree_bare", "event", "run_start", 0,
			`{"type":"run_start","id":"tree_bare"}`, nil)}}); err != nil {
			t.Fatal(err)
		}
		if tree, err := tr.RunTreeSpans(ctx(), "tree_bare"); err != nil || len(tree.Spans) != 0 || len(tree.Missing) != 0 {
			t.Errorf("RunTreeSpans(a run with no span) = %v, %v; want empty", tree, err)
		}
		if _, err := tr.RunTreeSpans(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("RunTreeSpans(unknown) = %v, want ErrNotFound", err)
		}

		// ChildRuns: two parents' children in one read, each list in
		// Children's order; a parent with none absent; Run's answer.
		kids, err := tr.ChildRuns(ctx(), []string{director, ops, app, "nope"})
		if err != nil {
			t.Fatal(err)
		}
		if len(kids) != 2 || len(kids[director]) != 1 || kids[director][0].ID != ops ||
			len(kids[ops]) != 1 || kids[ops][0].ID != worker || kids[ops][0].ParentCallID != "call_1" {
			t.Errorf("ChildRuns = %v, want director → ops, ops → worker", kids)
		}
		det, err := db.Run(ctx(), director)
		if err != nil {
			t.Fatal(err)
		}
		if len(det.Children) != 1 || len(kids[director]) != 1 || det.Children[0].EventCount != kids[director][0].EventCount ||
			det.Children[0].Status != kids[director][0].Status || det.Children[0].Started != kids[director][0].Started {
			t.Errorf("ChildRuns(director) = %+v, want Run's Children %+v", kids[director], det.Children)
		}
		if none, err := tr.ChildRuns(ctx(), nil); err != nil || len(none) != 0 {
			t.Errorf("ChildRuns(nil) = %v, %v; want empty", none, err)
		}

		dead, cancel := context.WithCancel(ctx())
		cancel()
		if _, err := tr.RunTreeSpans(dead, director); err == nil || errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("RunTreeSpans on a cancelled context = %v, want the read's own error", err)
		}
		if _, err := tr.ChildRuns(dead, []string{director}); err == nil {
			t.Errorf("ChildRuns on a cancelled context = %v, want the read's own error", err)
		}
	}
}
