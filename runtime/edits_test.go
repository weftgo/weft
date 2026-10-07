package runtime

import (
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
)

// editMsgs is a two-step transcript: step 0 looks an order up, step 1
// refunds it, step 2 answers.
func editMsgs() []core.Message {
	return []core.Message{
		core.User("refund order #4411 please"),
		{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{"order_id":"4411"}`)}}},
		{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "shipped"}}},
		{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c2", Name: "refund", Args: []byte(`{"order_id":"4411"}`)}}},
		{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c2", Name: "refund", Content: "refunded"}}},
		core.Assistant("Refunded — anything else?"),
	}
}

// editSource is editMsgs as a resolved source: the prompt is the run's
// input, everything after it the run's own steps.
func editSource() *sourceRun {
	msgs := editMsgs()
	return &sourceRun{input: msgs[:1], steps: msgs[1:]}
}

// TestApplyTranscriptEdits pins §1's Transcript rules: the kept prefix
// carries the edits, an edit may not leave a call without a result,
// the kept prefix must end at a step boundary, and a rewrite may not
// drop a step's calls.
func TestApplyTranscriptEdits(t *testing.T) {
	msgs := editSource()

	// The P2 gate's shape: continue from step 2 with the refund result
	// patched to the counterfactual.
	patched, err := applyTranscriptEdits(msgs, 2, []transcriptEdit{
		{Step: 1, CallID: "c2", ToolResult: "429"},
	})
	if err != nil {
		t.Fatalf("patch = %v", err)
	}
	if got := patched[4].Content[0].(core.ToolResultPart).Content; got != "429" {
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
	mid := &sourceRun{input: msgs.input, steps: msgs.steps[:3]} // through the refund call, before its result
	_, err = applyTranscriptEdits(mid, 2, []transcriptEdit{{Step: 0, CallID: "c1", ToolResult: "shipped"}})
	if err == nil || !strings.Contains(err.Error(), "without a result") {
		t.Errorf("mid-step cut err = %v, want the boundary rule", err)
	}
}

// TestRecordedCalls pins the substitute mode's index: (tool, args) →
// the recorded result, exactly the calls that have results.
func TestRecordedCalls(t *testing.T) {
	records := recordedCalls(editSource().steps, nil)
	if got, ok := records.take("lookup_order", []byte(`{"order_id":"4411"}`)); !ok || got.content != "shipped" {
		t.Errorf("lookup record = %+v %v", got, ok)
	}
	if got, ok := records.take("refund", []byte(`{"order_id":"4411"}`)); !ok || got.content != "refunded" {
		t.Errorf("refund record = %+v %v", got, ok)
	}
	if _, ok := records.take("refund", []byte(`{"order_id":"9999"}`)); ok {
		t.Error("different args matched a record (a changed prompt must not)")
	}
}

// TestSubstitutesOrderAndKey pins the substitute lookup (§6 rule 3):
// repeated calls of one (tool, args) answer in the order the source
// made them — paired with their own step's result, since a
// deterministic model reuses call ids — the last answer repeats, a
// recorded error stays an error, the steps the run re-executes answer
// before the kept ones, and the arguments match as JSON, not as bytes.
func TestSubstitutesOrderAndKey(t *testing.T) {
	call := func(id, args string) core.Message {
		return core.Message{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: id, Name: "next_ticket", Args: []byte(args)}}}
	}
	result := func(id, content string, isErr bool) core.Message {
		return core.Message{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: id, Name: "next_ticket", Content: content, IsError: isErr}}}
	}
	kept := []core.Message{call("c1", `{"queue":"a","n":1}`), result("c1", "kept", false)}
	fresh := []core.Message{
		call("c1", `{"queue":"a","n":1}`), result("c1", "ticket 1", false),
		call("c1", `{"queue":"a","n":1}`), result("c1", "sold out", true),
		call("c2", `{"queue":"b"}`), // parked in the source: no result, no record
	}
	records := recordedCalls(fresh, kept)
	// The same arguments, re-serialised: other key order, other spacing.
	args := []byte(`{ "n": 1, "queue": "a" }`)
	if got, ok := records.take("next_ticket", args); !ok || got.content != "ticket 1" || got.isError {
		t.Errorf("first take = %+v %v, want ticket 1", got, ok)
	}
	for i := range 2 {
		if got, ok := records.take("next_ticket", args); !ok || got.content != "sold out" || !got.isError {
			t.Errorf("take %d = %+v %v, want the recorded error (the last answer repeats)", i+2, got, ok)
		}
	}
	if _, ok := records.take("next_ticket", []byte(`{"queue":"b"}`)); ok {
		t.Error("a call the source never answered matched a record")
	}
	if _, ok := records.take("next_ticket", []byte(`{"queue":"a","n":1.0}`)); ok {
		t.Error("1.0 matched 1: numbers match as written")
	}
	// A key only the kept steps recorded still answers.
	if got, ok := recordedCalls(nil, kept).take("next_ticket", args); !ok || got.content != "kept" {
		t.Errorf("kept-only record = %+v %v", got, ok)
	}
	if callKey("t", nil) != callKey("t", []byte(" {} ")) {
		t.Error("absent arguments and {} are the same call")
	}
}

// TestTranscriptEditsAreTheRunsOwnSteps pins the step an edit names:
// a step of the source run, not a turn of the conversation it was fed.
// With an earlier turn in the input — its own assistant reply, its own
// call c1 — an edit of step 0 patches the run's c1, and the input is
// untouched.
func TestTranscriptEditsAreTheRunsOwnSteps(t *testing.T) {
	msgs := editMsgs()
	src := &sourceRun{
		input: []core.Message{
			core.User("an earlier question"),
			{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{"order_id":"1"}`)}}},
			{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "delivered"}}},
			core.Assistant("an earlier answer"),
			msgs[0],
		},
		steps: msgs[1:],
	}
	patched, err := applyTranscriptEdits(src, 1, []transcriptEdit{{Step: 0, CallID: "c1", ToolResult: "lost in transit"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(patched) != 7 {
		t.Fatalf("kept prefix = %d messages, want the input's 5 and step 0's 2", len(patched))
	}
	if got := patched[2].Content[0].(core.ToolResultPart).Content; got != "delivered" {
		t.Errorf("the earlier turn's result = %q, want it untouched", got)
	}
	if got := patched[6].Content[0].(core.ToolResultPart).Content; got != "lost in transit" {
		t.Errorf("step 0's result = %q, want the patch", got)
	}
	if got := src.steps[1].Content[0].(core.ToolResultPart).Content; got != "shipped" {
		t.Errorf("the source itself was patched (%q): the edit must work on a copy", got)
	}
	// The call exists in step 0, not step 1: naming the wrong step misses.
	if _, err := applyTranscriptEdits(src, 2, []transcriptEdit{{Step: 1, CallID: "c1", ToolResult: "x"}}); err == nil ||
		!strings.Contains(err.Error(), `no tool call "c1" in the kept prefix's step 1`) {
		t.Errorf("an edit naming the wrong step: err = %v", err)
	}
	// keptPrefix without edits obeys the same boundary rule.
	if _, err := keptPrefix(&sourceRun{input: src.input, steps: src.steps[:3]}, 2); err == nil ||
		!strings.Contains(err.Error(), "without a result") {
		t.Errorf("a kept prefix with an unanswered call: err = %v", err)
	}
}
