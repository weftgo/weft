# ADR 0024 — Observability data: OTel out, one identity chain

- Status: decided (2026-09-30, the observability-data programme's step 1;
  the owner decided D1–D13 the same evening)
- Supersedes: ADR 0010 (run records) — its Status line says so
- Amends: ADR 0016 (observability) — the 2026-09-30 amendment at its end
- Depends on: ADR 0001 (the message wire), ADR 0004 (event ordering, the
  wire discriminators, `Nested`), ADR 0005 (module layout, two-phase
  release), ADR 0011 (thread's boundary), ADR 0012 (the manifest),
  ADR 0014 (subagents), ADR 0018 (the Studio API as the one contract)
- Specification: `WEFT-OTEL-DATA-ARCHITECTURE.md`, in the research
  checkout, not this repo. Part II (S1–S7) is the buildable spec and wins
  over Part I wherever they disagree. This ADR records the decisions and
  the contract; the signatures, SQL, JSON and SSE frames stay in the spec.
- Numbered 0024 because 0009 was never used and two files share 0017
  (`0017-record-replay.md`, `0017-testing-conventions.md`), so a file
  count would understate the next free number.

## Context

The owner's question (2026-09-30): get rid of `weft/store`, do
`weft/otel`, put Studio on ClickHouse with real-time views, link traces
to sessions, prompts and a public id — and is devtools an OTel-from-Studio
interface? Behind it sit the gaps the current design cannot close: two
data models (store events vs spans) joined only by run id, with their
ownership rule enforced by hand; runs that cannot be linked to a session
(store tags are fixed per agent, spans carry no session); crashed or
running runs invisible to OTel because spans export when they end; and a
store that is single-process (the loop's `Seq` is a per-run in-memory
counter) and single-node (SQLite).

A final pre-implementation review (2026-09-30,
`WEFT-OTEL-DEVTOOLS-PLAYGROUND-REVIEW-2026-09-30.md`) verified every code
claim against `main` and found eight items that did not work as written
(a provider type-assertion that always fails through the OTel global, a
strip processor that would mutate the record every destination shares, an
embedded setup with no live lane, a gap detector that `Nested` and deltas
would break, a replay claim that was false because the run's input is
never observed, a per-run override with no mechanism, breakpoints without
handles, and a runtime link that would invert the module arrows). The
owner decided all of them the same evening (D1–D13 below), and allowed
breaking changes outright: everything is early phase, `store` is deleted
rather than deprecated, Studio's backend is rewritten, consumers are
ported in the same change, and no data migrates.

## Decision

**OpenTelemetry is the only way observability data leaves the process,
and `weft/store` is deleted.** The core keeps reporting spans at ADR
0016's phases and additionally emits the run's events and transcript as
OTel log records; `weft/otel` wires one Tracer and one Logger provider
with one processor chain per destination (local SQLite, Studio, Datadog,
Langfuse, any OTLP endpoint, any exporter), all active at once, each with
its own content policy. The store's guarantees (complete, durable,
offline, queryable) move into `weft/otel`'s local sink and Studio's
database. New modules: `weft/obsdb` (the observability database: model,
`DB` interface, SQLite backend, OTLP mapping, the live hub interface),
`obsdb/clickhouse` (its own module, so the driver is opt-in), `weft/otel`
(destinations, heartbeats, the local sink), `weft/runtime` (the playground
link), `studio/cmd` (the local binary). Arrows only point down: `thread`
never imports `obsdb`/`otel`/`runtime`; `obsdb` never imports
`otel`/`runtime`/`studio`; `otel` never imports `thread`; root imports
none of them. Studio ingests OTLP and serves one API; the devtools panel
and the Studio UI are both clients of it.

### The decisions D1–D13

**D1 — the run's repaired input is the first `messages` record; a
session's context is stored once per turn.** The loop repairs the input
(`loop.go:98`) and never reported it, and a resume that rebuilds a tool
message reported nil, so "the stored messages rebuild the transcript"
was false without this emission. Storing the input fully, per run, is the
GenAI conventions' own shape (`gen_ai.input.messages` is the full chat
history) taken at its cheapest point: once per run, not once per model
call. The alternative, storing a reference to the previous turn, would
make a lost turn break replay of every later turn. The cost is accepted
by the owner: a session's records grow with the square of its length, and
the transcript is never capped, because a capped transcript is not
replay-grade. Caps apply to event bodies and deltas; a destination that
must bound transcript size turns content off.

**D2 — capture is signalled through the Logs API's `Enabled`, not a
provider interface; `MaxBytes`/`Redact` live per destination in
`weft/otel`; the core reads no environment variable.** The OTel global
returns a delegating wrapper, never the SDK provider, so a
`ContentPolicyProvider` type assertion on the provider in force always
fails on the default path. The standards-only channel: every processor
`weft/otel` registers answers `Enabled(EventName: "weft.messages")` with
its destination's content setting, the SDK logger answers true when any
processor says yes, and the global forwards `Enabled` faithfully. The
core asks one question at each emission and needs nothing else. Caps and
redaction move out of the core because the core cannot discover them
through that channel, and per destination is the better privacy shape
anyway: Studio may keep full content while Datadog gets a redacted,
capped copy. Environment variables (`OTEL_SERVICE_NAME`, the OTLP
endpoint, the GenAI capture variable, `WEFT_STUDIO_URL`) are read only by
`weft/otel` when it builds destinations; no weft-only variable exists
where a standard one does.

**D3 — two counters: `weft.event.pos` for durable events, `weft.delta.pos`
for deltas; `Nested` has neither.** If `Nested` consumed a position,
every subagent call would leave holes and the gap detector would report
false losses, since a child run numbers its own events with its own
counter. If deltas shared the durable counter, dropping them (they travel
but are never stored) would make one field mean both "a delta was
dropped" and "a batch was lost". So the seven durable events
(`run_start`, `step_start`, `tool_start`, `tool_finish`, `step_finish`,
`steered`, `run_finish`) are numbered contiguously from 0 per run in
`deliver`, the one place every event passes exactly once; deltas are
records of kind `delta` on their own counter; `Nested` is not a record at
all (the parent's wrapper is a copy, and the child's `invoke_agent` span
sits under the parent's `execute_tool` with the parent ids from
`CallFromContext`). A single writer emits at any instant, so positions
follow emission order. A hole in the durable sequence means a lost batch,
never a delta.

**D4 — setup A shares the handle: `studio.Handler(studio.DB(otel.LocalDB()))`;
the live hub interface lives in `obsdb`.** (The accessor was decided as
`otel.Local()` and ships as `otel.LocalDB()`: `Local(path)` is the
destination option, and Go allows one function of a name per package.) Nothing else connects the
pipeline to Studio's live lane: two processes that only share the SQLite
file get history, not live, and the acceptance test (a running run
streams into the panel) fails. A package-level default would be a new
global in a satellite; polling the file breaks the "never poll for a
live tail" rule. The hub interface and its in-process implementation live
in `obsdb` because both `weft/otel` and `studio` import it, so the
arrows still point down; the SQLite backend publishes every `Write` to
its hub, and passing the pipeline's local DB hands Studio the same
handle, live lane included, with no network hop.

**D5 — per-run configuration is dual options plus two RunOptions; there
is no `OverrideSpec`.** A config struct fights the house rule against
config structs, and the dual shape already exists: `Thinking`,
`ToolChoice` and `Params` are Option and RunOption at once. So
`Instructions`, `MaxSteps` and `Parallelism` become dual the same way,
and `weft.OnlyTools(names...)` (narrowing only; an unknown name fails
the run with `ErrInvalidRunOption` before any model call, because the
playground cannot add tools — a new tool is code) and `weft.UseModel(m)`
(from the alternates the runtime registered) join as RunOptions.
`MaxSteps`/`Parallelism` lower only; a raise is `ErrInvalidRunOption`.

**D6 — the runtime link is its own module, `weft/runtime`; `weft/otel`
never imports `thread`.** An SSE client, a command executor, an agent
registry, budget accounting and fork-mode transcript reads do not belong
in an exporter-wiring module, and `otel.PlaygroundThreads(thread.Storage)`
would have put `thread` under `otel`, inverting the module arrows. The
link dials out (so hosted Studio can reach a laptop behind NAT),
registers agent manifests, and waits for commands; in the embedded setup
it is an in-process call. It is the same shape as the future Redis-queue
worker: a command is a job, a runtime is a worker.

**D7 — debugger rungs 3–4 (breakpoints, steer, fork) apply to
runtime-started runs in v1, through a per-run `weft.ParkOn`; the app's
own turns are viewer-only until a session registry exists.** Agents are
immutable after `New`, and steering goes through the session's single
writer, so breakpoints and steer on the app's own turns need per-agent
options and a live-session registry nobody has built. `ParkOn(tools...)`
is the approval boundary (ADR 0007) applied per run — a call to a named
tool parks as if the tool had `RequireApproval()` — which reaches a
runtime-started run without touching the agent.

**D8 — hosted Studio is recorded in THE-END-GOAL's cut list as the one
hosted product: same binary, same API, self-hostable first.** The goal
doc said "never a hosted cloud" while the product and pricing docs had
already moved; the contradiction is resolved in the goal doc, not left
standing (see the tensions below for why the design is defensible
anyway).

**D9 — root ships additive and source-compatible: apidiff green with
three allow-listed lines; only `store` (deleted) and `studio`
(rewritten) break.** Every root change is an addition (`Metadata`,
`Content`, `LoggerProvider`, `StripContent`, the dual options,
`OnlyTools`/`UseModel`/`ParkOn`). The dual options widen their return
type from `Option` to a superset interface, which apidiff flags although
it is source-compatible — exactly the pre-1.0 case `scripts/apidiff.sh`
documents for `.apidiff-allow` (three lines: `Instructions`, `MaxSteps`,
`Parallelism`). Root therefore ships a minor and the two-phase release
holds. `store` stays in the tree, unimported, until the last consumer
port merges, then is deleted — the tree builds at every commit and the
"no compatibility" stance loses nothing.

**D10 — the open questions are closed.** Q1: the public id is per
session, opaque, app-minted (or `thread.PublicID`), browser-safe, never
the internal session id — internal ids name storage paths and cannot be
rotated, and scoping by public id also covers ephemeral playground runs,
which carry one without a session id. Q4: deltas are never stored by
default; `obsdb`'s `KeepDeltas` turns storage on for debugging. Q5:
`obsdb/clickhouse` is open-source, its own module in this repo. Q6: the
ClickHouse schema starts from the OTel Collector's exporter schema,
column-compatible rather than DDL-verbatim (the exporter is Beta and
tells production users to manage their own schema), pinned per
migration. Q7: the runtime link is SSE down plus POST up. Q8: dev tokens
for the local binary, HMAC panel tokens scoped to a public id for
hosted; no OAuth in the panel.

**D11 — span-level content for trace-only backends (F1) is post-v1.**
Langfuse, Phoenix and Datadog's LLM views read content from span
attributes, and weft's spans carry none (ADR 0016 O7). v1 ships
Studio-complete records and content-free spans; a trace-only destination
gets timing, usage, tool names and the identity chain. F1 (the semconv
content attributes on `chat`/`execute_tool` when capture is on, a
per-vendor key mapping, per-destination filtering) is scoped when the
first such user asks. Not a blocker: records are the content channel,
and Studio reads records.

**D12 — WEFT-PLAYGROUND's PQ2, PQ5, PQ6, PQ7 are post-v1.** None blocks
the P0–P5 ladder; they are the playground's own TODO after v1.

**D13 — hmm is out of scope and is not changed in any way.** This
programme covers weft (root, thread, obsdb, otel, runtime, studio) and
weft-arena only. hmm keeps building against the published `store/v0.1.0`
tag and resolves it from the module proxy once the directory is gone.

### The record contract: one identity chain, two counters

Every link is a weft id that always exists; OTel trace and span ids are
correlation, because they are zero with no SDK and missing when sampled.
The chain, carried as attributes on every span and record of a run:

| Link | Attribute | Minted by |
|---|---|---|
| public id | `weft.public_id` | the app (or `thread.PublicID`), via `Metadata` |
| session | `weft.session.id` — mirrored `gen_ai.conversation.id` and `session.id` (Logfire reads the first, Langfuse and Phoenix the second; `Metadata` rides every span, which is what Langfuse needs) | `thread` or the app |
| run | `weft.run.id` | the core / `thread` (`<session>-t<n>`) |
| step | `weft.step.index` | the core loop |
| tool call | `weft.tool.seq` (+ `gen_ai.tool.call.id`) | the core loop |
| event / delta | `weft.event.pos` / `weft.delta.pos` (D3) | the core loop, in `deliver` |
| agent config | `weft.manifest.hash` (sha256 of the one agent's manifest, computed at `New`; unnamed agent means absent) + `weft.version` + `gen_ai.agent.name` | the core |
| prompt | `weft.prompt.id` + `weft.prompt.version` (F-phase, `weft/prompt`) | later |
| end user | `enduser.id`, mirrored `user.id` — attribution, not part of the chain | the app, via `Metadata` |

One lookup walks the chain — public id → session → runs ordered by turn
→ each run's trace → spans and events → prompt versions — on indexed
columns, never by parsing a run id prefix.

The records themselves are OTel log records (logger name
`github.com/weftgo/weft`; `EventName` `weft.event`, `weft.delta`,
`weft.messages`, and `weft.heartbeat` from `weft/otel`), body = the
core's wire JSON (ADR 0004; the sinks add no codec of their own for
anything the core already encodes — ADR 0010 §2's rule, kept), timestamp
= emission time (events finally get the time they never carried),
severity INFO (WARN for a `tool_finish` with `is_error`). Attribute
families, pinned by tests like model-visible bytes:

- **Kind and identity:** `weft.record` = `event` | `delta` | `messages`
  | `heartbeat` (the same word as `EventName`, for backends that drop
  it); `weft.run.id`; `gen_ai.agent.name` when named; every `Metadata`
  key verbatim (mirrors included).
- **Event records:** `weft.event.type` (one of the seven); the position
  `weft.event.pos`; `weft.step.index` on `step_start`, `step_finish`,
  `steered`; `weft.tool.seq`, `gen_ai.tool.call.id`, `gen_ai.tool.name`
  on `tool_start`, `tool_finish`; `weft.parent.run.id`,
  `weft.parent.call.id`, `weft.manifest.hash`, `weft.version` on
  `run_start`.
- **Delta records:** `weft.event.type` (`text_delta`, `reasoning_delta`,
  `tool_args_delta`) and `weft.delta.pos`. They travel and are never
  stored; the live lane forwards them; `NoDeltas()` drops them before
  export.
- **Messages records:** at five emission points (the repaired input at
  run start, the tool message a resume creates or rebuilds, each
  assistant message, each tool message, each steered batch), with
  `weft.step.index`, `weft.messages.index` (0 is the input),
  `weft.messages.count`, `weft.messages.input = true` on index 0. Their
  concatenation equals `RunResult.Messages` byte-for-byte, signatures
  included.
- **Content state:** `weft.content` = `full` | `stripped` | `none` (set
  by the core; `weft/otel`'s strip processor sets `stripped`), and
  `weft.content.truncated_bytes` when a destination's cap cut.

Span attribute families are ADR 0016's, unchanged, plus: the linkage
`weft.parent.run.id` and `weft.parent.call.id` on a subagent's
`invoke_agent`; `weft.manifest.hash` and `weft.version` on the run span;
every `Metadata` key verbatim, with `gen_ai.conversation.id`,
`session.id` and `user.id` as mirrors of `weft.session.id` and
`enduser.id`; `weft.metadata.dropped` when the metadata limits dropped
pairs; and `weft.override.hash` plus the changed `weft.override.*`
values when a RunOption changed the agent's configuration. The existing
families keep their ADR 0016 meanings: `gen_ai.operation.name`,
`gen_ai.agent.name`, `gen_ai.provider.name`, `gen_ai.request.model`,
`gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` with the
split attributes `gen_ai.usage.cache_read.input_tokens`,
`gen_ai.usage.cache_creation.input_tokens` and
`gen_ai.usage.reasoning.output_tokens`, `weft.run.steps`,
`weft.run.pending`, `weft.run.stop_reason`, `weft.step.index`,
`gen_ai.response.finish_reasons`, `weft.model.tool_calls`,
`weft.stop.raw`, `gen_ai.tool.name`, `gen_ai.tool.call.id`,
`weft.tool.seq`, `weft.tool.approved`, `weft.tool.pending`,
`weft.tool.result_bytes`, and `error.type` with the span status. Spans
still carry no content (O7).

**The new "forever".** ADR 0010's stance was that a written record is
forever and every consumer that ever read a file keeps reading it; that
was the store's stance, and the store is being deleted with no
migration — the owner's early-phase call. What replaces "forever" is
narrower and named: the `obsdb` schema, versioned by an
`obsdb_migrations` table under ADR 0010's own rules (a file written by a
newer weft fails `Open` loudly, never a silent misread), plus the
attribute names above. Those two things are the contract between the
core's emission and everything that reads it back (Studio, the panel, a
stock collector, Langfuse); changing either is an ADR. ADR 0010's survey
lessons carry into `obsdb`: status is derived, never free text
(`failed` from the error span, `succeeded` from `run_finish`, and
`interrupted` derived, never stored, from a last-seen older than 30 s —
heartbeats keep a quiet run "running" and are themselves never stored
as rows, they only move last-seen); writes are idempotent on (run id,
record kind, position); reads are loud on the unknown.

### Content: off by default, on through `Install`

Content (text, args, results, messages) is off by default in the core —
ADR 0016's O7 holds; nothing populates Opt-In content without a policy
asking for it, which is also the GenAI conventions' own rule. `weft/otel`
turns it on: capture happens once (the core emits content when any
destination wants it, through the D2 `Enabled` channel) and stripping
happens per destination (each chain clones the record first — the SDK
hands every processor the same pointer — then strips, redacts, caps, or
drops `messages` records outright). Vendor presets and plain `OTLP`
default to content off, so content never reaches a third party by
accident; `Local` and `Studio` default to on. The core never truncates
or rewrites content; `weft.StripContent` is the one shaping function and
`weft/otel` calls it.

### The core's dependency: trace + logs APIs

ADR 0016's "the OTel trace API is the one dependency" becomes "the OTel
trace **and logs** APIs": `go.opentelemetry.io/otel/log v0.22.0`, which
requires exactly the pinned `otel v1.46.0` and shares its `attribute`
package, so no version moves. The Logs API is pre-1.0; `v1.47.0-rc.1`
exists, and dragging the otel line to an rc for it is not worth it. The
risk is confined to `observe.go`, the one file that touches the API, and
the move to the stable line when v1.47.0 is final is an amendment to
this section. THE-END-GOAL principle 5 sanctions "the OTel API"; this
widening is an amendment within that exception, not a new one. The
no-provider cost rule holds: the core checks `Enabled` before
marshalling anything, so a program with no SDK pays no JSON encoding,
and a whole-run allocation bound joins `TestObserverNoopAllocations`.

### Per-run configuration is not a seam

`Instructions`, `MaxSteps`, `Parallelism` as dual options,
`OnlyTools`, `UseModel` and `ParkOn` are configuration the run carries,
like `Thinking` already does: the loop reads them from the run's config
instead of the agent, and rebuilds the model middleware chain per run
when `UseModel` is set. Nothing wraps a call and nothing observes; ADR
0006 is unchanged, and ADR 0007 is unchanged — `ParkOn` applies its
boundary per run through the existing `ErrApprovalRequired` parking
path.

Amendment, 2026-10-02 — `ParkAllExcept(names...)` joins `ParkOn` as
run configuration: the same ADR 0007 boundary applied default-deny.
Every call whose tool is not named parks, evaluated by name against the
step's dispatch snapshot (so `ToolSource` tools are covered), and the
except-list rides the run's context, so runs started inside it (a
Subagent's child) apply it to their own tools; a parked child is
`SUBAGENT_PENDING` as before. Park rules only add up (`ParkOn`,
`RequireApproval`, several except-lists intersect); an agent's own
`Output` submission is the run's answer and never parks under it. The
fingerprint gains `weft.override.park_all_except` (a sorted set, present
and empty when everything parks) inside `weft.override.hash`. Not a
seam: ADR 0006 and ADR 0007 are unchanged. Why: a `ParkOn` list computed
from `Agent.Tools()` cannot name a `ToolSource` tool or a child run's
tools, so WEFT-PLAYGROUND §6 rule 3 did not hold for them; `weft/runtime`
now passes `ParkAllExcept(safe ∪ opted-in)` on every playground run.

### `Tap` narrows

`Tap` keeps its contract exactly and loses its persistence jobs:
`weft/otel` records, `store` is gone, and devtools reads Studio. Its
remaining work is streaming live output to the app's own users and
`thread`'s turn forwarding. It is no longer how anyone observes weft.

### Tensions, stated

1. **Hosted Studio vs "never a hosted cloud" (D8).** THE-END-GOAL's cut
   list said never; setup C is a hosted Studio. The resolution, written
   into the goal doc: hosted Studio is the one hosted product, and it is
   the same binary and the same API, self-hostable first — `WEFT_STUDIO_URL`
   is one environment destination beside the standard OTLP one, no
   hosted UI is privileged (the panel and the Studio UI are both clients
   of the one API), and the SQLite sink stays first-class so "one binary,
   one file" keeps working offline. The contradiction is resolved where
   it lived, not worked around.
2. **The core's dependency widens.** Trace API plus logs API, with the
   logs API pre-1.0 (see above). This is the price of records being
   standard OTel log records — the thing that makes a stock collector,
   Langfuse and Studio all read the same emission — and it is paid in
   one file, `observe.go`, behind an API the goal doc already sanctions.
3. **Root additive, with three acknowledged apidiff lines (D9).** The
   widening of `Instructions`/`MaxSteps`/`Parallelism` return types is
   source-compatible but not apidiff-silent; the gate stays green with
   three `.apidiff-allow` lines, the mechanism `scripts/apidiff.sh`
   documents for exactly this pre-1.0 case. The alternative — keeping
   the constructors narrow and adding `*Run` variants — would double the
   option names for no reader's benefit.

## What this does not change

- **ADR 0006 (two seams) and ADR 0007 (the approval boundary).** Records
  in `deliver` are the loop's own reporting; per-run configuration is
  configuration; `ParkOn` applies the existing boundary. No third seam,
  no phase becomes a hook.
- **ADR 0004's event contract.** `Tap`, `Events()`, `Seq`, the wire
  discriminators, `Nested`, cancellation silence — all unchanged. Records
  are a second consumer of the same events, not a new event shape.
- **ADR 0011's boundary.** `thread` owns state, not observability; it
  never writes to the observability database; `thread.Storage` stays
  pluggable. Its only contribution is `Metadata`
  (`weft.session.id`, `weft.turn`, the optional `weft.public_id`). Two
  copies of every transcript exist in the embedded setup — thread's for
  resume, `obsdb`'s for observation — and that price is deliberate:
  Studio never reads thread storage, which may be anywhere and anything.
- **ADR 0016 O7.** Spans carry no content, ever; content lives in
  records, gated by D2. F1 (D11) will amend O7 when it is built.
- **ADR 0017 (testing conventions).** Record/replay stays at the model
  seam; Weft CI fixtures come from the local sink or are verified
  complete by position contiguity (D3 makes that check sound).
- **The manifest (ADR 0012).** `weft.json` is still generated,
  golden-gated, never read back. The hash of one agent's manifest moves
  onto the run span and the `run_start` record, computed at `New`.
- **AGENTS.md's rules.** Reporting, not a seam; no environment reads in
  the core; no config structs; nothing above the core is imported by the
  core; model-visible behaviour is a contract. The satellites are new
  modules with their own tags, released after the root they require.

## Consequences

- The build programme is the spec's §9 (eight steps plus 6b), each step
  gated by its S7 acceptance lines; the first code step lands the core
  additions above, all additive, with `make apidiff` green and the three
  allow-listed lines.
- ADR 0010 is superseded: `weft/store` and its format are deleted, and
  the record contract is the `obsdb` schema plus the attribute names
  above. Its evidence appendix (the field survey) stays the reference
  for why `obsdb` versions, derives status, and stays loud on the
  unknown.
- ADR 0016 is amended (see its 2026-09-30 note): the logs API joins the
  trace API as the core's one dependency; `Metadata`, the event and
  messages records, capture through `Install`, and the manifest hash on
  the run span join the observer's outputs.
- `weft-arena` and Studio port onto `obsdb` before `store` is deleted;
  hmm does not (D13).
- Every attribute name above is pinned by a named-constant block and
  tests, like ADR 0016's span keys; changing one is an amendment to this
  ADR.
