# ADR 0021 — Approvals in sessions: durable, signed, granted, audited

- Status: decided (2026-09-28, TODO §14 "Approval workflows"; ships in
  `weft/thread` v0.2, plan §4)
- Depends on: ADR 0007 (the approval boundary: `Pending`, `Approve`,
  `Deny`, `Resolve`, `ResolveError`; "`DENIED: no decision`"), ADR 0011
  (sessions, entries, versioning), ADR 0014 (subagents — nested
  propagation is ADR 0022's)

## Context

The core parks a call and ends the run successfully with `res.Pending`;
the caller resumes with a decision per call (ADR 0007). The core keeps
nothing: a restart loses the parked calls, a decision is whatever the
caller passes, and every application rebuilds "always allow" by hand.
ADR 0007 point 4 leaves "signed decisions, persistence and workflows" to
this tier.

What the field taught:

- **Vercel AI SDK**: approvals travel over a network, so a decision is an
  HMAC over tool name + call id + input, verified fail-closed with a
  typed error.
- **Crush #497**: grants were in-memory and tool-wide; users wanted them
  persisted and scoped to a command. A hook that pre-approves must still
  leave an audit trail.
- **Vercel AI SDK dive**: pick one approval model — two (run-boundary and
  live-prompt) confuse users. weft keeps the run boundary and offers the
  live prompt as a policy on top of it.
- Approval is a policy and UX seam, **not a security boundary**; the
  boundary is OS-level isolation plus `thread/sandbox` (ADR 0023).

## Decision

### 1. One model: the run boundary, made durable

Every parked call becomes an `approval_request` entry (call id, tool,
arguments and their SHA-256, run id, time, the reason, an optional
expiry) written in the same `Append` as the turn's messages. Reopening
the session after a restart lists them: `s.Pending()`.

Decisions are entries too (`approval_decision`: approve | deny(reason) |
resolve(content) | resolve_error(content), who, when, via, key id).
`s.Decide(ctx, decisions...)` records them; when every pending call of
the run has a decision the session resumes with the core's
`Approve`/`Deny`/`Resolve`/`ResolveError` (`AutoResume`, default on), or
the caller calls `s.Resume(ctx)` — undecided calls are then denied with
the core's "no decision" text. A decision for a call that is not pending
is `ErrNotPending` (the core's rule, raised before any run starts).

### 2. The decision chain

Before a request parks, the session asks, in order:

1. **Grants** (§4) — a matching live grant approves at once.
2. **An `Approver`** — `func(ctx, Request) (Decision, bool)`, optional;
   the "ask now" path for a terminal or a UI already connected. It
   returns `false` to decline to decide (a pipe, no TTY), never blocks
   forever: the session gives it `ApproverTimeout` (default 0 = no
   waiting when not interactive).
3. **Park** — the request is persisted and the turn ends with
   `Pending`; any transport can decide later, hours later.

Each step writes an audit entry, including automatic approvals.

### 3. Signed decisions

For decisions that cross a process boundary (a web UI, a chat
integration, email):

- `s.Request(callID)` returns a `Request` with a **challenge**: session
  id, call id, tool, `args_sha256`, allowed outcomes, expiry, nonce, key
  id — HMAC-SHA256 under the session's `Keyring`.
- The client returns a `SignedDecision` over the challenge plus the
  outcome. `s.DecideSigned(ctx, sd)` verifies **fail-closed** before
  anything is recorded: `ErrBadSignature`, `ErrExpired`, `ErrReplay`
  (nonce already used — nonces are entries, so replay protection
  survives restarts), `ErrArgsChanged` (the arguments hash no longer
  matches), `ErrUnknownKey`.
- `Keyring` holds several keys by id; one is active for signing, all
  verify — rotation without invalidating requests in flight. Keys never
  touch the session file; only key ids do.
- Unsigned `Decide` stays available for in-process callers; a session
  opened with `RequireSigned()` rejects it.

### 4. Grants

A grant approves future requests without asking:

- **Match**: a tool name, plus optional argument predicates — equality
  on a JSON pointer, a path prefix, a command prefix/glob — so "allow
  `run_command` for `go test …`" is expressible, not just "allow
  `run_command`".
- **Scope**: this session, or a `GrantStore` shared by many sessions
  (application-wide); session grants are entries, shared grants live
  behind the interface.
- **Limits**: expiry, maximum uses; revocable (`s.Revoke(id)`), and a
  revocation is an entry.
- Created explicitly or from a decision ("approve and always allow this
  command").
- A deny-grant exists too (always deny, with the reason the model sees).

### 5. Workflow features

- **Expiry**: a request past its expiry is denied with a stated reason
  on the next resume.
- **Quorum**: `Quorum(n)` requires n decisions from distinct approver
  identities before a call resolves; conflicting decisions resolve to
  deny.
- **Notifications**: `OnRequest(func(Request))` fires when a request
  parks, so an application can push it anywhere; the session never does
  I/O of its own for this.
- **Audit**: `s.Audit()` iterates every request, chain step, decision,
  grant, revocation and expiry with its entry id and time.
- **Interrupt and busy rules**: a `Send` while approvals are pending is
  queued (follow-up) by default; an interrupt (v0.3) denies the pending
  calls with a stated reason before starting over.

### 6. What is not here

- **Nested subagent approvals** — a child run's parked call today fails
  the parent's call loudly (`SUBAGENT_PENDING`, ADR 0014). Carrying it up
  and resuming the child under its lineage id needs the pool's child
  bookkeeping: ADR 0022.
- **MCP elicitation → a parked call** — belongs with `weft/mcp`, built on
  this ADR's `Request`/`Decision` types.

## Consequences

- New entry kinds (`approval_request`, `approval_decision`, `grant`,
  `grant_revoked`, `approval_audit`) carry `"v":2` (ADR 0011 §6); a
  v0.1 reader fails loudly on a session that used approvals.
- The core stays unchanged: everything maps to the existing options.
- Model-visible text for expiry, quorum denial and deny-grants is pinned
  by golden tests (standing rule: model-visible bytes need an ADR).

## Rejected

- **A live, in-run prompt as a second model** (pi RPC / AI SDK
  `PromptControl`) — offered instead as the `Approver` step of the one
  run-boundary model.
- **Keys in the session file** — a leaked file must not forge decisions.
- **Grants that match on tool name only** — Crush #497.

## Amendment (2026-09-29 — the boundary holds the tail raw)

An open approval boundary holds the compaction trigger: a parked
tail's dangling calls are exactly what the resume's decisions resolve,
and a compaction that summarized them would orphan every decision.
While a boundary is open no automatic compaction runs and a manual
`Compact` refuses, naming the pending approvals; the trigger re-arms
on the turn that resolves the boundary (the resume's own post-turn
site). The rule was implemented and pinned from the start
(`compaction.go` cites "ADR 0021 §1's raw-transcript rule") but stated
in no ADR — the 2026-09-29 release review asked for it to be decided,
not drifted; this is that decision. A shared grant's matches are also
namespaced in the audit detail ("shared grant …"), so a store id that
collides with a session grant's entry id can never inflate the
session grant's use count — audit prose is not a data channel between
the two scopes (§4).
