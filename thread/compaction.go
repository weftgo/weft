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
}

func defaultCompactConfig() compactConfig {
	return compactConfig{reserve: defaultReserve, keepRecent: defaultKeepRecent}
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
func (s *Session) PreviewCompaction(ctx context.Context) (*Compaction, error) {
	return s.computeCompaction(ctx, ReasonManual)
}

// Compact computes and applies the session's next compaction in one
// call: PreviewCompaction, then ApplyCompaction. Nothing is deleted —
// the summarized entries stay in the file, and the context at the leaf
// becomes the summary, then the entries from the compaction's first
// kept entry onward.
func (s *Session) Compact(ctx context.Context) error {
	c, err := s.PreviewCompaction(ctx)
	if err != nil {
		return err
	}
	return s.ApplyCompaction(ctx, c)
}

// ApplyCompaction writes a computed Compaction as the session's next
// compaction entry, appended at the leaf. c must name a FirstKept the
// session holds; anything else is an error, and nothing is written.
// The entry lands whatever else happened between its computation and
// this call — the tree only grew, so the kept range stays correct.
func (s *Session) ApplyCompaction(ctx context.Context, c *Compaction) error {
	if c == nil {
		return fmt.Errorf("thread: ApplyCompaction with no Compaction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[c.FirstKept]; !ok {
		return fmt.Errorf("thread: compaction keeps first entry %q, which session %s does not hold", c.FirstKept, s.header.ID)
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
	return nil
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

// computeCompaction is the algorithm: snapshot the leaf's path, cut it,
// serialize the summarized range for the summarizer, hash it, and run
// the summary. The model call runs without the session lock — it takes
// seconds — and the tree only grows meanwhile, so the snapshot's cut
// stays valid to apply.
func (s *Session) computeCompaction(ctx context.Context, reason Reason) (*Compaction, error) {
	s.mu.Lock()
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	cfg := s.cfg.compaction
	prev := ""
	for i := len(path) - 1; i >= 0; i-- {
		if c, ok := path[i].(CompactionEntry); ok {
			prev = c.Summary // iterative: the previous summary feeds the next
			break
		}
	}
	s.mu.Unlock()

	cut := cutIndex(path, cfg.keepRecent)
	if cut < 0 {
		return nil, fmt.Errorf("%w: session %s's tail fits inside KeepRecent", errNothingToCompact, s.header.ID)
	}
	kept, summarized := path[cut:], path[:cut]
	var sumMsgs []weft.Message
	var filesRead []string
	for _, e := range summarized {
		switch e := e.(type) {
		case MessageEntry:
			sumMsgs = append(sumMsgs, e.Message)
		case CustomMessageEntry:
			sumMsgs = append(sumMsgs, e.Message)
		case BranchSummaryEntry:
			sumMsgs = append(sumMsgs, summaryMessage(e.Summary))
		}
	}
	view, files := summarizerView(sumMsgs)
	filesRead = append(filesRead, files...)
	slices.Sort(filesRead)
	filesRead = slices.Compact(filesRead)

	var tokensBefore int64
	for _, m := range sumMsgs {
		tokensBefore += estimateMessage(m)
	}

	summary, usage, err := s.summarize(ctx, view, prev)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(mustJSON(view))
	return &Compaction{
		Summary:         summary,
		FirstKept:       idOf(kept[0]),
		TokensBefore:    tokensBefore,
		Reason:          reason,
		SummarizerUsage: usage,
		SummarizerModel: weft.InfoOf(s.agent.Model()),
		FilesRead:       filesRead,
		Pinned:          nil, // s.Pin arrives in step 1.9
		RangeHash:       hex.EncodeToString(hash[:]),
	}, nil
}

// cutIndex finds where the path cuts: the earliest boundary such that
// the entries kept after it fit within about keepRecent estimated
// tokens, moved forward to a valid cut position. A valid position is
// between two entries where the kept side starts at a user or
// assistant message, and the summarized side does not end on an
// assistant message with tool calls — never between a call and its
// result (ADR 0020 §2). A single turn larger than keepRecent is split
// at an assistant message inside it. -1 means nothing to compact.
func cutIndex(path []Entry, keepRecent int64) int {
	// Walk back from the leaf accumulating the estimated tail.
	suffix := int64(0)
	cut := -1
	for i := len(path) - 1; i >= 0; i-- {
		suffix += estimateEntry(path[i])
		if suffix > keepRecent {
			cut = i + 1 // entries[i+1:] fit; entries[i] starts the overflow
			break
		}
	}
	if cut <= 0 {
		return -1 // the whole path fits inside the keep window
	}
	if cut >= len(path) {
		// Even a single tail entry overflows the keep window — the
		// extreme of a turn larger than KeepRecent. Keep the smallest
		// tail there is: the latest boundary that leaves whole
		// messages, split at an assistant boundary where there is one
		// (pi's split turn). An estimate cannot refuse to compact a
		// context the window cannot hold.
		for c := len(path) - 1; c >= 1; c-- {
			if validCut(path, c) {
				return c
			}
		}
		return -1
	}
	// Move forward to the next valid boundary: keeping more is always
	// safe, and every path starts with a user- or assistant-role
	// message (the session's first write is a prompt or a custom
	// message — a custom message is a user message unless its caller
	// made it something else, and the walk then skips to the next
	// real boundary).
	for cut < len(path) && !validCut(path, cut) {
		cut++
	}
	if cut >= len(path) {
		return -1
	}
	return cut
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

// summarize runs the summarizer: the session's own model, the skeleton
// prompt, the previous summary as the range's first message when one
// exists (iterative compaction), no tools, no cache hints (prompt-cache
// writes stay off), and the output capped at 0.8 × Reserve. The reply
// is the concatenated text of the single step it produces.
func (s *Session) summarize(ctx context.Context, view []weft.Message, prevSummary string) (string, weft.Usage, error) {
	input := view
	if prevSummary != "" {
		input = append([]weft.Message{summaryMessage(prevSummary)}, view...)
	}
	cap := int(float64(s.cfg.compaction.reserve) * summaryCapShare)
	req := weft.ModelRequest{
		System:   summarySkeleton,
		Messages: input,
		Params:   weft.RequestParams{MaxTokens: &cap},
	}
	var sb strings.Builder
	var usage weft.Usage
	for ev, err := range s.agent.Model().Stream(ctx, req) {
		if err != nil {
			return "", usage, fmt.Errorf("thread: summarizer: %w", err)
		}
		switch ev := ev.(type) {
		case weft.ModelTextDelta:
			sb.WriteString(ev.Text)
		case weft.ModelFinish:
			usage = ev.Usage
		}
	}
	summary := strings.TrimSpace(sb.String())
	if summary == "" {
		return "", usage, fmt.Errorf("thread: summarizer returned no text")
	}
	return summary, usage, nil
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
// summary carries its summary; the bookkeeping kinds weigh nothing
// (they never reach the context).
func estimateEntry(e Entry) int64 {
	switch e := e.(type) {
	case MessageEntry:
		return estimateMessage(e.Message)
	case CustomMessageEntry:
		return estimateMessage(e.Message)
	case BranchSummaryEntry:
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
	lastInput := s.lastInput
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		s.mu.Unlock()
		return
	}
	var since []weft.Message
	counting := s.lastMeasureLeaf == ""
	for _, e := range path {
		if !counting && idOf(e) == s.lastMeasureLeaf {
			counting = true
			continue
		}
		if !counting {
			continue
		}
		switch e := e.(type) {
		case MessageEntry:
			since = append(since, e.Message)
		case CustomMessageEntry:
			since = append(since, e.Message)
		}
	}
	s.mu.Unlock()

	var est int64
	for _, m := range since {
		est += estimateMessage(m)
	}
	if lastInput+est <= cfg.window-cfg.reserve {
		return
	}
	if err := s.Compact(ctx); err != nil {
		// A failed compaction leaves the session unchanged — the next
		// trigger tries again; the caller's turn still runs.
		if !errors.Is(err, errNothingToCompact) {
			s.agent.Logger().Warn("thread: automatic compaction failed", "session", s.header.ID, "err", err)
		}
	}
}

// errNothingToCompact names the no-op path of the trigger: a context
// that crossed the line by estimate but whose tail still fits the keep
// window — nothing to write, nothing to warn about twice.
var errNothingToCompact = errors.New("thread: nothing to compact")

// summarizeBranch summarizes the branch a SummarizeLeft Branch leaves
// behind — the messages after the divergence entry up to the current
// leaf — with the same skeleton, marker and model as compaction
// (ADR 0020 §6): one summary, cache prefix shared with nothing, the
// cost documented rather than hidden.
func (s *Session) summarizeBranch(ctx context.Context, divergence string) (string, error) {
	s.mu.Lock()
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	var sumMsgs []weft.Message
	counting := false
	for _, e := range path {
		if !counting {
			if idOf(e) == divergence {
				counting = true // the summarized range starts after the divergence
			}
			continue
		}
		switch e := e.(type) {
		case MessageEntry:
			sumMsgs = append(sumMsgs, e.Message)
		case CustomMessageEntry:
			sumMsgs = append(sumMsgs, e.Message)
		case BranchSummaryEntry:
			sumMsgs = append(sumMsgs, summaryMessage(e.Summary))
		}
	}
	s.mu.Unlock()
	if len(sumMsgs) == 0 {
		return "", fmt.Errorf("thread: SummarizeLeft on a branch with no messages after %q", divergence)
	}
	view, _ := summarizerView(sumMsgs)
	summary, _, err := s.summarize(ctx, view, "")
	return summary, err
}
