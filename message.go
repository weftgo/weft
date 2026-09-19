package weft

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the version of the message wire format. The JSON
// encoding of Message and its parts is a compatibility contract: within a
// version, field names and shapes change only additively. Persistence and
// serving layers envelope messages with this number; the core itself never
// needs it.
const SchemaVersion = 1

// Role is the author of a Message.
//
// There is no system role: the system instruction is agent-level
// (Instructions) and travels on ModelRequest.System, so a transcript never
// carries it and adapters never have to merge it.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool carries the results of one step's tool calls back to the
	// model, one ToolResultPart per call.
	RoleTool Role = "tool"
)

// Message is one turn in a conversation: a role plus an ordered list of
// content parts. A step's tool results are collected on a single RoleTool
// message; provider adapters fan out or merge as their wire format
// requires.
//
// On the wire every part carries a "type" discriminator ("text",
// "tool_call", "tool_result", "reasoning"), so a Message round-trips
// through encoding/json losslessly.
type Message struct {
	Role    Role   `json:"role"`
	Content []Part `json:"content"`
}

// User returns a user message with a single text part.
func User(text string) Message {
	return Message{Role: RoleUser, Content: []Part{TextPart{Text: text}}}
}

// UserParts returns a user message with the given parts, for prompts
// that mix text and files: UserParts(TextPart{"What is this?"},
// FilePart{MediaType: "image/png", URL: u}). The parts are copied; the
// caller's slice is not retained.
func UserParts(parts ...Part) Message {
	return Message{Role: RoleUser, Content: append([]Part(nil), parts...)}
}

// Assistant returns an assistant message with a single text part.
func Assistant(text string) Message {
	return Message{Role: RoleAssistant, Content: []Part{TextPart{Text: text}}}
}

// Text returns the concatenation of the message's text parts.
func (m Message) Text() string {
	var b []byte
	for _, p := range m.Content {
		if t, ok := p.(TextPart); ok {
			b = append(b, t.Text...)
		}
	}
	return string(b)
}

// UnmarshalJSON decodes a message, dispatching each content part on its
// "type" discriminator. An unknown part type is an error: the wire format
// is versioned, and silently dropping content would corrupt transcripts.
func (m *Message) UnmarshalJSON(b []byte) error {
	var wire struct {
		Role    Role              `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	m.Role = wire.Role
	m.Content = m.Content[:0]
	for i, raw := range wire.Content {
		p, err := unmarshalPart(raw)
		if err != nil {
			return fmt.Errorf("weft: message content[%d]: %w", i, err)
		}
		m.Content = append(m.Content, p)
	}
	return nil
}

// Part is one content part of a Message. The set of part types is closed:
// text, tool calls, tool results, reasoning, and files today; approval
// parts are planned additions that will join this interface.
type Part interface {
	isPart()
}

// Wire discriminators for Part types.
const (
	partText       = "text"
	partToolCall   = "tool_call"
	partToolResult = "tool_result"
	partReasoning  = "reasoning"
	partFile       = "file"
)

func unmarshalPart(raw json.RawMessage) (Part, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	var (
		p   Part
		err error
	)
	switch head.Type {
	case partText:
		var v TextPart
		err, p = json.Unmarshal(raw, (*textPartWire)(&v)), v
	case partToolCall:
		var v ToolCallPart
		err, p = json.Unmarshal(raw, (*toolCallPartWire)(&v)), v
	case partToolResult:
		var v ToolResultPart
		err, p = json.Unmarshal(raw, (*toolResultPartWire)(&v)), v
	case partReasoning:
		var v ReasoningPart
		err, p = json.Unmarshal(raw, (*reasoningPartWire)(&v)), v
	case partFile:
		var v FilePart
		err, p = json.Unmarshal(raw, (*filePartWire)(&v)), v
	case "":
		return nil, fmt.Errorf("part has no %q field", "type")
	default:
		return nil, fmt.Errorf("unknown part type %q", head.Type)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// TextPart is a span of user or assistant text.
type TextPart struct {
	Text string `json:"text"`
}

// ToolCallPart is a tool invocation requested by the model. Args is the raw
// JSON the model produced; the loop unmarshals it into the tool's input
// type before invoking the handler. Signature is the provider's opaque
// token attached to the call itself (Gemini's thought signatures ride
// functionCall parts and must return on the same part); empty for
// providers without one.
type ToolCallPart struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Args      json.RawMessage `json:"args"`
	Signature string          `json:"signature,omitempty"`
}

// ToolResultPart is the outcome of one tool call, returned to the model as
// data. Content is the JSON encoding of the tool's output, or the failure
// message when IsError is set. Tool failures never abort a run; the model
// sees them and can recover.
type ToolResultPart struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ReasoningPart is one provider reasoning block surfaced by providers
// that expose it (Anthropic thinking blocks, Gemini thought parts). Weft
// preserves it in the transcript but does not act on it. Signature
// is the provider's opaque token for the block (Anthropic rejects
// thinking sent back without its signature); adapters echo it unchanged.
// A step yields one ReasoningPart per provider block — a delta carrying
// a signature closes the block (see ModelReasoningDelta) — placed before
// the TextPart of the same assistant message, in the order the model
// produced them.
type ReasoningPart struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

// FilePart is a file the user supplies to the model: an image, a PDF,
// audio. Exactly one of Data (inline; base64 on the wire via []byte's
// default encoding) or URL is set. Adapters map it to the vendor's
// image/document block; an adapter that cannot carry this MediaType
// fails the model call with an error wrapping ErrUnsupported. The core
// never reads the bytes. The exactly-one rule is documented, not
// enforced here — the adapter is the layer that knows what it can send,
// and it returns ErrUnsupported for a part with both or neither set.
type FilePart struct {
	MediaType string `json:"media_type"`
	Data      []byte `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

func (TextPart) isPart()       {}
func (ToolCallPart) isPart()   {}
func (ToolResultPart) isPart() {}
func (ReasoningPart) isPart()  {}
func (FilePart) isPart()       {}

// The MarshalJSON methods below repeat deliberately, for the reason
// recorded once at the events' wire-alias declaration (events.go): a
// generic helper cannot preserve the flattened wire shape.
// The *Wire aliases have no methods, so encoding them uses the plain
// struct encoding — the MarshalJSON methods below add the discriminator
// without recursing into themselves.
type (
	textPartWire       TextPart
	toolCallPartWire   ToolCallPart
	toolResultPartWire ToolResultPart
	reasoningPartWire  ReasoningPart
	filePartWire       FilePart
)

// MarshalJSON encodes the part with its "type" discriminator.
func (p TextPart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		textPartWire
	}{partText, textPartWire(p)})
}

// MarshalJSON encodes the part with its "type" discriminator.
func (p ToolCallPart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		toolCallPartWire
	}{partToolCall, toolCallPartWire(p)})
}

// MarshalJSON encodes the part with its "type" discriminator.
func (p ToolResultPart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		toolResultPartWire
	}{partToolResult, toolResultPartWire(p)})
}

// MarshalJSON encodes the part with its "type" discriminator.
func (p ReasoningPart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		reasoningPartWire
	}{partReasoning, reasoningPartWire(p)})
}

// MarshalJSON encodes the part with its "type" discriminator.
func (p FilePart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		filePartWire
	}{partFile, filePartWire(p)})
}
