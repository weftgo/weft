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

## Amendment (2026-09-14 — fail truncated calls, TODO §5.6a; tool
## errors have codes, §5.2a; the approval boundary, §4.4)

**Fail truncated calls — a decision reversal.** The 2026-09-09
amendment let a `max_tokens` step *with* tool calls execute them,
relying on decode failure for cut arguments. That covers undecodable
arguments only: an intact call issued just before the cut would still
run, from a message the model never finished. Now, when a step
finishes `StopMaxTokens` and carries tool calls, **every call fails
and none executes**: each gets the pinned result `tool call <name> was
not executed: the response hit the output token limit`, the tool
message is appended, and the loop continues so the model retries with
a full output budget. No `ToolStart`/`ToolFinish` is emitted for them
(nothing started). `RunResult.StopReason` still records the truncation
when it is the last step. Model-visible change: the earlier
`ErrInvalidToolInput` text for cut arguments is replaced by the uniform
string. (pi `failToolCallsFromTruncatedMessage`.)

**Tool errors have codes.** `*weft.ToolError{Code, Message, Err}`
renders as `CODE: Message`; `Err` is the internal cause, reachable
through `errors.As`/`Unwrap` for middleware and audit logs and never
shown to the model. `weft.Errorf(code, format, args...)` builds one.
The loop's own failures are coded the same way — model-visible
change, pinned by tests:

| condition | before | now |
|---|---|---|
| arguments do not decode | `weft: tool input is not valid for its schema: tool "x": …` | `INVALID_INPUT: tool "x": field "days": expected integer, got string` |
| unknown tool | `weft: no tool with that name: "x"` | `NO_SUCH_TOOL: no tool named "x"` |

Both still wrap their sentinels, so `errors.Is(err, ErrInvalidToolInput)`
and `errors.Is(err, ErrNoSuchTool)` hold through `CallTool` and the
seam. Plain handler errors keep rendering as `err.Error()`;
`mw.MapErrors` codes them centrally (`INTERNAL: tool "x" failed` by
default, cause retained). Timeout, panic, and cancellation strings are
unchanged. Codes are not validated; SCREAMING_SNAKE is the convention.

**Two approval sentinels** join the catalogue (ADR 0007):
`ErrApprovalRequired` — returned by the chain to park a call; a
`RequireApproval` tool through `CallTool` — and `ErrApprovalDenied`,
the cause on a `DENIED: <reason>` result. Neither is a run error.

## Amendment (2026-09-18, from the core audit — two model-visible
## tightenings, one clarified)

- **A tool call ID repeated within one step fails the run** wrapping
  `ErrModelContract`, alongside the empty-ID and empty-name checks.
  A repeated ID made the transcript ambiguous: `Repair` keeps only
  the first result per ID, so the loop could emit a transcript its
  own repair pass rewrites, and `Approve`/`Deny` keyed on the ID
  became ambiguous. IDs may still repeat across steps.
- **Tool arguments must be exactly one JSON value.** Trailing data
  after the arguments object — `{"a":1} {"a":2}` or `{"a":1} x` —
  is now an `ErrInvalidToolInput` result (`trailing data after the
  JSON arguments`) in lenient and strict modes alike; the decoder
  previously took the first value and ignored the rest.
- **The truncation marker names the bytes omitted.**
  `\n…[truncated N bytes]` now carries N = bytes the model did not
  receive (the old marker carried the cap, which read as "N bytes
  missing" however many were cut). The shape is unchanged; this
  fixes the number, unambiguous for the model.

## Amendment (2026-09-18, remediation pass — the catalogue's fourth
## run-error class; the kill-switch clause narrowed)

- **A malformed tool-source snapshot fails the run.** The three
  classes above gain a fourth: a `ToolSource` snapshot carrying a
  duplicate name or a nil entry is the registry's bug, not something
  the model can see and correct, so the run fails wrapping
  **`ErrDuplicateTool`** or **`ErrNilTool`** (sentinels in
  `errors.go`) — always `*RunError` with the partial transcript, like
  every run error. (Spec finding R15.)
- **The kill-switch clause above is narrowed** to the clients it was
  always about: `WEFT_MODEL_REQUESTS=deny` makes every first-party
  adapter yield `ErrModelRequestsDenied` before any network I/O only
  for a client the adapter built itself from credentials. A client
  the caller injected through the adapter's `Client(c)` option stays
  reachable — ADR 0013's amended kill-switch clause records the full
  rationale.

## Amendment (2026-09-19 — budgets are checked at the continuation
## point; ErrUsageLimit, TODO §5.3)

`weft.UsageLimit(max Usage)` bounds a run's total usage — its own model
calls plus every subagent's (`RunResult.Usage`). **Budgets are checked
only when the loop would otherwise make another model call**: a step
that ends the run — final answer, `StopWhen`, pending approvals —
succeeds even if it overshot, because a budget's job is to stop further
spend, not to discard finished work. `MaxSteps` already behaved this
way (the check is the loop header). Consequence, pinned by tests: a
breach is reported with `StopReason == tool_calls` on the last step and
the transcript ends with a complete tool message. Zero fields are
unlimited; `Usage.Total()` is deliberately not a third counter
(widening `Usage` would change every adapter's folding rules). Off by
default: the right value is workload-specific — Anthropic's research
finding that token usage "explains 80 % of the variance" in quality
makes a too-low limit a correctness bug, not a saving — so the core
will not guess one; `MaxSteps` is the default budget. Breach is the
sentinel **`ErrUsageLimit`** (wrapped with the input/output numbers), a
run error with the partial transcript. A parent step that fans out to
four subagents can overshoot by four children's worth before the next
check; documented, and the reason `Timeout` on a subagent tool exists.

## Amendment (2026-09-19 — the subagent codes, TODO §5.1 / ADR 0014)

Three coded tool errors join the model-visible table, rendered by the
`Subagent` tool's handler (a child run failure is data the parent model
sees, never a parent run error):

| condition | result the parent model sees |
|---|---|
| child run failed (`*RunError`, cause on `ToolError.Err`) | `SUBAGENT_FAILED: agent "research" failed at step 3: weft: run exceeded the maximum number of steps` |
| child ended awaiting approval | `SUBAGENT_PENDING: agent "research" ended awaiting approval of 1 call(s)` |
| child already running in this call chain (refused before any model call) | `SUBAGENT_CYCLE: agent "research" is already running in this call chain` |

The quoted name is the **tool name** (ADR 0014 G3), and the constants
are exported (`CodeSubagentFailed`, `CodeSubagentPending`,
`CodeSubagentCycle`). A child cancelled by the parent's run reports
through the existing cancellation rules; a child cut off by the
subagent tool's `Timeout` renders the ordinary `tool "x" timed out
after d` string — no new strings for either.

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
