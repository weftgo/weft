# ADR 0030 — a dynamic park source

- Status: decided (2026-10-11; the devtools plan's item F4.2, closing
  §13.6 PQ7's second half: the debugger's breakpoints park tools on the
  app's own runs)
- Depends on: ADR 0006 (the two seams), ADR 0007 (the approval
  boundary), ADR 0014 (subagents as tools), ADR 0021 (thread
  approvals), ADR 0024 D7 (the agent is immutable; run options carry
  per-run configuration)

## Context

The debugger's breakpoints (WEFT-DEVTOOLS §8.3) park a tool's calls at
the approval boundary. Until now they reached only the runs the
playground runtime starts: the runtime adds `ParkOn(breaks...)` to each
run it starts, because the agent is immutable after `New` (D7). The
app's own runs — a `Generate` in a handler, a weft/thread turn — never
carried the option, so a breakpoint set in the panel was silently
ignored by exactly the runs a developer is usually looking at (PQ7).

The app cannot add `ParkOn` to its own runs either: the breakpoint set
changes while the process runs (the debugger PUTs it, Studio re-sends
it at every registration), and the call sites that start runs know
nothing about a debugger. What is missing is a way for the agent,
built once, to read a park set that is current *when each run starts*.

A tool call that parks where it would otherwise have run is
model-visible behaviour (AGENTS.md rule 5), hence this record.

## Decision

1. **`core.ParkSource(fn func(ctx context.Context) []string) Option`.**
   An agent option holding a plain function. The loop reads every
   source of the agent **once per run**, at its start — after the run
   options apply and the run's metadata is placed on the context,
   after the override attributes are taken, before the first model call
   and before a resume's decided calls run — and merges the names into
   the run's park set exactly as a `ParkOn(names...)` run option would:
   the same matching rule, by name, against each step's dispatch
   snapshot (a tool only a `ToolSource` supplies parks like any other;
   a `Subagent` tool's call parks before its child runs). Like
   `ParkOn`, the set covers the run it was read for: a Subagent's child
   run reads its own agent's sources, not its parent's. Several sources
   add up; a nil function is ignored; a resume reads the sources again
   (it is its own run) and an approved call still runs once, as under
   `ParkOn`.
2. **The source must answer promptly.** It runs on the loop's
   goroutine before the first model call. The contract is "read a
   cached set": the runtime's source copies a small map under a mutex.
   A nil or empty answer parks nothing; a panic is contained, parks
   nothing and is reported as one Debug line ("park source panicked").
3. **It is behaviour, not an override.** The names are not written to
   `weft.override.*` and do not change the run's override hash: that
   fingerprint describes what a caller changed about a run, and a
   source is part of the agent the app built. A source that answers
   nothing leaves the run byte-identical — events, transcript, records,
   spans — to one without it.
4. **`(*Agent).ParkSources()`** returns the agent's sources (a fresh
   slice), a read-only accessor in the `Params`/`Thinking`/`ToolChoice`
   line, so a satellite that installed one can recognise it (the
   playground runtime reports `breakpoints_own` at registration).
5. **`runtime.Breakpoints()`** (package weft/runtime) is the first
   user: a `ParkSource` bound lazily to the process's runtime link —
   the agent is built before `runtime.Install` runs, so the source
   reads the breakpoint set of the links Install has started and not
   yet stopped (their union, should there be several). No Install, an
   Install not enabled, a stopped link or an empty set answers nil, and
   so does a playground run (below). The set is process-wide — every
   agent built with the option reads it, whether or not it was given to
   `runtime.Agents` — and a link whose stream dropped but was not
   stopped keeps its last set until it reconnects and Studio re-sends
   it.

### Not a seam, not a hook

ADR 0006 allows two behavioural seams and one tap, and says a phase
turned into a hook needs an ADR. `ParkSource` is neither: it does not
wrap a model or a tool call, it is not called at a phase of each step,
and it cannot change a request, a result or an event. It is a value
the run reads once at its start, of the same kind as `ToolSource` (a
value the loop reads per step): configuration whose current value
lives with its owner. The approval boundary it feeds is ADR 0007's,
unchanged.

## Consequences

- The model sees nothing new. A parked call is ADR 0007's parked call:
  its `ToolStart` and no `ToolFinish`, the run ending successfully
  with the call on `RunResult.Pending` / `RunFinish.Pending`; the
  resuming run's `Approve`/`Deny`/`Resolve` decide it, and an
  undecided call reads `DENIED: no decision`. No result text, schema
  or transcript shape changes.
- On a weft/thread turn the park is the session's (ADR 0021): the turn
  parks, `s.Pending()` lists the call (its request's reason reads
  "middleware required approval", thread's word for a park the tool
  did not ask for), and `s.Decide` — the app's own, or the debugger's
  through `runtime.Sessions` — resumes it. A plain `Generate` ends
  pending and only the app resumes it: Studio decides session turns
  only.
- `runtime.Breakpoints()` parks a thread turn only when the process
  also set `runtime.Sessions` (a live link holds a resolver): without
  it the source answers nothing for a run carrying `weft.session.id`.
  A park nobody but the app could decide would hold the session — every
  later `Send` queues behind the open boundary (ADR 0021 §5), clearing
  the breakpoint releases nothing, Studio refuses the app-owned turn —
  until an app with perhaps no approval UI decides it; the same
  reasoning as a stopped link's set parking nothing (a runtime that
  cannot decide does not park). A plain `Generate` (no session id)
  still parks: the app resumes it. Pinned by
  `TestBreakpointWithoutSessionsSkipsThreadTurns`.
- In a `Subagent`'s child run the source parks the child, not the
  parent: the parent's call to the subagent is the existing
  `SUBAGENT_PENDING` result, which the parent model sees and continues
  past — core's rule for any park in a child run.
- Observability still never changes behaviour: an app opts in at
  `New`, and `runtime.Breakpoints()` without a live link is inert. A
  configured breakpoint *is* behaviour — the app chose it by building
  the agent with the option.
- The core gains an option and an accessor, no import: the function is
  the app's (or a satellite's); the core only calls it.

## What this does not change

`ParkOn`, `ParkAllExcept`, `RequireApproval` and middleware parking
keep their rules; the rules still only add up toward parking.

The playground runtime's own runs keep exactly their pre-F4.2 rule,
whether or not the agent carries `runtime.Breakpoints()`: the runtime
gives each run it starts the link's breakpoint set filtered to the
agent's static tools, as a `ParkOn` written into its override
fingerprint (`weft.override.park_on`, `weft.override.hash`), and the
runtime's source answers **nothing** for a run carrying
`weft.playground` metadata — every run the runtime starts, and every
run started inside one (a Subagent child inherits the metadata). The
rule follows from what the fingerprint is for: it must determine what
an experiment run did. Were the source to answer there too, a
breakpoint on a tool only a `ToolSource` supplies would park it (the
filtered `ParkOn` lets it run), and a Subagent child built with the
option would re-read the set and park a tool its playground parent
vouched for or allowed — with the run's fingerprint byte-identical to
one where nothing parked. So on a playground run the fingerprint alone
decides; the source acts on the app's own runs only. Pinned by
`TestBreakpointSourceSkipsPlaygroundRuns`.

Nothing new crosses the wire to Studio except the registration's
`breakpoints_own` flag; no route is added.
