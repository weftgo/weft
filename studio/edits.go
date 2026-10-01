package studio

import (
	"fmt"

	"github.com/weftgo/weft"
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
// The rules mirror runtime/edits.go (each module owns its copy of the
// contract, like the wire shapes — nothing above the link is shared).

// validateTranscriptEdits applies the edits to the source transcript's
// kept prefix in memory and reports the first rule they break. A nil
// error means the patched prefix is complete: every call answered, the
// cut at an assistant-message boundary.
func validateTranscriptEdits(msgs []weft.Message, fromStep int, edits []linkruntime.TranscriptEdit) error {
	if len(edits) == 0 {
		return nil
	}
	if fromStep <= 0 {
		return fmt.Errorf("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
	}
	cut := cutTranscriptAtStep(msgs, fromStep)
	prefix := append([]weft.Message(nil), msgs[:cut]...)
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
			if !patchTranscriptResult(prefix, e.CallID, e.ToolResult) {
				return fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
		case e.Content != "":
			if !checkRewrite(prefix, e.Step) {
				return fmt.Errorf("step %d has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)", e.Step)
			}
		default:
			return fmt.Errorf("an empty edit (neither tool_result nor content)")
		}
	}
	// §1's boundary rule over the patched prefix: every call answered.
	answered := map[string]bool{}
	for _, m := range prefix {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if tr, ok := p.(weft.ToolResultPart); ok {
				answered[tr.CallID] = true
			}
		}
	}
	for _, m := range prefix {
		if m.Role != weft.RoleAssistant {
			continue
		}
		for _, p := range m.Content {
			if c, ok := p.(weft.ToolCallPart); ok && !answered[c.ID] {
				return fmt.Errorf("the kept prefix leaves call %q (%s) without a result: from_step must end at a step boundary", c.ID, c.Name)
			}
		}
	}
	return nil
}

// cutTranscriptAtStep is §5.1's from_step cut: the message index where
// step N begins (the Nth assistant message opens a step).
func cutTranscriptAtStep(msgs []weft.Message, fromStep int) int {
	if fromStep <= 0 {
		return 0
	}
	assistants := 0
	for i, m := range msgs {
		if m.Role != weft.RoleAssistant {
			continue
		}
		if assistants == fromStep {
			return i
		}
		assistants++
	}
	return len(msgs)
}

func patchTranscriptResult(prefix []weft.Message, callID, content string) bool {
	patched := false
	for mi := range prefix {
		if prefix[mi].Role != weft.RoleTool {
			continue
		}
		for pi := range prefix[mi].Content {
			tr, ok := prefix[mi].Content[pi].(weft.ToolResultPart)
			if !ok || tr.CallID != callID {
				continue
			}
			tr.Content = content
			tr.IsError = false
			prefix[mi].Content[pi] = tr
			patched = true
		}
	}
	return patched
}

// checkRewrite reports whether step's assistant message exists and
// carries no tool calls (dropping them would orphan their results).
func checkRewrite(prefix []weft.Message, step int) bool {
	assistants := 0
	for _, m := range prefix {
		if m.Role != weft.RoleAssistant {
			continue
		}
		if assistants == step {
			for _, p := range m.Content {
				if _, ok := p.(weft.ToolCallPart); ok {
					return false
				}
			}
			return true
		}
		assistants++
	}
	return false
}
