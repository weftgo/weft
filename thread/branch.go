package thread

import (
	"context"
	"fmt"
	"time"
)

// BranchOption configures one Branch call — the per-call layer over
// the session's own SessionOptions. There is one: SummarizeLeft.
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
// everything — the next entry is then a new root, and the tree holds
// several. An id the session does not hold is an error.
//
// Branching is a between-turns operation: while a turn runs the
// session holds the line the run's transcript must land on, and a
// navigation underneath it would strand the run's messages on a
// branch whose context the model never saw — so a Branch while a
// turn is in flight fails with ErrBusy, like a Send under the Reject
// policy. A parked approval boundary is not a running turn:
// branching away from it is the documented way out of an unwanted
// boundary (the armed resume reads ErrNotPending). Fork is the
// operation that works mid-turn: it copies and never moves this
// session's leaf.
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
		if err := ctx.Err(); err != nil {
			return err
		}
		// Two entries, one atomic batch: the navigation off the old
		// leaf, then the summary sitting on the new line — its parent
		// is the branch point, so the walk from any future leaf
		// carries it in the abandoned branch's place. Branch summaries
		// share no cache prefix with the main line; that cost is
		// documented, not hidden.
		now := s.now()
		nav, err := s.mintCheckedLocked(nil)
		if err != nil {
			return err
		}
		batch := []Entry{LeafEntry{ID: nav, ParentID: s.leaf, Created: now, Entry: entryID}}
		sum, err := s.mintCheckedLocked(batch)
		if err != nil {
			return err
		}
		batch = append(batch, BranchSummaryEntry{ID: sum, ParentID: entryID, Created: now, Summary: summary, FromEntry: fromEntry})
		if err := s.appendEntriesLocked(ctx, batch...); err != nil {
			return err
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

// forkedStop is the Stop text of the settlement a fork writes for a
// pool delegation it copied unsettled: the child runs for the session
// that delegated it, and this copy of the receipt will never hear
// from it.
const forkedStop = "forked: the delegation belongs to the session this one was forked from"

// Fork copies the session's path root → entryID into a new session in
// the same storage, and returns it positioned at the end of that
// path: the new session is self-contained — its file holds the whole
// copied path, the entries keeping the ids they were born with — and
// traceable, its header naming the session and entry it grew from
// (ADR 0011 §3). entryID follows Branch's rule — any held entry, or
// "" for the root (a fork of an empty session is an empty session
// with a parent) — with one exception: a leaf entry is a navigation,
// not a position, and Fork rejects it; fork the entry it navigates
// to.
//
// # Options
//
// opts configure the new session, not this one, exactly as they
// would at Create: IDs mints the fork's session id and its later
// entries, Clock stamps its header, WithMeta and PublicID fill its
// header metadata, WithLineage its pool lineage; the busy policy,
// compaction and approval options are the fork's own. Nothing is
// inherited from this session's options or header — not its public
// id, not its header metadata: a fork is another session — with one
// exception, the signing rule: a fork of a session that requires
// signed decisions (RequireSigned) requires them too, recorded in its
// own header, and takes this session's keyring when opts give it
// none. A copy must not be a way around the rule its origin's
// approvals were held to.
//
// # What a fork inherits
//
// Everything recorded on the copied path, because the path is copied
// whole:
//
//   - the conversation, its compactions and branch summaries: the
//     fork's Context at entryID is this session's;
//   - the title and the SetInfo metadata — the info entries on the
//     path (the fork starts with this session's title, and renames
//     itself with SetInfo);
//   - labels, custom entries, the turn ledger (Usage counts the
//     copied turns, and the fork's run ids continue after them), and
//     the compaction trigger's last measurement;
//   - grants and their revocations, with the uses the path recorded;
//   - an open approval boundary: if the path ends on parked calls the
//     fork parks on them too, with their requests and whatever
//     decisions the path holds. Each session then resolves its own
//     copy — a call approved in both runs in both.
//
// And what it does not — the fork copies nothing that would start a
// run, and nothing that reaches into this session's work:
//
//   - the running turn: it lives in this Session value, not in the
//     tree;
//   - queued sends and queued steers: a send accepted and still
//     waiting for its turn, or a steer still waiting, on the path is
//     recorded as dropped in the fork (one receipt entry each,
//     appended after the copy), so the fork's file says what became
//     of it and reopening the fork never restores it — the message
//     belongs to this session, which runs it;
//   - mirrored child approval requests (thread/pool): they are
//     handles on this session's children, so they are left out of
//     the copy — the one case where a copied entry's parent link is
//     rewritten, to the nearest entry the fork does hold. The fork
//     cannot decide another session's children;
//   - pool delegations still unsettled on the path: each is settled
//     in the fork as canceled, with a Stop naming the fork. A call
//     parked on one — a sync delegation's wrapper — stays as an
//     ordinary parked call the fork decides itself;
//   - in a session opened under Salvage, an orphaned entry at the
//     head of the path is copied as the fork's root: the fork's file
//     is whole, and loads without Salvage.
//
// When the fork had to append such settling entries its Leaf is the
// last of them rather than entryID; its Context is the same either
// way.
//
// # During a turn
//
// Unlike Branch, Fork is allowed while a turn runs: it takes a
// snapshot under the session's lock and writes somewhere else, so it
// never disturbs the run. The snapshot is of what has been appended:
// forking at the running turn's latest entry copies a transcript the
// run is still extending, and calls the run has issued but not yet
// answered read in the fork as an open boundary (Pending lists them;
// Resume denies the undecided ones). Fork also works on a closed
// session — it reads this session and writes another.
//
// The fork is durable when Fork returns. If its entries cannot be
// written, the half-made session is deleted again and the error
// returned.
func (s *Session) Fork(ctx context.Context, entryID string, opts ...SessionOption) (*Session, error) {
	cfg := resolveSession(opts...)
	h, err := newHeader(&cfg)
	if err != nil {
		return nil, err
	}
	h.Parent = &ParentRef{Session: s.header.ID, Entry: entryID}
	// A fork of a session that requires signed decisions requires
	// them too, and takes the origin's keyring when given none.
	if err := cfg.inheritApprovals(&s.cfg, &h); err != nil {
		return nil, err
	}

	// The snapshot: everything Fork reads of this session, under its
	// lock; the writes below go to another session and need no lock
	// of this one.
	s.mu.Lock()
	if i, ok := s.byID[entryID]; ok {
		if _, isLeaf := s.order[i].(LeafEntry); isLeaf {
			s.mu.Unlock()
			return nil, fmt.Errorf("thread: Fork at leaf entry %q: a navigation is not a position — fork the entry it navigates to", entryID)
		}
	}
	path, err := s.pathLocked(entryID)
	st := s.storageLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	entries, err := forkEntries(&cfg, path)
	if err != nil {
		return nil, err
	}
	if err := st.Create(ctx, h); err != nil {
		return nil, err
	}
	f, err := s.writeFork(ctx, st, cfg, h, entries)
	if err != nil {
		// A fork that holds half of nothing is not a session anyone
		// asked for: remove it, best effort, and say why.
		if derr := st.Delete(context.WithoutCancel(ctx), h.ID); derr != nil {
			s.agent.Logger().Warn("thread: failed fork not removed",
				"session", h.ID, "forked_from", s.header.ID, "err", derr)
		}
		return nil, err
	}
	return f, nil
}

// writeFork stores the fork's entries and builds its Session through
// the constructor Open uses, so a fork derives its state — leaf, run
// counter, trigger measurement, resolved compaction — exactly as a
// reopen of its file would.
func (s *Session) writeFork(ctx context.Context, st Storage, cfg sessionConfig, h Header, entries []Entry) (*Session, error) {
	if len(entries) > 0 {
		if err := st.Append(ctx, h.ID, entries...); err != nil {
			return nil, err
		}
	}
	f, err := newSession(st, s.agent, cfg, h, entries, nil)
	if err != nil {
		return nil, err
	}
	if err := f.claim(ctx); err != nil { // the fork's Session is its writer from birth, like Create's
		return nil, err
	}
	if err := f.flushLocked(ctx); err != nil { // f is not shared yet: no lock to take
		return nil, fmt.Errorf("thread: fork flush: %w", err)
	}
	return f, nil
}

// forkEntries turns a copied path into the entries a fork's file
// holds (Fork's inheritance rules): the path itself, minus the
// mirrored child requests, each entry attached to the one before it
// in the copy; then one settling entry per steer still queued, per
// send accepted and not yet started, and per pool delegation still
// unsettled on it, minted through the fork's own ids and clock.
func forkEntries(cfg *sessionConfig, path []Entry) ([]Entry, error) {
	entries := make([]Entry, 0, len(path))
	last := ""
	for _, e := range path {
		if re, ok := e.(ApprovalRequestEntry); ok && re.Child != "" {
			continue // a handle on the original's child: not the fork's to hold
		}
		// A path is a chain, so in the copy each entry's parent is the
		// entry kept before it. That already holds except after a
		// mirror left out above, and at an orphaned head (Salvage),
		// whose stored parent no file holds.
		if parentOf(e) != last {
			e = withParent(e, last)
		}
		entries = append(entries, e)
		last = idOf(e)
	}

	settledSteer := map[string]bool{}
	settledPool := map[string]bool{}
	for _, e := range entries {
		switch e := e.(type) {
		case ReceiptEntry:
			if e.Receipt != "" {
				settledSteer[e.Receipt] = true
			}
		case PoolReceiptEntry:
			if e.Receipt != "" && isPoolSettled(e.Status) {
				settledPool[e.Receipt] = true
			}
		}
	}
	held := make(map[string]bool, len(entries))
	for _, e := range entries {
		held[idOf(e)] = true
	}
	mint := func() (string, error) {
		id := NewEntryID()
		if cfg.ids != nil {
			id = cfg.ids()
		}
		if !ValidID(id) {
			return "", fmt.Errorf("thread: invalid entry id %q", id)
		}
		if held[id] {
			return "", fmt.Errorf("thread: entry id %q already held by the forked path", id)
		}
		held[id] = true
		return id, nil
	}
	copied := entries // the settling entries join behind the copied path
	for _, e := range copied {
		var settle Entry
		switch e := e.(type) {
		case ReceiptEntry:
			if e.Receipt != "" || settledSteer[e.ID] {
				continue
			}
			switch e.Status {
			case ReceiptQueued:
			case ReceiptAccepted:
				if held[e.Turn] {
					continue // its prompt entry is on the path: the turn started
				}
			default:
				continue
			}
			settle = ReceiptEntry{Receipt: e.ID, Status: ReceiptDropped}
		case PoolReceiptEntry:
			if e.Receipt != "" || settledPool[e.ID] {
				continue
			}
			settle = PoolReceiptEntry{Receipt: e.ID, Status: PoolCanceled, Child: e.Child, Stop: forkedStop}
		default:
			continue
		}
		id, err := mint()
		if err != nil {
			return nil, err
		}
		switch r := settle.(type) {
		case ReceiptEntry:
			r.ID, r.ParentID, r.Created = id, last, cfg.now()
			settle = r
		case PoolReceiptEntry:
			r.ID, r.ParentID, r.Created = id, last, cfg.now()
			settle = r
		}
		entries = append(entries, settle)
		last = id
	}
	return entries, nil
}
