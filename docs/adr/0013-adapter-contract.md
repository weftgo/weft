# ADR 0013 — Adapter contract and conformance

- Status: decided (2026-09-10, with TODO §3.1–§3.7); amended 2026-09-12
  (thinking pass-through, TODO §5.14), 2026-09-14 (argument-delta
  progress, enforced cancellation, empty-content and refusal handling —
  cross-cutting rules and appendices below), 2026-09-18
  (per-request tool conversion, the narrowed kill switch, the
  `slow_stream` and `tool_args_delta` cases, init-client error wording),
  and 2026-09-22 (Phase 2a parity round, TODO §2a — the amendment
  below: tool choice, request params, `ExtraBody`/`ExtraHeaders`,
  prompt-cache markers, and the provider-executed-tools stance)
- Numbering: 0013, not 0006 — 0006–0011 are reserved by the TODO items
  that name them (§4 seams, §4.4 approval, §6 output, §10 wire, §11
  record, §14 runtime) and 0012 is the manifest.

## Context

THE-END-GOAL makes three of its five checkable properties about this
layer: adapters wrap the vendors' **official Go SDKs** (no vendor
gravity, no owned HTTP client), streaming is **normalised** so the core
sees one event shape, and the loop **never retries a model call**. An
adapter is the only place weft touches a vendor, so the adapter
contract — what every first-party adapter must do, and what a
third-party one should — must be executable, not prose.

## Decision

**The conformance suite (`wefttest/conformance`) is the normative
adapter contract.** `conformance.Run(t, caps, newModel)` executes the
table below; when it is green for an adapter, the adapter honours the
contract. It lives in the root module, imports no vendor SDK, and
drives the model through weft's public API only — so it also runs
against `wefttest` models (proving the harness itself).

### The table

| Case | Asserts |
|---|---|
| `text_only` | one step, `StopEndTurn`, non-empty `Text()`, usage > 0 when `Caps.Usage` |
| `tool_roundtrip_struct` | a step calls the suite's `probe` tool with args; the JSON struct output round-trips (`{"n":3,"doubled":6}`); the final text mentions the value |
| `parallel_three_calls` | a suite-owned prompt yields three calls in one step with distinct non-empty IDs; results batch on one RoleTool message |
| `usage_nonzero` (`Caps.Usage`) | every step's usage has input and output > 0 |
| `cancel_mid_stream` | cancelling after the first text delta ends the run with `context.Canceled`: exactly one terminal error, no events after it |
| `max_tokens` | the adapter's token-limit option maps to `StopMaxTokens` |
| `sequential_hint` (`Caps.Sequential`) | under `weft.Sequential()`, ≤1 call per step |
| `reasoning_passthrough` (`Caps.Reasoning`) | a ReasoningPart precedes text; feeding the transcript back succeeds (signature round trip) |
| `thinking_option` | a run with the `weft.Thinking` option completes normally (the option threads through the public API); wire-shape assertions live in adapter unit tests, where the request body is recordable |
| `file_input` | with `Caps.Files` an inline PNG is answered; without, the run fails wrapping `ErrUnsupported` |
| `idle_timeout` (offline only) | a stalled stream fails wrapping `ErrStreamIdle` |
| `slow_stream` (offline only) | a dripping stream under a tight idle timeout succeeds with text — the per-chunk idle timer resets, so only a true stall fails |
| `tool_args_delta` (`Caps.ToolArgDeltas`) | argument fragments surface live as `ToolArgsDelta` progress before the call's `ToolStart`, and the assembled call still arrives whole |
| `kill_switch` | `WEFT_MODEL_REQUESTS=deny` fails the run wrapping `ErrModelRequestsDenied` before any request (offline runs point the case at `conformance.NoRequestServer`, which fails the test if anything arrives) |
| `never_contract_violation` | no case's run error wraps `ErrModelContract` (a detector flags it suite-wide) |

`Caps` declares what the adapter supports (`Reasoning`, `Files`,
`Sequential`, `Usage`, `ToolArgDeltas`, `Live`), so the suite asserts
instead of skipping silently: `Usage` false declares a compatible
server that never reports usage — the adapter must not fake numbers;
`ToolArgDeltas` false declares that calls arrive whole (Google), so
the case's progress assertions do not apply; `Live` relaxes
determinism (a live model may make a different but valid choice; those
cases `t.Skip`, contract breaches still fail) and skips the
fixture-only idle case.

### Cross-cutting rules (every adapter, every PR)

- **No owned HTTP.** Every request goes through the vendor SDK's
  client; the only HTTP an adapter module touches is `httptest` in
  tests. The SDK's stream type is the contract; no hand-rolled SSE.
- **No retries in the adapter.** Transport retries are the SDK's
  (`MaxRetries` forwards where the SDK exposes it); logic retries are
  model-seam middleware (§4.1). The loop never retries a model call.
- **Errors unchanged.** Vendor SDK error types pass through so callers
  `errors.As` them; the only errors an adapter *creates* are wraps of
  `ErrUnsupported`, `ErrStreamIdle`, `ErrModelRequestsDenied`, and
  `ctx.Err()` — and, since 2026-09-18, `ErrModelContract` for the
  adapter-detected contract violation: a provider payload too
  malformed to translate (Anthropic's empty `tool_use` id or name) —
  "the contract was broken before the loop could enforce it", the
  cause wrapped and reachable. One recorded exception: a failure to
  build the SDK client at all (Google's lazy init) is a
  location-prefixed wrap of the SDK's own error
  (`google: init client: …`), not a sentinel — credential discovery
  can fail transiently, and `mw.Retryable` declines `ErrModelContract`,
  so a sentinel there would brick a retryable condition. The SDK's own
  error passes through unchanged, per this rule's first sentence.
- **Cancellation is terminal.** A caller whose ctx ends mid-stream gets
  `(nil, ctx.Err())` — never a finish fabricated from buffered state.
  The reader goroutine can exit its handshake on cancellation with the
  SDK's error state still nil, so every adapter checks ctx explicitly
  after its read loop (2026-09-14; the `cancel_mid_stream` case and
  per-adapter regression tests pin it).
- **Whole tool calls, before `ModelFinish`, in a stable order.**
  Streaming argument fragments are the adapter's problem (the core's
  uniform-streaming rent). Empty IDs or names fail the run via
  `ErrModelContract` — synthesised ids (`call_<i>`, first-seen order)
  prevent that on servers that omit them. Since 2026-09-14 the
  fragments also surface live as `ModelToolCallDelta` progress (run
  side: `ToolArgsDelta`, ADR 0004's amendment) — progress only, the
  assembled call still arrives whole; adapters whose calls arrive whole
  (Google) simply yield none.
- **Read-only `ModelRequest`**; convert into fresh SDK values.
- **Tool defs are converted per request** (since 2026-09-18). The
  pointer-keyed cache this rule used to bless never evicted, and a
  `ToolSource` that rebuilds its `[]*ToolDef` per fetch — a registry
  snapshotting an MCP server per step, the documented use — inserted
  fresh pointers every step: an unbounded leak in exactly the scenario
  the seam exists for. Measured first (`openai.BenchmarkConvertTool`):
  one conversion of a ten-property schema costs ~2–5µs / 62 allocs
  against the network round trip every request also pays.
- **Vendor extras live in adapter options**, never on core types;
  provider stop reasons ride on `ModelFinish.Raw` (recorded on
  `StepRecord.RawStopReason` and the `StepFinish` event, never
  interpreted).
- **The kill switch** (`weft.ModelRequestsAllowed`, env
  `WEFT_MODEL_REQUESTS=deny`) is checked at the top of `Stream`, but
  only for a client the adapter built itself from credentials. A client
  the caller injected through the `Client(c)` option is a test double
  by construction — the same "policy seam, not security boundary"
  stance ADR 0007 takes for approvals — so it stays reachable under
  deny and weft's own offline suites run in the very mode the switch
  exists for (`make offline` is that gate, workspace-wide).
  `wefttest` models ignore the switch.

### Fixtures and live runs

Offline runs hit `conformance.FixtureServer` — an `httptest.Server`
serving recorded wire bytes from `<adapter>/testdata/<case>.sse`, one
response per request, separated by `=== response N ===` lines; an
under-recorded script 500s loudly. `conformance.StallServer` serves one
chunk then holds the stream open (cancel/idle cases). Live runs
(`//go:build live`, key env vars, never in CI) point the same `Run` at
the real client. Fixtures are committed recordings of realistic wire
format; when a vendor changes its wire format, the fixture is
regenerated and the diff is the review. (The plan's `make fixtures`
recording round-tripper is **deferred**: hand-recorded fixtures cover
the suite today, and the tooling needs live keys to be worth building;
tracked in TODO §9.2's orbit.) Application-level replay — testing an
*agent* against a recorded model stream — is ADR 0017's layer and
replaces nothing here: the wire fixtures stay the adapter proof.

### Capability matrix (Caps per adapter today)

| Adapter | Reasoning | Files | Sequential | Usage | Tool choice | Notes |
|---|---|---|---|---|---|---|
| `weft/openai` | off¹ | ✓ | ✓ | ✓ | ✓ (`required`/named/`"none"`) | ¹ `reasoning_content` from compatible servers is surfaced (DeepSeek-style, no signature); OpenAI's own reasoning items need the Responses API — a future `openai/responses` package |
| `weft/anthropic` | ✓ (thinking + signature) | ✓ (images, PDF) | ✓ (`disable_parallel_tool_use`) | ✓ (cache tokens folded into input) | ✓ (`any`/`tool`/`none`) | redacted thinking blocks are a known gap |
| `weft/google` | ✓ (thought signatures, per block/part) | ✓ (inline/URI; audio and video are native) | ✗ ² | ✓ (thoughts tokens folded into output) | ✓ (`ANY`/`allowedFunctionNames`/`NONE`) | ² Gemini has no parallel-tool-calls switch — a declared gap, not a silent skip |

## Appendix A — OpenAI (Chat Completions, `openai-go` v1.12.0)

- Messages: system → `system`; user text → text parts; image files →
  `image_url` (data URL or URL), other files → `ErrUnsupported`;
  assistant text → `content`, `ToolCallPart` → `tool_calls`;
  `ReasoningPart` dropped (no reasoning input); one RoleTool message
  fans out to N `tool` messages (error results as plain content).
- Stop reasons: `stop`→end_turn, `tool_calls`→tool_calls,
  `length`→max_tokens, empty-with-calls→tool_calls (compatible
  servers), anything else→end_turn + `Raw`. A streamed safety refusal
  (`delta.refusal`, `finish_reason` `content_filter`) is the model's
  answer text (2026-09-14): it streams as a text delta rather than
  vanishing, and `content_filter` rides `Raw` via the unmapped rule.
- Usage from the final chunk (`stream_options.include_usage`);
  `SequentialTools` → `parallel_tool_calls: false`, sent only
  alongside a tool catalog (several compatible servers reject the hint
  without one — 2026-09-18);
  `MaxTokens` → `max_completion_tokens`; `MaxRetries` → the SDK's
  transport retries. Compatible servers: `Provider` stays `"openai"`
  (a base URL host is not an identity), ids synthesised when missing,
  zero usage allowed via `Caps.Usage=false`.
- Thinking (§5.14): `ModelRequest.Thinking` maps by dialect —
  `reasoning_effort` low/medium/high for the official API and
  unrecognized hosts; a `thinking:{"type":"enabled"|"disabled"}` object
  injected into the JSON body via a per-request SDK middleware for the
  known gateway hosts (z.ai, bigmodel.cn, moonshot.ai/cn), whose param
  the SDK's typed params cannot carry. `Dialect(...)` overrides
  detection; `DialectNone` sends nothing for strict servers. Declared
  gaps: the official API has no off switch (`ThinkOff` sends nothing
  there) and no budget form (`Budget` is dropped in the effort
  dialect); Kimi k3's advertised `think_efforts` scale is future work. Live finding
  (2026-09-12): Moonshot's kimi-k2.7-code family rejects
  `{"type":"disabled"}` ("only type=enabled is allowed for this
  model") — always-thinking models; the 400 is the honest answer.

## Appendix B — Anthropic (Messages, `anthropic-sdk-go` v1.72.0)

- Messages: system → `system` blocks; the batched RoleTool message is
  already the vendor shape (one user message, N `tool_result` blocks,
  `is_error` preserved); assistant order thinking → text → `tool_use`
  (the loop already builds it); unsigned `ReasoningPart` dropped —
  Anthropic rejects unsigned thinking; images (png/jpeg/gif/webp) and
  PDF, inline or URL; other media → `ErrUnsupported`. Empty content the
  API rejects never reaches the wire (2026-09-14): an empty tool result
  travels as a visible `"(empty tool output)"` placeholder, a user
  message whose every part was empty text as `"(empty message)"`, and
  an assistant message with nothing sendable is skipped —
  model-visible, recorded here per the contracts rule.
- Streaming: `input_json_delta` fragments accumulate per block index;
  `thinking_delta`/`signature_delta` become reasoning deltas; calls are
  held until `message_stop` and yielded in block order.
- Stop reasons: `end_turn`/`tool_use`/`max_tokens` map exactly;
  `stop_sequence`, `refusal` (with `stop_details.category` appended),
  `pause_turn` → end_turn + `Raw`. Usage: `message_start` input
  (cache read + creation folded in — billed input) + `message_delta`
  output.
- `Thinking(true)` → `thinking:{"type":"adaptive"}` (the plan's guess,
  confirmed against the pinned SDK). `max_tokens` required; default
  4096. Vertex/Bedrock compose through `Client(c)`. Forced tool choice
  is never sent.
- Thinking (§5.14): the run-level `ModelRequest.Thinking` overrides the
  construction default — `ThinkOff` → `{"type":"disabled"}` (for
  models that think by default), `Budget>0` →
  `{"type":"enabled","budget_tokens":n}` (the SDK requires
  `budget_tokens` alongside a raised `max_tokens` — callers own that
  relationship), a bare level → adaptive. Off wins over a contradictory
  Budget.

## Appendix C — Google (Gemini, `google.golang.org/genai` v1.71.0)

- Messages: system → `SystemInstruction`; user text/files → parts
  (inline bytes or file URI — audio/video are native Gemini input);
  `ToolCallPart` → `functionCall` (args JSON → map). The batched
  RoleTool message is one user content with N `functionResponse`
  parts in order (the shape Gemini documents for parallel calls),
  keyed `{"output": …}` / `{"error": …}` (the API has no is_error
  flag); the API matches responses by name and position.
- Thought signatures return to the part Gemini issued them on, per the
  API's rule ("always send the thought_signature back inside its
  original Part"; parallel calls may each carry one). A signature on a
  `functionCall` rides the `ModelToolCall` (ADR 0001's 2026-09-11
  amendment) and comes back on that same call; a signature on a thought
  or text part streams as a reasoning delta after the part's text (the
  signature closes the core's reasoning block, so text-then-signature
  pairs them) and returns as a signed thought part. Transcripts recorded
  before per-call signatures kept the step's single signature on the
  `ReasoningPart`; those fall back to the first function call, where
  current models attach it. Unsigned reasoning is dropped, as in B. The
  signature is stored base64 (the wire form) so recorded transcripts
  survive `encoding/json`.
- Function calls arrive whole. IDs: the API populates `functionCall.id`
  on current versions — used when present; otherwise `call_<i>` per
  step, and the position-matched responses keep the second step valid.
- Stop reasons: `STOP`→end_turn, or tool_calls when calls are present
  (Gemini reports STOP either way); `MAX_TOKENS`→max_tokens;
  `SAFETY`/`RECITATION`/… → end_turn + `Raw`. Usage:
  `promptTokenCount` in; `candidatesTokenCount + thoughtsTokenCount`
  out.
- Empty content never reaches the wire (2026-09-18): empty text
  parts and Contents reduced to zero parts (all-empty text, unsigned
  reasoning dropped) are skipped — `{"role":...}` with no parts is
  API-rejected. `MaxTokens` and `Thinking.Budget` are int32 on the
  wire; values above the ceiling fail the call wrapping
  `ErrUnsupported` rather than wrapping around.
- `SequentialTools`: no Gemini switch exists — declared gap
  (`Caps.Sequential=false`). The SDK client is created lazily on first
  run (`genai.NewClient` wants a context for credential discovery), so
  `Model()` performs no I/O like its siblings. `MaxRetries(n)` →
  `HTTPOptions.RetryOptions{Attempts: n+1}` (the SDK retries nothing
  unless asked, so the zero configuration matches the other adapters).
  `BaseURL` unset defers to the SDK's own `$GOOGLE_GEMINI_BASE_URL`.
- Thinking (§5.14): `ThinkOff` → `thinkingBudget: 0` (Gemini's off
  switch); `Budget>0` → `thinkingBudget: n`; a bare level →
  `thinkingLevel` low/medium/high. An explicit level or budget also
  sets `includeThoughts: true` so reasoning streams; the default sends
  nothing and keeps the model's own behavior.

## Decisions recorded from the plan's guesses

1. `Provider` stays `"openai"` for compatible servers (telemetry
   cardinality; callers wrap `Info` if they need the host).
2. `ModelFinish.Raw` rides on `StepRecord.RawStopReason` and the
   `StepFinish` event — no vendor-specific core field.
3. The suite owns the parallel-calls prompt and the `probe` tool; live
   runs skip valid-but-different choices, fail only contract breaches.
4. Idle timeout (default 60s, `IdleTimeout(0)` disables) via a reader
   goroutine over the SDK stream: the timer resets per chunk (a slow
   but streaming response is never killed), the caller's ctx deadline
   stays the hard limit, and expiry cancels the stream's derived
   context before the sentinel is reported.
5. Error results on OpenAI go as plain content (no is_error in Chat
   Completions; the text already says what failed).
6. The kill switch is **checked per call, not cached** — a deviation
   from the plan's "read once", made so test suites can toggle it per
   test and the package keeps no state (see ADR 0002's amendment).
   Since 2026-09-18 it guards **self-built client egress only**: an
   injected `Client(c)` is a test double and stays reachable (see the
   cross-cutting rule above).
7. Adapter example programs live at `<adapter>/example/`, not
   `examples/<vendor>/` — the root module must not gain the SDKs to
   its module graph (the core stays dependency-free).
8. Anthropic cache tokens fold into input usage; Google thought tokens
   fold into output usage — both are billed tokens, both are guesses
   the conformance `usage_nonzero` case keeps honest.
9. Thinking control is **per-run, not per-construction** (§5.14,
   2026-09-12): `ModelRequest.Thinking` is the seam — a neutral
   `ThinkingLevel` scale plus an optional token budget — filled from a
   `weft.Thinking` option that works as both agent default and run
   override (the `PolicyOption` shape). Adapter construction options
   (`anthropic.Thinking`) remain the default the run value overrides.
   The openai adapter's gateway escape hatch rewrites the request body
   via an SDK request middleware, in the open — the predicted
   openai-go friction arrived as expected, and the RawTool-precedented
   override is the answer, never a silent absorption into a fake SDK
   field.

## Consequences

- A third-party adapter for any vendor can adopt the same contract:
  run the suite offline with fixtures, live behind the tag.
- The suite is the reason §3.5 closed before the adapters: its Done
  line gates every adapter, so the skeleton came first.
- The adapters share a reader-goroutine idle-timeout design; each
   module carries its own copy over its own SDK types. SDK-free helpers
   (schema rendering, the terminal-error rule, the FilePart guard) live
   in the root module's `internal/adapterkit` since 2026-09-18: Go's
   path-based internal rule admits `github.com/weftgo/weft/*` imports
   while keeping the root's public API exactly as small as the
   complexity budget demands — the resolution of the byte-identical
   copies finding from that day's review. Helpers that mention an SDK
   type stay per-adapter.


## Amendment (2026-09-19 — the kill switch does not govern `tools/call`)

`WEFT_MODEL_REQUESTS` governs model requests — an adapter calling its
provider. An MCP `tools/call` is not one: imported tools
(`weft/mcp.Tools`, ADR 0015) are the caller's tools, like any handler
that makes an HTTP call, and run under `deny`. The line matters when
MCP sampling ships (a server asking *our* model to generate): that is
a model request and will come under the switch — recorded in ADR 0015's
orbit.

## Amendment (2026-09-22 — Phase 2a: tool choice, request params, the escape hatch, prompt-cache markers)

The parity round (TODO §2a, `docs/phase2a-plan.md`) reshapes what
adapters send. Every decision below follows the standing rules: data on
`ModelRequest`, not seams (ADR 0006); zero value = v0.2.0's bytes,
pinned by default-bytes tests; vendor mapping lives in the adapters,
the core learns no provider noun. The 2026-09-22 second pass verified
every SDK shape named here against the pinned module versions.

### Tool choice

`ModelRequest.ToolChoice ToolChoiceConfig{Mode ToolChoiceMode; Name
string}` — modes `ToolChoiceAuto` (the zero value: provider default,
nothing sent), `ToolChoiceAny` (some tool must be called),
`ToolChoiceNamed` (`Name` must be called), `ToolChoiceNone` (no call
may be made; the catalogue stays advertised). `weft.ToolChoice(cfg)`
works as both agent option and run override (the `Thinking` shape), and
`PrepareStep` can rewrite it per step — no third mechanism.

Mappings:

| Adapter | `any` | `tool` (named) | `none` |
|---|---|---|---|
| openai | `tool_choice:"required"` | `{"type":"function","function":{"name":…}}` | `tool_choice:"none"` |
| anthropic | `OfAny` | `OfTool{Name}` | `OfNone` |
| google | `mode:"ANY"` | `mode:"ANY"` + `allowedFunctionNames:[name]` | `mode:"NONE"` |

- **Anthropic union merge:** when `SequentialTools` is also set,
  `disable_parallel_tool_use:true` rides the chosen `OfAny`/`OfTool`
  member instead of `OfAuto` — one `tool_choice` on the wire, both
  hints kept. `none` has no parallel field.
- **Google:** ANY-mode forcing is *not* the sequential hint —
  `Caps.Sequential` stays false (a declared gap), and forcing does not
  close it.
- **Loop validation, no sentinel:** a non-auto choice with an empty
  step tool list, a named choice whose tool is not advertised, a named
  choice with an empty name, or a `Name` set under another mode, fails
  the run with a descriptive `*RunError` at the snapshot-validation
  site (after `PrepareStep`). This is a programming error the caller
  fixes, not a condition to branch on — the snapshot sentinels exist
  only because middleware produces those lists dynamically. `none` with
  no tools is covered by the same empty-list rule (every provider
  rejects a forced choice without a catalogue).
- `ToolChoice` constrains what the provider is *asked* to emit, never
  execution: a provider that ignores `none` and calls anyway has its
  call run (the model was shown the tool; property 3 holds).
- **Conformance:** case `tool_choice_forcing` (declared via
  `Caps.ToolChoice`), with the request body asserted — the case's
  server records what the adapter sent and must see the provider's
  tool-choice field in it. Live: a valid-but-different choice under
  `any` skips; a call under `none` or a wrong name under `tool` is a
  breach.
- **wefttest:** `Script` records the field and does not act on it —
  scripts say what the model said; a double that invented a call to
  satisfy a request field would be a second behaviour to learn.
- **Replay key (ADR 0017):** `ToolChoice` joins the key the way
  `Thinking` does (nil when zero, so every existing fixture key is
  unchanged); `Params` does not (below).

### Request params

`ModelRequest.Params RequestParams` — `Temperature *float64`, `TopP
*float64`, `MaxTokens *int`, `Stop []string`, `Seed *int64`. Pointer
semantics, three states per knob: construction unset + request unset →
not sent; construction set + request unset → the construction value;
request set → the request value. A set pointer to `0` *is* a value
(`Temperature: ptr(0.0)` sends 0), with one documented exception:
anthropic `MaxTokens` of 0 falls to the adapter's 4096 default because
the API requires a positive value. `weft.Params(p)` is the dual option
(the `Thinking` shape); a run override replaces the agent's struct
whole, it does not merge field by field, and `PrepareStep` can edit it
per step. Adapters gained construction options for the same knobs:
`TopP`, `Stop` (anthropic: `Stop`, mapping `stop_sequences`), and
`Seed` where the vendor has one.

- **openai:** `temperature`, `top_p`, `max_completion_tokens`,
  `stop` (string-array union), `seed`.
- **anthropic:** `temperature`, `top_p`, `max_tokens`,
  `stop_sequences`. **No `Seed`** — the Messages API has none; a
  `Params.Seed` on a request is dropped under the "adapters document
  what they drop" rule (seed is a determinism *hint*, not a contract;
  documented in `anthropic/doc.go`). `TopK` stays out (no other
  provider has it in Chat Completions; a construction option later if
  asked).
- **google:** `temperature`, `topP` (narrowed to `*float32`), `maxOutputTokens`,
  `stopSequences`, `seed` (`*int32`): a `Seed` or `MaxTokens` above
  `math.MaxInt32` fails the call wrapping `ErrUnsupported` naming the
  ceiling — the existing narrowing rule, not a silent wrap.

### The escape hatch: `ExtraBody` / `ExtraHeaders`

Each adapter gained `ExtraBody(fields map[string]any) Option` and
`ExtraHeaders(h http.Header) Option` — the public version of the
openai adapter's internal thinking injection. Construction-time only:
per-request dynamic provider options stay a register row (they would
put provider nouns on `ModelRequest`, the P5 line). Mechanism:
openai and anthropic apply an SDK request middleware that deep-merges
`fields` into the JSON body (nested maps merge recursively, every
other value replaces) plus `option.WithHeader` per header entry, as
per-request options so they also apply to an injected `Client(c)`;
google sets `GenerateContentConfig.HTTPOptions{Headers, ExtraBody}` per
request and lets the SDK merge (`recursiveMapMerge`).

**Caller wins on conflict.** The escape hatch is the caller taking
responsibility for bytes weft did not choose; a valve that silently
declined to override would be the one surprise the option exists to
remove (genai's native `ExtraBody` merges the same direction; decided
in the 2026-09-22 review, plan §4.2). On openai the gateway thinking
object and `ExtraBody` compose in that order — weft's injection first,
the caller's merge last. Documented on the option: ExtraBody can
change request bytes — yours, not weft's; the default-bytes tests do
not cover it, and a colliding key replaces weft's value. A header the
SDK itself sets (`Authorization`, `Content-Type`) is the caller's
problem not to clobber.

### Prompt-cache markers (anthropic)

`anthropic.PromptCache()` — opt-in; without it no `cache_control`
appears anywhere (pinned). With it, `cache_control:{"type":"ephemeral"}`
lands on exactly three positions, each only when it exists: the system
text block, the final tool definition, and the final content block of
the final message of the transcript — the stable prefix edges (Crush's
placement). Three of anthropic's four-breakpoint budget; the fourth
stays unspent for `thread`'s compaction summary block. No TTL option
and no position options in v0.3.0 (the residuals pattern: one good
default, revisit when a consumer asks). `PrepareStep` interaction is
documented on the option: trimming mid-run invalidates the trailing
breakpoint on purpose — the option composes with deliberate trimming,
and `ToolChoiceNone` is the way to stop calls without breaking the
tool-definition breakpoint. The measured payoff (`CachedInputTokens > 0`
on step 2+) is part of the carried live debt (TODO §3.5).

Google's explicit context caching is **deferred** (G5): a created
resource with TTL, not a request marker — a different mechanism whose
lifecycle the adapter would own. Decision criterion: the first consumer
with Gemini long-transcript economics asks. Gemini's *implicit* caching
needs no option and is already visible through 2a.4's
`CachedInputTokens`.

### Provider-executed tools and the Responses API (stance, TODO §2a.7)

Anthropic server tools (`web_search`, `code_execution`), Google
grounding, and the OpenAI Responses API's built-ins are
provider-executed tools — a kind `Tool`/`RawTool` has no mapping for,
because nothing in the process executes them. Not wrapping them is a
stance, not an oversight; the candidate mapping when the first consumer
needs one is an adapter-mounted `RawTool` with a provider-executed
marker routed to a request-level tool entry, argued in a future
amendment of this ADR. The Responses API is its own sub-question
(reasoning items, `previous_response_id` statefulness vs weft's
caller-owned transcripts) — named here so the next person does not
rediscover it.
