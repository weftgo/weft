# ADR 0016 — Observability: the loop's own reporting

- Status: decided (2026-09-19, TODO §8); amended 2026-09-22 (usage
  split attributes, TODO §2a.4 — the note at the end)
- Implementation plan: `docs/phase2-observability-plan.md`; every open
  decision it marked **Guess** is recorded here (the O-register below).
- THE-END-GOAL: the core's rent list includes "OTel spans"; principle 5
  sanctions the OTel API as the core's **one** dependency ("a global
  no-op tracer unless an SDK registers — that is its design"); the ops
  bar is NestJS-Observe's — instrumentation hooks in the core via the
  OTel API, no-op until an exporter registers, exporters themselves in a
  satellite.
- Amends: ADR 0004 (the tap amendment's "OTel and slog attach through
  it"), ADR 0005 (the release version constant).

## Context

TODO §8 asked for two things: OTel API spans (8.1) and an slog `Logger`
option (8.2), both sketched as "implemented as a Tap". The tap sketch
does not survive contact with the code, for three reasons visible in
`loop.go`:

1. **A tap cannot decorate a context.** Taps receive the *run's* ctx
   inside `execute`'s `deliver` wrapper; the tool's context is built
   later, on the tool goroutine, from `withNest(withCall(...))`. A tool
   span must be *in* that context so a handler's own spans — an HTTP
   client's, a database driver's, a child run's `invoke_agent` — parent
   under it. Only the loop can put it there.
2. **A tap never sees the end of a cancelled run.** Nothing is delivered
   after cancellation, to taps or sinks (ADR 0004). A tap that opened a
   span on `RunStart` would never close it: the span leaks in the SDK's
   processor and the trace shows a run that never ended — for the
   cancelled-run case, which is the common hung-tool case.
3. **Durations need a clock.** A tap computing durations keeps per-run
   and per-call start times in maps keyed by ids, cleaned on the finish
   event — the same leak as (2), plus a mutex inside an observer that
   must be fast.

## Decision

**Spans and log lines are the loop's own reporting, at the five phases
every run passes through** — run begin/end, model call, tool call —
emitted by one unexported `observer` (in `observe.go`) built at `New`
from two new options:

```go
func TracerProvider(tp trace.TracerProvider) Option
func Logger(l *slog.Logger) Option
```

The observer is **not a third seam**: it takes no user function, sees no
request or result content, changes nothing, and is not on either
middleware chain. It is the loop reporting its own phases the way `emit`
reports events. `Tap` keeps its contract exactly — the *user's*
observer, in emission order, contained; a tap's ctx now carries the run
span, so a tap that starts its own spans parents them correctly for
free. ADR 0004's sentence "store, OTel (§8.1) and slog (§8.2) attach
through it" is amended to: *"`store` attaches through it; OTel and slog
are the loop's own reporting (ADR 0016)"*.

Spans and lines are two outputs of the same calls — one clock, one
outcome — so their ids, names, durations and outcomes cannot disagree,
and a future third output (metrics) has one place to attach.

### The dependency (the core's one)

`go.mod` gains `go.opentelemetry.io/otel` v1.46.0 and nothing else.
Packages imported: `otel` (in `agent.go`, the global provider),
`otel/trace`, `otel/attribute`, `otel/codes`, and
`otel/semconv/v1.41.0` — **v1.41.0 is the newest semconv package in
v1.46.0 that carries the GenAI group** (v1.42.0 and v1.43.0 dropped the
unstable group; verified by symbol count: 44 GenAI symbols in v1.41.0,
zero in the two after). Pin it; when the group stabilises, moving is an
amendment to this ADR. Transitive closure (go.sum):
`go.opentelemetry.io/otel/metric`, `go.opentelemetry.io/auto/sdk`,
`github.com/go-logr/logr`, `github.com/go-logr/stdr`,
`github.com/cespare/xxhash/v2` — one module plus indirects for one
API (`golang.org/x/sys` appears only in `examples/otel`'s `go.sum`,
the SDK's). Nothing in weft imports the OTel SDK, an
exporter, or semconv's metric packages; the SDK appears only in
`examples/otel`, its own module in `go.work`.

**Zero-config holds by the API's own design**: the tracer is resolved
once per agent at `New` from `otel.GetTracerProvider()` — the global
delegating provider — so an SDK registered *after* the agent was built
is still picked up. A program that sets up an SDK gets weft's spans with
no weft option at all; `TracerProvider(tp)` exists for tests and
dependency-injected programs, which never touch the global.

### The spans

Names follow the GenAI semantic conventions, not the TODO's
`weft.run`/`weft.step`/`weft.tool` sketch (O4): every backend the field
uses (Langfuse, Logfire, otel-tui, Datadog's LLM view) renders
`chat {model}` and `invoke_agent {name}` from the operation name, and
`weft.step` would render as an anonymous span everywhere. The TODO's
three-span count is kept; the middle span is the model call, because a
step (model call *plus* tool execution) has no semconv shape and a
`chat` span containing tool time would mis-measure
`gen_ai.client.operation.duration` for every consumer. The step index is
an attribute on both child spans. A fourth `weft.step` span grouping a
step's `chat` and `execute_tool`s was considered and rejected for v1
(no renderer needs it; the Inspector groups by `StepFinish`).

| Span | Name | Kind | Set at start | Set at end |
|---|---|---|---|---|
| run | `invoke_agent <agent>` (or `invoke_agent`) | Internal | `gen_ai.operation.name`, `gen_ai.agent.name` (named agents), `gen_ai.provider.name`, `gen_ai.request.model`, `weft.run.id` | `gen_ai.usage.input_tokens/output_tokens` (run totals, subagents included), `weft.run.steps`, `weft.run.pending` (when > 0), `weft.run.stop_reason`; status `Ok`, or `Error` + recorded exception + `error.type` |
| model call | `chat <model>` | Client | `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `weft.run.id`, `weft.step.index` | `gen_ai.usage.*` (this call's), `gen_ai.response.finish_reasons`, `weft.stop.raw` (when set), `weft.model.tool_calls`; status `Ok` or `Error` + exception + `error.type` |
| tool call | `execute_tool <tool>` | Internal | `gen_ai.operation.name`, `gen_ai.tool.name`, `gen_ai.tool.call.id`, `weft.run.id`, `weft.step.index`, `weft.tool.seq`, `weft.tool.approved` (resumed calls) | `weft.tool.result_bytes` (the capped length the model sees) or `weft.tool.pending=true` (parked) or `Error` status + `error.type`; the result text is never on the span |

Placement, wired in `loop.go`:

- **Run** (`execute`, first lines after `withAncestry`): started before
  `RunStart` is emitted, ended by every exit — `endRun` with the result
  on success, `fail` with the `*RunError` — so cancellation ends the
  span with the ctx error even though no event is delivered. A panic
  nothing contains — a PrepareStep function is arbitrary user code;
  model, tool, and tap panics are contained further down — is caught by
  a guard that ends the span (`error.type run_panicked`) and re-panics,
  so the crash still reaches the caller and the span still closes. The
  ctx it returns carries the span: every chat, execute_tool and tap
  below parents under it.
- **Model call** (around `consume`): started after `PrepareStep` has
  built the request (a PrepareStep function rewrites the request; it is
  not the model call), ended when the stream is consumed, before the
  contract check's outcome becomes a run error. `mw.Log`, `mw.Retry`,
  `mw.Fallback` run *inside* it — one span per step measures the chain's
  outcome. `ModelInfo` is the agent's, i.e. the model that was *asked*
  (middleware forwards Info); the model that *answered* after a fallback
  is not observable from the loop and is not invented (O9).
- **Tool call** (`execTools`' goroutine): started on the call's context
  after it is built and before the chain runs — just after
  `ToolStart` is emitted, so `weft.tool.seq` carries the event's Seq
  (the write happens on the dispatching goroutine before the goroutine
  launch, so the read is race-free) — so `SpanFromContext` in
  a handler is the tool span, and a `Subagent` handler's
  `child.execute` starts its `invoke_agent` under it (the tree mirrors
  `Nested` by construction) — ended with the call's outcome before
  `ToolFinish` is emitted, so no span operation runs under `emitMu`.
  Resumed calls (`Approve`) run through the same path and carry
  `weft.tool.approved`; denied and undecided calls produce no span (they
  never execute); `Agent.CallTool` produces no span either — manual
  dispatchers own their context and their reporting (O10).

**Provider names** (O5): `ModelInfo.Provider` maps by a three-row table
— `openai`→`openai`, `anthropic`→`anthropic`, `google`→`gcp.gemini`
(the semconv well-known values) — everything else verbatim (`wefttest`,
a third-party adapter's own name). Adapters do not learn about OTel.

**Finish reasons**: `gen_ai.response.finish_reasons` carries weft's
`StopReason` names verbatim (`stop`, `tool_calls`, `max_tokens`) — they
are the wire vocabulary (ADR 0002); the vendor's raw reason rides
`weft.stop.raw` exactly as on `StepFinish.Raw`.

**Error types** (O6): for run and model errors, `error.type` is
`context.Canceled`/`context.DeadlineExceeded` for cancellation, a
snake_case token per named sentinel — `error.type` is a
low-cardinality identifier backends group on, so never the sentinel's
text — and `model_error` otherwise; the vendor error's text rides the
recorded exception, not the type. The tokens (pinned by
`TestSpansErrorTypes`, table `errorTypes` in `observe.go`):

| Sentinel | `error.type` |
|---|---|
| `ErrMaxSteps` | `max_steps` |
| `ErrUsageLimit` | `usage_limit` |
| `ErrModelContract` | `model_contract` |
| `ErrLoopDetected` | `loop_detected` |
| `ErrModelRetriesExceeded` | `model_retries_exceeded` |
| `ErrDuplicateTool` | `duplicate_tool` |
| `ErrNilTool` | `nil_tool` |
| `ErrNoOutput` | `no_output` |
| `ErrStreamIdle` | `stream_idle` |
| `ErrUnsupported` | `unsupported` |
| `ErrModelRequestsDenied` | `model_requests_denied` |
| `errRunPanicked` (the loop's own, unexported) | `run_panicked` |

Tool error results: the `ToolError` code (`ORDER_NOT_FOUND`), `timeout`
for a timed-out call (the loop's timeout error is a typed error with
the pinned model-visible string, `toolTimeoutError`),
`context.Canceled`/`context.DeadlineExceeded` when the run's
cancellation reached the handler (the same name the run span reports),
`tool_error` otherwise; status description is empty and no exception is
recorded — the result text is model-visible content the model may
recover from.

**No content** (O7): no prompt text, message content, tool arguments,
tool results, or instructions ever reach a span — ids, names, counts,
durations, reasons, and error *types* only. Error *text* is the one
non-identifier: a run or model failure's error is the span's recorded
exception and status description (a vendor error may quote the
request; that is the vendor's message, not weft's capture), and the
log lines carry the error text a tool or model returned — a log is the
caller's. Content capture
(`gen_ai.input.messages` etc.) is a later, opt-in option with its own
redaction question; recorded here as not-built.

Attributes are set only when `span.IsRecording()`, so the no-SDK path
pays `Start`/`End` alone. There is no "is an SDK registered?" fast path:
the API offers none that is not a private-type check, and a
non-recording span is the API's design for exactly this case.

### The cost, measured (2026-09-19, Ryzen 7 7800X3D, go1.26.0)

| Operation | ns/op | B/op | allocs/op |
|---|---|---|---|
| `observer.run` start+end, no SDK, Debug off | 218 | 368 | 6 |
| `observer.model` start+end, no SDK, Debug off | 227 | 400 | 6 |
| `observer.tool` start+end, no SDK, Debug off | 235 | 416 | 6 |
| `BenchmarkGenerate` before (worktree at the pre-§8 commit) | 17,634 | 14,834 | 197 |
| `BenchmarkGenerate` after (the same run: 1 run + 2 model + 4 tool spans) | 24,501 | 18,184 | 251 |

≈ 7.7 allocations and ≈ 1 µs per span on the default path — under the
plan's bar of ten per span kind (pinned by `TestObserverNoopAllocations`),
noise beside one JSON marshal of the transcript, three orders of
magnitude under one model call. No goroutine, no lock, no background
flush: the observer runs on the calling goroutine of each phase; the
tool phase runs on the tool's goroutine, which already exists.

### The log lines

`Logger(l)` (nil: `slog.Default`, resolved **at log time** — a program
that calls `slog.SetDefault` after building its agents is honoured, O13;
`slog.DiscardHandler` turns the lines off outright). Five lines, all
`Debug`, written with `LogAttrs` on the span-carrying context, so a
handler that bridges to OTel correlates lines with spans through
`Handle`'s ctx with no weft code (L6). `Enabled` is checked before any
attribute is built (L1).

| Point | msg | Keys |
|---|---|---|
| run start | `run start` | `run`, `agent` (when named), `provider`, `model` |
| run end, success | `run finish` | `run`, `steps`, `input_tokens`, `output_tokens`, `stop`, `pending` (when > 0), `dur` |
| run end, error | `run error` | `run`, `step`, `err` (the cause's text — a log is the caller's, unlike a span), `dur` |
| model call end | `model call` | `run`, `step`, `provider`, `model`, then `reason`/`raw`/`input_tokens`/`output_tokens`/`tool_calls`, or `err`; `dur` |
| tool call end | `tool call` | `run`, `step`, `call`, `tool`, `dur`, then one of `result_bytes`; `pending=true`; `err` (the model-visible text) |

No start lines for model and tool calls (O12): a start line doubles the
volume and says nothing the end line does not — it carries `dur`. `run
start` exists because a run that never ends (a hang the caller must
find) should have left a trace. A child run's lines carry the child's
run id (`<parent>/<step>/<callID>`) — the hierarchy is in the id, not
in extra keys. `mw.Log` and `mw.Audit` are unchanged and may coexist:
the core's lines are "what did the loop do" at Debug in every program;
`mw.Audit` is an audit trail at Info with internal causes — an audit
log is a policy decision with a level and a cause the core should not
make, so retiring it into the core is not proposed.

### Line and key stability

Span names, the `gen_ai.*`/`weft.*` keys, their values, and the log
lines' messages and keys are pinned by tests (`otel_test.go`,
`log_test.go`, `TestSemconvConstantsPinned`) and are a contract like
model-visible bytes: changing one is an amendment to this ADR. The
`weft.*` keys and log keys are one named-constant block in `observe.go`
with this table as its doc comment; no inline string keys.

## The O-register (the plan's guesses, as shipped)

| # | Guess | Resolution |
|---|---|---|
| O1 | Spans and lines are the loop's own reporting, not a tap | shipped as guessed; ADR 0004 amended |
| O2 | OTel API in the core; `semconv/v1.41.0` pinned as the newest with `gen_ai` | verified against v1.46.0 and shipped |
| O3 | Per-span cost is a number, not a hope; bar "< 10 allocs, no goroutine, no lock" | measured: 6 allocs, 218–235 ns per span; `BenchmarkGenerate` delta +54 allocs / +7 µs for 7 spans |
| O4 | Semconv span names; no `weft.step` span; the step index is an attribute | shipped as guessed |
| O5 | Three-row provider map; adapters never learn about OTel | shipped as guessed |
| O6 | Tool errors are status + `error.type`, never result text; run errors record the exception and type the sentinel | shipped as guessed; `toolTimeoutError` types the timeout while preserving the pinned string |
| O7 | No content on spans; capture is a later opt-in | shipped as guessed |
| O8 | `TracerProvider` takes the OTel interface; `Logger` takes `*slog.Logger`; nil = the global | shipped as guessed |
| O9 | The `chat` span wraps the chain; the answering model after a fallback is not observable | shipped as guessed; recorded in §"Recorded for later" |
| O10 | `Agent.CallTool` produces no span or line | shipped as guessed; documented on `CallTool` |
| O11 | Root tests use an in-test recording tracer on the `embedded` types; the real SDK only in `examples/otel` | shipped as guessed (~140 lines, `otel_test.go`) |
| O12 | Five log lines, Debug, no start lines for model/tool; `mw.Audit` stays | shipped as guessed |
| O13 | `slog.Default()` resolved at log time; `Logger(l)` once | shipped as guessed |
| O14 | Metrics, content capture, resumed-run links, `CallTool` spans, exporters: recorded, not built | see below |

One reconciliation beyond the plan's text: the plan's §3.5 sketch
`endTool(outcomes[i], parked[i])` cannot produce S4's `error.type` (a
`ToolResultPart` does not carry the code), so `callTool` returns the
chain's error beside the result — folded into the result's text for the
model, typed for the observer. Unexported; no public surface moved.

## Recorded for later, not built

- **Metrics**: `gen_ai.client.token.usage` and
  `gen_ai.client.operation.duration` histograms via the OTel metric API
  — a second dependency surface; when the Inspector or a consumer wants
  dashboards without a tracing backend.
- **Content capture**: `gen_ai.input.messages`, `gen_ai.output.messages`,
  `gen_ai.tool.call.arguments`/`.result` on spans, opt-in, with the
  redaction question (Mastra's `SensitiveDataFilter`, LangGraph's
  `TracePolicy`) answered in its own ADR.
- **The answering model after `mw.Fallback`**: needs `ModelFinish` (or
  `ModelInfo` on the finish) to carry the responder — an additive event
  field, to be decided with the store (§11), which wants it too.
- **`Agent.CallTool` spans** — when a second manual dispatcher besides
  `mcp.Serve` wants them.
- **Span links for resumed runs**: a run resumed with `Approve` is a new
  trace; a link to the parking run's span needs the run-id → span-context
  map only the store can hold.
- **Exporters** (`trace` module, post-v1): OTLP and Langfuse one-liners;
  the module's name is still open in TODO's register.

## Consequences

- The README's "No dependencies" bullet and STATUS's "zero dependencies"
  become "one dependency, the OTel API (this ADR)" — THE-END-GOAL
  principle 5 sanctioned exactly this exception, and the manifest,
  adapters, satellites, and `mw` are untouched by it.
- `make apidiff` reports two additions (`TracerProvider`, `Logger`) and
  nothing else; `AGENTS.md` gains two lines in the `New` block and stays
  one screen.
- Taps receive the span-carrying ctx; a tap that measures or correlates
  may use it, and must not depend on it (the no-SDK path's span is
  non-recording).
- The Inspector (§12) reads events, not spans, but renders the same
  run/step/tool tree; the span names and `weft.run.id` values here are
  the names it should keep agreeing with.

Tests: `TestSpans*` (S1–S10) and `TestSemconvConstantsPinned` in
`otel_test.go` against the in-test recording tracer;
`TestObserverNoopAllocations`/`BenchmarkObserverNoop` in
`observe_test.go`; `TestLogger*` (L1–L8) in `log_test.go` on a capturing
handler; `ExampleTracerProvider`, `ExampleLogger`; the real SDK's tree
in `examples/otel/main_test.go` (`tracetest` in-memory exporter, plus
the global-provider path), all offline under `-race`. The existing
`TestTap*` contract tests pass unchanged.

## Amendment (2026-09-22 — usage split attributes, TODO §2a.4)

`Usage` gained three reporting subsets (`CachedInputTokens`,
`CacheWriteTokens`, `ReasoningTokens`; ADR 0001's wire note records
the fields). The pinned `semconv/v1.41.0` carries all three as GenAI
attributes, so the splits go on spans under their standard names —
`gen_ai.usage.cache_read.input_tokens`,
`gen_ai.usage.cache_creation.input_tokens`, and
`gen_ai.usage.reasoning.output_tokens` — each set only when non-zero,
at the two sites that already set `gen_ai.usage.input/output_tokens`
(`invoke_agent` with the run totals, `chat` with the call's). No
`weft.*` attribute and no fallback: these are the standard names or
nothing. The slog lines gain nothing — the two totals stay the lines'
fields, per the line-stability rule above; the splits are span-only.
(The first pass of the plan believed reasoning tokens had no semconv
attribute; the pinned package has one, and all three splits ride it.)

## Amendment (2026-09-27 — `OnRunEnd`, the fourth observation output; TODO §11, root v0.3.5)

The observation family gains a fourth output: `weft.OnRunEnd(fn)` —
the user's *outcome* observer, the counterpart the loop's own reporting
already has. Spans and log lines report the run's end to backends
(including a cancelled run's end, which no event reports); `Tap`
reports the event stream to Go code; `OnRunEnd` reports the outcome to
Go code — one call per run, on the loop's goroutine, after the run
span has ended and before `Run` returns. `res` is the run's result
(the `RunError`'s partial on failure), `err` the `RunError` or nil; a
child run's fires inside the parent's tool call, like its events; a
panicked run fires nothing (a crash is not an outcome); a panic in an
observer is contained and counted with the tap panics. Not a seam
(ADR 0006's note): it sees, it cannot change. The store's `Record`
(ADR 0010) is the first consumer — the final save needs the failure
signal and the result that no event carries. The taxonomy stands at:
the loop reports (spans, lines); the user observes (Tap for the
stream, OnRunEnd for the outcome); the seams change.

## Amendment (2026-09-27 — the observation accessors; TODO §11, root v0.3.6)

Writing the store's `Record` from a satellite found the one thing
composition cannot reach: `weft.Option` is sealed (ADR 0004 — plugins
attach through seams, not option kinds), so a satellite composes
`weft.Options(weft.Tap(...), weft.OnRunEnd(...))` and never receives
the `*Agent` the option was applied to. Two additive accessors close
the gap, both observation-scoped, neither a seam:

- `weft.AgentFromContext(ctx)` — the head of the ancestry chain the
  cycle guard already keeps on every run context (ADR 0014): the agent
  whose run produced the event an observer received. Nil outside a
  run, so observers must not depend on it. Read-only by construction —
  an Agent is immutable after New. The store uses it to record the
  run's manifest hash (ADR 0010) and to find its logger.
- `(*Agent).Logger()` — the `Logger` option's value or
  `slog.Default()`, the same resolution the run's own lines use, so an
  observer's errors land beside the loop's lines in one sink.

In the same spirit as `CallFromContext` (an accessor over data the
context already carries), both are taps-see-not-change surface. Pinned
by `TestAgentFromContext*` and `TestAgentLogger`.

## Amendment (2026-09-30 — observability data: the Logs API joins the trace API; ADR 0024)

The observability-data programme (ADR 0024, which decides it) widens
this ADR's dependency sentence: "the OTel trace API is the one
dependency" becomes "the OTel trace **and logs** APIs".
`go.opentelemetry.io/otel/log v0.22.0` requires exactly the pinned
`otel v1.46.0` and shares its `attribute` package, so no version moves;
the pre-1.0 risk is confined to `observe.go`, and the move to the
stable line when v1.47.0 is final will amend this note. On top of the
spans, the observer now emits **records** through the Logs API: every
durable event as a log record (`weft.record = event`, `weft.event.type`,
`weft.event.pos` contiguous from 0 per run), the three deltas as records
of kind `delta` on their own counter `weft.delta.pos`, and `messages`
records at five emission points including the run's repaired input —
their concatenation equals `RunResult.Messages` byte-for-byte. A new
run option, `weft.Metadata(map[string]string)`, rides the context, lands
on every span and record of the run (keys under `weft.` are the weft
modules' namespace), and is inherited by subagents; `thread` stamps
`weft.session.id` and `weft.turn` through it.

O7 holds: content is still off by default, and spans still carry none.
`weft/otel`'s `Install` turns capture on, and the core learns "on"
through one standard call at each emission —
`Logger.Enabled(ctx, EnabledParameters{EventName: "weft.messages"})` —
because the global provider delegates `Enabled` faithfully and the SDK
answers true when any processor does. Caps and redaction are
`weft/otel`'s, per destination; the core reads no environment variable
and never truncates content (`weft.StripContent` is the satellite's one
shaping tool). The manifest hash moves onto the run's `invoke_agent`
span and its `run_start` record as `weft.manifest.hash`, computed at
`New` for a named agent — the store computed it per run through
`AgentFromContext`, and the store is deleted (ADR 0010 is superseded by
ADR 0024). The span table above gains the new attribute families (the
linkage, provenance, metadata and override rows) unchanged in meaning;
the full contract, including the record attribute families and the
identity chain, is ADR 0024's.

## Amendment (2026-10-07 — the reporting hook; devtools plan A8)

The loop cannot see inside the model chain: a retry's three tries, a
fallback's second model and an adapter's wire bodies are one `chat`
span to it. Code inside the chain now reports into the loop's record
through one context-carried hook, `weft.ReportFromContext(ctx)
Reporter`, with `Attempt(AttemptInfo)` and `Raw(RawPair)`. The loop
puts the reporter on the context it hands the chain (the `chat` span's
context, one allocation per step); anywhere else — outside a run, a
tool handler's context — it is the zero, no-op `Reporter`. A run
started on a chain's context (a middleware running a child agent)
masks the enclosing reporter at run start, so the child's handlers see
none and its own model calls install their own. The core still imports
nothing above it: `mw` and the adapters call in, the core never calls
out.

It is reporting, not a seam (ADR 0006): `Reporter` is a sealed struct,
not an interface the caller can replace; a report returns nothing,
never blocks, and changes no step, retry, tool call or model choice
(`TestReportingHookChangesNothingModelVisible` pins the transcript,
the event stream, the stop reason and the usage, through real
`mw.Retry` too). A panic out of the tracer or the logger during a
report is contained and counted in `Agent.TapPanics`, the report
dropped; a report made after its model call returned is dropped, best-effort
(no lock: one racing report from a goroutine the chain left behind may
still land under the ended chat span). The
reporter numbers the model call's attempts itself, 1..n in report
order (atomic, so concurrent reports stay unique) — the caller gives
no index. An `Attempt` becomes, with a tracer recording, an `attempt`
span (Client) child of the step's `chat` span — `weft.run.id`,
`weft.step.index`, `weft.attempt.index`, `gen_ai.provider.name`,
`gen_ai.request.model`, `weft.attempt.retry_after_ms` (when asked),
the run's metadata, status `Ok` or `Error` + `error.type` (the chat
span's classification), start and end at the reported times when both
are given and ordered (otherwise neither) — and, with the logger at
Debug, a `model attempt` line (`run`, `step`, `attempt`, `provider`,
`model`, `dur`, `retry_after`, `err`). `Raw` is accepted and
discarded: what the wire bodies become, and under which content
policy, is the request record's decision (ADR 0028), not this
amendment's; until then a Debug `model raw dropped` line carries the
sizes and media type, never the bytes. `mw.Retry` and `mw.Fallback`
report one attempt per provider request — a layer whose inner model
carries the duck-typed marker `ReportsAttempts() bool` (true on
Retry and Fallback, and on any adapter or middleware that reports for
itself, ADR 0013) stays silent, the Unwrap walk bounded at 64 hops —
so `Retry(Fallback)` and `Fallback(Retry)`
both yield exactly one attempt per request. For the first-party
adapters the hook is optional (ADR 0013). Cost under the default
program (no SDK, Debug off), measured 2026-10-07 on the machine above:
`BenchmarkReportAttempt` 2 ns/op outside a run and 16 ns/op inside a
model call, 0 allocs/op both.
