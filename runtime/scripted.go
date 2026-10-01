package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"iter"
	"slices"
	"sync"

	"github.com/weftgo/weft"
)

// The scripted engine (WEFT-PLAYGROUND §5.5, D1/C6): engine "scripted"
// answers each model call with the source run's recorded turn, so an
// experiment on today's code costs zero tokens. It is its own
// component, NOT wefttest.Replay: fixtures store the model's events
// keyed at the seam, while the observability database stores *messages*
// — this Model turns each recorded assistant message back into model
// events (what wefttest.Script's Say/ToolCalls synthesise) and matches
// requests with wefttest's key function (messages, tool names,
// thinking, tool choice, sequential — deliberately not the system
// prompt, wefttest/replay.go:77-96; the tool list comes from the
// manifest, so a PrepareStep that changed tools per step will miss).
//
// ReasoningPart.Signature never streams (ADR 0004), so a scripted run's
// rebuilt transcript lacks signatures — harmless without a provider,
// and recorded here so nobody expects otherwise.
//
// The prompt trap (§5.5): the key ignores the system prompt by design,
// so a prompt experiment would silently replay the old answer. Studio
// rejects `scripted` with an instructions or model override (400); the
// runtime re-refuses. Changes that alter the key (tools off, thinking,
// edited messages) simply miss, and the step fails with "no recorded
// turn" — either way it never silently answers.

// scriptedModel is a weft.Model over the source run's recorded
// assistant messages, keyed per request prefix like wefttest's
// fixtures.
type scriptedModel struct {
	info  weft.ModelInfo
	mu    sync.Mutex
	byKey map[string][][]weft.ModelEvent // keyed turns, recorded order
}

// newScriptedModel indexes the transcript: the request whose messages
// are msgs[:i] is answered by the assistant message at i. tools is the
// advertised set the requests carried (the agent's registered tools —
// the manifest's list).
func newScriptedModel(msgs []weft.Message, tools []string) *scriptedModel {
	m := &scriptedModel{
		info:  weft.ModelInfo{Provider: "weft/runtime", Name: "scripted"},
		byKey: map[string][][]weft.ModelEvent{},
	}
	names := append([]string(nil), tools...)
	slices.Sort(names)
	for i, msg := range msgs {
		if msg.Role != weft.RoleAssistant {
			continue
		}
		key := scriptedKey(msgs[:i], names)
		m.byKey[key] = append(m.byKey[key], eventsOf(msg))
	}
	return m
}

// eventsOf synthesises one recorded assistant message's model events.
func eventsOf(msg weft.Message) []weft.ModelEvent {
	var out []weft.ModelEvent
	var calls int
	for _, p := range msg.Content {
		switch p := p.(type) {
		case weft.ReasoningPart:
			// The signature never streams back (ADR 0004): a scripted
			// run's rebuilt transcript carries no signatures.
			out = append(out, weft.ModelReasoningDelta{Text: p.Text})
		case weft.TextPart:
			out = append(out, weft.ModelTextDelta(p))
		case weft.ToolCallPart:
			calls++
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, weft.ModelToolCall{ID: p.ID, Name: p.Name, Args: args})
		}
	}
	reason := weft.StopEndTurn
	if calls > 0 {
		reason = weft.StopToolCalls
	}
	// Usage stays zero: the point is zero tokens (D1/C6, J5).
	out = append(out, weft.ModelFinish{Reason: reason})
	return out
}

// scriptedKeyDoc is the canonical form the scripted engine keys on —
// wefttest's key (replay.go's keyDoc): the transcript verbatim, the
// tool catalogue by name only, the thinking and tool-choice requests,
// the sequential flag. The system prompt is deliberately absent.
// Thinking, tool choice and sequentialness are recorded as their zero
// values: an agent configured with non-zero defaults for them records
// requests this key cannot reproduce (documented limitation — the miss
// is loud, never a silent wrong answer).
type scriptedKeyDoc struct {
	Messages   []weft.Message       `json:"messages"`
	Tools      []string             `json:"tools,omitempty"`
	Thinking   *weft.ThinkingConfig `json:"thinking,omitempty"`
	ToolChoice *struct{}            `json:"tool_choice,omitempty"`
	Sequential bool                 `json:"sequential,omitempty"`
}

func scriptedKey(prefix []weft.Message, tools []string) string {
	doc := scriptedKeyDoc{Messages: prefix, Tools: tools}
	b, err := json.Marshal(doc)
	if err != nil {
		// Messages round-trip through encoding/json by contract.
		panic(fmt.Sprintf("weft/runtime: scripted key: %v", err))
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

func (m *scriptedModel) Info() weft.ModelInfo { return m.info }

func (m *scriptedModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	key := scriptedKey(req.Messages, toolNames(req.Tools))
	m.mu.Lock()
	queue := m.byKey[key]
	var events []weft.ModelEvent
	if len(queue) > 0 {
		events = queue[0]
		m.byKey[key] = queue[1:]
	}
	m.mu.Unlock()

	return func(yield func(weft.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if events == nil {
			// The deliberate failure (§5.5): a request the record does
			// not answer — a changed input, a turned-off tool, a
			// thinking override — fails the step rather than silently
			// replaying some other turn.
			yield(nil, fmt.Errorf("weft/runtime: no recorded turn for this request (scripted engine, key %s): the input changed since the recording, or the run is not scripted-replayable", key))
			return
		}
		for _, ev := range events {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// toolNames lists the request's advertised tool names, sorted — the
// wefttest key's tool catalogue.
func toolNames(tools []*weft.ToolDef) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	slices.Sort(out)
	return out
}
