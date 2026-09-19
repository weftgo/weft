package weft

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
)

// outputToolName is the tool Output registers. The model calls it with
// the final answer; the run ends once a valid call is recorded.
const outputToolName = "submit_output"

// outputToolDescription is model-visible contract text, pinned by tests.
const outputToolDescription = "Submit your final answer. Call this exactly once, when you are done: the run ends with it."

// ErrNoOutput is returned by GenerateAs and OutputOf when the run ended
// without a valid submit_output call — the model answered in text, hit
// the step budget, or never produced arguments that decode into Out.
var ErrNoOutput = errors.New("weft: run ended without a structured output")

// Output constrains the run's final answer to Out. It registers a tool
// named submit_output whose input schema is reflected from Out, exactly
// as Tool reflects a handler's input, and stops the run once the model
// has called it with arguments that decode. Invalid arguments come back
// to the model as an ErrInvalidToolInput result naming the field, so
// repair is the ordinary tool-error loop — nothing special happens.
// Read the value with GenerateAs, or OutputOf after Stream:
//
//	type Verdict struct {
//	    Approved bool   `json:"approved"`
//	    Reason   string `json:"reason" jsonschema:"one sentence"`
//	}
//
//	agt := weft.New(model, weft.Output[Verdict](), lookup)
//	v, res, err := weft.GenerateAs[Verdict](ctx, agt, weft.Prompt("Review order 42."))
//
// Tool mode works on every provider; adapters with a native JSON-schema
// mode may use it later behind the same API. Output panics if Out is
// not a struct (or a pointer to one), for the reason Tool does.
func Output[Out any]() Option {
	tool := Tool(outputToolName, outputToolDescription,
		func(_ context.Context, _ Out) (string, error) { return "recorded", nil })
	// Tool recorded this frame as the source; the manifest should name
	// Output's caller, where the schema type is chosen.
	if _, file, line, ok := runtime.Caller(1); ok {
		tool.sourceFile, tool.sourceLine = file, line
	}
	return outputOption{tool: tool}
}

type outputOption struct{ tool *ToolDef }

func (o outputOption) apply(a *Agent) {
	o.tool.apply(a)
	a.stops = append(a.stops, outputSubmitted{})
	a.hasOutput = true
}

// outputSubmitted stops the run once a step has recorded a non-error
// submit_output result — an invalid submission keeps the loop going so
// the model can repair it.
type outputSubmitted struct{}

func (outputSubmitted) Stop(steps []StepRecord) bool {
	if len(steps) == 0 {
		return false
	}
	last := steps[len(steps)-1]
	for _, r := range last.Results {
		if r.Name == outputToolName && !r.IsError {
			return true
		}
	}
	return false
}

func (outputSubmitted) String() string { return "output_submitted" }

// GenerateAs runs the agent and returns its structured output, decoded
// from the last valid submit_output call. The agent must have been built
// with Output[Out]; a run that ends without a valid submission returns
// ErrNoOutput alongside the result, so the transcript is still
// inspectable. Run errors are *RunError as for Generate.
func GenerateAs[Out any](ctx context.Context, a *Agent, opts ...RunOption) (Out, *RunResult, error) {
	res, err := a.Generate(ctx, opts...)
	if err != nil {
		var zero Out
		return zero, nil, err
	}
	out, err := OutputOf[Out](res)
	return out, res, err
}

// OutputOf decodes the structured output recorded in a finished run —
// the last submit_output call with a non-error result — for callers
// that streamed the run and hold its RunResult. It returns ErrNoOutput
// when no such call exists.
func OutputOf[Out any](res *RunResult) (Out, error) {
	var zero Out
	if res == nil {
		return zero, ErrNoOutput
	}
	for i := len(res.Steps) - 1; i >= 0; i-- {
		step := res.Steps[i]
		for j := len(step.ToolCalls) - 1; j >= 0; j-- {
			call := step.ToolCalls[j]
			if call.Name != outputToolName {
				continue
			}
			k := slices.IndexFunc(step.Results, func(r ToolResultPart) bool { return r.CallID == call.ID })
			if k < 0 || step.Results[k].IsError {
				continue
			}
			// Lenient decode, deliberately: these bytes already passed
			// the run's decode when the handler recorded them, so
			// strictness here would only reject a forged RunResult —
			// outside the threat model (a caller who can forge a result
			// can forge the output too).
			out, err := decodeInput[Out](outputToolName, call.Args, false)
			if err != nil {
				// The handler decoded these same bytes; a failure here
				// means Out differs from the type given to Output.
				return zero, fmt.Errorf("%w: %v", ErrNoOutput, err)
			}
			return out, nil
		}
	}
	return zero, ErrNoOutput
}
