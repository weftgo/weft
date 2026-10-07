package thread

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft/core"
)

// TestPointerArrayIndexStrict: an array reference token is RFC 6901's
// own grammar — "0", or digits without a leading zero — so "/01",
// "/+1", "/-0" and "/ 1" reach nothing instead of quietly naming
// element 1 (strconv.Atoi's reading).
func TestPointerArrayIndexStrict(t *testing.T) {
	doc := json.RawMessage(`{"a":["x","y","z"]}`)
	for _, tc := range []struct {
		pointer string
		want    string
		ok      bool
	}{
		{"/a/0", `"x"`, true},
		{"/a/1", `"y"`, true},
		{"/a/2", `"z"`, true},
		{"/a/01", "", false},
		{"/a/+1", "", false},
		{"/a/-0", "", false},
		{"/a/ 1", "", false},
		{"/a/1 ", "", false},
		{"/a/00", "", false},
		{"/a/3", "", false},
		{"/a/-", "", false},
		{"/a/", "", false},
		{"/a/99999999999999999999999", "", false},
	} {
		got, ok := pointerValue(doc, tc.pointer)
		if ok != tc.ok || string(got) != tc.want {
			t.Errorf("pointerValue(%q) = %q, %v; want %q, %v", tc.pointer, got, ok, tc.want, tc.ok)
		}
	}
}

// TestWildcardRuneWise: ? matches exactly one character — one rune,
// never one byte of a multi-byte character — and * still spans
// anything; there is no escape, so a literal ? or * cannot be asked
// for.
func TestWildcardRuneWise(t *testing.T) {
	for _, tc := range []struct {
		pattern, s string
		want       bool
	}{
		{"?", "é", true},
		{"??", "é", false},
		{"テ?ト", "テスト", true},
		{"テ??ト", "テスト", false},
		{"go test ?", "go test é", true},
		{"*ト", "テスト", true},
		{"?", "", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{`\?`, "?", false}, // no escape: the backslash is itself
		{`\?`, `\x`, true},
		{"", "", true},
		{"*", "", true},
		{"a*b", "a*xb", true}, // a * in the value is just a character the pattern's * spans
		{"*", "**", true},
	} {
		if got := wildcardMatch(tc.pattern, tc.s); got != tc.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
		}
	}
}

// TestArgEqualsNumbers: two integers compare exactly — beyond 2^53,
// where a float64 comparison calls neighbours equal — and anything
// with a fraction or an exponent compares as a float64 (1 equals 1.0
// equals 1e0).
func TestArgEqualsNumbers(t *testing.T) {
	for _, tc := range []struct {
		equals, args string
		want         bool
	}{
		{`9007199254740993`, `{"n":9007199254740993}`, true},
		{`9007199254740993`, `{"n":9007199254740992}`, false}, // equal as float64
		{`-0`, `{"n":0}`, true},
		{`3`, `{"n":3.0}`, true},
		{`3.0`, `{"n":3}`, true},
		{`1e2`, `{"n":100}`, true},
		{`3`, `{"n":4}`, false},
		{`3`, `{"n":"3"}`, false},
		{`[9007199254740993]`, `{"n":[9007199254740992]}`, false},
	} {
		a := ArgEquals("/n", json.RawMessage(tc.equals))
		if got := argMatches(a, json.RawMessage(tc.args)); got != tc.want {
			t.Errorf("ArgEquals(%s) over %s = %v, want %v", tc.equals, tc.args, got, tc.want)
		}
	}
}

// TestExactArgsGrantMatchesEmptyArgs: "approve and always allow" over
// a call that carried no arguments mints a grant that matches the next
// such call — absent arguments read as the empty object on both sides
// — and never a call that carries some.
func TestExactArgsGrantMatchesEmptyArgs(t *testing.T) {
	for _, args := range []json.RawMessage{nil, {}, json.RawMessage(` `), json.RawMessage(`{}`)} {
		g := exactArgsGrant("ping", args)
		for _, again := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(` {} `)} {
			if !grantMatches(g, core.ToolCallPart{Name: "ping", Args: again}) {
				t.Errorf("grant minted over %q does not match args %q", args, again)
			}
		}
		if grantMatches(g, core.ToolCallPart{Name: "ping", Args: json.RawMessage(`{"force":true}`)}) {
			t.Errorf("grant minted over %q matches a call with arguments", args)
		}
	}
	g := exactArgsGrant("run", json.RawMessage(`{"command":"ls"}`))
	if grantMatches(g, core.ToolCallPart{Name: "run"}) {
		t.Error("an exact-arguments grant matches a call with none")
	}
}
