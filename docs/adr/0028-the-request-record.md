# ADR 0028 — the request record

- Status: decided (2026-10-07; the devtools plan's record-contract items
  A1, A2, A4, A9, A10, with A3's badge table; the maintainer confirmed
  plan decision 1, "record the request", the same day)
- Amends: ADR 0024 (observability data) — its record contract gains
  three record kinds, their attributes, the compaction view record and
  four `obsdb` columns. ADR 0024's "changing either is an ADR" rule is
  why this ADR exists.
- Reverses: the stance in `core/run.go`'s `overrideAttrs` comment (at
  line 672 when this was written) that "no record carries the [instructions]
  text — only the hash does".
- Depends on: ADR 0001 (the message wire), ADR 0004 (event ordering and
  the wire discriminators), ADR 0006 (two seams), ADR 0014 (subagents
  as tools), ADR 0016 (observability; spans carry no content, O7),
  ADR 0020 (thread compaction)
- Ships with this ADR: the schema only — `obsdb/sqlite` migration
  `0003_request_record.sql` and `obsdb/clickhouse` migration
  `0004_request_record.sql`. Emission, the API routes and the UI are
  the plan items' own changes, each against this contract.

## Context

The loop builds a `ModelRequest` for every step, after `PrepareStep` has
rewritten it (`core/loop.go`), hands it to the model chain and forgets
it. Five things the model was given are therefore in no record: the
system text, the tool catalog (names, descriptions, schemas), the
sampling params, `tool_choice` and thinking, and — once `PrepareStep` or
compaction rewrites the messages — the exact messages. The `messages`
records rebuild `RunResult.Messages`, which is the transcript, not the
request: a transcript has no system role, and a trimmed or summarized
view is not the transcript at all.

The gap was partly deliberate. The override fingerprint
(`weft.override.hash`, ADR 0024's per-run configuration) hashes a
replaced instructions text and records only `weft.override.instructions
= true`, and the code comment states the policy: the text is content
and no record carries it. That was a privacy stance taken before there
was a content policy to put it under. Its cost became visible once
Studio existed: an inspector that cannot show the prompt it is
inspecting, a playground that edits instructions it cannot display, and
a diff between two runs that can say "the prompt changed" but never
how.

Since ADR 0024 the content policy exists and does exactly the job the
blanket rule was standing in for: content is off in the core by
default, on for the local sink and Studio, off for every vendor preset
and plain OTLP, redactable per destination, capped with a visible
marker. The transcript already travels under it, and a transcript holds
material at least as sensitive as a system prompt (user input, tool
results). Keeping the prompt out of the one channel that already
carries the conversation protected nothing, and it cost the product its
main view.

## Decision

### 1. The request is content, under the same policy as messages

**The constraint "the instructions text is never recorded" is reversed.**
The system text and the tool catalog are content, recorded through the
same OTel log-record path as the transcript and governed by the same
per-destination policy: on for `otel.Local` and `otel.Studio`, off for
third-party destinations, `Redact`-able, capped with a visible marker.
Nothing reaches a destination that the content policy would not already
let a transcript reach. The read path is the only new exposure, and it
is gated (§9): a read-scoped panel token never sees a system prompt.

The fingerprint's meaning does not change: `weft.override.instructions
= true` stays on the `invoke_agent` span, `weft.override.hash` stays
the experiment fingerprint, and spans still carry no content (ADR 0016
O7). The text lives only in records.

### 2. Three new record kinds beside `event` and `messages`

| `weft.record` (= EventName suffix) | Count per run | Position (the dedup key's `pos`) | Content | Body |
|---|---|---|---|---|
| `request` (`weft.request`) | one per model-call attempt | `weft.request.index` | hashes, names and numbers; `params.stop` is the one text field | the request object (§3) |
| `prompt` (`weft.prompt`) | one per distinct `system_hash` | `weft.prompt.index` | yes: the system text | `{"hash", "text"}` |
| `tools` (`weft.tools`) | one per distinct `catalog_hash` | `weft.tools.index` | yes: descriptions and schemas | `{"hash", "tools": [...]}` (§5) |

Each kind has its own per-run counter, contiguous from 0, assigned at
emission like `weft.messages.index`. Writes stay idempotent on (run id,
record kind, position) — ADR 0024's key, unchanged. Every record
carries the identity chain ADR 0024 defines (`weft.run.id`,
`gen_ai.agent.name` when named, every `Metadata` key) and `weft.content`.

**Emission point.** In the model-call phase, after `PrepareStep` has
produced the step's final `ModelRequest` and immediately before the
model chain is called, in this order: `prompt` (when its hash is new to
this run), `tools` (when its hash is new to this run), then `request`
(attempt 1; a further attempt's record follows its report, §7).
A reader that holds a `request` therefore always holds the `prompt` and
`tools` it names, unless a destination dropped them (§6), which the
reader can see.

**Dedupe by hash (plan decision 9).** The system text and the catalog
are stored once per distinct hash per run, never per step. A run that
never changes either has exactly one `prompt` and one `tools` record. A
second `tools` record appears only when the set the model is offered
changes: a `ToolSource` returning a different snapshot, a `PrepareStep`
that narrows or rewrites tools, an MCP server whose tool list changed
between steps. The dedupe set is per run: a new run (a resumed turn, a
subagent's child run) records its own.

### 3. The `request` record

Small and fixed-size; the two content payloads are referenced by hash.
Body (JSON, keys as written; absent = not set):

| Field | Meaning |
|---|---|
| `step` | the step index (also `weft.step.index`) |
| `attempt` | 1 for the loop's call; 2, 3, … for each further attempt the model chain reports (§7) |
| `system_hash` | sha256 of the system text this request carried (§4); `""` when it carried none |
| `messages_ref` | `{"index": i, "count": n}`: the request's messages are the run's view as of `messages` record `i` (§8), `n` messages long — the bytes are never duplicated |
| `tools` | `{"catalog_hash": h, "names": [...]}`, names in the order offered |
| `tool_choice` | the `ToolChoiceConfig` in force (mode, name) |
| `thinking` | the `ThinkingConfig` in force |
| `sequential_tools` | true when the step dispatches tools one at a time |
| `params` | `temperature`, `top_p`, `max_tokens`, `stop`, `seed`; an absent field (nil) means the adapter's default |
| `model` | `{"provider", "name"}` as requested by this attempt (a fallback attempt names its own model) |
| `stream` | true when the call streams |

Attributes: `weft.record = request`, `weft.request.index`,
`weft.step.index`, `weft.attempt.index`, `weft.system.hash`,
`weft.catalog.hash`, `weft.content`.

### 4. The `prompt` record and the instructions hash

Body `{"hash": h, "text": "..."}`; attributes `weft.record = prompt`,
`weft.prompt.index`, `weft.system.hash`. The hash is the lowercase hex
sha256 of the UTF-8 text — the encoding `weft.manifest.hash` and
`weft.override.hash` already use — computed over the text before any
destination redacts or caps it, so a hash identifies the prompt
everywhere even where the text was cut.

Two hashes, deliberately: `instructions_hash` is the run's configured
instructions (the agent's, or the run's override) and is fixed for the
run; `system_hash` is what one request carried after `PrepareStep`. They
are equal unless a `PrepareStep` rewrote the system text.

`RunStart` gains `instructions_hash` (an additive, omitted-when-empty
field on the event wire, ADR 0004), mirrored as `weft.instructions.hash`
on the `run_start` record and on the `invoke_agent` span. A hash is not
content; it travels on content-off chains and on spans.

### 5. The `tools` record

Body `{"hash": h, "tools": [...]}`, one entry per tool in the order
offered: `name`, `description`, `schema` (the JSON schema verbatim), and
the policy chips — `timeout_ms`, `approval` (the tool requires approval
or the run parks it), `replay` (the class the tool actually has; an
unannotated tool, MCP tools included, is `never`), `max_result_bytes`,
`sequential`, and `source`: `local` | `mcp` | `subagent`. Attributes
`weft.record = tools`, `weft.tools.index`, `weft.catalog.hash`.

The catalog hash is sha256 over the canonical JSON of the
model-visible list — `[{name, description, schema}]` in offered order,
schema re-encoded canonically — and not over the policy chips: it
names what the model was offered. A change of policy alone (a
`ToolSource` swapping in the same schema with another timeout) is
therefore not a new catalog and is not re-recorded; the chips describe
the catalog as first recorded in the run.

### 6. Content policy per kind

| Kind | Content-on chain (`Local`, `Studio`) | Content-off chain (vendor presets, `OTLP`) |
|---|---|---|
| `request` | as emitted; `Redact` runs over each `params.stop` string | kept, `params.stop` emptied, `weft.content = stripped`: the hashes, names and numbers survive, so a trace-only backend still sees which prompt and catalog a call used |
| `prompt` | `Redact` runs over `text`; `MaxBytes` caps it, setting `weft.content.truncated_bytes` | dropped, like a `messages` record |
| `tools` | `MaxBytes` caps the body, setting `weft.content.truncated_bytes` | dropped, like a `messages` record |

The core decides capture the way ADR 0024 D2 does: it asks the Logs
API's `Enabled` with the record's EventName before marshalling anything,
and never caps or rewrites. `Redact` receives two new `ContentKind`
values (the prompt text and a stop sequence); their names are A1's,
pinned by its tests. Unlike the transcript, a prompt is capped: a
system prompt is not needed to rebuild `RunResult.Messages`, and the
hash survives the cut. A capped or stripped request record is a badge,
never a silent gap (§10).

### 7. Attempts and timing (A4)

The loop calls the model chain once per step and emits that call's
`request` record with `attempt = 1` before the chain runs. A middleware
that calls `next` again (`mw.Retry`, `mw.Fallback`) reports each attempt
through the core's context-carried hook, `weft.ReportFromContext(ctx)
Reporter` (plan decision 10, item A8, ADR 0016's 2026-10-07 amendment).
For every reported attempt with index 2 or more the core emits another
`request` record — same hashes, `attempt` = the reported index, `model`
as that attempt requested — when the report arrives, so its timestamp
is the attempt's end, not its start. A run with no reporting middleware
has exactly one `request` record per step. The `attempt` child span
under `chat` is A8's, unchanged: `weft.attempt.index`,
`gen_ai.provider.name`, `gen_ai.request.model`,
`weft.attempt.retry_after_ms` when the provider asked, status `Ok` or
`Error` with `error.type`. The `request` record carries the same index
as `weft.attempt.index`, so a record and its span join on (run, step,
attempt). The hook reports; it wraps no call and changes no result, so
it is not a third seam (ADR 0006). `Reporter.Raw`'s wire bodies stay
dropped under this ADR: storing them is content of another size and a
separate amendment.

The `chat` span gains `weft.ttft_ms` (time to the first model event)
and `weft.stream = true` when the call streamed. The `step_finish`
event gains `latency_ms` and `ttft_ms` (additive wire fields, ADR 0004).

### 8. The step index on transcript batches (A2) and the compaction view (A9)

Every `messages` record carries `weft.step.index` — the step the batch
belongs to, 0 for the input; the core knows it at each of the five
growth points — and `obsdb` stores it in `records.step`. Readers stop
inferring a batch's step from its neighbours.

A view the transcript does not hold is recorded, never inferred: when
the messages a request carries are not the run's transcript so far —
a `thread` compaction (ADR 0020), an overflow re-run, a `PrepareStep`
that trims or rewrites messages — the core emits one more `messages`
record before that request, with `weft.messages.reason = compacted`:

- body: the replacement entries (the summary messages), the ordinary
  messages wire, so per-part `Redact` applies unchanged;
- `weft.messages.from_seq`, `weft.messages.to_seq`: the replaced range,
  half-open, as message ordinals over the view it replaces — the run's
  view (records in index order, input first, earlier compactions
  applied) when the replacement happens inside the run, and the
  session's context before compaction when `thread` compacted before
  the run began (then `weft.compaction.scope = session`, otherwise
  `run`);
- `weft.compaction.hash`: sha256 over the canonical JSON of
  `{from_seq, to_seq, entries}`, the compaction's own identity.

It takes the next `weft.messages.index` on the same counter, so the
(run, messages, index) key and contiguity hold. A record with a reason
is not transcript growth: it is excluded from the concatenation that
equals `RunResult.Messages` (ADR 0024's byte-for-byte rule now reads
"the records without `weft.messages.reason`"). The `request` record's
`messages_ref.index` points at the post-compaction record, so "what the
model saw" is always the literal view: the records up to that index,
compactions applied, `count` messages long. An absent reason means
growth; `compacted` is the only reason this ADR defines, and a reader
fails loudly on one it does not know.

### 9. Subagents (A10)

The three kinds are per run id. A subagent's child run (ADR 0014) emits
its own `prompt`, `tools` and `request` records under its own
`weft.run.id`, with its own counters, linked to the parent by
`weft.parent.run.id` and `weft.parent.call.id` on its `run_start`
record and its `invoke_agent` span — ADR 0024's linkage, unchanged. A
parent's records never describe a child's request.

### 10. `obsdb`: kinds, columns, no backfill

- `records.kind` accepts `request`, `prompt`, `tools`. SQLite's `kind`
  is free text and the write path already stores a kind it does not
  count; ClickHouse's `weft_records_mv` filter widens to the three
  kinds, with `Pos` read from their indexes. `obsdb.DeriveRecord`
  reads the three index attributes as the position.
- `records.step` holds `weft.step.index` (-1 = absent). SQLite has had
  the column since `0001`; ClickHouse's `weft_records` gains `Step`.
- The run row gains `instructions_hash` (from `run_start`'s
  `weft.instructions.hash`), `catalog_hash` (the run's first catalog:
  the `tools` record at index 0) and `request_count` (the request
  high-water mark, max `weft.request.index` + 1, retry-proof like
  `delta_count`). ClickHouse stores the three as max-aggregates on
  `weft_runs`; its run views populate them when the emission ships,
  restating their select with the contract tuple's new keys.
- **No backfill.** A run written before this version has no `request`
  records and `request_count = 0`, which reads "not recorded", never
  "made no model call"; the UI states it with the `not_recorded` badge.
- The API (A1): `GET /api/runs/{id}/requests` (paged, `step=` filter,
  hashes resolved inline unless `?refs=1`) and `GET
  /api/runs/{id}/tools`; `GET /api/runs/{id}` gains the three columns.
  Both routes are refused to read-scoped panel tokens; a playground
  scope sees them.

Migrations: `obsdb/sqlite/migrations/0003_request_record.sql`,
`obsdb/clickhouse/migrations/0004_request_record.sql`. Both additive;
`obsdb_migrations` versions them, and a file written by this version
fails `Open` loudly in an older binary (`ErrNewerSchema`), as ADR 0024
requires.

### 11. Holes are badges from one closed table (A3, plan decision 11)

Every place a record is missing, cut or derived is shown with a badge
from this table, each with a one-line reason and, where one exists, the
one-line fix. The vocabulary is closed; a new badge is an amendment to
this ADR.

| Badge | Means | Fix |
|---|---|---|
| `truncated` | a destination's cap cut the content (`weft.content.truncated_bytes`) | raise the destination's `MaxBytes` |
| `stripped` | the destination's chain is content-off (`weft.content = stripped`) | turn content on for that destination |
| `redacted` | the destination's `Redact` changed the content | — (by design) |
| `max_tokens` | the step finished on the output token limit | raise `max_tokens` |
| `interrupted` | the run stopped reporting (derived: last-seen older than 30 s, ADR 0024) | — |
| `gap` | a hole in a contiguous counter: a lost batch | check the exporter's drops |
| `not_recorded` | the record kind did not exist in the version that wrote the run | upgrade weft and re-run |
| `derived` | the value was computed by the reader, not recorded | — |
| `hidden` | the reader's scope may not see it (a read-scoped token and a system prompt) | use a playground-scoped token |
| `compacted` | the model saw a compacted view (§8) | open the compaction record |

## Why this is a record-contract change, not a model-visible one

AGENTS.md rule 5 makes model-visible behaviour a contract: tool result
text, schemas, transcript shape. Nothing here touches any of them. The
`ModelRequest` the adapter receives is byte-for-byte what it was; the
records are emitted beside the call, from values the loop already
holds, and an emission that fails is dropped into the logger (plan
§1.1: observability never changes behaviour). What changes is ADR
0024's contract between the core's emission and every reader — the
attribute names and the `obsdb` schema — which is exactly what that
ADR says needs an ADR. One ADR covers all of it because the kinds, the
columns and the badges are one contract and are read together.

## What this does not change

- **ADR 0006.** The reporting hook (§7) reports; it wraps nothing and
  observes nothing a `Tap` could. Two seams, one tap.
- **ADR 0016 O7.** Spans carry hashes, counts and timings, never text.
- **ADR 0024 D1, D3.** The transcript is still uncapped and stored
  once per turn; `weft.event.pos` and `weft.delta.pos` are untouched —
  the new kinds have their own counters and none of them is an event.
- **The core's dependencies.** No new import; the records leave
  through the existing observer and the OTel Logs API.

## Consequences

- Studio can show the prompt, the catalog and the exact request of
  every model call, diff two runs' requests by hash, and say precisely
  when it cannot (`stripped`, `truncated`, `not_recorded`, `hidden`).
- A run records at least one `prompt` and one `tools` record more than
  before, and one `request` per model call. The `request` record is
  small; the two content payloads are stored once per distinct hash, so
  the cost grows with how often a run changes its prompt or catalog,
  not with its step count.
- A system prompt now reaches every content-on destination. An
  application whose prompt must not leave the process sets `Redact`
  for it or turns content off for that destination; the local sink and
  Studio default to on, as they do for the transcript.
- `obsdb.DeriveRecord` positions the three kinds now; the ClickHouse
  contract tuple and the run views gain the new attribute keys with the
  emission, in the migration that restates them.
- Every attribute name above joins ADR 0024's pinned set: changing one
  is an amendment to this ADR.
