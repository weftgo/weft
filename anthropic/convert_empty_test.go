package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
)

// An empty tool result travels as a visible placeholder: the API
// rejects empty text inside tool_result blocks ("text content blocks
// must be non-empty"), and a perfectly legal Tool[In, string] handler
// returning "" must not fail the run's next model call.
func TestEmptyToolResultPlaceholder(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{
		Messages: []weft.Message{
			weft.User("q"),
			{Role: weft.RoleAssistant, Content: []weft.Part{
				weft.ToolCallPart{ID: "c1", Name: "t", Args: json.RawMessage(`{}`)},
			}},
			{Role: weft.RoleTool, Content: []weft.Part{
				weft.ToolResultPart{CallID: "c1", Name: "t"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolMsg := p.Messages[len(p.Messages)-1]
	if n := len(toolMsg.Content); n != 1 || toolMsg.Content[0].OfToolResult == nil {
		t.Fatalf("tool message has %d blocks, want one tool_result", n)
	}
	tr := toolMsg.Content[0].OfToolResult
	if len(tr.Content) != 1 || tr.Content[0].OfText == nil || tr.Content[0].OfText.Text != "(empty tool output)" {
		t.Fatalf("tool_result content = %+v, want the placeholder", tr.Content)
	}
}

// An assistant message with nothing sendable (only unsigned reasoning,
// which assistantBlocks drops) is skipped, not sent as an empty
// content array the API would reject.
func TestEmptyAssistantMessageSkipped(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{
		Messages: []weft.Message{
			weft.User("q"),
			{Role: weft.RoleAssistant, Content: []weft.Part{
				weft.ReasoningPart{Text: "unsigned, from another provider"},
			}},
			{Role: weft.RoleTool, Content: []weft.Part{
				weft.ToolResultPart{CallID: "c1", Name: "t", Content: "ok"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range p.Messages {
		if len(msg.Content) == 0 {
			t.Fatalf("message with role %q has an empty content array", msg.Role)
		}
	}
	if len(p.Messages) != 2 { // user + tool; the empty assistant is gone
		t.Fatalf("messages = %d, want 2 (empty assistant skipped)", len(p.Messages))
	}
}
