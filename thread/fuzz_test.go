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

// FuzzDecodeEntry (step 1.2): UnmarshalEntry over arbitrary bytes
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
	// Seeds: every committed format-1 golden line — the pins the
	// decoder must always accept — and the hostile shapes.
	files, err := filepath.Glob(filepath.Join("testdata", "format1", "*"))
	if err != nil {
		f.Fatal(err)
	}
	if len(files) == 0 {
		f.Fatal("no format1 goldens found")
	}
	// The format-2 goldens too (the approvals kinds): a new format's
	// seeds join the day the format does.
	files2, err := filepath.Glob(filepath.Join("testdata", "format2", "*"))
	if err != nil {
		f.Fatal(err)
	}
	files = append(files, files2...)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n")) {
			f.Add(line)
		}
	}
	f.Add([]byte(`{"type":"approval","id":"e_1"}`))                  // unknown kind
	f.Add([]byte(`{"type":"message","v":2,"id":"e_1"}`))             // newer version
	f.Add([]byte(`{"type":"approval_request","v":3,"id":"e_1"}`))    // newer approvals v
	f.Add([]byte(`{"type":"approval_decision","outcome":"maybe"}`))  // hostile outcome
	f.Add([]byte(`{"type":"approval_audit","step":""}`))             // hostile step
	f.Add([]byte(`{"id":"e_1"}`))                                    // no type
	f.Add([]byte(`{"tYpe":"custom"}`))                               // case-matched key
	f.Add([]byte(`{"type":"message",`))                              // malformed
	f.Add([]byte(`[]`))                                              // not an object
	f.Add([]byte(""))                                                // empty
	f.Add([]byte(`{"type":"label","name":"héllo 世界","entry":""}`))   // unicode
	f.Add([]byte(`{"type":"turn","usage":{"input_tokens":-1e300}}`)) // hostile numbers
	f.Add([]byte(`{"type":"message","v":-1,"id":"e_1"}`))            // negative v reads as v1
	f.Add([]byte(`{"type":"custom","id":"e_1","kind":"k"}`))         // absent data stays nil

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
