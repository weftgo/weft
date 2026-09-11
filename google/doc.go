// Package google is the weft adapter for the Gemini API via the
// official google.golang.org/genai SDK. weft never owns the HTTP: every
// request goes through the SDK's client, and the SDK's stream iterator
// is the contract.
//
// Mapping highlights (ADR 0013 has the full tables):
//
//   - Tool definitions become function declarations from Schema.
//     Thought signatures round-trip as reasoning: surfaced from
//     whichever part carries them, sent back on the first function
//     call of a tool turn (where the API validates them) or on a
//     thought part otherwise; unsigned reasoning is dropped.
//   - FilePart becomes inline data (base64) or a file URI.
//   - Function calls arrive whole; the adapter prefers the call id the
//     API populates and synthesises call_<i> per step when absent,
//     matching responses by position (the API matches by name and
//     order).
//   - SequentialTools has no Gemini switch: function-calling config
//     stays AUTO and the adapter's conformance run declares the
//     sequential cap false — a documented gap, not a silent one.
//
// Retry stance: transport retries (429, 5xx, connection errors) belong
// to the SDK via MaxRetries (off unless asked); the weft loop never
// retries a model call, and logic retries are model-seam middleware.
//
// Vertex AI and Bedrock-style setups compose through Client(c) with
// their own genai.Client. Grounding, search, and code-execution tools
// are not wrapped here.
package google
