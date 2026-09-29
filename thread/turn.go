package thread

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"sync"
	"time"

	"github.com/weftgo/weft"
)

// Policy is what a Send does when the session is already running a
// turn — the busy policy (ADR 0011 §4). Queue is the default.
type Policy int

const (
	// Queue holds the follow-up and runs it when the current turn
	// ends, in acceptance order: the send is accepted, its Turn is
	// returned at once, and its prompt becomes durable when its turn
	// starts. The default.
	Queue Policy = iota
	// Reject fails the send with ErrBusy: one run per session at a
	// time, and a busy session says so instead of holding work.
	Reject
	// Steer delivers the send into the running turn instead of
	// waiting for it (ADR 0019): the message is accepted at once —
	// its queued receipt entry durable, flushed — and handed to the
	// run's next steering drain point: after the step's tool batch,
	// every call paired with its result, or at what would have been
	// the final step, which the delivery redirects into one more
	// step. A steer that meets an intended end (StopWhen) or an open
	// approval boundary is never drained: it becomes a deferred
	// follow-up that runs as the next turn, the receipt recording
	// the fate. The Send's Turn is the receipt: it finishes — nil
	// result, nil error — when the steer reaches its final state,
	// and its Next is the follow-up turn a deferred steer became.
	Steer
)

type busyPolicyOption Policy

func (o busyPolicyOption) applySession(c *sessionConfig) { c.policy = Policy(o) }

// BusyPolicy returns the SessionOption setting what Send does when the
// session is busy: Queue (the default), Reject, or Steer. A single
// Send overrides it with As.
func BusyPolicy(p Policy) SessionOption { return busyPolicyOption(p) }

// SendOption configures one Send. Step 1.7 carries one: RunOptions,
// the extra run options for this turn's run.
type SendOption interface {
	applySend(*sendConfig)
}

// sendConfig is one Send's resolved configuration.
type sendConfig struct {
	runOpts []weft.RunOption
	// policy overrides the session's busy policy for this one Send
	// when policySet (As).
	policy    Policy
	policySet bool
}

func resolveSend(opts ...SendOption) sendConfig {
	var cfg sendConfig
	for _, o := range opts {
		if o != nil {
			o.applySend(&cfg)
		}
	}
	return cfg
}

type asOption Policy

func (o asOption) applySend(c *sendConfig) { c.policy, c.policySet = Policy(o), true }

// As returns the SendOption overriding the session's busy policy for
// this one Send (ADR 0019): As(Steer) steers a message into the
// running turn on a Queue session; As(Queue) holds a message for the
// next turn on a Steer session. The policy in force is the one Send
// ran under, captured with the turn's other settings.
func As(p Policy) SendOption { return asOption(p) }

type runOptionsOption struct{ opts []weft.RunOption }

func (o runOptionsOption) applySend(c *sendConfig) { c.runOpts = append(c.runOpts, o.opts...) }

// RunOptions returns the SendOption carrying extra weft.RunOptions
// into this turn's run — budgets, taps, thinking, an approval resume
// (weft.Approve, weft.Deny). The transcript is Send's to build and the
// run id the session's to mint: an option that would set the run's
// messages (weft.Messages, weft.Prompt) or its id (weft.RunID) is
// rejected when the Send runs, because either would quietly detach the
// run from the session's tree (ADR 0011 §4).
func RunOptions(opts ...weft.RunOption) SendOption { return runOptionsOption{opts} }

// The dynamic types of the run options Send must refuse, taken from
// the core's own constructors. RunOption is sealed (ADR 0004) — only
// the core constructs values — so type identity names the option
// without spelling an unexported type name in a string, and the
// constructor stays the source of truth through any internal rename;
// the tests pin the rejection with the constructors themselves.
var (
	messagesOptionType = reflect.TypeOf(weft.Messages())
	promptOptionType   = reflect.TypeOf(weft.Prompt(""))
	runIDOptionType    = reflect.TypeOf(weft.RunID(""))
	steeringOptionType = reflect.TypeOf(weft.Steering(nil))
)

// rejectTranscriptOptions fails a Send whose run options would set the
// run's transcript or run id: the session would otherwise persist a
// transcript that diverges from the one the model saw.
func rejectTranscriptOptions(opts []weft.RunOption) error {
	for _, o := range opts {
		if o == nil {
			continue
		}
		switch reflect.TypeOf(o) {
		case messagesOptionType:
			return fmt.Errorf("thread: weft.Messages in RunOptions: the transcript is Send's to build from the session's tree (ADR 0011 §4)")
		case promptOptionType:
			return fmt.Errorf("thread: weft.Prompt in RunOptions: Send's msg is the prompt (ADR 0011 §4)")
		case runIDOptionType:
			return fmt.Errorf("thread: weft.RunID in RunOptions: the session mints <session>-t<n> run ids (ADR 0011 §4)")
		case steeringOptionType:
			return fmt.Errorf("thread: weft.Steering in RunOptions: the session owns the steer queue (ADR 0019; Send under the Steer policy)")
		}
	}
	return nil
}

// pendingSend is an accepted Send waiting for its turn: the context it
// was sent with, its message, its extra run options, and the Turn the
// caller already holds.
type pendingSend struct {
	ctx  context.Context
	msg  weft.Message
	opts []weft.RunOption
	turn *Turn
}

// Send appends the prompt and runs the session's agent on the leaf's
// context, returning the turn's receipt at once. The prompt entry is
// appended and flushed before the run starts (ADR 0011 §4: accepted
// input is durable input), the run carries the session's context plus
// msg and the run id <session>-t<n>, and at the end — success, failure
// or cancellation — the run's new messages and a turn entry are
// appended in one atomic batch under a context.WithoutCancel window,
// so a caller who walks away still leaves a complete session behind.
//
// Settings are captured at turn start: the agent, the extra run
// options, and the busy policy in force when Send ran. On a busy
// session the policy decides — Queue (the default) holds the
// follow-up, in order, and Reject fails with ErrBusy.
//
// A Send while approval requests are pending is queued, not run (ADR
// 0021 §5): the parked boundary must resolve first — Decide (which
// resumes on its own when AutoResume is on) or Resume — and the queued
// follow-up then runs with the boundary's transcript completed. Under
// Reject a pending boundary reads as busy.
func (s *Session) Send(ctx context.Context, msg weft.Message, opts ...SendOption) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := resolveSend(opts...)
	if err := rejectTranscriptOptions(cfg.runOpts); err != nil {
		return nil, err
	}
	extra := append([]weft.RunOption(nil), cfg.runOpts...) // captured at turn start
	policy := s.cfg.policy
	if cfg.policySet {
		policy = cfg.policy // captured at Send, like the run options
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.boundaryLocked() {
		// A running turn holds the session, and so does an open approval
		// boundary: the follow-up is queued and runs when the boundary
		// resolves — Decide or Resume resume it, the runner drains the
		// queue after (ADR 0021 §5). The queue holds acceptance order
		// across the boundary; the branch that cleared one restarts the
		// runner at the head on the next Send.
		if policy == Reject {
			return nil, fmt.Errorf("%w: session %s", ErrBusy, s.header.ID)
		}
		if policy == Steer {
			// The steering path (ADR 0019): durable acceptance now,
			// delivery at the running turn's drain point — or, when only
			// a boundary holds the session, a deferral at once: a steer
			// never resolves a parked call.
			return s.steerSendLocked(ctx, msg)
		}
		t := s.newTurnLocked()
		s.queue = append(s.queue, pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t})
		if !s.running && s.cfg.autoResume && len(s.pendingLocked()) == 0 {
			// A boundary with every call decided and no runner alive —
			// a reopen inside the crash window between Decide's append
			// and the resume it armed — must not wedge the queue:
			// AutoResume's contract says the session resumes on its
			// own, so this Send arms it (under a window that cannot be
			// canceled by the sender walking away), and the follow-up
			// runs behind the resume.
			if _, err := s.armResumeLocked(context.WithoutCancel(ctx)); err != nil {
				// Arming cannot fail today (it mints and spawns); if it
				// ever grows a failure, the queued send still runs once
				// the caller resolves the boundary by hand.
				s.agent.Logger().Error("thread: auto-resume arm failed",
					"session", s.header.ID, "err", err)
			}
		}
		return t, nil
	}
	if len(s.queue) > 0 {
		// No runner and no boundary, but earlier sends are still queued
		// (a Branch cleared the boundary that held them): acceptance
		// order rules — this send queues behind them, the runner starts
		// at the head.
		t := s.newTurnLocked()
		s.queue = append(s.queue, pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t})
		s.kickRunnerLocked()
		return t, nil
	}
	s.running = true
	t := s.newTurnLocked()
	s.inFlight = t
	// The prompt is durable before anything else happens: appended
	// under the caller's context and flushed, so a Send that returns
	// without error has already survived a crash.
	if err := s.appendPromptLocked(ctx, t.id, msg); err != nil {
		s.running, s.inFlight = false, nil
		return nil, err
	}
	t.setPromptDone()
	go s.execute(workItem{ps: pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t}})
	return t, nil
}

// newTurnLocked mints the receipt — the prompt entry's id — and the
// run id the turn will run under. Run ids count from the turn counter,
// which recovers from the turn entries ever written, so ids stay
// unique across a reopen; the counter moves at mint time, so even a
// turn whose end never lands (a crash) never shares its id with the
// next one.
func (s *Session) newTurnLocked() *Turn {
	s.turnSeq++
	t := &Turn{
		id:    s.mintIDLocked(),
		runID: fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq),
	}
	t.cond = sync.NewCond(&t.mu)
	return t
}

// mintIDLocked mints an entry id through the session's ids option.
func (s *Session) mintIDLocked() string {
	if s.cfg.ids != nil {
		return s.cfg.ids()
	}
	return NewEntryID()
}

// appendPromptLocked writes the prompt entry and flushes it. A flush
// failure fails the Send: the entry is written, but its durability is
// the promise, and nothing else has started.
func (s *Session) appendPromptLocked(ctx context.Context, id string, msg weft.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(id) {
		return fmt.Errorf("thread: invalid entry id %q", id)
	}
	if _, dup := s.byID[id]; dup {
		return fmt.Errorf("thread: entry id %q already held by session %s", id, s.header.ID)
	}
	e := MessageEntry{ID: id, ParentID: s.leaf, Created: time.Now().UTC(), Message: cloneMessage(msg)}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	if f, ok := s.st.(Flusher); ok {
		if err := f.Flush(ctx, s.header.ID); err != nil {
			return fmt.Errorf("thread: prompt flush: %w", err)
		}
	}
	return nil
}

// workItem is one unit of runner work: an accepted send, or a resume
// run over a parked approval boundary (whose ps carries only the
// context and the Turn — a resume has no prompt of its own).
type workItem struct {
	ps     pendingSend
	resume bool
}

// kickRunnerLocked starts the runner at the queue's head when work is
// waiting and no runner is alive: Send's queued path and the boundary
// clears (a Branch off the parked tail) both land here. Callers hold
// s.mu.
func (s *Session) kickRunnerLocked() {
	if s.running || len(s.queue) == 0 || s.boundaryLocked() {
		return
	}
	s.running = true
	first := s.queue[0]
	s.queue = s.queue[1:]
	s.inFlight = first.turn
	go s.execute(workItem{ps: first})
}

// execute runs runner work one item at a time until nothing is
// runnable: the session's single runner. Each item runs to completion
// — prompt, run, end-of-turn persistence — before the next starts, in
// acceptance order; a resume run for a completed boundary takes the
// slot before any queued send, because the queued sends wait for the
// boundary by contract (ADR 0021 §5). The runner exits when the queue
// is empty or an open boundary holds it — Decide, Resume or a later
// Send restart it.
func (s *Session) execute(first workItem) {
	cur := first
	for {
		s.runOneContained(cur)
		// A resume that failed without resolving its boundary — its
		// context died, its persistence failed — is not retried here:
		// an unbounded auto-retry loop has no backoff and no stop, so
		// the runner leaves the boundary open for the caller's next
		// Send or Resume (both arm it again).
		retry := !cur.resume || !cur.ps.turn.failed()
		s.mu.Lock()
		s.inFlight = nil // the item boundary: a Branch here is already safe
		if s.resumeWork != nil {
			rw := *s.resumeWork
			s.resumeWork = nil
			s.inFlight = rw.turn
			s.mu.Unlock()
			cur = workItem{ps: pendingSend{ctx: rw.ctx, turn: rw.turn}, resume: true}
			continue
		}
		if retry && s.cfg.autoResume && s.boundaryLocked() && len(s.pendingLocked()) == 0 {
			// The chain decided every call without parking (a grant or
			// the Approver), or the last decision of a parked boundary
			// landed while this runner worked: resume at once, under
			// the settings captured when the turn parked. The minted
			// resume is registered like any arming, so a concurrent
			// Resume joins it rather than arming a second one.
			t := s.mintResumeLocked()
			ctx := s.await.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			s.mu.Unlock()
			cur = workItem{ps: pendingSend{ctx: ctx, turn: t}, resume: true}
			continue
		}
		if len(s.queue) > 0 && !s.boundaryLocked() {
			cur = workItem{ps: s.queue[0]}
			s.queue = s.queue[1:]
			s.inFlight = cur.ps.turn
			s.mu.Unlock()
			continue
		}
		s.running = false
		s.inFlight = nil
		s.mu.Unlock()
		return
	}
}

// runOneContained keeps a panic in the session's own turn machinery
// from wedging the runner slot: the turn ends with the panic as its
// error, the agent's logger says so, and the queue moves on. Tool and
// model panics never reach here — the core contains them as errors
// (invokeContained, the stream consume) — this guards the session
// layer's own code and the caller's ids function.
func (s *Session) runOneContained(item workItem) {
	defer func() {
		if p := recover(); p != nil {
			// The turn may already be decided (the panic came from
			// after it); finish refuses to overwrite it. A resume's
			// arming retires first, so its caller can arm again.
			s.mu.Lock()
			if item.resume {
				s.settleResumeLocked(item.ps.turn)
			}
			s.retireInFlightLocked(item.ps.turn)
			s.mu.Unlock()
			item.ps.turn.finish(nil, fmt.Errorf("thread: turn panicked: %v", p))
			s.agent.Logger().Error("thread: turn panicked",
				"session", s.header.ID, "run", item.ps.turn.runID, "panic", p)
		}
	}()
	if item.resume {
		s.runResume(item.ps.ctx, item.ps.turn)
		return
	}
	s.runOne(item.ps)
}

// retireInFlightLocked drops the in-flight mark when it names t,
// before the turn is decided — every path that finishes a turn calls
// it under mu first, so a caller whose Wait returns may Branch at
// once and never read ErrBusy for a turn that already landed.
// Callers hold s.mu.
func (s *Session) retireInFlightLocked(t *Turn) {
	if s.inFlight == t {
		s.inFlight = nil
	}
}

// runOne runs one accepted send. The idle path wrote the prompt in
// Send; a dequeued send writes it here under a WithoutCancel window,
// so an accepted send never loses its prompt to a context that died
// while it waited. The session goroutine consumes the run's stream
// itself and forwards every event to the Turn, so the turn is fully
// observed however the caller reads it — or does not.
func (s *Session) runOne(ps pendingSend) {
	t := ps.turn
	persist := context.WithoutCancel(ps.ctx)
	s.mu.Lock()
	if !t.promptWritten() {
		if err := s.appendPromptLocked(persist, t.id, ps.msg); err != nil {
			s.retireInFlightLocked(t)
			s.mu.Unlock()
			t.finish(nil, fmt.Errorf("thread: prompt append: %w", err))
			return
		}
		t.setPromptDone()
	}
	s.mu.Unlock()

	if err := ps.ctx.Err(); err != nil {
		// The caller walked away before the turn started: the prompt is
		// kept, the turn is recorded as canceled, and the queue moves on.
		errOut := fmt.Errorf("thread: turn canceled before it started: %w", err)
		s.recordTurnEnd(persist, t, nil, errOut, 0, nil)
		s.mu.Lock()
		s.retireInFlightLocked(t)
		s.mu.Unlock()
		t.finish(nil, errOut)
		return
	}

	// The trigger's first site (ADR 0020 §2): before the run, with the
	// prompt already on the path so the estimate covers what the run
	// is about to be fed. It never fires over an open approval
	// boundary — the dangling tail must stay raw for its decisions.
	s.maybeAutoCompact(ps.ctx)

	s.runTurn(persist, ps.ctx, t, ps.opts)
}

// runResume runs one resume over a parked approval boundary (ADR 0021
// §1): no prompt of its own — the receipt is the turn entry it writes,
// reusing the id minted when the resume was armed — and the input is
// the boundary's dangling transcript, carried raw so the recorded
// decisions can resolve their calls. The run options the parked send
// captured ride along (across a restart they are gone: run options are
// not entries), with the recorded decisions applied after them, so a
// durable decision always beats a captured option. No compaction runs
// before a resume: there is no new prompt to cover, and the boundary
// must stay raw.
//
// The boundary must still be open when the resume runs: between arming
// and this run the caller may have branched away from the parked tail
// (the documented way out of an unwanted boundary), and resuming then
// would be a model call over nothing — a ghost turn nobody asked for.
// The turn instead ends with ErrNotPending, the same class Resume
// itself raises, and the arming retires.
func (s *Session) runResume(ctx context.Context, t *Turn) {
	persist := context.WithoutCancel(ctx)
	if err := ctx.Err(); err != nil {
		errOut := fmt.Errorf("thread: resume canceled before it started: %w", err)
		s.recordTurnEnd(persist, t, nil, errOut, 0, nil)
		s.mu.Lock()
		s.settleResumeLocked(t)
		s.retireInFlightLocked(t)
		s.mu.Unlock()
		t.finish(nil, errOut)
		return
	}
	s.mu.Lock()
	if !s.boundaryLocked() {
		errOut := fmt.Errorf("%w: the boundary this resume was armed for is gone (branched away?)", ErrNotPending)
		s.settleResumeLocked(t)
		s.mu.Unlock()
		t.finish(nil, errOut)
		return
	}
	// The audit trail says a resume started before the run does: a
	// crash between the two leaves the boundary resumable, the audit
	// honest about the attempt.
	dangling := len(s.danglingCallsLocked())
	if err := s.appendLocked(persist, func(id, parent string, created time.Time) Entry {
		return ApprovalAuditEntry{
			ID: id, ParentID: parent, Created: created,
			Step: StepResume, Outcome: "started",
			Detail: fmt.Sprintf("%d call(s) to resolve", dangling), RunID: t.runID,
		}
	}); err != nil {
		s.settleResumeLocked(t)
		s.mu.Unlock()
		t.finish(nil, fmt.Errorf("thread: resume audit: %w", err))
		return
	}
	opts := append([]weft.RunOption(nil), s.await.opts...)
	s.mu.Unlock()
	s.runTurn(persist, ctx, t, opts)
}

// runTurn is the shared body of a send's and a resume's run: the raw
// context in, the stream drained and forwarded, the turn's end
// recorded and the boundary chain run when the run parks calls
// (ADR 0021 §2). callerOpts are the run options captured for this
// turn — the resume inherits the parked send's — and after them come
// the recorded decisions, which win.
func (s *Session) runTurn(persist, ctx context.Context, t *Turn, callerOpts []weft.RunOption) {
	// The input is the session's context — the walk already includes
	// the prompt entry appended for this turn, or the boundary's
	// dangling calls for a resume — carried raw: the loop repairs its
	// input itself, leaving a decision's pending calls unresolved so it
	// can resolve them (the approval resume).
	input := s.rawContext()
	s.mu.Lock()
	decisions := s.danglingDecisionsLocked(t.resume)
	s.mu.Unlock()
	runOpts := append([]weft.RunOption(nil), callerOpts...)
	runOpts = append(runOpts, decisions...)
	// The session's steering source rides every run (ADR 0019): an
	// empty queue drains nothing, and a Send under the Steer policy
	// can queue at any moment — including after this run started.
	runOpts = append(runOpts, weft.Steering(s.steerSource))
	runOpts = append(runOpts, weft.Messages(input...), weft.RunID(t.runID))
	run := s.agent.Stream(ctx, runOpts...)
	for ev, err := range run.Events() {
		if err != nil {
			t.setStreamErr(err)
			break
		}
		t.push(ev)
	}
	res, err := run.Wait()
	// The decision chain (ADR 0021 §2) runs before the turn's end is
	// persisted, so a call about to park is asked — grants, then a
	// bounded Approver — and its request entry lands in the same
	// atomic Append as the turn: no window where the turn is durable
	// and the request is not.
	var chain *chainResult
	if err == nil && res != nil && len(res.Pending) > 0 {
		chain = s.runChain(persist, t, callerOpts, res.Pending)
	}
	s.recordTurnEnd(persist, t, res, err, len(input), chain)
	// Steers still live when the run ended defer here (plan §6): the
	// run met a StopWhen end or parked approvals without draining
	// them, or they arrived after the last drain point. The follow-ups
	// join the send queue the runner drains next.
	s.mu.Lock()
	s.settleSteersLocked()
	s.mu.Unlock()
	// The trigger's second site: after the turn, with the new
	// measurement recorded. It runs before the turn is decided so a
	// Wait that returns leaves the session fully settled — turn,
	// ledger and any between-turn compaction. A panic here cannot
	// repaint the turn: finish is idempotent and the hooks contain
	// their own panics.
	s.maybeAutoCompact(persist)
	// The parked-request notifications go out before the turn is
	// decided, so a caller whose Wait returns has already seen every
	// OnRequest delivery this turn owes: durable first (the append
	// above), then delivered, then the turn completes. The same rule
	// links the chain-decided auto-resume: a caller whose Wait returns
	// can follow Turn.Next at once, so the resume is minted and linked
	// here — handed to the runner as resumeWork — not after the wake.
	if chain != nil {
		s.fireOnRequest(chain)
		s.mu.Lock()
		undecided := -1
		if len(chain.parkedCalls) == 0 {
			// The chain decided every call without parking. That only
			// completes the boundary when every dangling call holds an
			// effective decision, quorum included: one approval under
			// Quorum(n) — the Approver's, or a grant's single identity —
			// leaves it open for the next decision instead of arming a
			// resume that would deny the undecided calls as "no
			// decision" (the same gate the runner's own pickup and
			// Decide apply, ADR 0021 §5).
			undecided = len(s.pendingLocked())
			if undecided == 0 && s.cfg.autoResume {
				// The runner's own arming goes through the same
				// registration as Decide and Resume: the minted resume is
				// the boundary's one armed resume, so a Resume arriving
				// while it flies returns this turn instead of arming a
				// second one beside it (one boundary, one resume).
				rt := s.mintResumeLocked()
				s.resumeWork = &pendingResume{ctx: chain.awaitCtx, turn: rt}
			}
		}
		s.mu.Unlock()
		if undecided > 0 {
			// A chain-decided boundary that stayed open: the turn is
			// done, but Pending() still shows the calls awaiting their
			// quorum — the log line is the operator's signal; nothing
			// parked, so OnRequest owes no notification.
			s.agent.Logger().Debug("thread: chain decided without completing the boundary",
				"session", s.header.ID, "run", t.runID, "undecided", undecided)
		}
	}
	if t.resume {
		// Settled before the turn is decided, so a caller whose Wait
		// returns never sees a stale arming: a failed resume can retry,
		// a resolved boundary reads ErrNotPending.
		s.mu.Lock()
		s.settleResumeLocked(t)
		s.mu.Unlock()
	}
	// The in-flight mark retires before the turn is decided, the same
	// rule: a caller whose Wait returns may Branch at once, and the
	// runner's epilogue must not be able to answer ErrBusy for a turn
	// that already landed. (The runner's own boundary clear stays as
	// the catch-all.)
	s.mu.Lock()
	s.retireInFlightLocked(t)
	s.mu.Unlock()
	t.finish(res, err)
}

// recordTurnEnd appends the turn's new messages and its turn entry in
// one atomic batch, under a WithoutCancel window — the run is over,
// and its transcript must land whatever happened to the caller's
// context (ADR 0011 §4). On failure the partial transcript from
// RunError.Result is kept after weft.Repair; a cancellation is
// recorded as canceled; the calls a pending approval left unrun are
// recorded on the entry. inputLen is the run's input length — the
// messages beyond it are the turn's new ones. chain, when the turn
// parked calls, carries the decision chain's entries — requests,
// audits, chain decisions — into the same Append: the request and the
// turn are atomic, all or none (ADR 0021 §1). A resume turn's entry
// reuses the receipt id minted when the resume was armed (it has no
// prompt entry of its own). A persistence failure is logged through
// the agent's logger and the turn still completes: the session keeps
// its in-memory tree, and the storage says why.
func (s *Session) recordTurnEnd(ctx context.Context, t *Turn, res *weft.RunResult, err error, inputLen int, chain *chainResult) {
	var full []weft.Message
	if err != nil {
		var runErr *weft.RunError
		if errors.As(err, &runErr) && runErr.Result != nil {
			full = runErr.Result.Messages
			res = runErr.Result // the ledger fields (usage, steps, pending) live on the partial
		}
	} else if res != nil {
		full = res.Messages
	}

	// The estimated tail runs before the lock: the Estimator is a
	// caller's hook, consulted outside s.mu like every other — one
	// that calls back into the Session (Leaf, Pending) must not
	// deadlock the turn's own persistence.
	var tailEst int64
	if res != nil && len(res.Steps) > 0 {
		tailStart := inputLen
		for i := len(full) - 1; i >= inputLen; i-- {
			if full[i].Role == weft.RoleAssistant {
				tailStart = i
				break
			}
		}
		for _, m := range full[tailStart:] {
			tailEst += s.estimate(m)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []Entry
	parent := s.leaf
	if len(full) > inputLen {
		// A successful run's messages are already sound model input
		// (the loop repairs its input before appending its own), and a
		// pending call must stay unresolved in the tree so a later
		// decision can resolve it — so no repair here on success. A
		// failed run's partial is repaired on the way in, the ADR's
		// rule. Repairs land exactly at the input's tail (the tree
		// holds valid prefixes with at most a pending tail), so the
		// boundary slice stays aligned.
		newMsgs := full[inputLen:]
		if err != nil {
			newMsgs = weft.Repair(newMsgs)
		}
		for _, m := range newMsgs {
			id := s.mintIDLocked()
			entries = append(entries, MessageEntry{
				ID: id, ParentID: parent, Created: time.Now().UTC(), Message: cloneMessage(m),
			})
			parent = id
		}
	}
	teID := s.mintIDLocked()
	if t.resume {
		teID = t.id // a resume has no prompt entry; its receipt names its turn entry
	}
	te := TurnEntry{ID: teID, ParentID: parent, Created: time.Now().UTC(), RunID: t.runID}
	if res != nil {
		te.StopReason = res.StopReason
		te.Usage = res.Usage
		te.Steps = len(res.Steps)
		te.Pending = res.Pending
		if n := len(res.Steps); n > 0 {
			// The trigger's baseline: the final step's reported input,
			// plus the estimated tail no report covers — the final
			// step's own messages (everything from its assistant
			// message onward). Recorded as one number, so the live
			// session and a reopen read the same baseline from the
			// same mark, the turn entry (ADR 0020 §2: reported tokens
			// are the signal; only what the report cannot cover is
			// estimated).
			te.LastInput = res.Steps[n-1].Usage.InputTokens + tailEst
		}
	}
	if err != nil {
		te.Err = err.Error()
		if errors.Is(err, context.Canceled) {
			te.Canceled = true
		}
	}
	entries = append(entries, te)
	parent = te.ID
	// The delivered steers' receipts join the turn's batch (plan §6):
	// the messages themselves are already in it — the run's transcript
	// carries them beyond the input — so the receipt is what ties each
	// to its queued entry and names the run that drained it.
	for i := range s.handed {
		id := s.mintIDLocked()
		entries = append(entries, ReceiptEntry{ID: id, ParentID: parent,
			Created: time.Now().UTC(), Receipt: s.handed[i].receipt,
			Status: ReceiptDelivered, RunID: t.runID})
		parent = id
	}
	handed := s.handed
	s.handed = nil
	if chain != nil {
		// The chain's entries join the turn's batch: ids and parents
		// assigned here, in call order, after the turn entry (ADR 0021
		// §1–§2). Their kinds carry no prompt and no messages; they are
		// bookkeeping the model never sees.
		now := time.Now().UTC()
		for _, e := range chain.entries {
			e = fillApprovalEntry(e, s.mintIDLocked(), parent, now)
			entries = append(entries, e)
			if id := idOf(e); id != "" {
				parent = id
			}
		}
	}
	if appendErr := s.st.Append(ctx, s.header.ID, entries...); appendErr != nil {
		s.agent.Logger().Error("thread: turn end not persisted",
			"session", s.header.ID, "run", t.runID, "err", appendErr)
		// The steers were handed to the run and are in its transcript:
		// their receipts finish as delivered even though the batch (the
		// messages included) did not land — the in-memory tree keeps
		// them, and the storage error is on the record.
		for i := range handed {
			handed[i].turn.finish(nil, nil)
		}
		return
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	for i := range handed {
		// The delivery is durable: the steer's Turn — the receipt —
		// reaches its final state with the turn's landing.
		handed[i].turn.finish(nil, nil)
	}
	if chain != nil {
		// The boundary's captured settings: what the resume run
		// inherits from the send that parked (ADR 0021 §1), and the
		// turn a resume links back to through Turn.Next.
		s.await = awaitState{opts: chain.opts, ctx: chain.awaitCtx, runID: chain.runID, turn: t}
	}
	if te.LastInput > 0 {
		// The trigger's new measurement, marked at the turn entry:
		// everything the path holds after it is the next delta.
		s.lastInput = te.LastInput
		s.lastMeasureLeaf = te.ID
	}
	if f, ok := s.st.(Flusher); ok {
		if flushErr := f.Flush(ctx, s.header.ID); flushErr != nil {
			s.agent.Logger().Warn("thread: turn end flush failed",
				"session", s.header.ID, "run", t.runID, "err", flushErr)
		}
	}
}

// A Turn is the receipt and the handle of one accepted Send — or of a
// resume run over a parked approval boundary, which has no prompt of
// its own: its receipt names its turn entry.
type Turn struct {
	id    string
	runID string
	// resume marks a resume run: no prompt entry, and the turn entry
	// reuses the receipt id minted when the resume was armed.
	resume bool
	// next is the turn this turn's parked boundary resumed under — the
	// link a caller follows to watch an approval flow through. Written
	// once, under mu, by the session when it arms a resume.
	next *Turn

	mu         sync.Mutex
	cond       *sync.Cond
	events     []weft.Event
	streamErr  error
	result     *weft.RunResult
	waitErr    error
	done       bool
	promptDone bool
}

// ID returns the turn's receipt: the prompt entry's id, minted when
// the Send was accepted and durable before the run started — or, for
// a resume turn, its turn entry's id, reused from the mint at arming
// (a resume writes no prompt of its own). Looking a turn up in the
// tree starts here.
func (t *Turn) ID() string { return t.id }

// RunID returns the run's id, <session>-t<n> — the key the run store
// holds the run's records under, unique across a reopen.
func (t *Turn) RunID() string { return t.runID }

// Next returns the turn the session resumed this turn's parked
// approval boundary with, or nil while there is none — this turn did
// not park, its boundary resumed under a turn the caller already
// holds (Decide and Resume return it), or the boundary is still
// undecided. Following Next is how a caller watches an approval flow
// through: Send's turn parks; the auto-resume the decision chain or a
// completed decision set starts links here, and its Wait is the
// conversation's continuation. One boundary resumes at most once, so
// the link is set at most once.
func (t *Turn) Next() *Turn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.next
}

// setNext links the boundary's resume turn. The first link wins — a
// boundary resumes once — and a second attempt is dropped silently:
// the caller who armed the resume holds the same Turn.
func (t *Turn) setNext(n *Turn) {
	if n == nil {
		return
	}
	t.mu.Lock()
	if t.next == nil {
		t.next = n
	}
	t.mu.Unlock()
}

// Events yields the turn's run events in emission order, forwarded by
// the session as it observes the run. Unlike the core's Run, a Turn's
// Events may be ranged more than once — the session, not the consumer,
// drives the run, so abandoning the stream mid-way (breaking out of
// the range) never cancels it; the turn's end wakes a suspended range
// and ends it. A failed run delivers its error exactly once as the
// final element, the core's rule.
func (t *Turn) Events() iter.Seq2[weft.Event, error] {
	return func(yield func(weft.Event, error) bool) {
		i := 0
		for {
			t.mu.Lock()
			for !t.done && len(t.events) <= i {
				t.cond.Wait()
			}
			if i < len(t.events) {
				ev := t.events[i]
				i++
				t.mu.Unlock()
				if !yield(ev, nil) {
					return
				}
				continue
			}
			err, done := t.streamErr, t.done
			t.mu.Unlock()
			if !done {
				continue
			}
			if err != nil {
				yield(nil, err)
			}
			return
		}
	}
}

// Wait blocks until the turn ends and returns the run's result and
// error — *weft.RunError on failure, its Result carrying the partial
// transcript, exactly as the core's Wait reports it. The session
// drains the run itself, so Wait alone observes every event; it is
// safe to call after or while ranging over Events.
func (t *Turn) Wait() (*weft.RunResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.done {
		t.cond.Wait()
	}
	return t.result, t.waitErr
}

func (t *Turn) push(ev weft.Event) {
	t.mu.Lock()
	t.events = append(t.events, ev)
	t.mu.Unlock()
	t.cond.Broadcast()
}

func (t *Turn) setStreamErr(err error) {
	t.mu.Lock()
	t.streamErr = err
	t.mu.Unlock()
	t.cond.Broadcast()
}

func (t *Turn) setPromptDone() {
	t.mu.Lock()
	t.promptDone = true
	t.mu.Unlock()
}

func (t *Turn) promptWritten() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.promptDone
}

// finish ends the turn with the run's outcome — or, for a turn that
// never ran, its error. Idempotent: the first call wins, so a late
// containment path cannot repaint a decided turn.
func (t *Turn) finish(res *weft.RunResult, err error) {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.result, t.waitErr, t.done = res, err, true
	t.mu.Unlock()
	t.cond.Broadcast()
}

// failed reports whether the turn ended with an error — the runner's
// signal that a resume work item left its boundary unresolved, so the
// settled-boundary pickup does not auto-retry it (the caller's next
// Send or Resume arms the retry).
func (t *Turn) failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done && t.waitErr != nil
}

// cloneMessage copies a message's part slice, the one mutable field,
// so an entry's message never aliases a value the caller held.
func cloneMessage(m weft.Message) weft.Message {
	m.Content = append([]weft.Part(nil), m.Content...)
	return m
}
