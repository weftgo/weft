package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/weftgo/weft"
)

// SessionOption configures a session at Create or Open — one value per
// concern, folded over the defaults, the constructor convention the
// core set (ADR 0011 §6 borrows it). Options later steps add (busy
// policy and run options in step 1.7, the compaction layers in steps
// 1.8–1.9) join the same interface.
type SessionOption interface {
	applySession(*sessionConfig)
}

// sessionConfig is a session's resolved configuration; callers never
// construct it — Create and Open fold SessionOptions into it.
type sessionConfig struct {
	// ids mints session and entry ids; nil means the package's own
	// time-sortable ids (NewSessionID, NewEntryID).
	ids func() string
}

func resolveSession(opts ...SessionOption) sessionConfig {
	var cfg sessionConfig
	for _, o := range opts {
		if o != nil {
			o.applySession(&cfg)
		}
	}
	return cfg
}

type idsOption func() string

func (o idsOption) applySession(c *sessionConfig) { c.ids = o }

// IDs returns the SessionOption that takes every id a session mints —
// the session id Create generates and the entry id of each entry a
// Session appends — from id. Tests and examples pin deterministic ids
// with it. A nil id is ignored, leaving the default time-sortable ids;
// a value that fails ValidID fails the write that would carry it.
func IDs(id func() string) SessionOption {
	if id == nil {
		return nil // an ignored option, the constructor convention
	}
	return idsOption(id)
}

// A Session is a loaded conversation: the append-only entry tree
// (ADR 0011 §2) held in memory, every write going through the
// storage's Append, the leaf tracked as the entry the next one
// attaches to. Create or Open return one; it is safe for concurrent
// use by any number of goroutines. The Session is the session's one
// writer — entries appended to the storage behind its back are
// invisible to it until the next Open.
type Session struct {
	mu     sync.Mutex
	st     Storage
	agent  *weft.Agent
	cfg    sessionConfig
	header Header
	order  []Entry        // the whole tree, append order
	byID   map[string]int // entry id → index into order
	// leaf is the entry the next appended entry attaches to: the id of
	// the last entry, or the target of a trailing leaf entry (branch
	// navigation), or "" while the session holds no entries — the next
	// entry is then the root.
	leaf string
	// turns counts the turn entries ever appended, branches included —
	// the counter Send mints <session>-t<n> run ids from (step 1.7).
	turns int
}

// Create starts a new session in st: a fresh header under a new
// time-sortable id (or the IDs option's), the tree empty. agent is the
// session's own — Send runs it (step 1.7) and compaction summarizes
// with its model (step 1.8) — and must not be nil, like the core's
// New. The header is all that is written: nothing else lands in the
// storage until the first append.
func Create(ctx context.Context, st Storage, agent *weft.Agent, opts ...SessionOption) (*Session, error) {
	if st == nil {
		return nil, fmt.Errorf("thread: Create with nil storage")
	}
	if agent == nil {
		return nil, fmt.Errorf("thread: Create with nil agent")
	}
	cfg := resolveSession(opts...)
	id := NewSessionID()
	if cfg.ids != nil {
		id = cfg.ids()
	}
	if !ValidID(id) {
		return nil, fmt.Errorf("thread: invalid session id %q", id)
	}
	h := Header{ID: id, Created: time.Now().UTC()}
	if err := st.Create(ctx, h); err != nil {
		return nil, err
	}
	return &Session{
		st:     st,
		agent:  agent,
		cfg:    cfg,
		header: h,
		byID:   map[string]int{},
	}, nil
}

// Open loads an existing session from st, positioned at its leaf: the
// entry the next append attaches to, recovered by replaying the
// entries in append order (a trailing leaf entry redirects; any other
// entry leaves the leaf at itself). A load that had to drop a torn
// tail or skip a salvaged line reports it once, as a warning through
// the agent's logger — a repair is never silent (ADR 0011 §5) — and
// the session still opens: the entries that survived are the session.
func Open(ctx context.Context, st Storage, id string, agent *weft.Agent, opts ...SessionOption) (*Session, error) {
	if st == nil {
		return nil, fmt.Errorf("thread: Open with nil storage")
	}
	if agent == nil {
		return nil, fmt.Errorf("thread: Open with nil agent")
	}
	h, entries, report, err := st.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	s := &Session{
		st:     st,
		agent:  agent,
		cfg:    resolveSession(opts...),
		header: h,
		order:  entries,
		byID:   make(map[string]int, len(entries)),
	}
	leaf := ""
	for i, e := range entries {
		if id := idOf(e); id != "" {
			s.byID[id] = i
		}
		if le, ok := e.(LeafEntry); ok {
			leaf = le.Entry
		} else if id := idOf(e); id != "" {
			leaf = id
		}
		if _, ok := e.(TurnEntry); ok {
			s.turns++
		}
	}
	s.leaf = leaf
	if s.leaf != "" {
		if _, ok := s.byID[s.leaf]; !ok {
			// Loud on the undefined (ADR 0011 §5): a trailing leaf
			// entry that navigates to an entry the file does not hold
			// leaves the session's active position meaningless — every
			// read would quietly answer nothing. No code path here
			// writes one (Branch validates its target); a file that
			// holds one was written by something else.
			return nil, &CorruptError{Session: id, Err: fmt.Errorf(
				"the leaf names entry %q, which the file does not hold", s.leaf)}
		}
	}
	if report != nil {
		agent.Logger().Warn("thread: session loaded with a repair",
			"session", id, "torn_line", report.Torn, "skipped_lines", report.Skipped)
	}
	return s, nil
}

// List returns a page of session headers from st — headers only, never
// entries; see Query for the cursor and the limit. Listing needs no
// Session: it is the storage's List, nil storage aside.
func List(ctx context.Context, st Storage, q Query) (Page, error) {
	if st == nil {
		return Page{}, fmt.Errorf("thread: List with nil storage")
	}
	return st.List(ctx, q)
}

// Delete removes a session and its entries from st. A Session value
// already loaded keeps its in-memory tree, but its next append fails
// with ErrNotFound: history is removed with the session, never
// rewritten (ADR 0011 §5).
func Delete(ctx context.Context, st Storage, id string) error {
	if st == nil {
		return fmt.Errorf("thread: Delete with nil storage")
	}
	return st.Delete(ctx, id)
}

// ID returns the session's id — the header's, and the storage key.
func (s *Session) ID() string { return s.header.ID }

// Leaf returns the entry the next appended entry attaches to: the id
// of the last entry, the target of a trailing leaf entry, or "" while
// the session holds no entries (the next entry is the root).
func (s *Session) Leaf() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leaf
}

// Entries returns the session's whole tree in append order — every
// entry ever written, abandoned branches included. The slice is fresh
// and the entries' bookkeeping fields are copies: mutating what comes
// back never reaches the session or the storage.
func (s *Session) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.order))
	for i, e := range s.order {
		out[i] = cloneEntry(e)
	}
	return out
}

// Path returns the entries from the root to entryID, inclusive, in
// conversation order — the chain of parent links. entryID "" is the
// root and returns no entries; an id the session does not hold is an
// error. Path names tree structure: a leaf entry's own parent is
// where it was appended, not the entry it navigated to.
func (s *Session) Path(entryID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pathLocked(entryID)
}

func (s *Session) pathLocked(entryID string) ([]Entry, error) {
	if entryID == "" {
		return nil, nil
	}
	i, ok := s.byID[entryID]
	if !ok {
		return nil, fmt.Errorf("thread: session %s holds no entry %q", s.header.ID, entryID)
	}
	// Walk parent links to the root, then reverse, so the path reads
	// in conversation order. Two guards keep a hand-edited stored file
	// from hanging the walk — neither can happen through this package,
	// whose appends are atomic over a validated tree: the step bound
	// stops a parent-link cycle, and a parent the file does not hold
	// ends the walk where the record ends.
	path := make([]Entry, 0, 8)
	for steps := 0; i >= 0 && steps <= len(s.order); steps++ {
		e := s.order[i]
		path = append(path, cloneEntry(e))
		parent := parentOf(e)
		if parent == "" {
			break
		}
		pi, ok := s.byID[parent]
		if !ok {
			break
		}
		i = pi
	}
	slices.Reverse(path)
	return path, nil
}

// Context returns the messages the model sees at the session's leaf:
// the message and custom_message entries on the leaf's path, in
// conversation order, with weft.Repair applied last — every call the
// transcript shows has a result (ADR 0011 §2, ADR 0001). Entries of
// the bookkeeping kinds never reach it; a custom entry's whole point
// is to survive outside it.
//
// This walk is where compaction lands: from step 1.8, the latest
// compaction entry on the path contributes its summary ahead of the
// entries from its first kept id onward, and a branch_summary entry
// contributes the abandoned branch's summary in that branch's place
// (ADR 0020 §1, §6). Nothing before step 1.8 writes either kind, so
// the walk below reads the whole path raw.
func (s *Session) Context() []weft.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return nil // the leaf is always an entry the session holds
	}
	var msgs []weft.Message
	for _, e := range path {
		switch e := e.(type) {
		case MessageEntry:
			msgs = append(msgs, e.Message)
		case CustomMessageEntry:
			msgs = append(msgs, e.Message)
		}
	}
	return weft.Repair(msgs)
}

// Label names an entry — bookmarks, checkpoints, the anchors a UI
// lists. The label is an appended entry, never a rewrite, and never
// enters the model's context. entryID must name an entry the session
// holds; the name must not be empty.
func (s *Session) Label(ctx context.Context, entryID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return fmt.Errorf("thread: Label with empty name")
	}
	if _, ok := s.byID[entryID]; !ok {
		return fmt.Errorf("thread: session %s holds no entry %q", s.header.ID, entryID)
	}
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return LabelEntry{ID: id, ParentID: parent, Created: created, Entry: entryID, Name: name}
	})
}

// SetInfo edits the session's title and metadata as an appended info
// entry, never a rewrite of the header (ADR 0011 §2): the current
// title is the last info entry's Title, the current metadata every
// info entry's Meta merged in append order — Title and Meta resolve
// them (an empty title keeps the current one; a nil meta map changes
// nothing; a later entry overwrites the keys it names and leaves the
// rest).
func (s *Session) SetInfo(ctx context.Context, title string, meta map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if title == "" {
		title = s.titleLocked()
	}
	m := cloneMeta(meta)
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return InfoEntry{ID: id, ParentID: parent, Created: created, Title: title, Meta: m}
	})
}

// Title returns the session's current title — the last info entry's
// (SetInfo's rule), "" before any SetInfo named one.
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.titleLocked()
}

func (s *Session) titleLocked() string {
	title := ""
	for _, e := range s.order {
		if ie, ok := e.(InfoEntry); ok && ie.Title != "" {
			title = ie.Title
		}
	}
	return title
}

// Meta returns the session's current metadata as a fresh map the
// caller owns: the header's Meta as the base layer, overlaid by every
// info entry's Meta in append order. nil before anything set any.
func (s *Session) Meta() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metaLocked()
}

func (s *Session) metaLocked() map[string]string {
	var meta map[string]string
	merge := func(kv map[string]string) {
		for k, v := range kv {
			if meta == nil {
				meta = map[string]string{}
			}
			meta[k] = v
		}
	}
	merge(s.header.Meta)
	for _, e := range s.order {
		if ie, ok := e.(InfoEntry); ok {
			merge(ie.Meta)
		}
	}
	return meta
}

// Custom appends application state: a caller-chosen Kind and opaque
// JSON Data, surviving every compaction and never entering the model's
// context (ADR 0011 §2, ADR 0020 §4). data is stored verbatim — nil
// stores no data key at all; bytes that are not JSON are rejected, the
// same failure the storage's Append would raise, caught before
// anything is written. An empty kind is rejected: state nobody can
// name is not state.
func (s *Session) Custom(ctx context.Context, kind string, data json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" {
		return fmt.Errorf("thread: Custom with empty kind")
	}
	if len(data) > 0 && !json.Valid(data) {
		return fmt.Errorf("thread: Custom data for kind %q is not JSON", kind)
	}
	d := slices.Clone(data)
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return CustomEntry{ID: id, ParentID: parent, Created: created, Kind: kind, Data: d}
	})
}

// CustomMessage appends an application message: a caller-chosen Kind
// and a weft.Message that is always in the model's context — how an
// application puts a note the model must see without attributing it to
// the user (ADR 0011 §2). An empty kind is rejected.
func (s *Session) CustomMessage(ctx context.Context, kind string, msg weft.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" {
		return fmt.Errorf("thread: CustomMessage with empty kind")
	}
	msg.Content = slices.Clone(msg.Content)
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return CustomMessageEntry{ID: id, ParentID: parent, Created: created, Kind: kind, Message: msg}
	})
}

// Usage is a session's cost ledger (ADR 0020 §4): what its turns cost
// and what its summaries cost, never mixed.
type Usage struct {
	// Turns sums every turn entry's usage — every run the session ever
	// made, abandoned branches included: the tokens were spent.
	Turns weft.Usage
	// Summaries sums the summarizer usage of every compaction entry —
	// the cost of keeping the context small, in its own bucket.
	Summaries weft.Usage
}

// Usage returns the session's cost ledger over every entry in the
// file, not only the leaf's path: turns from the per-turn ledger,
// summarizer costs in their own bucket (ADR 0011 §2, ADR 0020 §4).
func (s *Session) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var u Usage
	for _, e := range s.order {
		switch e := e.(type) {
		case TurnEntry:
			u.Turns = u.Turns.Add(e.Usage)
		case CompactionEntry:
			u.Summaries = u.Summaries.Add(e.SummarizerUsage)
		}
	}
	return u
}

// appendLocked is every Session write: it mints the entry's id, parent
// (the leaf) and time, hands the entry to the storage, and adopts it
// into the in-memory tree only after the storage accepted it — a
// failed Append leaves the session exactly as it was, so the tree in
// memory and the session in storage cannot diverge. Callers hold s.mu.
func (s *Session) appendLocked(ctx context.Context, build func(id, parent string, created time.Time) Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := NewEntryID()
	if s.cfg.ids != nil {
		id = s.cfg.ids()
	}
	if !ValidID(id) {
		return fmt.Errorf("thread: invalid entry id %q", id)
	}
	e := build(id, s.leaf, time.Now().UTC())
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	return nil
}

// adoptLocked folds an accepted entry into the in-memory tree: the
// append order, the id index, and the leaf — a leaf entry moves the
// leaf to its target, any other entry becomes it.
func (s *Session) adoptLocked(e Entry) {
	s.order = append(s.order, e)
	id := idOf(e)
	if id != "" {
		s.byID[id] = len(s.order) - 1
	}
	if le, ok := e.(LeafEntry); ok {
		s.leaf = le.Entry
	} else if id != "" {
		s.leaf = id
	}
	if _, ok := e.(TurnEntry); ok {
		s.turns++
	}
}

// cloneEntry copies an entry's mutable fields — the maps, slices and
// raw bytes a caller could write into through a retained value — so
// Entries and Path hand out snapshots. Message parts are values the
// core hands out the same way its own results do, and are shared, not
// deep-copied.
func cloneEntry(e Entry) Entry {
	switch e := e.(type) {
	case MessageEntry:
		return e
	case TurnEntry:
		e.Pending = slices.Clone(e.Pending)
		return e
	case CompactionEntry:
		e.FilesRead = slices.Clone(e.FilesRead)
		e.FilesModified = slices.Clone(e.FilesModified)
		e.Pinned = slices.Clone(e.Pinned)
		return e
	case BranchSummaryEntry:
		return e
	case LeafEntry:
		return e
	case LabelEntry:
		return e
	case InfoEntry:
		e.Meta = cloneMeta(e.Meta)
		return e
	case CustomEntry:
		e.Data = slices.Clone(e.Data)
		return e
	case CustomMessageEntry:
		return e
	}
	return e
}
