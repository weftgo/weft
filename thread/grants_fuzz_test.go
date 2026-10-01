package thread

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
)

// FuzzGrantMatches (step 7.1): the grant predicate engine over
// arbitrary grants and call arguments never panics and stays
// deterministic — the same grant and the same call answer the same way
// twice, whatever the bytes hold. The engine is internal, so this is
// an internal fuzz; the shapes it may see on the wire are pinned by
// the format goldens, and a hand-made Arg with several tests set uses
// Equals first, then Prefix, then Glob (the documented order).
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
	// The exact-reading rules (strict RFC 6901 indexes, a rune-wise ?,
	// integers compared digit for digit, an argument-less call as {}).
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"/a/01","equals":2}]}`),
		[]byte(`{"a":[1,2]}`), "/a/01", "?")
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"/a/-","equals":1}]}`),
		[]byte(`{"a":[1]}`), "/a/+1", "??")
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"/n","equals":9007199254740993}]}`),
		[]byte(`{"n":9007199254740992}`), "/n", "é?")
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"/n","equals":1.0}]}`),
		[]byte(`{"n":1e0}`), "/n", "世?界")
	f.Add([]byte(`{"tool":"x","args":[{"pointer":"","equals":{}}]}`),
		[]byte(nil), "", "\xff?")

	f.Fuzz(func(t *testing.T, grantJSON, argsJSON []byte, pointer, glob string) {
		var g Grant
		if err := json.Unmarshal(grantJSON, &g); err != nil {
			return // malformed JSON is loud; the wire shapes are pinned elsewhere
		}
		call := weft.ToolCallPart{ID: "c_fuzz", Name: g.Tool, Args: argsJSON}
		m1, m2 := grantMatches(g, call), grantMatches(g, call)
		if m1 != m2 {
			t.Fatalf("nondeterministic match over grant %q, args %q", grantJSON, argsJSON)
		}
		w1, w2 := wildcardMatch(glob, pointer), wildcardMatch(glob, pointer)
		if w1 != w2 { // never panics either, whatever the pattern holds
			t.Fatalf("nondeterministic wildcard %q over %q", glob, pointer)
		}
		// The pointer walk as the engine runs it: over the normalized
		// arguments (an argument-less call reads as {}), never the raw
		// bytes — pointer "" is the document itself, and the engine
		// never hands the walk an absent one.
		doc := normalArgs(argsJSON)
		v1, ok1 := pointerValue(doc, pointer)
		if ok1 {
			v2, ok2 := pointerValue(doc, pointer)
			if !ok2 {
				t.Fatalf("pointerValue flipped on %q / %q", argsJSON, pointer)
			}
			if v1 == nil || v2 == nil {
				t.Fatalf("pointerValue found nil on %q / %q", argsJSON, pointer)
			}
		}
	})
}
