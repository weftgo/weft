# ADR 0022 — `thread/pool`: bounded child runs, receipts, nested approvals

- Status: **proposed** (2026-09-28; to be decided at the start of
  `weft/thread` v0.5, plan §8 — the open questions below are answered
  then, against the code as it is)
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

## Proposed decision

1. `pool.New(max int, opts...)` — a process-wide semaphore with FIFO
   fairness; `pool.Wrap(agent)` returns a subagent tool whose child runs
   acquire a slot (a `ToolMiddleware` over the subagent's `ToolDef`, no
   core change).
2. **Child sessions**: each child run can be a session of its own,
   linked to the parent session and call (`parent_session`,
   `parent_call_id` in its header — Crush's `parent_session_id`), so a
   child's transcript survives and can be resumed.
3. **Receipts**: `pool.Submit(ctx, child, prompt)` returns a `Receipt{ID,
   State}` (accepted → running → done | failed | canceled | capped) and
   records it in the parent session; the parent run gets the receipt as
   the tool result and a later turn reads the outcome.
4. **Nested approvals**: when a child parks, the pool persists the
   child's pending requests in the child session and surfaces them on
   the parent session's `Pending()` with their lineage; a decision
   resumes the child under its lineage id, then the parent's call
   completes with the child's answer.
5. **Steering forwarding**: explicit only — `pool.Forward(receiptID,
   msg)` steers a running child through its session (ADR 0019 §7).

## Open questions (answer at v0.5)

- Does the parent's call return at once with a receipt (async) or wait
  (sync, today's behaviour)? Both, per tool option?
- Fairness across sessions vs within one: one semaphore or per-session
  quotas?
- Budget roll-up for async children: into the parent's `Usage` when they
  finish, or only into the child session's ledger?
- Cancellation: does canceling the parent turn cancel async children?
