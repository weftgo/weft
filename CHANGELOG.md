# Changelog

Notable changes to weft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project
is pre-1.0 and tags per module (ADR 0005).

## Unreleased (2026-09-18)

### Changed — run & event semantics (read before upgrading)

- **Events carry `RunID`.** Every event except `RunStart` (whose `id`
  is the run's) carries `run_id` on the wire, so taps and stream
  consumers can attribute events under concurrent runs; the per-run
  `Seq` counter was already unique only within its run. Old recordings
  without the field decode with an empty `RunID`.
- **Events are snapshots.** `ToolStart.Args` and `RunFinish.Pending`
  no longer alias the transcript's byte slices: writing into a
  received event cannot corrupt the run. The copies ride tool-event
  frequency, not delta frequency — no benchmark movement.
- **A step consults its `ToolSource` exactly once.** Advertising, the
  sequential barrier, and dispatch resolve against one per-step
  snapshot, so a source that changes mid-step can no longer produce an
  advertised-then-`NO_SUCH_TOOL` failure or a barrier that disagrees
  with the executed def. A tool registered mid-step becomes callable
  on the next step (the per-step refresh `TestToolSource` always
  modelled). A snapshot with a duplicate name now fails the run with
  the new `ErrDuplicateTool` instead of silently resolving
  first-wins; `Agent.CallTool` reports the same condition as an error.
- **Registered tools are frozen at `New`.** The agent keeps a deep
  copy; mutating the value you passed in (fields or schema trees)
  after construction no longer reaches dispatch, advertisement, or a
  running run. `Agent.Tools` returns deep copies too. The "immutable,
  reusable, concurrent" contract now holds by construction.
- **`ModelRequest` hands adapters copies.** `Messages` and `Tools`
  are fresh slice copies per request; a hostile or careless `Model`
  can no longer corrupt the transcript or the agent's tool list at
  slice level. `wefttest`'s mock already cloned both — loop, contract,
  and test double now agree.

### Added

- `Run.Close` releases an abandoned run's resources (idempotent; a
  run you will consume needs no Close — Events and Wait release
  everything themselves).
- `Agent.TapPanics` counts contained tap panics, so a dead observer
  is no longer invisible.
- `Schema.AdditionalProperties` types map values (`map[string]int` →
  an object of integers; `map[string]any` stays a bare object — an
  `any` value type has nothing to say) and rides the wire in the
  manifest and the OpenAI and Anthropic adapters. The Google adapter
  drops it: Gemini's schema subset (and the genai SDK's `Schema`) has
  no `additionalProperties` field. Wire output for schemas without
  typed maps is byte-identical.
- `wefttest.ConformInfo` / `ConformInfoT` check that model middleware
  forwards the inner model's identity (the Info convention, checked).
- `ExampleTap_async`: the supported pattern for slow observers — hand
  each event to a queue inside the tap, drain on your own goroutine.
- `Run.Events` and `Tap` docs now state the consumer-speed coupling:
  tool events are emitted under the step's ordering lock, so a slow
  consumer gates the start of subsequent tools.

### Fixed

- `Repair`'s purity is pinned: the input is never mutated
  (fuzz-checked byte-for-byte), and the synthesis append no longer
  relies on whose backing array the kept tool message uses.

## Unreleased (2026-09-14)

### Changed — model-visible contracts (read before upgrading)

- **A `max_tokens` step with tool calls executes none of them.** Every
  call gets the error result `tool call <name> was not executed: the
  response hit the output token limit` and the loop continues so the
  model retries with a full budget. Previously intact calls ran and
  only cut arguments failed decoding. (ADR 0002 amendment, TODO §5.6a.)
- **The loop's own tool failures are coded.** Undecodable arguments
  render `INVALID_INPUT: tool "x": field "days": expected integer, got
  string` (was `weft: tool input is not valid for its schema: …`) and
  an unknown tool `NO_SUCH_TOOL: no tool named "x"` (was `weft: no tool
  with that name: "x"`). `errors.Is` on the sentinels is unchanged.
- `Sequential` returns `PolicyOption` (source-compatible); `RunFinish`
  gained `Pending` and is no longer `==`-comparable.
- `Agent.CallTool` now runs the tool middleware chain, and applies the
  agent-level `StrictInput` exactly as the loop does.
- A run whose context is canceled while calls are parked for approval
  fails with the cancellation error (the parked calls stay resumable on
  `RunError.Result.Pending`) — cancellation wins, as everywhere else.

### Added

- **The two middleware seams** (ADR 0006): `WrapModel(mw
  ...ModelMiddleware)` and `WrapTools(mw ...ToolMiddleware)` — chi-style,
  first listed outermost; `WrapTools` also works on one tool.
  `ToolCaller`, `ToolMiddleware`, `ModelMiddleware`, `InfoOf`.
- **Package `mw`**, the reference middleware: `Retry` (backoff with
  jitter, retry-after honoured, >60s asks fail fast, overflow never
  retried), `Fallback`/`FallbackWhen`, `Log`, `RepairJSON`; `Allow`,
  `Audit`, `MapErrors`.
- **`ToolError`** — `{Code, Message, Err}` renders `CODE: Message`; the
  cause is for middleware and logs only. `weft.Errorf(code, format,
  ...)`; several `%w` verbs keep every cause reachable.
- **The approval boundary** (ADR 0007): `RequireApproval()`,
  `RunResult.Pending`, `RunFinish.Pending`, `Approve(id)`, `Deny(id,
  reason)`, `Call.Approved`, `ErrApprovalRequired`, `ErrApprovalDenied`;
  `examples/approval`.
- Per-tool policy: `Sequential()` on a tool is a barrier;
  `PromptSnippet(text)` composes into the instructions; `Replay(policy)`
  annotates checkpoint restart (`ReplaySafe`/`ReplayNever`). All in the
  manifest. `Timeout(0)` on a tool removes the agent's default for
  that tool alone (the manifest records `"timeout": "0s"`), the
  timeout analogue of `MaxResultBytes(0)`.
- The apidiff gate (`scripts/apidiff.sh`, `make apidiff`, CI job) with
  the pre-1.0 `.apidiff-allow` acknowledgement file.
- `docs/life-of-a-call.md`: where every phase of a step and a tool call
  sits.

- Per-run reasoning control: `weft.Thinking(weft.ThinkingConfig{...})`
  — an agent option sets every run's default, a run option overrides
  one — on a provider-neutral scale (`ThinkOff/Low/Medium/High`, plus
  an optional token `Budget`). Anthropic maps it to
  `thinking:{disabled|adaptive}` or `budget_tokens`; Gemini to
  `thinkingBudget`/`thinkingLevel` (and asks for thought summaries
  back); the OpenAI adapter sends `reasoning_effort` on the official
  API and injects a `thinking` object for the known gateway hosts
  (z.ai, bigmodel.cn, moonshot.ai/cn), with `openai.Dialect(...)`
  pinning the wire form (`DialectNone` for strict servers). Declared
  gaps: Chat Completions has no off switch or budget form — `ThinkOff`
  and `Budget` are dropped in the effort dialect.
- Streamed tool-argument progress: a new `ToolArgsDelta` event (wire
  type `tool_args_delta`) surfaces argument fragments live while the
  model writes a tool call; the assembled call still arrives as
  `ToolStart` when it executes. OpenAI and Anthropic adapters emit it;
  Gemini calls arrive whole and yield none.
- Per-tool policy: trailing options on `Tool` — `Timeout`,
  `MaxResultBytes`, `StrictInput` — override the agent's defaults for
  that tool alone.
- Structured output: `Output[T]`, `GenerateAs`, and `OutputOf`
  constrain the final answer to a struct via a `submit_output` tool
  (ADR 0008); undecodable tool arguments come back as schema-shaped
  errors naming the field.
- Conformance suite: a `thinking_option` case asserting the option
  threads through the public API without breaking the exchange.

### Fixed

- A canceled stream can no longer fabricate a successful
  `ModelFinish`: all three adapters enforce the Model contract's
  `(nil, ctx.Err())` terminal yield when the caller's context ends
  mid-stream — a canceled run previously could report success with
  partial data.
- anthropic: empty tool outputs travel as a visible "(empty tool
  output)" placeholder, empty user messages as "(empty message)", and
  assistant messages with nothing sendable (only unsigned reasoning)
  are skipped — the Messages API rejects empty content, which failed
  the next model call.
- anthropic: an empty stop reason on a later `message_delta` no longer
  overwrites a real one.
- openai: a streamed safety refusal (`delta.refusal`,
  `finish_reason: content_filter`) is surfaced as text instead of
  being dropped.
- google: a transient failure creating the SDK client on first use is
  retried on the next call instead of being cached forever.
- `StopWhen` conditions (`HasToolCall`, structured output's stop) no
  longer panic when called with an empty step slice.
- mw: `Retry` panicked on a `BaseDelay` below 2ns (jitter computed
  `rand.Int64N(0)`); the backoff now returns such delays unchanged.
- mw: `MaxRetries(0)` kept its documented retry-after behaviour only in
  the docs — the attempt-budget check returned the raw error before the
  fail-fast ran, so `ErrRetryAfterTooLong` was unreachable. The fail-fast
  now outranks the budget.
- mw: the doc.go composition example put `Retry` outside `Fallback`,
  which retries the fallback chain as a whole — the primary gets one
  attempt and its transient failures are never retried. The canonical
  order is `Fallback` outside `Retry` (retry, then fail over).
- all adapters: a chunk landing in the same instant as the idle deadline
  could be reported as `ErrStreamIdle` (select picks uniformly among
  ready cases); a waiting chunk now wins over the timer.
- anthropic: `input_json_delta` of non-`tool_use` blocks (server tools
  such as web_search) leaked as `ToolArgsDelta` progress for a call that
  never arrives; only `tool_use` fragments surface now.
- anthropic: an empty assistant text part and a hand-built tool call
  with nil arguments no longer reach the wire (the Messages API rejects
  empty text blocks and `input:null`; the inbound stream already
  normalised empty arguments to `{}`).

### Changed

- Documentation corrected: the anthropic and openai adapters' zero
  configuration inherits the vendor SDK's transport retry default (2
  retries on 429/5xx/connection errors), and `MaxRetries(0)` cannot
  disable retries — supply a zero-retry client via `Client(c)` if you
  need one. No code changed; the previous docs were wrong.
- Docs: ADR 0013 amended (per-run thinking mappings, argument-delta
  progress, enforced cancellation, empty-content and refusal handling);
  adapter package docs updated to match.

## 0.1.0 — 2026-09-11

Initial public release: the core agent loop (tools from plain
functions, parallel tool execution with defined failure semantics,
typed streaming events, transcript repair, the `weft.json` manifest),
the `wefttest` offline model and conformance suite, and the
openai/anthropic/google adapters.
