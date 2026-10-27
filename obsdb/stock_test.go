package obsdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

var stockEpoch = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func stockAt(ms int) time.Time { return stockEpoch.Add(time.Duration(ms) * time.Millisecond) }

// A Python agent's stock GenAI spans — no weft.* attribute — are a run
// (docs/record-contract.md §9): the invoke_agent span with a
// conversation id is its root, its chat spans its steps, its
// execute_tool spans its tool calls.
func ExampleDeriveStockRuns() {
	const trace = "4bf92f3577b34da6a3ce929d0e0e4736"
	spans := []obsdb.Span{
		{TraceID: trace, SpanID: "00f067aa0ba902b7", Name: "invoke_agent support",
			Start: stockAt(0), End: stockAt(3200), StatusCode: 1, Service: "py-support",
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "support",
				"gen_ai.conversation.id": "conv_7f3a"}},
		{TraceID: trace, SpanID: "a1b2c3d4e5f60001", ParentSpanID: "00f067aa0ba902b7", Name: "chat gpt-5",
			Start: stockAt(50), End: stockAt(1250),
			Attrs: map[string]any{"gen_ai.operation.name": "chat", "gen_ai.provider.name": "openai",
				"gen_ai.request.model": "gpt-5", "gen_ai.usage.input_tokens": int64(812), "gen_ai.usage.output_tokens": int64(41)}},
		{TraceID: trace, SpanID: "a1b2c3d4e5f60002", ParentSpanID: "00f067aa0ba902b7", Name: "execute_tool lookup_order",
			Start: stockAt(1300), End: stockAt(1420),
			Attrs: map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "lookup_order",
				"gen_ai.tool.call.id": "call_abc"}},
	}
	for _, r := range obsdb.DeriveStockRuns(spans) {
		fmt.Println(r.Row.ID)
		fmt.Println(r.Row.Derived, r.Row.Status, r.Row.SessionID, r.Row.Provider+"/"+r.Row.Model, r.Row.Steps, r.Row.Usage.InputTokens)
		fmt.Println(r.Row.RequestsHole())
		for _, s := range r.Spans {
			fmt.Println(s.Name, s.Attrs["weft.step.index"], s.Attrs["weft.tool.seq"])
		}
	}
	// Output:
	// otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7
	// true succeeded conv_7f3a openai/gpt-5 1 812
	// derived
	// invoke_agent support <nil> <nil>
	// chat gpt-5 0 <nil>
	// execute_tool lookup_order 0 1
}

func TestStockRunID(t *testing.T) {
	id := obsdb.StockRunID("4BF92F3577B34DA6A3CE929D0E0E4736", "00F067AA0BA902B7")
	if id != "otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7" {
		t.Fatalf("StockRunID = %q, want lowercase hex", id)
	}
	tr, sp, ok := obsdb.ParseStockRunID(id)
	if !ok || tr != "4bf92f3577b34da6a3ce929d0e0e4736" || sp != "00f067aa0ba902b7" {
		t.Errorf("ParseStockRunID(%q) = %q %q %v", id, tr, sp, ok)
	}
	for _, bad := range []string{
		"4bf92f3577b34da6a3ce929d0e0e4736", // a weft run id
		"s_1-t2",                           // a thread turn
		"4bf92f3577b34da6a3ce929d0e0e4736/0/call_1",              // a Subagent child
		"otel-4bf92f3577b34da6a3ce929d0e0e4736",                  // no span
		"otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b",  // short span
		"otel-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7", // upper case
		"otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7/0/c",
	} {
		if _, _, ok := obsdb.ParseStockRunID(bad); ok {
			t.Errorf("ParseStockRunID(%q) ok, want refused", bad)
		}
	}
	if got := obsdb.StockTraces([]string{"r1", id, id, "otel-x"}); len(got) != 1 || got[0] != tr {
		t.Errorf("StockTraces = %v, want the one trace once", got)
	}
}

// What makes a root on its own (§9): the operation (attribute, else the
// name), no weft.run.id, a conversation id. Without one, the trace
// decides (TestDeriveTopLevelWithoutConversation).
func TestIsStockRootAndCandidate(t *testing.T) {
	for _, c := range []struct {
		name      string
		span      obsdb.Span
		root, can bool
	}{
		{"attr root", obsdb.Span{Name: "agent", Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "c"}}, true, true},
		{"named root", obsdb.Span{Name: "invoke_agent support", Attrs: map[string]any{"gen_ai.conversation.id": "c"}}, true, true},
		{"no conversation", obsdb.Span{Name: "invoke_agent support", Attrs: map[string]any{}}, false, true},
		{"weft run", obsdb.Span{Name: "invoke_agent", Attrs: map[string]any{"weft.run.id": "r", "gen_ai.conversation.id": "c"}}, false, false},
		{"attr wins over name", obsdb.Span{Name: "invoke_agent x", Attrs: map[string]any{"gen_ai.operation.name": "chat", "gen_ai.conversation.id": "c"}}, false, true},
		{"named chat", obsdb.Span{Name: "chat gpt-5"}, false, true},
		{"chatbot is not chat", obsdb.Span{Name: "chatbot handler"}, false, false},
		{"named tool", obsdb.Span{Name: "execute_tool lookup"}, false, true},
		{"http", obsdb.Span{Name: "GET /x", Attrs: map[string]any{"http.request.method": "GET"}}, false, false},
	} {
		if got := obsdb.IsStockRoot(c.span); got != c.root {
			t.Errorf("%s: IsStockRoot = %v, want %v", c.name, got, c.root)
		}
		if got := obsdb.StockCandidate(c.span); got != c.can {
			t.Errorf("%s: StockCandidate = %v, want %v", c.name, got, c.can)
		}
	}
}

// The usage rule (§9): the root's gen_ai.usage.* verbatim when it
// carries any key — the cache and reasoning splits included — else the
// sum over its chat spans; a cycle a malformed producer made attributes
// nothing and does not hang; a chain broken by a missing parent
// attributes nothing yet; duplicates are read once.
func TestDeriveStockRunsRules(t *testing.T) {
	const tr = "0000000000000000000000000000000a"
	root := obsdb.Span{TraceID: tr, SpanID: "0000000000000001", Name: "invoke_agent a", Start: stockAt(0), End: stockAt(100),
		Attrs: map[string]any{"gen_ai.conversation.id": "c", "gen_ai.usage.input_tokens": "9",
			"gen_ai.usage.cache_read.input_tokens": int64(3), "gen_ai.usage.reasoning.output_tokens": float64(2)}}
	chat := obsdb.Span{TraceID: tr, SpanID: "0000000000000002", ParentSpanID: root.SpanID, Name: "chat m", Start: stockAt(10), End: stockAt(20),
		Attrs: map[string]any{"gen_ai.usage.input_tokens": int64(100), "gen_ai.usage.output_tokens": int64(7)}}
	orphan := obsdb.Span{TraceID: tr, SpanID: "0000000000000003", ParentSpanID: "00000000000000ff", Name: "chat m", Start: stockAt(30), End: stockAt(40)}
	loopA := obsdb.Span{TraceID: tr, SpanID: "0000000000000004", ParentSpanID: "0000000000000005", Name: "chat m"}
	loopB := obsdb.Span{TraceID: tr, SpanID: "0000000000000005", ParentSpanID: "0000000000000004", Name: "chat m"}
	runs := obsdb.DeriveStockRuns([]obsdb.Span{root, chat, chat, orphan, loopA, loopB})
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	r := runs[0].Row
	if r.Usage != (core.Usage{InputTokens: 9, CachedInputTokens: 3, ReasoningTokens: 2}) {
		t.Errorf("usage = %+v, want the root's verbatim", r.Usage)
	}
	if r.Steps != 1 || len(runs[0].Spans) != 2 {
		t.Errorf("steps = %d, spans = %d; want the one chat, the orphan and the cycle left out", r.Steps, len(runs[0].Spans))
	} else if op := runs[0].Spans[1].Attrs["gen_ai.operation.name"]; op != "chat" {
		// The chat span carries no operation attribute — its name decided;
		// the copy says what the derivation resolved.
		t.Errorf("the chat span copy's gen_ai.operation.name = %v, want the resolved \"chat\"", op)
	}
	delete(root.Attrs, "gen_ai.usage.input_tokens")
	delete(root.Attrs, "gen_ai.usage.cache_read.input_tokens")
	delete(root.Attrs, "gen_ai.usage.reasoning.output_tokens")
	if u := obsdb.DeriveStockRuns([]obsdb.Span{root, chat})[0].Row.Usage; u != (core.Usage{InputTokens: 100, OutputTokens: 7}) {
		t.Errorf("usage without the root's = %+v, want the chat spans' sum", u)
	}
	// The caller's spans are never written to: the stamps are on copies.
	if _, stamped := chat.Attrs["weft.step.index"]; stamped {
		t.Error("DeriveStockRuns stamped the caller's span")
	}
}

// A failed root with no status description still says why (§9): the
// reader's words, never an empty error. An empty gen_ai.operation.name
// is unset: the span's name decides.
func TestDeriveStockFailedNoDescriptionAndEmptyOp(t *testing.T) {
	const tr = "0000000000000000000000000000000b"
	root := obsdb.Span{TraceID: tr, SpanID: "0000000000000001", Name: "invoke_agent a", Start: stockAt(0), End: stockAt(10),
		StatusCode: 2, Attrs: map[string]any{"gen_ai.operation.name": "", "gen_ai.conversation.id": "c"}}
	runs := obsdb.DeriveStockRuns([]obsdb.Span{root})
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want the root read by its name", len(runs))
	}
	if obsdb.StockErrNoDescription != "the root span's status is Error (no description)" {
		t.Errorf("StockErrNoDescription = %q, docs/record-contract.md §9 words it otherwise", obsdb.StockErrNoDescription)
	}
	if r := runs[0].Row; r.Status != obsdb.StatusFailed || r.Err != obsdb.StockErrNoDescription {
		t.Errorf("failed root without a description = %s %q", r.Status, r.Err)
	}
}

// An invoke_agent span without gen_ai.conversation.id (§9, I6): at the
// top of its trace — under an HTTP span, or with its parent not stored —
// it is a run with no session; nested under another agent span it folds
// into the enclosing run, its calls that run's steps, as before.
func TestDeriveTopLevelWithoutConversation(t *testing.T) {
	const tr = "0000000000000000000000000000000b"
	sp := func(id, parent, name string, ms int, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: name,
			Start: stockAt(ms), End: stockAt(ms + 10), Attrs: attrs}
	}
	usage := func(in int64) map[string]any {
		return map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": in,
			"gen_ai.input.messages": `[{"role":"user","parts":[{"type":"text","content":"hi"}]}]`}
	}
	http := sp("0000000000000001", "", "POST /plan", 0, nil)
	outer := sp("0000000000000002", http.SpanID, "invoke_agent planner", 1, map[string]any{"gen_ai.agent.name": "planner"})
	chat := sp("0000000000000003", outer.SpanID, "chat m", 2, usage(10))
	inner := sp("0000000000000004", outer.SpanID, "invoke_agent helper", 3, map[string]any{"gen_ai.agent.name": "helper"})
	chat2 := sp("0000000000000005", inner.SpanID, "chat m", 4, map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": int64(5)})

	runs := obsdb.DeriveStockRuns([]obsdb.Span{http, outer, chat, inner, chat2})
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want the top-level planner alone (the helper folds)", len(runs))
	}
	r := runs[0].Row
	if r.ID != obsdb.StockRunID(tr, outer.SpanID) || r.SessionID != "" || r.ParentRunID != "" || r.Steps != 2 ||
		r.Usage.InputTokens != 15 || r.MessageCount != 1 {
		t.Errorf("planner = %+v, want no session, two steps (the helper's folded), 15 in, one chat span with messages", r)
	}

	// Its parent not stored yet: the inner agent alone is top-level for
	// now — a run of its own — and folds once the planner arrives.
	early := obsdb.DeriveStockRuns([]obsdb.Span{inner, chat2})
	if len(early) != 1 || early[0].Row.ID != obsdb.StockRunID(tr, inner.SpanID) {
		t.Fatalf("inner before its parent = %v, want it a run for now", early)
	}

	// Under a weft run's span: top-level (weft's rows win), a run.
	weftTool := sp("0000000000000006", "", "execute_tool delegate", 0, map[string]any{"weft.run.id": "r1"})
	agent := sp("0000000000000007", weftTool.SpanID, "invoke_agent stock", 1, nil)
	if got := obsdb.DeriveStockRuns([]obsdb.Span{weftTool, agent}); len(got) != 1 || got[0].Row.ParentRunID != "" {
		t.Errorf("stock agent under a weft tool = %v, want one top-level run", got)
	}
}
