package weft

// Repair makes a transcript valid model input: every tool call has a
// result (missing ones become visible error results), results with no
// call are dropped, everything else is untouched. The loop applies it
// to the input of every run; store and runtime call it before
// persisting. It is pure (the input is never mutated) and idempotent:
// Repair(Repair(m)) equals Repair(m). nil input yields nil; an empty
// non-nil input yields an empty non-nil transcript.
func Repair(msgs []Message) []Message { return repair(msgs, nil) }

// interruptedResult is the synthesised content for a call whose result
// never arrived — model-visible contract, pinned by tests (ADR 0001).
const interruptedResult = "no result recorded: the call was interrupted"

// repair is Repair with a set of call ids to leave unresolved — the
// approval boundary (TODO §4.4) resolves those itself.
//
// Semantics (ADR 0001, "Transcript invariants"):
//   - A tool message is matched against the assistant message
//     immediately before it, and only the first surviving tool message
//     after that assistant is kept. The canonical shape batches a
//     step's results on one message, so a second consecutive tool
//     message is non-canonical and dropped whole, as is a tool message
//     not preceded by an assistant message with calls.
//   - Within the kept message a part survives when its CallID names an
//     unserved call of that assistant message; the first result per
//     call id wins.
//   - Calls still missing a result are synthesised in call order as
//     visible error results (interruptedResult), appended to the kept
//     tool message or placed on a new one directly after the assistant
//     message — including when the assistant message is last.
//   - Drops carry no marker; synthesis does. Repair is visible in the
//     transcript, never hidden.
func repair(msgs []Message, skip map[string]bool) []Message {
	if msgs == nil {
		return nil
	}
	out := make([]Message, 0, len(msgs)+1)
	var (
		pending []ToolCallPart // calls of the assistant awaiting results
		open    bool           // a tool message may still attach to it
		served  = map[string]bool{}
	)
	finalize := func() {
		defer func() { pending, open, served = nil, false, map[string]bool{} }()
		var missing []Part
		for _, c := range pending {
			if served[c.ID] || skip[c.ID] {
				continue
			}
			missing = append(missing, ToolResultPart{
				CallID:  c.ID,
				Name:    c.Name,
				Content: interruptedResult,
				IsError: true,
			})
		}
		if len(missing) == 0 {
			return
		}
		// The kept tool message, if any, sits directly after the
		// assistant message with nothing between (later tool messages
		// were dropped), so appending at the end places the missing
		// results on it — or creates the tool message it lacks.
		if n := len(out); n > 0 && out[n-1].Role == RoleTool {
			out[n-1].Content = append(out[n-1].Content, missing...)
			return
		}
		out = append(out, Message{Role: RoleTool, Content: missing})
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleAssistant:
			finalize()
			out = append(out, m)
			for _, p := range m.Content {
				if c, ok := p.(ToolCallPart); ok {
					pending = append(pending, c)
				}
			}
			open = len(pending) > 0
		case RoleTool:
			if len(pending) == 0 || !open {
				continue // orphans: no calls to match against
			}
			// Positional rule: only the first tool message after an
			// assistant message is considered, kept or dropped — the
			// canonical shape batches a step's results on one message.
			open = false
			var content []Part
			for _, p := range m.Content {
				r, ok := p.(ToolResultPart)
				if !ok || served[r.CallID] || !hasCall(pending, r.CallID) {
					continue
				}
				served[r.CallID] = true
				content = append(content, r)
			}
			if len(content) == 0 {
				continue
			}
			out = append(out, Message{Role: RoleTool, Content: content})
		default:
			finalize()
			out = append(out, m)
		}
	}
	finalize()
	return out
}

func hasCall(calls []ToolCallPart, id string) bool {
	for _, c := range calls {
		if c.ID == id {
			return true
		}
	}
	return false
}
