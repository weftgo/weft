# ADR 0029 — the replay input is what the model saw

- Status: decided (2026-10-09; the devtools plan's item F1.1, the Go
  half of F1 "replay from here", closing A9's and A10's replay clauses
  and the phase 4 gate's "a replay across a compaction boundary
  reproduces the model's exact input")
- Depends on: ADR 0007 (the approval boundary), ADR 0014 (subagents as
  tools), ADR 0020 (thread compaction), ADR 0024 (observability data),
  ADR 0028 §8 (the compaction view) and §9 (subagents)

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
   runs again); no view applies there. `from_step` counts the run's own
   steps, unchanged — a compaction changes no step count.
2. **One assembly.** `obsdb.MessagesAsOf(ctx, db, runID, step)` (over
   `TranscriptBatches`, `Requests` and `Compactions`; `AssembleStep` for
   records already read, `ViewOf` and `ApplyView` its parts) is the one
   reader: weft/runtime's local path, Studio's transcript route and the
   wefttest fixture export all use it, so they agree byte for byte. A
   run without request records (before ADR 0028, or a content-off
   request) falls back to the `from_step` cut rule and says so
   (`Derived`, the `derived` badge); records that do not rebuild the
   count a request names are an error (`ErrStepMessages`), never a
   guess.
3. **`GET /api/runs/{id}/transcript?step=N`.** Without `step` the route
   is unchanged (growth records only: a view is not transcript). With
   it, the answer adds `step`, `messages` (decision 1) and
   `compacted_at` — the view's `{index, step, from_seq, to_seq, hash,
   replaced, entries}`, counts and hashes only, or `null` — plus
   `badge: "derived"` for the fallback. A step the run never reached is
   404, as `steps/{n}` answers it; the route is scoped like the bare
   one. The runtime's Studio path reads the view from it; its thread
   path (no request records) asks the local obsdb, then Studio.
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

## Consequences

- "Replay from here" on a step after a compaction reproduces the
  model's exact input; `runtime`'s
  `TestReplayAcrossCompactionReproducesTheModelsInput` pins it against
  the request records over both the local and the Studio path.
- The scripted engine keys the recorded turns from `from_step` on over
  the compacted prefix too, so a scripted replay across a compaction
  still answers from the record.
- F2's preview can say which prefix a replay uses (`compacted_at`).
- A Studio older than the parameter answers the bare transcript; the
  runtime then cannot know a view and logs that it used the original.
