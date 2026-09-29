package thread

import (
	"context"
	"reflect"
	"time"

	"github.com/weftgo/weft"
)

// Per-step durability (ADR 0011 §7, the v0.4 amendment): a turn's
// messages are appended to the tree as they join the run's transcript
// — through weft.OnMessages, the core's transcript observer — so a
// crash mid-turn loses nothing emitted. The step entries are the same
// message entries the turn-end batch used to write alone; the turn's
// end then appends only what the run added after the last step batch
// (normally nothing) plus its bookkeeping: the turn entry, receipts,
// the decision chain's entries.
//
// A turn that dies mid-step may leave a dangling call in the tree — an
// assistant message whose tool message never arrived. The loop repairs
// its input on every run (weft.Repair at the run's start), with the
// same golden completion bytes the turn-end repair used to write, so
// the model sees an identical transcript either way; only the tree's
// stored form differs (raw tail, repaired at read).

// stepPersist is one run attempt's per-step persistence state: the
// messages already in the tree (counted from the attempt's input) and
// the leaf the attempt started from — the target an overflow re-run
// branches back to, so the failed attempt's step messages leave the
// active path and stay on their own branch, exactly the transcript
// ADR 0020 §5 says a failed overflow attempt records. count is read
// and written under Session.mu only.
type stepPersist struct {
	count     int
	startLeaf string
}

// observer returns the run-scoped transcript observer for one attempt.
// ctx is the turn's persistence context — the WithoutCancel window the
// turn-end batch uses — because the observer's own ctx is the run's,
// and a step's durability must not die with a canceled caller.
func (s *Session) observer(ctx context.Context, sp *stepPersist) weft.RunOption {
	return weft.OnMessages(func(_ context.Context, _ int, msgs []weft.Message) {
		s.persistStep(ctx, sp, msgs)
	})
}

// persistStep appends one batch of step messages as message entries,
// atomically, and adopts them — the entries the turn-end batch would
// have written, written when they happen. A persistence failure is
// logged and nothing is counted: the messages are not in the tree, and
// the turn-end batch appends them itself, so a failed step write is
// repaired by the run's own ending, never lost. Callers hold nothing;
// the session lock is taken here.
func (s *Session) persistStep(ctx context.Context, sp *stepPersist, msgs []weft.Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []Entry
	parent := s.leaf
	for _, m := range msgs {
		id := s.mintIDLocked()
		entries = append(entries, MessageEntry{
			ID: id, ParentID: parent, Created: time.Now().UTC(), Message: cloneMessage(m),
		})
		parent = id
	}
	if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
		s.agent.Logger().Warn("thread: step not persisted; the turn-end batch will write it",
			"session", s.header.ID, "err", err)
		return
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	sp.count += len(msgs)
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

// turnEndMessages computes the message entries' payloads for the
// turn-end batch: the run's new messages beyond its input, minus what
// the step observer already appended (normally all of them), repaired
// on failure — the ADR's rule. treeTail is the raw form the tree holds
// (s.turnTailLocked); rewrite reports the failure case where that raw
// form differs from the final one (the interrupted turn's rebuilt
// completions, the repair's synthesized results): the whole final tail
// is returned then, for a rewrite on a fresh line, since appending
// only the changed suffix would leave the raw prefix in place under it.
//
// The repair runs over the whole transcript, never the tail slice
// alone: a resume's completed tool message pairs with an assistant
// inside the input and would read as an orphan in the slice — dropped,
// when the boundary it closed must survive the failed resume that
// recorded it (ADR 0011 §7: mid-resume, the tail can already read
// resolved). Repairing the whole also keeps the comparison in range:
// skip never exceeds the repaired tail's own length.
func turnEndMessages(full []weft.Message, inputLen int, treeTail []weft.Message, err error) (msgs []weft.Message, rewrite bool) {
	if len(full) <= inputLen {
		return nil, false
	}
	if err == nil {
		tail := full[inputLen:]
		skip := min(len(treeTail), len(tail)) // a counting bug clamps to a missed batch, not a panic
		return tail[skip:], false
	}
	repaired := weft.Repair(full)
	if inputLen > len(repaired) {
		inputLen = len(repaired) // repair dropped inside the input: nothing beyond it to write
	}
	tail := repaired[inputLen:]
	skip := min(len(treeTail), len(tail))
	if skip > 0 && !reflect.DeepEqual(tail[:skip], treeTail[:skip]) {
		return tail, true
	}
	return tail[skip:], false
}

// turnTailLocked returns the messages the step observer appended for
// the run that started at startLeaf — the raw form of the turn's tail
// as the tree holds it — read along the active path from the leaf to
// startLeaf: the run's message entries above it, in order, and nothing
// else. Bookkeeping the turn admits between its steps — a queued
// steer's receipt, a label, an info or custom entry — sits on the same
// path and is skipped, not counted: only message entries are the
// run's, and every one of them above startLeaf is (a mid-run Branch is
// ErrBusy, so the path below the turn's start is fixed). A startLeaf
// the path does not reach (an empty one, a turn that never ran) owns
// no step messages. Callers hold s.mu.
func (s *Session) turnTailLocked(startLeaf string) []weft.Message {
	if startLeaf == "" {
		return nil
	}
	path, err := s.pathLocked(s.leaf)
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
	var out []weft.Message
	for _, e := range path[start+1:] {
		if me, ok := e.(MessageEntry); ok {
			out = append(out, me.Message)
		}
	}
	return out
}
