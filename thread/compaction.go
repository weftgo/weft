package thread

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
)

// The compaction defaults (ADR 0020 §2): zero configuration works once
// a window is known, and these are the numbers it runs with.
const (
	// defaultReserve is the headroom the trigger keeps above the
	// window: compaction fires before the context is within Reserve
	// tokens of full.
	defaultReserve int64 = 16_384
	// defaultKeepRecent is roughly how much of the tail stays raw on
	// either side of the cut.
	defaultKeepRecent int64 = 20_000
	// defaultToolResultCap is the character cap on a tool result in
	// the transcript the summarizer sees.
	defaultToolResultCap = 2_000
	// summaryCapShare of Reserve is the summarizer's output cap.
	summaryCapShare = 0.8
)

// The fixed summary marker — model-visible bytes, pinned by the
// golden test testdata/compaction/summary-marker.txt. The summary
// rides in the context as a user message wrapped in it, so the model
// can tell weft's summaries from the conversation, and so the bytes
// never drift silently (ADR 0020 §2: model-visible changes need an ADR
// and a golden).
const (
	summaryMarkerOpen  = "<weft-summary>"
	summaryMarkerClose = "</weft-summary>"
)

// summaryMessage wraps a summary text in the fixed marker as a user
// message — the one shape both a compaction's summary and a branch
// summary take in the context.
func summaryMessage(text string) core.Message {
	return core.User(summaryMarkerOpen + "\n" + text + "\n" + summaryMarkerClose)
}

// summarySkeleton is the summarizer's system prompt — model-visible
// bytes, pinned by testdata/compaction/summary-prompt.txt. The
// headings are the ADR's; the two lists stay cumulative across
// iterative compactions because each summary is fed the previous one.
const summarySkeleton = `You are summarizing the older part of a conversation so it can be
replaced by this summary. Under each of these exact headings, in this
order, write that part's content in plain prose:

Goal:
Constraints:
Progress:
Key Decisions:
Next Steps:
Critical Context:

Then, last, two lists that carry over anything the previous summary
already listed, one item per line, no bullets:

Files read:
Files modified:

Keep every file path, identifier, number, and decision exact. A detail
you drop is a detail the conversation loses. Do not add commentary
about this task.`

// The compaction sentinels. Match with errors.Is; every error this
// file returns for one of these conditions wraps its sentinel.
var (
	// ErrNothingToCompact is the refusal of a compaction that would
	// summarize nothing: the tail fits inside KeepRecent, nothing new
	// sits past the previous boundary, or the plan names the boundary
	// the previous compaction already left. Nothing is written.
	ErrNothingToCompact = errors.New("thread: nothing to compact")

	// ErrCompactCanceled is returned when a BeforeCompact hook answered
	// Cancel. Nothing is written.
	ErrCompactCanceled = errors.New("thread: compaction canceled")

	// ErrNoEntry is returned when an operation names an entry the
	// session does not hold — Pin's target, a compaction's FirstKept —
	// or asks for one the leaf's path does not have (Uncompact with no
	// compaction to undo).
	ErrNoEntry = errors.New("thread: session holds no such entry")

	// ErrSummaryTruncated is the failure of a summary the model cut
	// off at its output cap (a max_tokens finish). A cut summary is
	// never stored: the attempt is retried once, the chain falls back,
	// and with no fallback left the compaction fails wrapping this.
	// Raise SummaryMaxTokens (or Reserve, which sizes the default cap).
	ErrSummaryTruncated = errors.New("thread: summary truncated at the output cap")

	// ErrInvalidCompaction is returned for a Compaction that cannot be
	// written as given: a FirstKept off the leaf's path or before the
	// previous boundary, a summary-less plan that is not a trim, a trim
	// without its record, a trim record naming a result the path does
	// not hold — and for a Trimmer whose output the trim record cannot
	// represent.
	ErrInvalidCompaction = errors.New("thread: invalid compaction")

	// ErrCompactConfig is returned by Create and Open when the
	// compaction knobs cannot work together: with a known window,
	// Reserve must be below it and KeepRecent below window − Reserve.
	ErrCompactConfig = errors.New("thread: invalid compaction configuration")

	// ErrNotPinnable is returned by Pin for an entry that contributes
	// no message to the context (a turn, a label, a custom entry, …):
	// there is nothing a compaction could keep showing.
	ErrNotPinnable = errors.New("thread: entry cannot be pinned")

	// ErrAwaitingApproval is returned by Compact and ApplyCompaction
	// while approval requests are pending: the parked tail must stay
	// raw for its decisions to resolve. Decide, or branch away, first.
	ErrAwaitingApproval = errors.New("thread: approval requests pending")
)

// Compaction is one computed compaction: the plan PreviewCompaction
// returns, ApplyCompaction writes as a compaction entry, and Compact
// does both. It carries everything the entry records (ADR 0020 §1) —
// nothing else: applying writes exactly these fields, nothing is
// recomputed.
type Compaction struct {
	// Summary is the summarizer's text, shown in the context behind
	// the fixed marker from the entry's first kept entry onward. Empty
	// only on a trim.
	Summary string
	// FirstKept is the id of the first entry the context still shows
	// raw after this compaction.
	FirstKept string
	// TokensBefore is the estimated size of the context the model was
	// shown at compaction time — the compacted view (the previous
	// summary, pinned entries, the kept tail with trims applied), not
	// the raw path.
	TokensBefore int64
	// Reason is why it ran: manual, threshold, overflow, from_hook, or
	// trim. ApplyCompaction records an empty Reason as manual.
	Reason Reason
	// SummarizerUsage and SummarizerModel record what the summary
	// cost and which model made it — the cost ledger's inputs.
	SummarizerUsage core.Usage
	SummarizerModel core.ModelInfo
	// FilesRead lists the file URLs the summarized range carried,
	// sorted and deduplicated.
	FilesRead []string
	// Pinned lists the pinned entry ids below FirstKept: the context
	// re-includes each of them, raw, after the summary.
	Pinned []string
	// RangeHash is the SHA-256 of the serialized range the summarizer
	// was fed — the audit that a summary summarizes exactly this.
	RangeHash string
	// Trim is the trim record: required when Reason is trim, forbidden
	// otherwise. The session's Trimmer pre-pass builds it; see
	// TrimRecord.
	Trim *TrimRecord

	// runner marks a plan computed on the session's own runner — the
	// trigger and the overflow re-run — whose write is part of the turn
	// in flight and so passes ApplyCompaction's busy guard.
	runner bool
}

// compactConfig is the compaction configuration a session resolves:
// the defaults with the options' overrides.
type compactConfig struct {
	window     int64 // 0 = unknown: no automatic compaction
	reserve    int64
	keepRecent int64
	// The five layers (ADR 0020 §3), all optional; the zero values
	// mean "not set" and the defaults above carry the algorithm.
	modelWindows     map[core.ModelInfo]int64
	modelReserves    map[core.ModelInfo]int64
	trigger          func(TriggerInput) bool
	minTurnsBetween  int
	maxPerSession    int
	estimator        Estimator
	disabled         bool
	summaryModel     core.Model
	summaryPrompt    string
	summaryFocus     string
	summaryMaxTokens int64
	summarizer       Summarizer
	compactor        Compactor
	trimmer          Trimmer
	before           func(context.Context, *Preparation) (Verdict, error)
	after            func(context.Context, CompactionEntry)
	failed           func(context.Context, Reason, error)
	check            func(Summary) error
	preferNative     bool
}

func defaultCompactConfig() compactConfig {
	return compactConfig{reserve: defaultReserve, keepRecent: defaultKeepRecent}
}

// resolve folds the per-model overrides for the session agent's model
// into the plain fields and validates the result — Create and Open
// call it once, after the options and with the agent known. With a
// known window the knobs must leave room for each other: a Reserve at
// or above the window makes the trigger line zero or negative, and a
// KeepRecent at or above window − Reserve keeps a tail that alone
// crosses it — either way compaction could never bring the context
// under the line, so the configuration is an error (ErrCompactConfig)
// instead of a session that silently never compacts. With no window
// known nothing is validated: only manual compaction runs.
func (c *compactConfig) resolve(m core.Model) error {
	if n, ok := c.modelWindows[core.InfoOf(m)]; ok {
		c.window = n
	}
	if n, ok := c.modelReserves[core.InfoOf(m)]; ok {
		c.reserve = n
	}
	if c.window <= 0 {
		return nil
	}
	if c.reserve >= c.window {
		return fmt.Errorf("%w: Reserve %d must be below the context window %d", ErrCompactConfig, c.reserve, c.window)
	}
	if c.keepRecent >= c.window-c.reserve {
		return fmt.Errorf("%w: KeepRecent %d must be below window − Reserve (%d − %d = %d)",
			ErrCompactConfig, c.keepRecent, c.window, c.reserve, c.window-c.reserve)
	}
	return nil
}

// PreviewCompaction computes the session's next compaction without
// writing anything: the cut, the summarized range, the summary text,
// and the entry fields a Compact would record. It runs the whole
// algorithm — the BeforeCompact hook, then the Compactor or the
// summary chain (SummaryModel, then the session's own model, ADR 0020
// §2) with the skeleton prompt, the previous summary (iterative
// compaction) as the range's first message, no cache hints, and the
// output capped at SummaryMaxTokens or 0.8 × Reserve.
//
// A session whose tail fits inside KeepRecent has nothing to compact
// and gets ErrNothingToCompact, not a no-op plan; a hook's Cancel is
// ErrCompactCanceled; a summary cut off at the cap with no fallback
// left is ErrSummaryTruncated. A dry run is not a failed compaction:
// CompactFailed does not run for a PreviewCompaction error. It may be
// called while a turn runs — it reads a snapshot — but the plan's
// ApplyCompaction then waits for the turn (ErrBusy).
func (s *Session) PreviewCompaction(ctx context.Context, opts ...CompactOption) (*Compaction, error) {
	return s.planCompaction(ctx, ReasonManual, resolveCompact(opts...).instructions)
}

// Compact computes and applies the session's next compaction in one
// call: PreviewCompaction, then ApplyCompaction. Nothing is deleted —
// the summarized entries stay in the file, and the context at the leaf
// becomes the summary, the pinned entries, then the entries from the
// compaction's first kept entry onward.
//
// Compaction is a between-turns operation: while a turn is in flight
// Compact fails with ErrBusy before any model call, and while approval
// requests are pending with ErrAwaitingApproval. A failure past those
// gates — other than ErrNothingToCompact and ErrCompactCanceled —
// also reaches the CompactFailed hook.
func (s *Session) Compact(ctx context.Context, opts ...CompactOption) error {
	// Fail fast before the model call: the authoritative checks run
	// under the lock at the append, but a turn already running should
	// not make the caller pay for a summary first.
	s.mu.Lock()
	err := s.compactGateLocked(false)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	c, err := s.planCompaction(ctx, ReasonManual, resolveCompact(opts...).instructions)
	if err != nil {
		s.reportFailed(ctx, ReasonManual, err)
		return err
	}
	return s.ApplyCompaction(ctx, c)
}

// compactGateLocked is the between-turns rule every compaction write
// passes: no turn in flight (unless the write is the runner's own, the
// turn's housekeeping), and no approval boundary open — a parked tail
// must stay raw for its decisions to resolve. Callers hold s.mu.
func (s *Session) compactGateLocked(runner bool) error {
	// A closed session compacts nothing, and says so before a summary
	// is paid for: a caller's compaction stops at Close, the runner's
	// own stops once the session can no longer be written.
	if runner {
		if err := s.writableLocked(); err != nil {
			return err
		}
	} else if err := s.admitLocked(); err != nil {
		return err
	}
	if !runner && s.running && s.inFlight != nil {
		return fmt.Errorf("%w: session %s is running a turn; compact between turns", ErrBusy, s.header.ID)
	}
	if s.boundaryLocked() {
		return fmt.Errorf("%w: session %s; compact after they resolve", ErrAwaitingApproval, s.header.ID)
	}
	return nil
}

// ApplyCompaction writes a computed Compaction as the session's next
// compaction entry, appended at the leaf, and then runs the
// AfterCompact hook with the entry. The entry lands whatever else
// happened between its computation and this call — the tree only grew,
// so the kept range stays correct — provided the plan still fits the
// leaf's path:
//
//   - c.FirstKept must name an entry on the leaf's path (ErrNoEntry
//     when the session does not hold it, ErrInvalidCompaction when it
//     sits on another branch), at or after the previous summary
//     compaction's boundary — an iterative compaction never summarizes
//     what a summary already replaced (ADR 0020 §1).
//   - A summary compaction naming the very boundary the previous one
//     left adds nothing and is refused with ErrNothingToCompact.
//   - A compaction without a summary must be a trim (Reason trim), and
//     a trim must carry its TrimRecord, every stub naming a tool
//     result on the path; a summary compaction must carry none.
//
// Like Compact, it is a between-turns operation: ErrBusy while a turn
// is in flight, ErrAwaitingApproval while approval requests are
// pending. Nothing is written on any error; a validation or storage
// failure also reaches the CompactFailed hook. An empty c.Reason is
// recorded as manual.
func (s *Session) ApplyCompaction(ctx context.Context, c *Compaction) error {
	if c == nil {
		return fmt.Errorf("%w: ApplyCompaction with no Compaction", ErrInvalidCompaction)
	}
	plan := *c
	if plan.Reason == "" {
		plan.Reason = ReasonManual
	}
	s.mu.Lock()
	e, flushErr, err := s.applyCompactionLocked(ctx, &plan)
	s.mu.Unlock()
	// Everything below runs without the lock: the logger and both
	// hooks are the caller's code, and a hook that reads the session
	// (Context, Usage, Leaf) must not deadlock on the write it is
	// being told about.
	if err != nil {
		s.reportFailed(ctx, plan.Reason, err)
		return err
	}
	if flushErr != nil {
		s.agent.Logger().Warn("thread: compaction flush failed", "session", s.header.ID, "err", flushErr)
	}
	if e.Trim != nil {
		s.agent.Logger().Info("thread: trimmed old tool results",
			"trace", "compaction",
			"session", s.header.ID, "reason", string(e.Reason),
			"tokens_before", e.TokensBefore, "cleared", len(e.Trim.Stubs))
	} else {
		s.agent.Logger().Info("thread: compacted",
			"trace", "compaction",
			"session", s.header.ID, "reason", string(e.Reason),
			"tokens_before", e.TokensBefore, "first_kept", e.FirstKept)
	}
	s.safeAfter(ctx, e) // the durable record, not the plan
	return nil
}

// applyCompactionLocked validates c against the leaf's path and
// appends its entry. It returns the entry, a flush error to log (the
// entry is adopted either way), and the error that stopped the write.
// Callers hold s.mu.
func (s *Session) applyCompactionLocked(ctx context.Context, c *Compaction) (CompactionEntry, error, error) {
	if err := s.compactGateLocked(c.runner); err != nil {
		return CompactionEntry{}, nil, err
	}
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return CompactionEntry{}, nil, err
	}
	keptIdx := indexOfID(path, c.FirstKept)
	if keptIdx < 0 {
		if _, held := s.byID[c.FirstKept]; !held {
			return CompactionEntry{}, nil, fmt.Errorf("%w: compaction keeps first entry %q, which session %s does not hold", ErrNoEntry, c.FirstKept, s.header.ID)
		}
		return CompactionEntry{}, nil, fmt.Errorf("%w: compaction keeps first entry %q, which is not on session %s's leaf path", ErrInvalidCompaction, c.FirstKept, s.header.ID)
	}
	// The previous boundary is the latest summary compaction's — a trim
	// record in between never moved it.
	b := boundaryOf(path)
	if b.governor >= 0 && keptIdx < b.start {
		return CompactionEntry{}, nil, fmt.Errorf("%w: compaction keeps first entry %q, before the previous compaction's kept boundary %q (ADR 0020 §1)",
			ErrInvalidCompaction, c.FirstKept, idOf(path[b.start]))
	}
	if c.Reason == ReasonTrim {
		if c.Summary != "" {
			return CompactionEntry{}, nil, fmt.Errorf("%w: a trim carries no summary", ErrInvalidCompaction)
		}
		if err := validTrimRecord(path, c.Trim); err != nil {
			return CompactionEntry{}, nil, err
		}
	} else {
		if c.Trim != nil {
			return CompactionEntry{}, nil, fmt.Errorf("%w: only a trim (Reason trim) carries a trim record", ErrInvalidCompaction)
		}
		if c.Summary == "" {
			return CompactionEntry{}, nil, fmt.Errorf("%w: a compaction without a summary must be a trim (ADR 0020 §1)", ErrInvalidCompaction)
		}
		if b.governor >= 0 && keptIdx == b.start {
			return CompactionEntry{}, nil, fmt.Errorf("%w: session %s's previous compaction already keeps from %q", ErrNothingToCompact, s.header.ID, c.FirstKept)
		}
	}
	id := s.mintIDLocked()
	if !ValidID(id) {
		return CompactionEntry{}, nil, fmt.Errorf("thread: invalid entry id %q", id)
	}
	if _, dup := s.byID[id]; dup {
		return CompactionEntry{}, nil, fmt.Errorf("thread: entry id %q already held by session %s", id, s.header.ID)
	}
	e := CompactionEntry{
		ID:              id,
		ParentID:        s.leaf,
		Created:         s.now(),
		Summary:         c.Summary,
		FirstKept:       c.FirstKept,
		TokensBefore:    c.TokensBefore,
		Reason:          c.Reason,
		SummarizerUsage: c.SummarizerUsage,
		SummarizerModel: c.SummarizerModel,
		FilesRead:       slices.Clone(c.FilesRead),
		Pinned:          slices.Clone(c.Pinned),
		RangeHash:       c.RangeHash,
		Trim:            cloneTrim(c.Trim),
	}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return CompactionEntry{}, nil, err
	}
	s.adoptLocked(e)
	// A compaction landed: the next "nothing to compact" after a
	// trigger fire is news again.
	s.warnedNothing = false
	out := cloneEntry(e).(CompactionEntry) // the hook's copy, never the tree's slices
	return out, s.flushLocked(ctx), nil
}

// cloneTrim copies a trim record so a caller's Compaction and the
// tree's entry never share a stub slice.
func cloneTrim(t *TrimRecord) *TrimRecord {
	if t == nil {
		return nil
	}
	return &TrimRecord{Stubs: slices.Clone(t.Stubs)}
}

// validTrimRecord checks a trim's record against the path it lands on:
// present, non-empty, and every stub naming a tool result an entry on
// the path really holds — the walk replays the record literally, so a
// stub pointing at nothing would be a silent no-op forever.
func validTrimRecord(path []Entry, t *TrimRecord) error {
	if t == nil || len(t.Stubs) == 0 {
		return fmt.Errorf("%w: a trim must carry a trim record naming at least one stubbed tool result", ErrInvalidCompaction)
	}
	for _, st := range t.Stubs {
		i := indexOfID(path, st.Entry)
		if i < 0 {
			return fmt.Errorf("%w: trim record names entry %q, which is not on the leaf's path", ErrInvalidCompaction, st.Entry)
		}
		m, ok := contextMessage(path[i])
		found := false
		if ok {
			for _, p := range m.Content {
				if r, isResult := p.(core.ToolResultPart); isResult && r.CallID == st.CallID {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("%w: trim record names tool result %q in entry %q, which holds none", ErrInvalidCompaction, st.CallID, st.Entry)
		}
	}
	return nil
}

// pathBoundary is where a path's context begins: the governing summary
// compaction, its first kept entry, and the trim records layered above
// it.
type pathBoundary struct {
	governor int   // index of the governing summary compaction; -1 when none
	start    int   // index of the first kept entry; 0 when none governs
	trims    []int // indices of the trim records above the governor, newest first
	// unusable lists summary compactions the walk had to skip: their
	// FirstKept is not on the path below them (a hand-made file).
	unusable []CompactionEntry
}

// boundaryOf finds the path's boundary: walking back from the leaf,
// the first summary compaction whose FirstKept the path reaches below
// it governs. Trim records never govern — a trim that reset the
// boundary would resurface, raw, everything the summary had replaced —
// and a summary compaction whose FirstKept the path does not reach is
// skipped, so the next older one governs (none: the whole path).
func boundaryOf(path []Entry) pathBoundary {
	b := pathBoundary{governor: -1}
	for i := len(path) - 1; i >= 0; i-- {
		c, ok := path[i].(CompactionEntry)
		if !ok {
			continue
		}
		if c.isTrim() {
			b.trims = append(b.trims, i)
			continue
		}
		j := indexOfID(path[:i], c.FirstKept)
		if j < 0 {
			b.unusable = append(b.unusable, c)
			continue
		}
		b.governor, b.start = i, j
		break
	}
	return b
}

// The hook wrappers: a panicking hook is contained — the deciding
// hooks (before, compactor, summarizer, check) turn the panic into
// the compaction's error, and the observing hooks (after, failed)
// log it through the agent's logger and move on. A hook must not
// take the turn machinery with it. Every one of them runs without
// s.mu: a hook may call the session.
func safeBefore(fn func(context.Context, *Preparation) (Verdict, error), ctx context.Context, p *Preparation) (v Verdict, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("thread: BeforeCompact panicked: %v", r)
		}
	}()
	return fn(ctx, p)
}

func safeCompactor(c Compactor, ctx context.Context, p Preparation) (comp *Compaction, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("thread: the Compactor panicked: %v", r)
		}
	}()
	return c.Compact(ctx, p)
}

func safeSummarize(fn Summarizer, ctx context.Context, in SummaryInput) (sum Summary, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("thread: the Summarizer panicked: %v", r)
		}
	}()
	return fn.Summarize(ctx, in)
}

func safeCheck(fn func(Summary) error, sum Summary) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("thread: CheckSummary panicked: %v", r)
		}
	}()
	return fn(sum)
}

func (s *Session) safeAfter(ctx context.Context, e CompactionEntry) {
	fn := s.cfg.compaction.after
	if fn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.agent.Logger().Error("thread: AfterCompact panicked", "panic", r)
		}
	}()
	fn(ctx, e)
}

// Uncompact undoes the latest compaction entry on the leaf's path —
// a summary compaction or a trim, whichever landed last — the only
// way the format allows: a branch back to the entry before it (ADR
// 0020 §1), a leaf entry, no rewrite, nothing deleted. The context is
// exactly what it was before that entry landed; the entry and
// everything after it stay in the file, off the path. After a
// compaction followed by a trim, the first Uncompact undoes the trim
// and a second one the compaction.
//
// It is a navigation, so Branch's rule applies: ErrBusy while a turn
// is in flight. With no compaction on the path it fails wrapping
// ErrNoEntry.
func (s *Session) Uncompact(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && s.inFlight != nil {
		return fmt.Errorf("%w: session %s is running a turn; uncompact between turns", ErrBusy, s.header.ID)
	}
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return err
	}
	for i := len(path) - 1; i >= 0; i-- {
		if _, ok := path[i].(CompactionEntry); ok {
			target := parentOf(path[i])
			if target == "" {
				return fmt.Errorf("%w: session %s's compaction has no entry before it to branch back to", ErrNoEntry, s.header.ID)
			}
			if _, ok := s.byID[target]; !ok {
				return fmt.Errorf("%w: session %s holds no entry %q to branch back to", ErrNoEntry, s.header.ID, target)
			}
			if err := s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
				return LeafEntry{ID: id, ParentID: parent, Created: created, Entry: target}
			}); err != nil {
				return err
			}
			// Like any navigation off a parked tail, the undo may clear
			// what held queued sends.
			s.kickRunnerLocked()
			return nil
		}
	}
	return fmt.Errorf("%w: session %s has no compaction on its leaf's path to undo", ErrNoEntry, s.header.ID)
}

// computeCompaction is the runner's planCompaction: the trigger and
// the overflow re-run compute through it. A failure reaches the
// CompactFailed hook (the refusals aside), and the plan it returns is
// marked as the runner's own, so its ApplyCompaction passes the busy
// guard — the write is part of the turn in flight, placed by the
// runner between that turn's steps or at its edges.
func (s *Session) computeCompaction(ctx context.Context, reason Reason, instructions string) (*Compaction, error) {
	c, err := s.planCompaction(ctx, reason, instructions)
	if err != nil {
		s.reportFailed(ctx, reason, err)
		return nil, err
	}
	c.runner = true
	return c, nil
}

// planCompaction is the algorithm: snapshot the leaf's context and
// path, cut, build the Preparation, let the BeforeCompact hook decide
// and edit, then summarize — through the configured layers: a custom
// Compactor replaces the rest, a custom Summarizer just the text, and
// the default runs the model chain (SummaryModel → session model) with
// the CheckSummary retry and the PreferNative seam. Every caller hook
// and every model call runs without the session lock — they take
// seconds and may call the session — and the tree only grows
// meanwhile, so the snapshot's cut stays valid to apply. It writes
// nothing and reports to no hook: its callers decide what a failure
// means.
func (s *Session) planCompaction(ctx context.Context, reason Reason, instructions string) (*Compaction, error) {
	s.mu.Lock()
	view, err := s.contextViewLocked()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	cfg := s.cfg.compaction
	pinned := s.pinnedIDsLocked()
	s.mu.Unlock()

	path := view.path
	// The iterative chain: the previous summary is the governing
	// summary compaction's — a trim record above it is skipped, or its
	// empty summary would cut the chain and drop everything the earlier
	// summaries held. The previous kept boundary is where the range to
	// summarize starts: entries below it exist only inside the previous
	// summary, and re-serializing them would feed the summarizer the
	// whole history every time — an input that itself outgrows the
	// window (ADR 0020 §1).
	prev, rangeStart := "", 0
	if view.governor >= 0 {
		prev = path[view.governor].(CompactionEntry).Summary
		rangeStart = view.start
	}

	cut := cutIndexWeighted(path, cfg.keepRecent, s.entryWeight)
	if cut < 0 {
		return nil, fmt.Errorf("%w: session %s's tail fits inside KeepRecent", ErrNothingToCompact, s.header.ID)
	}
	if cut <= rangeStart {
		// Nothing new to summarize: everything past the previous kept
		// boundary still fits — the iterative chain is fed nothing it
		// does not already hold.
		return nil, fmt.Errorf("%w: session %s has nothing new past the kept boundary", ErrNothingToCompact, s.header.ID)
	}
	rangeMsgs := rangeMessages(path, rangeStart, cut)
	pinnedThrough := pinnedBelow(path, cut, pinned)
	// What the model was shown: the compacted view, not the raw path —
	// after a first compaction the two differ by everything the summary
	// replaced. Each message carries its own Content slice: the hook
	// and the Compactor are handed copies, never the tree's.
	contextMsgs := make([]core.Message, len(view.msgs))
	for i, vm := range view.msgs {
		contextMsgs[i] = cloneMessage(vm.msg)
	}
	tokensBefore := s.estimateAll(contextMsgs)

	prep := Preparation{
		Reason:       reason,
		Context:      contextMsgs,
		Messages:     rangeMsgs,
		PrevSummary:  prev,
		FirstKept:    idOf(path[cut]),
		TokensBefore: tokensBefore,
		Instructions: instructions,
		Pinned:       pinnedThrough,
	}
	if cfg.before != nil {
		v, err := safeBefore(cfg.before, ctx, &prep)
		if err != nil {
			return nil, err
		}
		switch {
		case v.isCancel():
			return nil, fmt.Errorf("%w: BeforeCompact canceled it", ErrCompactCanceled)
		case v.isReplace():
			c := v.replacement()
			if c == nil {
				return nil, fmt.Errorf("%w: BeforeCompact replaced the compaction with nothing", ErrInvalidCompaction)
			}
			out := *c
			out.Reason = ReasonFromHook
			s.mu.Lock()
			_, held := s.byID[out.FirstKept]
			s.mu.Unlock()
			if !held {
				return nil, fmt.Errorf("%w: BeforeCompact replacement keeps first entry %q", ErrNoEntry, out.FirstKept)
			}
			return &out, nil
		}
		// Proceed: the hook's edits are the plan. The four editable
		// fields are read back; the session's facts are restored.
		edited, err := adoptEdits(path, rangeStart, cut, pinned, prep, sameSlice(prep.Messages, rangeMsgs), sameSlice(prep.Pinned, pinnedThrough))
		if err != nil {
			return nil, err
		}
		prep = edited
		prep.Reason, prep.Context, prep.PrevSummary, prep.TokensBefore = reason, contextMsgs, prev, tokensBefore
	}
	if cfg.compactor != nil {
		c, err := safeCompactor(cfg.compactor, ctx, prep)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, fmt.Errorf("%w: the Compactor returned no Compaction", ErrInvalidCompaction)
		}
		out := *c
		if out.Reason == "" {
			out.Reason = reason
		}
		return &out, nil
	}
	sumView, files := summarizerView(prep.Messages)
	slices.Sort(files)
	files = slices.Compact(files)

	summary, err := s.produceSummary(ctx, SummaryInput{
		Messages:     sumView,
		PrevSummary:  prep.PrevSummary,
		Instructions: prep.Instructions,
		MaxTokens:    cfg.maxTokens(),
		Reason:       reason,
		SystemPrompt: cfg.summarySystemPrompt(prep.Instructions),
	})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(mustJSON(sumView))
	c := &Compaction{
		Summary:         summary.Text,
		FirstKept:       prep.FirstKept,
		TokensBefore:    tokensBefore,
		Reason:          reason,
		SummarizerUsage: summary.Usage,
		SummarizerModel: summary.Model,
		FilesRead:       files,
		Pinned:          prep.Pinned,
		RangeHash:       hex.EncodeToString(hash[:]),
	}
	if cfg.preferNative && c.SummarizerModel == (core.ModelInfo{}) {
		// A native compaction that reported no model info still names
		// the model that ran it.
		c.SummarizerModel = core.InfoOf(s.agent.Model())
	}
	return c, nil
}

// rangeMessages returns the messages of path[from:to] — the range a
// compaction summarizes — each with its own Content slice, so a
// BeforeCompact hook that edits a message in place edits its copy and
// never the tree.
func rangeMessages(path []Entry, from, to int) []core.Message {
	var out []core.Message
	for _, e := range path[from:to] {
		if m, ok := contextMessage(e); ok {
			out = append(out, cloneMessage(m))
		}
	}
	return out
}

// pinnedBelow lists the pinned ids a compaction cutting at cut keeps
// through: every pin below the cut, in the range it summarizes and in
// the older summaries' ranges alike — the walk re-includes them from
// the entry, so a pin never has to hold the cut back (ADR 0020 §4).
func pinnedBelow(path []Entry, cut int, pinned map[string]bool) []string {
	var out []string
	for _, e := range path[:cut] {
		if pinned[idOf(e)] {
			out = append(out, idOf(e))
		}
	}
	return out
}

// sameSlice reports whether a is still the slice b — same length, same
// backing array — which is how a hook's "left it alone" reads: an
// assignment of a new slice changes it, an in-place edit does not.
func sameSlice[T any](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// adoptEdits validates what a BeforeCompact hook left on the
// Preparation and returns the plan to proceed with. FirstKept may
// move: it must stay on the path, past the previous boundary, at a
// valid cut (a user or assistant message that does not split a tool
// call from its result). When it moved and the hook did not assign
// Messages (or Pinned), the range (or the pinned list) is rebuilt for
// the new boundary; an assigned Messages is summarized as given — the
// redaction hook's whole point. Assigned Pinned ids must be entries on
// the path.
func adoptEdits(path []Entry, rangeStart, cut int, pinned map[string]bool, prep Preparation, msgsUntouched, pinnedUntouched bool) (Preparation, error) {
	if prep.FirstKept != idOf(path[cut]) {
		idx := indexOfID(path, prep.FirstKept)
		switch {
		case idx < 0:
			return prep, fmt.Errorf("%w: BeforeCompact moved FirstKept to %q, which is not on the leaf's path", ErrNoEntry, prep.FirstKept)
		case idx < rangeStart:
			return prep, fmt.Errorf("%w: BeforeCompact moved FirstKept to %q, before the previous compaction's kept boundary", ErrInvalidCompaction, prep.FirstKept)
		case idx == rangeStart:
			return prep, fmt.Errorf("%w: BeforeCompact moved FirstKept to %q, which leaves nothing to summarize", ErrNothingToCompact, prep.FirstKept)
		case !validCut(path, idx):
			return prep, fmt.Errorf("%w: BeforeCompact moved FirstKept to %q, which is not a user or assistant message or splits a tool call from its result", ErrInvalidCompaction, prep.FirstKept)
		}
		if msgsUntouched {
			prep.Messages = rangeMessages(path, rangeStart, idx)
		}
		if pinnedUntouched {
			prep.Pinned = pinnedBelow(path, idx, pinned)
		}
	}
	if !pinnedUntouched {
		for _, id := range prep.Pinned {
			if indexOfID(path, id) < 0 {
				return prep, fmt.Errorf("%w: BeforeCompact pinned %q, which is not on the leaf's path", ErrNoEntry, id)
			}
		}
	}
	return prep, nil
}

// contextMessage returns the message an entry contributes to the
// context — message, custom_message, and branch summaries in their
// marked shape.
func contextMessage(e Entry) (core.Message, bool) {
	switch e := e.(type) {
	case MessageEntry:
		return e.Message, true
	case CustomMessageEntry:
		return e.Message, true
	case BranchSummaryEntry:
		return summaryMessage(e.Summary), true
	}
	return core.Message{}, false
}

// indexOfID returns the position of the entry with the given id on the
// path, or -1 when the path does not reach it.
func indexOfID(path []Entry, id string) int {
	for i, e := range path {
		if idOf(e) == id {
			return i
		}
	}
	return -1
}

// reportFailed tells the CompactFailed hook that a compaction which
// was to be written was not — unless err is one of the refusals that
// attempt nothing (nothing to compact, canceled by the hook, busy,
// awaiting approval). Callers must not hold s.mu.
func (s *Session) reportFailed(ctx context.Context, reason Reason, err error) {
	if errors.Is(err, ErrNothingToCompact) || errors.Is(err, ErrCompactCanceled) ||
		errors.Is(err, ErrBusy) || errors.Is(err, ErrAwaitingApproval) {
		return
	}
	s.compactFailed(ctx, reason, err)
}

// compactFailed runs the CompactFailed hook when one is set — the
// session is unchanged, and the caller is told why. A panicking hook
// is contained: the failure it reports is already the story. Callers
// must not hold s.mu.
func (s *Session) compactFailed(ctx context.Context, reason Reason, err error) {
	fn := s.cfg.compaction.failed
	if fn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.agent.Logger().Error("thread: CompactFailed panicked", "panic", r)
		}
	}()
	fn(ctx, reason, err)
}

// produceSummary runs the summary chain: a custom Summarizer if one is
// set, else the models — SummaryModel first when configured, the
// session's own after it. Each source gets one retry when its summary
// is rejected — by CheckSummary, or as ErrSummaryTruncated when the
// model stopped at the output cap — before the chain moves on; a
// source that errors outright is not retried. A summary with no text
// is a failure. Exhausted, the chain returns its last error wrapped
// (errors.Is finds ErrSummaryTruncated when that was it) and nothing
// is stored: a cut or rejected summary never becomes the context (ADR
// 0020 §4).
func (s *Session) produceSummary(ctx context.Context, in SummaryInput) (Summary, error) {
	cfg := s.cfg.compaction
	var sources []func() (Summary, error)
	if fn := cfg.summarizer; fn != nil {
		sources = append(sources, func() (Summary, error) { return safeSummarize(fn, ctx, in) })
	} else {
		if cfg.summaryModel != nil {
			m := cfg.summaryModel
			sources = append(sources, func() (Summary, error) { return s.summarizeWith(ctx, m, in) })
		}
		sources = append(sources, func() (Summary, error) { return s.summarizeWith(ctx, s.agent.Model(), in) })
	}
	var lastErr error
	for _, run := range sources {
		for attempt := 0; attempt < 2; attempt++ {
			sum, err := run()
			rejected := false
			if err == nil && strings.TrimSpace(sum.Text) == "" {
				err = errors.New("thread: summarizer returned no text")
			}
			if err == nil && cfg.check != nil {
				if err = safeCheck(cfg.check, sum); err != nil {
					rejected = true
				}
			}
			if err == nil {
				return sum, nil
			}
			lastErr = err
			if !rejected && !errors.Is(err, ErrSummaryTruncated) {
				break // the source itself failed: the chain falls back
			}
		}
	}
	return Summary{}, fmt.Errorf("thread: every summarizer failed, last error: %w", lastErr)
}

// summarizeWith makes one model produce the summary: the provider's
// own compaction when PreferNative is set and the model (or something
// it wraps) offers it, else a single text step under the system prompt
// with the output cap. No cache hints are sent, so no prompt-cache
// writes happen on the summary call. A max_tokens finish is
// ErrSummaryTruncated — the text that fit under the cap is half a
// summary, and storing it would silently lose the other half.
func (s *Session) summarizeWith(ctx context.Context, m core.Model, in SummaryInput) (Summary, error) {
	input := in.Messages
	if in.PrevSummary != "" {
		input = append([]core.Message{summaryMessage(in.PrevSummary)}, in.Messages...)
	}
	if s.cfg.compaction.preferNative {
		if nc := nativeOf(m); nc != nil {
			msg, usage, err := nc.CompactNative(ctx, core.ModelRequest{
				System:   in.SystemPrompt,
				Messages: input,
			}, in.Instructions)
			if err == nil && strings.TrimSpace(msg.Text()) != "" {
				return Summary{Text: strings.TrimSpace(msg.Text()), Usage: usage, Model: core.InfoOf(m)}, nil
			}
			// An error or a text-less answer: the text summary is the
			// fallback (ADR 0020 §7) — said out loud, never swallowed.
			if err == nil {
				err = errors.New("the provider returned no text")
			}
			s.agent.Logger().Warn("thread: provider-native compaction failed; falling back to the text summary",
				"session", s.header.ID, "err", err)
		}
	}
	maxTokens := int(in.MaxTokens)
	req := core.ModelRequest{
		System:   in.SystemPrompt,
		Messages: input,
		Params:   core.RequestParams{MaxTokens: &maxTokens},
	}
	// The stream contract, enforced the way the loop enforces it:
	// exactly one ModelFinish, nothing after it (ErrModelContract) —
	// a summarizer stream that ends without a finish is an error, not
	// whatever text happened to arrive.
	var sb strings.Builder
	var usage core.Usage
	var stop core.StopReason
	finished := false
	for ev, err := range m.Stream(ctx, req) {
		if err != nil {
			return Summary{}, fmt.Errorf("thread: summarizer: %w", err)
		}
		if finished {
			return Summary{}, fmt.Errorf("%w: the summarizer stream continued after ModelFinish", core.ErrModelContract)
		}
		switch ev := ev.(type) {
		case core.ModelTextDelta:
			sb.WriteString(ev.Text)
		case core.ModelFinish:
			usage, stop, finished = ev.Usage, ev.Reason, true
		}
	}
	if !finished {
		return Summary{}, fmt.Errorf("%w: the summarizer stream ended without ModelFinish", core.ErrModelContract)
	}
	if stop == core.StopMaxTokens {
		return Summary{}, fmt.Errorf("%w: the summarizer stopped at %d output tokens (raise SummaryMaxTokens or Reserve)", ErrSummaryTruncated, maxTokens)
	}
	summary := strings.TrimSpace(sb.String())
	if summary == "" {
		return Summary{}, errors.New("thread: summarizer returned no text")
	}
	return Summary{Text: summary, Usage: usage, Model: core.InfoOf(m)}, nil
}

// cutIndex finds where the path cuts: the earliest boundary such that
// the entries kept after it fit within about keepRecent estimated
// tokens, moved forward to a valid cut position. A valid position is
// between two entries where the kept side starts at a user or
// assistant message, and the summarized side does not end on an
// assistant message with tool calls — never between a call and its
// result (ADR 0020 §2). A single turn larger than keepRecent is split
// at an assistant message inside it. -1 means nothing to compact.
// Pinned entries do not constrain the cut: the walk re-includes them
// from the compaction entry's Pinned list, so a pin near the root
// cannot hold the whole context raw forever.
func cutIndex(path []Entry, keepRecent int64) int {
	return cutIndexWeighted(path, keepRecent, defaultEntryWeight)
}

// defaultEntryWeight is the default per-entry weight for the walk.
func defaultEntryWeight(e Entry) int64 { return estimateEntry(e) }

// cutIndexWeighted is cutIndex with a replaceable per-entry weight —
// the session's Estimator when one is configured.
func cutIndexWeighted(path []Entry, keepRecent int64, weight func(Entry) int64) int {
	// Walk back from the leaf accumulating the estimated tail.
	suffix := int64(0)
	cut := -1
	for i := len(path) - 1; i >= 0; i-- {
		suffix += weight(path[i])
		if suffix > keepRecent {
			cut = i + 1 // entries[i+1:] fit; entries[i] starts the overflow
			break
		}
	}
	if cut <= 0 {
		return -1 // the whole path fits inside the keep window
	}
	// Move forward to the next valid boundary: keeping more is always
	// safe, and every path starts with a user- or assistant-role
	// message (the session's first write is a prompt or a custom
	// message — a custom message is a user message unless its caller
	// made it something else, and the walk then skips to the next
	// real boundary). Pinned entries do not hold the cut back: the
	// walk re-includes them from the compaction entry's Pinned list,
	// so a pin near the root cannot keep the whole context raw.
	for cut < len(path) && !validCut(path, cut) {
		cut++
	}
	if cut < len(path) {
		return cut
	}
	// No forward-valid boundary — the extremes: a tail whose entries
	// each overflow the keep window (a turn larger than KeepRecent),
	// or bookkeeping entries at the very tip. Keep the smallest tail
	// there is: the latest boundary that leaves whole messages, split
	// at an assistant boundary where there is one (pi's split turn).
	// An estimate cannot refuse to compact a context the window
	// cannot hold.
	for c := len(path) - 1; c >= 1; c-- {
		if validCut(path, c) {
			return c
		}
	}
	return -1
}

// validCut reports whether path[cut] may be the first kept entry: a
// user or assistant message, whose previous entry is not an assistant
// message carrying tool calls (that would split a call from its
// results).
func validCut(path []Entry, cut int) bool {
	first := path[cut]
	m, ok := first.(MessageEntry)
	if !ok {
		return false
	}
	if m.Message.Role != core.RoleUser && m.Message.Role != core.RoleAssistant {
		return false
	}
	if cut > 0 {
		if prev, ok := path[cut-1].(MessageEntry); ok && prev.Message.Role == core.RoleAssistant {
			for _, p := range prev.Message.Content {
				if _, isCall := p.(core.ToolCallPart); isCall {
					return false
				}
			}
		}
	}
	return true
}

// summarizerView renders messages the way the summarizer sees them
// (ADR 0020 §2): tool results truncated to the character cap on a
// rune boundary with a visible marker, reasoning parts carrying a
// signature dropped whole (they cannot be resent and their text is
// the provider's internal scratch), and file parts reduced to their
// names — the URL, or the media type and size for inline data. It
// returns the view and the file URLs it saw, unsorted.
func summarizerView(msgs []core.Message) ([]core.Message, []string) {
	var files []string
	out := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		vm := core.Message{Role: m.Role}
		for _, p := range m.Content {
			switch p := p.(type) {
			case core.ToolResultPart:
				if len([]rune(p.Content)) > defaultToolResultCap {
					r := []rune(p.Content)
					p.Content = string(r[:defaultToolResultCap]) + "…[truncated]"
				}
				vm.Content = append(vm.Content, p)
			case core.ReasoningPart:
				if p.Signature != "" {
					continue // signed reasoning is dropped
				}
				vm.Content = append(vm.Content, p)
			case core.FilePart:
				name := p.URL
				if name == "" {
					name = fmt.Sprintf("(%s, %d bytes)", p.MediaType, len(p.Data))
				} else {
					files = append(files, name)
				}
				vm.Content = append(vm.Content, core.TextPart{Text: "[file: " + name + "]"})
			default:
				vm.Content = append(vm.Content, p)
			}
		}
		if len(vm.Content) > 0 || m.Role == core.RoleUser {
			out = append(out, vm)
		}
	}
	return out, files
}

// estimate is the session's token estimate for one message: the
// configured Estimator when one is set, the default quarter-of-wire-
// bytes otherwise. The Estimator is a caller's hook: callers must not
// hold s.mu.
func (s *Session) estimate(m core.Message) int64 {
	if est := s.cfg.compaction.estimator; est != nil {
		return est.Estimate([]core.Message{m})
	}
	return estimateMessage(m)
}

// estimateAll is estimate over a batch — one Estimator call for the
// lot, so a provider-aware estimator can count them together. Callers
// must not hold s.mu.
func (s *Session) estimateAll(msgs []core.Message) int64 {
	if len(msgs) == 0 {
		return 0
	}
	if est := s.cfg.compaction.estimator; est != nil {
		return est.Estimate(msgs)
	}
	var n int64
	for _, m := range msgs {
		n += estimateMessage(m)
	}
	return n
}

// entryWeight is the cut walk's per-entry weight under the session's
// estimator: what the entry costs in the window — its message, or the
// summary a compaction or branch summary shows for it. Trim records
// and the bookkeeping kinds weigh nothing. Callers must not hold s.mu.
func (s *Session) entryWeight(e Entry) int64 {
	if c, ok := e.(CompactionEntry); ok {
		if c.isTrim() {
			return 0
		}
		return s.estimate(summaryMessage(c.Summary))
	}
	if m, ok := contextMessage(e); ok {
		return s.estimate(m)
	}
	return 0
}

// estimateMessage is the default token estimate for one message: a
// quarter of its wire bytes, rounded up. It estimates the DELTA the
// trigger adds to provider-reported input — the signal itself is never
// an estimate (ADR 0020 §2's rule); WithEstimator replaces it.
func estimateMessage(m core.Message) int64 {
	return (int64(len(mustJSON(m))) + 3) / 4
}

// estimateEntry estimates the context weight of one path entry with
// the default estimate: message and custom_message entries carry their
// message; a branch summary and a summary compaction carry the summary
// the context shows for them; a trim record and the bookkeeping kinds
// weigh nothing (they never reach the context). The summaries weigh
// what they cost in the window — a cut that ignored them would keep
// more than KeepRecent really allows.
func estimateEntry(e Entry) int64 {
	switch e := e.(type) {
	case MessageEntry:
		return estimateMessage(e.Message)
	case CustomMessageEntry:
		return estimateMessage(e.Message)
	case BranchSummaryEntry:
		return estimateMessage(summaryMessage(e.Summary))
	case CompactionEntry:
		if e.isTrim() {
			return 0
		}
		return estimateMessage(summaryMessage(e.Summary))
	}
	return 0
}

// mustJSON encodes v or panics — only values this package constructed,
// which always encode (the storage contract enforces it on every
// write).
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("thread: cannot encode %T: %v", v, err))
	}
	return b
}

// maybeAutoCompact is the trigger (ADR 0020 §2), run from the session's
// single runner before a turn starts — after the prompt is on the
// path, so the estimated delta covers what the run is about to be fed
// — and after each turn ends: compact when the provider-reported input
// of the last model step plus the estimated messages added since
// crosses window − Reserve. The reported input is the signal, never a
// chars-per-token guess at the whole context; only the delta is
// estimated. With no window known there is no automatic compaction,
// and one warning says so through the agent's logger.
//
// The trigger stands down whenever the report no longer describes the
// path: no step was ever measured, the measured turn is off the leaf's
// path (a Branch moved the line), or a compaction entry landed after
// it — the report predates the compaction, and firing on it again
// would compact a context that just shrank. The next turn's report
// re-arms it.
func (s *Session) maybeAutoCompact(ctx context.Context) {
	cfg := s.cfg.compaction
	if cfg.disabled {
		return // manual only, and quietly: the session asked for it
	}
	if cfg.window <= 0 {
		s.mu.Lock()
		warned := s.warnedNoWindow
		s.warnedNoWindow = true
		s.mu.Unlock()
		if !warned {
			s.agent.Logger().Warn("thread: no context window known for this session; automatic compaction stays off until one is configured (manual Compact always works)")
		}
		return
	}
	s.mu.Lock()
	if s.boundaryLocked() {
		// An open approval boundary holds the tail raw: its dangling
		// calls are what the resume's decisions resolve, and a
		// compaction that summarized them would orphan every decision
		// (ADR 0021 §1's raw-transcript rule). The trigger re-arms on
		// the turn that resolves the boundary.
		s.mu.Unlock()
		return
	}
	lastInput := s.lastInput
	if s.lastMeasureLeaf == "" {
		// No model step has ever been measured — there is no reported
		// number, and estimating the whole context would make the
		// trigger a chars-per-token guess, the one signal ADR 0020
		// forbids. The first turn's report becomes the signal.
		s.mu.Unlock()
		return
	}
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		s.mu.Unlock()
		return
	}
	var since []core.Message
	counting, stale := false, false
	for _, e := range path {
		if !counting {
			if idOf(e) == s.lastMeasureLeaf {
				counting = true // the messages after the measured turn are the delta
			}
			continue
		}
		if _, ok := e.(CompactionEntry); ok {
			// A compaction or a trim landed after the measurement: the
			// reported number describes the context before it.
			stale = true
			break
		}
		if m, ok := contextMessage(e); ok {
			since = append(since, m) // branch summaries count: the context carries them
		}
	}
	s.mu.Unlock()
	if !counting || stale {
		// The mark is off the leaf's path — a Branch or an Uncompact
		// moved the line since the measurement — or a compaction
		// rewrote the context under it. There is no honest delta
		// against a report that no longer describes this context, so
		// the trigger stands down; the next turn's report becomes the
		// signal again (the same rule as a never-measured session).
		return
	}

	in := TriggerInput{LastInput: lastInput, Estimated: s.estimateAll(since), Window: cfg.window, Reserve: cfg.reserve}
	if cfg.trigger != nil {
		if !s.safeTrigger(in) {
			return
		}
	} else if !firesAt(in) {
		return
	}

	// Rate limits first: a context that re-crosses the line every turn
	// must not recompact every turn.
	if !s.rateLimitAllows() {
		return
	}

	// The trimmer pre-pass (ADR 0020 §4): a cheap stub of the old tool
	// results may bring the context back under the line, and then no
	// summary is made — a lighter trim record lands in the compaction
	// entry instead.
	if cfg.trimmer != nil && s.tryTrim(ctx) {
		return
	}

	if err := s.applyAuto(ctx); err != nil {
		// A failed compaction leaves the session unchanged — the next
		// trigger tries again; the caller's turn still runs.
		switch {
		case errors.Is(err, ErrNothingToCompact):
			// The trigger fired and there was nothing to cut: the
			// context is over the line and compaction cannot help — the
			// first time is worth a line, a line per turn is not.
			s.mu.Lock()
			warned := s.warnedNothing
			s.warnedNothing = true
			s.mu.Unlock()
			if !warned {
				s.agent.Logger().Warn("thread: the compaction trigger fired but there is nothing to compact; the context stays over window − Reserve until the tail outgrows KeepRecent",
					"session", s.header.ID, "last_input", in.LastInput, "estimated", in.Estimated,
					"window", in.Window, "reserve", in.Reserve, "keep_recent", cfg.keepRecent, "err", err)
			}
		case errors.Is(err, ErrCompactCanceled):
			// The hook's decision; nothing to say.
		default:
			s.agent.Logger().Warn("thread: automatic compaction failed", "session", s.header.ID, "err", err)
		}
	}
}

// firesAt is the default trigger condition (ADR 0020 §2): the
// measured context plus the estimated delta crosses window minus
// Reserve. Measured plus estimated — never an estimate standing in
// for the measurement.
func firesAt(in TriggerInput) bool {
	return in.LastInput+in.Estimated > in.Window-in.Reserve
}

// safeTrigger consults the trigger function, containing a panic as a
// no-fire with a log line — the runner must survive its caller's hook.
// Callers must not hold s.mu.
func (s *Session) safeTrigger(in TriggerInput) (fire bool) {
	fn := s.cfg.compaction.trigger
	defer func() {
		if r := recover(); r != nil {
			fire = false
			s.agent.Logger().Error("thread: TriggerFunc panicked", "panic", r)
		}
	}()
	return fn(in)
}

// safeTrim runs the trimmer, containing a panic as an error. A nil
// result is an error too: a trimmer that returns nothing has not
// trimmed the context to nothing. Callers must not hold s.mu.
func (s *Session) safeTrim(ctx context.Context, before []core.Message) (trimmed []core.Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			trimmed, err = nil, fmt.Errorf("thread: the Trimmer panicked: %v", r)
		}
	}()
	trimmed, err = s.cfg.compaction.trimmer.Trim(ctx, before)
	if err != nil {
		return nil, err
	}
	if trimmed == nil {
		return nil, errors.New("thread: the Trimmer returned no messages")
	}
	return trimmed, nil
}

// tryTrim runs the trimmer pre-pass and reports whether it settled
// this trigger fire: the trimmer is shown the model's context, its
// output is diffed against that input into a trim record, and when the
// trimmed context fits under window − Reserve the record lands as a
// compaction entry with Reason trim. False means the summary
// compaction should run: the trimmer failed, changed nothing, made a
// change the record cannot represent (logged and reported through
// CompactFailed), or did not trim enough.
func (s *Session) tryTrim(ctx context.Context) bool {
	cfg := s.cfg.compaction
	s.mu.Lock()
	view, err := s.contextViewLocked()
	s.mu.Unlock()
	if err != nil || len(view.msgs) == 0 {
		return false
	}
	before := make([]core.Message, len(view.msgs))
	input := make([]core.Message, len(view.msgs))
	for i, vm := range view.msgs {
		before[i] = vm.msg
		input[i] = cloneMessage(vm.msg) // the trimmer's own copy: an in-place edit must not reach the tree
	}
	trimmed, err := s.safeTrim(ctx, input)
	if err != nil {
		s.agent.Logger().Warn("thread: the trimmer failed; summarizing instead", "session", s.header.ID, "err", err)
		return false
	}
	rec, err := deriveTrim(view.msgs, trimmed)
	if err != nil {
		s.agent.Logger().Warn("thread: the trim cannot be recorded; summarizing instead", "session", s.header.ID, "err", err)
		s.compactFailed(ctx, ReasonTrim, err)
		return false
	}
	if len(rec.Stubs) == 0 {
		return false // nothing left to trim: only a summary can shrink this context
	}
	if s.estimateAll(trimmed) > cfg.window-cfg.reserve {
		return false // the trim alone is not enough
	}
	c := &Compaction{
		// A trim drops nothing, so it keeps the boundary in force: the
		// governing compaction's, or the root when none governs.
		FirstKept:    idOf(view.path[view.start]),
		TokensBefore: s.estimateAll(before),
		Reason:       ReasonTrim,
		Trim:         rec,
		runner:       true,
	}
	if err := s.ApplyCompaction(ctx, c); err != nil {
		s.agent.Logger().Warn("thread: trim record not written", "session", s.header.ID, "err", err)
	}
	return true
}

// deriveTrim turns a trimmer's output into the trim record the entry
// persists: the diff of the trimmed context against the view the
// trimmer was shown. The one representable change is a tool result's
// Content (and IsError) replaced in place; a message added, dropped or
// reordered, a part other than a tool result changed, or a result's
// CallID or Name changed is an error wrapping ErrInvalidCompaction — a
// change the walk could not replay must not be silently lost. An
// unchanged context yields an empty record.
func deriveTrim(view []viewMsg, trimmed []core.Message) (*TrimRecord, error) {
	if len(trimmed) != len(view) {
		return nil, fmt.Errorf("%w: the Trimmer returned %d messages for %d; a trim may only replace tool-result content", ErrInvalidCompaction, len(trimmed), len(view))
	}
	rec := &TrimRecord{}
	for i, vm := range view {
		in, out := vm.msg, trimmed[i]
		if in.Role != out.Role || len(in.Content) != len(out.Content) {
			return nil, fmt.Errorf("%w: the Trimmer changed the shape of message %d; a trim may only replace tool-result content", ErrInvalidCompaction, i)
		}
		for j := range in.Content {
			if samePart(in.Content[j], out.Content[j]) {
				continue
			}
			ir, inOK := in.Content[j].(core.ToolResultPart)
			or, outOK := out.Content[j].(core.ToolResultPart)
			if !inOK || !outOK || ir.CallID != or.CallID || ir.Name != or.Name || vm.entry == "" {
				return nil, fmt.Errorf("%w: the Trimmer changed part %d of message %d, which is not a tool result's content", ErrInvalidCompaction, j, i)
			}
			rec.Stubs = append(rec.Stubs, TrimStub{Entry: vm.entry, CallID: or.CallID, Content: or.Content, IsError: or.IsError})
		}
	}
	return rec, nil
}

// samePart reports whether two message parts are the same on the wire
// — the comparison that matters, since the wire is what the model
// sees.
func samePart(a, b core.Part) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// applyAuto is the trigger's Compact: compute and apply with the
// threshold reason — a manual Compact records itself as manual.
func (s *Session) applyAuto(ctx context.Context) error {
	c, err := s.computeCompaction(ctx, ReasonThreshold, "")
	if err != nil {
		return err
	}
	return s.ApplyCompaction(ctx, c)
}

// rateLimitAllows reports whether the rate limits (ADR 0020 §4) let an
// automatic compaction run: MinTurnsBetween turns since the last
// compaction entry, and fewer than MaxPerSession automatic ones — both
// counted along the leaf's path, in path order. Another branch's
// compactions and turns are not this line's: counting the whole file
// would let an abandoned branch starve the live one. Manual Compact
// never asks.
func (s *Session) rateLimitAllows() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return false
	}
	automatic, turnsSince := 0, 1<<30
	for _, e := range path {
		switch e := e.(type) {
		case CompactionEntry:
			if e.Reason != ReasonManual {
				automatic++
			}
			turnsSince = 0
		case TurnEntry:
			turnsSince++
		}
	}
	cfg := s.cfg.compaction
	if cfg.maxPerSession > 0 && automatic >= cfg.maxPerSession {
		return false
	}
	if cfg.minTurnsBetween > 0 && turnsSince < cfg.minTurnsBetween {
		return false
	}
	return true
}

// summarizeBranch summarizes the branch a SummarizeLeft Branch leaves
// behind — what the model was shown of it, from the divergence up to
// the current leaf — with the same skeleton, marker and model chain as
// compaction (ADR 0020 §6): one summary, cache prefix shared with
// nothing, the cost documented rather than hidden. The divergence is
// the common ancestor of the current leaf and the branch target: a
// target on another branch summarizes everything this branch grew
// since the two parted, and the returned from-entry names that
// ancestor ("" when the branch being left is the whole session, grown
// from the root).
//
// The input is the branch's part of the compacted view, never the raw
// path: when the branch compacted itself, its summary stands in for
// the range it replaced (that summary is the only record the context
// kept of it) followed by the entries from its first kept entry on —
// feeding the raw range beside its own summary would double the
// history and could alone outgrow the window. A failure is the
// Branch's error; it is not a compaction and does not reach
// CompactFailed.
func (s *Session) summarizeBranch(ctx context.Context, target string) (summary, fromEntry string, err error) {
	s.mu.Lock()
	view, err := s.contextViewLocked()
	if err != nil {
		s.mu.Unlock()
		return "", "", err
	}
	targetPath, err := s.pathLocked(target)
	if err != nil {
		s.mu.Unlock()
		return "", "", err
	}
	s.mu.Unlock()
	leafPath := view.path
	common := 0
	for common < len(leafPath) && common < len(targetPath) &&
		idOf(leafPath[common]) == idOf(targetPath[common]) {
		common++
	}
	fromEntry = ""
	if common > 0 {
		fromEntry = idOf(leafPath[common-1])
	}
	var sumMsgs []core.Message
	for _, vm := range view.msgs {
		if vm.entry == "" {
			// The governing compaction's summary: the branch's own when
			// both the compaction and the range it replaced sit past
			// the divergence.
			if view.governor >= common && view.start >= common {
				sumMsgs = append(sumMsgs, vm.msg)
			}
			continue
		}
		if vm.idx >= common {
			sumMsgs = append(sumMsgs, vm.msg)
		}
	}
	if len(sumMsgs) == 0 {
		return "", "", fmt.Errorf("thread: SummarizeLeft with no branch to summarize: the leaf is on %q's path already", target)
	}
	sumView, _ := summarizerView(sumMsgs)
	cfg := s.cfg.compaction
	sum, err := s.produceSummary(ctx, SummaryInput{
		Messages:     sumView,
		MaxTokens:    cfg.maxTokens(),
		Reason:       ReasonManual,
		SystemPrompt: cfg.summarySystemPrompt(""),
	})
	return sum.Text, fromEntry, err
}
