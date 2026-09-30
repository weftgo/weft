package thread

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
)

// FuzzGrantMatches (step 7.1): the grant predicate engine over
// arbitrary grants and call arguments never panics and answers against
// oracles, whatever the bytes hold: a call naming another tool never
// matches; a value the pointer reaches matches the Equals grant built
// from it, and a string value its own Prefix and its own Glob; and the
// glob matcher agrees with a plain dynamic-programming matcher. The
// engine is internal, so this is an internal fuzz; the shapes it may
// see on the wire are pinned by the format goldens.
func FuzzGrantMatches(f *testing.F) {
	f.Add([]byte(`{"tool":"refund","args":[{"pointer":"/order_id","equals":"1"}]}`),
		[]byte(`{"order_id":"1"}`), "/order_id", "refund*")
	f.Add([]byte(`{"tool":"fs","args":[{"pointer":"/file/path","glob":"*.tmp"}]}`),
		[]byte(`{"file":{"path":"/tmp/a.tmp"}}`), "/file/path", "*.tmp")
	f.Add([]byte(`{"tool":"shell","args":[{"pointer":"/command","prefix":"ls"}]}`),
		[]byte(`{"command":"ls -la"}`), "/command", "ls*")
	f.Add([]byte(`{"tool":"refund","deny":true,"reason":"no refunds"}`),
		[]byte(`{}`), "", "")
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"/deep/missing","prefix":"a"}]}`),
		[]byte(`{"deep":{"other":1}}`), "/deep/missing", "")
	f.Add([]byte(`{"tool":"x","expiry":"2026-01-01T00:00:00Z","max_uses":3}`),
		[]byte(`null`), "", "")
	f.Add([]byte(`{}`), []byte(``), "/", "*")
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"","equals":null}]}`),
		[]byte(`{"a":[1,2,{"b":"c"}]}`), "/a/2/b", "{*")
	f.Add([]byte(`{"tool":"unicode","args":[{"pointer":"/名前","equals":"テスト"}]}`),
		[]byte(`{"名前":"テスト"}`), "/名前", "テ*")
	f.Add([]byte(`{"tool":"x"}`), []byte(`{"a~b":{"c/d":[1e3,1000.0]}}`), "/a~0b/c~1d/1", "*?*")

	f.Fuzz(func(t *testing.T, grantJSON, argsJSON []byte, pointer, glob string) {
		var g Grant
		if err := json.Unmarshal(grantJSON, &g); err != nil {
			return // malformed JSON is loud; the wire shapes are pinned elsewhere
		}
		call := weft.ToolCallPart{ID: "c_fuzz", Name: g.Tool, Args: argsJSON}
		_ = grantMatches(g, call) // never panics
		call.Name = g.Tool + "_other"
		if grantMatches(g, call) {
			t.Fatalf("grant for %q matched a call naming %q", g.Tool, call.Name)
		}
		if got, want := wildcardMatch(glob, pointer), refWildcard(glob, pointer); got != want {
			t.Fatalf("wildcardMatch(%q, %q) = %v, the reference says %v", glob, pointer, got, want)
		}
		v, ok := pointerValue(argsJSON, pointer)
		if !ok || !json.Valid(v) {
			return
		}
		call.Name = "t"
		if !grantMatches(Grant{Tool: "t", Args: []Arg{ArgEquals(pointer, v)}}, call) {
			t.Fatalf("args %q at %q: the Equals grant over the value it reaches (%s) did not match", argsJSON, pointer, v)
		}
		if str, isStr := jsonString(v); isStr && str != "" {
			if !grantMatches(Grant{Tool: "t", Args: []Arg{ArgPrefix(pointer, str)}}, call) {
				t.Fatalf("args %q at %q: the string's own prefix did not match", argsJSON, pointer)
			}
			if !grantMatches(Grant{Tool: "t", Args: []Arg{ArgGlob(pointer, str)}}, call) {
				t.Fatalf("args %q at %q: the string as its own glob did not match", argsJSON, pointer)
			}
		}
	})
}

// refWildcard is the oracle for wildcardMatch: the textbook dynamic
// program over bytes — * any run, ? one byte, everything else itself.
func refWildcard(pattern, s string) bool {
	// dp[j]: pattern[:i] matches s[:j], rolled over i.
	dp := make([]bool, len(s)+1)
	dp[0] = true
	for i := 0; i < len(pattern); i++ {
		next := make([]bool, len(s)+1)
		if pattern[i] == '*' {
			next[0] = dp[0]
			for j := 1; j <= len(s); j++ {
				next[j] = dp[j] || next[j-1]
			}
		} else {
			for j := 1; j <= len(s); j++ {
				next[j] = dp[j-1] && (pattern[i] == '?' || pattern[i] == s[j-1])
			}
		}
		dp = next
	}
	return dp[len(s)]
}
