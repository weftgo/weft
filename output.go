package weft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
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

// lastSubmitted returns the newest submit_output call with a non-error
// result, searching steps and calls newest first — the one submission
// OutputOf decodes and a Subagent delegation returns verbatim. ok is
// false when the run never submitted.
func lastSubmitted(res *RunResult) (ToolCallPart, bool) {
	if res == nil {
		return ToolCallPart{}, false
	}
	for i := len(res.Steps) - 1; i >= 0; i-- {
		step := res.Steps[i]
		for j := len(step.ToolCalls) - 1; j >= 0; j-- {
			call := step.ToolCalls[j]
			if call.Name != outputToolName {
				continue
			}
			k := slices.IndexFunc(step.Results, func(r ToolResultPart) bool { return r.CallID == call.ID })
			if k >= 0 && !step.Results[k].IsError {
				return call, true
			}
		}
	}
	return ToolCallPart{}, false
}

// OutputOf decodes the structured output recorded in a finished run —
// the last submit_output call with a non-error result — for callers
// that streamed the run and hold its RunResult. It returns ErrNoOutput
// when no such call exists.
func OutputOf[Out any](res *RunResult) (Out, error) {
	var zero Out
	call, ok := lastSubmitted(res)
	if !ok {
		return zero, ErrNoOutput
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

// OutputDecoder turns a streaming run's submit_output argument deltas
// into a filling-in Out — the UI half of structured output: a consumer
// rendering a form as the model writes it. It is a decoder value, not
// an event-stream wrapper: feed it the events you already consume and
// render what comes back.
//
//	dec := weft.NewOutputDecoder[Form]()
//	for ev, err := range run.Events() {
//	    if err != nil { return err }
//	    if p, ok := dec.Feed(ev); ok { render(p) }
//	}
//	form, err := dec.Result()
//
// The decoder keys on the submit_output stream identity ToolArgsDelta
// carries — the tool name and step boundaries — resets its buffer on
// StepStart and on a fresh submit_output ToolStart, and closes it on
// the matching ToolFinish. Nested events
// are ignored: a subagent's structured output is its own decoder's
// job. Decode is lenient and prefix-shaped: after each delta it
// attempts the longest closed prefix of the arguments so far (see
// partial_json.go) and reports it when it changed and decoded — fields
// not yet present stay zero, a garbage mid-stream prefix keeps the
// last good partial, and errors surface only at Result, which follows
// the OutputOf rule (the last submit_output call with a non-error
// result) and returns ErrNoOutput when none finished. Never
// model-visible: the decoder reads the stream and writes nothing back.
//
// Cost: every delta rescans the buffered arguments (the close is
// linear in what has arrived), so a submission's decode cost grows
// with the square of its size — the price of a fresh partial on every
// delta. At tool-argument scale (a few KiB) it is noise; a UI feeding
// very large submissions can trade freshness for linearity by calling
// Feed less often — the decoder keeps the last good partial across
// the deltas it skips.
type OutputDecoder[Out any] struct {
	buf       strings.Builder
	lastGood  string
	partial   Out
	changed   bool // attempt's verdict, carried to Feed's return
	closed    bool // a ToolFinish closed the current buffer
	candidate []byte
}

// NewOutputDecoder returns a decoder ready to feed a run's events.
func NewOutputDecoder[Out any]() *OutputDecoder[Out] {
	return &OutputDecoder[Out]{}
}

// Feed consumes one run event. It ignores everything except this run's
// StepStart, the submit_output ToolStart (a whole-call arrival, and
// the marker of a fresh call), the submit_output ToolArgsDelta
// stream, and the submit_output ToolFinish; after an args delta it
// returns the best-effort partial Out and ok == true when the partial
// changed. A second submit_output call within the step starts the
// buffer over — Result follows the last call, the OutputOf rule;
// concatenating two distinct calls' arguments could only ever produce
// bytes that do not decode.
func (d *OutputDecoder[Out]) Feed(ev Event) (partial Out, ok bool) {
	d.changed = false
	switch e := ev.(type) {
	case StepStart:
		d.buf.Reset()
		d.lastGood = ""
		d.closed = false
	case ToolStart:
		if e.Name == outputToolName {
			// Calls arrive whole: the assembled args may be the first
			// content the buffer sees (Google) or identical to the
			// concatenated deltas. lastGood survives, so a re-decode of
			// the same prefix reports no change.
			d.buf.Reset()
			d.buf.Write(cloneRaw(e.Args))
			d.closed = false
			d.attempt()
		}
	case ToolArgsDelta:
		if e.Name != outputToolName || d.closed {
			return d.partial, false
		}
		d.buf.WriteString(e.Args)
		d.attempt()
	case ToolFinish:
		if e.Name != outputToolName || e.IsError {
			return d.partial, false
		}
		d.closed = true
		d.candidate = []byte(d.buf.String())
	}
	return d.partial, d.changed
}

// attempt decodes the buffer's closed prefix, keeping the last good
// partial on failure. ok is decided on bytes: the prefix must differ
// from the last one that decoded (Out need not be comparable).
func (d *OutputDecoder[Out]) attempt() {
	d.changed = false
	if d.buf.Len() == 0 {
		return
	}
	prefix := closedPrefix(d.buf.String())
	if prefix == "" || prefix == d.lastGood {
		return
	}
	var out Out
	if err := json.Unmarshal([]byte(prefix), &out); err != nil {
		return
	}
	d.partial, d.lastGood, d.changed = out, prefix, true
}

// Result decodes the completed submission — the last submit_output
// call with a non-error result, the OutputOf rule — and returns
// ErrNoOutput when no valid call finished.
func (d *OutputDecoder[Out]) Result() (Out, error) {
	var zero Out
	if len(d.candidate) == 0 {
		return zero, ErrNoOutput
	}
	out, err := decodeInput[Out](outputToolName, d.candidate, false)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrNoOutput, err)
	}
	return out, nil
}
