# ADR 0004 — Event ordering for concurrent tools

- Status: decided (v0 skeleton, 2026-09-09)

## Context

THE-END-GOAL.md calls this "the genuinely hard part": interleaved progress
events from concurrent tools must be renderable by a UI and replayable by
devtools. Deterministic *result* ordering is trivial; live interleaving is
not. The rule had to ship with the stream contract, not after.

## Decision

- Every `ToolStart` / `ToolFinish` event carries a per-run `Seq`, assigned
  from one counter at emission time.
- **Assignment and emission happen under one lock** (`ordered` in
  `loop.go`), so the order events are observed in is exactly the order of
  their `Seq` values. Channel receives then preserve that order.
- Consumers pair tool events by `CallID` and may totally order the stream
  by `Seq`.
- Tool *results* are ordered by call index (deterministic transcripts),
  independent of the live event interleaving.
- The concurrency slot is acquired in the parent loop, in call order,
  before a tool's goroutine is spawned. So `ToolStart` events are emitted
  in call order under any parallelism, and `Sequential()` genuinely means
  one at a time *in call order* (a goroutine-per-call race for a
  semaphore does not — that was the v0 bug).
- A call that never obtains a slot because the run was canceled gets an
  error result and **no events**: every `ToolFinish` is preceded by its
  `ToolStart`, always. After cancellation, event delivery stops entirely;
  the transcript on `RunError.Result` is the source of truth.
- `ToolFinish` carries the tool's output (`Content`), the same value the
  model sees, so a UI can render results as they land.

Without the lock, two goroutines could assign Seq 5 and 6 but emit 6 before
5 — an observed order that contradicts the sequence numbers, which makes
recorded streams unreplayable and UIs nondeterministic.

## Amendment (2026-09-09, from REVIEW-v0 finding 21 — naming the alternative)

**Post-cancellation event drop vs. a flush.** Crush synthesizes orphaned
tool results and flushes final state with `context.WithoutCancel`; weft
instead stops delivering events entirely once the run context is canceled
(`emit` in `run.go`), and `RunError.Result` is the authoritative partial
transcript. Considered and rejected for the core: a flush would require
emitting events after the terminal error the stream contract promises at
most once, and the partial transcript already carries everything a
consumer or the future `store` replay needs. If a satellite ever needs
tombstone-style completion (e.g. a UI that must render every started
tool), it derives them from `RunError.Result`, not from the event stream.

Also added with the review fixes: the loop enforces the `Model` stream
contract (`ErrModelContract` — exactly one `ModelFinish`, nothing after
it, panics converted to run errors), so a contract-violating adapter can
never produce an unreplayable or half-observed stream in the first place.

## Amendment (2026-09-10 — the observation tap, TODO §2.7)

**Observation is not middleware: taps see, seams change.** `weft.Tap(fn)`
is an agent option registering an observer that sees every event of
every run — including runs made with `Generate`, which has no stream to
range over — synchronously, in emission order, on the emitting
goroutine. `store`, OTel (§8.1) and slog (§8.2) attach through it
without a third behavioural seam; the two middleware seams (TODO §4)
remain the only places behaviour changes.

The rules:

- Taps run in registration order, inside `execute`'s single `emit`
  wrapper, before the sink. Because tool events are assigned their `Seq`
  and emitted under `emitMu`, and taps run inside `emit`, taps observe
  events in exactly `Seq` order — the same guarantee `Events()` has.
- The post-cancellation drop moved into the same wrapper (it previously
  lived in `run.go`), so `Generate` and `Stream` agree and the rule has
  one home: nothing is delivered after cancellation, to taps or sinks.
- A panicking tap is recovered and dropped (`safeTap`); a broken
  observer cannot break a run.
- The lock consequence, stated in lock terms: taps execute inside
  `emit`, and tool events are emitted under `emitMu`, so a slow tap
  delays every tool event of its step and blocks the emitting tool
  goroutines. Nothing enforces "must be fast" but the doc comment; that
  trade is accepted — same as any observer API.
- Taps are per agent, not per run: §8.1/§8.2 are agent options too, and
  a caller who needs per-run observation wraps their own function. A
  `RunOption` form would be additive.

## Amendment (2026-09-10 — the event wire format, TODO §2.6)

Every `Event` marshals with a `type` discriminator — `run_start`,
`step_start`, `text_delta`, `reasoning_delta`, `tool_start`,
`tool_finish`, `step_finish`, `run_finish` — and `weft.UnmarshalEvent`
restores it; an unknown or missing type is an error, never a silent
drop. The same rule, the same `*Wire`-alias code pattern, and the same
compatibility contract as the message parts (ADR 0001): `store` records
event streams, the Inspector replays them, `serve` will stream them.
Field names are snake_case (`call_id`, `is_error`, …), matching the
parts; the additive-only rule extends from parts to events.

One normalisation, documented rather than enforced: `ToolStart.Args`
that is nil marshals as JSON `null` and decodes back as
`json.RawMessage("null")`; in a live stream Args is never nil (the loop
only sees calls with Args set by the adapter; `wefttest` defaults to
`{}`). `TestEventJSONRoundTrip` pins the exact bytes of every event —
they are a compatibility contract now.

Out of scope, deliberately: an envelope with `SchemaVersion` or a run
id per event (the `store` module defines its envelope, as ADR 0001 says
for messages) and a streaming codec (JSON lines needs nothing from the
core).

## Amendment (2026-09-14 — tool-argument progress, `tool_args_delta`)

`ToolArgsDelta` joins the wire set: an increment of a tool call's
arguments while the model is still "writing" it. It is **progress
only** — the assembled call still arrives as `ToolStart` when it
executes, and every `ToolArgsDelta` precedes the `ToolStart` of the
call it belongs to by construction (deltas stream during the model
call; `ToolStart` is emitted by tool dispatch after it). Like
`TextDelta`, it is emitted from the single loop goroutine and carries
no `Seq`. The adapters' side of the rule (which providers stream
fragments, and the matching `ModelToolCallDelta`) is ADR 0013's.

## Consequences

- Replaying a recorded event stream reproduces the exact live interleaving.
- Step-scoped events (`RunStart`, `StepStart`, `StepFinish`, `TextDelta`,
  `RunFinish`) are emitted from a single goroutine and need no Seq; they
  are totally ordered by construction.
- **Every run has an id** (`Run.ID()`, `RunStart.ID`, `RunResult.ID`;
  supply one with `weft.RunID`) — generated at `Stream`/`Generate` time,
  so it can be handed to a client before the first event. Mastra's
  retroactive id requirement at 1.0 is the lesson: ids as storage keys
  from day one. With TODO §2.8, `RunStart` also **names its model**: a
  Model implementing the optional `interface{ Info() ModelInfo }` is
  detected by the loop and reported on `RunStart.Model` (zero value
  otherwise) — telemetry attributes and the Inspector need provider and
  model name without widening the one-method `Model` interface.
- Tests: `TestStreamEventOrdering` (observed order never contradicts
  `Seq`; every `ToolFinish` follows its `ToolStart`),
  `TestToolStartsInCallOrder`, `TestSequentialRunsInCallOrder`,
  `TestCanceledBeforeStartEmitsNoOrphanEvents`.

## Amendment (2026-09-14 — pending calls, `RunFinish.Pending`, ADR 0007)

A call parked by the approval boundary has its `ToolStart` — the
dispatcher handed it to the tool chain, which is where the decision
to park is made — and **no `ToolFinish`**. It is listed on
`RunFinish.Pending` (wire: `pending`, omitted when empty), mirroring
`RunResult.Pending`, so a UI resolves the open call from the last
event. The "every `ToolFinish` is preceded by its `ToolStart`" rule
stands; its converse now has one documented exception. Resumed calls
(`Approve`) emit their `ToolStart`/`ToolFinish` into the resuming run,
before its first `StepStart`. A step that finished `max_tokens` with
calls emits no tool events at all: nothing started (ADR 0002).


## Amendment (2026-09-18 — `run_id` on every event)

The "out of scope, deliberately" line above is superseded: every event
except `RunStart` now carries `run_id` (wire: `run_id`, snake_case as
the rest), because concurrent runs on one agent emit interleaved
streams and a per-run `Seq` is unique only within its run — `RunID` is
what attributes an event to its run, for taps and stream consumers
alike. `RunStart` keeps its own `id` field as the run's identity and
gains nothing redundant. Old recordings without the field decode with
an empty `RunID`; the additive-only compatibility rule is unchanged.
The `store` module's envelope (when it exists) may still layer its own
`SchemaVersion`; the core's field is the minimal attribution the
concurrent-runs case needs.


## Amendment (2026-09-19 — `Nested` events and the late-event rule,
## ADR 0014)

One event joins the set: `Nested{RunID, Seq, CallID, Event}` (wire
`nested`), wrapping one event of a child run started by a `Subagent`
tool. The envelope's `RunID` and `Seq` are the **parent's** — assigned
and emitted under the parent's event-ordering lock from the parent's
counter — so the total order holds one level down without a new rule
and child events interleave correctly with sibling tools' events. The
inner event keeps its own run id and its own per-run `Seq`
(`run_id`-attributed per the 2026-09-18 amendment); a grandchild is a
`Nested` inside a `Nested`, and `UnmarshalEvent` recurses, so an
unknown inner type is an error exactly as at the top level. Lock order
is strictly child-`emitMu` → parent-`emitMu`; the parent never takes a
child's lock, so there is no cycle.

**The late-event rule:** no `Nested` event for a call is delivered
after that call's `ToolFinish`, and no usage is recorded after it. The
`nest.closed` flag is set inside the `ordered` closure that emits the
`ToolFinish`, and the child's emit and usage paths check it under the
same lock before acting — close, finish, and any racing child event
are totally ordered by the parent's `emitMu`. Without the rule, a
replayed stream could show a call finishing and then continuing; with
it, an abandoned (timed-out or cancelled) delegation's late events and
usage are dropped rather than racing the step's already-read records.

## Amendment (2026-09-19 — OTel and slog are the loop's own reporting, ADR 0016)

The 2026-09-10 amendment's sentence "`store`, OTel (§8.1) and slog
(§8.2) attach through it" is half-right and is corrected: **`store`
attaches through it; OTel and slog are the loop's own reporting
(ADR 0016)**. A tap cannot decorate the tool handler's context (it is
built after the tap sees the run's context), never sees the end of a
cancelled run (nothing is delivered after cancellation), and has no
clock for durations. The loop has all three, so the loop reports its
own phases — one `invoke_agent` span per run, one `chat` span per model
call, one `execute_tool` span per executed tool call, and the matching
slog lines, from one internal observer that is not a seam. `Tap` keeps
its contract exactly as specified here; the ctx a tap receives now
carries the run span, so a tap that starts its own spans parents them
correctly for free.
