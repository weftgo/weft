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
// user and last assistant message with text; a body that does not
// parse is no text, never an error.
func TestMessagesText(t *testing.T) {
	body := []byte(`[
		{"role":"user","content":[{"type":"file","media_type":"image/png"}]},
		{"role":"user","content":[{"type":"text","text":"one"},{"type":"tool_result","content":"no"},{"type":"text","text":"two"}]},
		{"role":"assistant","content":[{"type":"text","text":"answer"}]},
		{"role":"assistant","content":[{"type":"tool_call","name":"x"}]}]`)
	if got, ok := obsdb.FirstUserText(body); !ok || got != "one\ntwo" {
		t.Errorf("FirstUserText = %q, %v", got, ok)
	}
	if got, ok := obsdb.LastAssistantText(body); !ok || got != "answer" {
		t.Errorf("LastAssistantText = %q, %v", got, ok)
	}
	if _, ok := obsdb.FirstUserText([]byte(`not json`)); ok {
		t.Error("FirstUserText of a malformed body found text")
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
