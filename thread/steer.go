package thread

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft"
)

// queuedSteer is one accepted steer waiting for the running turn's
// drain: the receipt entry's id, the message, the context its Send
// carried (the follow-up's persistence window if it defers), the run
// options its Send carried (the follow-up's, if it defers — a
// delivered steer joins another turn's run, under that run's options),
// and the Turn the caller holds. step is the step whose drain took it,
// set when it is handed to a run.
type queuedSteer struct {
	receipt string
	msg     weft.Message
	ctx     context.Context
	opts    []weft.RunOption
	turn    *Turn
	step    int
}

// QueuedSteer is one message the session has accepted and not yet
// given to a model, as Queue reports it.
type QueuedSteer struct {
	// Receipt is the id the message's Turn reports as its own (Turn.ID):
	// for a steer, its queued receipt entry; for a queued send, the id
	// its prompt entry takes when its turn starts — the accepted
	// receipt entry names it in ReceiptEntry.Turn.
	Receipt string
	// Msg is the message held.
	Msg weft.Message
	// Policy says how it waits: Steer — held for the running turn's
	// next drain point — or Queue — an accepted send (or a deferred
	// steer's follow-up) waiting for a turn of its own.
	Policy Policy
}

// steerSource is the SteerFunc the session installs on every run
// (ADR 0019 §1: a pull hook): it drains the live steer queue without
// blocking — drain a queue, do not wait on one — moving what it hands
// over to the handed list, whose delivered receipts join the turn's
// end batch. The core calls it on the run goroutine between steps,
// never while the session's mutex is held.
func (s *Session) steerSource(_ context.Context, at weft.SteerPoint) []weft.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steerQueue) == 0 {
		return nil
	}
	msgs := make([]weft.Message, 0, len(s.steerQueue))
	for _, q := range s.steerQueue {
		msgs = append(msgs, q.msg)
		q.step = at.Step
		s.handed = append(s.handed, q)
	}
	s.steerQueue = s.steerQueue[:0]
	return msgs
}

// steerSendLocked is Send's Steer path (called under mu): the queued
// receipt entry is appended and flushed — accepted input is durable
// input (ADR 0011 §4) — and the message joins the live steer queue,
// unless the session's only busy state is an open approval boundary:
// a steer that meets pending approvals is never drained, so it
// defers at once and runs as the follow-up when the boundary resolves.
//
// The live queue has no bound of its own: every accepted steer costs
// one durable receipt entry and stays queued until a drain point, the
// turn's end or ClearQueue takes it. An application that must cap what
// a user can pile onto a running turn checks len(Queue()) before it
// sends.
func (s *Session) steerSendLocked(ctx context.Context, msg weft.Message, opts []weft.RunOption) (*Turn, error) {
	if msg.Role != weft.RoleUser {
		return nil, fmt.Errorf("thread: %w: a steered message must be RoleUser, got %q — the model's own turns come from the model",
			weft.ErrInvalidSteer, msg.Role)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t := s.newTurnLocked()
	t.policy, t.hasPolicy = Steer, true
	if !ValidID(t.id) {
		return nil, fmt.Errorf("thread: invalid entry id %q", t.id)
	}
	if _, dup := s.byID[t.id]; dup {
		return nil, fmt.Errorf("thread: entry id %q already held by session %s", t.id, s.header.ID)
	}
	clone := cloneMessage(msg)
	e := ReceiptEntry{ID: t.id, ParentID: s.leaf, Created: s.now(),
		Status: ReceiptQueued, Msg: &clone}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return nil, err
	}
	s.adoptLocked(e)
	q := queuedSteer{receipt: t.id, msg: msg, ctx: ctx, opts: opts, turn: t}
	if err := s.flushLocked(ctx); err != nil {
		// The receipt is written but not synced, and Send reports the
		// failure — yet the entry is on disk, and a reopen would
		// restore it to the steer queue. The live session must match:
		// the steer defers now instead of waiting forever in a queued
		// receipt nothing will drain.
		s.settleSteerLocked(q)
		return nil, fmt.Errorf("thread: steer receipt flush: %w", err)
	}
	if !s.running && s.boundaryLocked() {
		// Nothing runs to drain it, and the boundary holds the session:
		// the steer defers now, its follow-up queued like any Send's.
		if err := s.deferSteerLocked(q); err != nil {
			return nil, err
		}
		return t, nil
	}
	s.steerQueue = append(s.steerQueue, q)
	return t, nil
}

// deferSteerLocked settles a steer as a deferred follow-up (called
// under mu): the follow-up turn is minted, and one atomic append
// records both halves — the deferred receipt naming it, and the
// accepted receipt that makes the follow-up a durable queued send —
// before it joins the queue in acceptance order. The steer's Turn is
// linked to the follow-up through Next and ends deferred: the caller
// following Next watches the follow-up run. Callers hold mu.
func (s *Session) deferSteerLocked(q queuedSteer) error {
	ft := s.newTurnLocked()
	ft.policy, ft.hasPolicy = Steer, true
	ctx := context.WithoutCancel(q.ctx)
	receipt, err := s.acceptLocked(ctx, ft, q.msg, func(id, parent string, created time.Time) Entry {
		return ReceiptEntry{ID: id, ParentID: parent, Created: created,
			Receipt: q.receipt, Status: ReceiptDeferred, Turn: ft.id}
	})
	if err != nil {
		return err
	}
	s.queue = append(s.queue, pendingSend{ctx: q.ctx, msg: q.msg, opts: q.opts, turn: ft, receipt: receipt})
	if q.turn != nil {
		q.turn.setNext(ft)
		q.turn.finishAs(TurnDeferred, nil, nil)
	}
	return nil
}

// settleSteerLocked settles one accepted steer as a deferred follow-up
// (called under mu): the deferred receipt entry is appended, the
// follow-up turn minted and queued in acceptance order, and the
// steer's Turn linked to it through Next and finished. When the
// receipts cannot be persisted the failure is logged, not raised — the
// follow-up still runs from the in-memory queue, the message in hand:
// the turn has landed, and only the receipt entry stays queued (a
// reopen restores the steer from it).
func (s *Session) settleSteerLocked(q queuedSteer) {
	if err := s.deferSteerLocked(q); err != nil {
		s.agent.Logger().Error("thread: steer deferral not persisted",
			"session", s.header.ID, "receipt", q.receipt, "err", err)
		ft := s.newTurnLocked()
		ft.policy, ft.hasPolicy = Steer, true
		s.queue = append(s.queue, pendingSend{ctx: q.ctx, msg: q.msg, opts: q.opts, turn: ft})
		if q.turn != nil {
			q.turn.setNext(ft)
			q.turn.finishAs(TurnDeferred, nil, nil)
		}
	}
}

// settleSteersLocked runs at the end of every turn (called under mu):
// every steer still live when the run ended — it met a StopWhen end,
// the run parked approvals, or it arrived after the last drain point —
// defers to a follow-up, and so does every steer the run's drain took
// whose turn never recorded its end (a panicked session path, a turn
// batch the storage refused): handed-but-unrecorded is not a final
// state, and the message re-runs rather than dying with a transcript
// that is in no tree.
func (s *Session) settleSteersLocked() {
	for len(s.steerQueue) > 0 {
		q := s.steerQueue[0]
		s.steerQueue = s.steerQueue[1:]
		s.settleSteerLocked(q)
	}
	for len(s.handed) > 0 {
		q := s.handed[0]
		s.handed = s.handed[1:]
		s.settleSteerLocked(q)
	}
}

// Queue returns what the session has accepted and not yet given to a
// model, in the order it will get there: first the steers waiting for
// the running turn's next drain point (QueuedSteer.Policy is Steer),
// then the sends waiting for a turn of their own (Policy is Queue) —
// sends accepted while the session was busy, an interrupting send's
// follow-up, the follow-up a deferred steer became. Delivered steers
// and turns that have started are not listed — their stories live on
// the entries (Entries).
//
// On a session that was just opened, the queue holds what Open
// restored: steers and sends accepted by a writer that stopped before
// settling them. They wait here — Open runs nothing — until the
// session's next Send (the restored sends run ahead of it, in order,
// and a restored steer is delivered into the first turn that runs),
// Continue runs them now, or ClearQueue drops them.
//
// Queue lists messages; the Policy constant of the same name is the
// busy policy that queues them.
func (s *Session) Queue() []QueuedSteer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steerQueue) == 0 && len(s.queue) == 0 {
		return nil
	}
	out := make([]QueuedSteer, 0, len(s.steerQueue)+len(s.queue))
	for _, q := range s.steerQueue {
		out = append(out, QueuedSteer{Receipt: q.receipt, Msg: deepCloneMessage(q.msg), Policy: Steer})
	}
	for _, ps := range s.queue {
		out = append(out, QueuedSteer{Receipt: ps.turn.id, Msg: deepCloneMessage(ps.msg), Policy: Queue})
	}
	return out
}

// ClearQueue drops everything Queue lists — every steer not yet
// delivered and every send not yet started — marking each receipt
// dropped in one atomic append: the messages never reach the model,
// and a reopen does not restore them. Each dropped message's Turn ends
// with an error wrapping ErrDropped (Outcome TurnDropped). Steers
// already delivered or deferred are untouched — the runs that took
// them own them — though the follow-up a deferred steer became is a
// queued send like any other, and is dropped. A resume waiting for the
// runner is not queue: it is the boundary's resolution, and stays.
// ClearQueue returns how many messages it dropped.
func (s *Session) ClearQueue(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.steerQueue) + len(s.queue)
	if n == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now := s.now()
	var entries []Entry
	parent := s.leaf
	drop := func(receipt string) error {
		if receipt == "" {
			return nil // queued in memory only: nothing to settle
		}
		id, err := s.mintCheckedLocked(entries)
		if err != nil {
			return err
		}
		entries = append(entries, ReceiptEntry{ID: id, ParentID: parent, Created: now,
			Receipt: receipt, Status: ReceiptDropped})
		parent = id
		return nil
	}
	for _, q := range s.steerQueue {
		if err := drop(q.receipt); err != nil {
			return 0, err
		}
	}
	for _, ps := range s.queue {
		if err := drop(ps.receipt); err != nil {
			return 0, err
		}
	}
	if len(entries) > 0 {
		if err := s.appendEntriesLocked(ctx, entries...); err != nil {
			if _, written := s.byID[idOf(entries[0])]; !written {
				return 0, err
			}
			// Appended, not flushed: the drops are in the file and in
			// the tree, so the queue empties with them; the caller
			// still hears that their durability is not confirmed.
			s.endDroppedLocked()
			return n, fmt.Errorf("thread: dropped receipts flush: %w", err)
		}
	}
	s.endDroppedLocked()
	return n, nil
}

// endDroppedLocked empties both queues and ends every Turn in them as
// dropped. Callers hold s.mu and have recorded the drops.
func (s *Session) endDroppedLocked() {
	dropped := fmt.Errorf("%w: session %s", ErrDropped, s.header.ID)
	steers, sends := s.steerQueue, s.queue
	s.steerQueue, s.queue = nil, nil
	for _, q := range steers {
		q.turn.finishAs(TurnDropped, nil, dropped)
	}
	for _, ps := range sends {
		ps.turn.finishAs(TurnDropped, nil, dropped)
	}
}

// resurrectSteers recovers the crash window a reopen can see, for both
// kinds of accepted input (ADR 0011 §4: accepted input is durable
// input). A steer's queued receipt with no delivered, deferred or
// dropped entry linking back to it means the writer stopped between
// acceptance and the steer's fate: it returns to the live steer queue.
// A send's accepted receipt whose prompt entry never landed, and that
// no dropped entry settles, means the writer stopped while the send
// waited for its turn: it returns to the send queue, under the receipt
// id and the run id it was accepted with. Both in acceptance order,
// exactly as they stood — and that is all Open does with them: nothing
// is written and no run starts (Open's rule).
//
// From the queues they meet a queued message's ordinary fates: the
// next turn the session runs drains the steers, Continue or the next
// Send runs the sends, ClearQueue drops them. The Turns built here are
// the receipts' handles for those paths; no caller holds them
// (Continue returns the first). The run options a send was accepted
// with are gone — they are not entries. ctx is detached from Open's
// cancellation: a turn restored here must not die with the call that
// loaded the session. Continue binds the restored steers and sends to
// its own context; one that runs because of a later Send instead runs
// detached, with nobody's cancellation to answer to but Close's.
//
// A fork does not inherit its origin's queue: Fork settles the steers
// it copied as dropped, and an accepted receipt on the copied path is
// skipped here — the send it stands for belongs to the origin session.
func (s *Session) resurrectSteers(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	settled := map[string]bool{}
	for _, e := range s.order {
		if r, ok := e.(ReceiptEntry); ok && r.Receipt != "" {
			settled[r.Receipt] = true
		}
	}
	// The entries a Fork copied sit at and before the fork point in
	// append order. An accepted receipt among them is the origin
	// session's queued send, not this one's: the origin runs it.
	inherited := -1
	if p := s.header.Parent; p != nil && p.Entry != "" {
		if i, ok := s.byID[p.Entry]; ok {
			inherited = i
		}
	}
	for i, e := range s.order {
		r, ok := e.(ReceiptEntry)
		if !ok || r.Msg == nil || settled[r.ID] {
			continue
		}
		if r.Status == ReceiptAccepted && i <= inherited {
			continue
		}
		switch r.Status {
		case ReceiptQueued:
			s.steerQueue = append(s.steerQueue, queuedSteer{
				receipt: r.ID, msg: deepCloneMessage(*r.Msg), ctx: ctx, turn: newTurn(r.ID),
			})
		case ReceiptAccepted:
			if _, started := s.byID[r.Turn]; started || !ValidID(r.Turn) {
				continue // its prompt entry landed: the turn ran, or was running
			}
			t := newTurn(r.Turn)
			t.policy, t.hasPolicy = Queue, true
			t.runID = r.RunID
			if n, ok := runSeq(s.header.ID, r.RunID); ok {
				t.turn = n
			} else {
				// A receipt without a run id of this session's (a file
				// from other hands): mint one now.
				s.turnSeq++
				t.turn = s.turnSeq
				t.runID = fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq)
			}
			s.queue = append(s.queue, pendingSend{
				ctx: ctx, msg: deepCloneMessage(*r.Msg), turn: t, receipt: r.ID,
				restored: true,
			})
		}
	}
}

// runSeq parses the n of a run id <session>-t<n>.
func runSeq(session, runID string) (int, bool) {
	rest, ok := strings.CutPrefix(runID, session+"-t")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}
