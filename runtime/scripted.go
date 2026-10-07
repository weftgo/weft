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
// Signatures (a reasoning block's, a tool call's) are replayed with the
// events that carry them, so the rebuilt assistant message equals the
// recorded one byte for byte — the next step's request is then the key
// the record holds.
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

// newScriptedModel indexes the source run's own steps: the request
// whose messages are the run's input plus its steps before assistant
// message i is answered by that message. The input's assistant
// messages are earlier turns' answers — context, never a turn this
// engine replays. tools is the advertised set the requests carried
// (the agent's registered tools — the manifest's list).
func newScriptedModel(src *sourceRun, tools []string) *scriptedModel {
	m := &scriptedModel{
		info:  weft.ModelInfo{Provider: "weft/runtime", Name: "scripted"},
		byKey: map[string][][]weft.ModelEvent{},
	}
	names := append([]string(nil), tools...)
	slices.Sort(names)
	msgs := src.all()
	for i := len(src.input); i < len(msgs); i++ {
		if msgs[i].Role != weft.RoleAssistant {
			continue
		}
		key := scriptedKey(msgs[:i], names)
		m.byKey[key] = append(m.byKey[key], eventsOf(msgs[i]))
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
			// The block's signature rides its delta (a signed delta closes
			// the block): the rebuilt message must equal the recorded one,
			// or the next step's request keys differently and misses.
			out = append(out, weft.ModelReasoningDelta(p))
		case weft.TextPart:
			out = append(out, weft.ModelTextDelta(p))
		case weft.ToolCallPart:
			calls++
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, weft.ModelToolCall{ID: p.ID, Name: p.Name, Args: args, Signature: p.Signature})
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
type scriptedKeyDoc struct {
	Messages   []weft.Message         `json:"messages"`
	Tools      []string               `json:"tools,omitempty"`
	Thinking   *weft.ThinkingConfig   `json:"thinking,omitempty"`
	ToolChoice *weft.ToolChoiceConfig `json:"tool_choice,omitempty"`
	Sequential bool                   `json:"sequential,omitempty"`
}

// scriptedKey indexes the record (step 8b review fix 3): messages and
// tool names only — thinking, tool choice and sequentialness index as
// their zero values, because the stored transcript does not record
// them. The request side (scriptedRequestKey) carries them
// nil-when-zero exactly like wefttest's canonical, so a request that
// sets any of them misses the record loudly instead of replaying the
// recorded answer — §5.5 names thinking among the key-altering
// changes. (An agent configured with non-zero defaults for them
// therefore records a transcript its own scripted re-runs cannot
// answer: the miss is loud, never a silent wrong answer.)
func scriptedKey(prefix []weft.Message, tools []string) string {
	return hashScriptedKey(scriptedKeyDoc{Messages: prefix, Tools: tools})
}

// scriptedRequestKey is the request side of the same key: wefttest's
// canonical rules verbatim (replay.go:89-104).
func scriptedRequestKey(req weft.ModelRequest) string {
	doc := scriptedKeyDoc{Messages: req.Messages, Tools: toolNames(req.Tools), Sequential: req.SequentialTools}
	if req.Thinking != (weft.ThinkingConfig{}) {
		tc := req.Thinking
		doc.Thinking = &tc
	}
	if req.ToolChoice != (weft.ToolChoiceConfig{}) {
		cc := req.ToolChoice
		doc.ToolChoice = &cc
	}
	return hashScriptedKey(doc)
}

func hashScriptedKey(doc scriptedKeyDoc) string {
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
	key := scriptedRequestKey(req)
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
