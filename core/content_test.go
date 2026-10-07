package core_test

// StripContent's table (ADR 0024 S1.4), pinned on the wire: what a
// content-off destination receives. The exact bytes matter — "args
// becomes null" and "messages becomes []" are contract, and the identity
// fields must survive so a stripped record still attributes and orders.

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft/core"
)

func TestStripContent(t *testing.T) {
	cases := []struct {
		name string
		in   core.Event
		want string
	}{
		{
			name: "text_delta keeps run_id, empties text",
			in:   core.TextDelta{RunID: "r", Text: "hello"},
			want: `{"type":"text_delta","run_id":"r","text":""}`,
		},
		{
			name: "reasoning_delta keeps run_id, empties text",
			in:   core.ReasoningDelta{RunID: "r", Text: "thinking…"},
			want: `{"type":"reasoning_delta","run_id":"r","text":""}`,
		},
		{
			name: "tool_args_delta keeps run_id and name, empties args",
			in:   core.ToolArgsDelta{RunID: "r", Name: "lookup", Args: `{"id":1}`},
			want: `{"type":"tool_args_delta","run_id":"r","name":"lookup","args":""}`,
		},
		{
			name: "tool_start args become null",
			in:   core.ToolStart{RunID: "r", Seq: 2, CallID: "c1", Name: "lookup", Args: json.RawMessage(`{"id":1}`)},
			want: `{"type":"tool_start","run_id":"r","seq":2,"call_id":"c1","name":"lookup","args":null}`,
		},
		{
			name: "tool_finish empties content, keeps is_error",
			in:   core.ToolFinish{RunID: "r", Seq: 3, CallID: "c1", Name: "lookup", Content: `{"status":"shipped"}`, IsError: true},
			want: `{"type":"tool_finish","run_id":"r","seq":3,"call_id":"c1","name":"lookup","content":"","is_error":true}`,
		},
		{
			name: "steered messages become []",
			in:   core.Steered{RunID: "r", Seq: 4, Step: 1, Messages: []core.Message{core.User("wait")}},
			want: `{"type":"steered","run_id":"r","seq":4,"step":1,"messages":[]}`,
		},
		{
			name: "run_finish keeps usage, steps and pending ids, drops pending args",
			in: core.RunFinish{RunID: "r", Usage: core.Usage{InputTokens: 10, OutputTokens: 5}, Steps: 2,
				Pending: []core.ToolCallPart{{ID: "c9", Name: "refund", Args: json.RawMessage(`{"order":"42"}`)}}},
			want: `{"type":"run_finish","run_id":"r","usage":{"input_tokens":10,"output_tokens":5},"steps":2,"pending":[{"type":"tool_call","id":"c9","name":"refund","args":null}]}`,
		},
		{
			name: "no-content events pass through unchanged",
			in:   core.StepFinish{RunID: "r", Index: 1, Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 1}},
			want: `{"type":"step_finish","run_id":"r","index":1,"reason":"stop","usage":{"input_tokens":1,"output_tokens":0}}`,
		},
		{
			name: "nested recurses into the child event",
			in: core.Nested{RunID: "r", Seq: 7, CallID: "c2",
				Event: core.TextDelta{RunID: "r/0/c2", Text: "child text"}},
			want: `{"type":"nested","run_id":"r","seq":7,"call_id":"c2","event":{"type":"text_delta","run_id":"r/0/c2","text":""}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(core.StripContent(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("stripped = %s\n       want %s", got, tc.want)
			}
		})
	}
}

// A stripped event still decodes: stripping keeps the wire shape, so a
// destination can UnmarshalEvent what StripContent produced.
func TestStripContentStillUnmarshals(t *testing.T) {
	in := core.ToolStart{RunID: "r", Seq: 2, CallID: "c1", Name: "lookup", Args: json.RawMessage(`{"id":1}`)}
	b, err := json.Marshal(core.StripContent(in))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := core.UnmarshalEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	start, ok := ev.(core.ToolStart)
	if !ok {
		t.Fatalf("decoded %T, want ToolStart", ev)
	}
	if start.Name != "lookup" || start.CallID != "c1" || string(start.Args) != "null" {
		t.Errorf("decoded = %+v, want identity kept and args null", start)
	}
}
