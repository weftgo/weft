package weft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
)

// RunOption configures a single run.
type RunOption interface {
	applyRun(*runConfig)
}

type runConfig struct {
	id            string
	messages      []Message
	thinking      ThinkingConfig
	thinkingSet   bool // a run-level Thinking option was applied
	toolChoice    ToolChoiceConfig
	toolChoiceSet bool // a run-level ToolChoice option was applied
	params        RequestParams
	paramsSet     bool // a run-level Params option was applied
	// decisions resolves calls left pending by an earlier run: call id →
	// approve, deny with a reason, or resolve with content
	// (Approve, Deny, Resolve, ResolveError).
	decisions map[string]decision
	// steer is the run's steering source, installed by Steering; nil on
	// an ordinary run, and never inherited by a Subagent's child runs.
	steer SteerFunc
	// onMessages holds the run's transcript observers, installed by
	// OnMessages; the loop calls them as messages join the transcript.
	// Empty on an ordinary run, and never inherited by a child.
	onMessages []func(context.Context, int, []Message)
	// metadata is the run's own caller pairs, merged across its Metadata
	// options (later key winning); execute overlays it on the context's
	// inherited metadata before any span starts (metadata.go).
	metadata map[string]string
	// The dual options' run-side values (Instructions, MaxSteps,
	// Parallelism): a *Set flag per knob, the Thinking shape. execute
	// validates the lower-only rules (a raise is ErrInvalidRunOption,
	// before any model call) and resolves the effective values.
	system         string
	systemSet      bool
	maxSteps       int
	maxStepsSet    bool
	parallelism    int
	parallelismSet bool
	// onlyTools narrows the run to the named tools among the agent's
	// registered ones (narrowing only; an unknown name fails the run
	// before any model call). nil keeps the full set.
	onlyTools []string
	// model replaces the agent's model for this run; the WrapModel chain
	// is rebuilt over it. nil keeps the agent's.
	model Model
	// parkOn parks calls to the named tools at the approval boundary,
	// exactly as RequireApproval would (ADR 0007 applied per run).
	parkOn []string
	// parkAll is ParkAllExcept: every call parks unless its tool is in
	// parkExcept — the names every ParkAllExcept option of the run lists
	// (non-nil once set, possibly empty).
	parkAll    bool
	parkExcept map[string]bool
}

type decision struct {
	approved bool
	reason   string
	// A resolved call never runs its handler: content becomes the
	// ToolResultPart verbatim (isError marks ResolveError). A decision
	// is exactly one of approve / deny / resolve (ADR 0007's
	// 2026-09-22 amendment).
	content  string
	resolved bool
	isError  bool
}

func (c *runConfig) decide(id string, d decision) {
	if c.decisions == nil {
		c.decisions = map[string]decision{}
	}
	c.decisions[id] = d
}

type approveOption string

func (o approveOption) applyRun(c *runConfig) { c.decide(string(o), decision{approved: true}) }

// Approve resumes a call left on RunResult.Pending by an earlier run:
// pass the earlier transcript with Messages and the decision, and the
// loop executes the call — through the ordinary tool chain, with
// Call.Approved set — before its next model call. Ids that are not
// pending are ignored.
func Approve(callID string) RunOption { return approveOption(callID) }

type denyOption struct{ id, reason string }

func (o denyOption) applyRun(c *runConfig) { c.decide(o.id, decision{reason: o.reason}) }

// Deny resolves a pending call without running it: the model sees an
// error result reading "DENIED: <reason>" and the loop continues.
// Pending calls given neither Approve nor Deny are denied with the
// reason "no decision" ("DENIED: no decision").
func Deny(callID, reason string) RunOption { return denyOption{callID, reason} }

type resolveOption struct {
	id, content string
	isError     bool
}

func (o resolveOption) applyRun(c *runConfig) {
	c.decide(o.id, decision{content: o.content, resolved: true, isError: o.isError})
}

// Resolve resumes a pending call with a result computed outside the
// process — the human-as-tool-executor shape: run the query in prod,
// paste what happened, and the next model call sees it. The content
// becomes the call's ToolResultPart verbatim; the handler never runs,
// no execute_tool span or ToolStart/ToolFinish is emitted (nothing
// executed), and Call.Approved is never set. MaxResultBytes applies
// as to any result. Composes with Approve and Deny in one resuming
// call; the last option for an id wins.
//
// Resolve on a call that is not pending in the resumed transcript is a
// loud run error at step 0 — a deliberate asymmetry with Approve and
// Deny, which ignore unknown ids: those are yes/no marks over an id
// set, while Resolve carries a payload the caller expects the model to
// see, and dropping it silently is the one thing the error model
// forbids (ADR 0007's 2026-09-22 amendment).
func Resolve(callID, content string) RunOption {
	return resolveOption{id: callID, content: content}
}

// ResolveError is Resolve with the result marked as an error: the
// model sees the content on an error result, the shape a failed
// execution would have produced.
func ResolveError(callID, content string) RunOption {
	return resolveOption{id: callID, content: content, isError: true}
}

type runIDOption string

func (o runIDOption) applyRun(c *runConfig) { c.id = string(o) }

// RunID sets the run's identifier instead of generating one — for
// replays, idempotent retries, and correlating with an outer system's own
// ids. Empty values are ignored.
func RunID(id string) RunOption { return runIDOption(id) }

type onlyToolsOption struct{ names []string }

func (o onlyToolsOption) applyRun(c *runConfig) {
	if len(o.names) > 0 {
		c.onlyTools = append(c.onlyTools, o.names...)
	}
}

// OnlyTools narrows this run to the named tools among the agent's
// registered ones — the ToolSource snapshot when one exists, fetched
// fresh per step as ever. Narrowing only: the playground cannot add a
// tool, because a new tool is code. A name the agent does not have
// fails the run with ErrInvalidRunOption before any model call. The
// step's advertisement and its dispatch resolve against the same
// narrowed snapshot, so what the model was shown is exactly what runs;
// calls to a tool a PrepareStep function dropped fail as unknown, as
// today. Manifest and Agent.Tools keep reporting the static set: they
// describe the code, not one run's experiment (WEFT-PLAYGROUND §10.1
// [D5]). With no names, the run keeps the agent's full set. Several
// OnlyTools options add up: the run keeps every tool any of them names.
func OnlyTools(names ...string) RunOption { return onlyToolsOption{names} }

type useModelOption struct{ m Model }

func (o useModelOption) applyRun(c *runConfig) {
	if o.m != nil && !isNilModel(o.m) {
		c.model = o.m
	}
}

// UseModel replaces the agent's model for this run, rebuilding the
// WrapModel chain over it (first registered = outermost, the New rule):
// the run's model calls go through the same middleware over m. Pass a
// model the runtime registered as an allowed alternate; a nil model is
// ignored. RunStart.Model and the chat spans report the run's model
// (middleware forwards Info); Agent.Model and the manifest keep naming
// the agent's own (WEFT-PLAYGROUND §10.1 [D5]).
func UseModel(m Model) RunOption { return useModelOption{m} }

type parkOnOption struct{ names []string }

func (o parkOnOption) applyRun(c *runConfig) {
	if len(o.names) > 0 {
		c.parkOn = append(c.parkOn, o.names...)
	}
}

// ParkOn parks a call to any of the named tools at the approval
// boundary (ADR 0007), exactly as if the tool had been built with
// RequireApproval: the call gets its ToolStart and no ToolFinish, the
// run ends successfully with the call on RunResult.Pending, and
// Approve/Deny/Resolve on a resuming run decide it. This is how a
// breakpoint or side-effect parking reaches a runtime-started run
// without touching the agent, which is immutable after New (ADR 0024
// D7, WEFT-PLAYGROUND §10.1). With no names, nothing parks. Names are
// not validated — a name no tool carries parks nothing — and the set
// covers this run only: a Subagent's child run does not inherit it.
// ParkAllExcept is the default-deny form, for when the tools to park
// cannot all be named.
func ParkOn(tools ...string) RunOption { return parkOnOption{tools} }

type parkAllExceptOption struct{ names []string }

func (o parkAllExceptOption) applyRun(c *runConfig) {
	// Every option is its own "park all but these" rule and the rules
	// add up toward parking, so a second option keeps only the names
	// the first one also let through.
	keep := make(map[string]bool, len(o.names))
	for _, name := range o.names {
		if !c.parkAll || c.parkExcept[name] {
			keep[name] = true
		}
	}
	c.parkAll, c.parkExcept = true, keep
}

// ParkAllExcept parks, at the approval boundary (ADR 0007), every tool
// call of the run whose tool is not named — ParkOn turned around: the
// caller lists what may run, and everything else waits for a decision.
// It is the rule for a run that must not fire a side effect nobody
// vouched for (a playground re-run, WEFT-PLAYGROUND §6 rule 3; ADR 0024
// D7), where a list of tools to park cannot be complete:
//
//   - The rule is applied by name to the tool each call resolves to in
//     its step's dispatch snapshot, so a tool only a ToolSource supplies
//     — absent from Agent.Tools and the manifest — parks like any other.
//   - It reaches the runs started inside this run: a Subagent's child
//     run (any run on a tool call's context) applies the same rule to
//     its own tools, where ParkOn stops at the run it was given to. A
//     child that parks ends as a child approval boundary always has —
//     the delegating call's result is SUBAGENT_PENDING (ADR 0014); the
//     parent does not park. Names are matched in parent and child
//     alike, so name a tool only if every tool of that name down the
//     delegation may run. A child run's own ParkAllExcept can narrow
//     the inherited list, never widen it.
//
// A parked call is ParkOn's parked call: its ToolStart and no
// ToolFinish, the run ending successfully with it on RunResult.Pending,
// Approve/Deny/Resolve on the resuming run deciding it — an approved
// call runs once even when the resume carries the rule again, and the
// next call to the tool parks again. The rules only add up toward
// parking: a tool ParkOn names or built with RequireApproval parks
// whether or not it is named here, and several ParkAllExcept options
// let through only the names all of them list. OnlyTools is
// independent — it decides what is offered, this decides what of it
// runs unasked. A name no tool carries is not an error (a ToolSource's
// names are not known up front) and with no names every call parks —
// except an agent's own Output submission (submit_output on an agent
// built with Output, in this run or a child's): it is the run's
// answer, not a side effect, so it never needs naming; ParkOn can
// still park it.
func ParkAllExcept(names ...string) RunOption { return parkAllExceptOption{names} }

// parkRule is a run's park rule, resolved once in execute: the ParkOn
// names, and — when a ParkAllExcept is in force, the run's own or one
// inherited from the run it was started inside — the names that may
// still run. nil parks nothing.
type parkRule struct {
	on     map[string]bool
	all    bool
	except map[string]bool
}

// parks reports whether a call to the named tool parks under the rule.
// answer marks the running agent's own Output submission, which the
// default-deny half lets through: it is the run's answer, not a side
// effect. ParkOn naming it still parks it.
func (p *parkRule) parks(name string, answer bool) bool {
	if p == nil {
		return false
	}
	return p.on[name] || (p.all && !answer && !p.except[name])
}

// parkExceptKey is the context key for the ParkAllExcept list in force:
// execute places it so the runs started inside this one inherit it. The
// map is never mutated after it is placed; a present, empty map parks
// everything.
type parkExceptKey struct{}

// resolvePark builds the run's park rule from its own options and the
// except-list inherited on ctx, and returns the context the run's tool
// calls — and so its child runs — carry the list on. A child's own
// list intersects the inherited one: default-deny only tightens on the
// way down.
func (c *runConfig) resolvePark(ctx context.Context) (context.Context, *parkRule) {
	inherited, inForce := ctx.Value(parkExceptKey{}).(map[string]bool)
	except := inherited
	if c.parkAll {
		except = c.parkExcept
		if inForce {
			except = make(map[string]bool, len(c.parkExcept))
			for name := range c.parkExcept {
				if inherited[name] {
					except[name] = true
				}
			}
		}
		ctx = context.WithValue(ctx, parkExceptKey{}, except)
		inForce = true
	}
	if len(c.parkOn) == 0 && !inForce {
		return ctx, nil
	}
	rule := &parkRule{all: inForce, except: except}
	if len(c.parkOn) > 0 {
		rule.on = make(map[string]bool, len(c.parkOn))
		for _, name := range c.parkOn {
			rule.on[name] = true
		}
	}
	return ctx, rule
}

type onMessagesOption struct {
	fn func(context.Context, int, []Message)
}

func (o onMessagesOption) applyRun(c *runConfig) {
	if o.fn != nil {
		c.onMessages = append(c.onMessages, o.fn)
	}
}

// OnMessages returns the RunOption registering an observer the loop
// calls whenever messages join the run's transcript: the assistant
// message a step produced (reasoning, text and calls in their final
// shape, signatures included), the tool message that follows its calls,
// and the messages a steering drain delivered. msgs is exactly what
// joined, in transcript order, as a deep copy — retaining or mutating
// it changes nothing the run sees — and step is the step the messages
// belong to (a steer's messages name the step whose drain delivered
// them). The calls are synchronous on the run's goroutine, in transcript
// order, so a consumer that appends each batch to durable storage
// persists a mid-run crash's worth of exact transcript; like Tap an
// observer must be fast and must not block (the run waits for it), and
// like Tap a panic in one is contained and counted (TapPanics), never
// breaking the run. Observers cannot change anything — the transcript
// is the run's; behaviour attaches at the two seams. Several OnMessages
// options run in registration order. A Subagent's child run does not
// inherit them: a child's transcript belongs to whoever runs the child
// (the same rule as Steering). This is TODO §5.12's shape (b), the
// answer to "reconstruct messages from events (lossy: signatures, block
// boundaries) or wait for the run to end": the exact bytes, as they
// join.
func OnMessages(fn func(ctx context.Context, step int, msgs []Message)) RunOption {
	return onMessagesOption{fn}
}

// newRunID returns a random 128-bit hex identifier.
func newRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("weft: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func (c *runConfig) finish() {
	if c.id == "" {
		c.id = newRunID()
	}
}

// effectiveThinking resolves the run's reasoning request: a run-level
// Thinking option overrides the agent's construction-time default;
// neither set keeps the provider default (the zero ThinkingConfig).
func (c *runConfig) effectiveThinking(agentDefault ThinkingConfig) ThinkingConfig {
	if c.thinkingSet {
		return c.thinking
	}
	return agentDefault
}

// effectiveToolChoice resolves the run's tool-choice request the same
// way: a run-level ToolChoice option overrides the agent's default;
// neither set keeps the provider default (the zero ToolChoiceConfig).
func (c *runConfig) effectiveToolChoice(agentDefault ToolChoiceConfig) ToolChoiceConfig {
	if c.toolChoiceSet {
		return c.toolChoice
	}
	return agentDefault
}

// effectiveParams resolves the run's sampling request the same way: a
// run-level Params option replaces the agent's default whole (no field
// merge — the Thinking rule); neither set keeps the adapter's
// construction defaults (the zero RequestParams).
func (c *runConfig) effectiveParams(agentDefault RequestParams) RequestParams {
	if c.paramsSet {
		return c.params
	}
	return agentDefault
}

// effectiveSystem resolves the run's system prompt: a run-level
// Instructions option replaces the agent's; neither set keeps the
// agent's (the Thinking rule).
func (c *runConfig) effectiveSystem(agentDefault string) string {
	if c.systemSet {
		return c.system
	}
	return agentDefault
}

// effectiveMaxSteps resolves the run's step budget. The caller has
// already validated the lower-only rule; this only picks the value.
func (c *runConfig) effectiveMaxSteps(agentDefault int) int {
	if c.maxStepsSet {
		return c.maxSteps
	}
	return agentDefault
}

// effectiveParallelism resolves the run's tool width, the same rule.
func (c *runConfig) effectiveParallelism(agentDefault int) int {
	if c.parallelismSet {
		return c.parallelism
	}
	return agentDefault
}

type promptOption string

func (o promptOption) applyRun(c *runConfig) {
	c.messages = append(c.messages, User(string(o)))
}

// Prompt adds a user message to the run's input.
func Prompt(text string) RunOption { return promptOption(text) }

type messagesOption []Message

func (o messagesOption) applyRun(c *runConfig) {
	c.messages = append(c.messages, o...)
}

// Messages adds existing messages (a session transcript, few-shot examples)
// to the run's input.
func Messages(msgs ...Message) RunOption { return messagesOption(msgs) }

// StepRecord captures everything one model step produced: its text, the
// tool calls it requested, and the results of executing them in call order.
type StepRecord struct {
	Index int
	// StopReason is the mapped reason (stop, tool_calls, max_tokens);
	// RawStopReason is the provider's own value when the mapping was
	// approximated ("refusal", "content_filter", ...) — see
	// ModelFinish.Raw.
	StopReason    StopReason
	RawStopReason string
	Usage         Usage
	Text          string
	ToolCalls     []ToolCallPart
	Results       []ToolResultPart
	// SubagentUsage is the usage of each child run this step started,
	// keyed by the parent's call id — including a failed child's partial
	// usage. It is already included in RunResult.Usage; Usage above is
	// the step's own model call only. Nil when the step ran no subagent.
	SubagentUsage map[string]Usage
}

// RunResult is the outcome of a completed run: its id, the full transcript
// (including the input messages), one record per step, and summed usage.
type RunResult struct {
	ID string
	// StopReason is the last step's finish reason. StopMaxTokens here
	// means the final reply was cut off by the output-token limit — the
	// run still succeeds, and the caller decides what truncated text
	// means.
	StopReason StopReason
	Messages   []Message
	Steps      []StepRecord
	Usage      Usage
	// Pending lists the tool calls of the last step that await an
	// approval decision (RequireApproval, or middleware returning
	// ErrApprovalRequired). The run ended successfully without running
	// them and the transcript carries no result for them; resume with
	// Messages(res.Messages...) plus Approve/Deny per call.
	Pending []ToolCallPart
}

// NumSteps returns how many model calls the run made.
func (r *RunResult) NumSteps() int { return len(r.Steps) }

// Text returns the final assistant text — the text parts of the last
// assistant message.
func (r *RunResult) Text() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == RoleAssistant {
			return r.Messages[i].Text()
		}
	}
	return ""
}

// Run is a handle to one streaming execution. Create it with Agent.Stream,
// then either range over Events (exactly once) or call Wait, which runs the
// agent to completion and reports the final result.
type Run struct {
	agent   *Agent
	ctx     context.Context
	cancel  context.CancelFunc
	cfg     runConfig
	mu      sync.Mutex
	started bool
	result  *RunResult
	err     error
	done    chan struct{}
}

// Stream starts a run and returns its handle. The run is lazy: nothing
// executes until Events is consumed. Canceling ctx aborts the model call
// and any in-flight tools.
func (a *Agent) Stream(ctx context.Context, opts ...RunOption) *Run {
	cfg := runConfig{}
	for _, o := range opts {
		if o != nil {
			o.applyRun(&cfg)
		}
	}
	cfg.finish()
	ctx, cancel := context.WithCancel(ctx)
	return &Run{agent: a, ctx: ctx, cancel: cancel, cfg: cfg, done: make(chan struct{})}
}

// ID returns the run's identifier. It is fixed at Stream time, so it can be
// logged or handed to a client before the first event is consumed.
func (r *Run) ID() string { return r.cfg.id }

// Events returns the run's event stream. It is single-use; a second call
// yields only ErrRunConsumed.
//
// Events arrive in emission order (see ToolStart for the concurrent-tool
// ordering rule). Delivery is a direct hand-off over an unbuffered
// channel, and tool events are emitted while holding the step's
// event-ordering lock — so a slow consumer does not merely receive late:
// it delays event emission and gates the start of the step's subsequent
// tools. For latency-sensitive parallel tools, consume promptly (range
// over Events in a dedicated goroutine that buffers) or use Generate,
// which needs no consumer.
//
// A failed run delivers its error exactly once as the final
// element; a successful run ends with RunFinish. Breaking out of the range
// cancels the run.
func (r *Run) Events() iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		r.mu.Lock()
		if r.started {
			r.mu.Unlock()
			yield(nil, ErrRunConsumed)
			return
		}
		r.started = true
		r.mu.Unlock()

		ch := make(chan Event)
		go func() {
			// Defers run LIFO: close(ch) ends the consumer's range, then
			// close(done) unblocks Wait, then the run's context is
			// released.
			defer r.cancel()
			defer close(r.done)
			defer close(ch)
			emit := func(ev Event) {
				// The post-cancellation drop lives in execute's emit
				// wrapper (one home for the rule); this select is what
				// prevents blocking when the consumer has gone.
				select {
				case ch <- ev:
				case <-r.ctx.Done():
				}
			}
			res, err := r.agent.execute(r.ctx, r.cfg, emit)
			r.mu.Lock()
			r.result, r.err = res, err
			r.mu.Unlock()
		}()

		for ev := range ch {
			if !yield(ev, nil) {
				r.cancel() // consumer stopped early; unblock the producer
				return
			}
		}
		<-r.done
		r.mu.Lock()
		err := r.err
		r.mu.Unlock()
		if err != nil {
			yield(nil, err)
		}
	}
}

// Close releases the run's resources, canceling it if still running.
// It is for abandoned runs: a run you will consume needs no Close —
// Events and Wait release everything themselves. Safe to call any
// number of times, before or after consumption.
func (r *Run) Close() { r.cancel() }

// Wait blocks until the run finishes and returns its result. If Events has
// not been consumed, Wait runs the agent itself, discarding events; it is
// safe to call after ranging over Events, or from another goroutine while
// ranging over them.
func (r *Run) Wait() (*RunResult, error) {
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if !started {
		for range r.Events() {
		}
	}
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err
}

// Generate runs the agent to completion and returns the final result.
// Internally it is Stream with the events folded away; errors are returned
// as *RunError, with the partial transcript attached.
func (a *Agent) Generate(ctx context.Context, opts ...RunOption) (*RunResult, error) {
	cfg := runConfig{}
	for _, o := range opts {
		if o != nil {
			o.applyRun(&cfg)
		}
	}
	cfg.finish()
	return a.execute(ctx, cfg, func(Event) {})
}

// The weft.override.* span attributes (ADR 0024 S1.2, WEFT-PLAYGROUND
// §10.1): one per knob a RunOption changed, plus the hash over their
// canonical JSON — an experiment's fingerprint, so two sibling runs
// with equal changes carry equal hashes and a plain run carries none.
const (
	attrOverrideHash         = "weft.override.hash"
	attrOverrideInstructions = "weft.override.instructions"
	attrOverrideTools        = "weft.override.tools"
	attrOverrideModel        = "weft.override.model"
	attrOverrideParkOn       = "weft.override.park_on"
	attrOverrideParkExcept   = "weft.override.park_all_except"
	attrOverrideThinking     = "weft.override.thinking"
	attrOverrideToolChoice   = "weft.override.tool_choice"
	attrOverrideParams       = "weft.override.params"
	attrOverrideMaxSteps     = "weft.override.max_steps"
	attrOverrideParallelism  = "weft.override.parallelism"
)

// overrideAttrs renders the run's weft.override.* attributes: present
// only when a RunOption changed the agent's configuration, absent on a
// plain run. "Changed" means a run-level option was applied — the value
// may coincide with the agent's own; the fingerprint records what the
// run carried, not a diff. The instructions text itself is content: the
// attribute says only that it was replaced (true), and no record carries
// the text — a transcript has no system role, so the input messages
// record cannot — only the hash does. The hash covers every
// changed value, text included, as sha256 over the canonical JSON of a
// map (encoding/json sorts map keys, so equal changes hash equal); the
// tool subset and the parked tools enter it as sets (nameSet).
func (c *runConfig) overrideAttrs() []attribute.KeyValue {
	values := map[string]any{}
	var attrs []attribute.KeyValue
	add := func(name string, attrValue any, kv attribute.KeyValue) {
		values[name] = attrValue
		attrs = append(attrs, kv)
	}
	if c.systemSet {
		add("instructions", c.system, attribute.Bool(attrOverrideInstructions, true))
	}
	if len(c.onlyTools) > 0 {
		names := nameSet(c.onlyTools)
		add("tools", names, attribute.String(attrOverrideTools, strings.Join(names, ",")))
	}
	if c.model != nil {
		info := InfoOf(c.model)
		name := info.Name
		if info.Provider != "" {
			name = info.Provider + "/" + info.Name
		}
		add("model", name, attribute.String(attrOverrideModel, name))
	}
	if c.thinkingSet {
		add("thinking", thinkingOverride(c.thinking), attribute.String(attrOverrideThinking, thinkingOverride(c.thinking)))
	}
	if c.toolChoiceSet {
		tc := c.toolChoice
		value := string(tc.Mode)
		if tc.Mode == ToolChoiceNamed {
			value = string(ToolChoiceNamed) + ":" + tc.Name
		}
		add("tool_choice", value, attribute.String(attrOverrideToolChoice, value))
	}
	if c.paramsSet {
		b, err := json.Marshal(c.params)
		if err != nil {
			b = []byte("{}")
		}
		add("params", json.RawMessage(b), attribute.String(attrOverrideParams, string(b)))
	}
	if c.maxStepsSet {
		add("max_steps", c.maxSteps, attribute.Int(attrOverrideMaxSteps, c.maxSteps))
	}
	if c.parallelismSet {
		add("parallelism", c.parallelism, attribute.Int(attrOverrideParallelism, c.parallelism))
	}
	if len(c.parkOn) > 0 {
		names := nameSet(c.parkOn)
		add("park_on", names, attribute.String(attrOverrideParkOn, strings.Join(names, ",")))
	}
	if c.parkAll {
		// The run's own except-list — a set, on its own attribute, so
		// park_on keeps meaning ParkOn. Present and empty when the run
		// parks everything.
		names := slices.Sorted(maps.Keys(c.parkExcept))
		add("park_all_except", names, attribute.String(attrOverrideParkExcept, strings.Join(names, ",")))
	}
	if len(values) == 0 {
		return nil
	}
	b, err := json.Marshal(values)
	if err != nil {
		return attrs // unhashable overrides still name themselves
	}
	sum := sha256.Sum256(b)
	return append([]attribute.KeyValue{attribute.String(attrOverrideHash, hex.EncodeToString(sum[:]))}, attrs...)
}

// nameSet renders an option's accumulated tool names as the set they
// are — sorted, each once — so the fingerprint and its attribute do not
// depend on the order the names were given in, or on a name several
// options repeated.
func nameSet(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)
	return slices.Compact(out)
}

// thinkingOverride renders a ThinkingConfig for weft.override.thinking:
// the level by name, the budget beside it when set.
func thinkingOverride(cfg ThinkingConfig) string {
	var level string
	switch cfg.Level {
	case ThinkUnset:
		level = "unset"
	case ThinkOff:
		level = "off"
	case ThinkLow:
		level = "low"
	case ThinkMedium:
		level = "medium"
	case ThinkHigh:
		level = "high"
	default:
		level = strconv.Itoa(int(cfg.Level))
	}
	if cfg.Budget > 0 {
		return level + "/" + strconv.FormatInt(cfg.Budget, 10)
	}
	return level
}
