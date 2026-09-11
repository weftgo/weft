package anthropic

import (
	"encoding/base64"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/weftgo/weft"
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
	if m.tempSet {
		p.Temperature = anthropic.Float(m.temperature)
	}
	// The API rejects tool_choice when tools is empty, so the hint is
	// sent only alongside a catalog — an agent without tools has nothing
	// to serialize anyway.
	if req.SequentialTools && len(req.Tools) > 0 {
		p.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{
				DisableParallelToolUse: anthropic.Bool(true),
			},
		}
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case weft.RoleUser:
			blocks, err := userBlocks(msg)
			if err != nil {
				return p, err
			}
			p.Messages = append(p.Messages, anthropic.NewUserMessage(blocks...))
		case weft.RoleAssistant:
			p.Messages = append(p.Messages, anthropic.NewAssistantMessage(assistantBlocks(msg)...))
		case weft.RoleTool:
			// weft's batched tool message is already Anthropic's shape:
			// one user message, N tool_result blocks, in part order.
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
			for _, part := range msg.Content {
				tr, ok := part.(weft.ToolResultPart)
				if !ok {
					continue
				}
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.CallID, tr.Content, tr.IsError))
			}
			if len(blocks) > 0 {
				p.Messages = append(p.Messages, anthropic.NewUserMessage(blocks...))
			}
		}
	}
	for _, t := range req.Tools {
		converted, ok := m.tools.Load(t)
		if !ok {
			converted = convertTool(t)
			m.tools.Store(t, converted)
		}
		p.Tools = append(p.Tools, converted.(anthropic.ToolUnionParam))
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
			blocks = append(blocks, anthropic.NewTextBlock(p.Text))
		case weft.FilePart:
			if (len(p.Data) == 0) == (p.URL == "") {
				return nil, fmt.Errorf("%w: a file part must set exactly one of Data or URL", weft.ErrUnsupported)
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
			blocks = append(blocks, anthropic.NewTextBlock(p.Text))
		case weft.ToolCallPart:
			blocks = append(blocks, anthropic.NewToolUseBlock(p.ID, p.Args, p.Name))
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
		schema := anthropic.ToolInputSchemaParam{}
		if props := schemaMap(t.InputSchema)["properties"]; props != nil {
			schema.Properties = props
		}
		if req := t.InputSchema.Required; len(req) > 0 {
			schema.Required = req
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

// schemaMap renders a weft.Schema as a plain JSON map.
func schemaMap(s *weft.Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{"type": s.Type}
	if s.Format != "" {
		m["format"] = s.Format
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Items != nil {
		m["items"] = schemaMap(s.Items)
	}
	if s.Properties != nil {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaMap(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	return m
}
