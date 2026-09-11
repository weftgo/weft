package weft_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

var benchTool = weft.Tool("bench", "", func(_ context.Context, in struct {
	Q string `json:"q"`
}) (string, error) {
	return in.Q, nil
})

func benchCalls() []wefttest.Call {
	return []wefttest.Call{
		{Name: "bench", Args: `{"q":"a"}`},
		{Name: "bench", Args: `{"q":"b"}`},
		{Name: "bench", Args: `{"q":"c"}`},
		{Name: "bench", Args: `{"q":"d"}`},
	}
}

// BenchmarkGenerate measures a full two-step run: four tool calls fanned
// out in parallel, then a final reply. The script and agent are rebuilt
// each iteration, so the number includes agent construction — the
// per-run cost a caller actually pays.
func BenchmarkGenerate(b *testing.B) {
	calls := benchCalls()
	for b.Loop() {
		agt := weft.New(wefttest.Script(
			wefttest.ToolCalls(calls...),
			wefttest.Say("done"),
		), benchTool)
		if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStreamEvents measures the event path: the same run as
// BenchmarkGenerate, consumed through Run.Events.
func BenchmarkStreamEvents(b *testing.B) {
	calls := benchCalls()
	for b.Loop() {
		agt := weft.New(wefttest.Script(
			wefttest.ToolCalls(calls...),
			wefttest.Say("done"),
		), benchTool)
		n := 0
		for _, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
			if err != nil {
				b.Fatal(err)
			}
			n++
		}
		if n == 0 {
			b.Fatal("no events")
		}
	}
}

// BenchmarkToolConstruction measures schema reflection — the one-time
// cost of defining a tool.
func BenchmarkToolConstruction(b *testing.B) {
	for b.Loop() {
		_ = weft.Tool("lookup", "", func(_ context.Context, in struct {
			City string   `json:"city" jsonschema:"the city to look up"`
			Days *int     `json:"days,omitempty"`
			Tags []string `json:"tags,omitempty"`
		}) (string, error) {
			return "", nil
		})
	}
}

// BenchmarkMessageJSONRoundTrip measures the wire format: marshal and
// unmarshal a transcript with every part type.
func BenchmarkMessageJSONRoundTrip(b *testing.B) {
	msgs := []weft.Message{
		weft.User("hello"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "thinking"},
			weft.TextPart{Text: "calling"},
			weft.ToolCallPart{ID: "c1", Name: "bench", Args: json.RawMessage(`{"q":"x"}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "c1", Name: "bench", Content: "x"},
		}},
		weft.Assistant("done"),
	}
	for b.Loop() {
		data, err := json.Marshal(msgs)
		if err != nil {
			b.Fatal(err)
		}
		var out []weft.Message
		if err := json.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}
