package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/weftgo/weft"
)

// The compaction options (ADR 0020 §3). The naming rule of this file:
// With* injects an implementation of an interface (WithEstimator,
// WithSummarizer, WithCompactor, WithTrimmer — the bare names are the
// interface types); bare names set values or functions.
//
// The lock rule of this file: every caller-supplied function or
// implementation — TriggerFunc, Estimator, Trimmer, Summarizer,
// Compactor, BeforeCompact, AfterCompact, CompactFailed, CheckSummary
// — runs without the session lock and may call the session
// (Context, Usage, Leaf, Entries, …). The write that follows a hook
// re-validates against the tree as it is then.

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
// ModelWindows overrides it per model. A known window must leave room
// for the other two knobs — Reserve < window and KeepRecent < window −
// Reserve — or Create and Open fail with ErrCompactConfig: a window
// too small for its reserve would otherwise never compact, silently.
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
// windows, keyed by ModelInfo (provider + name): the session agent's
// model is looked up once, at Create or Open, and its entry overrides
// ContextWindow for the life of that Session value — one options list
// shared by sessions that run different agents. The lookup is not
// repeated per turn: a run that swaps its model (weft.UseModel) keeps
// the window resolved for the agent's own model, and a session
// reopened with another agent resolves again for that agent.
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
// keyed by ModelInfo, overriding Reserve for the session agent's model
// — the small-window model that needs a different headroom than the
// default. Resolved once at Create or Open, like ModelWindows.
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
// window. Zero or negative keeps the default 16,384. It also sizes the
// summarizer's output cap (0.8 × Reserve unless SummaryMaxTokens says
// otherwise).
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
// condition — fire when fn says so. It is consulted only when a window
// is known and a provider-reported input describes the current path:
// the no-estimated-signal rule stands under a custom trigger too, and
// so does the stand-down after a compaction (the trigger waits for the
// next provider report). fn runs without the session lock; it may call
// the session. A panic in fn is contained and reads as "do not fire".
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
// compaction: no automatic compaction or trim within n turns of the
// last compaction entry on the leaf's path (manual Compact always
// works). Zero, the default, means no limit. The count runs along the
// leaf's path, so a compaction on another branch does not hold this
// one back. The stop against a context that re-crosses the line every
// turn.
func MinTurnsBetween(n int) SessionOption { return minTurnsBetweenOption(n) }

type maxPerSessionOption int

func (o maxPerSessionOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.maxPerSession = int(o)
	}
}

// MaxPerSession returns the SessionOption capping how many automatic
// compactions (every reason but manual, trims included) may sit on the
// leaf's path — zero (the default) means no cap; at the cap the
// trigger stops firing and manual Compact keeps working. The count
// runs along the leaf's path: an abandoned branch's compactions are
// not this line's.
func MaxPerSession(n int) SessionOption { return maxPerSessionOption(n) }

// Estimator estimates the token weight of messages — the trigger's
// delta, the cut's walk and the entry's TokensBefore. The default is a
// quarter of the wire bytes; a provider-aware implementation can do
// better. Estimate returns the total for msgs, in tokens, the unit
// every other number here uses; it is called with one message when the
// walk weighs entries one by one and with a batch otherwise. It runs
// without the session lock and may call the session.
type Estimator interface {
	Estimate(msgs []weft.Message) int64
}

type estimatorOption struct{ est Estimator }

func (o estimatorOption) applySession(c *sessionConfig) {
	if o.est != nil {
		c.compaction.estimator = o.est
	}
}

// WithEstimator returns the SessionOption replacing the token
// estimator. A nil est is ignored.
func WithEstimator(est Estimator) SessionOption { return estimatorOption{est} }

type noAutoCompactOption struct{}

func (noAutoCompactOption) applySession(c *sessionConfig) { c.compaction.disabled = true }

// NoAutoCompact returns the SessionOption turning automatic compaction
// off entirely — no trigger, no trim, no no-window warning: the
// session that compacts only by hand. Manual Compact,
// PreviewCompaction, ApplyCompaction and Uncompact are unaffected, and
// so is the overflow compaction a failed turn runs (ReRunOnOverflow
// governs that one).
func NoAutoCompact() SessionOption { return noAutoCompactOption{} }

// ── Layer 2 — the summary ───────────────────────────────────────────

type summaryModelOption struct{ m weft.Model }

func (o summaryModelOption) applySession(c *sessionConfig) {
	if o.m != nil {
		c.compaction.summaryModel = o.m
	}
}

// SummaryModel returns the SessionOption setting a different model for
// summaries — a cheap one. When it fails — an error, a summary
// CheckSummary rejects twice, or one cut off at the output cap twice —
// the session's own model takes over (the chain: SummaryModel →
// session model → no compaction, the error returned and reported
// through CompactFailed).
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

type summaryMaxTokensOption int64

func (o summaryMaxTokensOption) applySession(c *sessionConfig) {
	if o > 0 {
		c.compaction.summaryMaxTokens = int64(o)
	}
}

// SummaryMaxTokens returns the SessionOption overriding the summarizer
// output cap, in tokens (the default 0.8 × Reserve). Zero or negative
// keeps the default. A summary that reaches the cap is not stored: it
// fails as ErrSummaryTruncated through the retry-and-fallback chain.
func SummaryMaxTokens(n int64) SessionOption { return summaryMaxTokensOption(n) }

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

type summaryInstructionsOption string

func (o summaryInstructionsOption) applyCompact(c *compactCall) {
	if o != "" {
		c.instructions = string(o)
	}
}

// SummaryInstructions returns the CompactOption appending per-call
// instructions to this compaction's summary prompt: s.Compact(ctx,
// thread.SummaryInstructions("focus on the API design")). They guide
// the summarizer for this one call; the agent's own instructions
// (weft.Instructions) are untouched.
func SummaryInstructions(text string) CompactOption { return summaryInstructionsOption(text) }

// ── Layer 3 — swap the parts ────────────────────────────────────────

// SummaryInput is what a Summarizer is asked to summarize: Messages is
// the range, already serialized for summarizing (tool results capped,
// signed reasoning dropped, files as names); PrevSummary is the
// previous summary when one exists — the iterative chain's last link,
// which the new summary must carry forward; Instructions is the
// per-call text (SummaryInstructions, possibly edited by BeforeCompact);
// MaxTokens the output cap; Reason why the compaction runs.
// SystemPrompt is the prompt the default summarizer would send — the
// skeleton or SummaryPrompt, then SummaryFocus and Instructions —
// handed over so a custom Summarizer can reuse it; it may ignore it.
type SummaryInput struct {
	Messages     []weft.Message
	PrevSummary  string
	Instructions string
	MaxTokens    int64
	Reason       Reason
	SystemPrompt string
}

// Summary is a summarizer's output: the text, and what it cost.
type Summary struct {
	// Text is the summary. Empty text is a failed summary.
	Text string
	// Usage and Model are what the summary cost and which model made
	// it — the cost ledger's inputs; a custom Summarizer that runs no
	// model leaves them zero.
	Usage weft.Usage
	Model weft.ModelInfo
}

// Summarizer produces the summary text and nothing else: the cut, the
// serialization, the entry and the retry chain stay the session's. The
// default runs a model (SummaryModel, then the session's own). A
// Summarizer that knows its output was cut short returns an error
// wrapping ErrSummaryTruncated. It runs without the session lock and
// may call the session; a panic in it fails the compaction.
type Summarizer interface {
	Summarize(ctx context.Context, in SummaryInput) (Summary, error)
}

// Preparation is a computed compaction on its way to becoming one:
// what the algorithm, the BeforeCompact hook and a custom Compactor
// see.
//
// The hook may edit four fields, and the compaction proceeds with the
// edited values: Messages (the range to summarize — redact here),
// Instructions, Pinned, and FirstKept. An edited FirstKept must name a
// user or assistant message on the leaf's path, past the previous
// compaction's boundary, that does not split a tool call from its
// result; when the hook moves it and leaves Messages alone, the range
// is rebuilt for the new boundary. Reason, Context, PrevSummary and
// TokensBefore are the session's facts: edits to them are ignored.
type Preparation struct {
	// Reason is why the compaction runs.
	Reason Reason
	// Context is the context the model is shown now — the compacted
	// view at the leaf: the previous summary, pinned entries, the kept
	// tail with recorded trims applied.
	Context []weft.Message
	// Messages is the range this compaction summarizes: the entries
	// from the previous boundary up to FirstKept, as stored. A split
	// turn's prefix is part of it (one pass, ADR 0020's 2026-09-29
	// amendment).
	Messages []weft.Message
	// PrevSummary is the iterative chain's last link: the latest
	// summary compaction's text on the path, trims skipped.
	PrevSummary string
	// FirstKept is the id of the first entry kept raw.
	FirstKept string
	// TokensBefore is the estimated size of Context.
	TokensBefore int64
	// Instructions is the per-call text (SummaryInstructions).
	Instructions string
	// Pinned lists the pinned entry ids below the cut that the context
	// keeps showing after the summary.
	Pinned []string
}

// Compactor is the whole compaction algorithm, swapped in whole: it
// receives the Preparation (after BeforeCompact's edits) and returns
// the Compaction to write. The default serializes, summarizes and
// hashes; a replacement may ignore the suggested cut and keep any
// FirstKept on the leaf's path past the previous boundary. A returned
// Compaction with no Reason takes the Preparation's. It runs without
// the session lock and may call the session; a panic in it fails the
// compaction.
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
// SummaryModel and PreferNative do not apply under it; CheckSummary
// does (one retry, then the compaction fails). A nil s is ignored.
func WithSummarizer(s Summarizer) SessionOption { return withSummarizerOption{s} }

type withCompactorOption struct{ c Compactor }

func (o withCompactorOption) applySession(c *sessionConfig) {
	if o.c != nil {
		c.compaction.compactor = o.c
	}
}

// WithCompactor returns the SessionOption replacing the whole
// algorithm past the cut: the summary layers (SummaryModel,
// WithSummarizer, CheckSummary, PreferNative) do not run under it. A
// nil c is ignored.
func WithCompactor(c Compactor) SessionOption { return withCompactorOption{c} }

// Trimmer is the cheap pre-pass: before summarizing, replace old tool
// results with a stub, and the context may fit again (Anthropic
// clear_tool_uses, Vercel pruneMessages). It runs on the automatic
// path only — a manual Compact summarizes.
//
// Trim receives the context the model is shown (a copy it may edit)
// and returns the trimmed context. The session diffs the two and
// persists the difference as the compaction entry's trim record, which
// is what every later context build replays — the Trimmer itself is
// never consulted on read. The representable change is exactly one:
// replace a tool result's Content (and IsError); the messages, their
// order, every other part, and each result's CallID and Name must come
// back as given. Any other change fails the trim with
// ErrInvalidCompaction — reported through the logger and
// CompactFailed — and the summary compaction runs instead. A trim
// that changes nothing, or that does not bring the estimated context
// under window − Reserve, writes nothing and the summary compaction
// runs.
//
// Trim runs without the session lock and may call the session; an
// error or a panic is logged and the summary compaction runs.
type Trimmer interface {
	Trim(ctx context.Context, msgs []weft.Message) ([]weft.Message, error)
}

type withTrimmerOption struct{ t Trimmer }

func (o withTrimmerOption) applySession(c *sessionConfig) {
	if o.t != nil {
		c.compaction.trimmer = o.t
	}
}

// WithTrimmer returns the SessionOption setting a custom trimmer. The
// default trimmer is off; ClearOldToolResults turns a built-in one on.
// A nil t is ignored.
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

func (t clearResultsTrimmer) Trim(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
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
	for _, p := range parts {
		if keep[p] {
			continue
		}
		m := out[p.msg]
		if r, ok := m.Content[p.part].(weft.ToolResultPart); ok {
			stubbed := weft.ToolResultPart{CallID: r.CallID, Name: r.Name, Content: clearedResultStub(r.CallID, r.Name)}
			content := make([]weft.Part, len(m.Content))
			copy(content, m.Content)
			content[p.part] = stubbed
			m.Content = content
			out[p.msg] = m
		}
	}
	return out, nil
}

type clearOldToolResultsOption int

func (o clearOldToolResultsOption) applySession(c *sessionConfig) {
	if o >= 0 {
		c.compaction.trimmer = clearResultsTrimmer{keepLast: int(o)}
	}
}

// ClearOldToolResults returns the SessionOption turning on the built-in
// trimmer: when the trigger fires, every tool result in the context
// except the newest keepLast is replaced with the stub naming the call
// ("[cleared tool result NAME CALLID]"). If the trimmed context fits
// window − Reserve, no summary is made and a compaction entry with
// Reason trim lands instead, its Trim record naming each stubbed
// result — the record, not this option, is what later context builds
// replay, so reopening with another keepLast (or none) never changes
// what a recorded trim shows. A negative keepLast is ignored.
func ClearOldToolResults(keepLast int) SessionOption { return clearOldToolResultsOption(keepLast) }

// ── Layer 4 — hooks ─────────────────────────────────────────────────

// Verdict is what a BeforeCompact hook returns: Proceed with the
// computed compaction, Cancel it, or Replace it with a hook-made one
// (recorded from_hook). The zero Verdict proceeds.
type Verdict struct {
	action   int // 0 proceed, 1 cancel, 2 replace
	replaces *Compaction
}

// Proceed returns the Verdict that runs the compaction as prepared —
// with whatever the hook edited on the Preparation.
func Proceed() Verdict { return Verdict{} }

// Cancel returns the Verdict that stops the compaction: nothing is
// written, and the call fails with ErrCompactCanceled (the automatic
// path stays quiet about it).
func Cancel() Verdict { return Verdict{action: 1} }

// Replace returns the Verdict that writes c instead — the hook's own
// summary, recorded with Reason from_hook. c.FirstKept must name an
// entry the session holds; ApplyCompaction's rules decide the rest.
func Replace(c *Compaction) Verdict { return Verdict{action: 2, replaces: c} }

func (v Verdict) isReplace() bool          { return v.action == 2 }
func (v Verdict) isCancel() bool           { return v.action == 1 }
func (v Verdict) replacement() *Compaction { return v.replaces }

type beforeCompactOption func(context.Context, *Preparation) (Verdict, error)

func (o beforeCompactOption) applySession(c *sessionConfig) { c.compaction.before = o }

// BeforeCompact returns the SessionOption setting the hook that sees
// every computed summary compaction before the summarizer runs, told
// the reason, and decides: Proceed (with its edits to the Preparation
// — see Preparation for the editable fields; a redaction hook rewrites
// p.Messages), Cancel, or Replace with a hook-made summary. A hook
// error or panic fails the compaction with that error. A trim does
// not pass through it: a trim summarizes nothing.
//
// fn runs without the session lock; it may call the session.
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
// not the plan. It runs for every entry, however it was written:
// Compact, ApplyCompaction, the automatic trigger, the overflow
// re-run, and a trim (e.Reason is ReasonTrim and e.Trim holds the
// record). A panic in fn is contained and logged.
//
// fn runs without the session lock; it may call the session — the
// Context it reads already shows the compaction.
func AfterCompact(fn func(ctx context.Context, e CompactionEntry)) SessionOption {
	if fn == nil {
		return nil
	}
	return afterCompactOption(fn)
}

type compactFailedOption func(context.Context, Reason, error)

func (o compactFailedOption) applySession(c *sessionConfig) { c.compaction.failed = o }

// CompactFailed returns the SessionOption setting the hook that runs
// when a compaction that was to be written is not — the session is
// unchanged, and the caller is told why, with the reason it was
// attempted for. That covers Compact and the automatic paths failing
// at any stage after every fallback (the hook, the Compactor, the
// summary chain, an unrepresentable trim), and ApplyCompaction failing
// to validate or store its entry. It does not run for
// PreviewCompaction — a dry run writes nothing and returns its error
// to its caller — nor for the refusals that attempt nothing:
// ErrNothingToCompact, ErrCompactCanceled, ErrBusy and
// ErrAwaitingApproval. A panic in fn is contained and logged.
//
// fn runs without the session lock; it may call the session.
func CompactFailed(fn func(ctx context.Context, r Reason, err error)) SessionOption {
	if fn == nil {
		return nil
	}
	return compactFailedOption(fn)
}

type checkSummaryOption func(Summary) error

func (o checkSummaryOption) applySession(c *sessionConfig) { c.compaction.check = o }

// CheckSummary returns the SessionOption validating each summary — the
// headings-present, length-floor checks a careful caller writes. A
// failure retries the same summarizer once, then falls back down the
// chain (SummaryModel → session model → no compaction: the error is
// returned and reported through CompactFailed). A summary cut off at
// the output cap never reaches fn: it is rejected as
// ErrSummaryTruncated on the same retry-and-fallback road.
//
// fn runs without the session lock; it may call the session. A panic
// in fn counts as a rejection.
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
// through middleware. Only the returned message's text is kept: it is
// stored as the summary, shown behind the same marker as any other,
// and the entry records the model that made it. An opaque
// provider-native compaction item is not stored or replayed — that
// part of ADR 0020 §7 is not implemented. On a model without the
// interface, an error (logged) or an empty text, the text summary
// runs: the seam, with the fallback.
func PreferNative() SessionOption { return preferNativeOption{} }

// maxModelChain bounds the native lookup's walk. Comparable chains
// terminate on their own — a repeated model ends the walk — but a
// non-comparable Model value (a struct wrapper with a slice field)
// can never compare equal to itself, so a self-wrapping one would
// walk forever; the cap ends it, and nil (the text summary's
// fallback) is the answer.
const maxModelChain = 64

// nativeOf walks m's middleware chain looking for a NativeCompactor.
// The walk remembers what it has seen: middleware whose Unwrap loops
// (a wrapper returning itself, or a cycle) ends the walk instead of
// hanging it — by comparison when the value is comparable, and by the
// depth cap when it is not. The seen list is a slice, not a map — a
// Model value need not be hashable (anything but a pointer or another
// comparable kind would panic the map), and chains are short.
func nativeOf(m weft.Model) NativeCompactor {
	var seen []weft.Model
	for m != nil {
		if len(seen) >= maxModelChain {
			return nil
		}
		known := false
		for _, s := range seen {
			// reflect, not ==: interface comparison panics on a
			// non-comparable dynamic type, and a Model value need not
			// be comparable.
			if equalModel(s, m) {
				known = true
				break
			}
		}
		if known {
			return nil
		}
		seen = append(seen, m)
		if nc, ok := m.(NativeCompactor); ok {
			return nc
		}
		m = weft.Unwrap(m)
	}
	return nil
}

// equalModel compares two Model interface values without hashing:
// true when they hold identical dynamic types and those types are
// comparable (a struct wrapper with a slice field is not, and reads
// as never-equal rather than panicking).
func equalModel(a, b weft.Model) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	return reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && reflect.ValueOf(a).Equal(reflect.ValueOf(b))
}

// ── Extras — pinning ────────────────────────────────────────────────

// pinKind is the reserved custom-entry kind a Pin writes: its data is
// the pinned entry's id as a JSON string. A reserved prefix the format
// owns; callers' kinds stay their own.
const pinKind = "weft/pin"

// Pin marks an entry to stay in the model's context through every
// compaction — a requirement, a key decision — by appending a pin
// record (a custom entry, which survives compaction the way everything
// custom does). A pin does not constrain the cut: compactions
// summarize past a pinned entry like any other, record its id in the
// entry's Pinned list, and the context re-includes the pinned
// message, raw, right after the summary — so a pin near the root never
// holds the whole context raw.
//
// Only an entry that contributes a message to the context can be
// pinned — a message, a custom_message or a branch_summary; any other
// kind fails with ErrNotPinnable, and an id the session does not hold
// with ErrNoEntry. A pin is read when a compaction is computed: one
// made on an entry already below the boundary takes effect at the
// next compaction, not immediately.
func (s *Session) Pin(ctx context.Context, entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.byID[entryID]
	if !ok {
		return fmt.Errorf("%w: session %s holds no entry %q", ErrNoEntry, s.header.ID, entryID)
	}
	if _, ok := contextMessage(s.order[i]); !ok {
		return fmt.Errorf("%w: entry %q carries no message the context could keep showing", ErrNotPinnable, entryID)
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
func (c *compactConfig) maxTokens() int64 {
	if c.summaryMaxTokens > 0 {
		return c.summaryMaxTokens
	}
	return int64(float64(c.reserve) * summaryCapShare)
}
