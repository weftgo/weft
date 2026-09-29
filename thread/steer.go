package thread

import (
	"context"
	"fmt"
	"time"

	"github.com/weftgo/weft"
)

// queuedSteer is one accepted steer waiting for the running turn's
// drain: the receipt entry's id, the message, the context its Send
// carried (the follow-up's persistence window if it defers), and the
// Turn the caller holds.
type queuedSteer struct {
	receipt string
	msg     weft.Message
	ctx     context.Context
	turn    *Turn
}

// QueuedSteer is one entry of the session's live steer queue, as
// Queue reports it: the receipt entry's id and the message held for
// delivery.
type QueuedSteer struct {
	// Receipt is the queued receipt entry's id — the handle the
	// steer's Turn reports as its own ID.
	Receipt string
	// Msg is the message held for the running turn's drain.
	Msg weft.Message
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
// defers at once and runs as the follow-up when the boundary resolves
// (plan §6).
func (s *Session) steerSendLocked(ctx context.Context, msg weft.Message) (*Turn, error) {
	if msg.Role != weft.RoleUser {
		return nil, fmt.Errorf("thread: a steered message must be RoleUser, got %q (weft.ErrInvalidSteer: the model's own turns come from the model)", msg.Role)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t := s.newTurnLocked()
	if !ValidID(t.id) {
		return nil, fmt.Errorf("thread: invalid entry id %q", t.id)
	}
	if _, dup := s.byID[t.id]; dup {
		return nil, fmt.Errorf("thread: entry id %q already held by session %s", t.id, s.header.ID)
	}
	clone := cloneMessage(msg)
	e := ReceiptEntry{ID: t.id, ParentID: s.leaf, Created: time.Now().UTC(),
		Status: ReceiptQueued, Msg: &clone}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return nil, err
	}
	s.adoptLocked(e)
	if f, ok := s.st.(Flusher); ok {
		if err := f.Flush(ctx, s.header.ID); err != nil {
			return nil, fmt.Errorf("thread: steer receipt flush: %w", err)
		}
	}
	q := queuedSteer{receipt: t.id, msg: msg, ctx: ctx, turn: t}
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
// under mu): the deferred receipt entry is appended, the follow-up
// turn minted and queued in acceptance order, and the steer's Turn
// linked to it through Next and finished — the caller following Next
// watches the follow-up run. Callers hold mu.
func (s *Session) deferSteerLocked(q queuedSteer) error {
	ft := s.newTurnLocked()
	if err := s.appendLocked(context.WithoutCancel(q.ctx), func(id, parent string, created time.Time) Entry {
		_ = id
		return ReceiptEntry{ID: id, ParentID: parent, Created: created,
			Receipt: q.receipt, Status: ReceiptDeferred, Turn: ft.id}
	}); err != nil {
		return err
	}
	s.queue = append(s.queue, pendingSend{ctx: q.ctx, msg: q.msg, turn: ft})
	if q.turn != nil {
		q.turn.setNext(ft)
		q.turn.finish(nil, nil)
	}
	return nil
}

// settleSteersLocked runs at the end of every turn (called under mu):
// every steer still live when the run ended — it met a StopWhen end,
// the run parked approvals, or it arrived after the last drain point —
// defers to a follow-up. A persistence failure is logged, not raised:
// the turn has landed, and the steer's follow-up still runs from the
// in-memory queue (the receipt entry alone stays queued).
func (s *Session) settleSteersLocked() {
	for len(s.steerQueue) > 0 {
		q := s.steerQueue[0]
		s.steerQueue = s.steerQueue[1:]
		if err := s.deferSteerLocked(q); err != nil {
			s.agent.Logger().Error("thread: steer deferral not persisted",
				"session", s.header.ID, "receipt", q.receipt, "err", err)
			// The follow-up still runs: the message is in hand, and the
			// caller's next interaction re-reads the tree.
			ft := s.newTurnLocked()
			s.queue = append(s.queue, pendingSend{ctx: q.ctx, msg: q.msg, turn: ft})
			if q.turn != nil {
				q.turn.setNext(ft)
				q.turn.finish(nil, nil)
			}
		}
	}
}

// Queue returns the session's live steer queue: the messages accepted
// under the Steer busy policy that are waiting for the running turn's
// drain, in acceptance order. Delivered and deferred steers are not
// listed — their stories live on the receipt entries (Entries).
func (s *Session) Queue() []QueuedSteer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steerQueue) == 0 {
		return nil
	}
	out := make([]QueuedSteer, 0, len(s.steerQueue))
	for _, q := range s.steerQueue {
		out = append(out, QueuedSteer{Receipt: q.receipt, Msg: q.msg})
	}
	return out
}

// ClearQueue drops every queued steer that has not been delivered,
// marking each receipt dropped: the messages never reach the model.
// Steers already delivered or deferred are untouched — their runs
// own them. It returns how many steers were dropped.
func (s *Session) ClearQueue(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.steerQueue)
	if n == 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	var entries []Entry
	parent := s.leaf
	for _, q := range s.steerQueue {
		id := s.mintIDLocked()
		entries = append(entries, ReceiptEntry{ID: id, ParentID: parent, Created: now,
			Receipt: q.receipt, Status: ReceiptDropped})
		parent = id
	}
	if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
		return 0, err
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	dropped := s.steerQueue
	s.steerQueue = nil
	for _, q := range dropped {
		q.turn.finish(nil, nil)
	}
	return n, nil
}

// resurrectSteers recovers the crash window a reopen can see: a
// ReceiptEntry still queued — no delivered, deferred or dropped entry
// links back to it — means the writer died between acceptance and the
// steer's fate. Each defers now (the deferred receipt entry, then the
// follow-up in the send queue), and the runner starts if the session
// is otherwise idle: an accepted steer is durable input, and it runs
// rather than waits (ADR 0011 §4, plan §6).
func (s *Session) resurrectSteers(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settled := map[string]bool{}
	for _, e := range s.order {
		if r, ok := e.(ReceiptEntry); ok && r.Receipt != "" {
			settled[r.Receipt] = true
		}
	}
	for _, e := range s.order {
		r, ok := e.(ReceiptEntry)
		if !ok || r.Status != ReceiptQueued || r.Msg == nil || settled[r.ID] {
			continue
		}
		if err := s.deferSteerLocked(queuedSteer{receipt: r.ID, msg: *r.Msg, ctx: context.WithoutCancel(ctx)}); err != nil {
			s.agent.Logger().Error("thread: steer resurrection not persisted",
				"session", s.header.ID, "receipt", r.ID, "err", err)
			continue
		}
	}
	s.kickRunnerLocked()
}
