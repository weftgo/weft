# ADR 0010 — Run records: events plus result, versioned, append-safe

- Status: decided (2026-09-27, TODO §11; implementation plan
  `docs/phase2b-store-plan.md` §2, whose §0.1 survey is this ADR's
  evidence appendix); **superseded by ADR 0024** (2026-09-30 —
  observability data: `weft/store` is deleted with no migration; the
  record contract becomes the `obsdb` schema, versioned by
  `obsdb_migrations`, plus the OTel record attribute names ADR 0024
  pins; the survey appendix stays the reference for `obsdb`'s
  versioning, status derivation and loud-on-unknown rules)
- Depends on: ADR 0001 (the message wire), ADR 0002 (`RunError.Result`,
  the partial-transcript rule), ADR 0004 (event ordering, the wire
  discriminators, `Nested`), ADR 0005 (the `weft` envelope integer,
  two-phase release), ADR 0012 (the manifest hash's source), ADR 0014
  (subagents — the child-run tree this format carries)
- Implementation: module `weft/store` (TODO §11) — a `Record` tap plus
  an `OnRunEnd` observer writing `RunRecord`s through a `Store`
  interface; SQLite first (`store/sqlite`), Postgres later, same
  module. This ADR is a root ADR even though the code is a satellite:
  the format is the contract between the core's events and everything
  that reads them back.

## Context

The store is the keystone of Phase 2b: the Inspector (§12), Weft CI,
a hosted Studio, and hmm's session migration all read what it writes.
Code changes; a written record is forever — every consumer that ever
read a file has to keep reading it. The field's scars are the context:

- **LangGraph burned four on-disk checkpoint formats in 26 months**, and
  its Postgres saver *runs nothing and says nothing on a database newer
  than the code* (`checkpoint-postgres/base.py` refuses to operate but
  raises no error a user sees).
- **Mastra has no version table**; its LibSQL store silently drops
  unknown columns on read (`filterRecordToKnownColumns`).
- **Crush uses goose with embedded migrations** — the one quiet upgrade
  path in the set.

So the format is decided before any Go, in its own ADR, and every
decision below names the precedent that decided it (the full table is
the appendix).

## Decision

### 1. One record per run, two halves

A `RunRecord` is the run's **identity** — id, the parent run's id and
the parent's call id when this is a subagent's child run, agent name,
model info, manifest hash, weft version, started, finished, heartbeat,
status, tags — and its two halves:

- the **event stream**: every `weft.Event` the run emitted, in `Seq`
  order, `Nested` included, so a parent's record replays as one stream
  (ADR 0004's total order *is* the record's order); and
- the **result**: the `RunResult` when the run ended, plus the
  `RunError` text when it failed.

**The result is stored on failure too.** `RunError` carries `Result`
(ADR 0002's partial-result shape), so a failed run's transcript up to
the failure is kept and the Inspector shows where it stopped. Every
surveyed store keeps the partial: LangGraph stores `__error__` as a
write at the checkpoint and keeps completed nodes' writes "so we don't
re-run the successful nodes"; Mastra's snapshot has `error` *and*
`result`; Deer-flow's `runs` row has `status`, `error`, `stop_reason`;
Crush writes synthetic error tool results plus a finish part.

### 2. Wire JSON is the core's, not the store's

Events serialise with the core's own `MarshalJSON`/`UnmarshalEvent`
(events.go: the `"type"` discriminator, snake_case keys); messages with
the core's message JSON (ADR 0001). **The store adds no codec of its
own for anything the core already encodes** — one codec, one set of
pinned bytes, one fuzz surface.

`RunResult` has no JSON today. The store defines a `resultDoc`
(stop_reason, messages, steps, usage, pending) with explicit tags over
the core's types, so a core field added later is a deliberate store
change with a migration, not a silent schema drift. `StepRecord` is
encoded the same way.

### 3. Envelope and version

Every stored document is wrapped as `{"weft": 1, …}` — the integer the
manifest already uses (`manifest.go`: `Version int json:"weft"`, ADR
0012). One number for all weft wire documents; it moves when *any* of
them changes incompatibly.

- The number is the store's package-level `store.FormatVersion`
  (currently `1`).
- **What bumps it:** a renamed or retyped key. **What never bumps it:**
  an added optional key — readers ignore unknown keys.
- The backend tracks applied migrations in a table
  (`schema_migrations(version, applied_at)`), and a file written by a
  newer weft — a migrations table ahead of the binary's highest — fails
  `Open` with a named error (`ErrNewerSchema`), never a silent misread.
  LangGraph's four formats and its mute newer-database saver are the
  cautionary tale; Crush's embedded goose migrations are the mechanism.

### 4. Status is derived, never free text

Stored statuses: `running`, `succeeded`, `failed`. The first row is
written at `RunStart` as `running`; the end write sets
`succeeded`/`failed`.

**`interrupted` is never stored.** A reader derives it from
`status == running && heartbeat_at < now - HeartbeatTimeout` (30 s
default). The store does not rewrite the row on detection: a crash
leaves evidence (the `running` row plus the events that landed), and a
live run in another process is not mistaken for a corpse. Mastra tells
a crash from a live run only by *restarting every `running` run at
boot*, and Langfuse rendered interrupted runs as errors until users
filed it; Deer-flow stores a `lease_expires_at`. The heartbeat is the
lease without the ownership protocol, which a read-only record does not
need — the writer bumps it per event and on a ticker while a run is
alive but quiet (a long tool call must not read as a crash).

### 5. Append-safe, loud on the unknown

The SQLite backend is transactional, so torn writes are the driver's
problem. The format's stance for any file-shaped backend (a future
JSONL export, hmm's migration) is pi's, recorded as the rule:

- append a newline to a torn final line, then drop it;
- skip a malformed mid-stream line with a warning;
- bound the header scan;
- one corrupt record never hides the others.

**An event whose `type` the reader's weft does not know fails `Get`
with a named error (`ErrUnknownEvent`) carrying the type and the run
id; `List` — which never reads events — still works**, so an older
Inspector shows the run and says why it cannot open it. Loud over
silent is ADR 0002's stance and AGENTS.md rule 2; the AI SDK and
Pydantic AI throw on unknown parts, pi skips malformed lines silently —
we side with the throwers, at the document level where skipping would
corrupt a replay.

### 6. Growth path, recorded not built

Three extensions the format must not preclude, named so a future change
checks against them:

- **Tree sessions** (branching in place, pi v3): `parent_id` and
  `parent_call_id` exist from day one and carry the subagent tree; a
  child run is its own record *and* stays inline in the parent's stream
  (§1).
- **A usage ledger** with adjustment rows: usage is stored as the
  core's `Usage` struct per run *and* per step, so a ledger can be
  derived without a migration.
- **The `custom` vs `custom_message` entry taxonomy** (pi): events are
  opaque JSON, so a new event type is a core change, never a store
  migration.

### 7. What the record is not

One sentence each, so nobody asks the record to be these:

- **Not an HTTP cassette** — ADR 0017: no vendor cassettes ever, at any
  layer.
- **Not a replay fixture** — that is `wefttest`'s `fixture`, keyed on
  the model request; the store keys on the run.
- **Not a message-only session** — hmm's JSONL format is the message
  half of this record; §5 of the plan migrates hmm *to* the record,
  never the reverse.
- **Not a checkpoint** — weft records what happened and replays it; it
  does not promise to resume a run mid-step. Pydantic AI removed
  exactly its persistence package over "the complexity of achieving
  consistent snapshotting with parallel execution"; weft's resume story
  is the approval boundary (ADR 0007), which is a run boundary, not a
  mid-step rewind.

## Consequences

- `weft/store` is the only writer of the format; the Inspector (§12),
  Weft CI, and hmm are readers. Readers older than a format bump fail
  loudly at `Get`/`Open`, never silently.
- The core gains `OnRunEnd` (plan §3.7, ADR 0006 note) — a tap cannot
  see a failed run's end, because a failed run emits nothing after its
  last delivered event (ADR 0004) — plus the two observation
  accessors the recorder needs from outside the sealed Option
  interface: `AgentFromContext` (the manifest hash's source) and
  `(*Agent).Logger()` (the observer's error sink), both recorded in
  ADR 0016's 2026-09-27 accessor amendment.
- The manifest hash rides on the record (computed by `Record` from the
  agent it is installed on), not on `RunStart`: an event is the run's
  output, the record is what ran it. Nobody in the field stores a graph
  hash — Mastra stores the whole serialized graph in every snapshot,
  which is the other extreme; the hash is the middle.
- hmm's session files keep working during migration; the record's
  `Result.Messages` is a superset of their content.

## Evidence appendix — the survey (2026-09-27)

Read from the cloned trees in the research checkout (`repos/`):
LangGraph's checkpoint savers (Python and JS), Mastra's storage and
observability layers, pi's session manager, Crush's SQLite store,
Pydantic AI, the Vercel AI SDK, Deer-flow. Each row is the finding and
the decision it decided.

| Finding | Where | Decides |
|---|---|---|
| LangGraph burned four on-disk checkpoint formats in 26 months; its Postgres saver runs nothing and says nothing on a DB newer than the code; Mastra has no version table and "unknown columns are silently dropped"; Crush uses goose with embedded migrations | langgraph.md §8; `checkpoint-postgres/base.py:39-42`; mastra `filterRecordToKnownColumns`; crush `db/migrations` | §3: one integer format version, a migrations table, a newer file fails `Open` loudly |
| Everyone keeps the partial on failure: LangGraph stores `__error__` as a write at the checkpoint and keeps completed nodes' writes "so we don't re-run the successful nodes"; Mastra's snapshot has `error` and `result`; Deer-flow's `runs` has `status`, `error`, `stop_reason`; Crush writes synthetic error tool results plus a finish part | `checkpoint/README.md:59`; `workflows/types.ts:383`; `persistence/run/model.py`; `agent/agent.go:1130-1165` | §1: result stored on failure |
| Status is derived where it is good: Mastra computes error/running/success from `error` and `endedAt`, stores none; LangGraph's API derives `pending\|running\|error\|success\|timeout\|interrupted`; Langfuse renders interrupted runs as errors and users filed it; Mastra tells a crash from a live run only by restarting every `running` run at boot; DeerFlow stores a `lease_expires_at` | `computeTraceStatus`; `langgraph-api/storage/types.mts:43-51`; langfuse#14034; `mastra/index.ts:3854-3900`; `run/model.py` | §4: status derived; heartbeat column for liveness |
| Children get identity everywhere: Crush gives a sub-agent its own session row with `parent_session_id`; LangGraph gives a subgraph its own lineage under `checkpoint_ns`; Mastra stores one row per span with `parentSpanId`, a trace is the root; DeerFlow links `follow_up_to_run_id` | `session/session.go:351`; `_constants.py:87-89`; `tracing.ts:111`; `run/model.py` | §1/§6: a child run is its own record **and** stays inline in the parent |
| Events as rows: DeerFlow `run_events(seq, event_type, content)`; Mastra one row per span, `UNIQUE(spanId, traceId)`; LangGraph writes as rows per `(task, idx)`; only Crush keeps parts as one JSON array, and only per message | `models/run_event.py`; libsql `db/index.ts:709`; `checkpoint_writes` PK | the SQLite schema: `run_events` keyed `(run_id, seq)` |
| List bodies hurt: Mastra shipped `listTraces` with full input/output, then added `listTracesLight` "excludes input, output, attributes, tags, links" and has an open issue on message-history pagination; LangGraph's `list` returns full tuples and has an open 85 % storage-bloat issue | `listTracesLight`; mastra #21349; langgraph #7714 | the API: `List` returns no events |
| Cursors, not offsets: LangGraph's `before` is a keyset on monotonic ids, no offset anywhere; Mastra ships offset `page/perPage` with `{total, hasMore}` and is adding an opaque `after` cursor in its next Postgres store; Crush lists everything | `checkpoint/base/__init__.py:101`; `pg/v-next/listing.ts` | the API: `Before` cursor, plus a `Total` count |
| Tags as JSON, filtered by `json_extract`: Mastra's `metadata` jsonb with regex-validated keys; pi keys sessions by `cwd` in the header; Crush has no tags | libsql `json_extract(metadata,'$.key')`; `session-manager.ts:32` | the schema: `tags` column in migration 0001 |
| Write cadence: pi appends every entry synchronously (no fsync, no batching) and loses the partial assistant message on a crash because it persists only on `message_end`; Crush debounces streaming updates at 33 ms, flushes terminal updates synchronously; Mastra's exporter batches 1000 / 5 s with a 10 000 emergency flush and drops after four retries | `agent-session.ts:669-685`; `defaultUpdateDebounce`; `mastra-storage.ts` | the tap: per-event append, nothing buffered — the Inspector can tail a live run |
| Nobody stores a graph hash (LangGraph puts only `langgraph_version` in run metadata) but Mastra stores the whole `serializedStepGraph` in every snapshot | `stream.mts:219`; `workflows/types.ts:383` | the schema: `manifest_hash` + `weft_version` columns, computed by `Record` |
| Delete is in the contract: LangGraph's five-method saver includes `delete_thread`; Mastra bolted retention on later as an opt-in `prune()` nothing calls by itself, no VACUUM | langgraph.md §10; `storage/retention.ts` | the API: `Delete` from day one |
| Unknown types on read: the AI SDK and Pydantic AI throw on an unknown part and tell users to catch it; pi skips malformed lines silently; Mastra drops unknown columns | `validate-ui-messages.ts:50`; `messages.py:2624`; `session-manager.ts:514` | §5: `Get` fails loudly naming the type, `List` still works |
| SQLite done right: Crush on `modernc.org/sqlite`, WAL, `synchronous=NORMAL`, `busy_timeout=30000`, `foreign_keys=ON`, `_txlock=immediate` ("preventing deferred-to-writer upgrade deadlocks"), `SetMaxOpenConns(1)`; DeerFlow shares one WAL file between the checkpointer and its own tables | `db/connect_modernc.go:30-33` | the backend: the same pragmas |
| Pydantic AI removed its persistence package ("complexity of achieving consistent snapshotting with parallel execution") and hands durability to Temporal/DBOS/Prefect; the AI SDK ships no store and its canonical example is a JSON file per chat | `docs/changelog.md:119`; `examples/next/util/chat-store.ts` | §7: a run *record*, not a checkpoint |
