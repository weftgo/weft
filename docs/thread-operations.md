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

| | `thread/jsonl` | `thread/sqlite` (its own module) |
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
- **sqlite** commits every `Append` as one transaction, in WAL mode
  with `synchronous=NORMAL`. That is durable against the process
  dying. It is SQLite's documented trade for a power loss or kernel
  crash: the most recent commits can be rolled back (the database is
  not corrupted). The fsync options are accepted and change nothing
  on this backend. If "the prompt is durable before the run starts"
  must hold through a power cut, use jsonl with its default policy.
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
  sessions on shutdown (§5) so no rows are left, or clear the stale
  rows by hand once the old host is known to be gone:
  `DELETE FROM session_locks WHERE host = '<old hostname>';`
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
- **Stale detection is a count.** A Session compares the number of
  complete entry lines the storage holds with the number it has seen.
  It catches a writer that appended behind it; it does not catch a
  session deleted and re-created to the same length.
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
  those that never finished a run — it never re-runs a child. A child
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
  always safe to call.
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
- **Depth.** `pool.MaxDepth(n)` (default 8) bounds a delegation chain;
  the model reads `SUBAGENT_DEPTH` past it.

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

The session's keys win over a caller's colliding
`thread.RunOptions(weft.Metadata(...))`; other keys pass through.

What the module logs on its own. Through the agent's logger
(`weft.Logger`): `Warn` for a repaired load at `Open`, a step write
that failed and was held, a compaction that failed or fell back;
`Error` for what could not be recorded (a turn end, a rollback, a
receipt) and for a contained panic in a hook; `Info` for each
compaction that lands. Through the backend's logger
(`thread.OpenLogger`, `slog.Default()` otherwise): `Warn` for a torn
tail removed and a lock taken over. No `Warn` or `Error` line from
`thread` means the storage took every write the session made.
