package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/weftgo/weft"
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
func summaryMessage(text string) weft.Message {
	return weft.User(summaryMarkerOpen + "\n" + text + "\n" + summaryMarkerClose)
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

// Compaction is one computed compaction: the plan PreviewCompaction
// returns, ApplyCompaction writes as a compaction entry, and Compact
// does both. It carries everything the entry records (ADR 0020 §1) —
// nothing else: applying writes exactly these fields, nothing is
// recomputed.
type Compaction struct {
	// Summary is the summarizer's text, shown in the context behind
	// the fixed marker from the entry's first kept entry onward.
	Summary string
	// FirstKept is the id of the first entry the context still shows
	// raw after this compaction.
	FirstKept string
	// TokensBefore is the estimated size of the whole context at
	// compaction time — the number the compaction was judged against.
	TokensBefore int64
	// Reason is why it ran: manual here, threshold for the trigger,
	// from_hook and trim in step 1.9, overflow in v0.3.
	Reason Reason
	// SummarizerUsage and SummarizerModel record what the summary
	// cost and which model made it — the cost ledger's inputs.
	SummarizerUsage weft.Usage
	SummarizerModel weft.ModelInfo
	// FilesRead lists the file URLs the summarized range carried,
	// sorted and deduplicated; FilesModified fills in when the sandbox
	// write log lands (v0.6, ADR 0023) — the lists are the summary's
	// memory across iterative compactions.
	FilesRead     []string
	FilesModified []string
	// Pinned lists the pinned entry ids this compaction kept raw; the
	// Pin call arrives in step 1.9 and the list is its record.
	Pinned []string
	// RangeHash is the SHA-256 of the serialized range the summarizer
	// was fed — the audit that a summary summarizes exactly this.
	RangeHash string
}

// compactConfig is the compaction configuration a session resolves —
// the defaults with, from step 1.9, the public layers' overrides. In
// step 1.8 no public option sets these: the window is unknown, so the
// automatic trigger stays off (one warning, ADR 0020 §2) and manual
// compaction works with the default cut and cap.
type compactConfig struct {
	window     int64 // 0 = unknown: no automatic compaction
	reserve    int64
	keepRecent int64
	// The five layers (ADR 0020 §3), all optional; the zero values
	// mean "not set" and the defaults above carry the algorithm.
	modelWindows     map[weft.ModelInfo]int64
	modelReserves    map[weft.ModelInfo]int64
	trigger          func(TriggerInput) bool
	minTurnsBetween  int
	maxPerSession    int
	estimator        Estimator
	disabled         bool
	summaryModel     weft.Model
	summaryPrompt    string
	summaryFocus     string
	summaryMaxTokens int
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

// resolve folds the per-model overrides for the session's own model —
// Create and Open call it once, after the options and with the agent
// known; the effective window and reserve land in the plain fields.
func (c *compactConfig) resolve(m weft.Model) {
	if n, ok := c.modelWindows[weft.InfoOf(m)]; ok {
		c.window = n
	}
	if n, ok := c.modelReserves[weft.InfoOf(m)]; ok {
		c.reserve = n
	}
}

// PreviewCompaction computes the session's next compaction without
// writing anything: the cut, the summarized range, the summary text,
// and the entry fields a Compact would record. It runs the summarizer
// — the session's own model (ADR 0020 §2) — with the skeleton prompt,
// the previous summary (iterative compaction) as the range's first
// message, prompt-cache writes left off (the request carries no cache
// hints) and the output capped at 0.8 × Reserve. A session whose tail
// fits inside KeepRecent has nothing to compact and gets an error, not
// a no-op entry.
func (s *Session) PreviewCompaction(ctx context.Context, opts ...CompactOption) (*Compaction, error) {
	return s.computeCompaction(ctx, ReasonManual, resolveCompact(opts...).instructions)
}

// Compact computes and applies the session's next compaction in one
// call: PreviewCompaction, then ApplyCompaction. Nothing is deleted —
// the summarized entries stay in the file, and the context at the leaf
// becomes the summary, then the entries from the compaction's first
// kept entry onward.
func (s *Session) Compact(ctx context.Context, opts ...CompactOption) error {
	c, err := s.PreviewCompaction(ctx, opts...)
	if err != nil {
		return err
	}
	return s.ApplyCompaction(ctx, c)
}

// ApplyCompaction writes a computed Compaction as the session's next
// compaction entry, appended at the leaf. c must name a FirstKept on
// the leaf's path — a value the walk could never reach is an error,
// not a fallback — and one at or after the previous compaction's kept
// boundary: an iterative compaction never summarizes what a summary
// already replaced (ADR 0020 §1). A summary-less compaction must be a
// trim. Anything else is an error, and nothing is written. The entry
// lands whatever else happened between its computation and this call —
// the tree only grew, so the kept range stays correct.
func (s *Session) ApplyCompaction(ctx context.Context, c *Compaction) error {
	if c == nil {
		return fmt.Errorf("thread: ApplyCompaction with no Compaction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.boundaryLocked() {
		// The same rule the trigger follows: a parked tail must stay
		// raw for its decisions to resolve, so a manual compaction
		// waits too. Resolve or branch away from the boundary first.
		return fmt.Errorf("thread: session %s has approval requests pending; compact after they resolve", s.header.ID)
	}
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return err
	}
	keptIdx := indexOfID(path, c.FirstKept)
	if keptIdx < 0 {
		return fmt.Errorf("thread: compaction keeps first entry %q, which is not on session %s's leaf path", c.FirstKept, s.header.ID)
	}
	for i := len(path) - 1; i >= 0; i-- {
		if prev, ok := path[i].(CompactionEntry); ok {
			if prevIdx := indexOfID(path, prev.FirstKept); prevIdx >= 0 && keptIdx < prevIdx {
				return fmt.Errorf("thread: compaction keeps first entry %q, before the previous compaction's kept boundary %q (ADR 0020 §1)", c.FirstKept, prev.FirstKept)
			}
			break
		}
	}
	if c.Summary == "" && c.Reason != ReasonTrim {
		return fmt.Errorf("thread: a compaction without a summary must be a trim (ADR 0020 §1)")
	}
	e := CompactionEntry{
		ID:              s.mintIDLocked(),
		ParentID:        s.leaf,
		Created:         time.Now().UTC(),
		Summary:         c.Summary,
		FirstKept:       c.FirstKept,
		TokensBefore:    c.TokensBefore,
		Reason:          c.Reason,
		SummarizerUsage: c.SummarizerUsage,
		SummarizerModel: c.SummarizerModel,
		FilesRead:       slices.Clone(c.FilesRead),
		FilesModified:   slices.Clone(c.FilesModified),
		Pinned:          slices.Clone(c.Pinned),
		RangeHash:       c.RangeHash,
	}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	if f, ok := s.st.(Flusher); ok {
		if err := f.Flush(ctx, s.header.ID); err != nil {
			s.agent.Logger().Warn("thread: compaction flush failed", "session", s.header.ID, "err", err)
		}
	}
	s.agent.Logger().Info("thread: compacted",
		"session", s.header.ID, "reason", string(c.Reason),
		"tokens_before", c.TokensBefore, "first_kept", c.FirstKept)
	s.safeAfter(ctx, e) // the durable record, not the plan
	return nil
}

// The hook wrappers: a panicking hook is contained — the deciding
// hooks (before, compactor, summarizer, check) turn the panic into
// the compaction's error, and the observing hooks (after, failed)
// log it through the agent's logger and move on. A hook must not
// take the turn machinery with it (the review's containment row).
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

// Uncompact undoes the latest compaction on the leaf's path the only
// way the format allows: a branch back to the entry before the
// compaction entry (ADR 0020 §1) — a leaf entry, no rewrite, nothing
// deleted. The context is exactly what it was before the compaction
// ran; the compaction and everything after it stay in the file, off
// the path.
func (s *Session) Uncompact(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return err
	}
	for i := len(path) - 1; i >= 0; i-- {
		if _, ok := path[i].(CompactionEntry); ok {
			target := parentOf(path[i])
			if target == "" {
				return fmt.Errorf("thread: session %s's compaction has no entry before it to branch back to", s.header.ID)
			}
			if _, ok := s.byID[target]; !ok {
				return fmt.Errorf("thread: session %s holds no entry %q to branch back to", s.header.ID, target)
			}
			return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
				return LeafEntry{ID: id, ParentID: parent, Created: created, Entry: target}
			})
		}
	}
	return fmt.Errorf("thread: session %s has no compaction on its leaf's path to undo", s.header.ID)
}

// computeCompaction is the algorithm: snapshot the leaf's path, cut
// it, build the Preparation, let the BeforeCompact hook decide, then
// summarize — through the configured layers: a custom Compactor
// replaces the whole thing, a custom Summarizer just the text, and the
// default runs the model chain (SummaryModel → session model) with the
// CheckSummary retry and the PreferNative seam. The model calls run
// without the session lock — they take seconds — and the tree only
// grows meanwhile, so the snapshot's cut stays valid to apply.
func (s *Session) computeCompaction(ctx context.Context, reason Reason, instructions string) (*Compaction, error) {
	s.mu.Lock()
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	cfg := s.cfg.compaction
	prev := ""
	rangeStart := 0
	for i := len(path) - 1; i >= 0; i-- {
		if c, ok := path[i].(CompactionEntry); ok {
			prev = c.Summary // iterative: the previous summary feeds the next
			// …and the previous kept boundary is where the range to
			// summarize starts: entries below it exist only inside the
			// previous summary, and re-serializing them would feed the
			// summarizer the whole history every time — an input that
			// itself outgrows the window (ADR 0020 §1).
			if j := indexOfID(path, c.FirstKept); j >= 0 {
				rangeStart = j
			}
			break
		}
	}
	pinned := s.pinnedIDsLocked()
	s.mu.Unlock()

	weight := defaultEntryWeight
	if cfg.estimator != nil {
		weight = func(e Entry) int64 {
			if m, ok := contextMessage(e); ok {
				return int64(cfg.estimator.Estimate([]weft.Message{m}))
			}
			return 0
		}
	}
	cut := cutIndexWeighted(path, cfg.keepRecent, weight)
	if cut < 0 {
		return nil, fmt.Errorf("%w: session %s's tail fits inside KeepRecent", errNothingToCompact, s.header.ID)
	}
	if cut <= rangeStart {
		// Nothing new to summarize: everything past the previous kept
		// boundary still fits — the iterative chain is fed nothing it
		// does not already hold.
		return nil, fmt.Errorf("%w: session %s has nothing new past the kept boundary", errNothingToCompact, s.header.ID)
	}
	kept, summarized := path[cut:], path[rangeStart:cut]
	var rangeMsgs, contextMsgs []weft.Message
	for _, e := range path {
		if m, ok := contextMessage(e); ok {
			contextMsgs = append(contextMsgs, m)
		}
	}
	for _, e := range summarized {
		if m, ok := contextMessage(e); ok {
			rangeMsgs = append(rangeMsgs, m)
		}
	}
	// The pinned ids this compaction keeps through: every pin below the
	// cut, in the range it summarizes and in the older summary's range
	// alike — the walk re-includes them from the entry, so a pin never
	// has to hold the cut back (ADR 0020 §4).
	var pinnedThrough []string
	for _, e := range path[:cut] {
		if pinned[idOf(e)] {
			pinnedThrough = append(pinnedThrough, idOf(e))
		}
	}
	var tokensBefore int64
	for _, m := range contextMsgs {
		tokensBefore += s.estimate(m)
	}

	prep := Preparation{
		Reason:       reason,
		Context:      contextMsgs,
		Messages:     rangeMsgs,
		PrevSummary:  prev,
		FirstKept:    idOf(kept[0]),
		TokensBefore: tokensBefore,
		Instructions: instructions,
		Pinned:       pinnedThrough,
	}
	if cfg.before != nil {
		v, err := safeBefore(cfg.before, ctx, &prep)
		if err != nil {
			s.compactFailed(ctx, reason, err)
			return nil, err
		}
		switch {
		case v.isCancel():
			return nil, fmt.Errorf("%w: BeforeCompact canceled it", errCompactCanceled)
		case v.isReplace():
			c := v.replacement()
			if c == nil {
				return nil, fmt.Errorf("thread: BeforeCompact replaced the compaction with nothing")
			}
			c.Reason = ReasonFromHook
			s.mu.Lock()
			_, held := s.byID[c.FirstKept]
			s.mu.Unlock()
			if !held {
				return nil, fmt.Errorf("thread: BeforeCompact replacement keeps first entry %q, which the session does not hold", c.FirstKept)
			}
			return c, nil
		}
	}
	if cfg.compactor != nil {
		c, err := safeCompactor(cfg.compactor, ctx, prep)
		if err != nil {
			s.compactFailed(ctx, reason, err)
			return nil, err
		}
		if c == nil {
			return nil, fmt.Errorf("thread: the Compactor returned no Compaction")
		}
		return c, nil
	}
	view, files := summarizerView(rangeMsgs)
	slices.Sort(files)
	files = slices.Compact(files)

	summary, err := s.produceSummary(ctx, SummaryInput{
		Messages:     view,
		PrevSummary:  prev,
		Instructions: instructions,
		MaxTokens:    cfg.maxTokens(),
		Reason:       reason,
		SystemPrompt: cfg.summarySystemPrompt(instructions),
		SummaryModel: cfg.summaryModel,
	}, reason)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(mustJSON(view))
	c := &Compaction{
		Summary:         summary.Text,
		FirstKept:       prep.FirstKept,
		TokensBefore:    tokensBefore,
		Reason:          reason,
		SummarizerUsage: summary.Usage,
		SummarizerModel: summary.Model,
		FilesRead:       files,
		Pinned:          pinnedThrough,
		RangeHash:       hex.EncodeToString(hash[:]),
	}
	if cfg.preferNative && c.SummarizerModel == (weft.ModelInfo{}) {
		// A native compaction that reported no model info still names
		// the model that ran it.
		c.SummarizerModel = weft.InfoOf(s.agent.Model())
	}
	return c, nil
}

// contextMessage returns the message an entry contributes to the
// context — message, custom_message, and branch summaries in their
// marked shape.
func contextMessage(e Entry) (weft.Message, bool) {
	switch e := e.(type) {
	case MessageEntry:
		return e.Message, true
	case CustomMessageEntry:
		return e.Message, true
	case BranchSummaryEntry:
		return summaryMessage(e.Summary), true
	}
	return weft.Message{}, false
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

// compactFailed runs the CompactFailed hook when one is set — the
// session is unchanged, and the caller is told why. A panicking hook
// is contained: the failure it reports is already the story.
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
// session's own after it — each retried once on a CheckSummary failure
// before the chain moves on. Exhausted, the compaction fails and
// CompactFailed hears why (ADR 0020 §4).
func (s *Session) produceSummary(ctx context.Context, in SummaryInput, reason Reason) (Summary, error) {
	if fn := s.cfg.compaction.summarizer; fn != nil {
		sum, err := safeSummarize(fn, ctx, in)
		if err == nil && s.cfg.compaction.check != nil {
			if err = safeCheck(s.cfg.compaction.check, sum); err == nil {
				return sum, nil
			}
			// One retry, then the chain is exhausted for a custom
			// summarizer: there is nothing to fall back to.
			sum, err = safeSummarize(fn, ctx, in)
			if err == nil {
				if err = safeCheck(s.cfg.compaction.check, sum); err == nil {
					return sum, nil
				}
			}
		}
		if err != nil {
			s.compactFailed(ctx, reason, err)
			return Summary{}, err
		}
		return sum, nil
	}
	models := []weft.Model{}
	if in.SummaryModel != nil {
		models = append(models, in.SummaryModel)
	}
	models = append(models, s.agent.Model())
	var lastErr error
	for _, m := range models {
		for attempt := 0; attempt < 2; attempt++ {
			sum, err := s.summarizeWith(ctx, m, in)
			if err != nil {
				lastErr = err
				break // model error: the chain falls back
			}
			if s.cfg.compaction.check != nil {
				if err := safeCheck(s.cfg.compaction.check, sum); err != nil {
					lastErr = err
					continue // retry once on the same model
				}
			}
			return sum, nil
		}
	}
	err := fmt.Errorf("thread: every summarizer failed, last error: %w", lastErr)
	s.compactFailed(ctx, reason, err)
	return Summary{}, err
}

// summarizeWith makes one model produce the summary: the provider's
// own compaction when PreferNative is set and the model (or something
// it wraps) offers it, else a single text step under the system prompt
// with the output cap. No cache hints are sent, so no prompt-cache
// writes happen on the summary call.
func (s *Session) summarizeWith(ctx context.Context, m weft.Model, in SummaryInput) (Summary, error) {
	input := in.Messages
	if in.PrevSummary != "" {
		input = append([]weft.Message{summaryMessage(in.PrevSummary)}, in.Messages...)
	}
	if s.cfg.compaction.preferNative {
		if nc := nativeOf(m); nc != nil {
			msg, usage, err := nc.CompactNative(ctx, weft.ModelRequest{
				System:   in.SystemPrompt,
				Messages: input,
			}, in.Instructions)
			if err == nil && msg.Text() != "" {
				return Summary{Text: strings.TrimSpace(msg.Text()), Reason: in.Reason, Usage: usage, Model: weft.InfoOf(m)}, nil
			}
			// Any other model, a switch, or an error: the text summary
			// is the fallback (ADR 0020 §7).
		}
	}
	cap := in.MaxTokens
	req := weft.ModelRequest{
		System:   in.SystemPrompt,
		Messages: input,
		Params:   weft.RequestParams{MaxTokens: &cap},
	}
	// The stream contract, enforced the way the loop enforces it:
	// exactly one ModelFinish, nothing after it (ErrModelContract) —
	// a summarizer stream that ends without a finish is an error, not
	// whatever text happened to arrive.
	var sb strings.Builder
	var usage weft.Usage
	finished := false
	for ev, err := range m.Stream(ctx, req) {
		if err != nil {
			return Summary{}, fmt.Errorf("thread: summarizer: %w", err)
		}
		if finished {
			return Summary{}, fmt.Errorf("%w: the summarizer stream continued after ModelFinish", weft.ErrModelContract)
		}
		switch ev := ev.(type) {
		case weft.ModelTextDelta:
			sb.WriteString(ev.Text)
		case weft.ModelFinish:
			usage, finished = ev.Usage, true
		}
	}
	if !finished {
		return Summary{}, fmt.Errorf("%w: the summarizer stream ended without ModelFinish", weft.ErrModelContract)
	}
	summary := strings.TrimSpace(sb.String())
	if summary == "" {
		return Summary{}, fmt.Errorf("thread: summarizer returned no text")
	}
	return Summary{Text: summary, Reason: in.Reason, Usage: usage, Model: weft.InfoOf(m)}, nil
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
	if m.Message.Role != weft.RoleUser && m.Message.Role != weft.RoleAssistant {
		return false
	}
	if cut > 0 {
		if prev, ok := path[cut-1].(MessageEntry); ok && prev.Message.Role == weft.RoleAssistant {
			for _, p := range prev.Message.Content {
				if _, isCall := p.(weft.ToolCallPart); isCall {
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
func summarizerView(msgs []weft.Message) ([]weft.Message, []string) {
	var files []string
	out := make([]weft.Message, 0, len(msgs))
	for _, m := range msgs {
		vm := weft.Message{Role: m.Role}
		for _, p := range m.Content {
			switch p := p.(type) {
			case weft.ToolResultPart:
				if len([]rune(p.Content)) > defaultToolResultCap {
					r := []rune(p.Content)
					p.Content = string(r[:defaultToolResultCap]) + "…[truncated]"
				}
				vm.Content = append(vm.Content, p)
			case weft.ReasoningPart:
				if p.Signature != "" {
					continue // signed reasoning is dropped
				}
				vm.Content = append(vm.Content, p)
			case weft.FilePart:
				name := p.URL
				if name == "" {
					name = fmt.Sprintf("(%s, %d bytes)", p.MediaType, len(p.Data))
				} else {
					files = append(files, name)
				}
				vm.Content = append(vm.Content, weft.TextPart{Text: "[file: " + name + "]"})
			default:
				vm.Content = append(vm.Content, p)
			}
		}
		if len(vm.Content) > 0 || m.Role == weft.RoleUser {
			out = append(out, vm)
		}
	}
	return out, files
}

// estimate is the session's token estimate for one message: the
// configured Estimator when one is set, the default quarter-of-wire-
// bytes otherwise.
func (s *Session) estimate(m weft.Message) int64 {
	if est := s.cfg.compaction.estimator; est != nil {
		return int64(est.Estimate([]weft.Message{m}))
	}
	return estimateMessage(m)
}

// estimateMessage is the default token estimate for one message: a
// quarter of its wire bytes, rounded up. It estimates the DELTA the
// trigger adds to provider-reported input — the signal itself is never
// an estimate (ADR 0020 §2's rule); the Estimator interface in step
// 1.9 makes this replaceable.
func estimateMessage(m weft.Message) int64 {
	return (int64(len(mustJSON(m))) + 3) / 4
}

// estimateEntry estimates the context weight of one path entry:
// message and custom_message entries carry their message; a branch
// summary and a compaction carry the summary the context shows for
// them; the bookkeeping kinds weigh nothing (they never reach the
// context). The summaries weigh what they cost in the window — a cut
// that ignored them would keep more than KeepRecent really allows.
func estimateEntry(e Entry) int64 {
	switch e := e.(type) {
	case MessageEntry:
		return estimateMessage(e.Message)
	case CustomMessageEntry:
		return estimateMessage(e.Message)
	case BranchSummaryEntry:
		return estimateMessage(summaryMessage(e.Summary))
	case CompactionEntry:
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
// — and after each turn ends: compact when the provider-reported input of the last
// model step plus the estimated messages added since crosses window −
// Reserve. The reported input is the signal, never a chars-per-token
// guess at the whole context; only the delta is estimated. With no
// window known there is no automatic compaction, and one warning says
// so through the agent's logger.
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
	var since []weft.Message
	counting := false
	for _, e := range path {
		if !counting {
			if idOf(e) == s.lastMeasureLeaf {
				counting = true // the messages after the measured turn are the delta
			}
			continue
		}
		if m, ok := contextMessage(e); ok {
			since = append(since, m) // branch summaries count: the context carries them
		}
	}
	s.mu.Unlock()
	if !counting {
		// The mark is off the leaf's path — a Branch or an Uncompact
		// moved the line since the measurement. There is no honest
		// delta against a report that no longer describes this path,
		// so this trigger stands down; the next turn's report becomes
		// the signal again (the same rule as a never-measured
		// session).
		return
	}

	var est int64
	for _, m := range since {
		est += s.estimate(m)
	}
	in := TriggerInput{LastInput: lastInput, Estimated: est, Window: cfg.window, Reserve: cfg.reserve}
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
	if cfg.trimmer != nil {
		before := s.rawContext()
		trimmed, ok := s.safeTrim(ctx, before)
		if !ok {
			return // the trimmer panicked: logged, no trim this turn
		}
		var after int64
		for _, m := range trimmed {
			after += s.estimate(m)
		}
		if after <= cfg.window-cfg.reserve {
			if err := s.writeTrim(ctx); err != nil {
				s.agent.Logger().Warn("thread: trim record not written", "session", s.header.ID, "err", err)
			}
			return
		}
	}

	if err := s.applyAuto(ctx); err != nil {
		// A failed compaction leaves the session unchanged — the next
		// trigger tries again; the caller's turn still runs.
		if !errors.Is(err, errNothingToCompact) && !errors.Is(err, errCompactCanceled) {
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

// safeTrim runs the trimmer, containing a panic as no-trim.
func (s *Session) safeTrim(ctx context.Context, before []weft.Message) (trimmed []weft.Message, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			trimmed, ok = nil, false
			s.agent.Logger().Error("thread: the Trimmer panicked", "panic", r)
		}
	}()
	trimmed, _ = s.cfg.compaction.trimmer.Trim(ctx, before)
	return trimmed, true
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
// automatic compaction run: MinTurnsBetween turns since the last one,
// and at most MaxPerSession in the file. Manual Compact never asks.
func (s *Session) rateLimitAllows() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	compactions, turnsSince := 0, 1<<30
	for _, e := range s.order {
		switch e.(type) {
		case CompactionEntry:
			compactions++
			turnsSince = 0
		case TurnEntry:
			turnsSince++
		}
	}
	cfg := s.cfg.compaction
	if cfg.maxPerSession > 0 && compactions >= cfg.maxPerSession {
		return false
	}
	if cfg.minTurnsBetween > 0 && turnsSince < cfg.minTurnsBetween {
		return false
	}
	return true
}

// writeTrim records a trim: the same compaction entry, no summary, the
// kept boundary the last compaction left (the root when none has — a
// trim drops nothing, so everything is kept), and the trimmed view
// re-derived on read from the configured trimmer — the built-in one
// is deterministic over the file.
func (s *Session) writeTrim(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathLocked(s.leaf)
	if err != nil || len(path) == 0 {
		return fmt.Errorf("thread: no path to trim")
	}
	firstKept := idOf(path[0])
	for i := len(path) - 1; i >= 0; i-- {
		if c, ok := path[i].(CompactionEntry); ok {
			// The boundary the walk already reads from: a trim on a
			// compacted session keeps it, so its record satisfies the
			// same iterative rule every compaction does.
			if indexOfID(path, c.FirstKept) >= 0 {
				firstKept = c.FirstKept
			}
			break
		}
	}
	var before int64
	for _, e := range path {
		before += defaultEntryWeight(e)
	}
	e := CompactionEntry{
		ID:           s.mintIDLocked(),
		ParentID:     s.leaf,
		Created:      time.Now().UTC(),
		FirstKept:    firstKept,
		TokensBefore: before,
		Reason:       ReasonTrim,
	}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	s.agent.Logger().Info("thread: trimmed old tool results",
		"session", s.header.ID, "tokens_before", before)
	return nil
}

// errCompactCanceled names a compaction a BeforeCompact hook stopped.
var errCompactCanceled = errors.New("thread: compaction canceled")

// errNothingToCompact names the no-op path of the trigger: a context
// that crossed the line by estimate but whose tail still fits the keep
// window — nothing to write, nothing to warn about twice.
var errNothingToCompact = errors.New("thread: nothing to compact")

// summarizeBranch summarizes the branch a SummarizeLeft Branch leaves
// behind — the messages from the divergence up to the current leaf —
// with the same skeleton, marker and model as compaction (ADR 0020
// §6): one summary, cache prefix shared with nothing, the cost
// documented rather than hidden. The divergence is the common
// ancestor of the current leaf and the branch target: a target on
// another branch summarizes everything this branch grew since the two
// parted, and the returned from-entry names that ancestor ("" when
// the branch being left is the whole session, grown from the root).
func (s *Session) summarizeBranch(ctx context.Context, target string) (summary, fromEntry string, err error) {
	s.mu.Lock()
	leafPath, err := s.pathLocked(s.leaf)
	if err != nil {
		s.mu.Unlock()
		return "", "", err
	}
	targetPath, err := s.pathLocked(target)
	if err != nil {
		s.mu.Unlock()
		return "", "", err
	}
	common := 0
	for common < len(leafPath) && common < len(targetPath) &&
		idOf(leafPath[common]) == idOf(targetPath[common]) {
		common++
	}
	fromEntry = ""
	if common > 0 {
		fromEntry = idOf(leafPath[common-1])
	}
	var sumMsgs []weft.Message
	for _, e := range leafPath[common:] {
		switch e := e.(type) {
		case MessageEntry:
			sumMsgs = append(sumMsgs, e.Message)
		case CustomMessageEntry:
			sumMsgs = append(sumMsgs, e.Message)
		case BranchSummaryEntry:
			sumMsgs = append(sumMsgs, summaryMessage(e.Summary))
		case CompactionEntry:
			// The abandoned branch may hold its own compaction; its
			// summary is the only record of that branch's older part,
			// and the branch summary must keep it.
			if e.Summary != "" {
				sumMsgs = append(sumMsgs, summaryMessage(e.Summary))
			}
		}
	}
	s.mu.Unlock()
	if len(sumMsgs) == 0 {
		return "", "", fmt.Errorf("thread: SummarizeLeft with no branch to summarize: the leaf is on %q's path already", target)
	}
	view, _ := summarizerView(sumMsgs)
	cfg := s.cfg.compaction
	sum, err := s.produceSummary(ctx, SummaryInput{
		Messages:     view,
		MaxTokens:    cfg.maxTokens(),
		SystemPrompt: cfg.summarySystemPrompt(""),
		SummaryModel: cfg.summaryModel,
	}, ReasonManual)
	return sum.Text, fromEntry, err
}
