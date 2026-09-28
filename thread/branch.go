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
// carries in the abandoned branch's place (ADR 0020 §6).
//
// Not in this build: step 1.6 appends the navigation only, and a
// Branch called with SummarizeLeft fails with ErrNotImplemented
// rather than navigating silently without its summary. Step 1.8 wires
// the summarizer and makes it work.
func SummarizeLeft() BranchOption { return summarizeLeftOption{} }

// Branch navigates the session to entryID: it appends a leaf entry
// (never a rewrite, ADR 0011 §3), and the next write attaches there —
// the model's context is rebuilt from that path alone, and nothing
// appended on the abandoned branch reaches it again (pi's invariant).
// entryID may name any entry the session holds, on any branch,
// including the current leaf (a recorded no-op navigation); "" is the
// root, restarting the conversation from nothing while the file keeps
// everything. An id the session does not hold is an error.
func (s *Session) Branch(ctx context.Context, entryID string, opts ...BranchOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID != "" {
		if _, ok := s.byID[entryID]; !ok {
			return fmt.Errorf("thread: session %s holds no entry %q", s.header.ID, entryID)
		}
	}
	if resolveBranch(opts...).summarizeLeft {
		return fmt.Errorf("%w: SummarizeLeft writes branch summaries from step 1.8 (ADR 0020 §6)", ErrNotImplemented)
	}
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return LeafEntry{ID: id, ParentID: parent, Created: created, Entry: entryID}
	})
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
	return f, nil
}
