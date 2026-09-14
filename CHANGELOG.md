# Changelog

Notable changes to weft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project
is pre-1.0 and tags per module (ADR 0005).

## Unreleased (2026-09-14)

### Added

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
