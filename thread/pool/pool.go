// Package pool provides bounded concurrent child runs for thread
// sessions (ADR 0022): one process-wide semaphore with FIFO fairness,
// subagent tools whose children hold a slot, child sessions linked to
// the parent session and call, and receipts recording every
// delegation's journey. The pool imports the root module and
// weft/thread, never weft/store (ADR 0011 §1's module rule, one level
// down), and changes nothing in the core: it is a tool middleware,
// session entries, and pool-owned goroutines.
package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// ErrClosed is returned by Submit on a pool whose Close has run: an
// accepted delegation outlives the pool that would run it, so a
// closing pool takes no new work.
var ErrClosed = errors.New("thread/pool: pool is closed")

// ErrNotRunning is returned by Cancel and Forward for a receipt no
// running child answers to — settled before the call, parked at an
// approval (its fate is the decision, not a cancellation or a steer),
// or never this pool's (a receipt id from another process is not
// distinguishable from a settled one, and all refuse the same way).
var ErrNotRunning = errors.New("thread/pool: receipt is not running")

// Pool is a process-wide bound on concurrent child runs (ADR 0022 §1,
// D2): every child a pool starts — wrapped or submitted — holds one
// of its max slots for the duration of the child's run. Admission is
// FIFO (the semaphore's wait queue); a session's fan-out is already
// bounded by its agent's Parallelism, so the pool adds no per-session
// quota — the process bound is the one knob. A Pool is safe for
// concurrent use. The zero value is not usable; New constructs.
type Pool struct {
	max int
	sem chan struct{}
	ids func() string

	mu        sync.Mutex
	closed    bool
	delegates map[string]*delegate // by receipt id — Cancel's key
	byChild   map[string]*delegate // by child session id — the bridge's key

	// sessionAgents holds agents registered for child sessions —
	// creation-time delegates and Register calls (the restart hook,
	// ADR 0022 §7); nameAgents holds the agents the pool's wraps
	// stand for, keyed by wrap name, which Wrap-made children record
	// in their header metadata.
	sessionAgents map[string]*weft.Agent
	nameAgents    map[string]*weft.Agent

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// An Option configures a Pool at New.
type Option interface{ applyPool(*Pool) }

type idsOption func() string

func (o idsOption) applyPool(p *Pool) { p.ids = o }

// IDs sets the function minting child session ids, in place of the
// thread module's own time-sortable ids — the determinism knob tests
// and examples use. It mints session ids only; entry ids inside every
// session stay the session's own.
func IDs(id func() string) Option { return idsOption(id) }

// New returns a pool admitting at most max concurrent child runs. The
// bound is the pool's reason to exist, so max below 1 panics — an
// unbounded pool is no pool (DeerFlow's max_running, the field's
// evidence for the shape).
func New(max int, opts ...Option) *Pool {
	if max < 1 {
		panic(fmt.Sprintf("thread/pool: New called with max=%d; the bound must be at least 1", max))
	}
	p := &Pool{
		max:           max,
		sem:           make(chan struct{}, max),
		delegates:     map[string]*delegate{},
		byChild:       map[string]*delegate{},
		sessionAgents: map[string]*weft.Agent{},
		nameAgents:    map[string]*weft.Agent{},
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	for _, o := range opts {
		if o != nil {
			o.applyPool(p)
		}
	}
	return p
}

// Receipt is a delegation's handle and what is known of it: the
// acceptance entry's id, the state the receipt records last (ADR 0022
// §4's machine: accepted → running → exactly one of done, failed,
// canceled, capped — pending children sit at running while their
// boundary is open), the child session, and on settlement the child's
// stop text — its answer, when the state is done.
type Receipt struct {
	ID    string
	State string
	Child string
	Stop  string
}

// Submit hands a task to a child session of parent and returns at
// once (ADR 0022 §2): the child session is created in the parent's
// storage with a lineage naming the parent, the acceptance receipt is
// durable in the parent session before Submit returns, and the child
// runs on a pool-owned context — independent of the submitting
// caller's cancellation by design (D4), canceled explicitly by Cancel
// or Close. agent must not be nil; the receipt's Child names the
// session to watch (the v0.4 Watcher) or reopen.
func (p *Pool) Submit(ctx context.Context, parent *thread.Session, agent *weft.Agent, prompt string) (*Receipt, error) {
	r, _, err := p.submit(ctx, parent, agent, prompt, "", "", 0, true)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Cancel cancels the child running for receiptID — the one explicit
// cancellation an async child has (D4). The child's session records
// the canceled turn and the receipt settles canceled; a receipt that
// is queued but not yet running is dequeued the same way. A receipt
// with no running child — settled, parked at an approval, or never
// this pool's — fails with ErrNotRunning: a parked child's fate is
// the decision that resumes it, not a cancellation.
func (p *Pool) Cancel(receiptID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.delegates[receiptID]
	if !ok || d.phase == phaseParked || d.cancel == nil {
		return fmt.Errorf("%w: %s", ErrNotRunning, receiptID)
	}
	d.cancel()
	return nil
}

// Close stops taking work and drains: every running child is canceled
// — async ones on their pool-owned contexts, sync ones through their
// registered cancels, their turns settling with their parents — and
// Close waits for the pool's own goroutines to settle their receipts,
// every child's session recording its final state before the process
// gives up the storage (DeerFlow's gateway drain). ctx bounds the
// wait; its error returns with the drain still progressing. A second
// Close returns nil — the drain is already done or underway.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.cancel()
	for _, d := range p.delegates {
		if d.cancel != nil {
			d.cancel()
		}
	}
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	// An already-done ctx must not mask a finished drain: the drain
	// wins when both are ready at once.
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// The waiter goroutine exits on its own once the canceled
		// children settle; it holds no reference the pool keeps.
		return ctx.Err()
	}
}

// Receipts returns parent's pool receipts, in acceptance order, each
// at the state its entries record last — the ledger read back after a
// restart the same as live (entries are the truth; the pool holds no
// receipt state of its own). A session that never delegated returns
// nil.
func Receipts(parent *thread.Session) []Receipt {
	if parent == nil {
		return nil
	}
	var order []string
	byID := map[string]*Receipt{}
	for _, e := range parent.Entries() {
		pr, ok := e.(thread.PoolReceiptEntry)
		if !ok {
			continue
		}
		if pr.Receipt == "" {
			r := &Receipt{ID: pr.ID, State: pr.Status, Child: pr.Child}
			byID[pr.ID] = r
			order = append(order, pr.ID)
			continue
		}
		if r, ok := byID[pr.Receipt]; ok {
			r.State = pr.Status
			r.Stop = pr.Stop
		}
	}
	out := make([]Receipt, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// outcome is what a sync delegation's child came to: its answer, its
// error, or the requests it left pending at an approval boundary
// (ADR 0021 §1 — the pool bridges them, ADR 0022 §7).
type outcome struct {
	answer   string
	err      error
	pending  int
	requests []thread.Request
}

// submit is the one delegation path under Wrap and Submit: it creates
// the child session, records acceptance, and either runs the child
// inline (sync — the delegating call waits) or on a pool goroutine
// (async — the caller holds the receipt). callID names the
// delegating tool call in the child's lineage when there was one;
// depth is the delegation chain's depth for the deadlock guard. The
// mutex-checked closed state refuses new work before anything is
// created.
func (p *Pool) submit(ctx context.Context, parent *thread.Session, agent *weft.Agent, prompt, callID, wrapName string, depth int, async bool) (*Receipt, outcome, error) {
	if parent == nil {
		return nil, outcome{}, fmt.Errorf("thread/pool: delegation with no parent session")
	}
	if agent == nil {
		return nil, outcome{}, fmt.Errorf("thread/pool: delegation with nil agent")
	}
	if err := ctx.Err(); err != nil {
		return nil, outcome{}, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, outcome{}, ErrClosed
	}
	p.mu.Unlock()

	opts := []thread.SessionOption{thread.WithLineage(parent.ID(), callID)}
	if wrapName != "" {
		// The wrap's name is the resume key a restarted process
		// re-Wraps into existence (ADR 0022 §7).
		opts = append(opts, thread.WithMeta(map[string]string{"pool_agent": wrapName}))
	}
	if p.ids != nil {
		opts = append(opts, thread.IDs(p.ids))
	}
	child, err := thread.Create(ctx, parent.Storage(), agent, opts...)
	if err != nil {
		return nil, outcome{}, fmt.Errorf("thread/pool: child session: %w", err)
	}
	accept, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Status: thread.PoolAccepted, Child: child.ID(), Call: callID, Prompt: prompt,
	})
	if err != nil {
		// The acceptance is the contract (durable before the caller
		// holds the receipt); without it the child is an empty session
		// nothing points at — remove it best-effort and refuse.
		_ = thread.Delete(context.WithoutCancel(ctx), parent.Storage(), child.ID())
		return nil, outcome{}, fmt.Errorf("thread/pool: acceptance receipt: %w", err)
	}

	// The child's run context: pool-owned for an async child (it
	// survives the submitting turn — D4), the delegating call's for a
	// sync one (it cancels with the parent — ADR 0014's rule). Cancel
	// reaches either through the registry. Only the branch taken may
	// build its context: a pool-owned WithCancel dropped on the sync
	// path would stay registered on the pool's context until Close,
	// one leaked child per sync delegation.
	var runCtx context.Context
	var cancel context.CancelFunc
	if async {
		runCtx, cancel = context.WithCancel(p.ctx)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	runCtx = withDepth(runCtx, depth)
	d := &delegate{cancel: cancel, child: child, parent: parent, agent: agent,
		wrapper: callID, receipt: accept.ID}
	p.mu.Lock()
	if p.closed { // Close ran between the check and the registration
		p.mu.Unlock()
		cancel()
		_, _ = parent.AppendPoolReceipt(context.WithoutCancel(ctx), thread.PoolReceiptEntry{
			Receipt: accept.ID, Status: thread.PoolCanceled, Child: child.ID(),
			Stop: "canceled: the pool closed before the child started",
		})
		return nil, outcome{}, ErrClosed
	}
	p.delegates[accept.ID] = d
	p.byChild[child.ID()] = d
	p.sessionAgents[child.ID()] = agent
	p.mu.Unlock()

	rec := &Receipt{ID: accept.ID, State: thread.PoolAccepted, Child: child.ID()}
	if async {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.runChild(runCtx, d, prompt)
		}()
		return rec, outcome{}, nil
	}
	out := p.runChild(runCtx, d, prompt)
	return rec, out, nil
}

// runChild runs one delegation to its settlement: slot, start, run,
// settle — in that order on the receipt (ADR 0022 §4). The slot is
// held from start to park-or-settlement — a child parked at a nested
// approval holds no slot (its run is over; the boundary is
// bookkeeping), which is what keeps a fleet of parked children from
// starving the pool. A canceled acquisition settles canceled without
// a start. Panics in the pool's own work are contained and settle the
// receipt failed — a background goroutine must not take the process
// down over a ledger entry, and the child session contains its own
// run's panics already.
func (p *Pool) runChild(runCtx context.Context, d *delegate, prompt string) outcome {
	cancel := d.cancel
	parent, child, agent, receiptID := d.parent, d.child, d.agent, d.receipt
	// The settlement (and the running entry) append in a
	// WithoutCancel window: a receipt that never settles because its
	// own cancellation also canceled its ledger is a ledger that lies.
	settleCtx := context.WithoutCancel(runCtx)
	defer func() {
		if r := recover(); r != nil {
			agent.Logger().Error("thread/pool: delegation panicked", "receipt", receiptID, "panic", r)
			p.settle(settleCtx, parent, agent.Logger(), receiptID, child.ID(), thread.PoolFailed, fmt.Sprintf("panic: %v", r), weft.Usage{})
		}
	}()
	defer cancel()

	var out outcome
	select {
	case p.sem <- struct{}{}:
	case <-runCtx.Done():
		p.settle(settleCtx, parent, agent.Logger(), receiptID, child.ID(), thread.PoolCanceled,
			fmt.Sprintf("canceled before a slot was acquired: %v", runCtx.Err()), weft.Usage{})
		out.err = runCtx.Err()
		return out
	}
	defer p.release()

	if _, err := parent.AppendPoolReceipt(settleCtx, thread.PoolReceiptEntry{
		Receipt: receiptID, Status: thread.PoolRunning, Child: child.ID(),
	}); err != nil {
		agent.Logger().Error("thread/pool: running receipt not recorded", "receipt", receiptID, "err", err)
	}

	// Running before the turn flies: the mark must never trail the run
	// it names — a Forward arriving the moment the model began would
	// read a stale queued phase and refuse a running child. The mark
	// happens-before Send returns, so before anything the run does.
	p.markPhase(d, phaseRunning)
	turn, err := child.Send(runCtx, weft.User(prompt))
	if err == nil {
		var res *weft.RunResult
		res, err = turn.Wait()
		out.answer, out.err = answerOf(res), err
		if err == nil && res != nil && len(res.Pending) > 0 {
			// The child parked at an approval boundary (ADR 0022 §7):
			// its requests mirror onto the parent — durably, before
			// anything else happens — the receipt sits at running, and
			// the delegate stays as the bridge a decision resumes
			// through. The slot is already released (the child's run
			// is over); the delegate retires with the settlement, and
			// its cancel — the run's own, already-done context — stays
			// for Close, where canceling it is a no-op.
			out.pending = len(res.Pending)
			out.requests = child.Pending()
			p.markPhase(d, phaseParked)
			parked := mirrorRequests(out.requests, child.ID(), d.wrapper)
			if _, merr := parent.AppendApprovalRequests(settleCtx, parked...); merr != nil {
				agent.Logger().Error("thread/pool: nested requests not mirrored",
					"child", child.ID(), "err", merr)
				out.err = merr
			}
			return out
		}
		if err == nil {
			p.settle(settleCtx, parent, agent.Logger(), receiptID, child.ID(), thread.PoolDone, out.answer, usageOf(res, err))
			return out
		}
	} else {
		out.err = err
	}
	p.settle(settleCtx, parent, agent.Logger(), receiptID, child.ID(), stateOf(out.err), stopOf(out.err), usageOf(nil, out.err))
	return out
}

// settle records the settlement, logging a failure to do so — an
// unsettled receipt reads running forever, and that is a lie the
// operator must see, not one to swallow.
func (p *Pool) settle(ctx context.Context, parent *thread.Session, log *slog.Logger, receiptID, child, state, stop string, usage weft.Usage) {
	_, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: receiptID, Status: state, Child: child, Stop: stop, Usage: usage,
	})
	if err != nil {
		log.Error("thread/pool: settlement receipt not recorded",
			"receipt", receiptID, "state", state, "err", err)
	}
	p.mu.Lock()
	if d, ok := p.byChild[child]; ok {
		delete(p.delegates, d.receipt)
		delete(p.byChild, child)
	}
	p.mu.Unlock()
}

// release returns one slot.
func (p *Pool) release() { <-p.sem }

// stateOf maps a child run's error to the settlement state (ADR 0022
// §4): a budget death caps, a cancellation cancels, everything else
// fails.
func stateOf(err error) string {
	switch {
	case errors.Is(err, weft.ErrMaxSteps), errors.Is(err, weft.ErrUsageLimit):
		return thread.PoolCapped
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return thread.PoolCanceled
	default:
		return thread.PoolFailed
	}
}

// stopOf renders the settlement's cause text.
func stopOf(err error) string {
	var re *weft.RunError
	if errors.As(err, &re) {
		return re.Err.Error()
	}
	return err.Error()
}

// usageOf reads a child's total usage — a failed child's partial
// included, off RunError.Result, exactly the core's own subagent
// roll-up rule (ADR 0014).
func usageOf(res *weft.RunResult, err error) weft.Usage {
	if res != nil {
		return res.Usage
	}
	var re *weft.RunError
	if errors.As(err, &re) && re.Result != nil {
		return re.Result.Usage
	}
	return weft.Usage{}
}

// answerOf renders the child's answer the way the core's Subagent
// does (ADR 0014 G4): an Output child's submitted bytes verbatim —
// empty bytes are a submission — and a child that never submitted its
// final text.
func answerOf(res *weft.RunResult) string {
	if res == nil {
		return ""
	}
	if b, err := weft.OutputOf[json.RawMessage](res); err == nil {
		return string(b)
	}
	return res.Text()
}

// acquire waits for a slot on ctx — the fallback path's bounded
// ordinary subagent call (Wrap with no parent session).
func (p *Pool) acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// receiptLine is the model-visible result an async delegation returns
// (ADR 0022 §2): the receipt, not the answer — pinned byte-for-byte
// by TestWrapAsyncGolden, and model-visible bytes change only with an
// ADR and a golden.
const receiptLine = "background task accepted; receipt %s"
