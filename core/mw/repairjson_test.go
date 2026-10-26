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

// Whitespace is trimmed around the arguments as 0.18.0 trimmed it,
// except at a cut inside an open string: there only JSON whitespace
// (space, tab, CR, LF) goes, and a raw NBSP, NEL, U+2028/2029,
// U+2000-200A or U+3000 the model ended it with is payload the
// repaired arguments keep byte-for-byte (0.18.0 trimmed any Unicode
// whitespace there, so "prix\u00a0" reached the tool as "prix").
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
	// Unicode whitespace outside the document is trimmed as 0.18.0
	// trimmed it: in front, behind a document that closes, before a
	// fence (NBSP before ``` opens one) and around its payload. Each
	// want is v0.18.0's repairJSON output for the same input.
	for _, c := range []struct{ in, want string }{
		{"\u00a0{\"a\":1", `{"a":1}`},
		{"\u00a0{\"a\":1}", `{"a":1}`},
		{"\u2028\u3000{\"s\":\"x\"}", `{"s":"x"}`},
		{"{\"a\":1}\u3000", `{"a":1}`},
		{"{\"a\":[1,2]}\u00a0\u2028", `{"a":[1,2]}`},
		{"\u00a0```json\n{\"a\":1}\n```", `{"a":1}`},
		{"\u3000```\n{\"a\":1}\n```\u00a0", `{"a":1}`},
		{"```json\n\u00a0{\"a\":1}\u3000\n```", `{"a":1}`},
		{"\u00a0```json\n{\"s\":\"x", `{"s":"x"}`},
	} {
		if got, ok := repairJSON(c.in); !ok || got != c.want {
			t.Errorf("repair(%q) = %q, %v; want %q, true (v0.18.0's)", c.in, got, ok, c.want)
		}
	}
}

// The repair kind RepairJSON reports (core.RepairInfo.Kind, the
// weft.repair event's weft.repair.kind; U23.1).
func TestRepairJSONKind(t *testing.T) {
	for _, c := range []struct{ in, kind string }{
		{"```json\n{\"a\":1}\n```", repairFence},
		{`{"a":"b`, repairUnterminated},
		{"```\n{\"a\":\"b", repairBoth},
		{"  {\"a\":1} ", repairTrim},
		{"   ", repairTrim},
	} {
		if _, kind, ok := repairJSONKind(c.in); !ok || kind != c.kind {
			t.Errorf("repairJSONKind(%q) = %q, %v; want %q", c.in, kind, ok, c.kind)
		}
	}
}
