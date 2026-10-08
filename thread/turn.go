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

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread/internal/carry"
)

// Policy is what a Send does when the session is already running a
// turn — the busy policy (ADR 0011 §4). Queue is the default.
//
// The constant Queue names a policy; the method Session.Queue lists
// what is waiting. They share a word, not a meaning.
type Policy int

const (
	// Queue holds the follow-up and runs it when the current turn
	// ends, in acceptance order: the send is accepted — an accepted
	// receipt entry (ReceiptAccepted) carrying the message is appended
	// and flushed, so the send survives a crash — its Turn is returned
	// at once, and its prompt entry is written when its turn starts.
	// The default.
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
	// the fate. The Send's Turn is the receipt: it ends when the steer
	// reaches its final state — Turn.Outcome says which: delivered,
	// deferred (Turn.Next is the follow-up turn) or dropped — with a
	// nil result. A Steer on a session that is not busy has nothing to
	// steer into: it runs as a plain turn.
	Steer
	// Interrupt cancels the running turn and runs the message next:
	// the in-flight run's context is canceled, the calls its partial
	// transcript left without a result carry the golden interruption
	// text, an approval boundary that holds the session is denied with
	// the interrupted reason, and the message runs as the next turn.
	// The interrupted turn's entries stay on the tree — evidence,
	// never deleted. A boundary holding nested approvals (thread/pool,
	// ADR 0022 §7) is denied whole — the delegating call and the
	// child's mirrored requests: the pool that made the child replays
	// the denial into it and the child runs to its end, its answer on
	// its receipt; in a process whose pool has not taken the session
	// over yet (after a restart, before Recover) the child stays
	// parked until it does.
	Interrupt
	// Rollback is an Interrupt that also branches the leaf back to
	// before the interrupted turn's receipt entry: the follow-up runs
	// as though the interrupted turn never happened, while its
	// entries keep their own line of the tree (nothing lost).
	Rollback
)

// String returns the policy's name — "queue", "reject", "steer",
// "interrupt" or "rollback" — the value TurnEntry.Policy records.
func (p Policy) String() string {
	switch p {
	case Queue:
		return "queue"
	case Reject:
		return "reject"
	case Steer:
		return "steer"
	case Interrupt:
		return "interrupt"
	case Rollback:
		return "rollback"
	}
	return "policy(" + strconv.Itoa(int(p)) + ")"
}

type busyPolicyOption Policy

func (o busyPolicyOption) applySession(c *sessionConfig) { c.policy = Policy(o) }

// BusyPolicy returns the SessionOption setting what Send does when the
// session is busy: Queue (the default), Reject, Steer, Interrupt, or
// Rollback. A single Send overrides it with As.
func BusyPolicy(p Policy) SessionOption { return busyPolicyOption(p) }

// SendOption configures one Send: RunOptions carries extra run options
// into the turn's run, As overrides the busy policy.
type SendOption interface {
	applySend(*sendConfig)
}

// sendConfig is one Send's resolved configuration.
type sendConfig struct {
	runOpts []core.RunOption
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
// next turn on a Steer session. The policy is captured when Send is
// called, with the turn's other settings, and recorded on the entry of
// the turn the send runs as (TurnEntry.Policy). On a session that is
// not busy every policy does the same thing — the send runs as a plain
// turn: there is nothing to steer into, interrupt or queue behind.
func As(p Policy) SendOption { return asOption(p) }

type runOptionsOption struct{ opts []core.RunOption }

func (o runOptionsOption) applySend(c *sendConfig) { c.runOpts = append(c.runOpts, o.opts...) }

// RunOptions returns the SendOption carrying extra core.RunOptions
// into this turn's run — budgets, taps, thinking, metadata. Three
// things are the session's own, and an option that would set one is
// rejected by Send with an error wrapping core.ErrInvalidRunOption:
//
//   - the transcript and the run id — core.Messages, core.Prompt,
//     core.RunID: Send builds the transcript from the session's tree
//     and mints <session>-t<n>; either option would quietly detach the
//     run from the tree (ADR 0011 §4);
//   - the steering source — core.Steering: the session owns the steer
//     queue (Send under the Steer policy);
//   - approval decisions — core.Approve, core.Deny, core.Resolve,
//     core.ResolveError: a Send never runs while an approval boundary
//     is open (it queues behind it), so a decision passed here would
//     reach no parked call. Decisions are recorded with Session.Decide
//     and applied by the boundary's resume run (ADR 0021 §1).
//
// The options bind every run the session starts on the turn's behalf:
// the resume run of a boundary the turn parked (after Decide or
// Resume), its overflow re-run, and the follow-up turn a steer aimed at
// it becomes when the steer cannot join the run (it meets the
// approval boundary or a StopWhen end, or arrives after the last drain
// point) — the follow-up runs under these options, the steer's own
// after them. A steer that finds no turn in flight and no boundary
// open runs as a plain turn under its own options. The same holds for
// the values on the turn's context (a ParkAllExcept list or metadata a
// delegating run handed down): those runs see every value their own
// context lacks. Options and context are process state — a reopened
// session's restored sends and boundaries run without them.
func RunOptions(opts ...core.RunOption) SendOption { return runOptionsOption{opts} }

// sessionOwned names the run option Send must refuse, or "" for one it
// carries. RunOption is sealed (ADR 0004) — only the core constructs
// values — so the dynamic type of what a constructor returns names the
// option without spelling an unexported type name in a string, and the
// constructor stays the source of truth through any internal rename;
// the tests pin the rejection with the constructors themselves. The
// probes are built per call: no package state, and Send is not a hot
// path.
func sessionOwned(o core.RunOption) string {
	switch reflect.TypeOf(o) {
	case reflect.TypeOf(core.Messages()):
		return "core.Messages in RunOptions: the transcript is Send's to build from the session's tree (ADR 0011 §4)"
	case reflect.TypeOf(core.Prompt("")):
		return "core.Prompt in RunOptions: Send's msg is the prompt (ADR 0011 §4)"
	case reflect.TypeOf(core.RunID("")):
		return "core.RunID in RunOptions: the session mints <session>-t<n> run ids (ADR 0011 §4)"
	case reflect.TypeOf(core.Steering(nil)):
		return "core.Steering in RunOptions: the session owns the steer queue (ADR 0019; Send under the Steer policy)"
	case reflect.TypeOf(core.Approve("")):
		return "core.Approve in RunOptions: a Send never reaches a parked call; record the decision with Session.Decide (ADR 0021 §1)"
	case reflect.TypeOf(core.Deny("", "")):
		return "core.Deny in RunOptions: a Send never reaches a parked call; record the decision with Session.Decide (ADR 0021 §1)"
	case reflect.TypeOf(core.Resolve("", "")): // ResolveError is the same option type
		return "core.Resolve in RunOptions: a Send never reaches a parked call; record the decision with Session.Decide (ADR 0021 §1)"
	}
	return ""
}

// rejectSessionOwned fails a Send whose run options would set what the
// session owns — the transcript, the run id, the steering source, the
// approval decisions — wrapping core.ErrInvalidRunOption.
func rejectSessionOwned(opts []core.RunOption) error {
	for _, o := range opts {
		if o == nil {
			continue
		}
		if why := sessionOwned(o); why != "" {
			return fmt.Errorf("thread: %w: %s", core.ErrInvalidRunOption, why)
		}
	}
	return nil
}

// pendingSend is an accepted Send waiting for its turn: the context it
// was sent with, its message, its extra run options, the Turn the
// caller already holds, and the id of the accepted receipt entry that
// made it durable ("" for the one path that queues in memory only: a
// deferral whose receipt could not be written).
type pendingSend struct {
	ctx     context.Context
	msg     core.Message
	opts    []core.RunOption
	turn    *Turn
	receipt string
	// restored marks a send Open put back in the queue from its
	// accepted receipt: its ctx is a detached placeholder — the Send
	// that accepted it is gone — until Continue binds it to its own.
	restored bool
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
// Settings are captured when Send is called: the agent, the extra run
// options, and the busy policy. On a busy session the policy decides —
// Queue (the default) holds the follow-up, in order, durably: an
// accepted receipt entry carries the message until its turn starts, so
// a queued send survives a crash and a reopened session finds it in
// its queue (Session.Queue; Continue runs it). Reject fails with
// ErrBusy; Steer, Interrupt and Rollback are described on Policy.
//
// A Send while approval requests are pending is queued, not run (ADR
// 0021 §5): the parked boundary must resolve first — Decide (which
// resumes on its own when AutoResume is on) or Resume — and the queued
// follow-up then runs with the boundary's transcript completed. Under
// Reject a pending boundary reads as busy.
//
// Between a turn's end and the session's next piece of work — the
// between-turn compaction, when one is due — nothing is running to
// steer into, interrupt or reject for: a Send in that window is
// accepted under every policy and runs next.
func (s *Session) Send(ctx context.Context, msg core.Message, opts ...SendOption) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := resolveSend(opts...)
	if err := rejectSessionOwned(cfg.runOpts); err != nil {
		return nil, err
	}
	extra := append([]core.RunOption(nil), cfg.runOpts...) // captured with the Send
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
	// A runner whose turn has landed is between items: the turn's Wait
	// has returned, and what the runner still does — the between-turn
	// compaction, its next item — is not a turn to be busy with.
	if s.busyLocked() || s.boundaryLocked() {
		// A running turn holds the session, and so does an open approval
		// boundary: the follow-up is queued and runs when the boundary
		// resolves — Decide or Resume resume it, the runner drains the
		// queue after (ADR 0021 §5). The queue holds acceptance order
		// across the boundary; the branch that cleared one restarts the
		// runner at the head on the next Send.
		switch policy {
		case Reject:
			return nil, fmt.Errorf("%w: session %s", ErrBusy, s.header.ID)
		case Steer:
			// The steering path (ADR 0019): durable acceptance now,
			// delivery at the running turn's drain point — or, when only
			// a boundary holds the session, a deferral at once: a steer
			// never resolves a parked call.
			return s.steerSendLocked(ctx, msg, extra)
		case Interrupt, Rollback:
			return s.interruptSendLocked(ctx, msg, extra, policy)
		}
		t, err := s.enqueueLocked(ctx, msg, extra, policy)
		if err != nil {
			return nil, err
		}
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
	if s.running || len(s.queue) > 0 {
		// Nothing is in flight and no boundary holds, yet this send is
		// not first in line: the runner is between items (its next
		// decision takes the queue's head), or earlier sends are still
		// queued (restored by Open, or held by a boundary a Branch
		// cleared). Acceptance order rules — it queues behind them.
		t, err := s.enqueueLocked(ctx, msg, extra, policy)
		if err != nil {
			return nil, err
		}
		s.kickRunnerLocked()
		return t, nil
	}
	t := s.newTurnLocked()
	t.policy, t.hasPolicy = policy, true
	// The prompt is durable before anything else happens: appended
	// under the caller's context and flushed, so a Send that returns
	// without error has already survived a crash.
	if err := s.appendPromptLocked(ctx, t, msg); err != nil {
		return nil, err
	}
	t.setPromptDone()
	s.startRunnerLocked(workItem{ps: pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t}})
	return t, nil
}

// busyLocked reports whether a turn is in flight: the runner is alive
// and inside an item that has not landed. Callers hold s.mu.
func (s *Session) busyLocked() bool {
	return s.running && s.inFlight != nil && !s.inFlight.isDecided()
}

// enqueueLocked accepts a send that cannot run now: durable first — an
// accepted receipt entry (ReceiptAccepted) carrying the message, the
// id its prompt entry will take and the run id minted for it, appended
// and flushed (ADR 0011 §4: accepted input is durable input) — then
// queued, in acceptance order. A reopened session restores an accepted
// receipt whose prompt entry never landed (restoreAccepted). Callers
// hold s.mu.
func (s *Session) enqueueLocked(ctx context.Context, msg core.Message, opts []core.RunOption, policy Policy) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t := s.newTurnLocked()
	t.policy, t.hasPolicy = policy, true
	receipt, err := s.acceptLocked(ctx, t, msg, nil)
	if err != nil {
		return nil, err
	}
	s.queue = append(s.queue, pendingSend{ctx: ctx, msg: msg, opts: opts, turn: t, receipt: receipt})
	return t, nil
}

// acceptLocked writes the accepted receipt entry for a queued send's
// turn — after lead, the entry that must land in the same atomic
// Append (a steer's deferred receipt) — and returns its id. A receipt
// that was written but could not be flushed is dropped again, best
// effort, so a reopen does not run a send its caller was told failed.
// Callers hold s.mu.
func (s *Session) acceptLocked(ctx context.Context, t *Turn, msg core.Message, lead func(id, parent string, created time.Time) Entry) (string, error) {
	if !ValidID(t.id) {
		return "", fmt.Errorf("thread: invalid entry id %q", t.id)
	}
	if _, dup := s.byID[t.id]; dup {
		return "", fmt.Errorf("thread: entry id %q already held by session %s", t.id, s.header.ID)
	}
	now := s.now()
	var entries []Entry
	parent := s.leaf
	mint := func() (string, error) {
		id, err := s.mintCheckedLocked(entries)
		if err == nil && id == t.id {
			err = fmt.Errorf("thread: entry id %q minted twice for one append to session %s", id, s.header.ID)
		}
		return id, err
	}
	if lead != nil {
		id, err := mint()
		if err != nil {
			return "", err
		}
		entries = append(entries, lead(id, parent, now))
		parent = id
	}
	id, err := mint()
	if err != nil {
		return "", err
	}
	clone := deepCloneMessage(msg)
	entries = append(entries, ReceiptEntry{ID: id, ParentID: parent, Created: now,
		Status: ReceiptAccepted, Msg: &clone, Turn: t.id, RunID: t.runID})
	if err := s.appendEntriesLocked(ctx, entries...); err != nil {
		if _, written := s.byID[id]; written {
			// Appended, not flushed: the entry is in the file.
			s.dropAcceptedLocked(context.WithoutCancel(ctx), id)
			return "", fmt.Errorf("thread: accepted receipt flush: %w", err)
		}
		return "", err
	}
	return id, nil
}

// dropAcceptedLocked settles an accepted receipt as dropped, best
// effort: the send it stood for will not run, and a reopen must not
// restore it. A failure is logged — the receipt then stays accepted in
// the file, and the next Open restores the send to the queue. Callers
// hold s.mu.
func (s *Session) dropAcceptedLocked(ctx context.Context, receipt string) {
	if receipt == "" {
		return
	}
	id, err := s.mintCheckedLocked(nil)
	if err == nil {
		e := ReceiptEntry{ID: id, ParentID: s.leaf, Created: s.now(), Receipt: receipt, Status: ReceiptDropped}
		if err = s.st.Append(ctx, s.header.ID, e); err == nil {
			s.adoptLocked(e)
			return
		}
	}
	s.agent.Logger().Error("thread: accepted receipt not dropped; a reopen restores the send",
		"session", s.header.ID, "receipt", receipt, "err", err)
}

// newTurnLocked mints the receipt — the prompt entry's id — and the
// run id the turn will run under. Run ids count from the turn counter,
// which recovers on a reopen from the highest <session>-t<n> the tree
// records anywhere (recoverTurnSeq), and every minted id is recorded
// before its run starts — on the prompt entry, the accepted receipt,
// the resume's audit entry, the failed overflow attempt's turn entry —
// so a turn whose end never lands (a crash) never shares its id with
// the next one.
func (s *Session) newTurnLocked() *Turn {
	id := s.mintIDLocked()
	s.turnSeq++
	t := newTurn(id)
	t.runID = fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq)
	t.turn = s.turnSeq
	return t
}

// newTurn builds a Turn around its receipt id.
func newTurn(id string) *Turn {
	t := &Turn{id: id, done: make(chan struct{})}
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

// recoverTurnSeq returns the highest <session>-t<n> any entry records
// for this session: turn entries (the run that ended, and the re-run a
// failed overflow attempt armed), prompt entries, accepted and
// delivered receipts, approval requests, decisions and audit steps. A
// run id is written before its run starts, so the counter recovered
// here never falls behind an id a crashed writer already used — the
// overflow re-run's and a turn whose end never landed included.
func recoverTurnSeq(session string, entries []Entry) int {
	seq := 0
	see := func(runID string) {
		if n, ok := runSeq(session, runID); ok && n > seq {
			seq = n
		}
	}
	for _, e := range entries {
		switch e := e.(type) {
		case TurnEntry:
			see(e.RunID)
			see(e.ReRun)
		case MessageEntry:
			see(e.RunID)
		case ReceiptEntry:
			see(e.RunID)
		case ApprovalRequestEntry:
			see(e.RunID)
		case ApprovalDecisionEntry:
			see(e.RunID)
		case ApprovalAuditEntry:
			see(e.RunID)
		}
	}
	return seq
}

// appendPromptLocked writes the turn's prompt entry — carrying the run
// id minted for it — and flushes it. A flush failure fails the Send:
// the entry is written, but its durability is the promise, and nothing
// else has started. The leaf then moves back to where it was — a
// recorded leaf entry, the prompt being in the file — so the failed
// prompt sits on a line of its own and a retried Send leaves one
// prompt in the context, not two. Callers hold s.mu.
func (s *Session) appendPromptLocked(ctx context.Context, t *Turn, msg core.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(t.id) {
		return fmt.Errorf("thread: invalid entry id %q", t.id)
	}
	if _, dup := s.byID[t.id]; dup {
		return fmt.Errorf("thread: entry id %q already held by session %s", t.id, s.header.ID)
	}
	prev := s.leaf
	e := MessageEntry{ID: t.id, ParentID: prev, Created: s.now(), Message: cloneMessage(msg), RunID: t.runID}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	if err := s.flushLocked(ctx); err != nil {
		s.unwindPromptLocked(context.WithoutCancel(ctx), prev)
		return fmt.Errorf("thread: prompt flush: %w", err)
	}
	return nil
}

// unwindPromptLocked moves the leaf back to prev after a prompt that
// was written but not flushed: a leaf entry records the move, so a
// reopen reads the same position; when even that cannot be written the
// in-memory leaf still moves — a retry then attaches beside the failed
// prompt, never after it — and the log says the file disagrees until
// the next entry lands. Callers hold s.mu.
func (s *Session) unwindPromptLocked(ctx context.Context, prev string) {
	id, err := s.mintCheckedLocked(nil)
	if err == nil {
		e := LeafEntry{ID: id, ParentID: s.leaf, Created: s.now(), Entry: prev}
		if err = s.st.Append(ctx, s.header.ID, e); err == nil {
			s.adoptLocked(e)
			return
		}
	}
	s.leaf = prev
	s.agent.Logger().Error("thread: unflushed prompt's leaf move not recorded",
		"session", s.header.ID, "err", err)
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
	first := s.queue[0]
	s.queue = s.queue[1:]
	s.startRunnerLocked(workItem{ps: first})
}

// startRunnerLocked starts the session's runner on item: the slot is
// taken and the item is in flight before the goroutine exists, so no
// caller sees a runner without its turn. Callers hold s.mu and have
// checked that no runner is alive.
func (s *Session) startRunnerLocked(item workItem) {
	s.running = true
	s.inFlight = item.ps.turn
	s.bindLocked(item)
	s.between = false
	go s.execute(item)
}

// execute runs runner work one item at a time until nothing is
// runnable: the session's single runner. Each item runs to completion
// — prompt, run, end-of-turn persistence, the between-turn compaction
// — before the next starts, in acceptance order; a resume run for a
// completed boundary takes the slot before any queued send, because
// the queued sends wait for the boundary by contract (ADR 0021 §5).
// The runner exits when the queue is empty or an open boundary holds
// it — Decide, Resume or a later Send restart it.
func (s *Session) execute(first workItem) {
	cur := first
	for {
		s.runOneContained(cur)
		next, more := s.epilogue(cur)
		if !more {
			return
		}
		cur = next
	}
}

// epilogue is the runner's item boundary: with the item's turn landed,
// it settles what the turn left — a rollback's branch, the steers
// still live — and decides what the runner does next: a resume armed
// while it worked, a boundary that settled, the queue's head, or exit.
// The decision and the slot's state change are one critical section,
// so a Send, a Decide or a Close sees either the runner with its next
// item in flight or no runner at all.
//
// The caller's IDs and Clock functions run in here (the settles append
// entries). One that panics is contained: the lock is released, the
// runner exits with the slot free — the queue intact, the next Send or
// Continue restarting it — and the log says why.
func (s *Session) epilogue(cur workItem) (next workItem, more bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Error("thread: runner epilogue panicked; the runner exits",
				"session", s.header.ID, "panic", p)
			s.exitRunnerLocked()
			next, more = workItem{}, false
		}
	}()
	t := cur.ps.turn
	// A resume that failed without resolving its boundary — its
	// context died, its persistence failed — is not retried here: an
	// unbounded auto-retry loop has no backoff and no stop, so the
	// runner leaves the boundary open for the caller's next Send or
	// Resume (both arm it again).
	retry := !cur.resume || !t.failed()
	// The catch-all for the slot: every path lands its turn before it
	// returns, and a path that did not is landed here.
	if s.inFlight == t {
		s.inFlight, s.between = nil, true
	}
	// A Rollback turn branches the leaf back before its receipt entry
	// here — its entries have landed, the follow-up (queued by the
	// interrupting Send) starts next on the rolled-back line.
	s.rollbackLocked(t)
	// Every exit path settles the live steers here, not only runTurn's
	// own end: a turn that died early — a panicked session path, a
	// prompt append that failed, a caller who walked away before the
	// run started — leaves its queued steers undelivered, and they
	// defer now instead of waiting for whichever turn drains them next
	// (the receipt reaches its final state on every path).
	s.settleSteersLocked()
	if s.closing >= stateAborted {
		// A Close that gave up waiting has ended the queue; nothing new
		// starts behind it — what the settle above queued in memory
		// ends the same way.
		closed := fmt.Errorf("%w: session %s", ErrClosed, s.header.ID)
		for _, ps := range s.queue {
			ps.turn.finish(nil, closed)
		}
		s.queue = nil
		if rw := s.resumeWork; rw != nil {
			s.resumeWork = nil
			if s.await.resumed == rw.turn {
				s.await.resumed = nil
			}
			rw.turn.finish(nil, closed)
		}
		s.exitRunnerLocked()
		return workItem{}, false
	}
	if cur.resume && t.failed() && t.wasInterrupted() &&
		s.cfg.autoResume && s.boundaryLocked() {
		// An Interrupt felled this resume while it was the boundary's
		// one resolver, and it recorded nothing that resolves the
		// calls — they still dangle and the boundary still holds the
		// queue. A failed resume is not retried on its own, but the
		// interrupt is the caller's next word: re-arm the resolution so
		// the interrupting Send's follow-up runs behind it, on a
		// context no caller's walk-away can fell — the resolution is
		// what frees the message.
		if _, err := s.armResumeLocked(context.Background()); err != nil {
			s.agent.Logger().Error("thread: interrupt re-arm failed",
				"session", s.header.ID, "err", err)
		}
	}
	if s.resumeWork != nil {
		rw := *s.resumeWork
		s.resumeWork = nil
		return s.handLocked(workItem{ps: pendingSend{ctx: rw.ctx, turn: rw.turn}, resume: true})
	}
	if retry && s.cfg.autoResume && s.settledBoundaryLocked() { // expiry resolved first
		// The last decision of a parked boundary landed while this
		// runner worked, or its last undecided request lapsed: resume
		// at once, under the settings captured when the turn parked.
		// The minted resume is registered like any arming, so a
		// concurrent Resume joins it rather than arming a second one.
		rt := s.mintResumeLocked()
		ctx := s.await.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		return s.handLocked(workItem{ps: pendingSend{ctx: ctx, turn: rt}, resume: true})
	}
	if len(s.queue) > 0 && !s.boundaryLocked() {
		head := s.queue[0]
		s.queue = s.queue[1:]
		return s.handLocked(workItem{ps: head})
	}
	s.exitRunnerLocked()
	return workItem{}, false
}

// handLocked puts the runner's next item in flight: the busy state —
// what Branch, a Reject Send and Close read — names it before the
// lock is released. Callers hold s.mu.
func (s *Session) handLocked(item workItem) (workItem, bool) {
	s.inFlight = item.ps.turn
	s.bindLocked(item)
	s.between = false
	s.signalRunnerLocked()
	return item, true
}

// bindLocked records on the item's turn, as it goes in flight, what a
// steer aimed at it inherits if the steer becomes a follow-up: a
// send's own run options and context, or — for a resume — the parked
// send's options and the context the resume runs on (resumeCtx).
// Callers hold s.mu.
func (s *Session) bindLocked(item workItem) {
	t := item.ps.turn
	if item.resume {
		t.aimOpts, t.aimCtx = s.await.opts, s.resumeCtxLocked(item.ps.ctx)
		return
	}
	t.aimOpts, t.aimCtx = item.ps.opts, item.ps.ctx
}

// resumeCtxLocked is the context a resume runs on: armed's
// cancellation and deadline — the call that armed it, or the
// boundary's own window — and, for every value armed does not carry,
// the parked turn's (internal/carry). The resume continues the parked
// turn, so what rode that turn's context — a ParkAllExcept list
// inherited from a delegating run, its metadata — still binds the
// resumed steps, whoever decided. After a restart the parked turn's
// context is gone, like its run options. Callers hold s.mu.
func (s *Session) resumeCtxLocked(armed context.Context) context.Context {
	return carry.Values(armed, s.await.ctx)
}

// exitRunnerLocked frees the runner slot. Callers hold s.mu.
func (s *Session) exitRunnerLocked() {
	s.running = false
	s.inFlight = nil
	s.between = false
	s.signalRunnerLocked()
}

// runnerMovedLocked returns a channel closed the next time the runner
// changes state — takes its next item, or exits. Close waits on it
// instead of polling. Callers hold s.mu.
func (s *Session) runnerMovedLocked() <-chan struct{} {
	if s.runnerMoved == nil {
		s.runnerMoved = make(chan struct{})
	}
	return s.runnerMoved
}

// signalRunnerLocked wakes whoever waits on runnerMovedLocked. Callers
// hold s.mu.
func (s *Session) signalRunnerLocked() {
	if s.runnerMoved != nil {
		close(s.runnerMoved)
		s.runnerMoved = nil
	}
}

// busyInvariantLocked checks the runner slot's invariant, the one the
// busy policy, Branch and Close all read: with a runner alive, exactly
// one of "an item is in flight" and "the runner is between items"
// holds; with none alive, neither does. It returns a description of
// the violation, or nil. Callers hold s.mu.
func (s *Session) busyInvariantLocked() error {
	switch {
	case s.running && s.inFlight == nil && !s.between:
		return fmt.Errorf("thread: session %s: a runner is alive with no item in flight and not between items", s.header.ID)
	case s.running && s.inFlight != nil && s.between:
		return fmt.Errorf("thread: session %s: the runner is between items with an item in flight", s.header.ID)
	case !s.running && (s.inFlight != nil || s.between):
		return fmt.Errorf("thread: session %s: no runner is alive, yet an item is in flight or the runner is between items", s.header.ID)
	}
	return nil
}

// locked runs fn with s.mu held and releases it however fn returns —
// by a panic included. The runner's critical sections that call the
// caller's IDs and Clock functions run through it, so a callback that
// panics never leaves the lock held behind it: the panic propagates to
// the runner's containment with the session still usable.
func (s *Session) locked(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// land decides the turn: its in-flight mark retires and its outcome is
// set in one critical section, so a caller whose Wait returns may
// Branch or Send at once and never read ErrBusy for a turn that
// already landed — and no reader sees a runner that is neither inside
// an item nor between two. A resume's arming is settled first, so its
// caller faces a settled boundary: a retry after a failure arms fresh,
// a resolution reads ErrNotPending.
func (s *Session) land(item workItem, res *core.RunResult, err error) {
	t := item.ps.turn
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.resume {
		s.settleResumeLocked(t)
	}
	if s.inFlight == t {
		s.inFlight, s.between = nil, true
	}
	t.finish(res, err)
}

// runOneContained keeps a panic in the session's own turn machinery
// from wedging the runner slot: the turn ends with an error wrapping
// ErrTurnPanicked, the agent's logger says so, and the queue moves on.
// Tool and model panics never reach here — the core contains them as
// errors — this guards the session layer's own code and the caller's
// IDs and Clock functions, which run under the session's lock: every
// such section releases the lock on its way out (locked), so the
// containment can take it.
func (s *Session) runOneContained(item workItem) {
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Error("thread: turn panicked",
				"session", s.header.ID, "run", item.ps.turn.RunID(), "panic", p)
			// The turn may already be decided (the panic came from
			// after it); finish refuses to overwrite it.
			s.land(item, nil, fmt.Errorf("%w: %v", ErrTurnPanicked, p))
		}
	}()
	if item.resume {
		s.runResume(item)
		return
	}
	s.runOne(item)
}

// runOne runs one accepted send. The idle path wrote the prompt in
// Send; a dequeued send writes it here under a WithoutCancel window,
// so an accepted send never loses its prompt to a context that died
// while it waited. The session goroutine consumes the run's stream
// itself and forwards every event to the Turn, so the turn is fully
// observed however the caller reads it — or does not.
func (s *Session) runOne(item workItem) {
	ps := item.ps
	t := ps.turn
	persist := context.WithoutCancel(ps.ctx)
	var promptErr error
	s.locked(func() {
		if t.promptWritten() {
			return
		}
		if promptErr = s.appendPromptLocked(persist, t, ps.msg); promptErr != nil {
			// The send will not run: its accepted receipt must not
			// bring it back on a reopen.
			s.dropAcceptedLocked(persist, ps.receipt)
			return
		}
		t.setPromptDone()
	})
	if promptErr != nil {
		s.land(item, nil, fmt.Errorf("%w: prompt append: %w", ErrNotRun, promptErr))
		return
	}

	if err := ps.ctx.Err(); err != nil {
		// The caller walked away before the turn started: the prompt is
		// kept, the turn is recorded as canceled, and the queue moves on.
		errOut := fmt.Errorf("%w: canceled before it started: %w", ErrNotRun, err)
		perr := s.recordTurnEnd(persist, t, turnEnd{err: errOut})
		s.land(item, nil, joinErrs(errOut, perr))
		return
	}

	// The trigger's first site (ADR 0020 §2): before the run, with the
	// prompt already on the path so the estimate covers what the run
	// is about to be fed. It never fires over an open approval
	// boundary — the dangling tail must stay raw for its decisions.
	s.maybeAutoCompact(ps.ctx)

	// The run flies under a cancelable child of the caller's context:
	// an Interrupt or Rollback send fells it without touching the
	// caller's own.
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
	s.runTurn(persist, rctx, item, ps.opts)
}

// runResume runs one resume over a parked approval boundary (ADR 0021
// §1): no prompt of its own — the receipt is the turn entry it writes,
// reusing the id minted when the resume was armed — and the input is
// the boundary's dangling transcript, carried raw so the recorded
// decisions can resolve their calls. The run options the parked send
// captured ride along (across a restart they are gone: run options are
// not entries), with the recorded decisions applied after them, so a
// durable decision always beats a captured option. The run's context
// is the arming call's for cancellation and the parked turn's for the
// values the arming call's lacks (resumeCtxLocked): a decider never
// strips the park rule or metadata the parked turn ran under. No compaction runs
// before a resume: there is no new prompt to cover, and the boundary
// must stay raw.
//
// The boundary must still be open when the resume runs: between arming
// and this run the caller may have branched away from the parked tail
// (the documented way out of an unwanted boundary), and resuming then
// would be a model call over nothing — a ghost turn nobody asked for.
// The turn instead ends with ErrNotPending, the same class Resume
// itself raises, and the arming retires.
func (s *Session) runResume(item workItem) {
	ctx, t := item.ps.ctx, item.ps.turn
	persist := context.WithoutCancel(ctx)
	if err := ctx.Err(); err != nil {
		errOut := fmt.Errorf("%w: resume canceled before it started: %w", ErrNotRun, err)
		perr := s.recordTurnEnd(persist, t, turnEnd{err: errOut})
		s.land(item, nil, joinErrs(errOut, perr))
		return
	}
	var (
		startErr error
		opts     []core.RunOption
	)
	s.locked(func() {
		if !s.boundaryLocked() {
			startErr = fmt.Errorf("%w: %w: the boundary this resume was armed for is gone (branched away?)", ErrNotRun, ErrNotPending)
			return
		}
		// The audit trail says a resume started before the run does: a
		// crash between the two leaves the boundary resumable, the
		// audit honest about the attempt.
		if err := s.auditResumeStartLocked(persist, t); err != nil {
			startErr = fmt.Errorf("%w: resume audit: %w", ErrNotRun, err)
			return
		}
		opts = append([]core.RunOption(nil), s.await.opts...)
		ctx = s.resumeCtxLocked(ctx)
	})
	if startErr != nil {
		s.land(item, nil, startErr)
		return
	}
	// The window carries the parked turn's values too: a resume that
	// parks again hands them on to the next one.
	persist = context.WithoutCancel(ctx)
	// A resume is as interruptible as a send's turn.
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.setRunCancel(cancel)
	defer t.clearRunCancel()
	if t.wasInterrupted() {
		cancel()
	}
	s.runTurn(persist, rctx, item, opts)
}

// runTurn is the shared body of a send's and a resume's run: the raw
// context in, the stream drained and forwarded, the turn's end
// recorded and the boundary chain run when the run parks calls
// (ADR 0021 §2). callerOpts are the run options captured for this
// turn — the resume inherits the parked send's — and after them come
// the recorded decisions, which win.
func (s *Session) runTurn(persist, ctx context.Context, item workItem, callerOpts []core.RunOption) {
	t := item.ps.turn
	var res *core.RunResult
	var err error
	inputLen := 0
	// One re-run is allowed (ADR 0020 §5): a turn failing with
	// core.ErrContextOverflow compacts — reason overflow — and tries
	// again over the shrunken path under a fresh run id. The failed
	// attempt's partial is never recorded on the active path (an
	// overflowed request produced no transcript worth keeping; the run
	// store holds the attempt's own records under its id), but its
	// ledger is: a turn entry of its own carries the attempt's run id,
	// the overflow, and the usage of the steps it completed. A second
	// failure fails the turn with both errors joined. Per-step
	// durability (ADR 0011 §7) makes "never recorded" a branch, not an
	// absence: the attempt's step messages stay on the tree, on their
	// own line of it.
	var firstOverflow error
	var sp *stepPersist
	s.locked(func() { sp = &stepPersist{startLeaf: s.leaf, resume: t.resume} })
	for attempt := 0; ; attempt++ {
		// The input is the session's context — the walk already includes
		// the prompt entry appended for this turn, or the boundary's
		// dangling calls for a resume — carried raw: the loop repairs its
		// input itself, leaving a decision's pending calls unresolved so it
		// can resolve them (the approval resume).
		input := s.rawContext()
		var decisions []core.RunOption
		s.locked(func() { decisions = s.danglingDecisionsLocked(t.resume) })
		runOpts := append([]core.RunOption(nil), callerOpts...)
		runOpts = append(runOpts, decisions...)
		// The session's steering source rides every run (ADR 0019): an
		// empty queue drains nothing, and a Send under the Steer policy
		// can queue at any moment — including after this run started.
		runOpts = append(runOpts, core.Steering(s.steerSource))
		// The transcript observer (ADR 0011 §7): each batch of messages
		// the run emits is appended as it joins, so a crash mid-turn
		// loses nothing emitted.
		runOpts = append(runOpts, s.observer(persist, sp, t.RunID()))
		// The session's identity rides every run (ADR 0024 S5), appended
		// after the caller's options and before the transcript — a later
		// core.Metadata wins (S1.1), so the session's keys win over a
		// caller's colliding thread.RunOptions(core.Metadata(...)).
		runOpts = append(runOpts, core.Metadata(s.runMetadata(t)))
		runOpts = append(runOpts, core.Messages(input...), core.RunID(t.RunID()))
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
		s.locked(func() { inputLen = max(len(runMessages(res, err))-sp.observed, 0) })
		if attempt == 0 && errors.Is(err, core.ErrContextOverflow) && s.cfg.reRunOnOverflow && !t.overflowRetried {
			firstOverflow = err
			t.overflowRetried = true
			// Branch back before compacting: the compaction must walk the
			// path the re-run will continue from — the failed attempt's
			// step messages are not part of it (ADR 0020 §5).
			branched := false
			s.locked(func() {
				branched = s.branchBackLocked(persist, sp.startLeaf) == nil
				if branched {
					// A fresh attempt: nothing observed, nothing held (the
					// failed attempt's unwritten messages die with it).
					*sp = stepPersist{startLeaf: s.leaf, resume: t.resume, late: sp.late, err: sp.err}
				}
			})
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
				s.locked(func() {
					requeue := append([]queuedSteer(nil), s.handed...)
					s.steerQueue = append(requeue, s.steerQueue...)
					s.handed = nil
					s.turnSeq++
					next := fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq)
					s.recordAttemptLocked(persist, t, firstOverflow, next)
					t.remintRunID(next, s.turnSeq)
				})
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
					// The branch-back landed (the attempt's state already
					// reset) and the compaction did not: the turn fails
					// with the overflow, and the failed attempt's repaired
					// tail is written on the active path by the turn's
					// end — the same bytes a failed overflow turn always
					// recorded.
					s.agent.Logger().Warn("thread: overflow compaction failed; no re-run",
						"session", s.header.ID, "run", t.RunID(), "err", cerr)
				}
			}
		}
		break
	}
	if t.wasInterrupted() && err != nil {
		// The interrupted partial carries completions for its dangling
		// calls — the golden text — before the repair sees it: the
		// recorded transcript stays sound model input, and the model
		// sees why a call has no answer. The completion is made on a
		// copy: the core's own RunError and its Result belong to the
		// run — its observers may still hold them.
		err = withInterruptedPartial(err)
	}
	// The transcript recorded is the last attempt's; the error reported
	// names both attempts when the re-run failed too.
	report := err
	if t.reRan && err != nil {
		report = errors.Join(firstOverflow, err)
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
	perr := s.recordTurnEnd(persist, t, turnEnd{res: res, err: err, report: report, inputLen: inputLen, chain: chain, sp: sp})
	// Steers still live when the run ended defer here: the run met a
	// StopWhen end or parked approvals without draining them, or they
	// arrived after the last drain point. The follow-ups join the send
	// queue the runner drains next.
	s.locked(s.settleSteersLocked)
	// The parked-request notifications go out before the turn is
	// decided, so a caller whose Wait returns has already seen every
	// OnRequest delivery this turn owes: durable first (the append
	// above), then delivered, then the turn completes. The same rule
	// links the chain-decided auto-resume: a caller whose Wait returns
	// can follow Turn.Next at once, so the resume is minted and linked
	// here — handed to the runner as resumeWork — not after the wake.
	if chain != nil && perr == nil {
		s.fireOnRequest(chain)
		undecided := -1
		s.locked(func() {
			if len(chain.parkedCalls) != 0 {
				return
			}
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
		})
		if undecided > 0 {
			// A chain-decided boundary that stayed open — a mirrored
			// child request still undecided beside it: the turn is
			// done, and Pending() shows what it waits for; the log line
			// is the operator's signal. Every call the chain left open
			// parked with a request, so OnRequest has already fired
			// for each.
			s.agent.Logger().Debug("thread: chain decided without completing the boundary",
				"session", s.header.ID, "run", t.RunID(), "undecided", undecided)
		}
	}
	// The turn is decided: its Wait returns here. A turn whose end the
	// storage refused says so (ErrNotPersisted), the run's result
	// riding beside the error.
	s.land(item, res, joinErrs(report, perr))
	// The trigger's second site (ADR 0020 §2): after the turn, with the
	// new measurement recorded — and after the turn is decided, so a
	// caller waiting on it never waits on a summarizer's model call.
	// The compaction still runs on the runner, before its next item: a
	// Send that arrives meanwhile queues behind it, and the next turn
	// reads the compacted context. Its hooks contain their own panics,
	// and a panic past them cannot repaint the turn — finish is
	// idempotent.
	s.maybeAutoCompact(persist)
}

// recordAttemptLocked writes the ledger of an overflow attempt that is
// about to be re-run (ADR 0020 §5): a turn entry under the attempt's
// own run id, carrying the overflow and the usage of the steps the
// attempt completed — tokens spent that the re-run's turn entry does
// not count — and naming the re-run's id, so the id is on the record
// before the re-run starts. A failure is logged: the re-run proceeds,
// the attempt's usage in no ledger. Callers hold s.mu.
func (s *Session) recordAttemptLocked(ctx context.Context, t *Turn, overflow error, reRun string) {
	te := TurnEntry{RunID: t.runID, Err: overflow.Error(), ReRun: reRun}
	if t.hasPolicy {
		te.Policy = t.policy.String()
	}
	var runErr *core.RunError
	if errors.As(overflow, &runErr) && runErr.Result != nil {
		te.StopReason = runErr.Result.StopReason
		te.Usage = runErr.Result.Usage
		te.Steps = len(runErr.Result.Steps)
	}
	t.reRan = true
	if err := s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		te.ID, te.ParentID, te.Created = id, parent, created
		return te
	}); err != nil {
		s.agent.Logger().Error("thread: overflow attempt's ledger not persisted",
			"session", s.header.ID, "run", t.runID, "err", err)
	}
}

// withInterruptedPartial returns the run's error with its partial
// transcript completed for an interrupted turn — every dangling call
// answered with the interruption text — on a copy: the *core.RunError
// the core returned and its Result are left as the run made them.
func withInterruptedPartial(err error) error {
	var runErr *core.RunError
	if !errors.As(err, &runErr) || error(runErr) != err || runErr.Result == nil {
		return err
	}
	res := *runErr.Result
	res.Messages = withInterruptedResults(runErr.Result.Messages)
	return &core.RunError{Step: runErr.Step, Err: runErr.Err, Result: &res}
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
// when the header names one. The public id is the header's — the
// reserved "weft." keys are written once, at Create or Fork (WithMeta,
// PublicID), SetInfo refuses them, and Session.Meta never lets an info
// entry override one — which is also why List's Meta filter, matching
// the header, finds the session by it. Caller-side, a run built by
// hand (not through a session) carries none of this: the session is
// the only minter.
func (s *Session) runMetadata(t *Turn) map[string]string {
	return s.identityMetadata(t.turn)
}

// identityMetadata is runMetadata for the run minted as turn n (the
// turnSeq its id carries): the session's identity pairs, without the
// caller's. The compaction marker's fallback when it has no sight of
// the run it is filed under (compactmarker.go).
func (s *Session) identityMetadata(n int) map[string]string {
	md := map[string]string{
		"weft.session.id": s.header.ID,
		"weft.turn":       strconv.Itoa(n), // the turnSeq the run id was minted from
	}
	if v := s.Meta()[publicIDKey]; v != "" {
		md[publicIDKey] = v
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
// on success, the partial riding on the *core.RunError on failure, nil
// when the run produced neither.
func runMessages(res *core.RunResult, err error) []core.Message {
	if err != nil {
		var runErr *core.RunError
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

// turnEnd is what recordTurnEnd records: the last attempt's result or
// error (whose partial transcript is the one kept), the error the
// turn reports — the same one, or both attempts' joined after a failed
// overflow re-run; err when unset — the run's input length in its own
// transcript, the decision chain's entries when the turn parked calls,
// and the attempt's per-step state.
type turnEnd struct {
	res      *core.RunResult
	err      error
	report   error
	inputLen int
	chain    *chainResult
	sp       *stepPersist
}

// recordTurnEnd closes the turn: it appends the run's new messages
// that per-step durability has not already written (normally none —
// the step observer wrote them as they joined, ADR 0011 §7), then the
// turn entry, in one atomic batch under a WithoutCancel window — the
// run is over, and its ledger must land whatever happened to the
// caller's context (ADR 0011 §4). On failure the partial transcript
// from RunError.Result is kept after core.Repair; a turn whose context
// ended — canceled, or past its deadline — is recorded as canceled;
// the calls a pending approval left unrun are recorded on the entry.
// The messages beyond the run's input are compared with what the tree
// holds for the turn, one by one, and the batch writes what is missing
// — after the last entry that matches, so a tail that differs from the
// run's final form leaves the active path (turnEndMessages). The
// chain, when the turn parked calls, carries the decision chain's
// entries — requests, audits, chain decisions — into the same Append:
// the request and the turn are atomic, all or none (ADR 0021 §1). A
// resume turn's entry reuses the receipt id minted when the resume was
// armed (it has no prompt entry of its own), and is followed by the
// audit step that says how the resume ended (ADR 0021 §2).
//
// The error returned is the persistence failure, wrapping
// ErrNotPersisted: the batch is in no tree — written nowhere, adopted
// nowhere — and the turn reports it (Turn.Wait). It is also logged
// through the agent's logger.
func (s *Session) recordTurnEnd(ctx context.Context, t *Turn, end turnEnd) error {
	res, err := end.res, end.err
	report := end.report
	if report == nil {
		report = err
	}
	var full []core.Message
	if err != nil {
		var runErr *core.RunError
		if errors.As(err, &runErr) && runErr.Result != nil {
			full = runErr.Result.Messages
			res = runErr.Result // the ledger fields (usage, steps, pending) live on the partial
		}
	} else if res != nil {
		full = res.Messages
	}
	inputLen := min(max(end.inputLen, 0), len(full))

	// The estimated tail runs before the lock: the Estimator is a
	// caller's hook, consulted outside s.mu like every other — one
	// that calls back into the Session (Leaf, Pending) must not
	// deadlock the turn's own persistence.
	var tailEst int64
	if res != nil && len(res.Steps) > 0 {
		tailStart := inputLen
		for i := len(full) - 1; i >= inputLen; i-- {
			if full[i].Role == core.RoleAssistant {
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
	sp := end.sp
	startLeaf, late := "", 0
	if sp != nil {
		// A join the tree still lacks goes first: the run's own
		// messages attach after it.
		if jErr := s.flushJoinLocked(ctx, sp); jErr != nil {
			return fail(jErr)
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
	have := make([]core.Message, len(tail))
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
	mint := func() (string, error) {
		id, idErr := s.mintCheckedLocked(entries)
		if idErr == nil && t.resume && id == t.id {
			idErr = fmt.Errorf("thread: entry id %q minted twice for one append to session %s", id, s.header.ID)
		}
		return id, idErr
	}
	for _, m := range newMsgs {
		id, idErr := mint()
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
		if teID, idErr = mint(); idErr != nil {
			return fail(idErr)
		}
	}
	te := TurnEntry{ID: teID, ParentID: parent, Created: now, RunID: t.runID, LateSteps: late}
	if t.hasPolicy {
		te.Policy = t.policy.String()
	}
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
	if report != nil {
		te.Err = report.Error()
		te.Canceled = errors.Is(report, context.Canceled) || errors.Is(report, context.DeadlineExceeded)
	}
	entries = append(entries, te)
	parent = te.ID
	if t.resume {
		// How the resume ended, on the approval trail itself (ADR 0021
		// §2): the step that pairs with the "started" entry written
		// before the run.
		id, idErr := mint()
		if idErr != nil {
			return fail(idErr)
		}
		ended := ApprovalAuditEntry{ID: id, ParentID: parent, Created: now,
			Step: StepResume, Outcome: "completed", RunID: t.runID}
		if report != nil {
			ended.Outcome, ended.Detail = "failed", report.Error()
		}
		entries = append(entries, ended)
		parent = id
	}
	// The delivered steers' receipts join the turn's batch (ADR 0019):
	// the messages themselves are already in it — the run's transcript
	// carries them beyond the input — so the receipt is what ties each
	// to its queued entry and names the run that drained it. A steer
	// the run took and never answered — it failed before another model
	// step completed — says so (ADR 0019 §5).
	steps := 0
	if res != nil {
		steps = len(res.Steps)
	}
	for i := range s.handed {
		id, idErr := mint()
		if idErr != nil {
			return fail(idErr)
		}
		entries = append(entries, ReceiptEntry{ID: id, ParentID: parent,
			Created: now, Receipt: s.handed[i].receipt,
			Status: ReceiptDelivered, RunID: t.runID,
			Unanswered: steps <= s.handed[i].step+1})
		parent = id
	}
	// handed stays set until the batch lands: a turn-end append that
	// fails — or panics — leaves these steers delivered to a run whose
	// messages are in no tree (the batch, them included, is neither
	// written nor adopted), and the settle after recordTurnEnd defers
	// them so their messages re-run instead of finishing as delivered
	// into nothing.
	handed := s.handed
	if chain := end.chain; chain != nil {
		// The chain's entries join the turn's batch: ids and parents
		// assigned here, in call order, after the turn entry (ADR 0021
		// §1–§2). Their kinds carry no prompt and no messages; they are
		// bookkeeping the model never sees.
		for _, e := range chain.entries {
			id, idErr := mint()
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
		handed[i].turn.finishAs(TurnDelivered, nil, nil)
	}
	if chain := end.chain; chain != nil {
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
	if flushErr := s.flushLocked(ctx); flushErr != nil {
		s.agent.Logger().Warn("thread: turn end flush failed",
			"session", s.header.ID, "run", t.runID, "err", flushErr)
	}
	return nil
}

// TurnOutcome is how a Turn ended — what Turn.Outcome reports. A Turn
// stands for one accepted Send, whatever became of it: a run of its
// own, a message delivered into another turn's run, a follow-up, or
// nothing at all.
type TurnOutcome int

const (
	// TurnRunning is the outcome of a turn that has not ended: queued,
	// running, or — a steer — waiting for its fate.
	TurnRunning TurnOutcome = iota
	// TurnAnswered: the turn's run completed; Wait returns its result.
	TurnAnswered
	// TurnParked: the turn's run ended at an approval boundary — a
	// success whose result lists the calls awaiting a decision
	// (RunResult.Pending). Turn.Next is the resume, once one is armed.
	TurnParked
	// TurnDelivered: a steer the running turn's run drained. The
	// message is in that run's transcript, and the answer is that
	// turn's, not this one's: Wait returns a nil result and a nil
	// error. A delivery the run never got to answer — it failed
	// before another model step — is marked on the receipt entry
	// (ReceiptEntry.Unanswered).
	TurnDelivered
	// TurnDeferred: a steer that became a follow-up turn — it met an
	// intended end, an approval boundary, or a run that ended before
	// its drain. Wait returns nil, nil; Turn.Next is the follow-up.
	TurnDeferred
	// TurnDropped: ClearQueue removed the message before it reached a
	// model. Wait returns an error wrapping ErrDropped.
	TurnDropped
	// TurnFailed: the turn ended with an error that is not a
	// cancellation — its run failed, it could not start, or its end
	// could not be persisted. Wait returns the error.
	TurnFailed
	// TurnCanceled: the turn's context ended (canceled, or past its
	// deadline), an Interrupt or Rollback send felled it, or the
	// session closed before it could run. Wait returns the error.
	TurnCanceled
)

// String returns the outcome's name: "running", "answered", "parked",
// "delivered", "deferred", "dropped", "failed" or "canceled".
func (o TurnOutcome) String() string {
	switch o {
	case TurnRunning:
		return "running"
	case TurnAnswered:
		return "answered"
	case TurnParked:
		return "parked"
	case TurnDelivered:
		return "delivered"
	case TurnDeferred:
		return "deferred"
	case TurnDropped:
		return "dropped"
	case TurnFailed:
		return "failed"
	case TurnCanceled:
		return "canceled"
	}
	return "outcome(" + strconv.Itoa(int(o)) + ")"
}

// A Turn is the receipt and the handle of one accepted Send — or of a
// resume run over a parked approval boundary, which has no prompt of
// its own: its receipt names its turn entry. It is safe for concurrent
// use.
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
	// policy is the busy policy the turn's Send was called under,
	// recorded on its turn entry; hasPolicy is false for a resume,
	// which no Send started. Written before the Turn is shared.
	policy    Policy
	hasPolicy bool
	// next is the turn that continues this one — the resume of the
	// boundary it parked, or the follow-up a deferred steer became.
	// Written once, under mu.
	next *Turn

	// interrupt is the Interrupt/Rollback bookkeeping: cancel fells
	// the run's context, interrupted marks the turn (the partial's
	// dangling calls get the golden completion), rollback names the
	// Rollback policy, and preTurn is the leaf before this turn's
	// receipt entry — where a rollback branches back to. Written by a
	// Send under mu while the run flies; read by the runner under mu
	// at the turn's end.
	cancel      context.CancelFunc
	interrupted bool
	rollback    bool
	preTurn     string
	// overflowRetried marks the turn that already spent its one
	// overflow re-run (ADR 0020 §5), reRan the turn whose re-run
	// actually started. Runner-goroutine only.
	overflowRetried bool
	reRan           bool

	// aimOpts and aimCtx are what a steer aimed at this turn inherits
	// when it cannot join the run and becomes a follow-up turn (ADR
	// 0019, amendment 2026-10-07): the run options the turn runs under — a resume's, the
	// parked send's — and the context whose values it runs on. Set
	// under s.mu when the turn goes in flight (bindLocked); read under
	// s.mu.
	aimOpts []core.RunOption
	aimCtx  context.Context

	mu         sync.Mutex
	cond       *sync.Cond
	done       chan struct{} // closed by finish
	events     []core.Event
	streamErr  error
	result     *core.RunResult
	waitErr    error
	outcome    TurnOutcome
	ended      bool
	promptDone bool
}

// ID returns the turn's receipt id: the id of the entry that makes the
// Send durable and names the turn in the tree.
//
//   - A send that started at once: its prompt entry, appended and
//     flushed before Send returned.
//   - A send accepted while the session was busy (Queue, Interrupt,
//     Rollback, and a steer's follow-up): the id its prompt entry
//     takes when its turn starts. Until then no entry has this id; the
//     accepted receipt entry — durable from the moment Send returned —
//     records it in ReceiptEntry.Turn.
//   - A steer (Send under the Steer policy on a busy session): its
//     queued receipt entry.
//   - A resume: its turn entry, written when the resume ends (a resume
//     writes no prompt of its own).
func (t *Turn) ID() string { return t.id }

// RunID returns the run's id, <session>-t<n> — the key the run store
// holds the run's records under, unique across a reopen. A turn that
// re-ran after an overflow reports the re-run's id (the run that
// produced its outcome). A steer has no run of its own: it reports the
// id minted for it at acceptance, which no run uses.
func (t *Turn) RunID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runID
}

// remintRunID re-ids the turn for its overflow re-run: a fresh
// <session>-t<n>, spent from the same counter, so the re-run's store
// records stay addressable (the failed attempt's id is in its run
// records and on its own turn entry). turn is the new number the id
// was minted from, set beside it, so the re-run's metadata names the
// turn it now is.
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

// Next returns the turn that continues this one, or nil when there is
// none: the resume the session started for the approval boundary this
// turn parked (TurnParked), or the follow-up turn a deferred steer
// became (TurnDeferred). It is nil for every other outcome, and for a
// parked turn whose boundary has not been resumed yet.
// Following Next is how a caller watches an approval flow through:
// Send's turn parks, and the resume links here whichever path armed it
// — the decision chain, a Decide or DecideSigned that completed the
// boundary, Resume — so the Turn those calls return is this same
// Turn, and its Wait is the conversation's continuation. One boundary
// resumes at most once and a steer defers at most once, so the link is
// set at most once. The link is the live Session's: after a restart
// the parked Turn value is gone, and the resume is reached through
// what Decide, Resume or Continue return.
func (t *Turn) Next() *Turn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.next
}

// setNext links the turn's continuation. The first link wins — a
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
// final element, the core's rule. A turn with no run of its own — a
// steer, a turn that never started — yields nothing.
func (t *Turn) Events() iter.Seq2[core.Event, error] {
	return func(yield func(core.Event, error) bool) {
		i := 0
		for {
			t.mu.Lock()
			for !t.ended && len(t.events) <= i {
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
			err, ended := t.streamErr, t.ended
			t.mu.Unlock()
			if !ended {
				continue
			}
			if err != nil {
				yield(nil, err)
			}
			return
		}
	}
}

// Done returns a channel that is closed when the turn ends — the
// select-shaped form of Wait:
//
//	select {
//	case <-turn.Done():
//		res, err := turn.Wait() // returns at once
//	case <-ctx.Done():
//		// stop waiting; the turn keeps running
//	}
//
// The turn is decided when the channel closes: its entries have
// landed and Outcome is final. Work the session does between turns —
// the automatic compaction — happens after, and never delays it.
func (t *Turn) Done() <-chan struct{} { return t.done }

// Wait blocks until the turn ends and returns its result and error.
// The session drains the run itself, so Wait alone observes every
// event; it is safe to call after or while ranging over Events, from
// any number of goroutines, any number of times.
//
// The result is the run's on success — including a run that ended at
// an approval boundary (RunResult.Pending) — and nil for a turn with
// no run of its own (a steer: see Outcome).
//
// The error says what kind of end it was; match with errors.Is and
// errors.As:
//
//   - *core.RunError — the run started and failed, exactly as the
//     core's Wait reports it: Result carries the partial transcript,
//     Unwrap the cause (a canceled run's is context.Canceled). After a
//     failed overflow re-run the error joins both attempts'.
//   - ErrNotRun — the turn's run never started: its context ended
//     first (the error also wraps context.Canceled or
//     context.DeadlineExceeded), its prompt entry could not be written
//     (the storage's error), or — a resume — the boundary was gone
//     (ErrNotPending) or its audit entry could not be written.
//   - ErrClosed — the session closed before the turn ran.
//   - ErrDropped — ClearQueue removed the message.
//   - ErrTurnPanicked — the session's own turn machinery, or the
//     caller's IDs or Clock function, panicked.
//   - ErrNotPersisted — the run ended but its end could not be
//     written. It is joined to the run's own error when there is one;
//     when the run succeeded the result is returned beside it.
func (t *Turn) Wait() (*core.RunResult, error) {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.result, t.waitErr
}

// WaitContext is Wait bounded by ctx: it returns the turn's result and
// error when the turn ends, or nil and ctx.Err() as soon as ctx does.
// A turn that has already ended is reported whatever ctx says — the
// answer is there, and no waiting is needed for it. Giving up the wait
// changes nothing for the turn — it keeps running, and a later Wait or
// WaitContext still returns its end. To stop the turn itself, cancel
// the context its Send was given.
func (t *Turn) WaitContext(ctx context.Context) (*core.RunResult, error) {
	select {
	case <-t.done:
		return t.Wait()
	default:
	}
	select {
	case <-t.done:
		return t.Wait()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WaitIdle blocks until the session has nothing in progress — no turn
// in flight, no between-turn work, nothing runnable left in its queue
// — and returns nil; it returns ctx.Err() when ctx ends first.
//
// A turn's Wait returns when the turn is decided. What the session
// does between turns — the automatic compaction a turn can trigger
// (ADR 0020 §2) — runs after that, before the next queued turn. A
// caller that needs the session's state after that housekeeping (a
// test, a tool that prints the tree, a pause that is not a Close)
// waits here; a caller that only sends the next message need not —
// the Send queues behind the housekeeping on its own.
//
// Idle is not empty: sends held by an open approval boundary wait for
// a decision, not for the runner, and steers restored by Open wait for
// a turn. WaitIdle returns while they are still queued.
func (s *Session) WaitIdle(ctx context.Context) error {
	for {
		s.mu.Lock()
		if !s.running {
			s.mu.Unlock()
			return nil
		}
		moved := s.runnerMovedLocked()
		s.mu.Unlock()
		select {
		case <-moved:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Outcome reports how the turn ended, or TurnRunning while it has not.
// It is what tells apart the ends Wait reports alike: a delivered
// steer from a deferred one (both nil, nil), a canceled turn from a
// failed one.
func (t *Turn) Outcome() TurnOutcome {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outcome
}

func (t *Turn) push(ev core.Event) {
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
// never ran, its error — and classifies it: a result with calls
// pending is parked, any other success answered; an error is dropped
// (ErrDropped), canceled (the context ended, the turn was interrupted,
// the session closed) or failed. Idempotent: the first call wins, so a
// late containment path cannot repaint a decided turn.
func (t *Turn) finish(res *core.RunResult, err error) {
	t.finishAs(TurnRunning, res, err)
}

// finishAs is finish with the outcome given — the steer's ends, which
// no result or error tells apart. TurnRunning asks for the
// classification finish describes.
func (t *Turn) finishAs(outcome TurnOutcome, res *core.RunResult, err error) {
	t.mu.Lock()
	if t.ended {
		t.mu.Unlock()
		return
	}
	if outcome == TurnRunning {
		switch {
		case err == nil && res != nil && len(res.Pending) > 0:
			outcome = TurnParked
		case err == nil:
			outcome = TurnAnswered
		case errors.Is(err, ErrDropped):
			outcome = TurnDropped
		case t.interrupted || errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrClosed):
			outcome = TurnCanceled
		default:
			outcome = TurnFailed
		}
	}
	t.result, t.waitErr, t.outcome, t.ended = res, err, outcome, true
	close(t.done)
	t.mu.Unlock()
	t.cond.Broadcast()
}

// isDecided reports whether the turn has landed — finish has run.
func (t *Turn) isDecided() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ended
}

// failed reports whether the turn ended with an error — the runner's
// signal that a resume work item left its boundary unresolved, so the
// settled-boundary pickup does not auto-retry it (the caller's next
// Send or Resume arms the retry).
func (t *Turn) failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ended && t.waitErr != nil
}

// cloneMessage copies a message's part slice, the one mutable field,
// so an entry's message never aliases a value the caller held.
func cloneMessage(m core.Message) core.Message {
	m.Content = append([]core.Part(nil), m.Content...)
	return m
}
