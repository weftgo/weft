package weft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// rblock accumulates one provider reasoning block. A block stays open
// until a delta carries its signature (providers send the signature
// last); the next reasoning delta then starts a fresh block.
type rblock struct {
	text strings.Builder
	sig  string
	open bool
}

// execute runs the agent loop: model call, tool fan-out, repeat until the
// model stops requesting tools or the step budget runs out. Every notable
// moment is reported through emit, which must be safe for concurrent use.
func (a *Agent) execute(ctx context.Context, cfg runConfig, sink func(Event)) (*RunResult, error) {
	// This agent now runs on this context: the ancestry chain grows by
	// one per nesting level, and a Subagent handler refuses a delegation
	// whose child is already on it (the cycle guard, ADR 0014).
	ctx = withAncestry(ctx, append(slices.Clone(ancestryOf(ctx)), a))
	// Every event passes through here exactly once: the taps observe it
	// (synchronously, in emission order, on the emitting goroutine),
	// then the sink receives it. Nothing is delivered after
	// cancellation — for taps and sinks alike, so Generate and Stream
	// agree and a consumer never observes a stream that continues past
	// its error. It reports whether the event was delivered: the
	// terminal RunFinish decides the run's outcome by that answer, so
	// a cancellation landing between a ctx check and the emit can never
	// produce a success without its RunFinish, or a RunFinish followed
	// by an error (rule 4; Run.Events' one-terminal-element promise).
	deliver := func(ev Event) bool {
		if ctx.Err() != nil {
			return false
		}
		for _, tap := range a.taps {
			a.safeTap(ctx, tap, ev)
		}
		sink(ev)
		return true
	}
	emit := func(ev Event) { deliver(ev) }
	// The input transcript is repaired before the first model call, so
	// anything a caller feeds back in (a partial transcript, a resumed
	// session) becomes valid provider input. When the caller supplies
	// approval decisions, the calls left pending by the earlier run are
	// exempt from repair: this run resolves them itself, below.
	var resume []ToolCallPart
	var skip map[string]bool
	if len(cfg.decisions) > 0 {
		resume = unresolvedCalls(cfg.messages)
		skip = make(map[string]bool, len(resume))
		for _, c := range resume {
			skip[c.ID] = true
		}
	}
	res := &RunResult{ID: cfg.id, Messages: repair(cfg.messages, skip)}
	seq := new(atomic.Int64)
	fail := func(step int, err error) (*RunResult, error) {
		return nil, &RunError{Step: step, Err: err, Result: res}
	}
	// Every successful exit ends here — four sites share the shape, and
	// a field added to RunFinish must not miss any of them. One check
	// decides both the event's delivery and the run's outcome: a
	// delivered RunFinish is always followed by success, an undelivered
	// one (the run's ctx ended first) always by the cancellation error,
	// with the resumable result riding on it — cancellation wins over
	// success at the approval boundary too (ADR 0007). The caller sets
	// res.Pending first; snapshotPending(nil) is nil, so ordinary exits
	// carry no pending list.
	endRun := func(step int) (*RunResult, error) {
		if !deliver(RunFinish{RunID: cfg.id, Usage: res.Usage, Steps: len(res.Steps), Pending: snapshotPending(res.Pending)}) {
			return fail(step, ctx.Err())
		}
		return res, nil
	}
	// A model that can name itself does so on the first event; the
	// interface stays optional so Model remains one method.
	emit(RunStart{ID: cfg.id, Model: a.modelInfo(), Agent: a.name})

	// The approval boundary's second half: approved calls run now,
	// before any model call, and every other pending call is denied.
	// Their results complete the dangling tool message of the earlier
	// run, so the model sees an ordinary transcript.
	if len(resume) > 0 {
		if err := ctx.Err(); err != nil {
			return fail(0, err)
		}
		results, pending, sub, err := a.resolvePending(ctx, cfg, resume, seq, emit)
		if err != nil {
			return fail(0, err)
		}
		res.Messages = attachResults(res.Messages, resume, results)
		// Resumed delegations roll into the total only: there is no
		// StepRecord for resumed calls (ADR 0007), so no per-call map.
		for _, u := range sub {
			res.Usage = res.Usage.Add(u)
		}
		if len(pending) > 0 {
			res.Pending = pending
			return endRun(0)
		}
	}

	for step := 0; step < a.maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return fail(step, err)
		}
		// One snapshot per step: advertising, the sequential barrier,
		// and dispatch all resolve against this fetch, so what the
		// model was shown is exactly what runs. A source with a
		// duplicate name fails here rather than silently dropping a
		// tool.
		tools, err := a.dispatchTools()
		if err != nil {
			return fail(step, err)
		}
		emit(StepStart{RunID: cfg.id, Index: step})

		req := ModelRequest{
			System:          composeSystem(a.system, tools),
			Messages:        slices.Clone(res.Messages), // adapters cannot reach the run's transcript
			Tools:           slices.Clone(tools),        // nor the agent's tool list
			SequentialTools: a.parallelism == 1,
			// A run-level Thinking option overrides the agent's default
			// for this run alone (the thinkingOption applies to both).
			Thinking: cfg.effectiveThinking(a.thinking),
		}
		var (
			sb       strings.Builder
			rblocks  []rblock // provider reasoning, one entry per block
			calls    []ToolCallPart
			finish   ModelFinish
			finished bool
		)
		// The Model stream contract (see Model) is enforced here, not just
		// documented: exactly one ModelFinish, nothing after it, and a
		// panicking implementation becomes a run error instead of crashing
		// the run goroutine — which no caller could recover.
		consume := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("%w: model stream panicked: %v", ErrModelContract, p)
				}
			}()
			for mev, serr := range a.model.Stream(ctx, req) {
				if serr != nil {
					return fmt.Errorf("model stream: %w", serr)
				}
				if finished {
					return fmt.Errorf("%w: event %T after ModelFinish", ErrModelContract, mev)
				}
				switch e := mev.(type) {
				case ModelTextDelta:
					emit(TextDelta{RunID: cfg.id, Text: e.Text})
					sb.WriteString(e.Text)
				case ModelReasoningDelta:
					emit(ReasoningDelta{RunID: cfg.id, Text: e.Text})
					// One ReasoningPart per provider block: a signature
					// closes the block (providers send it last), the next
					// delta opens a new one. Unsigned reasoning keeps
					// accumulating into the open block — adapters drop
					// unsigned blocks on send, so their boundaries cannot
					// corrupt a round trip.
					if len(rblocks) == 0 || !rblocks[len(rblocks)-1].open {
						rblocks = append(rblocks, rblock{open: true})
					}
					cur := &rblocks[len(rblocks)-1]
					cur.text.WriteString(e.Text)
					if e.Signature != "" {
						cur.sig = e.Signature
						cur.open = false
					}
				case ModelToolCall:
					if e.ID == "" {
						return fmt.Errorf("%w: tool call with an empty ID", ErrModelContract)
					}
					// A repeated ID within one step makes the transcript
					// ambiguous — repair keeps only the first result per
					// ID, and Approve/Deny key on it — so it fails the
					// run like an empty one. IDs may repeat across steps.
					if slices.ContainsFunc(calls, func(c ToolCallPart) bool { return c.ID == e.ID }) {
						return fmt.Errorf("%w: duplicate tool call ID %q", ErrModelContract, e.ID)
					}
					if e.Name == "" {
						return fmt.Errorf("%w: tool call with an empty name", ErrModelContract)
					}
					calls = append(calls, ToolCallPart(e))
				case ModelToolCallDelta:
					// Progress only — the assembled call still arrives
					// as a ModelToolCall before ModelFinish.
					emit(ToolArgsDelta{RunID: cfg.id, Name: e.Name, Args: e.Args})
				case ModelFinish:
					finish = e
					finished = true
				default:
					return fmt.Errorf("%w: unexpected event %T", ErrModelContract, mev)
				}
			}
			if !finished {
				return fmt.Errorf("%w: stream ended without ModelFinish", ErrModelContract)
			}
			return nil
		}
		if err := consume(); err != nil {
			return fail(step, err)
		}

		// An assistant turn with no text and no calls is not appended:
		// providers reject empty content, and a transcript that is fed
		// back into a later run must stay valid input. Reasoning counts
		// as content — a signed block the provider may expect back must
		// not be dropped — and is placed before the text, the order the
		// model produced it in.
		msg := Message{Role: RoleAssistant}
		for i := range rblocks {
			b := &rblocks[i]
			if b.text.Len() > 0 || b.sig != "" {
				msg.Content = append(msg.Content, ReasoningPart{Text: b.text.String(), Signature: b.sig})
			}
		}
		if text := sb.String(); text != "" {
			msg.Content = append(msg.Content, TextPart{Text: text})
		}
		for _, c := range calls {
			msg.Content = append(msg.Content, c)
		}
		if len(msg.Content) > 0 {
			res.Messages = append(res.Messages, msg)
		}

		rec := StepRecord{
			Index:         step,
			StopReason:    finish.Reason,
			RawStopReason: finish.Raw,
			Usage:         finish.Usage,
			Text:          sb.String(),
			ToolCalls:     calls,
		}
		var pending []ToolCallPart
		var sub map[string]Usage
		switch {
		case len(calls) == 0:
		case finish.Reason == StopMaxTokens:
			// A message cut by the output-token limit must not have its
			// calls acted on: an intact-looking call may be the first
			// of several the model never finished issuing. Every call
			// fails without executing — a uniform, visible result — and
			// the loop continues so the model retries with a full
			// budget (ADR 0002, "fail truncated calls").
			for _, c := range calls {
				rec.Results = append(rec.Results, ToolResultPart{
					CallID:  c.ID,
					Name:    c.Name,
					IsError: true,
					Content: truncatedCallResult(c.Name),
				})
			}
		default:
			rec.Results, pending, sub = a.execTools(ctx, cfg.id, step, tools, calls, seq, emit, false)
		}
		if len(sub) > 0 {
			rec.SubagentUsage = sub
		}
		if len(rec.Results) > 0 {
			toolMsg := Message{Role: RoleTool}
			for _, r := range rec.Results {
				toolMsg.Content = append(toolMsg.Content, r)
			}
			res.Messages = append(res.Messages, toolMsg)
		}
		res.Steps = append(res.Steps, rec)
		res.StopReason = finish.Reason
		res.Usage = res.Usage.Add(finish.Usage)
		// Child-run usage rolls up after the step's own: StepFinish
		// reports the model call's numbers alone (finish.Usage), while
		// RunResult.Usage carries the whole bill, subagents included.
		for _, u := range sub {
			res.Usage = res.Usage.Add(u)
		}
		emit(StepFinish{RunID: cfg.id, Index: step, Reason: finish.Reason, Usage: finish.Usage, Raw: finish.Raw})

		if len(pending) > 0 {
			// The approval boundary: the step's other tools have run;
			// the pending calls have no result in the transcript. The
			// run ends successfully and the caller resumes it with
			// Approve/Deny — unless the run was canceled, in which case
			// cancellation wins, as it does everywhere else: the run
			// fails with the ctx error, the parked calls riding on
			// RunError.Result.Pending so the transcript stays resumable.
			res.Pending = pending
			return endRun(step)
		}
		if len(calls) == 0 {
			// A max_tokens finish is recorded, not fatal: RunResult
			// .StopReason (and the last StepRecord) carry it, so callers
			// can branch on truncation without indexing. Tool-call
			// arguments truncated into undecodable JSON already come back
			// as error results the model recovers from.
			return endRun(step)
		}
		if a.stopped(res.Steps) {
			return endRun(step)
		}
		// The continuation point: the loop is about to spend more, so
		// every budget is checked here. A step that ended the run above
		// succeeded even if it overshot — a budget stops further spend,
		// it does not discard finished work (ADR 0002).
		if err := a.guard(res); err != nil {
			return fail(step, err)
		}
	}

	// Cancellation during the last allowed step's tools is reported as
	// cancellation, not as step exhaustion: the cause wins over the budget.
	if err := ctx.Err(); err != nil {
		return fail(a.maxSteps-1, err)
	}
	// The model still wanted tools after its last allowed step. The
	// transcript, including the final step's tool results, rides on the
	// error.
	return fail(a.maxSteps, ErrMaxSteps)
}

// safeTap runs one tap, containing a panic and counting it (TapPanics):
// a broken observer must not break a run, but it must not be invisible
// either.
func (a *Agent) safeTap(ctx context.Context, tap func(context.Context, Event), ev Event) {
	defer func() {
		if recover() != nil {
			a.tapPanics.Add(1)
		}
	}()
	tap(ctx, ev)
}

// cloneRaw detaches a raw-JSON byte slice: json.RawMessage is mutable,
// and events are snapshots, so an event's Args must not alias the
// transcript's bytes.
func cloneRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

// snapshotPending deep-copies the pending calls carried on RunFinish:
// the event travels to its consumer, and its Args bytes must not alias
// the transcript's.
func snapshotPending(pending []ToolCallPart) []ToolCallPart {
	if pending == nil {
		return nil
	}
	out := make([]ToolCallPart, len(pending))
	for i, c := range pending {
		c.Args = cloneRaw(c.Args)
		out[i] = c
	}
	return out
}

// stopped reports whether any StopWhen condition is met.
func (a *Agent) stopped(steps []StepRecord) bool {
	for _, cond := range a.stops {
		if cond.Stop(steps) {
			return true
		}
	}
	return false
}

// guard is the continuation point: the checks that decide whether the
// loop may make another model call. The first breach wins; the others
// are not evaluated. Ordered by cheapness — a counter compare, then
// token compares (ADR 0002: budgets are checked only when the loop
// would otherwise spend more).
func (a *Agent) guard(res *RunResult) error {
	if limit := a.usageLimit; limit.InputTokens > 0 || limit.OutputTokens > 0 {
		over := (limit.InputTokens > 0 && res.Usage.InputTokens > limit.InputTokens) ||
			(limit.OutputTokens > 0 && res.Usage.OutputTokens > limit.OutputTokens)
		if over {
			return fmt.Errorf("%w (input %d/%d, output %d/%d)",
				ErrUsageLimit, res.Usage.InputTokens, limit.InputTokens,
				res.Usage.OutputTokens, limit.OutputTokens)
		}
	}
	return nil
}

// modelInfo reports the model's identity when it implements the
// optional Info interface; the zero ModelInfo otherwise.
func (a *Agent) modelInfo() ModelInfo { return InfoOf(a.model) }

// execTools runs one step's tool calls with bounded concurrency, all of
// them resolved against tools — the step's snapshot: advertising showed
// that list, so dispatch cannot name-resolve against anything fresher.
// Results are returned in call order regardless of completion order —
// determinism for the transcript; ToolStart/ToolFinish events carry the
// live interleaving. A failing tool never cancels its siblings; only
// ctx does.
//
// The concurrency slot is acquired here, in call order, before the tool's
// goroutine is spawned. That is what makes "in call order" true: tools
// start in the order the model requested them, and under Sequential each
// one finishes before the next begins. Calls that never obtain a slot
// because ctx was canceled produce an error result and no events.
//
// A tool marked Sequential is a barrier: the dispatcher waits for the
// in-flight calls to finish, runs it alone, and resumes. Calls the chain
// parks with ErrApprovalRequired are returned as pending rather than as
// results: they had a ToolStart and get no ToolFinish. approved marks
// calls resumed under an Approve decision.
//
// Every dispatched call carries a nest: how a Subagent handler reports
// its child run's events (wrapped in Nested, numbered from this run's
// counter under emitMu) and usage (the returned per-call map, keyed by
// call id). The dispatcher otherwise knows nothing about subagents.
func (a *Agent) execTools(ctx context.Context, runID string, step int, tools []*ToolDef, calls []ToolCallPart, seq *atomic.Int64, emit func(Event), approved bool) (results []ToolResultPart, pending []ToolCallPart, subagents map[string]Usage) {
	outcomes := make([]ToolResultPart, len(calls))
	parked := make([]bool, len(calls))
	sem := make(chan struct{}, a.parallelism)
	subs := map[string]Usage{}

	// Event-ordering rule for concurrent tools: the Seq is assigned and the
	// event emitted under one lock, so observed order always matches Seq
	// order. Without this, two tools could emit events whose arrival order
	// contradicts their sequence numbers — unreplayable streams.
	var emitMu sync.Mutex
	ordered := func(ev func(seq int64) Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(ev(seq.Add(1)))
	}
	// nestFor builds one call's reporting channel. emit and usage both
	// check the closed flag and act under emitMu, so a child event is
	// either fully before or fully after the call's ToolFinish, and a
	// usage record either lands in the map the step reports or is
	// dropped with the abandoned goroutine that produced it — a timed
	// out or cancelled delegation is not waited for, and its result was
	// already recorded (the usage half of the late-event rule, ADR 0004
	// and ADR 0014).
	nestFor := func(call ToolCallPart) *nest {
		n := new(nest)
		n.emit = func(ev Event) {
			emitMu.Lock()
			defer emitMu.Unlock()
			if n.closed.Load() {
				return
			}
			emit(Nested{RunID: runID, Seq: seq.Add(1), CallID: call.ID, Event: ev})
		}
		n.usage = func(u Usage) {
			emitMu.Lock()
			defer emitMu.Unlock()
			if n.closed.Load() {
				return
			}
			subs[call.ID] = subs[call.ID].Add(u)
		}
		return n
	}

	var wg sync.WaitGroup
	for i, call := range calls {
		def, _ := findTool(tools, call.Name)
		barrier := def != nil && def.sequential
		if barrier {
			// Let everything in flight finish before this call starts.
			// wg.Add only happens on this goroutine, so Wait is safe.
			wg.Wait()
		}
		// Never start a tool once the run is canceled. The explicit check
		// makes this deterministic: a select alone picks at random when a
		// slot frees up at the same moment ctx is done.
		canceled := ctx.Err() != nil
		if !canceled {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				canceled = true
			}
		}
		if canceled {
			outcomes[i] = ToolResultPart{
				CallID:  call.ID,
				Name:    call.Name,
				IsError: true,
				Content: "run canceled before the tool started",
			}
			continue
		}
		// ToolStart is emitted here, on the dispatching goroutine, so
		// start events are in call order by construction.
		ordered(func(s int64) Event {
			return ToolStart{RunID: runID, Seq: s, CallID: call.ID, Name: call.Name, Args: cloneRaw(call.Args)}
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			n := nestFor(call)
			callCtx := withNest(withCall(ctx, Call{RunID: runID, Step: step, CallID: call.ID, Name: call.Name, Approved: approved}), n)
			outcomes[i], parked[i] = a.callTool(callCtx, call, def)
			if parked[i] {
				return
			}
			ordered(func(s int64) Event {
				// The late-event rule (ADR 0004): the close rides under
				// emitMu with the finish, so no Nested event for this
				// call can follow its ToolFinish in the stream.
				n.closed.Store(true)
				return ToolFinish{
					RunID:   runID,
					Seq:     s,
					CallID:  call.ID,
					Name:    call.Name,
					Content: outcomes[i].Content,
					IsError: outcomes[i].IsError,
				}
			})
		}()
		if barrier {
			// Run alone: nothing after it starts until it is done.
			wg.Wait()
		}
	}
	wg.Wait()
	results = make([]ToolResultPart, 0, len(calls))
	for i, call := range calls {
		if parked[i] {
			pending = append(pending, call)
			continue
		}
		results = append(results, outcomes[i])
	}
	return results, pending, subs
}

// resolvePending runs the calls an earlier run left pending, under this
// run's decisions: approved calls execute through the ordinary chain
// with Call.Approved set; denied and undecided calls become error
// results the model sees. Results come back in call order; a call the
// chain parks again is returned as pending. The step index of the
// resumed calls is reported as 0: the original index is not recoverable
// from the transcript, and there is no StepRecord for them (ADR 0007).
// Audit lines should key on the CallID, not the step.
func (a *Agent) resolvePending(ctx context.Context, cfg runConfig, calls []ToolCallPart, seq *atomic.Int64, emit func(Event)) ([]ToolResultPart, []ToolCallPart, map[string]Usage, error) {
	// Resumed calls run before any step exists, so they fetch their own
	// snapshot — a separate consultation, like CallTool's.
	tools, err := a.dispatchTools()
	if err != nil {
		return nil, nil, nil, err
	}
	var approved []ToolCallPart
	for _, c := range calls {
		if d, ok := cfg.decisions[c.ID]; ok && d.approved {
			approved = append(approved, c)
		}
	}
	ran, pending, sub := a.execTools(ctx, cfg.id, 0, tools, approved, seq, emit, true)
	byID := make(map[string]ToolResultPart, len(ran))
	for _, r := range ran {
		byID[r.CallID] = r
	}
	parked := make(map[string]bool, len(pending))
	for _, c := range pending {
		parked[c.ID] = true
	}
	results := make([]ToolResultPart, 0, len(calls))
	for _, c := range calls {
		if parked[c.ID] {
			continue
		}
		if r, ok := byID[c.ID]; ok {
			results = append(results, r)
			continue
		}
		reason := "no decision"
		if d, ok := cfg.decisions[c.ID]; ok {
			reason = d.reason
		}
		results = append(results, ToolResultPart{
			CallID:  c.ID,
			Name:    c.Name,
			IsError: true,
			Content: deniedResult(reason),
		})
	}
	return results, pending, sub, nil
}

// unresolvedCalls returns the tool calls of the last assistant message
// that have no result on the tool message directly after it — the
// calls an earlier run left pending.
func unresolvedCalls(msgs []Message) []ToolCallPart {
	i := lastAssistantWithCalls(msgs)
	if i < 0 {
		return nil
	}
	served := map[string]bool{}
	if i+1 < len(msgs) && msgs[i+1].Role == RoleTool {
		for _, p := range msgs[i+1].Content {
			if r, ok := p.(ToolResultPart); ok {
				served[r.CallID] = true
			}
		}
	}
	var out []ToolCallPart
	for _, p := range msgs[i].Content {
		if c, ok := p.(ToolCallPart); ok && !served[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

func lastAssistantWithCalls(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != RoleAssistant {
			continue
		}
		for _, p := range msgs[i].Content {
			if _, ok := p.(ToolCallPart); ok {
				return i
			}
		}
		return -1
	}
	return -1
}

// attachResults places results on the tool message directly after the
// assistant message that issued calls, creating that message when the
// earlier run recorded none — so the transcript's shape is the
// canonical one whether or not other tools ran in that step. The
// message is rebuilt in the assistant's call order (ADR 0007 §3), each
// call taking its resumed result when one exists and its earlier one
// otherwise: Gemini matches functionResponses by name and position, so
// a reordered tool message could attach a result to the wrong call.
func attachResults(msgs []Message, calls []ToolCallPart, results []ToolResultPart) []Message {
	if len(results) == 0 {
		return msgs
	}
	i := lastAssistantWithCalls(msgs)
	if i < 0 {
		return msgs
	}
	resumed := make(map[string]ToolResultPart, len(results))
	for _, r := range results {
		resumed[r.CallID] = r
	}
	existing := map[string][]ToolResultPart{}
	if i+1 < len(msgs) && msgs[i+1].Role == RoleTool {
		for _, p := range msgs[i+1].Content {
			if r, ok := p.(ToolResultPart); ok {
				existing[r.CallID] = append(existing[r.CallID], r)
			}
		}
	}
	parts := make([]Part, 0, len(resumed)+len(existing))
	for _, p := range msgs[i].Content {
		c, ok := p.(ToolCallPart)
		if !ok {
			continue
		}
		if r, ok := resumed[c.ID]; ok {
			parts = append(parts, r)
			continue
		}
		if rs := existing[c.ID]; len(rs) > 0 {
			parts = append(parts, rs[0])
			existing[c.ID] = rs[1:]
		}
	}
	if i+1 < len(msgs) && msgs[i+1].Role == RoleTool {
		out := slices.Clone(msgs)
		out[i+1].Content = parts
		return out
	}
	return slices.Insert(slices.Clone(msgs), i+1, Message{Role: RoleTool, Content: parts})
}

// composeSystem appends the advertised tools' PromptSnippets to the
// agent's instructions: one paragraph per tool, in order, blank-line
// separated. Without snippets the instructions pass through unchanged.
func composeSystem(system string, tools []*ToolDef) string {
	var b strings.Builder
	b.WriteString(system)
	for _, t := range tools {
		if t.snippet == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t.snippet)
	}
	return b.String()
}

// Model-visible result strings — contract, pinned by tests (ADR 0002,
// ADR 0007).
func truncatedCallResult(name string) string {
	return fmt.Sprintf("tool call %s was not executed: the response hit the output token limit", name)
}

func deniedResult(reason string) string {
	return (&ToolError{Code: CodeDenied, Message: reason, Err: ErrApprovalDenied}).Error()
}

// CallTool dispatches one tool call by name the way the loop does —
// through the agent's WrapTools chain and the tool's own — and returns
// the result text the model would see. It is the seam for manual
// dispatchers. Unlike the loop it does not contain failures or apply
// run policy: an unknown name returns an error wrapping ErrNoSuchTool,
// undecodable arguments one wrapping ErrInvalidToolInput, a
// RequireApproval tool one wrapping ErrApprovalRequired, and handler
// errors, middleware errors, and panics propagate. A tool source whose
// snapshot carries a duplicate name returns an error wrapping
// ErrDuplicateTool. Timeouts and result caps are not applied;
// StrictInput is, at both levels, as in the loop.
func (a *Agent) CallTool(ctx context.Context, call ToolCallPart) (string, error) {
	tools, err := a.dispatchTools()
	if err != nil {
		return "", err
	}
	def, _ := findTool(tools, call.Name)
	strict := a.strict
	if def != nil && def.strict {
		strict = true
	}
	return a.chain(def, strict)(ctx, call)
}

// chain builds the tool-call chain for one call: agent middleware
// (outermost, first registered first) around the tool's middleware
// around the base caller, which resolves the tool, enforces
// RequireApproval, decodes, and runs the handler. def is nil for an
// unknown tool — the chain still runs, so Allow/Audit see the attempt,
// and the base returns the NO_SUCH_TOOL error.
func (a *Agent) chain(def *ToolDef, strict bool) ToolCaller {
	base := func(ctx context.Context, call ToolCallPart) (string, error) {
		if def == nil {
			return "", noSuchTool(call.Name)
		}
		if def.approval {
			if c, _ := CallFromContext(ctx); !c.Approved {
				return "", fmt.Errorf("%w: tool %q", ErrApprovalRequired, call.Name)
			}
		}
		return def.invoke(ctx, call.Args, strict)
	}
	c := base
	if def != nil {
		for i := len(def.mw) - 1; i >= 0; i-- {
			c = def.mw[i](c)
		}
	}
	for i := len(a.toolMW) - 1; i >= 0; i-- {
		c = a.toolMW[i](c)
	}
	return c
}

// callTool dispatches one call under the tool's effective policy and
// folds every failure — unknown tool, bad arguments, handler error,
// handler or middleware panic, timeout — into an error result the model
// can correct: a tool failure is data, not a run failure. def is the
// call's resolution against the step's (or CallTool's) snapshot; nil
// for an unknown name. Per-tool options override the agent's defaults;
// the result cap is applied last, over every outcome. An error wrapping
// ErrApprovalRequired is the one non-result: the call is reported
// pending instead.
func (a *Agent) callTool(ctx context.Context, call ToolCallPart, def *ToolDef) (res ToolResultPart, pending bool) {
	res = ToolResultPart{CallID: call.ID, Name: call.Name}
	resultCap := a.resultCap
	timeout := a.toolTimeout
	strict := a.strict
	if def != nil {
		if def.capSet {
			resultCap = def.resultCap
		}
		if def.timeoutSet {
			timeout = def.timeout // includes the Timeout(0) lift
		}
		strict = strict || def.strict
	}
	defer func() { res.Content = capResult(res.Content, resultCap) }()

	fn := a.chain(def, strict)
	var out string
	var err error
	if timeout <= 0 {
		out, err = invokeContained(ctx, fn, call)
	} else {
		out, err = invokeWithTimeout(ctx, fn, call, timeout)
	}
	if err != nil {
		if errors.Is(err, ErrApprovalRequired) {
			return res, true
		}
		res.IsError = true
		res.Content = err.Error()
		return res, false
	}
	res.Content = out
	return res, false
}

// invokeContained runs the chain, turning a panic — in the handler or
// in middleware — into an error. Containment sits outside the chain by
// design: no middleware can turn a panic into a run failure.
func invokeContained(ctx context.Context, fn ToolCaller, call ToolCallPart) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool %q panicked: %v", call.Name, p)
		}
	}()
	return fn(ctx, call)
}

// invokeWithTimeout runs the tool under a per-call deadline. The handler
// sees the deadline on its ctx; if it has not returned when the deadline
// passes, the call is recorded as timed out and the handler's goroutine
// is abandoned — a hung tool must not hang the run. A cancellation of
// the run's own ctx is reported as that cancellation, not as a timeout.
func invokeWithTimeout(ctx context.Context, fn ToolCaller, call ToolCallPart, timeout time.Duration) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := invokeContained(tctx, fn, call)
		done <- outcome{out, err}
	}()
	timedOut := func() error {
		return fmt.Errorf("tool %q timed out after %s", call.Name, timeout)
	}
	select {
	case o := <-done:
		// A handler that honoured the deadline returns
		// DeadlineExceeded itself; name the timeout rather than
		// echoing the context error.
		if o.err != nil && errors.Is(o.err, context.DeadlineExceeded) && ctx.Err() == nil && tctx.Err() != nil {
			return "", timedOut()
		}
		return o.out, o.err
	case <-tctx.Done():
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", timedOut()
	}
}

// capResult enforces a tool-result cap. The cut lands on a rune
// boundary and always ends with a marker naming the bytes the model did
// not receive, so the model knows the output is partial and how much of
// it is missing rather than silently receiving a prefix.
func capResult(s string, resultCap int) string {
	if resultCap <= 0 || len(s) <= resultCap {
		return s
	}
	cut := resultCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…[truncated %d bytes]", len(s)-cut)
}
