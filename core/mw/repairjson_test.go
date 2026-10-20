package mw

import (
	"encoding/json"
	"strings"
	"testing"
)

// repairJSON's fence handling, pinned directly (it is unexported;
// retry_test.go sets the internal-test precedent): the strip must eat
// a language tag and only a language tag.
func TestRepairJSONFenceTag(t *testing.T) {
	if got, ok := repairJSON("```json\n{\"a\":1}\n```"); !ok || got != `{"a":1}` {
		t.Errorf("repair(json-tagged fence) = %q, %v; want `%s`, true", got, ok, `{"a":1}`)
	}
	// A tag followed by a space before the newline is still a tag.
	if got, ok := repairJSON("```json \n{\"a\":1}\n```"); !ok || got != `{"a":1}` {
		t.Errorf("repair(tag + trailing space) = %q, %v; want `%s`, true", got, ok, `{"a":1}`)
	}
	// A bare fence whose payload starts with the literal letters
	// "json": there is no tag (it does not end the line), so the
	// payload must survive byte-for-byte in the returned string —
	// repairJSON reports it honestly as not valid JSON rather than
	// eating four bytes to fabricate one.
	got, ok := repairJSON("```\njsonry{\"a\":1}\n```")
	if ok {
		t.Errorf("repair(bare fence, jsonry payload) = %q, true; want ok=false", got)
	}
	if !strings.Contains(got, "jsonry") {
		t.Errorf("repair(bare fence, jsonry payload) = %q; the payload's literal letters must survive", got)
	}
	if json.Valid([]byte(got)) {
		t.Errorf("repair(bare fence, jsonry payload) = %q; it is not valid JSON and must not be reported as content it fixed away", got)
	}
}

// Only JSON whitespace (space, tab, CR, LF) is trimmed around the
// arguments: a raw NBSP, NEL, U+2028/2029, U+2000–200A or U+3000 at
// the end of a truncated open string is payload the model wrote, and
// the repaired arguments keep it byte-for-byte (0.18.0 trimmed any
// Unicode whitespace, so "prix\u00a0" reached the tool as "prix").
func TestRepairJSONKeepsUnicodeWhitespace(t *testing.T) {
	for _, ws := range []string{"\u00a0", "\u0085", "\u2028", "\u2029", "\u2000", "\u200a", "\u3000", "\u00a0\u3000"} {
		in := `{"s":"prix` + ws
		want := in + `"}`
		if got, ok := repairJSON(in); !ok || got != want {
			t.Errorf("repair(%q) = %q, %v; want %q, true", in, got, ok, want)
		}
		// The same inside a fence the reply was cut in.
		if got, ok := repairJSON("```json\n" + in); !ok || got != want {
			t.Errorf("repair(fenced %q) = %q, %v; want %q, true", in, got, ok, want)
		}
		// An open array's string too.
		arr := `{"a":["x` + ws
		if got, ok := repairJSON(arr); !ok || got != arr+`"]}` {
			t.Errorf("repair(%q) = %q, %v; want %q, true", arr, got, ok, arr+`"]}`)
		}
	}
	// ASCII whitespace around a document is still trimmed, a fence
	// included — the 0.18.0 behaviour exactly.
	for _, c := range []struct{ in, want string }{
		{" \t\r\n{\"a\":1\n\t ", `{"a":1}`},
		{"\n  ```json\n{\"a\":1}\n```  \n", `{"a":1}`},
		{" ```\n{\"a\":\"x\n", `{"a":"x"}`},
		{"```json\n{\"a\":1}\n``` \r\n", `{"a":1}`},
		{"  \n", "{}"},
	} {
		if got, ok := repairJSON(c.in); !ok || got != c.want {
			t.Errorf("repair(%q) = %q, %v; want %q, true", c.in, got, ok, c.want)
		}
	}
	// Unicode whitespace outside the document is not JSON whitespace
	// either: it is not trimmed, and NBSP before ``` is no fence. Such
	// arguments are not repaired; their bytes pass through (the loop
	// then answers INVALID_INPUT, and the model writes them again).
	for _, c := range []struct{ in, keep string }{
		{"\u00a0{\"a\":1", "\u00a0{\"a\":1"},
		{"{\"a\":1}\u3000", "{\"a\":1}\u3000"},
		{"\u00a0```json\n{\"a\":1}\n```", "\u00a0```json"},
	} {
		got, ok := repairJSON(c.in)
		if ok {
			t.Errorf("repair(%q) = %q, true; want ok=false (Unicode whitespace is not JSON whitespace)", c.in, got)
		}
		if !strings.Contains(got, c.keep) {
			t.Errorf("repair(%q) = %q; want it to keep %q", c.in, got, c.keep)
		}
	}
}
