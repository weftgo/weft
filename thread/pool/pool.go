// Package pool provides bounded concurrent child runs for thread
// sessions (ADR 0022): a FIFO semaphore per Pool value, subagent tools
// whose children run as sessions of their own, linked to the parent
// session and call, and receipts recording every delegation's journey.
// The pool imports the root module and weft/thread, never a
// persistence module (ADR 0011 §1's module rule, one level down), and
// changes nothing in the core: it is a tool middleware, session
// entries, and pool-owned goroutines.
//
// # The bound
//
// A Pool admits at most max child runs doing work at once — model
// calls and tool calls. The bound is the Pool value's: two pools are
// two bounds, and a process that wants one bound shares one Pool. A
// run that is only waiting holds no slot: a child whose sync
// delegation is running a child of its own gives its slot up for the
// wait and queues for one again before it works again, and a child
// parked at an approval holds none. That hand-off is what makes
// fan-out times depth safe on any max, 1 included; the depth of a
// delegation chain has its own limit (MaxDepth).
//
// Admission is first in, first out, in the order work queued: Submit
// queues before it returns, a wrapped tool queues when it is called,
// and a parent taking its slot back queues behind whatever was
// already waiting.
//
// # Costs
//
// A pool child is a session, not a tool-call child of the core's
// (weft.Subagent), so the core's rule "a child's usage is yours" does
// not reach it: its usage is on neither the parent's RunResult.Usage
// nor StepRecord.SubagentUsage. It is on the receipt, and
// thread.Session.Usage sums the settled receipts into its Delegated
// bucket. That is the pool's one exception to the core's roll-up
// (AGENTS.md rule 13), and a budget that must cover delegated work
// reads Delegated.
//
// # Lineage
//
// A child is linked to its parent by reference — its header names the
// parent session and the delegating call; the parent's receipts name
// the child — and by nothing else. Deleting the parent deletes no
// child: Children and Descendants enumerate them so an application
// can cascade, children first. A child still running when its parent
// is deleted runs to its end and cannot record its settlement (the
// failure is logged). A Branch in the parent moves no child: receipts
// are ledger, read from every line of the tree, and a parked child's
// requests stay pending wherever the leaf is; a delegating call
// abandoned by the branch is simply no longer there to resolve, and
// the child's answer stays on its receipt. A Fork copies the ledger
// but not the delegations: the fork's copies of unsettled receipts
// are settled canceled and mirrored requests are left out (the core's
// rule), and the children stay the origin's — Children does not list
// them for the fork.
package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// ErrClosed is returned for work handed to a pool whose Close has
// run — Submit, Decide, Recover, and a wrapped tool's call, in or
// outside a session: an accepted delegation outlives the pool that
// would run it, so a closing pool takes no new work.
var ErrClosed = errors.New("thread/pool: pool is closed")

// ErrUnknownReceipt is returned by Cancel, Forward and Wait for a
// receipt id the parent session's ledger does not hold — a wrong id,
// or another session's.
var ErrUnknownReceipt = errors.New("thread/pool: unknown receipt")

// ErrNotRunning is what every *StateError matches under errors.Is:
// the receipt exists, and its child is not in the state the call
// needs. The StateError says which state it is in.
var ErrNotRunning = errors.New("thread/pool: receipt is not running")

// ErrNoAgent is returned by Decide, Recover and Cancel when a child
// session must be reopened and the pool holds no agent for it: no
// Wrap of this pool carries the name the child was made under, and
// nothing was registered for the session. Register the agent, or Wrap
// it again under its name, and call again.
var ErrNoAgent = errors.New("thread/pool: no agent for child session")

// ErrDepth is returned by Submit for a delegation that would exceed
// the pool's MaxDepth; a wrapped tool reports the same refusal to the
// model as SUBAGENT_DEPTH.
var ErrDepth = errors.New("thread/pool: delegation depth exceeds the pool's limit")

// ErrCycle is returned by Submit for a delegation to an agent that is
// already running above the delegating run; a wrapped tool reports
// the same refusal to the model as SUBAGENT_CYCLE.
var ErrCycle = errors.New("thread/pool: agent is already running in this call chain")

// ErrDuplicateWrap is returned by Wrap for a name the pool already
// wrapped a different agent under: the name is how a restarted
// process finds the agent a parked child resumes with, so one name
// names one agent.
var ErrDuplicateWrap = errors.New("thread/pool: wrap name already in use")

// A StateError reports a receipt whose child is not in the state the
// call serves — Forward to a child that is queued, parked or settled;
// Cancel of a settled one. It matches ErrNotRunning under errors.Is;
// State tells an operator "already done" from "parked".
type StateError struct {
	// Receipt is the receipt id the call named.
	Receipt string
	// State is the state the receipt is in.
	State State
	// Orphan reports an unsettled receipt no child of this pool runs
	// for: the ledger of an earlier process. Recover settles it.
	Orphan bool
}

// Error names the receipt and its state.
func (e *StateError) Error() string {
	if e.Orphan {
		return fmt.Sprintf("thread/pool: receipt %s is %s in the ledger, and no child of this pool runs for it (Recover the parent session)", e.Receipt, e.State)
	}
	return fmt.Sprintf("thread/pool: receipt %s is %s, not running", e.Receipt, e.State)
}

// Is reports whether target is ErrNotRunning.
func (e *StateError) Is(target error) bool { return target == ErrNotRunning }

// DefaultMaxDepth is the delegation depth a Pool allows when MaxDepth
// is not given: a child of a child, eight levels down.
const DefaultMaxDepth = 8

// Pool is a bound on concurrent child runs (ADR 0022 §1, D2), per
// Pool value: at most max of the children it started — wrapped or
// submitted — are doing work at any moment. A child holds a slot
// while its run works and none while it waits: not while a sync
// delegation of its own runs a child (the slot is handed back for
// the wait and re-acquired, through the same queue, before the run's
// next model call), and not while it is parked at an approval.
// Admission is FIFO in queueing order. A session's fan-out is already
// bounded by its agent's Parallelism, so the pool adds no per-session
// quota; a delegation chain's depth is bounded separately (MaxDepth).
//
// A Pool is safe for concurrent use. The zero value is not usable;
// New constructs. One Pool should own a storage's delegations at a
// time: Recover treats an unsettled receipt no child of this pool
// runs for as left behind by a process that is gone.
type Pool struct {
	max      int
	maxDepth int
	sem      *fifo
	ids      func() string

	mu        sync.Mutex
	closed    bool
	delegates map[string]*delegate // by receipt id — Cancel's key; unsettled only
	byChild   map[string]*delegate // by child session id — the bridge's key
	// sessionAgents holds the agents Register attached to child
	// sessions (the restart hook, ADR 0022 §7), dropped when the
	// session's delegation settles; nameAgents holds the agents the
	// pool's wraps stand for, keyed by wrap name, which Wrap-made
	// children record in their header metadata.
	sessionAgents map[string]*weft.Agent
	nameAgents    map[string]*weft.Agent
	// calls holds the cancels of wrapped calls running outside any
	// session (the bare path), so Close reaches them too.
	calls    map[uint64]context.CancelFunc
	nextCall uint64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// An Option configures a Pool at New.
type Option interface{ applyPool(*Pool) }

type idsOption func() string

func (o idsOption) applyPool(p *Pool) { p.ids = o }

// IDs sets the function minting ids for the child sessions the pool
// creates, in place of the thread module's own time-sortable ids —
// the determinism knob tests and examples use. It is handed to each
// child as its thread.IDs option, so it mints the child's session id
// and then every entry id inside that child; ids in the parent
// session stay the parent's own. It is called from many goroutines
// and under session locks: it must be safe for concurrent use, return
// quickly, and never repeat an id.
func IDs(id func() string) Option { return idsOption(id) }

type maxDepthOption int

func (o maxDepthOption) applyPool(p *Pool) { p.maxDepth = int(o) }

// MaxDepth sets how deep a chain of delegations through the pool may
// go: 1 allows children, 2 children of children. A delegation that
// would go deeper is refused before any child is created — a wrapped
// tool's call with SUBAGENT_DEPTH, Submit with ErrDepth. The default
// is DefaultMaxDepth. The limit is its own bound, unrelated to max:
// waiting parents hold no slot, so depth costs sessions and tokens,
// not capacity. New panics when n is below 1.
func MaxDepth(n int) Option { return maxDepthOption(n) }

// New returns a pool admitting at most max child runs at work at
// once. The bound is the pool's reason to exist, so max below 1
// panics — an unbounded pool is no pool (DeerFlow's max_running, the
// field's evidence for the shape) — as does a MaxDepth below 1.
func New(max int, opts ...Option) *Pool {
	if max < 1 {
		panic(fmt.Sprintf("thread/pool: New called with max=%d; the bound must be at least 1", max))
	}
	p := &Pool{
		max:           max,
		maxDepth:      DefaultMaxDepth,
		sem:           newFIFO(max),
		delegates:     map[string]*delegate{},
		byChild:       map[string]*delegate{},
		sessionAgents: map[string]*weft.Agent{},
		nameAgents:    map[string]*weft.Agent{},
		calls:         map[uint64]context.CancelFunc{},
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	for _, o := range opts {
		if o != nil {
			o.applyPool(p)
		}
	}
	if p.maxDepth < 1 {
		panic(fmt.Sprintf("thread/pool: MaxDepth(%d); the depth limit must be at least 1", p.maxDepth))
	}
	return p
}

// State is where a delegation is (ADR 0022 §4): accepted → running ⇄
// parked → exactly one of done, failed, canceled, capped. Its values
// are the wire strings of thread.PoolReceiptEntry.Status.
type State string

// The receipt states.
const (
	// Accepted: recorded, and queued for a slot.
	Accepted State = thread.PoolAccepted
	// Running: the child holds a slot and its run is in flight — or
	// is waiting, slotless, on a sync delegation of its own.
	Running State = thread.PoolRunning
	// Parked: the child's run ended at an approval boundary; it holds
	// no slot and resumes when its mirrored requests are decided.
	Parked State = thread.PoolParked
	// Done: the child ran to its end; Stop is its answer.
	Done State = thread.PoolDone
	// Failed: the child's run failed, or the delegation could not
	// proceed; Stop is the cause.
	Failed State = thread.PoolFailed
	// Canceled: Cancel, the pool's Close, or — for a sync child — the
	// delegating call's own cancellation or timeout.
	Canceled State = thread.PoolCanceled
	// Capped: the child died on a budget (MaxSteps, a usage limit).
	Capped State = thread.PoolCapped
)

// String returns the state's wire string.
func (s State) String() string { return string(s) }

// Settled reports whether the state is final: done, failed, canceled
// or capped. An unknown state — a newer writer's — is not settled.
func (s State) Settled() bool {
	switch s {
	case Done, Failed, Canceled, Capped:
		return true
	}
	return false
}

// Receipt is a delegation's handle and what is known of it: the
// acceptance entry's id, the state the ledger records last, the child
// session, the delegating call when a wrapped tool made it, and on
// settlement the child's stop text — its answer, when the state is
// Done; the cause otherwise.
type Receipt struct {
	ID    string
	State State
	Child string
	// Call is the parent-side tool call that delegated; empty for a
	// Submit.
	Call string
	Stop string
}

// Settled reports whether the receipt reached a final state.
func (r Receipt) Settled() bool { return r.State.Settled() }

// Submit hands a task to a child session of parent and returns at
// once (ADR 0022 §2): the child session is created in the parent's
// storage with a lineage naming the parent and the parent's approval
// policy (thread.InheritApprovals), the acceptance receipt is durable
// in the parent session and the delegation has its place in the
// pool's queue before Submit returns, and the child runs on a
// pool-owned context — independent of the submitting caller's
// cancellation by design (D4), canceled explicitly by Cancel or
// Close.
//
// The receipt's Child names the session to read, reopen or tail. A
// live tail needs a storage with the thread.Watcher capability —
// thread/jsonl and thread/sqlite have it, thread.Memory does not; on
// any storage Wait blocks until the delegation settles or parks, and
// Receipts reads the ledger.
//
// parent and agent must not be nil. Called from inside a pool child's
// run — a tool handler passing its context — Submit counts the
// delegation's depth and ancestry from there, and fails with ErrDepth
// or ErrCycle as a wrapped tool would refuse. A closed pool fails
// with ErrClosed.
func (p *Pool) Submit(ctx context.Context, parent *thread.Session, agent *weft.Agent, prompt string) (*Receipt, error) {
	if agent == nil {
		return nil, fmt.Errorf("thread/pool: Submit with a nil agent")
	}
	info, err := p.admit(ctx, agent)
	if err != nil {
		return nil, err
	}
	r, _, err := p.submit(ctx, parent, agent, prompt, "", "", info, true)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Cancel ends the delegation behind receiptID, a receipt of parent's
// (D4). What that means depends on where the child is:
//
//   - queued or running: its run is canceled. The child session
//     records the canceled turn, the receipt settles canceled on the
//     run's goroutine — Cancel does not wait for it; Wait does — and
//     a sync delegation's parent model reads SUBAGENT_CANCELED as the
//     call's result.
//   - parked at an approval: the child's pending requests are denied
//     in the child session (so its parked calls can never run on a
//     later approval), its run is not resumed, the receipt settles
//     canceled before Cancel returns, the mirrored requests leave the
//     parent's Pending, and a sync delegation's parked call resolves
//     with SUBAGENT_CANCELED.
//   - unsettled in the ledger with no child of this pool running for
//     it — a previous process's: it is recovered first (see Recover)
//     and then canceled if recovery left it parked.
//
// A settled receipt fails with a *StateError (ErrNotRunning under
// errors.Is) carrying its state; an id the ledger does not hold with
// ErrUnknownReceipt.
func (p *Pool) Cancel(ctx context.Context, parent *thread.Session, receiptID string) error {
	if parent == nil {
		return fmt.Errorf("thread/pool: Cancel with no parent session")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		p.mu.Lock()
		d, ok := p.delegates[receiptID]
		if ok && d.parent.ID() == parent.ID() {
			switch d.phase {
			case phaseQueued, phaseRunning:
				d.canceled = true
				cancel := d.cancel
				p.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				return nil
			case phaseParked:
				d.phase = phaseSettling
				p.mu.Unlock()
				return p.cancelParked(ctx, d)
			default:
				// Settling: its run, or another Cancel, is writing
				// the settlement. Once it is at rest the ledger says
				// how it ended.
				idle := d.idle
				p.mu.Unlock()
				if idle == nil {
					return nil
				}
				select {
				case <-idle:
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		p.mu.Unlock()
		rc, found := lookup(parent, receiptID)
		switch {
		case !found:
			return fmt.Errorf("%w: %s in session %s", ErrUnknownReceipt, receiptID, parent.ID())
		case rc.Settled():
			return &StateError{Receipt: receiptID, State: rc.State}
		case attempt > 0:
			// Recovery ran and left it unsettled without a bridge:
			// nothing this pool can cancel.
			return &StateError{Receipt: receiptID, State: rc.State, Orphan: true}
		}
		if err := p.recoverOne(ctx, parent, rc); err != nil {
			return err
		}
	}
}

// Wait blocks until the delegation behind receiptID is at rest in
// this pool — settled, or parked at an approval — and returns its
// receipt as the ledger then reads. When it returns for a sync
// delegation that settled after a park, the parent's parked call is
// already resolved and its resume armed. A receipt no child of this
// pool is working returns at once, as it stands. ctx bounds the wait;
// an id the ledger does not hold fails with ErrUnknownReceipt.
func (p *Pool) Wait(ctx context.Context, parent *thread.Session, receiptID string) (Receipt, error) {
	if parent == nil {
		return Receipt{}, fmt.Errorf("thread/pool: Wait with no parent session")
	}
	for {
		p.mu.Lock()
		var idle chan struct{}
		if d, ok := p.delegates[receiptID]; ok && d.parent.ID() == parent.ID() {
			idle = d.idle
		}
		p.mu.Unlock()
		if idle == nil {
			rc, found := lookup(parent, receiptID)
			if !found {
				return Receipt{}, fmt.Errorf("%w: %s in session %s", ErrUnknownReceipt, receiptID, parent.ID())
			}
			return rc, nil
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return Receipt{}, ctx.Err()
		}
	}
}

// Close stops taking work and drains: every queued or running child
// is canceled — async ones on their pool-owned contexts, sync ones
// through their registered cancels, their parents' models reading
// SUBAGENT_CANCELED — wrapped calls running outside any session are
// canceled too, and Close waits for all of them to settle their
// receipts and end, every child's session closed before the process
// gives up the storage (DeerFlow's gateway drain). A child parked at
// an approval is not canceled: its session is closed, its receipt
// stays parked, and a later pool resumes it (Recover, Decide).
//
// ctx bounds the wait; its error returns with the drain still
// progressing. A second Close returns nil — the drain is already done
// or underway.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.cancel()
	var parked []*delegate
	for _, d := range p.delegates {
		switch d.phase {
		case phaseQueued, phaseRunning:
			d.canceled = true
			if d.cancel != nil {
				d.cancel()
			}
		case phaseParked:
			parked = append(parked, d)
		}
	}
	for _, cancel := range p.calls {
		cancel()
	}
	p.mu.Unlock()

	// A parked child has no run to drain; closing its session lets
	// the storage go (a writer's hold, where the backend keeps one).
	for _, d := range parked {
		if err := d.child.Close(context.WithoutCancel(ctx)); err != nil {
			d.agent.Logger().Warn("thread/pool: parked child session not closed",
				"child", d.child.ID(), "receipt", d.receipt, "err", err)
		}
	}

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
// receipt state of its own). The first settlement a receipt records
// is its state for good. A session that never delegated returns nil.
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
			r := &Receipt{ID: pr.ID, State: State(pr.Status), Child: pr.Child, Call: pr.Call}
			byID[pr.ID] = r
			order = append(order, pr.ID)
			continue
		}
		if r, ok := byID[pr.Receipt]; ok && !r.Settled() {
			r.State = State(pr.Status)
			r.Stop = pr.Stop
		}
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]Receipt, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// lookup returns one receipt of parent's ledger.
func lookup(parent *thread.Session, receiptID string) (Receipt, bool) {
	for _, r := range Receipts(parent) {
		if r.ID == receiptID {
			return r, true
		}
	}
	return Receipt{}, false
}

// Children returns the sessions parent delegated to, in acceptance
// order: the child of every receipt in its ledger whose stored header
// names parent as its lineage. The check is what keeps a fork honest
// — a Fork copies its origin's receipts, and the children stay the
// origin's — and a child that has been deleted is left out. Deleting
// a session deletes none of its children; this is the list to cascade
// over (Descendants gives the whole subtree).
func Children(ctx context.Context, parent *thread.Session) ([]string, error) {
	if parent == nil {
		return nil, fmt.Errorf("thread/pool: Children with no parent session")
	}
	var ids []string
	for _, r := range Receipts(parent) {
		ids = append(ids, r.Child)
	}
	out, _, err := verified(ctx, parent.Storage(), parent.ID(), ids)
	return out, err
}

// Descendants returns every session delegated from parent, directly
// or through a child, deepest first: each session appears after all
// of its own children, so deleting in the order returned — and the
// parent last — never leaves a child whose parent is already gone
// half-way through. It reads each descendant from parent's storage;
// a session that has been deleted is left out, with whatever was
// below it.
func Descendants(ctx context.Context, parent *thread.Session) ([]string, error) {
	if parent == nil {
		return nil, fmt.Errorf("thread/pool: Descendants with no parent session")
	}
	st := parent.Storage()
	var ids []string
	for _, r := range Receipts(parent) {
		ids = append(ids, r.Child)
	}
	var out []string
	seen := map[string]bool{parent.ID(): true}
	var walk func(parentID string, ids []string) error
	walk = func(parentID string, ids []string) error {
		children, below, err := verified(ctx, st, parentID, ids)
		if err != nil {
			return err
		}
		for _, id := range children {
			if seen[id] {
				continue // a lineage is a tree; a damaged one must not loop
			}
			seen[id] = true
			if err := walk(id, below[id]); err != nil {
				return err
			}
			out = append(out, id)
		}
		return nil
	}
	if err := walk(parent.ID(), ids); err != nil {
		return nil, err
	}
	return out, nil
}

// verified loads each candidate child and keeps the ones whose header
// names parentID as their lineage, with the children their own
// ledgers accepted.
func verified(ctx context.Context, st thread.Storage, parentID string, ids []string) ([]string, map[string][]string, error) {
	var out []string
	below := map[string][]string{}
	for _, id := range ids {
		h, entries, _, err := st.Load(ctx, id)
		if errors.Is(err, thread.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("thread/pool: child session %s: %w", id, err)
		}
		if h.Lineage == nil || h.Lineage.Session != parentID {
			continue
		}
		out = append(out, id)
		for _, e := range entries {
			if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Receipt == "" && pr.Child != "" {
				below[id] = append(below[id], pr.Child)
			}
		}
	}
	return out, below, nil
}

// outcome is what one run of a delegation's child came to, for the
// sync wrapper that waited on it: the answer, the error, whether the
// child parked at an approval boundary (ADR 0021 §1 — the pool
// bridges it, ADR 0022 §7), and whether the pool itself canceled it.
type outcome struct {
	answer   string
	err      error
	parked   bool
	canceled bool
}

// admit checks a delegation to agent, made from ctx, against the
// pool's two structural limits — ancestry and depth — and returns the
// run info the child will carry. The ancestry is the real cycle
// check (ADR 0014): the agents whose runs delegated down to ctx plus
// the agent running on it; the core's own guard lives in the Subagent
// handler, which a session-run delegation never reaches.
func (p *Pool) admit(ctx context.Context, agent *weft.Agent) (runInfo, error) {
	above := runFromContext(ctx)
	chain := above.chain
	if cur := weft.AgentFromContext(ctx); cur != nil && (len(chain) == 0 || chain[len(chain)-1] != cur) {
		chain = append(append([]*weft.Agent(nil), chain...), cur)
	}
	for _, a := range chain {
		if a == agent {
			return runInfo{}, ErrCycle
		}
	}
	if above.depth+1 > p.maxDepth {
		return runInfo{}, fmt.Errorf("%w: depth %d, limit %d", ErrDepth, above.depth+1, p.maxDepth)
	}
	return runInfo{depth: above.depth + 1, chain: chain}, nil
}

// submit is the one delegation path under Wrap and Submit: it creates
// the child session, records acceptance, takes the delegation's place
// in the queue, and either runs the child inline (sync — the
// delegating call waits) or on a pool goroutine (async — the caller
// holds the receipt). callID names the delegating tool call in the
// child's lineage when there was one; name is the wrap's, the resume
// key and the name the model-visible texts use. The mutex-checked
// closed state refuses new work before anything is created.
func (p *Pool) submit(ctx context.Context, parent *thread.Session, agent *weft.Agent, prompt, callID, name string, info runInfo, async bool) (*Receipt, outcome, error) {
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

	// The child takes the parent's approval policy (ADR 0022 §7):
	// expiry, quorum, the signing rule and its keyring, the clock.
	opts := []thread.SessionOption{thread.WithLineage(parent.ID(), callID), thread.InheritApprovals(parent)}
	if name != "" {
		// The wrap's name is the resume key a restarted process
		// re-Wraps into existence (ADR 0022 §7).
		opts = append(opts, thread.WithMeta(map[string]string{metaAgent: name}))
	}
	if p.ids != nil {
		opts = append(opts, thread.IDs(p.ids))
	}
	st := parent.Storage()
	child, err := thread.Create(ctx, st, agent, opts...)
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
		clean := context.WithoutCancel(ctx)
		_ = child.Close(clean)
		_ = thread.Delete(clean, st, child.ID())
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
	d := &delegate{
		receipt: accept.ID, name: name,
		child: child, parent: parent, agent: agent, info: info,
		phase: phaseQueued,
	}
	// Decisions the parent records on its own paths reach this child
	// through the pool's watch (bridge.go).
	p.attach(parent)
	if !async {
		// Only a sync delegation's call waits on the child: an async
		// one returned its receipt line, and nothing parks under it.
		d.wrapper = callID
	}
	p.mu.Lock()
	if p.closed { // Close ran between the check and the registration
		p.mu.Unlock()
		cancel()
		p.conclude(context.WithoutCancel(ctx), d, Canceled, "canceled: the pool closed before the child started", "", false)
		return nil, outcome{}, ErrClosed
	}
	p.delegates[accept.ID] = d
	p.byChild[child.ID()] = d
	// The WaitGroup and the queue are joined under the lock that
	// guards closed: Close's wait can never miss a delegation it did
	// not refuse, and the queue's order is the order of acceptance.
	p.wg.Add(1)
	r := d.begin(cancel, p.sem.enqueue())
	p.mu.Unlock()

	rec := &Receipt{ID: accept.ID, State: Accepted, Child: child.ID(), Call: callID}
	first := func(ctx context.Context) (*thread.Turn, error) {
		return child.Send(ctx, weft.User(prompt))
	}
	if async {
		go func() {
			defer p.wg.Done()
			p.run(runCtx, d, r, first)
		}()
		return rec, outcome{}, nil
	}
	defer p.wg.Done()
	return rec, p.run(runCtx, d, r, first), nil
}

// run drives one run of a delegation's child — its first turn, or a
// resume after a park — from the queue to rest: slot, running
// receipt, the turn, then either the park (requests mirrored, parked
// receipt, no slot) or the settlement (ADR 0022 §4). t is the run's
// place in the queue, nil to queue now. A canceled wait settles
// canceled without a start. Every way out leaves the receipt parked
// or settled with its cause: a panic in the pool's own work is
// contained and settles failed — a background goroutine must not take
// the process down over a ledger entry, and the child session
// contains its own run's panics already.
func (p *Pool) run(ctx context.Context, d *delegate, r runHandle, start func(context.Context) (*thread.Turn, error)) (out outcome) {
	// The ledger's appends run in a WithoutCancel window: a receipt
	// that never settles because its own cancellation also canceled
	// its ledger is a ledger that lies.
	ledger := context.WithoutCancel(ctx)
	defer p.rest(d, r) // last: Wait returns only when everything below is done
	defer func() {
		if rec := recover(); rec != nil {
			d.agent.Logger().Error("thread/pool: delegation panicked", "receipt", d.receipt, "panic", rec)
			stop := fmt.Sprintf("panic: %v", rec)
			p.conclude(ledger, d, Failed, stop, d.failureText(stop), true)
			out = outcome{err: fmt.Errorf("thread/pool: delegation panicked: %v", rec)}
		}
	}()

	t := r.ticket
	if t == nil {
		t = p.sem.enqueue()
	}
	if err := t.wait(ctx); err != nil {
		p.conclude(ledger, d, Canceled, fmt.Sprintf("canceled before a slot was acquired: %v", err),
			d.canceledText(), true)
		return outcome{err: err, canceled: p.wasCanceled(d)}
	}
	sl := &slot{f: p.sem, ctx: ctx, held: true}
	defer sl.finish()
	info := d.info
	info.slot = sl
	runCtx := withRun(ctx, info)

	if _, err := d.parent.AppendPoolReceipt(ledger, thread.PoolReceiptEntry{
		Receipt: d.receipt, Status: thread.PoolRunning, Child: d.child.ID(),
	}); err != nil {
		// A ledger that cannot record the start cannot record the
		// rest: the child is not started under it.
		stop := fmt.Sprintf("running receipt not recorded: %v", err)
		p.conclude(ledger, d, Failed, stop, d.failureText(stop), true)
		return outcome{err: fmt.Errorf("thread/pool: running receipt: %w", err)}
	}
	// Running before the turn flies: the mark must never trail the run
	// it names — a Forward arriving the moment the model began would
	// read a stale queued phase and refuse a running child. The mark
	// happens-before start returns, so before anything the run does.
	p.setPhase(d, phaseRunning)

	turn, err := start(runCtx)
	var res *weft.RunResult
	if err == nil && turn != nil {
		res, err = turn.Wait()
		// A boundary the child's own decision chain settled — a grant,
		// an Approver — resumes by itself, linked before Wait returns:
		// the delegation's run is the chain of them.
		for err == nil && res != nil && len(res.Pending) > 0 {
			next := turn.Next()
			if next == nil {
				break
			}
			turn = next
			res, err = turn.Wait()
		}
	}
	switch {
	case err == nil && turn == nil && len(d.child.Pending()) == 0:
		return p.concludeStale(ledger, d)
	case err == nil && (turn == nil || (res != nil && len(res.Pending) > 0)):
		if perr := p.park(ledger, d); perr != nil {
			return outcome{err: perr}
		}
		return outcome{parked: true}
	case err == nil:
		answer := answerOf(res)
		p.concludeRun(ctx, ledger, d, Done, answer, answer, false)
		return outcome{answer: answer}
	}
	canceled := p.wasCanceled(d)
	text := d.runFailureText(err)
	if canceled {
		text = d.canceledText()
	}
	p.concludeRun(ctx, ledger, d, stateOf(err), stopOf(err), text, true)
	return outcome{err: err, canceled: canceled}
}

// concludeStale settles a resume that found nothing to resume: the
// boundary the receipt parked on is no longer open in the child, and
// no resume of it is in flight — it ran in a process that died before
// the settlement was written. The child's own file says how the
// delegation ended; a file that ends mid-resume says it did not
// survive.
func (p *Pool) concludeStale(ctx context.Context, d *delegate) outcome {
	state, stop, _, parked := classify(d.child.Entries())
	if parked {
		state, stop = Failed, "nothing to resume: the child's resume did not survive a restart"
	}
	switch state {
	case Done:
		p.conclude(ctx, d, Done, stop, stop, false)
		return outcome{answer: stop}
	case Canceled:
		p.conclude(ctx, d, state, stop, d.canceledText(), true)
	default:
		p.conclude(ctx, d, state, stop, d.failureText(stop), true)
	}
	return outcome{err: errors.New("thread/pool: " + stop)}
}

// A runHandle is one run's own pieces of its delegate: its place in
// the queue (nil to queue when the run starts), the cancel of its
// context, and the channel Wait watches. A delegate that parks and
// resumes has one per run, and a run ends only its own — the resume
// armed while the parked run was still unwinding keeps its cancel and
// its channel.
type runHandle struct {
	ticket *ticket
	cancel context.CancelFunc
	idle   chan struct{}
}

// begin installs a new run's handle on the delegate. Callers hold
// p.mu.
func (d *delegate) begin(cancel context.CancelFunc, t *ticket) runHandle {
	r := runHandle{ticket: t, cancel: cancel, idle: make(chan struct{})}
	d.cancel, d.idle, d.canceled = cancel, r.idle, false
	return r
}

// rest ends a run: its context is canceled, and — unless a later run
// has already taken the delegate over — the delegate is left at rest,
// parked or settled, with nothing to cancel; then Wait wakes.
func (p *Pool) rest(d *delegate, r runHandle) {
	r.cancel()
	p.mu.Lock()
	if d.idle == r.idle {
		d.idle, d.cancel = nil, nil
	}
	p.mu.Unlock()
	close(r.idle)
}

// wasCanceled reports whether Cancel or Close asked for the run's
// end — as against the delegating call's own cancellation.
func (p *Pool) wasCanceled(d *delegate) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return d.canceled
}

// setPhase transitions the delegate's phase under the pool lock.
func (p *Pool) setPhase(d *delegate, phase int) {
	p.mu.Lock()
	d.phase = phase
	p.mu.Unlock()
}

// concludeRun ends a delegation whose child ran: the child session is
// closed first — draining a steer deferred to a follow-up turn while
// the slot is still held, or abandoning it when the run was canceled
// — and then the delegation is concluded.
func (p *Pool) concludeRun(runCtx, ledger context.Context, d *delegate, state State, stop, text string, isErr bool) {
	p.closeChild(runCtx, d)
	p.conclude(ledger, d, state, stop, text, isErr)
}

// conclude settles a delegation and completes what hangs on it: the
// receipt records the final state with its cause, the delegate
// retires, the child session is closed, and — when a sync
// delegation's call is parked in the parent — that call resolves with
// text, the model-visible outcome (ADR 0022 §7): the child's answer,
// or the failure as a resolve_error. The parent's own resume then
// fires through the ordinary boundary machinery.
func (p *Pool) conclude(ctx context.Context, d *delegate, state State, stop, text string, isErr bool) {
	if !p.settle(ctx, d, state, stop) {
		return
	}
	p.closeChild(ctx, d)
	// Requests the delegation leaves undecided — a canceled child's, a
	// failed one's — are denied on the parent, with the reason: nothing
	// can resume the child through it any more, and an undecided
	// mirror would hold the parent's own boundary open for good.
	if err := d.parent.DenyMirrored(ctx, d.child.ID(), endedReason(state)); err != nil {
		d.agent.Logger().Error("thread/pool: an ended delegation's requests still read pending; Recover denies them",
			"receipt", d.receipt, "child", d.child.ID(), "err", err)
	}
	p.mu.Lock()
	resolve := d.resolve
	p.mu.Unlock()
	if resolve {
		p.resolveWrapper(ctx, d, text, isErr)
	}
}

// settle records a delegation's one settlement and retires its
// delegate; it reports whether this call was the one that settled.
// It is idempotent per receipt: a second settle — a panic after the
// first, a Cancel racing the run's own end — records nothing, so the
// parent's Delegated bucket counts the child once. The usage is the
// child session's whole ledger, every run the delegation made. A
// settlement the parent cannot record is logged, loudly — the receipt
// then reads unsettled, and Recover settles it from the child.
func (p *Pool) settle(ctx context.Context, d *delegate, state State, stop string) bool {
	p.mu.Lock()
	if d.settled {
		p.mu.Unlock()
		return false
	}
	d.settled = true
	d.phase = phaseSettled
	p.mu.Unlock()

	u := d.child.Usage()
	_, err := d.parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: d.receipt, Status: string(state), Child: d.child.ID(), Stop: stop,
		Usage: u.Turns.Add(u.Summaries),
	})
	if err != nil {
		d.agent.Logger().Error("thread/pool: settlement receipt not recorded",
			"receipt", d.receipt, "child", d.child.ID(), "state", state, "stop", stop, "err", err)
	}
	// The delegate retires only once the ledger holds the settlement:
	// whoever finds no delegate reads the ledger, and must never read
	// it a step behind.
	p.mu.Lock()
	if p.delegates[d.receipt] == d {
		delete(p.delegates, d.receipt)
	}
	if p.byChild[d.child.ID()] == d {
		delete(p.byChild, d.child.ID())
	}
	delete(p.sessionAgents, d.child.ID())
	p.mu.Unlock()
	return true
}

// endedReason is the deny reason recorded on the parent for a mirrored
// request its delegation left undecided. Never model-visible: the
// child is over, and the denial is replayed nowhere.
func endedReason(state State) string {
	return fmt.Sprintf("the delegation ended (%s) with the request undecided", state)
}

// closeChild closes the child session of a delegation that is over.
// Under a live ctx the close drains what the session still holds — a
// forwarded steer deferred to a follow-up turn; under a canceled one
// the first Close abandons that work and the second seals.
func (p *Pool) closeChild(ctx context.Context, d *delegate) {
	err := d.child.Close(ctx)
	if err != nil && ctx.Err() != nil {
		err = d.child.Close(context.WithoutCancel(ctx))
	}
	if err != nil {
		d.agent.Logger().Warn("thread/pool: child session not closed",
			"child", d.child.ID(), "receipt", d.receipt, "err", err)
	}
}

// stateOf maps a child run's error to the settlement state (ADR 0022
// §4): a budget death caps, a cancellation cancels, everything else
// fails.
func stateOf(err error) State {
	switch {
	case errors.Is(err, weft.ErrMaxSteps), errors.Is(err, weft.ErrUsageLimit):
		return Capped
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return Canceled
	default:
		return Failed
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
