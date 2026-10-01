# ADR 0019 — Steering: one pull hook in the loop, policy above it

- Status: decided (2026-09-28, TODO §5.16; consumer: `weft/thread`
  v0.3, `docs/phase3-thread-plan.md` §5)
- Depends on: ADR 0001 (the message model), ADR 0002 (budgets, the
  partial transcript), ADR 0004 (event ordering, `Seq`, the sealed
  event set), ADR 0006 (seams — this is not a third one), ADR 0007
  (the approval boundary), ADR 0010 (the record format), ADR 0017
  (record/replay)
- Implementation: root module, a minor release (planned v0.4.0), plus
  lockstep sub-module bumps (TODO §1.1 two-phase release)

## Context

A user types while an agent is working. Three outcomes are possible:
**steer** (the model sees the message before its next call, inside the
same run), **follow-up** (the message waits until the run ends and
starts the next one), and **interrupt** (cancel the run and start over
with the message). Queue policy ("reject", "enqueue", "interrupt",
"rollback" — LangGraph Platform's double-texting names) sits on top.

The survey (2026-09-28) found one delivery point everyone agrees on and
one split everyone makes:

| Framework | Steer | Where the queue lives | Delivery point |
|---|---|---|---|
| pi | yes | `Agent` class; the loop only has pull callbacks (`getSteeringMessages`, `packages/agent/src/agent-loop.ts`) | after the tool batch, before the next model call; also when the run would stop |
| Pydantic AI | yes (`asap`) | run-scoped queue, drained by a capability that writes into the run's messages (`capabilities/_pending_messages.py`, "pi-mono parity") | before the next model request; a leftover redirects the run into one more request |
| Codex app-server | yes | turn/session layer (`turn/steer`) | next provider request, with the tool outputs |
| Claude Agent SDK | yes | session process (streaming input) | after the running tool finishes |
| Vercel AI SDK | by hand | `prepareStep` returning messages | per step; not in the response messages |
| LangGraph | no | double texting is Agent Server only, not the OSS runtime | between runs |
| OpenAI Agents SDK | via stop + `RunState.add_input` | runner | the resumed model call |

Two facts decide weft's shape:

1. **It cannot be done faithfully from outside the loop.** `PrepareStep`
   works on a deep copy and never alters the transcript (ADR 0006), so a
   steer injected there is seen by the model but missing from
   `RunResult.Messages`, the record, and the next turn — the "accepted
   but never persisted" bug Codex #40805 shows users notice. Stopping
   the run and starting another splits the run id, the `Seq` stream,
   the budgets and the trace, and a steer that arrives during a final
   tool-less call is silently demoted to a follow-up.
2. **Queues and policies are session state.** The core is stateless and
   the caller owns the transcript; who may interrupt whom, receipts, and
   what happens to a message sent while an approval is pending are
   `thread` concerns (ADR 0011).

## Decision

### 1. The mechanism: `weft.Steering`, a run option

```go
// SteerFunc returns the messages to deliver at a safe point, or nil.
// It must not block: drain a queue, do not wait on one.
type SteerFunc func(ctx context.Context, at SteerPoint) []Message

// SteerPoint tells the source where the run is.
type SteerPoint struct {
	RunID string
	Step  int  // the step that just finished
	Final bool // true when the run would otherwise end (no tool calls)
}

// Steering installs a steering source for this run.
func Steering(fn SteerFunc) RunOption
```

It is a **`RunOption` only**. A session steers the run it started;
an agent-level default has no queue to read from, and a run option is
the one way a layer that did not construct the `*Agent` can attach
per-run behaviour (options are sealed; `Agent.With` is deferred).

### 2. The two drain points

The loop calls `fn` after `StepFinish` is emitted for a step, in this
order of the existing checks:

1. **Pending approvals — not drained.** A run that parks calls ends at
   the approval boundary (ADR 0007) untouched; a steer cannot resolve a
   parked call. The source keeps its messages; the session delivers them
   after the decision, or as a follow-up.
2. **The step made tool calls and no `StopWhen` fired** (`Final:
   false`): drain. Every call of the batch already has its result
   appended — success, error, truncated, or denied — so the tool
   call/result pairing (ADR 0001) cannot be split.
3. **The step made no tool calls** (`Final: true`): drain. A non-empty
   return appends the messages and the loop runs one more step instead
   of ending — the pi/Pydantic redirect; without it a late steer is
   stranded.
4. **A `StopWhen` condition fired — not drained.** An intended end
   (structured output submitted, a stop tool called) stays an end; the
   session delivers the message as a follow-up.

Steering never cancels a tool. Cancelling is interrupt, and interrupt
is the context (ADR 0002).

### 3. Delivered messages are transcript

Drained messages are appended to `res.Messages` as ordinary messages
(normally `RoleUser`), before the next step's `PrepareStep` runs, so
request rewrites see them and the transcript stays the single source of
truth. A message with any role other than `RoleUser` fails the run with
`ErrInvalidSteer` — the model's own turns come from the model.

### 4. One new event: `Steered`

```go
// Steered reports messages delivered by the run's steering source.
type Steered struct {
	RunID    string
	Seq      int64
	Step     int       // the step after which they were delivered
	Messages []Message
}
```

Emitted between that step's `StepFinish` and the next `StepStart`,
under the same `Seq` lock as every other event (ADR 0004). Wire
discriminator `"steered"`. It is the only addition to the sealed event
set; `Nested` wraps it for child runs like any event.

### 5. Budgets

A redirect at a `Final` drain point consumes a step. The continuation
check (`guard`: `MaxSteps`, `UsageLimit`, `DetectLoops`) runs as it
does for any continuation; if it fails, the run fails with the usual
error and the steer is in `RunError.Result.Messages`, delivered but
unanswered. The session reports that on the message's receipt.

### 6. Record and replay

The recorder stores `Steered` like any event, so a record shows what
the model saw and when (ADR 0010 is additive here: a new event type is
readable by a store that knows it; an older reader answers
`ErrUnknownEvent` → `newer_format`, as designed). `wefttest.Replay`
feeds recorded steers from the record instead of calling a live source,
keyed by step, so a replayed run cannot diverge (ADR 0017).

### 7. Subagents

The source belongs to the run it was passed to. A child run started by
`Subagent` gets no steering source; the option is not inherited through
the context. Forwarding a steer to a child is a session decision made
explicitly (ADR 0011, pool milestone).

## Consequences

- One run option, one event, one error sentinel. No third seam: the
  hook cannot rewrite the request or the transcript, only append user
  messages at two fixed points (PLAYBOOK §4's red flag does not apply —
  it changes no existing behaviour and is off unless installed).
- `weft/thread` builds every policy on it: steer, follow-up, interrupt,
  reject, with receipts (queued → delivered | deferred | dropped).
- Studio folds `Steered` as a user turn inside the run (a studio minor).
- Model-visible change: a delivered steer is a user message at a new
  position. Pinned by a golden transcript test in `contract_test.go`.

## Rejected

- **`PrepareStep` injection** — invisible to the transcript and record.
- **`Steer(ch <-chan string)`** — strings drop images and files; channel
  close semantics are one more thing to get wrong; a pull function is
  scriptable in tests and a channel wraps into it in three lines.
- **Queues on `*weft.Run` (`run.Steer()`, `run.FollowUp()`)** — puts
  session policy in the stateless core and still needs a follow-up
  owner after the run ends.
- **Stop and restart from the session** — splits run identity, budgets
  and traces, and demotes late steers silently. Kept only as the
  fallback a caller on an older core can build.

## Amendment 2026-10-01 — what a steer's receipt says, in `weft/thread`

The core mechanism above is unchanged. This amendment records how
`weft/thread` reports a steer's fate, where §5 and the Consequences
left it to "the session".

**Every end has a name.** A Send under the Steer policy returns a
`Turn` that is the message's receipt. Its `Wait` used to return
`nil, nil` for three different ends, and `Next` was nil for two of
them. `Turn.Outcome()` now tells them apart, and the receipt entry
records the same thing durably:

| The steer… | `Turn.Outcome()` | `Turn.Wait()` | `Turn.Next()` | Receipt entry |
|---|---|---|---|---|
| was drained by the running run (§2, points 2–3) | `TurnDelivered` | `nil, nil` | nil | `delivered`, `run_id` |
| met a non-drain exit — pending approvals, `StopWhen`, or a run that ended before its drain (§2, points 1 and 4) | `TurnDeferred` | `nil, nil` | the follow-up turn | `deferred`, `turn` |
| was removed by `ClearQueue` before delivery | `TurnDropped` | `nil`, an error wrapping `thread.ErrDropped` | nil | `dropped` |

The outcomes of a turn with a run of its own (`TurnAnswered`,
`TurnParked`, `TurnFailed`, `TurnCanceled`) and the not-yet-ended
`TurnRunning` complete the set; they are ADR 0011 §4's.

**Delivered but unanswered (§5).** §5 promises that a steer delivered
into a run that then fails — the continuation guard, a model error, a
cancellation — is "delivered but unanswered", and that the session
reports it on the receipt. It does now: the `delivered` receipt entry
carries `unanswered: true` when the run recorded no completed model
step after the step whose drain delivered the message. The steer's
`Turn` still ends `TurnDelivered` — the message is in that run's
recorded transcript, so the next turn's model sees it — and the field
is what tells an application that no reply to it exists. The field is
additive (`omitempty`, the receipt kind stays at `"v":3`).

**A deferred steer's follow-up is durable.** The follow-up turn a
deferred steer becomes is a queued send, and a queued send is durable
at acceptance (ADR 0011 §4, amended the same day): the `deferred`
receipt and the follow-up's `accepted` receipt land in one atomic
append. A writer that stops between the deferral and the follow-up's
turn loses nothing; `Open` restores the follow-up to the queue.

**A steer on an idle session runs as a plain turn.** Steering is what a
Send does *when the session is busy*. With no turn in flight and no
approval boundary open there is nothing to steer into: the Send
appends its prompt and runs, exactly as under `Queue`, and its `Turn`
is that run's handle (`TurnAnswered`, a result from `Wait`). The same
holds in the window between a turn's end and the session's
between-turn housekeeping: the Send is accepted and runs next. The
turn entry records the policy the Send was called under
(`TurnEntry.Policy` — `"steer"` here, and on a deferred steer's
follow-up), so the difference is on the record even though the
behaviour is a plain turn's.

**Run options.** A steer's run options do not apply to the run that
drains it — the message joins another turn's run, under that run's
options. They apply to the follow-up turn when the steer defers.

**The live steer queue is unbounded.** Each accepted steer costs one
durable receipt entry and stays queued until a drain point, the turn's
end, or `ClearQueue` takes it; the session does not cap how many may
wait. `Session.Queue()` lists them (and the queued sends behind them),
so an application that must bound what a user can pile onto a running
turn checks its length before sending. A `MaxQueue` option was
considered and left out: the right bound, and what to do at it (reject,
drop the oldest, coalesce), is product policy, and one more
`ErrBusy`-shaped path is not free.

**Errors.** A steered message with a role other than `RoleUser` is
refused by `Send` with an error wrapping `weft.ErrInvalidSteer` — the
same sentinel the loop raises for a source that returns one — before
any receipt is written. `weft.Steering` passed through
`thread.RunOptions` is refused with `weft.ErrInvalidRunOption`: the
session owns the steering source of every run it starts.
