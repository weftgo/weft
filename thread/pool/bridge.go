package pool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// concluded is made by the settlement and closed when the delegate
	// retires — once a sync delegation's parked call is resolved and
	// the parent's resume armed. Wait and Cancel watch it for a
	// delegate settled with no run in flight.
	concluded chan struct{}
	// parks counts the delegate's parks: each park's phase write takes
	// the next number. A boundary read — the child's Pending and the
	// replay built from it — belongs to the park whose number was read
	// with the parked phase before it; arm takes that number along and
	// refuses once another park has come. Call ids repeat across runs:
	// a replay read at an earlier park would decide the re-parked
	// run's calls of the same id.
	parks uint64
}

const (
	phaseQueued   = iota // accepted or resuming, waiting for a slot
	phaseRunning         // the child's run is in flight
	phaseParked          // at an approval boundary, no run, no slot
	phaseSettling        // a parked child being canceled
	phaseSettled         // retired
)

// restOrConclusion is what a waiter on the delegate watches: the run
// in flight's rest, else — once settled — its conclusion; nil for a
// delegate at rest and unsettled (parked). Callers hold p.mu.
func (d *delegate) restOrConclusion() chan struct{} {
	if d.idle != nil {
		return d.idle
	}
	if d.settled {
		return d.concluded
	}
	return nil
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
func (p *Pool) park(ctx context.Context, d *delegate) error {
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
	d.phase = phaseParked
	d.parks++
	gen := d.parks
	if d.wrapper != "" {
		d.resolve = true
	}
	p.mu.Unlock()
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
// requests and waits for the next Decide.
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
	for _, childID := range order {
		rc, ok := unsettled[childID]
		if !ok {
			continue // a mirror with no delegation behind it: nothing to resume
		}
		d, err := p.delegateFor(ctx, parent, rc, wrappers[childID])
		if errors.Is(err, errSettled) {
			continue // it finished since the ledger read above (a racing pump armed it)
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
			p.arm(d, gen, replay)
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
// canceled — a child parked again since that read, and a closed pool
// arm nothing; the later park reads its own boundary and arms it.
func (p *Pool) arm(d *delegate, gen uint64, replay []thread.ApprovalDecisionEntry) {
	p.mu.Lock()
	if p.closed || d.phase != phaseParked || d.parks != gen {
		p.mu.Unlock()
		return
	}
	d.phase = phaseQueued
	// A resumed child runs on the pool's context: the call that first
	// delegated it has parked and returned, its context with it.
	ctx, cancel := context.WithCancel(p.ctx)
	r := d.begin(cancel, nil)
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		p.awaitWrapper(ctx, d)
		p.run(ctx, d, r, func(ctx context.Context) (*thread.Turn, error) {
			return d.child.ReplayDecisions(ctx, replay...)
		})
	}()
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
func (p *Pool) awaitWrapper(ctx context.Context, d *delegate) {
	p.mu.Lock()
	wait := d.wrapper != "" && !d.wrapperSeen
	p.mu.Unlock()
	if !wait {
		return
	}
	backoff := 200 * time.Microsecond
	for {
		if wrapperLanded(d.parent, d.child.ID(), d.wrapper) {
			p.mu.Lock()
			d.wrapperSeen = true
			p.mu.Unlock()
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
			backoff = min(2*backoff, 10*time.Millisecond)
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
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
		return nil, "", fmt.Errorf("%w: %s (made by wrap %q — Wrap it again under that name, or Register the session)", ErrNoAgent, childID, name)
	}
	return nil, "", fmt.Errorf("%w: %s (a Submit child — Register the session)", ErrNoAgent, childID)
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
// it from the ledger.
func (p *Pool) resolveWrapper(ctx context.Context, d *delegate, content string, isErr bool) {
	_, err := d.parent.ResolveDelegation(ctx, d.wrapper, d.child.ID(), content, isErr)
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
// is the steer's receipt turn.
//
// receiptID is a receipt of parent's. A child that is not running
// refuses with a *StateError (ErrNotRunning under errors.Is) naming
// the state it is in — accepted (queued for a slot: a steer now would
// run as the session's first turn, the task's prompt behind it),
// parked (its fate is the decision), or settled; an id the ledger
// does not hold fails with ErrUnknownReceipt.
func (p *Pool) Forward(ctx context.Context, parent *thread.Session, receiptID string, msg core.Message) (*thread.Turn, error) {
	if parent == nil {
		return nil, fmt.Errorf("thread/pool: Forward with no parent session")
	}
	p.mu.Lock()
	d, ok := p.delegates[receiptID]
	ok = ok && d.parent.ID() == parent.ID() // a receipt is its own session's
	running := ok && d.phase == phaseRunning
	p.mu.Unlock()
	if running {
		return d.child.Send(ctx, msg, thread.As(thread.Steer))
	}
	rc, found := lookup(parent, receiptID)
	if !found {
		return nil, fmt.Errorf("%w: %s in session %s", ErrUnknownReceipt, receiptID, parent.ID())
	}
	return nil, &StateError{Receipt: receiptID, State: rc.State, Orphan: !ok && !rc.Settled()}
}
