package obsdb

import (
	"encoding/json"
	"testing"
)

// The wire shapes below are what the core's recorder emits for a
// resume (records_test.go's TestMessagesRecordsResumeRebuilt): the
// input as fed, then the tool message attachResults rebuilt or created.
const (
	msgUser      = `{"role":"user","content":[{"type":"text","text":"refund please"}]}`
	msgAssistant = `{"role":"assistant","content":[{"type":"tool_call","id":"c_e","name":"echo","args":{"msg":"x"}},{"type":"tool_call","id":"c_r","name":"refund","args":{}}]}`
	msgPartial   = `{"role":"tool","content":[{"type":"tool_result","call_id":"c_e","name":"echo","content":"x","is_error":false}]}`
	msgRebuilt   = `{"role":"tool","content":[{"type":"tool_result","call_id":"c_e","name":"echo","content":"x","is_error":false},{"type":"tool_result","call_id":"c_r","name":"refund","content":"refunded","is_error":false}]}`
	msgFinal     = `{"role":"assistant","content":[{"type":"text","text":"all set"}]}`
)

func raws(bodies ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(bodies))
	for i, b := range bodies {
		out[i] = json.RawMessage(b)
	}
	return out
}

func flatten(t *testing.T, bodies []json.RawMessage) []string {
	t.Helper()
	var out []string
	for _, b := range bodies {
		var batch []json.RawMessage
		if err := json.Unmarshal(b, &batch); err != nil {
			t.Fatalf("body %s: %v", b, err)
		}
		for _, m := range batch {
			out = append(out, string(m))
		}
	}
	return out
}

// A rebuild-path resume: the partial tool message the input carried is
// superseded by the rebuilt one — the transcript holds one tool
// message, every other message byte-for-byte, the body count unchanged.
func TestDedupTranscriptRebuiltResume(t *testing.T) {
	in := raws(
		"["+msgUser+","+msgAssistant+","+msgPartial+"]",
		"["+msgRebuilt+"]",
		"["+msgFinal+"]",
	)
	got := DedupTranscript(in)
	if len(got) != 3 {
		t.Fatalf("bodies = %d, want 3 (a body's place is its record's index)", len(got))
	}
	want := []string{msgUser, msgAssistant, msgRebuilt, msgFinal}
	flat := flatten(t, got)
	if len(flat) != len(want) {
		t.Fatalf("transcript = %d messages %v, want %d (the partial tool message superseded)", len(flat), flat, len(want))
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Errorf("message %d = %s, want %s", i, flat[i], want[i])
		}
	}
	if string(in[0]) != "["+msgUser+","+msgAssistant+","+msgPartial+"]" {
		t.Error("the caller's slice was modified")
	}
}

// Every other shape passes through untouched, the same slice.
func TestDedupTranscriptLeavesOtherShapes(t *testing.T) {
	for name, in := range map[string][]json.RawMessage{
		"fresh run":       raws("["+msgUser+"]", "["+msgAssistant+"]", "["+msgRebuilt+"]", "["+msgFinal+"]"),
		"created resume":  raws("["+msgUser+","+msgAssistant+"]", "["+msgRebuilt+"]", "["+msgFinal+"]"),
		"continuation":    raws("["+msgUser+","+msgAssistant+","+msgRebuilt+","+msgFinal+","+msgUser+"]", "["+msgFinal+"]"),
		"one body":        raws("[" + msgUser + "," + msgAssistant + "," + msgPartial + "]"),
		"unrelated calls": raws("["+msgUser+","+msgAssistant+","+msgRebuilt+"]", "["+msgPartial+"]"),
		"malformed":       raws(`"a bare string"`, "["+msgRebuilt+"]"),
		"empty":           nil,
	} {
		got := DedupTranscript(in)
		if len(got) != len(in) {
			t.Errorf("%s: %d bodies, want %d", name, len(got), len(in))
			continue
		}
		for i := range in {
			if string(got[i]) != string(in[i]) {
				t.Errorf("%s: body %d changed: %s", name, i, got[i])
			}
		}
	}
}
