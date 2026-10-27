package obsdb

import (
	"encoding/json"
	"strings"
)

// The GenAI conventions' opt-in content attributes on an execute_tool
// span (semconv v1.41.0): what the call was given and what it returned.
// A producer sets them only when it captures content
// (OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true in the
// Python and JavaScript instrumentations); weft's own spans never carry
// them (§8: weft's content travels in log records under §5's policy).
const (
	attrToolCallArguments = "gen_ai.tool.call.arguments"
	attrToolCallResult    = "gen_ai.tool.call.result"
)

// The conventions' message content keys on a model-call span: the
// current family (semconv v1.41.0) and the legacy ones (OpenLLMetry's
// indexed gen_ai.prompt.N.* / gen_ai.completion.N.*, the plain and the
// event-era keys) — the families Studio's chat view reads
// (studio/web/src/lib/genai.ts genaiChat).
const (
	attrSystemInstructions = "gen_ai.system_instructions"
	attrInputMessages      = "gen_ai.input.messages"
	attrOutputMessages     = "gen_ai.output.messages"
	attrLegacyPrompt       = "gen_ai.prompt"
	attrLegacyCompletion   = "gen_ai.completion"
)

var plainMessageKeys = [...]string{
	attrSystemInstructions, attrInputMessages, attrOutputMessages,
	attrLegacyPrompt, attrLegacyCompletion, "gen_ai.content.prompt", "gen_ai.content.completion",
}

// StockHasMessages reports whether a chat span carries the conventions'
// message content (docs/record-contract.md §9): any key of the current
// family or the legacy ones. A derived run's MessageCount counts its
// chat spans that do.
func StockHasMessages(attrs map[string]any) bool {
	for _, k := range plainMessageKeys {
		if _, ok := attrs[k]; ok {
			return true
		}
	}
	for k := range attrs {
		if strings.HasPrefix(k, attrLegacyPrompt+".") || strings.HasPrefix(k, attrLegacyCompletion+".") {
			return true
		}
	}
	return false
}

// StockToolContent reads a derived run's execute_tool span's call
// content (docs/record-contract.md §9): its gen_ai.tool.call.arguments
// as JSON — a JSON string value as the JSON it holds, any other string
// as a JSON string, a structured value marshalled — and its
// gen_ai.tool.call.result as text (a string as is, a structured value
// as its JSON). hasArgs and hasResult say whether the span carried the
// attribute: absent, the producer captured no content, and the call's
// arguments and result stay unknown — never an empty value.
func StockToolContent(attrs map[string]any) (args json.RawMessage, result string, hasArgs, hasResult bool) {
	if v, ok := attrs[attrToolCallArguments]; ok && v != nil {
		hasArgs = true
		switch x := v.(type) {
		case string:
			if json.Valid([]byte(x)) {
				args = json.RawMessage(x)
			} else {
				args, _ = json.Marshal(x)
			}
		default:
			if b, err := json.Marshal(x); err == nil {
				args = b
			} else {
				hasArgs = false
			}
		}
	}
	if v, ok := attrs[attrToolCallResult]; ok && v != nil {
		hasResult = true
		switch x := v.(type) {
		case string:
			result = x
		default:
			if b, err := json.Marshal(x); err == nil {
				result = string(b)
			} else {
				hasResult = false
			}
		}
	}
	return args, result, hasArgs, hasResult
}

// The reasons a trace is not a run (StockNotRun), each with its fix:
// what a Python or TypeScript developer sets on the spans.
const (
	// notRunChatOnly: GenAI model or tool calls with no invoke_agent
	// span above them — a model-call instrumentation (openai-v2, an
	// HTTP handler calling the SDK) records no agent.
	notRunChatOnly = "model or tool calls only: no invoke_agent span opens a run (docs/record-contract.md §9) — the instrumentation records the calls, not an agent"
	// notRunChatOnlyFix is notRunChatOnly's fix.
	notRunChatOnlyFix = "wrap the agent's turn in an invoke_agent span (gen_ai.operation.name = invoke_agent) to get a run page; set gen_ai.conversation.id on it for a session"
	// notRunParentMissing: GenAI spans whose parent span is not stored
	// (yet): their agent span — or the span between — has not arrived,
	// or was recorded by a service that does not export here.
	notRunParentMissing = "its agent span has not arrived: a GenAI span's parent span is not stored here (yet) — exported later, dropped, or recorded by a service that does not export to this Studio (docs/record-contract.md §9)"
	// notRunParentMissingFix is notRunParentMissing's fix.
	notRunParentMissingFix = "wait for the batch that carries the parent (a BatchSpanProcessor exports a parent after its children), or export the parent's service to this Studio too"
	// notRunNoGenAI: a trace with no GenAI span at all.
	notRunNoGenAI = "no GenAI span: nothing in the trace carries gen_ai.operation.name invoke_agent, chat or execute_tool (docs/record-contract.md §9)"
	// notRunNoGenAIFix is notRunNoGenAI's fix.
	notRunNoGenAIFix = "instrument the agent with the OTel GenAI conventions (an invoke_agent span per turn) or a weft SDK (Go today)"
)

// StockNotRun says why a trace's spans make no run (§9): "" when any
// span carries weft.run.id (a weft run) or a derived run comes of them
// (DeriveStockRuns); else notRunParentMissing when a GenAI span's parent
// span is not stored (its agent span may still be on the way),
// notRunChatOnly when GenAI chat or execute_tool spans are there with
// no invoke_agent span to open a run, and
// notRunNoGenAI when no span is a GenAI operation. fix is the reason's.
func StockNotRun(spans []Span) (reason, fix string) {
	genai := false
	for _, s := range spans {
		if attr(s.Attrs, attrRunID) != "" {
			return "", ""
		}
		if stockOp(s) != "" {
			genai = true
		}
	}
	if !genai {
		return notRunNoGenAI, notRunNoGenAIFix
	}
	if len(DeriveStockRuns(spans)) > 0 {
		return "", ""
	}
	stored := make(map[string]bool, len(spans))
	for _, sp := range spans {
		stored[sp.SpanID] = true
	}
	for _, sp := range spans {
		if stockOp(sp) != "" && sp.ParentSpanID != "" && !stored[sp.ParentSpanID] {
			return notRunParentMissing, notRunParentMissingFix
		}
	}
	return notRunChatOnly, notRunChatOnlyFix
}
