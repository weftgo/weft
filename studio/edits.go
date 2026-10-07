package studio

import (
	"encoding/json"
	"fmt"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// Transcript-edit validation (WEFT-PLAYGROUND §1's Transcript knob,
// D2/D3): Studio rejects an edit that would leave a call without a
// result or a kept prefix that does not end at a step boundary (400,
// §10.4's table) — Repair would otherwise synthesize or drop results
// silently. The runtime's copy re-validates before its accepted ack
// (its transcript is the one the run feeds on); this file keeps the
// HTTP side honest against Studio's own database.
//
// The counting is the part that must not drift:
//
//   - the run's INPUT record — the conversation it was fed plus the
//     turn's own prompt (D1) — is context, never a step: an assistant
//     message of an earlier turn is not a step of this run;
//   - every other record is what the run's steps added, and it says
//     which: the step it joined, as the core stamped it on the record
//     (weft.step.index, ADR 0028 §8). Step N is the run's (N+1)th model
//     call. A resumed run's rebuilt tool message is step 0's; a steered
//     message the step that just finished;
//   - from_step N keeps the input and steps 0..N−1 and runs step N
//     fresh; an edit names one of the kept steps, and a tool-result
//     patch is scoped to the step it names (call ids are a step's own
//     — a deterministic model reuses them).
//
// The split is the input flag (stored, or inferred by a backend
// without the attribute). Only a transcript with a record that carries
// no step (a ClickHouse row from before 0004, a producer that never
// stamped one) is numbered by order — each assistant message opens the
// next step, what precedes the first is step 0's — the same rule as
// weft/runtime's orderSteps, so the two never disagree on a command.

// stepMessage is one of the run's own messages and the step it joined.
type stepMessage struct {
	step int
	msg  core.Message
}

// runSteps returns the run's own messages (every record but the input
// one), each with its step: the stored step when every record carries
// one, else the order walk.
func runSteps(batches []obsdb.TranscriptBatch) ([]stepMessage, error) {
	stored := true
	var out []stepMessage
	for _, b := range batches {
		if b.Input || len(b.Messages) == 0 || string(b.Messages) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(b.Messages, &batch); err != nil {
			return nil, fmt.Errorf("messages body: %w", err)
		}
		if b.Step < 0 {
			stored = false
		}
		for _, m := range batch {
			out = append(out, stepMessage{step: b.Step, msg: m})
		}
	}
	if !stored {
		at := -1
		for i := range out {
			if out[i].msg.Role == core.RoleAssistant {
				at++
			}
			out[i].step = max(at, 0)
		}
	}
	return out, nil
}

// sourceSteps splits a run's messages bodies where the run itself
// began: the first record is the input, the rest are the steps
// (runtime/transcript.go's decodeBodies). A null or empty body is
// skipped; one that is not messages is an error.
func sourceSteps(bodies []json.RawMessage) (input, steps []core.Message, err error) {
	first := true
	for _, body := range bodies {
		if len(body) == 0 || string(body) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, nil, fmt.Errorf("messages body: %w", err)
		}
		if first {
			input, first = batch, false
			continue
		}
		steps = append(steps, batch...)
	}
	return input, steps, nil
}

// stepCount is how many steps the run recorded: one past the last
// step holding an assistant message (each step's model call writes
// one).
func stepCount(steps []stepMessage) int {
	n := 0
	for _, m := range steps {
		if m.msg.Role == core.RoleAssistant && m.step+1 > n {
			n = m.step + 1
		}
	}
	return n
}

// validateTranscriptEdits applies the edits to the source run's kept
// steps in memory and reports the first rule they break, in the
// runtime's own words. steps are the run's own (runSteps), never its
// input. A nil error means the patched prefix is complete: every kept
// call answered, the cut at a step boundary.
func validateTranscriptEdits(steps []stepMessage, fromStep int, edits []linkruntime.TranscriptEdit) error {
	if len(edits) == 0 {
		return nil
	}
	if fromStep <= 0 {
		return fmt.Errorf("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
	}
	if n := stepCount(steps); fromStep >= n {
		return fmt.Errorf("from_step %d is beyond the source run's last step (it recorded %d; a run past the end has nothing fresh to answer)",
			fromStep, n)
	}
	cut := cutTranscriptAtStep(steps, fromStep)
	// Copied deep enough to patch: the parts slices are the decoded
	// transcript's.
	kept := make([]stepMessage, cut)
	for i, m := range steps[:cut] {
		kept[i] = stepMessage{step: m.step, msg: core.Message{Role: m.msg.Role, Content: append([]core.Part(nil), m.msg.Content...)}}
	}
	for _, e := range edits {
		if e.Step < 0 {
			return fmt.Errorf("edit step %d is negative", e.Step)
		}
		if e.Step >= fromStep {
			return fmt.Errorf("edit step %d is not in the kept prefix (from_step %d keeps steps 0..%d)",
				e.Step, fromStep, fromStep-1)
		}
		if e.ToolResult != "" && e.Content != "" {
			return fmt.Errorf("an edit is one thing: tool_result (patch a result) or content (rewrite the reply), not both")
		}
		switch {
		case e.ToolResult != "":
			if e.CallID == "" {
				return fmt.Errorf("a tool_result edit needs call_id")
			}
			if !patchTranscriptResult(kept, e.Step, e.CallID, e.ToolResult) {
				return fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
		case e.Content != "":
			if !checkRewrite(kept, e.Step) {
				return fmt.Errorf("step %d has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)", e.Step)
			}
		default:
			return fmt.Errorf("an empty edit (neither tool_result nor content)")
		}
	}
	// §1's boundary rule over the kept steps: every call answered.
	answered := map[string]bool{}
	for _, m := range kept {
		if m.msg.Role != core.RoleTool {
			continue
		}
		for _, p := range m.msg.Content {
			if tr, ok := p.(core.ToolResultPart); ok {
				answered[tr.CallID] = true
			}
		}
	}
	for _, m := range kept {
		if m.msg.Role != core.RoleAssistant {
			continue
		}
		for _, p := range m.msg.Content {
			if c, ok := p.(core.ToolCallPart); ok && !answered[c.ID] {
				return fmt.Errorf("the kept prefix leaves call %q (%s) without a result: from_step must end at a step boundary", c.ID, c.Name)
			}
		}
	}
	return nil
}

// cutTranscriptAtStep is §5.1's from_step cut over a run's own steps:
// the index of step fromStep's assistant message (its model call), so
// what lies before it is steps 0..fromStep−1 complete — weft/runtime's
// cutAt.
func cutTranscriptAtStep(steps []stepMessage, fromStep int) int {
	if fromStep < 0 {
		fromStep = 0
	}
	for i, m := range steps {
		if m.msg.Role == core.RoleAssistant && m.step >= fromStep {
			return i
		}
	}
	return len(steps)
}

// patchTranscriptResult patches the result of call callID inside step
// — scoped to the step, like the runtime's patchResult.
func patchTranscriptResult(steps []stepMessage, step int, callID, content string) bool {
	patched := false
	for mi := range steps {
		if steps[mi].msg.Role != core.RoleTool || steps[mi].step != step {
			continue
		}
		parts := steps[mi].msg.Content
		for pi := range parts {
			tr, ok := parts[pi].(core.ToolResultPart)
			if !ok || tr.CallID != callID {
				continue
			}
			tr.Content = content
			tr.IsError = false
			parts[pi] = tr
			patched = true
		}
	}
	return patched
}

// checkRewrite reports whether step's assistant message exists and
// carries no tool calls (dropping them would orphan their results).
func checkRewrite(steps []stepMessage, step int) bool {
	for _, m := range steps {
		if m.msg.Role != core.RoleAssistant || m.step != step {
			continue
		}
		for _, p := range m.msg.Content {
			if _, ok := p.(core.ToolCallPart); ok {
				return false
			}
		}
		return true
	}
	return false
}
