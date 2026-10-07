package google

import (
	"errors"
	"math"
	"testing"

	"github.com/weftgo/weft/core"
)

// The empty-content rules of ADR 0013's 2026-09-14 amendment, ported
// from anthropic's convert_empty_test.go: a Content with zero parts
// serializes as {"role":...} and is API-rejected, so it never reaches
// the wire.

// A user message whose every part was empty text contributes no
// Content at all; a message with real text keeps exactly its text
// parts.
func TestEmptyUserContentSkipped(t *testing.T) {
	m := Model("m").(*model)
	contents, _, err := m.contents(core.ModelRequest{Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.Part{core.TextPart{Text: ""}}},
		core.User("real"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("contents = %d, want 1 (the empty user message skipped)", len(contents))
	}
	if p := contents[0].Parts[0]; p.Text != "real" {
		t.Errorf("surviving part = %+v, want the real text", p)
	}
}

// An assistant message with nothing sendable (only unsigned reasoning,
// which Gemini rejects and the adapter drops) contributes no Content —
// never a {"role":"model"} with zero parts.
func TestEmptyModelContentSkipped(t *testing.T) {
	m := Model("m").(*model)
	contents, _, err := m.contents(core.ModelRequest{Messages: []core.Message{
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ReasoningPart{Text: "hmm"}, // unsigned: dropped
		}},
		core.User("real"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("contents = %d, want 1 (the empty model content skipped)", len(contents))
	}
}

// The API's int32 ceilings fail loudly wrapping ErrUnsupported — a
// silent narrowing would wrap a large value into a garbage (possibly
// negative) limit on the wire.
func TestInt32CeilingsFailLoudly(t *testing.T) {
	m := Model("m", MaxTokens(math.MaxInt32+1)).(*model)
	_, _, err := m.contents(core.ModelRequest{Messages: []core.Message{core.User("hi")}})
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("MaxTokens over int32: err = %v, want ErrUnsupported", err)
	}
	m = Model("m").(*model)
	_, _, err = m.contents(core.ModelRequest{
		Messages: []core.Message{core.User("hi")},
		Thinking: core.ThinkingConfig{Budget: int64(math.MaxInt32) + 1},
	})
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Budget over int32: err = %v, want ErrUnsupported", err)
	}
}
