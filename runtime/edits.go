package runtime

import (
	"bytes"
	"encoding/json"
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
//
// Steps are the source run's own: step 0 is its first assistant
// message, whatever conversation the run was fed before it (the
// input). An edit names a step of the run, never a turn of the
// context.

// keptPrefix is §5.1's from_step semantics over a resolved source: the
// run's whole input, then its steps through step from_step − 1 (their
// tool results included). from_step 0 keeps the input alone — plus
// whatever the run recorded before its first model call (a resumed
// run's completed tool message), which is not a step's to re-run. It
// refuses a prefix that leaves one of the kept steps' calls without a
// result: Repair would synthesize one, and the experiment would run on
// a transcript nobody wrote.
func keptPrefix(src *sourceRun, fromStep int) ([]weft.Message, error) {
	cut := cutAtStep(src.steps, fromStep)
	if err := prefixComplete(src.steps[:cut]); err != nil {
		return nil, err
	}
	out := make([]weft.Message, 0, len(src.input)+cut)
	out = append(out, src.input...)
	return append(out, src.steps[:cut]...), nil
}

// applyTranscriptEdits returns the kept prefix (the input, then the
// steps through step from_step − 1) with the edits applied. It rejects:
//
//   - an edit to a step the fresh run re-executes (step >= from_step);
//   - a tool-result patch whose call_id is not in the named kept step;
//   - a reply rewrite of a step whose assistant message carried tool
//     calls (dropping them would orphan their results silently — patch
//     the results instead);
//   - an edit carrying both a tool_result and a content (one edit, one
//     meaning);
//   - a kept prefix that leaves a call without a result (from_step
//     must land on a step boundary).
func applyTranscriptEdits(src *sourceRun, fromStep int, edits []transcriptEdit) ([]weft.Message, error) {
	if len(edits) == 0 {
		return keptPrefix(src, fromStep)
	}
	if fromStep <= 0 {
		return nil, fmt.Errorf("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
	}
	cut := cutAtStep(src.steps, fromStep)
	// The kept steps are copied deep enough to patch: the source is
	// shared by every reader of this command.
	steps := make([]weft.Message, cut)
	for i, m := range src.steps[:cut] {
		steps[i] = weft.Message{Role: m.Role, Content: append([]weft.Part(nil), m.Content...)}
	}

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
			if !patchResult(steps, e.Step, e.CallID, e.ToolResult) {
				return nil, fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
		case e.Content != "":
			if !rewriteReply(steps, e.Step, e.Content) {
				return nil, fmt.Errorf("step %d has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)", e.Step)
			}
		default:
			return nil, fmt.Errorf("an empty edit (neither tool_result nor content)")
		}
	}
	if err := prefixComplete(steps); err != nil {
		return nil, err
	}
	out := make([]weft.Message, 0, len(src.input)+cut)
	out = append(out, src.input...)
	return append(out, steps...), nil
}

// patchResult replaces one call's result content in place — the result
// of call callID inside step (a tool message belongs to the step its
// assistant message opened). Scoped to the step: call ids are only
// unique within a run's step, and a deterministic model reuses them.
func patchResult(steps []weft.Message, step int, callID, content string) bool {
	patched := false
	at := -1 // the step the walk is in; -1 before the first assistant message
	for mi := range steps {
		if steps[mi].Role == weft.RoleAssistant {
			at++
			continue
		}
		if steps[mi].Role != weft.RoleTool || at != step {
			continue
		}
		for pi := range steps[mi].Content {
			tr, ok := steps[mi].Content[pi].(weft.ToolResultPart)
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

// rewriteReply replaces one step's assistant message with a plain-text
// reply. It refuses a message that carried tool calls: their results
// would become orphans Repair drops silently.
func rewriteReply(steps []weft.Message, step int, content string) bool {
	assistants := 0
	for mi := range steps {
		if steps[mi].Role != weft.RoleAssistant {
			continue
		}
		if assistants == step {
			for _, p := range steps[mi].Content {
				if _, ok := p.(weft.ToolCallPart); ok {
					return false
				}
			}
			steps[mi].Content = []weft.Part{weft.TextPart{Text: content}}
			return true
		}
		assistants++
	}
	return false
}

// prefixComplete is §1's boundary rule over the kept steps: every tool
// call they made has a result among them. A from_step that lands
// mid-step (or a source that never finished one — a parked or failed
// run) fails here instead of letting Repair synthesize results.
func prefixComplete(steps []weft.Message) error {
	answered := map[string]bool{}
	for _, m := range steps {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if tr, ok := p.(weft.ToolResultPart); ok {
				answered[tr.CallID] = true
			}
		}
	}
	for _, m := range steps {
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

// recorded is one recorded tool result: what the source run's call
// answered, and whether it answered with an error.
type recorded struct {
	content string
	isError bool
}

// substitutes is the substitute mode's lookup (§6 rule 3): the source
// run's recorded results by (tool, canonical args), each key a queue
// in the order the source made the calls — a tool called twice with
// the same arguments answered twice, and the re-run gets the answers
// in that order, never the last one for both.
type substitutes struct {
	byKey map[string][]recorded
}

// take answers one call from the record: the next recorded result of
// that (tool, args), ok false on a miss. The last result of a key
// stays — a call the re-run repeats beyond the record's count is
// answered like the last recorded one (the handler still never runs).
func (s *substitutes) take(name string, args json.RawMessage) (recorded, bool) {
	key := callKey(name, args)
	q := s.byKey[key]
	if len(q) == 0 {
		return recorded{}, false
	}
	if len(q) > 1 {
		s.byKey[key] = q[1:]
	}
	return q[0], true
}

// recordedCalls indexes the source's calls for the substitute mode.
// fresh are the steps the run re-executes (from from_step on) — their
// results queue first, in order, because those are the calls the re-run
// is making again; kept are the steps before it, whose results answer
// only a key the fresh steps never recorded (the model re-issuing a
// call the kept prefix already made). Only calls with a result count.
func recordedCalls(fresh, kept []weft.Message) *substitutes {
	s := &substitutes{byKey: map[string][]recorded{}}
	index := func(msgs []weft.Message, skip map[string]bool) map[string]bool {
		seen := map[string]bool{}
		// A result pairs with the call of the assistant message before
		// it, never by id across the whole run: call ids are a step's
		// own, and a deterministic model reuses them step after step.
		var calls []weft.ToolCallPart
		for _, m := range msgs {
			switch m.Role {
			case weft.RoleAssistant:
				calls = calls[:0]
				for _, p := range m.Content {
					if c, ok := p.(weft.ToolCallPart); ok {
						calls = append(calls, c)
					}
				}
			case weft.RoleTool:
				for _, c := range calls {
					for _, p := range m.Content {
						tr, ok := p.(weft.ToolResultPart)
						if !ok || tr.CallID != c.ID {
							continue
						}
						key := callKey(c.Name, c.Args)
						if !skip[key] {
							s.byKey[key] = append(s.byKey[key], recorded{content: tr.Content, isError: tr.IsError})
							seen[key] = true
						}
						break
					}
				}
			}
		}
		return seen
	}
	index(kept, index(fresh, nil))
	return s
}

// callKey is the substitute match: the tool's name and its arguments
// in canonical JSON — object keys sorted, insignificant whitespace
// gone, numbers as written — so a call that differs only in key order
// or spacing still matches its record (a miss never re-fires, it only
// parks; this keeps a re-serialising model from parking every call).
// Arguments that are not JSON match byte for byte.
func callKey(name string, args json.RawMessage) string {
	return name + "\x00" + canonicalArgs(args)
}

func canonicalArgs(args json.RawMessage) string {
	if len(bytes.TrimSpace(args)) == 0 {
		return "{}"
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(args)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(args)
	}
	return string(out)
}
