package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
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
	// A run started on a model chain's context (a middleware running a
	// child agent) does not report into that chain's step: its handlers
	// see no reporter, its own model calls install theirs.
	ctx = maskReport(ctx)
	// The run's metadata is merged and placed before any span starts, so
	// every span and record of this run carries it and a Subagent's child
	// run inherits it through the tool call's context (ADR 0024 S1.1).
	md, mdDropped := mergeMetadata(metadataFromCtx(ctx), cfg.metadata)
	if n := metadataDroppedFromCtx(ctx); n > 0 {
		// What WithMetadata dropped is this run's to report, once: its
		// children start from a cleared count.
		mdDropped += n
		ctx = context.WithValue(ctx, metadataDroppedKey{}, 0)
	}
	if md != nil || metadataFromCtx(ctx) != nil {
		// nil too when an own empty value removed every inherited pair.
		ctx = withMetadata(ctx, md)
	}
	// The run's model: the agent's, or — when UseModel replaced it — the
	// WrapModel chain rebuilt over the alternate, first registered
	// outermost, exactly as New builds it (ADR 0024: configuration the
	// run carries, not a seam). Every model call, span and RunStart of
	// this run resolves against this chain.
	model := a.model
	if cfg.model != nil {
		model = cfg.model
		for i := len(a.modelMW) - 1; i >= 0; i-- {
			model = a.modelMW[i](model)
			if isNilModel(model) {
				return nil, &RunError{Step: 0, Err: fmt.Errorf("%w: model middleware returned a nil Model", ErrModelContract), Result: &RunResult{ID: cfg.id}}
			}
		}
	}
	// The run span's own linkage and provenance: a subagent's child run
	// names its parent (the call it executes under), a named agent its
	// manifest hash, every run the core version, and a run whose metadata
	// hit the limits the drop count (ADR 0024 S1.2). A run that changed
	// its configuration carries the experiment's fingerprint
	// (weft.override.*).
	// The raw configured instructions' hash (ADR 0028 §4): the agent's
	// Instructions or the run's override, before PrepareStep and before
	// composition — fixed for the run, always present (sha256 of "" when
	// there are none). One hash per run, on RunStart, its record and the
	// invoke_agent span; it is not content. The agent's own is hashed
	// once at New; only a run override is hashed here.
	instructionsHash := a.instructionsHash
	if cfg.systemSet {
		instructionsHash = hashText(cfg.system)
	}
	// A resume's pending calls — the calls the earlier run left without
	// a result — are found before the run span starts: the span and the
	// run_start record report each one's decision (weft.resume.calls),
	// beside the run ResumeOf named (weft.resumed_from).
	var resume []ToolCallPart
	var skip map[string]bool
	if len(cfg.decisions) > 0 {
		resume = unresolvedCalls(cfg.messages)
		skip = make(map[string]bool, len(resume))
		for _, c := range resume {
			skip[c.ID] = true
		}
	}
	resumeCalls := resumeCallsAttr(resume, cfg.decisions)
	var runExtra []attribute.KeyValue
	runExtra = append(runExtra, cfg.overrideAttrs()...)
	if resumeCalls != "" {
		runExtra = append(runExtra, attrResumeCalls.String(resumeCalls))
	}
	if cfg.resumedFrom != "" {
		runExtra = append(runExtra, attrResumedFrom.String(cfg.resumedFrom))
	}
	runExtra = append(runExtra, attrInstructionsHash.String(instructionsHash))
	if c, ok := CallFromContext(ctx); ok {
		runExtra = append(runExtra, attrParentRunID.String(c.RunID), attrParentCallID.String(c.CallID))
	}
	if a.manifestHash != "" {
		runExtra = append(runExtra, attrManifestHash.String(a.manifestHash))
	}
	runExtra = append(runExtra, attrVersion.String(version))
	if mdDropped > 0 {
		runExtra = append(runExtra, attrMetadataDropped.Int(mdDropped))
	}
	// The run's own reporting begins here: one invoke_agent span, on this
	// context, so every chat, execute_tool and tap below parents under
	// it, ended by every exit with the outcome decided — including
	// cancellation, which no event reports (ADR 0016).
	// The recorder is built before the span so the span's end reads its
	// counters: weft.run.events / weft.run.messages, the records it
	// handed to the LoggerProvider (record contract §3).
	records := recorder{
		elog:         a.obs.elog,
		capture:      a.content,
		runID:        cfg.id,
		agent:        a.name,
		manifestHash: a.manifestHash,
		resumedFrom:  cfg.resumedFrom,
		resumeCalls:  resumeCalls,
		obs:          &a.obs,
		panics:       &a.tapPanics,
	}
	ctx, endSpan := a.obs.run(ctx, cfg.id, a.name, InfoOf(model), runExtra, &records)
	// A panic nothing contains — PrepareStep functions are arbitrary
	// user code; model, tool, and tap panics are contained further down
	// — must not leak the run span: this guard ends it with the panic
	// as its outcome and re-panics, so the caller still crashes and the
	// trace still closes (ADR 0016).
	var spanEnded bool
	var res *RunResult
	defer func() {
		if spanEnded {
			return
		}
		p := recover()
		r := res
		if r == nil {
			r = &RunResult{}
		}
		spanEnded = true
		endSpan(r, fmt.Errorf("%w: %v", errRunPanicked, p), runEnd{})
		if p != nil {
			panic(p)
		}
	}()
	// Every event passes through here exactly once: the taps observe it
	// (synchronously, in emission order, on the emitting goroutine), the
	// recorder reports it as an OTel log record (durable events and
	// deltas on two counters; Nested is not reported — the child run
	// emits its own), then the sink receives it. Nothing is delivered
	// after cancellation — for taps, records and sinks alike, so
	// Generate and Stream agree and a consumer never observes a stream
	// that continues past its error. It reports whether the event was
	// delivered: the terminal RunFinish decides the run's outcome by
	// that answer, so a cancellation landing between a ctx check and the
	// emit can never produce a success without its RunFinish, or a
	// RunFinish followed by an error (rule 4; Run.Events'
	// one-terminal-element promise).
	deliver := func(ev Event) bool {
		if ctx.Err() != nil {
			return false
		}
		for _, tap := range a.taps {
			a.safeTap(ctx, tap, ev, &records)
		}
		records.recordEvent(ctx, ev)
		sink(ev)
		return true
	}
	emit := func(ev Event) { deliver(ev) }
	// The input transcript is repaired before the first model call, so
	// anything a caller feeds back in (a partial transcript, a resumed
	// session) becomes valid provider input. When the caller supplies
	// approval decisions, the calls left pending by the earlier run are
	// exempt from repair: this run resolves them itself, below (resume
	// and skip are computed before the run span starts, which reports
	// them as weft.resume.calls).
	res = &RunResult{ID: cfg.id, Messages: repair(cfg.messages, skip)}
	seq := new(atomic.Int64)
	// The OnRunEnd observers report the run's outcome exactly once —
	// endRun's undelivered-RunFinish path routes through fail, and the
	// run must read as ended once, with the failure. They do not run on
	// the panic path below: a panicked run did not end, it crashed.
	observed := false
	observeRunEnd := func(err error) {
		if observed {
			return
		}
		observed = true
		for _, fn := range a.onRunEnd {
			a.safeRunEnd(ctx, fn, res, err)
		}
	}
	// held is the input's tail a resume will rebuild or extend (below):
	// recorded after the resumed results attach, or here, if the run
	// fails before they do — so the messages records still concatenate
	// to the transcript (ADR 0024 D1, ADR 0028 §8). It is accepted input,
	// like record 0, so it is recorded even when the run's context has
	// been cancelled (WithoutCancel): a cancelled resume's records still
	// rebuild RunError.Result.Messages.
	var held []Message
	fail := func(step int, err error) (*RunResult, error) {
		if held != nil {
			records.recordMessages(context.WithoutCancel(ctx), 0, held, false)
			held = nil
		}
		spanEnded = true
		re := &RunError{Step: step, Err: err, Result: res}
		endSpan(res, re, failEnd(ctx, err))
		observeRunEnd(re)
		return nil, re
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
	//
	// by names what ended the run (RunFinish.StoppedBy,
	// weft.run.stopped_by): a fired StopWhen condition's manifest name,
	// stoppedFinalAnswer or stoppedParked.
	endRun := func(step int, by string) (*RunResult, error) {
		if !deliver(RunFinish{RunID: cfg.id, Usage: res.Usage, Steps: len(res.Steps), Pending: snapshotPending(res.Pending), StoppedBy: by}) {
			return fail(step, ctx.Err())
		}
		spanEnded = true
		endSpan(res, nil, runEnd{stoppedBy: by})
		observeRunEnd(nil)
		return res, nil
	}
	// A model that can name itself does so on the first event; the
	// interface stays optional so Model remains one method.
	emit(RunStart{ID: cfg.id, Model: InfoOf(model), Agent: a.name, InstructionsHash: instructionsHash})
	// The input joins the record stream before anything else grows the
	// transcript: the repaired input, index 0, step 0 (ADR 0024 D1 —
	// today it is never reported at all, so a stored transcript could
	// not rebuild what the run was fed). An empty input (a run with no
	// messages) emits nothing.
	//
	// A resume attaches its results right after the last assistant
	// message with calls — rebuilding the partial tool message there,
	// or inserting a new one before anything that follows. The input
	// record stops at that assistant message, and what follows it is
	// recorded after the attach, as the next growth record: records
	// concatenated in index order stay the transcript, and a request's
	// messages_ref resolves to exactly its messages (ADR 0028 §3).
	inputRec := res.Messages
	if len(resume) > 0 {
		if i := lastAssistantWithCalls(res.Messages); i >= 0 && i+1 < len(res.Messages) {
			inputRec, held = res.Messages[:i+1], res.Messages[i+1:]
		}
	}
	records.recordMessages(ctx, 0, inputRec, true)
	if ctx.Err() != nil {
		// Cancelled before the input record went out: no record of this
		// run exists, so its tail is not recorded alone either.
		held = nil
	}

	// The approval boundary's second half: approved calls run now,
	// before any model call, and every other pending call is denied.
	// Their results complete the dangling tool message of the earlier
	// run, so the model sees an ordinary transcript.
	if err := ctx.Err(); err != nil {
		return fail(0, err)
	}
	// A Resolve carries a payload the caller expects the model to
	// see: one aimed at a call that is not pending is a programming
	// error, loud at step 0. The check runs even when nothing is
	// pending — an empty resume makes every resolved id a breach, and
	// the check living inside the resume block used to drop such
	// Resolves silently instead (review 2026-09-24 §2.1). Approve and
	// Deny keep ignoring unknown ids — the deliberate asymmetry ADR
	// 0007's 2026-09-22 amendment records. Sorted, so the first breach
	// is deterministic when several ids miss. The common run carries
	// no decisions at all, so the pending-id set is only built when
	// there is something to check it against.
	if len(cfg.decisions) > 0 {
		pendingIDs := make(map[string]bool, len(resume))
		for _, c := range resume {
			pendingIDs[c.ID] = true
		}
		for _, id := range slices.Sorted(maps.Keys(cfg.decisions)) {
			if cfg.decisions[id].resolved && !pendingIDs[id] {
				return fail(0, fmt.Errorf("resolve: call %q is not pending in this run's transcript", id))
			}
		}
	}
	// Per-run configuration the agent refuses [D5]: a raised limit. The
	// check runs before any model call and before any resumed call, so a
	// misconfigured run does not start; errors.Is(err, ErrInvalidRunOption)
	// is the branch. Tool narrowing (OnlyTools) validates against the
	// step-0 snapshot just below, with the same rule.
	if cfg.maxStepsSet && cfg.maxSteps > a.maxSteps {
		return fail(0, fmt.Errorf("%w: max_steps %d raises the agent's %d; per run it may only lower", ErrInvalidRunOption, cfg.maxSteps, a.maxSteps))
	}
	if cfg.parallelismSet && cfg.parallelism > a.parallelism {
		return fail(0, fmt.Errorf("%w: parallelism %d raises the agent's %d; per run it may only lower", ErrInvalidRunOption, cfg.parallelism, a.parallelism))
	}
	// Narrowing validates against the run's first snapshot, before any
	// model call: an unknown name is a misconfiguration, not a run-time
	// surprise. A ToolSource that later drops a narrowed tool simply
	// stops offering it — calls to it fail as unknown, the standing
	// rule.
	if len(cfg.onlyTools) > 0 {
		snapshot, err := a.dispatchTools()
		if err != nil {
			return fail(0, err)
		}
		for _, name := range cfg.onlyTools {
			if _, ok := findTool(snapshot, name); !ok {
				return fail(0, fmt.Errorf("%w: tool %q is not registered on this agent; OnlyTools narrows, it cannot add", ErrInvalidRunOption, name))
			}
		}
	}
	// The run's tool fetch: the agent's snapshot narrowed to the named
	// subset. Advertising and dispatch both resolve against it, so what
	// the model was shown is exactly what runs (the one-snapshot rule).
	fetchTools := func() ([]*ToolDef, error) {
		tools, err := a.dispatchTools()
		if err != nil || len(cfg.onlyTools) == 0 {
			return tools, err
		}
		return narrowTools(tools, cfg.onlyTools), nil
	}
	// The run's effective configuration, resolved once: the loop reads
	// these, not the agent's fields, so a run-level option (the dual
	// Instructions/MaxSteps/Parallelism, the Thinking shape) reaches
	// every step of this run alone (ADR 0024: configuration the run
	// carries, not a seam).
	maxSteps := cfg.effectiveMaxSteps(a.maxSteps)
	parallelism := cfg.effectiveParallelism(a.parallelism)
	// The run's park rule (ParkOn, ParkAllExcept): the approval boundary
	// applied per run — a call the rule names parks exactly as
	// RequireApproval would (ADR 0007; ADR 0024 D7). An except-list in
	// force goes onto the context here, before any tool call's context
	// is derived from it, so the runs started inside this one inherit
	// it; nothing else reads it.
	//
	// The agent's ParkSources (ADR 0030) are read here, once: their names
	// join the run's ParkOn set. cfg is this run's copy, and the override
	// attributes were taken above, so a source's answer is never an
	// override.
	if len(a.parkSources) > 0 {
		cfg.parkOn = append(slices.Clone(cfg.parkOn), a.readParkSources(ctx, cfg.id)...)
	}
	ctx, parkSet := cfg.resolvePark(ctx)
	if len(resume) > 0 {
		results, pending, sub, err := a.resolvePending(ctx, cfg, resume, seq, emit, fetchTools, parkSet, &records)
		if err != nil {
			return fail(0, err)
		}
		var joined []Message
		cut, tailHeld := len(res.Messages)-len(held), held != nil
		res.Messages, joined = attachResults(res.Messages, resume, results)
		if tailHeld {
			// The held tail as it is now: the rebuilt (or inserted)
			// tool message and whatever followed it — accepted input,
			// recorded through a cancellation (see held).
			held = nil
			records.recordMessages(context.WithoutCancel(ctx), 0, res.Messages[cut:], false)
		}
		if len(joined) > 0 {
			// The completed tool message — created, or rebuilt over one
			// the earlier run left partial — joins the transcript at
			// step 0 of the resume (ADR 0007 §3). The transcript
			// observers see it here, where the transcript grew, exactly
			// as they see every step's messages. Its record is the growth
			// record after the input: the held tail above when the input
			// continued past the assistant message (ADR 0028 §8), else
			// the joined message itself, below — so a resume's stored
			// transcript rebuilds too.
			a.observeMessages(ctx, cfg, 0, joined, &records)
			if !tailHeld {
				// Appended at the end: the next growth record.
				// Recorded through a cancellation, as the held tail is:
				// the joined message is accepted transcript either way.
				records.recordMessages(context.WithoutCancel(ctx), 0, joined, false)
			}
		}
		// Resumed delegations roll into the total only: there is no
		// StepRecord for resumed calls (ADR 0007), so no per-call map.
		res.Usage = rollUp(res.Usage, sub)
		if len(pending) > 0 {
			res.Pending = pending
			return endRun(0, stoppedParked)
		}
	}

	state := &loopState{retries: map[string]int{}}
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return fail(step, err)
		}
		// One snapshot per step: advertising, the sequential barrier,
		// and dispatch all resolve against this fetch, so what the
		// model was shown is exactly what runs. A source with a
		// duplicate name fails here rather than silently dropping a
		// tool.
		tools, err := fetchTools()
		if err != nil {
			return fail(step, err)
		}
		emit(StepStart{RunID: cfg.id, Index: step})

		req := ModelRequest{
			System:   cfg.effectiveSystem(a.system),
			Messages: slices.Clone(res.Messages), // adapters cannot reach the run's transcript
			Tools:    slices.Clone(tools),        // nor the agent's tool list
			// A run-level Thinking option overrides the agent's default
			// for this run alone (the thinkingOption applies to both);
			// so do the dual Instructions/MaxSteps/Parallelism.
			SequentialTools: parallelism == 1,
			Thinking:        cfg.effectiveThinking(a.thinking),
			// The same dual-option rule for a forced tool choice.
			ToolChoice: cfg.effectiveToolChoice(a.toolChoice),
			// And for per-request sampling knobs.
			Params: cfg.effectiveParams(a.params),
		}
		// PrepareStep functions are arbitrary user code, and the request
		// they see promises they may mutate it freely (ADR 0006
		// amendment): messages and tool definitions are deep-copied at
		// that boundary, so nothing a function writes in place — a
		// dropped part, re-formed argument bytes, a rewritten
		// description — reaches the run's transcript or the agent's
		// frozen registry. The model seam keeps the lighter slice
		// copies: adapters hold the read-only request contract.
		if a.prepare != nil {
			req.Messages = cloneMessages(req.Messages)
			req.Tools = cloneTools(req.Tools)
		}
		// The one loop knob: PrepareStep rewrites the request before the
		// model seam, on the raw instructions; what its chain returns is
		// what the step both advertises and dispatches against, so the
		// one-snapshot-per-step invariant holds by construction (ADR
		// 0006 amendment).
		for _, fn := range a.prepare {
			var err error
			req, err = fn(ctx, step, req)
			if err != nil {
				return fail(step, err)
			}
		}
		tools = req.Tools
		if a.prepare != nil {
			if err := validateSnapshot(tools); err != nil {
				return fail(step, err)
			}
		}
		// A forced choice runs against the same snapshot the step
		// advertises and dispatches over — validated after the
		// PrepareStep chain, so a function that sets a name and drops
		// the tool fails in the same place. A programming error the
		// caller fixes, reported in the text, not a sentinel (ADR
		// 0013's 2026-09-22 amendment).
		if err := validateToolChoice(req.ToolChoice, tools); err != nil {
			return fail(step, err)
		}
		// Sampling knobs validated at the same point, so an agent
		// default, a run option, and a PrepareStep edit fail alike.
		if err := validateParams(req.Params); err != nil {
			return fail(step, err)
		}
		req.System = composeSystem(req.System, tools)
		var (
			sb       strings.Builder
			rblocks  []rblock // provider reasoning, one entry per block
			calls    []ToolCallPart
			finish   ModelFinish
			finished bool
		)
		// The model call's own reporting: one chat span around the chain —
		// PrepareStep has run and mw.Retry/mw.Fallback sit inside it, so
		// the span measures the chain's outcome (ADR 0016). The stream is
		// consumed on the span's context, so an adapter's own HTTP spans
		// parent under chat.
		info := InfoOf(model)
		mctx, endModel := a.obs.model(ctx, cfg.id, step, info)
		// What this call is given, recorded beside it (ADR 0028): the
		// prompt and tools records when their hash is new to the run,
		// then the request record (attempt 1), on the chat span's
		// context, after PrepareStep, validation and composition, right
		// before the chain runs. Reporting only: req is not touched.
		reqRec := records.recordRequest(mctx, a, step, req, info, parkSet, res.Messages, a.prepare != nil)
		// The chain's reporting path (ReportFromContext): attempts and
		// wire bodies the chain reports land on this step's record; each
		// attempt after the first adds its own request record.
		rep := a.obs.withReport(mctx, cfg.id, step, &a.tapPanics)
		rep.request = reqRec
		rep.rec = &records
		mctx = rep
		// The Model stream contract (see Model) is enforced here, not just
		// documented: exactly one ModelFinish, nothing after it, and a
		// panicking implementation becomes a run error instead of crashing
		// the run goroutine — which no caller could recover.
		//
		// The call's timing is measured here as the stream is consumed
		// (ADR 0016's A4 note): a clock read at the start, one at the
		// first TextDelta/ToolArgsDelta, one at the end, kept on the
		// step's reporter (already allocated, so timing costs no
		// allocation) — and nothing the loop decides on.
		consume := func() (err error) {
			rep.start = time.Now()
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("%w: model stream panicked: %v", ErrModelContract, p)
				}
			}()
			for mev, serr := range model.Stream(mctx, req) {
				if serr != nil {
					return fmt.Errorf("model stream: %w", serr)
				}
				if finished {
					return fmt.Errorf("%w: event %T after ModelFinish", ErrModelContract, mev)
				}
				switch e := mev.(type) {
				case ModelTextDelta:
					rep.firstDelta()
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
					rep.firstDelta()
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
		streamErr := consume()
		timing := callTiming{ttft: rep.ttft, latency: max(sinceAtLeast(rep.start), rep.ttft)}
		rep.end()
		// The model that answered (ADR 0016's A4 note): the last attempt
		// the chain reported as a success, else the model the call asked
		// for; only a call that finished has one.
		if finished && streamErr == nil {
			if timing.answered = rep.answeredModel(); timing.answered == "" {
				timing.answered = info.Name
			}
		}
		endModel(finish, finished, len(calls), streamErr, timing)
		if streamErr != nil {
			return fail(step, streamErr)
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
			a.observeMessages(ctx, cfg, step, []Message{msg}, &records)
			records.recordMessages(ctx, step, []Message{msg}, false)
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
			var retried []string
			rec.Results, pending, sub, retried = a.execTools(ctx, cfg.id, step, tools, calls, seq, emit, false, parallelism, parkSet, &records, state.retries)
			for _, name := range retried {
				state.retries[name]++
			}
			for _, r := range rec.Results {
				if !r.IsError {
					delete(state.retries, r.Name) // a success resets the count
				}
			}
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
			a.observeMessages(ctx, cfg, step, []Message{toolMsg}, &records)
			records.recordMessages(ctx, step, []Message{toolMsg}, false)
		}
		res.Steps = append(res.Steps, rec)
		res.StopReason = finish.Reason
		res.Usage = res.Usage.Add(finish.Usage)
		// Child-run usage rolls up after the step's own: StepFinish
		// reports the model call's numbers alone (finish.Usage), while
		// RunResult.Usage carries the whole bill, subagents included.
		res.Usage = rollUp(res.Usage, sub)
		records.stepModel = timing.answered
		emit(StepFinish{RunID: cfg.id, Index: step, Reason: finish.Reason, Usage: finish.Usage, Raw: finish.Raw,
			LatencyMS: ceilMS(timing.latency), TTFTMS: ceilMS(timing.ttft)})

		if len(pending) > 0 {
			// The approval boundary: the step's other tools have run;
			// the pending calls have no result in the transcript. The
			// run ends successfully and the caller resumes it with
			// Approve/Deny — unless the run was canceled, in which case
			// cancellation wins, as it does everywhere else: the run
			// fails with the ctx error, the parked calls riding on
			// RunError.Result.Pending so the transcript stays resumable.
			res.Pending = pending
			return endRun(step, stoppedParked)
		}
		// final marks the step the run would otherwise end on: the model
		// made no tool calls (a max_tokens finish with calls is not
		// final — its calls all carry error results and the loop
		// continues).
		final := len(calls) == 0
		if by, ok := a.stopped(res.Steps); ok {
			// An intended end stays an end: a StopWhen condition firing
			// ends the run here in every case, before the steering
			// drain below — a steer cannot un-end it (ADR 0019 §2).
			return endRun(step, by)
		}
		// The steering drain points (ADR 0019 §2): after a step's tool
		// batch, when every call of the batch has its result — success,
		// error, truncated, or denied — so the call/result pairing
		// cannot be split, and at a final step, where a delivered
		// message redirects the run into one more step instead of
		// ending. The boundary above and the StopWhen end just passed
		// are the two non-drain exits: the source keeps its messages
		// for a follow-up. The loop goroutine is alone here — tools
		// finished, the next model call not started — so the Seq below
		// cannot interleave with a tool event's.
		var steered []Message
		if cfg.steer != nil {
			steered = cfg.steer(ctx, SteerPoint{RunID: cfg.id, Step: step, Final: final})
			for _, m := range steered {
				if m.Role != RoleUser {
					return fail(step, fmt.Errorf("%w: role %q at step %d", ErrInvalidSteer, m.Role, step))
				}
			}
		}
		if final && len(steered) == 0 {
			return endRun(step, stoppedFinalAnswer)
		}
		if len(steered) > 0 {
			// Delivered messages are transcript (ADR 0019 §3): appended
			// before the next step's PrepareStep chain runs, so request
			// rewrites see them and the transcript stays the single
			// source of truth. The event is a snapshot — the transcript
			// owns the delivered messages, the event carries a copy.
			res.Messages = append(res.Messages, steered...)
			a.observeMessages(ctx, cfg, step, steered, &records)
			records.recordMessages(ctx, step, steered, false)
			emit(Steered{RunID: cfg.id, Seq: seq.Add(1), Step: step, Messages: cloneMessages(steered)})
		}
		// The continuation point: the loop is about to spend more, so
		// every budget is checked here. A step that ended the run above
		// succeeded even if it overshot — a budget stops further spend,
		// it does not discard finished work (ADR 0002). A redirect at a
		// Final point consumes a step and goes through the same checks
		// as any continuation; if they fail, the steer is in
		// RunError.Result.Messages, delivered but unanswered (ADR 0019
		// §5).
		if err := a.guard(res, &rec, state); err != nil {
			return fail(step, err)
		}
	}

	// Cancellation during the last allowed step's tools is reported as
	// cancellation, not as step exhaustion: the cause wins over the budget.
	if err := ctx.Err(); err != nil {
		return fail(maxSteps-1, err)
	}
	// The model still wanted tools after its last allowed step. The
	// transcript, including the final step's tool results, rides on the
	// error.
	return fail(maxSteps, ErrMaxSteps)
}

// observeMessages runs the run's transcript observers (OnMessages) over
// one batch of messages that just joined the run's transcript — a deep
// copy, so an observer that retains or mutates its slice touches
// nothing the run sees. The fire sites are the transcript's growth
// points (the loop goroutine, between event emissions), so the calls
// come in transcript order without holding the event-ordering lock. A
// panicking observer is contained and counted like a tap: a broken
// observer must not break a run, but it must not be invisible either.
func (a *Agent) observeMessages(ctx context.Context, cfg runConfig, step int, msgs []Message, rec *recorder) {
	if len(cfg.onMessages) == 0 {
		return
	}
	snapshot := cloneMessages(msgs)
	for _, fn := range cfg.onMessages {
		a.safeOnMessages(ctx, fn, step, snapshot, rec)
	}
}

// safeOnMessages runs one transcript observer, containing a panic and
// counting it (TapPanics) — the same containment a tap gets.
func (a *Agent) safeOnMessages(ctx context.Context, fn func(context.Context, int, []Message), step int, msgs []Message, rec *recorder) {
	defer func() {
		if recover() != nil {
			if rec != nil {
				rec.countPanic()
			} else {
				a.tapPanics.Add(1)
			}
		}
	}()
	fn(ctx, step, msgs)
}

// safeTap runs one tap, containing a panic and counting it (TapPanics):
// a broken observer must not break a run, but it must not be invisible
// either.
//
// rec, when not nil, is the run's recorder: the panic is counted there
// (it adds to TapPanics too) for the run's weft.tap.panics.
func (a *Agent) safeTap(ctx context.Context, tap func(context.Context, Event), ev Event, rec *recorder) {
	defer func() {
		if recover() != nil {
			if rec != nil {
				rec.countPanic()
			} else {
				a.tapPanics.Add(1)
			}
		}
	}()
	tap(ctx, ev)
}

// safeRunEnd runs one OnRunEnd observer under Tap's containment rule:
// the outcome observer runs after the run is decided, so a panic in it
// cannot change the outcome either — it is contained and counted with
// the tap panics.
func (a *Agent) safeRunEnd(ctx context.Context, fn func(context.Context, *RunResult, error), res *RunResult, err error) {
	defer func() {
		if recover() != nil {
			a.tapPanics.Add(1)
		}
	}()
	fn(ctx, res, err)
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

// cloneMessages deep-copies a transcript for a request handed to user
// code: each message and its parts are copied, so an in-place write —
// dropping a part, rewriting a tool call's raw arguments — cannot
// reach the run's transcript. Strings are immutable; the two mutable
// payloads, a tool call's Args and a file's Data, are re-allocated.
func cloneMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		content := make([]Part, len(m.Content))
		for j, p := range m.Content {
			switch p := p.(type) {
			case ToolCallPart:
				p.Args = cloneRaw(p.Args)
				content[j] = p
			case FilePart:
				p.Data = bytes.Clone(p.Data)
				content[j] = p
			default:
				content[j] = p
			}
		}
		m.Content = content
		out[i] = m
	}
	return out
}

// cloneTools deep-copies a tool list for the same boundary: each
// definition is a frozen-registry clone, so a PrepareStep function
// that rewrites a definition changes this step's snapshot only.
func cloneTools(tools []*ToolDef) []*ToolDef {
	out := make([]*ToolDef, len(tools))
	for i, t := range tools {
		out[i] = t.clone()
	}
	return out
}

// stopped reports whether any StopWhen condition is met and, when one
// is, the first in StopWhen order that fired, by its manifest name
// (stopName): the run's weft.run.stopped_by.
func (a *Agent) stopped(steps []StepRecord) (string, bool) {
	for _, cond := range a.stops {
		if cond.Stop(steps) {
			return stopName(cond), true
		}
	}
	return "", false
}

// loopState is the run-scoped state the guard carries between steps.
type loopState struct {
	// retries counts consecutive RETRY results per tool name; a
	// successful result for the tool resets it (Pydantic AI's rule: a
	// tool that eventually succeeds is not stuck).
	retries map[string]int
	// lastSig/streak track the loop detector: the previous step's tool
	// signature and how many consecutive steps have shared it.
	lastSig uint64
	streak  int
}

// guard is the continuation point: the checks that decide whether the
// loop may make another model call. The first breach wins; the others
// are not evaluated. The retry pass sorts its keys so an exhaustion
// error names one deterministic tool when several are stuck; the usage
// compares and the loop hash follow (ADR 0002: budgets are checked
// only when the loop would otherwise spend more).
func (a *Agent) guard(res *RunResult, rec *StepRecord, st *loopState) error {
	for _, name := range slices.Sorted(maps.Keys(st.retries)) {
		if st.retries[name] > a.maxModelRetries {
			return fmt.Errorf("%w: tool %q retried %d times, past MaxModelRetries(%d)",
				ErrModelRetriesExceeded, name, st.retries[name], a.maxModelRetries)
		}
	}
	if limit := a.usageLimit; limit.InputTokens > 0 || limit.OutputTokens > 0 {
		over := (limit.InputTokens > 0 && res.Usage.InputTokens > limit.InputTokens) ||
			(limit.OutputTokens > 0 && res.Usage.OutputTokens > limit.OutputTokens)
		if over {
			return fmt.Errorf("%w (input %d/%d, output %d/%d)",
				ErrUsageLimit, res.Usage.InputTokens, limit.InputTokens,
				res.Usage.OutputTokens, limit.OutputTokens)
		}
	}
	if a.detectLoops > 0 {
		sig := stepSignature(rec.ToolCalls)
		if sig == st.lastSig && st.streak > 0 {
			st.streak++
		} else {
			st.streak = 1
		}
		st.lastSig = sig
		if st.streak >= a.detectLoops {
			return fmt.Errorf("%w: %d identical steps in a row", ErrLoopDetected, st.streak)
		}
	}
	return nil
}

// stepSignature hashes a step's tool calls — name and raw argument
// bytes — sorted, so a permuted batch is the same request ("the same
// set" in DetectLoops' spec is literal). The args are hashed as the
// model emitted them, without canonicalisation: a model that reformats
// its JSON has changed something, and the cost of a false negative is
// one more step. Results are deliberately absent (ADR 0002).
func stepSignature(calls []ToolCallPart) uint64 {
	pairs := make([][2]string, len(calls))
	for i, c := range calls {
		pairs[i] = [2]string{c.Name, string(c.Args)}
	}
	slices.SortFunc(pairs, func(a, b [2]string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return strings.Compare(a[1], b[1])
	})
	h := fnv.New64a()
	for _, p := range pairs {
		h.Write([]byte(p[0]))
		h.Write([]byte{0})
		h.Write([]byte(p[1]))
		h.Write([]byte{0})
	}
	return h.Sum64()
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
//
// rec is the run's recorder, told each call's truncation before its
// ToolFinish is emitted (the tool_finish record's
// weft.tool.truncated_bytes); nil records nothing.
func (a *Agent) execTools(ctx context.Context, runID string, step int, tools []*ToolDef, calls []ToolCallPart, seq *atomic.Int64, emit func(Event), approved bool, parallelism int, parkSet *parkRule, rec *recorder, retryBase map[string]int) (results []ToolResultPart, pending []ToolCallPart, subagents map[string]Usage, retried []string) {
	outcomes := make([]ToolResultPart, len(calls))
	parked := make([]bool, len(calls))
	sem := make(chan struct{}, parallelism)
	subs := map[string]Usage{}
	// A counted RETRY's span waits for its mark (below): retriedAt[i]
	// marks call i's outcome a RETRY, endRetry[i] ends its span.
	retriedAt := make([]bool, len(calls))
	endRetry := make([]func(retryMark), len(calls))

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
		// start events are in call order by construction. The Seq is
		// captured for the tool span's weft.tool.seq attribute; the
		// goroutine launch happens after this emit, so the read is safe.
		var startSeq int64
		ordered(func(s int64) Event {
			startSeq = s
			return ToolStart{RunID: runID, Seq: s, CallID: call.ID, Name: call.Name, Args: cloneRaw(call.Args)}
		})
		c := Call{RunID: runID, Step: step, CallID: call.ID, Name: call.Name, Approved: approved}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			n := nestFor(call)
			callCtx := withNest(withCall(ctx, c), n)
			// The tool call's own reporting: one execute_tool span, on the
			// handler's context — a span the handler starts is its child,
			// and a child run's invoke_agent hangs under it — ended with
			// the call's outcome, before ToolFinish is emitted so no span
			// operation runs under emitMu (ADR 0016).
			callCtx, endTool := a.obs.tool(callCtx, c, startSeq)
			var retry bool
			var callErr error
			var cut int
			outcomes[i], parked[i], retry, cut, callErr = a.callTool(callCtx, call, def, parkSet)
			retriedAt[i] = retry
			if retry && retryBase != nil {
				// Its mark depends on the whole batch: the span ends
				// after wg.Wait, stamped now. Attribution only — the
				// ToolFinish below is not delayed.
				at := time.Now()
				res, c := outcomes[i], cut
				endRetry[i] = func(m retryMark) { endTool(res, false, callErr, c, m, at) }
			} else {
				endTool(outcomes[i], parked[i], callErr, cut, retryMark{}, time.Time{})
			}
			if parked[i] {
				return
			}
			if cut > 0 {
				rec.noteTruncated(call.ID, cut)
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
	// The retry marks (weft.tool.retry), assigned once the batch is
	// known, in call order — the guard's count exactly: the steps
	// before plus this batch's RETRYs of the tool, none when the same
	// batch has a success of that tool (the guard resets it).
	succeeded := map[string]bool{}
	for i, call := range calls {
		if !parked[i] && !outcomes[i].IsError {
			succeeded[call.Name] = true
		}
	}
	counted := map[string]int{}
	for i, call := range calls {
		if !retriedAt[i] {
			continue
		}
		retried = append(retried, call.Name)
		if endRetry[i] == nil {
			continue
		}
		var mark retryMark
		if !succeeded[call.Name] {
			counted[call.Name]++
			mark = retryMark{n: retryBase[call.Name] + counted[call.Name], budget: a.maxModelRetries}
		}
		endRetry[i](mark)
	}
	results = make([]ToolResultPart, 0, len(calls))
	for i, call := range calls {
		if parked[i] {
			pending = append(pending, call)
			continue
		}
		results = append(results, outcomes[i])
	}
	return results, pending, subs, retried
}

// resolvePending runs the calls an earlier run left pending, under this
// run's decisions: approved calls execute through the ordinary chain
// with Call.Approved set; denied and undecided calls become error
// results the model sees. Results come back in call order; a call the
// chain parks again is returned as pending. The step index of the
// resumed calls is reported as 0: the original index is not recoverable
// from the transcript, and there is no StepRecord for them (ADR 0007).
// Audit lines should key on the CallID, not the step.
func (a *Agent) resolvePending(ctx context.Context, cfg runConfig, calls []ToolCallPart, seq *atomic.Int64, emit func(Event), fetchTools func() ([]*ToolDef, error), parkSet *parkRule, rec *recorder) ([]ToolResultPart, []ToolCallPart, map[string]Usage, error) {
	// Resumed calls run before any step exists, so they fetch their own
	// snapshot — a separate consultation, like CallTool's, through the
	// run's (possibly narrowed) fetch.
	tools, err := fetchTools()
	if err != nil {
		return nil, nil, nil, err
	}
	var approved []ToolCallPart
	for _, c := range calls {
		if d, ok := cfg.decisions[c.ID]; ok && d.approved {
			approved = append(approved, c)
		}
	}
	ran, pending, sub, _ := a.execTools(ctx, cfg.id, 0, tools, approved, seq, emit, true, cfg.effectiveParallelism(a.parallelism), parkSet, rec, nil)
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
		// A resolved call never runs: the decision's content is the
		// result, verbatim, capped as any result would be (the cap is
		// a transcript rule, not an execution rule) — a result in the
		// tool message, no span, no events, Call.Approved never set
		// (ADR 0007's 2026-09-22 amendment); weft.resume.calls names
		// it resolved.
		if d, ok := cfg.decisions[c.ID]; ok && d.resolved {
			resultCap := a.resultCap
			if def, _ := findTool(tools, c.Name); def != nil && def.capSet {
				resultCap = def.resultCap
			}
			results = append(results, ToolResultPart{
				CallID:  c.ID,
				Name:    c.Name,
				IsError: d.isError,
				Content: capResult(d.content, resultCap),
			})
			continue
		}
		if r, ok := byID[c.ID]; ok {
			results = append(results, r)
			continue
		}
		reason, kind := "no decision", deniedKindNoDecision
		if d, ok := cfg.decisions[c.ID]; ok {
			reason, kind = d.reason, deniedKindDenied
		}
		results = append(results, ToolResultPart{
			CallID:  c.ID,
			Name:    c.Name,
			IsError: true,
			Content: deniedResult(reason),
		})
		// A denied call never ran: no event (ADR 0007), a zero-length
		// execute_tool span naming its kind (UX-E-17), and the run's
		// root counts it (weft.run.denied).
		a.obs.deniedTool(ctx, cfg.id, c, kind)
		rec.noteDenied()
	}
	return results, pending, sub, nil
}

// resumeCallsAttr renders weft.resume.calls: each pending call of a
// resume, in call order, as <call id>=<kind> joined by commas — kind
// approved (Approve), denied (Deny), resolved (Resolve, ResolveError)
// or no_decision (left undecided: rule 12's "DENIED: no decision").
// "" when the run resumes no pending call. Ids and enums only, never a
// reason or a resolved payload (ADR 0016).
func resumeCallsAttr(calls []ToolCallPart, decisions map[string]decision) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range calls {
		if i > 0 {
			b.WriteByte(',')
		}
		kind := deniedKindNoDecision
		if d, ok := decisions[c.ID]; ok {
			switch {
			case d.approved:
				kind = "approved"
			case d.resolved:
				kind = "resolved"
			default:
				kind = deniedKindDenied
			}
		}
		b.WriteString(c.ID)
		b.WriteByte('=')
		b.WriteString(kind)
	}
	return b.String()
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
// joined returns the completed tool message either way — created, or
// rebuilt over the partial one the earlier run left (ADR 0024 S1.3
// widened the report to the rebuild): the caller hands it to the
// transcript observers and the messages record. nil when no result was
// attached.
func attachResults(msgs []Message, calls []ToolCallPart, results []ToolResultPart) (out []Message, joined []Message) {
	if len(results) == 0 {
		return msgs, nil
	}
	i := lastAssistantWithCalls(msgs)
	if i < 0 {
		return msgs, nil
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
		return out, []Message{out[i+1]}
	}
	created := Message{Role: RoleTool, Content: parts}
	return slices.Insert(slices.Clone(msgs), i+1, created), []Message{created}
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
// StrictInput is, at both levels, as in the loop. No span or log line
// is produced: manual dispatchers own their context and their own
// reporting (ADR 0016). Outside a run it therefore leaves no trace in
// the record at all — no run, no event, no span — so Studio has
// nothing to show for it. Called from a tool handler, the dispatched call
// is its own: CallFromContext in its handler reports its id and name
// (the run and step stay the dispatching call's), and an approval the
// dispatching call ran under does not carry over — a RequireApproval
// tool still returns ErrApprovalRequired.
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
	// Dispatched from inside a tool handler, ctx carries that handler's
	// Call — possibly approved. The dispatched call is a call of its
	// own: never approved (an approval names one call, not what its
	// handler goes on to do), and its handler sees its own id and name,
	// in the same run and step. Outside the loop there is no Call, and
	// none is invented.
	if outer, ok := CallFromContext(ctx); ok {
		ctx = withCall(ctx, Call{RunID: outer.RunID, Step: outer.Step, CallID: call.ID, Name: call.Name})
	}
	return a.chain(def, strict, nil, nil)(ctx, call)
}

// chain builds the tool-call chain for one call: agent middleware
// (outermost, first registered first) around the tool's middleware
// around the base caller, which resolves the tool, enforces
// RequireApproval, decodes, and runs the handler. def is nil for an
// unknown tool — the chain still runs, so Allow/Audit see the attempt,
// and the base returns the NO_SUCH_TOOL error.
//
// mark, when not nil, notes a panic out of the handler itself as it
// unwinds through the base (handlerMark): the containment then tells a
// handler's panic from one the middleware around it raised (the
// observer's weft.panic.layer; nothing else reads it).
func (a *Agent) chain(def *ToolDef, strict bool, parkSet *parkRule, mark *handlerMark) ToolCaller {
	base := func(ctx context.Context, call ToolCallPart) (string, error) {
		if def == nil {
			return "", noSuchTool(call.Name)
		}
		// The approval boundary (ADR 0007): the tool's own RequireApproval,
		// or the run's park rule naming it (ParkOn, ParkAllExcept). Same
		// boundary, same resume path — the rule is the policy applied per
		// run (ADR 0024 D7).
		if def.approval || parkSet.parks(def.Name, a.hasOutput && def.Name == outputToolName) {
			if c, _ := CallFromContext(ctx); !c.Approved {
				return "", fmt.Errorf("%w: tool %q", ErrApprovalRequired, call.Name)
			}
		}
		if mark == nil {
			return def.invoke(ctx, call.Args, strict)
		}
		// Every return path leaves the mark right: a normal return
		// notes nothing; a panic is noted with its value and goes on
		// unwinding unchanged (the same value re-raised), so a
		// middleware that recovers it sees what the handler threw.
		defer func() {
			if p := recover(); p != nil {
				mark.note(p)
				panic(p)
			}
		}()
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
// pending instead. The chain's error is returned beside the result —
// folded into the result's text for the model, but typed for the
// observer's error.type (ADR 0016) — and is nil on success and on a
// parked call's pending report.
//
// cut is the bytes the result cap removed from the content (0 when it
// cut nothing): the observer's weft.tool.truncated_bytes.
func (a *Agent) callTool(ctx context.Context, call ToolCallPart, def *ToolDef, parkSet *parkRule) (res ToolResultPart, pending, retry bool, cut int, err error) {
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
	defer func() { res.Content, cut = capResultN(res.Content, resultCap) }()

	// mark belongs to this call's chain alone: the goroutine that runs
	// the chain writes it and the same goroutine's recover reads it.
	mark := new(handlerMark)
	fn := a.chain(def, strict, parkSet, mark)
	var out string
	if timeout <= 0 {
		out, err = invokeContained(ctx, fn, call, mark)
	} else {
		out, err = invokeWithTimeout(ctx, fn, call, timeout, mark)
	}
	if err != nil {
		if errors.Is(err, ErrApprovalRequired) {
			return res, true, false, 0, nil
		}
		res.IsError = true
		res.Content = err.Error()
		// Counted while the error is still an error, through errors.As,
		// so a middleware-produced RETRY counts exactly like a
		// handler's (the chain's outcome is what the loop sees).
		var te *ToolError
		if errors.As(err, &te) && te.Code == CodeRetry {
			return res, false, true, 0, err
		}
		return res, false, false, 0, err
	}
	res.Content = out
	return res, false, false, 0, nil
}

// toolTimeoutError is a per-call timeout, typed so the observer can
// classify it as error.type "timeout" without matching text; Error
// preserves the pinned model-visible string (ADR 0002).
type toolTimeoutError struct {
	name string
	d    time.Duration
}

func (e *toolTimeoutError) Error() string {
	return fmt.Sprintf("tool %q timed out after %s", e.name, e.d)
}

// toolPanicError is a contained tool panic, typed so the observer
// classifies it as error.type "panic" with the layer it came from
// (weft.panic.layer: "handler" or "middleware") without matching text;
// Error preserves the pinned model-visible string (ADR 0002). It wraps
// nothing: the panic value is reported, not unwrapped, exactly as the
// untyped error before it.
type toolPanicError struct {
	name  string
	value any
	layer string
}

func (e *toolPanicError) Error() string {
	return fmt.Sprintf("tool %q panicked: %v", e.name, e.value)
}

// The weft.panic.layer values.
const (
	panicLayerHandler    = "handler"
	panicLayerMiddleware = "middleware"
)

// invokeContained runs the chain, turning a panic — in the handler or
// in middleware — into an error. Containment sits outside the chain by
// design: no middleware can turn a panic into a run failure. mark
// is the chain's handler mark (chain): set when the panic unwound
// through the handler; nil reads as middleware.
func invokeContained(ctx context.Context, fn ToolCaller, call ToolCallPart, mark *handlerMark) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			layer := panicLayerMiddleware
			if mark.from(p) {
				layer = panicLayerHandler
			}
			err = &toolPanicError{name: call.Name, value: p, layer: layer}
		}
	}()
	return fn(ctx, call)
}

// handlerMark is one call's record of a panic out of its handler
// (chain's base notes it while unwinding). invokeContained reads the
// layer from it: the panic it recovered is the handler's when the
// handler panicked with that same value — still unwinding, or
// recovered and re-raised by a middleware — and the middleware's
// otherwise, including a middleware that recovered the handler's panic
// and later panicked with a value of its own.
type handlerMark struct {
	panicked bool
	value    any
}

func (m *handlerMark) note(p any) { m.panicked, m.value = true, p }

// from reports whether the recovered value p is the handler's panic.
// A value of an uncomparable type (a slice, a map) cannot be compared:
// it reads as the handler's when the handler panicked with one of the
// same type.
func (m *handlerMark) from(p any) bool {
	if m == nil || !m.panicked {
		return false
	}
	tp, tv := reflect.TypeOf(p), reflect.TypeOf(m.value)
	if tp != tv {
		return false
	}
	if tp != nil && !tp.Comparable() {
		return true
	}
	return samePanicValue(p, m.value)
}

// samePanicValue is p == q, true when the comparison itself panics (a
// comparable struct holding an uncomparable value): same type, so the
// handler's by the rule above.
func samePanicValue(p, q any) (same bool) {
	defer func() {
		if recover() != nil {
			same = true
		}
	}()
	return p == q
}

// invokeWithTimeout runs the tool under a per-call deadline. The handler
// sees the deadline on its ctx; if it has not returned when the deadline
// passes, the call is recorded as timed out and the handler's goroutine
// is abandoned — a hung tool must not hang the run. A cancellation of
// the run's own ctx is reported as that cancellation, not as a timeout.
func invokeWithTimeout(ctx context.Context, fn ToolCaller, call ToolCallPart, timeout time.Duration, mark *handlerMark) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := invokeContained(tctx, fn, call, mark)
		done <- outcome{out, err}
	}()
	timedOut := func() error {
		return &toolTimeoutError{name: call.Name, d: timeout}
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
	out, _ := capResultN(s, resultCap)
	return out
}

// capResultN is capResult reporting the bytes it removed (0 when it cut
// nothing) — the number its marker names, weft.tool.truncated_bytes.
func capResultN(s string, resultCap int) (string, int) {
	if resultCap <= 0 || len(s) <= resultCap {
		return s, 0
	}
	cut := resultCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…[truncated %d bytes]", len(s)-cut), len(s) - cut
}
