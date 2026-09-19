# ADR 0006 — The two middleware seams

- Status: decided (2026-09-14, TODO §4.1–§4.3)
- Depends on: ADR 0002 (error model), ADR 0004 (the observation tap)

## Context

Everything a consumer wants to *change* about a run — retry a flaky
provider, fall back to another model, deny a tool, log with a cause,
attach the caller's identity for a handler — has to attach somewhere.
The frameworks studied either grow a callback catalogue (`onStep`,
`onToolStart`, `onError`, …) that becomes the engine's public shape, or
they pick two composable seams and stop. Weft picks two, chi-style:
`func(next Model) Model` and `func(next ToolCaller) ToolCaller`. ADR
0004 already settled that *observation* is a tap, not a seam. The
playbook's red flag stands: a third seam needs an ADR; a phase in the
"life of a call" never becomes a hook.

## Decision

**Model seam.** `weft.WrapModel(mw ...ModelMiddleware)` wraps the
agent's model at `New`; the chain is built once. The first middleware
listed is the outermost — `WrapModel(a, b)` runs `a(b(model))` — and a
later `WrapModel` option appends inward. The chain sees the
`ModelRequest` exactly as the loop built it (system with prompt
snippets composed, tools of the step, the run's thinking level). A
middleware that wraps a `Model` forwards its identity with
`weft.InfoOf(next)`, so `RunStart.Model` and the manifest still name the
provider. Middleware returning a nil `Model` panics at `New`.

**Tool seam.** `weft.WrapTools(mw ...ToolMiddleware)` wraps every call
the loop dispatches, and `Agent.CallTool` — the manual dispatch seam —
runs the same chain. `ToolCaller` is `func(ctx, ToolCallPart) (string,
error)`: the result text the model will see, or an error. The chain is:

    agent WrapTools (outermost first) → the tool's own WrapTools →
    base: resolve the tool → RequireApproval check → decode → handler

- An unknown tool still runs the agent-level chain (so `Allow`/`Audit`
  see the attempt); the base returns the `NO_SUCH_TOOL` error.
- **Panic containment stays outside the chain.** A middleware panic is
  a `tool "x" panicked: …` result, never a run error. So is a timeout:
  `weft.Timeout` bounds the whole chain.
- A middleware error is rendered exactly as a handler error would be:
  a `*ToolError` gives the model a code; any other error its text.
- An error wrapping `ErrApprovalRequired` is the one non-result: the
  call is parked (ADR 0007).
- `CallFromContext` is available in middleware. Middleware that
  verifies something adds it to ctx before `next` and handlers read
  it back through a typed accessor — the **context decoration
  convention** (`ExampleWrapTools_context`). Decorate only with data
  the middleware has verified; derive business values in the handler
  after decode. This is the whole "typed request context" story.

**Per-tool policy that touched the loop** (TODO §4.3 leftovers):

- `Sequential()` on a tool is a **barrier**: the dispatcher lets every
  in-flight call of the step finish, runs this call alone, then
  resumes the step's parallelism. Results stay in call order.
- `WrapTools` on a tool wraps that tool alone, inside the agent chain.
- `PromptSnippet(text)` on a tool: the loop appends the advertised
  tools' snippets to the instructions — one paragraph per tool, in
  registration order, blank-line separated — on every model call.
- `Replay(ReplaySafe|ReplayNever)` is an annotation the manifest
  carries for the store/runtime's checkpoint restart; the loop ignores
  it.
- `RequireApproval()` → ADR 0007.

The manifest records `sequential`, `require_approval`, `replay`, and
`prompt_snippet` per tool; middleware is code and is not described.

**Reference middleware, package `weft/mw`** (same module, no
dependencies): model seam — `Retry`, `Fallback`/`FallbackWhen`, `Log`,
`RepairJSON`; tool seam — `Allow`, `Audit`, `MapErrors`. A rate limiter
needs `x/time` and is an example, not a package member.

**Retry policy** (TODO §4.1, from the pi dive; ADR 0002's "retry
transport, not logic" stance unchanged): transport retries stay in the
vendor SDKs; `mw.Retry` is the loop-visible logic layer above them.

- Retries only a call that failed **before yielding any event**. A
  failure after events reached the loop is a run error: part of the
  reply is already consumed, and no middleware can un-see it. The same
  rule applies to `Fallback`.
- Retryable by default: `ErrStreamIdle`, `net.Error`, and HTTP 408,
  409, 429, 5xx as reported by the vendor SDKs' error types (found
  structurally — an exported `StatusCode`/`Code` field — so `mw`
  imports no SDK). `x-should-retry` overrides the status rule. Never:
  context errors, the kill switch, `ErrUnsupported`, `ErrModelContract`,
  other 4xx (quota, billing, bad request), and any error whose text
  names a context-window overflow — overflow routes to compaction
  (§14) once it exists, and surfaces as a run error until then.
- Backoff: 500 ms doubling per attempt, capped at 8 s, ±25 % jitter;
  3 retries by default. A `retry-after-ms`/`retry-after` header wins;
  an ask above 60 s (`MaxWait`) fails fast wrapping
  `mw.ErrRetryAfterTooLong` rather than sleeping silently.
- Exhausted retries return the last error wrapped, so `errors.As` on
  the SDK type still works.

## Alternatives considered

- **Lifecycle hooks** (`OnStep`, `OnToolStart`, …): every hook is a
  contract; the two seams compose and the tap observes. Rejected;
  recorded as a playbook red flag.
- **A typed request-context API** (a struct threaded to handlers):
  Go's ctx-plus-accessor convention does the same with no new type
  and no third seam.
- **Retry inside the loop**: would silently mask provider behaviour
  from every consumer; as middleware it is opt-in, visible in `Log`,
  and replaceable.
- **Retrying a mid-stream failure by buffering**: doubles memory and
  changes streaming semantics; not worth it for a rare case.

## Consequences

- `Agent.CallTool` now runs middleware. Manual dispatchers get the
  same policy as the loop; the documented difference (no containment,
  no cap, no timeout) is unchanged.
- The `Sequential` return type widened to `PolicyOption` (source-
  compatible; acknowledged in `.apidiff-allow`).
- Model-visible strings pinned by tests: `DENIED: tool "x" is not
  allowed` (`mw.Allow`), `INTERNAL: tool "x" failed` (`mw.MapErrors`
  default), `tool "x" panicked: …` for middleware panics, and the
  prompt-snippet composition (`"\n\n"` separator).


## Amendment (2026-09-18 — `Replay` retracted until the store ships)

`Replay(ReplaySafe|ReplayNever)` is deleted from the core. The
checkpoint store that would consume it is post-v1 in THE-END-GOAL's
cut list, so no consumer could exist before the persistence module
does — and the annotation was rent-free surface ("every abstraction
has to pay rent"). It returns together with the store (§11), which
will re-add the option, the manifest key, and its ADR in one change.
The manifest no longer records a `replay` key (ADR 0012's shape
shrinks accordingly, pre-1.0).

## Amendment (2026-09-19 — `PrepareStep` is the one loop knob, not a
## third seam; TODO §5.5)

`weft.PrepareStep(fn)` installs a function the loop calls before every
model call, with the request it built; what the chain returns is what
the step uses. The red flag in PLAYBOOK.md is "a hook that can *change*
behaviour", and PrepareStep changes the request — three facts decide
that it is not a third seam:

1. **The model seam can already rewrite requests.** A ModelMiddleware
   receives the same `ModelRequest` and may return a modified one to
   `next`; PrepareStep adds no power the seams lack.
2. **What the seam cannot do is make the dispatch snapshot agree with
   what was advertised.** A middleware that drops a tool from
   `req.Tools` leaves the loop dispatching against the full snapshot,
   breaking the one-snapshot-per-step invariant from inside the seam.
   PrepareStep runs *before* the snapshot is fixed, so the prepared
   list **is** the snapshot — validated by the same rule as a
   ToolSource fetch (`ErrDuplicateTool`, `ErrNilTool`).
3. **It is shaped like `StopWhen`, not like a hook.** One function,
   loop-level, pure over its inputs, with the loop's own state (`step`)
   as an argument — the same category as `StopCondition`, a loop knob
   since v0 that nobody calls a seam.

So the taxonomy stands at two behavioural seams plus **one loop-level
knob** (`StopWhen`, `MaxSteps`-shaped budgets, `PrepareStep`), and the
"phases are documentation" rule is untouched: there is exactly one
function, for exactly one phase (building the request), and the
life-of-a-call phase list does not grow. Ordering, pinned by tests:
the loop builds the request on the agent's raw instructions → the
PrepareStep chain, in option order → the returned list becomes the
step's snapshot → PromptSnippets compose after it, from the returned
tools (removing a tool removes its snippet; the function never sees
the composed system) → the model seam → the adapter. Several
PrepareStep options chain, each receiving the previous result; a nil
function is ignored; an error fails the run with the caller's sentinel
reachable through `errors.Is`. The transcript is never affected — the
request a function receives is a deep copy (message parts, a tool
call's argument bytes, and tool definitions are cloned at that
boundary, so in-place mutation reaches neither the transcript nor the
frozen registry; the model seam keeps the lighter slice copies under
the adapter read-only contract) — and, like ToolSource, this is one
of the two knobs that can break a prompt-cache prefix; the godoc
says so. The manifest does not describe it (it is code, like
middleware).
