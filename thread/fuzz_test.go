package thread_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/weftgo/weft/thread"
)

// FuzzDecodeEntry: UnmarshalEntry over arbitrary bytes
// never panics, whatever it accepts always re-encodes, and the wire's
// canonical form is a fixpoint — decode∘encode once, and a second
// decode∘encode yields the same value and the same bytes again. The
// first pass is allowed to normalize (a case-insensitive key match
// re-cases, an absent nullable becomes explicit: encoding/json's
// standard leniency, the same one the root's message wire has always
// had); what must never happen is drift that keeps drifting, or a
// decoded value that cannot encode. The loud rules stay rules under
// fuzz: only the decoder's own errors come back, never a wrong value;
// the ErrNewerFormat cases are pinned exactly by TestUnmarshalEntryLoud.
func FuzzDecodeEntry(f *testing.F) {
	// Seeds: every committed golden line of every format — the pins
	// the decoder must always accept (the header goldens among them
	// are lines it must always refuse) — and the hostile shapes. The
	// glob takes a new format's directory the day it exists.
	files, err := filepath.Glob(filepath.Join("testdata", "format*", "*"))
	if err != nil {
		f.Fatal(err)
	}
	if len(files) == 0 {
		f.Fatal("no goldens found")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n")) {
			f.Add(line)
		}
	}
	f.Add([]byte(`{"type":"approval","id":"e_1"}`))                      // unknown kind
	f.Add([]byte(`{"type":"message","v":2,"id":"e_1"}`))                 // newer version
	f.Add([]byte(`{"type":"approval_request","v":3,"id":"e_1"}`))        // newer approvals v
	f.Add([]byte(`{"type":"pool_receipt","v":5,"id":"e_1"}`))            // newer pool v
	f.Add([]byte(`{"type":"pool_receipt","id":"e_1","status":"weird"}`)) // hostile status
	f.Add([]byte(`{"type":"pool_receipt","id":"e_1","status":"done","usage":{"input_tokens":-1e300}}`))
	f.Add([]byte(`{"type":"approval_request","id":"e_1","child":"s_1","wrapper":"call_1"}`)) // a mirror
	f.Add([]byte(`{"type":"approval_decision","outcome":"maybe"}`))                          // hostile outcome
	f.Add([]byte(`{"type":"approval_audit","step":""}`))                                     // hostile step
	f.Add([]byte(`{"id":"e_1"}`))                                                            // no type
	f.Add([]byte(`{"tYpe":"custom"}`))                                                       // case-matched key
	f.Add([]byte(`{"type":"message",`))                                                      // malformed
	f.Add([]byte(`[]`))                                                                      // not an object
	f.Add([]byte(""))                                                                        // empty
	f.Add([]byte(`{"type":"label","name":"héllo 世界","entry":""}`))                           // unicode
	f.Add([]byte(`{"type":"turn","usage":{"input_tokens":-1e300}}`))                         // hostile numbers
	f.Add([]byte(`{"type":"message","v":-1,"id":"e_1"}`))                                    // negative v reads as v1
	f.Add([]byte(`{"type":"custom","id":"e_1","kind":"k"}`))                                 // absent data stays nil
	// The shapes the 2026-10-01 fixes added to the wire, in hostile
	// form: a trim record that names nothing, a newer compaction, an
	// accepted receipt without its message, a parked pool receipt with
	// a settlement's fields, audit and decision entries with the new
	// fields mistyped or empty.
	f.Add([]byte(`{"type":"compaction","v":5,"id":"e_1","reason":"trim","trim":{"stubs":[]}}`))
	f.Add([]byte(`{"type":"compaction","v":5,"id":"e_1","reason":"trim","trim":{"stubs":[{"entry":"","call_id":"","content":""}]}}`))
	f.Add([]byte(`{"type":"compaction","v":5,"id":"e_1","reason":"trim","trim":null}`))
	f.Add([]byte(`{"type":"compaction","v":6,"id":"e_1","reason":"trim"}`))
	f.Add([]byte(`{"type":"compaction","id":"e_1","reason":"trim","trim":{"stubs":[{"entry":"e_0","call_id":"c","content":"x","is_error":true}]}}`))
	f.Add([]byte(`{"type":"receipt","v":3,"id":"e_1","status":"accepted","turn":"e_2","run_id":"s-t9"}`))
	f.Add([]byte(`{"type":"receipt","v":3,"id":"e_1","status":"accepted","msg":null,"turn":"","unanswered":true}`))
	f.Add([]byte(`{"type":"pool_receipt","v":4,"id":"e_1","receipt":"e_0","status":"parked","stop":"x","usage":{"input_tokens":1}}`))
	f.Add([]byte(`{"type":"approval_audit","v":2,"id":"e_1","step":"resume","outcome":"completed","decisions":[]}`))
	f.Add([]byte(`{"type":"approval_audit","v":2,"id":"e_1","step":"grant","grant_id":"","grant_shared":true,"key_id":"k"}`))
	f.Add([]byte(`{"type":"approval_audit","v":2,"id":"e_1","step":"signed","decisions":"not a list"}`))
	f.Add([]byte(`{"type":"approval_decision","v":2,"id":"e_1","outcome":"approve","request_id":"e_0","always":true,"via":"interrupt"}`))
	f.Add([]byte(`{"type":"message","id":"e_1","run_id":"s-t1","message":{"role":"user","content":[]}}`))
	f.Add([]byte(`{"type":"turn","id":"e_1","policy":"rollback","re_run":"s-t2","late_steps":-1,"canceled":true}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		e1, err := thread.UnmarshalEntry(data)
		if err != nil {
			return // loud is loud; the shapes are pinned elsewhere
		}
		b1, err := json.Marshal(e1)
		if err != nil {
			t.Fatalf("decoded %q does not re-encode: %v", data, err)
		}
		e2, err := thread.UnmarshalEntry(b1)
		if err != nil {
			t.Fatalf("re-encoded %q does not decode: %v", b1, err)
		}
		b2, err := json.Marshal(e2)
		if err != nil {
			t.Fatalf("canonical %q does not re-encode: %v", b1, err)
		}
		e3, err := thread.UnmarshalEntry(b2)
		if err != nil {
			t.Fatalf("canonical %q does not decode: %v", b2, err)
		}
		if !reflect.DeepEqual(e2, e3) || !bytes.Equal(b1, b2) {
			t.Fatalf("the canonical form is not a fixpoint:\n in   %q\n once %q\n twice %q", data, b1, b2)
		}
	})
}
