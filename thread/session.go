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
	// policy is the busy policy Send follows (ADR 0011 §4): Queue (the
	// zero value, the default) or Reject.
	policy Policy
	// compaction is the compaction configuration (ADR 0020): the
	// defaults until step 1.9's public layers override them.
	compaction compactConfig
	// The approvals configuration (ADR 0021): the chain's live step
	// and its timeout, whether a completed boundary resumes on its own
	// (default on), the OnRequest notification, the lifetime given
	// every parked request (0 = never expires), the quorum a call's
	// approvals must reach (0 and 1 both mean one decision resolves),
	// the shared grant scope, the signed-decision keyring, and whether
	// the unsigned Decide door is closed.
	approver        Approver
	approverTimeout time.Duration
	autoResume      bool
	onRequest       func(Request)
	requestExpiry   time.Duration
	quorum          int
	grantStore      GrantStore
	keyring         *Keyring
	requireSigned   bool
	// reRunOnOverflow arms the overflow re-run (ADR 0020 §5): a turn
	// failing with weft.ErrContextOverflow compacts — reason overflow —
	// and runs once more over the shrunken path. Default on.
	reRunOnOverflow bool
	// lineage is the pool origin recorded in a created session's
	// header (ADR 0022 §3): the parent session and the delegating
	// call. Create-time only — Open reads it from the file.
	lineage Lineage
}

func resolveSession(opts ...SessionOption) sessionConfig {
	cfg := sessionConfig{compaction: defaultCompactConfig(), autoResume: true, reRunOnOverflow: true}
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
type lineageOption Lineage

func (o lineageOption) applySession(c *sessionConfig) { c.lineage = Lineage(o) }

// WithLineage sets a created session's pool lineage (ADR 0022 §3):
// the parent session and the delegating call it grew from. A
// Create-time option — the header is immutable once written; Open
// reads the lineage from the file and ignores it.
func WithLineage(parentSession, call string) SessionOption {
	return lineageOption{Session: parentSession, Call: call}
}

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
	// turns counts the turn entries ever appended, branches included,
	// and turnSeq is the run-id counter Send mints <session>-t<n> ids
	// from (step 1.7): it recovers from turns on a reopen and moves at
	// mint time, so a crashed turn's id is never reused.
	turns   int
	turnSeq int
	// running is the one-runner flag behind the busy policy: a Send
	// while true queues or rejects; the runner clears it when the
	// queue drains. queue holds the accepted sends waiting for their
	// turn, in acceptance order.
	//
	// inFlight names the turn the runner is currently inside — set
	// with running whenever an item starts, cleared at the item
	// boundary under mu, before the runner's epilogue decisions. The
	// pair is what "a turn is running" means to Branch: running alone
	// also covers the epilogue window after a turn's finish (its Wait
	// has returned, its entries have landed), where a navigation is
	// already safe and must not read ErrBusy.
	running  bool
	inFlight *Turn
	queue    []pendingSend
	// The approval boundary's runner hand-off (ADR 0021 §1–§2): await
	// holds the parked boundary's captured turn settings — the extra
	// run options of the Send that parked and the persistence window
	// the resume inherits (lost across a restart: run options are not
	// entries) — and resumeWork is a resume run Decide or Resume
	// minted while a runner was alive, waiting for the runner to pick
	// it up. The runner itself resumes completed boundaries whenever
	// AutoResume is on.
	await      awaitState
	resumeWork *pendingResume

	// The steering state (ADR 0019, plan §6): steerQueue holds the
	// steers accepted but not yet drained by the running turn, in
	// acceptance order; handed holds the ones the run's drain took,
	// whose delivered receipts join that turn's end batch. Both are
	// guarded by mu; the drain itself (steerSource) never blocks.
	steerQueue []queuedSteer
	handed     []queuedSteer

	// The compaction trigger's state (ADR 0020 §2): lastInput is the
	// provider-reported input of the last model step the session ran,
	// lastMeasureLeaf the entry that step's request covered up to (the
	// turn's prompt — the messages after it are the estimated delta),
	// and warnedNoWindow keeps the no-window warning to one line.
	lastInput       int64
	lastMeasureLeaf string
	warnedNoWindow  bool
}

// awaitState is the parked boundary's captured turn settings, plus
// the Turn that parked — the anchor a resume links back to through
// Turn.Next.
type awaitState struct {
	opts  []weft.RunOption
	ctx   context.Context
	runID string
	turn  *Turn
	// resumed is the resume turn already armed for this boundary —
	// the idempotency key for arming (one boundary, one resume),
	// cleared when the resume completes so a failed one can retry.
	resumed *Turn
}

// pendingResume is a resume run minted by Decide or Resume while a
// runner was alive: the context it was armed with and its Turn.
type pendingResume struct {
	ctx  context.Context
	turn *Turn
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
	if cfg.lineage.Session != "" {
		l := cfg.lineage
		h.Lineage = &l
	}
	if err := st.Create(ctx, h); err != nil {
		return nil, err
	}
	cfg.compaction.resolve(agent.Model()) // per-model overrides need the agent
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
		header: h,
		order:  entries,
		byID:   make(map[string]int, len(entries)),
	}
	s.cfg = resolveSession(opts...)
	s.cfg.compaction.resolve(agent.Model()) // per-model overrides need the agent
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
	s.turnSeq = s.turns
	// The trigger's measurement recovers from the last turn entry that
	// recorded one: its input is the last provider-reported number, and
	// the messages after its entry are the estimated delta.
	for _, e := range entries {
		if te, ok := e.(TurnEntry); ok && te.LastInput > 0 {
			s.lastInput = te.LastInput
			s.lastMeasureLeaf = te.ID
		}
	}
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
	// A queued steer whose fate never landed — the writer crashed or
	// was killed between accepting it and the turn's end batch — is
	// durable input (ADR 0011 §4): on reopen it defers to a follow-up
	// that runs when the session next can, so an accepted message is
	// never lost to the crash window.
	s.resurrectSteers(ctx)
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

// Storage returns the storage the session writes through — the handle
// thread/pool holds to create child sessions in the same place (ADR
// 0022 §3). It is the app's own storage returned, read-only by
// convention: every write goes through a Session, never behind one.
func (s *Session) Storage() Storage { return s.st }

// Lineage returns the session's pool lineage (ADR 0022 §3): the parent
// session and delegating call it was started from, as recorded in its
// header at Create. The zero value means the session is nobody's
// child.
func (s *Session) Lineage() Lineage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.header.Lineage == nil {
		return Lineage{}
	}
	return *s.header.Lineage
}

// sessionCtxKey is the context key carrying the *Session whose run is
// running. Only thread puts a session on a context; SessionFromContext
// is the read side, for satellite packages (thread/pool, ADR 0022)
// that need the parent of the delegation they are wrapping.
type sessionCtxKey struct{}

// SessionFromContext returns the session whose run ctx carries it, or
// nil outside a session's run — a wrapped tool invoked through a bare
// Generate has no parent session, and the pool falls back to the
// ordinary subagent path (ADR 0022 §2).
func SessionFromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionCtxKey{}).(*Session)
	return s
}

// withSession decorates the run context with its session.
func withSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

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
// is to survive outside it. A call left pending by its turn is shown
// repaired here — the caller's view; the run Send starts repairs
// pending calls itself, so the decision options can resolve them.
//
// This walk is where compaction lands: from step 1.8, the latest
// compaction entry on the path contributes its summary ahead of the
// entries from its first kept id onward, and a branch_summary entry
// contributes the abandoned branch's summary in that branch's place
// (ADR 0020 §1, §6). Nothing before step 1.8 writes either kind, so
// the walk below reads the whole path raw.
func (s *Session) Context() []weft.Message {
	return weft.Repair(s.rawContext())
}

// rawContext is Context before weft.Repair: the leaf's path messages
// exactly as stored. The run Send starts carries these — the loop
// repairs its input itself, and with a decision option in force it
// leaves that decision's pending calls unresolved so it can resolve
// them (loop.go: repair-with-skip). Repairing here would close the
// approval boundary: an Approve arriving at a transcript whose call
// already reads "interrupted" has nothing left to resolve.
func (s *Session) rawContext() []weft.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rawContextLocked()
}

// rawContextLocked is the walk (callers hold s.mu): the latest
// compaction entry on the path leads with its summary behind the fixed
// marker, and the context then reads from its first kept entry onward
// (ADR 0020 §1); a branch_summary entry contributes its summary in
// the abandoned branch's place wherever it sits (§6); and reasoning
// parts carrying a signature are stripped from entries recorded before
// the compaction — their prefix changed, and a resent signature breaks
// (pi #9391, Anthropic prefix_binding_mismatch). Entries recorded
// after the compaction keep theirs: they were made over a prefix that
// already held the summary. A FirstKept the path does not reach (a
// hand-made file) makes the compaction unusable, and the walk falls
// back to the whole path rather than a summary of nothing.
//
// A trim record (no summary of its own) never governs the boundary:
// the latest summary compaction at or below it keeps supplying the
// marker and the first-kept id, and the trim only layers its stubs
// over that kept range. A trim that reset the boundary to the root
// would resurface, raw, everything the summary had replaced — a
// context larger than the one the trimmer measured.
// pinnedKept is one pinned entry the walk re-includes: its path
// index (for the trim's stub set) and its stripped message.
type pinnedKept struct {
	idx int
	msg weft.Message
}

func (s *Session) rawContextLocked() []weft.Message {
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return nil // the leaf is always an entry the session holds
	}
	var msgs []weft.Message
	var pinnedMsgs []pinnedKept
	start, compactionAt, trimAt := 0, -1, -1
	for i := len(path) - 1; i >= 0; i-- {
		c, ok := path[i].(CompactionEntry)
		if !ok {
			continue
		}
		if c.Summary == "" && c.Reason == ReasonTrim {
			if trimAt < 0 {
				trimAt = i // the latest trim: where its stubs reach up to
			}
			continue // a trim does not govern; the summary below it does
		}
		msgs = append(msgs, summaryMessage(c.Summary))
		compactionAt = i
		for j := 0; j <= i; j++ {
			if idOf(path[j]) == c.FirstKept {
				start = j
				break
			}
		}
		// The pinned ids the compaction kept through: message-kind
		// entries below the boundary re-enter the context after the
		// summary, in path order — a pin survives every compaction
		// without holding the cut back (ADR 0020 §4). Collected here,
		// appended once the trim's stub set is known.
		if len(c.Pinned) > 0 {
			pinnedSet := make(map[string]bool, len(c.Pinned))
			for _, id := range c.Pinned {
				pinnedSet[id] = true
			}
			for j := 0; j < start; j++ {
				if m, ok := contextMessage(path[j]); ok && pinnedSet[idOf(path[j])] {
					pinnedMsgs = append(pinnedMsgs, pinnedKept{idx: j, msg: stripSignedReasoning(m)})
				}
			}
		}
		break
	}
	// A trim record re-derives the built-in trimmer's view on read:
	// every tool result in the kept range recorded before the trim
	// reads as the golden stub, except the newest keepLast of them
	// (the same rule the trimmer applied when it decided the trim was
	// enough). A custom trimmer's record is not re-derived — its view
	// was its own; the raw messages read as stored.
	var stubParts map[struct{ msg, part int }]bool
	if trimAt >= 0 && trimAt > start {
		if t, ok := s.cfg.compaction.trimmer.(clearResultsTrimmer); ok {
			// The mirror of the trimmer's rule, per result part (a
			// step's results batch on one message): the newest keepLast
			// parts in the pre-trim range survive, every older one's
			// message is stubbed.
			type at = struct{ msg, part int }
			var parts []at // oldest first
			for i := start; i < trimAt; i++ {
				if m, ok := path[i].(MessageEntry); ok {
					for j := range m.Message.Content {
						if _, isResult := m.Message.Content[j].(weft.ToolResultPart); isResult {
							parts = append(parts, at{i, j})
						}
					}
				}
			}
			stubParts = map[at]bool{}
			for _, p := range parts {
				stubParts[p] = true
			}
			for n := 0; n < min(t.keepLast, len(parts)); n++ {
				delete(stubParts, parts[len(parts)-1-n]) // the newest survive
			}
		}
	}
	for _, pk := range pinnedMsgs {
		msgs = append(msgs, stubMessageParts(pk.msg, pk.idx, stubParts))
	}
	// Below the governing compaction entry the recorded entries are
	// pre-compaction (signed reasoning stripped, trim stubs applied).
	// A trim's stubs rewrite prefixes below the trim itself, so when
	// one exists the strip boundary is the trim — the lower of the two
	// positions, since the trim always sits above the governor.
	stripBelow := compactionAt
	if trimAt >= 0 {
		stripBelow = trimAt
	}
	for i := start; i < len(path); i++ {
		switch e := path[i].(type) {
		case MessageEntry:
			m := e.Message
			if stripBelow >= 0 && i < stripBelow {
				m = stripSignedReasoning(m)
				m = stubMessageParts(m, i, stubParts)
			}
			msgs = append(msgs, m)
		case CustomMessageEntry:
			m := e.Message
			if stripBelow >= 0 && i < stripBelow {
				m = stripSignedReasoning(m)
			}
			msgs = append(msgs, m)
		case BranchSummaryEntry:
			msgs = append(msgs, summaryMessage(e.Summary))
		}
	}
	return msgs
}

// stubMessageParts replaces this message's trimmed result parts — the
// ones the walk marked — with the golden stub naming the call.
func stubMessageParts(m weft.Message, pathIdx int, stubParts map[struct{ msg, part int }]bool) weft.Message {
	if len(stubParts) == 0 {
		return m
	}
	var any bool
	for j := range m.Content {
		if stubParts[struct{ msg, part int }{pathIdx, j}] {
			any = true
			break
		}
	}
	if !any {
		return m
	}
	content := make([]weft.Part, len(m.Content))
	copy(content, m.Content)
	for j := range content {
		if stubParts[struct{ msg, part int }{pathIdx, j}] {
			if r, ok := content[j].(weft.ToolResultPart); ok {
				content[j] = weft.ToolResultPart{
					CallID:  r.CallID,
					Name:    r.Name,
					Content: clearedResultStub(r.CallID, r.Name),
				}
			}
		}
	}
	m.Content = content
	return m
}

// stripSignedReasoning drops the message's signed reasoning parts —
// the kept-side half of the compaction's reasoning rule, applied to
// the context the model sees and never to what the file holds.
func stripSignedReasoning(m weft.Message) weft.Message {
	has := false
	for _, p := range m.Content {
		if r, ok := p.(weft.ReasoningPart); ok && r.Signature != "" {
			has = true
			break
		}
	}
	if !has {
		return m
	}
	out := m
	out.Content = nil
	for _, p := range m.Content {
		if r, ok := p.(weft.ReasoningPart); ok && r.Signature != "" {
			continue
		}
		out.Content = append(out.Content, p)
	}
	return out
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

// AppendPoolReceipt appends one pool receipt entry (ADR 0022 §4) and
// returns it as stored, its minted ID the receipt handle a later entry
// links back to with Receipt. The pool calls this for every state its
// delegations pass through — acceptance, the start, the settlement —
// under the rule the entry kind's contract states: pool receipts are
// ledger, never model context, and a child's answer reaches the model
// only through its delegating call's result or the application. A
// hand caller owns the same rules.
func (s *Session) AppendPoolReceipt(ctx context.Context, e PoolReceiptEntry) (PoolReceiptEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out PoolReceiptEntry
	err := s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		e.ID, e.ParentID, e.Created = id, parent, created
		out = e
		return e
	})
	if err != nil {
		return PoolReceiptEntry{}, err
	}
	return out, nil
}

// isPoolSettled reports whether a pool receipt status is a settlement
// — the states whose entry carries the child's final usage (ADR 0022
// §4): exactly one of them follows every acceptance.
func isPoolSettled(status string) bool {
	switch status {
	case PoolDone, PoolFailed, PoolCanceled, PoolCapped:
		return true
	}
	return false
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
	// Delegated sums the usage every settled pool receipt carries —
	// the cost of work handed to thread/pool children (ADR 0022 D3),
	// in its own bucket: a delegated child's tokens are not this
	// session's turns, and the ledger never mixes kinds.
	Delegated weft.Usage
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
		case PoolReceiptEntry:
			if e.Receipt != "" && isPoolSettled(e.Status) {
				u.Delegated = u.Delegated.Add(e.Usage)
			}
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
	if _, dup := s.byID[id]; dup {
		// An id the tree already holds would make it ambiguous —
		// which entry does the id name? — and every walk reads one of
		// the two. Random ids cannot collide; an IDs function can.
		return fmt.Errorf("thread: entry id %q already held by session %s", id, s.header.ID)
	}
	e := build(id, s.leaf, time.Now().UTC())
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	// Every session write is durable when it returns: under the
	// FsyncOnFlush cadence the flush is what makes it so, and losing a
	// revocation — or a decision, Decide's own rule — to a crash must
	// not be possible after a successful call.
	return s.flushLocked(ctx)
}

// flushLocked completes the storage's buffered durability when it
// offers the Flusher capability (open.go): under FsyncOnFlush, the
// append is in the page cache until here. The paths that cannot fail
// their turn for a late flush (recordTurnEnd) log it instead of
// returning it; everything else returns it — the durability a call
// promised is part of the call. Callers hold s.mu.
func (s *Session) flushLocked(ctx context.Context) error {
	if f, ok := s.st.(Flusher); ok {
		return f.Flush(ctx, s.header.ID)
	}
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
	case ApprovalRequestEntry:
		e.Args = slices.Clone(e.Args)
		return e
	case ApprovalDecisionEntry:
		return e
	case ApprovalAuditEntry:
		return e
	case GrantEntry:
		e.Args = append([]Arg(nil), e.Args...)
		for i := range e.Args {
			e.Args[i].Equals = slices.Clone(e.Args[i].Equals)
		}
		return e
	case GrantRevokedEntry:
		return e
	}
	return e
}
