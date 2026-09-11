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
