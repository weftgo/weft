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
resolve(content) | resolve_error(content), who, when, via, key id, the
request entry answered).
`s.Decide(ctx, decisions...)` records them; when every pending call of
the run has a decision the session resumes with the core's
`Approve`/`Deny`/`Resolve`/`ResolveError` (`AutoResume`, default on), or
the caller calls `s.Resume(ctx)` — undecided calls are then denied with
the core's "no decision" text. A decision for a call that is not pending
is `ErrNotPending` (the core's rule, raised before any run starts); a
batch naming one call twice, or holding a decision with no outcome, is
`ErrInvalidDecision`, and nothing of it is recorded.

A request entry's id names one **occurrence** of a call. Call ids repeat
across turns (ADR 0007); the entry id never does, and neither does the
run that parked it. Decisions belong to an occurrence, never to a call
id, and a decision is **spent** by the resume that applies it: it
resolves its call on that resume's line of the tree only. A `Branch`
back to a decided boundary — or a `Fork` of one — shows the call
pending again and takes a new decision; an approved call never runs
twice on one approval.

### 2. The decision chain

Before a request parks, the session asks, in order:

1. **Grants** (§4) — a matching live grant approves at once.
2. **An `Approver`** — `func(ctx, Request) (Decision, bool)`, optional;
   the "ask now" path for a terminal or a UI already connected. It
   returns `false` to decline to decide (a pipe, no TTY), never blocks
   forever: `WithApprover(a, timeout)` takes the time it is given, and
   the timeout must be positive — a session that is not interactive
   sets no Approver at all.
3. **Park** — the request is persisted and the turn ends with
   `Pending`; any transport can decide later, hours later.

Each step writes an audit entry, including automatic approvals. A call
the chain leaves open always parks with its request entry — also when
a step decided and the decision alone does not resolve the call (one
approval under `Quorum`): the request is what carries the expiry, the
notification and the challenge the remaining decisions answer.

### 3. Signed decisions

For decisions that cross a process boundary (a web UI, a chat
integration, email):

- `s.Request(callID)` returns a `Request` with a **challenge**: session
  id, the request entry's id and the run that parked the call (the
  occurrence — a signature for run t1's `call_1` says nothing about the
  `call_1` a later run parks), call id, tool, `args_sha256`, expiry,
  nonce, key id — HMAC-SHA256 under the session's `Keyring`. The nonce
  carries a tag only a ring key can compute for that request, so the
  session recognises the challenges it minted without storing them.
- The client returns a `SignedDecision` over the challenge plus the
  outcome (`Keyring.Sign`, or `Key.Sign` for an approver holding one
  key). `s.DecideSigned(ctx, sd)` verifies **fail-closed** before any
  decision is recorded: `ErrBadSignature` (the MAC, an empty or
  unissued nonce, an expiry or tool that is not the request's — and an
  unknown key id, in the same words: the verifier never says which key
  ids exist), `ErrReplay` (nonce already used — nonces are entries, so
  replay protection survives restarts), `ErrExpired` (the *request's*
  expiry against the session's clock; the signature cannot choose its
  own), `ErrNotPending` (no such pending call, or another occurrence
  of it), `ErrArgsChanged` (the arguments hash no longer matches).
  `ErrUnknownKey` is what `Keyring.Sign` tells the ring's holder.
- A refused signature is audited (step `signed`, the reason, no
  decision), within bounds: nothing for an unverifiable signature that
  names no pending call, and at most 16 per request.
- `Keyring` holds several keys by id; at most one is active — the key
  new challenges are minted under — and all verify: rotation without
  invalidating requests in flight. A ring with no active key is
  verify-only. Keys never touch the session file; only key ids do.
- Unsigned `Decide` stays available for in-process callers; a session
  under `RequireSigned()` rejects it. The rule is the session's:
  `Create` writes it into the header (`weft.require_signed`), every
  `Open` enforces it, a `Fork` takes it from its origin (with the
  origin's keyring, unless given its own), and nothing loosens it.
  `RequireSigned` without a keyring that can mint a challenge fails at
  `Create`/`Open`. The session's own paths — grants, the Approver,
  expiry, an interrupt's denial, a pool delegation's resolution — are
  not the unsigned door and record under it.

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
  command"). The grant belongs to the verdict, not the decision: it is
  minted only when the call's effective verdict is approve — with the
  approval that completes a quorum, never beside a denial.
- Uses are counted from the audit entries' `grant_id` field — audit
  prose is never parsed — and within one turn too: two matching calls
  in one step spend a one-use grant once.
- A deny-grant exists too (always deny, with the reason the model sees).

### 5. Workflow features

- **Expiry**: a request past its expiry takes no decision — `Decide`
  and `DecideSigned` refuse one with `ErrExpired` — and is denied with
  a stated reason the next time the session looks at the boundary:
  every path that records a decision or arms a resume sweeps first
  (`Decide`, `DecideSigned`, `Resume`, a `Send`, the runner's own
  pickup). Nothing fires on a timer.
- **Quorum**: `Quorum(n)` requires n approvals from distinct approver
  identities before a call resolves; conflicting decisions resolve to
  deny. A **signed** approval's identity is its key id — one key, one
  approver, whatever `Who` it carries. An **unsigned** approval's
  identity is its `Who`, which is a declaration nobody verifies: one
  caller can write two names. A quorum that must hold against the
  deciding process needs `RequireSigned` and a key per approver;
  without it `Quorum` is a workflow rule among trusted callers. A
  grant's approval is one identity of its own. The session's own
  denials (expiry, interrupt) resolve with their stated reason even
  beside an approval — they are not one side of a split verdict.
- **Notifications**: `OnRequest(func(Request))` fires when a request
  parks, so an application can push it anywhere; the session never does
  I/O of its own for this. It runs synchronously on the runner.
- **Audit**: `s.Audit()` iterates every request, chain step, decision,
  grant, revocation and expiry with its entry id and time, plus the
  turn entry of each resume (how it ended). The trail is an **index of
  the log, not tamper-evident evidence**: entries are plain appended
  lines, unsigned and unchained; whoever can write the storage can
  rewrite them. Tamper-evidence belongs to the storage.
- **Interrupt and busy rules**: a `Send` while approvals are pending is
  queued (follow-up) by default; an interrupt denies the pending calls
  with a stated reason (decision via `interrupt`) before starting over,
  and fails the `Send` — nothing queued — when the denial cannot be
  recorded.

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

## Amendment (2026-10-07 — a resume runs on the parked turn's context values)

A resume armed by `Decide`, `Resume`, a `Send` or `Continue` ran on
the arming call's context alone, so a rule that rode the parked turn's
*context* — a `weft.ParkAllExcept` list a delegating run handed down
to a `thread/pool` child — was gone for the resumed steps, whoever
decided. The resume now runs on the arming call's context for
cancellation and deadline and takes the parked turn's context values
for every key the arming context lacks (the parked turn's run options
already rode along, §1). After a restart the parked turn's context is
gone, like its options.

## Amendment (2026-10-01 — the approvals hardening)

A review of the shipped subsystem found the decision above under-kept
in places; this amendment is the decision restated where the code now
differs, folded into §1–§5 above. What changed, and why:

- **Occurrence binding (§1, §3).** A challenge covered the call id but
  not the run or the request entry, and call ids repeat: a signature
  minted for one turn's `call_1` approved the next turn's. The request
  entry id and run id are now signed and checked against the pending
  request; the challenge domain moved to `weft/approval-challenge/v2`,
  so no v1 signature verifies. An empty nonce is a bad signature.
- **"Allowed outcomes" is gone (§3).** The challenge listed every
  outcome, always — a claim that restricted nothing. It is removed
  rather than pretended; a request that allows a subset is a future
  decision with its own domain string.
- **Expiry on every path (§5).** The sweep ran only in `Resume`, so
  `Decide(Approve)` on a lapsed request ran the tool. It now runs
  wherever a decision is recorded or a resume armed.
- **Decisions are spent (§1).** Branching back to a decision entry
  re-ran the approved call. The resume's audit entry now lists the
  decisions it applied (`decisions`), and a decision whose resume is
  on another line of the tree is not in force.
- **Quorum identity (§5).** Distinct `Who` strings satisfied a quorum,
  signed or not; one key could sign as two names.
- **`RequireSigned` is durable and does not wedge the session (§3).**
  It was a process flag, lost on reopen, and it blocked the session's
  own denial on interrupt and the pool's wrapper resolution.
- **Wire.** All additive under `"v":2`: `request_id` and `always` on
  decisions; `grant_id`, `grant_shared`, `key_id` and `decisions` on
  audit entries; the header metadata key `weft.require_signed`. An
  older reader skips them and behaves as it always did. Model-visible
  text is unchanged; one reachable case moved — an expired or
  interrupted request that already held an approval is denied with the
  expiry or interrupt reason, not "conflicting decisions".
