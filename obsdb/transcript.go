package obsdb

import (
	"bytes"
	"encoding/json"
)

// DedupTranscript makes a run's messages bodies concatenate to the
// transcript the run actually held. bodies are the messages records'
// bodies in index order, as a backend's Transcript reads them; every
// backend's Transcript returns its result through this.
//
// One stored shape needs it: a run recorded before ADR 0028 §8's
// partial-resume shape. A resume whose input already holds a partial
// tool message (some calls of the step answered, the rest parked) was
// recorded with that input as fed — index 0 — and then the tool message
// it rebuilt over the partial one, at index 1. Concatenated naively the
// two versions of the one message sit side by side; the later record is
// the authoritative one. The current core never writes that shape (its
// input record stops at the last assistant message with tool calls and
// the rebuilt message is the next growth record, step 0); this stays
// for runs stored in the old one. So when the second body is a lone
// tool message and the first holds, directly after its last assistant
// message with tool calls, a tool message whose results the lone one
// repeats, the partial message is dropped from the first body. Every
// other message stays byte-for-byte, the number of bodies never changes
// (a body's place is still its record's index), and any other shape is
// returned untouched.
func DedupTranscript(bodies []json.RawMessage) []json.RawMessage {
	if len(bodies) < 2 {
		return bodies
	}
	var joined []json.RawMessage
	if json.Unmarshal(bodies[1], &joined) != nil || len(joined) != 1 {
		return bodies
	}
	rebuilt, ok := toolResultIDs(joined[0])
	if !ok {
		return bodies
	}
	var input []json.RawMessage
	if json.Unmarshal(bodies[0], &input) != nil {
		return bodies
	}
	at := lastAssistantWithCalls(input) + 1
	if at == 0 || at >= len(input) {
		return bodies
	}
	partial, ok := toolResultIDs(input[at])
	if !ok {
		return bodies
	}
	for id := range partial {
		if !rebuilt[id] {
			return bodies // not a rebuild of this message
		}
	}
	kept := append(append([]json.RawMessage{}, input[:at]...), input[at+1:]...)
	var b bytes.Buffer
	b.WriteByte('[')
	for i, m := range kept {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(m)
	}
	b.WriteByte(']')
	out := append([]json.RawMessage{}, bodies...)
	out[0] = b.Bytes()
	return out
}

// wireMessage is the part of core.Message's wire shape the dedup reads:
// the role, and each part's type and — on a tool result — its call id.
type wireMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
	} `json:"content"`
}

// toolResultIDs returns the call ids a tool message answers; ok is
// false for any other message.
func toolResultIDs(raw json.RawMessage) (ids map[string]bool, ok bool) {
	var m wireMessage
	if json.Unmarshal(raw, &m) != nil || m.Role != "tool" {
		return nil, false
	}
	ids = map[string]bool{}
	for _, p := range m.Content {
		if p.Type == "tool_result" {
			ids[p.CallID] = true
		}
	}
	return ids, true
}

// lastAssistantWithCalls is the core's rule of the same name: the index
// of the last assistant message when it carries a tool call, else -1.
func lastAssistantWithCalls(msgs []json.RawMessage) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		var m wireMessage
		if json.Unmarshal(msgs[i], &m) != nil || m.Role != "assistant" {
			continue
		}
		for _, p := range m.Content {
			if p.Type == "tool_call" {
				return i
			}
		}
		return -1
	}
	return -1
}
