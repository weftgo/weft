package runtime

import (
	"fmt"

	"github.com/weftgo/weft"
)

// Transcript edits (WEFT-PLAYGROUND §1's Transcript knob, D2/D3): a
// command that continues from step N may rewrite one of the kept
// steps' assistant replies (a plain-text content edit) or patch one of
// its tool results (the "what if the API returned 429?" counterfactual).
//
// The rule the whole file enforces (§1): an edit may not leave a tool
// call without a result, and the kept prefix must end at a step
// boundary — Repair would otherwise synthesize or drop results
// silently, and the experiment would run on a transcript nobody
// wrote. Both surfaces reject with the named reasons; the runtime's
// copy is authoritative (§10.4).

// applyTranscriptEdits returns the kept prefix (through step from_step
// − 1) with the edits applied. It rejects:
//
//   - an edit to a step the fresh run re-executes (step >= from_step);
//   - a tool-result patch whose call_id is not in the kept prefix;
//   - a reply rewrite of a step whose assistant message carried tool
//     calls (dropping them would orphan their results silently — patch
//     the results instead);
//   - an edit carrying both a tool_result and a content (one edit, one
//     meaning);
//   - a kept prefix that leaves a call without a result (from_step
//     must land on a step boundary).
func applyTranscriptEdits(msgs []weft.Message, fromStep int, edits []transcriptEdit) ([]weft.Message, error) {
	if len(edits) == 0 {
		return msgs, nil
	}
	if fromStep <= 0 {
		return nil, fmt.Errorf("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
	}
	cut := cutAtStep(msgs, fromStep)
	prefix := append([]weft.Message(nil), msgs[:cut]...)

	for _, e := range edits {
		if e.Step < 0 {
			return nil, fmt.Errorf("edit step %d is negative", e.Step)
		}
		if e.Step >= fromStep {
			return nil, fmt.Errorf("edit step %d is not in the kept prefix (from_step %d keeps steps 0..%d)",
				e.Step, fromStep, fromStep-1)
		}
		if e.ToolResult != "" && e.Content != "" {
			return nil, fmt.Errorf("an edit is one thing: tool_result (patch a result) or content (rewrite the reply), not both")
		}
		switch {
		case e.ToolResult != "":
			if e.CallID == "" {
				return nil, fmt.Errorf("a tool_result edit needs call_id")
			}
			if !patchResult(prefix, e.Step, e.CallID, e.ToolResult) {
				return nil, fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
		case e.Content != "":
			if !rewriteReply(prefix, e.Step, e.Content) {
				return nil, fmt.Errorf("step %d has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)", e.Step)
			}
		default:
			return nil, fmt.Errorf("an empty edit (neither tool_result nor content)")
		}
	}
	if err := prefixComplete(prefix); err != nil {
		return nil, err
	}
	return prefix, nil
}

// patchResult replaces one call's result content in place.
func patchResult(prefix []weft.Message, step int, callID, content string) bool {
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

// rewriteReply replaces one step's assistant message with a plain-text
// reply. It refuses a message that carried tool calls: their results
// would become orphans Repair drops silently.
func rewriteReply(prefix []weft.Message, step int, content string) bool {
	assistants := 0
	for mi := range prefix {
		if prefix[mi].Role != weft.RoleAssistant {
			continue
		}
		if assistants == step {
			for _, p := range prefix[mi].Content {
				if _, ok := p.(weft.ToolCallPart); ok {
					return false
				}
			}
			prefix[mi].Content = []weft.Part{weft.TextPart{Text: content}}
			return true
		}
		assistants++
	}
	return false
}

// prefixComplete is §1's boundary rule: every tool call in the kept
// prefix has a result after it. A from_step that lands mid-step (or a
// source that never finished one) fails here instead of letting Repair
// synthesize results.
func prefixComplete(prefix []weft.Message) error {
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
			c, ok := p.(weft.ToolCallPart)
			if !ok || answered[c.ID] {
				continue
			}
			return fmt.Errorf("the kept prefix leaves call %q (%s) without a result: from_step must end at a step boundary", c.ID, c.Name)
		}
	}
	return nil
}

// recordedCalls indexes the source transcript's calls by (tool, args):
// the substitute mode's lookup — a call that matches one of these is
// answered with the recorded result, never re-fired (§6 rule 3).
func recordedCalls(msgs []weft.Message) map[string]string {
	out := map[string]string{}
	results := map[string]string{}
	for _, m := range msgs {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if tr, ok := p.(weft.ToolResultPart); ok {
				results[tr.CallID] = tr.Content
			}
		}
	}
	for _, m := range msgs {
		if m.Role != weft.RoleAssistant {
			continue
		}
		for _, p := range m.Content {
			c, ok := p.(weft.ToolCallPart)
			if !ok {
				continue
			}
			if res, has := results[c.ID]; has {
				out[c.Name+"\x00"+string(c.Args)] = res
			}
		}
	}
	return out
}
