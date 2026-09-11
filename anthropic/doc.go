// Package anthropic is the weft adapter for the Anthropic Messages
// API, wrapping the official anthropic-sdk-go. weft never owns the
// HTTP: every request goes through the SDK's client, and the SDK's
// stream type is the contract.
//
// Mapping highlights (ADR 0013 has the full tables):
//
//   - weft's batched RoleTool message is already Anthropic's shape: one
//     user message with N tool_result blocks.
//   - Reasoning round-trips as thinking blocks with their signature;
//     an unsigned ReasoningPart (a transcript from another provider)
//     is dropped rather than failing the call.
//   - SequentialTools sets tool_choice.auto with
//     disable_parallel_tool_use.
//   - max_tokens is required by the API; the adapter defaults it to
//     4096 when MaxTokens is not given.
//
// Retry stance: transport retries (429, 5xx, net.Error) belong to the
// SDK via MaxRetries; the weft loop never retries a model call, and
// logic retries are model-seam middleware.
//
// Vertex/Bedrock clients compose through Client(c).
package anthropic
