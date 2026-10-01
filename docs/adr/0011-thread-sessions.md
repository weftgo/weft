# ADR 0011 — `weft/thread`: sessions as an append-only entry tree

- Status: decided (2026-09-28, TODO §14; plan `docs/phase3-thread-plan.md`);
  amended 2026-09-29 (§7, per-step turns) and 2026-10-01 (the
  amendment at the end — where it disagrees with the text above it,
  the amendment is the decision). The format and the API are **not
  frozen**: the freeze §6 describes is deferred (amendment §D)
- Depends on: ADR 0001 (the message wire), ADR 0002 (`RunError.Result`,
  the partial transcript), ADR 0004 (events, `Seq`), ADR 0005 (module
  layout, the envelope integer, two-phase release), ADR 0007 (the
  approval boundary), ADR 0010 (run records — and what they are not),
  ADR 0014 (subagents), ADR 0019 (steering)
- Companion ADRs: 0020 (compaction), 0021 (approvals), 0022 (pool,
  decided 2026-09-29), 0023 (sandbox — abandoned 2026-09-29, never
  decided)
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
| `thread/backend` | for backend authors: resolves the shared open options (`Config`, `Resolve`) |
| `thread/sqlite` | second backend (v0.4); its own module, because its driver would otherwise leak into `thread` |
| `thread/pool` | bounded concurrent child runs (ADR 0022) |

(`thread/sandbox`, ADR 0023's file firewall, was listed here; it was
abandoned on 2026-09-29 and never built.)

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
- Per-step persistence (a crash mid-turn loses nothing emitted) is
  §7's amendment, shipped in v0.4 (plan §7).

### 5. Storage

```go
type Storage interface {
	Create(ctx context.Context, h Header) error
	Append(ctx context.Context, session string, entries ...Entry) error
	Load(ctx context.Context, session string) (Header, []Entry, *LoadReport, error)
	List(ctx context.Context, q Query) (Page, error)
	Delete(ctx context.Context, session string) error
}
```

- `Append` of several entries is atomic against the writer's death:
  after a crash a later `Load` returns all of the batch or none of it.
  (It is not isolated from a concurrent reader in another process on a
  file backend — amendment §B, the torn-tail rule.)
- One writer per session. A backend that can be opened by two processes
  locks (jsonl: an advisory file lock; second writer → `ErrLocked`).
  Amendment §B states the whole rule: the lock is a lease, and between
  `Session` values it is per Session.
- Optional capabilities are separate small interfaces discovered by type
  assertion (the `io.WriterTo` pattern), so the core interface never
  grows. Four exist: `Flusher` (the deferred-fsync cadence), `Watcher`
  (live tail), `Releaser` (end the backend's hold) and `Leaser` (the
  per-Session writer lease). (`Sizer`, named here originally, was
  never built.)
- **Loud on the unknown** (ADR 0010 §5): an unknown entry kind or a
  newer version is `ErrNewerFormat`, never skipped. A torn final line
  (crash mid-write) is dropped and reported through `Load`'s
  `LoadReport`; a malformed line elsewhere is `ErrCorrupt` naming the
  line, unless the caller opens with `thread.Salvage()`, which skips it
  and reports it. `weft.Repair` runs on every loaded context.
- Files and directories are created `0600`/`0700`; ids are validated
  against path traversal.

### 6. Stable now, scalable to later majors

> 2026-10-01: this section is the design the format is built to — it
> is not yet a promise. The freeze that would make it one is deferred
> (amendment §D); the appendix documents the format as it is today.

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
  never a silent rewrite on open. (`thread.Migrate` does not exist:
  no layout change has needed it, and it is deferred with the freeze.)
- **Golden files** for every format version live in `thread/testdata`
  and are read by every release.
- **API**: functional options everywhere, small interfaces, sealed entry
  kinds, sentinel errors with `errors.Is`, `context.Context` first.
  Pre-1.0 minors may break with a CHANGELOG entry and an apidiff
  allowance; from v1.0 additive only (apidiff gate); a v2, if ever, is
  `github.com/weftgo/weft/thread/v2` with v1 kept importable and able
  to read v2 files it understands.

### 7. Per-step turns (amended 2026-09-29, v0.4)

Per-step persistence shipped through a core addition, not an event
rebuild: `weft.OnMessages` (ADR 0006's 2026-09-29 note; TODO §5.12's
shape (b)) is a run-scoped, read-only transcript observer — the loop
calls it with the exact messages as they join the run's transcript,
signatures and part boundaries included. The session appends each
batch as message entries the moment it joins, so a crash mid-turn
loses nothing emitted; the turn's end then appends only its
bookkeeping — the turn entry, receipts, the decision chain's entries —
never a message twice (the step messages, minus what already landed).

Why not the rebuild (the option plan §7 weighed): the event stream
cannot carry `ReasoningPart.Signature` (ADR 0004 never streams it), so
a rebuilt transcript is lossy for thinking models — unverifiable on
replay — and the loop's assembly rules (empty-turn suppression,
StopMaxTokens synthetic results, one batched tool message) would live
a second time in thread. The step put both options to the maintainer
on 2026-09-29; no answer arrived in the session, so this amendment
follows the step's own review bar ("the rebuilt or streamed step
messages equal RunResult.Messages exactly, including signatures") and
the plan's option-b release contingency (the v0.4 Release prompt
carries the two-phase root release). **Ratified by the maintainer on
2026-09-29**, after the v0.4 release: option (b) — the core addition
`weft.OnMessages` (root v0.5.0, ADR 0006's 2026-09-29 note) — is the
decided shape.

Consequences the amendment owns:

- A turn that dies mid-step may leave a dangling call in the tree: the
  raw tail is evidence; the loop's input repair answers it with the
  same golden bytes the turn-end repair used to write, so the model
  sees an identical transcript either way.
- A failed turn whose final form differs from its raw tail — an
  interrupted turn's golden completions, the repair's synthesized
  results — rewrites the whole tail on a fresh line (a leaf entry back
  to where the turn started): the active path holds exactly the bytes
  the turn-end batch always wrote, and the raw tail stays on its own
  branch, nothing deleted.
- An overflow re-run's failed attempt keeps its step messages on their
  own branch: "the failed attempt records nothing" (ADR 0020 §5)
  becomes "records nothing on the active path" — the leaf entry
  navigates back before the compaction, and the re-run continues from
  where the turn started.
- A resume's completed tool message (the core's attachResults) is a
  transcript join and persists when it joins, where the turn's end
  used to write it; and Resume's idempotency key is the armed resume,
  not the dangling tail — mid-resume, the tail can already read
  resolved.

## Consequences

- A session is a file you can read with `jq`, back up with `cp`, and
  branch without losing history.
- The run store and the session store are separate; linking them is by
  run id (`<session>-t<n>`). A Studio "threads" view groups runs by that
  prefix (a later store/studio change, out of this ADR).
- Mid-turn crash durability is §7: a crash loses at most the step in
  flight, never an emitted one, and never the prompt.
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

## Amendment 2026-10-01 — session core rules, the lease, the turn's durable shapes, and the deferred freeze

The 2026-10-01 review of the shipped module
(`WEFT-THREAD-REVIEW-2026-10-01.md`) found the decisions above
under-kept or under-stated in places. This amendment is the decision
restated where the code now differs; the companion amendments of the
same day are in ADR 0019 (a steer's receipt), ADR 0020 (compaction),
ADR 0021 (approvals) and ADR 0022 (the pool). Pre-1.0, so the API and
the wire moved where a fix needed it; the CHANGELOG lists every
breaking change with what to write instead.

### A. Session core rules (§2, §3 — "§8" in the fix notes)

- **Open validates the tree.** Every entry has a non-empty id that
  passes `ValidID` and appears once; every non-empty parent names an
  entry earlier in the file (append order makes that exclude cycles
  and parents the file does not hold); a `leaf` entry navigates to the
  root or to an earlier entry. A file that fails is refused with a
  `*CorruptError` (`errors.Is(err, ErrCorrupt)`) naming the line and
  the entry — a context is never quietly cut short at a broken link.
  Several roots are legal: `Branch` to the root starts a new one.
- **Salvage keeps orphans.** Under `thread.Salvage()` an entry whose
  parent was on a skipped line stays, as the root of what survives of
  its line, and is reported (`Session.LoadReport()` returns an
  `OpenReport`: the storage's `Torn` and `Skipped`, plus `Orphaned`).
  Nothing is re-linked by guessing. An orphaned `leaf` entry is
  ignored: the leaf stays where it was.
- **Open reads and nothing else.** It writes no entry, takes no lock
  or lease, and starts no run. Input the file shows accepted and never
  settled — steers and queued sends a stopped writer left — is
  restored to `Session.Queue()` and waits: the next turn delivers a
  restored steer, restored sends run ahead of the next `Send`,
  `Session.Continue` runs them now, `Session.ClearQueue` drops them.
- **The header is written once.** `WithMeta`, `PublicID` and
  `WithLineage` are create-only: `Create` and `Fork` honour them,
  `Open` refuses them with `ErrCreateOnly`. Keys under the reserved
  `weft.` prefix live in the header only: `SetInfo` rejects them with
  `ErrReservedKey`, so a public id cannot be rotated, and
  `Session.Meta()` never lets an info entry override one.
- **Fork copies the path and nothing that acts for the original.**
  The fork is built as `Open` builds a session and takes its own
  options; nothing is inherited from the origin's options or header,
  with one exception — a fork of a `RequireSigned` session requires
  signed decisions too, and takes the origin's keyring when given
  none.
  On the copied path: steers still queued are recorded as dropped in
  the fork; queued sends (accepted receipts) are the origin's — a
  reopened fork restores nothing from them; mirrored child approval
  requests are left out (the one case where a copied entry's parent
  link is rewritten, to the nearest entry the fork holds); unsettled
  pool receipts are settled `canceled`. An open approval boundary *is*
  inherited — each session resolves its own copy. A leaf entry is not
  a fork target. A fork
  that cannot write its entries is deleted again. `Fork` is a snapshot
  and is allowed while a turn runs; `Branch` is not (`ErrBusy`).
- **Snapshots share nothing.** `Entries`, `Path` and `Audit` return
  deep copies of every entry kind.
- **Close.** `Session.Close(ctx)` stops `Send` and `Continue` at once
  (`ErrClosed`), waits for the running turn and the queue to drain,
  seals the Session (every later write is `ErrClosed`; reads keep
  answering) and releases the storage's hold. If `ctx` ends first,
  Close cancels the running turn, ends the queued `Turn`s with
  `ErrClosed`, and returns `ctx.Err()`; a later `Close` completes the
  seal and the release. Sends queued behind an approval boundary
  nobody decides cannot drain: their `Turn`s end `ErrClosed`, their
  accepted receipts stay, and the next `Open` restores them.
- **A clock.** `thread.Clock(func() time.Time)` is the session's time
  source for the header and every entry it stamps, as `IDs` is for
  ids. Both run under the session's lock.

### B. Storage: the lock is a lease, and one Session writes (§5)

**Lock and lease.** One writer per session. A backend takes the
session's lock on the writer's first write (`Create`, `Append`, or a
`Leaser.Acquire`) and holds it until `Releaser.Release`, `Delete`, or
the death of the process — a lease the writer renews by writing, not a
lifetime: a later `Append` through the releasing `Storage` re-acquires
it, or fails with `ErrLocked` if another writer took the session in
between. Readers never lock.

- jsonl: `flock` on unix, `LockFileEx` on Windows, on the session
  file; the kernel drops it when the holder dies. A platform with
  neither fails `jsonl.Open` unless `thread.NoLock()` says the caller
  takes the rule on itself.
- sqlite: a row per session naming the holder by `Storage` instance,
  host, process token, pid and process start time. No row: taken. The
  same instance: proceed. Another host: `ErrLocked` — a holder there
  is never judged dead from here. The same process token: a live
  sibling `Storage` in this process, `ErrLocked`. This process's own
  pid under another token: an earlier incarnation (a restarted
  container's PID 1), taken over. A pid that is not a live process, or
  a live one whose start time differs from the row's: dead, taken
  over. Otherwise `ErrLocked`, the safe side. A takeover is logged.
- Memory: one process, one map — no lock to take; only the lease
  below.

**One writer, per Session.** A backend's lock tells `Storage` values
and processes apart; it cannot tell two `Session` values on one
`Storage` value apart, because `Append` names a session and not a
writer. The optional `Leaser` capability can: `Acquire(ctx, session,
holder)` takes the backend's lock as a first `Append` does and records
an opaque holder; `Yield(ctx, session, holder)` releases only its own
holder's lease; `Release` and `Delete` end the lease whoever holds it.
A `Session` acquires before every write — `Create` and `Fork` are
first writes — and yields in `Close`. While it holds the lease every
write of another `Session` on that `Storage` value fails with
`ErrLocked` and changes nothing. Reading takes no lease. A `Storage`
used directly, without Sessions, enforces one writer per instance; a
backend without `Leaser` gives Sessions no such check. Memory, jsonl
and sqlite implement it; `threadtest.RunLeaser` and `RunOneWriter`
pin it.

**Stale writers.** `Acquire` reports how many complete entry lines the
storage holds (a torn tail and the header are not counted). A Session
compares that with what it has loaded and written; a different number
means another writer appended since this Session opened, and the write
fails with `ErrStale`, nothing written — instead of attaching to a
leaf the session has moved past. It is a count, not a content
comparison: a session deleted and re-created to the same length behind
an open Session is not detected.

**Delete under a lease.** `Delete` through the `Storage` value the
writer uses removes the session whether or not a Session holds its
lease, and ends the lease; the Session's next write is `ErrNotFound`.
Through another `Storage` value, `Delete` of a held session is
`ErrLocked`.

**Torn-tail repair.** A writer that dies inside an append can leave a
final line without its newline. `Load` drops it and reports it
(`LoadReport.Torn`); `Watch` waits it out as a write in flight. The
next writer removes it before it appends — on taking the lock, and
after any failed write of its own: jsonl truncates to the last newline
and fsyncs, sqlite deletes the torn row, Memory clips its bytes — and
logs one `Warn` through `thread.OpenLogger`. So a crash costs the
half-written line and nothing after it. A file with no complete header
line is not repaired: it is `ErrCorrupt` on line 1, and its recovery
is `Delete` then `Create`. `Append` is atomic against the writer's
death; it is not isolated from a concurrent reader in another process,
which can see the write in flight as a torn tail.

**Keyset cursor.** `List` orders by `Created` descending, ties by `ID`
descending. `Query.Before` and `Query.BeforeID` are a keyset over that
order: a session qualifies when its `Created` is before `Before`, or
equal to it with an `ID` below `BeforeID`. A zero `Before` means no
cursor; `Before` alone is "strictly before", which skips the rest of
a group sharing that creation time. `Limit` 0 or negative is 50; above
500 clamps to 500. `Page.Total` counts the filter's matches, ignoring
the cursor and the limit. `Query.Meta` matches the header's
create-time metadata only, and a key must be present to match.

**Watch.** A watcher is a reader: the consumer may call the same
`Storage` from inside the loop. The stream ends with `ErrNotFound`
when the session is deleted, or deleted and created again, under it,
and with `ErrCorrupt` naming the line on undecodable data.

### C. Turns (§4, §7)

**Queued sends are durable at acceptance (§4).** "Accepted input is
durable input" now holds for every accepted `Send`, not only one that
starts at once. A send that waits for a turn of its own — the `Queue`
policy on a busy session, an interrupting send, a deferred steer's
follow-up — writes a `receipt` entry with status `accepted` carrying
the message, the id its prompt entry will take (`turn`) and the run id
minted for it, flushed before `Send` returns. It is settled by that
prompt entry landing, or by a `dropped` receipt. `Open` restores an
accepted receipt with neither to the queue. `Session.Queue()` lists
steers and queued sends (`QueuedSteer.Policy` tells them apart);
`ClearQueue` drops both. The steer queue is unbounded (ADR 0019's
amendment).

**Waiting on a turn (§4).** A turn is decided when its entries have
landed — before the between-turn compaction, so `Wait` never waits on
a summarizer; `Session.WaitIdle` waits for that housekeeping too, and
a `Send` arriving in that window is accepted under every policy and
runs next. `Turn.Done()` and `Turn.WaitContext(ctx)` let a caller stop
waiting without stopping the turn. `Turn.Outcome()` names the end:
running, answered, parked, delivered, deferred, dropped, failed,
canceled. The error `Wait` returns says what kind of end it was:
`*weft.RunError` (the run started and failed), or an error wrapping
`ErrNotRun` (no model was called), `ErrClosed`, `ErrDropped`
(`ClearQueue`, or a refused interrupt), `ErrTurnPanicked` (the
session's own machinery, or the caller's `IDs`/`Clock`), or
`ErrNotPersisted` (the run ended and its end could not be written; the
result rides beside it). `Canceled` on the turn entry covers a
deadline as well as a cancellation.

**Run ids (§4).** A run id `<session>-t<n>` is written before its run
starts — on the prompt entry (`run_id`), on an accepted receipt, and,
for an overflow re-run, on the failed attempt's turn entry (`rerun`).
`Open` recovers the counter from the highest id any entry records, not
from the number of turn entries, so a reopened session never mints an
id a crashed writer already used.

**The resume join (§7).** A resume's completed tool message goes
exactly where the core put it: directly after the assistant message
whose calls it answers. When the parked step left a partial tool
message there (one call ran, another parked), the join is appended as
a child of the assistant entry — a new line of the tree, the leaf
moving with it — so the active path holds one tool message, the
complete one; the partial stays on its own line, evidence like every
abandoned line. Messages an application wrote after the parked step
follow the join as fresh copies. The batch is one atomic append. A
call left dangling by a crash mid-step is not an approval boundary.

**Exactly once (§7).** The tree's tail for a turn is always a prefix
of the messages the run added. A step batch whose append fails is held
and written — in order, ahead of the next batch — by the next step or
by the turn's end, which compares the tree with the run's transcript
message by message and writes what is missing. Nothing is lost and
nothing is written twice; `TurnEntry.LateSteps` counts the appends
that landed late. If the turn's end itself cannot be written, `Wait`
returns an error wrapping `ErrNotPersisted`.

**Overflow (§7, ADR 0020 §5).** One run, one turn entry: an attempt
that overflowed and was re-run leaves its own turn entry — its run id,
the overflow in `err`, the usage of the steps it completed, `rerun`
naming the run that followed — and then the re-run's. `Session.Usage`
sums both: the attempt's tokens were spent.

**Run options.** `thread.RunOptions` rejects, with an error wrapping
`weft.ErrInvalidRunOption`, the options that are the session's own:
`weft.Messages`, `weft.Prompt`, `weft.RunID`, `weft.Steering`, and
the decision options `weft.Approve`, `weft.Deny`, `weft.Resolve`,
`weft.ResolveError` (decisions are recorded with `Session.Decide`).

### D. The freeze is deferred (§6)

By maintainer decision (2026-10-01) the "v0.8 freeze" — the format
spec as a compatibility promise, `thread.Migrate`, an emptied
`.apidiff-allow` — is deferred until the API has proven stable in
real use. Production readiness does not wait for it. Until then
breaking changes are made without deprecation shims and recorded in
the CHANGELOG. What a reader of a stored session can rely on **today**
is the reader's behaviour, not the format's permanence:

- an entry kind this build does not know, an entry `"v"` above what it
  reads of that kind, or a header envelope above `FormatVersion`,
  fails with `ErrNewerFormat` — never skipped, `Salvage` or not;
- every other unknown key is ignored;
- goldens for each format version live in `thread/testdata/format1` …
  `format5`, and the current build reads all of them.

## Appendix — Format reference (current, not frozen)

This documents the format the current build writes and reads. It is
**not yet a compatibility promise** (amendment §D): it is here so an
operator or a tool author can read a session file, and so a change to
it is a visible diff.

**The file.** A stored session is lines of JSON: the header first,
then one entry per line in append order. jsonl stores exactly those
lines in `<dir>/<id>.jsonl`; sqlite stores the same line bytes, one
per row.

**The envelope rule.** The header carries `"weft": 1`
(`thread.FormatVersion`, the integer every weft wire document
carries, ADR 0005). It moves only for a layout change a reader cannot
handle additively; new kinds, new optional keys and new entry
versions do not move it.

**The `"v"` rule.** An entry kind born in format 1 carries no `"v"`.
A kind added later is written with `"v": N`, the minimum reader
version; a format-1 kind that gains a field an older reader would
misread writes `"v": N` on the entries that carry that field. A
reader fails with `ErrNewerFormat` on a kind it does not know or a
`"v"` above the one it reads for that kind. A new optional key an
older reader can safely ignore is added without a version.

**Header** — `"type": "session"`:

| Field | Type | Notes |
|---|---|---|
| `weft` | integer | the envelope, `1` |
| `id` | string | the session id; 1–128 of letters, digits, `_`, `-` |
| `created` | RFC 3339 time | |
| `parent` | `{session, entry}` | a fork's origin; optional |
| `lineage` | `{parent_session, parent_call_id}` | a pool child's parent (format 4); optional |
| `meta` | object of strings | create-time metadata; optional. Keys under `weft.` are reserved: `weft.public_id`, `weft.require_signed` (`"true"`). A pool child made by a wrap also carries `pool_agent` (the wrap's name, its resume key) |

**Every entry** carries `type` (the discriminator), `id`, `parent`
(omitted on a root) and `created`. The kind-specific fields:

| `type` | Since | `"v"` | Fields (optional ones in *italics*) | In the model's context |
|---|---|---|---|---|
| `message` | 1 | — | `message` (the ADR 0001 message wire), *`run_id`* (on a turn's prompt entry) | yes |
| `turn` | 1 | — | `run_id`, `usage`, *`stop_reason`*, *`steps`*, *`err`*, *`pending`* (tool-call parts), *`canceled`*, *`policy`*, *`rerun`*, *`last_input`*, *`late_steps`* | no |
| `compaction` | 1 | — (5 with `trim`) | `first_kept`, `tokens_before`, *`summary`*, *`reason`* (`manual`, `threshold`, `overflow`, `from_hook`, `trim`), *`summarizer_usage`*, *`summarizer_model`*, *`files_read`*, *`files_modified`* (read, never written), *`pinned`*, *`range_hash`*, *`trim`* = `{stubs: [{entry, call_id, content, is_error}]}` | the summary |
| `branch_summary` | 1 | — | `summary`, `from_entry` | yes |
| `leaf` | 1 | — | `entry` (the entry the leaf moves to; empty is the root) | no |
| `label` | 1 | — | `entry`, `name` | no |
| `info` | 1 | — | *`title`*, *`meta`* | no |
| `custom` | 1 | — | `kind`, *`data`* (any JSON) | no |
| `custom_message` | 1 | — | `kind`, `message` | yes |
| `approval_request` | 2 | 2 | `call_id`, `tool`, `args_sha256`, `run_id`, *`args`*, *`reason`*, *`expiry`*, *`child`*, *`wrapper`* (the last two on a mirrored pool request) | no |
| `approval_decision` | 2 | 2 | `call_id`, `outcome` (`approve`, `deny`, `resolve`, `resolve_error`), *`reason`*, *`content`*, *`who`*, *`via`* (`user`, `signed`, `approver`, `grant`, `expiry`, `interrupt`, `child`, `parent`), *`run_id`*, *`nonce`*, *`key_id`*, *`request_id`*, *`always`* | no |
| `approval_audit` | 2 | 2 | `step` (`grant`, `approver`, `park`, `expiry`, `resume`, `signed`), *`call_id`*, *`outcome`*, *`detail`*, *`run_id`*, *`grant_id`*, *`grant_shared`*, *`key_id`*, *`decisions`* | no |
| `grant` | 2 | 2 | `tool`, *`args`* = `[{pointer, equals \| prefix \| glob}]`, *`deny`*, *`reason`*, *`expiry`*, *`max_uses`* | no |
| `grant_revoked` | 2 | 2 | `grant_id` | no |
| `receipt` | 3 | 3 | `status` (`queued`, `accepted`, `delivered`, `deferred`, `dropped`), *`receipt`* (the acceptance entry a later one settles), *`msg`*, *`run_id`*, *`turn`*, *`unanswered`* | no |
| `pool_receipt` | 4 | 4 | `status` (`accepted`, `running`, `parked`, `done`, `failed`, `canceled`, `capped`), *`receipt`*, *`child`*, *`call`*, *`prompt`*, *`stop`*, *`usage`* | no |

"Since" is the format step that introduced the kind: 1 is thread
v0.1, 2 approvals (v0.2), 3 steering receipts (v0.3), 4 the pool
(v0.5), 5 the trim record (the 2026-10-01 fix train). Fields and
status values added without a version, because an older reader loses
nothing by ignoring them: `message.run_id`; `turn.policy`, `rerun`,
`late_steps`; `approval_request.child`, `wrapper`;
`approval_decision.request_id`, `always`; `approval_audit.grant_id`,
`grant_shared`, `key_id`, `decisions`; `receipt.unanswered` and the
status `accepted`; the `pool_receipt` status `parked`; the header
metadata key `weft.require_signed`. Two of those change what an older
*build* does, not what it reads: one from before `accepted` does not
restore a queued send, and one from before `weft.require_signed` does
not enforce it.

Goldens: one file per kind and variant under
`thread/testdata/format1` … `format5`, with a whole session in
`format1/session.jsonl`; `TestReadEveryGolden` (formats 1–4) and
`TestTrimRecordGolden` (format 5) read them back and re-marshal them
to the same bytes.
