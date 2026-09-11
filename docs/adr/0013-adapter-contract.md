# ADR 0013 — Adapter contract and conformance

- Status: decided (2026-09-10, with TODO §3.1–§3.7)
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
| `file_input` | with `Caps.Files` an inline PNG is answered; without, the run fails wrapping `ErrUnsupported` |
| `idle_timeout` (offline only) | a stalled stream fails wrapping `ErrStreamIdle` |
| `kill_switch` | `WEFT_MODEL_REQUESTS=deny` fails the run wrapping `ErrModelRequestsDenied` before any request (offline runs point the case at `conformance.NoRequestServer`, which fails the test if anything arrives) |
| `never_contract_violation` | no case's run error wraps `ErrModelContract` (a detector flags it suite-wide) |

`Caps` declares what the adapter supports (`Reasoning`, `Files`,
`Sequential`, `Usage`, `Live`), so the suite asserts instead of
skipping silently: `Usage` false declares a compatible server that
never reports usage — the adapter must not fake numbers; `Live` relaxes
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
  `ctx.Err()`.
- **Whole tool calls, before `ModelFinish`, in a stable order.**
  Streaming argument fragments are the adapter's problem (the core's
  uniform-streaming rent). Empty IDs or names fail the run via
  `ErrModelContract` — synthesised ids (`call_<i>`, first-seen order)
  prevent that on servers that omit them.
- **Read-only `ModelRequest`**; convert into fresh SDK values.
- **Tool defs converted once**, cached by `*ToolDef` pointer.
- **Vendor extras live in adapter options**, never on core types;
  provider stop reasons ride on `ModelFinish.Raw` (recorded on
  `StepRecord.RawStopReason` and the `StepFinish` event, never
  interpreted).
- **The kill switch** (`weft.ModelRequestsAllowed`, env
  `WEFT_MODEL_REQUESTS=deny`) is checked at the top of `Stream`;
  `wefttest` models ignore it.

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
tracked in TODO §9.2's orbit.)

### Capability matrix (Caps per adapter today)

| Adapter | Reasoning | Files | Sequential | Usage | Notes |
|---|---|---|---|---|---|
| `weft/openai` | off¹ | ✓ | ✓ | ✓ | ¹ `reasoning_content` from compatible servers is surfaced (DeepSeek-style, no signature); OpenAI's own reasoning items need the Responses API — a future `openai/responses` package |
| `weft/anthropic` | ✓ (thinking + signature) | ✓ (images, PDF) | ✓ (`disable_parallel_tool_use`) | ✓ (cache tokens folded into input) | redacted thinking blocks are a known gap |
| `weft/google` | ✓ (thought signatures, per block/part) | ✓ (inline/URI; audio and video are native) | ✗ ² | ✓ (thoughts tokens folded into output) | ² Gemini has no parallel-tool-calls switch — a declared gap, not a silent skip |

## Appendix A — OpenAI (Chat Completions, `openai-go` v1.12.0)

- Messages: system → `system`; user text → text parts; image files →
  `image_url` (data URL or URL), other files → `ErrUnsupported`;
  assistant text → `content`, `ToolCallPart` → `tool_calls`;
  `ReasoningPart` dropped (no reasoning input); one RoleTool message
  fans out to N `tool` messages (error results as plain content).
- Stop reasons: `stop`→end_turn, `tool_calls`→tool_calls,
  `length`→max_tokens, empty-with-calls→tool_calls (compatible
  servers), anything else→end_turn + `Raw`.
- Usage from the final chunk (`stream_options.include_usage`); tools
  cached by pointer; `SequentialTools` → `parallel_tool_calls: false`;
  `MaxTokens` → `max_completion_tokens`; `MaxRetries` → the SDK's
  transport retries. Compatible servers: `Provider` stays `"openai"`
  (a base URL host is not an identity), ids synthesised when missing,
  zero usage allowed via `Caps.Usage=false`.

## Appendix B — Anthropic (Messages, `anthropic-sdk-go` v1.72.0)

- Messages: system → `system` blocks; the batched RoleTool message is
  already the vendor shape (one user message, N `tool_result` blocks,
  `is_error` preserved); assistant order thinking → text → `tool_use`
  (the loop already builds it); unsigned `ReasoningPart` dropped —
  Anthropic rejects unsigned thinking; images (png/jpeg/gif/webp) and
  PDF, inline or URL; other media → `ErrUnsupported`.
- Streaming: `input_json_delta` fragments accumulate per block index;
  `thinking_delta`/`signature_delta` become reasoning deltas; calls are
  held until `message_stop` and yielded in block order.
- Stop reasons: `end_turn`/`tool_use`/`max_tokens` map exactly;
  `stop_sequence`, `refusal` (with `stop_details.category` appended),
  `pause_turn` → end_turn + `Raw`. Usage: `message_start` input
  (cache read + creation folded in — billed input) + `message_delta`
  output.
- `Thinking(true)` → `thinking:{"type":"adaptive"}` (the plan's guess,
  confirmed against the pinned SDK; no `budget_tokens` — rejected by
  current models). `max_tokens` required; default 4096. Vertex/Bedrock
  compose through `Client(c)`. Forced tool choice is never sent.

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
- `SequentialTools`: no Gemini switch exists — declared gap
  (`Caps.Sequential=false`). The SDK client is created lazily on first
  run (`genai.NewClient` wants a context for credential discovery), so
  `Model()` performs no I/O like its siblings. `MaxRetries(n)` →
  `HTTPOptions.RetryOptions{Attempts: n+1}` (the SDK retries nothing
  unless asked, so the zero configuration matches the other adapters).
  `BaseURL` unset defers to the SDK's own `$GOOGLE_GEMINI_BASE_URL`.

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
7. Adapter example programs live at `<adapter>/example/`, not
   `examples/<vendor>/` — the root module must not gain the SDKs to
   its module graph (the core stays dependency-free).
8. Anthropic cache tokens fold into input usage; Google thought tokens
   fold into output usage — both are billed tokens, both are guesses
   the conformance `usage_nonzero` case keeps honest.

## Consequences

- A third-party adapter for any vendor can adopt the same contract:
  run the suite offline with fixtures, live behind the tag.
- The suite is the reason §3.5 closed before the adapters: its Done
  line gates every adapter, so the skeleton came first.
- The adapters share a reader-goroutine idle-timeout design; each
   module carries its own copy over its own SDK types (adapters import
   only the root module — a shared helper would have to live in the
   root's public API, which the complexity budget forbids).
