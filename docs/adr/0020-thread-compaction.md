# ADR 0020 — Compaction: summarize the old, keep everything, configure every layer

- Status: decided (2026-09-28, TODO §14 "Compaction"; ships in
  `weft/thread` v0.1, overflow in v0.3 — plan §3.7, §6)
- Depends on: ADR 0001 (messages; `ReasoningPart.Signature`), ADR 0006
  (seams), ADR 0011 (entries, the tree, versioning), ADR 0013 (the
  adapter contract; the cache breakpoint kept for the summary block)
- Core additions it needs: `Agent.Model()` (root v0.3.7, before thread
  v0.1); `ErrContextOverflow` (root v0.4.0, before thread v0.3)

## Context

A session outgrows the model's context window. The field's answer is
compaction: summarize the older part, keep the recent part raw, and show
the model summary + recent. Everything about it is a trade-off between
cost, cache economics and what the model can still do after.

The survey (2026-09-28) — defaults and knobs:

| Framework | Summary model default | Configurable | Trigger | Hooks |
|---|---|---|---|---|
| pi | session model | via hook | `ctx > window − 16384`, keep 20k, per-model overrides | before (cancel/replace), after, failed |
| Crush | main model | no | ≤20k left, or 20% of window; none if window unknown | no |
| Claude Code | session model | prompt only (`/compact <focus>`) | ~83% of window | PreCompact (block), PostCompact |
| Codex CLI | session model | `compact_prompt` | `model_auto_compact_token_limit` | no |
| LangChain v1 | required argument | model, prompt, trigger, keep, token counter | fraction / tokens / messages | the middleware is the strategy |
| DeerFlow | run model; override falls back to it | model, prompt | as LangChain, footgun checks | `CompactionEvent` with hashes |
| Mastra OM | cheap model (flash) | model, fallback list, instruction | 30k observe / 40k reflect | processors |
| Google ADK | configured `LlmEventSummarizer` | model, template | interval + overlap, or tokens + retention | custom summarizer |
| Anthropic / OpenAI APIs | server-side | instructions (Anthropic) | input tokens threshold | opaque compaction item |

The scars (TODO §14): signed reasoning before the cut must be dropped
(pi #9391, Anthropic `prefix_binding_mismatch`); constant
chars-per-token triggers wedged sessions and caused a ~400k-token
destructive compaction (pi #9409/#9476/#9482); branch summaries share no
cache prefix (#9411); destructive-only compaction lost users' history
(Crush #2240/#3620).

## Decision

### 1. What a compaction is

A `compaction` entry (ADR 0011 §2) appended at the leaf: the summary
text, the id of the first entry kept raw, tokens before, the reason
(manual | threshold | overflow), the summarizer's usage and model,
details (files read, files modified, pinned ids), and a hash of the
summarized range. **Nothing is deleted.** The context at a leaf is:
the latest compaction on the path's summary, then the entries from its
first-kept id onward. A second compaction summarizes from the previous
kept boundary and is given the previous summary (iterative). Undo is a
branch back to the entry before the compaction (`s.Uncompact(ctx)`
appends a `leaf` entry — no rewrite).

### 2. The default algorithm (zero configuration)

- **Trigger**: before each `Send` and after each turn, when
  `lastInputTokens + estimate(new since) > window − Reserve`.
  `lastInputTokens` is the provider-reported input of the last step
  (`StepRecord.Usage`, input + cached); only the messages added since
  are estimated. **Never** a constant chars-per-token as the signal.
- **Window**: from `ContextWindow(n)` (per session, or per model via
  `ModelWindows(map)`), later from the model catalog (TODO §5.13). With
  no window known: **no automatic compaction**; one warning through the
  agent's logger; manual and overflow compaction still work.
- **Cut**: walk back from the leaf keeping ~`KeepRecent` tokens; cut
  only at a user or assistant boundary, **never between a tool call and
  its result**. A single turn larger than `KeepRecent` is split at an
  assistant message and its prefix summarized separately (pi's split
  turn), then merged.
- **Serialize** the range as a plain transcript for the summarizer (so
  it summarizes instead of continuing the conversation): tool results
  truncated to 2,000 characters, `ReasoningPart`s with a `Signature`
  dropped, files reduced to their names.
- **Summary skeleton** (model-visible, pinned by golden test): Goal /
  Constraints / Progress / Key Decisions / Next Steps / Critical
  Context, then cumulative files-read and files-modified lists.
- **Summarizer model**: the session agent's model (`Agent.Model()`,
  middleware included, so retry/fallback apply), with prompt-cache
  writes off, output capped at 0.8 × `Reserve`. This is what pi, Crush,
  Claude Code and Codex do: a weak summary costs more in rework than a
  cheap model saves.
- **Kept turns**: reasoning parts with a `Signature` older than the
  compaction are stripped from the context the model sees, unless the
  adapter declares them safe to resend.
- **Placement**: the summary goes first in the context, as a user
  message wrapped in a fixed marker, right after the system prompt, so
  the system prompt's cache prefix survives (ADR 0013 keeps a breakpoint
  free for it).

Defaults: `Reserve` 16,384, `KeepRecent` 20,000, summary output cap
0.8 × reserve, tool-result truncation 2,000 characters.

### 3. Configuration — five layers, all in v0.1

```go
thread.Create(ctx, st, agent,
	thread.Compaction(
		// Layer 1 — knobs
		thread.ContextWindow(200_000), thread.ModelWindows(windows),
		thread.Reserve(16_384), thread.KeepRecent(20_000),
		thread.TriggerFunc(func(in thread.TriggerInput) bool { … }),
		thread.MinTurnsBetween(2), thread.MaxPerSession(0),
		thread.Estimator(est), // provider-aware, replaceable
		thread.Disabled(),     // manual only
		// Layer 2 — the summary
		thread.SummaryModel(cheap), // falls back to the session model on failure
		thread.SummaryPrompt(tmpl), // replace the template
		thread.SummaryFocus("keep every file path"), // append instructions
		thread.SummaryMaxTokens(n),
		// Layer 3 — swap the parts
		thread.WithSummarizer(s), // just the text
		thread.WithCompactor(c),  // the whole algorithm
		thread.WithTrimmer(t),    // a cheap pre-pass (below)
		// Layer 4 — hooks
		thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) { … }),
		thread.AfterCompact(func(ctx context.Context, e thread.CompactionEntry) { … }),
		thread.CompactFailed(func(ctx context.Context, r thread.Reason, err error) { … }),
		thread.CheckSummary(func(s thread.Summary) error { … }),
		// Layer 5 — provider-native
		thread.PreferNative(),
	),
)
```

- **Interfaces** (small, root types only):
  `Summarizer{Summarize(ctx, SummaryInput) (Summary, error)}`;
  `Compactor{Compact(ctx, Preparation) (*Compaction, error)}`;
  `Trimmer{Trim(ctx, []weft.Message) ([]weft.Message, TrimReport)}`;
  `Estimator{Estimate([]weft.Message) int}`.
- **`Preparation`** carries the reason, the messages to summarize, the
  split-turn prefix, the previous summary, the first kept id, tokens
  before, the per-call instructions, pinned entries.
- **`BeforeCompact`** returns `Proceed`, `Cancel`, or
  `Replace(*Compaction)` (a hook-made summary, recorded `from_hook`).
- **Per-call**: `s.Compact(ctx, thread.Instructions("focus on the API
  design"))`, `s.PreviewCompaction(ctx, …)` (a `*Compaction` with no
  write), `s.ApplyCompaction(ctx, c)`.

### 4. More proposals, decided with the layers

- **Pinning**: `s.Pin(ctx, entryID)` keeps an entry in the context
  through every compaction (a requirement, a key decision); `custom`
  entries always survive. DeerFlow's "compaction-surviving ledgers" and
  pi's `custom` vs `custom_message` split, made explicit.
- **Trimmer pre-pass**: before summarizing, a cheap trim may be enough —
  replace old tool results with a stub naming the call (Anthropic
  `clear_tool_uses`, Vercel `pruneMessages`). The default trimmer is
  off; `thread.ClearOldToolResults(keepLast int)` turns it on. If the
  trim brings the context under the threshold, no summary is made and a
  lighter `trim` record lands in the compaction entry.
- **Summary check**: `CheckSummary` validates the summary (the skeleton
  headings present, a length floor); a failure retries once, then falls
  back (cheap model → session model → keep the context uncompacted and
  report via `CompactFailed`). A failed compaction never loses entries.
- **Rate limits**: `MinTurnsBetween` and `MaxPerSession` stop
  compaction thrash (a context that re-crosses the line every turn).
- **Cost ledger**: the summarizer's usage is recorded in the entry and
  counted in `s.Usage()` under its own bucket, never mixed with turns.
- **Observability**: every compaction is logged through the agent's
  logger and carries a `trace` attribute set; a Studio view of a
  session's compactions is a later studio feature reading the entries.
- **Per-model overrides**: `ModelWindows` and `ModelReserve` keyed by
  `ModelInfo` (provider + name), pi's per-model settings.

### 5. Overflow (thread v0.3)

A turn failing with `weft.ErrContextOverflow` (root v0.4.0: an exported
sentinel each adapter maps; `mw.Retry` never retries it) compacts with
reason `overflow` and re-runs the turn once. If the re-run overflows
again, the turn fails with both errors joined.

### 6. Branch summaries

`s.Branch(ctx, id, thread.SummarizeLeft())` summarizes the branch being
left back to the common ancestor with the same summarizer and writes a
`branch_summary` entry. Branch summaries share no cache prefix with the
main line (#9411): documented as a cost, not hidden.

### 7. Provider-native compaction — the seam

```go
// NativeCompactor is implemented by adapters whose provider compacts
// server-side. Root types only, so an adapter implements it without
// importing thread.
type NativeCompactor interface {
	CompactNative(ctx context.Context, req weft.ModelRequest, instructions string) (weft.Message, weft.Usage, error)
}
```

With `PreferNative()`, thread looks for it on the summary model,
following `Unwrap() weft.Model` through middleware; the returned
message (possibly an opaque provider part) is stored as the summary and
the entry records the provider that can replay it. On any other model,
on a model switch, or on error, the text summary is used. Adapter
implementations (Anthropic `compact_*`, OpenAI `/responses/compact`)
land in the adapters' next lockstep release; thread v0.1 ships the seam
and the fallback.

## Consequences

- Zero configuration works once a window is known; nothing is ever lost;
  every layer can be replaced without forking.
- Two small core additions, each in its own release: `Agent.Model()`
  (v0.3.7) and `ErrContextOverflow` (v0.4.0). An `Unwrap() Model`
  convention for middleware joins `InfoOf`'s.
- The summary text, its marker and the stub text of cleared tool
  results are model-visible bytes: golden-tested.

## Rejected

- **Destructive compaction** (Crush) — history is the user's.
- **A default cheap model** (Mastra) — weft has no model registry to
  pick one, and quality decides long sessions; one option away instead.
- **A chars-per-token trigger** — the pi scars.
- **Assuming a window** — wrong for small and huge models alike.

## Amendment (2026-09-29 — the split turn is summarized in one pass)

§2 says a single turn larger than `KeepRecent` "is split at an
assistant message and its prefix summarized separately, then merged".
The implementation folds the split prefix into the same summarized
range as the older entries — one pass over the whole range, one model
call, one merged summary text. The merged result §2 asked for is what
lands; only the road there differs (pi runs two summarizer calls, weft
one — the cheaper shape for the same output, at the cost of the
summarizer seeing the split prefix as ordinary range content).
`Preparation.SplitPrefix` stays declared for a future two-pass mode
and is always nil today, as its doc says. Found by the 2026-09-29
review of the thread v0.2 branch; documented here rather than changed,
per the standing rule that an ADR divergence is decided, not drifted.

## Amendment 2026-10-01 — the context shape, the trim record, the hook rules, and what is not built

Found by the 2026-10-01 review of `weft/thread`
(`WEFT-THREAD-REVIEW-2026-10-01.md`). Pre-1.0, so the API and
the wire moved where the fix needed it; each point below is pinned by a
test, and the model-visible ones by goldens in
`thread/testdata/compaction` and `thread/testdata/format5`.

### A. The compacted context, exactly

With a summary compaction on the leaf's path, `Session.Context()` is, in
this order:

1. **The summary message** — one user message with one text part:
   `<weft-summary>\n` + summary + `\n</weft-summary>` (golden
   `summary-marker.txt`). It is the *latest summary compaction's*; a
   trim record above it never replaces it.
2. **The pinned entries** that compaction recorded (`Pinned`), raw, in
   path order — the message-kind entries below the boundary. A pin does
   not constrain the cut (§4 said "keeps an entry in the context"; the
   mechanism is re-inclusion here, not a held-back cut). A pin made on
   an entry already below the boundary takes effect at the next
   compaction. `Pin` refuses entries that carry no message
   (`ErrNotPinnable`).
3. **The kept tail**: the entries from `first_kept` onward. Entries
   recorded before the latest compaction or trim lose their signed
   reasoning parts; every trim record above the compaction is replayed
   (C below).
4. **Branch summaries** ride where they sit on the path, each as the
   same marked user message.

The whole shape is pinned by `compacted-context-full.txt` (a summary, a
pinned entry, a trim record and a branch summary together).

**Branch summaries share the `<weft-summary>` marker** with compaction
summaries. A distinguishing attribute (`<weft-summary kind="branch">`)
was considered and not taken in this change: it changes model-visible
text that tests outside the compaction files pin, and whether a model
benefits from telling the two apart has not been measured. It stays an
open question for its own decision.

A summary compaction whose `first_kept` the path does not reach below
it (a hand-made file; `ApplyCompaction` never writes one) is skipped
with one warning and the next older usable compaction governs — the
whole path, raw, when there is none. The walk never shows a summary on
top of the range it was meant to replace.

### B. `TokensBefore` and `Preparation.Context`

Both describe **what the model was shown** — the compacted view of A —
not the raw path. After a first compaction the two differ by everything
the summary replaced; counting the raw path overstated every later
entry.

### C. The trim record and its replay rule

A trim is a compaction entry with no summary, reason `trim`, and a
**trim record**:

```json
{"type":"compaction","v":5,…,"reason":"trim",
 "trim":{"stubs":[{"entry":"e_…","call_id":"call_1","content":"[cleared tool result read call_1]"}]}}
```

- Each stub names the entry holding a tool result, the call it answers,
  and the content the model sees in its place (`is_error` when set).
- **Replay rule**: the context walk applies exactly the stubs of every
  trim record above the governing summary compaction, oldest first (a
  later record wins for the same result). It never consults the
  session's configured `Trimmer`: a recorded trim reads the same under
  any options, in any process. A summary compaction supersedes the
  trims below it — its kept tail reads raw again.
- **Where the record comes from**: the `Trimmer` (built-in or custom) is
  shown the model's context and returns the trimmed one; the session
  diffs the two. The representable change is one: a tool result's
  `content` (and `is_error`) replaced in place. Anything else — a
  message added, dropped or reordered, another part changed, a call id
  or name changed — fails the trim with `ErrInvalidCompaction`
  (logged, reported through `CompactFailed`) and the summary compaction
  runs instead. `Trimmer.Trim` now returns `([]weft.Message, error)`;
  `TrimReport` is gone (the record is the report).
- **Wire**: an entry carrying a trim record is written with `"v":5`, its
  minimum reader version (ADR 0011 §6) — an older reader would replay
  the trim from its own options, so it must fail loudly instead. A
  summary compaction stays a format-1 line with no `"v"`. Golden:
  `testdata/format5/compaction_trim.json`.
- **Legacy**: a trim entry written before this amendment has no record.
  It is read as it always was: under `ClearOldToolResults(n)` the walk
  re-derives the built-in stubs with the *configured* `n`; under any
  other configuration it stubs nothing. Only such old entries depend on
  options.
- A trim never moves the boundary and never feeds the iterative chain:
  the next compaction's previous summary and range start come from the
  latest *summary* compaction. (The bug this fixes: a trim's empty
  summary was taken as "previous" and the earlier summary fell out of
  the chain.)

### D. Not implemented — stated, not implied

- **The adapter "safe to resend" seam for signed reasoning** (§2, "unless
  the adapter declares them safe to resend"): there is no such
  declaration. Signed reasoning recorded before the latest compaction
  or trim is always stripped from the context.
- **Opaque provider-native summary storage** (§7, "possibly an opaque
  provider part … the entry records the provider that can replay it"):
  `PreferNative` keeps **only the returned message's text**, stored and
  shown like any other summary. No opaque part is stored or replayed,
  and no first-party adapter implements `NativeCompactor` yet. A native
  error or an empty text is logged and the text summary runs.
- **`files_modified`** (§1): nothing writes it — the sandbox write log
  it was reserved for was abandoned. The wire field stays readable;
  `Compaction.FilesModified` is removed.
- **Per-turn model windows**: `ModelWindows` / `ModelReserves` are
  resolved once, at Create or Open, for the session agent's model. A
  run that swaps its model (`weft.UseModel`) does not re-resolve.

### E. The hook lock rule

Every caller-supplied function or implementation — `TriggerFunc`,
`Estimator`, `Trimmer`, `Summarizer`, `Compactor`, `BeforeCompact`,
`AfterCompact`, `CompactFailed`, `CheckSummary` — runs **without the
session lock and may call the session**. (The bug: `AfterCompact` ran
under the lock and any hook reading the session deadlocked.)

What fires when:

- `AfterCompact` runs for every landed compaction entry — manual,
  threshold, overflow, from_hook, **and trim** (`Reason` tells them
  apart).
- `CompactFailed` runs when a compaction that was to be written is not:
  `Compact` and the automatic paths failing at any stage, an
  unrepresentable trim, and `ApplyCompaction` failing to validate or
  store. It does not run for `PreviewCompaction` (a dry run), nor for
  the refusals `ErrNothingToCompact`, `ErrCompactCanceled`, `ErrBusy`,
  `ErrAwaitingApproval`.
- `BeforeCompact` may edit `Messages`, `Instructions`, `Pinned` and
  `FirstKept` on the `Preparation`; the compaction proceeds with the
  edited values (a redaction hook works). A moved `FirstKept` must be a
  valid cut past the previous boundary. `Preparation.SplitPrefix` is
  removed (always nil, see the 2026-09-29 amendment).

### F. The truncated-summary rule

A summary whose model finished with `max_tokens` is **never stored**. It
is a failed summary on the `CheckSummary` road — one retry on the same
model, then the fallback (SummaryModel → session model) — and with no
fallback left the compaction fails wrapping `ErrSummaryTruncated`. A
custom `Summarizer` reports the same condition by returning that
sentinel. An empty summary is a failure too.

### G. Smaller decisions made with these

- **Between turns only**: `Compact`, `ApplyCompaction` and `Uncompact`
  fail with `ErrBusy` while a turn is in flight (Branch's rule) — a
  manual compaction never lands between a running turn's per-step
  entries. The trigger's and the overflow re-run's own writes are the
  turn's housekeeping and pass.
- **The trigger stands down after a compaction**: a compaction or trim
  entry after the measured turn makes the reported input stale; the
  trigger waits for the next provider report instead of re-firing on a
  context that just shrank.
- **Same boundary twice**: a summary compaction naming the boundary the
  previous one left is refused (`ErrNothingToCompact`).
- **Rate limits count along the leaf's path**, not over the file: an
  abandoned branch's compactions and turns are not this line's.
- **Configuration is validated**: with a known window, `Reserve <
  window` and `KeepRecent < window − Reserve`, or Create/Open fail with
  `ErrCompactConfig` — a session that could never compact under its
  line is an error, not a silence. A trigger that fires over a tail
  that fits `KeepRecent` warns once.
- **`SummarizeLeft` summarizes the branch's compacted view** — its own
  compaction's summary plus the entries from that compaction's first
  kept entry — never the raw range beside the summary of that range.
- **Sentinels**: `ErrNothingToCompact`, `ErrCompactCanceled`,
  `ErrNoEntry`, `ErrSummaryTruncated`, `ErrInvalidCompaction`,
  `ErrCompactConfig`, `ErrNotPinnable`, `ErrAwaitingApproval`.
- **Names** (§3's sketch, corrected): there is no `thread.Compaction(…)`
  wrapper — the options are plain `SessionOption`s. `With*` injects an
  implementation of an interface (`WithEstimator`, `WithSummarizer`,
  `WithCompactor`, `WithTrimmer`); bare names set values.
  `thread.Disabled()` is `thread.NoAutoCompact()`; the per-call
  `thread.Instructions(…)` is `thread.SummaryInstructions(…)` (it never
  meant `weft.Instructions`); `Proceed` and `Cancel` are functions
  (`thread.Proceed()`), not reassignable package variables.
  `Estimator.Estimate` returns `int64` tokens like every other number
  here, and `SummaryMaxTokens` takes `int64`.
