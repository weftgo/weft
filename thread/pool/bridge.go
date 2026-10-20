package pool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// A delegate is one unsettled delegation's state in a live pool: its
// two sessions and agent, the delegating call a sync delegation
// parked in the parent, and where its child is. Delegates live from
// acceptance to settlement — a parked child keeps its delegate (it
// holds no slot and has nothing to cancel, but the lineage is the
// bridge decisions cross) — and settlement retires it. After a
// restart a delegate is rebuilt from the ledger (delegateFor).
type delegate struct {
	receipt string
	wrapper string // the delegating call in the parent; empty for a Submit
	name    string // the wrap's name; empty for a Submit
	child   *thread.Session
	parent  *thread.Session
	agent   *core.Agent
	info    runInfo // the child run's depth and ancestry

	// The fields below are guarded by the pool's mutex.

	// phase is where the child is. Forward serves the running phase
	// only (ADR 0022 §8) — a steer into an idle session would run as
	// its first turn, the task's own prompt queueing behind its steer.
	phase int
	// sent records that the run in flight has its turn: set once the
	// run's start returned one, reset when a run begins. The running
	// mark comes before the start (a Forward must never read a stale
	// queued phase), so between the two the phase reads running with
	// no turn in the child's session; Forward serves only a running
	// phase whose turn is sent — a steer in that window would be the
	// session's first turn, the task's prompt queued behind it.
	sent bool
	// cancel cancels the run in flight; nil at rest.
	cancel context.CancelFunc
	// canceled records that Cancel or Close asked for the run's end.
	canceled bool
	// idle is closed when the run in flight comes to rest; nil at
	// rest. Wait watches it.
	idle chan struct{}
	// resolve is set once the child has parked under a sync
	// delegation: from then on the delegating call is parked in the
	// parent, and the delegation's end resolves it.
	resolve bool
	// wrapperSeen records that the parent's turn has been seen to
	// park the delegating call — the moment a resolution can land.
	wrapperSeen bool
	settled     bool
	// concluded is made when the delegate starts to settle — a Cancel
	// moving a parked child to settling, else the settlement itself —
	// and closed when the delegate retires, once a sync delegation's
	// parked call is resolved and the parent's resume armed (or by a
	// cancel that could not close the child's boundary, the child
	// parked again). Wait and Cancel watch it while no run is in
	// flight; nil when nothing is settling.
	concluded chan struct{}
	// parks counts the delegate's parks: each park's phase write takes
	// the next number. A boundary read — the child's Pending and the
	// replay built from it — belongs to the park whose number was read
	// with the parked phase before it; arm takes that number along and
	// refuses once another park has come. Call ids repeat across runs:
	// a replay read at an earlier park would decide the re-parked
	// run's calls of the same id.
	parks uint64
	// nest is the resume run in flight's hold for a nested resolution:
	// set for the life of every resumed run (arm), nil otherwise. A
	// child that is itself a parent — its boundary the sync delegation
	// of a grandchild — resumes through that grandchild: the replay
	// reaches the grandchild, and the child's own resume is the one its
	// delegating call's resolution starts (resolveWrapper), under this
	// run's context, handed over here.
	nest *nestWait
	// sl is the slot of the run in flight; nil at rest. A resolution
	// lends it to the parent's resume (resolveWrapper).
	sl *slot
	// ending records that the delegation has begun to end its own
	// sync delegations (endNested): from then on none of them is armed
	// again, and none resolves its delegating call in this child — the
	// child's session is about to close, and a resume would run in it.
	ending bool
	// forwards holds the cancels of the contexts Forward sent steers
	// under: a steer that defers runs as a follow-up turn on its own
	// context, outside the run's, so a Cancel or Close that ends the run
	// ends those too (stopLocked) — a follow-up must never work after
	// its run gave its slot up. Released when the delegate retires.
	forwards []context.CancelFunc
}

// stopLocked is a Cancel or Close asking for the end of the run in
// flight: it records that the pool canceled it and returns what cancels
// it — the run's context and every forwarded steer's — for the caller
// to call (under the lock or after it). Callers hold p.mu.
func (d *delegate) stopLocked() func() {
	d.canceled = true
	run, fw := d.cancel, d.forwards
	d.forwards = nil
	return func() {
		if run != nil {
			run()
		}
		for _, f := range fw {
			f()
		}
	}
}

// A nestWait is a resumed run's side of a nested resolution: the
// run's context the grandchild resolves the child's delegating call
// under (so the child's resume is this run's work — its cancellation),
// the run's slot (the grandchild's is lent to it for the resume, so
// the resume never works without one), the resume turn that
// resolution started, and a wake for the run waiting on it. ctx, slot
// and turn are guarded by the pool's mutex.
//
// mu serializes the resolutions landing on the hold — one lend, one
// resolution, one give-back at a time — and closed, under mu, marks
// the run over (rest): a resolution after that records under its own
// context, the parent's next resume not this run's to follow.
type nestWait struct {
	ctx  context.Context
	slot *slot
	turn *thread.Turn
	wake chan struct{} // buffered 1: a resolution landed, a grandchild parked or retired
	// waiting is set while the run waits on its delegations
	// (awaitNested), guarded by the pool's mutex: a sync grandchild is
	// armed only then (arm), so its resolution always lands on a run
	// that follows the resume it starts — never on a parked child, whose
	// session would resume with nobody to follow it.
	waiting bool

	mu     sync.Mutex
	closed bool
}

// signal wakes the run waiting on n, without blocking.
func (n *nestWait) signal() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// upstreamNestLocked returns the nested-resolution hold of the run
// that resumes d's parent session, when d's parent is itself a child
// of this pool being resumed; nil otherwise. Callers hold p.mu.
func (p *Pool) upstreamNestLocked(d *delegate) *nestWait {
	up, ok := p.byChild[d.parent.ID()]
	if !ok || up.child != d.parent {
		return nil
	}
	return up.nest
}

// parentEndingLocked reports whether d is a sync delegation whose
// parent session is itself a child of this pool that is ending — a
// Cancel settling it, its run ending its delegations (endNested), or
// already settled: d is not armed again, and its end resolves nothing
// in that parent. Callers hold p.mu.
func (p *Pool) parentEndingLocked(d *delegate) bool {
	if d.wrapper == "" {
		return false // an async child follows D4: its parent's end is not its own
	}
	up, ok := p.byChild[d.parent.ID()]
	if !ok || up.child != d.parent {
		return false
	}
	return up.ending || up.phase == phaseSettling || up.phase == phaseSettled
}

// parentLocked returns the delegate of this pool that runs d's parent
// session, when d is a sync delegation made from a pool child; nil
// otherwise. Callers hold p.mu.
func (p *Pool) parentLocked(d *delegate) *delegate {
	if d.wrapper == "" {
		return nil
	}
	up, ok := p.byChild[d.parent.ID()]
	if !ok || up.child != d.parent {
		return nil
	}
	return up
}

// heldByLiveParentLocked reports whether d is a sync delegation under
// a child of this pool not at rest parked — queued, running, waiting on
// its delegations, being canceled — directly or through parked sync
// delegations: under Close that ancestor is canceled, and its end, not
// Close, ends d (see Close) — at d's boundary, or through the parked
// children between them, each canceled at its own. Callers hold p.mu.
func (p *Pool) heldByLiveParentLocked(d *delegate) bool {
	for e := d; ; {
		up := p.parentLocked(e)
		if up == nil {
			return false
		}
		switch up.phase {
		case phaseQueued, phaseRunning, phaseSettling:
			return true
		case phaseParked:
			e = up
		default:
			return false
		}
	}
}

// parkedBelowLocked returns the parked sync delegations under d's
// child, directly or through parked ones: what Close left to d's end
// (heldByLiveParentLocked), for d's park to close with its own
// session. Callers hold p.mu.
func (p *Pool) parkedBelowLocked(d *delegate) []*delegate {
	var out []*delegate
	for _, e := range p.delegates {
		if e.parent == d.child && e.wrapper != "" && e.phase == phaseParked {
			out = append(out, e)
			out = append(out, p.parkedBelowLocked(e)...)
		}
	}
	return out
}

// nestedBoundary reports whether a run of d's that came to a boundary
// (or resumed nothing) has to wait on its own sync delegations before
// it can park: nothing in the child is offered — the boundary is open
// only on delegating calls, whose children's requests were decided or
// lapsed in the child — or some sync delegation of the child's is
// still a delegate (awaitNested parks only once all of them are
// parked).
func (p *Pool) nestedBoundary(d *delegate) bool {
	p.mu.Lock()
	live := p.delegatingLocked(d)
	p.mu.Unlock()
	return live || len(d.child.Pending()) == 0
}

// delegatingLocked reports whether a sync delegation made from d's
// child session is still a delegate of this pool — a grandchild whose
// resolution is what resumes d's child. Callers hold p.mu.
func (p *Pool) delegatingLocked(d *delegate) bool {
	for _, e := range p.delegates {
		if e.parent == d.child && e.wrapper != "" {
			return true
		}
	}
	return false
}

// workingLocked reports whether a sync delegation made from d's child
// session is queued, running or settling — anything but parked at its
// boundary: the child's own boundary is not final while one is, for
// that one's end resolves a delegating call of it. Callers hold p.mu.
func (p *Pool) workingLocked(d *delegate) bool {
	for _, e := range p.delegates {
		if e.parent == d.child && e.wrapper != "" && e.phase != phaseParked {
			return true
		}
	}
	return false
}

// awaitNested is a resumed run whose replay started nothing, or any
// run that came to a boundary its own sync delegations hold open (a
// grandchild's request decided or lapsed in the child before it
// parked, a sibling delegation still at work — nestedBoundary): the
// child's boundary is open only on its own sync delegations — the
// decision went on down to a grandchild (ADR 0022 §7, a nested park
// decided on the top parent) — so the child's resume is the one the
// grandchild's resolution starts. The run waits for it, holding no
// slot (the resolution lends it the grandchild's), and returns it to
// be followed like any resume. A grandchild in a restarted process has
// no bridge yet: the child session is pumped first, which rebuilds and
// arms it. A grandchild that parks again mirrors its new requests into
// the child, and once no other sync delegation of the child's is
// queued, running or settling — every one left parked — the wait ends
// with nothing to follow: the child's boundary is open on them now,
// and the run parks the child, which mirrors them on up to its own
// parent. When no delegation of the child's is left and none handed a
// resume over, the boundary is whatever the replay finds now — nil
// when there is nothing to resume (concludeStale's case, a resume that
// did not survive). A canceled run returns its context's error; its
// conclusion ends its delegations first and waits for them to rest
// (endNested), as a sync call's turn waits for its child.
func (p *Pool) awaitNested(ctx context.Context, d *delegate, n *nestWait, sl *slot) (*thread.Turn, error) {
	// Waiting from here: the child's sync delegations may be armed (arm
	// refuses them while it is not), so the pump below and every one
	// after it can resume them.
	p.mu.Lock()
	n.waiting = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		n.waiting = false
		p.mu.Unlock()
	}()
	p.attach(d.child)
	pump := func() {
		if err := p.pump(ctx, d.child); err != nil && ctx.Err() == nil {
			d.agent.Logger().Warn("thread/pool: a nested child's delegations not pumped",
				"child", d.child.ID(), "receipt", d.receipt, "err", err)
		}
	}
	pump()
	yielded := false
	regain := func() {
		if yielded {
			yielded = false
			_ = sl.regain() // a canceled run holds nothing; its error is the run's
		}
	}
	defer regain()
	for {
		offered := len(d.child.Pending()) > 0
		p.mu.Lock()
		t, live, working := n.turn, p.delegatingLocked(d), p.workingLocked(d)
		if t != nil {
			n.turn = nil // taken: a later resolution hands its own over
		}
		if t != nil || (offered && !working) || !live {
			// Done waiting, under the lock arm reads the flag by: a
			// grandchild armed before this is working (read above), one
			// after it is refused and left for the child's next wait.
			n.waiting = false
		}
		p.mu.Unlock()
		if t != nil {
			return t, nil // the resolution lent the slot: regain finds it held
		}
		if offered && !working {
			// A grandchild parked again, and every other one is parked
			// too (or over): the run parks the child, which works no
			// more — the slot is not taken back for it. While a sibling
			// grandchild still works, the child waits on: parking it
			// then would leave that sibling running under a parked
			// child, its end resolving a call a Cancel or Close of the
			// child has closed the session of.
			if yielded {
				yielded = false
				sl.unyield() // no longer waiting, and not taken back
			}
			return nil, nil
		}
		if !live {
			regain() // the replay may start the resume: the slot first
			return d.child.ReplayDecisions(ctx)
		}
		if !yielded {
			yielded = true
			sl.yield()
		}
		select {
		case <-n.wake:
			// A resolution landed, a grandchild parked or retired, or a
			// decision reached one while this run was not waiting (arm
			// refused it): read the boundary again, arming what it holds.
			pump()
		case <-ctx.Done():
			return nil, ctx.Err() // the run's conclusion ends the delegations (endNested)
		}
	}
}

// nestedEndWait bounds how long an ending child waits for its own
// sync delegations to rest (endNested): their runs are canceled, and a
// handler that ignores its context is abandoned by its tool's timeout,
// so this is the backstop for one with none. Pool.nestedEnd holds it
// (tests shorten it).
const nestedEndWait = 30 * time.Second

// endNested ends the sync delegations made from d's child session when
// that child's delegation ends while they live — its resume canceled
// (Cancel of the child's own receipt, the pool's Close), Cancel of the
// child parked at its boundary, any conclusion that would close the
// child's session — and waits for each to rest, so every grandchild
// settles in the child's ledger before the child concludes and its
// session closes: a running or queued grandchild is canceled and
// settles canceled, and one parked at an approval is canceled at its
// boundary — under the pool's Close too: Close leaves a parked child
// parked only under a parent at rest (see Close), and d is ending,
// not parking. From the
// moment it starts no grandchild of d is armed again and none resolves
// its delegating call in d's child (parentEndingLocked): the child is
// ending, and a resume would run in the session being closed. Async
// children (Submit, an Async wrap) are not touched: they follow D4.
//
// The wait is bounded by nestedEndWait. A grandchild whose run has not
// ended by then — a handler ignoring its context, with no Timeout — is
// settled canceled by the pool itself, in the child's ledger, while
// that session is still open, its stop naming the bound; its run is
// abandoned, still counted by Close's drain, and its own end, when it
// comes, records nothing more (a settlement is once per receipt).
func (p *Pool) endNested(d *delegate) {
	p.mu.Lock()
	d.ending = true
	bound := p.nestedEnd
	var live []*delegate
	for _, e := range p.delegates {
		if e.parent == d.child && e.wrapper != "" {
			live = append(live, e)
		}
	}
	p.mu.Unlock()
	if len(live) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), bound)
	defer cancel()
	// Under Close too: Close canceled the running ones already, and a
	// parked one is canceled at its boundary here — d is ending (not
	// parking), and nothing could resume a child under a parent that
	// settles.
	for _, e := range live {
		if err := p.cancelLive(ctx, e); err != nil {
			d.agent.Logger().Warn("thread/pool: an ending child's delegation not canceled",
				"child", d.child.ID(), "receipt", e.receipt, "err", err)
		}
	}
	for _, e := range live {
		for {
			if err := p.waitLive(ctx, e); err != nil {
				p.abandon(d, e, bound)
				break
			}
			// A grandchild whose canceled run reached its boundary first
			// settles canceled there (park); one that rests parked anyway
			// — a cancel landing between two of its runs — is canceled
			// again, at its boundary.
			p.mu.Lock()
			again := p.delegates[e.receipt] == e && e.phase == phaseParked
			p.mu.Unlock()
			if !again {
				break
			}
			if err := p.cancelLive(ctx, e); err != nil {
				d.agent.Logger().Warn("thread/pool: an ending child's delegation not canceled",
					"child", d.child.ID(), "receipt", e.receipt, "err", err)
				break
			}
		}
	}
}

// cancelLive is Cancel for a delegate the pool holds, by its value:
// a queued or running one is canceled, a parked one canceled at its
// boundary, one already settling waited for. A delegate retired
// meanwhile is done.
func (p *Pool) cancelLive(ctx context.Context, e *delegate) error {
	for {
		p.mu.Lock()
		if p.delegates[e.receipt] != e {
			p.mu.Unlock()
			return nil
		}
		switch e.phase {
		case phaseQueued, phaseRunning:
			stop := e.stopLocked()
			p.mu.Unlock()
			stop()
			return nil
		case phaseParked:
			e.phase = phaseSettling
			e.concluded = make(chan struct{})
			p.mu.Unlock()
			return p.cancelParked(ctx, e)
		}
		idle := e.restOrConclusion()
		p.mu.Unlock()
		if idle == nil {
			return nil
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// waitLive waits until a delegate the pool holds is retired or at
// rest parked (Close's case), or ctx ends.
func (p *Pool) waitLive(ctx context.Context, e *delegate) error {
	for {
		p.mu.Lock()
		var idle chan struct{}
		if p.delegates[e.receipt] == e {
			idle = e.restOrConclusion()
		}
		p.mu.Unlock()
		if idle == nil {
			return nil
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// abandon settles a grandchild that did not rest within endNested's
// bound: canceled, by the pool, in its parent's ledger while that
// session is still open; its mirrors there are denied. The run stays
// in flight, abandoned: its own end records nothing more, and its
// delegating call resolves nothing (its parent is ending).
func (p *Pool) abandon(d, e *delegate, bound time.Duration) {
	ledger := context.WithoutCancel(p.ctx)
	stop := fmt.Sprintf("canceled: its run did not end within %v of its parent's end; the pool settled it and abandoned the run", bound)
	if !p.settle(ledger, e, Canceled, stop) {
		return // it settled on its own meanwhile
	}
	defer p.retire(e)
	d.agent.Logger().Warn("thread/pool: an ending child's delegation did not rest; settled canceled, its run abandoned",
		"child", d.child.ID(), "receipt", e.receipt, "bound", bound)
	if err := e.parent.DenyMirrored(ledger, e.child.ID(), endedReason(Canceled)); err != nil {
		d.agent.Logger().Error("thread/pool: an abandoned delegation's requests still read pending; Recover denies them",
			"receipt", e.receipt, "child", e.child.ID(), "err", err)
	}
}

const (
	phaseQueued   = iota // accepted or resuming, waiting for a slot
	phaseRunning         // the child's run is in flight
	phaseParked          // at an approval boundary, no run, no slot
	phaseSettling        // a parked child being canceled
	phaseSettled         // retired
)

// restOrConclusion is what a waiter on the delegate watches: the run
// in flight's rest, else — once a Cancel moved it to settling, or it
// settled — its conclusion; nil for a delegate at rest and parked.
// Callers hold p.mu.
func (d *delegate) restOrConclusion() chan struct{} {
	if d.idle != nil {
		return d.idle
	}
	return d.concluded
}

// label is the name the model-visible texts call the delegation by.
func (d *delegate) label() string {
	if d.name == "" {
		return unnamed
	}
	return d.name
}

// The texts a parked delegating call resolves with — the same
// contract the wrapped tool's own errors keep (see wrap.go's table).

func (d *delegate) canceledText() string {
	return (&core.ToolError{Code: CodeSubagentCanceled,
		Message: fmt.Sprintf(canceledMessage, d.label())}).Error()
}

func (d *delegate) failureText(cause string) string {
	return (&core.ToolError{Code: core.CodeSubagentFailed,
		Message: fmt.Sprintf(failedPlainMessage, d.label(), cause)}).Error()
}

func (d *delegate) runFailureText(err error) string {
	var re *core.RunError
	if errors.As(err, &re) {
		return (&core.ToolError{Code: core.CodeSubagentFailed,
			Message: fmt.Sprintf(failedMessage, d.label(), re.Step, re.Err)}).Error()
	}
	return d.failureText(err.Error())
}

func (d *delegate) unmirroredText(err error) string {
	return (&core.ToolError{Code: core.CodeSubagentFailed,
		Message: fmt.Sprintf(unmirroredMessage, d.label(), err)}).Error()
}

// errCanceledAtPark is park's answer for a run Cancel had asked to end
// that came to its approval boundary: the delegation settled canceled
// there instead of parking.
var errCanceledAtPark = fmt.Errorf("thread/pool: canceled at its approval boundary: %w", context.Canceled)

// errNothingOffered is park's answer for a boundary with nothing left
// to offer: its requests were decided or lapsed since the run read
// them, and the run waits on its delegations instead of parking.
var errNothingOffered = errors.New("thread/pool: nothing to offer at the boundary")

// unmirroredError is a park the pool could not record on the parent:
// the delegation fails with it instead of parking unseen.
type unmirroredError struct{ err error }

func (e *unmirroredError) Error() string {
	return "thread/pool: nested approval requests not mirrored: " + e.err.Error()
}
func (e *unmirroredError) Unwrap() error { return e.err }

// mirrorCallID namespaces a child's call id for the parent's tree:
// call ids are unique per step, not per run (ADR 0014's lineage note),
// so the child's call_1 and the parent's own call_1 collide unless the
// mirror carries the child's name. Decisions address the namespaced
// form in the parent; the pool rewrites it to the bare id when
// replaying into the child, where the bare id is the key.
func mirrorCallID(child, callID string) string {
	return child + "/" + callID
}

// mirrorRequests builds the parent-side mirror entries for a child's
// parked calls (ADR 0022 §7): Child names the session the calls park
// in, Wrapper the delegating call they park under, the CallID is the
// child-namespaced form decisions address, and every field a decision
// or a signature needs rides along verbatim — the child's own expiry
// included, so the parent's sweep lapses the mirror when the child's
// request lapses.
func mirrorRequests(reqs []thread.Request, child, wrapper string) []thread.ApprovalRequestEntry {
	out := make([]thread.ApprovalRequestEntry, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, thread.ApprovalRequestEntry{
			CallID: mirrorCallID(child, r.CallID), Tool: r.Tool, Args: r.Args, ArgsSHA256: r.ArgsSHA256,
			RunID: r.RunID, Reason: r.Reason, Expiry: r.Expiry,
			Child: child, Wrapper: wrapper,
		})
	}
	return out
}

// park brings a child whose run ended at an approval boundary to
// rest (ADR 0022 §7): its pending requests are mirrored onto the
// parent — durably, before anything else — the receipt records
// parked, and the delegate stays as the bridge a decision resumes
// through. The slot is released by the run that called it.
//
// A park the parent cannot record must not stand: an unmirrored
// child would wait on requests nobody is offered, under a receipt
// that reads running. The delegation fails instead — settled failed
// with the cause, the sync call a tool error — and the error returns.
// The child's boundary stays in its own file, undecided.
//
// A run Cancel asked to end (not Close) that reached its boundary
// before its cancellation stopped it does not park: its boundary is
// closed as Cancel closes a parked child's, the delegation settles
// canceled, and park returns errCanceledAtPark. Close's cancellation
// leaves it parked, as Close leaves every parked child.
func (p *Pool) park(ctx context.Context, d *delegate) error {
	if len(d.child.Pending()) == 0 {
		// Nothing to offer: the boundary's requests were decided or
		// lapsed since the run read them. A park now would mirror
		// nothing — the delegating call above would read as a plain,
		// decidable call — so the run goes on instead (run).
		return errNothingOffered
	}
	if err := p.mirror(ctx, d); err != nil {
		um := &unmirroredError{err}
		p.conclude(ctx, d, Failed, um.Error(), d.unmirroredText(err), true)
		return um
	}
	if _, err := d.parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: d.receipt, Status: thread.PoolParked, Child: d.child.ID(),
	}); err != nil {
		// The mirrors are durable and are what a decision resumes by;
		// only the ledger's word for the wait is stale.
		d.agent.Logger().Error("thread/pool: parked receipt not recorded; the ledger reads running",
			"receipt", d.receipt, "child", d.child.ID(), "err", err)
	}
	if p.testHookParking != nil {
		p.testHookParking(d)
	}
	p.mu.Lock()
	d.parks++
	gen := d.parks
	if d.canceled && (!p.closed || p.parentEndingLocked(d)) {
		// Cancel asked for the run's end and the run came to its
		// boundary instead (its context was canceled too late to stop
		// it): the cancel stands — the boundary is closed at once, as
		// Cancel of a parked child closes it, and the delegation
		// settles canceled. A first run's delegating call has not
		// parked: it returns SUBAGENT_CANCELED, and nothing resolves.
		// Close's cancel stands the same way under a parent that is
		// ending: nothing could resume the child under it.
		d.phase = phaseSettling
		d.concluded = make(chan struct{})
		p.mu.Unlock()
		if err := p.cancelParked(ctx, d); err != nil {
			// The boundary could not be closed: the child rests parked
			// and decidable (cancelParked put it back), its delegating
			// call parked under it.
			p.mu.Lock()
			if d.wrapper != "" {
				d.resolve = true
			}
			p.mu.Unlock()
			d.agent.Logger().Error("thread/pool: a canceled child parked; its boundary could not be closed",
				"receipt", d.receipt, "child", d.child.ID(), "err", err)
			return nil
		}
		return errCanceledAtPark
	}
	d.phase = phaseParked
	if d.wrapper != "" {
		d.resolve = true
	}
	// A Close that ran while this child was parking took it for a
	// running one — canceled its context, left its session open — and
	// its census of parked children is done. Close what Close would
	// have: the session's writer lease must not outlive the pool, or
	// the process that resumes the child finds it locked. Under the
	// same lock Close sets closed by, so exactly one of them closes it.
	// Two refinements, Close's census rule: a child whose parent is a
	// pool child still at work is left to that parent's end (which
	// cancels it at its boundary, or parks and closes it here); and the
	// parked sync children Close left to this child are closed with it,
	// now it parks instead of ending.
	closing, leftToParent := p.closed, p.closed && p.heldByLiveParentLocked(d)
	var below []*delegate
	if closing && !leftToParent {
		below = p.parkedBelowLocked(d)
	}
	// A parent being resumed through this child (awaitNested) waits on
	// its delegations: this park mirrored new requests into it, so its
	// boundary is open on them now and it must park in turn, mirroring
	// them on up — else they reach no session anyone decides.
	if n := p.upstreamNestLocked(d); n != nil {
		n.signal()
	}
	p.mu.Unlock()
	if leftToParent {
		return nil
	}
	if closing {
		for _, e := range append(below, d) {
			if err := e.child.Close(context.WithoutCancel(ctx)); err != nil {
				e.agent.Logger().Warn("thread/pool: parked child session not closed",
					"child", e.child.ID(), "receipt", e.receipt, "err", err)
			}
		}
		return nil
	}
	// The mirrors were decidable before the phase read parked: a
	// decision recorded in between met a pump that saw the run still
	// in flight and left it — and no later pump comes unless someone
	// calls. Read the parent's verdicts again now the phase is parked:
	// a decision recorded before this read is armed here, one recorded
	// after it meets a pump that reads parked. arm is once per park
	// whichever of them gets there — and only for this park: a pump
	// may arm the resume first, and the child run and park again
	// before this arm, which gen then refuses.
	p.rearm(ctx, d, gen)
	return nil
}

// rearm reads the parent's verdicts for a child that has just come to
// rest at park gen and arms its resume when every pending call holds
// one — the decision a pump skipped while the child was not yet (or
// no longer) parked. A read error is logged and left for the next
// Decide, which pumps the child.
func (p *Pool) rearm(ctx context.Context, d *delegate, gen uint64) {
	mirrors, err := d.parent.MirroredRequests(ctx)
	if err != nil {
		d.agent.Logger().Error("thread/pool: parked child's decisions not re-read; the next Decide pumps it",
			"receipt", d.receipt, "child", d.child.ID(), "err", err)
		return
	}
	if replay, ready, _ := readiness(d.child.ID(), d.child.Pending(), heldMirrors(mirrors)); ready {
		if p.testHookArming != nil {
			p.testHookArming(d)
		}
		p.arm(d, gen, replay)
	}
}

// mirrorKey keys a mirrored request: its namespaced call id and the
// child run that parked it.
type mirrorKey [2]string

// heldMirrors indexes the parent's mirrors in force by mirrorKey.
func heldMirrors(mirrors []thread.MirroredRequest) map[mirrorKey]thread.MirroredRequest {
	held := make(map[mirrorKey]thread.MirroredRequest, len(mirrors))
	for _, m := range mirrors {
		held[mirrorKey{m.Request.CallID, m.Request.RunID}] = m
	}
	return held
}

// readiness reads a parked child's boundary against the parent's
// mirrors: ready when every call pending in the child holds an
// effective verdict in the parent, with the decisions to replay under
// the child's own call ids; unmirrored when a pending call has no
// mirror at all (a crash between the child's park and the mirror).
func readiness(childID string, pending []thread.Request, held map[mirrorKey]thread.MirroredRequest) (replay []thread.ApprovalDecisionEntry, ready, unmirrored bool) {
	ready = true
	for _, r := range pending {
		m, ok := held[mirrorKey{mirrorCallID(childID, r.CallID), r.RunID}]
		if !ok {
			ready, unmirrored = false, true
			continue
		}
		if !m.Decided {
			ready = false
			continue
		}
		for _, de := range m.Decisions {
			de.CallID = r.CallID // the child's own key
			replay = append(replay, de)
		}
	}
	return replay, ready, unmirrored
}

// mirror appends the child's pending requests the parent holds no
// live mirror for — every one on a first park, the new ones on a
// re-park, the lost ones after a crash between the child's park and
// the mirror.
func (p *Pool) mirror(ctx context.Context, d *delegate) error {
	pending := d.child.Pending()
	if len(pending) == 0 {
		return nil
	}
	held, err := d.parent.MirroredRequests(ctx)
	if err != nil {
		return err
	}
	have := map[[2]string]bool{}
	for _, m := range held {
		have[[2]string{m.Request.CallID, m.Request.RunID}] = true
	}
	var missing []thread.Request
	for _, r := range pending {
		if !have[[2]string{mirrorCallID(d.child.ID(), r.CallID), r.RunID}] {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	_, err = d.parent.AppendApprovalRequests(ctx, mirrorRequests(missing, d.child.ID(), d.wrapper)...)
	return err
}

// Register attaches an agent to a child session the pool holds none
// for — the restart hook (ADR 0022 §7): a parked child's boundary
// outlives the process, and resuming it needs the agent that runs it,
// which no file carries. Children a Wrap made need no call: the
// wrap's name, recorded in the child's header metadata, is looked up
// among the pool's wrapped agents. Register is for the rest — Submit
// children — and for wraps renamed between runs; it takes precedence
// over the wrap name. The registration is dropped when the session's
// delegation settles; one made after that is dropped when the pool
// next reads that settled receipt for the session — Wait on it,
// Recover of its parent, or a pump (Decide, a decision the parent
// records) reaching the session through a mirror still in force. An
// empty session id or a nil agent is an error.
// Register alone recovers nothing: Recover does, and Decide resumes.
func (p *Pool) Register(sessionID string, agent *core.Agent) error {
	if sessionID == "" {
		return fmt.Errorf("thread/pool: Register with an empty session id")
	}
	if agent == nil {
		return fmt.Errorf("thread/pool: Register %s with a nil agent", sessionID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionAgents[sessionID] = agent
	return nil
}

// Decide records decisions over a parent session's nested approvals
// and arms the children they complete (ADR 0022 §7). It does not wait
// for any child to run.
//
// The decisions are recorded in the parent first, through its own
// Decide — validation, quorum, audit and expiry all live there, and a
// RequireSigned parent refuses them with thread.ErrSignatureRequired
// (use DecideSigned). Then the pump runs: every child parked on this
// parent whose currently pending calls all hold an effective verdict
// in the parent — decided, or lapsed past their expiry and denied by
// the parent's sweep — is queued to resume. With no decisions Decide
// is the pump alone.
//
// Decisions the parent session records by a path of its own reach the
// children the same way, without a call: the pool watches every
// parent it delegates from (and every one handed to Decide,
// DecideSigned or Recover), and a decision recorded for a mirrored
// request — Session.Decide or DecideSigned used directly, the denial
// an interrupting Send (thread.Interrupt, thread.Rollback) gives a
// parked boundary, a request lapsing past its expiry when the session
// is next touched — runs the pump on a pool goroutine. An interrupted
// child is therefore resumed with the denial ("DENIED: interrupted by
// a newer message"), runs to its end and settles; its delegating
// call, denied by the same interrupt, is no longer there to resolve,
// and the child's answer stays on its receipt. The watch is the live
// Session's: after a restart nothing is watched until Recover or
// Decide is called for the parent, which is also what pumps whatever
// was decided in between.
//
// A resumed child is pool work like its first run: on a pool-owned
// goroutine and context, behind the queue, holding a slot while it
// runs, its receipt recording running again. There the parent's
// decisions for the child's pending calls are replayed into the child
// session (thread.Session.ReplayDecisions: the decider's identity
// kept, Via "parent") — only for calls pending in the child now, so
// the decisions of an earlier park are never replayed — and the child
// resumes exactly as a top-level session does (ADR 0021 §1). When it
// ends, the delegation completes: the receipt settles, and a sync
// delegation's parked call resolves with the child's answer, which
// resumes the parent. A child that parks again mirrors its new
// requests and waits for the next Decide. A child that is itself
// parked on a sync delegation of its own — a grandchild's request
// mirrored up through it — resumes through that grandchild: the
// replay reaches the grandchild, whose answer resolves the child's
// delegating call, and the child's resume that starts is this
// delegation's run, followed to its end like any other. One decision
// on the top parent resumes the whole chain, after a restart too
// (the child's own delegations are pumped from the ledger). A
// grandchild that parks again while the child waits on it mirrors its
// new request into the child, and once every other delegation of the
// child's is parked too — none still running its approved call — the
// child parks in turn, mirroring it on up: every park of a chain, at
// any depth, is offered on the top parent and decided there. The new request keeps the namespaced call
// id (call ids repeat across a child's runs) and names the new
// occurrence by its run; it supersedes the earlier one, and a decision
// recorded for the earlier one does not decide it. A Cancel or Close
// of the child while it waits — or a Cancel of it parked on the
// grandchild — ends the grandchild first and waits for it to rest, so
// it settles in the child's ledger (the bound and its outcome are
// Cancel's).
//
// A child parks only on a boundary that offers something. Under a
// request expiry (thread.RequestExpiry, inherited down the chain) a
// grandchild's request can lapse in the child before the child parks
// — the child's step still running another call — and the child's run
// then ends with nothing to offer: it does not park but waits on its
// delegations as above, the lapsed grandchild resumed with the denial
// and its answer resuming the child. A sync grandchild resumes only
// while the child above it waits on it; a decision reaching it
// otherwise (the child's own expiry sweep while the child is parked)
// waits for the child's resume, which the pump starts when the child's
// own boundary allows.
//
// The delegating call a child is parked under is never decided: it
// is not in Pending, and a decision naming it fails with
// thread.ErrDelegated — it completes with the child's answer. The
// parent session's own decision chain is bound by the same rule: no
// grant and no live Approver is consulted for a delegating call when
// it parks, whatever they would match — the call parks, and the
// child's mirrored requests are what there is to decide. Decisions
// for the parent's own, ordinary calls may ride in the same batch; a
// resume they arm is the parked turn's Next, as with Session.Decide.
//
// Decide returns once the decisions are recorded and the ready
// children queued; ctx bounds that, not the children. Follow a child
// with Wait, Receipts, or the parent's own continuation (Turn.Next
// once Wait reports the receipt settled). The error is every failure
// joined (errors.Join): the parent's refusal of the decisions —
// nothing is recorded and nothing pumped — or, per child that could
// not be armed, why: ErrNoAgent for one the pool cannot reopen. The
// other children are armed regardless.
func (p *Pool) Decide(ctx context.Context, parent *thread.Session, ds ...thread.Decision) error {
	if parent == nil {
		return fmt.Errorf("thread/pool: Decide with no parent session")
	}
	if err := p.open(ctx); err != nil {
		return err
	}
	p.attach(parent)
	if len(ds) > 0 {
		// Recording only: a nested boundary is the children's to
		// resume, and the parent arms nothing of its own while a
		// delegating call is still pending.
		if _, err := parent.Decide(ctx, ds...); err != nil {
			return err
		}
	}
	return p.pump(ctx, parent)
}

// DecideSigned is Decide for one signed decision: sd is verified and
// recorded by the parent session (thread.Session.DecideSigned, every
// check its own), then the pump runs as in Decide.
func (p *Pool) DecideSigned(ctx context.Context, parent *thread.Session, sd thread.SignedDecision) error {
	if parent == nil {
		return fmt.Errorf("thread/pool: DecideSigned with no parent session")
	}
	if err := p.open(ctx); err != nil {
		return err
	}
	p.attach(parent)
	if _, err := parent.DecideSigned(ctx, sd); err != nil {
		return err
	}
	return p.pump(ctx, parent)
}

// attach installs the pool's notification on a parent session: a
// decision the session records for a mirrored request by a path of
// its own — an interrupting Send's denial, an expiry, a Decide made
// on the session directly — runs the pump, so the decision reaches
// the child without waiting for the next Pool.Decide. One
// notification per pool and session; attaching again replaces it.
func (p *Pool) attach(parent *thread.Session) {
	parent.WatchMirrors(p, func(log *slog.Logger) {
		// Called under the session's lock: the pump reads the session,
		// so it runs on a pool goroutine, joined to the drain.
		go p.pumpAsync(parent, log)
	})
}

// pumpAsync is the pump as pool-owned background work: counted in
// the drain, on the pool's context, its failures logged — nobody
// waits on it. A closed pool pumps nothing.
func (p *Pool) pumpAsync(parent *thread.Session, log *slog.Logger) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.wg.Add(1)
	p.mu.Unlock()
	defer p.wg.Done()
	if err := p.pump(p.ctx, parent); err != nil && p.ctx.Err() == nil {
		log.Error("thread/pool: a decision recorded on the parent did not reach its children; Pool.Decide retries",
			"session", parent.ID(), "err", err)
	}
}

// open reports whether the pool takes work under ctx.
func (p *Pool) open(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	return nil
}

// pump arms every child parked on parent whose boundary is complete.
// The walk is entry-driven — the parent's mirrors in force, the
// ledger's unsettled receipts, each child's own pending calls — so it
// works the same live and after a restart.
func (p *Pool) pump(ctx context.Context, parent *thread.Session) error {
	mirrors, err := parent.MirroredRequests(ctx) // resolves the parent's expiry first
	if err != nil {
		return err
	}
	held := heldMirrors(mirrors)
	var order []string
	wrappers := map[string]string{}
	seen := map[string]bool{}
	for _, m := range mirrors {
		if !seen[m.Request.Child] {
			seen[m.Request.Child] = true
			order = append(order, m.Request.Child)
			wrappers[m.Request.Child] = m.Wrapper
		}
	}
	if len(order) == 0 {
		return nil
	}
	unsettled := map[string]Receipt{}
	for _, r := range Receipts(parent) {
		if !r.Settled() {
			unsettled[r.Child] = r
		} else {
			p.forgetSettled(r.Child)
		}
	}
	var errs []error
	var ups []*thread.Session
	for _, childID := range order {
		rc, ok := unsettled[childID]
		if !ok {
			continue // a mirror with no delegation behind it: nothing to resume
		}
		d, err := p.delegateFor(ctx, parent, rc, wrappers[childID])
		if errors.Is(err, errSettled) {
			continue // it finished since the ledger read above (a racing pump armed it)
		}
		if errors.Is(err, errForeign) {
			continue // another pool's child: that pool's watch pumps it
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p.mu.Lock()
		parked, gen := d.phase == phaseParked, d.parks
		p.mu.Unlock()
		if !parked {
			continue // queued, running or being canceled: not the pump's (a park re-reads the verdicts itself)
		}
		replay, ready, unmirrored := readiness(childID, d.child.Pending(), held)
		if unmirrored {
			// A pending call the parent was never shown (a crash
			// between the child's park and the mirror): show it now.
			if err := p.mirror(ctx, d); err != nil {
				errs = append(errs, fmt.Errorf("thread/pool: child session %s: %w", childID, &unmirroredError{err}))
			}
		}
		if ready {
			if up := p.arm(d, gen, replay); up != nil && !slices.Contains(ups, up) {
				ups = append(ups, up)
			}
		}
	}
	// A child decided under a parent at rest parked waits for that
	// parent's resume (arm): pump the parent's own parent, whose
	// verdicts may complete the parent's boundary now — a lapse in the
	// parent leaves nothing there to decide, and without this pump the
	// chain would wait for the top session's next touch.
	for _, up := range ups {
		if err := p.pump(ctx, up); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// arm queues a parked child's resume: a pool goroutine that waits for
// the parent's delegating call to be parked (a sync delegation's
// resolution has nowhere to land before), takes a slot, replays the
// decisions and runs the child to its next rest. gen is the park the
// caller read the child's boundary at, its replay built from that
// read (a parked phase and d.parks read together, then Pending). A
// child that is not parked — already armed by a racing pump, being
// canceled — a child parked again since that read, a sync child whose
// parent child is ending (endNested cancels it at its boundary) and a
// closed pool arm nothing; the later park reads its own boundary and
// arms it.
func (p *Pool) arm(d *delegate, gen uint64, replay []thread.ApprovalDecisionEntry) (up *thread.Session) {
	p.mu.Lock()
	if p.closed || d.phase != phaseParked || d.parks != gen || p.parentEndingLocked(d) {
		p.mu.Unlock()
		return nil
	}
	if pd := p.parentLocked(d); pd != nil && (pd.nest == nil || !pd.nest.waiting) {
		// A sync child of a pool child resumes only while that parent's
		// run waits on its delegations (awaitNested): its answer resolves
		// a call of the parent's, and the resume that starts must be a
		// run's to follow. A decision reaching it otherwise — the
		// parent's own expiry sweep lapsing its mirror while the parent
		// is parked, queued, or still in the step that delegated — waits
		// for that wait, whose pump arms it. A parent at rest parked is
		// resumed for it when its own boundary allows: the caller pumps
		// the parent's parent.
		if pd.phase == phaseParked {
			up = pd.parent
		} else if pd.nest != nil {
			pd.nest.signal() // its next wait pumps again
		}
		p.mu.Unlock()
		return up
	}
	d.phase = phaseQueued
	// A resumed child runs on the pool's context: the call that first
	// delegated it has parked and returned, its context with it. The
	// hold is in place before the replay (begin): the replay is what
	// reaches a grandchild, which may resolve the child's delegating
	// call before the replay returns.
	ctx, cancel := context.WithCancel(p.ctx)
	r := d.begin(cancel, nil)
	r.resume = true
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		p.awaitWrapper(ctx, d)
		p.run(ctx, d, r, func(ctx context.Context) (*thread.Turn, error) {
			return d.child.ReplayDecisions(ctx, replay...)
		})
	}()
	return nil
}

// awaitWrapper holds a sync delegation's resume until the parent's
// turn has parked the delegating call. The child's requests are
// mirrored while that turn is still running — it parks a moment
// later, when the call returns — and a decision can arrive in
// between; a child resumed and finished inside that window would
// resolve a call that is not yet pending, and the parent would then
// park on it for good. It returns when the parent's ledger shows the
// call parked, when the delegating turn ended without parking it
// (canceled, failed: there is nothing to resolve), or when ctx ends.
func (p *Pool) awaitWrapper(ctx context.Context, d *delegate) bool {
	backoff := 200 * time.Microsecond
	for {
		if p.wrapperParked(d) {
			return true
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
			backoff = min(2*backoff, 10*time.Millisecond)
		case <-ctx.Done():
			timer.Stop()
			return p.wrapperParked(d)
		}
	}
}

// wrapperParked reports, without waiting, whether a resolution of d's
// delegating call can land now: d delegates by no call (a Submit),
// the parent's turn has been seen to land, or it has landed now.
func (p *Pool) wrapperParked(d *delegate) bool {
	p.mu.Lock()
	seen := d.wrapper == "" || d.wrapperSeen
	p.mu.Unlock()
	if seen {
		return true
	}
	if !wrapperLanded(d.parent, d.child.ID(), d.wrapper) {
		return false
	}
	p.mu.Lock()
	d.wrapperSeen = true
	p.mu.Unlock()
	return true
}

// wrapperLanded reports whether the turn that delegated to child has
// ended: the first turn entry after the child's first mirror is that
// turn's — one runner per session — and it either lists the wrapper
// among its pending calls (parked) or does not (the turn died; the
// call was never parked and never will be).
func wrapperLanded(parent *thread.Session, child, wrapper string) bool {
	mirrored := false
	for _, e := range parent.Entries() {
		switch e := e.(type) {
		case thread.ApprovalRequestEntry:
			if e.Child == child && e.Wrapper == wrapper {
				mirrored = true
			}
		case thread.TurnEntry:
			if mirrored {
				return true
			}
		}
	}
	// No mirror at all — a boundary the child's expiry settled before
	// anything was mirrored, a parent branch that left the entries
	// behind: nothing to wait for.
	return !mirrored
}

// delegateFor returns the bridge for an unsettled receipt's child:
// the live delegate when the pool holds it, or one rebuilt from the
// ledger after a restart (ADR 0022 §7) — the child session reopened
// under the registered or wrap-named agent with the parent's approval
// policy, at rest and parked (the only state a child of a dead
// process can be resumed from; Recover settles the others). It fails
// with ErrNoAgent when no agent can be found: the resume refuses
// loudly rather than guess.
func (p *Pool) delegateFor(ctx context.Context, parent *thread.Session, rc Receipt, wrapper string) (*delegate, error) {
	p.mu.Lock()
	if d, ok := p.byChild[rc.Child]; ok {
		p.mu.Unlock()
		return d, nil
	}
	// From the miss on, a settlement of this child marks the lookup
	// (markRetired): a racing pump — Decide's own beside the watch's —
	// can rebuild the child, arm it and run it to its end inside this
	// lookup, dropping its registration, and its settlement must not be
	// answered with ErrNoAgent or a bridge to a delegation that is over.
	// The mark is per child: settlements of other children never send a
	// lookup round again, so a lookup is one ledger read, one agent
	// lookup and at most one Open, with no retry loop.
	look := p.resolving[rc.Child]
	if look == nil {
		look = &lookupMark{}
		p.resolving[rc.Child] = look
	}
	look.n++
	p.mu.Unlock()
	defer p.doneResolving(rc.Child, look)

	// No live delegate: the ledger is the truth, read after the miss.
	// rc may be a step behind it — a delegation that settled between
	// the caller's Receipts read and this lookup has retired its
	// delegate and its Register'd agent (settle records the ledger
	// first), and reopening it would report ErrNoAgent for a child
	// that just finished, or resume one that is over.
	if receiptSettled(parent, rc.ID) {
		p.forgetSettled(rc.Child)
		return nil, errSettled
	}
	if p.testHookResolving != nil {
		p.testHookResolving(rc)
	}
	agent, name, err := p.agentForSession(ctx, parent, rc.Child)
	if err != nil {
		if p.settledWhileResolving(rc.Child, look) {
			return nil, errSettled
		}
		if errors.Is(err, ErrNoAgent) && p.foreign(parent, rc.Child, name) {
			return nil, errForeign
		}
		return nil, err
	}
	child, err := thread.Open(ctx, parent.Storage(), rc.Child, agent, thread.InheritApprovals(parent))
	if err != nil {
		return nil, fmt.Errorf("thread/pool: child session %s not reopened: %w", rc.Child, err)
	}
	d := &delegate{
		receipt: rc.ID, wrapper: wrapper, name: name,
		child: child, parent: parent, agent: agent,
		info:    p.lineageInfo(ctx, parent),
		phase:   phaseParked,
		resolve: wrapper != "",
	}
	p.mu.Lock()
	if existing, ok := p.byChild[rc.Child]; ok {
		p.mu.Unlock()
		_ = child.Close(context.WithoutCancel(ctx)) // lost the race: one bridge per child
		return existing, nil
	}
	if p.closed {
		p.mu.Unlock()
		_ = child.Close(context.WithoutCancel(ctx))
		return nil, ErrClosed
	}
	if look.retired {
		// Settled since the ledger read — the lookup succeeded anyway
		// (a wrap-name agent no settlement drops, a registration made
		// again): installing d would keep a finished delegation's
		// bridge for the life of the pool.
		delete(p.sessionAgents, rc.Child)
		p.mu.Unlock()
		_ = child.Close(context.WithoutCancel(ctx))
		return nil, errSettled
	}
	p.byChild[rc.Child] = d
	p.delegates[rc.ID] = d
	p.mu.Unlock()
	return d, nil
}

// A lookupMark is delegateFor's in-flight mark on a child: how many
// lookups hold it, and whether the child's delegation settled since
// the first of them missed. Marks live only while a lookup runs.
type lookupMark struct {
	n       int
	retired bool
}

// markRetired records, under p.mu, that childID's delegation settled
// and was retired, for the lookups in flight on it.
func (p *Pool) markRetired(childID string) {
	if look := p.resolving[childID]; look != nil {
		look.retired = true
	}
}

// doneResolving releases a lookup's mark.
func (p *Pool) doneResolving(childID string, look *lookupMark) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if look.n--; look.n == 0 && p.resolving[childID] == look {
		delete(p.resolving, childID)
	}
}

// settledWhileResolving reports whether the child's delegation settled
// since the lookup missed, dropping a registration made since.
func (p *Pool) settledWhileResolving(childID string, look *lookupMark) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if look.retired {
		delete(p.sessionAgents, childID)
	}
	return look.retired
}

// forgetSettled drops the registration of a child whose delegation
// the ledger holds settled: one made after the settlement retired the
// delegate (Register racing a fast child's end) would otherwise stay
// for the life of the pool. Called wherever the pool reads a settled
// receipt for a child it holds no delegate for.
func (p *Pool) forgetSettled(childID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sessionAgents[childID]; !ok {
		return
	}
	if _, live := p.byChild[childID]; live {
		return // the delegate's own settlement drops it
	}
	delete(p.sessionAgents, childID)
}

// errSettled is delegateFor's answer for a delegation the ledger
// already holds settled: there is nothing to arm or reattach.
var errSettled = errors.New("thread/pool: the delegation is settled")

// receiptSettled reads the parent's ledger now for receipt id.
func receiptSettled(parent *thread.Session, id string) bool {
	r, ok := lookup(parent, id)
	return ok && r.Settled()
}

// lineageInfo rebuilds the run info of a child of parent after a
// restart: its depth, from the live delegate that runs parent or from
// the lineage headers above it. The ancestry of agents is not
// recoverable from files — a rebuilt child's cycle guard starts at
// its own agent; the depth limit still bounds what it can start.
func (p *Pool) lineageInfo(ctx context.Context, parent *thread.Session) runInfo {
	p.mu.Lock()
	if pd, ok := p.byChild[parent.ID()]; ok {
		p.mu.Unlock()
		return runInfo{depth: pd.info.depth + 1}
	}
	p.mu.Unlock()
	depth := 1
	st := parent.Storage()
	for at := parent.Lineage(); at.Session != "" && depth < p.maxDepth; {
		depth++
		h, _, _, err := st.Load(ctx, at.Session)
		if err != nil || h.Lineage == nil {
			break
		}
		at = *h.Lineage
	}
	return runInfo{depth: depth}
}

// agentForSession finds the agent a child session resumes under, and
// the wrap name it was made under: Register's session register first,
// then the wrap-name register by the child header's metadata.
func (p *Pool) agentForSession(ctx context.Context, parent *thread.Session, childID string) (*core.Agent, string, error) {
	h, _, _, err := parent.Storage().Load(ctx, childID)
	if err != nil {
		return nil, "", fmt.Errorf("thread/pool: child session %s: %w", childID, err)
	}
	name := h.Meta[metaAgent]
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.sessionAgents[childID]; ok {
		return a, name, nil
	}
	if a, ok := p.nameAgents[name]; ok && name != "" {
		return a, name, nil
	}
	if name != "" {
		return nil, name, fmt.Errorf("%w: %s (made by wrap %q — Wrap it again under that name, or Register the session)", ErrNoAgent, childID, name)
	}
	return nil, "", fmt.Errorf("%w: %s (a Submit child — Register the session)", ErrNoAgent, childID)
}

// errForeign is delegateFor's answer for a child another pool holds:
// this pool has no delegate, registration or wrap for it, and a pool
// watching the same parent has one. Its callers skip the receipt — it
// is that pool's to resume — with no error and no log.
var errForeign = errors.New("thread/pool: another pool's child")

// foreign reports whether childID, a child of parent made under the
// wrap name (empty for a Submit child), is another pool's: this pool
// cannot resume it, and a pool watching the same parent can. Several
// pools may delegate from one parent; each one's pump walks every
// receipt of the parent's ledger, and a decision recorded there pumps
// them all. No lock is held across pools: each is asked under its own.
func (p *Pool) foreign(parent *thread.Session, childID, name string) bool {
	if p.canResume(childID, name) {
		return false
	}
	for _, o := range parent.MirrorWatchers() {
		if q, ok := o.(*Pool); ok && q != p && q.canResume(childID, name) {
			return true
		}
	}
	return false
}

// canResume reports whether the pool, open, holds what resumes
// childID: a live delegate, a registration, or the wrap it was made
// under.
func (p *Pool) canResume(childID, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	if _, ok := p.byChild[childID]; ok {
		return true
	}
	if _, ok := p.sessionAgents[childID]; ok {
		return true
	}
	_, ok := p.nameAgents[name]
	return ok && name != ""
}

// resolveWrapper completes a sync delegation's parked parent call: a
// decision entry resolving it with the delegation's outcome, which
// the parent's own auto-resume machinery turns into the core's
// Resolve on the resumed run (ADR 0021 §1, ADR 0022 §7). The
// session's delegation path, not the caller's Decide: the resolution
// is recorded with Via "child" and lands under RequireSigned too.
// A call that is no longer there to resolve — the parent's turn died
// before parking it, an interrupting Send denied it, a Branch left it
// behind — is reported and left: the receipt already holds the
// outcome. Any other failure leaves the call parked; Recover resolves
// it from the ledger. A parent that is itself a child of this pool
// and is ending — canceled, settling — takes no resolution: it is
// ending its delegations (endNested), and a resolution would resume
// the very session that is being closed.
//
// When the parent session is itself a child of this pool whose resume
// is waiting on this resolution (awaitNested), the resolution is
// recorded under that run's context — the parent's resume is that
// run's work — and the resume it starts is handed to it, with a slot:
// this run's, lent straight over, or — when this run holds none (it
// was canceled while queued, it panicked, it was parked) — one queued
// for on the waiting run's behalf before the resolution is recorded.
// The resume works from its first model call under a slot, never
// beside the bound (the pool's bound holds through the hand-off). A
// resolution that starts no resume — another delegation of the
// parent's still open — gives the slot back. When that run was
// canceled the resolution is not recorded: the parent is ending, and
// its delegating call ends with it.
func (p *Pool) resolveWrapper(ctx context.Context, d *delegate, content string, isErr bool) {
	p.mu.Lock()
	ending := p.parentEndingLocked(d)
	n := p.upstreamNestLocked(d)
	mine := d.sl
	var rctx context.Context
	var to *slot
	if n != nil {
		rctx, to = n.ctx, n.slot
	}
	p.mu.Unlock()
	if ending {
		d.agent.Logger().Debug("thread/pool: delegating call left open: its parent is ending",
			"wrapper", d.wrapper, "child", d.child.ID(), "receipt", d.receipt)
		return
	}
	if n == nil {
		_, err := d.parent.ResolveDelegation(ctx, d.wrapper, d.child.ID(), content, isErr)
		d.reportResolution(err)
		return
	}
	// No slot of this run's to lend, and the run the resume belongs to
	// waits without one: queue for one on that run's behalf — outside
	// the hold, so a sibling grandchild resolving meanwhile never waits
	// on the hold while it holds the slot this queue waits for.
	spare := false
	if rctx != nil && !mine.holds() && to.waitsSlotless() {
		spare = p.sem.enqueue().wait(rctx) == nil
	}
	n.mu.Lock()
	p.mu.Lock()
	rctx, to = n.ctx, n.slot
	p.mu.Unlock()
	if n.closed || rctx == nil {
		// The run is over (or not started): the parent's next resume is
		// not its to follow.
		n.mu.Unlock()
		if spare {
			p.sem.release()
		}
		_, err := d.parent.ResolveDelegation(ctx, d.wrapper, d.child.ID(), content, isErr)
		d.reportResolution(err)
		return
	}
	lent := mine.lend(to)
	if spare {
		if lent {
			p.sem.release() // this run's own slot went over after all
		} else {
			lent = to.adopt() // back to the queue when to no longer waits slotless
		}
	}
	turn, err := d.parent.ResolveDelegation(rctx, d.wrapper, d.child.ID(), content, isErr)
	if turn != nil && err == nil {
		p.mu.Lock()
		if n.turn == nil {
			n.turn = turn
		}
		p.mu.Unlock()
	} else if lent {
		to.unlend()
	}
	n.mu.Unlock()
	n.signal()
	if err != nil && rctx.Err() != nil {
		d.agent.Logger().Debug("thread/pool: delegating call left open: its parent's resume was canceled",
			"wrapper", d.wrapper, "child", d.child.ID(), "receipt", d.receipt)
		return
	}
	d.reportResolution(err)
}

// reportResolution logs a delegating call's resolution that did not
// land.
func (d *delegate) reportResolution(err error) {
	switch {
	case err == nil:
	case errors.Is(err, thread.ErrNotPending):
		d.agent.Logger().Warn("thread/pool: delegating call no longer pending; the outcome is on the receipt",
			"wrapper", d.wrapper, "child", d.child.ID(), "receipt", d.receipt, "err", err)
	default:
		d.agent.Logger().Error("thread/pool: delegating call not resolved; Recover resolves it from the ledger",
			"wrapper", d.wrapper, "child", d.child.ID(), "receipt", d.receipt, "err", err)
	}
}

// cancelParked is Cancel for a child at an approval boundary: its
// pending calls are denied in its own session — never to run on a
// later approval — nothing is resumed, and the delegation is
// concluded canceled. The caller moved the delegate to phaseSettling.
func (p *Pool) cancelParked(ctx context.Context, d *delegate) error {
	var err error
	if p.testHookCanceling != nil {
		err = p.testHookCanceling(d)
	}
	if err == nil {
		err = d.child.CancelDelegated(ctx, cancelDenyReason)
	}
	if err != nil {
		// The boundary could not be closed: the child stays parked and
		// decidable rather than half-canceled. A decision recorded
		// while it read settling met a pump that skipped it, and no
		// later pump comes unless someone calls: read the verdicts
		// again, as park does. The park is the same one — a failed
		// cancel left its boundary as it was — so gen stands.
		p.mu.Lock()
		d.phase = phaseParked
		gen := d.parks
		// Release whoever waited on the settlement that did not come:
		// they read the child parked again.
		if d.concluded != nil {
			close(d.concluded)
			d.concluded = nil
		}
		p.mu.Unlock()
		p.rearm(context.WithoutCancel(ctx), d, gen)
		return fmt.Errorf("thread/pool: child session %s: %w", d.child.ID(), err)
	}
	p.conclude(context.WithoutCancel(ctx), d, Canceled, "canceled while parked at an approval", d.canceledText(), true)
	return nil
}

// Forward steers a running child with msg, explicitly (ADR 0022 §8,
// ADR 0019 §7): the message is sent to the child's session under the
// Steer policy — delivered at the run's next steering drain point, or
// deferred to a follow-up turn when the run ends on an intended stop
// or an open approval boundary (the follow-up runs before the
// delegation's session closes, the receipt keeping the answer of the
// run it steered); the child's session records the steer's receipt
// either way. Nothing is ever forwarded implicitly. The returned Turn
// is the steer's receipt turn. A follow-up runs on ctx — and ends when
// Cancel or Close ends the child's run: a steer the canceled run never
// drained is abandoned, its follow-up ending without a model call, so
// it never works after the run gave its slot up.
//
// receiptID is a receipt of parent's. A child that is not running
// refuses with a *StateError (ErrNotRunning under errors.Is) naming
// the state it is in — accepted (queued for a slot: a steer now would
// run as the session's first turn, the task's prompt behind it),
// parked (its fate is the decision), or settled; an id the ledger
// does not hold fails with ErrUnknownReceipt. Forward needs the run's
// turn in flight, not only its running receipt: a run that holds its
// slot and reads running but has not yet sent its turn into the child
// session — the first turn's prompt, or a resume — refuses as the
// state it is leaving (accepted before the first turn, parked before
// a resume); the refusal changes nothing and a retry a moment later
// steers the run.
func (p *Pool) Forward(ctx context.Context, parent *thread.Session, receiptID string, msg core.Message) (*thread.Turn, error) {
	if parent == nil {
		return nil, fmt.Errorf("thread/pool: Forward with no parent session")
	}
	p.mu.Lock()
	d, ok := p.delegates[receiptID]
	ok = ok && d.parent.ID() == parent.ID() // a receipt is its own session's
	running := ok && d.phase == phaseRunning && d.sent
	if ok && d.phase == phaseRunning && !d.sent {
		// The run is starting: its turn is not in the child's session
		// yet. Refused as the state it is leaving — accepted before the
		// first turn, parked before a resume's — retryable at once.
		state := Accepted
		if d.parks > 0 {
			state = Parked
		}
		p.mu.Unlock()
		return nil, &StateError{Receipt: receiptID, State: state}
	}
	var sctx context.Context
	if running {
		// The steer's context, which a deferred steer's follow-up turn
		// runs on: the caller's, ended too when Cancel or Close ends the
		// child's run (stopLocked) — the follow-up must not work on after
		// the run it steered gave its slot up.
		var cancel context.CancelFunc
		sctx, cancel = context.WithCancel(ctx)
		if d.canceled {
			cancel() // the run is ending already
		} else {
			d.forwards = append(d.forwards, cancel)
		}
	}
	p.mu.Unlock()
	if running {
		return d.child.Send(sctx, msg, thread.As(thread.Steer))
	}
	rc, found := lookup(parent, receiptID)
	if !found {
		return nil, fmt.Errorf("%w: %s in session %s", ErrUnknownReceipt, receiptID, parent.ID())
	}
	return nil, &StateError{Receipt: receiptID, State: rc.State, Orphan: !ok && !rc.Settled()}
}
