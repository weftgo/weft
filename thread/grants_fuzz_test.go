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

	f.Fuzz(func(t *testing.T, grantJSON, argsJSON []byte, pointer, glob string) {
		var g Grant
		if err := json.Unmarshal(grantJSON, &g); err != nil {
			return // malformed JSON is loud; the wire shapes are pinned elsewhere
		}
		call := weft.ToolCallPart{ID: "c_fuzz", Name: g.Tool, Args: argsJSON}
		if grantMatches(g, call) != grantMatches(g, call) {
			t.Fatalf("nondeterministic match over grant %q, args %q", grantJSON, argsJSON)
		}
		wildcardMatch(glob, pointer) // never panics, whatever the pattern holds
		v1, ok1 := pointerValue(argsJSON, pointer)
		if ok1 {
			v2, ok2 := pointerValue(argsJSON, pointer)
			if !ok2 {
				t.Fatalf("pointerValue flipped on %q / %q", argsJSON, pointer)
			}
			if v1 == nil || v2 == nil {
				t.Fatalf("pointerValue found nil on %q / %q", argsJSON, pointer)
			}
		}
	})
}
