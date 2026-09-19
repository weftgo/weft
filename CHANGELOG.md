# Changelog

Notable changes to weft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project
is pre-1.0 and tags per module (ADR 0005).

## Unreleased (2026-09-18)

### Added — usage limits (TODO §5.3, ADR 0002 amendment)

- **`weft.UsageLimit(max weft.Usage)`** bounds a run's total token
  usage, subagents included, checked at the continuation point — only
  when the loop would otherwise make another model call; a run-ending
  step may overshoot and still succeed. Breach fails the run with
  **`ErrUsageLimit`** (wrapped with the numbers) and the partial
  transcript. Off by default; the manifest policy records it
  (`usage_limit`, zero fields omitted).

### Added — subagents as tools (TODO §5.1, ADR 0014)

- **`weft.Subagent(name, description, child, opts...)`** — a tool whose
  handler runs another agent on the prompt alone; the child's events
  arrive in the parent's stream wrapped in the new **`weft.Nested`**
  event (wire `nested`, recursive through `UnmarshalEvent`), numbered
  from the parent's counter under the parent's ordering lock; the
  child's usage rolls into `RunResult.Usage` and is recorded per call
  on the new `StepRecord.SubagentUsage`. Every `ToolOption` applies to
  the delegation (`Timeout`, `MaxResultBytes`, `RequireApproval`,
  `Sequential`, `WrapTools`); the parent's `Parallelism` bounds
  concurrent delegations.
- **Child failure is data**: `SUBAGENT_FAILED: agent "x" failed at
  step N: …` (the child's `*RunError` on `ToolError.Err`), a child
  ending pending is `SUBAGENT_PENDING: …`, and a delegation to an
  agent already running in the call chain is refused before any model
  call with `SUBAGENT_CYCLE: …`. Codes exported as
  `CodeSubagentFailed`/`CodeSubagentPending`/`CodeSubagentCycle`.
- **Lineage ids**: a child run's id is `<parent>/<step>/<callID>`
  (`<parent>/resume/<callID>` under `Approve`), visible on the nested
  `RunStart` and on `CallFromContext` inside the child.
- **The late-event rule** (ADR 0004 amendment): no `Nested` event and
  no usage record for a call is delivered after that call's
  `ToolFinish` — the close is atomic with the finish under the
  parent's lock, so replayed streams never show a finished call
  continuing.
- **Manifest**: tools from `Subagent` carry `"subagent": "<child
  name>"` (omitempty; existing goldens unchanged). **wefttest**:
  `Flatten` unwraps `Nested` events recursively for assertions.

### Fixed — go1.26 decode-error compatibility (follow-up to the review pass)

- The schema walk behind `INVALID_INPUT` messages now handles toolchains
  (go1.26) that omit map keys from `encoding/json`'s error field path
  (`meta.when` for an error under `meta["k"]`): a segment at a map
  schema is first tried as a property of the value schema before being
  taken as a key, so the expected type named is still the advertised
  one. The `",string"` mismatch tests accept both toolchain renderings
  — the newer UnmarshalTypeError form (field named) and the older
  plain-error form — both speak the schema's vocabulary.

### Changed — the 2026-09-18 code-review pass (all 43 findings)

Schema correctness (ADR 0003's same-day amendment):

- **Embedded shadowing now tracks depth, encoding/json's real rule**:
  a name claimed at two embedding depths keeps the shallower
  contribution (the schema previously cancelled a name the wire still
  carried), and at equal depth exactly one json-tagged claim beats
  untagged ones (the schema previously kept the last writer — for a
  struct with both a tagged int and an untagged bool claiming "Name",
  it advertised a boolean the wire never emitted). Any other
  equal-depth tie cancels the name, as encoding/json drops it. One
  deliberate, conservative divergence: in a double diamond the schema
  cancels where encoding/json's breadth-first resolver may keep — the
  cancelled side never advertises what decode cannot reliably
  deliver.
- **Two fields of one struct claiming one JSON name panic at
  construction** (always both tagged), at any *named* nesting depth:
  one handler field can never receive a value. Inside an embedded
  struct the same collision instead flattens into the parent's
  dominance rules and drops, matching encoding/json. The
  duplicate-name and non-struct-input panics set the precedent.
- **Decode errors speak the schema's vocabulary**: the expected type
  in `INVALID_INPUT: … field "x": expected T, got U` is read from the
  tool's advertised schema, walked along the error's field path, so
  `[]byte` fields report "expected string" and a `,string` integer
  reports `field "n": expected string, got number` — never the Go kind
  the schema did not show. The Go-type mapping is the fallback for a
  path the walk cannot resolve.

Run and loop correctness:

- **Approval-resume results complete the earlier step's tool message
  in the assistant's call order** (ADR 0007 §3), not appended after
  the earlier run's results — Gemini matches functionResponses by name
  and position, so a reordered tool message could attach a result to
  the wrong call. The seams test is order-sensitive now and a contract
  pin covers the mixed approved/earlier shape.
- **The terminal `RunFinish` decides the run's outcome.** One check now
  governs both the event's delivery and the return value: a delivered
  `RunFinish` is always followed by success, and a cancellation that
  lands between the loop's last ctx check and the emit fails the run
  with the cancellation error (the resumable result on it) instead of
  returning success with no `RunFinish` — or, at the approval boundary,
  delivering `RunFinish` and then failing. Rule 4 and `Run.Events`' one
  terminal element now hold under every interleaving; pinned by a tap
  that cancels on the final `StepFinish`.
- `StepFinish`'s doc now says what the loop does: the event follows the
  step's tool events and precedes the stop-condition check (no wire or
  order change).
- **A tool-source snapshot with a nil entry fails the run with the new
  `ErrNilTool`** (the `ErrDuplicateTool` pattern) instead of silently
  advertising a nil every adapter dereferences into a misleading
  `ErrModelContract` panic.

Deny mode is a gate, not an aspiration:

- **`WEFT_MODEL_REQUESTS=deny go test ./...` is green workspace-wide**
  (`make offline`, pinned in CI). The kill switch now guards clients
  the adapters build from credentials; a client injected through the
  adapters' `Client(c)` option is a test double by construction and
  stays reachable (ADR 0013's amended clause) — the switch no longer
  breaks weft's own fixture suites in the mode it exists for. The
  `ModelRequestsAllowed` meta-test is ambient-aware.

Adapters (ADR 0013's appendices updated in the same change):

- **Empty content never reaches the wire** in openai and google, the
  2026-09-14 rules anthropic already had: a reasoning-only assistant
  message is skipped (`{"role":"assistant"}` was API-rejected), empty
  text parts are dropped, and Google skips Contents reduced to zero
  parts.
- **openai normalizes empty tool-call arguments to `{}`** on the
  convert side, matching its own stream side and the other adapters; a
  replayed nil-args transcript no longer 400s as "arguments is not
  valid JSON".
- **openai's `parallel_tool_calls` hint is sent only alongside a tool
  catalog** — several compatible servers reject the hint without one.
- **Synthesised `call_<i>` ids skip ids the server already populated**
  in the same step (openai, google): a collision failed the run with
  `ErrModelContract`, the failure the synthesis exists to prevent.
- **openai overwrites repeated function-name fragments** instead of
  concatenating them ("pingpingping").
- **google streams a thought signature riding an empty non-thought
  text part** — Gemini validates its return on the next request.
- **google's int32 ceilings fail loudly** wrapping `ErrUnsupported`
  (`MaxTokens`, `Thinking.Budget`) instead of wrapping around into a
  garbage wire value.
- **Tool defs are converted per request.** The pointer-keyed
  `sync.Map` never evicted, and a `ToolSource` that rebuilds its
  snapshot per step — the seam's documented use — grew it without
  bound. Measured first (`openai.BenchmarkConvertTool`): ~2–5µs /
  62 allocs per conversion against the network round trip every
  request also pays.
- **SDK-free helpers moved to `internal/adapterkit`** (schema
  rendering, the terminal-error rule, the FilePart guard): three
  byte-identical copies become one; the root module's public API is
  untouched.

The gate and the tooling:

- **The apidiff gate fails closed.** A tree that does not compile, or
  an apidiff run that dies before writing a report, can no longer read
  as "no incompatible changes"; the gate builds both sides first and
  distinguishes execution failure from a clean diff. A self-test
  exercises all three modes (`make apidiff-selftest`), CI pins the
  apidiff version the way golangci-lint is pinned, and the offline
  deny gate is its own CI job.

Test support and middleware:

- The conformance suite gained the two cases the README already
  claimed: `tool_args_delta` (fragments surface live before the call's
  `ToolStart`; `Caps.ToolArgDeltas` declares it — Google's calls
  arrive whole) and `slow_stream` (a dripping stream under a tight
  idle timeout must succeed; only a true stall fails).
- `wefttest`'s `Requests()` records exhausted calls too (real requests
  that they are), and the scripted model yields `ctx.Err()` before its
  scripted error, matching the Model contract the adapters enforce.
- `mw.RepairJSON` strips any fence language tag (```JSON`,
  ```javascript`), not just lowercase `json`, and no longer
  eats literal "json" content after a bare fence. `mw.MaxWait(0)`
  means "no cap"; every mw option's ignore rule is documented like the
  core's. `Retryable`'s `ErrModelContract` exclusion is pinned in the
  classifier test.
- The loop's own failure codes are exported constants —
  `CodeInvalidInput`, `CodeNoSuchTool`, `CodeDenied` — so external
  policy middleware produces the contract strings without duplicating
  them (`mw.Allow` already uses `CodeDenied`).
- `Replay`/`ReplayPolicy` and the manifest's `replay` key are
  **retracted** until the checkpoint store ships (ADR 0006's
  amendment): the annotation had no consumer before `store` exists.
  They return with it.

### Changed — run & event semantics (read before upgrading)

- **A tool-call ID repeated within one step fails the run** with
  `ErrModelContract`, like an empty ID: a repeated ID made `Repair`
  drop the second result and left `Approve`/`Deny` keyed on it
  ambiguous. IDs may still repeat across steps.
- **Tool arguments must be exactly one JSON value.** Trailing data
  after the arguments object (`{"a":1} {"a":2}`, `{"a":1} x`) is an
  `ErrInvalidToolInput` result in lenient and strict modes alike;
  the decoder previously took the first value and ignored the rest.
- **The truncation marker names the bytes omitted.**
  `…[truncated N bytes]` now carries N = bytes the model did not
  receive; the marker previously printed the cap, which read as "N
  bytes missing" however many were cut. The marker's shape is
  unchanged (ADR 0002, 2026-09-18 amendment).
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

- Embedded-struct schema derivation now follows encoding/json's
  shadowing rules: a struct's own field wins over an embedded one with
  the same JSON name (its type *and* its required flag, whatever the
  declaration order — previously the property was last-writer-wins and
  `required` could name it twice, an invalid schema), and the same
  name from two embedded structs cancels instead of surviving with a
  nondeterministic type. `required` stays in declaration order.
- The json `,string` option is reflected: the property is typed
  `string`, the quoted wire form. Previously the schema advertised the
  bare type, so a schema-following model's unquoted value was
  rejected (`invalid use of ,string struct tag`).
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
