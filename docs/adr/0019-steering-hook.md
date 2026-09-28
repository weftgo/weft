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
