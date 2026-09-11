# ADR 0001 — The message model

- Status: decided (v0 skeleton, 2026-09-09)
- Phase-1 blocker from THE-END-GOAL.md: everything above the core —
  sessions, persistence, devtools, the wire — depends on this contract.

## Context

The message model is what broke on every Vercel AI SDK major: message and
content-part shapes churned repeatedly. Weft must publish one stable shape
now and treat its JSON encoding as a compatibility contract.

## Decision

**A message is a role plus an ordered list of typed content parts.**

- Parts: `TextPart`, `ToolCallPart`, `ToolResultPart`, `ReasoningPart`
  (placeholder, preserved but uninterpreted). `FilePart` and approval
  request/response parts are planned additions.
- Roles: `user`, `assistant`, `tool`. **There is no system role**: the
  system instruction is agent-level (`Instructions`) and travels on
  `ModelRequest.System`, so a transcript never carries it and adapters
  never have to merge or de-duplicate it. Two ways to say "system" was
  exactly the kind of ambiguity that churns.
- **A step's tool results are collected on a single `tool`-role message,
  one part per call**, in call order. This is Anthropic-shaped (batched)
  rather than OpenAI-shaped (one message per result). Adapters translate to
  per-provider wire formats; the internal model stays lossless and compact.
- Tool call arguments and tool outputs are carried as raw JSON / JSON-
  encoded strings, so the core never needs to understand a tool's types to
  replay a transcript.
- **On the wire every part carries a `type` discriminator** (`text`,
  `tool_call`, `tool_result`, `reasoning`) and `Message` implements
  `UnmarshalJSON`, so a transcript round-trips through `encoding/json`
  losslessly. Unknown part types are a decode error, never silently
  dropped. `TestMessageJSONRoundTrip` pins the exact wire bytes.
- The JSON encoding carries an explicit `SchemaVersion` (currently 1).
  Within a version, field names and shapes change only additively.
  Persistence and serving layers envelope messages with the version; the
  core itself never needs it.

## Alternatives considered

- **OpenAI wire shape as internal model** (one tool message per call): more
  verbose transcripts, awkward batch semantics, and it would bake one
  vendor's current format into our contract.
- **Untyped content (`[]any`)**: what churns and breaks; rejected.
- **Versioned envelope inside the core**: unnecessary coupling; the version
  constant is enough until a persistence layer defines the envelope.

## Amendment (2026-09-10 — reasoning round-trip, TODO §2.2)

`ReasoningPart` gains an opaque `Signature` (`json:"signature,omitempty"`)
so adapters can send provider reasoning back (Anthropic rejects thinking
blocks without their signature). Rules, pinned by tests:

- A step's reasoning deltas accumulate into **one** `ReasoningPart`
  placed before the `TextPart` of the same assistant message — the order
  the model produced them in. The last non-empty signature wins, so a
  provider that emits several signed blocks in one step loses per-block
  boundaries; the core cannot re-split what it merged. Anthropic, the
  signature's only consumer today, emits one block per turn. If a future
  provider needs per-block fidelity, that is an additive change (a
  `Blocks` field), not a fix here.
- A reasoning part is appended when its text **or** signature is
  non-empty. Adaptive-thinking Claude models return thinking blocks
  whose text is omitted by default but whose signature must be replayed,
  so a signed empty-text block is real content.
- A turn with reasoning but no text and no calls **is** appended (it has
  content, and dropping it would lose a signed block the provider may
  expect back). A provider that rejects reasoning-only turns has its
  adapter drop it — vendor quirks live in adapters, not the core.
- The core stores and forwards reasoning and never reads it: no stop
  condition, no `RunResult.Text()`, no interpretation.

## Amendment (2026-09-11 — per-block reasoning fidelity)

The 2026-09-10 rule above merged a step's reasoning into one part and
named the condition for revisiting it: a provider that needs per-block
fidelity. Gemini's documented rule ("always send the thought_signature
back inside its original Part"; a turn's parallel calls may each carry
one) and Anthropic's ("pass thinking blocks back exactly as received")
both demand it, so the core now records **one `ReasoningPart` per
provider reasoning block**:

- A `ModelReasoningDelta` carrying a non-empty signature closes the
  current block; the next reasoning delta opens a new one. Providers
  send a block's signature last (Anthropic `signature_delta`, Gemini's
  per-part signature after the part's text), so pairing is lossless.
- Reasoning with no signature anywhere keeps accumulating into a single
  block: adapters drop unsigned blocks on send, so their internal
  boundaries cannot corrupt a round trip and the transcript stays
  compact. This is byte-identical to the previous behaviour for
  signature-less providers (OpenAI).
- `ToolCallPart` gains an opaque `Signature` (`json:"signature,omitempty"`,
  additive; empty encodes exactly as before) so Gemini's per-call thought
  signatures ride their own call and return to their own part; the
  google adapter falls back to the pre-per-call single-part placement
  for transcripts recorded before this change.

Pinned by `TestReasoningMultipleBlocksKeepBoundaries`,
`TestUnsignedReasoningStaysOneBlock`, and
`TestToolCallSignatureRoundTrip`.

## Amendment (2026-09-10 — FilePart and ErrUnsupported, TODO §2.3)

`FilePart{MediaType, Data, URL}` joins the closed part set
(discriminator `"file"`), for multimodal input without changing the
message model's shape:

- **Exactly one of `Data` (inline bytes, base64 on the wire via
  `[]byte`'s default encoding) or `URL` is set** — a documented rule,
  not one enforced by the core. The core would have to choose between
  panicking on user input and silently fixing it; the adapter is the
  layer that knows what it can send, and it fails the model call with
  `ErrUnsupported` (ADR 0002) for a part with both or neither set, or a
  media type the vendor does not accept. Size limits are the vendor's
  too.
- The core never reads the bytes; `Message.Text()` ignores file parts.
- `weft.UserParts(parts...)` builds a user message from mixed parts —
  the one way to send files (no `RunOption` sugar; a transcript is the
  input, not an option).
- A transcript with an inline file is a valid, if large, JSON document;
  `store` may externalise blobs later without changing this format.
- File *outputs* from the model and file-carrying tool results are out
  of scope; they get their own decision when a provider needs them.

## Amendment (2026-09-10 — transcript invariants and Repair, TODO §2.4)

The loop guarantees, and `weft.Repair` restores, one invariant: **every
assistant message with tool calls is followed by a tool message carrying
exactly one result per call id.** The loop applies `Repair` to the input
of every run, so any transcript a caller feeds back in (`Messages(...)`,
a resumed session, a partial from `RunError.Result`) becomes valid
provider input before the first model call. This is the Crush lesson —
orphaned tool results lock a session forever — and Deer-flow's
anti-silent-failure rule: the repair is visible in the transcript.

Precise semantics (pinned by tests and fuzzed):

- A tool message is matched against the assistant message **immediately
  before it**; only the **first** tool message after that assistant is
  considered, kept or dropped. A second consecutive tool message is
  non-canonical (the canonical shape batches a step's results on one
  message) and dropped whole — as is a tool message not preceded by an
  assistant message with calls.
- Within the considered message, a part survives when its `CallID` names
  an unserved call of that assistant message; the first result per call
  id wins.
- Calls still missing a result are synthesised in call order as visible
  error results — `no result recorded: the call was interrupted` —
  appended to the kept tool message or placed on a new one directly
  after the assistant message, including when the assistant message is
  last (the interrupted-mid-step case).
- **Drops carry no marker; synthesis does.** Both facts are deliberate.
- Consecutive same-role messages are left alone; merging is the
  adapter's job (providers differ).
- `Repair` is pure (input never mutated) and idempotent; `nil` maps to
  `nil`, empty non-nil to empty non-nil.
- The approval boundary (TODO §4.4) will pass the call ids it is about
  to resolve so they are *not* synthesised; the hook is internal.

## Consequences

- Providers whose APIs differ (per-result messages, tool role naming)
  translate in their adapter — one place, not spread across the core.
- Adding a part type is additive: a new discriminator value, a new struct,
  one more case in `unmarshalPart`; consumers switch over a sealed
  interface and can lint exhaustiveness.
- An assistant turn with neither text nor tool calls is not appended to
  the transcript (providers reject empty content, and transcripts must
  stay valid input for later runs); the step is still recorded.
