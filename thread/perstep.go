package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/weftgo/weft/core"
)

// Per-step durability (ADR 0011 §7): a turn's messages are appended to
// the tree as they join the run's transcript — through core.OnMessages,
// the core's transcript observer — so a crash mid-turn loses nothing
// emitted. The step entries are the same message entries the turn-end
// batch used to write alone; the turn's end then appends only what the
// tree does not hold yet (normally nothing) plus its bookkeeping: the
// turn entry, receipts, the decision chain's entries.
//
// Exactly once, in order: the tree's tail for a turn is always a prefix
// of the messages the run added. A step batch whose append fails is
// held and written — in order, ahead of the next batch — by the next
// step or by the turn's end, which compares the tree against the run's
// transcript message by message and writes what is missing; a tail that
// differs from the run's (a failed turn's repaired form) is replaced
// from the first differing message on, on a fresh line.
//
// A turn that dies mid-step may leave a dangling call in the tree — an
// assistant message whose tool message never arrived. The loop repairs
// its input on every run (core.Repair at the run's start), with the
// same golden completion bytes the turn-end repair used to write, so
// the model sees an identical transcript either way; only the tree's
// stored form differs (raw tail, repaired at read).

// stepPersist is one run attempt's per-step persistence state. Every
// field is read and written under Session.mu only.
type stepPersist struct {
	// startLeaf is the entry the attempt's own messages attach after:
	// the leaf when the attempt started, moved past the resume join
	// when one lands (the join completes the input; it is not one of
	// the run's new messages). It is where the turn's tail is read from
	// and where an overflow re-run branches back to, so the failed
	// attempt's step messages leave the active path and stay on their
	// own branch — the transcript ADR 0020 §5 says a failed overflow
	// attempt records.
	startLeaf string
	// resume marks a resume run's attempt: its first observed batch may
	// be the join — the completed tool message of the boundary.
	resume bool
	// seen is set by the first observed batch.
	seen bool
	// observed counts the messages the run added beyond its input —
	// every message the observer was handed except the join. The run's
	// input length is its transcript's length minus this.
	observed int
	// join is a resume join the tree does not hold yet, and backlog the
	// step messages it does not hold yet, in order: both are retried
	// ahead of the next batch.
	join    *core.Message
	backlog []core.Message
	// late counts the appends that failed during the attempt: each was
	// a window in which a crash would have lost emitted messages.
	late int
	// err is the first such failure.
	err error
}

// observer returns the run-scoped transcript observer for one attempt.
// ctx is the turn's persistence context — the WithoutCancel window the
// turn-end batch uses — because the observer's own ctx is the run's,
// and a step's durability must not die with a canceled caller.
func (s *Session) observer(ctx context.Context, sp *stepPersist) core.RunOption {
	return core.OnMessages(func(_ context.Context, _ int, msgs []core.Message) {
		s.persistStep(ctx, sp, msgs)
	})
}

// persistStep appends one batch of step messages as message entries,
// atomically, and adopts them — the entries the turn-end batch would
// have written, written when they happen. A resume run's first batch,
// when it is a tool message, is the join: the boundary's completed
// tool message, which takes the place of the partial one (appendJoin).
//
// A persistence failure is logged, counted (TurnEntry.LateSteps) and
// the batch held: the next batch's append writes it first, in order,
// and the turn's end writes whatever is still held — so a failed step
// write is written late, never lost and never out of order. Callers
// hold nothing; the session lock is taken here.
func (s *Session) persistStep(ctx context.Context, sp *stepPersist, msgs []core.Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	first := !sp.seen
	sp.seen = true
	if first && sp.resume && msgs[0].Role == core.RoleTool {
		// The core reports the resume's completed tool message before
		// any step runs (the transcript's one in-place growth point).
		j := cloneMessage(msgs[0])
		sp.join = &j
		msgs = msgs[1:]
	}
	sp.observed += len(msgs)
	for _, m := range msgs {
		sp.backlog = append(sp.backlog, cloneMessage(m))
	}
	if err := s.flushStepsLocked(ctx, sp); err != nil {
		sp.late++
		if sp.err == nil {
			sp.err = err
		}
		s.agent.Logger().Warn("thread: step not persisted; held for the next step or the turn's end",
			"session", s.header.ID, "err", err)
	}
}

// flushJoinLocked writes a join the tree does not hold yet. Callers
// hold s.mu.
func (s *Session) flushJoinLocked(ctx context.Context, sp *stepPersist) error {
	if sp.join == nil {
		return nil
	}
	if err := s.appendJoinLocked(ctx, sp, *sp.join); err != nil {
		return err
	}
	sp.join = nil
	return nil
}

// flushStepsLocked writes what the attempt holds and the tree does
// not: the join first, then the held step messages, in order. Callers
// hold s.mu.
func (s *Session) flushStepsLocked(ctx context.Context, sp *stepPersist) error {
	if err := s.flushJoinLocked(ctx, sp); err != nil {
		return err
	}
	if len(sp.backlog) == 0 {
		return nil
	}
	var entries []Entry
	parent := s.leaf
	now := s.now()
	for _, m := range sp.backlog {
		id, err := s.mintCheckedLocked(entries)
		if err != nil {
			return err
		}
		entries = append(entries, MessageEntry{ID: id, ParentID: parent, Created: now, Message: m})
		parent = id
	}
	if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
		return err
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	sp.backlog = nil
	return nil
}

// appendJoinLocked persists a resume's join (ADR 0011 §7): the tool
// message that completes the boundary — every call of the parked step
// answered, in call order — exactly where the core put it, directly
// after the assistant message whose calls it answers.
//
// When no message sits between that assistant entry and the leaf (the
// step parked every call), the join is appended at the leaf and the
// boundary's bookkeeping stays on the path. When the parking run left
// a partial tool message there (one call ran, another parked), the
// join replaces it the append-only way: it is appended as a child of
// the assistant entry — a new line of the tree, the leaf moving with it
// — so the active path holds one tool message, the complete one, and
// the partial stays on its own line with the boundary's bookkeeping,
// evidence like every abandoned line. Messages an application wrote
// after the parked step (CustomMessage) follow the join as fresh
// copies, in order, where the core's transcript has them. The batch is
// one atomic Append: there is no state between "partial" and
// "complete" for a crash to land in.
//
// The join completes the run's input; the run's own messages attach
// after it, so the attempt's start moves here. Callers hold s.mu.
func (s *Session) appendJoinLocked(ctx context.Context, sp *stepPersist, tool core.Message) error {
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return err
	}
	// The assistant the join answers: the last assistant message of the
	// context — the core's own rule (the boundary is the transcript's
	// last assistant message with calls).
	asst := -1
	for i := len(path) - 1; i >= 0; i-- {
		if m, ok := contextMessage(path[i]); ok && m.Role == core.RoleAssistant {
			asst = i
			break
		}
	}
	parent := s.leaf
	var carry []Entry
	if asst >= 0 {
		replace := false
		for _, e := range path[asst+1:] {
			m, ok := contextMessage(e)
			if !ok {
				continue
			}
			replace = true
			if m.Role != core.RoleTool { // the partial tool message is what the join replaces
				carry = append(carry, e)
			}
		}
		if replace {
			parent = idOf(path[asst])
		} else {
			carry = nil
		}
	}
	now := s.now()
	var entries []Entry
	id, err := s.mintCheckedLocked(entries)
	if err != nil {
		return err
	}
	entries = append(entries, MessageEntry{ID: id, ParentID: parent, Created: now, Message: tool})
	parent = id
	for _, e := range carry {
		id, err := s.mintCheckedLocked(entries)
		if err != nil {
			return err
		}
		entries = append(entries, restamp(cloneEntry(e), id, parent, now))
		parent = id
	}
	if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
		return err
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	sp.startLeaf = s.leaf
	return nil
}

// restamp returns a context-message entry — the three kinds whose
// content the model sees — as a new entry: the same content under a
// new id, parent and time. Any other kind is returned unchanged.
func restamp(e Entry, id, parent string, created time.Time) Entry {
	switch e := e.(type) {
	case MessageEntry:
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	case CustomMessageEntry:
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	case BranchSummaryEntry:
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	}
	return e
}

// branchBackLocked appends a leaf entry navigating to entry — the
// overflow re-run's reset (ADR 0011 §7 with ADR 0020 §5): the failed
// attempt's step messages stay on their own branch (evidence, never
// deleted, out of every context), and the re-run continues from where
// the turn started. Callers hold s.mu.
func (s *Session) branchBackLocked(ctx context.Context, entry string) error {
	if entry == "" || entry == s.leaf {
		return nil // nothing to leave
	}
	if _, ok := s.byID[entry]; !ok {
		// The start leaf was branched away from under the run — a caller
		// navigated mid-turn. Leave the tree as the caller shaped it.
		return nil
	}
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return LeafEntry{ID: id, ParentID: parent, Created: created, Entry: entry}
	})
}

// turnEndMessages plans the message entries of the turn-end batch: the
// run's messages beyond its input in their final form — as the run
// left them on success, repaired on failure (the ADR's rule) — set
// against treeTail, the messages the tree already holds for the turn
// (s.turnTailLocked). keep is how many of the tree's messages match
// the final form, compared one by one from the start; msgs is what the
// batch must write after them. When keep is short of the tree's tail,
// the tree holds something the final form does not — an interrupted
// turn's raw results, a tail the repair rewrote, a message written out
// of order — and the batch attaches after the last matching entry, on
// a fresh line: the differing entries leave the active path and stay
// in the tree.
//
// The repair runs over the whole transcript, never the tail slice
// alone: a tool message of the tail may pair with an assistant inside
// the input and would read as an orphan in the slice. A transcript
// that holds nothing beyond the input plans nothing.
func turnEndMessages(full []core.Message, inputLen int, treeTail []core.Message, err error) (keep int, msgs []core.Message) {
	inputLen = max(inputLen, 0)
	if len(full) <= inputLen {
		return len(treeTail), nil
	}
	final := full
	if err != nil {
		final = core.Repair(full)
	}
	want := final[min(inputLen, len(final)):] // repair dropped inside the input: nothing beyond it to write
	for keep < len(treeTail) && keep < len(want) && sameMessage(treeTail[keep], want[keep]) {
		keep++
	}
	return keep, want[keep:]
}

// sameMessage reports whether two messages are the same transcript
// content: equal values, or — a nil slice against an empty one — equal
// wire bytes.
func sameMessage(a, b core.Message) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	if a.Role != b.Role || len(a.Content) != len(b.Content) {
		return false
	}
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)
	return aerr == nil && berr == nil && bytes.Equal(ab, bb)
}

// tailEntry is one message entry of a turn's tail: its id and message.
type tailEntry struct {
	id  string
	msg core.Message
}

// turnTailLocked returns the message entries the step observer
// appended for the run that started at startLeaf — the raw form of the
// turn's tail as the tree holds it — read along the active path from
// the leaf to startLeaf: the run's message entries above it, in order,
// and nothing else. Bookkeeping the turn admits between its steps — a
// queued steer's receipt, a label, an info or custom entry — sits on
// the same path and is skipped, not counted: only message entries are
// the run's, and every one of them above startLeaf is (a mid-run Branch
// is ErrBusy, so the path below the turn's start is fixed). A startLeaf
// the path does not reach (an empty one, a turn that never ran) owns
// no step messages. Callers hold s.mu.
func (s *Session) turnTailLocked(startLeaf string) []tailEntry {
	if startLeaf == "" {
		return nil
	}
	path, err := s.walkLocked(s.leaf) // read-only: the tail is compared, never kept or edited
	if err != nil {
		return nil // the leaf is always an entry the session holds
	}
	start := -1
	for i, e := range path {
		if idOf(e) == startLeaf {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var out []tailEntry
	for _, e := range path[start+1:] {
		if me, ok := e.(MessageEntry); ok {
			out = append(out, tailEntry{id: me.ID, msg: me.Message})
		}
	}
	return out
}
