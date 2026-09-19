package google

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/adapterkit"
	"google.golang.org/genai"
)

// contents converts the transcript and system instruction for one
// step. The ModelRequest is read-only: conversion builds fresh SDK
// values and never mutates req.Messages or req.Tools.
func (m *model) contents(req weft.ModelRequest) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	contents := make([]*genai.Content, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch msg.Role {
		case weft.RoleUser:
			parts, err := userParts(msg)
			if err != nil {
				return nil, nil, err
			}
			// A Content with zero parts serializes as {"role":"user"}
			// — the API rejects it — so a message whose every part was
			// empty text is skipped (ADR 0013's 2026-09-14 empty-content
			// rules).
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: "user", Parts: parts})
			}
		case weft.RoleAssistant:
			// Same rule for the model role: unsigned reasoning drops and
			// can leave nothing sendable.
			if parts := modelParts(msg); len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: "model", Parts: parts})
			}
		case weft.RoleTool:
			// One step's results travel on one user content with N
			// functionResponse parts, in part order — the shape Gemini
			// documents for parallel calls; the API matches responses to
			// calls by name and position.
			parts := make([]*genai.Part, 0, len(msg.Content))
			for _, part := range msg.Content {
				tr, ok := part.(weft.ToolResultPart)
				if !ok {
					continue
				}
				resp := map[string]any{"output": tr.Content}
				if tr.IsError {
					// Gemini has no is_error flag; the key is the signal.
					resp = map[string]any{"error": tr.Content}
				}
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:       tr.CallID,
						Name:     tr.Name,
						Response: resp,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: "user", Parts: parts})
			}
		}
	}
	cfg := &genai.GenerateContentConfig{}
	if req.System != "" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: req.System}}}
	}
	if m.maxTokens > 0 {
		// The API's limit is an int32; narrowing silently would wrap a
		// large cap into a garbage (possibly negative) limit on the
		// wire — fail naming the ceiling instead ("adapters document
		// what they drop", ADR 0013).
		if int64(m.maxTokens) > math.MaxInt32 {
			return nil, nil, fmt.Errorf("%w: MaxTokens %d exceeds Gemini's int32 limit", weft.ErrUnsupported, m.maxTokens)
		}
		cfg.MaxOutputTokens = int32(m.maxTokens)
	}
	if m.tempSet {
		t := float32(m.temperature)
		cfg.Temperature = &t
	}
	// Run-level thinking (TODO §5.14): Off disables (a zero budget is
	// Gemini's off switch), a Budget pins depth, a bare level maps to
	// Gemini's thinkingLevel. An explicit level or budget asks for
	// thought summaries back, so reasoning streams; the default sends
	// nothing and keeps the model's own behavior.
	switch {
	case req.Thinking.Level == weft.ThinkOff:
		zero := int32(0)
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: &zero}
	case req.Thinking.Budget > 0:
		// The budget is an int32 on the wire; see MaxTokens above.
		if req.Thinking.Budget > math.MaxInt32 {
			return nil, nil, fmt.Errorf("%w: Thinking Budget %d exceeds Gemini's int32 limit", weft.ErrUnsupported, req.Thinking.Budget)
		}
		b := int32(req.Thinking.Budget)
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: &b, IncludeThoughts: true}
	case req.Thinking.Level != weft.ThinkUnset:
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: geminiLevel(req.Thinking.Level), IncludeThoughts: true}
	}
	// Converted per request: conversion is microseconds against the
	// network round trip, and the pointer-keyed cache this replaced
	// never evicted — an unbounded leak under a per-step ToolSource
	// (ADR 0013, 2026-09-18).
	for _, t := range req.Tools {
		cfg.Tools = append(cfg.Tools, convertTool(t))
	}
	// SequentialTools has no Gemini switch (function-calling config
	// stays AUTO) — a documented gap; see ADR 0013.
	return contents, cfg, nil
}

// geminiLevel maps the neutral scale onto Gemini's own; an unmapped
// level (Unset never reaches here, Off is handled by the budget switch)
// lands on medium.
func geminiLevel(l weft.ThinkingLevel) genai.ThinkingLevel {
	switch l {
	case weft.ThinkLow:
		return genai.ThinkingLevelLow
	case weft.ThinkHigh:
		return genai.ThinkingLevelHigh
	default:
		return genai.ThinkingLevelMedium
	}
}

// userParts converts a user message: text to text parts, file parts to
// inline data (Gemini carries images, audio, and video natively) or a
// file URI. A FilePart with both or neither of Data and URL is refused
// wrapping ErrUnsupported. Empty text parts are skipped: the SDK omits
// empty text, so the part would serialize as a bare {} on the wire.
func userParts(msg weft.Message) ([]*genai.Part, error) {
	parts := make([]*genai.Part, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			if p.Text == "" {
				continue
			}
			parts = append(parts, &genai.Part{Text: p.Text})
		case weft.FilePart:
			if err := adapterkit.FilePartSource(p); err != nil {
				return nil, err
			}
			if p.URL != "" {
				parts = append(parts, &genai.Part{
					FileData: &genai.FileData{FileURI: p.URL, MIMEType: p.MediaType},
				})
				continue
			}
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{MIMEType: p.MediaType, Data: p.Data},
			})
		}
	}
	return parts, nil
}

// modelParts converts an assistant message: text, reasoning, and whole
// function calls (args arrive as raw JSON; the API takes a map).
//
// Thought signatures go back where Gemini issued them — on the same
// function call part when the turn calls tools (the API validates they
// return there), on a thought part otherwise. Each call carries its own
// recorded signature; a transcript from before per-call signatures
// (which kept the step's single signature on the ReasoningPart) falls
// back to returning that one on the first call. Unsigned reasoning — a
// transcript from another provider — is dropped rather than failing the
// call. Signatures are stored base64 (see Stream); decoding failures
// send the value as-is.
func modelParts(msg weft.Message) []*genai.Part {
	var (
		parts     []*genai.Part
		hasCalls  bool
		callSigs  bool   // any call recorded its own signature
		legacy    []byte // the pre-per-call step signature, if any
		legacySet bool
	)
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.ReasoningPart:
			if p.Signature != "" && !legacySet {
				legacy, legacySet = decodeSignature(p.Signature), true
			}
		case weft.ToolCallPart:
			hasCalls = true
			if p.Signature != "" {
				callSigs = true
			}
		}
	}
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			if p.Text == "" {
				continue // the SDK omits empty text; a bare {} part is rejected
			}
			parts = append(parts, &genai.Part{Text: p.Text})
		case weft.ReasoningPart:
			if p.Signature == "" || hasCalls {
				continue
			}
			parts = append(parts, &genai.Part{
				Text:             p.Text,
				Thought:          true,
				ThoughtSignature: decodeSignature(p.Signature),
			})
		case weft.ToolCallPart:
			args := map[string]any{}
			if len(p.Args) > 0 {
				_ = json.Unmarshal(p.Args, &args)
			}
			fc := &genai.Part{
				FunctionCall: &genai.FunctionCall{ID: p.ID, Name: p.Name, Args: args},
			}
			switch {
			case p.Signature != "":
				fc.ThoughtSignature = decodeSignature(p.Signature)
			case legacySet && !callSigs:
				fc.ThoughtSignature = legacy
				legacySet = false // the first call returns the step's signature
			}
			parts = append(parts, fc)
		}
	}
	return parts
}

// encodeSignature renders the API's opaque signature bytes as the
// string ReasoningPart.Signature carries: base64, so a recorded
// transcript survives encoding/json (raw bytes need not be UTF-8).
func encodeSignature(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// decodeSignature inverts encodeSignature; a value that is not base64
// (hand-written, or from another adapter) is sent as its raw bytes.
func decodeSignature(s string) []byte {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}

// convertTool converts a ToolDef to the SDK's tool declaration.
func convertTool(t *weft.ToolDef) *genai.Tool {
	return &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  genaiSchema(t.InputSchema),
		}},
	}
}

// genaiSchema converts a weft.Schema to the SDK's by JSON round trip:
// json.Marshal (which emits a foreign schema parsed with
// weft.ParseSchema verbatim) then json.Unmarshal into genai.Schema, so
// every field the vendor type has is carried and every field it lacks
// — additionalProperties, oneOf, pattern, … — is dropped by the
// decoder: the same rule the hand-built mapping this replaced applied
// to AdditionalProperties alone, now per field. A Gemini limit, not a
// weft one. genai.Type is an uppercase enum, so the decoded tree is
// normalised through upperType; a decode error (a foreign schema with
// a value where a string belongs) falls back to the structured fields
// alone. Unconstrained nodes (type "") are unchanged: the API takes
// the empty type as "any", as before.
func genaiSchema(s *weft.Schema) *genai.Schema {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		// Unreachable: see adapterkit.SchemaMap.
		return &genai.Schema{}
	}
	var out genai.Schema
	if err := json.Unmarshal(b, &out); err != nil {
		return &genai.Schema{
			Type:        genai.Type(upperType(s.Type)),
			Format:      s.Format,
			Description: s.Description,
			Required:    s.Required,
		}
	}
	normalizeGenaiTypes(&out)
	return &out
}

// normalizeGenaiTypes uppercases the type names of a decoded schema
// tree in place: JSON Schema says "string", the genai enum says
// "STRING", and every nested node needs the same fix.
func normalizeGenaiTypes(s *genai.Schema) {
	s.Type = genai.Type(upperType(string(s.Type)))
	for _, p := range s.Properties {
		normalizeGenaiTypes(p)
	}
	for _, a := range s.AnyOf {
		normalizeGenaiTypes(a)
	}
	if s.Items != nil {
		normalizeGenaiTypes(s.Items)
	}
}

func upperType(t string) string {
	up := map[string]string{
		"string": "STRING", "number": "NUMBER", "integer": "INTEGER",
		"boolean": "BOOLEAN", "array": "ARRAY", "object": "OBJECT",
	}
	if v, ok := up[t]; ok {
		return v
	}
	return t
}

// mapFinish converts the candidate's finish reason; a candidate with
// function calls is a tool step regardless (Gemini reports STOP), and
// unmapped reasons keep their raw value on ModelFinish.Raw.
func mapFinish(reason genai.FinishReason, hasCalls bool) (weft.StopReason, string) {
	switch reason {
	case genai.FinishReasonStop:
		if hasCalls {
			return weft.StopToolCalls, ""
		}
		return weft.StopEndTurn, ""
	case genai.FinishReasonMaxTokens:
		return weft.StopMaxTokens, ""
	case "":
		if hasCalls {
			return weft.StopToolCalls, ""
		}
		return weft.StopEndTurn, ""
	default:
		return weft.StopEndTurn, string(reason)
	}
}
