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
//     are decided, as if nothing had happened.
//   - the child finished its run: the receipt settles from the child's
//     own ledger — done with its final text, failed, capped or
//     canceled with its recorded cause — with the child's usage.
//   - the child never finished a run — queued, or mid-run when the
//     process died: the receipt settles failed, saying so. The run is
//     not started again: what it had done, it had done.
//   - the child session is gone: the receipt settles failed.
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
	state, stop, usage, parked := classify(entries)
	if parked {
		return p.reattach(ctx, parent, rc)
	}
	return p.settleFromLedger(ctx, parent, rc, name, state, stop, usage)
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
	d, err := p.delegateFor(ctx, parent, rc, wrapper)
	if errors.Is(err, errSettled) {
		return nil // settled since the caller read the ledger: nothing to reattach
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
	if _, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: rc.ID, Status: string(state), Child: rc.Child, Stop: stop, Usage: usage,
	}); err != nil {
		return fmt.Errorf("thread/pool: settlement receipt for %s: %w", rc.ID, err)
	}
	p.mu.Lock()
	delete(p.sessionAgents, rc.Child)
	p.mu.Unlock()
	if err := parent.DenyMirrored(ctx, rc.Child, endedReason(state)); err != nil {
		return fmt.Errorf("thread/pool: requests of ended child %s: %w", rc.Child, err)
	}
	rc.State, rc.Stop = state, stop
	return p.resolveFromLedger(ctx, parent, rc, name)
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
