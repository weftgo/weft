package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft"
)

// SessionOption configures a session at Create, Open or Fork — one
// value per concern, folded over the defaults, the constructor
// convention of the core (ADR 0011 §6). Every option family of the
// package joins this one interface: ids and the clock (IDs, Clock),
// the busy policy (BusyPolicy), compaction (ContextWindow and its
// layers), approvals (WithApprover, Quorum, WithKeyring, …).
//
// Three options only mean something while a session's header is being
// written — WithMeta, PublicID and WithLineage. Create and Fork honour
// them; Open refuses them with ErrCreateOnly, because the header it
// loads is immutable and an option it silently dropped would read as
// applied.
type SessionOption interface {
	applySession(*sessionConfig)
}

// sessionConfig is a session's resolved configuration; callers never
// construct it — Create, Open and Fork fold SessionOptions into it.
type sessionConfig struct {
	// ids mints session and entry ids; nil means the package's own
	// time-sortable ids (NewSessionID, NewEntryID).
	ids func() string
	// clock is the session's time source (Clock); nil means
	// time.Now. Read it through now, which also normalises to UTC.
	clock func() time.Time
	// policy is the busy policy Send follows (ADR 0011 §4): Queue (the
	// zero value, the default), Reject, Steer, Interrupt or Rollback.
	policy Policy
	// compaction is the compaction configuration (ADR 0020): the
	// defaults, overridden by the compaction options.
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
	// call. A header option — Open reads the lineage from the file.
	lineage Lineage
	// meta is caller metadata merged into a created session's header
	// (WithMeta, PublicID). A header option — later-life edits are
	// info entries.
	meta map[string]string
	// createOnly names the header options that were applied, in the
	// order given, so Open can refuse them by name (ErrCreateOnly).
	createOnly []string
}

// now is the session's time source: the Clock option's function, or
// time.Now, always in UTC — the zone every stored timestamp carries.
func (c *sessionConfig) now() time.Time {
	if c.clock != nil {
		return c.clock().UTC()
	}
	return time.Now().UTC()
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
// the session id Create or Fork generates and the entry id of each
// entry the Session appends — from id. Tests and examples pin
// deterministic ids with it. A nil id is ignored, leaving the default
// time-sortable ids; a value that fails ValidID, or repeats an id the
// session already holds, fails the write that would carry it.
//
// The session calls id with its lock held — ids are minted inside the
// write they name, so their order is the entries' order. The function
// must return quickly and must not call back into the Session: any
// Session method called from it deadlocks.
func IDs(id func() string) SessionOption {
	if id == nil {
		return nil // an ignored option, the constructor convention
	}
	return idsOption(id)
}

type clockOption func() time.Time

func (o clockOption) applySession(c *sessionConfig) { c.clock = o }

// Clock returns the SessionOption that makes now the session's time
// source: the header's Created at Create and Fork, and the Created of
// every entry the Session appends, are read from it (converted to
// UTC). The default is time.Now. Tests and examples pin time with it,
// the way IDs pins ids; a nil now is ignored.
//
// Like the IDs function, now is called with the session's lock held:
// it must return quickly and must not call back into the Session.
// Expiry arithmetic that compares against the wall clock — request
// and grant expiries, signing challenges — is not routed through it.
func Clock(now func() time.Time) SessionOption {
	if now == nil {
		return nil
	}
	return clockOption(now)
}

type lineageOption Lineage

func (o lineageOption) applySession(c *sessionConfig) {
	c.lineage = Lineage(o)
	c.createOnly = append(c.createOnly, "WithLineage")
}

// WithLineage sets a new session's pool lineage (ADR 0022 §3): the
// parent session and the delegating call it grew from. A header
// option — Create and Fork write it into the header, which is
// immutable once stored; Open fails with ErrCreateOnly when given it
// (the lineage is read from the file, see Session.Lineage).
func WithLineage(parentSession, call string) SessionOption {
	return lineageOption{Session: parentSession, Call: call}
}

type metaOption map[string]string

func (o metaOption) applySession(c *sessionConfig) {
	if c.meta == nil {
		c.meta = map[string]string{}
	}
	maps.Copy(c.meta, o)
	c.createOnly = append(c.createOnly, "WithMeta")
}

// WithMeta adds keys to a new session's header metadata — the caller
// metadata List's Query.Meta filter matches (ADR 0011 §1). A header
// option, merged over earlier ones: Create and Fork write it; Open
// fails with ErrCreateOnly when given it. SetInfo is the later-life
// edit, for every key outside the reserved "weft." prefix — those are
// set here, once, or never.
func WithMeta(meta map[string]string) SessionOption { return metaOption(meta) }

// reservedMetaPrefix marks the metadata keys that are the session's
// identity: written to the header at Create or Fork, never edited by
// an info entry.
const reservedMetaPrefix = "weft."

// publicIDKey is the header metadata key PublicID sets and every run
// carries as its weft.public_id.
const publicIDKey = "weft.public_id"

type publicIDOption string

func (o publicIDOption) applySession(c *sessionConfig) {
	if c.meta == nil {
		c.meta = map[string]string{}
	}
	c.meta[publicIDKey] = string(o)
	c.createOnly = append(c.createOnly, "PublicID")
}

// PublicID sets the session's public id: an opaque, browser-safe
// handle stamped on every run of the session as weft.public_id, beside
// weft.session.id and weft.turn (ADR 0024 S5). It is stored as the
// header metadata key "weft.public_id" — exactly what
// WithMeta(map[string]string{"weft.public_id": id}) stores — so List's
// Query.Meta filter finds the session by it: backends match header
// metadata only.
//
// The public id is fixed for the session's life. It is a header
// option — Create and Fork write it, Open fails with ErrCreateOnly
// when given it — and no later edit can rotate it: SetInfo rejects
// every key under the reserved "weft." prefix with ErrReservedKey,
// and Session.Meta never lets an info entry override a "weft." key
// (a session file written before that rule keeps the first value it
// recorded). A fork does not inherit it: a fork is another session,
// and takes its own PublicID or none.
func PublicID(id string) SessionOption { return publicIDOption(id) }

// A Session is a loaded conversation: the append-only entry tree
// (ADR 0011 §2) held in memory, every write going through the
// storage's Append, the leaf tracked as the entry the next one
// attaches to. Create, Open and Session.Fork return one.
//
// # Concurrency
//
// A Session is safe for concurrent use by any number of goroutines:
// one mutex guards the tree, and every method takes it.
//
// One writer. The Session is its session's only writer: entries
// appended to the storage behind its back are invisible to it until
// the next Open, and the tree it holds would then disagree with the
// file. Keep one Session value per session id per process. Backends
// refuse a second writer from another process or another Storage
// value with ErrLocked; a second Open of the same id on the same
// Storage value is not refused today and its behaviour is undefined —
// both values would append, each blind to the other.
//
// One run at a time. A Send while a turn runs — or while an approval
// boundary is open — follows the busy policy captured at that Send:
// Queue holds it and runs it next, in acceptance order; Reject fails
// it with ErrBusy; Steer delivers it into the running turn at its
// next drain point, or defers it to a follow-up turn; Interrupt
// cancels the running turn and runs the message next; Rollback also
// branches back to before the interrupted turn. Concurrent Sends are
// serialised by the lock: the order they acquire it in is the
// acceptance order. Branch fails with ErrBusy while a turn is in
// flight; Fork, the reads, and the bookkeeping writes (Label, SetInfo,
// Custom, CustomMessage, Grant, Decide) do not wait for a turn, and a
// write made during one lands on the line the turn is extending.
//
// Callbacks. The session calls the caller's hooks — an Approver,
// OnRequest, the compaction hooks and Summarizer, an Estimator, the
// agent's own taps and tools — without holding its lock, so they may
// call back into the Session. The two exceptions are the IDs and
// Clock functions, which run under the lock and must not.
//
// The lock is held across the storage's Append and Flush: a write —
// and under FsyncEveryAppend that is an fsync — stalls every other
// method, reads included, until it returns. That is what makes a
// returned write durable and the tree in memory equal to the file.
//
// Close. Close stops new work at once — Send and Continue fail with
// ErrClosed — then waits for the running turn and the queue to drain,
// seals the session so that every later write fails with ErrClosed,
// and releases the storage's hold on the session. Reads keep
// answering from the tree the session held. See Close for what a
// canceled wait leaves behind.
//
// Delete. Delete removes the stored session whether or not a Session
// value is open on it. An open Session keeps its in-memory tree; its
// next write fails with ErrNotFound, and a turn running at that
// moment loses its remaining entries (the failure is logged through
// the agent's logger). Close a session before deleting it.
type Session struct {
	mu     sync.Mutex
	st     Storage // the write path; sealed by Close
	agent  *weft.Agent
	cfg    sessionConfig
	header Header         // immutable after construction: read without mu
	order  []Entry        // the whole tree, append order
	byID   map[string]int // entry id → index into order
	// leaf is the entry the next appended entry attaches to: the id of
	// the last entry, or the target of a trailing leaf entry (branch
	// navigation), or "" while the session holds no entries — the next
	// entry is then the root.
	leaf string
	// orphans are the entries accepted under Salvage whose parent the
	// file does not hold (a skipped line took it): each is the root of
	// what survives of its line, and the one place a path may end
	// without reaching a root. report is what Open tells the caller
	// about the load (LoadReport); nil when it was clean.
	orphans map[string]bool
	report  *OpenReport
	// turns counts the turn entries ever appended, branches included,
	// and turnSeq is the run-id counter Send mints <session>-t<n> ids
	// from: it recovers from turns on a reopen and moves at mint time,
	// so a crashed turn's id is never reused.
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

	// The steering state (ADR 0019): steerQueue holds the steers
	// accepted but not yet drained by a running turn, in acceptance
	// order — the ones Open restored from the file included; handed
	// holds the ones the run's drain took, whose delivered receipts
	// join that turn's end batch. Both are guarded by mu; the drain
	// itself (steerSource) never blocks.
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

	// The close state (Close): closing moves open → draining →
	// sealed, with aborted the side state a Close whose context ended
	// leaves behind; closeDone is closed once the seal's release has
	// returned, and closeErr is what it returned.
	closing   closeState
	closeDone chan struct{}
	closeErr  error
}

// closeState is where a Session is in its life (Close).
type closeState int

const (
	// stateOpen: the session accepts work.
	stateOpen closeState = iota
	// stateDraining: Close has been called. Send and Continue are
	// refused; the runner finishes what it holds.
	stateDraining
	// stateAborted: a Close gave up waiting (its context ended). The
	// running turn was canceled and the queues dropped; beyond what
	// stateDraining refuses, no session-level append lands any more —
	// which is what keeps a resume from starting — while the canceled
	// turn's own end batch still does.
	stateAborted
	// stateSealed: the runner is gone, the write path is sealed, the
	// storage's hold is released (or being released: closeDone).
	stateSealed
)

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

// OpenReport names what Open had to drop, skip or accept to return a
// session — a repair is never silent (ADR 0011 §5). It is the
// storage's LoadReport (the torn final line, the lines skipped under
// Salvage) plus what the tree validation found among the entries that
// survived. Session.LoadReport returns it; a clean load has none.
type OpenReport struct {
	LoadReport
	// Orphaned lists, in append order, the ids of the entries whose
	// link names an entry the file does not hold — possible only when
	// a line was skipped under Salvage, which is the only time Open
	// accepts it (otherwise the same shape fails with ErrCorrupt).
	//
	// An orphaned entry is kept, never dropped: it stays in Entries,
	// its turns stay in Usage, and it is the root of what survives of
	// its line — Path and Context walk back to it and end there. So a
	// session whose leaf descends from an orphaned entry has a context
	// that starts at that entry: everything recorded before the
	// skipped line is no longer on the path. An orphaned leaf entry —
	// a navigation whose target was skipped — is ignored: the leaf
	// stays where it was before the navigation.
	Orphaned []string
}

// Create starts a new session in st: a fresh header under a new
// time-sortable id (or the IDs option's), stamped with the session's
// clock, carrying the WithMeta, PublicID and WithLineage options; the
// tree is empty. agent is the session's own — Send runs it and
// compaction summarizes with its model — and must not be nil, like
// the core's New. The header is all that is written: nothing else
// lands in the storage until the first append. An id the storage
// already holds fails with ErrExists.
func Create(ctx context.Context, st Storage, agent *weft.Agent, opts ...SessionOption) (*Session, error) {
	if st == nil {
		return nil, fmt.Errorf("thread: Create with nil storage")
	}
	if agent == nil {
		return nil, fmt.Errorf("thread: Create with nil agent")
	}
	cfg := resolveSession(opts...)
	h, err := newHeader(&cfg)
	if err != nil {
		return nil, err
	}
	if err := st.Create(ctx, h); err != nil {
		return nil, err
	}
	return newSession(st, agent, cfg, h, nil, nil)
}

// newHeader builds the header Create and Fork write: the minted id,
// the clock's time, and the header options (lineage, metadata).
func newHeader(cfg *sessionConfig) (Header, error) {
	id := NewSessionID()
	if cfg.ids != nil {
		id = cfg.ids()
	}
	if !ValidID(id) {
		return Header{}, fmt.Errorf("thread: invalid session id %q", id)
	}
	h := Header{ID: id, Created: cfg.now()}
	if cfg.lineage.Session != "" {
		l := cfg.lineage
		h.Lineage = &l
	}
	if len(cfg.meta) > 0 {
		h.Meta = maps.Clone(cfg.meta)
	}
	return h, nil
}

// Open loads an existing session from st, positioned at its leaf: the
// entry the next append attaches to, recovered by replaying the
// entries in append order (a leaf entry redirects; any other entry
// leaves the leaf at itself).
//
// Open validates the tree as it indexes it, and a file that fails is
// refused with a *CorruptError (errors.Is ErrCorrupt) naming the line
// and the entry: every entry has a non-empty id that passes ValidID
// and appears once; every non-empty parent names an entry earlier in
// the file — append-only order makes that rule exclude cycles and
// parents the file does not hold — and every leaf entry navigates to
// the root or to an earlier entry. Several roots are legal: Branch to
// the root starts a new one. Nothing is repaired by guessing; a
// context is never quietly cut short where a link is broken.
//
// A load that had to drop a torn tail or skip a salvaged line still
// opens — the entries that survived are the session — and says so
// twice: one warning through the agent's logger, and the report
// Session.LoadReport returns. Under Salvage an entry orphaned by a
// skipped line is kept and listed (OpenReport.Orphaned) rather than
// refused.
//
// Open reads and nothing else: it writes no entry and starts no run.
// A steer the file shows accepted but never settled — the writer
// crashed between the two — is restored to the steer queue (Queue
// lists it) and waits there: the next turn the session runs delivers
// it, Continue runs it as a turn of its own, ClearQueue drops it.
//
// The header options (WithMeta, PublicID, WithLineage) fail Open with
// ErrCreateOnly: the stored header is what the session has.
func Open(ctx context.Context, st Storage, id string, agent *weft.Agent, opts ...SessionOption) (*Session, error) {
	if st == nil {
		return nil, fmt.Errorf("thread: Open with nil storage")
	}
	if agent == nil {
		return nil, fmt.Errorf("thread: Open with nil agent")
	}
	cfg := resolveSession(opts...)
	if len(cfg.createOnly) > 0 {
		return nil, fmt.Errorf("%w: %s given to Open; the stored header is immutable",
			ErrCreateOnly, strings.Join(cfg.createOnly, ", "))
	}
	h, entries, report, err := st.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	s, err := newSession(st, agent, cfg, h, entries, report)
	if err != nil {
		return nil, err
	}
	if r := s.report; r != nil {
		agent.Logger().Warn("thread: session loaded with a repair",
			"session", id, "torn_line", r.Torn, "skipped_lines", r.Skipped,
			"orphaned_entries", r.Orphaned)
	}
	// A queued steer whose fate never landed — the writer crashed or
	// was killed between accepting it and the turn's end batch — is
	// durable input (ADR 0011 §4): it returns to the steer queue, and
	// nothing runs until the caller makes the session run.
	s.resurrectSteers(ctx)
	return s, nil
}

// newSession is the one constructor behind Create, Open and Fork: it
// resolves the configuration against the agent, validates and indexes
// the entries (Open's rules), and recovers everything a session
// derives from them — the leaf, the turn counter the run ids continue
// from, and the compaction trigger's last measurement. report is the
// storage's load report, nil for a session whose entries this process
// just wrote.
func newSession(st Storage, agent *weft.Agent, cfg sessionConfig, h Header, entries []Entry, report *LoadReport) (*Session, error) {
	cfg.compaction.resolve(agent.Model()) // per-model overrides need the agent
	s := &Session{
		st:     st,
		agent:  agent,
		cfg:    cfg,
		header: h,
		order:  entries,
		byID:   make(map[string]int, len(entries)),
	}
	// Only a skipped line can take an entry's parent with it: without
	// one, a link to nowhere is corruption, never damage to work
	// around.
	salvaged := report != nil && len(report.Skipped) > 0
	skipped := map[int]bool{}
	if report != nil {
		for _, n := range report.Skipped {
			skipped[n] = true
		}
	}
	corrupt := func(line int, entry string, format string, args ...any) error {
		return &CorruptError{Session: h.ID, Line: line, Entry: entry, Err: fmt.Errorf(format, args...)}
	}
	var orphaned []string
	orphan := func(id string) {
		if s.orphans == nil {
			s.orphans = map[string]bool{}
		}
		s.orphans[id] = true
		orphaned = append(orphaned, id)
	}
	line := 1 // the header's; entries follow, skipped lines keep their numbers
	for i, e := range entries {
		line++
		for skipped[line] {
			line++
		}
		id := idOf(e)
		switch {
		case id == "":
			return nil, corrupt(line, "", "the entry has no id")
		case !ValidID(id):
			return nil, corrupt(line, id, "the entry's id is not a valid id")
		}
		if first, dup := s.byID[id]; dup {
			return nil, corrupt(line, id, "the entry's id is already held by entry %d of the session", first+1)
		}
		if parent := parentOf(e); parent != "" {
			// The index holds only what came before: a parent found in
			// it is an earlier entry, so the parent links can form
			// neither a cycle nor a chain that leaves the file.
			if _, ok := s.byID[parent]; !ok {
				if !salvaged {
					return nil, corrupt(line, id, "the entry's parent %q is not an earlier entry of the session", parent)
				}
				orphan(id)
			}
		}
		s.byID[id] = i
		if le, ok := e.(LeafEntry); ok {
			_, held := s.byID[le.Entry]
			switch {
			case le.Entry == "" || (held && le.Entry != id):
				s.leaf = le.Entry
			case salvaged:
				// The navigation's target went with a skipped line:
				// the leaf stays where it was, and the report says so.
				if !s.orphans[id] {
					orphan(id)
				}
			default:
				// Loud on the undefined (ADR 0011 §5): a leaf entry
				// that navigates to an entry the file does not hold
				// leaves the session's position meaningless. No code
				// path here writes one (Branch validates its target);
				// a file that holds one was written by something else.
				return nil, corrupt(line, id, "the leaf entry navigates to %q, which is not an earlier entry of the session", le.Entry)
			}
		} else {
			s.leaf = id
		}
		if te, ok := e.(TurnEntry); ok {
			s.turns++
			// The trigger's measurement recovers from the last turn
			// entry that recorded one: its input is the last
			// provider-reported number, and the messages after its
			// entry are the estimated delta.
			if te.LastInput > 0 {
				s.lastInput = te.LastInput
				s.lastMeasureLeaf = te.ID
			}
		}
	}
	// The copied or reloaded turns count for run ids: the next Send
	// mints <session>-t<n+1>, never an id the file already holds.
	s.turnSeq = s.turns
	if report != nil {
		s.report = &OpenReport{
			LoadReport: LoadReport{Torn: report.Torn, Skipped: slices.Clone(report.Skipped)},
			Orphaned:   orphaned,
		}
	}
	return s, nil
}

// LoadReport returns what Open had to drop, skip or accept to load
// the session — the torn final line, the lines skipped under Salvage,
// the entries those skips orphaned — or nil when the load was clean,
// which is every session Create or Fork returned. The value is a copy
// the caller owns. Open also logs it once, as a warning through the
// agent's logger; this accessor is how code, not an operator, learns
// that the session it holds is a repaired one.
func (s *Session) LoadReport() *OpenReport {
	// Written once by the constructor: no lock needed.
	if s.report == nil {
		return nil
	}
	return &OpenReport{
		LoadReport: LoadReport{Torn: s.report.Torn, Skipped: slices.Clone(s.report.Skipped)},
		Orphaned:   slices.Clone(s.report.Orphaned),
	}
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

// Delete removes a session and its entries from st; an id the storage
// does not hold fails with ErrNotFound. History is removed with the
// session, never rewritten (ADR 0011 §5).
//
// Delete does not look for open Session values. One already loaded
// keeps its in-memory tree, its next write fails with ErrNotFound,
// and a turn it is running loses the entries it has yet to write —
// Close the session first.
func Delete(ctx context.Context, st Storage, id string) error {
	if st == nil {
		return fmt.Errorf("thread: Delete with nil storage")
	}
	return st.Delete(ctx, id)
}

// ID returns the session's id — the header's, and the storage key.
func (s *Session) ID() string { return s.header.ID }

// Storage returns the storage the session was created or opened on —
// the handle thread/pool holds to create child sessions in the same
// place (ADR 0022 §3). It is the application's own storage returned,
// read-only by convention: every write to this session goes through
// the Session, never behind it. A closed session still returns it.
func (s *Session) Storage() Storage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storageLocked()
}

// storageLocked is the storage beneath the seal Close puts on the
// write path. Callers hold s.mu.
func (s *Session) storageLocked() Storage {
	if c, ok := s.st.(sealedStorage); ok {
		return c.Storage
	}
	return s.st
}

// Lineage returns the session's pool lineage (ADR 0022 §3): the parent
// session and delegating call it was started from, as recorded in its
// header by WithLineage. The zero value means the session is nobody's
// child.
func (s *Session) Lineage() Lineage {
	// The header is immutable after construction: like ID, no lock.
	if s.header.Lineage == nil {
		return Lineage{}
	}
	return *s.header.Lineage
}

// now is the session's clock: the Clock option's function, or
// time.Now, in UTC. Every timestamp the session writes is read here.
// The function runs under s.mu wherever the caller holds it.
func (s *Session) now() time.Time { return s.cfg.now() }

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
// entry ever written, abandoned branches included. A tree may hold
// several roots (entries with no parent): Branch to the root starts a
// new one. The slice is fresh and every entry in it is a deep copy —
// its maps, slices, raw JSON, and the parts of the messages it
// carries: mutating what comes back never reaches the session or the
// storage.
func (s *Session) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.order))
	for i, e := range s.order {
		out[i] = cloneEntry(e)
	}
	return out
}

// Path returns the entries from a root to entryID, inclusive, in
// conversation order — the chain of parent links. entryID "" is the
// root and returns no entries; an id the session does not hold is an
// error. Path names tree structure: a leaf entry's own parent is
// where it was appended, not the entry it navigated to. The entries
// are deep copies, like Entries'.
//
// The path starts at whichever root entryID descends from — a session
// may hold several — or, in a session opened under Salvage, at an
// entry LoadReport lists as orphaned: what preceded the skipped line
// is not on the path. A parent link that is broken in any other way
// cannot survive Open; were one found here the walk fails with
// ErrCorrupt rather than returning a path cut short.
func (s *Session) Path(entryID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pathLocked(entryID)
}

// pathLocked is Path with s.mu held. Open's validation makes every
// parent link name an earlier entry, so the walk only ever moves
// backwards through the append order and ends at a root or at an
// orphan the load report named. It checks both facts anyway: a link
// that does not hold is an error — the tree in memory is not the tree
// Open vetted — never a silent stop, because a path cut short is a
// model context cut short.
func (s *Session) pathLocked(entryID string) ([]Entry, error) {
	if entryID == "" {
		return nil, nil
	}
	i, ok := s.byID[entryID]
	if !ok {
		return nil, fmt.Errorf("thread: session %s holds no entry %q", s.header.ID, entryID)
	}
	path := make([]Entry, 0, 8)
	for {
		e := s.order[i]
		path = append(path, cloneEntry(e))
		parent := parentOf(e)
		if parent == "" {
			break
		}
		pi, ok := s.byID[parent]
		if !ok {
			if s.orphans[idOf(e)] {
				break // the reported root of a salvaged line
			}
			return nil, &CorruptError{Session: s.header.ID, Entry: idOf(e),
				Err: fmt.Errorf("the entry's parent %q is not held by the session", parent)}
		}
		if pi >= i {
			return nil, &CorruptError{Session: s.header.ID, Entry: idOf(e),
				Err: fmt.Errorf("the entry's parent %q does not precede it", parent)}
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
// title is the last info entry's non-empty Title, the current
// metadata the header's overlaid by every info entry's Meta in append
// order — Title and Meta resolve them.
//
// An empty title keeps the current one, so a title can be replaced
// but not cleared. A nil or empty meta changes nothing; a non-empty
// one overwrites the keys it names and leaves the rest (a key is set
// to "" to blank it; there is no removal). A call that names neither
// a title nor a key has nothing to record: it returns nil and writes
// no entry.
//
// Keys under the reserved "weft." prefix are the session's identity
// (weft.public_id) and belong to the header: SetInfo rejects them
// with ErrReservedKey and writes nothing. Set them at Create, with
// WithMeta or PublicID.
func (s *Session) SetInfo(ctx context.Context, title string, meta map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range slices.Sorted(maps.Keys(meta)) {
		if strings.HasPrefix(k, reservedMetaPrefix) {
			return fmt.Errorf("%w: %q — the %q keys are set once, at Create (WithMeta, PublicID)",
				ErrReservedKey, k, reservedMetaPrefix)
		}
	}
	if title == "" && len(meta) == 0 {
		// Nothing to record; still the answer a closed session or a
		// dead context gives every write.
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.writableLocked()
	}
	if title == "" {
		title = s.titleLocked()
	}
	m := cloneMeta(meta)
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return InfoEntry{ID: id, ParentID: parent, Created: created, Title: title, Meta: m}
	})
}

// Title returns the session's current title — the last info entry's
// (SetInfo's rule), "" before any SetInfo named one. A fork carries
// the info entries on its copied path, and so their title.
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
//
// Keys under the reserved "weft." prefix are the exception to the
// overlay: the header's value stands, whatever an info entry says,
// and a key the header lacks keeps the first value an info entry gave
// it. SetInfo refuses those keys, so only a file written before that
// rule, or by other hands, can hold such an entry — and even there
// the session's identity never changes mid-life.
func (s *Session) Meta() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metaLocked()
}

func (s *Session) metaLocked() map[string]string {
	var meta map[string]string
	merge := func(kv map[string]string, overlay bool) {
		for k, v := range kv {
			if meta == nil {
				meta = map[string]string{}
			}
			if overlay && strings.HasPrefix(k, reservedMetaPrefix) {
				if _, fixed := meta[k]; fixed {
					continue // identity is first-write-wins
				}
			}
			meta[k] = v
		}
	}
	merge(s.header.Meta, false)
	for _, e := range s.order {
		if ie, ok := e.(InfoEntry); ok {
			merge(ie.Meta, true)
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
// the user (ADR 0011 §2). An empty kind is rejected. The message is
// copied: the caller's value is not retained.
func (s *Session) CustomMessage(ctx context.Context, kind string, msg weft.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" {
		return fmt.Errorf("thread: CustomMessage with empty kind")
	}
	msg = deepCloneMessage(msg)
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

// AppendApprovalRequests appends mirrored approval requests in one
// atomic batch (ADR 0022 §7): the pool writes a child session's parked
// calls onto the parent's tree — Child naming the session they park
// in, Wrapper the delegating call they park under — so the parent's
// Pending surfaces them and a decision records like any other. The
// entries' tree fields are minted here, each id vetted like every
// other the session mints (valid, and new to the tree and to the
// batch); the stored entries return. Mirrors are ledger until
// decided: they never join the model's context, and their resolution
// is the pool's to route.
func (s *Session) AppendApprovalRequests(ctx context.Context, reqs ...ApprovalRequestEntry) ([]ApprovalRequestEntry, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("thread: AppendApprovalRequests with no requests")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(reqs))
	parent := s.leaf
	now := s.now()
	out := make([]ApprovalRequestEntry, 0, len(reqs))
	for _, r := range reqs {
		id, err := s.mintCheckedLocked(entries)
		if err != nil {
			return nil, err
		}
		r.ID, r.ParentID, r.Created = id, parent, now
		r.Args = slices.Clone(r.Args)
		entries = append(entries, r)
		out = append(out, r)
		parent = id
	}
	if err := s.appendEntriesLocked(ctx, entries...); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Args = slices.Clone(out[i].Args) // the caller's copy, not the tree's
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

// Continue starts the work the session holds but is not running, and
// returns the first turn that will run — nil when nothing is waiting.
// It is the explicit counterpart of a rule Open keeps: opening a
// session never runs anything.
//
// What can be waiting on an idle session:
//
//   - steers restored by Open — accepted before a crash, never
//     settled (Queue lists them). Continue defers each to a follow-up
//     turn, recorded on its receipt, and runs them in acceptance
//     order under ctx;
//   - sends queued behind an approval boundary that has since been
//     cleared or decided;
//   - an approval boundary whose every call is decided but whose
//     resume never ran (the writer died between the two): with
//     AutoResume on, Continue arms the resume, and the queue follows
//     it.
//
// Without Continue the same work starts with the session's next
// Send, Decide or Resume: a restored steer is delivered into the turn
// that runs, at its first drain point, as any queued steer is.
// Continue is for the caller who wants the accepted input to run now,
// with no new message. While an undecided approval boundary holds the
// session the returned turn waits behind it, as a Send's would. On a
// session that is already running, Continue starts nothing and
// returns the next queued turn, if any. A closed session fails with
// ErrClosed.
func (s *Session) Continue(ctx context.Context) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.admitLocked(); err != nil {
		return nil, err
	}
	var first *Turn
	if !s.running {
		// Nothing drains a steer on an idle session: each becomes a
		// follow-up now, and its run answers to this call's context.
		for i := range s.steerQueue {
			s.steerQueue[i].ctx = ctx
		}
		s.settleSteersLocked()
		if s.cfg.autoResume && s.boundaryLocked() && len(s.pendingLocked()) == 0 {
			t, err := s.armResumeLocked(ctx)
			if err != nil {
				return nil, err
			}
			first = t
		}
	}
	if first == nil && len(s.queue) > 0 {
		first = s.queue[0].turn
	}
	s.kickRunnerLocked()
	return first, nil
}

// admitLocked is the gate new work passes: Send and Continue call it
// first, and a session whose Close has been called refuses. Callers
// hold s.mu.
func (s *Session) admitLocked() error {
	if s.closing != stateOpen {
		return fmt.Errorf("%w: session %s", ErrClosed, s.header.ID)
	}
	return nil
}

// writableLocked is the gate a session-level append passes: refused
// once the session is sealed, and already while a Close that gave up
// waits for its canceled turn to land — every resume run appends its
// audit entry through here before it calls the model, so the refusal
// is what keeps an abandoned session from starting another run.
// Callers hold s.mu.
func (s *Session) writableLocked() error {
	if s.closing >= stateAborted {
		return fmt.Errorf("%w: session %s", ErrClosed, s.header.ID)
	}
	return nil
}

// Close quiesces the session and lets go of its storage. In order:
//
//  1. New work stops at once: from the moment Close is called, Send
//     and Continue fail with ErrClosed.
//  2. Close waits for the session to drain — the running turn, the
//     sends queued behind it, and any approval resume those turns
//     arm. Sends queued behind an approval boundary nobody is going
//     to decide cannot drain: once no turn is running their Turns end
//     with ErrClosed (a queued send's prompt is not yet durable, so
//     nothing stored is lost).
//  3. The session is sealed: every later write — Label, SetInfo,
//     Branch, Decide, Compact, any of them — fails with ErrClosed.
//     Reads (Entries, Path, Context, Pending, Usage, …) keep
//     answering from the tree the session held, and Fork still works:
//     it writes another session.
//  4. The storage's hold on the session is released, when the backend
//     offers that — a Release(ctx, session) method — so another
//     writer can open it. Its error is Close's result.
//
// If ctx ends while Close is waiting, Close gives up: it cancels the
// running turn (which records as canceled, its unanswered calls
// carrying the interruption text), ends every queued Turn with
// ErrClosed, and returns ctx.Err(). The session then stays closed to
// new work, and nothing can start another run — but the canceled turn
// may still be writing its end, so the session is not yet sealed and
// the storage not yet released. Call Close again to finish: it waits
// for that turn to land, seals, and releases. Steers still queued at
// that moment keep their queued receipts in the file, and the next
// Open restores them.
//
// Close is idempotent and safe to call from several goroutines: once
// a Close has completed, every call returns what the release
// returned. It does not wait for thread/pool children the session
// delegated to — close the pool first. It must not be called from
// inside the session's own run (a tool, a hook): it would wait for
// the turn that is calling it.
func (s *Session) Close(ctx context.Context) error {
	backoff := 50 * time.Microsecond
	for {
		s.mu.Lock()
		if s.closing == stateSealed {
			done := s.closeDone
			s.mu.Unlock()
			select {
			case <-done:
				s.mu.Lock()
				err := s.closeErr
				s.mu.Unlock()
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if s.closing == stateOpen {
			s.closing = stateDraining
		}
		if !s.running {
			return s.sealLocked(ctx) // unlocks
		}
		t, aborted := s.inFlight, s.closing == stateAborted
		s.mu.Unlock()
		if aborted && t != nil {
			// A Close that gave up left the session refusing work, yet
			// a turn is in flight: fell it too, so the wait is short.
			t.fell()
		}
		if t != nil && !t.isDecided() {
			backoff = 50 * time.Microsecond
			done := make(chan struct{})
			go func() {
				_, _ = t.Wait() // ends with the turn; a canceled one lands at once
				close(done)
			}()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				s.abandon()
				return ctx.Err()
			}
		}
		// Between a turn's landing and the runner's next decision —
		// its next item, or its exit — there is nothing to wait on
		// but the runner itself: look again shortly.
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
			backoff = min(2*backoff, 5*time.Millisecond)
		case <-ctx.Done():
			timer.Stop()
			s.abandon()
			return ctx.Err()
		}
	}
}

// sealLocked is Close's last step, entered with s.mu held and no
// runner alive; it returns with s.mu released. Whatever is still
// queued can no longer run and ends with ErrClosed; the write path is
// sealed; the storage's hold is released outside the lock, and every
// Close waits for that release through closeDone.
func (s *Session) sealLocked(ctx context.Context) error {
	closed := fmt.Errorf("%w: session %s", ErrClosed, s.header.ID)
	queued := s.queue
	s.queue = nil
	st := s.storageLocked()
	s.st = sealedStorage{Storage: st, session: s.header.ID}
	s.closing = stateSealed
	done := make(chan struct{})
	s.closeDone = done
	s.mu.Unlock()

	for _, ps := range queued {
		ps.turn.finish(nil, closed)
	}
	var err error
	if r, ok := st.(interface {
		Release(ctx context.Context, session string) error
	}); ok {
		// The release must not die with the caller's context: the
		// session is sealed either way, and a hold left behind would
		// lock the next writer out.
		err = r.Release(context.WithoutCancel(ctx), s.header.ID)
	}
	s.mu.Lock()
	s.closeErr = err
	s.mu.Unlock()
	close(done)
	return err
}

// abandon is what a Close whose context ended leaves behind: the
// queues emptied — their Turns ended with ErrClosed — the running
// turn felled, and the session in the aborted state, where no
// session-level append lands, so that no resume can start a run
// behind the caller's back. The felled turn's own end batch still
// lands; a later Close seals after it.
func (s *Session) abandon() {
	s.mu.Lock()
	if s.closing == stateSealed {
		s.mu.Unlock()
		return // another Close finished the job meanwhile
	}
	s.closing = stateAborted
	closed := fmt.Errorf("%w: session %s", ErrClosed, s.header.ID)
	var ended []*Turn
	for _, ps := range s.queue {
		ended = append(ended, ps.turn)
	}
	s.queue = nil
	// Queued steers keep their queued receipts in the file: the next
	// Open restores them. Only their in-memory Turns end here.
	for _, q := range s.steerQueue {
		if q.turn != nil {
			ended = append(ended, q.turn)
		}
	}
	s.steerQueue = nil
	if rw := s.resumeWork; rw != nil {
		s.resumeWork = nil
		if s.await.resumed == rw.turn {
			s.await.resumed = nil
		}
		ended = append(ended, rw.turn)
	}
	t := s.inFlight
	s.mu.Unlock()
	for _, e := range ended {
		e.finish(nil, closed)
	}
	if t != nil {
		t.fell()
	}
}

// fell cancels the turn's run on behalf of Close: the interrupted
// mark makes a run that has not armed its cancel yet die at birth,
// and gives the partial transcript's unanswered calls the
// interruption text — the same mechanics as a Send under the
// Interrupt policy, without a follow-up.
func (t *Turn) fell() {
	t.mu.Lock()
	t.interrupted = true
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// sealedStorage is the write path of a closed session: the storage it
// wraps, with every write to the closed session refused. Close swaps
// it in under s.mu, so every append site in the package — whichever
// file it lives in — fails with ErrClosed once the session is sealed,
// without each having to ask. Creating another session (Fork) and the
// reads pass through.
type sealedStorage struct {
	Storage
	session string
}

// Append refuses writes to the sealed session.
func (c sealedStorage) Append(ctx context.Context, session string, entries ...Entry) error {
	if session == c.session {
		return fmt.Errorf("%w: session %s", ErrClosed, session)
	}
	return c.Storage.Append(ctx, session, entries...)
}

// appendLocked is the Session's single-entry write: it mints the
// entry's id, parent (the leaf) and time, hands the entry to the
// storage, and adopts it into the in-memory tree only after the
// storage accepted it — a failed Append leaves the session exactly as
// it was, so the tree in memory and the session in storage cannot
// diverge. Callers hold s.mu.
func (s *Session) appendLocked(ctx context.Context, build func(id, parent string, created time.Time) Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id, err := s.mintCheckedLocked(nil)
	if err != nil {
		return err
	}
	return s.appendEntriesLocked(ctx, build(id, s.leaf, s.now()))
}

// mintCheckedLocked mints one entry id through the session's ids
// option and vets it — the guard every id a write carries passes: it
// must satisfy ValidID, and it must be new to the tree and to batch,
// the entries already built for the same append. An id the tree
// already holds would make it ambiguous — which entry does the id
// name? — and every walk would read one of the two. Random ids cannot
// collide; an IDs function can. Callers hold s.mu.
func (s *Session) mintCheckedLocked(batch []Entry) (string, error) {
	id := s.mintIDLocked()
	if !ValidID(id) {
		return "", fmt.Errorf("thread: invalid entry id %q", id)
	}
	if _, dup := s.byID[id]; dup {
		return "", fmt.Errorf("thread: entry id %q already held by session %s", id, s.header.ID)
	}
	for _, e := range batch {
		if idOf(e) == id {
			return "", fmt.Errorf("thread: entry id %q minted twice for one append to session %s", id, s.header.ID)
		}
	}
	return id, nil
}

// appendEntriesLocked writes a batch whose ids mintCheckedLocked
// vetted: one atomic Append, the entries adopted into the tree only
// once the storage accepted them, then the flush that makes the write
// durable before the call returns — under the FsyncOnFlush cadence
// the flush is what makes it so, and losing a revocation or a
// decision to a crash must not be possible after a successful call.
// A session whose Close has sealed or abandoned it refuses with
// ErrClosed. Callers hold s.mu.
func (s *Session) appendEntriesLocked(ctx context.Context, entries ...Entry) error {
	if err := s.writableLocked(); err != nil {
		return err
	}
	if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
		return err
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
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

// cloneEntry returns a deep copy of an entry: every map, slice, raw
// JSON value and pointed-to message it carries is copied, the parts of
// its messages included, so Entries, Path and Audit hand out
// snapshots — nothing a caller does to one reaches the tree. Every
// kind of the sealed set has a case; a kind without one would be
// returned shared, and the clone test fails on it.
func cloneEntry(e Entry) Entry {
	switch e := e.(type) {
	case MessageEntry:
		e.Message = deepCloneMessage(e.Message)
		return e
	case TurnEntry:
		e.Pending = slices.Clone(e.Pending)
		for i := range e.Pending {
			e.Pending[i].Args = slices.Clone(e.Pending[i].Args)
		}
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
		e.Meta = maps.Clone(e.Meta)
		return e
	case CustomEntry:
		e.Data = slices.Clone(e.Data)
		return e
	case CustomMessageEntry:
		e.Message = deepCloneMessage(e.Message)
		return e
	case ApprovalRequestEntry:
		e.Args = slices.Clone(e.Args)
		return e
	case ApprovalDecisionEntry:
		return e
	case ApprovalAuditEntry:
		return e
	case GrantEntry:
		e.Args = slices.Clone(e.Args)
		for i := range e.Args {
			e.Args[i].Equals = slices.Clone(e.Args[i].Equals)
		}
		return e
	case GrantRevokedEntry:
		return e
	case ReceiptEntry:
		if e.Msg != nil {
			m := deepCloneMessage(*e.Msg)
			e.Msg = &m
		}
		return e
	case PoolReceiptEntry:
		return e
	}
	return e
}

// deepCloneMessage copies a message down to the bytes: the part slice,
// and inside each part the fields a retained value could be written
// through — a tool call's raw arguments, a file's data. The other
// part kinds hold only strings.
func deepCloneMessage(m weft.Message) weft.Message {
	m.Content = slices.Clone(m.Content)
	for i, p := range m.Content {
		switch p := p.(type) {
		case weft.ToolCallPart:
			p.Args = slices.Clone(p.Args)
			m.Content[i] = p
		case weft.FilePart:
			p.Data = slices.Clone(p.Data)
			m.Content[i] = p
		}
	}
	return m
}
