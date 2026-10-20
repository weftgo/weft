# Running `weft/thread` — the operator's page

What a person who deploys, backs up, restarts and debugs sessions needs
to know. The godoc is the API reference
(`go doc github.com/weftgo/weft/thread`), [ADR 0011](adr/0011-thread-sessions.md)
is the design record and holds the format reference; this page is the
operating rules, each one checked against the code.

`weft/thread` is pre-1.0 and **not frozen**: the API and the stored
format may change between minor versions, and the CHANGELOG says how.
Nothing here is a compatibility promise. What you can rely on today is
how a reader behaves when it meets something it does not understand:
it fails loudly (§7), it never guesses.

## 1. What is on disk

| | `thread/jsonl` | `thread/sqlite` (the one package that imports the SQLite driver) |
|---|---|---|
| Layout | one directory; one `<id>.jsonl` file per session | one SQLite database file holding every session |
| A session | the header line, then one JSON line per entry, append-only | the same line bytes, one per row (`sessions.header`, `entries.line`) |
| Permissions | files `0600`; the directory `0700` when `Open` creates it (an existing directory keeps its mode) | the file `0600` and its directory `0700` when `Open` creates them |
| Side files | none | `<file>-wal` and `<file>-shm` beside the main file (WAL mode, set once on the file) |
| Lock | an advisory lock on the session file | a row per session in `session_locks` |
| Schema | the format itself | embedded migrations, tracked in `thread_migrations`; a database from a newer weft fails `Open` with `sqlite.ErrNewerSchema` |

`thread.Memory()` keeps sessions in a map and loses them with the
process; it is for tests.

A session id is 1–128 letters, digits, `_` or `-` (`thread.ValidID`),
so it is always exactly one path component. Read a jsonl session with
`jq -c . <dir>/<id>.jsonl`; the entry kinds and their fields are in
ADR 0011's format reference.

Both backends store the same lines, so they differ in where the bytes
live, never in what they say.

## 2. Durability

- **jsonl** fsyncs every `Append` before it returns (and the file and
  directory at `Create`). With `thread.FsyncOnFlush()` the fsync moves
  to the turn's end — the session flushes the prompt before the run
  and again when the turn lands — which is cheaper and can lose the
  unsynced tail of a turn in a crash, never a synced one.
- **sqlite** commits every `Append` as one transaction, in WAL mode.
  The fsync policy is SQLite's `synchronous` level. The default,
  `thread.FsyncEveryAppend()`, is `synchronous=FULL`: the log is
  fsynced at every commit, so an entry survives a power cut as well as
  the process dying. `thread.FsyncOnFlush()` is `synchronous=NORMAL`:
  durable against the process dying, and a power loss or kernel crash
  can roll back the most recent commits (the database is not
  corrupted); `Flush` checkpoints the log, which syncs it.
- An `Append` batch is atomic against the writer's death: after a
  crash a load returns all of it or none of it.

## 3. Backups

**jsonl.** `cp` (or rsync, or a filesystem snapshot) of the directory
is a backup, at any time, with writers running. A file copied while
its writer is mid-append can end in a half-written line; the copy
still loads — the torn line is dropped and reported (§6) — and it is
missing at most that one batch. Restore by putting files back. The
advisory lock is not in the file, so a restored copy is unlocked.

**sqlite.** The main file alone is **not** a backup while the
database is in use: committed data can still be in `-wal`. Use
SQLite's own online backup (`sqlite3 sessions.db ".backup out.db"`, or
`VACUUM INTO 'out.db'`), or stop every writer and copy the main file
together with `-wal` and `-shm`. One more thing travels with a sqlite
backup: the `session_locks` rows of whichever writers held sessions
when it was taken. Restored on the same host they belong to a dead
process and are taken over on first write (one `Warn` line each).
Restored on a host with a different hostname they are never taken
over (§4) — clear the table after such a restore.

Neither backend has export, import or encryption at rest; encrypt the
volume.

## 4. Locks and leases — one writer per session

There are two layers, and they answer different questions.

Neither, nor the backend or its durability, is recorded: Studio's
session page shows a `not_recorded` storage badge unless the app names
them itself on every turn —
`thread.WithRunMetadata(map[string]string{"storage": "jsonl · per-step durable"})`
— which the page then shows as its storage line, a first clue when a
write fails with `ErrLocked`.

**The backend's lock: which `Storage` value, in which process, may
write.** It is taken on a session's first write (`Create`, `Append`,
or a Session's first write) and held until the session is released,
deleted, or the process dies. A second writer gets `thread.ErrLocked`,
naming the session. Readers never lock: `Load`, `List`, `Watch`,
`thread.Open` and every read of a `Session` always work.

- jsonl: `flock` on unix, `LockFileEx` on Windows, on the open session
  file. The kernel drops it when the holder exits, however it exits —
  a crashed writer never strands a session. The price is one open file
  descriptor per session a process is writing, until that session is
  closed; size your fd limit for the sessions you hold open at once.
- sqlite: the lock row names its holder by `Storage` instance, host,
  a random per-process token, pid and process start time, and a later
  writer decides from those whether the holder is dead:
  - the same process (another `Storage` value in it): alive,
    `ErrLocked`;
  - this process's own pid under another process token — a restarted
    container that is PID 1 again: an earlier incarnation, taken over;
  - a pid that is not running, or one that is running but started at a
    different time than the row records (the pid was reused): dead,
    taken over;
  - a holder with a **different hostname**: never judged dead from
    here, `ErrLocked`. The start time is read from `/proc` on Linux
    and from the kernel on macOS and Windows; on other unixes it is
    unknown, and an unrelated live process wearing a dead holder's pid
    keeps the session locked until it exits.
  A takeover is logged as one `Warn` (through `thread.OpenLogger`,
  `slog.Default()` otherwise).

**The lease: which `Session` value may write.** Two `Session` values
on one `Storage` value look identical to the backend, so a Session
names itself: it takes the session's lease with its first write
(`Create` and `Fork` are first writes) and keeps it until
`Session.Close`. Meanwhile another Session on the same `Storage` value
opens and reads normally and every write it makes fails with
`ErrLocked`, changing nothing. A Session that was opened before
another writer appended — and so would attach to a leaf the session
has moved past — fails its write with `thread.ErrStale` instead.

Consequences for a deployment:

- **Containers.** A restarted container that keeps its hostname
  reclaims its sqlite sessions on its own (same pid, new process
  token). A *replacement* container with a new hostname — the default
  for a new pod or `docker run` — does not: the old rows read as
  another host's. Give session writers a stable hostname, or close
  sessions on shutdown (§5) so no rows are left. Once the old host is
  known to be gone, `sqlite.BreakLock(ctx, st, session)` removes a
  session's lock row; the same applies to a database restored from a
  backup that carried its lock rows. You vouch that the holder is
  gone: nothing can check it. A holder that was alive after all is
  stopped at its next write, which finds the row is no longer its own
  and fails with `ErrLocked`.
- **Several hosts.** jsonl's lock is only as good as the filesystem's
  `flock` (network filesystems vary; some lie). A sqlite database on a
  shared filesystem is outside SQLite's own supported envelope. Run
  one writer host per directory or database; readers elsewhere are
  fine on jsonl.
- **`thread.NoLock()`** opens jsonl without the advisory lock — for a
  platform that has none (there `jsonl.Open` fails without it) or a
  filesystem whose locks cannot be trusted. One-writer-per-session
  then becomes your promise: two processes writing one session will
  interleave lines. sqlite and Memory accept the option and keep
  locking.
- **Stale detection is a count plus the header's creation time.** A
  Session compares the number of complete entry lines the storage
  holds with the number it has seen, and the stored header's `Created`
  with the one it loaded. It catches a writer that appended behind it
  and a session deleted and re-created under the same id; it is not a
  content comparison of the entries.
- `s.Claim(ctx)` asks the lease question ahead of a write: it takes the lease for this Session (nil for the holder), or fails `ErrLocked` / `ErrStale` / `ErrClosed` and changes nothing — on `ErrStale` the lease the check took is given back, so the next Session opened on the session can claim it — weft/runtime's `Sessions` asks it before acking a debugger's decision. A Claim with no later write still holds the lease (jsonl's `flock`, sqlite's lock row) until `Close`, as one that wrote does: claim only with a Close to come.
- `s.Steer(ctx, runID, msgs...)` is the steer for a caller that does not own the turn (the devtools debugger): delivered into the turn running now when it is `runID` (`""`: any running turn), exactly as `Send(…, As(Steer))` on a busy session; `ErrNotRunning` and nothing written when none is — idle, between turns, parked, or a session reopened from a crash mid-turn (its prompt is durable, but nothing runs in the reopened value) — or when the running turn is another run (the error names both). A delivered steer that misses its drain point (StopWhen, a park, the run ending first) becomes a follow-up turn under ADR 0019's rule. A multi-message Steer whose later append fails has accepted the earlier messages.
- **A Session someone forgot to close** holds its lease for the life
  of the process. `st.(thread.Releaser).Release(ctx, id)` ends the
  hold whoever has it — the escape hatch, to be used only when that
  Session is truly abandoned (its next write is refused once another
  writer has written).
- The Windows paths (the `LockFileEx` lock, process liveness) compile
  and pass `GOOS=windows go vet`, run by hand — CI has no Windows job
  — and have never been executed by a test. Treat Windows as untested.

## 5. Close and shutdown

`Session.Close(ctx)`, in order: `Send` and `Continue` fail with
`ErrClosed` at once; Close waits for the running turn and the queue
behind it to drain; the Session is sealed (every later write is
`ErrClosed`, reads keep answering); the lease and the backend's hold
are released, and with them jsonl's file descriptor and sqlite's lock
row.

If `ctx` ends first, Close cancels the running turn — it is recorded
as canceled — ends the queued turns with `ErrClosed`, and returns
`ctx.Err()`. The canceled turn may still be writing its end: call
`Close` again to finish sealing and releasing.

What Close does not lose: sends still queued when it gives up keep
their `accepted` receipts in the file, steers their `queued` ones, and
the next `Open` restores both to the queue (§8).

A shutdown sequence that leaves nothing behind:

1. Stop accepting requests.
2. `pool.Close(ctx)` for each pool: running children are canceled and
   settle their receipts, parked ones stay parked for the next
   process. Session.Close does not wait for pool children.
3. `s.Close(ctx)` for each open session, with a deadline you can
   afford.
4. Exit.

Skipping it is survivable — that is what the crash rules are for —
but it costs the in-flight turn, and on sqlite it leaves lock rows for
the next process to take over (§4).

Never call `Close` from inside the session's own run (a tool, a hook):
it would wait for the turn that is calling it.

## 6. After a crash

**The torn tail.** A writer killed inside an append can leave a final
line without its newline. Nothing needs to be done by hand:

- a load drops it and says so — `LoadReport.Torn` from
  `Storage.Load`, `Session.LoadReport()` on an opened session, and one
  warning through the agent's logger;
- the next writer removes it before appending (jsonl truncates to the
  last newline and fsyncs; sqlite deletes the torn row) and logs one
  `Warn`;
- a live tail (`Watch`) treats it as a write in flight and waits.

So a crash costs the half-written batch and nothing after it.

**A damaged line elsewhere** fails the load with `thread.ErrCorrupt`
(a `*thread.CorruptError` naming the session, the line and, when the
tree is the problem, the entry). Opening the backend with
`thread.Salvage()` skips such lines instead and reports them
(`LoadReport.Skipped`); entries whose parent was on a skipped line are
kept and listed (`OpenReport.Orphaned`), and the model's context for a
leaf below one starts at that entry — everything before the skipped
line is off the path. Salvage is a recovery tool: open with it, read
`s.LoadReport()`, decide whether the session is still usable. It never
skips data from a newer weft.

**A file with no complete header line** (a crash inside `Create`) is
`ErrCorrupt` on line 1 and is not repaired; `thread.Delete` it and
create the session again.

**The interrupted turn.** Its prompt and every message its steps
emitted are in the file. A tool call left without a result is given
an error result by the loop's input repair, so the next turn's model
sees a complete transcript. The turn entry may be missing; the run id
is not reused (it is on the prompt entry).

**Accepted input that never ran** is back in `s.Queue()` after `Open`
(§8). `Open` never runs anything on its own.

**Pool delegations** a dead process left unsettled: §10.

## 7. Errors: what to do with each

Match with `errors.Is`.

| Error | Class | What it means | What to do |
|---|---|---|---|
| `ErrBusy` | retry | a turn is running (a `Reject`-policy `Send`, `Branch`, `Compact`, `ApplyCompaction`, `Uncompact`) | wait for the turn (`turn.Wait`, `s.WaitIdle`) and call again, or send under `Queue` |
| `ErrLocked` | retry | another writer holds the session — another process, another `Storage` value, or another `Session` that has not closed | retry after that writer closes; if it is dead on sqlite see §4 |
| `ErrStale` | reopen | the stored session moved on behind this Session | `thread.Open` it again and redo the write |
| `ErrClosed` | reopen | this Session's `Close` has run | `thread.Open` it again |
| `ErrNotFound` | terminal for the call | no such session (or it was deleted under an open Session) | — |
| `ErrExists` | terminal for the call | `Create` over an existing id | pick another id |
| `ErrCorrupt` | terminal for the data | a line this build cannot decode, or a tree that does not hold together | inspect the line named; reopen the backend with `thread.Salvage()` to read around it |
| `ErrNewerFormat` | terminal for this build | written by a newer weft: an unknown entry kind, a newer `"v"`, a newer header envelope | upgrade the reader; Salvage does not skip it. `List` cannot see a session whose *header* is newer |
| `ErrAwaitingApproval` | wait | compaction while approval requests are pending | decide them first |
| `sqlite.ErrNewerSchema` | terminal for this build | the database was migrated by a newer weft | upgrade |

How a turn ended is on `turn.Outcome()`, and the error `turn.Wait()`
returns says why: a `*weft.RunError` (the run started and failed — the
partial transcript is in `.Result`), or an error wrapping `ErrNotRun`
(no model was called), `ErrDropped` (`ClearQueue` removed it),
`ErrClosed`, `ErrTurnPanicked`, or `ErrNotPersisted` — the run ended
but its end could not be written; treat that as a storage incident,
the turn's usage is in no ledger. A turn entry with `late_steps > 0`
means step writes failed and landed late: nothing was lost, and the
storage was unhealthy for that long.

The decision errors (`ErrNotPending`, `ErrExpired`, `ErrDelegated`,
`ErrInvalidDecision`, `ErrSignatureRequired`, `ErrBadSignature`,
`ErrReplay`, `ErrArgsChanged`) each refuse one decision and record
nothing; godoc says which call returns which.

## 8. The queue

A `Send` that meets a busy session is, by default, queued — and
durable: an `accepted` receipt entry is written and flushed before
`Send` returns. A steer writes a `queued` receipt the same way. After
a crash or a `Close` that gave up, `thread.Open` restores both to
`s.Queue()`; nothing runs until the application says so:

- the next `Send` runs behind the restored sends, in order, and a
  restored steer is delivered into the first turn that runs;
- `s.Continue(ctx)` runs them now, with no new message;
- `s.ClearQueue(ctx)` drops them, on the record.

**A failed `Send` of a message that will not run leaves the line as
it found it.** When the prompt entry or the `accepted` receipt is
written but its flush fails — or an `Interrupt`'s denial of the parked
calls cannot be recorded — `Send` returns the error, the receipt is
settled `dropped` (a reopen does not run it) and a `leaf` entry moves
the leaf back to where it stood, the two in one append, flushed. That
is best effort: if the append is refused too, the in-memory leaf still
moves and a reopen restores the send from its undropped receipt (the
log says so). The idle path and the queued one read the same after the
failure, so it does not matter which one a `Send` took — and the one
right after a turn's `Wait` can take either: `Wait` returns once the
turn's entries have landed, while the runner is still between items
(the automatic compaction runs there), and a `Send` in that window is
accepted and queued behind it (after a turn that parked, the approval
boundary holds the session instead and the policy's boundary rules
apply). Call `s.WaitIdle(ctx)` first when the next `Send` must find
the session idle.

A steer is the exception: a `Steer` whose `queued` receipt was written
but not flushed returns the error, yet the receipt is on disk and a
reopen would restore it, so the live session matches — the steer
defers to a follow-up turn and runs. A steer whose deferral at its
turn's end was written but not flushed keeps it the same way: its
`Send` succeeded, the follow-up runs under the turn its `accepted`
receipt names, and a reopen before it ran restores it, never after. A
deferral refused outright — nothing written — runs from memory too,
and the steer's `queued` receipt is what a reopen sees: the follow-up's
prompt is appended together with the `deferred` receipt that settles
it, so a reopen before the prompt landed restores the steer once, and
one after restores nothing. A steer the running turn drained
is in that run's transcript at once, its `delivered` receipt at the
turn's end: after a crash between the two, `Open` reads the message on
the line as the delivery and does not restore the steer. The live
session reads it the same way when the storage refuses the turn's end
batch: a drained steer whose message is already on the line ends
delivered, its `delivered` receipt written on its own, not deferred
into a second copy.

**Steer follow-ups inherit.** A steer that cannot join the running
turn becomes a follow-up turn under that turn's run options (and the
values on its context), the steer's own options after them — a turn
sent with `weft.ParkAllExcept` keeps its follow-ups parked. A steer to
an idle session runs under its own options. Restored messages run with
neither: run options are not entries.

**The queue is unbounded.** The session does not cap how many steers
or sends may wait; each costs one entry. If users can pile messages
onto a running turn, check `len(s.Queue())` before sending.

## 9. Costs and limits

Numbers are from the budget tests, measured on the maintainer's
machine on the dates given; CI enforces generous bounds around them,
not the numbers themselves.

- **Opening a session reads all of it** into memory, and a `Session`
  holds its whole tree. A 100,000-entry session opens in about 3 s on
  both backends (2026-09-30).
- **`List` on jsonl reads every header in the directory on every
  call** — the directory is the index. A page over 10,000 sessions
  takes about 70 ms (2026-10-01) and allocates a header's worth per
  file. Cost grows with the fleet, not the page. A header line longer
  than 1 MiB is not listed.
- **`List` on sqlite pages in SQL**: the page is an index range read
  and `Total` a count over the same index; walking 10,000 sessions a
  hundred at a time takes about 0.17 s in all (2026-10-01).
- **`Query.TitleSearch`** costs more than the rest: jsonl reads each
  candidate session's whole file to find its title; sqlite scans a
  title column.
- `Query.Meta` matches the header's create-time metadata only — a key
  added later with `SetInfo` never matches — and a key must be present
  to match.
- `List` skips what it cannot decode (a torn or foreign header, a
  newer envelope) without an error; `Load` on that id says why.
- **Paging**: newest first, 50 per page by default, 500 at most. Pass
  the last session's `Created` and `ID` as `Before` and `BeforeID`;
  `Before` alone skips sessions sharing that creation time.
- **A Session's lock is held across its storage writes.** With the
  default fsync policy every method of that Session — reads included
  — waits out an fsync in progress.
- **`Watch` polls** every 200 ms and reads only what is new.
- **`Delete` does not look for open Sessions.** Close first; a turn
  running at that moment loses the entries it had yet to write.

## 10. Pool operations

- **One pool per storage's delegations.** `Recover` treats an
  unsettled receipt that no child of *this* pool is running as left by
  a dead process. With two live pools over the same sessions, a
  `Recover` or `Cancel` through one settles the other's running work
  as failed.
- **After a restart**, for each parent session the new process takes
  over: wrap or register the agents first, then
  `p.Recover(ctx, parent)`. It reattaches children parked at an
  approval (mirroring any request the crash left unmirrored), settles
  children that finished from their own files, and settles `failed`
  those that never finished their first run — that run is not started
  again. A child that was resuming when the process died is still at
  its decided boundary (a resume that started and did not finish
  leaves it resumable, as in any session), so `Recover` resumes it
  again: **an approved call whose result was not recorded runs
  again**, at any depth of a chain. Nothing on file tells a call that
  never started from one cut off half-way, and `weft.Replay`'s
  side-effect classes are the playground's, not a session resume's:
  a tool that must not run twice has to be idempotent itself (key it
  by the call id, `weft.CallFromContext`). A child
  whose agent the pool does not hold is reported (`pool.ErrNoAgent`)
  and left for a later call. Limits: a finished child's answer is read
  back as its last assistant text (a structured `Output` answer is not
  recovered as such), and the cycle guard above a rebuilt child
  restarts at that child's own agent — the depth limit still holds.
- **Wrap names are resume keys.** A child made by `p.Wrap`/`MustWrap`
  records the wrap's name; a restarted process that wraps the same
  agents under the same names can resume them. Renamed a wrap, or used
  `Submit`? `p.Register(childID, agent)` before `Recover`.
- **Receipt states** (`pool.Receipts(parent)`): `accepted` (queued for
  a slot) → `running` ⇄ `parked` (at an approval, holding no slot) →
  exactly one of `done`, `failed`, `canceled`, `capped`. A receipt
  that stays `accepted` or `running` across a restart needs `Recover`.
- **Decisions.** `p.Decide(ctx, parent, ds...)` records in the parent
  and queues the children that are now decided; it does not wait.
  `p.Wait(ctx, parent, receiptID)` blocks until that delegation is
  settled or parked again. Decisions taken directly on the parent
  session, expiries, and an interrupting `Send` that denies nested
  requests reach the children when the pump next runs:
  `p.Decide(ctx, parent)` with no decisions is the pump alone and is
  always safe to call. A grandchild's park (lead → child → grandchild,
  each a sync wrap) surfaces on the lead and one decision there
  resumes the chain: the grandchild runs, its answer resumes the
  middle child, whose answer is the lead's tool result — in one
  process and after a restart that wraps all three under their names
  again (before 0.18.1 the middle delegation settled failed with "the
  child's resume did not survive a restart"). That failure is now only
  a restart's whose grandchild has no agent to resume it. A grandchild
  that asks a second approval parks the chain again — the middle child
  parks and the new request is offered on the lead (same namespaced
  call id, a new run id; the earlier decision does not carry over) —
  and each decision there takes the chain one park further. `Cancel`
  or `Close` of a middle child whose resume waits on its grandchild
  ends the grandchild first and waits for it (at most 30 s), so both
  ledgers end settled.
- **Do not let the parent's decision chain approve a delegating
  call.** A grant or a live `Approver` that matches a pool wrap's tool
  re-runs the delegation when it parks (see `Pool.Decide`'s godoc for
  whether your version still has this gap). Scope grants and the
  Approver away from wrap names.
- **Deleting a parent deletes no child.** Cascade yourself:
  `pool.Descendants(ctx, parent)` returns the whole subtree deepest
  first — delete in that order, the parent last.
  `pool.Children(ctx, parent)` is the direct ones.
- **Cost.** A child's usage is on its receipt and in the parent
  session's `Usage().Delegated` — not in the parent run's
  `RunResult.Usage`. A budget that must cover delegated work reads
  `Delegated`.
- **What the recorder sees.** A child session's turn is a session of
  its own in the records: its own `weft.session.id`, its lineage in
  `weft.session.parent` / `weft.session.parent_call`, and — under a
  `Wrap`ped call — `weft.caller.run.id` / `weft.caller.call.id` naming
  the delegating turn and call, never `weft.parent.*` (that names a
  `core.Subagent` child alone; record contract §2). Studio lists the
  child session with its lineage, opens it from the child's run page,
  and lists it under the parent's session page with the delegated
  usage and cost beside the parent's own (`GET /api/sessions/{id}`'s
  `delegated`, `delegated_usage`).
- **Depth.** `pool.MaxDepth(n)` (default 8) bounds a delegation chain;
  the model reads `SUBAGENT_DEPTH` past it.
- **Default-deny reaches every child.** A parent turn sent with
  `thread.RunOptions(weft.ParkAllExcept(...))` binds its children,
  sync and async, on every turn they run — the first and each resume —
  so a child's side-effect tools park unless named. Name a wrap's tool
  in the list for the delegation itself to run.

## 11. Approvals operations

- **Keys.** A `thread.Keyring` holds HMAC keys by id; the one active
  key mints new challenges, every key verifies. Keys are never
  written to a session — only key ids are — and a ring is
  configuration: pass it with `thread.WithKeyring` at every `Create`
  and `Open`.
- **Rotation.** Build a new ring with the new key active and the old
  keys still in it, and hand it to sessions as they open. Requests
  already challenged under an old key keep verifying. Drop an old key
  only when nothing minted under it can still be pending; after that a
  decision signed with it is `ErrBadSignature` (the same answer as a
  key that never existed).
- **`RequireSigned`** is stored in the session's header at `Create`
  (`weft.require_signed`) and enforced by every later `Open`, whatever
  options it passes; nothing turns it off. Such a session cannot be
  opened without a keyring that has an active key. Under it the
  unsigned `Decide` is refused; grants, the live Approver, expiry and
  the session's own denials still record. `s.Grant` stays an
  in-process, unsigned call: whoever can call it can pre-approve.
- **Signed decisions are bound** to the request entry and the run that
  parked the call, and are single-use (`ErrReplay`, across restarts).
  A challenge minted before upgrading to this version does not verify
  (the challenge domain moved to v2): request a fresh one.
- **Expiry is lazy.** `thread.RequestExpiry(d)` gives each request a
  lifetime, read against the session's clock. Nothing fires on a
  timer: an expired request is denied, with the stated reason, the
  next time the session is touched — `Decide`, `Resume`, `Send`, the
  runner, or the pool's pump. An idle session holds its lapsed request
  until then.
- **Quorum.** A signed approval counts as its key; an unsigned one as
  whatever `Who` the caller wrote. A quorum that must hold against the
  deciding process itself needs `RequireSigned` and one key per
  approver.
- **The audit trail is an index, not evidence.** `s.Audit()` returns
  every request, chain step, decision, grant and revocation the
  session recorded. The entries are plain appended lines, unsigned and
  unchained: whoever can write the storage can change them. If you
  need tamper-evidence, put it in the storage (an append-only volume,
  a signed export). Refused signed decisions are recorded, at most 16
  per request.
- A session file written before this version counted grant uses in
  prose; those uses are not counted any more, so a `MaxUses` grant in
  such a file starts counting again.

## 12. Retention

There is one retention operation: `thread.Delete(ctx, st, id)`. It
removes the session and all its entries; nothing else in the module
removes data — compaction, branching, `Uncompact` and `ClearQueue` all
append. There is no TTL, no archive tier and no per-entry redaction;
build them on `List` + `Delete` (and `pool.Descendants` for children).
A session grows for as long as it is used, and so does the cost of
opening it (§9).

## 13. Observability

Every run a session starts carries these as run metadata — on every
span and every log record of the run (ADR 0024):

| Key | Value |
|---|---|
| `weft.session.id` | the session id |
| `weft.turn` | the turn counter the run id `<session>-t<n>` was minted from |
| `weft.public_id` | the session's public id, when created with `thread.PublicID` |
| `weft.session.forked_from` | `<session>#<entry>`, on a fork |
| `weft.session.parent`, `weft.session.parent_call` | the parent session and delegating call, on a pool child |
| `weft.turn.parent` | the turn whose line this turn continues, on the first turn after a navigation (`Branch`, `Uncompact`, a rollback) took the leaf off the newest line; `0` when that line holds no turn |
| `weft.session.branch` | the branch the turn's line descends from: the label on the entry the navigation went to, else the navigation's entry id |

The session's keys win over a caller's colliding
`thread.RunOptions(weft.Metadata(...))`; other keys pass through. A
key the session has no value for is set to `""`, which the core reads
as none: a session's turn sent from inside another session's tool
handler (or a pool child's turn) never carries the enclosing run's
public id, fork origin or pool lineage as its own
(`docs/record-contract.md` §2).

A session's own tags ride every turn with `thread.WithRunMetadata(kv)`:
written into the header at `Create` and `Fork` (`Header.RunMeta`; a fork
takes its origin's under its own), held over the stored pairs for an
`Open`'s life, stamped under the identity keys above (which win) and
under a `Send`'s own `thread.RunOptions(weft.Metadata(...))` (which wins
per key) — a tenant or a batch tag on every turn without threading a
context through each `Send`.

The compaction marker: every compaction that lands (threshold,
`Compact`, `ApplyCompaction`, a trim, the overflow re-run) emits a
record of kind `compaction` (event name `weft.compaction`, ADR 0028 §8)
through the agent's `LoggerProvider`, at once, under the id of the last
run that produced the compacted context — on that run's span, with its
metadata, when this Session drove it — so a compaction followed by
`Close` is reported. It carries `weft.compaction.scope = session`, the
compaction entry's hash, and a body with the reason, the messages
replaced → summary entries and the estimated tokens before → after; no
messages. A compaction of a context no run produced (entries appended
directly to the store) is emitted under the next run the Session
drives, or at `Close` as the state log's records held for no run are:
on a context carrying no span, its body `held: true`, filed under no
run (no `weft.run.id`), keyed by the session's metadata alone. `obsdb`'s
`DB.Compactions` reads it back for Studio. Its body also names the
summary's cost (`summarizer_model`, `summarizer_usage`), and the
summarizer's model call itself — every compaction's and every
`SummarizeLeft` branch summary's — is a `chat` span with
`weft.chat.role = summarizer` and its usage, filed under the turn in
flight or the last run (a compaction the pre-run trigger makes waits for
that turn's run to start and opens under it; one whose run ends, or
the Session closes, before that run reports is dropped with a Debug
line, never opened as a trace of its own), never folded into that
run's own usage: `obsdb`'s session row counts it apart, as
`SummarizerUsage`.

The state log, as records. Beside the marker, the session reports every
state fact it writes as a record of kind `thread` (event name
`weft.thread.<kind>`, `docs/record-contract.md` §4): approval requests,
decisions (with `via`, the key id, the grant that decided) and refused
signatures; grants minted, used (with the use count), revoked and lapsed
(`MaxUses` reached, or an `Expiry` the chain noticed passed); the busy
policies' receipts (a steer queued, delivered, deferred or dropped; a
send accepted, with the turn it was minted as — and a queued steer's
turn number too, the one no run will carry); interrupts (the turn
that felled the run) and rewinds (a rollback, an overflow re-run's
branch-back); branches (with a summary's cost), uncompacts, labels,
pins, forks; and `thread/pool` delegations and their settlements. Each
is filed under the run it concerns — the run the entry names, else the
turn in flight, else the last run on the leaf's line; with none yet
(grants minted before the first turn), or while the turn in flight has
not reported, held for that run; what is still held at `Close` goes out
then, marked `held` — with no run, filed under none and keyed by the
session id. The session decides each record under its lock and one
emitter goroutine per Session hands it to the provider, in order, so a
slow or blocking destination never holds the session (`Pending`,
`Entries`, `Decide` stay prompt); `Close` waits for the emitter, bounded
by its context. Ids, enums and counts always; who decided,
reasons, a steer's text and a summary only where content is on (the
agent's `weft.Content`, else the destination's policy — a content-off
destination receives the record without them); a label's name is
metadata and always rides. A resumed call's `execute_tool` span names
who approved it — `weft.tool.approved_by` `user`, `grant` (with
`weft.grant.id`), `approver`, `quorum` or `policy` (`weft.ApproveBy`).
The file stays the record: the exported copy
is an index, not evidence (ADR 0021, amendment 2026-10-19), and an Observability
destination that drops one changes nothing the session does.

What the module logs on its own. Through the agent's logger
(`weft.Logger`): `Warn` for a repaired load at `Open`, a step write
that failed and was held, a compaction that failed or fell back;
`Error` for what could not be recorded (a turn end, a rollback, a
receipt) and for a contained panic in a hook; `Info` for each
compaction that lands. Through the backend's logger
(`thread.OpenLogger`, `slog.Default()` otherwise): `Warn` for a torn
tail removed and a lock taken over. No `Warn` or `Error` line from
`thread` means the storage took every write the session made.
