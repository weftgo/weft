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
