package studio

import (
	"encoding/json"
	"fmt"

	"github.com/weftgo/weft/core"
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
// The rules mirror weft/runtime's edits.go and transcript.go line for
// line (each module owns its copy of the contract, like the wire
// shapes — nothing above the link is shared), and the counting is the
// part that must not drift:
//
//   - a run's first messages record is its INPUT record — the
//     conversation it was fed plus the turn's own prompt (D1). It is
//     context, never a step: an assistant message of an earlier turn
//     is not a step of this run;
//   - every later record is what the run's steps added. Step N is the
//     run's (N+1)th model call: the Nth assistant message after the
//     input record (from 0), with the tool results that follow it —
//     the index step_start / step_finish carry (weft.step.index);
//   - from_step N keeps the input and steps 0..N−1 and runs step N
//     fresh; an edit names one of the kept steps, and a tool-result
//     patch is scoped to the step it names (call ids are a step's own
//     — a deterministic model reuses them).

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

// stepCount is how many steps the run recorded — one assistant message
// opens each.
func stepCount(steps []core.Message) int {
	n := 0
	for _, m := range steps {
		if m.Role == core.RoleAssistant {
			n++
		}
	}
	return n
}

// validateTranscriptEdits applies the edits to the source run's kept
// steps in memory and reports the first rule they break, in the
// runtime's own words. steps are the run's own (sourceSteps), never
// its input. A nil error means the patched prefix is complete: every
// kept call answered, the cut at a step boundary.
func validateTranscriptEdits(steps []core.Message, fromStep int, edits []linkruntime.TranscriptEdit) error {
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
	kept := make([]core.Message, cut)
	for i, m := range steps[:cut] {
		kept[i] = core.Message{Role: m.Role, Content: append([]core.Part(nil), m.Content...)}
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
		if m.Role != core.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok {
				answered[tr.CallID] = true
			}
		}
	}
	for _, m := range kept {
		if m.Role != core.RoleAssistant {
			continue
		}
		for _, p := range m.Content {
			if c, ok := p.(core.ToolCallPart); ok && !answered[c.ID] {
				return fmt.Errorf("the kept prefix leaves call %q (%s) without a result: from_step must end at a step boundary", c.ID, c.Name)
			}
		}
	}
	return nil
}

// cutTranscriptAtStep is §5.1's from_step cut over a run's own steps:
// the index of step N's assistant message (the Nth, from 0), so what
// lies before it is steps 0..N−1 complete. Step 0's cut is the first
// assistant message: what a resumed run recorded before its first
// model call is not a step's to re-run.
func cutTranscriptAtStep(steps []core.Message, fromStep int) int {
	if fromStep < 0 {
		fromStep = 0
	}
	assistants := 0
	for i, m := range steps {
		if m.Role != core.RoleAssistant {
			continue
		}
		if assistants == fromStep {
			return i
		}
		assistants++
	}
	return len(steps)
}

// patchTranscriptResult patches the result of call callID inside step
// (a tool message belongs to the step its assistant message opened) —
// scoped to the step, like the runtime's patchResult.
func patchTranscriptResult(steps []core.Message, step int, callID, content string) bool {
	patched := false
	at := -1 // the step the walk is in; -1 before the first assistant message
	for mi := range steps {
		if steps[mi].Role == core.RoleAssistant {
			at++
			continue
		}
		if steps[mi].Role != core.RoleTool || at != step {
			continue
		}
		for pi := range steps[mi].Content {
			tr, ok := steps[mi].Content[pi].(core.ToolResultPart)
			if !ok || tr.CallID != callID {
				continue
			}
			tr.Content = content
			tr.IsError = false
			steps[mi].Content[pi] = tr
			patched = true
		}
	}
	return patched
}

// checkRewrite reports whether step's assistant message exists and
// carries no tool calls (dropping them would orphan their results).
func checkRewrite(steps []core.Message, step int) bool {
	assistants := 0
	for _, m := range steps {
		if m.Role != core.RoleAssistant {
			continue
		}
		if assistants == step {
			for _, p := range m.Content {
				if _, ok := p.(core.ToolCallPart); ok {
					return false
				}
			}
			return true
		}
		assistants++
	}
	return false
}
