# ADR 0002 — The error model

- Status: decided (v0 skeleton, 2026-09-09)
- Phase-1 blocker from THE-END-GOAL.md: retries, model-visible retry hints,
  and approvals all hang off this decision.

## Context

`errgroup` semantics (first error cancels siblings) are wrong for agent
loops: a failing tool must not cancel its siblings, and the model must see
the failure to recover. Every serious framework behaves this way, and the
distinction is the core of weft's error model.

## Decision

**One rule: a tool error is data the model sees; a run error is a Go error
the caller sees.**

Tool errors (any of these):

- a handler returning a non-nil error,
- a handler panicking,
- the model naming an unknown tool,
- arguments that don't decode into the tool's input type,

become `ToolResultPart{IsError: true}` carrying the failure text, fed back
to the model. Siblings in the same step always run to completion. The run
continues. These conditions never surface to the caller as Go errors, so
the model gets exactly one chance-free, uniform way to perceive and correct
them. The same dispatch is public as `Agent.CallTool`, which does *not*
contain failures: it returns errors wrapping `ErrNoSuchTool` /
`ErrInvalidToolInput` and lets handler errors propagate — the seam for
manual dispatchers and future tool middleware.

Run errors — the only things a caller ever sees, always as `*RunError`
with `Step`, the cause, and the partial-transcript `Result`:

- model stream failure (provider error),
- context cancellation,
- step-budget exhaustion (`ErrMaxSteps`).

All sentinels; callers use `errors.Is` / `errors.As`, never string matching.

Precedence: if the context is canceled during the last allowed step,
the run reports the cancellation, not `ErrMaxSteps` — the cause wins over
the budget.

**Stop conditions vs the step budget** (AI SDK `stopWhen`, Mastra, Crush
loop-detection-as-`StopWhen`, Pydantic AI `UsageLimits`):

- `StopWhen(conds...)` is the *intended* end of a run. Conditions are
  composable predicates over the steps so far (`HasToolCall`,
  `StepCountIs`, custom); when one is met after a step's tools have run,
  the run ends **successfully** with `RunFinish` — no further model call.
- `MaxSteps(n)` (default 10) is the *safety budget*. Reaching it is a
  **failure** (`ErrMaxSteps`): Mastra shipped with no default cap and
  Pydantic AI raises on limits; a runaway loop that looks like success is
  the worst outcome. The two mechanisms are deliberately not merged.

Note on `MaxSteps(N)`: the Nth step's tool calls **are executed** before
`ErrMaxSteps` is returned (their results are on `RunError.Result`), so a
caller can resume from the partial transcript. This matches the AI SDK.
A side-effecting tool can therefore fire on a step whose model reply the
run never sees; size the budget with that in mind.

**Scoped tool context** (AI SDK `toolsContext`, Pydantic AI
`RunContext`): the ctx a handler receives carries `Call{RunID, Step,
CallID, Name}` via `CallFromContext` — per-call identity without changing
the MCP-compatible handler signature. Audit, idempotency keys and
progress reporting hang off it.

## Amendment (2026-09-09, from REVIEW-v0 findings 9–10; truncation
## decision per TODO §5.6)

Two model-visible behaviors added with the review fixes, both narrowing
what can silently pass:

- **A `max_tokens` finish is recorded, not fatal** (TODO §5.6 decided
  against a run error): the run succeeds and the last step's
  `StopReason` is surfaced on `RunResult.StopReason`, so a caller can
  branch on truncation without indexing into steps. A `max_tokens` step
  *with* tool calls still executes — arguments truncated into
  undecodable JSON already come back as `ErrInvalidToolInput` results
  the model recovers from. Considered and rejected: failing the run with
  an `ErrTruncated` sentinel (the REVIEW-v0 recommendation) — it would
  grow the run-error catalogue for a condition providers themselves do
  not treat as an error, and the caller is better placed than the loop
  to decide what a truncated reply means.
- **Tool results are capped.** The loop caps a tool result's text at
  64 KiB by default (`weft.MaxResultBytes(n)` to change, `0` to
  disable) — over successes, failures, and panic text alike — cutting on
  a rune boundary and appending the marker `\n…[truncated N bytes]`.
  The marker is model-visible contract: the model must be able to tell
  a truncated output from a complete one. A runaway tool can no longer
  silently fill the context window. `Agent.CallTool` returns uncapped
  output; the cap is a run policy applied by the loop.

Also from the review fixes (TODO §2.5): a Model stream that violates
the contract documented on `Model` — ending without `ModelFinish`,
yielding after it, a tool call with an empty ID or name, or panicking —
fails the run wrapping **`ErrModelContract`**. Adapter bugs are run
errors, and loud.

With TODO §2.3, the catalogue gains **`ErrUnsupported`**: a Model that
cannot honour part of a request (a `FilePart` media type the provider
does not accept, a feature the vendor lacks) fails the model call
wrapping it — a run error the caller can `errors.Is` to fall back to
another model. Capability gaps are loud, never silent truncation.

## Amendment (2026-09-10, from TODO §3.6, §3.7, §9.1 — the adapter-era
## error additions)

Three decisions landed with the provider adapters:

- **Idle vs hard timeouts** (Crush `request_timeout.go`: "a slow but
  actively streaming response is never killed"). The ctx deadline is the
  hard limit on a whole model call; an adapter's `IdleTimeout` option is
  the maximum gap between two stream chunks. Idle expiry ends the stream
  wrapping **`ErrStreamIdle`** — a run error like any provider error, so
  callers branch on it provider-agnostically with `errors.Is`. The two
  conditions have distinct sentinels and distinct error text; a slow but
  actively streaming response is never killed by the idle timeout, and
  the SDK's whole-response request timeout is not used for streaming.
- **Retry stance** (LangGraph: retry transport, not logic). Transport
  retries (429, 5xx, `net.Error`) belong to the vendor SDK's retry
  configuration, surfaced through each adapter's options (e.g.
  `openai.MaxRetries`). **Weft's loop never retries a model call.** Logic
  retries (fallback model, JSON repair) are model-seam middleware
  (TODO §4.1), not loop behaviour.
- **The kill switch** (Pydantic AI `ALLOW_MODEL_REQUESTS`). With
  `WEFT_MODEL_REQUESTS=deny`, every first-party adapter yields
  **`ErrModelRequestsDenied`** from `Stream` before any network I/O, and
  `weft.ModelRequestsAllowed()` reports the setting — checked on every
  call, not cached, so test suites toggle it per test with `t.Setenv`
  and the package keeps no state. Test suites that must never reach the
  network set it once and get a loud failure if any adapter slips
  through. `wefttest` models ignore it: ordinary offline tests are
  unaffected.

`ModelFinish.Raw` also joins the catalogue's edges: the provider's own
stop reason when the mapped `StopReason` had to be approximated
("refusal", "pause_turn", "content_filter"). It is recorded on
`StepRecord.RawStopReason` and the `StepFinish` event and never
interpreted — a vendor value surfaces without a vendor-specific field on
a core type.

## Alternatives considered

- **errgroup abort on first tool error**: cancels unrelated work the model
  declared independent; rejected outright.
- **Tool errors as run errors with a "continueOnError" flag**: makes the
  common case configurable and the semantics per-call; the uniform rule is
  simpler to document and test.

## Consequences

- Handlers are ordinary Go functions; error and panic containment is the
  loop's job, not the handler author's.
- Cancellation is the only cross-tool abort: tools that want it honor ctx.
- Future `ModelRetry`-style hint errors (Pydantic AI pattern) slot in as a
  typed tool error the loop marks specially — additive, no model change.
