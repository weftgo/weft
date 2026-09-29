package thread_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// The format-1 samples: fixed ids and times so the golden bytes are
// deterministic. sessionFile is the full-file golden — a header plus
// one entry of every kind, the shape every "read every golden" release
// must keep decoding.
const (
	sessionID = "s_01J8X9M2K7QW4R5N8T6V2B3C4D"
	entryID0  = "e_01J8X9M2K7QW4R5N8T6V2B3C4E"
	entryID1  = "e_01J8X9M2K7QW4R5N8T6V2B3C4F"
	entryID2  = "e_01J8X9M2K7QW4R5N8T6V2B3C4G"
	entryID3  = "e_01J8X9M2K7QW4R5N8T6V2B3C4H"
	entryID4  = "e_01J8X9M2K7QW4R5N8T6V2B3C4J"
	entryID5  = "e_01J8X9M2K7QW4R5N8T6V2B3C4K"
	entryID6  = "e_01J8X9M2K7QW4R5N8T6V2B3C4M"
	entryID7  = "e_01J8X9M2K7QW4R5N8T6V2B3C4N"
	entryID8  = "e_01J8X9M2K7QW4R5N8T6V2B3C4P"
	entryID9  = "e_01J8X9M2K7QW4R5N8T6V2B3C4Q"
	entryID10 = "e_01J8X9M2K7QW4R5N8T6V2B3C4R"
	entryID11 = "e_01J8X9M2K7QW4R5N8T6V2B3C4S"
)

var baseTime = time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC)

func at(i int) time.Time { return baseTime.Add(time.Duration(i) * time.Second) }

func sampleHeader() thread.Header {
	return thread.Header{
		ID:      sessionID,
		Created: at(0),
		Meta:    map[string]string{"cwd": "/home/wajih/hmm"},
	}
}

// sampleEntries is one entry of every kind, parented in a chain — the
// order the golden session file carries them in.
func sampleEntries() []thread.Entry {
	return []thread.Entry{
		thread.MessageEntry{
			ID: entryID0, Created: at(1),
			Message: weft.User("Where is order 1234?"),
		},
		thread.TurnEntry{
			ID: entryID1, ParentID: entryID0, Created: at(2),
			RunID:      sessionID + "-t1",
			StopReason: weft.StopToolCalls,
			Usage:      weft.Usage{InputTokens: 410, OutputTokens: 62, CachedInputTokens: 128},
			Steps:      1,
			Pending:    []weft.ToolCallPart{{ID: "call_1", Name: "refund_order", Args: json.RawMessage(`{"order_id":"1234"}`)}},
		},
		thread.CompactionEntry{
			ID: entryID2, ParentID: entryID1, Created: at(3),
			Summary:         "Goal: ship order 1234. Progress: located and refunded.",
			FirstKept:       entryID0,
			TokensBefore:    2817,
			Reason:          thread.ReasonThreshold,
			SummarizerUsage: weft.Usage{InputTokens: 480, OutputTokens: 96},
			SummarizerModel: weft.ModelInfo{Provider: "anthropic", Name: "claude-sonnet-5"},
			FilesRead:       []string{"orders/1234.json"},
			FilesModified:   []string{"refunds/2026-09-28.json"},
			RangeHash:       "9f2c51e4a7d3b806",
		},
		thread.BranchSummaryEntry{
			ID: entryID3, ParentID: entryID2, Created: at(4),
			Summary:   "The abandoned branch tried the carrier API; the webhook approach won.",
			FromEntry: entryID0,
		},
		thread.LeafEntry{
			ID: entryID4, ParentID: entryID3, Created: at(5),
			Entry: entryID0,
		},
		thread.LabelEntry{
			ID: entryID5, ParentID: entryID4, Created: at(6),
			Entry: entryID0,
			Name:  "before-carrier-detour",
		},
		thread.InfoEntry{
			ID: entryID6, ParentID: entryID5, Created: at(7),
			Title: "Order 1234",
			Meta:  map[string]string{"room": "table-1"},
		},
		thread.CustomEntry{
			ID: entryID7, ParentID: entryID6, Created: at(8),
			Kind: "todo",
			Data: json.RawMessage(`{"items":["ship order 1234"]}`),
		},
		thread.CustomMessageEntry{
			ID: entryID8, ParentID: entryID7, Created: at(9),
			Kind:    "system_note",
			Message: weft.User("Order 1234 shipped on 2026-09-28."),
		},
	}
}

// goldenName maps an entry to its golden file name: one file per kind,
// the format-1 pin (plan §3.2).
func goldenName(e thread.Entry) string {
	switch e.(type) {
	case thread.MessageEntry:
		return "message.json"
	case thread.TurnEntry:
		return "turn.json"
	case thread.CompactionEntry:
		return "compaction.json"
	case thread.BranchSummaryEntry:
		return "branch_summary.json"
	case thread.LeafEntry:
		return "leaf.json"
	case thread.LabelEntry:
		return "label.json"
	case thread.InfoEntry:
		return "info.json"
	case thread.CustomEntry:
		return "custom.json"
	default:
		return "custom_message.json"
	}
}

// TestEntryGoldens pins the wire bytes of every format-1 kind: what
// the writer emits is the contract, and a change to these bytes is a
// format change (ADR 0011 §6). Regenerate with
// `go test ./thread -update` from the module root.
func TestEntryGoldens(t *testing.T) {
	for _, e := range sampleEntries() {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("%T: %v", e, err)
		}
		wefttest.Golden(t, filepath.Join("testdata", "format1", goldenName(e)), append(b, '\n'))
	}
}

// TestEntryRoundTrip pins the codec's first promise: an entry
// marshals, decodes back to an equal value, and marshals again to the
// same bytes. The message kind embeds the ADR 0001 wire verbatim —
// pinned here by comparing the embedded object against the core's own
// encoding of the same message — and the richer-than-golden cases (a
// message with every part type, a fork header) round-trip too.
func TestEntryRoundTrip(t *testing.T) {
	all := sampleEntries()
	// A message with every part type exercises the core's part codecs
	// through the entry wire.
	all = append(all, thread.MessageEntry{
		ID: entryID0, Created: at(1),
		Message: weft.Message{
			Role: weft.RoleAssistant,
			Content: []weft.Part{
				weft.ReasoningPart{Text: "checking", Signature: "sig1"},
				weft.TextPart{Text: "looking"},
				weft.ToolCallPart{ID: "call_2", Name: "lookup", Args: json.RawMessage(`{"order_id":"99"}`)},
				weft.FilePart{MediaType: "image/png", URL: "https://example.com/p.png"},
			},
		},
	})
	// FilePart's other half — inline Data, base64 on the wire —
	// round-trips byte-for-byte too.
	all = append(all, thread.MessageEntry{
		ID: entryID0, Created: at(1),
		Message: weft.Message{
			Role: weft.RoleUser,
			Content: []weft.Part{
				weft.TextPart{Text: "what is this?"},
				weft.FilePart{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff}},
			},
		},
	})
	for _, e := range all {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("%T: %v", e, err)
		}
		got, err := thread.UnmarshalEntry(b)
		if err != nil {
			t.Fatalf("%T: %v", e, err)
		}
		if !reflect.DeepEqual(e, got) {
			t.Errorf("%T round trip: got %+v, want %+v", e, got, e)
		}
		again, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, again) {
			t.Errorf("%T: re-marshal differs\n got %s\nwant %s", e, again, b)
		}
	}
	// The message kind carries the ADR 0001 wire verbatim.
	msg := weft.Message{
		Role: weft.RoleTool,
		Content: []weft.Part{
			weft.ToolResultPart{CallID: "call_1", Name: "lookup", Content: `{"status":"shipped"}`},
		},
	}
	b, err := json.Marshal(thread.MessageEntry{ID: entryID0, Created: at(1), Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	core, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire.Message, core) {
		t.Errorf("message embedded verbatim: got %s, want the core wire %s", wire.Message, core)
	}
}

// TestReadEveryGolden is the test every later release keeps (plan
// §3.2): every golden in testdata — from this format and every future
// one — still decodes, and re-marshals to the same bytes. A release
// that cannot read a golden cannot read a user's session file.
func TestReadEveryGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "format1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no format1 goldens found")
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		line := bytes.TrimRight(b, "\n")
		if strings.HasSuffix(path, "header.json") {
			var h thread.Header
			if err := json.Unmarshal(line, &h); err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			again, err := json.Marshal(h)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(line, again) {
				t.Errorf("%s: header re-marshal differs\n got %s\nwant %s", path, again, line)
			}
			continue
		}
		e, err := thread.UnmarshalEntry(line)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		again, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(line, again) {
			t.Errorf("%s: entry re-marshal differs\n got %s\nwant %s", path, again, line)
		}
	}
	// The format-2 goldens (the approvals kinds, ADR 0021) read the
	// same way: this build decodes them and re-marshals their bytes.
	files2, err := filepath.Glob(filepath.Join("testdata", "format2", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files2) == 0 {
		t.Fatal("no format2 goldens found")
	}
	for _, path := range files2 {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		line := bytes.TrimRight(b, "\n")
		e, err := thread.UnmarshalEntry(line)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		again, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(line, again) {
			t.Errorf("%s: entry re-marshal differs\n got %s\nwant %s", path, again, line)
		}
	}
	// The full session file: a header, then one entry per line, every
	// line re-marshalling to itself.
	raw, err := os.ReadFile(filepath.Join("testdata", "format1", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 1+len(sampleEntries()) {
		t.Fatalf("session.jsonl has %d lines, want 1 header + %d entries", len(lines), len(sampleEntries()))
	}
	var h thread.Header
	if err := json.Unmarshal(lines[0], &h); err != nil {
		t.Fatalf("session.jsonl header: %v", err)
	}
	if h.ID != sessionID || h.Weft != thread.FormatVersion {
		t.Errorf("session.jsonl header: %+v", h)
	}
	for i, line := range lines[1:] {
		e, err := thread.UnmarshalEntry(line)
		if err != nil {
			t.Fatalf("session.jsonl line %d: %v", i+2, err)
		}
		again, _ := json.Marshal(e)
		if !bytes.Equal(line, again) {
			t.Errorf("session.jsonl line %d re-marshal differs\n got %s\nwant %s", i+2, again, line)
		}
	}
}

// TestHeaderGolden pins the header line's bytes. Regenerate with
// `go test ./thread -update`.
func TestHeaderGolden(t *testing.T) {
	hb, err := json.Marshal(sampleHeader())
	if err != nil {
		t.Fatal(err)
	}
	wefttest.Golden(t, filepath.Join("testdata", "format1", "header.json"), append(hb, '\n'))
}

// TestSessionFileGolden pins the full format-1 file: the header first,
// then one entry of every kind, newline-terminated — a session you can
// read with jq. Regenerate with `go test ./thread -update`.
func TestSessionFileGolden(t *testing.T) {
	var buf bytes.Buffer
	hb, err := json.Marshal(sampleHeader())
	if err != nil {
		t.Fatal(err)
	}
	buf.Write(hb)
	buf.WriteByte('\n')
	for _, e := range sampleEntries() {
		eb, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(eb)
		buf.WriteByte('\n')
	}
	wefttest.Golden(t, filepath.Join("testdata", "format1", "session.jsonl"), buf.Bytes())
}

// TestUnmarshalEntryLoud pins the format's loudest rule (ADR 0011
// §5–§6): an unknown kind or a newer version is ErrNewerFormat, never
// a skip; anything else that fails to decode is a plain error a
// backend reports as corrupt data.
func TestUnmarshalEntryLoud(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		newer bool
	}{
		{"unknown kind", `{"type":"approval","id":"e_1"}`, true},
		{"newer version of a known kind", `{"type":"message","v":2,"id":"e_1"}`, true},
		{"no type field", `{"id":"e_1"}`, false},
		{"malformed json", `{"type":"message",`, false},
		{"not an object", `[]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := thread.UnmarshalEntry([]byte(tc.line))
			if err == nil {
				t.Fatalf("decoded %#v, want an error", e)
			}
			if got := errors.Is(err, thread.ErrNewerFormat); got != tc.newer {
				t.Errorf("errors.Is(err, ErrNewerFormat) = %v, want %v (err: %v)", got, tc.newer, err)
			}
		})
	}
	// Everything else on the line is additive: an unknown key from a
	// later weft decodes and is ignored (ADR 0011 §6).
	e, err := thread.UnmarshalEntry([]byte(`{"type":"label","id":"e_1","entry":"e_0","name":"x","future":"ignored"}`))
	if err != nil {
		t.Fatalf("unknown-key entry failed to decode: %v", err)
	}
	if l, ok := e.(thread.LabelEntry); !ok || l.Name != "x" {
		t.Errorf("unknown-key entry decoded to %#v, want the label", e)
	}
}

// TestHeaderEnvelope pins the header's rules: the envelope integer is
// written and validated (a newer format is loud, a wrong one was never
// ours), the type discriminator is "session", and a fork's origin
// round-trips with its two keys.
func TestHeaderEnvelope(t *testing.T) {
	h := sampleHeader()
	h.Weft = 0 // the zero value writes the current format
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Type string `json:"type"`
		Weft int    `json:"weft"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Type != "session" || wire.Weft != thread.FormatVersion {
		t.Errorf("header wire = %v, want type=session weft=%d", wire, thread.FormatVersion)
	}
	var back thread.Header
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.Weft = h.Weft // marshal normalized the zero; compare otherwise
	if !reflect.DeepEqual(h, back) {
		t.Errorf("header round trip: got %+v, want %+v", back, h)
	}

	cases := []struct {
		name  string
		line  string
		newer bool
	}{
		{"newer envelope", `{"type":"session","weft":2,"id":"s_1"}`, true},
		{"zero envelope", `{"type":"session","weft":0,"id":"s_1"}`, false},
		{"missing envelope", `{"type":"session","id":"s_1"}`, false},
		{"wrong discriminator", `{"type":"message","weft":1,"id":"s_1"}`, false},
		{"no discriminator", `{"weft":1,"id":"s_1"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h thread.Header
			err := json.Unmarshal([]byte(tc.line), &h)
			if err == nil {
				t.Fatal("decoded, want an error")
			}
			if got := errors.Is(err, thread.ErrNewerFormat); got != tc.newer {
				t.Errorf("errors.Is(err, ErrNewerFormat) = %v, want %v (err: %v)", got, tc.newer, err)
			}
		})
	}

	// A fork's origin carries both keys.
	fork := sampleHeader()
	fork.ID = "s_01J8X9M2K7QW4R5N8T6V2B3C4Q"
	fork.Parent = &thread.ParentRef{Session: sessionID, Entry: entryID2}
	b, err = json.Marshal(fork)
	if err != nil {
		t.Fatal(err)
	}
	var fw struct {
		Parent *struct {
			Session string `json:"session"`
			Entry   string `json:"entry"`
		} `json:"parent"`
	}
	if err := json.Unmarshal(b, &fw); err != nil {
		t.Fatal(err)
	}
	if fw.Parent == nil || fw.Parent.Session != sessionID || fw.Parent.Entry != entryID2 {
		t.Errorf("fork header parent = %+v, want session and entry keys", fw.Parent)
	}
	var forkBack thread.Header
	if err := json.Unmarshal(b, &forkBack); err != nil {
		t.Fatal(err)
	}
	forkBack.Weft = fork.Weft // marshal normalized the zero; compare otherwise
	if !reflect.DeepEqual(fork, forkBack) {
		t.Errorf("fork header round trip: got %+v, want %+v", forkBack, fork)
	}
}

// TestIDs pins the id rules (plan §3.2): the shapes, uniqueness, and
// sortability to the millisecond; ValidID's boundary table.
func TestIDs(t *testing.T) {
	s := thread.NewSessionID()
	if !strings.HasPrefix(s, "s_") || len(s) != 2+26 {
		t.Errorf("session id %q: want s_ plus 26 chars", s)
	}
	e := thread.NewEntryID()
	if !strings.HasPrefix(e, "e_") || len(e) != 2+26 {
		t.Errorf("entry id %q: want e_ plus 26 chars", e)
	}
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		id := thread.NewEntryID()
		if seen[id] {
			t.Fatalf("duplicate entry id %q", id)
		}
		seen[id] = true
		if !thread.ValidID(id) {
			t.Fatalf("generated id %q fails ValidID", id)
		}
	}
	// Generated in order, apart in time, ids sort: the timestamp
	// prefix is non-decreasing, so the whole string is.
	prev := ""
	for i := 0; i < 4; i++ {
		id := thread.NewEntryID()
		if prev != "" && id < prev {
			t.Fatalf("ids not time-sortable: %q after %q", id, prev)
		}
		prev = id
		time.Sleep(2 * time.Millisecond)
	}

	// Concurrent generation — many goroutines in the same milliseconds
	// — still never collides: the 80 random bits carry the uniqueness.
	const workers = 8
	const each = 500
	ids := make(chan string, workers*each)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				ids <- thread.NewEntryID()
			}
		}()
	}
	wg.Wait()
	close(ids)
	uniq := map[string]bool{}
	for id := range ids {
		if uniq[id] {
			t.Fatalf("concurrent generation collided on %q", id)
		}
		uniq[id] = true
		if !thread.ValidID(id) {
			t.Fatalf("concurrently generated id %q fails ValidID", id)
		}
	}
	if len(uniq) != workers*each {
		t.Fatalf("%d unique ids, want %d", len(uniq), workers*each)
	}

	valid := []string{"s", "e_01J8", strings.Repeat("x", 128), "A-b_9"}
	invalid := []string{
		"",                       // empty
		strings.Repeat("x", 129), // too long
		"../evil",                // traversal
		"a/b", "a\\b",            // separators
		".hidden", "..", "a.b", // dots never appear
		"a b", "a+b", "héllo", "a:b", "a;b", // no spaces, accents, punctuation
	}
	for _, id := range valid {
		if !thread.ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if thread.ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

// The turn entry's trigger baseline has a pinned wire shape: present
// as "last_input" when set, absent when zero — additive either way,
// but named forever once written.
func TestTurnEntryLastInputWire(t *testing.T) {
	set, err := json.Marshal(thread.TurnEntry{ID: "e_t", LastInput: 42})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set), `"last_input":42`) {
		t.Errorf("TurnEntry with LastInput marshals as %s, want last_input", set)
	}
	zero, err := json.Marshal(thread.TurnEntry{ID: "e_t"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zero), "last_input") {
		t.Errorf("TurnEntry without LastInput marshals as %s, want no last_input key", zero)
	}
	var back thread.TurnEntry
	if err := json.Unmarshal(set, &back); err != nil {
		t.Fatal(err)
	}
	if back.LastInput != 42 {
		t.Errorf("LastInput round trip = %d, want 42", back.LastInput)
	}
}
