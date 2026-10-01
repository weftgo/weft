package thread

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strconv"
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
	// Interrupt cancels the running turn and runs the message next
	// (plan §6): the in-flight run's context is canceled, the calls
	// its partial transcript left without a result carry the golden
	// interruption text, an approval boundary that holds the session
	// is denied with the interrupted reason, and the message runs as
	// the next turn. The interrupted turn's entries stay on the
	// tree — evidence, never deleted.
	Interrupt
	// Rollback is an Interrupt that also branches the leaf back to
	// before the interrupted turn's receipt entry: the follow-up runs
	// as though the interrupted turn never happened, while its
	// entries keep their own line of the tree (nothing lost).
	Rollback
)

type busyPolicyOption Policy

func (o busyPolicyOption) applySession(c *sessionConfig) { c.policy = Policy(o) }

// BusyPolicy returns the SessionOption setting what Send does when the
// session is busy: Queue (the default), Reject, Steer, Interrupt, or
// Rollback. A single Send overrides it with As.
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
	// A session whose Close has been called accepts no new work
	// (session.go: admitLocked, ErrClosed).
	if err := s.admitLocked(); err != nil {
		return nil, err
	}
	// A runner whose in-flight turn is already decided is on its way
	// out — the epilogue frees the slot moments after the Wait that
	// returned to this caller. Reading it as busy would answer ErrBusy
	// (or queue) for a turn that already landed, the same stale-state
	// race retireInFlightLocked closes for Branch; a Send that takes
	// the slot here is legitimate, and the epilogue leaves it alone.
	if (s.running && !s.inFlightDecidedLocked()) || s.boundaryLocked() {
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
		if policy == Interrupt || policy == Rollback {
			return s.interruptSendLocked(ctx, msg, policy == Rollback)
		}
		t := s.newTurnLocked()
		s.queue = append(s.queue, pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t})
		if !s.running && s.cfg.autoResume && s.settledBoundaryLocked() {
			// A boundary with every call decided and no runner alive —
			// a reopen inside the crash window between Decide's append
			// and the resume it armed — must not wedge the queue:
			// AutoResume's contract says the session resumes on its
			// own, so this Send arms it (under a window that cannot be
			// canceled by the sender walking away), and the follow-up
			// runs behind the resume. Expiry is resolved first, like
			// on every arming path (ADR 0021 §5): a boundary whose
			// only undecided requests lapsed settles here too.
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
		turn:  s.turnSeq,
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
		// The item boundary: a Branch here is already safe. A Send may
		// have taken the slot while this turn was being decided (the
		// busy check above reads a decided turn as free): this runner
		// then owns nothing but its own turn's epilogue — it exits
		// without touching the slot or the queue, and the new runner's
		// loop drains what is left.
		usurped := s.inFlight != nil
		if !usurped {
			s.inFlight = nil
		}
		// A Rollback turn branches the leaf back before its receipt
		// entry here — its entries have landed, the follow-up (queued
		// by the interrupting Send) starts next on the rolled-back
		// line (plan §6).
		s.rollbackLocked(cur.ps.turn)
		// Every exit path settles the live steers here, not only
		// runTurn's own end: a turn that died early — a panicked
		// session path, a prompt append that failed, a caller who
		// walked away before the run started — leaves its queued
		// steers undelivered, and they defer now instead of waiting
		// for whichever turn drains them next (the receipt reaches
		// its final state on every path).
		s.settleSteersLocked()
		if cur.resume && cur.ps.turn.failed() && cur.ps.turn.wasInterrupted() &&
			s.cfg.autoResume && s.boundaryLocked() {
			// An Interrupt felled this resume while it was the
			// boundary's one resolver, and its corpse recorded nothing
			// beyond the turn entry — the input was carried raw (the
			// decisions resolve their calls in the run), so the calls
			// still dangle and the boundary still holds the queue. A
			// failed resume is not retried on its own, but the interrupt
			// is the caller's next word: re-arm the resolution so the
			// interrupting Send's follow-up runs behind it (plan §6
			// steps 3–4), on a context no caller's walk-away can fell —
			// the resolution is what frees the message.
			if _, err := s.armResumeLocked(context.Background()); err != nil {
				s.agent.Logger().Error("thread: interrupt re-arm failed",
					"session", s.header.ID, "err", err)
			}
		}
		if usurped {
			// The resume work, the queue and the slot belong to the new
			// runner's loop now; this one's own turn is settled above.
			s.mu.Unlock()
			return
		}
		if s.resumeWork != nil {
			rw := *s.resumeWork
			s.resumeWork = nil
			s.inFlight = rw.turn
			s.mu.Unlock()
			cur = workItem{ps: pendingSend{ctx: rw.ctx, turn: rw.turn}, resume: true}
			continue
		}
		if retry && s.cfg.autoResume && s.settledBoundaryLocked() { // expiry resolved first
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
// inFlightDecidedLocked reports whether the runner slot's turn has
// already landed: the epilogue is freeing it. Callers hold s.mu.
func (s *Session) inFlightDecidedLocked() bool {
	return s.inFlight == nil || s.inFlight.isDecided()
}

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
		perr := s.recordTurnEnd(persist, t, nil, errOut, 0, nil, nil)
		s.mu.Lock()
		s.retireInFlightLocked(t)
		s.mu.Unlock()
		t.finish(nil, joinErrs(errOut, perr))
		return
	}

	// The trigger's first site (ADR 0020 §2): before the run, with the
	// prompt already on the path so the estimate covers what the run
	// is about to be fed. It never fires over an open approval
	// boundary — the dangling tail must stay raw for its decisions.
	s.maybeAutoCompact(ps.ctx)

	// The run flies under a cancelable child of the caller's context:
	// an Interrupt or Rollback send fells it (plan §6) without
	// touching the caller's own.
	rctx, cancel := context.WithCancel(ps.ctx)
	defer cancel()
	t.setRunCancel(cancel)
	defer t.clearRunCancel()
	if t.wasInterrupted() {
		// An Interrupt or Rollback arrived before the run armed its
		// cancel — between Send and here. The run dies at birth: the
		// interrupting Send's follow-up is already queued.
		cancel()
	}
	s.runTurn(persist, rctx, t, ps.opts)
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
		perr := s.recordTurnEnd(persist, t, nil, errOut, 0, nil, nil)
		s.mu.Lock()
		s.settleResumeLocked(t)
		s.retireInFlightLocked(t)
		s.mu.Unlock()
		t.finish(nil, joinErrs(errOut, perr))
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
	if err := s.auditResumeStartLocked(persist, t); err != nil {
		s.settleResumeLocked(t)
		s.mu.Unlock()
		t.finish(nil, fmt.Errorf("thread: resume audit: %w", err))
		return
	}
	opts := append([]weft.RunOption(nil), s.await.opts...)
	s.mu.Unlock()
	// A resume is as interruptible as a send's turn.
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.setRunCancel(cancel)
	defer t.clearRunCancel()
	if t.wasInterrupted() {
		cancel()
	}
	s.runTurn(persist, rctx, t, opts)
}

// runTurn is the shared body of a send's and a resume's run: the raw
// context in, the stream drained and forwarded, the turn's end
// recorded and the boundary chain run when the run parks calls
// (ADR 0021 §2). callerOpts are the run options captured for this
// turn — the resume inherits the parked send's — and after them come
// the recorded decisions, which win.
func (s *Session) runTurn(persist, ctx context.Context, t *Turn, callerOpts []weft.RunOption) {
	var res *weft.RunResult
	var err error
	inputLen := 0
	// One re-run is allowed (ADR 0020 §5): a turn failing with
	// weft.ErrContextOverflow compacts — reason overflow — and tries
	// again over the shrunken path under a fresh run id. The failed
	// attempt's partial is never recorded (an overflowed request
	// produced no transcript worth keeping; the run store holds the
	// attempt's own records under its id); a second failure fails the
	// turn with both errors joined. Per-step durability (ADR 0011 §7)
	// makes "never recorded" a branch, not an absence: the attempt's
	// step messages stay on the tree, on their own line of it.
	var firstOverflow error
	s.mu.Lock()
	sp := &stepPersist{startLeaf: s.leaf, resume: t.resume}
	s.mu.Unlock()
	for attempt := 0; ; attempt++ {
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
		// The transcript observer (ADR 0011 §7): each batch of messages
		// the run emits is appended as it joins, so a crash mid-turn
		// loses nothing emitted.
		runOpts = append(runOpts, s.observer(persist, sp))
		// The session's identity rides every run (ADR 0024 S5), appended
		// after the caller's options and before the transcript — a later
		// weft.Metadata wins (S1.1), so the session's keys win over a
		// caller's colliding thread.RunOptions(weft.Metadata(...)).
		runOpts = append(runOpts, weft.Metadata(s.runMetadata(t)))
		runOpts = append(runOpts, weft.Messages(input...), weft.RunID(t.RunID()))
		run := s.agent.Stream(withSession(ctx, s), runOpts...)
		for ev, serr := range run.Events() {
			if serr != nil {
				t.setStreamErr(serr)
				break
			}
			t.push(ev)
		}
		res, err = run.Wait()
		// The run's input length, in the run's own coordinates: the core
		// repairs its input before it runs, and a resume completes the
		// boundary's tool message in place, so the raw input's length
		// says nothing about where the run's own messages start. What
		// the run added is exactly what the observer was handed.
		s.mu.Lock()
		inputLen = max(len(runMessages(res, err))-sp.observed, 0)
		s.mu.Unlock()
		if attempt == 0 && errors.Is(err, weft.ErrContextOverflow) && s.cfg.reRunOnOverflow && !t.overflowRetried {
			firstOverflow = err
			t.overflowRetried = true
			// Branch back before compacting: the compaction must walk the
			// path the re-run will continue from — the failed attempt's
			// step messages are not part of it (ADR 0020 §5).
			s.mu.Lock()
			branched := s.branchBackLocked(persist, sp.startLeaf) == nil
			if branched {
				// A fresh attempt: nothing observed, nothing held (the
				// failed attempt's unwritten messages die with it).
				*sp = stepPersist{startLeaf: s.leaf, resume: t.resume, late: sp.late, err: sp.err}
			}
			s.mu.Unlock()
			if cerr := s.compactForOverflow(persist); cerr == nil && branched {
				// The shrunken path gets a fresh run id; the consumer
				// sees one turn, its outcome from the attempt that
				// answered. Steers the failed attempt drained die with
				// its transcript (an overflowed attempt records none),
				// so they rejoin the live queue — in acceptance order,
				// ahead of anything newer — and the re-run delivers
				// them again; otherwise their messages are lost while
				// their receipts would read delivered (ADR 0020 §5
				// keeps the failed attempt transcript-less, ADR 0011 §4
				// keeps the accepted input).
				t.clearStreamErr()
				s.mu.Lock()
				requeue := append([]queuedSteer(nil), s.handed...)
				s.steerQueue = append(requeue, s.steerQueue...)
				s.handed = nil
				s.turnSeq++
				t.remintRunID(fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq), s.turnSeq)
				s.mu.Unlock()
				continue
			} else {
				// No re-run without the branch-back either: the failed
				// attempt's step messages would ride the re-run's path,
				// and ADR 0020 §5 keeps them off it. The turn fails with
				// the overflow (both errors joined below).
				if !branched {
					s.agent.Logger().Warn("thread: overflow re-run skipped; the branch-back did not land",
						"session", s.header.ID, "run", t.RunID())
				} else {
					// The branch-back landed (count already reset) and the
					// compaction did not: the turn fails with the overflow,
					// and the failed attempt's repaired tail is written on
					// the active path by the turn's end — the same bytes a
					// failed overflow turn always recorded.
					s.agent.Logger().Warn("thread: overflow compaction failed; no re-run",
						"session", s.header.ID, "run", t.RunID(), "err", cerr)
				}
			}
		}
		break
	}
	if firstOverflow != nil && err != nil {
		err = errors.Join(firstOverflow, err)
	}
	if t.wasInterrupted() && err != nil {
		// The interrupted partial carries completions for its dangling
		// calls — the golden text — before the repair sees it (plan §6:
		// the recorded transcript stays sound model input, and the model
		// sees why a call has no answer).
		var runErr *weft.RunError
		if errors.As(err, &runErr) && runErr.Result != nil {
			runErr.Result.Messages = withInterruptedResults(runErr.Result.Messages)
		}
	}
	// The decision chain (ADR 0021 §2) runs before the turn's end is
	// persisted, so a call about to park is asked — grants, then a
	// bounded Approver — and its request entry lands in the same
	// atomic Append as the turn: no window where the turn is durable
	// and the request is not.
	var chain *chainResult
	if err == nil && res != nil && len(res.Pending) > 0 {
		chain = s.runChain(persist, t, callerOpts, res.Pending)
	}
	perr := s.recordTurnEnd(persist, t, res, err, inputLen, chain, sp)
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
			// Decide apply, ADR 0021 §5). The chain parks such a call
			// with its request entry (parkedCalls holds it), so this
			// branch sees only boundaries the chain fully decided.
			s.sweepExpiredLocked() // expiry resolved on every arming path
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
			// A chain-decided boundary that stayed open — a mirrored
			// child request still undecided beside it: the turn is
			// done, and Pending() shows what it waits for; the log line
			// is the operator's signal. Every call the chain left open
			// parked with a request, so OnRequest has already fired
			// for each.
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
	// A turn whose end the storage refused says so (ErrNotPersisted):
	// the run's result still rides beside the error.
	t.finish(res, joinErrs(err, perr))
}

// joinErrs joins a turn's own error with its persistence failure; nil
// when both are nil, the one that is set when only one is.
func joinErrs(err, perr error) error {
	switch {
	case perr == nil:
		return err
	case err == nil:
		return perr
	}
	return errors.Join(err, perr)
}

// runMetadata is the identity every run of this session carries
// (ADR 0024 S5): the session id and the turn number always, the public
// id when the session has one, and the fork origin or pool lineage
// when the header names one. The public id reads Session.Meta — the
// header overlaid with every InfoEntry's meta, in order — so a later
// SetInfo can add other keys; the header itself is never rewritten,
// which is why the create-time public id is what List's Meta filter
// matches. Caller-side, a run built by hand (not through a session)
// carries none of this: the session is the only minter.
func (s *Session) runMetadata(t *Turn) map[string]string {
	md := map[string]string{
		"weft.session.id": s.header.ID,
		"weft.turn":       strconv.Itoa(t.turn), // the turnSeq the run id was minted from
	}
	if v := s.Meta()["weft.public_id"]; v != "" { // header ⊕ every InfoEntry
		md["weft.public_id"] = v
	}
	if p := s.header.Parent; p != nil { // a fork
		md["weft.session.forked_from"] = p.Session + "#" + p.Entry
	}
	if l := s.header.Lineage; l != nil { // a pool child
		md["weft.session.parent"] = l.Session
		if l.Call != "" {
			md["weft.session.parent_call"] = l.Call
		}
	}
	return md
}

// runMessages returns the transcript a run left behind: the result's
// on success, the partial riding on the *weft.RunError on failure, nil
// when the run produced neither.
func runMessages(res *weft.RunResult, err error) []weft.Message {
	if err != nil {
		var runErr *weft.RunError
		if errors.As(err, &runErr) && runErr.Result != nil {
			return runErr.Result.Messages
		}
		return nil
	}
	if res != nil {
		return res.Messages
	}
	return nil
}

// recordTurnEnd closes the turn: it appends the run's new messages
// that per-step durability has not already written (normally none —
// the step observer wrote them as they joined, ADR 0011 §7), then the
// turn entry, in one atomic batch under a WithoutCancel window — the
// run is over, and its ledger must land whatever happened to the
// caller's context (ADR 0011 §4). On failure the partial transcript
// from RunError.Result is kept after weft.Repair; a cancellation is
// recorded as canceled; the calls a pending approval left unrun are
// recorded on the entry. inputLen is the run's input length in the
// run's own transcript; the messages beyond it are compared with what
// the tree holds for the turn, one by one, and the batch writes what
// is missing — after the last entry that matches, so a tail that
// differs from the run's final form leaves the active path
// (turnEndMessages). chain, when the turn parked calls, carries the
// decision chain's entries — requests, audits, chain decisions — into
// the same Append: the request and the turn are atomic, all or none
// (ADR 0021 §1). A resume turn's entry reuses the receipt id minted
// when the resume was armed (it has no prompt entry of its own).
//
// The error returned is the persistence failure, wrapping
// ErrNotPersisted: the batch is in no tree — written nowhere, adopted
// nowhere — and the turn reports it (Turn.Wait). It is also logged
// through the agent's logger.
func (s *Session) recordTurnEnd(ctx context.Context, t *Turn, res *weft.RunResult, err error, inputLen int, chain *chainResult, sp *stepPersist) error {
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
	inputLen = min(max(inputLen, 0), len(full))

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
	fail := func(cause error) error {
		s.agent.Logger().Error("thread: turn end not persisted",
			"session", s.header.ID, "run", t.runID, "err", cause)
		return fmt.Errorf("%w: session %s, run %s: %w", ErrNotPersisted, s.header.ID, t.runID, cause)
	}
	startLeaf, late := "", 0
	if sp != nil {
		// A join the tree still lacks goes first: the run's own
		// messages attach after it.
		if sp.join != nil {
			if jErr := s.flushJoinLocked(ctx, sp); jErr != nil {
				return fail(jErr)
			}
		}
		startLeaf, late = sp.startLeaf, sp.late
	}
	// What per-step durability has not already written — normally
	// nothing: the observer wrote every message as it joined. A
	// successful run's messages are already sound model input (the loop
	// repairs its input before appending its own), and a pending call
	// must stay unresolved in the tree so a later decision can resolve
	// it — so no repair on success. A failed run's partial is repaired
	// on the way in, the ADR's rule. Either way the tree is compared
	// with the final form message by message: what matches stays, what
	// is missing is written, and where the tree holds something else —
	// the raw form of a tail the repair or the interruption rewrote —
	// the batch attaches after the last matching entry, so the active
	// path holds exactly the run's transcript and the differing entries
	// stay on their own line, evidence like every abandoned line (ADR
	// 0011 §7).
	tail := s.turnTailLocked(startLeaf)
	have := make([]weft.Message, len(tail))
	for i, te := range tail {
		have[i] = te.msg
	}
	keep, newMsgs := turnEndMessages(full, inputLen, have, err)
	parent := s.leaf
	if keep < len(tail) {
		parent = startLeaf
		if keep > 0 {
			parent = tail[keep-1].id
		}
	}
	now := s.now()
	var entries []Entry
	for _, m := range newMsgs {
		id, idErr := s.mintCheckedLocked(entries)
		if idErr != nil {
			return fail(idErr)
		}
		entries = append(entries, MessageEntry{ID: id, ParentID: parent, Created: now, Message: cloneMessage(m)})
		parent = id
	}
	var teID string
	if t.resume {
		teID = t.id // a resume has no prompt entry; its receipt names its turn entry
		if !ValidID(teID) {
			return fail(fmt.Errorf("thread: invalid entry id %q", teID))
		}
		if _, dup := s.byID[teID]; dup {
			return fail(fmt.Errorf("thread: entry id %q already held by session %s", teID, s.header.ID))
		}
	} else {
		var idErr error
		if teID, idErr = s.mintCheckedLocked(entries); idErr != nil {
			return fail(idErr)
		}
	}
	te := TurnEntry{ID: teID, ParentID: parent, Created: now, RunID: t.runID, LateSteps: late}
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
	// The delivered steers' receipts join the turn's batch (ADR 0019):
	// the messages themselves are already in it — the run's transcript
	// carries them beyond the input — so the receipt is what ties each
	// to its queued entry and names the run that drained it.
	for i := range s.handed {
		id, idErr := s.mintCheckedLocked(entries)
		if idErr != nil {
			return fail(idErr)
		}
		entries = append(entries, ReceiptEntry{ID: id, ParentID: parent,
			Created: now, Receipt: s.handed[i].receipt,
			Status: ReceiptDelivered, RunID: t.runID})
		parent = id
	}
	// handed stays set until the batch lands: a turn-end append that
	// fails — or panics — leaves these steers delivered to a run whose
	// messages are in no tree (the batch, them included, is neither
	// written nor adopted), and the settle after recordTurnEnd defers
	// them so their messages re-run instead of finishing as delivered
	// into nothing.
	handed := s.handed
	if chain != nil {
		// The chain's entries join the turn's batch: ids and parents
		// assigned here, in call order, after the turn entry (ADR 0021
		// §1–§2). Their kinds carry no prompt and no messages; they are
		// bookkeeping the model never sees.
		for _, e := range chain.entries {
			id, idErr := s.mintCheckedLocked(entries)
			if idErr != nil {
				return fail(idErr)
			}
			e = fillApprovalEntry(e, id, parent, now)
			entries = append(entries, e)
			if id := idOf(e); id != "" {
				parent = id
			}
		}
	}
	if appendErr := s.st.Append(ctx, s.header.ID, entries...); appendErr != nil {
		// The batch — the steered messages included — is in no tree:
		// written nowhere, adopted nowhere. The steers stay in handed
		// and the settle after this defers them, so their messages
		// re-run as follow-ups instead of finishing as delivered into
		// nothing.
		return fail(appendErr)
	}
	s.handed = nil
	for _, e := range entries {
		s.adoptLocked(e)
	}
	if sp != nil {
		sp.backlog = nil // the batch wrote what the steps could not
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
	return nil
}

// A Turn is the receipt and the handle of one accepted Send — or of a
// resume run over a parked approval boundary, which has no prompt of
// its own: its receipt names its turn entry.
type Turn struct {
	id    string
	runID string
	// turn is the turn number the run id was minted from — the
	// turnSeq counter at mint time, restamped beside the id when the
	// overflow re-run remints it — so the run's metadata can name the
	// turn the store records belong to (weft.turn, ADR 0024 S5).
	// Written under s.mu at mint, under t.mu at remint; read by the
	// runner goroutine alone (runMetadata).
	turn int
	// resume marks a resume run: no prompt entry, and the turn entry
	// reuses the receipt id minted when the resume was armed.
	resume bool
	// next is the turn this turn's parked boundary resumed under — the
	// link a caller follows to watch an approval flow through. Written
	// once, under mu, by the session when it arms a resume.
	next *Turn

	// interrupt is the Interrupt/Rollback bookkeeping (plan §6):
	// cancel fells the run's context, interrupted marks the turn (the
	// partial's dangling calls get the golden completion), rollback
	// names the Rollback policy, and preTurn is the leaf before this
	// turn's receipt entry — where a rollback branches back to. Written
	// by a Send under mu while the run flies; read by the runner under
	// mu at the turn's end.
	cancel      context.CancelFunc
	interrupted bool
	rollback    bool
	preTurn     string
	// overflowRetried marks the turn that already spent its one
	// overflow re-run (ADR 0020 §5). Runner-goroutine only.
	overflowRetried bool

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
// holds the run's records under, unique across a reopen. A turn that
// re-ran after an overflow reports the re-run's id (the run that
// produced its outcome).
func (t *Turn) RunID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runID
}

// remintRunID re-ids the turn for its overflow re-run: a fresh
// <session>-t<n>, spent from the same counter, so the re-run's store
// records stay addressable (the failed attempt's id is in its run
// records). turn is the new number the id was minted from, set beside
// it, so the re-run's metadata names the turn it now is.
func (t *Turn) remintRunID(id string, turn int) {
	t.mu.Lock()
	t.runID = id
	t.turn = turn
	t.mu.Unlock()
}

// setRunCancel arms the cancel that fells this turn's run; the runner
// calls it when the run starts and clears it when the run ends, so a
// late Interrupt cannot cancel a context nobody runs on.
func (t *Turn) setRunCancel(cancel context.CancelFunc) {
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
}

// clearRunCancel drops the cancel handle; see setRunCancel.
func (t *Turn) clearRunCancel() {
	t.mu.Lock()
	t.cancel = nil
	t.mu.Unlock()
}

// wasInterrupted reports the Interrupt mark (the runner's read of a
// field a Send may have written under mu mid-run).
func (t *Turn) wasInterrupted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.interrupted
}

// rollbackTarget reports the rollback mark and its branch target.
func (t *Turn) rollbackTarget() (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rollback, t.preTurn
}

// clearStreamErr drops a failed attempt's stream error when the turn
// gets another attempt (the overflow re-run): the consumer must see
// only the final attempt's error, once, at the end.
func (t *Turn) clearStreamErr() {
	t.mu.Lock()
	t.streamErr = nil
	t.mu.Unlock()
}

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

// isDecided reports whether the turn has landed — finish has run —
// the runner-epilogue race Send closes with it: a decided in-flight
// turn is a slot being freed, not a busy session.
func (t *Turn) isDecided() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
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
