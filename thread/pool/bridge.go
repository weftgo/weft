package pool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// A delegate is one pool child's bridge state: the cancel of its run
// context while it runs, its two sessions and agent while it is
// parked at a nested approval (the state a live process answers
// decisions from), and the wrapper call a sync delegation parked in
// the parent. Delegates live from acceptance to settlement — a parked
// child keeps its delegate (there is nothing to cancel, but the
// lineage is the bridge), and settlement retires it.
type delegate struct {
	cancel  context.CancelFunc
	child   *thread.Session
	parent  *thread.Session
	agent   *weft.Agent
	wrapper string // the parked parent call; empty for async children
	receipt string
	pumping bool // a pumped resume is in flight
	// phase is where the child is: queued before its turn starts,
	// running while it flies, parked at a nested approval. Forward
	// serves the running phase only (ADR 0022 §8) — a steer into an
	// idle session would run as its first turn, the task's own prompt
	// queueing behind its steer.
	phase int
}

const (
	phaseQueued = iota
	phaseRunning
	phaseParked
)

// mirrorCallID namespaces a child's call id for the parent's tree:
// call ids are unique per step, not per run (ADR 0014's lineage note),
// so the child's call_1 and the parent's own call_1 collide unless the
// mirror carries the child's name. Decisions address the namespaced
// form in the parent; the pool strips it when replaying into the
// child, where the bare id is the key.
func mirrorCallID(child, callID string) string {
	return child + "/" + callID
}

// mirrorRequests builds the parent-side mirror entries for a child's
// parked calls (ADR 0022 §7): Child names the session the calls park
// in, Wrapper the delegating call they park under, the CallID is the
// child-namespaced form decisions address, and every field a decision
// or a signature needs rides along verbatim.
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

// Register re-attaches an agent to a child session the process no
// longer holds one for — the restart hook (ADR 0022 §7): a parked
// child's boundary outlives the process, and resuming it needs the
// agent that runs it, which no file carries. Children a Wrap made
// need no call: the wrap's name, recorded in the child's header
// metadata, is looked up among the pool's wrapped agents. Register is
// for the rest — Submit children — and for wraps renamed between
// runs. A nil agent is ignored.
func (p *Pool) Register(sessionID string, agent *weft.Agent) {
	if agent == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionAgents[sessionID] = agent
}

// Decide records decisions over a parent session's nested approvals
// and resumes the children they address (ADR 0022 §7): decisions are
// recorded in the parent — validation, audit and the signed challenge
// all live there — then the pool replays them into the child session,
// which resumes exactly as a top-level session does (ADR 0021 §1).
// When the child runs to its end the delegation completes: a sync
// one's parked wrapper call resolves with the child's answer (the
// core's Resolve — re-running the delegation would replay the child),
// an async one's receipt settles. A child that parks again on the way
// mirrors its new requests and waits for the next Decide.
//
// With no decisions it is a pure pump: children whose every mirrored
// request now has an effective decision — or has lapsed past its
// expiry — resume, which is how a signed DecideSigned on the session
// drives children too.
//
// The returned Turn is the resumed child's, when the call resumed
// one; the parent's own continuation is reached through its parked
// boundary as always. The wrapper call never takes a direct decision:
// Pending shows the child's requests, and the wrapper completes
// through them.
func (p *Pool) Decide(ctx context.Context, parent *thread.Session, ds ...thread.Decision) (*thread.Turn, error) {
	if parent == nil {
		return nil, fmt.Errorf("thread/pool: Decide with no parent session")
	}
	if len(ds) > 0 {
		// Recording only: the wrapper stays pending, so the parent
		// arms no resume of its own — the children are the pool's to
		// run.
		if _, err := parent.Decide(ctx, ds...); err != nil {
			return nil, err
		}
	}
	return p.pump(ctx, parent)
}

// pump walks the parent's mirrored requests and resumes every child
// whose boundary is complete — every mirror decided or lapsed — and
// that is not already resuming. The walk is entry-driven, so it works
// the same live and after a restart; the agent comes from the pool's
// registers, falling back loudly when nothing holds it.
func (p *Pool) pump(ctx context.Context, parent *thread.Session) (*thread.Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The offered view carries the undecided mirrors; expiry lapses
	// count as ready (the child's resume denies them with the stated
	// reason — ADR 0021 §5).
	undecided := map[string]bool{}
	for _, r := range parent.Pending() {
		undecided[r.CallID] = true
	}
	now := time.Now()

	var order []string
	mirrors := map[string][]thread.ApprovalRequestEntry{}
	for _, e := range parent.Entries() {
		re, ok := e.(thread.ApprovalRequestEntry)
		if !ok || re.Child == "" {
			continue
		}
		if _, seen := mirrors[re.Child]; !seen {
			order = append(order, re.Child)
		}
		mirrors[re.Child] = append(mirrors[re.Child], re)
	}

	var firstErr error
	var first *thread.Turn
	for _, childID := range order {
		reqs := mirrors[childID]
		ready := true
		for _, re := range reqs {
			lapsed := !re.Expiry.IsZero() && now.After(re.Expiry)
			if undecided[re.CallID] && !lapsed {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		wrapper := ""
		if len(reqs) > 0 {
			wrapper = reqs[0].Wrapper
		}
		d := p.delegateFor(ctx, parent, childID, wrapper)
		if d == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("thread/pool: child session %s needs its agent to resume — Register it, or re-Wrap its name", childID)
			}
			continue
		}
		// The pumping check is resume's own, under the pool lock — an
		// unlocked peek here would race the very write it reads.
		t, err := p.resume(ctx, parent, d, reqs)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if first == nil {
			first = t
		}
	}
	return first, firstErr
}

// delegateFor returns the bridge for a child session: the live
// delegate when the process still holds it, or one rebuilt from the
// parent's entries after a restart (ADR 0022 §7) — the child session
// reopened under the wrap-named or registered agent, the wrapper and
// receipt recovered from the mirror entries and the receipt ledger.
// nil when no agent can be found: the resume refuses loudly rather
// than guess.
func (p *Pool) delegateFor(ctx context.Context, parent *thread.Session, childID, wrapper string) *delegate {
	p.mu.Lock()
	if d, ok := p.byChild[childID]; ok {
		p.mu.Unlock()
		return d
	}
	p.mu.Unlock()
	// The receipt ledger names the unsettled delegation.
	receipt := ""
	for _, r := range Receipts(parent) {
		if r.Child == childID && r.State != thread.PoolDone &&
			r.State != thread.PoolFailed && r.State != thread.PoolCanceled && r.State != thread.PoolCapped {
			receipt = r.ID
		}
	}
	agent := p.agentForSession(ctx, parent, childID)
	if agent == nil {
		return nil
	}
	child, err := thread.Open(ctx, parent.Storage(), childID, agent)
	if err != nil {
		agent.Logger().Error("thread/pool: parked child session not reopened",
			"child", childID, "err", err)
		return nil
	}
	// A rebuilt delegate bridges a parked child by construction — the
	// mirrors are why pump looked for it — so its phase starts parked
	// and it carries no cancel: nothing of its run is left to cancel.
	d := &delegate{child: child, parent: parent, agent: agent, wrapper: wrapper, receipt: receipt, phase: phaseParked}
	p.mu.Lock()
	if existing, ok := p.byChild[childID]; ok {
		p.mu.Unlock()
		return existing
	}
	p.byChild[childID] = d
	if receipt != "" {
		p.delegates[receipt] = d
	}
	p.mu.Unlock()
	return d
}

// agentForSession finds the agent a child session resumes under, from
// the registers alone (the restart path): Register's session register
// first, then the wrap-name register by the child header's metadata.
func (p *Pool) agentForSession(ctx context.Context, parent *thread.Session, childID string) *weft.Agent {
	p.mu.Lock()
	if a, ok := p.sessionAgents[childID]; ok {
		p.mu.Unlock()
		return a
	}
	p.mu.Unlock()
	h, _, _, err := parent.Storage().Load(ctx, childID)
	if err != nil {
		return nil
	}
	if name := h.Meta["pool_agent"]; name != "" {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.nameAgents[name]
	}
	return nil
}

// markPhase transitions the delegate's phase under the pool lock.
func (p *Pool) markPhase(d *delegate, phase int) {
	p.mu.Lock()
	d.phase = phase
	p.mu.Unlock()
}

// resume drives one child's boundary to its end: replay the recorded
// decisions into the child session, wait out its resumed turn, then
// settle — the receipt, the wrapper's resolution, the delegate — or
// mirror the child's next park and wait for the world to decide
// again. It runs on the caller's goroutine: Decide returns when the
// children it resumed have completed, so a decision's effect is
// visible when the call does (the wait is the child's turn, bounded
// by its own run).
func (p *Pool) resume(ctx context.Context, parent *thread.Session, d *delegate, reqs []thread.ApprovalRequestEntry) (*thread.Turn, error) {
	p.mu.Lock()
	if d.pumping {
		p.mu.Unlock()
		return nil, nil
	}
	d.pumping = true
	p.mu.Unlock()

	child, agent := d.child, d.agent
	if agent == nil {
		agent = p.agentForSession(ctx, d.parent, child.ID())
	}
	if agent == nil || child == nil {
		p.mu.Lock()
		d.pumping = false
		p.mu.Unlock()
		return nil, fmt.Errorf("thread/pool: child session %s needs its agent to resume — Register it, or re-Wrap its name", childLabel(d, reqs))
	}

	// The replay: every decision the parent recorded for the child's
	// calls, verbatim — outcome, reason, content, who — so quorum and
	// audit hold in the child's file too.
	var ds []thread.Decision
	for _, re := range reqs {
		bare, ok := strings.CutPrefix(re.CallID, re.Child+"/")
		if !ok {
			bare = re.CallID
		}
		for _, e := range parent.Entries() {
			de, ok := e.(thread.ApprovalDecisionEntry)
			if !ok || de.CallID != re.CallID {
				continue
			}
			ds = append(ds, thread.Decision{
				CallID:  bare,
				Kind:    de.Outcome,
				Reason:  de.Reason,
				Content: de.Content,
				Who:     de.Who,
				Via:     de.Via,
			})
		}
	}
	turn, err := child.Decide(context.WithoutCancel(ctx), ds...)
	if err != nil {
		p.mu.Lock()
		d.pumping = false
		p.mu.Unlock()
		return nil, err
	}
	if turn == nil {
		// The boundary did not complete inside the child — an
		// incomplete replay or a raced decision; nothing to wait.
		p.mu.Lock()
		d.pumping = false
		p.mu.Unlock()
		return nil, nil
	}
	res, err := turn.Wait()
	settleCtx := context.WithoutCancel(ctx)
	if err == nil && res != nil && len(res.Pending) > 0 {
		// Parked again on the way: mirror the new requests and hand
		// the boundary back to the decider.
		if _, merr := parent.AppendApprovalRequests(settleCtx,
			mirrorRequests(child.Pending(), child.ID(), d.wrapper)...); merr != nil {
			agent.Logger().Error("thread/pool: re-park mirrors not recorded", "child", child.ID(), "err", merr)
		}
		p.mu.Lock()
		d.pumping = false
		p.mu.Unlock()
		return turn, nil
	}
	p.complete(settleCtx, parent, d, res, err)
	return turn, nil
}

// complete settles a delegation whose child ran to its end: the
// receipt records the outcome and cost, and a sync delegation's
// wrapper call resolves with what the child came to — the answer, or
// the failure as a resolve_error the parent model reads. The parent's
// own resume then fires through the ordinary boundary machinery.
func (p *Pool) complete(ctx context.Context, parent *thread.Session, d *delegate, res *weft.RunResult, err error) {
	// settle retires the delegate from both indexes; pumping clears
	// here, on the way out of the one goroutine that set it.
	defer func() {
		p.mu.Lock()
		d.pumping = false
		p.mu.Unlock()
	}()
	usage := usageOf(res, err)
	switch err {
	case nil:
		p.settle(ctx, parent, d.agent.Logger(), d.receipt, d.child.ID(), thread.PoolDone, answerOf(res), usage)
		if d.wrapper != "" {
			p.resolveWrapper(ctx, parent, d, thread.OutcomeResolve, answerOf(res))
		}
	default:
		state, stop := stateOf(err), stopOf(err)
		p.settle(ctx, parent, d.agent.Logger(), d.receipt, d.child.ID(), state, stop, usage)
		if d.wrapper != "" {
			p.resolveWrapper(ctx, parent, d, thread.OutcomeResolveError, stop)
		}
	}
}

// resolveWrapper completes the parked parent call: a decision entry
// resolving it with the child's outcome, which the parent's own
// auto-resume machinery turns into the core's Resolve on the resumed
// run (ADR 0021 §1, ADR 0022 §7).
func (p *Pool) resolveWrapper(ctx context.Context, parent *thread.Session, d *delegate, kind thread.Outcome, content string) {
	// The session's own delegation path, not the caller's Decide: the
	// resolution is recorded with Via "child" and lands under
	// RequireSigned too.
	_, err := parent.ResolveDelegation(ctx, d.wrapper, content, kind == thread.OutcomeResolveError)
	if err != nil {
		d.agent.Logger().Error("thread/pool: wrapper call not resolved",
			"wrapper", d.wrapper, "child", d.child.ID(), "err", err)
	}
}

func childLabel(d *delegate, reqs []thread.ApprovalRequestEntry) string {
	if d.child != nil {
		return d.child.ID()
	}
	if len(reqs) > 0 {
		return reqs[0].Child
	}
	return "?"
}

// Forward steers a running child with msg, explicitly (ADR 0022 §8,
// ADR 0019 §7): the message is sent to the child's session under the
// Steer policy — delivered at the run's next steering drain point, or
// deferred to a follow-up turn when the run ends on an intended stop
// or an open approval boundary; the child's session records the
// receipt either way. Nothing is ever forwarded implicitly. The
// returned Turn is the steer's receipt turn; a child that is not
// running (queued, parked awaiting decisions, settled) refuses with
// ErrNotRunning.
func (p *Pool) Forward(ctx context.Context, receiptID string, msg weft.Message) (*thread.Turn, error) {
	p.mu.Lock()
	d, ok := p.delegates[receiptID]
	phase := phaseQueued
	if ok {
		phase = d.phase
	}
	p.mu.Unlock()
	if !ok || phase != phaseRunning {
		return nil, fmt.Errorf("%w: %s (not a running child)", ErrNotRunning, receiptID)
	}
	return d.child.Send(ctx, msg, thread.As(thread.Steer))
}
