# ADR 0029 — the replay input is what the model saw

- Status: decided (2026-10-09; the devtools plan's item F1.1, the Go
  half of F1 "replay from here", closing A9's and A10's replay clauses
  and the phase 4 gate's "a replay across a compaction boundary
  reproduces the model's exact input"; decision 8 added the same day
  by F2.1, the edited request)
- Depends on: ADR 0007 (the approval boundary), ADR 0014 (subagents as
  tools), ADR 0020 (thread compaction), ADR 0024 (observability data),
  ADR 0028 §8 (the compaction view) and §9 (subagents), ADR 0019 (the
  steer shape an insert edit takes at rest)

## Context

A playground command re-runs a recorded run from step N
(`source.from_step`): the runtime feeds a fresh run the source's input
and its steps 0..N−1 and lets step N run again. Until now the kept
prefix was the run's *transcript* — the growth records concatenated —
because that is what `obsdb.DB.Transcript` and
`GET /api/runs/{id}/transcript` answer. When a `PrepareStep` rewrote
step N's request (a trim, a summary), ADR 0028 §8 records a run-scope
`compacted` view and the request's `messages_ref` names it: the model
saw the view, not the transcript. A replay from such a step fed the
model a prefix it never saw, so "replay from here" was not a replay.

The request a replay sends is model-visible behaviour (AGENTS.md rule
5): what it contains is a contract, hence this record.

## Decision

1. **The prefix for `from_step` N is step N's request messages**, as
   ADR 0028 §8 reads them: the growth records up to the request's
   `messages_ref.index`, concatenated; when that index names a
   run-scope view, the growth records below it with the view's
   `[from_seq, to_seq)` replaced by its body. A view applies only to the
   request that names it — a replay from a step before (or after) a
   compacted step uses that step's own request, often the original
   transcript. A session-scope `compaction` marker is never applied: the
   run's input record already holds the compacted context. `from_step`
   0 re-runs the turn from what it was fed (its step-0 `PrepareStep`
   runs again over the original input); no view applies there.
   `from_step` counts the run's own steps, unchanged — a compaction
   changes no step count. `from_step` may equal the step count when the
   last step ended in answered tool calls (a failed or budget-stopped
   run): the prefix is that step's request if one was recorded, else
   the whole growth transcript (derived; no view carries forward), and
   the replay's first model call answers those results; after a
   call-free reply it is refused.

   *The limit.* The runtime runs the agent (Studio never does), so the
   replayed agent's own `PrepareStep` runs over that prefix as it is.
   The replay's input is exact; what its model sees is exact when the
   agent's `PrepareStep` is idempotent over its own output — a count- or
   index-keyed `PrepareStep` re-shapes the replay's input (pinned by
   `TestReplayRerunsTheAgentsPrepareStep`). The runtime does not
   suppress or second-guess the agent's code.
2. **One assembly.** `obsdb.MessagesAsOf(ctx, db, runID, step)` (over
   `TranscriptBatches`, `Requests` and `Compactions`; `AssembleStep` for
   records already read, `ViewOf` and `ApplyView` its parts) is the one
   reader: Studio's transcript route reads through it, the wefttest
   fixture export picks and splices its views with `ViewOf` and
   `ApplyView`, and weft/runtime splices every view — read locally, or
   from Studio's answer — through `ApplyView`, so they agree byte for
   byte. A run without request records (before ADR 0028, or a
   content-off chain's request) falls back to the `from_step` cut rule
   and says so (`Derived`, the `derived` badge; the runtime logs a
   warning when it proceeds on one). Records that do not rebuild what a
   request names are a `*StepMessagesError` (`errors.Is` it
   `ErrStepMessages`) carrying its hole, never a guess: `gap` when the
   growth record a plain ref names is not stored (a lost view never
   passes as the original), a growth record below it is missing, the
   view does not fit, or a rewritten request has no view index (the
   growth is not what that model saw); `stripped` when no messages were
   captured.
3. **`GET /api/runs/{id}/transcript?step=N`.** Without `step` the route
   is unchanged (growth records only: a view is not transcript). With
   it, the answer adds `step`, `messages` — what step N's model call
   carried, the record's truth (decision 1's assembly), which is the
   replay prefix for N > 0; at N = 0 a compacted answer is not the
   replay's prefix, since `from_step` 0 re-runs step 0's `PrepareStep`
   over the original input — and `compacted_at`, the view's `{index,
   step, from_seq, to_seq, hash, replaced, entries}`, counts and hashes
   only, or `null` — plus `badge: "derived"` for the fallback. A view's
   messages are request content: a read-scoped panel token gets
   `messages: null` under `badge: "hidden"` (the counts and hash kept),
   as the export's compaction block does. A step the run never reached
   is 404, as `steps/{n}` answers it; records that do not rebuild the
   step are 409 with the error's hole (`gap`, `stripped`); the route is
   scoped like the bare one. The runtime's Studio path reads the view
   from it; its thread path (no request records) asks the local obsdb
   when it holds the run, else Studio — and there, records that cannot
   rebuild the step (content off, a lost record) leave the view unknown
   (logged) rather than refuse a turn the thread itself holds.
4. **Edits inside a compacted range are refused**, on both sides in one
   wording: an edit (a tool-result patch or a reply rewrite) whose
   message lies in the range `from_step`'s view replaced names a message
   the model never saw at that step — `call "c_1" of step 0 was
   compacted away before step 3's request (messages [1, 3) replaced by
   1): the model never saw it there; edit from an earlier from_step`.
   An edit outside the range applies, then the view is spliced in.
5. **A child run is its own source.** A command whose `source.run_id`
   is a subagent child's id (`<parent>/<step>/<call>`) and whose `agent`
   is the child's agent — registered on the runtime by that name —
   replays the child as its own run: `weft.playground`, no
   `weft.parent.run.id` / `weft.parent.call.id`, the parent untouched.
   A replay of the parent from the step that called the child runs the
   `Subagent` call the way the loop would, under its replay class: an
   unannotated `Subagent` tool is `never`, so the call parks, and
   substitute answers it from the recorded result (park leaves it for a
   human). The playground never re-runs a child from outside the loop.
6. **The link back** is the replay's `weft.forked_from =
   "<source run id>#<from_step>"` (unchanged), for a child source too.
7. **The sampling override is laid over the agent's own** (plan F3):
   a playground run's `options.temperature` and `params` are applied as
   one `weft.Params` built from the agent's `Params()` with the sent
   fields replaced, so a temperature-only command keeps the agent's
   `max_tokens`, `top_p`, `stop` and `seed` (a run-level `Params`
   alone would replace the struct whole); an absent field keeps the
   agent's value, and a command cannot clear the agent's `stop` or
   `max_tokens`.

8. **The edited request** (plan F2, item F2.1). A command's
   `transcript_edits` is one list; each edit's optional `kind` is the
   discriminator, and an edit without one reads as before F2 (a
   `tool_result` + `call_id` patch, or a `content` rewrite of a
   call-free reply — kinds `tool_result` and `reply`), so every body
   recorded before it still validates. Three kinds join them:

   - `user` (`step`, `content`, optional `index`) rewrites a user
     message of a kept step. A step's user messages are, in order: for
     step 0 the turn's prompt (the input's last message, when it is a
     user message), then the user messages the step's records hold (a
     steer delivered after its tool batch, a resumed turn's prompt in
     its step-0 tail); `index` (0 by default) picks one. The first text
     part takes the new text, the other text parts go, an image or a
     file stays. Earlier turns' messages in the input are not editable.
     A user edit of step 0 is what `input` is for `from_step` 0, so
     `input` beside `from_step` > 0 is refused pointing at it.
   - `tool_args` (`step`, `call_id`, `args`) rewrites a kept call's
     arguments: a JSON object, checked against the tool's input schema
     (`obsdb.CheckToolArgs` — the runtime against its agent's tool,
     Studio against the run's tools record; a tool with no schema at
     hand needs only an object) and refused in the loop's own
     `INVALID_INPUT: tool "x": field "f": expected …, got …` wording.
     The call keeps its id, name and signature; its result stays unless
     a `tool_result` edit rewrites it too.
   - `insert` (`step`, `content`) adds a user message at the boundary
     before step `step`'s model call, `0..from_step`: after step
     `step − 1`'s tool results — where ADR 0019's steer delivered after
     that step's tool batch lands, at rest. Inserts at one boundary
     keep the command's order.

   Every edit applies to the transcript first; then from_step's view
   (decision 1) is spliced in and the inserts are placed, a boundary at
   or past the view's range moved with it. The kept-prefix invariants
   stand: the prefix ends at a step boundary, every kept call has its
   result, nothing before `from_step` executes. An edit inside the view's
   range is refused in decision 4's words; so is an insert whose
   boundary lies strictly inside it (`the boundary before step N was
   compacted away before step M's request …`). Every refusal is worded
   once, in the runtime (authoritative, before the ack) and in Studio
   (the mirror, a 400), and both are pinned.

   *The mark.* A replayed run whose prefix was edited carries
   `weft.edits` (run metadata, on every span and record, in the run
   row's `meta`): one token per edit in the command's order,
   comma-joined — `<step>:<call_id>:args` (the pair's "args edited"
   mark), `<step>:<call_id>:result`, `<step>:reply`, `<step>:user`
   (`<step>:user:<index>` past the first) and `<step>:insert`. Past
   core's 1024-byte metadata value it ends in `+<n> more`.

   *The scripted engine.* Its recorded turns are keyed on what the
   model saw; every edit changes that, so a scripted command with
   edits is refused before the ack — `the scripted engine would replay
   the recorded turn N, which answered a different prompt: transcript
   edits need engine live` — never acked and failed "no recorded turn".

   *The preview.* `POST /api/playground/preview` (capability `preview`,
   with the playground) takes the command's body and answers the first
   request the replay would send — system, messages, tools, model,
   params, thinking, tool choice — beside step `from_step`'s recorded
   request and a diff of the two (messages aligned same / changed /
   added / removed; system, tools and knobs). It is assembly in Studio's
   process from the records (`MessagesAsOf`, the edits, the overrides
   laid over the registered agent's defaults, else the recorded
   request's): no model call, no tool, no runtime needed. It promises
   the messages exactly; what the model sees is exact when the agent's
   PrepareStep is idempotent over its own output (decision 1's limit,
   a warning on every answer). The scripted engine's refusals are
   warnings there, in the run route's words; the system text with an
   instructions override is shown as written (the loop appends tool
   prompt snippets the record does not hold — a warning); overrides
   only a registration can check are listed `unchecked` when no
   runtime holds the agent. A read-scoped panel token gets the system
   prompt and the catalog hidden, and the messages hidden when a view
   is in them.

## Consequences

- "Replay from here" on a step after a compaction reproduces the
  model's exact input; `runtime`'s
  `TestReplayAcrossCompactionReproducesTheModelsInput` pins it against
  the request records over both the local and the Studio path.
- The scripted engine keys the recorded turns from `from_step` on over
  the compacted prefix too, so a scripted replay across a compaction
  still answers from the record.
- F2's preview says which prefix a replay uses (`compacted_at`) and
  shows the edited request before it is sent (decision 8); the replayed
  run's `weft.edits` says what was edited, the pair's args included.
- A Studio older than the parameter answers the bare transcript; the
  runtime then cannot know a view and logs that it used the original.
