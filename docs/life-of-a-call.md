# Life of a step, life of a tool call

Where each thing sits, in order. **Phases are documentation; seams are
code.** A named phase here never becomes a hook — behaviour attaches at
the two middleware seams (ADR 0006), observation at the tap (ADR 0004).

## A step

```
loop builds ModelRequest
  system  = Instructions + PromptSnippets of the advertised tools
  tools   = ToolSource() if set, else the static list
  thinking, SequentialTools
        │
        ▼
model middleware chain (WrapModel; first listed = outermost)
  mw.Log ─── observation: request summary, finish, duration
  mw.Retry ─ alternative dispatch: retry a call that failed before any event
  mw.Fallback ─ alternative dispatch: another model on failure
  mw.RepairJSON ─ shaping: fix truncated tool-call arguments once
        │
        ▼
adapter (weft/openai, weft/anthropic, weft/google) → vendor SDK → HTTP
        │  ModelTextDelta / ModelReasoningDelta / ModelToolCallDelta /
        │  ModelToolCall … ModelFinish
        ▼
contract enforcement (ErrModelContract: exactly one finish, nothing after it)
        │
        ▼
transcript: one assistant message (reasoning parts, text, tool calls)
        │
        ├─ max_tokens with calls → every call fails without executing
        │                          (ADR 0002 "fail truncated calls")
        └─ calls → life of a tool call (below), then one tool message
        │
        ▼
StepFinish → StopWhen? → RunFinish, or the next step (MaxSteps bounds it)
```

## A tool call

```
slot acquisition (Parallelism(n); Sequential() = one at a time;
                  a Sequential tool = barrier: wait, run alone, resume)
        │
        ▼
ToolStart (Seq assigned under the ordering lock)
        │
        ▼
── containment boundary: panics and Timeout are handled here, outside ──
        │
        ▼
agent WrapTools chain (first listed = outermost)
  mw.Audit ──── observation: run, step, call, duration, error + cause
  AuthUser ──── decoration: verified data onto ctx (ExampleWrapTools_context)
  mw.Allow ──── decision: DENIED result, the model sees it
  mw.MapErrors ─ shaping: plain errors → coded ToolErrors (after next)
        │
        ▼
per-tool WrapTools chain (Tool(..., weft.WrapTools(...)))
        │
        ▼
base
  resolve name ─────────── unknown → NO_SUCH_TOOL
  RequireApproval? ──────── not Call.Approved → ErrApprovalRequired → pending
  decode (StrictInput) ──── failure → INVALID_INPUT naming the field
  handler ───────────────── string verbatim / JSON; error or *ToolError
        │
        ▼
── containment boundary ── panic → "tool X panicked"; Timeout → "timed out"
        │
        ▼
render: *ToolError → "CODE: message"; other error → its text
cap: MaxResultBytes (per tool, else per agent) + visible marker
        │
        ├─ pending → no ToolFinish; RunFinish.Pending after the step
        ▼
ToolFinish (Seq) → ToolResultPart, in call order, on the step's tool message
```

## The two conventions

- **Context decoration.** Middleware that verifies something (a user, a
  tenant, a quota) puts it on ctx before `next`; the handler reads it
  through a typed accessor, exactly like `weft.CallFromContext`.
  Decorate only with what the middleware verified; derive business
  values in the handler after decode.
- **Error shaping.** Handlers return `*weft.ToolError` when the model
  should branch on a code; `mw.MapErrors` codes everything else
  centrally. The cause (`ToolError.Err`) is for `Audit` and
  `errors.As`, never for the model.

## Resuming an approval

```
Generate(Messages(res.Messages...), Approve(id), Deny(id, reason))
        │
        ▼
repair, exempting the last assistant message's unresolved calls
        │
        ▼
approved → the tool-call chain above, Call.Approved = true
denied   → DENIED: <reason>;  undecided → DENIED: no decision
        │
        ▼
results complete the earlier tool message → first model call of this run
```

See ADR 0006 (seams), ADR 0007 (approval), ADR 0002 (errors), ADR 0004
(events).
