package obsdb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// ParseSearch's grammar: whitespace separates terms, a quoted span is
// one term with its spaces, an unclosed quote runs to the end, an empty
// quote is nothing, operators are text.
func TestParseSearch(t *testing.T) {
	for in, want := range map[string][]string{
		"":                         nil,
		"   ":                      nil,
		"refund failed":            {"refund", "failed"},
		`"refund failed" card`:     {"refund failed", "card"},
		`a"b c"d`:                  {"a", "b c", "d"},
		`"open quote runs on`:      {"open quote runs on"},
		`"" x`:                     {"x"},
		"refund AND failed OR x*":  {"refund", "AND", "failed", "OR", "x*"},
		"tab\tand\nnewline":        {"tab", "and", "newline"},
		`"  spaced  phrase  "`:     {"  spaced  phrase  "},
		`{prompt} : "x" NEAR(a b)`: {"{prompt}", ":", "x", "NEAR(a", "b)"},
	} {
		got, err := obsdb.ParseSearch(in)
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("ParseSearch(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := obsdb.ParseSearch(strings.Repeat("a ", obsdb.MaxSearchTerms)); err != nil {
		t.Errorf("%d terms: %v", obsdb.MaxSearchTerms, err)
	}
	for _, in := range []string{strings.Repeat("a ", obsdb.MaxSearchTerms+1), strings.Repeat("a", obsdb.MaxSearchTermBytes+1)} {
		if _, err := obsdb.ParseSearch(in); !errors.Is(err, obsdb.ErrInvalidSearch) {
			t.Errorf("ParseSearch(%d bytes) = %v, want ErrInvalidSearch", len(in), err)
		}
	}
}

// The search fields' text: text parts only, joined by "\n"; the first
// user (the input record's last) and last assistant message with text; a body that does not
// parse is no text, never an error.
func TestMessagesText(t *testing.T) {
	body := []byte(`[
		{"role":"user","content":[{"type":"file","media_type":"image/png"}]},
		{"role":"user","content":[{"type":"text","text":"one"},{"type":"tool_result","content":"no"},{"type":"text","text":"two"}]},
		{"role":"assistant","content":[{"type":"text","text":"answer"}]},
		{"role":"assistant","content":[{"type":"tool_call","name":"x"}]}]`)
	if got, ok := obsdb.PromptText(body, false); !ok || got != "one\ntwo" {
		t.Errorf("PromptText(not input) = %q, %v; want the first user text", got, ok)
	}
	// The input record: the turn's question is its last user message.
	turn := []byte(`[
		{"role":"user","content":[{"type":"text","text":"Where is order 1?"}]},
		{"role":"assistant","content":[{"type":"text","text":"Shipped."}]},
		{"role":"user","content":[{"type":"text","text":"Can I get a refund?"}]},
		{"role":"user","content":[{"type":"text","text":""}]}]`)
	if got, ok := obsdb.PromptText(turn, true); !ok || got != "Can I get a refund?" {
		t.Errorf("PromptText(input) = %q, %v; want the last user text", got, ok)
	}
	if got, _ := obsdb.PromptText(turn, false); got != "Where is order 1?" {
		t.Errorf("PromptText(not input) = %q; want the first user text", got)
	}
	if _, ok := obsdb.PromptText([]byte(`[{"role":"assistant","content":[{"type":"text","text":"x"}]}]`), true); ok {
		t.Error("PromptText of a record with no user text found one")
	}
	if got, ok := obsdb.LastAssistantText(body); !ok || got != "answer" {
		t.Errorf("LastAssistantText = %q, %v", got, ok)
	}
	if _, ok := obsdb.PromptText([]byte(`not json`), true); ok {
		t.Error("PromptText of a malformed body found text")
	}
	if got := obsdb.MessagesText([]byte(`[]`)); len(got) != 0 {
		t.Errorf("MessagesText([]) = %v", got)
	}
}

func TestPromptPreview(t *testing.T) {
	if got := obsdb.PromptPreview("short"); got != "short" {
		t.Errorf("PromptPreview(short) = %q", got)
	}
	long := strings.Repeat("ü", obsdb.PromptPreviewRunes+5)
	if got := obsdb.PromptPreview(long); got != strings.Repeat("ü", obsdb.PromptPreviewRunes) {
		t.Errorf("PromptPreview cut at %d runes, want %d", len([]rune(got)), obsdb.PromptPreviewRunes)
	}
}

func TestCheckSearch(t *testing.T) {
	for _, q := range []obsdb.SearchQuery{{}, {Sort: obsdb.SortStarted}, {Sort: obsdb.SortDuration}, {Sort: obsdb.SortTokens, Offset: obsdb.MaxSearchOffset}} {
		if _, err := obsdb.CheckSearch(q); err != nil {
			t.Errorf("CheckSearch(%+v) = %v", q, err)
		}
	}
	for _, q := range []obsdb.SearchQuery{{Sort: "cost"}, {Offset: obsdb.MaxSearchOffset + 1}, {Offset: -1}, {MinTokens: -1},
		{Tool: strings.Repeat("t", obsdb.MaxSearchTermBytes+1)}} {
		if _, err := obsdb.CheckSearch(q); !errors.Is(err, obsdb.ErrInvalidSearch) {
			t.Errorf("CheckSearch(%+v) = %v, want ErrInvalidSearch", q, err)
		}
	}
}

// DeriveRecord reads the two flags search needs as the transcript
// readers do: weft.messages.input (bool or "true"), and a
// weft.messages.reason of any type as not growth.
func TestDeriveRecordMessagesFlags(t *testing.T) {
	for _, c := range []struct {
		attrs  map[string]any
		reason string
		input  bool
	}{
		{map[string]any{}, "", false},
		{map[string]any{"weft.messages.input": true}, "", true},
		{map[string]any{"weft.messages.input": "true"}, "", true},
		{map[string]any{"weft.messages.reason": "compacted"}, "compacted", false},
		{map[string]any{"weft.messages.reason": int64(7)}, "7", false},
		{map[string]any{"weft.messages.reason": false}, "false", false},
	} {
		c.attrs["weft.record"] = "messages"
		w := obsdb.DeriveRecord(obsdb.Record{Attrs: c.attrs})
		if w.Reason != c.reason || w.Input != c.input {
			t.Errorf("DeriveRecord(%v) = reason %q input %v, want %q %v", c.attrs, w.Reason, w.Input, c.reason, c.input)
		}
	}
}

// TurnPrompt's resume rule: an input record cut at an assistant tool
// call takes the next record's user text before its first assistant
// message; otherwise — no cut, no next record, no such user text — the
// input record's last user text.
func TestTurnPrompt(t *testing.T) {
	const (
		cut   = `[{"role":"user","content":[{"type":"text","text":"Q1"}]},{"role":"assistant","content":[{"type":"tool_call","id":"c","name":"x","args":{}}]}]`
		plain = `[{"role":"user","content":[{"type":"text","text":"Q1"}]},{"role":"assistant","content":[{"type":"text","text":"A"}]}]`
		next  = `[{"role":"tool","content":[{"type":"tool_result","call_id":"c","content":"ok"}]},{"role":"user","content":[{"type":"text","text":"Q2"}]},{"role":"assistant","content":[{"type":"text","text":"A"}]}]`
		bare  = `[{"role":"tool","content":[{"type":"tool_result","call_id":"c","content":"ok"}]},{"role":"assistant","content":[{"type":"text","text":"A"}]},{"role":"user","content":[{"type":"text","text":"steered"}]}]`
	)
	for _, c := range []struct {
		name, input, next, want string
	}{
		{"a resume with a new prompt", cut, next, "Q2"},
		{"a plain resume", cut, bare, "Q1"}, // user text after the first assistant message never qualifies
		{"the next record not stored yet", cut, "", "Q1"},
		{"no cut: the next record is growth", plain, next, "Q1"},
	} {
		var nb []byte
		if c.next != "" {
			nb = []byte(c.next)
		}
		if got, _ := obsdb.TurnPrompt([]byte(c.input), nb); got != c.want {
			t.Errorf("%s: TurnPrompt = %q, want %q", c.name, got, c.want)
		}
	}
}
