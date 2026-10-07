# ADR 0028 — the request record

- Status: decided (2026-10-07; the devtools plan's record-contract items
  A1, A2, A4, A9, A10, with A3's badge table; the maintainer confirmed
  plan decision 1, "record the request", the same day)
- Amends: ADR 0024 (observability data) — its record contract gains
  three record kinds, their attributes, the compaction view record and
  five `obsdb` columns. ADR 0024's "changing either is an ADR" rule is
  why this ADR exists.
- Reverses: the stance in `core/run.go`'s `overrideAttrs` comment (at
  line 672 when this was written) that "no record carries the [instructions]
  text — only the hash does".
- Depends on: ADR 0001 (the message wire), ADR 0004 (event ordering and
  the wire discriminators), ADR 0006 (two seams), ADR 0014 (subagents
  as tools), ADR 0016 (observability; spans carry no content, O7),
  ADR 0020 (thread compaction)
- Ships with this ADR: the read side of `obsdb` — `obsdb/sqlite`
  migration `0003_request_record.sql`, `obsdb/clickhouse` migration
  `0004_request_record.sql` (columns, the widened records view, the run
  views filling the new columns), the new kinds' positions in
  `DeriveRecord` and the new keys in `MetaOf`. Emission, SQLite's
  run-row fill, the transcript readers' growth filter, the API routes
  and the UI are the plan items' own changes, each against this
  contract.

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
is gated (§10): a read-scoped panel token never sees a system prompt.

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
produced the step's final `ModelRequest`, after that request passed
validation (tool choice, params) and after the tools' `PromptSnippet`s
were composed into the system text, immediately before the model chain
is called, in this order: `prompt` (when its hash is new to this run),
`tools` (when its hash is new to this run), then `request` (attempt 1; a
further attempt's record follows its report, §7). A request that was
never sent — a `PrepareStep` error, a validation failure, a cancellation
before the call — is never recorded. The three records are emitted on
the `chat` span's context, so they carry its trace and span id and join
it by span id. A reader that holds a `request` therefore always holds
the `prompt` and `tools` it names, unless a destination dropped them
(§6), which the reader can see.

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
| `messages_ref` | `{"index": i, "count": n}`: the request's messages are the run's view as of `messages` record `i` (§8), `n` messages long — the bytes are never duplicated. On a run whose capture is off no `messages` record exists and `weft.messages.index` does not advance, so `index` is omitted and only `count` is kept |
| `tools` | `{"catalog_hash": h, "names": [...]}`, names in the order offered; `catalog_hash` is `""` when no tools were offered (§5) |
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
`weft.prompt.index`, `weft.system.hash`. Every text hash in this ADR is
the lowercase hex sha256 of the UTF-8 bytes — the encoding
`weft.manifest.hash` and `weft.override.hash` already use — computed
over the text before any destination redacts or caps it, so a hash
identifies the prompt everywhere even where the text was cut.

Two hashes over two different texts:

- `system_hash` is over the **composed** system text the model
  received: the request's system after `PrepareStep`, with the offered
  tools' `PromptSnippet`s appended by `composeSystem` (`core/loop.go`).
  It is the hash of the `prompt` record's text.
- `instructions_hash` is over the **raw configured** instructions: the
  agent's `Instructions`, or the run's override, before any
  `PrepareStep` and before composition. It is fixed for the run.

`RunStart` gains `instructions_hash` (an additive field on the event
wire, ADR 0004), mirrored as `weft.instructions.hash` on the `run_start`
record and on the `invoke_agent` span. It is **always** present on a
run written by this version: a run with no instructions carries the
hash of the empty string,
`e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`.
Its presence is what tells a reader the run was written under this
contract (§10). A hash is not content; it travels on content-off chains
and on spans.

### 5. The `tools` record

Body `{"hash": h, "tools": [...]}`, one entry per tool in name order
(the order the hash uses): `name`, `description`, `schema` (the JSON
schema verbatim), and the policy chips — `timeout_ms`, `approval` (the
tool requires approval or the run parks it), `replay` (the class the
tool actually has; an unannotated tool, MCP tools included, is
`never`), `max_result_bytes`, `sequential`, and `source`: `local` |
`mcp` | `subagent`. Attributes `weft.record = tools`, `weft.tools.index`,
`weft.catalog.hash`. The `request` record's `tools.names` keeps the
order the model was offered.

The catalog hash names what the model was offered, so it covers the
model-visible triple and not the policy chips. The procedure, in Go
terms; an emitter in another language follows the same steps:

1. For each tool, decode its JSON schema with a `json.Decoder` that has
   `UseNumber()` set, into an `any`.
2. Build one map per tool with the keys `description`, `name` and
   `schema` (the decoded value), and sort the maps by `name`. They are
   maps, not structs: every object, at every level, is written with its
   keys sorted (`description`, `name`, `schema` here), so an emitter
   that writes the fields in a struct's declaration order produces a
   different hash and is wrong.
3. Encode the array with a `json.Encoder` that has
   `SetEscapeHTML(false)`; Go's encoder writes map keys sorted and no
   insignificant whitespace. Drop the encoder's trailing newline.
4. The hash is sha256 of those bytes, lowercase hex.

Go's encoder output, produced by these steps, is the definition. An
emitter in another language that starts from RFC 8785 (JCS) must
account for the three known differences: numbers keep their source
lexeme (`1.0` stays `1.0`, which `UseNumber` preserves); U+2028 and
U+2029 are escaped as `\u2028` and `\u2029` even with
`SetEscapeHTML(false)`; and invalid UTF-8 in a string is replaced by
U+FFFD. An empty catalog — no tools offered — has `catalog_hash` `""`,
like a request with no system text has `system_hash` `""`: no `tools`
record is emitted and the `request` record carries no
`weft.catalog.hash` attribute, so no emitter hashes `[]`. A change
of policy alone (a `ToolSource` swapping in the same schema with another
timeout) is therefore not a new catalog and is not re-recorded; the
chips describe the catalog as first recorded in the run.

### 6. Content policy per kind

| Kind | Content-on chain (`Local`, `Studio`) | Content-off chain (vendor presets, `OTLP`) |
|---|---|---|
| `request` | as emitted; `Redact` runs over each `params.stop` string | kept, `params.stop` emptied, `weft.content = stripped`: the hashes, names and numbers survive, so a trace-only backend still sees which prompt and catalog a call used |
| `prompt` | `Redact` runs over `text`; `MaxBytes` caps it, setting `weft.content.truncated_bytes` | dropped, like a `messages` record |
| `tools` | `MaxBytes` caps the body by dropping whole entries from the end of the name-ordered list, never a byte cut (a cut schema is not JSON), setting `weft.content.truncated_bytes` to the bytes removed | dropped, like a `messages` record |

The core decides capture the way ADR 0024 D2 does: it asks the Logs
API's `Enabled` with the record's EventName before marshalling anything,
and never caps or rewrites. `Redact` receives two new `ContentKind`
values (the prompt text and a stop sequence); their names are A1's,
pinned by its tests. Unlike the transcript, a prompt is capped: a
system prompt is not needed to rebuild `RunResult.Messages`, and the
hash survives the cut. A capped or stripped request record is a badge,
never a silent gap (§11).

### 7. Attempts and timing (A4)

Attempt numbers are the core's own count per model call, in the order
attempts are reported: the reporter A8 put on the chain's context
(`weft.ReportFromContext(ctx) Reporter`, plan decision 10, ADR 0016's
2026-10-07 amendment) numbers them itself, with one counter per model
call; a reporter never supplies a number. The loop's own `request`
record, emitted before the chain runs, is attempt 1. Every reported
attempt after the first adds one more `request` record with that
number — same hashes, `model` as that attempt requested — emitted when
the report arrives, so its timestamp is the attempt's end, not its
start. Only the layer adjacent to the real model reports (`mw.Retry`,
`mw.Fallback`, or an adapter that reports its SDK's own internal
retries), and an adapter that does report SDK-internal retries adds
records the same way; a count of `request` records per step is
therefore "attempts the chain reported, at least one", not a fixed one.
A report made after its model call ended is dropped by the reporter
best-effort, as `core/report.go` documents: a goroutine the chain left
behind that reports while the call is ending may still land. So A1
additionally checks the reporter's ended state before it emits the
extra `request` record, and a late report adds no record.

The `attempt` child span under `chat` is A8's, unchanged:
`weft.attempt.index`, `gen_ai.provider.name`, `gen_ai.request.model`,
`weft.attempt.retry_after_ms` when the provider asked, status `Ok` or
`Error` with `error.type`. The `request` record carries its number as
`weft.attempt.index`, so a record joins its span on (run, step,
attempt); when no `attempt` span exists (attempt 1 with nothing
reporting, or no tracer recording), the join falls back to the `chat`
span by the record's span id (§2). The hook reports; it wraps no call
and changes no result, so it is not a third seam (ADR 0006).
`Reporter.Raw`'s wire bodies stay dropped under this ADR: storing them
is content of another size and a separate amendment.

The `chat` span gains `weft.ttft_ms` (time to the first model event)
and `weft.stream = true` when the call streamed. The `step_finish`
event gains `latency_ms` and `ttft_ms` (additive wire fields, ADR 0004).

### 8. The step index on transcript batches (A2) and the compaction view (A9)

Every `messages` record carries `weft.step.index` — the step the batch
belongs to, 0 for the input; the core knows it at each of the five
growth points — and `obsdb` stores it in `records.step`. A steered
batch carries the step that just finished and feeds the next step's
request. Readers stop inferring a batch's step from its neighbours.
ClickHouse rows written before `0004` read `step = -1`; for those a
reader falls back to inference and shows the `derived` badge.

**Resumes (amends ADR 0024 D1).** On a resume the input record 0
stops at the last assistant message with tool calls. The next growth
record — step 0, not flagged `weft.messages.input` — carries the tool
message the resume rebuilds or inserts and anything that followed it in
the fed-in transcript (a user prompt included), as it stands after the
resumed results attach. That tail is accepted input like record 0, so
it is emitted with `context.WithoutCancel`: a resume cancelled during
its approved tools still records it (if the run fails before the results
attach, the tail is recorded as it was fed), and the records
concatenate to `RunError.Result.Messages`; a resume cancelled before its
input record went out records nothing. The concatenation rule and every
request's `messages_ref` therefore hold on resumes. `obsdb.DedupTranscript`
stays only for runs stored in the old shape, where record 0 held the
partial tool message and record 1 its rebuilt copy.

**Growth and views.** A `messages` record without
`weft.messages.reason` is growth: readers concatenate growth records in
index order, and that concatenation equals `RunResult.Messages` (ADR
0024's byte-for-byte rule, now stated over growth records only). A
record with `weft.messages.reason = compacted` is a view: it says what
one request saw instead of the transcript, and it is never part of the
plain transcript. `compacted` is the only reason this ADR defines; a
reader fails loudly on one it does not know.

**Run scope (the core emits it).** When the messages a request carries
are not the run's transcript so far — a `PrepareStep` that trims,
summarizes or rewrites messages — the core emits
one `compacted` record immediately before that request's `request`
record:

- the range is computed by the longest common prefix and the longest
  common suffix (not overlapping the prefix) of the current transcript
  and the request's messages, where two messages are equal when their
  wire JSON (ADR 0001) is byte-equal; the messages between them in the
  transcript are the replaced half-open range `[from_seq, to_seq)` of
  message ordinals, and the request's messages between them are the
  record's body (the ordinary messages wire, so per-part `Redact`
  applies unchanged). If the two are equal, no record is emitted;
- attributes `weft.messages.from_seq`, `weft.messages.to_seq`,
  `weft.compaction.scope = run`, and `weft.compaction.hash`, sha256
  over the canonical JSON (§5's encoder) of `{"from_seq", "to_seq",
  "entries"}`;
- it takes the next `weft.messages.index` on the same counter, so the
  (run, messages, index) key and contiguity hold.

The request's `messages_ref.index` points at that record: what the
model saw is the growth records up to that index, concatenated, with
that one record's range replaced by its body — `count` messages long. A
run-scope rewrite applies only to the request that points at it and is
never carried into later steps: each step's `PrepareStep` sees the full
transcript again, and each rewritten request gets its own record.

Worked example. Growth records 0 (input: `u1`), 1 (`a1`), 2 (`t1`)
make the transcript `[u1, a1, t1]`. Step 2's `PrepareStep` replaces
`a1, t1` with one summary `s`, so the request carries `[u1, s]`. Common
prefix `[u1]`, common suffix empty: the core emits record 3, `compacted`,
`from_seq = 1`, `to_seq = 3`, body `[s]`, and the request's
`messages_ref = {index: 3, count: 2}`. The model answers `a2`, which is
growth record 4: the transcript is `[u1, a1, t1, a2]` — record 3 is not
in it. Step 3's request, if `PrepareStep` leaves the messages alone,
has `messages_ref = {index: 4, count: 4}` and no `compacted` record
applies to it.

**Session scope (thread reports it).** The core has no knowledge of a
`thread` compaction (ADR 0020): when `thread` compacted before the run
— a threshold or manual compaction, or the overflow re-run, which runs
again on the compacted context — the run's input record 0 already
holds the compacted context, which is literal and needs nothing
applied; the core emits no run-scope record for it. A marker with
`weft.compaction.scope = session` is informational only: `thread` emits
it through its own OTel records, it carries no messages, and readers
never apply it. A9 designs that carrier.

**Readers (A9's change, not this commit's).** Both transcript readers —
`obsdb/sqlite`'s `Transcript` (`query.go`, the `kind = 'messages'`
select) and `obsdb/clickhouse`'s (`query.go`, the `Kind = 'messages'`
select) — must filter to growth records (`reason = ''`, read from the
record's attributes in SQLite and from `weft_records.Reason` in
ClickHouse) when building the plain transcript, and a run's messages
count excludes `compacted` records. Until A9 lands the core emits no
`compacted` record, so today's readers stay correct.

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
  reads the three index attributes as the position. A record of one of
  the three kinds with no index attribute gets `Pos = -1` on both
  backends; duplicates at -1 collapse under the key, and only a
  malformed producer reaches it.
- `records.step` holds `weft.step.index` (-1 = absent). SQLite has had
  the column since `0001`; ClickHouse's `weft_records` gains `Step` and
  `Reason` (`weft.messages.reason`, `''` for growth), because ClickHouse
  keeps no attribute column to read the reason from — and, for the same
  reason, `Input` (`weft.messages.input` as 0/1; `-1` on rows written
  before the column, for which the transcript reader infers the flag
  and shows the `derived` badge), `Content` (`weft.content`) and
  `TruncatedBytes` (`weft.content.truncated_bytes`), which the request
  readers turn into the `stripped` and `truncated` badges, and
  `SystemHash` and `CatalogHash` (`weft.system.hash`,
  `weft.catalog.hash`), the hashes a reader falls back to when a
  malformed producer's body does not parse — that row reads `derived`
  (A1.2).
- The run row gains `instructions_hash` (`run_start`'s
  `weft.instructions.hash`, or the `invoke_agent` span's; the larger
  when the two differ, on both backends), `catalog_hash` (the
  `weft.catalog.hash` of `request` index 0 — the request record survives content-off chains,
  the `tools` record does not; `''` when that request offered no tools,
  §5) and `request_count` (the number of
  `request` records). ClickHouse fills them in its run views as
  max-aggregates: `request_count` is the high-water mark, max
  `weft.request.index` + 1, which equals the count because the index is
  contiguous from 0, and which a retried batch cannot inflate (a sum
  over rows could). SQLite's write path fills them with the emission
  (A1).
- The read side (A1.2): `obsdb.DB` gains `Requests` (paged by request
  index, a step filter), `Prompt` and `Tools` (one record by hash) and
  `Catalogs` (every tools record of a run, one per hash). A prompt or
  tools record the run does not hold is `ErrNotFound`, as an
  `obsdb.HoleError` naming the badge when the reason is known:
  `not_recorded` (the run's `instructions_hash` is `''`), `stripped` (a
  request naming the hash came through a content-off chain) or `gap` (a
  request naming it was stored as emitted, the record was dropped).
  `obsdb.Hole` is §11's table as a Go type.
- Every new attribute key (`weft.request.index`, `weft.prompt.index`,
  `weft.tools.index`, `weft.system.hash`, `weft.catalog.hash`,
  `weft.attempt.index`, `weft.instructions.hash`,
  `weft.messages.reason`, `weft.messages.from_seq`,
  `weft.messages.to_seq`, `weft.compaction.hash`,
  `weft.compaction.scope`) is part of the contract and never run
  metadata: `obsdb.MetaOf` excludes it and ClickHouse's contract tuple
  carries it.
- **No backfill, and three readings of a run row:**

  | Row | Means | Badge |
  |---|---|---|
  | `instructions_hash = ''` | written before this contract: no request records exist | `not_recorded` |
  | `instructions_hash != ''`, `request_count = 0` | made no model call (a resume that parked again, a `PrepareStep` or validation failure at step 0, an early cancellation, a live run before its first call) | none |
  | `instructions_hash != ''`, `request_count > 0` | recorded | none |

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
| `not_recorded` | the record kind did not exist in the version that wrote the run (`instructions_hash = ''`) | upgrade weft and re-run |
| `derived` | the value was computed by the reader, not recorded (e.g. a ClickHouse row from before `0004` with `step = -1`) | — |
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
  before, and one `request` per reported attempt (§7). The `request` record is
  small; the two content payloads are stored once per distinct hash, so
  the cost grows with how often a run changes its prompt or catalog,
  not with its step count.
- A system prompt now reaches every content-on destination. An
  application whose prompt must not leave the process sets `Redact`
  for it or turns content off for that destination; the local sink and
  Studio default to on, as they do for the transcript.
- `obsdb` is ready before the emission: `DeriveRecord` positions the
  three kinds, `MetaOf` and the ClickHouse contract tuple exclude the
  new keys, and the ClickHouse run views fill the three run columns.
  SQLite's run-row fill, the transcript readers' growth filter and the
  messages count's exclusion of `compacted` records ship with A1 and
  A9.
- Every attribute name above joins ADR 0024's pinned set: changing one
  is an amendment to this ADR.
