# ADR 0022 — `thread/pool`: bounded child runs, receipts, nested approvals

- Status: decided (2026-09-29, step 5.1 of plan §8; the four open
  questions were answered by the maintainer the same day — the
  "Decisions on the open questions" section is the record); amended
  2026-10-01 — the amendment at the end replaces the slot rule, the
  depth guard, the nested-approval bridge and `Decide`'s shape, and
  where it disagrees with the text above it, the amendment is the
  decision
- Depends on: ADR 0011 (sessions), ADR 0014 (subagents as tools), ADR
  0021 (approvals), ADR 0019 (steering, for forwarding)

## Context

`weft.Subagent` runs a child synchronously inside the parent's tool call
(ADR 0014). Three things are left to this tier by name:

- **Capacity**: many sessions each delegating at once need a shared bound
  (DeerFlow `max_running: 3`); the core's `Parallelism` bounds one run's
  batch, not a process.
- **Receipts**: asynchronous delegation — hand a task to a child, get an
  acceptance receipt now, the result later — with a stop-reason
  taxonomy (DeerFlow: token-capped, turn-capped, loop-capped).
- **Nested approvals**: a child's parked call fails the parent's call
  loudly today (`SUBAGENT_PENDING`); "`runtime`, which owns sessions,
  can resume the child under its lineage id" (ADR 0014).

## Decision

1. **The pool.** `pool.New(max)` is one FIFO semaphore per `Pool`
   value (D2; "process-wide" in the first draft — a process that wants
   one bound shares one pool): every child run a pool starts — wrapped
   or submitted — holds one slot while it works (amendment §A: not
   while it waits on a child of its own, not while it is parked). `pool.Wrap(agent)` returns a
   subagent tool (`weft.Subagent` under a `weft.WrapTools` middleware,
   a tool-level option — no core change) whose child runs acquire a
   slot before the child starts and release it when the child's session
   settles.

2. **Sync and async, per tool option (D1).** The default is sync —
   the call waits and the result is the child's answer, as an ordinary
   subagent's is. `pool.Async()`, a `Wrap` option, makes the
   delegation an acceptance: the middleware submits the child and
   returns the receipt line as the tool result — model-visible bytes,
   pinned by a golden — and a later turn reads the outcome from the
   parent session. `pool.Submit(ctx, parent, agent, prompt)` is the
   one primitive under both paths and the one an application calls
   directly: it creates the child session, records the acceptance
   receipt in the parent session, and starts the child running on a
   pool-owned goroutine. One rule covers both paths, forced by §3: a
   pool child always runs as its own session, so its events are not
   `Nested` in the parent's stream (ADR 0014's late-event rule would
   cut them at the call's `ToolFinish` anyway) and its usage bills
   through its receipt (§5), not the core's roll-up. A bare
   `weft.Subagent` with no pool keeps ADR 0014's `Nested` and roll-up
   exactly; a wrapped tool called outside any session run — a bare
   `Generate` — falls back to that ordinary path under the slot.

3. **Child sessions.** Every child a pool starts is a session of its
   own, in the parent's storage, its header naming the parent session
   and the delegating call (`parent_session`, `parent_call_id` —
   Crush's `parent_session_id`; a pool lineage, not a fork's copied
   path, which is `Header.Parent`'s meaning, ADR 0011 §3). The child's
   transcript survives, tails live through the v0.4 `Watcher`, and —
   what nested approvals need — the child is resumable after a restart.

4. **Receipts.** One `pool_receipt` entry kind, `"v":4` (ADR 0011 §6):
   acceptance records the child session and prompt; a `running` entry
   lands when the slot is acquired and the child starts — the wait
   between the two is the pool's queue, visible; settlement, linked by
   receipt id like the steering receipts (ADR 0019), records the final
   state — `done`, `failed`, `canceled` (explicit `Cancel`) or
   `capped` (the child died on a budget: `MaxSteps` or a usage limit —
   DeerFlow's token-capped/turn-capped/loop-capped collapse into one
   state, the stop reason already distinguishing them) — plus the
   child's stop text and usage. Receipt entries never enter the
   model's context. The answer reaches the model as the delegating
   call's result (sync); for an async delegation the application
   delivers it — the settled receipt is what a later turn or caller
   reads, not context.

5. **Budgets (D3).** The parent session's `Usage` gains a third
   bucket, `Delegated`, summed from settled receipts' usage — every
   pool child, sync or async, bills there, because a session-run child
   has no ride on the core's roll-up. `Turns` keeps its meaning — the
   session's own runs, bare `Subagent` children inside them through
   ADR 0014's roll-up — and `Summaries` stays separate (ADR 0020 §4's
   ledger discipline: costs are attributed, never mixed). The child
   session keeps its own ledger too.

6. **Cancellation (D4).** Async children are independent by
   construction: they run on a pool-owned context, because the
   submitting turn's context ends when the turn ends — a child tied to
   it would be canceled by the turn's normal completion. `Cancel` on
   the receipt cancels one child; closing the pool cancels every
   running child and waits for their sessions to settle (DeerFlow's
   gateway drain). Sync children keep ADR 0014's rule unchanged: they
   run on the parent call's context and cancel with it.

7. **Nested approvals.** When a child run parks — its own approval
   boundary opens (ADR 0021 §1) — a pool-wrapped delegation does not
   fail with `SUBAGENT_PENDING`. The child's requests persist in the
   child session and surface on the parent session's `Pending()` with
   their lineage. A decision addressed to a child's call resumes the
   child session exactly as a top-level session resumes (the core's
   `Approve`/`Deny`/`Resolve`/`ResolveError`, ADR 0021 §1), the child
   runs to its end, and the parent's delegation then completes with
   the child's answer — for a sync delegation by resolving the parked
   parent call with the child's final text (the core's `Resolve`:
   re-running the delegation would replay the child), for an async one
   by settling its receipt. The parked wrapper call itself is never
   offered for direct decision: `Pending()` shows the child's requests
   with their lineage, and the wrapper completes through them. A bare
   `weft.Subagent` with no pool keeps ADR 0014's `SUBAGENT_PENDING`
   exactly as written.

8. **Steering forwarding.** Explicit only — `pool.Forward(receiptID,
   msg)` steers a running child through its session (ADR 0019 §7);
   nothing is forwarded implicitly, and a child started by `Submit`
   has no steering source of its parent's.

## Decisions on the open questions (2026-09-29)

**D1 — sync or async?** Both, per tool option; sync the default. The
tool-call/result pairing invariant (ADR 0001) means async can only ever
be "the tool's result is a receipt, not the answer" — the core has no
late-arriving tool result, and none is added. ADR 0014's late-event
rule settles where an async child's transcript lives: its events cannot
keep flowing into the parent stream after the delegating call's
`ToolFinish`, so its session is the record (and the live tail). A
parent that needs the answer to continue should keep today's sync
semantics, which is why sync stays the default.

**D2 — fairness?** One FIFO semaphore per pool — an explicit waiter
queue, amendment §K; no per-session quotas. A session's in-flight children are already bounded by its
agent's `Parallelism` (one step's batch), so a pool-level per-session
cap would be a second way to say what `Parallelism` says (PLAYBOOK
§9's "second way to do something" red flag). A per-session cap can be
added additively later if starvation is ever measured in real use.

**D3 — budget roll-up?** A `Delegated` bucket on the parent's `Usage`.
The child-ledger-only alternative hides an orchestration session's
dominant cost exactly where the operator looks for the total; rolling
into `Turns` mixes kinds, which the ledger exists not to do
(ADR 0020 §4). The bucket is fed by settled receipts, which makes it
the one bucket every pool child bills — sync ones included, since a
session-run child (§2) cannot ride the core's roll-up the way a bare
`Subagent` child does.

**D4 — cancellation?** Independent plus explicit `Cancel`. The
coherent alternatives are not: the submitting turn's context dies at
turn end (normal completion would cancel the child), and a
session-lifetime tie needs a session→children registry kept consistent
across restarts that nothing else in this ADR requires. `Cancel` per
receipt and a draining `Close` cover both operator moves.

## Consequences

- ~~The pool's bound doubles as its depth guard~~ — withdrawn by the
  amendment (§A, §B). The guard was wrong twice: it refused only
  chains, so fan-out times depth still deadlocked (three children each
  delegating once fill `New(3)` with waiters), and it reported
  `SUBAGENT_CYCLE` for chains with no cycle in them. A waiting parent
  now holds no slot, and depth has a limit and a code of its own.

- One new entry kind (`pool_receipt`, `"v":4`) and one new header
  linkage (`parent_session`/`parent_call_id`); both additive (ADR
  0011 §6 — unknown kinds fail loudly on older readers, keys are
  ignored), goldens in `thread/testdata/format4/`.
- The async tool result is model-visible text and is pinned by a
  golden; so is the settled-receipt shape an application reads.
- `pool` imports `thread` and the root, never `weft/store` (ADR 0011
  §1's module rule, one level down).
- No core change: the pool is a `ToolMiddleware`, session entries, and
  pool-owned goroutines — the two release notes that matter
  (`ErrApprovalRequired` parking from middleware, `Resolve` on resume)
  are ADR 0007's existing rules.

## Rejected

- **A per-tool async that leaves the call open** — would need the core
  to support unpaired calls at step end; the pairing invariant is
  ADR 0001's.
- **Event forwarding for async children into the parent stream** —
  ADR 0014's late-event rule exists for replayability; the child
  session plus the `Watcher` is the window.
- **Implicit steer forwarding** — ADR 0019 §7: forwarding is an
  explicit session decision.
- **Per-session pool quotas** — a session's fan-out is `Parallelism`'s
  to bound; capacity is the semaphore's. (A depth cap was rejected
  here too, on ADR 0014's grounds, while the bound doubled as one; the
  amendment gives the pool an explicit `MaxDepth` — §B says why the
  pool, unlike the core, needs it.)

## Amendment 2026-10-01 — the hand-off, depth, the replay rule, recovery, and what the model reads

The 2026-10-01 review found the pool deadlocking under fan-out, a
child that parked twice unresumable, a wrapper call that could be
approved into a second child, and receipts that could stay `running`
for ever. This amendment is the record of what changed; where it
disagrees with the sections above, it is the decision. Breaking
changes to the pool's API are listed in §L.

### A. The slot rule: slots bound work, not waiting

**At most `max` child runs of a pool are doing work at once** — model
calls and tool calls. A run that is only waiting holds no slot:

- a child parked at an approval holds none (unchanged);
- a child whose **sync delegation is running a child of its own hands
  its slot back for the wait** and re-acquires one, through the same
  queue, before it works again. The grandchild takes its slot from
  the queue like any child.

Under fan-out — one step delegating several times (`Parallelism`) — a
run holds no slot while *at least one* of its sync delegations is
waiting, and re-acquires when the *last* of them has returned: the
next thing it does is its next model call. Re-acquiring earlier would
hold capacity the still-waiting siblings' children need; with one
slot that is the deadlock. The consequence, stated rather than
hidden: other, non-delegating tool calls of that same step keep
running while the run holds no slot — the bound counts runs, and a
run with a delegation in flight is counted as its children.

The re-acquisition waits on the *run's* context, not the delegating
call's: a call abandoned by its tool's `Timeout` must not leave its
run working without a slot. A run canceled while it queues holds
nothing.

So fan-out × depth is safe on any `max`, 1 included, sync and async;
the tests run fan-out 3 at depth 2 and 3 on `New(1)` and `New(3)`,
both ways, and assert the bound while they do. The same hand-off
applies on the bare path (a wrapped tool called outside any session,
§2), where calls nest under the core's own `Subagent`.

### B. Depth is its own bound

`pool.MaxDepth(n)` (default `pool.DefaultMaxDepth`, 8) bounds how deep
a chain of delegations through one pool may go; a delegation that
would go deeper is refused before any child is created —
`SUBAGENT_DEPTH` to a wrapped tool's model, `ErrDepth` from `Submit`.
ADR 0014 rejected a depth cap for the core because cost across depth
is budgets' job; the pool has one anyway because its children are
*sessions* — each level is a stored session and a queue entry that
budgets on the parent's run do not see (§5: pool children bill to
`Delegated`, not to the run). The limit is unrelated to `max`.

The cycle guard is separate and real: the pool carries the ancestry
of agents down the delegation chain (the core's guard lives in the
`Subagent` handler, which a session-run delegation never calls) and
refuses a delegation to an agent already running above the call with
`SUBAGENT_CYCLE` / `ErrCycle` — for cycles only. After a restart the
ancestry above a rebuilt child is not recoverable from files (agents
are not stored); its guard restarts at its own agent, and the depth
limit, rebuilt from the lineage headers, still bounds it.

### C. The replay rule (§7, replaced)

Decisions for a child's calls are still recorded in the parent — its
`Decide` or `DecideSigned`: validation, quorum, signature, audit and
expiry live there. What changed is how they reach the child:

- **Only the calls pending in the child now are replayed.** The pump
  reads the child's own `Pending()` and, for each call, the parent's
  mirror for *that occurrence* (the namespaced call id and the child
  run that parked it). The decisions of an earlier park are never
  replayed — a child may park any number of times.
- **The replay has its own door**: `Session.ReplayDecisions`, pool
  plumbing accepted only by a session with a pool lineage. It is not
  `Decide`: several decisions for one call (a quorum) record in
  order; a decision for a call no longer pending is dropped, not
  refused (a replay repeated after a crash records nothing twice);
  and it records on a `RequireSigned` child. The pool-child exception
  in `Session.Decide`'s duplicate check is gone — duplicates in one
  batch are `ErrInvalidDecision` on every session.
- **Identity survives, the channel is honest.** A replayed decision
  keeps `Who` and `KeyID` — a signed approver counts by key under the
  child's quorum exactly as under the parent's — and is recorded with
  `Via: "parent"`. Never `"signed"`: the signature and its nonce are
  in the parent's entry, where they were verified; the child's entry
  says where the decision came from, and the pair of files is the
  audit trail.
- **A mirror is pending until** it is decided or superseded by a
  later mirror for the same call id (call ids repeat across turns;
  the later entry is the request). A delegation that ends with
  requests nobody decided — a canceled child, a failed one, one that
  is gone — has them denied on the parent, on the record
  (`Session.DenyMirrored`: `Via: "child"`, Who `thread/pool`, the
  reason naming how the delegation ended): nothing can resume the
  child any more, and an undecided mirror would hold the parent's own
  boundary open for good.

### D. The wrapper is never decidable

The delegating call a mirrored request parks under takes no decision,
from anyone: `Session.Decide`, `Session.DecideSigned` and
`Session.Request` refuse it with `thread.ErrDelegated` (approving it
re-ran the delegation: a second child, the side effects twice). It
does not expire on its own either — the mirrors carry the expiry. It
is resolved only by `Session.ResolveDelegation`, which now names the
child and refuses a call that delegates to another one (a call id
reused by a later delegation must not take an earlier child's
answer). `Pending()` hides it by occurrence, not by id: an ordinary
call that later reuses the id is offered like any other.

The parent's own decision *chain* (ADR 0021 §2 — a grant, a live
`Approver`) obeys the same rule: it skips both steps for a call a
live mirror names as Wrapper. Such a call always parks, and only
`ResolveDelegation` resolves it. A decision the parent records for a
mirrored request by any other path — an interrupting Send's denial,
an expiry, a `Session.Decide` used directly — reaches the child too:
the pool installs a notification on every parent it delegates from
(`Session.WatchMirrors`, plumbing) and runs its pump when one lands.

### E. `Decide` arms; it does not wait

`Pool.Decide(ctx, parent, ds...) error` records and arms, and returns.
A resumed child is pool work like a first run: a pool goroutine, the
pool's context (the call that first delegated it has parked and
returned), **the queue and a slot**, a `running` receipt. `ctx`
bounds the recording and the arming only. `Pool.Wait(ctx, parent,
receipt)` blocks until a delegation is at rest (settled or parked);
the parent's own continuation is its parked turn's `Next`, set by the
time `Wait` reports the settlement. The error is every failure joined
(`errors.Join`) — per child that could not be armed, why — and the
other children are armed regardless. `Pool.DecideSigned` is the same
for one signed decision.

For a sync delegation the resume waits until the parent's turn has
actually parked the delegating call (the mirrors are written while
that turn is still running; a decision can arrive in between, and a
child finished inside that window would resolve a call not yet
pending).

### F. Receipts always end, and say why

- A new status, `parked` (same kind, same `"v":4` — a reader from
  before it sees one more unsettled state): the machine is `accepted →
  running ⇄ parked → done | failed | canceled | capped`. "Pending
  children sit at running" (§4) is withdrawn; a resume records
  `running` again.
- **A park the parent cannot record fails the delegation**: the child
  would wait on requests nobody is offered. The receipt settles
  `failed` with the cause and the sync call is a tool error — it does
  not park.
- A receipt that cannot progress settles `failed` with its cause (a
  start the ledger cannot record, a resume with nothing to resume, a
  panic). Settlement is idempotent per receipt, so `Delegated` counts
  a child once; and a settlement's usage is the child session's whole
  ledger — every run of the delegation, the ones before a park
  included (the last run alone used to be billed).
- `Cancel` works on a parked receipt: the child's pending calls are
  denied in the child (reason "the delegation was canceled while
  awaiting approval", `Via: "parent"`), nothing is resumed, the
  receipt settles `canceled`, its mirrors are denied on the parent,
  and a sync delegation's parked call resolves with
  `SUBAGENT_CANCELED`.
- The pool closes a child's session when its delegation settles, and
  the parked ones at `Close`; a parked child keeps one session value
  for its resume. `Close` covers the bare path too (refused after,
  canceled and awaited during).

### G. Recovery

`Pool.Recover(ctx, parent)` — once per parent session a new process
takes over. For every unsettled receipt no child of this pool is
working: a child parked at an approval is reattached (reopened under
its registered or wrap-named agent; **pending requests with no mirror
are mirrored** — the crash window between a child's park and the
mirror — and the receipt records `parked`); a child that finished is
settled from its own file; one that never finished a run, or is gone,
settles `failed`, saying so. What a crash lost after a settlement is
finished too: requests still offered for an ended delegation are
denied, a delegating call still parked beside a settled receipt is
resolved from the ledger. It never re-runs a
child. A child whose agent the pool does not hold is reported
(`ErrNoAgent`) and left. One pool should own a storage's delegations
at a time: Recover treats an unsettled receipt it is not working as a
dead process's.

### H. Expiry and signing are inherited

A child is created — and reopened — with `thread.InheritApprovals
(parent)`: the parent's `RequestExpiry` and `Clock`, its `Quorum`,
its keyring and its `RequireSigned` rule. So a nested request carries
a real expiry, mirrored verbatim; the parent's sweep denies the lapsed
mirror (`Via: "expiry"`); the pump resumes the child, whose own sweep
denies its request with the same stated reason, and the child's
answer resolves the delegating call. And nobody holding the child
session can decide its parked calls through the unsigned door the
parent closed: under `RequireSigned` the child's boundary is decided
only by the parent's replay (or a signature under the same ring).

### I. Model-visible text (rule 5)

Every string the pool puts in front of a model, each pinned by a
test:

| text | when | test |
| --- | --- | --- |
| `background task accepted; receipt <id>` | an `Async` wrap's result | `TestWrapAsyncGolden` |
| `SUBAGENT_CYCLE: agent "<name>" is already running in this call chain` | a real cycle (the core's text) | `TestCycle` |
| `SUBAGENT_DEPTH: delegation depth <n> exceeds the pool's limit <m>` | `MaxDepth` exceeded | `TestDepthLimit` |
| `SUBAGENT_FAILED: agent "<name>" failed at step <n>: <cause>` | the child's run failed (the core's text, held equal by `TestFailureTextMatchesCore`) | `TestWrapSyncFails`, `TestModelVisibleTexts` |
| `SUBAGENT_FAILED: agent "<name>" failed: <cause>` | a failure outside a child run, or recovered from the ledger (no step is recorded) | `TestModelVisibleTexts` |
| `SUBAGENT_FAILED: agent "<name>" parked at an approval the pool could not surface: <cause>` | the mirror could not be written | `TestMirrorFailureFailsDelegation` |
| `SUBAGENT_CANCELED: agent "<name>" was canceled before it finished` | `Cancel` or `Close` ended the child — not the parent's own cancellation, which stays a run error | `TestCancelRunningSyncChild`, `TestCancelParked` |
| `DENIED: the delegation was canceled while awaiting approval` | what a canceled, parked child's model would read if its session were resumed | `TestCancelParked` |
| `thread/pool: decode "<name>" arguments: <json error>` | arguments that are not `{"prompt": …}` | `TestModelVisibleTexts` |
| `thread/pool: pool is closed` | a wrapped call on a closed pool | `TestCloseCoversEverything` |

`<name>` is the wrap's name (`delegate` where a delegation has none).
A sync delegation that parked and later ends delivers the same texts
as the parked call's `resolve_error`.

### J. Children when the parent is deleted, branched or forked

Lineage is a reference, both ways, and nothing else. **Delete**:
deleting a parent deletes no child. `pool.Children` (direct) and
`pool.Descendants` (the subtree, deepest first) enumerate them from
the receipts, verified against each child's header, so an application
can cascade. A child still running under a deleted parent runs to its
end and cannot record its settlement (logged). **Branch**: receipts
are ledger, read from every line of the tree, and mirrors stay
pending wherever the leaf is; a delegating call the branch abandoned
is no longer there to resolve, and the child's answer stays on its
receipt. **Fork**: the core's rule — the fork's copies of unsettled
receipts are settled canceled and mirrored requests are left out; the
children stay the origin's, and `Children` lists none for the fork.

### K. The bound is per pool, and the queue is a queue

"Process-wide" was never true: the bound is the `Pool` value's. FIFO
is now a guarantee the pool makes itself — an explicit waiter queue
(`container/list` under a mutex, the shape of
`golang.org/x/sync/semaphore`), not the runtime's channel wait order,
which the language does not specify. Work takes its place when it is
accepted (`Submit` queues before it returns), and a parent
re-acquiring its slot queues behind what was already waiting.

Budgets (rule 13's exception, restated): a pool child's usage is on
its receipt and in the parent session's `Usage.Delegated` — not on
the parent run's `RunResult.Usage` or `StepRecord.SubagentUsage`. A
budget that must cover delegated work reads `Delegated`.

### M. Async children keep what the delegating run hands down (2026-10-07)

An async child ran on the pool's bare context (§6, D4), so a
`weft.ParkAllExcept` list in force on the delegating run — the rule
that reaches every run started inside it — and the run's metadata did
not reach the child: an async delegation from a default-deny run fired
its side effects unparked. The async child's context now takes the
pool's cancellation and the delegating call's context values — the
same values a sync child sees — so the rule binds it, and the turn's
end still does not cancel it. A child's resumes (its own `Decide`,
the pool's arming through `Pool.Decide`) run on the parked turn's
context values (ADR 0021, amendment 2026-10-07), so a resumed child,
sync or async, keeps the rule on every step.

### L. API changes (breaking, pre-1.0)

- `Wrap` returns `(*weft.ToolDef, error)`; `MustWrap` panics. One
  name names one agent (`ErrDuplicateWrap`). `Register` returns an
  error.
- `Decide(ctx, parent, ds...) error` (was `(*thread.Turn, error)`,
  blocking); `DecideSigned`, `Wait`, `Recover`, `Children`,
  `Descendants`, `MaxDepth` are new.
- `Cancel(ctx, parent, receiptID)` and `Forward(ctx, parent,
  receiptID, msg)` take the parent session: a receipt is its
  session's, and the ledger is how "wrong id" (`ErrUnknownReceipt`)
  is told from "already done" (`*StateError`, which matches
  `ErrNotRunning` and carries the state).
- `Receipt.State` is a typed `pool.State` with `Settled()`; the wire
  strings are unchanged. `Receipt.Call` names the delegating call.
- thread: `ErrDelegated`; `Session.ReplayDecisions`,
  `CancelDelegated`, `MirroredRequests`, `DenyMirrored`,
  `InheritApprovals` (pool
  plumbing, with `AppendPoolReceipt`, `AppendApprovalRequests` and
  `ResolveDelegation` in one file, `thread/poolplumbing.go`);
  `ResolveDelegation` takes the child id; `PoolParked`.
