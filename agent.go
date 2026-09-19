package weft

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultMaxSteps        = 10
	defaultParallelism     = 4
	defaultMaxModelRetries = 3
	// defaultResultCap bounds a single tool result's text so a runaway
	// tool cannot silently fill the context window. 64 KiB.
	defaultResultCap = 64 << 10
)

// Option configures an Agent at construction. Options are small values
// returned by Instructions, MaxSteps, Parallelism, Sequential, StopWhen,
// Output, the PolicyOptions (MaxResultBytes, Timeout, StrictInput), and
// the Tool constructor.
type Option interface {
	apply(*Agent)
}

type instructionsOption struct{ text string }

func (o instructionsOption) apply(a *Agent) { a.system = o.text }

// Instructions sets the agent's system prompt.
func Instructions(text string) Option { return instructionsOption{text} }

type maxStepsOption struct{ n int }

func (o maxStepsOption) apply(a *Agent) {
	if o.n >= 1 {
		a.maxSteps = o.n
	}
}

// MaxSteps is the safety budget: the most model calls a run may make
// (default 10). Exceeding it fails the run with ErrMaxSteps — a runaway
// loop is a failure to surface, never a quiet success. Use StopWhen for
// the intended end of a run. Values below 1 are ignored.
func MaxSteps(n int) Option { return maxStepsOption{n} }

type usageLimitOption struct{ max Usage }

func (o usageLimitOption) apply(a *Agent) { a.usageLimit = o.max }

// UsageLimit bounds a run's total token usage — the run's own model
// calls plus every subagent's (RunResult.Usage). A field left zero is
// unlimited. The limit is checked after each step, before the loop
// makes another model call: a step that ends the run — final answer,
// StopWhen, pending approvals — may overshoot and still succeed,
// because a budget's job is to stop further spend, not to discard
// finished work. Exceeding it fails the run with ErrUsageLimit and the
// partial transcript on RunError.Result. There is no default limit:
// the right value is workload-specific, and MaxSteps is the default
// budget. Usage.Total() is not a separate limit — a caller who wants a
// total sets both fields.
func UsageLimit(max Usage) Option { return usageLimitOption{max} }

type maxModelRetriesOption struct{ n int }

func (o maxModelRetriesOption) apply(a *Agent) {
	if o.n >= 1 {
		a.maxModelRetries = o.n
	}
}

type detectLoopsOption struct{ repeats int }

func (o detectLoopsOption) apply(a *Agent) {
	if o.repeats >= 2 {
		a.detectLoops = o.repeats
	}
}

// DetectLoops fails a run with ErrLoopDetected when repeats consecutive
// steps request the same set of tool calls — the same names with the
// same arguments, in any order. Varying arguments are not a loop: a
// corrected retry has a different signature. Results are not part of
// the signature: a tool whose output carries a timestamp must not hide
// a loop, and a result-inclusive hash would miss it. Off by default
// (values below 2 are ignored); the CLI scaffold writes DetectLoops(5).
func DetectLoops(repeats int) Option { return detectLoopsOption{repeats} }

// MaxModelRetries sets how many consecutive RETRY results (see
// ModelRetry) one tool may produce in a run before the run fails with
// ErrModelRetriesExceeded (default 3). The count is per tool name — two
// parallel calls to the same tool both retrying count as two — and a
// successful result for the tool resets it. Values below 1 are ignored.
func MaxModelRetries(n int) Option { return maxModelRetriesOption{n} }

type parallelismOption struct{ n int }

func (o parallelismOption) apply(a *Agent) {
	if o.n >= 1 {
		a.parallelism = o.n
	}
}

// Parallelism sets the maximum number of a step's tool calls executing at
// once (default 4). Values below 1 are ignored.
func Parallelism(n int) Option { return parallelismOption{n} }

type sequentialOption struct{}

func (sequentialOption) apply(a *Agent)       { a.parallelism = 1 }
func (sequentialOption) applyTool(t *ToolDef) { t.sequential = true }

// Sequential restricts a step's tool calls to run one at a time, in call
// order: each tool finishes before the next starts — the safe setting for
// tools with shared state. Under any parallelism, tools *start* in call
// order; Sequential additionally serializes their execution. It also sets
// ModelRequest.SequentialTools, so adapters ask the provider not to emit
// parallel batches in the first place.
//
// On a tool, Sequential is a barrier: the dispatcher lets every
// in-flight call of the step finish, runs this call alone, then resumes
// the step's parallelism for the calls after it. Other tools keep
// running in parallel; only this one is serialized.
func Sequential() PolicyOption { return sequentialOption{} }

// ModelMiddleware wraps a Model, the chi shape: it sees every
// ModelRequest the loop builds and every event the inner model yields,
// and may retry, substitute, log, or rewrite. Implementations should
// forward Info (see InfoOf) so RunStart.Model and the manifest still
// name the underlying model.
type ModelMiddleware func(next Model) Model

type wrapModelOption []ModelMiddleware

func (o wrapModelOption) apply(a *Agent) {
	for _, m := range o {
		if m != nil {
			a.modelMW = append(a.modelMW, m)
		}
	}
}

// WrapModel installs model middleware around the agent's model. The
// first middleware listed is the outermost — WrapModel(a, b) calls
// a(b(model)) — and successive WrapModel options append inward. The
// chain is built once, at New, and sees each step's ModelRequest as the
// loop built it. The reference set is in package mw: Retry, Fallback,
// Log, RepairJSON. Nil entries are ignored.
func WrapModel(mw ...ModelMiddleware) Option { return wrapModelOption(mw) }

// InfoOf reports m's ModelInfo when it implements the optional
//
//	interface{ Info() ModelInfo }
//
// and the zero ModelInfo otherwise. Model middleware forwards identity
// with it: func (w *wrapper) Info() weft.ModelInfo { return weft.InfoOf(w.next) }.
func InfoOf(m Model) ModelInfo {
	if m, ok := m.(interface{ Info() ModelInfo }); ok {
		return m.Info()
	}
	return ModelInfo{}
}

// ThinkingOption is accepted by both New and Stream/Generate: reasoning
// depth is a per-question concern, not a per-agent one. On an agent it
// is the default for every run; on a run it overrides that default.
type ThinkingOption interface {
	Option
	RunOption
}

type thinkingOption struct{ cfg ThinkingConfig }

func (o thinkingOption) apply(a *Agent)        { a.thinking = o.cfg }
func (o thinkingOption) applyRun(c *runConfig) { c.thinking, c.thinkingSet = o.cfg, true }

// Thinking sets the reasoning level for the agent's model calls. As an
// Option it is every run's default; as a RunOption it overrides that
// default for one run — the quick-ask shape: fast by default, think on
// demand, without rebuilding the agent.
//
//	agt := weft.New(m, weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}))
//	agt.Generate(ctx, weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkHigh}), weft.Prompt(q))
//
// The zero Level keeps the provider default; adapters map what the
// provider can express and document what they drop.
func Thinking(cfg ThinkingConfig) ThinkingOption { return thinkingOption{cfg} }

type maxResultBytesOption struct{ n int }

func (o maxResultBytesOption) apply(a *Agent) {
	if o.n >= 0 {
		a.resultCap = o.n
	}
}

func (o maxResultBytesOption) applyTool(t *ToolDef) {
	if o.n >= 0 {
		t.resultCap = o.n
		t.capSet = true
	}
}

type nameOption struct{ name string }

func (o nameOption) apply(a *Agent) {
	if o.name != "" {
		a.name = o.name
	}
}

// Name names the agent: it appears on RunStart.Agent and in the
// manifest, which requires it (weft.Manifest errors on unnamed agents).
// Empty values are ignored.
func Name(name string) Option { return nameOption{name} }

type tapOption struct{ fn func(context.Context, Event) }

func (o tapOption) apply(a *Agent) {
	if o.fn != nil {
		a.taps = append(a.taps, o.fn)
	}
}

// Tap registers an observer that sees every event of every run,
// including runs made with Generate, synchronously and in emission order
// on the emitting goroutine. It must be fast and must not block: it runs
// under the event-ordering lock, so a slow tap delays every tool event
// of its step and blocks the emitting tool goroutines — the same
// consumer-speed coupling Run.Events documents. Slow observation (a
// database write, a network sink) must not happen inside the tap: hand
// each event to a queue and drain it on your own goroutine;
// ExampleTap_async is the tested pattern. Taps run in registration
// order; a panic in one is recovered, counted (see TapPanics), and
// dropped, so a broken observer cannot break a run. Taps observe and
// cannot change anything — behaviour attaches at the two middleware
// seams. ctx is the run's context.
func Tap(fn func(ctx context.Context, ev Event)) Option { return tapOption{fn} }

// MaxResultBytes sets the maximum size of one tool result's text, in
// bytes (default 64 KiB). The loop caps longer results — successes,
// failures, and panics alike — cutting on a rune boundary and appending a
// marker the model can see, so it knows the output is partial.
// MaxResultBytes(0) removes the cap; negative values are ignored. On a
// tool it overrides the agent's cap for that tool alone — a per-tool
// MaxResultBytes(0) lifts the cap for a tool whose output must arrive
// whole. Agent.CallTool returns uncapped output: the cap is a run
// policy, applied by the loop.
func MaxResultBytes(n int) PolicyOption { return maxResultBytesOption{n} }

// StopCondition decides, after a step's tool calls have run, whether the
// run is complete. It sees every step so far; the last element is the step
// just finished. Returning true ends the run successfully without another
// model call. The built-ins — HasToolCall, StepCountIs — also implement
// fmt.Stringer so the manifest (TODO §2.9) can name them; adapt an
// ordinary function with StopFunc.
type StopCondition interface {
	Stop(steps []StepRecord) bool
}

// StopFunc adapts an ordinary function to a StopCondition, the
// http.HandlerFunc shape.
type StopFunc func(steps []StepRecord) bool

// Stop ends the run when f says so.
func (f StopFunc) Stop(steps []StepRecord) bool { return f(steps) }

type stopWhenOption []StopCondition

func (o stopWhenOption) apply(a *Agent) { a.stops = append(a.stops, o...) }

// StopWhen adds stop conditions; the run ends when any one is met. Without
// StopWhen a run ends when the model replies without requesting tools.
// Compose the built-ins — HasToolCall, StepCountIs — or write your own:
//
//	weft.StopWhen(weft.HasToolCall("submit_answer"))
//	weft.StopWhen(weft.StopFunc(func(steps []weft.StepRecord) bool { ... }))
//
// Stop conditions are the intended end of a run; MaxSteps is the safety
// budget behind them.
func StopWhen(conds ...StopCondition) Option { return stopWhenOption(conds) }

// HasToolCall stops the run once the step just finished called any of
// the named tools — the "final answer tool" pattern.
func HasToolCall(names ...string) StopCondition {
	return hasToolCall{names: names}
}

type hasToolCall struct{ names []string }

func (h hasToolCall) Stop(steps []StepRecord) bool {
	if len(steps) == 0 {
		return false
	}
	last := steps[len(steps)-1]
	for _, c := range last.ToolCalls {
		if slices.Contains(h.names, c.Name) {
			return true
		}
	}
	return false
}

func (h hasToolCall) String() string { return "has_tool_call:" + strings.Join(h.names, ",") }

// StepCountIs stops the run after exactly n steps, successfully — unlike
// MaxSteps, which treats reaching the budget as a failure.
func StepCountIs(n int) StopCondition { return stepCountIs{n: n} }

type stepCountIs struct{ n int }

func (s stepCountIs) Stop(steps []StepRecord) bool { return len(steps) >= s.n }

func (s stepCountIs) String() string { return fmt.Sprintf("step_count_is:%d", s.n) }

// Agent is an immutable, reusable value: a model, a system instruction, a
// tool set, and an execution policy. Build it once with New; run it many
// times, concurrently if you like — runs share no state, and the
// registered tool set is frozen at construction (see ToolDef).
type Agent struct {
	model           Model
	system          string
	tools           map[string]*ToolDef
	toolList        []*ToolDef
	toolSource      func() []*ToolDef
	stops           []StopCondition
	maxSteps        int
	usageLimit      Usage
	maxModelRetries int
	detectLoops     int
	parallelism     int
	resultCap       int
	toolTimeout     time.Duration
	strict          bool
	taps            []func(context.Context, Event)
	name            string
	thinking        ThinkingConfig
	modelMW         []ModelMiddleware
	toolMW          []ToolMiddleware
	// hasOutput records that Output was applied: a Subagent delegating
	// to this agent returns the submitted JSON, not the final text.
	hasOutput bool
	// tapPanics counts tap invocations that panicked and were contained;
	// read with TapPanics.
	tapPanics atomic.Int64
}

// New builds an Agent. Nil models panic — including typed nils such as
// var m *someModel; New(m), which would otherwise crash much later inside
// a run goroutine. Everything else has a working default.
func New(m Model, opts ...Option) *Agent {
	if isNilModel(m) {
		panic("weft: New called with a nil Model")
	}
	a := &Agent{
		model:           m,
		tools:           map[string]*ToolDef{},
		maxSteps:        defaultMaxSteps,
		maxModelRetries: defaultMaxModelRetries,
		parallelism:     defaultParallelism,
		resultCap:       defaultResultCap,
	}
	for _, o := range opts {
		if o != nil {
			o.apply(a)
		}
	}
	// The model chain is built once, here: first registered = outermost.
	for i := len(a.modelMW) - 1; i >= 0; i-- {
		a.model = a.modelMW[i](a.model)
		if isNilModel(a.model) {
			panic("weft: model middleware returned a nil Model")
		}
	}
	return a
}

// isNilModel reports whether m is nil or a nil pointer stored in the
// interface. A typed nil passes a plain m == nil check and panics when
// the loop first calls Stream — far from the caller's bug.
func isNilModel(m Model) bool {
	if m == nil {
		return true
	}
	switch v := reflect.ValueOf(m); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

// ToolSource replaces the tool set the loop advertises and dispatches
// against with the given function's return value, fetched fresh exactly
// once per step (and once per manual Agent.CallTool): the step's
// advertisement and its dispatch both resolve against that one
// snapshot, so what the model was shown is exactly what runs. The seam
// is for registries that change while the agent runs (plugins
// installed mid-run, MCP servers polled per step); a tool registered
// mid-step becomes callable on the next step's fetch. The Agent stays
// immutable: the source is a value; synchronization and freshness of
// the list belong to the source's owner. A snapshot with a duplicate
// name fails the run with ErrDuplicateTool and one with a nil entry
// with ErrNilTool (the runtime analogues of New's duplicate-name
// panic) rather than silently dropping the second tool or advertising a
// dereference. A nil function (the default) keeps the static
// construction-time list — Tool/option registration is then the only
// source of tools, byte-identical to an agent without a source.
// Manifest and Agent.Tools still report the static construction-time
// set: a manifest describes the code, not the registry behind a source.
func ToolSource(fn func() []*ToolDef) Option { return toolSourceOption{fn} }

type toolSourceOption struct{ fn func() []*ToolDef }

func (o toolSourceOption) apply(a *Agent) { a.toolSource = o.fn }

// effectiveTools returns the tools to advertise this step: the source's
// list when one is set, the static list otherwise.
func (a *Agent) effectiveTools() []*ToolDef {
	if a.toolSource == nil {
		return a.toolList
	}
	return a.toolSource()
}

// dispatchTools fetches and validates the snapshot one consultation
// resolves against: the step's snapshot inside the loop, a fresh fetch
// per manual Agent.CallTool. Static agents skip validation — New
// already panicked on duplicates — so the check costs nothing there.
// A nil entry or a duplicate name is a malformed snapshot and fails
// the consultation with its sentinel rather than shipping a tool list
// the adapters could only crash on.
func (a *Agent) dispatchTools() ([]*ToolDef, error) {
	tools := a.effectiveTools()
	if a.toolSource == nil {
		return tools, nil
	}
	seen := make(map[string]bool, len(tools))
	for i, t := range tools {
		if t == nil {
			return nil, fmt.Errorf("%w: entry %d", ErrNilTool, i)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateTool, t.Name)
		}
		seen[t.Name] = true
	}
	return tools, nil
}

// findTool resolves a name against a fetched snapshot; first match
// wins, the rule registration order already implies for static lists.
// Snapshots are validated by dispatchTools, so no nil guard here.
func findTool(tools []*ToolDef, name string) (*ToolDef, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

// Tools returns copies of the registered tool definitions, in
// registration order. Mutating them — fields or schema trees — does not
// affect the agent; the registered set is frozen at New.
func (a *Agent) Tools() []*ToolDef {
	out := make([]*ToolDef, len(a.toolList))
	for i, t := range a.toolList {
		out[i] = t.clone()
	}
	return out
}

// TapPanics reports how many tap invocations have panicked and been
// contained since construction. A rising counter means an observer is
// broken; runs are unaffected by design.
func (a *Agent) TapPanics() int64 { return a.tapPanics.Load() }
