# ADR 0007 — The approval boundary

- Status: decided (2026-09-14, TODO §4.4); amended 2026-09-22
  (externally-computed results, TODO §2a.5 — the amendment at the end)
- Depends on: ADR 0001 (transcript repair), ADR 0002 (tool errors have
  codes), ADR 0006 (the tool seam)

## Context

Human-in-the-loop has to work over any transport and need no
persistence in the core: a CLI prompt, an HTTP round trip, a queue with
a signed decision hours later. The AI SDK's run boundary, Pydantic AI's
`DeferredToolRequests`, and Deer-flow's HITL-as-turn-end converge on
the same shape: the run *ends*, the pending calls ride on the result,
and a later run resumes with the transcript plus the decisions. Crush
adds the lesson that a dangling call must never lock a session — which
transcript repair (ADR 0001) already handles.

## Decision

**A pending call ends the run successfully; the decision is a run
option on the next run.**

1. A tool built with `RequireApproval()` — or any call for which the
   tool chain returns an error wrapping `ErrApprovalRequired` — is
   **not executed**. The step's other calls run normally. The check
   sits at the base of the chain, so `Allow` can still deny first and
   `Audit` sees the attempt.
2. After the step's other tools finish, the run ends **successfully**
   with the parked calls on `RunResult.Pending` and `RunFinish.Pending`
   — unless the run's context was canceled, in which case cancellation
   wins, as it does everywhere else: the run fails with the ctx error
   and the parked calls ride on `RunError.Result.Pending`, so the
   transcript stays resumable. The transcript carries a tool message
   with the executed results only (none when every call is pending):
   it is intentionally dangling. A parked call has its `ToolStart` and
   **no `ToolFinish`** (ADR 0004 amendment): the model asked, the
   dispatcher handed it to the chain, and nothing finished.
3. Resume = `agt.Generate(ctx, Messages(res.Messages...), Approve(id),
   Deny(id, reason), …)`. When any decision is supplied, the calls of
   the last assistant message that have no result are exempt from
   repair and resolved before the first model call: approved calls run
   through the ordinary chain with `Call.Approved` set; denied ones get
   the result `DENIED: <reason>`; calls with **no decision** get
   `DENIED: no decision`. The results complete the earlier step's tool
   message in call order (inserted after the assistant message even
   when a new `Prompt` follows), and the loop continues. A transcript
   fed back **without** any decision is repaired as before: `no result
   recorded: the call was interrupted`.
4. Decisions are recorded in the transcript only through the resulting
   tool message — no new part types. Resumed calls emit
   `ToolStart`/`ToolFinish` into the new run and are not a
   `StepRecord` (there was no model call); `runtime` adds signed
   decisions, persistence, and workflows on top.
5. `Agent.CallTool` on a `RequireApproval` tool returns an error
   wrapping `ErrApprovalRequired`; middleware that defers calls reads
   `Call.Approved` from `CallFromContext` to let an approved resume
   through.

The denial code `DENIED` is shared with `mw.Allow`: to the model, a
refused call is a refused call, whoever refused it.

## Not a security boundary

Approval is a policy and UX seam. Per-call prompts do not contain a
compromised prompt or a malicious tool; the boundary that does is
OS-level sandboxing (§14 `SandboxFS`). Input trust — gating auto-loaded
configuration, extensions, skills — is consumer-side policy, not part
of this seam.

## Alternatives considered

- **Blocking the run on a channel/callback** until a decision arrives:
  ties the decision to the process lifetime and one transport. Rejected.
- **Approval parts in the transcript**: a new part type every provider
  adapter must skip; the tool message already records the outcome.
- **Denying undecided calls silently** or leaving them dangling: the
  model must see why a call did not happen; "no decision" is visible
  and pinned.

## Consequences

- `RunFinish` gained a slice field and is no longer `==`-comparable
  (acknowledged in `.apidiff-allow`).
- A canceled run with parked calls fails with the cancellation error
  (`RunError.Result.Pending` keeps the calls resumable) — the approval
  boundary is the one place a run could otherwise have swallowed a
  cancellation by ending "successfully".
- Resumed calls report `Step: 0` in their `Call`: the original step
  index is not recoverable from the transcript and there is no
  `StepRecord` for them. Audit lines should key on the `CallID`.
- Stop conditions do not see resumed calls (no `StepRecord`); a stop
  keyed on an approval-gated tool fires on the step that *issued* the
  call if it uses `HasToolCall`, or never if it needs the result.
  Documented; revisit if a consumer needs it.
- Model-visible strings pinned by tests: `DENIED: <reason>`,
  `DENIED: no decision`.

## Amendment (2026-09-22 — externally-computed results, TODO §2a.5)

The decision set grows a third kind: **resolve**. `weft.Resolve(callID,
content)` and `weft.ResolveError(callID, content)` resume a pending
call with a result computed outside the process — the "human as tool
executor" pattern (run the query in prod, paste what happened; Pydantic
AI's `DeferredToolResults`, LangGraph's `Command(resume=…)`). The
handler never runs; the content becomes the `ToolResultPart` verbatim
(`ResolveError` sets `IsError`), the tool message is completed in call
order, and the loop continues. `MaxResultBytes` applies to resolved
content as to any result — the cap is a transcript rule, not an
execution rule. Resolves compose with `Approve`/`Deny` in one resuming
call; the last option for an id wins; undecided calls keep
`DENIED: no decision`. A resolved call follows the denied path, not the
executed path: a result in the tool message, no `execute_tool` span,
no `ToolStart`/`ToolFinish`, and `Call.Approved` is never set —
nothing ran.

**`Resolve` on a call not in the resumed transcript's pending set is a
loud run error at step 0** — a deliberate asymmetry with
`Approve`/`Deny`, which ignore unknown ids (documented on `Approve`
since this ADR's first pass). Those are idempotent yes/no marks over an
id set, and a stale id in a resume list is harmless to ignore;
`Resolve` carries a payload the caller expects the model to see, and
dropping it silently is exactly the silent-skip behaviour the error
model forbids. No new sentinel: this is a programming error the caller
fixes, reported in the error's text.

`Resolve` is a *decision about* a parked call, not an execution
channel: `Call.Approved` stays the middleware-parking contract, and
`mcp.Serve` still refuses `RequireApproval` tools — MCP has no resolve
verb either.
