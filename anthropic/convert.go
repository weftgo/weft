package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/adapterkit"
)

// params builds the Messages request for one step. The ModelRequest is
// read-only: conversion builds fresh SDK values and never mutates
// req.Messages or req.Tools.
func (m *model) params(req weft.ModelRequest) (anthropic.MessageNewParams, error) {
	p := anthropic.MessageNewParams{
		Model:    anthropic.Model(m.name),
		Messages: make([]anthropic.MessageParam, 0, len(req.Messages)),
	}
	// max_tokens is required by the API; 4096 when unset.
	if m.maxTokens > 0 {
		p.MaxTokens = int64(m.maxTokens)
	} else {
		p.MaxTokens = defaultMaxTokens
	}
	if req.System != "" {
		p.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if m.thinking {
		p.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
	}
	// Run-level thinking (TODO §5.14) overrides the construction
	// default: Off explicitly disables (relevant for models that think
	// by default), a Budget pins the depth, and a bare level defers the
	// depth to the model — adaptive, the same shape Thinking(true)
	// sends. Off wins over a contradictory Budget.
	switch {
	case req.Thinking.Level == weft.ThinkOff:
		p.Thinking = anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
		}
	case req.Thinking.Budget > 0:
		p.Thinking = anthropic.ThinkingConfigParamOfEnabled(req.Thinking.Budget)
	case req.Thinking.Level != weft.ThinkUnset:
		p.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
	}
	if m.tempSet {
		p.Temperature = anthropic.Float(m.temperature)
	}
	// Tool choice: the sequential hint and a forced choice share one
	// tool_choice entry — disable_parallel_tool_use rides whichever
	// member is chosen, one field on the wire, both hints kept (ADR
	// 0013's 2026-09-22 amendment). The API rejects tool_choice when
	// tools is empty, so it is sent only alongside a catalog — the
	// loop's validation already fails a forced choice without one.
	if len(req.Tools) > 0 {
		switch req.ToolChoice.Mode {
		case weft.ToolChoiceAny:
			any := &anthropic.ToolChoiceAnyParam{}
			if req.SequentialTools {
				any.DisableParallelToolUse = anthropic.Bool(true)
			}
			p.ToolChoice = anthropic.ToolChoiceUnionParam{OfAny: any}
		case weft.ToolChoiceNamed:
			tool := &anthropic.ToolChoiceToolParam{Name: req.ToolChoice.Name}
			if req.SequentialTools {
				tool.DisableParallelToolUse = anthropic.Bool(true)
			}
			p.ToolChoice = anthropic.ToolChoiceUnionParam{OfTool: tool}
		case weft.ToolChoiceNone:
			p.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		default:
			if req.SequentialTools {
				p.ToolChoice = anthropic.ToolChoiceUnionParam{
					OfAuto: &anthropic.ToolChoiceAutoParam{
						DisableParallelToolUse: anthropic.Bool(true),
					},
				}
			}
		}
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case weft.RoleUser:
			blocks, err := userBlocks(msg)
			if err != nil {
				return p, err
			}
			if len(blocks) > 0 {
				p.Messages = append(p.Messages, anthropic.NewUserMessage(blocks...))
			}
		case weft.RoleAssistant:
			// An assistant message with no sendable blocks (only
			// unsigned reasoning, say) is skipped: the API rejects
			// empty content arrays.
			if blocks := assistantBlocks(msg); len(blocks) > 0 {
				p.Messages = append(p.Messages, anthropic.NewAssistantMessage(blocks...))
			}
		case weft.RoleTool:
			// weft's batched tool message is already Anthropic's shape:
			// one user message, N tool_result blocks, in part order.
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
			for _, part := range msg.Content {
				tr, ok := part.(weft.ToolResultPart)
				if !ok {
					continue
				}
				// The API rejects empty text inside a tool_result
				// ("text content blocks must be non-empty"); an empty
				// tool output is data, not a failure, so it travels as
				// a visible placeholder.
				content := tr.Content
				if content == "" {
					content = "(empty tool output)"
				}
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.CallID, content, tr.IsError))
			}
			if len(blocks) > 0 {
				p.Messages = append(p.Messages, anthropic.NewUserMessage(blocks...))
			}
		}
	}
	// Converted per request: conversion is microseconds against the
	// network round trip, and the pointer-keyed cache this replaced
	// never evicted — an unbounded leak under a per-step ToolSource
	// (ADR 0013, 2026-09-18).
	for _, t := range req.Tools {
		p.Tools = append(p.Tools, convertTool(t))
	}
	return p, nil
}

// userBlocks converts a user message: text parts to text blocks, image
// and PDF file parts to image/document blocks (inline bytes base64, or
// a URL). Anything else, or a FilePart with both or neither of Data and
// URL, is refused wrapping ErrUnsupported.
func userBlocks(msg weft.Message) ([]anthropic.ContentBlockParamUnion, error) {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			if p.Text == "" {
				continue // an empty text block is API-rejected; it carries nothing
			}
			blocks = append(blocks, anthropic.NewTextBlock(p.Text))
		case weft.FilePart:
			if err := adapterkit.FilePartSource(p); err != nil {
				return nil, err
			}
			switch p.MediaType {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
				if p.URL != "" {
					blocks = append(blocks, anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: p.URL}))
					continue
				}
				blocks = append(blocks, anthropic.NewImageBlockBase64(p.MediaType, base64.StdEncoding.EncodeToString(p.Data)))
			case "application/pdf":
				if p.URL != "" {
					blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: p.URL}))
					continue
				}
				blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{
					Data:      base64.StdEncoding.EncodeToString(p.Data),
					MediaType: "application/pdf",
				}))
			default:
				return nil, fmt.Errorf("%w: anthropic accepts image and PDF files, got %q", weft.ErrUnsupported, p.MediaType)
			}
		}
	}
	// A user message must carry at least one non-empty block; the API
	// rejects empty content arrays. A message whose every part was
	// empty text (weft.User("")) keeps a visible placeholder rather
	// than silently vanishing from the transcript.
	if len(blocks) == 0 {
		blocks = append(blocks, anthropic.NewTextBlock("(empty message)"))
	}
	return blocks, nil
}

// assistantBlocks converts an assistant message in Anthropic's required
// block order: thinking first, then text, then tool_use. A ReasoningPart
// without a signature — a transcript that came from another provider —
// is dropped rather than failing the call; Anthropic rejects unsigned
// thinking blocks.
func assistantBlocks(msg weft.Message) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
	for _, part := range msg.Content {
		if r, ok := part.(weft.ReasoningPart); ok {
			if r.Signature != "" {
				blocks = append(blocks, anthropic.NewThinkingBlock(r.Signature, r.Text))
			}
			continue
		}
	}
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			if p.Text == "" {
				continue // an empty text block is API-rejected; it carries nothing
			}
			blocks = append(blocks, anthropic.NewTextBlock(p.Text))
		case weft.ToolCallPart:
			// The API requires an object; nil args (a hand-built call)
			// would travel as input:null and be rejected. Inbound, the
			// stream already normalises empty arguments to {}.
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			blocks = append(blocks, anthropic.NewToolUseBlock(p.ID, args, p.Name))
		}
	}
	return blocks
}

// convertTool converts a ToolDef once; results are cached by pointer on
// the model (ToolDef is immutable after construction).
func convertTool(t *weft.ToolDef) anthropic.ToolUnionParam {
	tool := anthropic.ToolParam{Name: t.Name}
	if t.Description != "" {
		tool.Description = anthropic.String(t.Description)
	}
	if t.InputSchema != nil {
		m := adapterkit.SchemaMap(t.InputSchema)
		schema := anthropic.ToolInputSchemaParam{}
		if props := m["properties"]; props != nil {
			schema.Properties = props
		}
		if req := t.InputSchema.Required; len(req) > 0 {
			schema.Required = req
		}
		// The SDK param has fields for properties, required and type
		// only; every other top-level keyword — additionalProperties
		// (typed map values), and a foreign schema's $defs, $schema,
		// oneOf, … (weft.ParseSchema) — rides ExtraFields onto the
		// wire, so the model sees the schema whole and a $ref inside
		// properties never dangles.
		for k, v := range m {
			switch k {
			case "properties", "required", "type":
				continue
			}
			if schema.ExtraFields == nil {
				schema.ExtraFields = map[string]any{}
			}
			schema.ExtraFields[k] = v
		}
		tool.InputSchema = schema
	}
	return anthropic.ToolUnionParam{OfTool: &tool}
}

// mapStopReason converts the wire stop_reason; the raw value rides on
// ModelFinish.Raw whenever the mapping is approximate, with the refusal
// category appended when the API names one.
func mapStopReason(reason string, category string) (weft.StopReason, string) {
	switch reason {
	case "end_turn":
		return weft.StopEndTurn, ""
	case "tool_use":
		return weft.StopToolCalls, ""
	case "max_tokens":
		return weft.StopMaxTokens, ""
	case "refusal":
		if category != "" {
			return weft.StopEndTurn, "refusal:" + category
		}
		return weft.StopEndTurn, reason
	default:
		// stop_sequence, pause_turn, anything newer: end the turn and
		// keep the vendor's word on Raw (empty stays empty).
		return weft.StopEndTurn, reason
	}
}
