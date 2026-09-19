# ADR 0014 — Subagents as tools

- Status: decided (2026-09-19, TODO §5.1)
- Deep dive chapters 11 and 12 argued the design from the field's
  evidence; this ADR records the mechanics as shipped and every open
  decision the plan (`docs/phase2-loop-plan.md` §3) resolved with a
  guess.
- THE-END-GOAL: "subagents are goroutines" — the concurrency model is
  the engine; multi-agent orchestration is "loop + patterns
  (agents-as-tools)", not a graph.

## Context

An orchestrator agent needs to delegate a self-contained task to another
agent and read its answer, the way Claude Code's `Task` tool and the AI
SDK's agents-as-tools do. The field offers two shapes: delegation to a
fresh child run (Claude Code, Pydantic AI, Deer-flow's orchestrator -
workers), and pi's refusal of both in favour of **AgentLanes** — one
session, several named lanes over a shared tree, on the grounds that a
subagent is "a black box within a black box".

## Decision

**A subagent is an ordinary tool.** `weft.Subagent(name, description,
child, opts...)` returns a `*ToolDef` whose handler runs `child.execute`
— the same method `Generate` and `Stream` call — on a fresh transcript
holding exactly one user message, `prompt`. There is no second loop, no
special case in `execute` for "am I a child", no new interface, method,
message part, or option kind. Everything the tool contract already
offers applies unchanged: `Timeout` bounds the child run, `MaxResultBytes`
caps its answer, `RequireApproval` gates the delegation, `Sequential`
makes it a barrier, the parent's `WrapTools` wraps the delegation once
(the child's own seams govern inside it), `Parallelism` bounds concurrent
delegations because a subagent call holds a slot like any tool, and the
manifest lists it.

The mechanism is two unexported context values, neither with an
accessor:

- `ancestry []*Agent` — pushed by `execute` itself (`[root]` for a root
  run, `[root, child]` one level down). Its one consumer is the cycle
  guard.
- `nest{emit, usage, closed}` — built by `execTools` for **every**
  dispatched call, subagent or not, so the dispatcher stays ignorant of
  what a subagent is. `emit` wraps a child event in a `Nested` event
  numbered from the parent's counter under the parent's `emitMu`;
  `usage` records into the step's per-call map; `closed` implements the
  late-event rule.

Outside the loop (`Agent.CallTool`, `ToolDef.Invoke`) neither value is
present: a nil nest discards, the ancestry is empty, and the child
simply runs, as any tool does outside the loop.

**Events.** One new event, `Nested{RunID, Seq, CallID, Event}` (wire
`nested`, recursive through `UnmarshalEvent`). `RunID`/`Seq` are the
parent's — the wrapper is assigned and emitted under the parent's
ordering lock from the parent's counter, so ADR 0004's total order holds
one level down without a new rule, and the child's events interleave
with sibling tools' events correctly. The inner event keeps its own
run id and its own per-run `Seq`; a grandchild appears as a `Nested`
inside a `Nested`. A child's whole run — `RunStart` through
`RunFinish` — is bracketed by the delegating call's `ToolStart` and
`ToolFinish`. Lock order is strictly child-`emitMu` → parent-`emitMu`;
the parent never takes a child's lock, so there is no cycle.

**Usage.** The child's total (a failed child's partial usage included,
read off `RunError.Result` when the run returned no result) is added to
the parent's `RunResult.Usage` after the step's own model usage, and
recorded per call on `StepRecord.SubagentUsage`. `StepRecord.Usage` and
`StepFinish.Usage` stay the model call's own numbers — attribution
comes from the map, not from re-firing the parent's middleware per
delegated turn (the Crush rule). Resumed approved calls' delegations
roll into the total only: there is no `StepRecord` for resumed calls
(ADR 0007).

**Failure is data.** A child `*RunError` never fails the parent run; it
is the coded tool error `SUBAGENT_FAILED: agent "<tool>" failed at step
N: <cause>`, with the child's `*RunError` on `ToolError.Err` for
middleware (`errors.As`). A child that ends with pending approvals is
`SUBAGENT_PENDING: agent "<tool>" ended awaiting approval of N call(s)` —
under the run-boundary model the parent's transcript has nowhere to
carry the child's pending call, and no parent option can decide for a
call id that exists only inside the child, so the delegation is loud
rather than silently lossy; full propagation belongs to `runtime`, which
owns sessions and can resume the child under its lineage id. The
practical guidance, stated in the godoc and README: approval-gated tools
belong in the orchestrator, not in a child. `RequireApproval` **on the
subagent tool itself** composes: the delegation parks, and `Approve`
re-runs it with `Call.Approved` set — the approval gates the act of
delegating.

**The cycle guard.** Agents are immutable values, so a cycle cannot be
built at construction; `ToolSource` can close one at run time. The guard
is the ancestor check: a delegation whose child is already on the ctx's
ancestry chain returns `SUBAGENT_CYCLE: agent "<tool>" is already
running in this call chain` before any model call. Comparison is
pointer identity on `*Agent` — two `New` calls with identical options
are two agents and may nest. A depth cap was considered and rejected:
an arbitrary number every user must think about, and cost across depth
is `UsageLimit`'s job (TODO §5.3), which is why it lands next.

**Cancellation and timeouts.** The child runs on the parent call's ctx
(parent run ctx + `Call` + `nest` + the per-call deadline under
`Timeout`), so parent cancellation cancels the child at its next ctx
check and the child's own emit wrapper drops its events from then on.
A `Timeout` on the subagent tool abandons the handler goroutine at the
deadline exactly as for any tool; the child keeps running until its
next ctx check. The rendered result is the ordinary
`tool "x" timed out after d` string — the handler's `SUBAGENT_FAILED`
is discarded because its cause chain carries `DeadlineExceeded`, which
`invokeWithTimeout` recognizes; no new string for either case.

**The late-event rule** (amending ADR 0004): *no `Nested` event for a
call is delivered after that call's `ToolFinish`, and no usage is
recorded after it.* Between a timeout firing and the child noticing,
the child may still emit; `nest.closed` is set **inside** the
`ordered` closure that emits the `ToolFinish`, and both `emit` and
`usage` check it under the same lock before acting — the close, the
finish, and any racing child event are totally ordered by the parent's
`emitMu`. Without this, a replayed stream would show a call finishing
and then continuing, the exact class of bug ADR 0004 exists to prevent.
The usage half of the rule exists for the same reason the event half
does: an abandoned goroutine is not waited for, and a record it
produces after its call's finish would race with the step's map being
read — usage that lands before the finish counts, usage after it is
dropped with the goroutine that produced it.

**Lineage ids.** The child's run id is
`<parentRunID>/<step>/<callID>` for a call dispatched by step N, and
`<parentRunID>/resume/<callID>` for one executed under `Approve` (the
literal segment keeps resumed calls from colliding with step 0's, since
resumed calls report `Step: 0` and call ids are unique per step, not
per run — `wefttest` numbers every turn `call_1`, `call_2`, …). It is
set through `runConfig.id`, so it appears on the nested `RunStart`, on
the child's `RunResult.ID`, and on `CallFromContext` inside the child's
tools — a stable key for `store`. Outside the loop there is no parent
id and `runConfig.finish` generates a fresh one. The deep dive's
`<parent>/<callID>` was rejected: it collides on the first test that
delegates twice.

**pi's AgentLanes counterpoint, answered.** The "black box" objection
is about observability, and `Nested` events answer it: the parent
stream shows every child step, tool, and delta in order, and Studio
replays the child from the parent's record alone — a weft subagent is
a goroutine with a window, not a box. The "shared tree" half of
AgentLanes is a *session* concept (one transcript, several
configurations taking turns on it); it is expressible today as user
code — several agents sharing `Messages` on a caller-owned transcript —
and belongs to `runtime` sessions if it ever becomes a primitive. The
core does not choose between the shapes: `Nested` + usage roll-up
serve either.

## Register of guesses this ADR adopts

| # | Guess | Why |
|---|---|---|
| G2 | Child id `<parent>/<step>/<callID>`, `resume` for approved calls | call ids repeat across steps; the step segment makes the id unique for the life of the run while staying deterministic and self-describing |
| G3 | `SUBAGENT_*` messages quote the **tool name**, not the child agent's `Name` | the tool name is always present (a child may be unnamed), it is what the parent model called, and it is what `Audit` logs by `Call.Name`; the child's own name is on the nested `RunStart.Agent` |
| G4 | An `Output[T]` child returns the submitted `Args` bytes verbatim; a child that never submitted returns its final text, no error | the parent sees the child model's own bytes; `ErrNoOutput` is a `GenerateAs` concern — the parent model reads what the child said |
| G5 | The late-event rule (events and usage, close atomic with the finish under the parent lock) | replayability; abandoned goroutines are not waited for |
| G6 | `wefttest.Flatten` is a test helper, not core API | a test helper must not become core surface |
| G7 | Manifest `subagent` field names the child; the manifest does not recurse | the manifest describes the agents it was given; Studio draws the edge by name when both are in the fleet |
| — | The `SUBAGENT_*` / `RETRY` codes are **exported** constants (`CodeSubagentFailed`, …) | follows the 2026-09-18 review's export of the loop's own codes, so policy middleware can branch without duplicating wire strings |

## Out of scope, deliberately

A handoff primitive, a graph, shared mutable state between parent and
child, a subagent pool or receipts (`runtime`, TODO §14), propagation
of a child's pending approvals (`runtime`), and `Nested.Depth` (the
wrapping is its own record; depth is a viewer concern).

## Consequences

- The parent's `Generate` still wraps child events (one allocation per
  child event, dropped at the sink) — the cost is noise next to a model
  call, and taps see the child either way.
- A parent step that fans out to four children can overshoot a usage
  limit by four children's worth before the next check; that is
  `UsageLimit`'s documented behaviour (TODO §5.3) and the reason
  `Timeout` on the subagent tool exists.
- THE-END-GOAL success criterion 3 ("parallel subagents work correctly
  with zero configuration") is met by the existing dispatcher: a step
  fanning out to N delegations runs N child goroutines under the
  parent's `Parallelism`, results in call order, `Seq` monotonic.

Tests: `TestSubagent*` in `contract_test.go` (the fifteen contract
points of the plan's §3.2, the grandchild double-wrap, Generate usage
roll-up), `TestSubagentParallelDelegations` in `agent_test.go`,
`TestFlatten` in `wefttest`, all under `-race -count=3`; the
`nested` wire bytes are pinned in `TestEventJSONRoundTrip` and fuzzed.
