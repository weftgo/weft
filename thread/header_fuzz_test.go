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

// FuzzDecodeHeader (step 7.1): the session header's decoder over
// arbitrary bytes never panics, enforces the envelope rule loudly, and
// whatever it accepts round-trips — the canonical encoding is a
// fixpoint, the same rule the entries' decoder holds. The envelope's
// own error shapes (a newer weft, a line that is not a session) are
// pinned by TestHeaderEnvelope; here it is the never-panic and the
// fixpoint that fuzz owes.
func FuzzDecodeHeader(f *testing.F) {
	// Seeds: every committed header golden — the session file's first
	// line and the standalone pins (format1's header, format4's
	// lineage), which the decoder must always accept — and the hostile
	// shapes. A missing golden is a renamed pin, never a skip.
	raw, err := os.ReadFile(filepath.Join("testdata", "format1", "session.jsonl"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(bytes.SplitN(raw, []byte("\n"), 2)[0])
	for _, golden := range [...]string{"format1/header.json", "format4/session_lineage.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", golden))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(bytes.TrimSpace(raw))
	}
	f.Add([]byte(`{"type":"session","weft":99,"id":"s_1"}`))       // a newer envelope
	f.Add([]byte(`{"type":"session","weft":0,"id":"s_1"}`))        // no format wrote zero
	f.Add([]byte(`{"type":"session","id":"s_1"}`))                 // the field absent
	f.Add([]byte(`{"type":"message","id":"e_1"}`))                 // not a session line
	f.Add([]byte(`{"type":"session","weft":1}`))                   // no id
	f.Add([]byte(`{"tYpe":"session","weft":1,"id":"s_1"}`))        // case-matched key
	f.Add([]byte(`{"type":"session","weft":1,"id":"s","weft":2}`)) // duplicated key
	f.Add([]byte(`{"type":"session","weft":1,"created":"not a time","id":"s_1"}`))
	f.Add([]byte(`{"type":"session","weft":-1,"id":"s_1"}`))
	f.Add([]byte(`{"type":"session","weft":1,"lineage":{"parent_session":"s_0","parent_call_id":"call_1"},"meta":{"pool_agent":"research"},"id":"s_1"}`))
	f.Add([]byte(`{"type":"session","weft":1,"parent":{"session":"s_0","entry":"e_9"},"id":"s_1"}`))
	f.Add([]byte(``))
	f.Add([]byte(`[]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		h1, err := decodeHeader(data)
		if err != nil {
			return // loud is loud; the envelope errors are pinned elsewhere
		}
		// The first pass is allowed to normalize — encoding/json's
		// standard leniency, the same one the entries' decoder holds: a
		// case-insensitive key match re-cases, and an absent nullable
		// becomes explicit (an empty Meta decodes non-nil, omitempty
		// then drops it, and the next decode reads nil). What must never
		// happen is drift that keeps drifting, or a value that cannot
		// encode.
		b1, err := json.Marshal(h1)
		if err != nil {
			t.Fatalf("an accepted header does not re-encode: %v", err)
		}
		h2, err := decodeHeader(b1)
		if err != nil {
			t.Fatalf("the canonical form does not decode: %v", err)
		}
		b2, err := json.Marshal(h2)
		if err != nil {
			t.Fatalf("the canonical form does not re-encode: %v", err)
		}
		h3, err := decodeHeader(b2)
		if err != nil {
			t.Fatalf("the twice-canonical form does not decode: %v", err)
		}
		if !reflect.DeepEqual(h2, h3) || !bytes.Equal(b1, b2) {
			t.Fatalf("the canonical form is not a fixpoint:\n in   %q\n once %q\n twice %q", data, b1, b2)
		}
	})
}

// decodeHeader is json.Unmarshal through Header's own rule (the
// envelope check lives in UnmarshalJSON, so plain json.Unmarshal
// exercises it).
func decodeHeader(data []byte) (thread.Header, error) {
	var h thread.Header
	err := json.Unmarshal(data, &h)
	return h, err
}
