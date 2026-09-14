// Package anthropic is the weft adapter for the Anthropic Messages
// API, wrapping the official anthropic-sdk-go. weft never owns the
// HTTP: every request goes through the SDK's client, and the SDK's
// stream type is the contract.
//
// Mapping highlights (ADR 0013 has the full tables):
//
//   - weft's batched RoleTool message is already Anthropic's shape: one
//     user message with N tool_result blocks.
//   - Streamed input_json_delta argument fragments surface live as
//     ModelToolCallDelta progress; the assembled call is emitted whole
//     before ModelFinish.
//   - Empty content the API rejects never reaches the wire: an empty
//     tool result travels as "(empty tool output)", a user message
//     with no sendable parts as "(empty message)", and an assistant
//     message with nothing sendable is skipped.
//   - Reasoning round-trips as thinking blocks with their signature;
//     an unsigned ReasoningPart (a transcript from another provider)
//     is dropped rather than failing the call.
//   - SequentialTools sets tool_choice.auto with
//     disable_parallel_tool_use.
//   - ModelRequest.Thinking overrides the Thinking(true) construction
//     default: Off disables explicitly, a Budget pins budget_tokens,
//     a bare level sends adaptive (ADR 0013 amendment 9).
//   - max_tokens is required by the API; the adapter defaults it to
//     4096 when MaxTokens is not given.
//
// Retry stance: transport retries (429, 5xx, net.Error) belong to the
// SDK via MaxRetries; the weft loop never retries a model call, and
// logic retries are model-seam middleware.
//
// Vertex/Bedrock clients compose through Client(c).
package anthropic
