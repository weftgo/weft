package openai

import (
	"testing"

	"github.com/weftgo/weft"
)

// The empty-content rules of ADR 0013's 2026-09-14 amendment, ported
// from anthropic's convert_empty_test.go: nothing the API would reject
// — an empty content array, a contentless assistant message, an empty
// arguments string — ever reaches the wire.

// An assistant message with no text and no tool calls (reasoning only,
// which Chat Completions cannot carry back) is skipped entirely, never
// emitted as `{"role":"assistant"}`.
func TestEmptyAssistantMessageSkipped(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{Messages: []weft.Message{
		weft.User("hi"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "hmm", Signature: "sig"}, // dropped: no reasoning input
		}},
		weft.User("again"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Messages) != 2 { // user and user; the empty assistant is gone
		t.Fatalf("messages = %d, want 2 (the empty assistant skipped)", len(p.Messages))
	}
	for _, msg := range p.Messages {
		switch {
		case msg.OfAssistant != nil:
			t.Fatalf("an empty assistant message reached the wire: %+v", msg.OfAssistant)
		case msg.OfUser != nil:
			if len(msg.OfUser.Content.OfArrayOfContentParts) == 0 {
				t.Fatalf("user message with an empty content array: %+v", msg.OfUser)
			}
		}
	}
}

// Empty assistant text contributes nothing, and a tool call with nil
// arguments travels as `{}` — never as arguments:"" (invalid JSON) or
// null.
func TestAssistantEmptyTextAndNilArgsNeverReachTheWire(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{Messages: []weft.Message{
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.TextPart{Text: ""},
			weft.ToolCallPart{ID: "c1", Name: "ping"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(p.Messages))
	}
	am := p.Messages[0].OfAssistant
	if am == nil || len(am.ToolCalls) != 1 {
		t.Fatalf("assistant = %+v, want only the tool call (empty text dropped)", am)
	}
	if got := am.ToolCalls[0].Function.Arguments; got != "{}" {
		t.Errorf("arguments = %q, want {} (nil args must not reach the wire as \"\")", got)
	}
	if am.Content.OfString.Valid() && am.Content.OfString.Value == "" {
		t.Error("empty content string reached the wire")
	}
}

// A user message whose every part was empty keeps a visible placeholder
// — an empty content array is API-rejected.
func TestEmptyUserMessageKeepsPlaceholder(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{Messages: []weft.Message{
		{Role: weft.RoleUser, Content: []weft.Part{weft.TextPart{Text: ""}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parts := p.Messages[0].OfUser.Content.OfArrayOfContentParts
	if len(parts) != 1 || parts[0].OfText == nil || parts[0].OfText.Text != "(empty message)" {
		t.Fatalf("user content = %+v, want the placeholder", parts)
	}
}

// The parallel-tool-call hint is never sent without a tool catalog —
// several OpenAI-compatible servers reject it there (the anthropic
// adapter's guard, ported).
func TestSequentialHintWithoutToolsIsNotSent(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{
		Messages:        []weft.Message{weft.User("hi")},
		SequentialTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ParallelToolCalls.Valid() {
		t.Errorf("parallel_tool_calls = %v, want unset with an empty tool catalog", p.ParallelToolCalls.Value)
	}
	p, err = m.params(weft.ModelRequest{
		Messages:        []weft.Message{weft.User("hi")},
		Tools:           []*weft.ToolDef{testTool()},
		SequentialTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ParallelToolCalls.Valid() || p.ParallelToolCalls.Value {
		t.Errorf("parallel_tool_calls = %+v, want false alongside a catalog", p.ParallelToolCalls)
	}
}
