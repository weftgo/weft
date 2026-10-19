package obsdbtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/caps"
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

// lowerCaps sets each bound to its value for the rest of t (the table
// runs its cases one at a time: no read runs beside the change).
func lowerCaps(t *testing.T, to map[*int]int) {
	t.Helper()
	for v, n := range to {
		t.Cleanup(caps.Lower(v, n))
	}
}

func runTree(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		tr, ok := db.(obsdb.TreeReader)
		if !ok {
			t.Skip("backend does not implement obsdb.TreeReader")
		}
		// The caps' cases run over lowered caps (internal/caps), so a
		// past-the-cap tree is tens of rows, not tens of thousands; the
		// ordering the cases rely on holds as released (the trace cap
		// above the span cap and its noise). caps_test.go pins the
		// released values, tree_test.go CapTree at them.
		lowerCaps(t, map[*int]int{&caps.TreeSpan: 10, &caps.TreeTrace: 50, &caps.TreeRow: 10, &caps.ChildRuns: 10})
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
		full, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: director})
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
		if tree, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: ops}); err != nil || !slices.Equal(keys(tree.Spans), want[3:]) {
			t.Errorf("RunTreeSpans(ops) = %v, %v; want ops and worker alone", keys(tree.Spans), err)
		}
		if tree, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: app}); err != nil || len(tree.Spans) != 1 || tree.Spans[0].RunID != app {
			t.Errorf("RunTreeSpans(app) = %v, %v; want its own span alone", keys(tree.Spans), err)
		}
		// The descendants the parent chain alone cannot place (U9.1-fix):
		// a Subagent child whose run_start was lost (only its chat span,
		// no weft.parent.run.id — its row names no parent; the id rule
		// places it), a nested Generate inside a tool handler (its own id,
		// its invoke_agent's weft.parent.run.id, another public id), that
		// run's child stored under another trace (its row's parent), a
		// finished child with a row and no span (Missing, with its public
		// id), and a running child with no span yet (not missing: spans
		// export when they end, U9.1-fix2).
		const (
			deepTrace = "0102030405060708090a0b0c0d0e0f21"
			farTrace  = "0102030405060708090a0b0c0d0e0f22"
			deep      = "tree_deep"
			lost      = "tree_deep/0/call_a"
			nested    = "tree_nested_turn"
			far       = "tree_nested_turn/0/call_n"
			bare      = "tree_deep/1/call_b"
			live      = "tree_deep/1/call_live"
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
		// The running child is seen now, not at the fixture clock: a long
		// suite run would otherwise read it interrupted (a loss).
		liveStart := deepStart(live, deep, "pub_t", 3)
		liveStart.Time = time.Now().UTC()
		if err := db.Write(ctx(), obsdb.Batch{
			Spans: []obsdb.Span{
				inTrace(treeSpan("0c00000000000001", "", "invoke_agent deep", deep, "", 0), deepTrace, "pub_t"),
				inTrace(treeSpan("0c00000000000002", "0c00000000000001", "chat", lost, "", 1), deepTrace, "pub_t"),
				inTrace(treeSpan("0c00000000000003", "0c00000000000001", "invoke_agent nested", nested, deep, 2), deepTrace, "pub_other"),
				inTrace(treeSpan("0c00000000000004", "", "invoke_agent far", far, "", 3), farTrace, "pub_other"),
			},
			Records: []obsdb.Record{deepStart(deep, "", "pub_t", 0), deepStart(far, nested, "pub_other", 1),
				deepStart(bare, deep, "pub_t", 2), liveStart,
				record(bare, "event", "run_finish", 3, `{"type":"run_finish","run_id":"`+bare+`","steps":1}`,
					map[string]any{"weft.public_id": "pub_t"})},
		}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: deep})
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
			t.Errorf("RunTreeSpans(deep) = truncated %v, missing %+v; want missing %+v (the running child not missing)", got.Truncated, got.Missing, want)
		}
		if got.MissingCount != 1 || got.Hidden || got.TraceTruncated {
			t.Errorf("RunTreeSpans(deep) = missing count %d, hidden %v, trace truncated %v; want 1, neither", got.MissingCount, got.Hidden, got.TraceTruncated)
		}
		// Scoped to pub_t (U9.1-fix2): the nested turn (pub_other) and its
		// child are left out, Hidden; the rest as unscoped.
		sc, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: deep, PublicID: "pub_t"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(keys(sc.Spans), wantDeep[:2]) || !sc.Hidden || sc.Truncated || sc.MissingCount != 1 || sc.Missing[0].ID != bare {
			t.Errorf("RunTreeSpans(deep, pub_t) = %v, hidden %v, truncated %v, missing %+v; want %v, hidden, %s missing",
				keys(sc.Spans), sc.Hidden, sc.Truncated, sc.Missing, wantDeep[:2], bare)
		}
		if sc.HiddenCount != 2 || got.HiddenCount != 0 {
			t.Errorf("RunTreeSpans(deep, pub_t).HiddenCount = %d (unscoped %d), want 2 (the nested turn and its child), 0", sc.HiddenCount, got.HiddenCount)
		}

		// The id rule is core's shape alone (U9.1-fix2): an app's own
		// hierarchical ids — "tree_job/1" and "tree_job/1/retry/2", rows
		// naming no parent — are not each other's tree.
		const jobTrace = "0102030405060708090a0b0c0d0e0f24"
		job := func(id, span string, ms int) obsdb.Span {
			return inTrace(treeSpan(span, "", "invoke_agent job", id, "", ms), jobTrace, "")
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{
			job("tree_job/1", "0e00000000000001", 0), job("tree_job/1/retry/2", "0e00000000000002", 1),
		}}); err != nil {
			t.Fatal(err)
		}
		if jt, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: "tree_job/1"}); err != nil || len(jt.Spans) != 1 || jt.Spans[0].RunID != "tree_job/1" || jt.MissingCount != 0 {
			t.Errorf("RunTreeSpans(tree_job/1) = %v, missing %d, %v; want its own span alone", keys(jt.Spans), jt.MissingCount, err)
		}
		// A parent counts only when it is a member: under tree_j, the
		// app's own "tree_j/1" (not of core's shape) is no member, so
		// "tree_j/1/0/c" — core's shape under tree_j — is tree_j's
		// child on every backend, never an orphan under tree_j/1.
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{
			job("tree_j", "0e00000000000011", 10), job("tree_j/1", "0e00000000000012", 11), job("tree_j/1/0/c", "0e00000000000013", 12),
		}}); err != nil {
			t.Fatal(err)
		}
		jt, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: "tree_j"})
		if want := []key{{"tree_j", "0e00000000000011", ""}, {"tree_j/1/0/c", "0e00000000000013", "tree_j"}}; err != nil ||
			!slices.Equal(keys(jt.Spans), want) || jt.MissingCount != 0 {
			t.Errorf("RunTreeSpans(tree_j) = %v, missing %d, %v; want %v", keys(jt.Spans), jt.MissingCount, err, want)
		}

		// A derived run late in a large trace (U9.1-fix2): the subtree is
		// cut from the trace before the cap, so TreeSpanCap + 5 earlier
		// spans of other work never push it out.
		const noiseTrace = "0102030405060708090a0b0c0d0e0f25"
		noise := make([]obsdb.Span, 0, caps.TreeSpan+6)
		for i := range caps.TreeSpan + 5 {
			noise = append(noise, obsdb.Span{
				TraceID: noiseTrace, SpanID: fmt.Sprintf("0f%014x", i), Name: "GET /noise", Kind: 1,
				Start: at(time.Duration(i) * time.Microsecond), End: at(time.Duration(i+1) * time.Microsecond),
				StatusCode: 1, Service: "noise", Attrs: map[string]any{"http.request.method": "GET"},
				Resource: map[string]any{"service.name": "noise"},
			})
		}
		late := obsdb.Span{
			TraceID: noiseTrace, SpanID: "0fffffffffffff01", Name: "invoke_agent late", Kind: 1,
			Start: at(time.Second), End: at(2 * time.Second), StatusCode: 1, Service: "noise",
			Attrs:    map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "late", "gen_ai.conversation.id": "conv_late"},
			Resource: map[string]any{"service.name": "noise"},
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: append(noise, late)}); err != nil {
			t.Fatal(err)
		}
		lt, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: obsdb.StockRunID(noiseTrace, late.SpanID)})
		if err != nil {
			t.Fatal(err)
		}
		if lt.Truncated || lt.TraceTruncated || len(lt.Spans) != 1 || lt.Spans[0].SpanID != late.SpanID {
			t.Errorf("RunTreeSpans(late derived run) = truncated %v, trace truncated %v, %d spans; want its one span", lt.Truncated, lt.TraceTruncated, len(lt.Spans))
		}
		// Past the trace cap (obsdb.TreeTraceCap, lowered) spans of the trace the read stops and
		// says so: a root after them is not read.
		const hugeTrace = "0102030405060708090a0b0c0d0e0f26"
		huge := make([]obsdb.Span, 0, caps.TreeTrace+2)
		for i := range caps.TreeTrace + 1 {
			sp := noise[0]
			sp.TraceID, sp.SpanID = hugeTrace, fmt.Sprintf("1f%014x", i)
			sp.Start, sp.End = at(time.Duration(i)*time.Microsecond), at(time.Duration(i+1)*time.Microsecond)
			huge = append(huge, sp)
		}
		hugeRoot := late
		hugeRoot.TraceID = hugeTrace
		if err := db.Write(ctx(), obsdb.Batch{Spans: append(huge, hugeRoot)}); err != nil {
			t.Fatal(err)
		}
		ht, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: obsdb.StockRunID(hugeTrace, hugeRoot.SpanID)})
		if err != nil {
			t.Fatal(err)
		}
		if !ht.TraceTruncated || ht.Truncated || len(ht.Spans) != 0 {
			t.Errorf("RunTreeSpans(a root past the trace cap) = trace truncated %v, truncated %v, %d spans; want trace truncated, no span",
				ht.TraceTruncated, ht.Truncated, len(ht.Spans))
		}

		// The cap: a tree of more than obsdb.TreeSpanCap (lowered) spans
		// answers its first TreeSpanCap in start order and says it holds more.
		const big = "tree_big"
		many := make([]obsdb.Span, 0, caps.TreeSpan+1)
		for i := range caps.TreeSpan + 1 {
			sp := treeSpan(fmt.Sprintf("0d%014x", i), "", "chat", big, "", 10+i)
			sp.TraceID = "0102030405060708090a0b0c0d0e0f23"
			many = append(many, sp)
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: many}); err != nil {
			t.Fatal(err)
		}
		capped, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: big})
		if err != nil {
			t.Fatal(err)
		}
		if !capped.Truncated || len(capped.Spans) != caps.TreeSpan || capped.Spans[0].SpanID != many[0].SpanID ||
			capped.Spans[caps.TreeSpan-1].SpanID != many[caps.TreeSpan-1].SpanID || len(capped.Missing) != 0 {
			t.Errorf("RunTreeSpans(big) = truncated %v, %d spans, missing %v; want truncated, the first %d",
				capped.Truncated, len(capped.Spans), capped.Missing, caps.TreeSpan)
		}

		// A run with a row and no span: an empty tree, not an error.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{record("tree_bare", "event", "run_start", 0,
			`{"type":"run_start","id":"tree_bare"}`, nil)}}); err != nil {
			t.Fatal(err)
		}
		if tree, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: "tree_bare"}); err != nil || len(tree.Spans) != 0 || len(tree.Missing) != 0 {
			t.Errorf("RunTreeSpans(a run with no span) = %v, %v; want empty", tree, err)
		}
		if _, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: "nope"}); !errors.Is(err, obsdb.ErrNotFound) {
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

		// Past the caps: a run of obsdb.ChildRunsCap + 1 children answers
		// ErrChildRunsCapped from ChildRuns (never a part), and its tree
		// past obsdb.TreeRowCap rows is cut and says so.
		const wide = "tree_wide"
		wideRecs := []obsdb.Record{start(wide, "")}
		for i := range max(caps.ChildRuns, caps.TreeRow) + 1 {
			wideRecs = append(wideRecs, start(fmt.Sprintf("%s/0/c%05d", wide, i), wide))
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: wideRecs}); err != nil {
			t.Fatal(err)
		}
		if got, err := tr.ChildRuns(ctx(), []string{wide}); !errors.Is(err, obsdb.ErrChildRunsCapped) || got != nil {
			t.Errorf("ChildRuns(wide) = %d parents, %v; want ErrChildRunsCapped", len(got), err)
		}
		if got, err := tr.ChildRuns(ctx(), []string{director}); err != nil || len(got[director]) != 1 {
			t.Errorf("ChildRuns(director) beside a wide run = %v, %v", got, err)
		}
		if wt, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: wide}); err != nil || !wt.Truncated || wt.MissingCount != 0 {
			t.Errorf("RunTreeSpans(wide) = truncated %v, missing %d, %v; want truncated, no missing", wt.Truncated, wt.MissingCount, err)
		}

		// The row cap counts members, never the id range's superset
		// (U31.1): obsdb.TreeRowCap + 5 app-shaped rows under a run's
		// id ("<run>/job/N": hierarchical ids of the app's own, no
		// parent, never in its tree), started before its one real
		// descendant — a Subagent child whose run_start named no parent,
		// placed by core's id rule alone — leave that child in the tree,
		// uncut.
		const shaped = "tree_shaped"
		shapedChild := shaped + "/0/call_1"
		shapedRecs := []obsdb.Record{start(shaped, "")}
		shapedRecs[0].Time = at(time.Minute)
		for i := range caps.TreeRow + 5 {
			r := start(fmt.Sprintf("%s/job/%05d", shaped, i), "")
			r.Time = at(time.Minute + time.Duration(i+1)*time.Millisecond)
			shapedRecs = append(shapedRecs, r)
		}
		child := start(shapedChild, "")
		child.Time = at(2 * time.Minute)
		shapedRecs = append(shapedRecs, child)
		childSpan := treeSpan("0b000000000000a1", "", "chat", shapedChild, "", 120_000)
		if err := db.Write(ctx(), obsdb.Batch{Records: shapedRecs, Spans: []obsdb.Span{childSpan}}); err != nil {
			t.Fatal(err)
		}
		if st, err := tr.RunTreeSpans(ctx(), obsdb.TreeQuery{RunID: shaped}); err != nil || st.Truncated ||
			len(st.Spans) != 1 || st.Spans[0].RunID != shapedChild || st.Spans[0].ParentRunID != shaped {
			t.Errorf("RunTreeSpans(shaped) = truncated %v, spans %v, %v; want the id-shaped child's span alone, uncut (app-shaped ids are no members)", st.Truncated, keys(st.Spans), err)
		}

		dead, cancel := context.WithCancel(ctx())
		cancel()
		if _, err := tr.RunTreeSpans(dead, obsdb.TreeQuery{RunID: director}); err == nil || errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("RunTreeSpans on a cancelled context = %v, want the read's own error", err)
		}
		if _, err := tr.ChildRuns(dead, []string{director}); err == nil {
			t.Errorf("ChildRuns on a cancelled context = %v, want the read's own error", err)
		}
	}
}
