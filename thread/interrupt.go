package thread

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/weftgo/weft"
)

// reasonInterrupted is the denial reason an Interrupt or Rollback send
// gives the calls parked on an open approval boundary it supersedes —
// model-visible through the denied result's text, pinned by test.
const reasonInterrupted = "interrupted by a newer message"

// interruptedCallResult is the model-visible completion an interrupted
// turn gives a call that never got its result — the golden text (the
// pairing invariant: a fed-back transcript must be sound, and the
// model must see why the call has no answer).
func interruptedCallResult(name string) string {
	return fmt.Sprintf("tool call %s was interrupted: the run was canceled for a newer message", name)
}

// interruptSendLocked is Send's Interrupt and Rollback path (called
// under mu, in one critical section — no other Send can slip between
// its steps): the message is accepted as the next turn exactly like
// the Queue policy — durably, an accepted receipt first — and then the
// busy state is felled: the in-flight run's context is canceled (its
// Turn marked interrupted, and a Rollback remembers where the leaf
// must return to), or, when only an approval boundary holds the
// session, its parked calls are denied with the interrupted reason so
// the follow-up can run.
//
// The denial goes through the session's own recorder (Via
// "interrupt"), not the exported Decide: an interrupt is the session's
// path, so it records under RequireSigned too instead of wedging on
// the unsigned door. And it fails loudly: when the denial cannot be
// recorded the boundary still holds the session, so the Send returns
// the error and the message leaves the queue again — its receipt
// marked dropped — because a follow-up accepted behind a boundary
// nothing will clear would never run.
func (s *Session) interruptSendLocked(ctx context.Context, msg weft.Message, opts []weft.RunOption, policy Policy) (*Turn, error) {
	t, err := s.enqueueLocked(ctx, msg, opts, policy)
	if err != nil {
		return nil, err
	}
	if s.running && s.inFlight != nil {
		it := s.inFlight
		it.mu.Lock()
		it.interrupted = true
		if policy == Rollback {
			it.rollback = true
			if i, ok := s.byID[it.id]; ok {
				it.preTurn = parentOf(s.order[i]) // the leaf before this turn's receipt entry
			} else {
				// The receipt has not landed yet — the runner marks a
				// turn in-flight before its prompt appends (an
				// interrupt at birth), and a resume turn's entry lands
				// only at its end. The leaf before this Send's own
				// accepted receipt is the line before the turn either
				// way: the rollback targets it instead of silently
				// no-op'ing.
				it.preTurn = s.leafBeforeLocked(t)
			}
		}
		cancel := it.cancel
		it.mu.Unlock()
		// The mark outlives the arm: an Interrupt that lands between
		// Send and the runner's setRunCancel (cancel still nil here)
		// is not lost — runOne checks wasInterrupted when it arms the
		// cancel and fells the run at birth.
		if cancel != nil {
			// Safe under mu: cancel closes a channel; the woken run
			// goroutines that need mu (the turn's own persistence) block
			// until this Send returns.
			cancel()
		}
		return t, nil
	}
	// Only a boundary holds the session: deny its parked calls so the
	// follow-up is not queued behind it.
	if err := s.denyPendingLocked(ctx, reasonInterrupted, viaInterrupt); err != nil {
		s.dequeueLocked(context.WithoutCancel(ctx), t)
		return nil, fmt.Errorf("thread: interrupt could not deny the parked approvals: %w", err)
	}
	return t, nil
}

// leafBeforeLocked returns the leaf as it stood before t's accepted
// receipt entry was appended — the receipt is the queue's bookkeeping,
// not part of the line a rollback returns to. Callers hold s.mu.
func (s *Session) leafBeforeLocked(t *Turn) string {
	for _, ps := range s.queue {
		if ps.turn == t && ps.receipt != "" {
			if i, ok := s.byID[ps.receipt]; ok {
				return parentOf(s.order[i])
			}
		}
	}
	return s.leaf
}

// dequeueLocked takes t back out of the send queue — an acceptance
// that is being refused after all — and settles its accepted receipt
// as dropped, so neither this session nor a reopened one runs it.
// Callers hold s.mu.
func (s *Session) dequeueLocked(ctx context.Context, t *Turn) {
	for i, ps := range s.queue {
		if ps.turn != t {
			continue
		}
		s.queue = append(s.queue[:i], s.queue[i+1:]...)
		s.dropAcceptedLocked(ctx, ps.receipt)
		t.finishAs(TurnDropped, nil, fmt.Errorf("%w: session %s: the send was refused", ErrDropped, s.header.ID))
		return
	}
}

// rollbackLocked branches the leaf back to before the interrupted
// turn's receipt entry — nothing is deleted, the interrupted turn
// keeps its entries on its own line of the tree. The target is the
// root when the interrupted turn was the session's first: the
// follow-up then starts a new line from nothing. Callers hold mu; it
// runs at the runner's item boundary, after the interrupted turn's
// entries have landed and before the follow-up starts, so the
// follow-up's prompt attaches to the rolled-back leaf.
func (s *Session) rollbackLocked(t *Turn) {
	rollback, preTurn := t.rollbackTarget()
	if !rollback {
		return
	}
	if err := s.checkEntryLocked(preTurn); err != nil {
		// The target left the tree (a concurrent writer?): the leaf
		// stays where it is — loud in the log, never a broken tree.
		s.agent.Logger().Warn("thread: rollback target missing; the leaf stays",
			"session", s.header.ID, "target", preTurn, "err", err)
		return
	}
	if err := s.appendLocked(context.WithoutCancel(context.Background()), func(id, parent string, created time.Time) Entry {
		return LeafEntry{ID: id, ParentID: parent, Created: created, Entry: preTurn}
	}); err != nil {
		s.agent.Logger().Error("thread: rollback not persisted",
			"session", s.header.ID, "target", preTurn, "err", err)
		return
	}
	// A navigation off a parked tail may clear the approval boundary
	// that held queued sends: the runner restarts at the queue's head
	// (the same rule as Branch).
	s.kickRunnerLocked()
}

// withInterruptedResults completes an interrupted partial transcript:
// every call of the last assistant turn that never received its result
// carries one now — the golden interruption text — so the recorded
// transcript stays sound model input (the pairing invariant; the
// repair that follows has nothing left to synthesise). The tool
// message is rebuilt in the assistant's call order.
func withInterruptedResults(msgs []weft.Message) []weft.Message {
	i := -1
	for j := len(msgs) - 1; j >= 0; j-- {
		if msgs[j].Role != weft.RoleAssistant {
			continue
		}
		for _, p := range msgs[j].Content {
			if _, ok := p.(weft.ToolCallPart); ok {
				i = j
			}
		}
		break
	}
	if i < 0 {
		return msgs
	}
	served := map[string]weft.ToolResultPart{}
	if i+1 < len(msgs) && msgs[i+1].Role == weft.RoleTool {
		for _, p := range msgs[i+1].Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				served[r.CallID] = r
			}
		}
	}
	var parts []weft.Part
	for _, p := range msgs[i].Content {
		c, ok := p.(weft.ToolCallPart)
		if !ok {
			continue
		}
		if r, ok := served[c.ID]; ok && !isCancellationNoise(r.Content) {
			parts = append(parts, r)
			continue
		}
		// No result at all, or the bare cancellation noise a handler
		// returned as the run died under it — the model sees the golden
		// interruption text either way.
		parts = append(parts, weft.ToolResultPart{
			CallID:  c.ID,
			Name:    c.Name,
			IsError: true,
			Content: interruptedCallResult(c.Name),
		})
	}
	if len(parts) == 0 {
		return msgs
	}
	out := slices.Clone(msgs)
	if i+1 < len(out) && out[i+1].Role == weft.RoleTool {
		// The step's results message is rebuilt whole, in call order —
		// every call of the batch takes its kept result or the golden
		// text. Keeping the originals beside the rebuild would
		// duplicate every result, and leave the cancellation noise
		// beside its replacement: one result per call is the invariant.
		out[i+1].Content = parts
		return out
	}
	tool := weft.Message{Role: weft.RoleTool, Content: parts}
	return slices.Insert(out, i+1, tool)
}

// isCancellationNoise names the bare context-error texts a tool
// handler returns when the interrupt fells the run under it — true
// failures keep their own words.
func isCancellationNoise(content string) bool {
	return content == context.Canceled.Error() || content == context.DeadlineExceeded.Error()
}

// compactForOverflow runs the compaction an overflowed turn needs
// before its re-run (ADR 0020 §5): reason overflow, no window check —
// the provider just proved the request exceeded it. An open approval
// boundary holds the tail raw (ADR 0021 §1): the compaction is
// skipped and the re-run is not armed; the turn fails with the
// overflow, resumable once the boundary resolves.
func (s *Session) compactForOverflow(ctx context.Context) error {
	s.mu.Lock()
	open := s.boundaryLocked()
	s.mu.Unlock()
	if open {
		return fmt.Errorf("thread: an open approval boundary holds the tail; overflow compaction skipped")
	}
	c, err := s.computeCompaction(ctx, ReasonOverflow, "")
	if err != nil {
		return err
	}
	return s.ApplyCompaction(ctx, c)
}

type reRunOption bool

func (o reRunOption) applySession(c *sessionConfig) { c.reRunOnOverflow = bool(o) }

// ReRunOnOverflow sets whether a turn that fails with
// weft.ErrContextOverflow compacts (reason overflow) and re-runs once
// over the shrunken path (ADR 0020 §5) — on by default. A second
// overflow fails the turn with both errors joined; with the re-run
// off, the first overflow fails it directly.
func ReRunOnOverflow(on bool) SessionOption { return reRunOption(on) }
