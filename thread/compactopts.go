package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft"
)

// ── Layer 1 — knobs ─────────────────────────────────────────────────

type contextWindowOption int64

func (o contextWindowOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.window = int64(o)
	}
}

// ContextWindow returns the SessionOption setting the context window
// the trigger budgets against, in tokens. Zero or negative is ignored
// (the window stays unknown: no automatic compaction, one warning).
// ModelWindows overrides it per model.
func ContextWindow(n int64) SessionOption { return contextWindowOption(n) }

type modelWindowsOption map[weft.ModelInfo]int64

func (o modelWindowsOption) applySession(c *sessionConfig) {
	if c.compaction.modelWindows == nil {
		c.compaction.modelWindows = map[weft.ModelInfo]int64{}
	}
	for k, v := range o {
		if v > 0 {
			c.compaction.modelWindows[k] = v
		}
	}
}

// ModelWindows returns the SessionOption setting per-model context
// windows, keyed by ModelInfo (provider + name): the session's own
// model is looked up at Create/Open, and its entry overrides
// ContextWindow for this session — pi's per-model settings, for the
// agent that switches models mid-project.
func ModelWindows(windows map[weft.ModelInfo]int64) SessionOption {
	return modelWindowsOption(windows)
}

type modelReservesOption map[weft.ModelInfo]int64

func (o modelReservesOption) applySession(c *sessionConfig) {
	if c.compaction.modelReserves == nil {
		c.compaction.modelReserves = map[weft.ModelInfo]int64{}
	}
	for k, v := range o {
		if v > 0 {
			c.compaction.modelReserves[k] = v
		}
	}
}

// ModelReserves returns the SessionOption setting per-model reserves,
// keyed by ModelInfo, overriding Reserve for this session's model —
// the small-window model that needs more headroom than the default.
func ModelReserves(reserves map[weft.ModelInfo]int64) SessionOption {
	return modelReservesOption(reserves)
}

type reserveOption int64

func (o reserveOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.reserve = int64(o)
	}
}

// Reserve returns the SessionOption setting the trigger's headroom:
// compaction fires before the context is within Reserve tokens of the
// window. Zero or negative keeps the default 16,384.
func Reserve(n int64) SessionOption { return reserveOption(n) }

type keepRecentOption int64

func (o keepRecentOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.keepRecent = int64(o)
	}
}

// KeepRecent returns the SessionOption setting roughly how many
// estimated tokens of the tail stay raw on either side of the cut.
// Zero or negative keeps the default 20,000.
func KeepRecent(n int64) SessionOption { return keepRecentOption(n) }

// TriggerInput is what a TriggerFunc decides on: the provider-reported
// input of the last model step, the estimated tokens added since, and
// the window and reserve in force. The reported number is the signal;
// only the delta is estimated (ADR 0020 §2).
type TriggerInput struct {
	LastInput int64
	Estimated int64
	Window    int64
	Reserve   int64
}

type triggerFuncOption func(TriggerInput) bool

func (o triggerFuncOption) applySession(c *sessionConfig) { c.compaction.trigger = o }

// TriggerFunc returns the SessionOption replacing the trigger's
// condition — fire when in says so. It is consulted only when a window
// is known and a provider-reported input exists: the no-estimated-
// signal rule stands under a custom trigger too.
func TriggerFunc(fn func(TriggerInput) bool) SessionOption {
	if fn == nil {
		return nil
	}
	return triggerFuncOption(fn)
}

type minTurnsBetweenOption int

func (o minTurnsBetweenOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.minTurnsBetween = int(o)
	}
}

// MinTurnsBetween returns the SessionOption rate-limiting automatic
// compaction: no automatic compaction within n turns of the last one
// (manual Compact always works). Zero, the default, means no limit.
// The stop against a context that re-crosses the line every turn.
func MinTurnsBetween(n int) SessionOption { return minTurnsBetweenOption(n) }

type maxPerSessionOption int

func (o maxPerSessionOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.maxPerSession = int(o)
	}
}

// MaxPerSession returns the SessionOption capping how many automatic
// compactions a session may run — zero (the default) means no cap;
// after the cap the trigger stops firing and manual Compact keeps
// working.
func MaxPerSession(n int) SessionOption { return maxPerSessionOption(n) }

// Estimator estimates the token weight of messages — the trigger's
// delta and the cut's walk. The default is a quarter of the wire
// bytes; a provider-aware implementation can do better.
type Estimator interface {
	Estimate(msgs []weft.Message) int
}

type estimatorOption struct{ est Estimator }

func (o estimatorOption) applySession(c *sessionConfig) {
	if o.est != nil {
		c.compaction.estimator = o.est
	}
}

// WithEstimator returns the SessionOption replacing the token
// estimator. (The ADR sketches thread.Estimator(est); the interface
// already owns that name, so the option takes the With* shape its
// layer-3 neighbours use.)
func WithEstimator(est Estimator) SessionOption { return estimatorOption{est} }

type disabledOption struct{}

func (disabledOption) applySession(c *sessionConfig) { c.compaction.disabled = true }

// Disabled returns the SessionOption turning automatic compaction off
// entirely — no trigger, no warning: the session that compacts only by
// hand. Manual Compact, PreviewCompaction and Uncompact are unaffected
// (ADR 0020 §3).
func Disabled() SessionOption { return disabledOption{} }

// ── Layer 2 — the summary ───────────────────────────────────────────

type summaryModelOption struct{ m weft.Model }

func (o summaryModelOption) applySession(c *sessionConfig) {
	if o.m != nil {
		c.compaction.summaryModel = o.m
	}
}

// SummaryModel returns the SessionOption setting a different model for
// summaries — a cheap one. On failure it falls back to the session's
// own model (the chain: SummaryModel → session model → no compaction,
// reported through CompactFailed), because a weak summary costs more
// in rework than a cheap model saves only when it works at all.
func SummaryModel(m weft.Model) SessionOption { return summaryModelOption{m} }

type summaryPromptOption string

func (o summaryPromptOption) applySession(c *sessionConfig) {
	if o != "" {
		c.compaction.summaryPrompt = string(o)
	}
}

// SummaryPrompt returns the SessionOption replacing the skeleton
// system prompt whole. The replacement's bytes are the caller's; the
// default skeleton stays pinned by its golden.
func SummaryPrompt(tmpl string) SessionOption { return summaryPromptOption(tmpl) }

type summaryFocusOption string

func (o summaryFocusOption) applySession(c *sessionConfig) {
	if o != "" {
		c.compaction.summaryFocus = string(o)
	}
}

// SummaryFocus returns the SessionOption appending instructions to the
// summary prompt (a replacement or the skeleton): "keep every file
// path" — the caller's standing concern, appended after the skeleton's
// own rules.
func SummaryFocus(focus string) SessionOption { return summaryFocusOption(focus) }

type summaryMaxTokensOption int

func (o summaryMaxTokensOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.summaryMaxTokens = int(o)
	}
}

// SummaryMaxTokens returns the SessionOption overriding the summarizer
// output cap (the default 0.8 × Reserve).
func SummaryMaxTokens(n int) SessionOption { return summaryMaxTokensOption(n) }

// CompactOption configures one compaction call.
type CompactOption interface {
	applyCompact(*compactCall)
}

type compactCall struct {
	instructions string
}

func resolveCompact(opts ...CompactOption) compactCall {
	var c compactCall
	for _, o := range opts {
		if o != nil {
			o.applyCompact(&c)
		}
	}
	return c
}

type instructionsOption string

func (o instructionsOption) applyCompact(c *compactCall) {
	if o != "" {
		c.instructions = string(o)
	}
}

// Instructions returns the CompactOption appending per-call
// instructions to this compaction's summary prompt: s.Compact(ctx,
// thread.Instructions("focus on the API design")).
func Instructions(text string) CompactOption { return instructionsOption(text) }

// ── Layer 3 — swap the parts ────────────────────────────────────────

// SummaryInput is everything a Summarizer needs: the range already
// serialized for summarizing (tool results capped, signed reasoning
// dropped, files as names), the previous summary when one exists, the
// per-call instructions, the output cap, and the reason it runs.
type SummaryInput struct {
	Messages     []weft.Message
	PrevSummary  string
	Instructions string
	MaxTokens    int
	Reason       Reason
	SystemPrompt string
	SummaryModel weft.Model
}

// Summary is a summarizer's output: the text, and the reason it ran.
type Summary struct {
	Text   string
	Reason Reason
	// Usage and Model are what the summary cost and which model made
	// it — the cost ledger's inputs; a custom Summarizer that runs no
	// model leaves them zero.
	Usage weft.Usage
	Model weft.ModelInfo
}

// Summarizer produces summary text — just the text: the cut, the
// serialization and the entry are the session's. Implemented over a
// model by default (the session's own, or SummaryModel).
type Summarizer interface {
	Summarize(ctx context.Context, in SummaryInput) (Summary, error)
}

// Preparation is a computed compaction on its way to becoming one:
// everything the algorithm and the BeforeCompact hook see. Reason is
// why it runs; Context is the leaf's whole context; Messages the
// default cut's range to summarize; SplitPrefix the split turn's
// prefix when one turn alone overflows (nil in the one-call default,
// which folds it into Messages with the previous summary); PrevSummary
// the iterative chain's last link; FirstKept, TokensBefore and Pinned
// the entry's facts; Instructions the per-call text.
type Preparation struct {
	Reason       Reason
	Context      []weft.Message
	Messages     []weft.Message
	SplitPrefix  []weft.Message
	PrevSummary  string
	FirstKept    string
	TokensBefore int64
	Instructions string
	Pinned       []string
}

// Compactor is the whole compaction algorithm, swapped in whole: it
// receives the Preparation and returns the Compaction to write. The
// default cuts, serializes, summarizes and hashes; a replacement may
// ignore the suggested cut and keep any FirstKept it can name.
type Compactor interface {
	Compact(ctx context.Context, p Preparation) (*Compaction, error)
}

type withSummarizerOption struct{ s Summarizer }

func (o withSummarizerOption) applySession(c *sessionConfig) {
	if o.s != nil {
		c.compaction.summarizer = o.s
	}
}

// WithSummarizer returns the SessionOption replacing text production
// — just the text; the cut and the entry stay the session's.
func WithSummarizer(s Summarizer) SessionOption { return withSummarizerOption{s} }

type withCompactorOption struct{ c Compactor }

func (o withCompactorOption) applySession(c *sessionConfig) {
	if o.c != nil {
		c.compaction.compactor = o.c
	}
}

// WithCompactor returns the SessionOption replacing the whole
// algorithm.
func WithCompactor(c Compactor) SessionOption { return withCompactorOption{c} }

// TrimReport says what a trimmer did: how many tool results it
// replaced with the stub, and roughly how many estimated tokens that
// saved.
type TrimReport struct {
	Cleared       int
	EstimatedSave int64
}

// Trimmer is the cheap pre-pass: before summarizing, replace old tool
// results with a stub naming the call, and the context may fit again
// (Anthropic clear_tool_uses, Vercel pruneMessages). It runs on the
// automatic path only — a manual Compact summarizes.
type Trimmer interface {
	Trim(ctx context.Context, msgs []weft.Message) ([]weft.Message, TrimReport)
}

type withTrimmerOption struct{ t Trimmer }

func (o withTrimmerOption) applySession(c *sessionConfig) {
	if o.t != nil {
		c.compaction.trimmer = o.t
	}
}

// WithTrimmer returns the SessionOption setting a custom trimmer. The
// default trimmer is off; ClearOldToolResults turns a built-in one on.
func WithTrimmer(t Trimmer) SessionOption { return withTrimmerOption{t} }

// The stub that replaces a cleared tool result — model-visible bytes,
// pinned by testdata/compaction/cleared-stub.txt.
func clearedResultStub(callID, name string) string {
	return "[cleared tool result " + name + " " + callID + "]"
}

// clearResultsTrimmer is the built-in Trimmer behind
// ClearOldToolResults: every tool result except the last keepLast is
// replaced with the stub, newest kept first.
type clearResultsTrimmer struct{ keepLast int }

func (t clearResultsTrimmer) Trim(ctx context.Context, msgs []weft.Message) ([]weft.Message, TrimReport) {
	// A step's results batch on one tool message (ADR 0001), so the
	// unit is the result part: the newest keepLast parts in the whole
	// context survive, every older one reads as the stub.
	type at struct{ msg, part int }
	var parts []at // newest first
	for i := len(msgs) - 1; i >= 0; i-- {
		for j := len(msgs[i].Content) - 1; j >= 0; j-- {
			if _, ok := msgs[i].Content[j].(weft.ToolResultPart); ok {
				parts = append(parts, at{i, j})
			}
		}
	}
	keep := make(map[at]bool, t.keepLast)
	for n := 0; n < min(t.keepLast, len(parts)); n++ {
		keep[parts[n]] = true
	}
	out := make([]weft.Message, len(msgs))
	copy(out, msgs)
	var rep TrimReport
	for _, p := range parts {
		if keep[p] {
			continue
		}
		before := estimateMessage(out[p.msg])
		m := out[p.msg]
		if r, ok := m.Content[p.part].(weft.ToolResultPart); ok {
			stubbed := weft.ToolResultPart{CallID: r.CallID, Name: r.Name, Content: clearedResultStub(r.CallID, r.Name)}
			content := make([]weft.Part, len(m.Content))
			copy(content, m.Content)
			content[p.part] = stubbed
			m.Content = content
			out[p.msg] = m
			rep.Cleared++
			rep.EstimatedSave += before - estimateMessage(m)
		}
	}
	return out, rep
}

type clearOldToolResultsOption int

func (o clearOldToolResultsOption) applySession(c *sessionConfig) {
	if o >= 0 {
		c.compaction.trimmer = clearResultsTrimmer{keepLast: int(o)}
	}
}

// ClearOldToolResults returns the SessionOption turning on the built-in
// trimmer: every tool result in the context except the last keepLast
// is replaced, at compaction time, with the stub naming the call. If
// the trimmed context fits the window, no summary is made and a trim
// record lands in the compaction entry instead.
func ClearOldToolResults(keepLast int) SessionOption { return clearOldToolResultsOption(keepLast) }

// ── Layer 4 — hooks ─────────────────────────────────────────────────

// Verdict is what a BeforeCompact hook returns: proceed with the
// computed compaction, cancel it, or replace it with a hook-made one
// (recorded from_hook).
type Verdict struct {
	action   int // 0 proceed, 1 cancel, 2 replace
	replaces *Compaction
}

// Proceed runs the compaction as computed.
var Proceed = Verdict{}

// Cancel stops it: nothing is written.
var Cancel = Verdict{action: 1}

// Replace writes c instead — the hook's summary, recorded from_hook.
func Replace(c *Compaction) Verdict { return Verdict{action: 2, replaces: c} }

func (v Verdict) isReplace() bool          { return v.action == 2 }
func (v Verdict) isCancel() bool           { return v.action == 1 }
func (v Verdict) replacement() *Compaction { return v.replaces }

type beforeCompactOption func(context.Context, *Preparation) (Verdict, error)

func (o beforeCompactOption) applySession(c *sessionConfig) { c.compaction.before = o }

// BeforeCompact returns the SessionOption setting the hook that sees
// every computed compaction before it is written, told the reason, and
// decides: Proceed, Cancel, or Replace with a hook-made summary. A hook
// error fails the compaction with that error.
func BeforeCompact(fn func(ctx context.Context, p *Preparation) (Verdict, error)) SessionOption {
	if fn == nil {
		return nil
	}
	return beforeCompactOption(fn)
}

type afterCompactOption func(context.Context, CompactionEntry)

func (o afterCompactOption) applySession(c *sessionConfig) { c.compaction.after = o }

// AfterCompact returns the SessionOption setting the hook that runs
// after a compaction entry lands, with the entry — the durable record,
// not the plan.
func AfterCompact(fn func(ctx context.Context, e CompactionEntry)) SessionOption {
	if fn == nil {
		return nil
	}
	return afterCompactOption(fn)
}

type compactFailedOption func(context.Context, Reason, error)

func (o compactFailedOption) applySession(c *sessionConfig) { c.compaction.failed = o }

// CompactFailed returns the SessionOption setting the hook that runs
// when a compaction fails after every fallback — the session is
// unchanged, and the caller is told why, with the reason it was
// attempted for.
func CompactFailed(fn func(ctx context.Context, r Reason, err error)) SessionOption {
	if fn == nil {
		return nil
	}
	return compactFailedOption(fn)
}

type checkSummaryOption func(Summary) error

func (o checkSummaryOption) applySession(c *sessionConfig) { c.compaction.check = o }

// CheckSummary returns the SessionOption validating each summary. A
// failure retries the same model once, then falls back down the chain
// (SummaryModel → session model → no compaction, reported through
// CompactFailed) — the headings-present, length-floor checks a careful
// caller writes.
func CheckSummary(fn func(Summary) error) SessionOption {
	if fn == nil {
		return nil
	}
	return checkSummaryOption(fn)
}

// ── Layer 5 — provider-native ───────────────────────────────────────

// NativeCompactor is implemented by adapters whose provider compacts
// server-side. Root types only, so an adapter implements it without
// importing thread (ADR 0020 §7).
type NativeCompactor interface {
	CompactNative(ctx context.Context, req weft.ModelRequest, instructions string) (weft.Message, weft.Usage, error)
}

type preferNativeOption struct{}

func (preferNativeOption) applySession(c *sessionConfig) { c.compaction.preferNative = true }

// PreferNative returns the SessionOption asking thread to use the
// provider's own compaction when the summarizer model (or the one it
// wraps) implements NativeCompactor — found by following Unwrap()
// through middleware. The returned message's text is stored as the
// summary and the entry records the model that made it. On any other
// model, a switch, or an error, the text summary runs: the seam, with
// the fallback.
func PreferNative() SessionOption { return preferNativeOption{} }

// nativeOf walks m's middleware chain looking for a NativeCompactor.
func nativeOf(m weft.Model) NativeCompactor {
	for ; m != nil; m = weft.Unwrap(m) {
		if nc, ok := m.(NativeCompactor); ok {
			return nc
		}
	}
	return nil
}

// ── Extras — pinning ────────────────────────────────────────────────

// pinKind is the reserved custom-entry kind a Pin writes: its data is
// the pinned entry's id as a JSON string. A reserved prefix the format
// owns; callers' kinds stay their own.
const pinKind = "weft/pin"

// Pin marks an entry to survive every compaction raw — a requirement,
// a key decision — by appending a pin record (a custom entry, so it
// survives compaction itself, the way everything custom does). The cut
// moves forward past pinned entries: no compaction summarizes one.
func (s *Session) Pin(ctx context.Context, entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[entryID]; !ok {
		return fmt.Errorf("thread: session %s holds no entry %q", s.header.ID, entryID)
	}
	data, err := json.Marshal(entryID)
	if err != nil {
		return err
	}
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return CustomEntry{ID: id, ParentID: parent, Created: created, Kind: pinKind, Data: data}
	})
}

// pinnedIDsLocked collects the pinned entry ids — every pin record in
// the file, branches included: a pin is a fact about the entry, not
// about the branch it was pinned on.
func (s *Session) pinnedIDsLocked() map[string]bool {
	var out map[string]bool
	for _, e := range s.order {
		if c, ok := e.(CustomEntry); ok && c.Kind == pinKind {
			var id string
			if json.Unmarshal(c.Data, &id) == nil && id != "" {
				if out == nil {
					out = map[string]bool{}
				}
				out[id] = true
			}
		}
	}
	return out
}

// summarySystemPrompt assembles the summarizer's system prompt: the
// replacement or the skeleton, then the standing focus and the
// per-call instructions, each on its own line when present.
func (c *compactConfig) summarySystemPrompt(instructions string) string {
	var b strings.Builder
	if c.summaryPrompt != "" {
		b.WriteString(c.summaryPrompt)
	} else {
		b.WriteString(summarySkeleton)
	}
	if c.summaryFocus != "" {
		b.WriteString("\n\n")
		b.WriteString(c.summaryFocus)
	}
	if instructions != "" {
		b.WriteString("\n\n")
		b.WriteString(instructions)
	}
	return b.String()
}

// maxTokens is the summarizer output cap: the override or 0.8 ×
// Reserve.
func (c *compactConfig) maxTokens() int {
	if c.summaryMaxTokens > 0 {
		return c.summaryMaxTokens
	}
	return int(float64(c.reserve) * summaryCapShare)
}
