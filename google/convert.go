package google

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/weftgo/weft"
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
			contents = append(contents, &genai.Content{Role: "user", Parts: parts})
		case weft.RoleAssistant:
			contents = append(contents, &genai.Content{Role: "model", Parts: modelParts(msg)})
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
		cfg.MaxOutputTokens = int32(m.maxTokens)
	}
	if m.tempSet {
		t := float32(m.temperature)
		cfg.Temperature = &t
	}
	for _, t := range req.Tools {
		converted, ok := m.tools.Load(t)
		if !ok {
			converted = convertTool(t)
			m.tools.Store(t, converted)
		}
		cfg.Tools = append(cfg.Tools, converted.(*genai.Tool))
	}
	// SequentialTools has no Gemini switch (function-calling config
	// stays AUTO) — a documented gap; see ADR 0013.
	return contents, cfg, nil
}

// userParts converts a user message: text to text parts, file parts to
// inline data (Gemini carries images, audio, and video natively) or a
// file URI. A FilePart with both or neither of Data and URL is refused
// wrapping ErrUnsupported.
func userParts(msg weft.Message) ([]*genai.Part, error) {
	parts := make([]*genai.Part, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			parts = append(parts, &genai.Part{Text: p.Text})
		case weft.FilePart:
			if (len(p.Data) == 0) == (p.URL == "") {
				return nil, fmt.Errorf("%w: a file part must set exactly one of Data or URL", weft.ErrUnsupported)
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

// convertTool converts a ToolDef once; results are cached by pointer on
// the model (ToolDef is immutable after construction).
func convertTool(t *weft.ToolDef) *genai.Tool {
	return &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  genaiSchema(t.InputSchema),
		}},
	}
}

// genaiSchema converts a weft.Schema to the SDK's; the type names map
// from JSON-Schema lowercase to the API's uppercase.
func genaiSchema(s *weft.Schema) *genai.Schema {
	if s == nil {
		return nil
	}
	out := &genai.Schema{
		Type:        genai.Type(upperType(s.Type)),
		Format:      s.Format,
		Description: s.Description,
		Required:    s.Required,
	}
	if s.Items != nil {
		out.Items = genaiSchema(s.Items)
	}
	if s.Properties != nil {
		out.Properties = make(map[string]*genai.Schema, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = genaiSchema(v)
		}
	}
	return out
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
