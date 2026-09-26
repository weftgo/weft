# Changelog

Notable changes to weft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project
is pre-1.0 and tags per module (ADR 0005).

## Unreleased

Fixes from the 2026-09-24 review (full report in the research
checkout; `WEFT-CODE-REVIEW-2026-09-24.md`).

### Fixed — `mcp`: untrusted-input robustness (review §2.5, §2.6)

- **`Tools` fails loudly on a server tool with an empty name** instead
  of panicking inside `RawTool`. The SDK's server-side name check only
  logs and its list filter drops nil tools but not empty names, so a
  hostile or buggy server listing `{"name":""}` reached the import
  path as a panic — untrusted input escaping as a process crash.
- **A schema'd tool returning non-JSON text is an isError result, not
  a wedged session.** Both `AddTools` and `Serve` set
  structuredContent from the result text whenever the tool advertises
  an output schema; text that is not JSON produced a `CallToolResult`
  the SDK cannot serialize, so the server never wrote a reply and the
  client blocked in `CallTool` forever — a *successful* call hanging
  the session. The result now names the output-schema breach; regular
  `Tool` definitions are unaffected (a string `Out` carries no output
  schema, everything else marshals).

## 0.3.0 — 2026-09-22

The Phase 2a parity round (TODO §2a, `docs/phase2a-plan.md` — not
published with the repo; ADR 0013's 2026-09-22 amendment records the
decisions): the six capability items the cross-check against the
studied frameworks found missing, plus the three stances recorded in
the same ADR pass. Tags cut together: the root and the three adapters
at v0.3.0; `mcp` untouched at v0.1.0. Everything is additive — apidiff
is clean with no allowances. The cycle then went through the repo's
two-axis review process and the fix pass landed inside the same
release: the standards axis found no hard documented-standard
violations (one doc drift, two duplication smells, five
production-readiness findings — all fixed or pinned below), and the
spec axis confirmed the cycle faithful to the plan, with the one
deviation (the OutputDecoder's within-step rule) now a documented,
pinned decision.

### Added — Tool-choice forcing (TODO §2a.1, ADR 0013)

- **`weft.ToolChoice(ToolChoiceConfig{Mode, Name})`** — force a step
  to call some tool (`any`), a named tool (`tool`), or none (`none`,
  with the catalogue still advertised, so a prompt-cache prefix on the
  tool definitions survives a no-calls final step). Works as agent
  option, run option, and per-step via `PrepareStep` — the `Thinking`
  dual shape. Mapped per provider (`required`/named/`"none"`;
  `any`/`tool`/`none` — `disable_parallel_tool_use` merges onto the
  chosen member; `ANY`/`allowedFunctionNames`/`NONE`). New conformance
  case `tool_choice_forcing` (declared via `Caps.ToolChoice`) also
  asserts the request bytes carry the provider's field.

### Added — Request params and the escape hatch (TODO §2a.3, ADR 0013)

- **`weft.Params(RequestParams{Temperature, TopP, MaxTokens, Stop,
  Seed})`** — per-run/per-step sampling folding over the adapters'
  construction options (nil keeps the construction default; a run
  override replaces the struct whole). Adapters gained `TopP`, `Stop`,
  and `Seed` construction options where the vendor has them (anthropic
  has no seed — dropped, documented; google narrows Seed/MaxTokens to
  int32, failing `ErrUnsupported` past the ceiling).
- **`ExtraBody(map[string]any)` / `ExtraHeaders(http.Header)`** per
  adapter — the caller-wins valve for vendor knobs weft has no option
  for: nested maps deep-merge, every other value replaces, and **your
  key wins on conflict** (yours, not weft's — the default-bytes tests
  do not cover what it sends). Construction-time only.

### Added — Richer `Usage` (TODO §2a.4, ADR 0016)

- **`Usage.CachedInputTokens`, `Usage.CacheWriteTokens`,
  `Usage.ReasoningTokens`** — reporting subsets of the two totals
  (which stay inclusive; `UsageLimit` and `Total` are unchanged).
  Filled by all three adapters (anthropic cache read/write, openai
  cached/reasoning details, google implicit-cache and thought tokens).
  On spans under their semconv/v1.41.0 names
  (`gen_ai.usage.cache_read.input_tokens`,
  `…cache_creation.input_tokens`, `…reasoning.output_tokens`), each
  only when non-zero; the slog lines keep the two totals (line
  stability). Wire is omitempty — old event JSON round-trips.

### Added — Prompt caching (TODO §2a.2, ADR 0013)

- **`anthropic.PromptCache()`** — opt-in `cache_control` breakpoints at
  the three stable prefix edges (system block, final tool definition,
  trailing conversation edge); without it, no marker anywhere. Cache
  writes bill 1.25×, reads 0.1×; see the README cost note and the
  `Usage` splits for the measurement.

### Added — Externally-computed tool results (TODO §2a.5, ADR 0007)

- **`weft.Resolve(callID, content)` / `weft.ResolveError(callID,
  content)`** — resume a parked call with a result computed outside the
  process; the handler never runs, the content becomes the tool result
  verbatim (capped by `MaxResultBytes` as any result), composes with
  `Approve`/`Deny` in one resuming call. **Behaviour note:**
  `Resolve` on a call that is not pending is a loud run error at
  step 0 — a deliberate asymmetry with `Approve`/`Deny`, which ignore
  unknown ids.

### Added — Streaming partial structured output (TODO §2a.6)

- **`weft.NewOutputDecoder[Out]()`** — a decoder value fed from the
  run's events; `Feed` returns best-effort partials as `submit_output`'s
  arguments stream (lenient closed-prefix decode, hand-rolled in
  `partial_json.go`, fuzzed), `Result` follows the `OutputOf` rule.
  UI-only; never model-visible.

### Fixed — the review round (2026-09-22)

- **`ExtraBody`/`ExtraHeaders` snapshot at construction** (all three
  adapters): the options captured the caller's nested maps and header
  slices by reference, so mutating them after `Model()` raced
  concurrent runs. Values are deep-copied when the option applies
  (`adapterkit.CloneJSON`, header slices cloned); pinned per adapter
  by `TestExtraBodySnapshot`.
- **A negative `RequestParams.MaxTokens` fails the run at the step
  that carries it** — named in the error text, the `validateToolChoice`
  stance. Before, the adapters improvised: openai forwarded it into an
  opaque API error, google dropped it silently, anthropic folded it to
  the default. Explicit zero keeps its documented per-adapter meaning.
  Pinned by `TestParamsValidation`.

### Changed — internal (the review round)

- **The truncated-JSON closer lives once**, in `internal/jsonclose`,
  imported by both `partial_json.go` and `mw/repairjson.go`. The two
  copies had already drifted (the trailing-comma rule); the plan's
  "duplicated on purpose" rationale was wrong — `mw` importing an
  `internal/` package of its own module breaks no rule (the
  `adapterkit` precedent). Behaviour change rides along for
  `closedPrefix`: a trailing comma *run* now closes (it dropped back
  to the member boundary before) — strictly more salvage.
- **`OutputDecoder`'s within-step rule is last-call-wins, pinned.** A
  fresh `submit_output` `ToolStart` resets the buffer, so `Result`
  follows the last call — the `OutputOf` rule the godoc always named;
  the plan's "concatenates within a step" wording is amended in place
  (two distinct calls' arguments never decode as one document).
  `BenchmarkOutputDecoder` pins the documented cost shape (~0.9µs per
  delta at a 3 KiB form; the per-delta re-close is quadratic in
  submission size by design).
- The loop skips the resume pending-id set when a run carries no
  decisions, and a `cap`-shadowing local is renamed.

### Docs

- ADR 0013 amended (tool choice, params + escape hatch, cache markers,
  the provider-executed-tools stance); ADR 0007 amended (resolve);
  ADR 0014 noted (handoff isolation stands); ADR 0016 noted (usage
  split attributes); ADR 0017 amended (replay key gains `ToolChoice`,
  not `Params`). README: sampling/forcing paragraph, the prompt-cache
  cost note, and "Seams are the product" (governance middleware stays
  examples — TODO §2a.9). Godoc examples: `ExampleToolChoice`,
  `ExampleParams`, `ExampleOutputDecoder`; `examples/approval` gains
  the `run_sql` resolve path.
- The review round's docs: ADR 0013's capability matrix gains the
  Tool choice column (all three adapters ✓, with each provider's wire
  tokens); `RequestParams`, `ExtraBody`/`ExtraHeaders`, and
  `OutputDecoder` godocs state the new rules;
  `docs/phase2a-plan.md` §8.1 carries the two dated amendments.

### Live runs owed (carried debt — no provider keys on the build machine)

v0.3.0 ships on the offline suites, as v0.2.0 did (ADR 0013's
fixtures-and-live-runs rule). When keys exist, `make live` must prove:
the full `conformance.Run` table including `tool_choice_forcing` on
all three providers; `PromptCache`'s measured payoff
(`CachedInputTokens > 0` on step 2+ of a long-transcript run);
openai/google `CachedInputTokens`/`ReasoningTokens` non-zero against
real responses; and `mw.Retry`'s structural `HTTPStatus`/`RetryAfter`
extraction against real openai-go / anthropic-sdk-go / genai error
values (the same §3.5/§4.1 debt, now grown by 2a's request-byte
changes).

## 0.2.0 — 2026-09-19

Everything unreleased since 0.1.0, tagged as one set (ADR 0005): the
middleware seams and the approval boundary (the 2026-09-14 round), the
loop controls and subagents as tools plus two full review passes (the
2026-09-18 round), and MCP both ways, observability, and wefttest
record/replay (the 2026-09-19 round). Tags cut together: the root and
the three adapters at v0.2.0, `mcp` new at v0.1.0. The two groups
marked "read before upgrading" are the behavior changes to check
first.

### Added — Testing conventions: replay, wefttest growth, fuzz in CI (TODO §9, ADR 0017)

- **`wefttest.Record(t, dir, inner)` / `wefttest.Replay(t, dir)`** —
  record/replay at the `weft.Model` seam, so application tests run
  against what a real model actually said, offline and deterministically.
  Fixtures are one pretty-printed JSON file per request (reviewable in a
  diff; re-recording is the review), keyed on the request's messages,
  tool names, thinking level, and sequential flag — not the system
  prompt, so a prompt tweak does not invalidate fixtures. A miss fails
  loudly with `wefttest.ErrNoFixture` naming the directory, key, and
  first user text; repeated identical requests replay in recorded
  order. Both are ordinary `weft.Model`s (middleware, `PrepareStep`,
  and subagents run unchanged above them); the conformance suite is
  green against a `Replay`, and committed recordings under
  `wefttest/testdata/replay/` prove a fresh checkout replays with no
  key and no network. No new dependency; nothing under `weft/` proper
  changed. Adapters keep their wire-level `.sse` fixtures (ADR 0013) —
  replay answers the *application* question, fixtures the *adapter*
  question.
- **`wefttest` scripting helpers**: `Args(v)` (typed tool arguments),
  `Raw(events...)` (verbatim events — the one-liner for contract
  violations and signed reasoning blocks), `SayThenFail(text, err)`
  (mid-stream failure), `Turn.WithUsage(u)`, `Request` matchers
  (`HasTool`, `ToolNames`, `LastText`), and `Model.LastRequest()`.
- **`make fuzz`** runs every fuzz target of the root module (10 s
  apiece, `FUZZTIME` overridable) and a dedicated CI job gates it,
  uploading crashers on failure; a crasher becomes a committed
  regression seed (the `FuzzRepair` precedent).

### Added — Observability: OTel spans and slog lines (TODO §8, ADR 0016)

- **The core's first and only dependency: the OTel API**
  (`go.opentelemetry.io/otel` v1.46.0, `semconv/v1.41.0` — the newest
  semconv package carrying the GenAI group). Every run reports its own
  spans — `invoke_agent` per run, `chat` per model call, `execute_tool`
  per executed tool call, children nested under their parents, GenAI
  semantic attributes, no message or tool-argument content on any span —
  through the global provider, so setting up an SDK is the whole
  integration; no weft option needed. Cost with no SDK registered: six
  allocations and ~230 ns per span (`BenchmarkObserverNoop`).
- **`weft.TracerProvider(tp)`** replaces the global provider for one
  agent (tests and DI programs never touch the global).
- **`weft.Logger(l)`** writes one Debug line per phase — run start, run
  finish, model call, tool call — ids, model, durations, usage, stop
  reasons, outcomes; default `slog.Default`, resolved at log time,
  silent unless Debug is on; lines carry the span context so an
  OTel-bridging handler correlates them for free.
- New module **`examples/otel`** (own `go.mod`, in `go.work`): the real
  SDK with a stdout exporter, plus a test asserting the span tree
  through the SDK's in-memory exporter — offline.
- Amends ADR 0004 (OTel and slog are the loop's own reporting, not the
  tap's) and ADR 0005 (a `version` constant for instrumentation
  version). `Tap` is unchanged and now receives the span-carrying
  context.
- Review pass (2026-09-19): `error.type` for weft's sentinels is a
  snake_case token (`max_steps`, `model_contract`, …; table in ADR
  0016), not the sentinel's sentence; a cancelled tool call reports
  `context.Canceled` like the run; the `chat` span takes the loop's own
  finished flag instead of inferring it from an empty stop reason;
  the README states precisely what error text travels. A second pass
  the same day: a panic nothing contains — a PrepareStep function —
  ends the run span (`run_panicked`) and re-panics instead of leaking
  the span; ADR 0016's dependency closure drops `golang.org/x/sys`
  (it appears only in `examples/otel`'s go.sum); the ADR 0005
  amendment the entry above cites is written.

### Fixed — ParseSchema: the structured view is lenient (ADR 0003 amendment)

- A foreign schema whose keyword shape the `Schema` struct cannot hold
  — `"additionalProperties": false` (what zod-built TypeScript servers
  emit), a type array `["string","null"]`, tuple or boolean `items`, a
  non-string description — no longer fails `ParseSchema`: the keyword
  stays in the stored bytes (what the model sees) and the structured
  view leaves it zero (what readers walk). The document itself is
  still checked: invalid JSON, trailing data, or a non-object top
  level fail at import. The anthropic adapter now forwards every
  top-level keyword it cannot map (a foreign `$defs`, `oneOf`, …)
  onto the wire, so a `$ref` inside properties never dangles; the
  anonymous-field exemption matches `encoding/json`'s exactly (an
  unexported non-struct anonymous field stays out whatever its tag).

### Added — MCP, both ways (TODO §7, ADR 0015)

- **`mcp.AddTools(s, tools...)`** and **`mcp.Serve(s, agent, description)`**
  expose weft tools and agents as an MCP server: each tool listed with
  its own contract, every failure an `isError` result carrying weft's
  pinned text, the agent tool literally `weft.Subagent`, and the agent's
  tools dispatched through its chain (approval answers loudly; `AddTools`
  refuses gated tools outright). New module **`weft/mcp`**; the official
  Go SDK is pinned at v1.8.0 and aliased `sdk`.
- **`mcp.Tools(ctx, sess, opts...)`** imports a connected session's tools
  as ordinary weft tools (`RawTool`; schema bytes verbatim through
  **`weft.ParseSchema`**), shaped by **`mcp.Prefix`**, **`mcp.Policy`**
  and **`mcp.ErrToolError`**. Foreign tools are `Sequential` unless the
  server marks `readOnlyHint`; `structuredContent` wins in result
  rendering; transport failures are `mcp: `-prefixed tool errors.
- Core, additions only (`make apidiff`): **`weft.ParseSchema`** (foreign
  bytes kept whole; `Schema.MarshalJSON` emits them),
  **`(*Agent).Name`**, **`(*ToolDef).RequiresApproval`**.
- **Wire change, named:** unconstrained schema nodes reach OpenAI and
  Anthropic as `{}` where the hand-built map wrote `{"type":""}` — no
  test pinned the old bytes; ADR 0003's amendment records the change.

### Fixed — review pass (2026-09-19)

- `make build/test/vet/lint/tidy/offline/live` fail fast per module: a
  failing module no longer reads green because a later one passes (the
  loop kept only the last status).
- `TestToolsTransportFailureIsData` could hang the suite: the SDK's
  shutdown waits for an in-flight handler whose request ctx cancels only
  after that wait — the severed-transport call now carries its own
  deadline, the way a run's tool `Timeout` bounds it in production.
- Gemini's fallback for a foreign schema the `genai` decoder rejects maps
  the structured fields recursively — nested properties and items
  survive, not the top level alone.
- `weft.ParseSchema` no longer rejects a legal keyword shape the `Schema`
  struct cannot hold — `"additionalProperties": false` (every zod-built
  TypeScript MCP server emits it), a type array, tuple or boolean
  `items`, a non-string description. The structured view leaves such a
  field zero; the bytes still cross whole. One such tool used to fail
  the whole `mcp.Tools` import.
- `mcp.Tools`' image and audio markers report the payload's own byte
  count; the SDK already decodes the wire's base64, so `DecodedLen` on
  it under-reported by a quarter.
- The 2026-09-19 embed rule is narrowed to what `encoding/json` does: a
  tagged anonymous field of an unexported *non-struct* type is ignored
  on the wire, so the schema no longer advertises it as required.
- **Anthropic dropped a foreign schema's top-level keywords** other than
  `properties`/`required`/`additionalProperties`: `$defs` (so every
  `$ref` dangled and the API rejected the tool), `$schema`, top-level
  `oneOf`. Every other top-level key now rides `ExtraFields`; pinned.
- `Serve`'s tools answer with `structuredContent` when they advertise an
  `outputSchema`, as `AddTools`' already did (the spec's MUST).
- A client that omits `arguments` hands an exposed `RawTool` `{}`, not
  `null` — the consume side's rule, now on both sides.
- An `isError` result with no content reads `ErrToolError`'s text
  instead of an empty string; a `ResourceLink` item renders as
  `[resource <uri>]` instead of the opaque `[content]`.
- `ExampleTools_toolSource` guards the refreshed slice with a mutex, as
  the godoc now says: the SDK's `listChanged` handler and the loop read
  it from different goroutines.
- `TestAddToolsRefusesApprovalGated` ran one of its three cases (a
  recover deferred in a loop unwound the test); all three run.

### Added — option composition (TODO §5.10)

- **`weft.Options(opts...)`** composes agent options into one value,
  applied in order — a plugin is `func(deps) weft.Option`, dependencies
  are parameters, never globals. **`weft.ToolOptions(opts...)`** is the
  per-tool counterpart. Nil entries ignored; duplicates still panic.

### Added — PrepareStep, the one loop knob (TODO §5.5, ADR 0006 amendment)

- **`weft.PrepareStep(fn)`** — a function the loop calls before every
  model call with the request it built (raw instructions, transcript,
  tool snapshot). What it returns is what the step both advertises and
  dispatches against (validated like a tool-source snapshot); snippets
  compose after it, from the returned tools; the model seam sees the
  prepared request; the transcript is never touched. Several options
  chain in order; an error fails the run with the caller's sentinel
  reachable. Not a third seam — the loop-level knob beside `StopWhen`
  (ADR 0006 amendment).

### Added — loop detection (TODO §5.4, ADR 0002 amendment)

- **`weft.DetectLoops(repeats)`** fails a run with
  **`ErrLoopDetected`** when `repeats` consecutive steps request the
  same set of tool calls — names and raw argument bytes, sorted, calls
  only (results never enter the signature). Off by default; the
  manifest records it when on.

### Added — model retry hints (TODO §5.2, ADR 0002 amendment)

- **`weft.ModelRetry(hint)`** — a tool error rendering as
  `RETRY: <hint>` that asks the model to try the call again with the
  hint applied. The loop counts RETRY results per tool name (middleware
  retries included) and fails the run with **`ErrModelRetriesExceeded`**
  after more than **`weft.MaxModelRetries(n)`** (default 3) consecutive
  asks; a success resets the count. The manifest policy records
  `max_model_retries` (the only §5 change that touches a committed
  golden; `examples/getting-started/weft.json` regenerated).

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

### Fixed — §5 review pass (2026-09-19)

- `PrepareStep` functions now receive a deep copy of the request: the
  previous slice-level clone shared each message's parts and the frozen
  registry's `*ToolDef` pointers, so an in-place write — dropping a
  part, re-forming a tool call's argument bytes, rewriting a
  definition's fields — corrupted the run transcript and the agent for
  later runs, against the documented "mutate freely" promise (ADR 0006
  amendment). Message parts, argument bytes, and tool definitions are
  cloned at that boundary; the model seam keeps the lighter slice
  copies under the adapter read-only contract.
- A typed-output child that submitted with no argument bytes (empty
  decodes as `{}`) now counts as submitted: the delegation returns the
  empty bytes instead of falling back to the child's final text,
  matching what `OutputOf` decodes (ADR 0014, G4).
- Added the missing godoc example for `ToolOptions`, the untested
  `resume` lineage id (`<parent>/resume/<callID>`, asserted through an
  approved delegation), and a guard comment that described a sort as a
  counter compare.
- `docs/life-of-a-call.md` drew the budgets before the `StopWhen`
  check; the loop checks `StopWhen` first and the budgets only at the
  continuation point (ADR 0002 amendment, AGENTS rule 13). The diagram
  now matches the code. ADR 0012 now records the three §5 policy keys
  (`max_model_retries`, `usage_limit`, `detect_loops`) the manifest
  had been writing without an entry.
- Pinned four behaviours the §5 suite left implicit: cancelling the
  parent mid-child under `Stream` delivers the cancellation last and no
  `RunFinish` at either level; a child's `RETRY` results feed the
  child's counter, never the parent's; concurrent runs on one
  orchestrator keep `Nested.RunID` and `Seq` per run; `PrepareStep`
  over a `ToolSource` consults the source once per step and dispatches
  against the prepared subset.
- Second round (deep review of the §5 plan against the tree):
  `CodeSubagentFailed`'s godoc claimed the child's cause is "never
  shown to the model" while the handler renders it into the
  model-visible message — the comment now states the real contract
  (message carries the cause; the `*RunError` stays on `ToolError.Err`
  for `errors.As`). `MaxModelRetries`' godoc now records that calls
  resumed under `Approve` do not feed the counter (they belong to no
  step, ADR 0007), as ADR 0002 already did. The "last valid
  `submit_output`" walk existed twice — `OutputOf` and a Subagent
  delegation's `submittedJSON` — and is now one `lastSubmitted` helper
  both call, so the "exactly the bytes `OutputOf` would decode" promise
  cannot drift. `nestFromContext` dropped its never-read ok flag.
  `make lint` now works from a bare shell like `make apidiff` does
  (GOPATH/bin on PATH); golangci-lint is 0-issues across the four
  modules.

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

### Added — the rest of the 2026-09-18 round

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

### Fixed — the rest of the 2026-09-18 round

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

### Added — the rest of the 2026-09-14 round

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

### Fixed — the rest of the 2026-09-14 round

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

### Changed — the 2026-09-14 round (docs)

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
