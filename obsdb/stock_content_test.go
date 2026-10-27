package obsdb_test

import (
	"fmt"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// A stock agent with no gen_ai.conversation.id is a run with no
// session (docs/record-contract.md §9); its tool span's captured
// content is the call's arguments and result.
func ExampleStockToolContent() {
	const trace = "6a000000000000000000000000000001"
	spans := []obsdb.Span{
		{TraceID: trace, SpanID: "6a00000000000001", Name: "invoke_agent planner", Start: stockAt(0), End: stockAt(900),
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "planner"}},
		{TraceID: trace, SpanID: "6a00000000000002", ParentSpanID: "6a00000000000001", Name: "execute_tool lookup_order",
			Start: stockAt(100), End: stockAt(200),
			Attrs: map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "lookup_order",
				"gen_ai.tool.call.arguments": `{"id":"4411"}`, "gen_ai.tool.call.result": `{"status":"shipped"}`}},
	}
	runs := obsdb.DeriveStockRuns(spans)
	fmt.Printf("%s session=%q\n", runs[0].Row.ID, runs[0].Row.SessionID)
	args, result, _, _ := obsdb.StockToolContent(runs[0].Spans[1].Attrs)
	fmt.Println(string(args), "=", result)
	// Output:
	// otel-6a000000000000000000000000000001-6a00000000000001 session=""
	// {"id":"4411"} = {"status":"shipped"}
}

func TestStockToolContent(t *testing.T) {
	for _, c := range []struct {
		name               string
		attrs              map[string]any
		args, result       string
		hasArgs, hasResult bool
	}{
		{"none captured", map[string]any{"gen_ai.tool.name": "x"}, "", "", false, false},
		{"json string", map[string]any{"gen_ai.tool.call.arguments": `{"a":1}`, "gen_ai.tool.call.result": "ok"}, `{"a":1}`, "ok", true, true},
		{"plain text args", map[string]any{"gen_ai.tool.call.arguments": "not json"}, `"not json"`, "", true, false},
		{"structured", map[string]any{"gen_ai.tool.call.arguments": map[string]any{"a": int64(1)}, "gen_ai.tool.call.result": []any{"x"}}, `{"a":1}`, `["x"]`, true, true},
		{"empty result is a result", map[string]any{"gen_ai.tool.call.result": ""}, "", "", false, true},
	} {
		args, result, ha, hr := obsdb.StockToolContent(c.attrs)
		if string(args) != c.args || result != c.result || ha != c.hasArgs || hr != c.hasResult {
			t.Errorf("%s: = %s %q %v %v, want %s %q %v %v", c.name, args, result, ha, hr, c.args, c.result, c.hasArgs, c.hasResult)
		}
	}
}

// Why a trace is not a run (§9): nothing when it makes one, weft's or
// derived; a GenAI span whose parent is not stored; chat spans without
// an agent; no GenAI span at all.
func TestStockNotRun(t *testing.T) {
	const tr = "6b000000000000000000000000000001"
	http := obsdb.Span{TraceID: tr, SpanID: "6b00000000000001", Name: "POST /chat"}
	chat := obsdb.Span{TraceID: tr, SpanID: "6b00000000000002", ParentSpanID: http.SpanID, Name: "chat gpt-4o",
		Attrs: map[string]any{"gen_ai.system": "openai"}}
	agent := obsdb.Span{TraceID: tr, SpanID: "6b00000000000003", Name: "invoke_agent planner"}
	weft := obsdb.Span{TraceID: tr, SpanID: "6b00000000000004", Name: "invoke_agent a", Attrs: map[string]any{"weft.run.id": "r1"}}
	for _, c := range []struct {
		name  string
		spans []obsdb.Span
		want  string
	}{
		{"plain http", []obsdb.Span{http}, "no GenAI span"},
		{"chat only", []obsdb.Span{http, chat}, "model or tool calls only"},
		{"an agent without a conversation id", []obsdb.Span{agent}, ""},
		{"weft", []obsdb.Span{weft}, ""},
		{"parent not arrived", []obsdb.Span{chat}, "its agent span has not arrived"},
	} {
		reason, fix := obsdb.StockNotRun(c.spans)
		if c.want == "" && (reason != "" || fix != "") || c.want != "" && (len(reason) < len(c.want) || reason[:len(c.want)] != c.want || fix == "") {
			t.Errorf("%s: StockNotRun = %q, %q; want a reason starting %q", c.name, reason, fix, c.want)
		}
	}
}

// StockFoldedRuns (I6-fix2): the agents without a conversation id that
// the latest derivation folds — announced Removed — and none of the
// roots it makes.
func TestStockFoldedRuns(t *testing.T) {
	const tr = "6f000000000000000000000000000001"
	outer := obsdb.Span{TraceID: tr, SpanID: "6f00000000000001", Name: "invoke_agent outer"}
	inner := obsdb.Span{TraceID: tr, SpanID: "6f00000000000002", ParentSpanID: outer.SpanID, Name: "invoke_agent inner",
		Attrs: map[string]any{"gen_ai.agent.name": "inner"}}
	conv := obsdb.Span{TraceID: tr, SpanID: "6f00000000000003", ParentSpanID: outer.SpanID, Name: "invoke_agent c",
		Attrs: map[string]any{"gen_ai.conversation.id": "c"}}
	if got := obsdb.StockFoldedRuns([]obsdb.Span{inner}); len(got) != 0 {
		t.Errorf("inner alone (top-level for now) folded = %v, want none", got)
	}
	got := obsdb.StockFoldedRuns([]obsdb.Span{outer, inner, conv})
	if len(got) != 1 || got[0].ID != obsdb.StockRunID(tr, inner.SpanID) || !got[0].Removed || !got[0].Derived || got[0].Agent != "inner" {
		t.Errorf("folded = %+v, want the inner agent alone, Removed", got)
	}
	if p := obsdb.StockRunPrefix("6F000000000000000000000000000001"); p != "otel-"+tr+"-" {
		t.Errorf("StockRunPrefix = %q", p)
	}
}
