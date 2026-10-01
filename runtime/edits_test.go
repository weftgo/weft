package runtime

import (
	"strings"
	"testing"

	"github.com/weftgo/weft"
)

// editMsgs is a two-step transcript: step 0 looks an order up, step 1
// refunds it, step 2 answers.
func editMsgs() []weft.Message {
	return []weft.Message{
		weft.User("refund order #4411 please"),
		{Role: weft.RoleAssistant, Content: []weft.Part{weft.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{"order_id":"4411"}`)}}},
		{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "shipped"}}},
		{Role: weft.RoleAssistant, Content: []weft.Part{weft.ToolCallPart{ID: "c2", Name: "refund", Args: []byte(`{"order_id":"4411"}`)}}},
		{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{CallID: "c2", Name: "refund", Content: "refunded"}}},
		weft.Assistant("Refunded — anything else?"),
	}
}

// TestApplyTranscriptEdits pins §1's Transcript rules: the kept prefix
// carries the edits, an edit may not leave a call without a result,
// the kept prefix must end at a step boundary, and a rewrite may not
// drop a step's calls.
func TestApplyTranscriptEdits(t *testing.T) {
	msgs := editMsgs()

	// The P2 gate's shape: continue from step 2 with the refund result
	// patched to the counterfactual.
	patched, err := applyTranscriptEdits(msgs, 2, []transcriptEdit{
		{Step: 1, CallID: "c2", ToolResult: "429"},
	})
	if err != nil {
		t.Fatalf("patch = %v", err)
	}
	if got := patched[4].Content[0].(weft.ToolResultPart).Content; got != "429" {
		t.Errorf("patched result = %q, want 429", got)
	}
	if len(patched) != 5 {
		t.Errorf("kept prefix = %d messages, want the four of steps 0..1", len(patched))
	}

	// A reply rewrite of a call-free step, kept.
	if _, err := applyTranscriptEdits(msgs, 3, []transcriptEdit{{Step: 2, Content: "rewritten"}}); err != nil {
		t.Errorf("rewrite of a call-free reply = %v", err)
	}

	for _, tc := range []struct {
		name  string
		step  int
		edits []transcriptEdit
		want  string
	}{
		{"from_step 0 keeps nothing", 0, []transcriptEdit{{Step: 1, CallID: "c2", ToolResult: "x"}}, "from_step > 0"},
		{"edit outside the kept prefix", 1, []transcriptEdit{{Step: 1, CallID: "c2", ToolResult: "x"}}, "not in the kept prefix"},
		{"unknown call", 2, []transcriptEdit{{Step: 1, CallID: "cX", ToolResult: "x"}}, `no tool call "cX"`},
		{"rewrite of a step with calls", 2, []transcriptEdit{{Step: 0, Content: "just words"}}, "carried tool calls"},
		{"both fields", 2, []transcriptEdit{{Step: 1, CallID: "c2", ToolResult: "x", Content: "y"}}, "one thing"},
		{"empty edit", 2, []transcriptEdit{{Step: 1}}, "an empty edit"},
		{"patch without call_id", 2, []transcriptEdit{{Step: 1, ToolResult: "x"}}, "needs call_id"},
		{"negative step", 2, []transcriptEdit{{Step: -1, Content: "x"}}, "negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyTranscriptEdits(msgs, tc.step, tc.edits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}

	// The boundary rule on the unedited transcript itself: step 1's
	// call has no result yet when from_step cuts between the call and
	// its tool message — a from_step there is refused.
	mid := append([]weft.Message(nil), msgs[:4]...) // through the refund call, before its result
	_, err = applyTranscriptEdits(mid, 2, []transcriptEdit{{Step: 0, CallID: "c1", ToolResult: "shipped"}})
	if err == nil || !strings.Contains(err.Error(), "without a result") {
		t.Errorf("mid-step cut err = %v, want the boundary rule", err)
	}
}

// TestRecordedCalls pins the substitute mode's index: (tool, args) →
// the recorded result, exactly the calls that have results.
func TestRecordedCalls(t *testing.T) {
	records := recordedCalls(editMsgs())
	if got := records["lookup_order\x00"+`{"order_id":"4411"}`]; got != "shipped" {
		t.Errorf("lookup record = %q", got)
	}
	if got := records["refund\x00"+`{"order_id":"4411"}`]; got != "refunded" {
		t.Errorf("refund record = %q", got)
	}
	if _, ok := records["refund\x00"+`{"order_id":"9999"}`]; ok {
		t.Error("different args matched a record (a changed prompt must not)")
	}
}
