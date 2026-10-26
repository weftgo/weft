package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"iter"
	"slices"
	"strings"
	"sync"

	"github.com/weftgo/weft/core"
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

// scriptedModel is a core.Model over the source run's recorded
// assistant messages, keyed per request prefix like wefttest's
// fixtures.
type scriptedModel struct {
	info  core.ModelInfo
	mu    sync.Mutex
	byKey map[string][][]core.ModelEvent // keyed turns, recorded order
	// tools is the sorted tool list every recorded turn is keyed with;
	// entries every recorded request prefix with the source step it
	// answers — what a miss is compared against to say which step and
	// which key component (scriptedMiss).
	tools   []string
	entries []scriptedEntry
	// full is the whole recorded transcript (input and steps) as JSON
	// messages, steps the source's step count, and calls each step's
	// recorded tool-call names: a request that extends full asks the
	// step the source never answered, and a recorded call to a tool
	// outside tools got no recorded result at replay.
	full  []string
	steps int
	calls [][]string
}

// scriptedEntry is one recorded turn's request side: the source step
// it answered (the run's own step index, 0-based) and the messages its
// key holds.
type scriptedEntry struct {
	step int
	msgs []string // each message as JSON, the key's own encoding
}

// newScriptedModel indexes the source run's own steps: the request
// whose messages are the run's input plus its steps before assistant
// message i is answered by that message. The input's assistant
// messages are earlier turns' answers — context, never a turn this
// engine replays. tools is the advertised set the requests carried
// (the agent's registered tools — the manifest's list).
func newScriptedModel(src *sourceRun, tools []string) *scriptedModel {
	m := &scriptedModel{
		info:  core.ModelInfo{Provider: "weft/runtime", Name: "scripted"},
		byKey: map[string][][]core.ModelEvent{},
	}
	names := append([]string(nil), tools...)
	slices.Sort(names)
	m.tools = names
	msgs := src.all()
	step := 0
	for i := len(src.input); i < len(msgs); i++ {
		if msgs[i].Role != core.RoleAssistant {
			continue
		}
		key := scriptedKey(msgs[:i], names)
		m.byKey[key] = append(m.byKey[key], eventsOf(msgs[i]))
		m.entries = append(m.entries, scriptedEntry{step: step, msgs: encodeMessages(msgs[:i])})
		var names []string
		for _, p := range msgs[i].Content {
			if c, ok := p.(core.ToolCallPart); ok {
				names = append(names, c.Name)
			}
		}
		m.calls = append(m.calls, names)
		step++
	}
	m.full, m.steps = encodeMessages(msgs), step
	// A replay from a step whose request carried a compaction view is
	// fed what the model saw there (ADR 0029): from that step on, the
	// replay's requests hold the view's messages where the record holds
	// the replaced range, so each recorded turn from there is keyed on
	// that prefix too.
	if v := src.view; v != nil {
		vstep := 0
		for i := len(src.input); i < len(src.input)+src.cut(v.step) && i < len(msgs); i++ {
			if msgs[i].Role == core.RoleAssistant {
				vstep++
			}
		}
		for i := len(src.input) + src.cut(v.step); i < len(msgs); i++ {
			if msgs[i].Role != core.RoleAssistant {
				continue
			}
			seen, err := src.seen(msgs[:i])
			if err != nil {
				break
			}
			key := scriptedKey(seen, names)
			m.byKey[key] = append(m.byKey[key], eventsOf(msgs[i]))
			m.entries = append(m.entries, scriptedEntry{step: vstep, msgs: encodeMessages(seen)})
			vstep++
		}
	}
	return m
}

// eventsOf synthesises one recorded assistant message's model events.
func eventsOf(msg core.Message) []core.ModelEvent {
	var out []core.ModelEvent
	var calls int
	for _, p := range msg.Content {
		switch p := p.(type) {
		case core.ReasoningPart:
			// The block's signature rides its delta (a signed delta closes
			// the block): the rebuilt message must equal the recorded one,
			// or the next step's request keys differently and misses.
			out = append(out, core.ModelReasoningDelta(p))
		case core.TextPart:
			out = append(out, core.ModelTextDelta(p))
		case core.ToolCallPart:
			calls++
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, core.ModelToolCall{ID: p.ID, Name: p.Name, Args: args, Signature: p.Signature})
		}
	}
	reason := core.StopEndTurn
	if calls > 0 {
		reason = core.StopToolCalls
	}
	// Usage stays zero: the point is zero tokens (D1/C6, J5).
	out = append(out, core.ModelFinish{Reason: reason})
	return out
}

// scriptedKeyDoc is the canonical form the scripted engine keys on —
// wefttest's key (replay.go's keyDoc): the transcript verbatim, the
// tool catalogue by name only, the thinking and tool-choice requests,
// the sequential flag. The system prompt is deliberately absent.
type scriptedKeyDoc struct {
	Messages   []core.Message         `json:"messages"`
	Tools      []string               `json:"tools,omitempty"`
	Thinking   *core.ThinkingConfig   `json:"thinking,omitempty"`
	ToolChoice *core.ToolChoiceConfig `json:"tool_choice,omitempty"`
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
func scriptedKey(prefix []core.Message, tools []string) string {
	return hashScriptedKey(scriptedKeyDoc{Messages: prefix, Tools: tools})
}

// scriptedRequestKey is the request side of the same key: wefttest's
// canonical rules verbatim (replay.go:89-104).
func scriptedRequestKey(req core.ModelRequest) string {
	doc := scriptedKeyDoc{Messages: req.Messages, Tools: toolNames(req.Tools), Sequential: req.SequentialTools}
	if req.Thinking != (core.ThinkingConfig{}) {
		tc := req.Thinking
		doc.Thinking = &tc
	}
	if req.ToolChoice != (core.ToolChoiceConfig{}) {
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

func (m *scriptedModel) Info() core.ModelInfo { return m.info }

func (m *scriptedModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	key := scriptedRequestKey(req)
	m.mu.Lock()
	queue := m.byKey[key]
	var events []core.ModelEvent
	if len(queue) > 0 {
		events = queue[0]
		m.byKey[key] = queue[1:]
	}
	m.mu.Unlock()

	return func(yield func(core.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if events == nil {
			// The deliberate failure (§5.5): a request the record does
			// not answer — a changed input, a turned-off tool, a
			// thinking override — fails the step rather than silently
			// replaying some other turn. The error says which source
			// step and which key component (R2, UX-C-07).
			yield(nil, m.miss(req, key))
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
func toolNames(tools []*core.ToolDef) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	slices.Sort(out)
	return out
}

// The key components a miss names (scriptedMiss.components), in the
// order the error lists them. Studio maps the bracketed list to its
// sentence (studio/runtime ReasonOf); the words are the contract.
const (
	missTools      = "tools"       // the offered tool list is not the one the record is keyed with
	missThinking   = "thinking"    // the request sets a thinking level; the record keys none
	missToolChoice = "tool_choice" // the request sets a tool choice; the record keys none
	missSequential = "sequential"  // sequential tool calls (parallelism 1); the record keys none
	missMessages   = "messages"    // the messages are no recorded request's
	missUnanswered = "unanswered"  // the source recorded no turn to answer it
	missExhausted  = "exhausted"   // the recording answered this request fewer times than asked
)

// missPhrase is each component's clause in the error.
var missPhrase = map[string]string{
	missTools:      "the tool list differs from the recording",
	missThinking:   "the request sets a thinking level the recording does not hold",
	missToolChoice: "the request sets a tool choice the recording does not hold",
	missSequential: "the request asks for sequential tool calls (parallelism 1), which the recording does not hold",
	missMessages:   "the messages differ from the recording (a changed input or tool result)",
	missUnanswered: "the source recorded no turn for it",
	missExhausted:  "the recording answered this request fewer times than the replay asks it",
}

// scriptedMiss is the scripted engine's loud failure: the source step
// whose recorded turn was nearest the request (-1 when the source
// recorded none), the key components that differ from it, and the key.
type scriptedMiss struct {
	step       int
	components []string
	key        string
}

func (e *scriptedMiss) Error() string {
	at := "this request"
	if e.step >= 0 {
		at = fmt.Sprintf("step %d", e.step)
	}
	clauses := make([]string, len(e.components))
	for i, c := range e.components {
		clauses[i] = missPhrase[c]
	}
	return fmt.Sprintf("weft/runtime: no recorded turn for %s (scripted engine, key %s): %s [%s]; the scripted engine answers only what the source recorded: run on live",
		at, e.key, strings.Join(clauses, "; "), strings.Join(e.components, ", "))
}

// miss explains a request no recorded turn answers: each key component
// that differs from the record (the tool list the turns are keyed
// with; thinking, tool choice and sequential, which the record keys as
// unset; the messages, when no recorded request holds them), and the
// source step of the recorded request nearest it — the one whose
// messages it equals, else the longest shared prefix (ties: the
// nearest length).
func (m *scriptedModel) miss(req core.ModelRequest, key string) error {
	e := &scriptedMiss{step: -1, key: key}
	if !slices.Equal(toolNames(req.Tools), m.tools) {
		e.components = append(e.components, missTools)
	}
	if req.Thinking != (core.ThinkingConfig{}) {
		e.components = append(e.components, missThinking)
	}
	if req.ToolChoice != (core.ToolChoiceConfig{}) {
		e.components = append(e.components, missToolChoice)
	}
	if req.SequentialTools {
		e.components = append(e.components, missSequential)
	}
	if len(m.entries) == 0 {
		e.components = append(e.components, missUnanswered)
		return e
	}
	got := encodeMessages(req.Messages)
	// The request carries the whole recording and asks past it: the
	// source ended on a tool call (failed or canceled mid-run), so the
	// step it never answered is the one asked.
	if len(m.full) > 0 && len(got) >= len(m.full) && slices.Equal(got[:len(m.full)], m.full) {
		e.step = m.steps
		e.components = append(e.components, missUnanswered)
		return e
	}
	best, bestShared, bestGap, exact := 0, -1, 0, false
	for i, en := range m.entries {
		shared := 0
		for shared < len(got) && shared < len(en.msgs) && got[shared] == en.msgs[shared] {
			shared++
		}
		gap := len(got) - len(en.msgs)
		if gap < 0 {
			gap = -gap
		}
		if shared == len(got) && len(got) == len(en.msgs) {
			best, exact = i, true
			break
		}
		if shared > bestShared || shared == bestShared && gap < bestGap {
			best, bestShared, bestGap = i, shared, gap
		}
	}
	e.step = m.entries[best].step
	// A recorded call before this step to a tool the replay does not
	// offer: its result at replay is not the recorded one, so the
	// messages differ because of the tool list — said as tools, as the
	// preview says it. A request longer than the nearest entry carries
	// that entry's own answer and its calls' results too, so its calls
	// count; when that entry is the last recorded step, the request is
	// the step the source never answered (the preview's [tools,
	// unanswered] at m.steps).
	longer := len(got) > len(m.entries[best].msgs)
	upTo := e.step
	if longer {
		upTo++
	}
	if !exact && m.callsOutside(upTo) {
		if !slices.Contains(e.components, missTools) {
			e.components = append([]string{missTools}, e.components...)
		}
		if longer && e.step == m.steps-1 {
			e.step = m.steps
			e.components = append(e.components, missUnanswered)
		}
		return e
	}
	switch {
	case !exact:
		e.components = append(e.components, missMessages)
	case len(e.components) == 0:
		e.components = append(e.components, missExhausted)
	}
	return e
}

// callsOutside reports whether a recorded step before step (k < step)
// called a tool outside the list the record is keyed with.
func (m *scriptedModel) callsOutside(step int) bool {
	for k := 0; k < step && k < len(m.calls); k++ {
		for _, name := range m.calls[k] {
			if !slices.Contains(m.tools, name) {
				return true
			}
		}
	}
	return false
}

// encodeMessages is each message as JSON — the key document's own
// encoding, so equal strings are equal key messages.
func encodeMessages(msgs []core.Message) []string {
	out := make([]string, len(msgs))
	for i, msg := range msgs {
		b, err := json.Marshal(msg)
		if err != nil {
			panic(fmt.Sprintf("weft/runtime: scripted key: %v", err))
		}
		out[i] = string(b)
	}
	return out
}
