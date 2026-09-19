package weft

import (
	"errors"
	"fmt"
)

// The error model in one rule: a tool error is data the model sees; a run
// error is a Go error the caller sees.
//
// Tool handlers return ordinary errors (or panic); the loop encodes both as
// ToolResultPart{IsError: true} and keeps going — sibling tool calls in the
// same step are never canceled by a failing tool. Model-stream failures,
// context cancellation, step-budget exhaustion, and a malformed
// tool-source snapshot (ErrDuplicateTool, ErrNilTool) surface to the
// caller, wrapped in RunError.

// Sentinel errors for named run failures. Branch on them with errors.Is;
// never match on error strings.
var (
	// ErrMaxSteps is returned when the model still requests tools after the
	// last allowed step. The partial transcript rides along on RunError.
	ErrMaxSteps = errors.New("weft: run exceeded the maximum number of steps")

	// ErrNoSuchTool is returned by Agent.CallTool when the call names a
	// tool the agent does not have. Inside the loop the same condition is
	// folded into an error tool result — data for the model to
	// self-correct, not a run failure.
	ErrNoSuchTool = errors.New("weft: no tool with that name")

	// ErrInvalidToolInput is returned by ToolDef.Invoke and Agent.CallTool
	// when the arguments do not decode into the tool's input type. Like
	// ErrNoSuchTool, the loop turns it into model-visible data.
	ErrInvalidToolInput = errors.New("weft: tool input is not valid for its schema")

	// ErrRunConsumed is returned by Run.Events when the event stream has
	// already been consumed; each Run yields exactly one sequence.
	ErrRunConsumed = errors.New("weft: run events already consumed")

	// ErrModelContract is wrapped around failures of a Model
	// implementation to honor the stream contract documented on Model:
	// events after ModelFinish, a stream ending without one, a tool call
	// with an empty ID or name, or a panicking stream. It signals an
	// adapter bug, not a model outage — providers' own errors surface
	// unwrapped.
	ErrModelContract = errors.New("weft: model violated the stream contract")

	// ErrUnsupported is wrapped by a Model that cannot honour part of a
	// request — a FilePart whose media type the provider does not accept,
	// a feature the vendor lacks. It is a run error (the model call
	// fails), so callers can errors.Is on it and fall back to another
	// model.
	ErrUnsupported = errors.New("weft: request uses a feature the model does not support")

	// ErrStreamIdle is the stream error an adapter yields when the gap
	// between two chunks exceeds its IdleTimeout. The ctx deadline is the
	// hard limit on a whole call; the idle timeout only catches a stalled
	// stream, so a slow but actively streaming response is never killed.
	// It is a run error like any provider error; callers errors.Is on it
	// provider-agnostically, without knowing which adapter timed out.
	ErrStreamIdle = errors.New("weft: model stream idle timeout")

	// ErrModelRequestsDenied is the stream error every first-party
	// adapter yields from Stream when ModelRequestsAllowed is false — the
	// kill switch for test suites that must never reach the network.
	// wefttest models ignore the switch, so ordinary offline tests are
	// unaffected.
	ErrModelRequestsDenied = errors.New("weft: model requests denied by WEFT_MODEL_REQUESTS")

	// ErrApprovalRequired marks a tool call that must not run until a
	// human (or an outer system) decides. The loop raises it for tools
	// built with RequireApproval; tool middleware may return an error
	// wrapping it to defer any call. The run then ends successfully with
	// the call on RunResult.Pending; resume with Approve or Deny.
	ErrApprovalRequired = errors.New("weft: tool call requires approval")

	// ErrApprovalDenied is the cause on the error result the model sees
	// for a pending call that was denied (Deny, or no decision on
	// resume). It is a tool error — data — never a run error.
	ErrApprovalDenied = errors.New("weft: tool call denied")

	// ErrDuplicateTool is a run error raised when a step's tool
	// snapshot contains a name twice — the runtime analogue of New's
	// duplicate-name panic. Fix the tool source; the run fails rather
	// than silently dropping the second tool. Agent.CallTool reports
	// the same condition as an error.
	ErrDuplicateTool = errors.New("weft: duplicate tool name from tool source")

	// ErrNilTool is a run error raised when a tool-source snapshot
	// contains a nil entry — a malformed snapshot, not a tool. The run
	// fails rather than advertising a dereference every adapter would
	// panic on; Agent.CallTool reports the same condition as an error.
	ErrNilTool = errors.New("weft: nil tool in tool source snapshot")
)

// ToolError is a tool failure with a stable code the model can branch
// on. Code is SCREAMING_SNAKE by convention ("ORDER_NOT_FOUND"); Message
// is what the model reads; Err is the internal cause — available to
// tool middleware and audit logs through errors.As/Unwrap, and never
// shown to the model. A handler returning *ToolError produces the
// result "<CODE>: <Message>"; the loop renders its own failures with
// codes too: INVALID_INPUT (arguments that do not decode) and
// NO_SUCH_TOOL. Codes are not validated. Plain errors keep rendering as
// err.Error(); install mw.MapErrors to code them centrally.
type ToolError struct {
	Code    string
	Message string
	Err     error
}

// Error renders the model-visible form: "CODE: Message". Err is not
// included.
func (e *ToolError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the internal cause, for errors.Is/As in middleware and
// logs.
func (e *ToolError) Unwrap() error { return e.Err }

// Errorf builds a *ToolError with a formatted Message. A %w verb sets
// Err as fmt.Errorf would — with several %w verbs every cause stays
// reachable through errors.Is — so the cause is available to middleware
// while the model sees only the formatted text.
func Errorf(code, format string, args ...any) *ToolError {
	// fmt.Errorf renders %w as the wrapped error's text and records it
	// as the cause; the message the model sees is the rendered text.
	// Two or more %w verbs produce a joined error whose single-value
	// Unwrap is nil, so rejoin the causes to keep them reachable.
	wrapped := fmt.Errorf(format, args...)
	te := &ToolError{Code: code, Message: wrapped.Error()}
	if multi, ok := wrapped.(interface{ Unwrap() []error }); ok {
		te.Err = errors.Join(multi.Unwrap()...)
		return te
	}
	te.Err = errors.Unwrap(wrapped)
	return te
}

// RunError reports a step-scoped failure: the model stream failed, the
// context was canceled, or the step budget ran out. Err is the cause — use
// errors.Is/As on it. Result carries the transcript up to the failure, so
// partial work is never lost.
type RunError struct {
	Step   int
	Err    error
	Result *RunResult
}

func (e *RunError) Error() string {
	return fmt.Sprintf("weft: run failed at step %d: %v", e.Step, e.Err)
}

func (e *RunError) Unwrap() error { return e.Err }
