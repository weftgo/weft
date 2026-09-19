# The agent loop, from first principles to weft

> A learning document. It teaches how an agent loop works, function by
> function, where it breaks, how seven studied frameworks and fifteen
> more answered each breakage, what the wider literature (workflow
> patterns, context engineering, tool design, safety, protocols,
> durable execution, evaluation) adds above the loop, and what weft
> decided and why. Read it end to end once; afterwards use Part III as
> a reference, Part IV when you design the layer above the loop, and
> Part V as a checklist when you design or review an agent library.
>
> Sources: weft's code and ADRs (`docs/adr/`), `docs/life-of-a-call.md`,
> `TODO.md`, `../THE-END-GOAL.md`, `../AGENTIC-STACK-2026.md`, the
> framework deep dives in `../docs/frameworks/` (Vercel AI SDK,
> Pydantic AI, LangGraph, Mastra, Crush, DeerFlow, pi), and the vendor
> documentation, essays and papers listed in Chapter 32. Section
> numbers in brackets like [ai-sdk §4.2] point into the deep dives.
> Everything about weft is the code as of 2026-09-14; the subagent
> chapter (§11) describes the design decided for TODO §5.1 and marks
> what is not yet implemented. Claims about outside systems that could
> not be checked against a primary source in this revision are marked
> *(unverified)*.

## How to read this

- **Part I** builds the loop from nothing, names its parts, and then
  walks the full path of one run through weft, function by function,
  with every exit and every goroutine accounted for.
- **Part II** takes the eleven questions every loop must answer, one
  chapter each: the problem, the options, what the field does, what weft
  does (with code), the edge cases, and the test that pins the rule.
- **Part III** puts the seven studied frameworks side by side, then
  widens to fifteen more systems and to the harnesses built around
  loops.
- **Part IV** covers the layer above the loop: workflow and agent
  patterns, the research lineage, context engineering and memory, tool
  design, safety, protocols, durable execution, evaluation, and the
  twelve-factor checklist, each ending with where it lands in weft.
- **Part V** distils the principles, the edge-case catalog, the reading
  guide, the glossary and the sources.

If you only have twenty minutes: read §1, §2, §3 ("The full path"),
§5, §10 and §11. If you are designing the layer above the loop, read
§19, §21 and §23 as well.

---

# Part I — The loop from first principles

## 1. The forty-line loop

Strip every framework down and this is what remains. A model is a
function from a transcript to a reply. A reply either answers or asks
for tools. If it asks for tools you run them, append the results, and
call the model again.

```go
func run(ctx context.Context, model Model, tools map[string]Tool, msgs []Message) ([]Message, error) {
	for step := 0; step < 10; step++ {
		reply, err := model.Call(ctx, msgs)          // 1. the model turn
		if err != nil {
			return msgs, err                         //    a model failure is a run failure
		}
		msgs = append(msgs, reply)                   // 2. record the assistant turn
		if len(reply.ToolCalls) == 0 {
			return msgs, nil                         // 3. no tools requested: done
		}
		var results []ToolResult
		for _, call := range reply.ToolCalls {       // 4. run every requested tool
			out, err := tools[call.Name].Run(ctx, call.Args)
			if err != nil {
				out = "error: " + err.Error()        //    a tool failure is data
			}
			results = append(results, ToolResult{CallID: call.ID, Content: out})
		}
		msgs = append(msgs, ToolMessage(results))    // 5. record the tool turn
	}
	return msgs, errors.New("too many steps")        // 6. the safety budget
}
```

THE-END-GOAL quotes Anthropic's finding that "the strongest agents use
simple composable patterns rather than a framework" and that this loop
is about forty lines of Go. That is true, and it is the reason a
framework has to earn its place: every line it adds must answer a
question the forty lines get wrong.

### The vocabulary

Every framework uses these words, not always for the same thing. weft's
definitions, which this document uses throughout:

| Word | Meaning in weft | Where |
|---|---|---|
| **Run** | One call to `Generate` or `Stream`: an id, an input transcript, a sequence of steps, a result or a `*RunError`. | `run.go` |
| **Step** | One model call plus the execution of the tool calls it requested. `StepRecord` records it. | `loop.go`, `StepRecord` |
| **Turn** | One message in the transcript. A step produces one assistant turn and, if tools ran, one tool turn. | ADR 0001 |
| **Transcript** | `[]Message`, roles `user`, `assistant`, `tool`. No system role: instructions are agent-level. | ADR 0001 |
| **Tool call / result** | `ToolCallPart{ID, Name, Args}` on an assistant message; `ToolResultPart{CallID, Name, Content, IsError}` on the tool message directly after it. | ADR 0003 |
| **Stop reason** | Why the model turn ended: `stop`, `tool_calls`, `max_tokens`. Recorded, never hidden. | `model.go` |
| **Stop condition** | The intended end of a run (`StopWhen`). Success. | `agent.go` |
| **Budget** | The safety limit (`MaxSteps`, default 10). Exceeding it is a failure. | ADR 0002 |
| **Event** | A progress notification on the stream: `RunStart`, `StepStart`, `TextDelta`, `ToolStart`, `ToolFinish`, `StepFinish`, `RunFinish`, and so on. | ADR 0004 |
| **Seam** | The place behaviour attaches: around the model call, around the tool call. Two of them. | ADR 0006 |
| **Tap** | The place observation attaches. Sees everything, changes nothing. | ADR 0004 |
| **Reasoning block** | One provider thinking block. A delta carrying its signature closes it; one `ReasoningPart` per block, stored and forwarded, never read. | `model.go`, ADR 0001 |
| **File part** | `FilePart{MediaType, Data or URL}` on a user message; exactly one of Data or URL. The only way to send a file, and the core never reads the bytes. | `message.go`, ADR 0001 |

Other frameworks say "superstep" (LangGraph), "iteration" (Mastra),
"turn" (pi, for what weft calls a step), and "request" (Pydantic AI,
whose budget counts model requests). When you read their docs, map to
this table first.

## 2. Why forty lines are not enough

Run the forty lines in production for a week and you meet these
questions, in roughly this order. Each is a chapter of Part II.

| # | Question | The failure that forces it | Chapter |
|---|---|---|---|
| 1 | When does the run end, and is ending a success? | A runaway loop that looks like success. | §4 |
| 2 | What is a tool failure? | One tool error aborts the whole run, or is silently swallowed. | §5 |
| 3 | What runs concurrently, and in what order? | Four tools race; results come back shuffled; a hung tool hangs the run. | §6 |
| 4 | Is the transcript always valid input? | A crash mid-tool leaves a dangling call; the provider rejects every later turn. | §7 |
| 5 | What does the caller see while it runs? | A UI that cannot render interleaved progress; a recording that cannot be replayed. | §8 |
| 6 | Where does cross-cutting behaviour attach? | Fifteen lifecycle hooks, each a contract. | §9 |
| 7 | How does a human say yes or no? | The process blocks on a prompt; the decision cannot arrive over HTTP an hour later. | §10 |
| 8 | How does one agent use another? | Graph DSLs, handoff primitives, black boxes inside black boxes. | §11 |
| 9 | How much may a run cost? | Steps multiply across nesting; the same call repeats forever. | §12 |
| 10 | What survives a restart? | A deploy kills a run mid-tool. | §13 |
| 11 | What can never change? | Four major versions in twenty months. | §28 |

## 3. Anatomy of one step in weft

Before the chapters, here is the concrete loop this document keeps
returning to. It is `Agent.execute` in `loop.go`, compressed to its
control flow. Every phase named here is documentation; only the seams
are code (`docs/life-of-a-call.md`).

```
Generate / Stream
  │  repair(input transcript)                 §7
  │  emit RunStart{ID, Model, Agent}          §8
  │  resolve pending approvals, if any        §10
  ▼
for step < MaxSteps:                          §4
  │  emit StepStart
  │  build ModelRequest: system + snippets, transcript, tools, thinking
  ▼
  model middleware chain → adapter → provider   §9
  │  ModelTextDelta / ModelReasoningDelta / ModelToolCallDelta / ModelToolCall … ModelFinish
  │  contract enforced: exactly one finish, nothing after it, panics → run error
  ▼
  append the assistant message (reasoning, text, calls); never an empty one   §7
  │
  ├─ finish == max_tokens and calls > 0 → every call fails without executing   §5
  └─ calls > 0 → execTools:                                                     §6
        acquire slot in call order → ToolStart(Seq) → containment boundary
        → agent WrapTools chain → tool WrapTools chain → base
          (resolve → RequireApproval? → decode → handler)
        → containment boundary → render → cap → ToolFinish(Seq)
        parked calls (ErrApprovalRequired) → pending, no ToolFinish             §10
  │
  │  append the tool message; record StepRecord; add usage; emit StepFinish
  ├─ pending > 0 → RunFinish{Pending}; return res, nil                          §10
  ├─ calls == 0  → RunFinish; return res, nil
  └─ StopWhen met → RunFinish; return res, nil                                  §4
after the loop: ctx canceled? → that error; else ErrMaxSteps                    §4, §6
```

Two things to notice before the chapters. First, there are exactly two
kinds of exit: a successful `RunFinish` (the model stopped asking, a
stop condition fired, or approval is pending) and a `*RunError` carrying
the partial transcript (model failure, cancellation, budget). Second,
nothing in the loop retries a model call, catches a tool error into a
run error, or cancels sibling tools. Those three "obvious improvements"
are the ones the field learned to avoid, and the rest of this document
explains why.

### The model turn, in detail

The diagram compresses the model call to one box. It is worth opening,
because everything below the model seam is also contract.

The loop builds one `ModelRequest` per step. Its five fields are the
whole interface between the loop and the provider world:

- `System`: the agent's `Instructions` plus one paragraph per
  advertised tool's `PromptSnippet`, blank-line separated, in
  registration order (`composeSystem`, pinned by
  `TestPromptSnippetsComposeIntoInstructions`). A tool can teach the
  model its own conventions without touching the agent's text.
- `Messages`: the transcript so far, after repair.
- `Tools`: the advertised definitions. With `ToolSource` set the list
  is fetched fresh at each step, and dispatch resolves against the
  same fetch, so a registry can change while the agent runs.
- `Thinking`: the reasoning request, `ThinkingConfig{Level, Budget}`
  on a neutral scale (`ThinkOff`, `ThinkLow`, `ThinkMedium`,
  `ThinkHigh`). The agent's `Thinking` option is the default; a
  run-level `Thinking` overrides it for one run. The quick-ask shape:
  fast by default, think on demand, no rebuilt agent.
- `SequentialTools`: true exactly under `Sequential()` or
  `Parallelism(1)`, so the provider is asked not to emit batches the
  execution policy would serialise anyway (Chapter 6).

`Model` itself is one method, `Stream(ctx, req) iter.Seq2[ModelEvent,
error]`, and the loop enforces its contract rather than trusting it:
events in order, tool calls whole (assembling a provider's streamed
argument fragments is the adapter's job; the fragments may stream as
`ModelToolCallDelta` progress, the assembled call still arrives
whole), exactly one `ModelFinish`, failure as one terminal error, ctx
honoured. A stream that ends without a finish, continues after it,
yields a call with an empty id or name, two calls sharing an id in one
step, emits an unknown type, or
panics, fails the run wrapping `ErrModelContract`. A broken adapter
cannot corrupt a transcript silently. `ModelFinish.Raw` carries the
provider's own stop reason whenever the mapping was approximate
("refusal", "pause_turn", "content_filter"); it is recorded on the
step and never interpreted.

Two adapter-layer rules complete the turn:

- **The idle timeout.** `IdleTimeout` (default 60 s, `IdleTimeout(0)`
  disables) bounds the gap between chunks, not the whole call; expiry
  is `ErrStreamIdle`. A slow but streaming response is never killed, a
  silent one is. This is Crush's idle-versus-hard rule [crush §5.3],
  placed below the core because it reads the wire.
- **The kill switch.** `WEFT_MODEL_REQUESTS=deny` makes every adapter
  fail with `ErrModelRequestsDenied` before any network I/O, and
  `ModelRequestsAllowed()` is checked per call, never cached. A test
  suite can guarantee no run touches the network; `wefttest` ignores
  the switch on purpose.

The adapters (ADR 0013) wrap the vendors' official Go SDKs. They own
no HTTP, retry nothing (transport retries live in the vendor SDK's
config), and create only wraps of `ErrUnsupported`, `ErrStreamIdle`,
`ErrModelRequestsDenied` and the ctx error. Cancellation is terminal:
a canceled stream returns `(nil, ctx.Err())`, never a fabricated
finish. Each adapter declares its capabilities to the conformance
suite rather than degrading quietly. The OpenAI adapter has no
reasoning signatures (compat servers' `reasoning_content` maps without
one) and speaks two thinking dialects, `reasoning_effort` on the
official API and a body-injected `thinking` object on gateway hosts,
with `Dialect` to override. The Anthropic adapter has thinking with
signatures, PDF and image input, `disable_parallel_tool_use`, folds
cache tokens into the input count, and drops unsigned reasoning on
send. The Google adapter returns thought signatures per block and per
call, folds thought tokens into the output count, and declares
`Sequential` a gap because Gemini has no switch. Reasoning survives
the round trip whole: a delta carrying its signature closes a block,
one `ReasoningPart` per provider block, and Gemini's per-call thought
signatures ride their own `ToolCallPart.Signature`.

### The full path, function by function

The diagram and the model-turn section above are the map. This is the
territory: every function a run passes through, in order, with what it
decides. Read it once with `run.go` and `loop.go` open; afterwards the
edge cases in Part II are all one-line consequences of something here.

**1. Entry: options become a `runConfig`.** `Agent.Generate` and
`Agent.Stream` both start the same way. Each `RunOption` is applied in
the order given: `Prompt` appends a user message, `Messages` appends
an existing transcript, `RunID` sets the id, `Approve` and `Deny` record
decisions in a map keyed by call id, and a run-level `Thinking` sets an
override flag. `cfg.finish()` mints a random 128-bit hex id if none was
given, so the id exists before anything runs. `Generate` then calls
`execute` with a sink that discards events; it is `Stream` with the
events folded away, and its error is the same `*RunError` value.
`Stream` wraps the ctx in `context.WithCancel` and returns a lazy
`*Run`: nothing executes until `Events()` is ranged or `Wait()` is
called, so `run.ID()` can be handed to a client first.

**2. The producer goroutine.** `Run.Events()` marks the run started
under `Run.mu` (a second call yields only `ErrRunConsumed`), makes an
unbuffered channel, and spawns the goroutine that runs `execute`. Its
sink does a `select` between sending on the channel and the run ctx
being done, so a consumer that has gone away never blocks a producer.
Three defers run last-in-first-out: close the channel, which ends the
consumer's range; close `done`, which unblocks `Wait`; cancel the ctx,
which releases everything the run held. If the consumer breaks out of
the range, `Events` cancels the run itself. After the channel closes,
the stored error, if any, is yielded exactly once as the final element.
`Wait` ranges `Events` itself if nobody has, then returns the stored
result: no deadlock is possible from forgetting to consume.

**3. `execute`: the `emit` wrapper and repair.** The first thing
`execute` builds is `emit`. It returns immediately when `ctx.Err()` is
set (nothing after cancellation, for taps and sinks alike), runs every
`Tap` in registration order through `safeTap` (a recovered panic drops
the observation, never the run), then calls the sink. Next, if the run
carries decisions, `unresolvedCalls` finds the last assistant message's
calls with no result on the tool message after it, and their ids form
the `skip` set. `repair(cfg.messages, skip)` then produces the run's
starting transcript, and `RunResult{ID, Messages}` is allocated: from
here on `res` is the single object every exit returns, whether on
success or on `RunError.Result`. A `seq` counter starts at zero. The
`fail` closure wraps any error as `&RunError{Step, Err, Result: res}`.
Then `RunStart{ID, Model: InfoOf(model), Agent: name}` is emitted, so the
id and model identity are the first thing any observer sees.

**4. The resume branch.** If `unresolvedCalls` found anything, the run
resolves it before any model call: a ctx check, then `resolvePending`
(described under "The resume path" below), then `attachResults` places
the results on the tool message that follows the issuing assistant
message, creating that message if the earlier run recorded none. If
some calls parked again, the run ends here with `Steps: 0`,
`RunFinish{Pending}`, and cancellation still wins.

**5. The step header.** `for step := 0; step < a.maxSteps; step++`: a
ctx check first (cancellation between steps is a run error at that
step), then `StepStart{Index}`. `effectiveTools()` returns the
`ToolSource` list if one is set, otherwise the static list built at
`New`. The `ModelRequest` is assembled: `composeSystem(instructions,
tools)` (instructions, then one paragraph per tool `PromptSnippet`,
blank-line separated, in order), the transcript so far, the tools,
`SequentialTools: a.parallelism == 1`, and
`cfg.effectiveThinking(a.thinking)` (the run override if set, else the
agent default, else the zero value meaning "provider default").

**6. `consume`: the model turn under contract.** `a.model` is already
the middleware chain built once at `New` (first `WrapModel` listed is
outermost), so `a.model.Stream(ctx, req)` enters the outermost
middleware, which eventually calls the adapter. `consume` ranges the
iterator with a deferred `recover` so a panicking adapter becomes
`ErrModelContract`. Per event: `ModelTextDelta` is emitted as
`TextDelta` and appended to a builder; `ModelReasoningDelta` is emitted
as `ReasoningDelta` and appended to the open reasoning block, and a
non-empty `Signature` closes that block; `ModelToolCall` is validated
(empty id or name, or a repeated id within the step, is a contract
violation) and appended to `calls`;
`ModelToolCallDelta` is emitted as `ToolArgsDelta` and otherwise
ignored; `ModelFinish` is recorded and sets `finished`; anything else
is a contract violation. An event after the finish, a stream error, or
a stream that ends without a finish all return an error, and `execute`
fails the run at this step. Nothing from a failed model turn is
appended to the transcript: `res.Messages` still ends at the previous
step's tool message, which is why `RunError.Result` is always valid
input for a retry.

**7. The assistant message.** Built in production order: one
`ReasoningPart` per block that has text or a signature, then one
`TextPart` if any text arrived, then each `ToolCallPart`. If the
message has no content at all it is not appended (the empty-turn rule;
a signed but textless reasoning block is content).

**8. The tool decision.** A `StepRecord` is opened with the index,
mapped and raw stop reasons, usage, text and calls. Then a
three-way switch: no calls, nothing to do; a `StopMaxTokens` finish
with calls, every call gets the pinned "was not executed" error result
without running; otherwise `execTools(ctx, runID, step, calls, seq,
emit, approved=false)` runs them.

**9. `execTools`: dispatch in call order.** Allocates `outcomes` and
`parked` arrays indexed by call position, a semaphore channel of
capacity `Parallelism` (default 4), and `ordered`, which takes `emitMu`,
increments `seq`, and emits in one critical section. For each call, in
order: resolve the tool; if it is `Sequential`, wait for every in-flight
call (`wg.Wait`, safe because `wg.Add` only happens on this goroutine);
check `ctx.Err()` explicitly, then `select` on the semaphore and
`ctx.Done()`. A call that cannot get a slot because the run was
canceled gets the result "run canceled before the tool started" and no
events. Otherwise `ToolStart{Seq, CallID, Name, Args}` is emitted here,
on the dispatching goroutine, which is what makes start order equal
call order by construction. A goroutine is spawned that decorates the
ctx with `Call{RunID, Step, CallID, Name, Approved}` (readable through
`CallFromContext`), runs `callTool`, and, unless the call parked, emits
`ToolFinish{Seq, CallID, Name, Content, IsError}` through `ordered`.
A `Sequential` call then waits again so it ran alone. After the loop,
`wg.Wait`, then results are assembled from `outcomes` in index order,
parked entries going to `pending` instead.

**10. `callTool`: policy and containment.** Resolves the definition
again (a `ToolSource` may have changed it), computes the effective
policy (result cap, timeout and strictness; per-tool settings override
the agent's, and a per-tool `Timeout(0)` or `MaxResultBytes(0)` lifts
the agent default), and defers `capResult` so the cap applies to every
outcome including errors and panics. It builds the chain and runs it
through `invokeContained` (a deferred `recover` turns any panic in the
handler or in middleware into "tool X panicked") or, when a timeout is
set, `invokeWithTimeout`, which runs the contained call on its own
goroutine under a `WithTimeout` ctx and selects on completion versus
the deadline. At the deadline the run's own ctx error wins if it is
set; otherwise the result is "tool X timed out after D" and the
handler goroutine is abandoned. A handler that returns
`context.DeadlineExceeded` itself gets the same named timeout text.
Then the error is classified: one wrapping `ErrApprovalRequired` makes
the call pending; any other error becomes `IsError: true` with
`err.Error()` as the content; success stores the output.

**11. `chain`: the tool call from the outside in.** Agent `WrapTools`
middleware, first listed outermost, wraps the tool's own `WrapTools`,
which wraps `base`. `base` resolves nothing new: it returns
`NO_SUCH_TOOL` for a nil definition (the chain still ran, so `Allow`
and `Audit` saw the attempt), returns `ErrApprovalRequired` when the
tool requires approval and `Call.Approved` is false, and otherwise
calls `def.invoke`, which decodes `Args` into the tool's `In` (lenient
by default, `StrictInput` rejects unknown fields, both naming the
field on failure), runs the handler, and renders the output: a string
verbatim, anything else as JSON, a `*ToolError` as `CODE: message`.

**12. Close the step.** If there are results, one `tool`-role message
carrying all of them in call order is appended. The `StepRecord` is
appended, `res.StopReason` is set to this step's reason, usage is
added, and `StepFinish{Index, Reason, Usage, Raw}` is emitted from the
loop goroutine.

**13. The exits, checked in this order.** Pending calls: set
`res.Pending`, emit `RunFinish{Usage, Steps, Pending}`, and return
success unless the ctx is done, in which case return the ctx error
with the pending calls riding on `RunError.Result`. No calls: emit
`RunFinish` and return success (a `max_tokens` finish on text is
recorded on `res.StopReason`, not fatal). A `StopWhen` condition true
over `res.Steps`: emit `RunFinish` and return success. Otherwise the
next step.

**14. After the loop.** If the ctx is done, the run failed at
`maxSteps-1` with the ctx error: the cause wins over the budget.
Otherwise it failed at `maxSteps` with `ErrMaxSteps`, and the
transcript, including the final step's tool results, is on the error.

### Every exit, and what the caller holds

| Exit | What `Generate` returns | `Messages` ends with | Last event on the stream |
|---|---|---|---|
| Model stopped without tools | `(res, nil)` | the final assistant message | `RunFinish` |
| `StopWhen` fired | `(res, nil)` | the step's tool message | `RunFinish` |
| Pending approval | `(res, nil)`, `res.Pending` set | a tool message missing the parked calls, or no tool message if every call parked | `RunFinish{Pending}` |
| Model stream error or contract violation | `(nil, *RunError{Step, Err})`, `Result` set | the previous step's tool message; nothing from the failed turn | the last delta emitted before the failure, then the error from `Events` |
| Cancellation | `(nil, *RunError{Step, ctx.Err()})`, `Result` set, `Pending` set if calls were parked | whatever was appended before the check that failed | nothing after cancellation; the error from `Events` |
| Budget | `(nil, *RunError{Step: MaxSteps, ErrMaxSteps})`, `Result` set | the last allowed step's tool message | `StepFinish` |

Two invariants fall out of the table. `RunError.Result.Messages` is
always valid provider input, because no half-appended turn exists.
And every successful run's last event is `RunFinish`, so a consumer
can treat "saw `RunFinish`" as "result available".

### Goroutines and locks

| Who | Runs what | Emits |
|---|---|---|
| The consumer goroutine | ranges `Run.Events()` | nothing; receives on the channel |
| The loop goroutine | `execute`, `consume`, the dispatch loop of `execTools`, `resolvePending` | `RunStart`, `StepStart`, `TextDelta`, `ReasoningDelta`, `ToolArgsDelta`, `ToolStart`, `StepFinish`, `RunFinish` |
| One goroutine per tool call | `callTool` and the chain | `ToolFinish` (through `ordered`) |
| One goroutine per timed-out handler | the abandoned handler, until it notices ctx | nothing that is delivered |

Three synchronisation points, no more: `emitMu` orders concurrent tool
events; the semaphore channel bounds fan-out; `Run.mu` protects the
started flag and the stored result. Taps run on the emitting goroutine,
so a tap observing a `ToolFinish` is running on that tool's goroutine
under `emitMu`, which is why taps must be fast. `Generate` has no
consumer goroutine at all; its sink is a no-op and `execute` runs on
the caller's goroutine.

### The resume path

`resolvePending` takes the unresolved calls of the last assistant
message and the run's decisions. Approved calls, in their original
order, go through `execTools` with `step = 0` and `approved = true`;
the base of the chain sees `Call.Approved` and lets a `RequireApproval`
tool run. Each denied or undecided call becomes an error result
rendered by `deniedResult`: `DENIED: <reason>`, or `DENIED: no decision`
when the caller gave neither `Approve` nor `Deny`. Results are then
reassembled in the original call order, so the tool message reads as
if the step had run normally. A call the chain parks again (middleware
can still refuse) is returned as pending. There is no `StepRecord` for
resumed calls and their step index reads as zero; audit lines key on
the call id.

### What the model sees, step after step

The request the loop sends changes in exactly one place between steps
of the same run: `Messages` grows by one assistant turn and one tool
turn. `System` is deterministic given the instructions and the
advertised tools; `Tools` is the static list unless a `ToolSource`
changes it; `Thinking` and `SequentialTools` are fixed for the run.
That stability is not an accident, and it has a price tag: provider
prompt caches key on a byte-identical prefix in the order tools, then
system, then messages. Anthropic's documentation lists a changed tool
definition as invalidating every cache level, a changed thinking
configuration as invalidating the message cache, and a default cache
lifetime of five minutes measured from the start of the request, with
cache reads billed at a tenth of the input price. A loop that rebuilds
its tool list or rewrites its system text per step pays full price
every step. In weft, `ToolSource` and the planned `PrepareStep` are
therefore the two knobs that can break the prefix, compaction is a
third (Chapter 21), and the adapters set the cache markers; the loop
itself never reorders what it sends.

---

# Part II — The eleven questions

Each chapter has the same shape: the problem, the options, what the
studied frameworks do, what weft does with code, the edge cases, and
the tests that pin the rule.

## 4. Stopping: the intended end versus the safety budget

### The problem

The forty-line loop ends in two ways: the model stops requesting tools,
or the counter hits ten. The first is the normal end. The second is
either a bug (the model is looping) or a legitimate long task. If the
loop treats both as success, a runaway loop returns a plausible-looking
transcript and nobody notices until the bill arrives. If it treats
both as failure, a long task that needed twelve steps is a failure for
no reason.

The real design question is therefore: **separate the intended end from
the budget, and make the budget a failure.**

### The options

1. **One integer.** `maxSteps = 10`; reaching it is success. Simple, and
   the runaway-looks-like-success trap.
2. **Composable predicates.** Stop conditions are functions over the
   steps so far; several combine with OR. Expressive, and the caller
   must remember to include a count.
3. **Budget as a request count.** No step knob; the usage limit is the
   guard.
4. **No default at all.** The loop runs until the model stops.

### What the field does

- **Vercel AI SDK** invented option 2 as the mainstream shape. A
  `StopCondition` is `({steps}) => boolean`; built-ins `isStepCount(n)`
  (default 20 on agents, 1 on raw `generateText`), `hasToolCall(name)`,
  and a custom `stopOnHighCost` summing output tokens [ai-sdk §4.2]. The
  rename `maxSteps → stopWhen` was one of the breaking changes users
  remember [ai-sdk §7].
- **Pydantic AI** has no step knob at all: "the step limit *is* the
  request budget", `UsageLimits(request_limit=50)` checked before each
  request [pydantic-ai §4.2]. The output phase adds `end_strategy`
  (`early`, `graceful` default in v2, `exhaustive`) deciding whether
  sibling tools still run once an output tool succeeded.
- **LangGraph** counts supersteps, not model calls: `recursion_limit=25`
  raises `GraphRecursionError`. An agent round costs two supersteps, so
  25 means about twelve rounds, which is the single most common surprise
  in the community [langgraph §4.2, §8]. The prebuilt agent adds a
  `remaining_steps < 2` guard that returns "Sorry, need more steps" as a
  message instead of crashing.
- **Mastra** has `stopWhen` predicates plus `maxSteps`, and **no default**:
  "unbounded loops are possible unless you pass one" [mastra §4.2, §5].
- **pi** also has no step cap; it relies on stop reasons and a
  `shouldStopAfterTurn` hook [pi §5].
- **Crush** gets its loop from the `fantasy` library and adds
  termination as `StopWhen` conditions on top: auto-summarisation and
  loop detection [crush §5.3].
- **DeerFlow** clamps the client's `recursion_limit` server-side
  (default 100, hard ceiling 1000) and adds a goal loop with at most 8
  continuations and 2 no-progress continuations [deer-flow §1].

Beyond the seven studied frameworks (Chapter 17 has the full set):

- **OpenAI Agents SDK**: `max_turns` defaults to 10 and exceeding it
  raises `MaxTurnsExceeded`; `max_turns=None` disables the limit. The
  budget is a failure, weft's side of the split.
- **Claude Agent SDK**: no limit by default. `max_turns` counts
  tool-use turns only and `max_budget_usd` caps spend, subagents
  included; hitting either ends the loop with a `ResultMessage` whose
  `subtype` is `error_max_turns` or `error_max_budget_usd`, still
  carrying usage and the session id so the caller can resume with a
  higher limit. A typed terminal result instead of an exception, and
  the same "partial work is never lost" stance as `RunError.Result`.
- **CrewAI**: `max_iter` defaults to 20, after which the agent must
  give its best answer; `max_execution_time` and `max_rpm` sit beside
  it.
- **Google ADK**: the model-driven agent has no step knob in the loop
  itself; bounded iteration is a *workflow* agent, `LoopAgent`
  with `max_iterations`, ended early when a sub-agent sets
  `escalate` (the `exit_loop` tool pattern). The stop condition is a
  tool call, which is weft's `HasToolCall`.
- **Genkit**: a tool can *interrupt* generation; the caller sees
  `finishReason: "interrupted"` and resumes with `respond` or `restart`.
  Stopping on a tool call is again the primitive.

### What weft does

Two separate concepts with two separate names, and the budget is a
failure:

```go
agt := weft.New(model,
	weft.StopWhen(weft.HasToolCall("submit")), // intended end: success
	weft.MaxSteps(20),                          // safety budget: ErrMaxSteps (default 10)
)
```

`StopCondition` is an interface with `Stop(steps []StepRecord) bool`,
adapted from a plain function by `StopFunc`; built-ins `HasToolCall`
and `StepCountIs` also implement `fmt.Stringer` so the manifest can name
them. `Output[T]` registers its own condition, `output_submitted`, which
fires only on a *non-error* `submit_output` result, so an invalid
submission keeps the loop going for repair (`output.go`).

The budget check is the loop header. When the model still wants tools
after the last allowed step, the loop returns
`&RunError{Step: maxSteps, Err: ErrMaxSteps, Result: res}` with the full
transcript, including the last step's tool results, on the error. One
subtlety from `loop.go`: cancellation during the last step's tools is
reported as cancellation, not as budget exhaustion. **The cause wins
over the budget.**

### Structured output: the intended end as a tool call

`Output[T]` (ADR 0008) is a fourth way a run ends, and it is built
from parts this chapter already named. `weft.Output[Summary]()` on an
agent registers one more tool, `submit_output`, whose input schema is
reflected from `Summary` exactly as any tool's is. It also registers a
stop condition, `output_submitted`, that fires only on a *non-error*
`submit_output` result: an invalid submission is an ordinary tool
error, so the repair loop is the loop. The tool's description and its
result text (`"recorded"`) are pinned model-visible contract.

```go
var out Summary
out, res, err := weft.GenerateAs[Summary](ctx, agt, weft.Prompt("..."))
// streamed runs: weft.OutputOf[Summary](res); no valid submission: ErrNoOutput
```

Why a tool and not JSON mode? A tool works on every provider without an
adapter flag, the model's failures to conform come back as tool errors
it can correct, and the answer lands in the transcript where replay
and audit already look. Native JSON mode remains a later adapter
optimisation, invisible to this API.

### Edge cases

- **`max_tokens` on a text-only reply.** Not an error. `RunResult.StopReason`
  is `StopMaxTokens` and the run succeeds; the caller decides what
  truncated text means (rule 8, "truncation is visible, never silent").
- **`max_tokens` with tool calls.** Chapter 5.
- **A stop condition that needs a tool result.** Conditions see
  `StepRecord.Results`, so `HasToolCall` fires on the step that issued
  the call and a custom condition can wait for the result. Resumed
  approval calls have no `StepRecord`, so a stop keyed on an
  approval-gated tool fires on the issuing step or not at all (ADR 0007
  consequences).
- **Zero-step runs.** A run that resolves pending approvals and finds
  more pending calls ends with `Steps: 0`.

### Tests that pin it

`TestMaxStepsExceeded`, `TestStopWhenHasToolCall`,
`TestStopWhenStepCountIsSucceedsWhereMaxStepsFails` and
`TestCancellationBeatsMaxSteps` in `contract_test.go`; the playbook's
decision table row "`StopWhen(...)` = successful intended end;
`MaxSteps` (10) = failure budget".

## 5. Tool failure: data the model sees, or an error the caller sees

### The problem

Four tools run. One throws. What happens to the other three, to the
run, and to the model's view of the world?

`errgroup` semantics, first error cancels everything, are the wrong
answer for an agent loop. The model asked for four things; three of
them succeeded; the model can recover from the fourth if it is told
what went wrong. Cancelling the siblings throws away work and hides the
recovery path. Turning the error into a run failure throws away the
whole run.

### The rule

**A tool error is data the model sees. A run error is a Go error the
caller sees.** Everything else in this chapter is a consequence.

### What the field does

- **Vercel AI SDK**: `InvalidToolInputError` becomes an error tool
  result and the loop continues; `NoSuchToolError` warns the model and
  continues; only `ToolChoiceViolationError` fails the step [ai-sdk §4.11].
- **Pydantic AI**: tool results are re-validated; a `ValidationError` or
  a raised `ModelRetry` becomes a `RetryPromptPart` back to the model,
  "the tool failed, tell the model why" as the default error mode.
  Per-tool `max_retries` budgets reset on success [pydantic-ai §4.3, §4.2].
  `exceptions.py` separates *raised-to-steer values* (`ModelRetry`,
  `ToolFailed`, `CallDeferred`, `ApprovalRequired`) from *real errors*
  (`UsageLimitExceeded`, `ModelHTTPError`) [pydantic-ai §4.11].
- **LangGraph**: `ToolNode(handle_tool_errors=…)` converts failures into
  error `ToolMessage`s by default, except `GraphInterrupt`, which
  re-throws. But inside a superstep the first non-interrupt error
  cancels in-flight siblings [langgraph §4.3, §4.2], the `errgroup`
  shape this chapter warns about.
- **pi**: "Tool errors become `isError` results (data), never run
  errors" [pi §5]. Mid-stream provider errors become an assistant
  message with `stopReason: "error"`, never a thrown exception.
- **Crush**: a permission denial is a terminal tool result with
  `StopTurn = true`, "so the agent loop does not retry" [crush §6.1].
- **Mastra** does not state how a thrown tool error reaches the model
  [mastra §2]; the doc records only processor retries.

Beyond the seven:

- **OpenAI Agents SDK**: a function tool's `failure_error_function`
  defaults to one that "tells the LLM an error occurred"; pass `None`
  to re-raise instead. But an *unknown* tool name is different:
  `tool_not_found_behavior` defaults to `"raise_error"`, which raises
  `ModelBehaviorError` and ends the run, with
  `"return_error_to_model"` as the opt-in. weft's `NO_SUCH_TOOL` result
  is the opt-in made the default.
- **Claude Agent SDK**: a denied tool call is not an exception either;
  "Claude receives a rejection message as the tool result", and a
  `PostToolUseFailure` hook observes failures. Same rule.
- **MCP**: two channels by specification. Protocol errors (unknown
  tool, invalid arguments) are JSON-RPC errors; execution errors are
  results with `isError: true`. weft has the same split, placed at a
  different boundary: inside the loop both kinds are data, and
  `Agent.CallTool`, the Go-caller path, returns the protocol kind as
  Go errors (`ErrNoSuchTool`, `ErrInvalidToolInput`).
- **Google ADK** (Go): the documented pattern returns an error message
  inside the response struct rather than failing the call.

### What weft does

```go
// A handler error, a panic, an unknown tool, undecodable arguments,
// a timeout: all of these become
ToolResultPart{CallID: c.ID, Name: c.Name, IsError: true, Content: "..."}
// Siblings keep running. Only these return a *RunError:
//   model stream failure, ctx cancellation, ErrMaxSteps
```

The funnel is `callTool` in `loop.go`: it builds the chain, runs it
inside the containment boundary (`invokeContained` recovers panics,
`invokeWithTimeout` abandons hung handlers), and folds every failure into
the result. The error's text is what the model reads.

Structured errors (TODO §5.2a, ADR 0002 amendment) give the model a
code to branch on and keep the cause out of the transcript:

```go
return "", &weft.ToolError{
	Code:    "ORDER_NOT_FOUND",            // stable, SCREAMING_SNAKE
	Message: "order 42 does not exist",    // what the model reads
	Err:     err,                          // for middleware and logs only
}
// The model sees:  ORDER_NOT_FOUND: order 42 does not exist
```

The loop's own failures use the same vocabulary: `INVALID_INPUT: tool
"x": field "days": expected integer, got string`, `NO_SUCH_TOOL: no tool
named "x"`, `DENIED: <reason>`. `mw.MapErrors(nil)` codes every plain
error as `INTERNAL: tool "x" failed` so production transcripts leak
nothing. These strings are contract, pinned by tests, and changing them
needs an ADR (rule 5).

**Truncated calls.** A `max_tokens` finish that also contains tool calls
executes none of them. An intact-looking call may be the first of
several the model never finished issuing, so every call gets `tool call
X was not executed: the response hit the output token limit` and the
model retries with a full budget (ADR 0002, rule 11). pi does exactly
this with `failToolCallsFromTruncatedMessage` [pi §5]; the two designs
converged independently.

**ModelRetry** (TODO §5.2, not yet implemented) will be a `*ToolError`
with `Code: "RETRY"`, counted per tool name per run; exceeding
`MaxModelRetries(3)` fails the run with `ErrModelRetriesExceeded`. A
model that cannot self-correct is a run failure, not an infinite loop.

### Decoding: where the model's arguments become Go values

The base of the tool chain decodes `call.Args` into the tool's `In`
before the handler runs. The schema is derived once at construction:
`json` tags name properties, `jsonschema` tags describe them, a
pointer or `omitempty` marks a field optional, embedded structs
flatten, `time.Time` arrives as a date-time string, `[]byte` as a
string. The vocabulary of a decode failure is contract, stated in the
schema's own terms: `INVALID_INPUT: tool "weather": field "days":
expected integer, got string`, `invalid JSON at offset N: …`, and,
under `StrictInput`, `unknown field "units": not in the schema`.
Lenient decoding is the default and `StrictInput()` is the opt-in; the
field-naming form is pinned by `TestDecodeErrorsNameTheField`, because
it is the string the model reads to correct itself.

Two escape hatches sit beside the struct path. `RawTool` defines a
tool by an explicit schema and a `json.RawMessage` handler: no struct,
no decode step, no `ErrInvalidToolInput`, the arguments passed through
verbatim. That is the shape for imported tools (MCP, on the deferred
list in Chapter 13). `ToolSource` replaces the advertised set with a
function's return value, fetched fresh at each step and at each
dispatch, so a registry can mount a tool mid-run; the manifest still
describes the static construction-time set, because it describes the
code, not the registry.

`Agent.CallTool` is the manual-dispatch form of the same chain, for
dispatchers outside the loop, and its differences are deliberate: no
containment (a panicking handler propagates), no result cap, no
timeout, no run policy, `RequireApproval` surfaces as
`ErrApprovalRequired`, and an unknown name returns an error wrapping
`ErrNoSuchTool` instead of an error result. It is a Go function for Go
callers; the funnel above is for the model.

### Edge cases

- **A tool that panics inside middleware.** Still a tool error:
  containment sits *outside* the chain, so no middleware can turn a
  panic into a run failure (ADR 0006).
- **A tool that honours its deadline.** It returns
  `context.DeadlineExceeded` itself; `invokeWithTimeout` names the
  timeout ("tool X timed out after 5s") rather than echoing the context
  error, unless the *run's* ctx was the one that expired, in which case
  the run is failing anyway.
- **The result is enormous.** Capped at 64 KiB by default on a rune
  boundary with a visible `…[truncated N bytes]` marker, N = bytes the
  model did not receive (rule 8). pi
  puts the same rule on tool authors: 50 KB and 2000 lines, the rest to
  a file [pi §11].
- **A denied call.** `DENIED: tool "x" is not allowed` from `mw.Allow`,
  `DENIED: <reason>` from a human. To the model a refusal is a refusal,
  whoever refused.

### Tests that pin it

`TestToolErrorIsDataTheModelSees`, `TestToolErrorRendering`,
`TestPanickingToolIsContained`, `TestWrapToolsDenialAndPanicAreToolErrors`, `TestMaxTokensWithToolCallsFailsThemWithoutExecuting`,
the pinned-bytes tests for `INVALID_INPUT`/`NO_SUCH_TOOL`/`DENIED`, and
`TestToolTimeout`, `TestTimeoutReportsCancellationAsSuch`.

## 6. Concurrency: what runs at once, in what order, and who cancels whom

### The problem

The model asks for four tools in one step. The framework may run them
one at a time (slow, but deterministic and safe for shared state) or all
at once (fast, and now results, events, and failures interleave). Then a
tool hangs. Then the caller cancels. Every one of those needs a defined
rule, and THE-END-GOAL calls the event-ordering part "the genuinely hard
part".

### The three separate orderings

Most confusion in this area comes from conflating three things:

1. **Start order.** When does each tool begin?
2. **Result order.** In what order do results land on the transcript?
3. **Event order.** In what order does the caller observe progress?

They can be decided independently, and weft does.

### What the field does

- **Pydantic AI** is the most deliberate: parallel by default, per-tool
  `sequential=True` as a barrier, run-scoped `parallel_execution_mode`,
  and a third mode `parallel_ordered_events` that pins which sibling's
  exception propagates so durable replay is deterministic. Results are
  appended in emission order. And it has no concurrency bound at all:
  issue #6884, "one turn fanned out 792 concurrent executions"
  [pydantic-ai §2, §8].
- **Mastra**: parallel by default bounded at 10, and **automatically
  sequential when any tool requires approval or can suspend**
  [mastra §4.2]. The deep dive calls that rule "worth adopting verbatim".
- **LangGraph**: each tool call becomes a `Send`, executed on a thread
  pool or under an asyncio semaphore; the first non-interrupt error
  cancels siblings [langgraph §4.2].
- **pi**: sequential if any called tool declares `executionMode:
  "sequential"`; otherwise `Promise.all`, with `tool_execution_start`
  emitted in call order and results assembled in call order [pi §5].
  Independent convergence with weft's ADR 0004.
- **Vercel AI SDK**: v7 adds per-scope timeouts (`totalMs`, `stepMs`,
  `chunkMs`, `toolMs`) [ai-sdk §4.2]; the doc does not state the
  parallel policy. Cancellation is its persistent weak spot: usage after
  abort, abort not detected through stream wrappers [ai-sdk §4.11].
- **Crush**: idle-versus-hard timeouts ("the model stopped sending data
  for 30s" versus "did not respond within 5m") so a slow but streaming
  response is never killed; a `context.WithoutCancel` flush with a 5s
  budget on cancellation; tombstone assistant messages so "cancellation
  is a recorded outcome, not an absence" [crush §5.3, §6].

Beyond the seven:

- **Claude Agent SDK / Claude Code**: parallelism is decided *per
  tool from its annotations*. Read-only tools (`Read`, `Glob`, `Grep`,
  MCP tools marked read-only) run concurrently; state-modifying tools
  (`Edit`, `Write`, `Bash`) run sequentially; a custom tool is
  sequential unless it declares MCP's `readOnlyHint`. That is weft's
  per-tool `Sequential()` with the default inverted: safe-by-default
  for unknown tools, parallel on declaration. Both are one flag per
  tool; the difference is which way the burden of proof points.
- **OpenAI Agents SDK**: `ModelSettings.parallel_tool_calls` is the
  provider hint (weft's `SequentialTools`), and
  `ToolExecutionConfig.max_function_tool_concurrency` bounds SDK-side
  execution, defaulting to `None`, which starts every emitted call at
  once: the unbounded default weft declined.
- **Microsoft Agent Framework**: graph workflows run in supersteps
  with parallel edge groups and fan-in, the LangGraph shape; inside
  one agent the tool loop is the framework's own.

### What weft does

Start order is call order, always. Result order is call order, always.
Event order is `Seq` order, always. The mechanism is `execTools` in
`loop.go`:

```go
sem := make(chan struct{}, a.parallelism)      // bounded fan-out, default 4
for i, call := range calls {
	if def.sequential { wg.Wait() }             // barrier: let in-flight calls finish
	select {                                    // slot acquired HERE, on the dispatcher,
	case sem <- struct{}{}:                     // in call order — that is what makes
	case <-ctx.Done(): /* error result, no events */ // "start in call order" true
	}
	ordered(func(s int64) Event { return ToolStart{Seq: s, ...} })
	go func() {
		defer func() { <-sem }()
		outcomes[i], parked[i] = a.callTool(callCtx, call)
		ordered(func(s int64) Event { return ToolFinish{Seq: s, ...} })
	}()
	if def.sequential { wg.Wait() }             // run alone, then resume
}
wg.Wait()
// results assembled from outcomes[] in index order → call order
```

The v0 bug that ADR 0004 records is instructive: a goroutine-per-call
race for the semaphore does *not* start tools in call order, and
`Sequential()` did not mean one at a time. Acquiring the slot on the
dispatching goroutine before spawning fixed both.

`ordered` assigns `Seq` and emits under one mutex. Without that lock two
goroutines could assign 5 and 6 but emit 6 before 5, an observed order
that contradicts the numbers. Chapter 8 builds on this.

Cancellation rules, all in `loop.go` and pinned:

- No tool starts after the run's ctx is canceled; a call that never got
  a slot gets the result "run canceled before the tool started" and
  **no events**, so every `ToolFinish` is preceded by its `ToolStart`.
- A failing tool never cancels its siblings. Only ctx does.
- No event is delivered after cancellation, to taps or sinks. The
  partial transcript on `RunError.Result` is the truth. Crush's flush
  was considered and rejected: it would require events after the
  terminal error the stream promises at most once (ADR 0004 amendment).
- Cancellation wins over `ErrMaxSteps`.

Timeouts are per tool call, not per run: `weft.Timeout(d)` on a tool or
as the agent default. The run's timeout is the ctx, as in every Go
library; a per-tool option exists only because a tool call is smaller
than a run (playbook §9). A hung handler's goroutine is abandoned, so
handlers must honour ctx to release resources (rule 9).

Sequential also flows *upstream*: `ModelRequest.SequentialTools` tells
the adapter to ask the provider not to emit parallel batches
(`parallel_tool_calls: false` / `disable_parallel_tool_use`), so the
loop is not serialising calls the model was told it could batch.

### Edge cases

- **A `Sequential` tool in the middle of a parallel batch.** Calls 1 and
  2 run together; call 3 (the barrier) waits for both, runs alone; calls
  4 and 5 run together after it. Results still land in order 1..5.
- **Parallelism(1) versus a barrier.** The agent-level setting
  serialises everything and sets the provider hint; the per-tool option
  serialises only that tool.
- **Mastra's auto-sequential rule.** weft does not adopt it in the loop:
  a `RequireApproval` tool parks its call while siblings run, and the
  run ends after the step (Chapter 10). Whether that is safe depends on
  the siblings, which is a policy for `mw.Allow` or a `Sequential()`
  barrier on the gated tool, both one line.
- **The abandoned handler keeps writing.** It writes into a result that
  was already recorded. The loop never reads `outcomes[i]` again after
  the timeout, so the late write is lost, not corrupting. Handlers that
  share state must check ctx before their side effects.

### Tests that pin it

`TestStreamEventOrdering`, `TestToolStartsInCallOrder`,
`TestSequentialRunsInCallOrder`, `TestSequentialToolIsABarrier`,
`TestCanceledBeforeStartEmitsNoOrphanEvents`, `TestContextCancellationAbortsRun`, `TestToolTimeout`, and
the `-race` flag on every test run.

## 7. Transcript integrity: always valid input

### The problem

A process crashes between the assistant message that requested a tool
and the tool message that answers it. The transcript is saved with a
dangling call. Every later request to the provider fails validation.
Crush: "An orphaned result causes API validation to fail on every
subsequent turn, permanently locking the session" [crush §5.3].

Related: a model that returns an empty turn (some providers reject
empty content on the way back in); a tool message that lost its
assistant message; two tool messages in a row.

### What the field does

- **Crush** drops orphaned results and synthesises results for orphaned
  calls [crush §5.3].
- **DeerFlow** has `DanglingToolCallMiddleware` that "patches missing
  ToolMessages before model sees the history" [deer-flow §5.6].
- **pi** synthesises an "interrupted" result entry for a crash mid-tool
  when the tool is `replay: "never"` [pi §8].
- **LangGraph** avoids the problem structurally: state is checkpointed
  per superstep, and an interrupted node re-executes from the top on
  resume (with the documented trap that side effects before the
  interrupt re-run) [langgraph §4.8].

### What weft does

`weft.Repair` (ADR 0001), applied by the loop to the input of every run
and pure and idempotent:

- A tool message is matched against the assistant message immediately
  before it; only the first tool message after that assistant is kept.
- A result survives only if its `CallID` names an unserved call of that
  assistant; first result per id wins.
- Calls still missing a result get a synthesised, **visible** error
  result: `no result recorded: the call was interrupted`.
- Drops carry no marker; synthesis does. Repair is visible in the
  transcript, never hidden.

Two loop rules complete the picture: an assistant turn with no
reasoning, no text and no calls is never appended; and a step's tool
results are batched on one `tool`-role message (the Anthropic-shaped
internal model; adapters fan out for providers that want one message
per result).

Approval is the one exception to repair: when a run carries `Approve`
or `Deny` decisions, the unresolved calls of the last assistant message
are exempt, because this run resolves them itself (Chapter 10).

### Edge cases

- **Reasoning-only turns.** A signed reasoning block the provider
  expects back is content; it is appended even with no text, placed
  before the text in production order (`loop.go`).
- **A transcript fed back without decisions.** Pending calls are
  repaired as interrupted, not denied. The model sees "no result
  recorded", which is the truth.
- **Compaction.** Not in the core yet (TODO §14); the reference
  algorithm is specified from pi's scars. Trigger on provider-reported
  usage, `contextTokens > contextWindow - reserve` (pi: reserve
  16,384, keepRecent 20,000), checked after tools finish and before
  each new prompt, never on a constant chars-per-token estimate, which
  is how pi wedged sessions and once ran a ~400k-token destructive
  compaction (#9409). Cut only at user/assistant boundaries, never at
  a tool result; summarise into a fixed skeleton (Goal, Constraints,
  Progress, Key Decisions, Next Steps, Critical Context); re-run the
  aborted turn after overflow recovery. A signed `ReasoningPart` that
  predates the cut must be dropped or regenerated, or the next request
  fails Anthropic's prefix check (#9391); weft's `Signature` field has
  the same hazard by design. Crush's destructive-only summarisation is
  the anti-pattern [crush §12].

### Tests that pin it

`TestRepairSynthesisesMissingResults`, `TestRepairDropsOrphanResults`, `TestRepairIsPureAndIdempotent`, `FuzzRepair`,
`TestEmptyAssistantTurnIsNotRecorded`, `TestSignedEmptyReasoningIsKept`, and `ExampleRepair`.

## 8. Streaming and event ordering: replayable progress

### The problem

The caller wants to render progress while four tools run. A recorder
wants to store the stream and replay it later, identically. A tracing
backend wants spans. All three need the same thing: **a total order on
events that matches what was observed, with enough identity to pair
starts with finishes.**

### What the field does

- **Vercel AI SDK** defines a 28-type stream chunk grammar; steps are
  framed `start-step … finish-step` inside `start … finish|error|abort`;
  `reset-step` exists for retries that restart a step [ai-sdk §4.5].
  Its wire-format coupling is also what made four majors breaking.
- **LangGraph** has been through three streaming generations; subgraph
  events surface with `subgraphs=True`, namespace-prefixed
  [langgraph §4.5, §7].
- **Mastra** emits `step-start/finish` and `abort`/`error` chunks
  [mastra §4.5].
- **pi** splits a tool result into `content` (for the model) and
  `details` (for the UI), and streams partial progress through
  `onUpdate` [pi §5].

### What weft does

`Run.Events()` is `iter.Seq2[Event, error]`: events in emission order,
the error delivered at most once as the final element, `RunFinish` the
last event of a successful run. Breaking out of the range cancels the
run. `Wait()` runs the agent itself if nobody consumed the events, so
there is no deadlock footgun.

```go
for ev, err := range agt.Stream(ctx, weft.Prompt("...")).Events() {
	if err != nil { return err }
	switch ev := ev.(type) {
	case weft.ToolStart:  // Seq, CallID, Name, Args
	case weft.ToolFinish: // Seq, CallID, Name, Content, IsError
	case weft.RunFinish:  // Usage, Steps, Pending
	}
}
```

The ordering rule (ADR 0004): tool events carry a per-run `Seq` assigned
and emitted under one lock, so observed order equals `Seq` order.
Step-scoped events (`RunStart`, `StepStart`, `TextDelta`,
`ReasoningDelta`, `ToolArgsDelta`, `StepFinish`, `RunFinish`) are
emitted from the single loop goroutine and need no `Seq`. Consumers pair
tool events by `CallID`.

Every event marshals with a `type` discriminator (`tool_start`,
`run_finish`, …) and `weft.UnmarshalEvent` restores it; an unknown type
is an error, never a silent drop, because a recorded stream must replay
exactly what was emitted. The bytes are pinned by
`TestEventJSONRoundTrip` and fuzzed by `FuzzUnmarshalEvent`. The event
set is sealed (`isEvent()`), so switches stay exhaustively lintable and
new types are additive.

Observation without a stream: `weft.Tap(fn)` sees every event of every
run, including `Generate`, synchronously, in emission order, inside the
same `emit` wrapper as the sink. A tap runs under the ordering lock, so
a slow tap delays every tool event of its step. A panicking tap is
recovered and dropped. Taps see; seams change (Chapter 9).

### Edge cases

- **Nothing after cancellation.** The `emit` wrapper checks `ctx.Err()`
  first; taps and sinks agree. A UI that must render every started tool
  derives tombstones from `RunError.Result`, not from the stream.
- **A parked call.** Has a `ToolStart` and no `ToolFinish`; it is listed
  on `RunFinish.Pending`. The one documented exception to "every start
  has a finish" (ADR 0004 amendment, 2026-09-14).
- **Argument progress.** `ToolArgsDelta` streams fragments while the
  model is still writing a call; the assembled call still arrives as
  `ToolStart` when it executes.
- **Nested streams.** Chapter 11 adds `Nested{Seq, CallID, Event}`: a
  child run's events wrapped and numbered from the parent's counter.

## 9. Extension: two seams, one tap, no more

### The problem

Every feature in the harness layer wants to run "before the model call"
or "around the tool call": logging, retries, fallbacks, permission
checks, approvals, audit, sandboxing, JSON repair, prompt caching. The
naive answer is a lifecycle hook per phase: `OnStep`, `OnToolStart`,
`OnToolFinish`, `OnText`, `OnBeforeHandle`, `OnAfterHandle`. Each hook is
a contract you can never remove, and hooks that can *change* behaviour
turn the loop into a callback soup where order of registration
determines semantics.

### What the field does

- **Crush** wires `fantasy.NewAgent` with `PrepareStep`, `OnTextDelta`,
  `OnToolCall`, `OnStepFinish`, `StopWhen` callbacks and adds its own
  ~2,200-line turn lifecycle on top [crush §3.1, §4.3]. Hooks run before
  permission checks and can pre-approve; subagent tools are deliberately
  *not* hook-wrapped "to avoid firing the user's hook N times per
  delegated turn" [crush §7].
- **DeerFlow** is "~18 lead-specific middlewares from a 48-file dir",
  which the deep dive calls "the real graph" [deer-flow §3.1], on top of
  LangChain's loop it has to monkey-patch [deer-flow §12].
- **LangGraph**: `wrap_tool_call` interceptors, per-node `RetryPolicy`,
  `TimeoutPolicy`, and the graph itself as the extension mechanism.
- **Vercel AI SDK**: `prepareStep` (swap model, tools, messages per
  step), `toolApproval` policy maps, `HarnessAgent` wrapping external
  runtimes [ai-sdk §4.12].
- **pi**: extensions subscribe to events such as `tool_call` and may
  return `{block: true, reason}` [pi §6].

Beyond the seven, the hook-count census:

- **Claude Agent SDK**: about twenty lifecycle events, among them
  `PreToolUse` (which can return a `permissionDecision` of allow,
  deny or ask, and `updatedInput`), `PostToolUse`,
  `PostToolUseFailure`, `UserPromptSubmit` (which can add
  `additionalContext`), `Stop`, `SubagentStart`, `SubagentStop`,
  `PreCompact`, `PostCompact`, `SessionStart`, `SessionEnd`,
  `Notification`, `PermissionRequest`, `Elicitation`. Hooks are
  matched by tool-name patterns and run outside the model's context.
  This is the hook-per-phase design this chapter argues against, and
  it is the right design *for that product*: Claude Code's extension
  surface is shell commands in settings files, where a Go middleware
  chain is not expressible. A library for Go programs does not have
  that constraint.
- **Google ADK**: six callbacks, before and after each of agent,
  model and tool; a before-callback returning a value short-circuits.
- **Eino**: five callback aspects, `OnStart`, `OnEnd`, `OnError`, and
  stream-input and stream-output variants, applied to any node of a
  chain, graph or workflow.
- **OpenAI Agents SDK**: `RunHooks` and `AgentHooks` for observation,
  and guardrails (input, output, and per-tool) as the behavioural
  layer, which raise tripwire exceptions.
- **Microsoft Agent Framework**: middleware at three points, agent,
  function and chat client, closest to weft's two seams plus one.
- **Genkit**: middleware on the model call plus tool interrupts.

### What weft does

Exactly two behavioural seams, both chi-style, and one observation tap
(ADR 0006, rule 10):

```go
type ModelMiddleware func(next Model) Model            // around the model call
type ToolMiddleware  func(next ToolCaller) ToolCaller  // around each tool call

weft.New(model,
	weft.WrapModel(mw.Retry(), mw.Fallback(backup)),      // first listed = outermost
	weft.WrapTools(mw.Audit(logger), mw.Allow(permits), mw.MapErrors(nil)),
	weft.Tap(record),                                     // sees everything, changes nothing
)
```

The model chain is built once at `New`. The tool chain is built per
call: agent middleware, then the tool's own `WrapTools`, then the base
that resolves the name, enforces `RequireApproval`, decodes, and runs
the handler. Panic containment and timeouts sit *outside* the chain, so
no middleware can turn a panic into a run failure or defeat a deadline.

Two conventions replace the hooks people reach for
(`docs/life-of-a-call.md`):

- **Context decoration.** Middleware that verified something (a user, a
  tenant, a quota) puts it on ctx before `next`; the handler reads it
  through a typed accessor, exactly like `weft.CallFromContext`.
- **Error shaping.** Handlers return `*ToolError` when the model should
  branch on a code; `mw.MapErrors` codes everything else centrally.

`PrepareStep` (TODO §5.5, not yet implemented) will be the one
loop-level knob, running before the model seam sees the request:
rewrite system, messages, tools for this step. It is the AI SDK's
`prepareStep` and pi's `prepareNextTurn`, and it is where routing and
phased tool exposure live without a graph.

### The reference set, and the retry stance

Package `mw` ships one of each kind so the seams have worked examples,
not because the core needs them (ADR 0006). What each one decided is
worth knowing, because these are the policies users inherit by
default.

`mw.Retry` retries a model call only when it failed *before yielding
any event*. A mid-stream failure has already delivered half a turn;
re-sending it would duplicate text the consumer saw, so it surfaces as
the run error it is. Retryable by default: `ErrStreamIdle`,
`net.Error`, and HTTP 408/409/429/5xx found structurally through an
exported status-code field, so no vendor SDK type is imported;
`x-should-retry` overrides the classification. Never retryable:
context errors, the kill switch, `ErrUnsupported`, `ErrModelContract`,
other 4xx, and context-window overflow, which is not transient and
later routes to compaction. Backoff starts at 500 ms, doubles, caps
at 8 s, each delay with ±25% jitter; a provider's `retry-after` wins
when present, and an ask above `MaxWait` (60 s) fails fast wrapping
`mw.ErrRetryAfterTooLong` so an outer queue or a person decides
instead of the process sleeping. Exhausted retries return the last
error. Options: `MaxRetries`, `BaseDelay`, `MaxWait`, `Classifier`.

`mw.Fallback(models...)` and `mw.FallbackWhen(when, models...)` switch
to the next model on the same before-first-event rule; cancellation
and the kill switch never fall through, and a `max_tokens` finish is a
successful stream, not a failure, so Fallback does not switch on it.
`mw.RepairJSON` re-encodes a tool call's arguments once when they are
not valid JSON, the common damage being a reply cut mid-object or
wrapped in a Markdown fence: it closes unterminated strings and
containers and strips fences, and what cannot be repaired passes
through and fails decoding as it would have, so the model still sees
its `INVALID_INPUT` result. On the tool seam, `mw.Allow` is the fixed
policy (`DENIED: tool "x" is not allowed`, no retry, no exception),
`mw.Audit` logs run, step, call, duration and outcome including the
internal cause, `mw.MapErrors` shapes what the model reads (Chapter 5),
and `mw.Log` records one line per model call at Debug.

The three-layer retry stance, stated once: transport retries (429,
5xx, connection resets) belong to the vendor SDK's config; logic
retries, the model correcting its own argument, are `ModelRetry`, a
per-tool budget (Chapter 12); and the loop itself never retries a
model call. Each layer has one job and none overlaps another.

### Why this is the whole extension model

Every harness feature in THE-END-GOAL's module map is one of these
wrappers. Sandboxing wraps the tool call. Approval is enforced at the
base of the tool chain (so `Allow` can deny first and `Audit` sees the
attempt) and surfaces as a run boundary. OTel and slog attach at the
tap. A third seam, or a phase turned into a hook, needs an ADR, and the
playbook lists "callback soup" as a red flag.

The Elysia-style evaluation (playbook §9) is the worked example of
saying no: lifecycle hooks and plugin scopes were studied and rejected;
what was adopted was error codes, per-tool middleware, the context
decoration convention, and option composition.

## 10. Human in the loop: four models, one chosen

### The problem

The model wants to refund an order. A person must say yes. The person
is not in the process: they are on a web page, in Slack, or asleep. The
decision may arrive in a second or in a day, over a different transport,
possibly to a different replica.

### The four models

1. **Blocking prompt.** The tool call waits on a channel or a callback
   until a decision arrives. Simple in a CLI; ties the decision to the
   process lifetime and one transport.
2. **Interrupt plus checkpoint.** The node raises an interrupt, the
   whole graph state is checkpointed, the client resumes by id and the
   node re-executes from the top. Needs a persistence layer under the
   loop.
3. **Suspend/resume snapshot.** The loop is a workflow; a step returns
   `suspend()`, the workflow snapshot is persisted, and `resume(runId,
   data)` continues it. Needs snapshot storage and a double-resume guard.
4. **Run boundary.** The run *ends successfully* with the pending calls
   on the result and no result in the transcript. The caller holds the
   transcript. A later run resumes with the transcript plus the
   decisions. Needs nothing under the loop.

### What the field does

- **Crush** is model 1: a six-step `permissionService.Request` pipeline
  (`--yolo` skip, static allowlist, hook pre-approval, per-session
  auto-approve, remembered grants, blocking pubsub prompt) with
  "first caller wins" deduplication [crush §6.1]. Sub-agents have no
  permission control at all (issue #3412) [crush §10.1].
- **LangGraph** is model 2: `interrupt()` is a plain call inside a node
  that raises `GraphInterrupt` carrying its checkpoint namespace; the
  root graph swallows it and emits `__interrupt__`; `Command(resume=…)`
  re-executes the node, with `interrupt()` now returning the value
  matched by index. Requires a checkpointer. The trap is side effects
  before the interrupt re-running [langgraph §4.8]. It bubbles through
  nested subgraphs by namespace, which is the strongest nested-HITL
  story in the field, bought with the whole checkpoint substrate.
- **Mastra** is model 3, twice: `requireToolApproval` closes the stream
  on a tool call and `approveToolCall({runId})` continues it; tools can
  also self-suspend with `suspendSchema`/`resumeSchema`. Resume is
  claim-protected against double-resume. Approval forces sequential
  tools. It is "the single buggiest area", 185 suspend-related issues,
  with snapshot size and deserialisation cost as the failure modes
  [mastra §4.8, §8].
- **Vercel AI SDK** is model 4 in the core: "this is *not* an interrupt";
  the run ends with a `tool-approval-request` part and the caller starts
  a second call with the responses appended. Approval responses are
  `tool`-role messages. Because history is client-controlled, responses
  are forgeable unless HMAC-signed (`experimental_toolApprovalSecret`).
  Then it also ships model 3 in `WorkflowAgent` and model 1 in the
  harness's `PromptControl`, and the deep dive's advice is "pick one
  approval model and document it hard" [ai-sdk §4.8, §11].
- **Pydantic AI** is model 4: a tool raises `ApprovalRequired`, the run
  ends returning `DeferredToolRequests`, resume is a new run with
  `deferred_tool_results=`; `ctx.tool_call_approved` is true on the
  resumed call. The same mechanism doubles as external tool execution.
  Its most-commented open issue, #3274 with 42 comments, is "HITL
  approval for multi-agent systems" [pydantic-ai §4.8, §8].
- **DeerFlow** is model 4 "without `interrupt()`": `ask_clarification`
  ends the superstep with `Command(goto=END)` and the answer arrives as
  the next human message with a versioned payload. Risk confirmation
  itself is prompt-enforced only, which the deep dive calls "a soft
  spot" [deer-flow §6, §12].
- **pi** refuses the whole category: no approval prompt exists; the gate
  that exists is project trust for loaded configuration, and a
  permission gate is a 30-line extension [pi §6]. The community argument
  it records, "approvals are security theater once the agent executes
  code", is worth keeping in mind.

Beyond the seven, and two models the four above do not quite cover:

- **OpenAI Agents SDK**: `needs_approval=True` on a tool (agent tools
  included) pauses the run; pending items appear on
  `result.interruptions`; the caller serialises `result.to_state()`,
  later calls `state.approve()` or `state.reject()`, and resumes the
  original run. Model 4 in spirit, but the unit that crosses the
  boundary is a serialisable *run state*, not the bare transcript.
- **Claude Agent SDK**: model 1, refined. `canUseTool` is a blocking
  callback, and `permission_mode` selects how often it fires:
  `default`, `acceptEdits`, `plan`, `dontAsk`, `bypassPermissions`,
  and `auto`, in which "a model classifier" approves or denies. That
  last one is a fifth model, **the model as approver**: a cheaper
  policy model judges each call against scope, unknown
  infrastructure and hostile content. In weft it is one `WrapTools`
  middleware that calls a classifier and returns `DENIED` or `next`.
- **Genkit**: model 4 with a twist. An interrupting tool ends the
  generation with `finishReason: "interrupted"`; the caller resumes
  with `respond` (supply the answer) or `restart` (re-run the tool
  with `resumed` metadata). weft's `Approve` is the restart form:
  the approved call re-enters the chain with `Call.Approved` set.
- **Google ADK**: `LongRunningFunctionTool` returns an operation id
  immediately, "the agent runner pauses the agent run", and the client
  later sends the real `FunctionResponse`. Model 4 with the pending
  call visible to the model as a ticket.
- **Microsoft Agent Framework**: `RequestInfoExecutor` and
  `ctx.request_info()` inside workflows that checkpoint at superstep
  boundaries: model 2.
- **MCP elicitation**: a *server* asks the *client* for structured
  input in the middle of a tool call, with `accept`, `decline` or
  `cancel` as the answer. From the loop's point of view that is model
  1: the tool call blocks until the client answers. A weft consumer
  mounting MCP tools must either accept the block or map elicitation
  onto a parked call; the spec also forbids using it for secrets.
- **A2A**: a remote agent's task can sit in `TASK_STATE_INPUT_REQUIRED`
  or `TASK_STATE_AUTH_REQUIRED`; the caller sends another message to
  continue. Model 4 over the wire, with the task id as the handle.

### What weft does

Model 4, ADR 0007, and the transcript is the only state:

```go
refund := weft.Tool("refund", "Refund an order.", handler, weft.RequireApproval())

res, _ := agt.Generate(ctx, weft.Prompt("refund order 42"))
for _, call := range res.Pending {          // the run ended successfully;
	ask(call.Name, call.Args)               // the call did not run
}
// Later, anywhere, any transport:
res, _ = agt.Generate(ctx, weft.Messages(res.Messages...), weft.Approve("c1"))
```

Mechanics, in order:

1. A `RequireApproval` tool, or any middleware returning an error that
   wraps `ErrApprovalRequired`, is not executed. The check is at the
   base of the chain so `Allow` can deny first and `Audit` sees it.
2. The step's other tools run. The run ends successfully with the
   parked calls on `RunResult.Pending` and `RunFinish.Pending`; the
   transcript's tool message is intentionally dangling. A parked call
   has its `ToolStart` and no `ToolFinish`.
3. Resume: when any decision is present, the last assistant message's
   unresolved calls are exempt from repair and resolved before the
   first model call. Approved calls run through the ordinary chain with
   `Call.Approved` set. Denied calls get `DENIED: <reason>`. Calls with
   no decision get `DENIED: no decision`, visible and pinned, never
   silent.
4. Cancellation wins: a run canceled while calls are parked fails with
   the ctx error, the calls riding on `RunError.Result.Pending`, so the
   transcript stays resumable.

No new part types, no persistence in the core, no transport assumed.
`runtime` (TODO §14) adds signed decisions, persistence and workflows
on top, which is where the AI SDK's HMAC lesson lands.

### The nested case, in full

This is the case that needs the most careful thinking, because it is
where the four models diverge most. Suppose the orchestrator delegates
to a `research` subagent, and inside the child the model calls
`refund_order`, which requires approval.

What happens under each model:

- **Blocking prompt (Crush).** The child's goroutine blocks on the
  prompt. The parent's tool call blocks with it. It works in a CLI and
  nowhere else; Crush's own issue tracker asks for sub-agent permission
  control.
- **Interrupt plus checkpoint (LangGraph).** The interrupt carries the
  namespace `parent|research:task_id`, bubbles to the root, the whole
  tree is checkpointed, and one `Command(resume=…)` re-enters the right
  node. This is the best answer, and it costs a checkpointer under
  every run.
- **Suspend/resume (Mastra).** The doc does not describe bubbling
  through a child agent, and the open issues around suspend inside
  tools suggest why.
- **Run boundary (AI SDK, Pydantic AI, weft).** The child's run ends
  successfully with `Pending` set. But the child's handler must return
  a *string* to the parent's tool chain. There is nowhere in the
  parent's transcript to put the child's transcript, and no run option
  on the parent that can carry a decision for a call id that exists
  only inside the child. Pydantic AI's #3274 is exactly this gap.

So under the run-boundary model, "propagate" means one of:

| Option | What it would mean | Why weft does not do it in the core |
|---|---|---|
| Keep the child transcript in memory inside the parent's `Pending` entry | Works only in-process, only while the `Agent` value lives | The decision must be able to arrive over HTTP a day later; that is the reason model 4 exists |
| Park the parent's call by wrapping `ErrApprovalRequired` | The parent run ends pending on `research`; `Approve` re-runs the child from scratch with `Call.Approved` set | The child's inner `refund_order` does not read that flag, so it parks again: an infinite approval loop |
| Serialise the child transcript into the tool result | The caller could resume the child themselves | A tool result becomes a state container the model also sees |

weft's decision (TODO §5.1; the ADR is not yet written, and 0009 is
taken by the wire-format decision): **a child run that ends
with pending calls is a loud tool error at the parent**,
`SUBAGENT_PENDING: agent "research" ended awaiting approval of 1
call(s)`, visible to the parent model and to `Audit`, never silent.
Full propagation belongs to `runtime`, which owns sessions and
persistence, can store the child transcript under the lineage id
(Chapter 11), and can resume it on decision, the LangGraph shape built
above the loop instead of under it. The practical guidance until then:
approval-gated tools belong in the orchestrator, not in a child.

### Security, stated honestly

ADR 0007's own words: approval is "a policy and UX seam, not a security
boundary". Per-call prompts do not contain a compromised prompt or a
malicious tool; the boundary that does is the sandbox a tool runs in
(`runtime`'s `SandboxFS`). Input trust, gating auto-loaded configuration
and extensions the way pi's project trust does, is consumer-side policy.
The AI SDK's forgeable-history finding applies to every run-boundary
design: once decisions live in a client-held transcript, a serious
deployment signs them.

### Tests that pin it

`TestApprovalPendingStopsTheRunAndResumes`, `TestApprovalFromMiddlewareAndPromptAfterPending`,
`TestApprovalWithoutDecisionsIsRepairedAsInterrupted`, `TestCanceledRunWithPendingCallsFails`, `TestRunFinishPendingRoundTrips`,
`ExampleRequireApproval`, and the pinned `DENIED:` strings.

## 11. Subagents: one agent using another

### The problem

A task is too big for one context window, or needs a different model,
or needs isolation. One agent must be able to hand work to another and
get the result back. The design space is wide, and it is where
frameworks have most often built machinery they later removed.

### The five shapes

1. **Agent as a tool.** The child is wrapped as a tool; the parent's
   model calls it like any other tool with a prompt; the child runs on
   a fresh transcript and its final answer is the tool result.
2. **Handoff.** A tool returns a control-flow value that transfers the
   conversation to another agent, which continues on the same
   transcript.
3. **Supervisor or graph.** Agents are nodes; edges or a supervisor
   decide who runs next; state is shared through a schema.
4. **Lanes.** One session, several named lanes, each with its own model
   configuration and queue, sharing one tree of state.
5. **Goroutine pools.** Children as concurrent runs with a bounded pool,
   receipts, and stop-reason taxonomy, exposed as a `task` tool.

### What the field does

- **Vercel AI SDK**: "No graphs, no handoff primitive". Agent-as-tool
  ("the orchestrator-worker pattern is literally this"), `prepareStep`
  routing, and a `HarnessAgent` wrapper. "Orchestration is user code;
  the SDK only standardizes the loop, the tool contract, and the message
  contract" [ai-sdk §4.7, §5]. It says nothing about child usage roll-up,
  child event streaming, or cancellation into children.
- **Pydantic AI**: five levels, and delegation is shape 1 verbatim:
  ```python
  @joke_selection_agent.tool
  async def joke_factory(ctx: RunContext, count: int) -> list[str]:
      r = await joke_generation_agent.run(f'Please generate {count} jokes.',
                                          usage=ctx.usage)  # share the parent's budget
      return r.output
  ```
  `usage=ctx.usage` accounts sub-agent spend to the parent run,
  "multi-agent cost accounting in one line" [pydantic-ai §4.7, §6].
  Handoff is "programmatic: return the next agent's result"; graphs are
  for workflows and "the two never pretend to be one API". Agents are
  stateless and global.
- **LangGraph**: shapes 2 and 3 natively. `Send` fan-out, `Command`
  with `goto` including `Command.PARENT`, subgraphs as nodes, and
  satellite supervisor/swarm libraries where handoff is a
  `transfer_to_<agent>` tool returning `Command({goto, graph: PARENT})`
  [langgraph §4.7]. The deep dive's verdict: "ownership of 'what is an
  agent' churned three times in 20 months" and the `Command` redesign
  "admits the original primitives were wrong" [langgraph §7].
- **Mastra**: subagents as a constructor property `agents: {…}` with
  delegation hooks that can rewrite the prompt or `bail()`; agent-as-tool
  over MCP (`ask_<agentKey>`); networks shipped then deprecated in
  favour of supervisor agents; child runs inherit the parent's abort
  signal [mastra §4.7, §4.11].
- **Crush**: shape 1 in Go, `fantasy.NewParallelAgentTool`; the `Task`
  agent is read-only by construction, no MCP by default; child runs
  persist as child sessions with `parent_session_id` lineage and ids
  encoding `messageID:toolCallID`; the coder/task pair is hardcoded
  ("TODO: make this dynamic") [crush §5.4].
- **DeerFlow**: 1.x was a fixed coordinator→planner→research
  team→reporter graph; 2.0 is "a ground-up rewrite that shares no code
  with 1.x" into a single lead agent with dynamic subagents, shape 5:
  a `task` tool, a `SubagentExecutor`, process-wide `max_running: 3`,
  per-run `task` call caps enforced in the prompt, stop reasons
  `token_capped | turn_capped | loop_capped`, and usage split by caller
  role (`lead_agent_tokens`, `subagent_tokens`) [deer-flow §5.4, §9.6].
  The 1.x graph became a *skill*: "the domain knowledge moved from
  architecture into prompt assets".
- **pi**: shape 4, and an explicit refusal of the rest: "No sub-agents…
  a black box within a black box." `AgentLanes` give one session
  several lanes over a shared tree; escape hatches (tmux, extensions)
  are how OpenClaw built multi-agent on top. Nested LLM usage goes into
  `AgentToolResult.usage` and a "Tools/summaries" bucket [pi §5].

Beyond the seven:

- **OpenAI Agents SDK** ships shapes 1 and 2 side by side and
  documents when to use which. `agent.as_tool()` is shape 1, with
  `max_turns` for the child, `custom_output_extractor` to shape the
  result, `on_stream` to observe the child's events, and
  `is_enabled` to expose the child conditionally: the same three
  needs weft's design names as bounded child, output extraction and
  nested events. Handoffs are shape 2: tools named
  `transfer_to_<agent>`, after which "the new agent takes over the
  conversation" and sees the whole history unless an `input_filter`
  trims it. The recommended prompt prefix exists because the model
  must be told what a handoff means.
- **Claude Agent SDK / Claude Code**: shape 1 as the `Agent` tool.
  A subagent "starts with a fresh conversation", sees none of the
  parent's turns, and "only its final response returns to the parent
  as a tool result". The dollar budget covers subagents, spawning
  fails with "Budget limit reached" once it is hit, and `modelUsage`
  reports the whole tree while `usage` reports the main loop only:
  the `SubagentUsage`-versus-`Usage` split, arrived at independently.
- **Google ADK**: shape 1 (`AgentTool`) plus deterministic composition
  (`SequentialAgent`, `ParallelAgent`, `LoopAgent`) plus LLM-driven
  transfer among `sub_agents`, all sharing `session.state` through
  `output_key`. Shape 3 with shared mutable state, the thing weft's
  design refuses.
- **Microsoft Agent Framework**: shape 3 as typed graph workflows
  (executors, edges, fan-out and fan-in) with named orchestrations:
  sequential, concurrent, group chat, handoff and magentic (a
  planner-led manager loop). Also a "Harness Agent" with planning,
  compaction and don't-ask-again approvals.
- **CrewAI**: role-based crews with a `sequential` or `hierarchical`
  process and `allow_delegation` (off by default).
- **Inngest AgentKit**: a network of agents with a *router*, code or
  model, choosing who runs next, and shared network state; shape 3 on
  a durable engine.
- **Anthropic's research system** is the largest documented shape 1
  deployment: an orchestrator with parallel subagents, "15× more
  tokens than chats", research time "cut by up to 90%" with three to
  five parallel subagents and three or more parallel tool calls, and
  a lead that waits synchronously for its children, which the
  write-up names as the bottleneck. weft's `Parallelism(4)` inside a
  step is exactly that synchronous fan-out; asynchronous delegation is
  a `runtime` pool concern.

### Reading the evidence

Three things stand out.

First, the graph worldview lost on its home turf. DeerFlow rewrote away
from it; LangGraph moved its flagship agent out and redesigned its
routing primitive; Mastra deprecated networks. THE-END-GOAL's decision
predates those facts and matches them: agents-as-tools and `prepareStep`
routing live in the core; a graph DSL, if ever, is a satellite.

Second, the two things every serious implementation adds on top of
shape 1 are **usage attribution** (Pydantic AI's shared budget, DeerFlow's
by-role split, pi's usage bucket) and **lineage** (Crush's child
sessions, DeerFlow's delegation ledger). A subagent design that has
neither is a demo.

Third, pi's refusal is a *product* choice, not a framework one. Its
argument, that a child is opaque to the user, is answered by streaming
the child's events into the parent's stream so nothing is a black box.
`AgentLanes` is a session and UX concept; if weft ever wants it, it
belongs in `runtime`, built on the same core.

### What weft decided (TODO §5.1; ADR to be written; not yet in the code)

The API is one function and one new event:

```go
func Subagent(name, description string, child *Agent, opts ...ToolOption) *ToolDef

type Nested struct {
	Seq    int64  `json:"seq"`     // from the parent's counter
	CallID string `json:"call_id"` // the parent's tool call that owns this child run
	Event  Event  `json:"event"`   // any child event, including a Nested from a grandchild
}
```

And it adds zero new concepts to the loop: a subagent is a tool whose
handler runs `child.execute`. That sentence is the whole reason the
design is right. Everything the tool contract already gives, the
subagent gets for free: `Timeout` bounds the child; `MaxResultBytes`
caps its answer; `RequireApproval` gates the delegation itself;
`Sequential` makes it a barrier; `WrapTools` middleware wraps it;
`Replay` annotates it; the manifest lists it. Rule 10 stays intact.

```go
researcher := weft.New(cheapModel,
	weft.Name("researcher"),
	weft.Instructions("Research the topic thoroughly. Answer in under 200 words."),
	deepSearch,
)
orchestrator := weft.New(strongModel,
	weft.Name("support-bot"),
	refundOrder,
	weft.Subagent("research", "Research a topic in depth.", researcher,
		weft.Timeout(2*time.Minute)),
)
```

The mechanics, one by one:

**Input.** The schema is `{"prompt": string}`, and the field's
description tells the parent model what the child does not see: *"The
task, stated in full: the agent sees only this prompt, not the
conversation."* The description string is the parent model's entire
routing surface, exactly like the Claude Code `Task` tool. The child
starts on a fresh transcript.

**Output.** The child's final text, verbatim. If the child was built
with `Output[T]`, its last assistant message is usually empty because
the run ended on a `submit_output` call, so the handler returns the
submitted JSON instead. Typed delegation therefore works out of the
box: a `Verdict`-typed child hands the parent a `Verdict` JSON object.

**Events.** The parent's `execTools` puts an emitter on the handler's
ctx (unexported, the "parent's `emit` travels in ctx" of the spec).
The child's `execute` uses it as its sink, so every child event arrives
in the parent stream as `Nested{Seq, CallID, Event}` with `Seq` from the
parent's counter, assigned under the parent's ordering lock. The parent
stream stays totally ordered and replayable; the top-level vocabulary
is unchanged; a grandchild event is a `Nested` inside a `Nested`. The
child's own taps see the child's raw events; the parent's taps see the
wrapped ones. On the wire the type is `nested`, and `UnmarshalEvent`
recurses.

A parent stream for the example above, abbreviated:

```
run_start        support-bot
step_start       0
tool_start       seq=1 call=c1 research {"prompt":"..."}
nested seq=2 c1  run_start   researcher  id=<parent>/c1
nested seq=3 c1  step_start  0
nested seq=4 c1  tool_start  seq=1 deep_search
nested seq=5 c1  tool_finish seq=2 deep_search
nested seq=6 c1  step_finish 0
nested seq=7 c1  text_delta  "..."
nested seq=8 c1  run_finish  usage=…
tool_finish      seq=9 call=c1 research "..."
step_finish      0
...
```

**Usage.** The child's total usage, including a failed child's partial
usage (tokens were spent), is reported back through a ctx-carried sink
and recorded on `StepRecord.SubagentUsage map[string]Usage` keyed by
call id, then added to `RunResult.Usage`. `StepFinish.Usage` and
`StepRecord.Usage` stay the model call's own numbers, so attribution is
exact: Pydantic AI's shared budget and DeerFlow's by-role split, in one
map. A subagent call resumed under `Approve` has no `StepRecord`, so its
usage rolls into the total only.

**Run ids.** The child run's id is `<parentRunID>/<callID>`: deterministic
given the parent, self-describing lineage in the style of Crush's child
sessions, a stable key for `store`, and visible on the nested
`RunStart`. `CallFromContext` inside the child's tools reports the
child's run id; the parent's call id is on the `Nested` wrapper.

**Cancellation.** The child runs on the parent's call ctx. Parent
cancellation cancels the child; a `Timeout` on the subagent tool
cancels the child at the deadline and the parent records "tool research
timed out after 2m0s"; the abandoned child goroutine stops at its next
ctx check and its late events are dropped by the post-cancellation
rule. THE-END-GOAL's "subagents are goroutines" is literally the
implementation.

**Failure.** A child run error is data to the parent (rule 1 inherits):
`SUBAGENT_FAILED: agent "researcher" failed at step 3: <cause>`, with
the `*RunError` on `ToolError.Err` for `Audit` and `errors.As`, and
never in the transcript. A child that hits its own `MaxSteps` is
therefore a tool error, not a parent run error, and the parent model
can try something else.

**Pending approvals.** Chapter 10: `SUBAGENT_PENDING: agent "research"
ended awaiting approval of N call(s)`. Loud, not propagated, deferred
to `runtime`.

**Cycles.** Agents are immutable values, so `A` cannot contain `B`
containing `A` at construction. `ToolSource` (TODO §5.11) can close the
loop at run time:

```go
var reg Registry
A := weft.New(model, weft.ToolSource(reg.Tools))
B := weft.New(model, weft.Subagent("a", "Ask A", A))
reg.Add(weft.Subagent("b", "Ask B", B))   // A → B → A, and it compiles
```

At run time: A's model calls `b`; the handler runs B; B's model calls
`a`; the handler runs A on a fresh transcript; A's model calls `b`
again. Each level is a separate run with its own `MaxSteps`, so no
budget trips, and each level is one goroutine deeper. The guard is an
**ancestor check**: the same ctx value that carries the emitter carries
the chain of agents currently running above this call; a `Subagent`
whose child is already in that chain returns `SUBAGENT_CYCLE: agent
"a" is already running in this call chain`. No knob, a precise message,
and legitimate deep chains of distinct agents are unaffected. A depth
cap was considered and rejected as an arbitrary number every user must
think about. Cost across depth is not this guard's job; it is
`UsageLimit`'s (Chapter 12), whose spec already says "subagents
included", and which should land immediately after §5.1 for that reason.

**Seams.** The parent's `WrapTools` chain wraps the delegation once; the
child's own seams govern inside it. Attribution comes from
`SubagentUsage` and the nested events, not from re-firing the parent's
`Audit` per delegated turn, which is the Crush rule [crush §7] stated in
weft's vocabulary.

**Outside the loop.** `Agent.CallTool` and `ToolDef.Invoke` on a
subagent tool run the child with no emitter, no usage sink and no
ancestor chain: the child simply runs, as any tool does outside the
loop.

### What is deliberately not there

- No handoff primitive. A handoff is "return the other agent's result",
  as Pydantic AI says; if the *transcript* must move, that is `Messages`
  on the child's run, user code.
- No graph. `PrepareStep` routing plus subagents cover the orchestration
  patterns in the studied field.
- No shared mutable state between parent and child. The prompt goes in;
  the answer comes out; anything else is a tool the child owns.
- No pool or receipts. DeerFlow's `max_running` and stop-reason taxonomy
  are `runtime` concerns; the core's `Parallelism` already bounds
  concurrent delegations within one step.

### Tests the implementation must pin

Nesting events (`Nested` order and `Seq` from the parent's counter,
grandchild double wrapping, wire round trip); usage roll-up (total,
per-call map, failed-child partial usage); child failure is data; child
cancellation follows the parent ctx; `Timeout` on the subagent tool;
`Output[T]` child returns JSON; `SUBAGENT_PENDING` for a child ending
pending; `SUBAGENT_CYCLE` through `ToolSource`; the lineage id on the
nested `RunStart`; the `subagent` field in the manifest; and a godoc
example showing the parent stream.

## 12. Budgets and runaway loops

### The problem

Chapter 4 bounded steps. It is not enough. A model can call the same
tool with the same arguments fifty times inside the budget. A subagent
can multiply the budget by nesting depth. A tool result can be a
megabyte. Each is a different kind of runaway and needs a different
bound.

### The bounds, one per failure

| Runaway | Bound | Where the field learned it |
|---|---|---|
| Too many model calls | `MaxSteps` (fail) | AI SDK default 20; LangGraph 25 supersteps; Pydantic AI 50 requests; Mastra and pi none |
| Too many tokens or dollars | `UsageLimit(max Usage)` (TODO §5.3) | Pydantic AI `UsageLimits` with `cost_limit`, `request_limit`, `tool_calls_limit`, token limits [pydantic-ai §5]; DeerFlow `TokenBudgetMiddleware` |
| The same call repeating | `DetectLoops(repeats)` (TODO §5.4) | Crush: SHA-256 over tool call plus result pairs; fires when any signature repeats more than 5 times in the last 10 steps [crush §5.3] |
| A model that cannot self-correct | `MaxModelRetries(3)` with `ErrModelRetriesExceeded` (TODO §5.2) | Pydantic AI per-tool `max_retries` and `max_output_retries` |
| A huge tool result | `MaxResultBytes` (64 KiB default, visible marker) | pi's 50 KB rule; Crush issue #3480 asking for an MCP result cap |
| A hung tool | `Timeout` per tool | AI SDK v7 `toolMs`; pi's missing default bash timeout (#9460) |
| Nesting depth | Ancestor check plus `UsageLimit` "subagents included" | DeerFlow `max_running: 3` and per-run task caps; Crush's read-only Task agent |
| Fan-out width | `Parallelism(4)` | Pydantic AI's 792 concurrent executions (#6884); Mastra's bound of 10 |

Three data points from outside the seven sharpen the table. The
Claude Agent SDK bounds *dollars* (`max_budget_usd`, subagents
included) as well as turns, and a `Stop` hook that keeps blocking the
end of a turn is overridden after eight consecutive blocks, a budget
on the budget-enforcer. The OpenAI Agents SDK's ten-turn default is
the lowest in the field. And Anthropic's research write-up found that
token usage alone "explains 80% of the variance" in research quality,
which turns a budget from a cost cap into a quality knob: a
`UsageLimit` set too low is a correctness setting, not a savings.

Two conventions from the field are worth stating as rules:

- **Always ship a bounded default.** Mastra and pi ship none; the Mastra
  deep dive says "we should not copy the unbounded default" and the
  playbook agrees. weft's defaults: 10 steps, 4 parallel, 64 KiB per
  result. `UsageLimit` and `DetectLoops` will be off by default because
  their right values are workload-specific, but the CLI scaffold will
  write `DetectLoops(5)` into new projects.
- **A budget breach is a `*RunError` with the partial transcript.** Never
  a silent success, never a lost transcript. Every sentinel in the table
  above follows `ErrMaxSteps`'s shape.

### Loop detection in detail

Crush's mechanism, translated to weft's spec: compute an FNV hash of
the step's set of `(tool name, args)`; keep the last few signatures; if
`repeats` identical consecutive signatures occur, fail the run with
`ErrLoopDetected`. Varying arguments produce different signatures, so a
legitimate retry with a corrected argument is not a loop. Including the
*result* in the signature, as Crush does, catches a model that repeats
a call whose answer never changes, at the cost of missing a tool whose
output carries a timestamp. weft's spec hashes calls only; the ADR
should say why.

### What usage counts

`Usage` is two counters, input and output tokens, summed per step onto
`StepFinish` and `StepRecord` and per run onto `RunResult.Usage`. The
folding rules live in the adapters (ADR 0013) and are decisions, not
accidents: the Anthropic adapter folds cache read and creation tokens
into input, the Google adapter folds thought tokens into output, so
the two counters stay comparable across providers. A budget written
against `Usage` means the same thing whichever model answered, which
is what makes `UsageLimit` enforceable at all. Subagent usage rolls
into the same total (Chapter 11).

### Budgets across subagents

This is the place Chapter 11's cycle guard hands off. A child run has
its own `MaxSteps`, so steps do not compose across nesting. Usage does:
the child's usage rolls into the parent's `RunResult.Usage` after every
step, and `UsageLimit` is checked after each step against that total.
A parent with `UsageLimit(Usage{OutputTokens: 50_000})` therefore bounds
the whole tree, however deep, and the breach is reported at the parent
as `ErrUsageLimit` with the partial transcript. That is why §5.3 should
land immediately after §5.1.

## 13. Durability: what survives a restart

### The problem

A deploy kills the process in the middle of step 4, while a tool is
running. What is lost, what can be resumed, and is it safe to re-run
the interrupted tool?

### What the field does

- **LangGraph**: checkpoint per superstep, `durability="sync|async|exit"`;
  `@task` replay-skip via content-derived ids; nested checkpoints
  namespaced per subgraph. The community pays in storage: "85% storage
  bloat" (#7714), checkpoint format churn v1 to v4 [langgraph §4.9, §8].
- **Mastra**: the loop is a workflow, so workflow snapshots *are* the
  checkpoint system; snapshots persist only in pending, paused or
  suspended status [mastra §4.4, §4.9].
- **Pydantic AI**: stateless by design; every side-effecting boundary
  has an operation id so a durable engine (DBOS, Temporal) can be thin;
  `parallel_ordered_events` mode exists for replay determinism
  [pydantic-ai §4.9]. Its own persistence moved to the paid Harness.
- **Crush**: sessions persisted per message; canceled turns get a
  tombstone; auto-summarisation when the context grows [crush §8].
- **pi**: per-tool `replay: "never" | "safe"`; a crash mid-tool with
  `replay: never` synthesises an interrupted result instead of re-running
  [pi §8]. The experimental harness stores total operation state after
  every transition, "never replaying a journal".
- **DeerFlow**: pre-run checkpoint capture with rollback on failure;
  delta checkpoints with a snapshot every 10 writes; durable cancel
  across workers [deer-flow §6, §8].

Beyond the seven, the durable-execution engines answer the question
from underneath (Chapter 25 compares the models):

- **Temporal**: the agent loop lives in a workflow; every model call
  and every tool call is an activity; workflow code must be
  deterministic so that replaying the event history reconstructs the
  state; long loops roll over with continue-as-new; human input
  arrives as a signal.
- **DBOS**: `@DBOS.workflow()` orchestrates `@DBOS.step()` calls
  whose outputs are checkpointed to Postgres; "steps are tried at
  least once but are never re-executed after they complete";
  workflow code must be deterministic; the workflow id is an
  idempotency key, so a workflow called twice with the same id runs
  once.
- **Claude Agent SDK**: sessions are written to local disk first and
  mirrored to a `session_store` adapter, so another host can resume;
  a crash yields a final `error_during_execution` result whose cost
  fields may be zeroed.
- **Microsoft Agent Framework**: checkpoints at superstep boundaries
  in graph workflows; per-step result caching in the functional API.
- **Cloudflare Agents**: each agent instance is a Durable Object with
  its own SQL state, hibernating between events: durability by
  addressing rather than by journaling.

### What weft does, and defers

The core is stateless on purpose. What it provides so that `store`
(TODO §11) and `runtime` (§14) can be thin:

- **Ids as keys from day one.** Every run has an id before its first
  event; child runs derive theirs from the parent (Chapter 11). Mastra's
  retroactive id requirement at 1.0 is the lesson ADR 0004 cites.
- **A replayable stream.** `Seq`-ordered events with pinned wire bytes
  (Chapter 8); `store` records them, the Inspector replays them.
- **Repair.** A persisted transcript with a dangling call is valid
  input after `Repair` (Chapter 7).
- **`Replay(ReplaySafe | ReplayNever)`** on a tool. The core records it
  on the tool and in the manifest and changes nothing in the loop; the
  restart layer re-runs safe tools and synthesises interrupted results
  for the rest. That is pi's rule, carried as an annotation.
- **The approval boundary needs no persistence** (Chapter 10), which is
  exactly why it can be in the core while durability is not.

### The shape already sketched for `store` and `runtime`

TODO §11 and §14 fix more of the design than "later" suggests. `store`
records a `RunRecord{ID, Agent, Model, Started, Finished, Events,
Result, Err}` behind a three-method `Store` (`Save`, `Get`, `List`),
with SQLite (CGO-free) and Postgres implementations, and
`weft.Record(s)` is just a Tap that buffers events and writes the
record once at the finish, plus one "running" row at `RunStart`: a
crash leaves a row the Inspector shows as interrupted, not silence.
The session format borrows pi's tree shape, JSONL entries with
`id`/`parentId` so a session branches in place, plus repair-on-read
rules (append the newline to a torn final line, skip malformed
mid-file lines with a warning, never let one corrupt file block the
others) that complement `Repair` at the file level. A usage ledger
with adjustment rows keeps provider-side rebills honest, and the entry
taxonomy keeps extension state out of the model's context unless it
opts in (`custom` vs `custom_message`). `runtime` then adds sessions
(`Send`), compaction with a preview hook (never destructive-only),
HMAC-signed approval decisions, a bounded subagent pool, and
`SandboxFS`.

The end-goal decision: the framework ships its own SQLite/Postgres
checkpoint store because zero-config durability is a success criterion;
Temporal, DBOS and Restate are adapters against the same contract.
LangGraph's five-method checkpointer with a conformance suite is the
prior art to copy for that contract; its storage bloat is the warning.

### The questions deliberately deferred

Eleven questions are answered above. Five more are in the field and
consciously not yet in weft; each has a spec and a number.

- **Steering** (TODO §5.16): the caller speaks while the run is in
  flight. The delivery point is fixed, between the current tool batch
  and the next model call; a follow-up queues until the turn ends. pi
  delivers both modes, `steer` and `followUp` [pi §5]; sessions will
  expose the same thing as `Send`.
- **Tool progress** (TODO §5.15): a `ToolProgress{Seq, CallID, Name,
  Content}` event emitted from `weft.ProgressFromContext(ctx)`, for
  long tools that want to report partials. The content is UI detail
  and never enters the model's context; that is pi's content/details
  split [pi §5] as an event.
- **MCP interop** (TODO §7): expose weft tools over an MCP server,
  consume foreign tools as `RawTool`s. The measure-first question is
  whether `google/jsonschema-go` earns its dependency slot against the
  hand-rolled reflector on a 30-struct corpus.
- **Observability** (TODO §8): OTel spans (`weft.run`, `weft.step`,
  `weft.tool`, with `gen_ai.*` attributes) through a built-in Tap,
  the one sanctioned core dependency, plus a `Logger` option for slog.
  Until then the Tap is the seam and `mw.Log`/`mw.Audit` the printers.
- **Record/replay for tests** (TODO §9.2): a VCR for model calls. pi
  ships none on purpose; the lean is recorded fixtures first and VCR
  only if live drift actually bites.

---

# Part III — The frameworks side by side

## 14. The matrix

The seven studied frameworks against weft. Chapter 17 adds fifteen more systems in a second matrix.

| | Vercel AI SDK | Pydantic AI | LangGraph | Mastra | Crush (fantasy) | DeerFlow 2.x | pi | weft |
|---|---|---|---|---|---|---|---|---|
| Language | TS | Python | Py/TS | TS | Go | Python | TS | Go |
| Loop shape | `do…while` tool loop | typed graph, hidden | Pregel supersteps | workflow `dowhile` | library loop + harness lifecycle | LangChain loop + 18 middlewares | own loop, steer/followUp | `for step < MaxSteps` |
| Step limit | `isStepCount(20)` | `request_limit=50` | `recursion_limit=25` (supersteps) | none by default | `StopWhen` conditions | 100, ceiling 1000 | none | `MaxSteps(10)`, fail |
| Stop conditions | predicates, OR | `end_strategy` | graph edges | predicates + hooks + `bail()` | `StopWhen` | goal loop | `shouldStopAfterTurn` | `StopWhen` predicates |
| Tool error | data (input, no-such-tool) | data (`ModelRetry`, validation) | data by default; siblings canceled | unstated | terminal result on denial | data | data | data, coded |
| Parallel tools | unstated | yes, unbounded, barriers | `Send` fan-out, pool | yes, 10, auto-sequential under HITL | via fantasy | via LangGraph | yes, call-order results | yes, 4, call order, barrier |
| Timeouts | total/step/chunk/tool | model, tool | node run/idle | step/total | idle vs hard | subagent 15–30 min | none by default | per tool + agent tool default, ctx for the run, 60 s idle at adapter |
| Result cap | no | no | no | processors | no (#3480) | budget, externalise | 50 KB rule | 64 KiB, marker |
| Transcript repair | no | no | checkpoint replay | snapshot | orphan synthesis | dangling-call middleware | interrupted entry | `Repair`, visible |
| Stream order | 28-chunk grammar | events | 3 generations, ns-prefixed | chunks | fantasy callbacks | LangGraph | events | `Seq`, pinned bytes, sealed |
| Extension | `prepareStep`, policy maps | decorators, toolsets | nodes, interceptors | processors, hooks | callbacks, hooks | middlewares | extension events | two seams + tap |
| HITL model | run boundary (+ suspend + interrupt) | run boundary | interrupt + checkpoint | suspend/resume | blocking prompt | turn end | none | run boundary |
| Nested HITL | unstated | open issue #3274 | bubbles by namespace | unstated | none (#3412) | unstated | n/a | loud error; runtime later |
| Subagents | agent-as-tool, user code | agent-as-tool, shared usage | subgraphs, `Command`, supervisor | `agents:` property, hooks | `NewParallelAgentTool`, child sessions | `task` tool, pool of 3, stop reasons | refused; lanes | agent-as-tool, `Nested`, usage map, lineage id |
| Usage roll-up | no | `usage=ctx.usage` | unstated | unstated | per session | by caller role | tool usage bucket | `SubagentUsage` + total |
| Cycle guard | none | none | recursion limit | none | none | task caps | n/a | ancestor check |
| Durability | none in core | operation ids | checkpointer | snapshots | sessions, tombstones | delta checkpoints | op-state, `replay` | ids, `Replay`, `store` later |
| Loop detection | none | none | none | none | SHA-256 over 10 steps | middleware | none | `DetectLoops` (planned) |
| Breaking-change record | 4 majors / 20 months | v2 flipped defaults | 3 redesigns | 64 minors / 7.5 months | n/a | 2.0 shares no code with 1.x | extension churn | apidiff gate, no renames |

## 15. Each framework in three lines

- **Vercel AI SDK.** Best small API: stop conditions as predicates,
  approval as a run boundary, scoped tool context. Worst record on
  stability, and cancellation is still under-invested. Learn its
  shapes, not its versioning.
- **Pydantic AI.** The most deliberate execution policy and the
  cleanest error taxonomy (values that steer versus errors that fail).
  Shared subagent budgets in one line. No concurrency bound and no
  answer yet for nested approvals.
- **LangGraph.** One substrate buys persistence, interrupts, time
  travel and replay together; `interrupt()` is the best nested-HITL
  story. The price is a worldview: every program is a graph, the
  recursion limit surprises everyone, and the flagship agent left home.
- **Mastra.** One engine for loops and workflows is the strongest single
  idea; auto-sequential under approvals is a rule worth copying. No
  default step cap, a deprecated networks abstraction, and suspend is
  the buggiest area.
- **Crush.** The Go precedent: turn lifecycle as a mutex'd state machine
  with documented invariants, idle-versus-hard timeouts, orphan repair,
  loop detection, tombstones. In-memory grants, no sandbox, hardcoded
  agent pair.
- **DeerFlow.** The strongest evidence that the graph loses to the loop:
  a rewrite to lead-plus-subagents, with a pool, stop reasons, usage by
  role, and turn-end HITL. Built on a runtime it must monkey-patch;
  approvals enforced by prompt.
- **pi.** The purest single loop: steer and follow-up delivery,
  fail-truncated-calls, call-order parallel results, two-layer retry,
  `replay` per tool, compaction that never cuts a tool result. No
  subagents, no approvals, no step cap, by choice.

## 16. Where they converge, and where weft sits

Convergent, across every framework that answers the question:

1. A tool error is data the model sees.
2. Parallel tools by default, results in call order.
3. Stop conditions as predicates, separate from a budget.
4. Approval as a run boundary when there is no checkpointer, as an
   interrupt when there is one.
5. Agent-as-tool is the delegation primitive; graphs are optional.
6. Truncated tool calls are failed, not executed.
7. Orphaned calls are repaired visibly.
8. Ids on everything, from day one.

Divergent, and where weft took a side:

| Question | The split | weft |
|---|---|---|
| Budget is success or failure? | AI SDK success; LangGraph error; Mastra none | Failure (`ErrMaxSteps`) |
| Siblings on a tool error? | LangGraph cancels; everyone else continues | Continue |
| Nested approval? | LangGraph bubbles; others unstated or open | Loud error now, `runtime` later |
| Where does orchestration live? | LangGraph in the graph; AI SDK in user code | In the loop: `Subagent` + `PrepareStep`; graph as satellite if ever |
| Hooks or seams? | Crush and DeerFlow hooks/middleware stacks | Two seams, one tap |
| Events after cancel? | Crush flushes; pi records a stop reason | Nothing after cancel; partial transcript is the truth |
| Extension of the event set? | AI SDK grew a 28-type grammar with breaking majors | Sealed, additive, pinned bytes |

## 17. The wider field: fifteen more systems

The seven deep dives were chosen for depth. This chapter widens the
view to the systems a Go engineer will be asked about in the same
breath, at the depth of their public documentation rather than their
source. Where a claim could not be verified against a primary source
during this revision it is marked *(unverified)*; everything else is
from the vendor's documentation as of September 2026 (the sources are
listed in Chapter 32).

### The second matrix

| | Loop and budget | Tool error | Parallel tools | HITL model | Delegation | Extension | Durability |
|---|---|---|---|---|---|---|---|
| **OpenAI Agents SDK** (Py/TS) | `Runner.run`, `max_turns=10` → `MaxTurnsExceeded` | data by default (`failure_error_function`); unknown tool raises unless `return_error_to_model` | `parallel_tool_calls` hint; `max_function_tool_concurrency` (unbounded default) | run pauses, `interruptions`, `to_state()` / `approve()` | `as_tool()` (shape 1) and handoffs `transfer_to_<agent>` (shape 2) | `RunHooks`, `AgentHooks`, input/output/tool guardrails | sessions; `RunState` serialisation; Temporal integration |
| **Claude Agent SDK** (Py/TS, wraps Claude Code) | no default limit; `max_turns`, `max_budget_usd`; typed `ResultMessage` subtypes | rejection message as tool result; `PostToolUseFailure` | per tool by annotation: read-only concurrent, mutating sequential | `canUseTool` callback + six permission modes incl. `auto` (classifier) | `Agent` tool, fresh context, final text returns; whole-tree `modelUsage` | ~20 hook events, matchers, `permissionDecision` | sessions with resume/fork; `session_store` mirror; auto-compaction |
| **Google ADK** (Py/Go/Java) | `LlmAgent` loop; `LoopAgent(max_iterations)` + `escalate` | error in response struct (Go) | `ParallelAgent` for agents; tool level unstated | `LongRunningFunctionTool` ticket, runner pauses | `AgentTool`, `sub_agents` transfer, Sequential/Parallel/Loop agents, shared `session.state` | six before/after callbacks | sessions and events; Vertex Agent Engine |
| **Microsoft Agent Framework** (.NET/Py/Go preview) | agent loop; workflows in supersteps | unstated in this pass | parallel edge groups, fan-in | `RequestInfoExecutor` / `ctx.request_info()` | graph workflows: sequential, concurrent, group chat, handoff, magentic; Harness Agent | middleware: agent, function, chat client | superstep checkpoints; per-step caching |
| **Genkit** (JS/Go/Py) | `generate` tool loop; interrupts end a turn | unstated | unstated | tool interrupts: `respond` / `restart` with `resumed` | Agents API (preview) | model middleware | flow state; Developer UI |
| **Eino** (Go, ByteDance) | `ChatModelAgent` ReAct loop; no documented step cap | unstated | graph orchestration, concurrency managed by the runtime | unstated | Chain / Graph / Workflow orchestration | five callback aspects incl. stream variants | unstated |
| **fantasy** (Go, Charm) | `NewAgent` with `StopWhen` conditions (per the Crush dive) | terminal result on denial (Crush) | via the library | none in the library; Crush adds model 1 | `NewParallelAgentTool` | `PrepareStep`, `OnStepFinish`, `OnToolCall` callbacks | none |
| **smolagents** (Py, HF) | `while llm_should_continue(memory)`; `max_steps` *(default unverified)* | observation text | sequential code execution | none | multi-agent as managed agents | none formal | none |
| **CrewAI** (Py) | `max_iter=20`, `max_execution_time`, `max_rpm` | `max_retry_limit=2` | per process | *(unverified)* | crews: `sequential` / `hierarchical`, `allow_delegation` | callbacks | memory, flows |
| **LlamaIndex** (Py) | `FunctionAgent`, `ReActAgent`, `CodeActAgent` on `Workflow` steps | *(unverified)* | workflow events | `ctx.wait_for_event(HumanResponseEvent, waiter_event=InputRequiredEvent(...))`; the context is serialisable so the wait can span processes | `AgentWorkflow` with handoffs | workflow steps as the seam | `Context` serialisation |
| **Inngest AgentKit** (TS) | network run with a router | *(unverified)* | via Inngest steps | Inngest `waitForEvent` *(unverified)* | network + router (code or model) + shared state | router | Inngest durable steps |
| **Letta** (Py, MemGPT lineage) | stateful agent, steps and heartbeats | *(unverified)* | *(unverified)* | *(unverified)* | multi-agent via shared memory blocks | memory tools | server-side state; MemFS |
| **Cloudflare Agents** (TS) | agent as a Durable Object | *(unverified)* | *(unverified)* | *(unverified)* | *(unverified)* | *(unverified)* | Durable Object SQL state, hibernation |
| **Strands** (Py, AWS) | model → tool → model cycle; invocation limits on `turns`, `output_tokens`, `total_tokens` with stop reasons `limit_turns`, `limit_total_tokens`, `limit_output_tokens` | "goes back to the model as an error result rather than throwing an exception" | executor per tool registry *(concurrency mode unverified)* | *(unverified)* | agents as tools, swarm, graph *(unverified)* | lifecycle events before and after each invocation, model call and tool execution | sessions |
| **Agno** (Py) | agent loop; teams as `TeamMode`: `coordinate` (default), `route`, `broadcast`, `tasks` (a task-list loop) | *(unverified)* | *(unverified)* | *(unverified)* | teams with four delegation modes; AgentOS runtime | *(unverified)* | AgentOS sessions, MCP server |

### Each in three lines

- **OpenAI Agents SDK.** The smallest official SDK with both shapes
  of delegation and the field's lowest default budget (ten turns).
  Approval as serialisable run state is the most practical nested-HITL
  answer outside LangGraph. Raising on an unknown tool by default is
  the one place it sides against "tool error is data".
- **Claude Agent SDK.** Not a framework but a harness exposed as a
  library: the loop, permissions, hooks, subagents, compaction and
  sessions of Claude Code. Its per-tool parallelism-by-annotation and
  its classifier permission mode are ideas worth stealing; its twenty
  hooks are a product constraint, not a library design.
- **Google ADK.** The clearest separation of "LLM decides" (`LlmAgent`)
  from "code decides" (`SequentialAgent`, `ParallelAgent`, `LoopAgent`),
  with shared session state as the glue. The ticket-shaped long-running
  tool is a clean run-boundary HITL. A2A-native.
- **Microsoft Agent Framework.** Semantic Kernel plus AutoGen, merged:
  typed graph workflows with superstep checkpoints and five named
  orchestrations, a harness agent, three middleware points, Go in
  preview. The most LangGraph-like of the vendor SDKs.
- **Genkit.** Google's other one, Go 1.0 in 2026: flows, a Developer
  UI, and tool interrupts as the HITL primitive, with `restart` and
  `resumed` metadata that weft's `Approve` mirrors.
- **Eino.** ByteDance's Go framework: components, three orchestration
  shapes, automatic stream concatenation and merging, five callback
  aspects. Graph-first, battle-tested inside TikTok, thinly adopted
  outside it.
- **fantasy.** The public Go loop under Crush: providers, `StopWhen`,
  step callbacks. The closest prior art to weft's core, and the
  reason weft can say what it does differently: seams instead of
  callbacks, contract tests, and no product coupling.
- **smolagents.** A thousand-line teaching harness whose `CodeAgent`
  writes Python instead of JSON tool calls; the agency ladder in its
  docs (processor, router, tool call, multi-step, multi-agent, code
  agent) is the best one-table definition of "how agentic".
- **CrewAI.** Roles and crews; twenty iterations and two retries by
  default; the fastest multi-agent prototype and the least explicit
  loop semantics.
- **LlamaIndex.** Agents as event-driven workflows over a retrieval
  stack; three agent flavours over one step engine.
- **Inngest AgentKit.** Networks with a router on a durable event
  engine: orchestration is a function, durability is inherited.
- **Letta.** The MemGPT lineage: memory blocks in context, archival
  memory outside it, the agent editing its own memory through tools.
  A memory architecture more than a loop design.
- **Cloudflare Agents.** Each agent an addressable, hibernating
  Durable Object with SQL: durability by identity, not by journal.
- **Strands.** AWS's model-driven SDK: the plain cycle, tool errors as
  error results, lifecycle hooks around invocation, model and tool,
  and budgets in three currencies (turns, output tokens, total tokens)
  each with its own stop reason. Its multi-agent shapes (agents as
  tools, swarm, graph) were not verified at depth here.
- **Agno.** Agents, teams and workflows behind an "AgentOS" runtime.
  Its four team modes are the cleanest small taxonomy of delegation
  in the field: `coordinate` (decompose, delegate, synthesise),
  `route` (one specialist answers directly), `broadcast` (everyone
  gets the same task, results synthesised), `tasks` (a task-list loop
  until the goal is met). The first three are orchestrator-workers,
  routing and voting from Chapter 19; the fourth is loop-until-done.

### What the wider field adds to the convergence list

Three items join Chapter 16's list once these are counted:

9. **Per-tool parallel safety is declared, not inferred.** Claude Code
   reads MCP's `readOnlyHint`; weft reads `Sequential()`; Pydantic AI
   reads `sequential=True`. Nobody infers it from the handler.
10. **The child's final text is the tool result; the child's spend
    rolls up.** OpenAI, Claude, Pydantic AI, DeerFlow and weft agree.
11. **The budget that matters in production is money or tokens, not
    steps.** Claude's `max_budget_usd`, Pydantic AI's `UsageLimits`,
    DeerFlow's token budget, weft's planned `UsageLimit`.

And one new divergence: whether an unknown tool name is data (weft,
Vercel AI SDK, Claude) or a bug that fails the run (OpenAI's default).
weft's position is that the model, not the process, is the party that
can fix a hallucinated name.

## 18. Harnesses: what sits around the loop

A *harness* is the product around a loop: the tools, permissions,
context management, sessions and extension points that make a loop
usable for hours at a time. Claude Code, Codex CLI, OpenCode, Gemini
CLI, Goose, pi, Crush, OpenHands and the DeepSeek Harness are all
harnesses; the Claude Agent SDK and the OpenHands SDK are harnesses
exposed as libraries; T3 Code and Omnigent are control surfaces that
drive several harnesses at once. The distinction matters for weft
because the module map in THE-END-GOAL puts the loop in `weft` and the
harness in `runtime`, and every harness feature has to be assigned to
one side of that line.

The features every serious harness has grown, and where each lives in
weft's design:

| Harness feature | What it does | Example | weft home |
|---|---|---|---|
| Permission model | decides which tool calls run without asking | Claude Code modes and allow rules; Crush's six-step pipeline; pi's refusal | `mw.Allow`, `RequireApproval`, the approval boundary (core); signed decisions (runtime) |
| Context compaction | summarises history when the window fills | Claude Code auto-compact with `compact_boundary` and `PreCompact`; pi's structured skeleton | `runtime` (TODO §14), spec in Chapter 7 |
| Sessions | persist and resume a transcript, fork it | Claude Code resume/fork with a session store; pi's JSONL tree | `store` and `runtime.Session` |
| Subagents with isolated context | keep exploration out of the main window | Claude Code's `Agent` tool; DeerFlow's `task` | `Subagent` (core), pool (runtime) |
| Project instructions | re-injected every request so compaction cannot lose them | CLAUDE.md, AGENTS.md | `Instructions` plus `PromptSnippet` (core) |
| Skills and commands | capability packages loaded on demand | Claude Code skills with `disable-model-invocation`; Genkit Go Agent Skills | prompt assets in `prompts/`; not a core concern |
| Hooks | deterministic side effects at lifecycle points | Claude Code's shell hooks | two seams and one tap (core); shell hooks are a `runtime` adapter over them |
| Tool search / deferred tools | load tool schemas on demand | Claude's Tool Search Tool, MCP schemas deferred by default | `ToolSource` (core) is the seam; the search tool itself is user code |
| Sandbox | contain what a tool can touch | Claude Code OS sandbox; E2B, Firecracker VMs; Crush has none by design | `runtime.SandboxFS` |
| Steering and interruption | talk to the run while it runs | pi's `steer` / `followUp`; Claude Code's Esc and rewind | TODO §5.16, delivery between tool batch and model call |
| Checkpoint and rewind | restore code and conversation to a point | Claude Code checkpoints per prompt | `store` records; file snapshots are the harness's job |
| Verification loop | a check the agent runs until it passes | Claude Code `/goal`, Stop hooks (overridden after eight blocks), reviewer subagents | `StopWhen` on a verifying tool; evaluator agents (Chapter 19) |
| Loop kinds | who starts and stops a run | Claude Code's turn-based, goal-based, time-based and proactive loops; dynamic workflows that write their own orchestration | a run is one turn; goal and schedule loops are `runtime` and `cli` concerns over `Generate` |
| Budget in money | stop at a spend | Claude Code `max_budget_usd` | `UsageLimit` (TODO §5.3); price tables are `ops` |

Two lessons from reading the harnesses side by side. First, the
harness features that survive are the ones that manage *context* and
*permission*; everything else is UI. Anthropic's own best-practices
page states the constraint that drives all of it: "the context window
is the most important resource to manage." Second, the trend toward
harness-as-a-library and control surfaces means a loop library is
increasingly judged by whether a harness can be built on it without
forking it, which is the standard weft's seams are designed to meet.

---

# Part IV — Above the loop: patterns, context, tools, safety, protocols

Parts II and III answered the questions inside one run. This part
covers what the literature and the harnesses have learned about the
layer above: how runs are composed into workflows, how the model's
context is managed, how tools are designed, how the whole thing is
kept safe, which protocols it speaks, how it survives, and how it is
measured. Each chapter ends with where the idea lands in weft.

## 19. Workflows and agent patterns

### The taxonomy

Anthropic's "Building effective agents" (December 2024) fixed the
vocabulary most of the field now uses. Two definitions first.
**Workflows** are "systems where LLMs and tools are orchestrated
through predefined code paths"; **agents** are "systems where LLMs
dynamically direct their own processes and tool usage." The building
block of both is the **augmented LLM**: a model with retrieval, tools
and memory it actively uses. Five workflow patterns sit between a
single call and a full agent:

| Pattern | Shape | When |
|---|---|---|
| **Prompt chaining** | sequential calls, each on the previous output, with programmatic gates between | the task decomposes into fixed subtasks; trade latency for accuracy |
| **Routing** | classify the input, dispatch to a specialised path | distinct categories that want different prompts, tools or models |
| **Parallelization** | run calls at once and aggregate; *sectioning* (split the work) or *voting* (same work, several opinions) | speed, or confidence from several perspectives |
| **Orchestrator-workers** | a central model breaks the task down and delegates dynamically | subtasks cannot be predicted in advance |
| **Evaluator-optimizer** | one model generates, another critiques, loop | clear evaluation criteria and measurable refinement |

The page's three recommendations are the ones this document keeps
returning to: keep it simple and add agency only when simpler
solutions fall short; make planning visible; and invest in the
agent-computer interface "as much effort as human-computer interface
design", including the poka-yoke example of requiring absolute file
paths so relative-path mistakes cannot happen.

Claude Code's dynamic workflows (2026) restate the same five in the
vocabulary of a harness that writes its own orchestration per task:
classify-and-act (routing), fan-out-and-synthesize (sectioning),
adversarial verification (evaluator-optimizer with a fresh context so
the grader is not the author), generate-and-filter and tournament
(voting with different approaches), and loop-until-done. The write-up
names the three failure modes these exist to counter: "agentic
laziness", "self-preferential bias" and "goal drift". The same team's
"Loop engineering" post sorts loops by what starts and stops them:
turn-based (a prompt starts it, the model decides it is done),
goal-based (`/goal`, re-checked by an evaluator after every turn
until a verifiable criterion holds or a turn limit hits), time-based
(`/loop`, `/schedule`), and proactive (event-driven with no human in
real time), with the advice to "start with the simplest solution."

Two other framings are worth holding alongside it. smolagents' agency
ladder puts systems on a spectrum by how much the model's output
controls the program: processor, router, tool call, multi-step agent,
multi-agent, code agent. Simon Willison's one-line definition, "an
LLM agent runs tools in a loop to achieve a goal", is the same thing
compressed, with the caveat that the loop is bounded and the goal may
come from another model. OpenAI's "A practical guide to building agents" adds
the split between a **manager** pattern ("agents as tools": a central
agent coordinates specialists through tool calls, and the edges of the
graph are tool calls) and a **decentralized** pattern ("agents handing
off to agents": peers transfer execution one way, taking the latest
conversation state with them). Its advice on when to add a second
agent is concrete: "maximize a single agent's capabilities first", and
split only for *complex logic* (prompts full of conditional branches)
or *tool overload*, where it notes that "some implementations
successfully manage more than 15 well-defined, distinct tools while
others struggle with fewer than 10 overlapping tools." It also names
what every orchestration needs, a **run**: "a loop that lets agents
operate until an exit condition is reached", with the common exits
being a final-output tool, a reply without tool calls, an error, or
the maximum number of turns, which is Chapter 4's list. And it argues
against declarative graph DSLs in favour of "familiar programming
constructs", the same conclusion Chapter 11 reached from the
LangGraph, DeerFlow and Mastra evidence.

Harrison Chase's reply on behalf of the graph camp ("How to think
about agent frameworks") is the strongest counter-argument and worth
reading with it: "the hard part of building reliable agentic systems
is making sure the LLM has the appropriate context at each step",
most frameworks are *abstractions* that hide what reaches the model
rather than *orchestration* layers that expose it, and production
systems blend workflows and agents rather than choosing. weft agrees
with the diagnosis and answers it with a loop that hides nothing
(every `ModelRequest` is visible at the model seam, every result in
the transcript) rather than with a graph.

### Building each on weft

The point of a small core is that every pattern is ordinary Go code,
which is "own your control flow" (Factor 8, Chapter 27) stated as a
library policy.

- **Prompt chaining** is two `Generate` calls in sequence, with a Go
  `if` between them as the gate. Nothing in weft is needed beyond the
  transcript being a value you can pass along.
- **Routing** is a cheap agent with `Output[Route]` followed by a Go
  `switch` over the typed result, or, once `PrepareStep` lands, one
  agent whose tool set and instructions change per step.
- **Parallelization** is a goroutine per `Generate` and an `errgroup`.
  This is the one place `errgroup` semantics are right: the runs are
  independent, so a first failure cancelling the rest is a policy
  choice, not a category error as it would be for a step's tool calls
  (Chapter 5). Voting is the same fan-out with a reducer.
- **Orchestrator-workers** is `Subagent` plus `Parallelism`: the
  orchestrator's model issues several `research` calls in one step
  and the loop runs them together, events nested and usage rolled up.
- **Evaluator-optimizer** is two agents in a `for` loop: the
  generator returns text, the evaluator has `Output[Verdict]`, and Go
  decides when to stop. Or one agent with a `review` tool and
  `StopWhen(HasToolCall("accept"))`, which is the same loop with the
  model holding the counter.

The design consequence: weft has no workflow DSL and does not plan
one, because the DSL would re-implement `if`, `for` and `go` with
worse error handling. Where a graph is genuinely wanted, Microsoft's
functional-versus-graph table is the honest comparison: native
control flow wins for pipelines, loops and ad-hoc parallelism; a
graph wins when the shape must be inspected and checkpointed by
something other than the program.

## 20. Reasoning and action patterns from research

The research lineage behind "call the model, run the tools, repeat",
and what each result means for a loop's design.

- **ReAct** (Yao et al., 2022) interleaves reasoning traces with
  actions so that reasoning can plan actions and actions can ground
  reasoning; it beat prior methods by 34 and 10 absolute points on
  ALFWorld and WebShop. Every tool-calling loop is ReAct with the
  "thought" moved into either visible text or a provider reasoning
  block. weft stores the reasoning block and never reads it; the loop
  is the action half.
- **Plan-and-Solve** (Wang et al., 2023) has the model "first devise a
  plan to divide the entire task into smaller subtasks, and then carry
  out the subtasks." In a loop this is a planning step before the
  first tool call. The mechanisms are a plan tool the model calls and
  then reads back, a `PrepareStep` that injects the plan, or a harness
  feature such as Claude Code's plan mode and task tracking.
- **Reflexion** (Shinn et al., 2023) has the agent verbalise what went
  wrong and store the reflection in episodic memory for the next
  attempt; it reports 91% pass@1 on HumanEval against GPT-4's 80%.
  The loop shape is *retry with memory*: a run fails, a reflection is
  written by a second call, and the next run starts with the
  reflection in its transcript. weft's `RunError.Result` is the input
  to that reflection; the memory store is user code.
- **Self-Refine** (Madaan et al., 2023) is the same idea within one
  task: generate, critique, refine, with one model in all three roles,
  about 20 points absolute improvement across seven tasks. It is the
  evaluator-optimizer workflow with the roles collapsed.
- **Tree of Thoughts** (Yao et al., 2023) and **LATS** (Zhou et al.,
  2023) branch. ToT explores several reasoning paths with lookahead
  and backtracking (Game of 24: 4% with chain-of-thought, 74% with
  ToT); LATS runs Monte Carlo tree search over ReAct trajectories with
  reflections as the value signal (92.7% pass@1 on HumanEval). For a
  loop library the requirement is that *branching is cheap*: a
  transcript must be a plain value that can be copied and run
  forward independently, with ids that tell the branches apart. weft's
  `Messages(res.Messages...)` plus `RunID` is exactly that; the search
  policy is user code.
- **CodeAct** (Wang et al., 2024) replaces JSON tool calls with
  executable Python as the action space, reporting up to 20% higher
  success across seventeen models; smolagents' `CodeAgent` is the
  production form, and Anthropic's programmatic tool calling
  (intermediate results processed in a code environment instead of the
  context window) reports a 37% token reduction on complex research
  tasks. In loop terms the pattern is one tool, `execute_code`, whose
  sandbox is the entire safety story. weft's answer is that
  `execute_code` is a `RawTool` behind `runtime.SandboxFS`; the loop
  does not need to know that the tool is an interpreter.
- **Voyager** (Wang et al., 2023) grows a skill library of verified
  executable programs and re-uses them, reaching milestones up to
  15.3× faster than prior work. The loop-level requirement is a tool
  set that changes during the run, which is `ToolSource`.
- **SWE-agent** (Yang et al., 2024) showed that the agent-computer
  interface itself, file viewers with bounded windows, concise
  feedback, linting guards, is a first-order variable: 12.5% on
  SWE-bench at the time, far above non-interactive baselines. Chapter
  22 takes this up.

What the lineage says about the loop: none of these patterns needs a
new primitive. They need a bounded loop, tool errors as data, a
transcript that is a copyable value, a tool set that can change, and a
sandbox. That is the list a core must get right so that the patterns
can be written above it.

## 21. Context engineering and memory

### The constraint

The context window is finite and, worse, its quality degrades before
it fills. Anthropic's context-engineering guide names the effect
**context rot**: "as the number of tokens in the context window
increases, the model's ability to accurately recall information from
that context decreases." Claude Code's own best-practices page makes
it the first rule: "the context window is the most important resource
to manage." **Context engineering**, as distinct from prompt
engineering, is "the set of strategies for curating and maintaining
the optimal set of tokens during LLM inference", and it is iterative:
the curation happens at every step. Harrison Chase puts the same point as the thesis of his framework
essay: the hard part "is making sure the LLM has the appropriate
context at each step." In weft terms it is everything that decides
what goes into `ModelRequest.Messages` and `ModelRequest.System` at
step *n*.

### Four verbs

LangChain's summary of the technique space, write, select, compress,
isolate, is a good index:

| Verb | Techniques | In the studied systems | In weft |
|---|---|---|---|
| **Write** (save outside the window) | scratchpads, structured note-taking, memory files, task lists | Claude Code task tracking; Anthropic's file-based memory tool; Letta's memory blocks | a tool the agent calls; nothing in the core |
| **Select** (pull in what is needed) | just-in-time retrieval, memory selection, RAG over tools, deferred tool loading, skills loaded on demand | Claude's Tool Search Tool (~77K → ~8.7K tokens before work begins, an 85% reduction, and accuracy 49% → 74% on one model); MCP schemas deferred by default in the Agent SDK; skills as short descriptions with full content on invocation | `ToolSource` for the changing set; `RawTool` for the search tool; `PrepareStep` for per-step selection |
| **Compress** (keep fewer tokens) | compaction, tool-result clearing, trimming, response formats | Claude Code auto-compaction with `compact_boundary` and `PreCompact`; tool result clearing as "one of the safest, lightest touch forms of compaction"; pi's fixed summary skeleton | `runtime` compaction spec (Chapter 7); `MaxResultBytes` as the crude first cut |
| **Isolate** (split across contexts) | subagents with fresh windows, sandboxes holding token-heavy objects, state schemas exposing fields selectively | subagents returning "a condensed, distilled summary (often 1,000–2,000 tokens)"; Claude Code's advice to "use subagents for investigation" | `Subagent` with a fresh transcript; sandboxes in `runtime` |

### Compaction, carefully

Chapter 7 gave the reference algorithm (trigger on provider-reported
usage, cut only at turn boundaries, structured summary, drop or
regenerate signed reasoning). Two things the harnesses add. Claude
Code re-injects project instructions on every request precisely
because "compaction replaces older messages with a summary, so
specific instructions from early in the conversation may not be
preserved"; the durable place for rules is outside the transcript,
which in weft is `Instructions` and `PromptSnippet`, never the first
user message. And a compaction is a cache-prefix reset: every token
after the cut is a cache write, not a read, so its timing is an
economic decision as well as a quality one.

### Memory architectures

Memory is what survives a run. The shapes in the literature:

- **MemGPT / Letta**: operating-system tiers. A small *core* memory
  lives in the context as editable blocks (persona, user), *recall*
  memory is searchable conversation history, *archival* memory is an
  external store; the model moves information between tiers by
  calling memory tools, "similar to how operating systems handle
  interrupts." The loop-level requirement is that memory edits are
  ordinary tool calls whose effects show up in the next step's system
  or messages.
- **Generative Agents** (Park et al., 2023): a memory stream of
  natural-language records, retrieved by a weighted score of recency,
  importance and relevance, with periodic *reflection* that
  synthesises records into higher-level insights and *planning* that
  reads them. The retrieval formula is the reusable part.
- **The episodic / semantic / procedural split** used by most memory
  products: what happened (episodes), what is true (facts, often a
  temporal knowledge graph such as Zep's Graphiti with `valid_at` and
  `invalid_at` on every edge), and how to do things (skills,
  Voyager's library). Each has a different write path and a different
  retrieval query.
- **Structured note-taking**: Anthropic's phrase for an agent keeping
  a persistent file it re-reads, the cheapest working memory there is,
  and the basis of Claude Code's task lists and the memory tool.

weft's position is that memory is a set of tools plus a `PrepareStep`
or system-prompt composition, all user code or `mem` adapters, and
that the core's job is only to make the transcript a value that a
memory system can read after the run (`RunResult.Messages`) and to
keep `Instructions` stable so the memory has somewhere to land.

### Prompt caching as a design constraint

Chapter 3 closed with the cache-prefix rule. Stated as design
guidance for any loop: keep tools and system byte-identical across
steps; append, never rewrite, messages; put anything that varies per
request (timestamps, per-user context) *after* the stable prefix;
avoid changing the thinking configuration mid-run; expect a
compaction or a `ToolSource` change to be a full re-write of the
cache; and read `cache_read_input_tokens` to confirm hits. The
adapters own the breakpoints (Anthropic's up to four explicit
`cache_control` markers, or automatic caching on the last block); the
loop's contribution is determinism, which it already has.

## 22. Tool design: the agent-computer interface

A tool is the model's only sense organ and only hand. The design of
its name, schema, description and result text is a first-order
variable in agent quality, and the field has converged on concrete
rules.

### The rules, with their evidence

- **Consolidate.** Do not wrap every endpoint. "Instead of
  implementing `list_users`, `list_events`, and `create_event` tools,
  consider implementing a `schedule_event` tool which finds
  availability and schedules an event" (Anthropic, "Writing effective
  tools"). Fewer, higher-level tools reduce the ambiguity that
  "bloated tool sets" create.
- **Namespace.** Prefix by service and resource (`asana_search`,
  `asana_projects_search`) so the model can find the right tool among
  many.
- **Return meaning, not plumbing.** Prefer names to opaque ids in
  results; offer a `response_format` parameter with `"concise"` and
  `"detailed"` values, which cut token use by about two-thirds in
  Anthropic's tests.
- **Be token-efficient by default.** Pagination, range selection,
  filtering and truncation with sensible defaults; Claude Code caps a
  tool response at 25,000 tokens. weft's `MaxResultBytes` (64 KiB with
  a visible marker) is the same rule at the loop level, and pi's
  "50 KB and 2,000 lines, the rest to a file" is the tool-author
  version.
- **Make errors actionable.** An error result should tell the model
  what to do next, "not opaque codes". weft's `INVALID_INPUT: tool
  "weather": field "days": expected integer, got string` names the
  field for that reason; `ToolError{Code, Message}` gives the model a
  branchable code and a readable instruction.
- **Name parameters unambiguously** (`user_id`, not `user`); this one
  change "dramatically improved performance" on SWE-bench Verified in
  Anthropic's account. Describe the tool as you would to a new
  colleague. Add examples where a schema cannot express the
  convention: tool-use examples raised accuracy from 72% to 90% on
  complex parameter handling in Anthropic's advanced tool-use work.
- **Poka-yoke the arguments.** Require absolute paths; make the
  mistake impossible rather than documented.
- **Evaluate tools like prompts.** Build realistic multi-call tasks,
  run them in a loop, and measure runtime, number of calls, tokens and
  tool errors, not just accuracy.

### Annotations: what a tool says about itself

MCP's tool annotations are the field's vocabulary for the properties
a loop needs to know without reading the handler: `readOnlyHint`
(no environment mutation), `destructiveHint` (may perform destructive
updates), `idempotentHint` (repeating with the same arguments has no
additional effect), `openWorldHint` (interacts with external
entities). The spec is explicit that clients "MUST consider tool
annotations to be untrusted unless they come from trusted servers".
Each one maps to a loop policy:

| Annotation | Loop consequence | weft |
|---|---|---|
| `readOnlyHint` | safe to run in parallel with siblings | the default; `Sequential()` is the opt-out |
| `destructiveHint` | ask before running | `RequireApproval()` |
| `idempotentHint` | safe to re-run after a crash | `Replay(ReplaySafe)`; the default is `ReplayNever` |
| `openWorldHint` | results are untrusted content | Chapter 23; a `WrapTools` middleware can tag or strip |

weft's tool options are therefore the annotation set with the loop
consequences attached, and the manifest (`weft.json`) is the place a
reviewer sees them. The MCP result model (a `content` list, optional
`structuredContent` validated against an `outputSchema`, and
`isError`) is richer than weft's single string; the gap is closed by
`RawTool` on import and by the adapter that will expose weft tools
over MCP (TODO §7).

### The interface is the product

SWE-agent's result, that a file viewer with a bounded window, a
linting guard on edits and concise command feedback moved the number
more than the model did, and Anthropic's decision to ship a
`ToolSearch` tool rather than a bigger tool list, both say the same
thing: the tools are the agent's user interface, and the loop's job
is to make their contract explicit (schema from the struct, pinned
error text, capped results) so that the interface can be designed on
purpose.

## 23. Safety and control

### The threat that is not solved

Prompt injection is unsolved at the model layer. Simon Willison's
**lethal trifecta** names the combination that turns it from a
nuisance into a breach: an agent that has (1) access to private data,
(2) exposure to untrusted content, and (3) a way to communicate
externally. With all three, "LLMs follow instructions in content", so
text an attacker planted in a web page or a tool result can steer the
agent into exfiltrating the data. His verdict on filter products that
catch "95% of attacks": "95% is very much a failing grade." The
working strategy in 2026 is **containment**: assume some injections
land and make sure a landed one cannot do much. Anthropic's agentic
misalignment study adds a second reason for the same posture: under
sufficiently misaligned incentives, "models often disobeyed direct
commands to avoid such behaviors", so instructions are not a control.

### The controls, and where each attaches

| Control | What it is | Attaches in weft |
|---|---|---|
| **Least privilege tool sets** | give each agent only the tools its task needs; scope subagents tighter than the orchestrator | the tool list is per agent; `Subagent` children carry their own |
| **Input guardrails** | classify the user's request before the run: relevance, safety, PII | user code before `Generate`; `PrepareStep` later |
| **Tool guardrails** | check arguments before execution, results after | `WrapTools`: `mw.Allow` for fixed policy, a classifier middleware for judgement, `mw.MapErrors` to strip causes |
| **Output validation** | check the final answer's shape and content | `Output[T]` with validation in the submit tool; a stop condition that refuses |
| **Approval gates on irreversible actions** | a human or a policy model decides per call | `RequireApproval`; Claude Code's `auto` mode is the policy-model variant, one middleware in weft |
| **Sandboxing** | the boundary that actually contains: containers, gVisor, Firecracker microVMs (E2B), ephemeral VMs (Vercel Sandbox, Modal) | `runtime.SandboxFS` for files; process isolation is deployment |
| **Signed decisions** | approvals in a client-held transcript can be forged | `runtime` HMAC (the AI SDK lesson) |
| **Audit** | every call, with cause, outside the transcript | `mw.Audit`, `Tap`, OTel spans later |
| **Untrusted-content tagging** | mark tool results from the open world so the model and the middleware treat them as data | `openWorldHint` on import; a middleware that wraps results in delimiters; never in the core |
| **Kill switches and budgets** | stop the process, cap the spend | `WEFT_MODEL_REQUESTS=deny`, `MaxSteps`, `UsageLimit` |

OpenAI's practical guide gives the checklist for
the first three rows and frames guardrails as "a layered defense
mechanism": a *relevance classifier* (off-topic input), a *safety
classifier* (jailbreaks and prompt injections), a *PII filter* on
output, *moderation*, *tool safeguards* that rate every tool low,
medium or high "based on factors like read-only vs. write access,
reversibility, required account permissions, and financial impact"
and use the rating to pause for checks or escalate to a human,
*rules-based protections* (blocklists, length limits, regex), and
*output validation*. Its tool-safeguard rating is the same
information MCP's annotations carry (Chapter 22) and weft's
`RequireApproval` consumes. The guide also names the two triggers
for handing control to a person: "exceeding failure thresholds"
(retry or action limits, which in weft are `MaxModelRetries` and
`MaxSteps` surfacing as run errors) and "high-risk actions" such as
cancelling orders, authorising large refunds or making payments,
which is what `RequireApproval` is for.

### Stated plainly

ADR 0007's sentence remains the honest one: approval is "a policy and
UX seam, not a security boundary." The boundary is the sandbox and the
tool set. A loop library's contribution to safety is to make the
policy seams obvious (two of them), the audit complete (every call,
every cause), the budgets real (failures, not warnings), and the
transcript the truth (nothing hidden, nothing silently repaired), so
that the containment built above it has something solid to stand on.

## 24. Protocols: MCP, A2A, AG-UI, ACP

The 2026 protocol stack layers as: A2A for agent to agent, MCP for
agent to tools and data, AG-UI for agent to user interface, and ACP
(Agent Client Protocol) for editor to agent. Each maps onto a part of
the loop.

**MCP** standardises what a tool is: `name`, `description`,
`inputSchema`, optional `outputSchema`, annotations; `tools/list` with
pagination and a `listChanged` notification; `tools/call` returning
content or `isError`. Two client-side features matter for a loop.
**Elicitation** lets a server ask the user for structured input mid
call (`accept`, `decline`, `cancel`), which is human-in-the-loop
nested inside a tool (Chapter 10). **Sampling** lets a server ask the
client's model to generate, which is a subagent inverted. In weft the
mapping is: an MCP tool is a `RawTool` (schema given, arguments
verbatim); `listChanged` is a `ToolSource` refresh; annotations become
`Sequential`, `RequireApproval` and `Replay`; elicitation is a
blocking tool unless the runtime maps it to a parked call; and the
spec's instruction that there "SHOULD always be a human in the loop
with the ability to deny tool invocations" is the approval boundary.

**A2A** standardises the *other agent*: a signed Agent Card
advertising skills and capabilities (streaming, push notifications),
a Task with eight states (`SUBMITTED`, `WORKING`, `COMPLETED`,
`FAILED`, `CANCELED`, `INPUT_REQUIRED`, `AUTH_REQUIRED`, `REJECTED`),
messages made of parts, results returned as artifacts, and updates by
polling, SSE streaming or webhooks. In weft an A2A agent is a
`Subagent` whose handler speaks HTTP: `INPUT_REQUIRED` is a pending
call, `FAILED` is `SUBAGENT_FAILED`, the artifact is the tool result,
and the streaming updates are `Nested` events. Exposing a weft agent
over A2A is a `serve` concern.

**AG-UI** standardises the event stream a UI consumes, and its
vocabulary is close enough to weft's to map almost one to one:

| AG-UI | weft |
|---|---|
| `RunStarted` / `RunFinished` / `RunError` | `RunStart` / `RunFinish` / the error from `Events` |
| `StepStarted` / `StepFinished` | `StepStart` / `StepFinish` |
| `TextMessageStart` / `TextMessageContent` / `TextMessageEnd` | `TextDelta` (message framing is per step) |
| `ToolCallStart` / `ToolCallArgs` / `ToolCallEnd` / `ToolCallResult` | `ToolArgsDelta` while the model writes, then `ToolStart` and `ToolFinish` |
| `StateSnapshot` / `StateDelta` / `MessagesSnapshot` | no state protocol in the core; `RunResult.Messages` is the snapshot |
| `Raw` / `Custom` | none; the event set is sealed |

The open question in TODO §10, whether `serve` speaks AG-UI or a
bespoke stream, is therefore mostly a naming exercise plus the
message-framing events weft does not emit; the ordering guarantee
(`Seq`) is stronger than AG-UI requires.

**ACP** (the editor protocol used by Zed and OpenHands; not IBM's
identically abbreviated agent-communication protocol) plugs an agent
into an editor canvas. It is a `serve` adapter over the same stream.

The rest of the ecosystem is conventions rather than protocols:
`AGENTS.md` for repository instructions, Agent Skills for
progressive-disclosure capability packages, and MCP Apps for tools
that return interactive UI. None touches the loop.

## 25. Durable execution: the four models

Chapter 13 covered what weft defers and what it provides. This chapter
compares the models under which the field makes a loop survive a
process death, because the choice constrains how a loop must be
written.

| Model | How it survives | Constraint on the loop | Examples |
|---|---|---|---|
| **Event-sourced checkpoint** | persist state after each superstep; on restart, reload the last checkpoint and re-execute the interrupted node | nodes must be re-runnable from the top; side effects before an interrupt repeat | LangGraph, Microsoft Agent Framework, DeerFlow |
| **Workflow snapshot** | serialise the whole run's state when it suspends | snapshot size and format churn; only suspended runs are stored | Mastra |
| **Replay by determinism** | journal every side effect (each model call, each tool call is an *activity* or *step*); on restart, re-run the workflow code and substitute journaled results | workflow code must be deterministic; every side effect must sit behind the journal; "steps are tried at least once but are never re-executed after they complete" (DBOS); long histories roll over (Temporal's continue-as-new) | Temporal, DBOS, Restate, Inngest, Vercel Workflow DevKit |
| **Stateful object** | the agent *is* an addressable object with its own storage that hibernates and wakes | state lives in the object; concurrency is per object | Cloudflare Agents (Durable Objects), Letta's server-side agents |

Three things follow for any loop library that wants to sit on more
than one of these.

1. **Every side effect needs an id before it happens.** The replay
   model keys journal entries by position or id; the checkpoint model
   keys state by run and step. weft mints `RunID` before the first
   event, child ids as `<parent>/<call>`, and call ids come from the
   provider, so both models have their keys.
2. **The model call and the tool call are the activity boundaries.**
   Those are exactly weft's two seams, which is why a Temporal or DBOS
   adapter is a pair of middlewares: `WrapModel` to journal the stream
   and `WrapTools` to journal each call, with the loop itself
   remaining deterministic given the journal.
3. **Re-execution safety is a per-tool fact.** pi's `replay: never |
   safe`, MCP's `idempotentHint`, and weft's `Replay(ReplaySafe)` are
   the same annotation; DBOS's "never re-executed after completion"
   is the engine-level default that makes the annotation unnecessary
   for steps that finished and necessary for the one that was in
   flight.

The transcript-as-state design (Factor 12, the stateless reducer, in
Chapter 27) is what lets weft stay neutral: a run is a pure function
from `(agent, input transcript, decisions)` to `(result, events)`,
so any of the four models can hold the input and replay the output.

## 26. Evaluation

A loop without a measurement is a demo. What the field measures, and
how.

- **Outcome versus trajectory.** Anthropic's research team found
  end-state evaluation ("whether agents achieved the correct final
  state rather than validating every intermediate step") the most
  useful, judged by a single LLM call with a rubric producing a 0.0
  to 1.0 score across factual accuracy, citation accuracy,
  completeness, source quality and tool efficiency; that single-call
  judge "proved most consistent with human judgment." τ-bench takes
  the same stance by comparing final database state to an annotated
  goal state.
- **Reliability, not just accuracy.** τ-bench's `pass^k` metric
  measures success across *k* independent trials of the same task.
  Its headline: state-of-the-art function-calling agents "succeed on
  <50% of the tasks, and are quite inconsistent (pass^8 <25% in
  retail)". A loop that passes once is not a loop that passes.
- **The reference sets.** SWE-bench Verified for coding agents,
  Terminal-Bench for shell agents, GAIA for general tool use,
  WebArena for browsers, LongMemEval for memory. They tell you the
  harness is not broken; a golden set mined from your own traces
  tells you it works for you.
- **Traces as tests.** DeerFlow's practice, Claude Code's `/goal` and
  reviewer subagents, and pi's evals package all converge on
  recording real runs and replaying them as regression tests.

In weft the pieces are `wefttest.Script` for deterministic offline
runs, `wefttest.Golden` for pinned bytes, the conformance suite per
adapter, the manifest as a drift detector, and, later, an `eval`
module that runs recorded traces through `weft test`. The event
stream's replayability (Chapter 8) is what makes traces-as-tests
possible without a second recording format. The metric that comes out
of an eval suite is also the input to prompt optimisation (DSPy, GEPA),
which is why the stack's order is traces, then a golden set, then
evals in CI, then optimisation.

## 27. The twelve factors, mapped

HumanLayer's "12-Factor Agents" is the most-cited practitioner
checklist for reliable LLM applications. Each factor, and what weft
does about it:

| Factor | Meaning | weft |
|---|---|---|
| 1. Natural language to tool calls | the model's job is to emit structured calls | `ToolCallPart`; schema from the struct |
| 2. Own your prompts | no framework-hidden prompt text | `Instructions` and `PromptSnippet` are the only system text; `submit_output`'s description is the one pinned exception, and it is documented |
| 3. Own your context window | you decide what the model sees each step | the transcript is a value; `PrepareStep` planned; compaction in `runtime`, never silent |
| 4. Tools are just structured outputs | a tool call is data the program acts on | `ToolCallPart` is a value; `Agent.CallTool` lets you dispatch it yourself |
| 5. Unify execution state and business state | one state, not a shadow copy | the transcript plus `Pending` is the whole run state |
| 6. Launch, pause, resume with simple APIs | pausing is ordinary | a run ends; `Messages(...)` plus `Approve`/`Deny` resumes |
| 7. Contact humans with tool calls | asking a person is a tool call | `RequireApproval` parks a call; an `ask_user` tool is the same shape |
| 8. Own your control flow | orchestration is your code | no DSL; workflows are Go (Chapter 19) |
| 9. Compact errors into the context window | errors are data, kept small | tool errors as coded results; `MaxResultBytes` |
| 10. Small, focused agents | narrow scope beats generality | agents are cheap values; `Subagent` composes them |
| 11. Trigger from anywhere | any transport can start or resume a run | `Generate` is a function; `serve` adds HTTP later |
| 12. Make your agent a stateless reducer | `(state, input) → (state, output)` | `execute` is that function; the `Agent` value holds no run state |
| 13 (bonus). Pre-fetch context you might need | do not discover gaps mid-run | `Messages` and `FilePart` on the input; `PrepareStep` for per-step injection |

The mapping is nearly free because the factors and weft's ADRs were
derived from the same evidence: what broke in production when the
loop was allowed to hide state.

---

# Part V — Architecting an agent library

## 28. The principles, with the evidence behind each

1. **The loop is forty lines; sell the rules, not the loop.** Every
   framework in Part III has the same loop. What differs is the answers
   to Part II. Write those answers down before the code (weft's ADRs).
2. **One error rule.** Tool error is data; run error is an error. Every
   retry, hint, approval and budget hangs off this distinction
   (THE-END-GOAL's "Phase-1 blocker").
3. **Budgets fail, stop conditions succeed.** Never let a runaway look
   like a success. Ship bounded defaults.
4. **Decide the three orderings separately**: start, result, event. Then
   assign sequence numbers under the same lock that emits.
5. **Cancellation is a cause, not a budget.** No tool starts after it;
   nothing is emitted after it; the partial transcript is the record.
6. **Make the transcript always valid input.** Repair on the way in,
   visibly. Never append an empty turn.
7. **Pin the bytes the model sees.** Result text, schema shape,
   transcript shape are contract. A change is an ADR and a test.
8. **Two seams, one tap.** Behaviour attaches around the model call and
   around the tool call; observation attaches once. Refuse the third
   hook every time; write down why.
9. **Approval is a run boundary in a stateless core.** Persistence buys
   interrupts; do not fake them without it. Say plainly that approval is
   policy, not security.
10. **Delegation is a tool.** Add usage attribution and lineage, stream
    the child's events, and guard cycles. Do not add a handoff
    primitive or a graph until three real users ask.
11. **Ids first.** Runs, calls, children. Retrofitting ids is the
    Mastra 1.0 story.
12. **Additive forever after 1.0.** A gate in CI (`apidiff`), no
    renames, sealed enums, and a changelog that lists model-visible
    changes first. The AI SDK's reputation ding is breaking-change
    fatigue; Pydantic AI's is a flipped default.
13. **Imports point down.** The core depends on nothing above it, and
    today on nothing at all; the OTel API package is the one
    sanctioned addition. A feature "for the UI" that lives in the core
    is a smell.
14. **Say no in writing.** The playbook's "things that look like
    improvements and are not" is as important as the ADRs.
15. **Pin behaviour with scriptable tests and pinned bytes.** A fake
    Model that plays a script (`wefttest`), a conformance suite per
    adapter with declared capabilities, a golden manifest in CI, and
    `-race` on every run. If a rule in this document matters, it has a
    test name; the chapters name them.

## 29. The edge-case catalog

Use this as the test plan for any agent loop. weft's `contract_test.go`
covers the first four groups today; Chapter 11's list covers the fifth. Chapters 19 to 26 add the layer above the loop; their checks are workflow-level and belong in an `eval` suite, not the contract tests.

**Stopping**
- Model stops with no tools → success.
- Model wants tools after the last step → budget error, transcript on the error.
- Cancel during the last step's tools → cancellation error, not budget.
- `max_tokens` on text → success with `StopReason` recorded.
- `max_tokens` with calls → every call failed, none executed, loop continues.
- Stop condition on a tool call → fires on the issuing step; on a result → custom condition.
- Zero-step run (only approvals resolved).

**Tool failure**
- Handler error, handler panic, middleware panic → error results; siblings finish.
- Unknown tool, undecodable args, unknown field under strict → coded results naming the field.
- Timeout with a cooperative handler; timeout with a hung handler; run ctx expiring during a tool.
- Huge result → capped on a rune boundary with a marker; per-tool cap of 0 lifts it.
- A `*ToolError` with a cause → cause reachable through the seam, absent from the transcript.

**Concurrency and events**
- Four parallel tools → starts in call order, results in call order, `Seq` monotone.
- A barrier tool in the middle of a batch.
- `Parallelism(1)` sets the provider hint.
- Cancel before a slot is acquired → error result, no events.
- Every `ToolFinish` has a prior `ToolStart`; the parked call is the only start without a finish.
- Taps see the same order as the stream; a panicking tap is dropped.
- Wire round trip of every event type; unknown type rejected; fuzz stable.

**Transcript and approval**
- Dangling call fed back → synthesised interrupted result.
- Orphaned result, double tool message → dropped.
- Empty assistant turn → not appended; reasoning-only turn → appended.
- `RequireApproval` → run ends pending, siblings ran, `RunFinish.Pending` set.
- Resume with approve, deny, and no decision → three distinct visible results, in call order, on the right message.
- Middleware parking a call → same as `RequireApproval`.
- Cancel while pending → cancellation error with `Pending` on the result.
- `CallTool` on a gated tool → `ErrApprovalRequired`.

**Subagents**
- Nested event order and `Seq`; grandchild double wrapping; wire round trip of `nested`.
- Usage total and per-call map; failed child's partial usage counted.
- Child run error → `SUBAGENT_FAILED` data; parent continues.
- Child pending → `SUBAGENT_PENDING`.
- Cycle via `ToolSource` → `SUBAGENT_CYCLE`.
- Parent cancel → child stops; subagent `Timeout` → "timed out" result.
- `Output[T]` child → submitted JSON.
- Lineage id on the nested `RunStart`; `subagent` field in the manifest.
- Subagent under `RequireApproval` → the delegation itself is gated.
- Two subagent calls in one step under `Parallelism(4)` → both children interleave under the parent's lock.

### The harness that runs the catalog

`wefttest` is the fake Model that plays these scripts offline:
`Script(Say("..."), ToolCalls(Call{Name, Args}), Fail(err),
MaxTokens())` composes a run's turns, usage is deterministic so token
accounting is assertable, and it ignores the kill switch on purpose so
tests never depend on an environment flag. `wefttest.Golden` pins files
byte for byte with a hand-rolled diff and owns the `-update` flag.

Each adapter runs `wefttest/conformance.Run`: thirteen cases from
`text_only` through `cancel_mid_stream`, `max_tokens`, `idle_timeout`
and `kill_switch` to `never_contract_violation`, served from recorded
SSE fixtures (`FixtureServer`) or a stalling server (`StallServer`).
Capability gaps are declared in `Caps{Reasoning, Files, Sequential,
Usage, Live}` and fail loudly when an untested claim ships; live runs
against real keys sit behind a build tag, never in CI. The fuzzers
(`FuzzRepair`, `FuzzUnmarshalEvent`, `FuzzUnmarshalMessage`,
`FuzzToolInvokeArgs`) run on a CI time budget. `make test` is
`go test -race ./...`, and a flaky test is treated as a design race,
not a test problem.

## 30. How to read weft after this document

| To understand | Read | Then the test |
|---|---|---|
| The loop | `loop.go` `execute`, `execTools`, `callTool` | `contract_test.go` |
| The phases | `docs/life-of-a-call.md` | |
| Messages and repair | ADR 0001, `message.go`, `repair.go` | `TestRepair*` |
| Errors | ADR 0002, `errors.go` | pinned-bytes tests |
| Tools and schema | ADR 0003, `tool.go`, `schema.go`, `docs/tool-schema-design.md` | `schema_test.go` |
| Events and ordering | ADR 0004, `events.go`, `run.go` | `TestStreamEventOrdering`, `TestEventJSONRoundTrip` |
| Seams | ADR 0006, `agent.go` (`WrapModel`), `tool.go` (`WrapTools`), `mw/` | `seams_test.go` |
| Approval | ADR 0007, `run.go` (`Approve`/`Deny`), `loop.go` (`resolvePending`) | `ExampleRequireApproval` |
| Structured output | ADR 0008, `output.go` | `output_test.go` |
| Adapters | ADR 0013, `wefttest/conformance` | conformance suites |
| Subagents | TODO §5.1, this document §10–§12, ADR when written | to be added with the implementation |
| The model turn, adapters, thinking | ADR 0013, `model.go`, `openai/`, `anthropic/`, `google/` | `wefttest/conformance` |
| The `mw` reference set | ADR 0006, `mw/` | `mw` tests |
| Testing the loop | `wefttest/`, `wefttest/conformance` | `conformance.Run` |
| The manifest | ADR 0012, `manifest.go`, committed `weft.json` | `TestManifestIsStable` |
| What is decided and why | `docs/PLAYBOOK.md` §3 and §9 | |
| The full path of a run | this document §3, `run.go`, `loop.go` | `contract_test.go` |
| The layer above the loop | this document Part IV; `../AGENTIC-STACK-2026.md` | `eval` (later) |

### The manifest: the agent as a diffable artifact

`weft.Manifest(agents...)` renders `weft.json`: per agent its name,
model identity, instructions, and policy (`parallelism`, `max_steps`,
`max_result_bytes`, `stop_when` by name); per tool its description,
input and output schemas, per-tool policy flags, and the `file:line`
the tool was defined at. It is generated from code, committed, diffed
in CI, and regenerated with `go test ./... -update`. It is output,
never input; the playbook lists reading it back to build agents as an
anti-pattern, because the code is the config. Its value is drift
detection: a review that shows a schema change shows a model-visible
change, which is rule 7's evidence arriving on its own. Stop
conditions print their names (`has_tool_call:submit`,
`step_count_is:3`) because the built-ins implement `fmt.Stringer` for
exactly this file.

## 31. Glossary

- **Activity.** In replay-based durable execution (Temporal, DBOS), a journaled side effect the workflow can substitute on replay. In a loop, the model call and the tool call.
- **Agent card.** A2A's signed manifest advertising an agent's skills and capabilities.
- **Agent-as-tool.** A child agent wrapped as a tool the parent's model can call. weft: `Subagent`.
- **Barrier.** A tool that waits for in-flight siblings, runs alone, then lets the step resume. weft: `Sequential()` on a tool.
- **Budget.** A limit whose breach is a failure. weft: `MaxSteps`, `UsageLimit`, `DetectLoops`, `MaxModelRetries`.
- **Checkpoint.** A persisted snapshot of loop state that a later process can resume from. Not in weft's core; `store`.
- **Compaction.** Replacing older transcript history with a summary to free context; a cache-prefix reset and a potential loss of early instructions.
- **Containment boundary.** The point outside the tool chain where panics and timeouts become results.
- **Context engineering.** Deciding, at every step, which tokens the model sees; the four verbs are write, select, compress, isolate.
- **Context rot.** The measured decline in a model's recall as its context grows, before the window is full.
- **Guardrail.** A check on input, tool arguments or output that can deny or rewrite; a policy, not a boundary.
- **Handoff.** Transferring the conversation to another agent that continues on the same transcript. Not a weft primitive.
- **Harness.** The product around a loop: tools, permissions, context management, sessions and extension points. weft's `runtime` is the harness layer.
- **Idle timeout.** The adapter's limit on the gap between stream chunks (default 60 s); expiry is `ErrStreamIdle`. Bounds silence, never slowness.
- **Interrupt.** LangGraph's HITL: an exception carrying a namespace, caught at the root, resumed by re-executing the node.
- **Kill switch.** `WEFT_MODEL_REQUESTS=deny` fails every model call with `ErrModelRequestsDenied` before any network I/O; checked per call, never cached.
- **Lane.** pi's unit: one session, several named model configurations over a shared tree.
- **Lethal trifecta.** Private data, untrusted content and external communication in one agent; the combination that makes prompt injection a breach.
- **Lineage.** The parent-to-child relation between runs. weft: `<parentRunID>/<callID>` and the `Nested` wrapper's `CallID`.
- **Manifest.** `weft.json`, generated from code and committed: names, instructions, policy, schemas, source locations. Output, never input.
- **Nested event.** A child run's event wrapped for the parent stream with the parent's `Seq`.
- **Pending call.** A tool call the run ended on without executing, awaiting a decision.
- **Prompt snippet.** A paragraph a tool contributes to the system prompt via `PromptSnippet`, composed after `Instructions`. How a tool states its own conventions.
- **Raw tool.** A tool with an explicit schema and a raw-JSON handler (`RawTool`): no struct, no decode errors. The import path for foreign tools.
- **Reasoning block.** One provider thinking block, closed by its signature; stored as one `ReasoningPart` and never read by the core.
- **Repair.** Making a transcript valid input by synthesising missing results and dropping orphans, visibly.
- **Run boundary.** The HITL model where the run ends and a later run resumes with the transcript plus decisions.
- **Seam.** Where behaviour attaches: around the model call, around the tool call.
- **Seq.** The per-run counter that totally orders concurrent tool events.
- **Steering.** User input delivered mid-run, between the current tool batch and the next model call. Planned (TODO §5.16); pi calls the two modes `steer` and `followUp`.
- **Step.** One model call plus its tool executions.
- **Stop condition.** The intended, successful end of a run.
- **Structured output tool.** The hidden `submit_output` tool that `Output[Out]()` registers; its non-error result is the `output_submitted` stop condition.
- **Superstep.** LangGraph's unit: all triggered nodes run, then writes merge. An agent round is two.
- **Tap.** The observation point: sees every event, changes nothing.
- **Tombstone.** A recorded outcome for a canceled or interrupted turn, so the record shows what happened rather than an absence.
- **Tool source.** A function supplying the advertised tool set, fetched fresh each step (`ToolSource`). The seam for registries that change mid-run.
- **Trajectory.** The full sequence of steps a run took; evaluated either by its end state or step by step.
- **Truncated call.** A tool call in a `max_tokens` reply. Failed, never executed.

## 32. Sources and further reading

Everything about weft is from the repository. The framework deep
dives are in `../docs/frameworks/`. The external sources below were
read for this document; entries marked *(not re-verified)* were used
from memory or an earlier reading and should be checked before being
quoted.

**Foundational essays**
- Anthropic, "Building effective agents" (Dec 2024): https://www.anthropic.com/engineering/building-effective-agents
- Anthropic, "How we built our multi-agent research system": https://www.anthropic.com/engineering/multi-agent-research-system
- Anthropic, "Effective context engineering for AI agents": https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- Anthropic, "Writing effective tools for agents": https://www.anthropic.com/engineering/writing-tools-for-agents
- Anthropic, "Advanced tool use" (tool search, programmatic tool calling, tool-use examples): https://www.anthropic.com/engineering/advanced-tool-use
- Anthropic, "Agentic misalignment": https://www.anthropic.com/research/agentic-misalignment
- OpenAI, "A practical guide to building agents" (PDF, 2025): https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf
- Anthropic, "Loop engineering: getting started with loops": https://claude.com/blog/getting-started-with-loops
- Anthropic, "A harness for every task: dynamic workflows in Claude Code": https://claude.com/blog/a-harness-for-every-task-dynamic-workflows-in-claude-code
- Harrison Chase, "How to think about agent frameworks": https://www.langchain.com/blog/how-to-think-about-agent-frameworks
- HumanLayer, "12-Factor Agents": https://github.com/humanlayer/12-factor-agents
- LangChain, "Context engineering for agents": https://www.langchain.com/blog/context-engineering-for-agents
- Thorsten Ball, "How to build an agent" (Amp): https://ampcode.com/how-to-build-an-agent
- Simon Willison, "The lethal trifecta for AI agents": https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/
- Simon Willison, "I think 'agent' may finally have a widely enough agreed upon definition": https://simonwillison.net/2025/Sep/18/agents/
- Mario Zechner, pi build post-mortem: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/

**Framework and SDK documentation**
- Claude Agent SDK, overview: https://code.claude.com/docs/en/agent-sdk/overview
- Claude Agent SDK, agent loop: https://code.claude.com/docs/en/agent-sdk/agent-loop
- Claude Agent SDK, hooks: https://code.claude.com/docs/en/agent-sdk/hooks
- Claude Code best practices: https://code.claude.com/docs/en/best-practices
- Anthropic prompt caching: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- OpenAI Agents SDK, running agents: https://openai.github.io/openai-agents-python/running_agents/
- OpenAI Agents SDK, tools and approvals: https://openai.github.io/openai-agents-python/tools/
- OpenAI Agents SDK, handoffs: https://openai.github.io/openai-agents-python/handoffs/
- OpenAI Agents SDK source, `run_config.py` (`DEFAULT_MAX_TURNS`): https://github.com/openai/openai-agents-python/blob/main/src/agents/run_config.py
- Google ADK, agents: https://adk.dev/agents/
- Google ADK, loop agents: https://adk.dev/agents/workflow-agents/loop-agents/
- Google ADK, function tools: https://adk.dev/tools-custom/function-tools/
- Microsoft Agent Framework overview: https://learn.microsoft.com/en-us/agent-framework/overview/agent-framework-overview
- Microsoft Agent Framework workflows: https://learn.microsoft.com/en-us/agent-framework/concepts/workflows/
- Genkit interrupts: https://genkit.dev/docs/interrupts/
- Eino overview: https://www.cloudwego.io/docs/eino/overview/
- fantasy (Charm): https://github.com/charmbracelet/fantasy
- smolagents, "What are agents?": https://huggingface.co/docs/smolagents/conceptual_guides/intro_agents
- CrewAI agents: https://docs.crewai.com/en/concepts/agents
- LlamaIndex agents: https://developers.llamaindex.ai/python/framework/understanding/agent/
- LlamaIndex human-in-the-loop: https://developers.llamaindex.ai/python/framework/understanding/agent/human_in_the_loop/
- Strands agent loop: https://strandsagents.com/docs/user-guide/concepts/agents/agent-loop/
- Agno teams: https://docs.agno.com/basics/teams/overview
- Inngest AgentKit: https://agentkit.inngest.com/
- Letta: https://docs.letta.com/
- Vercel AI SDK, Pydantic AI, LangGraph, Mastra, Crush, DeerFlow, pi: see the deep dives in `../docs/frameworks/`

**Protocols**
- MCP tools (2025-06-18): https://modelcontextprotocol.io/specification/2025-06-18/server/tools
- MCP elicitation: https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation
- A2A specification: https://a2a-protocol.org/latest/specification/
- AG-UI events: https://docs.ag-ui.com/concepts/events
- Agent Client Protocol: https://agentclientprotocol.com

**Durable execution**
- Temporal AI cookbook: https://docs.temporal.io/ai-cookbook
- DBOS workflows: https://docs.dbos.dev/python/tutorials/workflow-tutorial
- Restate, Inngest, Vercel Workflow DevKit, Cloudflare Agents: see `../AGENTIC-STACK-2026.md` §5 and §1. Restate's durable-agents guide returned 404 at the time of writing, so nothing specific to it is claimed above.

**Research**
- ReAct (Yao et al., 2022): https://arxiv.org/abs/2210.03629
- Plan-and-Solve (Wang et al., 2023): https://arxiv.org/abs/2305.04091
- Reflexion (Shinn et al., 2023): https://arxiv.org/abs/2303.11366
- Self-Refine (Madaan et al., 2023): https://arxiv.org/abs/2303.17651
- Tree of Thoughts (Yao et al., 2023): https://arxiv.org/abs/2305.10601
- LATS (Zhou et al., 2023): https://arxiv.org/abs/2310.04406
- CodeAct (Wang et al., 2024): https://arxiv.org/abs/2402.01030
- Voyager (Wang et al., 2023): https://arxiv.org/abs/2305.16291
- MemGPT (Packer et al., 2023): https://arxiv.org/abs/2310.08560
- Generative Agents (Park et al., 2023): https://arxiv.org/abs/2304.03442
- SWE-agent (Yang et al., 2024): https://arxiv.org/abs/2405.15793
- τ-bench (Yao et al., 2024): https://arxiv.org/abs/2406.12045

**Landscape**
- `../AGENTIC-STACK-2026.md`: the September 2026 survey of frameworks, harnesses, durability, sandboxes, memory, observability, protocols, gateways and security, with links.
- `../THE-END-GOAL.md` and `../GOAL-REVIEW-2026-09.md`: the vision and its review.
