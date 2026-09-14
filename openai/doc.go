// Package openai is the weft adapter for the OpenAI Chat Completions
// API and OpenAI-compatible servers (gateways, local models), wrapping
// the official openai-go SDK. weft never owns the HTTP: every request
// goes through the SDK's client, and the SDK's stream type is the
// contract.
//
// Mapping highlights (ADR 0013 has the full tables):
//
//   - Text deltas pass through as ModelTextDelta; tool-call argument
//     fragments also surface live as ModelToolCallDelta progress while
//     the assembled call is buffered by delta index and emitted whole
//     as a ModelToolCall before ModelFinish — the core's
//     uniform-streaming rent.
//   - Stop reasons map to weft's three; anything unmapped keeps its raw
//     value on ModelFinish.Raw.
//   - A RoleTool message fans out to one OpenAI tool message per
//     result; assistant ReasoningParts are dropped (Chat Completions
//     has no reasoning input).
//   - SequentialTools sets parallel_tool_calls: false.
//   - ModelRequest.Thinking maps by dialect: reasoning_effort for the
//     official API and unrecognized hosts; a thinking object injected
//     into the request body (per-request SDK middleware) for the known
//     gateway hosts — z.ai, bigmodel.cn, moonshot.ai/cn — whose param
//     the typed params cannot carry. Dialect(...) overrides detection;
//     Off has no effort form and sends nothing there (ADR 0013
//     amendment 9 has the full table).
//
// Retry stance: transport retries (429, 5xx, net.Error) belong to the
// SDK via MaxRetries; the weft loop never retries a model call, and
// logic retries are model-seam middleware.
//
// The Responses API (reasoning items, built-in tools) is not wrapped
// here; it would be an openai/responses sub-package.
package openai
