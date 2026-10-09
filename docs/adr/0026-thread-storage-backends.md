# ADR 0026 — Thread storage backends: Postgres, the writer rule, listing, the write hook

- Status: decided (2026-10-02). Drafted 2026-10-01; the owner answered
  every open question on 2026-10-01 and 2026-10-02 — the "Decisions on
  the open questions" section is the record.
- Amends: ADR 0011 §5 (storage) — the one-writer rule gains a release
  and a fence, and three optional capabilities join `Flusher` and
  `Watcher`
- Depends on: ADR 0005 (module layout, two-phase release), ADR 0011
  (sessions, the `Storage` contract, the wire format), ADR 0024 (thread
  owns state; the observability database is never a session store)
- Related: `WEFT-GRAPH-FLOW.md` §3 (`Transactional()`) and its
  orchestrator note (leases, shared storage) in the research checkout
- Numbered 0026 because 0025 is reserved for the flow tier
- Implementation: new module `github.com/weftgo/weft/thread/postgres`;
  changes to `thread`, `thread/jsonl`, `thread/sqlite`,
  `thread/threadtest`

## Context

Product builders need conversations in their own database: list a
user's past conversations, search them, continue one from any replica.
This is product state, not telemetry, so it belongs to `thread`
(ADR 0024: `thread` never writes to the observability database).

A developer has three ways to store sessions, and all three must keep
working:

1. **Their own storage.** Implement `thread.Storage` over their own
   tables and prove it with `threadtest.Run`.
2. **`thread/sqlite`.** One file, one machine.
3. **`thread/postgres`** (new). Their application database or a
   separate one.

`weft/flow` journals into sessions, so it uses whichever of the three
the developer chose and needs no storage of its own.

Reading the code against that use case found five problems:

- **The writer's hold is never released.** jsonl and sqlite take the
  session on its first write and keep it until `Delete` or process exit
  (`sqlite.go:610`); `Session` has no `Close`. A holder on another host
  is always `ErrLocked`. Copied to Postgres, a conversation served by
  replica A could never be continued on replica B.
- **Two `Session` values on one `Storage` are not detected.**
  `thread.Open` loads and builds a tree in memory; the backends treat
  every append from their own instance as "ours". Two requests for the
  same conversation on one replica each open it, each append against a
  stale leaf, and the tree forks silently. A command-line program never
  does this; a web server does it whenever a user double-submits.
- **`List` does not scale.** `thread/sqlite` reads every session row,
  decodes every header and filters in Go (`sqlite.go:439`).
- **There is no way to write in the append's transaction.** A search
  index, an outbox row or an application table cannot commit together
  with the entries. Flow's `Transactional()` step needs the same thing.
- **No shared database.** jsonl and sqlite are one-machine.

The owner's constraint (2026-10-01): locking and search depend on the
application, so the framework must not force one policy. What the
framework fixes is what keeps a session correct; everything else is an
option with a default.

## Decision

**Invariants are fixed and enforced by the storage; policies are
options with defaults; the `Storage` interface and the write hook are
the escape hatches.**

Fixed, on every backend: appends are atomic and ordered; the stored
lines are the thread wire (ADR 0011 §2, §6); a writer whose view of a
session is stale cannot append to it.

Configurable: how writers are kept apart (D3), which database and
schema hold the tables (D1, D9), who runs migrations (D9), whether and
how conversations are searchable (D7, D8).

### D1 — `thread/postgres` is its own module, on pgx

`github.com/weftgo/weft/thread/postgres`, so the driver never enters
`thread`'s module graph (ADR 0011 §1's rule, applied as for sqlite).
Two ways in:

```go
st, err := postgres.Open(ctx, dsn, opts...)       // weft owns the pool
st, err := postgres.OpenPool(ctx, pool, opts...)  // the app's *pgxpool.Pool
```

`OpenPool` is how the tables live in the application's own database and
share its connections. The module never closes a pool it did not open.
It is pgx only: a `database/sql` or MySQL backend is a separate module
later, passing the same conformance table. The module ships under the
project licence and is never gated (the batteries-stay-open rule).

### D2 — the stored form is the wire line; identity stays in `Header.Meta`

Two tables hold what sqlite's hold: `weft_thread_sessions` (id,
created, header, the denormalised title) and `weft_thread_entries`
(session, seq, line), entries cascading on delete. The entry kinds are
not normalised into columns: that would fork the format and is the ORM
the never-list rules out. The loud rules (`ErrNewerFormat`,
`ErrCorrupt`) stay `thread`'s own decode path.

The header and the lines are `jsonb`, so a developer
can query and index entries in plain SQL. The cost is stated: Postgres
re-encodes JSON, so the stored bytes are not the jsonl bytes; the
values are, and an export to jsonl is a re-encode. A malformed or torn
line cannot exist in a `jsonb` column, so those conformance rows do not
apply here; the unknown-kind and newer-version rows do.

weft owns no user, organisation or tenant concept. An application puts
its keys in `Header.Meta` at create time (`thread.WithMeta`) and
filters with `Query.Meta`. `Load` and `Delete` take a session id and
check nothing else: authorising a caller against the header's meta is
the application's job, and the godoc says so.

### D3 — one writer at a time; the policy is an option

The rule is ADR 0011 §5's: one writer per session at a time. How a
backend enforces it is chosen at open:

| Mode | Behaviour | Default for |
|---|---|---|
| **Lease** | An owner and an expiry on the session row, taken on the first write, renewed by a heartbeat, released by `Close` (D5). A lapsed lease is taken over by the next writer. Time is the database's clock, never the application's. | `thread/postgres` |
| **Sticky** | Held until `Close`, `Delete` or process death; another live holder is `ErrLocked`. | `thread/jsonl`, `thread/sqlite` |
| **Off** | No hold. The application guarantees one writer (its own queue or router); D4 still catches a mistake. | — |

Lease details: the lease is taken and checked inside the append's own
transaction, under a row lock, so appends to one session are
serialised. The defaults are a 30-second lease renewed every 10
seconds and a 10-minute idle window, each an option: a crashed
replica's conversations free up within the lease, and a step silent
for longer than the idle window can lose its hold, loudly (D4). The
heartbeat stops renewing a session that has had no
write for an idle window, so a `Session` that was never closed does
not hold its conversation until the process exits. A writer whose
lease lapsed and was taken over is caught by D4. No session-level
advisory locks: they pin a pooled connection per open conversation and
do not survive transaction pooling.

`Send` writes the prompt before the run starts (ADR 0011 §4), so a
second writer meets `ErrLocked` before any model call is paid for.

### D4 — the fence: a stale writer's append is rejected

A lock keeps writers apart in advance; it cannot prove that the writer
appending now has seen everything the session holds. That is the
property that keeps the tree correct, so it is checked at the write.

A new optional capability lets `Session` append at a position: the
append succeeds only if the session holds exactly the lines the writer
has seen, and fails with a new sentinel, `ErrStale`, otherwise.
`Session` uses it whenever the storage offers it, and a stale session
is reopened by its caller.

This closes the two-`Session` gap on one replica, makes a lapsed-lease
takeover loud instead of corrupting, and makes Off a safe mode: two
writers race, one wins, the other is told. Every first-party backend
implements it; a developer's own `Storage` may. The position is
counted in stored lines, and the specification settles how a salvaged
load reports it.

The fence is the framework's job and has no off switch. The fork it
prevents comes from `Session`'s own in-memory tree, which an
application cannot see or repair from outside, and the check costs
nothing: the append's transaction already reads the session's last
position to number the new rows. It is a data-integrity check, like a
unique constraint or an HTTP `If-Match`, not a policy. What stays the
developer's is the reaction to `ErrStale`: reopen and retry, or return
a conflict to the client.

### D5 — `Session.Close` releases the writer's hold

`Session.Close(ctx)` flushes and releases through a new optional
capability on the storage. It applies to every backend: jsonl unlocks
and closes its file, sqlite deletes its lock row, Postgres clears the
lease. After Close every write fails; Close twice is a no-op. A lease
also ends on its own by expiry (process death) and by the idle window
(a forgotten Close).

With a turn running or queued, Close waits for the session to go idle,
bounded by its context. It never cancels a turn: a caller
who wants that interrupts the turn first. If the context ends first,
Close returns its error and the session stays open and held. This is
the standard library's shape (`sql.DB.Close` waits for running
queries; `http.Server.Shutdown(ctx)` waits, bounded by a context) and
it keeps `defer s.Close(ctx)` correct: a Close that refused with
`ErrBusy` would be dropped by the defer and leave the hold in place.
The context is the only knob; there is no option.

### D6 — `List` is answered by the database

On Postgres and on sqlite, the meta filter, the title filter, the
ordering (`created`, then id, descending), the cursor and the limit
run in SQL; nothing reads the whole table. Postgres indexes the
header's meta with one GIN index (containment covers every key, so
there is no per-key index option) and orders through an index on
(created, id). sqlite filters with its JSON functions over the header
it already stores. `Query` and `Page` do not change. `Page.Total`
stays a count of every match, cheap under a meta filter and a full
count without one, which the godoc states.

### D7 — the write hook: the application's writes join the transaction

Each database backend accepts a hook that runs inside its write
transaction, after the rows are written and before the commit. It
receives the transaction and what was written: the header on create,
the entries on append, the id on delete. An error rolls the whole
write back and is returned from the call.

```go
postgres.OnWrite(func(ctx context.Context, tx pgx.Tx, w postgres.Write) error)
sqlite.OnWrite(func(ctx context.Context, tx *sql.Tx, w sqlite.Write) error)
```

This is the flexible answer to search, outbox rows and application
tables: the developer indexes or mirrors however they want, and it
cannot disagree with the session. It is an option on a driver module,
typed by that driver's transaction; it is not a `thread` interface,
not a third loop seam (ADR 0006 is about the run loop), and jsonl and
`Memory` have nothing to offer it. It is also the storage surface
flow's `Transactional()` step needs; flow's own ADR decides the step
API over it.

### D8 — search: the hook, plus an opt-in helper on Postgres

weft imposes no search. With D7 a developer can build full-text,
trigram, vector or an external index. For the common case
`thread/postgres` ships a helper, off by default and built on the
same hook:

- An option creates and maintains a derived table of text vectors,
  one row per indexed entry, filled in the append's transaction.
- It indexes the text parts of user and assistant message entries.
  Tool results, custom entries and summaries are not indexed unless
  the developer's extractor says so.
- The text-search configuration is an option; the default is `simple`,
  which does not assume a language.
- A package-level query function takes text, a meta filter and a page,
  and returns session id, entry id, rank and a snippet. The meta
  filter is required; searching across every session is a separate,
  explicit call, so a forgotten filter cannot leak one user's
  conversations to another.
- A hit names an entry, not a position in the live conversation:
  entries on an abandoned branch and before a compaction are still
  history (nothing is ever deleted) and are returned. `Session.Path`
  tells an application whether a hit is on the active branch.

There is no `thread.Searcher` interface yet. It is extracted, by its
own ADR, when a second backend ships a helper. Embeddings need a model
call and stay out of storage.

### D9 — schema and migrations

Tables carry the `weft_thread_` prefix and live in the connection's
default schema, or the one an option names. A `weft_thread_migrations`
table records the version. `Open` migrates by default, serialised
across replicas starting together. An option turns that off, and the
module exposes its migration files so a team that forbids runtime DDL
can run them with its own tool. With migration off, a schema behind
the module fails `Open` with the missing version named; a schema ahead
of the module fails loudly, as a newer format does.

### D10 — what changes in `thread/sqlite`

SQL-native `List` (D6), release on `Close` (D5), the fence (D4) and
the write hook (D7). Its lock stays sticky and single-host, and its
`Watcher` is unchanged. One migration renames its tables to the
`weft_thread_` prefix, matching Postgres: with the hook an application
may add its own tables to the same file, and the bare names `sessions`
and `entries` would collide. Existing files are upgraded on open.

### D11 — live tail on Postgres

`thread/postgres` implements `thread.Watcher`: an append notifies on a
channel in its transaction and watchers read the new rows. Where
`LISTEN` is unavailable (transaction pooling), an option falls back to
polling, as sqlite does. The contract is arrival order and
exactly-once, not latency.

### D12 — conformance

`threadtest.Run` gains nothing backend-specific, and Postgres passes
it. Its corruption rows are split, so a backend that can hold only
well-formed JSON (D2) still runs the unknown-kind and newer-version
rows and skips the malformed and torn ones. New shared tables cover the
new promises: release and
reacquire, the fence, lease expiry and takeover (Postgres), and `List`
equivalence across backends. The crash matrix runs against Postgres
with a short lease. Tests that need a server read a DSN from the
environment and skip without one; CI provides the server.

## What this does not change

- `thread.Storage`'s five methods, `Query`, `Page`, the wire format
  and `FormatVersion`.
- `thread` imports no driver and no database package.
- jsonl stays the default backend and `Memory` the test one.
- The observability database (ADR 0024) holds no session state.
- No queue, daemon or timer store: that is the orchestrator's,
  outside `thread` (`WEFT-GRAPH-FLOW.md`).

## Consequences

- An application on several replicas can serve any conversation from
  any replica, with one Postgres and no session affinity.
- `thread` gains one public method (`Session.Close`), one sentinel
  (`ErrStale`) and optional capabilities for release and for the
  fenced append. Code that never closed a session keeps working on
  jsonl and sqlite and should close on Postgres.
- A developer's own `Storage` keeps working unchanged. Without the
  fence capability it has today's guarantees, no more.
- Each locking mode is a tested path: three on Postgres. A pluggable
  lock interface is deliberately not offered.
- The write hook runs application code inside weft's transaction: a
  slow hook slows every append, and a failing hook fails the turn's
  write. The godoc says so.
- `thread/postgres` is released two-phase after the `thread` tag it
  requires, and joins `go.work`.

## Rejected

- **A separate storage module above `thread`.** `Storage` speaks only
  thread's types; the standalone `weft/store` was deleted by ADR 0024.
- **Sessions in `obsdb`.** Telemetry and product state have different
  owners, lifetimes and content rules (ADR 0024).
- **Normalising entries into columns.** A second format to migrate
  for every entry kind.
- **Session-level advisory locks.** One pinned connection per open
  conversation; broken by transaction pooling.
- **Extending `Query` with search now.** One backend is not enough to
  fix a contract every backend must then honour.
- **A weft user or tenant table.** Identity is the application's.
- **Owning a vector index.** On the never-list; the hook leaves room
  for the developer's own.

## Decisions on the open questions

Answered by the owner on 2026-10-01 and 2026-10-02:

1. **Locking default on Postgres** — lease; sticky and off are options
   (D3).
2. **Releasing a session** — `Session.Close` on every backend, plus
   expiry (D5).
3. **Search** — the write hook, plus an opt-in full-text helper on
   Postgres (D7, D8).
4. **sqlite's share** — the `List` fix, release on Close, the fence
   and the hook (D10).
5. **Stored line type on Postgres** — `jsonb` (D2).
6. **sqlite table names** — renamed to the `weft_thread_` prefix by
   migration (D10).
7. **The fence** — always on, no off switch (D4).
8. **Close on a busy session** — wait for idle, bounded by the
   context; never cancel (D5).
9. **Lease timings** — 30 s lease, 10 s renewal, 10 min idle window,
   each an option (D3).
