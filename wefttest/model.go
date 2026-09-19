// Package wefttest provides a scriptable weft.Model, in the spirit of
// httptest: write the agent's dialogue as a sequence of scripted model
// steps and test agents offline, deterministically, with no network.
package wefttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"

	"github.com/weftgo/weft"
)

// ErrScriptExhausted is the stream error a Model yields when the agent
// makes more model calls than the script provides — almost always a test
// bug worth failing loudly.
var ErrScriptExhausted = errors.New("wefttest: model script exhausted (agent made more model calls than the script provides)")

// Turn is the script for one model call, built by Say, ToolCalls, or Fail.
type Turn struct {
	events []weft.ModelEvent
	err    error
}

// Say scripts a completed assistant reply: one text delta, then a normal
// stop. Usage is fixed at 10 input / 5 output tokens per scripted step, so
// usage accounting is assertable.
func Say(text string) Turn {
	return Turn{events: []weft.ModelEvent{
		weft.ModelTextDelta{Text: text},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}
}

// Think prefixes then with a scripted reasoning delta: Think("plan",
// ToolCalls(...)) is a step that reasons and then requests tools. The
// reasoning carries no signature; signed blocks are scripted with raw
// model events.
func Think(text string, then Turn) Turn {
	then.events = append([]weft.ModelEvent{weft.ModelReasoningDelta{Text: text}}, then.events...)
	return then
}

// Call describes one scripted tool invocation.
type Call struct {
	Name string
	Args string // raw JSON; "{}" when empty
	ID   string // optional; "call_N" within the turn when empty
}

// ToolCalls scripts a step that requests tool calls. Call IDs are assigned
// "call_1", "call_2", ... within the turn unless set explicitly.
func ToolCalls(calls ...Call) Turn {
	turn := Turn{}
	for i, c := range calls {
		args := c.Args
		if args == "" {
			args = "{}"
		}
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		turn.events = append(turn.events, weft.ModelToolCall{ID: id, Name: c.Name, Args: json.RawMessage(args)})
	}
	turn.events = append(turn.events, weft.ModelFinish{
		Reason: weft.StopToolCalls,
		Usage:  weft.Usage{InputTokens: 10, OutputTokens: 5},
	})
	return turn
}

// MaxTokens scripts a step that hit the output-token limit: the text so
// far, then a max_tokens finish. The run records it on
// RunResult.StopReason (and the StepRecord) rather than failing;
// truncated tool-call arguments decode as error results the model
// recovers from.
func MaxTokens(text string) Turn {
	return Turn{events: []weft.ModelEvent{
		weft.ModelTextDelta{Text: text},
		weft.ModelFinish{Reason: weft.StopMaxTokens, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}
}

// Fail scripts a model failure: the stream errors immediately with err.
func Fail(err error) Turn { return Turn{err: err} }

// Args marshals v as the JSON arguments of a scripted Call:
//
//	wefttest.Call{Name: "lookup", Args: wefttest.Args(lookupIn{Order: 42})}
//
// A value that cannot be marshalled is a test-construction bug and
// panics at the call site naming the type.
func Args(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("wefttest.Args(%T): %v", v, err))
	}
	return string(b)
}

// Raw scripts a step that plays exactly the events given, appending
// nothing — the way to script a signed reasoning block, a
// ModelToolCallDelta progress sequence, or a stream that violates the
// Model contract (no ModelFinish, two of them, an event after the
// finish) so a test can pin how the loop reports ErrModelContract.
// Raw still honours ctx like every scripted turn: it is malformed by
// content, never by ignoring cancellation.
func Raw(events ...weft.ModelEvent) Turn {
	return Turn{events: append([]weft.ModelEvent(nil), events...)}
}

// SayThenFail scripts a mid-stream failure: one text delta, then the
// stream errors with err. The loop discards the partial turn and
// reports err; mw.Retry sees a failure after output.
func SayThenFail(text string, err error) Turn {
	return Turn{events: []weft.ModelEvent{weft.ModelTextDelta{Text: text}}, err: err}
}

// WithUsage returns the turn with its ModelFinish.Usage replaced by u.
// A turn without a finish (Fail, a Raw stream without one) is returned
// unchanged.
func (t Turn) WithUsage(u weft.Usage) Turn {
	out := Turn{err: t.err, events: append([]weft.ModelEvent(nil), t.events...)}
	for i, ev := range out.events {
		if f, ok := ev.(weft.ModelFinish); ok {
			f.Usage = u
			out.events[i] = f
		}
	}
	return out
}

// Model is a scripted weft.Model. It plays one Turn per Stream call, in
// order, and records every ModelRequest it received for assertions.
type Model struct {
	mu       sync.Mutex
	turns    []Turn
	pos      int
	requests []weft.ModelRequest
}

// Script returns a Model that plays the turns in order.
func Script(turns ...Turn) *Model { return &Model{turns: turns} }

// Info identifies the scripted model, so RunStart.Model and manifests
// carry a non-zero value in tests.
func (*Model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "wefttest", Name: "script"}
}

// Stream implements weft.Model.
func (m *Model) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	// The request is recorded even when the script is exhausted: it is
	// a real request the agent made, and attempt-counting tests under
	// Retry/Fallback must not under-count silently (ErrScriptExhausted
	// on the run error says which entry it was).
	m.mu.Lock()
	var turn Turn
	exhausted := m.pos >= len(m.turns)
	if !exhausted {
		turn = m.turns[m.pos]
		m.pos++
	}
	m.requests = append(m.requests, cloneRequest(req))
	m.mu.Unlock()

	return func(yield func(weft.ModelEvent, error) bool) {
		// The Model contract, checked before anything scripted: when
		// ctx is done the stream yields ctx.Err() — the same rule the
		// adapters enforce, so middleware written against the
		// documented contract sees the same error from the double.
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if exhausted {
			yield(nil, ErrScriptExhausted)
			return
		}
		for _, ev := range turn.events {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
		// A turn's error follows its events (SayThenFail) or stands
		// alone (Fail, whose event list is empty).
		if turn.err != nil {
			yield(nil, turn.err)
		}
	}
}

// Requests returns every ModelRequest the agent sent, in order —
// including a call that exhausted the script (identify it by the
// ErrScriptExhausted run error) — so assert on system prompts,
// transcript shape, the tool catalog the model saw, and attempt counts
// under Retry/Fallback alike.
func (m *Model) Requests() []weft.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]weft.ModelRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// Request is one recorded ModelRequest with matchers for the assertions
// agent tests make most; every field of the request stays reachable
// through the embedded value.
type Request struct{ weft.ModelRequest }

// HasTool reports whether the request advertised a tool named name.
func (r Request) HasTool(name string) bool {
	for _, td := range r.Tools {
		if td.Name == name {
			return true
		}
	}
	return false
}

// ToolNames returns the advertised tool names, sorted.
func (r Request) ToolNames() []string { return toolNames(r.Tools) }

// LastText returns the text of the last text part of the request's last
// message — the user's prompt on the first call, a user follow-up
// later — and "" when the last message has no text part (a tool message
// after a tool step).
func (r Request) LastText() string {
	if len(r.Messages) == 0 {
		return ""
	}
	var text string
	for _, p := range r.Messages[len(r.Messages)-1].Content {
		if t, ok := p.(weft.TextPart); ok {
			text = t.Text
		}
	}
	return text
}

// LastRequest returns the most recent request the agent sent, or the
// zero Request when none was sent yet — whose matchers report false and
// empty, so a test that needs a request asserts len(Requests()) > 0
// first.
func (m *Model) LastRequest() Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return Request{}
	}
	return Request{ModelRequest: m.requests[len(m.requests)-1]}
}

func cloneRequest(req weft.ModelRequest) weft.ModelRequest {
	req.Messages = append([]weft.Message(nil), req.Messages...)
	req.Tools = append([]*weft.ToolDef(nil), req.Tools...)
	return req
}

// toolNames lists a request's advertised tool names, sorted — the tool
// catalogue by name only, the form the replay key and the Request
// matcher share.
func toolNames(tools []*weft.ToolDef) []string {
	out := make([]string, len(tools))
	for i, td := range tools {
		out[i] = td.Name
	}
	slices.Sort(out)
	return out
}
