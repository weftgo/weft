package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// Recover brings parent's delegations back under this pool after a
// restart (ADR 0022 §7): the ledger is entries, the pool's memory is
// not, and a process can die between any two of them. For every
// receipt of parent's that is unsettled and that no child of this
// pool is working, Recover reads the child session and makes the
// ledger say what is true:
//
//   - the child is parked at an approval: the bridge is rebuilt (the
//     child reopened under its registered or wrap-named agent), any
//     pending request the parent holds no mirror for is mirrored —
//     the crash window between a child's park and its mirror — and
//     the receipt records parked. The child resumes when its requests
//     are decided, as if nothing had happened. That includes a child
//     whose resume was under way when the process died: its decisions
//     are recorded and its boundary was never closed by a finished
//     run (the session's rule — a resume that started and did not
//     finish leaves the boundary resumable), so the pump at the end
//     of Recover resumes it again, and an approved call whose result
//     was not recorded runs again. Nothing on file tells a call that
//     never started from one cut off half-way; a tool that must not
//     run twice has to be idempotent itself (the playground's
//     side-effect classes, core.Replay, do not gate a session resume).
//     The same holds at any depth: a grandchild whose approved call was
//     running resumes through its parent's recovered delegation. A
//     sync delegation whose delegating run died before its turn ended
//     — the child parked and mirrored, the parent's park never
//     recorded — has no delegating call to resolve: nothing parked it
//     (no turn entry, no boundary; the parent's next run repairs it as
//     interrupted). The child is reattached as a delegation by no call:
//     its requests stay offered, a decision resumes it, and its answer
//     settles the receipt only.
//   - the child finished its run: the receipt settles from the child's
//     own ledger — done with its final text, failed, capped or
//     canceled with its recorded cause — with the child's usage.
//   - the child never finished its first run — queued, or mid-run
//     when the process died with no approval boundary on file: the
//     receipt settles failed, saying so. That run is not started
//     again: what it had done, it had done.
//   - the child session is gone: the receipt settles failed.
//
// A child settled as over that is itself a parent ends its own
// unsettled delegations first, depth first, as Cancel and Close end
// them: nothing will resolve their calls in it. A parked sync
// grandchild is canceled at its boundary — its pending calls denied in
// its session, its requests in the child denied — and settles canceled
// in the child's ledger; one whose run is over settles from its file;
// a parked async one is left parked (D4). Without the agent to reopen
// one of them, the child's receipt is left unsettled and reported with
// ErrNoAgent, for a later Recover. A kill inside this cascade is
// finished by the next Recover: a child is descended into while it
// holds requests no decision answers too, and a settled delegation's
// requests still offered in it are denied.
//
// A sync delegation's call still parked in the parent is resolved
// with the settlement, including for receipts that settled but whose
// resolution a crash lost. Last, the pump runs (Decide with no
// decisions): children whose requests were already decided resume.
//
// Call it once per parent session a new process takes over, after the
// agents are wrapped or registered. A child whose agent the pool does
// not hold is reported with ErrNoAgent and left for a later call; the
// rest are recovered. The error is every failure joined. Recover on a
// session with nothing to recover does nothing; it is safe to call on
// a live session too — receipts whose children this pool is working
// are left alone.
func (p *Pool) Recover(ctx context.Context, parent *thread.Session) error {
	if parent == nil {
		return fmt.Errorf("thread/pool: Recover with no parent session")
	}
	if err := p.open(ctx); err != nil {
		return err
	}
	p.attach(parent)
	var errs []error
	open := openWrappers(parent)
	offered := map[string]bool{} // children with a request still pending
	for _, r := range parent.Pending() {
		offered[r.Child] = true
	}
	for _, rc := range Receipts(parent) {
		p.mu.Lock()
		_, live := p.delegates[rc.ID]
		p.mu.Unlock()
		if live {
			continue
		}
		if !rc.Settled() {
			if err := p.recoverOne(ctx, parent, rc); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		p.forgetSettled(rc.Child)
		// A settlement whose aftermath a crash lost: requests still
		// offered for a delegation that is over, a delegating call
		// still parked beside it.
		if offered[rc.Child] {
			if err := parent.DenyMirrored(ctx, rc.Child, endedReason(rc.State)); err != nil {
				errs = append(errs, fmt.Errorf("thread/pool: requests of ended child %s: %w", rc.Child, err))
			}
		}
		if rc.Call != "" && open[rc.Child] == rc.Call {
			if err := p.reResolve(ctx, parent, rc); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := p.pump(ctx, parent); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// recoverOne recovers one unsettled receipt no delegate of this pool
// answers for.
func (p *Pool) recoverOne(ctx context.Context, parent *thread.Session, rc Receipt) error {
	h, entries, _, err := parent.Storage().Load(ctx, rc.Child)
	if errors.Is(err, thread.ErrNotFound) {
		return p.settleFromLedger(ctx, parent, rc, "", Failed,
			fmt.Sprintf("child session %s is gone", rc.Child), core.Usage{})
	}
	if err != nil {
		return fmt.Errorf("thread/pool: child session %s: %w", rc.Child, err)
	}
	name := h.Meta[metaAgent]
	if p.foreign(parent, rc.Child, name) {
		// Another pool watching the parent holds this child — it may be
		// working it now: its Recover recovers it, never this pool's.
		return nil
	}
	state, stop, usage, parked := classify(entries)
	if parked {
		return p.reattach(ctx, parent, rc)
	}
	if hasLoose(entries) {
		// A child that is itself a parent ends its own sync
		// delegations before it settles, as under Cancel and Close
		// (ADR 0022 §F): its run is over, nothing will resolve their
		// calls in it, and a parked one's requests would reach nobody.
		// When that cannot be done the receipt is left for a later
		// Recover, which ends them first.
		if err := p.endSubtree(ctx, parent, rc.Child); err != nil {
			return err
		}
	}
	return p.settleFromLedger(ctx, parent, rc, name, state, stop, usage)
}

// hasLoose reports whether a session's entries hold a delegation that
// is not finished with: a pool receipt with no settlement, or a
// mirrored child request no later decision answers (a crash between a
// settlement and the denial of its requests).
func hasLoose(entries []thread.Entry) bool {
	open := map[string]bool{}
	mirrors := map[[2]string]bool{} // call id, run id
	for _, e := range entries {
		switch e := e.(type) {
		case thread.PoolReceiptEntry:
			switch {
			case e.Receipt == "":
				open[e.ID] = true
			case State(e.Status).Settled():
				delete(open, e.Receipt)
			}
		case thread.ApprovalRequestEntry:
			if e.Child != "" {
				mirrors[[2]string{e.CallID, e.RunID}] = true
			}
		case thread.ApprovalDecisionEntry:
			delete(mirrors, [2]string{e.CallID, e.RunID})
		}
	}
	return len(open) > 0 || len(mirrors) > 0
}

// endSubtree ends the delegations of a child of above whose run is
// over (recoverOne): the child session is opened under its agent to
// record their settlements in its ledger, and closed again.
func (p *Pool) endSubtree(ctx context.Context, above *thread.Session, childID string) error {
	child, err := p.openEnded(ctx, above, childID)
	if err != nil {
		return err
	}
	defer func() { _ = child.Close(context.WithoutCancel(ctx)) }()
	return p.endDelegations(ctx, child)
}

// openEnded opens a child of above whose delegation Recover is ending,
// under its registered or wrap-named agent, to write its ledger.
func (p *Pool) openEnded(ctx context.Context, above *thread.Session, childID string) (*thread.Session, error) {
	agent, _, err := p.agentForSession(ctx, above, childID)
	if err != nil {
		return nil, err
	}
	child, err := thread.Open(ctx, above.Storage(), childID, agent, thread.InheritApprovals(above))
	if err != nil {
		return nil, fmt.Errorf("thread/pool: child session %s not reopened: %w", childID, err)
	}
	return child, nil
}

// endDelegations ends every unsettled delegation of s, a session whose
// run is over, that no child of this pool is working (endOne). A
// settled one's requests still offered in s — a crash between its
// settlement and their denial — are denied.
func (p *Pool) endDelegations(ctx context.Context, s *thread.Session) error {
	var errs []error
	for _, rc := range Receipts(s) {
		p.mu.Lock()
		_, live := p.delegates[rc.ID]
		p.mu.Unlock()
		if live {
			continue
		}
		if rc.Settled() {
			if err := s.DenyMirrored(ctx, rc.Child, endedReason(rc.State)); err != nil {
				errs = append(errs, fmt.Errorf("thread/pool: requests of ended child %s: %w", rc.Child, err))
			}
			continue
		}
		if err := p.endOne(ctx, s, rc); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// endOne ends one unsettled delegation of a session whose run is over,
// from the delegated child's file, depth first: a parked sync child is
// canceled at its boundary — its own delegations ended first, then its
// pending calls denied in its own session, never to run on a later
// approval — and settles canceled; a child whose run is over settles
// as classify reads it, its own delegations ended first; a gone one
// settles failed. Its requests still offered in s are denied. A parked
// async child is left parked: async delegations are independent of
// the run that made them (D4), and a Recover of s reattaches it.
// Nothing is resolved: s's delegating calls were never parked, and s
// does not run again.
func (p *Pool) endOne(ctx context.Context, s *thread.Session, rc Receipt) error {
	_, entries, _, err := s.Storage().Load(ctx, rc.Child)
	if errors.Is(err, thread.ErrNotFound) {
		return p.recordEnded(ctx, s, rc, Failed, fmt.Sprintf("child session %s is gone", rc.Child), core.Usage{})
	}
	if err != nil {
		return fmt.Errorf("thread/pool: child session %s: %w", rc.Child, err)
	}
	state, stop, usage, parked := classify(entries)
	switch {
	case parked:
		if !syncDelegation(s, rc) {
			return nil
		}
		child, err := p.openEnded(ctx, s, rc.Child)
		if err != nil {
			return err
		}
		defer func() { _ = child.Close(context.WithoutCancel(ctx)) }()
		if err := p.endDelegations(ctx, child); err != nil {
			return err
		}
		if err := child.CancelDelegated(ctx, cancelDenyReason); err != nil {
			return fmt.Errorf("thread/pool: child session %s: %w", rc.Child, err)
		}
		state, stop = Canceled, orphanCanceledStop
	case hasLoose(entries):
		if err := p.endSubtree(ctx, s, rc.Child); err != nil {
			return err
		}
	}
	return p.recordEnded(ctx, s, rc, state, stop, usage)
}

// orphanCanceledStop is the stop of a parked sync child Recover ends
// because the run that delegated to it did not survive a restart.
const orphanCanceledStop = "canceled while parked at an approval: the run that delegated to it did not survive a restart"

// syncDelegation reports whether rc, an unsettled receipt of s whose
// child is parked, is a sync delegation — its child's requests
// mirrored under a delegating call — rather than an async one. A child
// with no mirror (the crash window between its park and the mirror)
// counts as sync unless s holds a result for its delegating call, which
// an async delegation's receipt line is.
func syncDelegation(s *thread.Session, rc Receipt) bool {
	mirrored, answered, accepted := false, false, false
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case thread.ApprovalRequestEntry:
			if e.Child == rc.Child {
				if e.Wrapper != "" {
					return true
				}
				mirrored = true
			}
		case thread.PoolReceiptEntry:
			if e.ID == rc.ID {
				accepted = true
			}
		case thread.MessageEntry:
			if !accepted || e.Message.Role != core.RoleTool {
				continue
			}
			for _, part := range e.Message.Content {
				if r, ok := part.(core.ToolResultPart); ok && r.CallID == rc.Call {
					answered = true
				}
			}
		}
	}
	return !mirrored && rc.Call != "" && !answered
}

// classify reads what a child session's file says became of its
// delegation: parked at an approval (its last turn left calls
// pending), or over — done with its final text, canceled, capped or
// failed with the recorded cause, or failed because no turn was ever
// recorded (the process died with the run in flight or still queued).
// usage is the session's whole cost, turns and summaries.
func classify(entries []thread.Entry) (state State, stop string, usage core.Usage, parked bool) {
	var last *thread.TurnEntry
	for _, e := range entries {
		switch e := e.(type) {
		case thread.TurnEntry:
			last = &e
			usage = usage.Add(e.Usage)
		case thread.CompactionEntry:
			usage = usage.Add(e.SummarizerUsage)
		}
	}
	switch {
	case last == nil:
		return Failed, "the child's run did not survive a restart: no turn was recorded", usage, false
	case len(last.Pending) > 0:
		return "", "", usage, true
	case last.Canceled:
		return Canceled, last.Err, usage, false
	case last.Err != "":
		if strings.Contains(last.Err, core.ErrMaxSteps.Error()) || strings.Contains(last.Err, core.ErrUsageLimit.Error()) {
			return Capped, last.Err, usage, false
		}
		return Failed, last.Err, usage, false
	}
	return Done, finalText(entries), usage, false
}

// reattach rebuilds the bridge to a child parked at an approval:
// missing mirrors are written, and the receipt records parked.
func (p *Pool) reattach(ctx context.Context, parent *thread.Session, rc Receipt) error {
	wrapper := ""
	if rc.Call != "" && callPending(parent, rc.Call) {
		// The delegating call is still open in the parent: the child's
		// requests park under it again, and its answer resolves it.
		wrapper = rc.Call
	}
	for _, e := range parent.Entries() {
		if re, ok := e.(thread.ApprovalRequestEntry); ok && re.Child == rc.Child && re.Wrapper != "" {
			wrapper = re.Wrapper
		}
	}
	if wrapper != "" && !turnLandedSince(parent, rc.ID) {
		// The run that delegated died before its turn ended: the
		// delegating call was never parked (the session's rule — no
		// turn entry, no boundary; the parent's next run repairs it)
		// and never will be. The child is parked in its own right and
		// its requests stay decidable; it resumes as a delegation by no
		// call would, and its answer settles the receipt. Reattached
		// under the call, its resume would wait for a park that never
		// comes.
		wrapper = ""
	}
	d, err := p.delegateFor(ctx, parent, rc, wrapper)
	if errors.Is(err, errSettled) || errors.Is(err, errForeign) {
		return nil // settled since the caller read the ledger, or another pool's: nothing to reattach
	}
	if err != nil {
		return err
	}
	if err := p.mirror(ctx, d); err != nil {
		return fmt.Errorf("thread/pool: child session %s: %w", rc.Child, &unmirroredError{err})
	}
	if rc.State != Parked {
		if _, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
			Receipt: rc.ID, Status: thread.PoolParked, Child: rc.Child,
		}); err != nil {
			return fmt.Errorf("thread/pool: parked receipt for %s: %w", rc.ID, err)
		}
	}
	return nil
}

// turnLandedSince reports whether a turn of parent ended after
// receipt's acceptance: the run that delegated it — one runner per
// session — wrote its turn entry, parked or not.
func turnLandedSince(parent *thread.Session, receipt string) bool {
	accepted := false
	for _, e := range parent.Entries() {
		switch e := e.(type) {
		case thread.PoolReceiptEntry:
			if e.ID == receipt {
				accepted = true
			}
		case thread.TurnEntry:
			if accepted {
				return true
			}
		}
	}
	return false
}

// callPending reports whether callID is a delegating call a crash
// left open in parent: offered as pending, and with no request entry
// of its own — a tail the decision chain never saw, which is what a
// turn that died mid-delegation leaves. An ordinary parked call that
// merely reuses the id has a request entry, and is not it.
func callPending(parent *thread.Session, callID string) bool {
	for _, r := range parent.Pending() {
		if r.CallID == callID && r.Child == "" && r.ID == "" {
			return true
		}
	}
	return false
}

// settleFromLedger settles a receipt whose child no delegate of this
// pool holds, from what the child's file says, and resolves the
// delegating call if it is still parked in the parent.
func (p *Pool) settleFromLedger(ctx context.Context, parent *thread.Session, rc Receipt, name string, state State, stop string, usage core.Usage) error {
	if err := p.recordEnded(ctx, parent, rc, state, stop, usage); err != nil {
		return err
	}
	rc.State, rc.Stop = state, stop
	return p.resolveFromLedger(ctx, parent, rc, name)
}

// recordEnded records a settlement read from a child's file in
// parent's ledger, forgets the child, and denies its requests still
// offered in parent.
func (p *Pool) recordEnded(ctx context.Context, parent *thread.Session, rc Receipt, state State, stop string, usage core.Usage) error {
	if _, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: rc.ID, Status: string(state), Child: rc.Child, Stop: stop, Usage: usage,
	}); err != nil {
		return fmt.Errorf("thread/pool: settlement receipt for %s: %w", rc.ID, err)
	}
	p.mu.Lock()
	delete(p.sessionAgents, rc.Child)
	p.markRetired(rc.Child)
	p.mu.Unlock()
	if err := parent.DenyMirrored(ctx, rc.Child, endedReason(state)); err != nil {
		return fmt.Errorf("thread/pool: requests of ended child %s: %w", rc.Child, err)
	}
	return nil
}

// reResolve resolves the delegating call of a receipt that settled
// while the call stayed parked — the crash window between a
// settlement and its resolution.
func (p *Pool) reResolve(ctx context.Context, parent *thread.Session, rc Receipt) error {
	name := ""
	if h, _, _, err := parent.Storage().Load(ctx, rc.Child); err == nil {
		name = h.Meta[metaAgent]
	}
	return p.resolveFromLedger(ctx, parent, rc, name)
}

// openWrappers maps each child that has a mirror naming a delegating
// call to that call, when no decision recorded after the mirror has
// answered it — the cheap test, one pass over the parent's entries,
// before a resolution is attempted.
func openWrappers(parent *thread.Session) map[string]string {
	open := map[string]string{}  // child → wrapper
	owner := map[string]string{} // wrapper → child
	for _, e := range parent.Entries() {
		switch e := e.(type) {
		case thread.ApprovalRequestEntry:
			if e.Child != "" && e.Wrapper != "" {
				if prev, ok := owner[e.Wrapper]; ok && prev != e.Child {
					delete(open, prev) // the call id moved on to a later delegation
				}
				open[e.Child], owner[e.Wrapper] = e.Wrapper, e.Child
			}
		case thread.ApprovalDecisionEntry:
			if child, ok := owner[e.CallID]; ok {
				delete(open, child)
				delete(owner, e.CallID)
			}
		}
	}
	return open
}

// resolveFromLedger resolves a settled receipt's delegating call with
// the outcome the ledger records. A call that delegates to this child
// no longer — resolved already, or never parked — is not an error.
func (p *Pool) resolveFromLedger(ctx context.Context, parent *thread.Session, rc Receipt, name string) error {
	if rc.Call == "" {
		return nil
	}
	d := &delegate{name: name}
	content, isErr := rc.Stop, false
	switch rc.State {
	case Done:
	case Canceled:
		content, isErr = d.canceledText(), true
	default:
		content, isErr = d.failureText(rc.Stop), true
	}
	_, err := parent.ResolveDelegation(ctx, rc.Call, rc.Child, content, isErr)
	if err != nil && !errors.Is(err, thread.ErrNotPending) {
		return fmt.Errorf("thread/pool: delegating call %s of receipt %s: %w", rc.Call, rc.ID, err)
	}
	return nil
}

// finalText is a finished child's answer read back from its entries:
// the text of its last assistant message. A child built with Output
// answers through its submit call, which a live delegation reads off
// the run's result; recovered from the file, the answer is the text.
func finalText(entries []thread.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		me, ok := entries[i].(thread.MessageEntry)
		if !ok || me.Message.Role != core.RoleAssistant {
			continue
		}
		var b strings.Builder
		for _, part := range me.Message.Content {
			if tp, ok := part.(core.TextPart); ok {
				b.WriteString(tp.Text)
			}
		}
		return b.String()
	}
	return ""
}
