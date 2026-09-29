package thread

import (
	"context"
	"fmt"
	"time"
)

// BranchOption configures one Branch call — the per-call layer over
// the session's own SessionOptions. v0.1 step 1.6 carries one:
// SummarizeLeft, wired in step 1.8.
type BranchOption interface {
	applyBranch(*branchConfig)
}

// branchConfig is a Branch call's resolved configuration.
type branchConfig struct {
	summarizeLeft bool
}

func resolveBranch(opts ...BranchOption) branchConfig {
	var cfg branchConfig
	for _, o := range opts {
		if o != nil {
			o.applyBranch(&cfg)
		}
	}
	return cfg
}

type summarizeLeftOption struct{}

func (summarizeLeftOption) applyBranch(c *branchConfig) { c.summarizeLeft = true }

// SummarizeLeft returns the BranchOption that summarizes the branch
// being left — back to the common ancestor of the branch and the new
// branch point — into a branch_summary entry the new branch's context
// carries in the abandoned branch's place (ADR 0020 §6). The
// summarizer runs under the session's compaction chain (SummaryModel
// or the session's own model, the skeleton prompt) with the lock
// released, then the navigation and the summary land as one atomic
// batch.
func SummarizeLeft() BranchOption { return summarizeLeftOption{} }

// Branch navigates the session to entryID: it appends a leaf entry
// (never a rewrite, ADR 0011 §3), and the next write attaches there —
// the model's context is rebuilt from that path alone, and nothing
// appended on the abandoned branch reaches it again (pi's invariant).
// entryID may name any entry the session holds, on any branch,
// including the current leaf (a recorded no-op navigation); "" is the
// root, restarting the conversation from nothing while the file keeps
// everything. An id the session does not hold is an error.
//
// Branching is a between-turns operation: while a turn runs the
// session holds the line the run's transcript must land on, and a
// navigation underneath it would strand the run's messages on a
// branch whose context the model never saw — so a Branch while a
// turn is in flight fails with ErrBusy, like a Send under the Reject
// policy. A parked approval boundary is not a running turn:
// branching away from it is the documented way out of an unwanted
// boundary (the armed resume reads ErrNotPending).
func (s *Session) Branch(ctx context.Context, entryID string, opts ...BranchOption) error {
	if resolveBranch(opts...).summarizeLeft {
		// Fail fast before the model call: the authoritative check is
		// under the lock at the append, but a turn already running
		// should not make the caller pay for a summary first.
		s.mu.Lock()
		busy := s.running && s.inFlight != nil
		s.mu.Unlock()
		if busy {
			return fmt.Errorf("%w: session %s is running a turn; branch between turns", ErrBusy, s.header.ID)
		}
		// Validate, then summarize with the lock released — the model
		// call takes seconds — then append the batch under it.
		if err := s.checkEntry(entryID); err != nil {
			return err
		}
		summary, fromEntry, err := s.summarizeBranch(ctx, entryID)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.running && s.inFlight != nil {
			return fmt.Errorf("%w: session %s is running a turn; branch between turns", ErrBusy, s.header.ID)
		}
		nav := s.mintIDLocked()
		sum := s.mintIDLocked()
		for _, id := range []string{nav, sum} {
			if !ValidID(id) {
				return fmt.Errorf("thread: invalid entry id %q", id)
			}
			if _, dup := s.byID[id]; dup {
				return fmt.Errorf("thread: entry id %q already held by session %s", id, s.header.ID)
			}
		}
		now := time.Now().UTC()
		// Two entries, one atomic batch: the navigation off the old
		// leaf, then the summary sitting on the new line — its parent
		// is the branch point, so the walk from any future leaf
		// carries it in the abandoned branch's place. Branch summaries
		// share no cache prefix with the main line; that cost is
		// documented, not hidden.
		batch := []Entry{
			LeafEntry{ID: nav, ParentID: s.leaf, Created: now, Entry: entryID},
			BranchSummaryEntry{ID: sum, ParentID: entryID, Created: now, Summary: summary, FromEntry: fromEntry},
		}
		if err := s.st.Append(ctx, s.header.ID, batch...); err != nil {
			return err
		}
		for _, e := range batch {
			s.adoptLocked(e)
		}
		// A navigation off a parked tail may clear the approval
		// boundary that held queued sends: the runner restarts at the
		// queue's head (ADR 0021 §5).
		s.kickRunnerLocked()
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && s.inFlight != nil {
		return fmt.Errorf("%w: session %s is running a turn; branch between turns", ErrBusy, s.header.ID)
	}
	if err := s.checkEntryLocked(entryID); err != nil {
		return err
	}
	if err := s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return LeafEntry{ID: id, ParentID: parent, Created: created, Entry: entryID}
	}); err != nil {
		return err
	}
	// A navigation off a parked tail may clear the approval boundary
	// that held queued sends: the runner restarts at the queue's head.
	s.kickRunnerLocked()
	return nil
}

// checkEntry validates a branch target without holding the lock.
func (s *Session) checkEntry(entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkEntryLocked(entryID)
}

// checkEntryLocked is checkEntry with s.mu held (Branch's rule: any
// held entry, or "" for the root).
func (s *Session) checkEntryLocked(entryID string) error {
	if entryID != "" {
		if _, ok := s.byID[entryID]; !ok {
			return fmt.Errorf("thread: session %s holds no entry %q", s.header.ID, entryID)
		}
	}
	return nil
}

// Fork copies the session's path root → entryID into a new session in
// the same storage, and returns it positioned at entryID: the new
// session is self-contained — its file holds the whole copied path
// with the entries' own ids and parent links — and traceable, its
// header naming the session and entry it grew from (ADR 0011 §3).
// entryID follows Branch's rule: any held entry, or "" for the root
// (a fork of an empty session is an empty session with a parent).
// opts configure the new session, not this one: its ids option mints
// the fork's session id and its later entries; the copied entries
// keep the ids they were born with. The fork starts with no title or
// metadata — those belong to the original's info entries.
func (s *Session) Fork(ctx context.Context, entryID string, opts ...SessionOption) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathLocked(entryID)
	if err != nil {
		return nil, err
	}
	cfg := resolveSession(opts...)
	id := NewSessionID()
	if cfg.ids != nil {
		id = cfg.ids()
	}
	if !ValidID(id) {
		return nil, fmt.Errorf("thread: invalid session id %q", id)
	}
	h := Header{
		ID:      id,
		Created: time.Now().UTC(),
		Parent:  &ParentRef{Session: s.header.ID, Entry: entryID},
	}
	if err := s.st.Create(ctx, h); err != nil {
		return nil, err
	}
	if len(path) > 0 {
		if err := s.st.Append(ctx, id, path...); err != nil {
			return nil, err
		}
	}
	f := &Session{
		st:     s.st,
		agent:  s.agent,
		cfg:    cfg,
		header: h,
		order:  append([]Entry(nil), path...),
		byID:   make(map[string]int, len(path)),
		leaf:   entryID,
	}
	for i, e := range path {
		if id := idOf(e); id != "" {
			f.byID[id] = i
		}
		if _, ok := e.(TurnEntry); ok {
			f.turns++
		}
	}
	// The copied turns count for run ids the way Open's recovery
	// counts them: the fork's first Send mints <fork>-t<n+1>, not
	// <fork>-t1 — the file already holds n turns, and a numbering
	// that restarts would read as a different session's history.
	f.turnSeq = f.turns
	return f, nil
}
