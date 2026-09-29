# ADR 0022 — `thread/pool`: bounded child runs, receipts, nested approvals

- Status: decided (2026-09-29, step 5.1 of plan §8; the four open
  questions were answered by the maintainer the same day — the
  "Decisions on the open questions" section is the record)
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

1. **The pool.** `pool.New(max)` is one process-wide semaphore with FIFO
   fairness (D2): every child run a pool starts — wrapped or submitted —
   holds one slot for its duration. `pool.Wrap(agent)` returns a
   subagent tool (`weft.Subagent` under a `weft.WrapTools` middleware,
   a tool-level option — no core change) whose child runs acquire a
   slot before the child starts and release it when the child's session
   settles.

2. **Sync and async, per tool option (D1).** The default is sync —
   today's behaviour (ADR 0014): the call waits, the result is the
   child's answer, the child's events arrive wrapped in `Nested`, its
   usage rolls up through the core. `pool.Async()`, a `Wrap` option,
   makes the delegation an acceptance: the middleware submits the child
   and returns the receipt line as the tool result — model-visible
   bytes, pinned by a golden — and a later turn reads the outcome from
   the parent session. `pool.Submit(ctx, parent, agent, prompt)` is the
   one primitive under both paths and the one an application calls
   directly: it creates the child session, records the acceptance
   receipt in the parent session, and starts the child running on a
   pool-owned goroutine.

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
   bucket, `Delegated`, summed from settled receipts' usage.
   `Turns` keeps its meaning — the session's own runs, sync children
   inside them through ADR 0014's core roll-up — and `Summaries` stays
   separate (ADR 0020 §4's ledger discipline: costs are attributed,
   never mixed). The child session keeps its own ledger too.

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

**D2 — fairness?** One process-wide FIFO semaphore; no per-session
quotas. A session's in-flight children are already bounded by its
agent's `Parallelism` (one step's batch), so a pool-level per-session
cap would be a second way to say what `Parallelism` says (PLAYBOOK
§9's "second way to do something" red flag). A per-session cap can be
added additively later if starvation is ever measured in real use.

**D3 — budget roll-up?** A `Delegated` bucket on the parent's `Usage`.
The child-ledger-only alternative hides an orchestration session's
dominant cost exactly where the operator looks for the total; rolling
into `Turns` mixes kinds, which the ledger exists not to do
(ADR 0020 §4). Sync children need no rule: ADR 0014's core roll-up
already lands them in the parent's turns.

**D4 — cancellation?** Independent plus explicit `Cancel`. The
coherent alternatives are not: the submitting turn's context dies at
turn end (normal completion would cancel the child), and a
session-lifetime tie needs a session→children registry kept consistent
across restarts that nothing else in this ADR requires. `Cancel` per
receipt and a draining `Close` cover both operator moves.

## Consequences

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
- **A depth cap or per-session pool quotas** — cost across depth and
  fan-out is budgets' job (ADR 0014 rejected the depth cap on the
  same grounds); capacity is the semaphore's.
