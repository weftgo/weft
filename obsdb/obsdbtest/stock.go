package obsdbtest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// The derived-run fixture (docs/record-contract.md §9): one stock trace
// — no weft.* attribute — holding a planner root, a worker root nested
// under the planner's execute_tool span (a child run), an
// invoke_agent span without a conversation id (not a root: its spans
// are the planner's) and a generic HTTP span (part of the planner's
// tree, no GenAI operation).
const (
	stockTrace   = "5d0c0ffee0000000000000000000a001"
	stockPlanner = "5d00000000000001"
	stockWorker  = "5d00000000000005"
)

func stockSpan(id, parent, name string, start, end time.Duration, attrs map[string]any) obsdb.Span {
	return obsdb.Span{
		TraceID: stockTrace, SpanID: id, ParentSpanID: parent, Name: name, Kind: 1,
		Start: at(start), End: at(end), StatusCode: 1, Service: "py-conf", Attrs: attrs,
		Resource: map[string]any{"service.name": "py-conf"},
	}
}

func stockChat(id, parent string, start time.Duration, in, out int64) obsdb.Span {
	return stockSpan(id, parent, "chat gpt-conf", start, start+500*time.Millisecond, map[string]any{
		"gen_ai.operation.name": "chat", "gen_ai.provider.name": "openai", "gen_ai.request.model": "gpt-conf",
		"gen_ai.usage.input_tokens": in, "gen_ai.usage.output_tokens": out, "gen_ai.conversation.id": "conv_conf",
	})
}

func stockTool(id, parent, name, call string, start time.Duration) obsdb.Span {
	return stockSpan(id, parent, "execute_tool "+name, start, start+2*time.Second, map[string]any{
		"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name, "gen_ai.tool.call.id": call,
	})
}

// stockBatches is the fixture as three exports, children first (the
// order a BatchSpanProcessor sends them: a span is exported when it
// ends): the leaf calls, then the worker root, then the inner agent,
// the HTTP span and the planner root.
func stockBatches() [][]obsdb.Span {
	planner := stockSpan(stockPlanner, "", "invoke_agent planner", 0, 10*time.Second, map[string]any{
		"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "planner",
		"gen_ai.conversation.id": "conv_conf",
	})
	worker := stockSpan(stockWorker, "5d00000000000003", "invoke_agent worker", 2100*time.Millisecond, 4*time.Second, map[string]any{
		"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "worker",
		"gen_ai.provider.name": "anthropic", "gen_ai.request.model": "claude-conf",
		"gen_ai.conversation.id":    "conv_conf",
		"gen_ai.usage.input_tokens": int64(50), "gen_ai.usage.output_tokens": int64(5),
	})
	worker.StatusCode, worker.StatusMessage = 2, "worker exploded"
	helper := stockSpan("5d00000000000007", stockPlanner, "invoke_agent helper", 6*time.Second, 7*time.Second, map[string]any{
		"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "helper",
	})
	http := stockSpan("5d0000000000000a", stockPlanner, "GET /orders", 8*time.Second, 9*time.Second, map[string]any{
		"http.request.method": "GET",
	})
	return [][]obsdb.Span{
		{
			stockChat("5d00000000000002", stockPlanner, time.Second, 100, 10),
			stockTool("5d00000000000003", stockPlanner, "delegate", "call_1", 2*time.Second),
			stockChat("5d00000000000006", stockWorker, 2200*time.Millisecond, 40, 4),
			stockChat("5d00000000000008", "5d00000000000007", 6100*time.Millisecond, 7, 1),
			stockTool("5d00000000000009", "5d00000000000007", "lookup", "call_3", 6500*time.Millisecond),
		},
		{worker},
		{helper, http, planner},
	}
}

// derivedStock: a stock span tree with gen_ai.conversation.id is a run
// row on every backend (docs/record-contract.md §9) — converging over
// several exports, children before roots, the nested root a child run,
// the no-conversation-id agent's calls the enclosing run's, usage own
// only — and a weft span with the GenAI mirrors is never derived.
func derivedStock(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		plannerID := obsdb.StockRunID(stockTrace, stockPlanner)
		workerID := obsdb.StockRunID(stockTrace, stockWorker)
		batches := stockBatches()

		// The leaf calls alone open no run: no root has arrived.
		if err := db.Write(ctx(), obsdb.Batch{Spans: batches[0]}); err != nil {
			t.Fatal(err)
		}
		if page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"}); err != nil || page.Total != 0 {
			t.Fatalf("children only: %d runs (%v), want none before a root", page.Total, err)
		}
		// The worker root: a run of its own, its usage the root's own
		// figure; its parent not yet known (the chain above it breaks
		// at the planner's root, not stored yet).
		if err := db.Write(ctx(), obsdb.Batch{Spans: batches[1]}); err != nil {
			t.Fatal(err)
		}
		if w, err := db.Run(ctx(), workerID); err != nil || w.ParentRunID != "" || w.Steps != 1 {
			t.Fatalf("worker before its parent = %+v, %v; want a top-level row with 1 step", w.RunRow, err)
		}
		// The rest, twice: a retried export changes nothing.
		for range 2 {
			if err := db.Write(ctx(), obsdb.Batch{Spans: batches[2]}); err != nil {
				t.Fatal(err)
			}
		}

		page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 2 {
			t.Fatalf("runs = %d (%v), want the planner and the worker", page.Total, idsOf(page.Runs))
		}
		det, err := db.Run(ctx(), plannerID)
		if err != nil {
			t.Fatal(err)
		}
		p := det.RunRow
		if !p.Derived || p.Status != obsdb.StatusSucceeded || p.SessionID != "conv_conf" || p.Agent != "planner" ||
			p.Service != "py-conf" || p.TraceID != stockTrace || p.ParentRunID != "" {
			t.Errorf("planner = %+v", p)
		}
		// The root names no model: the first chat span's. Two steps — its
		// own chat and the helper's (no conversation id, so not a root) —
		// never the worker's; usage their sum, the worker's excluded.
		if p.Provider != "openai" || p.Model != "gpt-conf" || p.Steps != 2 ||
			p.Usage != (core.Usage{InputTokens: 107, OutputTokens: 11}) {
			t.Errorf("planner provider/model/steps/usage = %s/%s/%d/%+v, want openai/gpt-conf/2/107+11",
				p.Provider, p.Model, p.Steps, p.Usage)
		}
		if !p.Started.Equal(at(0)) || p.Finished == nil || !p.Finished.Equal(at(10*time.Second)) ||
			!p.LastSeen.Equal(at(10*time.Second)) {
			t.Errorf("planner times = %v %v %v, want the root's start and end", p.Started, p.Finished, p.LastSeen)
		}
		if p.EventCount != 0 || p.MessageCount != 0 || p.DeltaCount != 0 || p.RequestCount != 0 ||
			p.InstructionsHash != "" || p.Turn != 0 || p.PublicID != "" || p.Pending != 0 {
			t.Errorf("planner holds what spans cannot say: %+v", p)
		}
		if h := p.RequestsHole(); h != obsdb.HoleDerived {
			t.Errorf("planner RequestsHole = %q, want derived", h)
		}
		if len(det.Children) != 1 || det.Children[0].ID != workerID {
			t.Fatalf("planner children = %v, want the worker", idsOf(det.Children))
		}
		w := det.Children[0]
		if !w.Derived || w.ParentRunID != plannerID || w.ParentCallID != "call_1" || w.Status != obsdb.StatusFailed ||
			w.Err != "worker exploded" || w.Steps != 1 || w.Usage != (core.Usage{InputTokens: 50, OutputTokens: 5}) ||
			w.Provider != "anthropic" || w.Model != "claude-conf" {
			t.Errorf("worker = %+v", w)
		}
		if top, err := db.Runs(ctx(), obsdb.RunQuery{}); err != nil || len(top.Runs) != 1 || top.Runs[0].ID != plannerID {
			t.Errorf("top-level runs = %v (%v), want the planner alone", idsOf(top.Runs), err)
		}

		// The spans: the planner's subtree in start order, down to (not
		// into) the worker; the reader's positions stamped.
		spans, err := db.RunSpans(ctx(), plannerID)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, s := range spans {
			ids = append(ids, s.SpanID)
		}
		want := []string{stockPlanner, "5d00000000000002", "5d00000000000003", "5d00000000000007",
			"5d00000000000008", "5d00000000000009", "5d0000000000000a"}
		if !equalStrings(ids, want) {
			t.Errorf("planner spans = %v, want %v", ids, want)
		}
		pos := map[string][2]int64{}
		for _, s := range spans {
			pos[s.SpanID] = [2]int64{num(s.Attrs["weft.step.index"]), num(s.Attrs["weft.tool.seq"])}
		}
		for id, wantPos := range map[string][2]int64{
			"5d00000000000002": {0, -1}, "5d00000000000008": {1, -1}, // chats: their ordinal
			"5d00000000000003": {0, 1}, "5d00000000000009": {1, 2}, // tools: the latest chat before, seq
			stockPlanner: {-1, -1}, "5d0000000000000a": {-1, -1},
		} {
			if pos[id] != wantPos {
				t.Errorf("span %s (step, seq) = %v, want %v", id, pos[id], wantPos)
			}
		}
		if ws, err := db.RunSpans(ctx(), workerID); err != nil || len(ws) != 2 {
			t.Errorf("worker spans = %d (%v), want its root and its chat", len(ws), err)
		}

		// Nothing else exists for it, and saying so is not an error.
		ev, err := db.Events(ctx(), plannerID, -1, 10)
		if err != nil || len(ev.Events) != 0 || !ev.Done {
			t.Errorf("planner events = %+v, %v; want none, done", ev, err)
		}
		if tr, err := db.Transcript(ctx(), plannerID); err != nil || len(tr) != 0 {
			t.Errorf("planner transcript = %d, %v; want empty", len(tr), err)
		}
		var hole *obsdb.HoleError
		if _, err := db.Prompt(ctx(), plannerID, "h"); !errors.As(err, &hole) || hole.Hole != obsdb.HoleDerived {
			t.Errorf("planner prompt = %v, want the derived hole", err)
		}

		// The conversation is the session; its top-level derived runs its turns.
		sess, err := db.Session(ctx(), "conv_conf")
		if err != nil || len(sess.Runs) != 1 || sess.Runs[0].ID != plannerID {
			t.Errorf("session conv_conf = %v (%v), want the planner", idsOf(sess.Runs), err)
		}
		if sp, err := db.Sessions(ctx(), obsdb.SessionQuery{}); err != nil || len(sp.Sessions) != 1 || sp.Sessions[0].ID != "conv_conf" {
			t.Errorf("sessions = %+v (%v), want conv_conf", sp.Sessions, err)
		}

		// Searchable by its error text, agent and model.
		if s, ok := db.(obsdb.Searcher); ok {
			res, err := s.Search(ctx(), obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"}, Text: "exploded"})
			if err != nil || len(res.Runs) != 1 || res.Runs[0].ID != workerID || !res.Runs[0].Derived {
				t.Errorf("search exploded = %v (%v), want the worker", idsOf(res.Runs), err)
			}
			res, err = s.Search(ctx(), obsdb.SearchQuery{RunQuery: obsdb.RunQuery{Agent: "planner"}, Model: "gpt-conf"})
			if err != nil || len(res.Runs) != 1 || res.Runs[0].ID != plannerID {
				t.Errorf("search agent+model = %v (%v), want the planner", idsOf(res.Runs), err)
			}
		}

		// A weft run's spans carry the GenAI mirrors too — the
		// conversation id among them — and are never derived: one row.
		weftRun := invokeSpan("weft_mirrored", 1, map[string]any{"gen_ai.conversation.id": "s_conf"})
		weftRun.TraceID, weftRun.SpanID = "5d0c0ffee0000000000000000000a002", "5d000000000000ff"
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{weftRun}}); err != nil {
			t.Fatal(err)
		}
		all, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if err != nil || all.Total != 3 {
			t.Fatalf("after a weft span = %v (%v), want the weft run beside the two derived", idsOf(all.Runs), err)
		}
		if r, err := db.Run(ctx(), "weft_mirrored"); err != nil || r.Derived {
			t.Errorf("weft run = %+v, %v; want stored, not derived", r.RunRow, err)
		}

		// Deleting a derived run takes its subtree — the worker, its
		// child run, with it — though no span names it by run id.
		if err := db.DeleteRun(ctx(), plannerID); err != nil {
			t.Fatal(err)
		}
		if left, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"}); err != nil || left.Total != 1 || left.Runs[0].ID != "weft_mirrored" {
			t.Errorf("after DeleteRun = %v (%v), want the weft run alone", idsOf(left.Runs), err)
		}
		if tr, err := db.Trace(ctx(), stockTrace); err != nil || len(tr) != 0 {
			t.Errorf("stock trace after DeleteRun = %d spans (%v), want none", len(tr), err)
		}
	}
}

// num reads a stamped integer attribute (-1 when absent), whichever
// numeric type the backend's read gives back.
func num(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return -1
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// derivedMixed: weft spans and stock GenAI spans in one trace. A weft
// span is never a root and stops a stock subtree (§9): a weft run with
// a stock agent under its tool yields the weft row and the stock root's
// derived row — top-level, since the walk stops at the weft span — and
// a stock root with a weft run under it yields the derived row (its
// steps its own chat spans only) beside the weft row, never a derived
// twin of the weft run. Both weft spans carry the conversation id
// mirror, as weft's do for a session's run.
func derivedMixed(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const (
			weftInStock = "5d0c0ffee0000000000000000000b001" // a weft run under a stock root
			stockInWeft = "5d0c0ffee0000000000000000000b002" // a stock agent under a weft run's tool
		)
		weftSpan := func(trace, id, parent, name, op, run string, start, end time.Duration) obsdb.Span {
			sp := stockSpan(id, parent, name, start, end, map[string]any{
				"gen_ai.operation.name": op, "weft.run.id": run, "gen_ai.conversation.id": "s_mix",
				"gen_ai.agent.name": "weft-agent",
			})
			sp.TraceID = trace
			return sp
		}
		inTrace := func(trace string, s obsdb.Span) obsdb.Span { s.TraceID = trace; return s }
		stockRoot := func(trace, id, parent string, start, end time.Duration) obsdb.Span {
			return inTrace(trace, stockSpan(id, parent, "invoke_agent stock", start, end, map[string]any{
				"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "stock", "gen_ai.conversation.id": "conv_mix",
			}))
		}
		spans := []obsdb.Span{
			// A: stock root ⊃ stock chat, weft invoke_agent ⊃ weft chat.
			stockRoot(weftInStock, "5d000000000000a1", "", 0, 10*time.Second),
			inTrace(weftInStock, stockChat("5d000000000000a2", "5d000000000000a1", time.Second, 10, 1)),
			weftSpan(weftInStock, "5d000000000000a3", "5d000000000000a1", "invoke_agent weft-agent", "invoke_agent", "weft_in_stock", 2*time.Second, 5*time.Second),
			weftSpan(weftInStock, "5d000000000000a4", "5d000000000000a3", "chat m", "chat", "weft_in_stock", 3*time.Second, 4*time.Second),
			// B: weft invoke_agent ⊃ weft execute_tool ⊃ stock root ⊃ stock chat.
			weftSpan(stockInWeft, "5d000000000000b1", "", "invoke_agent weft-agent", "invoke_agent", "weft_with_stock", 0, 10*time.Second),
			weftSpan(stockInWeft, "5d000000000000b2", "5d000000000000b1", "execute_tool ask", "execute_tool", "weft_with_stock", time.Second, 9*time.Second),
			stockRoot(stockInWeft, "5d000000000000b3", "5d000000000000b2", 2*time.Second, 8*time.Second),
			inTrace(stockInWeft, stockChat("5d000000000000b4", "5d000000000000b3", 3*time.Second, 20, 2)),
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: spans}); err != nil {
			t.Fatal(err)
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]obsdb.RunRow{}
		for _, r := range page.Runs {
			got[r.ID] = r
		}
		a, b := obsdb.StockRunID(weftInStock, "5d000000000000a1"), obsdb.StockRunID(stockInWeft, "5d000000000000b3")
		want := []string{"weft_in_stock", "weft_with_stock", a, b}
		if len(got) != len(want) {
			t.Fatalf("runs = %v, want exactly %v: no derived twin of a weft run", idsOf(page.Runs), want)
		}
		for _, id := range want {
			r, ok := got[id]
			if !ok {
				t.Errorf("run %s missing from %v", id, idsOf(page.Runs))
				continue
			}
			if r.Derived != strings.HasPrefix(id, "otel-") {
				t.Errorf("run %s derived = %v", id, r.Derived)
			}
		}
		if r := got[a]; r.Steps != 1 || r.Usage.InputTokens != 10 || r.ParentRunID != "" {
			t.Errorf("stock root over a weft run = steps %d usage %+v parent %q; want its own chat alone, top-level", r.Steps, r.Usage, r.ParentRunID)
		}
		if r := got[b]; r.Steps != 1 || r.ParentRunID != "" || r.ParentCallID != "" {
			t.Errorf("stock root under a weft tool = steps %d parent %q/%q; want 1 step, no parent (the walk stops at weft)", r.Steps, r.ParentRunID, r.ParentCallID)
		}
		// Deleting the weft run takes the stock agent under its tool —
		// spans and derived row — and leaves the other trace alone.
		if err := db.DeleteRun(ctx(), "weft_with_stock"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Run(ctx(), b); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("derived run under the deleted weft run: %v, want ErrNotFound", err)
		}
		if tr, err := db.Trace(ctx(), stockInWeft); err != nil || len(tr) != 0 {
			t.Errorf("deleted weft run's trace = %d spans (%v), want none", len(tr), err)
		}
		if left, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"}); err != nil || left.Total != 2 {
			t.Errorf("after DeleteRun = %v (%v), want the other trace's two runs", idsOf(left.Runs), err)
		}
	}
}

// derivedTwoRoots: two stock roots in one trace — sibling agents under
// a generic request span — are two runs; deleting one keeps the other
// with every span of its own.
func derivedTwoRoots(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		root := func(id string, start time.Duration) obsdb.Span {
			return stockSpan(id, "5d000000000000c0", "invoke_agent "+id, start, start+3*time.Second, map[string]any{
				"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "conv_two",
			})
		}
		spans := []obsdb.Span{
			stockSpan("5d000000000000c0", "", "POST /chat", 0, 10*time.Second, map[string]any{"http.request.method": "POST"}),
			root("5d000000000000c1", time.Second),
			stockChat("5d000000000000c2", "5d000000000000c1", 1500*time.Millisecond, 1, 1),
			root("5d000000000000c3", 5*time.Second),
			stockChat("5d000000000000c4", "5d000000000000c3", 5500*time.Millisecond, 2, 2),
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: spans}); err != nil {
			t.Fatal(err)
		}
		first, second := obsdb.StockRunID(stockTrace, "5d000000000000c1"), obsdb.StockRunID(stockTrace, "5d000000000000c3")
		if page, err := db.Runs(ctx(), obsdb.RunQuery{}); err != nil || page.Total != 2 {
			t.Fatalf("two roots = %v (%v), want two top-level runs", idsOf(page.Runs), err)
		}
		if err := db.DeleteRun(ctx(), first); err != nil {
			t.Fatal(err)
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if err != nil || page.Total != 1 || page.Runs[0].ID != second || page.Runs[0].Steps != 1 {
			t.Fatalf("after deleting one = %v (%v), want the other alone", idsOf(page.Runs), err)
		}
		if sp, err := db.RunSpans(ctx(), second); err != nil || len(sp) != 2 {
			t.Errorf("the kept run's spans = %d (%v), want its root and chat", len(sp), err)
		}
		if tr, err := db.Trace(ctx(), stockTrace); err != nil || len(tr) != 3 {
			t.Errorf("trace after deleting one root = %d spans (%v), want the request span and the kept run's two", len(tr), err)
		}
	}
}

// derivedSweep: a retention sweep takes a weft run with a stock agent
// under its tool and counts the agent's derived row among the runs it
// deleted (DB.Sweep's count). The agent's spans end after the cutoff,
// so its row is no sweep candidate of its own: it goes, and is counted,
// only through the weft run's deletion (StockRunsOf).
func derivedSweep(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const trace = "5d0c0ffee0000000000000000000d001"
		in := func(s obsdb.Span) obsdb.Span { s.TraceID = trace; return s }
		weft := in(stockSpan("5d000000000000d1", "", "invoke_agent w", 0, 10*time.Second, map[string]any{
			"gen_ai.operation.name": "invoke_agent", "weft.run.id": "weft_swept"}))
		weft.StatusCode, weft.StatusMessage = 2, "boom" // failed: not running, so sweepable
		spans := []obsdb.Span{
			weft,
			in(stockSpan("5d000000000000d2", "5d000000000000d1", "execute_tool ask", time.Second, 9*time.Second, map[string]any{
				"gen_ai.operation.name": "execute_tool", "weft.run.id": "weft_swept"})),
			in(stockSpan("5d000000000000d3", "5d000000000000d2", "invoke_agent stock", 2*time.Second, 2*time.Hour, map[string]any{
				"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "conv_sweep"})),
			in(stockChat("5d000000000000d4", "5d000000000000d3", 90*time.Minute, 1, 1)),
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: spans}); err != nil {
			t.Fatal(err)
		}
		if r, err := db.Run(ctx(), obsdb.StockRunID(trace, "5d000000000000d3")); err != nil || !r.LastSeen.After(at(time.Hour)) {
			t.Fatalf("the derived row = %+v, %v; want it seen after the cutoff (no candidate of its own)", r.RunRow, err)
		}
		n, err := db.Sweep(ctx(), at(time.Hour))
		if err != nil || n != 2 {
			t.Fatalf("Sweep = %d, %v; want 2 (the weft run and the derived run under its tool)", n, err)
		}
		if left, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"}); err != nil || left.Total != 0 {
			t.Errorf("after the sweep = %v (%v), want none", idsOf(left.Runs), err)
		}
		if tr, err := db.Trace(ctx(), trace); err != nil || len(tr) != 0 {
			t.Errorf("swept trace = %d spans (%v), want none", len(tr), err)
		}
	}
}
