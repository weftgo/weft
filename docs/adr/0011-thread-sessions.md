# ADR 0011 — `weft/thread`: sessions as an append-only entry tree

- Status: decided (2026-09-28, TODO §14; plan `docs/phase3-thread-plan.md`)
- Depends on: ADR 0001 (the message wire), ADR 0002 (`RunError.Result`,
  the partial transcript), ADR 0004 (events, `Seq`), ADR 0005 (module
  layout, the envelope integer, two-phase release), ADR 0007 (the
  approval boundary), ADR 0010 (run records — and what they are not),
  ADR 0014 (subagents), ADR 0019 (steering)
- Companion ADRs: 0020 (compaction), 0021 (approvals), 0022 (pool,
  proposed), 0023 (sandbox, proposed)
- Implementation: module `github.com/weftgo/weft/thread`

## Context

The core is stateless on purpose: messages in (`weft.Messages`),
`RunResult.Messages` out, the caller owns the transcript. Every real
application then rebuilds the same layer — a session id, the transcript
saved between turns, recovery after a crash, what to do with a message
sent while the agent is busy, long conversations that outgrow the
context window, approvals that outlive the process. THE-END-GOAL names
this tier (first `harness`, then `runtime`, renamed `thread` on
2026-09-19) and puts it in v1.

The run store (ADR 0010, `weft/store`) is not this layer. Its §7 says so:
a run record is not a message-only session and not a checkpoint. Runs
and sessions have different lifetimes, different readers, and different
write patterns (a run is written once as it happens; a session is
appended to for months and branched).

The field's scars decide most of the shape:

- **pi** keeps one JSONL file per session as a tree (`id`/`parentId`),
  branches in place, and rebuilds the model's view by walking from the
  leaf to the root. Three on-disk versions, auto-migrated on load.
- **Crush** flushes at turn end in a `WithoutCancel` window and records a
  canceled turn as an outcome, not an absence.
- **Codex #40805**: input acknowledged before it was durable — users
  notice. **Mastra #25120**: no id for an accepted message.
- **Vercel AI SDK harness**: settings are captured at turn start so a
  resumed turn cannot pick up configuration from a later one.
- **LangGraph** burned four checkpoint formats in 26 months (ADR 0010).

## Decision

### 1. Module and tier

`weft/thread` is its own module (ADR 0005). It imports the root module
and nothing else from weft — **not** `weft/store`: recording runs stays
the caller's choice (`store.Record` on their agent), and a session
works with no database at all. Packages:

| Package | Contents |
|---|---|
| `thread` | `Session`, `Turn`, `Send`, entries, branching, compaction (ADR 0020), approvals (ADR 0021), the `Storage` interface, `Memory()` |
| `thread/jsonl` | the default durable backend: one file per session |
| `thread/threadtest` | the conformance table every `Storage` must pass |
| `thread/sqlite` | second backend (v0.4), own module only if its driver would leak into `thread` |
| `thread/pool` | bounded concurrent child runs (ADR 0022) |
| `thread/sandbox` | the file firewall (ADR 0023) |

No package-level state, no registry, no `init()`.

### 2. A session is an append-only tree of entries

Every entry has an `ID`, a `ParentID` (empty for a root) and a time.
Nothing is ever rewritten or deleted in place; the **leaf** is the entry
the next one attaches to. The model's view (the context) is built by
walking leaf → root, applying the latest compaction on that path
(ADR 0020).

Entry kinds (sealed, like `weft.Event`; one wire discriminator each):

| Kind | Carries | In the model's context |
|---|---|---|
| `message` | one `weft.Message` (ADR 0001 wire, verbatim) | yes |
| `turn` | run id, stop reason, usage, steps, error text — the per-turn ledger | no |
| `compaction` | summary, first kept entry, tokens before, summarizer usage (ADR 0020) | the summary |
| `branch_summary` | the abandoned branch's summary, from-entry id | yes |
| `leaf` | moves the leaf to an existing entry (branch navigation) | no |
| `label` | a name for an entry (bookmarks, checkpoints in a UI) | no |
| `info` | session title and metadata edits | no |
| `custom` | application state: `{kind, data}` — survives compaction | no |
| `custom_message` | an application message: `{kind, message}` | yes |

Later releases add kinds (approvals in v0.2, steering receipts in v0.3,
pool receipts in v0.5) under the versioning rule in §6.

### 3. Branching

- **Navigate**: `Session.Branch(ctx, entryID, ...)` appends a `leaf`
  entry; the next turn continues from `entryID`. Optionally the branch
  being left is summarized first (`branch_summary`, ADR 0020 §6).
- **Fork**: `Session.Fork(ctx, entryID)` creates a new session whose
  header names the parent session and entry; the new file holds the
  path root → `entryID` as its first entries, so it is self-contained.
- Tools added mid-session are not inherited across a branch point: a
  branch rebuilds its context from its own path only (pi's
  `addedToolNames` invariant).

### 4. Turns

```go
turn, err := s.Send(ctx, weft.User("…"), opts...) // *Turn
for ev, err := range turn.Events() { … }          // forwarded run events
res, err := turn.Wait()                           // or just Wait
```

- One run per session at a time. `Send` on a busy session follows the
  session's busy policy: v0.1 `Queue` (follow-up, the default) or
  `Reject` (`ErrBusy`); v0.3 adds `Steer` and `Interrupt` (ADR 0019).
- **The prompt is durable before the run starts.** The user message is
  appended and synced, then the run starts. Every accepted `Send`
  returns a receipt id.
- Settings (run options, the agent) are captured at turn start.
- The session starts the run itself with `weft.Messages(context...)`,
  `weft.RunID(<session>-t<n>)` and the caller's extra run options
  (`thread.RunOptions(...)`; `weft.Messages`/`weft.Prompt` passed there
  are rejected). The session consumes the event stream and forwards it,
  so it observes every event without any core hook.
- At the end — success, failure, or cancellation — the run's new
  messages and a `turn` entry are appended in a `context.WithoutCancel`
  window. On failure the partial transcript from `RunError.Result` is
  kept after `weft.Repair`; a canceled turn is recorded as canceled.
- Per-step persistence (a crash mid-turn loses nothing emitted) arrives
  in v0.4 (plan §7).

### 5. Storage

```go
type Storage interface {
	Create(ctx context.Context, h Header) error
	Append(ctx context.Context, session string, entries ...Entry) error
	Load(ctx context.Context, session string) (Header, []Entry, error)
	List(ctx context.Context, q Query) (Page, error)
	Delete(ctx context.Context, session string) error
}
```

- `Append` of several entries is atomic: all or none become visible.
- One writer per session. A backend that can be opened by two processes
  locks (jsonl: an advisory file lock; second writer → `ErrLocked`).
- Optional capabilities are separate small interfaces discovered by type
  assertion (the `io.WriterTo` pattern), so the core interface never
  grows: e.g. `Watcher` (live tail), `Sizer`.
- **Loud on the unknown** (ADR 0010 §5): an unknown entry kind or a
  newer version is `ErrNewerFormat`, never skipped. A torn final line
  (crash mid-write) is dropped and reported through `Load`'s
  `LoadReport`; a malformed line elsewhere is `ErrCorrupt` naming the
  line, unless the caller opens with `thread.Salvage()`, which skips it
  and reports it. `weft.Repair` runs on every loaded context.
- Files and directories are created `0600`/`0700`; ids are validated
  against path traversal.

### 6. Stable now, scalable to later majors

The format and the API are designed to grow without breaking:

- **File envelope**: the header line carries `{"weft":1,"type":"session",…}`
  (ADR 0005's envelope integer). It changes only for a layout change a
  reader cannot handle additively.
- **Entry versions**: an entry kind added after format 1 carries `"v":N`,
  the minimum reader version. A reader seeing `v` above what it knows
  fails loudly; everything else is additive. Old files never need a
  rewrite to gain new features.
- **Readers read every version forever**; writers write the latest. A
  future layout change ships a reader for the old one and an explicit,
  atomic `thread.Migrate` (temp file + rename, the original kept) —
  never a silent rewrite on open.
- **Golden files** for every format version live in `thread/testdata`
  and are read by every release.
- **API**: functional options everywhere, small interfaces, sealed entry
  kinds, sentinel errors with `errors.Is`, `context.Context` first.
  Pre-1.0 minors may break with a CHANGELOG entry and an apidiff
  allowance; from v1.0 additive only (apidiff gate); a v2, if ever, is
  `github.com/weftgo/weft/thread/v2` with v1 kept importable and able
  to read v2 files it understands.

## Consequences

- A session is a file you can read with `jq`, back up with `cp`, and
  branch without losing history.
- The run store and the session store are separate; linking them is by
  run id (`<session>-t<n>`). A Studio "threads" view groups runs by that
  prefix (a later store/studio change, out of this ADR).
- Mid-turn crash durability waits for v0.4; until then a crash loses the
  in-flight turn's model output but never the prompt.
- `weft/thread` is a new module with its own tags (`thread/vX.Y.Z`),
  released after the root it requires (TODO §1.1).

## Rejected

- **Sessions in `weft/store`** — a second concern in the record store,
  a store minor and migration for every session feature, and ADR 0010
  §7 reversed. Rejected by decision 2026-09-28.
- **Linear sessions** — branching would be a format break later; the
  tree costs one `parent_id` per entry now.
- **A checkpoint engine** (resume mid-step) — weft's resume story is
  the approval boundary (ADR 0007, ADR 0010 §7).
- **Silent upgrade on open** — pi auto-migrates on load; a backup and an
  explicit call are cheaper than one lost session.
