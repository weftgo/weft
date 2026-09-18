package weft

import (
	"encoding/json"
	"fmt"
)

// Event is the sealed set of run progress events, yielded by Run.Events in
// emission order. New event types may be added additively; external types
// cannot join, so switches over events stay exhaustively lintable.
//
// On the wire every event carries a "type" discriminator (run_start,
// step_start, text_delta, reasoning_delta, tool_args_delta, tool_start,
// tool_finish, step_finish, run_finish) and UnmarshalEvent restores it —
// the same rule and the same compatibility contract as the message parts
// (ADR 0004).
//
// Every event except RunStart carries RunID: concurrent runs on one
// agent emit interleaved streams, and a per-run Seq counter is unique
// only within its run, so RunID is what attributes an event to its run.
//
// Events are snapshots. Their fields — including the Args byte slices —
// do not alias the run's transcript; a consumer may retain or write
// into them freely.
type Event interface {
	isEvent()
}

// RunStart is always the first event of a run and carries its id and,
// when reported, the model's identity and the agent's name.
type RunStart struct {
	ID    string    `json:"id"`
	Model ModelInfo `json:"model"`
	Agent string    `json:"agent,omitempty"`
}

// StepStart reports that the model is being called for step Index.
type StepStart struct {
	RunID string `json:"run_id"`
	Index int    `json:"index"`
}

// TextDelta is an increment of assistant text.
type TextDelta struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

// ReasoningDelta is an increment of provider reasoning, in the order the
// model produced it relative to TextDelta. Signatures are not streamed;
// they are on the ReasoningPart of the transcript.
type ReasoningDelta struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

// ToolStart reports that a tool invocation began. Events from tools running
// in parallel interleave: pair them by CallID and order by Seq, a per-run
// counter assigned at emission that totally orders the stream. A call
// parked by the approval boundary has a ToolStart and no ToolFinish; it
// is listed on RunFinish.Pending instead.
type ToolStart struct {
	RunID  string          `json:"run_id"`
	Seq    int64           `json:"seq"`
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

// ToolArgsDelta reports an increment of a tool call's arguments as the
// model streams them — the model is "writing" the call, which can take
// a while for large arguments (generated code, long documents). It is
// progress only: the call has not been made, and ToolStart still
// arrives when it executes. Name is the best-known name so far; a
// provider that streams fragments of several calls interleaves their
// deltas, distinguished by name where the provider supplies one.
type ToolArgsDelta struct {
	RunID string `json:"run_id"`
	Name  string `json:"name"`
	Args  string `json:"args"`
}

// ToolFinish reports that a tool invocation completed, successfully or not.
// Content is the tool's JSON output, or the failure text when IsError is
// set — the same value the model sees on the matching ToolResultPart, so a
// UI can render results as they land.
type ToolFinish struct {
	RunID   string `json:"run_id"`
	Seq     int64  `json:"seq"`
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// StepFinish reports that step Index's model call completed. Raw is the
// provider's own stop reason when Reason was approximated (see
// ModelFinish.Raw); empty when the mapping was exact.
type StepFinish struct {
	RunID  string     `json:"run_id"`
	Index  int        `json:"index"`
	Reason StopReason `json:"reason"`
	Usage  Usage      `json:"usage"`
	Raw    string     `json:"raw,omitempty"`
}

// RunFinish is always the final event of a successful run and carries the
// run's total usage and step count.
type RunFinish struct {
	RunID string `json:"run_id"`
	Usage Usage  `json:"usage"`
	Steps int    `json:"steps"`
	// Pending mirrors RunResult.Pending: calls the run ended on without
	// executing, awaiting Approve/Deny. Such a call had its ToolStart
	// and no ToolFinish.
	Pending []ToolCallPart `json:"pending,omitempty"`
}

func (RunStart) isEvent()       {}
func (StepStart) isEvent()      {}
func (TextDelta) isEvent()      {}
func (ReasoningDelta) isEvent() {}
func (ToolArgsDelta) isEvent()  {}
func (ToolStart) isEvent()      {}
func (ToolFinish) isEvent()     {}
func (StepFinish) isEvent()     {}
func (RunFinish) isEvent()      {}

// Wire discriminators for Event types.
const (
	eventRunStart       = "run_start"
	eventStepStart      = "step_start"
	eventTextDelta      = "text_delta"
	eventReasoningDelta = "reasoning_delta"
	eventToolArgsDelta  = "tool_args_delta"
	eventToolStart      = "tool_start"
	eventToolFinish     = "tool_finish"
	eventStepFinish     = "step_finish"
	eventRunFinish      = "run_finish"
)

// The *Wire aliases have no methods, so encoding them uses the plain
// struct encoding — the MarshalJSON methods below add the discriminator
// without recursing into themselves.
type (
	runStartWire       RunStart
	stepStartWire      StepStart
	textDeltaWire      TextDelta
	reasoningDeltaWire ReasoningDelta
	toolArgsDeltaWire  ToolArgsDelta
	toolStartWire      ToolStart
	toolFinishWire     ToolFinish
	stepFinishWire     StepFinish
	runFinishWire      RunFinish
)

// MarshalJSON encodes the event with its "type" discriminator.
func (e RunStart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		runStartWire
	}{eventRunStart, runStartWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e StepStart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		stepStartWire
	}{eventStepStart, stepStartWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e TextDelta) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		textDeltaWire
	}{eventTextDelta, textDeltaWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e ReasoningDelta) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		reasoningDeltaWire
	}{eventReasoningDelta, reasoningDeltaWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e ToolArgsDelta) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		toolArgsDeltaWire
	}{eventToolArgsDelta, toolArgsDeltaWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e ToolStart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		toolStartWire
	}{eventToolStart, toolStartWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e ToolFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		toolFinishWire
	}{eventToolFinish, toolFinishWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e StepFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		stepFinishWire
	}{eventStepFinish, stepFinishWire(e)})
}

// MarshalJSON encodes the event with its "type" discriminator.
func (e RunFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		runFinishWire
	}{eventRunFinish, runFinishWire(e)})
}

// UnmarshalEvent decodes one wire event, dispatching on its "type"
// discriminator. An unknown or missing type is an error, never a silent
// drop: a recorded stream must replay exactly what was emitted.
func UnmarshalEvent(b []byte) (Event, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, err
	}
	var (
		ev  Event
		err error
	)
	switch head.Type {
	case eventRunStart:
		var v RunStart
		err, ev = json.Unmarshal(b, (*runStartWire)(&v)), v
	case eventStepStart:
		var v StepStart
		err, ev = json.Unmarshal(b, (*stepStartWire)(&v)), v
	case eventTextDelta:
		var v TextDelta
		err, ev = json.Unmarshal(b, (*textDeltaWire)(&v)), v
	case eventReasoningDelta:
		var v ReasoningDelta
		err, ev = json.Unmarshal(b, (*reasoningDeltaWire)(&v)), v
	case eventToolArgsDelta:
		var v ToolArgsDelta
		err, ev = json.Unmarshal(b, (*toolArgsDeltaWire)(&v)), v
	case eventToolStart:
		var v ToolStart
		err, ev = json.Unmarshal(b, (*toolStartWire)(&v)), v
	case eventToolFinish:
		var v ToolFinish
		err, ev = json.Unmarshal(b, (*toolFinishWire)(&v)), v
	case eventStepFinish:
		var v StepFinish
		err, ev = json.Unmarshal(b, (*stepFinishWire)(&v)), v
	case eventRunFinish:
		var v RunFinish
		err, ev = json.Unmarshal(b, (*runFinishWire)(&v)), v
	case "":
		return nil, fmt.Errorf("event has no %q field", "type")
	default:
		return nil, fmt.Errorf("unknown event type %q", head.Type)
	}
	if err != nil {
		return nil, err
	}
	return ev, nil
}
