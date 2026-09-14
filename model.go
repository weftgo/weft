package weft

import (
	"context"
	"encoding/json"
	"iter"
)

// Usage is token accounting for one step or one whole run.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Add returns the element-wise sum of u and o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + o.InputTokens,
		OutputTokens: u.OutputTokens + o.OutputTokens,
	}
}

// Total returns the sum of input and output tokens.
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// StopReason is why a model step ended.
type StopReason string

const (
	StopEndTurn   StopReason = "stop"
	StopToolCalls StopReason = "tool_calls"
	StopMaxTokens StopReason = "max_tokens"
)

// ModelInfo identifies a model for telemetry (§8.1) and the manifest.
// A Model that can report it implements the optional
//
//	interface{ Info() ModelInfo }
//
// which the loop detects and surfaces on RunStart.Model; Model itself
// stays one method. Middleware that wraps a Model should forward Info
// (TODO §4.1).
type ModelInfo struct {
	Provider string `json:"provider"` // "openai", "anthropic", "wefttest"
	Name     string `json:"name"`     // the vendor's model id
}

// ThinkingLevel is a provider-neutral reasoning-effort scale. The zero
// value, ThinkUnset, sends nothing and keeps the provider default;
// every other value asks the adapter to express that depth on the wire
// in whatever form the provider has — reasoning_effort, a thinking
// object, a token budget. Adapters map what the provider can express
// and document what they drop (TODO §5.14).
type ThinkingLevel int

const (
	ThinkUnset ThinkingLevel = iota // provider default; nothing is sent
	ThinkOff                        // suppress reasoning where allowed
	ThinkLow
	ThinkMedium
	ThinkHigh
)

// ThinkingConfig is the per-run reasoning request: a level on the
// neutral scale, plus a token budget for providers whose depth control
// is a cap (Anthropic budget_tokens, Gemini thinkingBudget). A level
// without a Budget leaves the depth to the provider (Anthropic adaptive
// thinking, Gemini's own level mapping); a Budget without a level pins
// it. Off wins over a Budget when both are set.
type ThinkingConfig struct {
	Level  ThinkingLevel
	Budget int64
}

// ModelRequest is everything a model needs for one step: the system
// instruction, the transcript so far, and the callable tools.
//
// Read-only: implementations must not modify Messages or Tools, and must
// clone anything they retain beyond the call.
type ModelRequest struct {
	System   string
	Messages []Message
	Tools    []*ToolDef
	// Thinking asks the model to reason at the given level for this
	// step. The zero value keeps the provider default (and any
	// construction-time adapter option, such as anthropic.Thinking);
	// the loop fills it from the agent's Thinking option, which a
	// run-level Thinking overrides.
	Thinking ThinkingConfig
	// SequentialTools asks the provider not to emit parallel tool-call
	// batches. The zero value keeps the provider default; the loop sets
	// it to true exactly under Sequential() (and Parallelism(1)), so the
	// model does not emit batches the execution policy would serialize
	// anyway. Adapters mirror it in the provider's parallel-tool-calls
	// setting.
	SequentialTools bool
}

// Model is the provider seam. Implementations stream one step's output as
// events; first-party adapters wrap the vendors' official Go SDKs rather
// than re-implementing HTTP.
//
// The stream contract:
//
//   - Events are yielded in order: any number of ModelTextDelta,
//     ModelReasoningDelta, and ModelToolCall values — optionally
//     interleaved with ModelToolCallDelta progress as argument
//     fragments stream — then exactly one ModelFinish.
//   - Failure is reported as a single terminal yield of (nil, err); no
//     events follow it.
//   - The sequence honors ctx: when ctx is done, the model yields
//     (nil, ctx.Err()) if it has not finished already.
//
// The loop enforces this contract: a stream that ends without
// ModelFinish, continues after it, or panics fails the run with an error
// wrapping ErrModelContract. A contract-violating adapter cannot corrupt
// a transcript silently.
type Model interface {
	Stream(ctx context.Context, req ModelRequest) iter.Seq2[ModelEvent, error]
}

// ModelEvent is the sealed set of events a model yields during one step.
// Tool calls arrive whole — assembling providers' streamed argument
// fragments is the adapter's job, which is what makes the core's streaming
// uniform across providers.
type ModelEvent interface {
	isModelEvent()
}

// ModelTextDelta is an increment of assistant text.
type ModelTextDelta struct {
	Text string
}

// ModelReasoningDelta is an increment of provider reasoning (Anthropic
// thinking, Gemini thought summaries). Signature is the provider's
// opaque token for the block, if any; adapters set it on the delta that
// completes a block. The core stores and forwards reasoning and never
// reads it.
//
// Block boundaries: a delta carrying a non-empty Signature closes the
// current reasoning block; the next reasoning delta opens a new one.
// Providers send a block's signature last (Anthropic's signature_delta
// ends a thinking block; Gemini's per-part signature is emitted after
// the part's text), so one ReasoningPart per provider block survives
// the round trip. Reasoning without any signature accumulates into a
// single block — nothing downstream can send unsigned blocks back
// anyway, so their boundaries are not load-bearing.
type ModelReasoningDelta struct {
	Text      string
	Signature string
}

// ModelToolCall is one complete tool invocation request. ID is the
// provider's call identifier, echoed back on the matching ToolResultPart.
// Signature is the provider's opaque token attached to the call itself
// (Gemini attaches thought signatures to functionCall parts and requires
// them returned on the same part); adapters that do not have one leave
// it empty.
type ModelToolCall struct {
	ID        string
	Name      string
	Args      json.RawMessage
	Signature string
}

// ModelToolCallDelta is an increment of a streamed tool call's
// arguments — progress only: the assembled call still arrives whole
// as a ModelToolCall before ModelFinish. Adapters whose providers
// stream argument fragments (OpenAI-compatible function.arguments
// pieces, Anthropic input_json_delta) yield these so consumers can
// show the model "writing" a call instead of dead air; adapters whose
// calls arrive whole (Google) simply yield none. Index is the
// provider's fragment key where one exists (OpenAI's delta index);
// Name is the best-known name so far — for many providers only the
// first fragment of a call carries it.
type ModelToolCallDelta struct {
	Index int
	Name  string
	Args  string
}

// ModelFinish closes a step with its stop reason and token usage.
type ModelFinish struct {
	Reason StopReason
	Usage  Usage
	// Raw is the provider's own stop reason when Reason had to be
	// approximated ("refusal", "pause_turn", "content_filter", ...).
	// Empty when the mapping was exact. The core never interprets it; it
	// is recorded on the step (StepRecord.RawStopReason) and the
	// StepFinish event so callers can see a refusal without a tap.
	Raw string
}

func (ModelTextDelta) isModelEvent()      {}
func (ModelReasoningDelta) isModelEvent() {}
func (ModelToolCallDelta) isModelEvent()  {}
func (ModelToolCall) isModelEvent()       {}
func (ModelFinish) isModelEvent()         {}
