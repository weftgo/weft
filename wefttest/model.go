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
	m.mu.Lock()
	var turn Turn
	exhausted := m.pos >= len(m.turns)
	if !exhausted {
		turn = m.turns[m.pos]
		m.pos++
		m.requests = append(m.requests, cloneRequest(req))
	}
	m.mu.Unlock()

	return func(yield func(weft.ModelEvent, error) bool) {
		if exhausted {
			yield(nil, ErrScriptExhausted)
			return
		}
		if turn.err != nil {
			yield(nil, turn.err)
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
	}
}

// Requests returns every ModelRequest the agent sent, in order — assert on
// system prompts, transcript shape, and the tool catalog the model saw.
func (m *Model) Requests() []weft.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]weft.ModelRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func cloneRequest(req weft.ModelRequest) weft.ModelRequest {
	req.Messages = append([]weft.Message(nil), req.Messages...)
	req.Tools = append([]*weft.ToolDef(nil), req.Tools...)
	return req
}
