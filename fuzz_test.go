package weft_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weftgo/weft"
)

var fuzzTool = weft.Tool("fuzz", "", func(_ context.Context, in struct {
	A string   `json:"a"`
	B int      `json:"b"`
	C []string `json:"c,omitempty"`
}) (string, error) {
	return in.A, nil
})

// FuzzToolInvokeArgs checks the model-facing parser: decoding arbitrary
// argument bytes must never panic and must fail only with
// ErrInvalidToolInput-wrapped errors.
func FuzzToolInvokeArgs(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"a":"x","b":1}`, `{"b":"not a number"}`, `null`, `[]`,
		`{"c":["a","b"]}`, `{"a":`, `{"a":"\ud800"}`, `0`, `"str"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, args string) {
		out, err := fuzzTool.Invoke(context.Background(), json.RawMessage(args))
		if err != nil && !errors.Is(err, weft.ErrInvalidToolInput) {
			t.Fatalf("Invoke(%q) error = %v, want ErrInvalidToolInput", args, err)
		}
		_ = out
	})
}

// FuzzRepair checks the transcript normaliser: on any decodable input
// it is idempotent and restores the invariant (every assistant call id
// has exactly one result in the following message).
func FuzzRepair(f *testing.F) {
	for _, seed := range []string{
		`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`,
		`[{"role":"assistant","content":[{"type":"tool_call","id":"a","name":"t","args":{}}]}]`,
		`[{"role":"assistant","content":[{"type":"tool_call","id":"a","name":"t","args":{}}]},{"role":"tool","content":[{"type":"tool_result","call_id":"a","name":"t","content":"ok","is_error":false}]}]`,
		`[{"role":"tool","content":[{"type":"tool_result","call_id":"x","name":"","content":"","is_error":true}]}]`,
		`[]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		var msgs []weft.Message
		if err := json.Unmarshal([]byte(data), &msgs); err != nil {
			return
		}
		// Purity pin: the input must be byte-identical after Repair —
		// the promise on Repair's doc, checked for every fuzz input.
		before, err := json.Marshal(msgs)
		if err != nil {
			return
		}
		out := weft.Repair(msgs)
		after, _ := json.Marshal(msgs)
		if string(before) != string(after) {
			t.Fatalf("Repair mutated its input:\n was %s\n now %s", before, after)
		}
		once, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("repaired transcript does not re-encode: %v", err)
		}
		twice, _ := json.Marshal(weft.Repair(out))
		if string(once) != string(twice) {
			t.Fatalf("Repair not idempotent:\n once  %s\n twice %s", once, twice)
		}
		for i, m := range out {
			if m.Role != weft.RoleAssistant {
				continue // repair only normalises canonical roles
			}
			var calls []string
			for _, p := range m.Content {
				if c, ok := p.(weft.ToolCallPart); ok {
					calls = append(calls, c.ID)
				}
			}
			if len(calls) == 0 {
				continue
			}
			if i+1 >= len(out) || out[i+1].Role != weft.RoleTool {
				t.Fatalf("assistant with calls at %d not followed by a tool message: %s", i, once)
			}
			got := map[string]int{}
			for _, p := range out[i+1].Content {
				if r, ok := p.(weft.ToolResultPart); ok {
					got[r.CallID]++
				}
			}
			for _, id := range calls {
				if got[id] != 1 {
					t.Fatalf("call %s has %d results, want exactly 1: %s", id, got[id], once)
				}
			}
		}
	})
}

// FuzzUnmarshalEvent checks the event wire parser: any byte string
// either fails cleanly or decodes into an event that re-encodes to the
// same bytes and decodes again identically.
func FuzzUnmarshalEvent(f *testing.F) {
	for _, seed := range []string{
		`{"type":"run_start","id":"r1","model":{"provider":"openai","name":"gpt-5-mini"},"agent":"a"}`,
		`{"type":"run_start","id":"r2","model":{"provider":"","name":""}}`,
		`{"type":"step_start","index":1}`,
		`{"type":"text_delta","text":"hi"}`,
		`{"type":"reasoning_delta","text":"hm"}`,
		`{"type":"tool_start","seq":5,"call_id":"c1","name":"echo","args":{"m":"x"}}`,
		`{"type":"tool_start","seq":7,"call_id":"c2","name":"t","args":null}`,
		`{"type":"tool_finish","seq":6,"call_id":"c1","name":"echo","content":"ok","is_error":false}`,
		`{"type":"step_finish","index":1,"reason":"tool_calls","usage":{"input_tokens":10,"output_tokens":5}}`,
		`{"type":"step_finish","index":2,"reason":"stop","usage":{"input_tokens":100,"output_tokens":50,"cached_input_tokens":60,"cache_write_tokens":10,"reasoning_tokens":20}}`,
		`{"type":"run_finish","usage":{"input_tokens":100,"output_tokens":50,"cached_input_tokens":60,"cache_write_tokens":10,"reasoning_tokens":20},"steps":2}`,
		`{"type":"run_finish","usage":{"input_tokens":20,"output_tokens":10},"steps":2}`,
		`{"type":"hologram"}`,
		`{"text":"no type"}`,
		`{"type":"nested","run_id":"r1","seq":4,"call_id":"c1","event":{"type":"tool_start","run_id":"r1/0/c1","seq":1,"call_id":"call_1","name":"deep_search","args":{}}}`,
		`{"type":"nested","run_id":"r1","seq":5,"call_id":"c1","event":{"type":"nested","run_id":"r1/0/c1","seq":2,"call_id":"call_1","event":{"type":"text_delta","run_id":"r1/0/c1/0/call_1","text":"deep"}}}`,
		`{"type":"nested","run_id":"r1","seq":6,"call_id":"c1","event":{"type":"hologram"}}`,
		`{"type":"nested","run_id":"r1","seq":7,"call_id":"c1"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		ev, err := weft.UnmarshalEvent([]byte(data))
		if err != nil {
			return
		}
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("decoded event does not re-encode: %v\ndata: %s", err, data)
		}
		back, err := weft.UnmarshalEvent(b)
		if err != nil {
			t.Fatalf("re-decode failed: %v\nbytes: %s", err, b)
		}
		b2, err := json.Marshal(back)
		if err != nil || string(b2) != string(b) {
			t.Fatalf("round trip unstable:\n first %s\n second %s (%v)", b, b2, err)
		}
	})
}

// FuzzMessageUnmarshal checks the wire-facing parser: any byte string
// either fails cleanly or decodes into a message that re-encodes.
func FuzzMessageUnmarshal(f *testing.F) {
	for _, seed := range []string{
		`{"role":"user","content":[{"type":"text","text":"hi"}]}`,
		`{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"t","args":{"x":1}}]}`,
		`{"role":"user","content":[{"type":"hologram"}]}`,
		`{"role":"user"}`,
		`{"role":"tool","content":[{"type":"tool_result","call_id":"","name":"","content":"","is_error":false}]}`,
		`{"role":"user","content":[{"type":"file","media_type":"image/png","data":"cG5n"}]}`,
		`{"role":"user","content":[{"type":"file","media_type":"image/png","url":"https://x.test/a.png"}]}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		var m weft.Message
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			return
		}
		if _, err := json.Marshal(m); err != nil {
			t.Fatalf("decoded message does not re-encode: %v\ndata: %s", err, data)
		}
	})
}
