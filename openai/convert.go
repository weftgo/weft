package openai

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"github.com/weftgo/weft"
)

// params builds the Chat Completions request for one step. The
// ModelRequest is read-only: conversion builds fresh SDK values and
// never mutates req.Messages or req.Tools.
func (m *model) params(req weft.ModelRequest) (openai.ChatCompletionNewParams, error) {
	p := openai.ChatCompletionNewParams{
		Model:    m.name,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1),
		// The final chunk carries the whole request's usage.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if req.System != "" {
		p.Messages = append(p.Messages, openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: openai.String(req.System)},
			},
		})
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case weft.RoleUser:
			parts, err := userParts(msg)
			if err != nil {
				return p, err
			}
			p.Messages = append(p.Messages, openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfArrayOfContentParts: parts,
					},
				},
			})
		case weft.RoleAssistant:
			p.Messages = append(p.Messages, assistantMessage(msg))
		case weft.RoleTool:
			// weft batches one step's results on a single tool message;
			// OpenAI wants one tool message per result, in part order.
			for _, part := range msg.Content {
				tr, ok := part.(weft.ToolResultPart)
				if !ok {
					continue
				}
				p.Messages = append(p.Messages, openai.ChatCompletionMessageParamUnion{
					OfTool: &openai.ChatCompletionToolMessageParam{
						Content: openai.ChatCompletionToolMessageParamContentUnion{
							OfString: openai.String(tr.Content),
						},
						ToolCallID: tr.CallID,
					},
				})
			}
		}
	}
	for _, t := range req.Tools {
		converted, ok := m.tools.Load(t)
		if !ok {
			converted = convertTool(t)
			m.tools.Store(t, converted)
		}
		p.Tools = append(p.Tools, converted.(openai.ChatCompletionToolParam))
	}
	if m.maxTokens > 0 {
		p.MaxCompletionTokens = openai.Int(int64(m.maxTokens))
	}
	if m.tempSet {
		p.Temperature = openai.Float(m.temperature)
	}
	if req.SequentialTools {
		p.ParallelToolCalls = openai.Bool(false)
	}
	// Effort dialect: reasoning_effort carries the level (the gateway
	// dialect injects its thinking object at the HTTP layer instead —
	// see thinking.go). ThinkOff and a Budget have no effort form and
	// are documented gaps, not silent guesses.
	if m.dialect == DialectEffort {
		if e := reasoningEffort(req.Thinking); e != "" {
			p.ReasoningEffort = e
		}
	}
	return p, nil
}

// userParts converts a user message's parts to OpenAI content parts.
// Images become image_url parts (inline bytes as a data URL); any other
// file, or a FilePart with both or neither of Data/URL, is refused
// wrapping ErrUnsupported — Chat Completions accepts images only.
func userParts(msg weft.Message) ([]openai.ChatCompletionContentPartUnionParam, error) {
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			parts = append(parts, openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{Text: p.Text},
			})
		case weft.FilePart:
			if !strings.HasPrefix(p.MediaType, "image/") {
				return nil, fmt.Errorf("%w: chat completions accept image files, got %q", weft.ErrUnsupported, p.MediaType)
			}
			if (len(p.Data) == 0) == (p.URL == "") {
				return nil, fmt.Errorf("%w: a file part must set exactly one of Data or URL", weft.ErrUnsupported)
			}
			u := p.URL
			if u == "" {
				u = "data:" + p.MediaType + ";base64," + base64.StdEncoding.EncodeToString(p.Data)
			}
			parts = append(parts, openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: u},
				},
			})
		}
	}
	return parts, nil
}

// assistantMessage converts an assistant message: text parts join into
// the content string, tool calls become tool_calls entries, and
// reasoning parts are dropped — Chat Completions has no reasoning
// input to send back.
func assistantMessage(msg weft.Message) openai.ChatCompletionMessageParamUnion {
	am := &openai.ChatCompletionAssistantMessageParam{}
	var sb strings.Builder
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			sb.WriteString(p.Text)
		case weft.ToolCallPart:
			am.ToolCalls = append(am.ToolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: p.ID,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      p.Name,
					Arguments: string(p.Args),
				},
			})
		}
	}
	if sb.Len() > 0 {
		am.Content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(sb.String())}
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: am}
}

// convertTool converts a ToolDef once; results are cached by pointer on
// the model (ToolDef is immutable after construction).
func convertTool(t *weft.ToolDef) openai.ChatCompletionToolParam {
	fn := shared.FunctionDefinitionParam{Name: t.Name}
	if t.Description != "" {
		fn.Description = openai.String(t.Description)
	}
	if t.InputSchema != nil {
		fn.Parameters = shared.FunctionParameters(schemaMap(t.InputSchema))
	}
	return openai.ChatCompletionToolParam{Function: fn}
}

// mapFinish converts the wire finish_reason to weft's StopReason. The
// raw value rides on ModelFinish.Raw whenever the mapping is
// approximate, so callers can see "content_filter" and friends without
// a tap.
func mapFinish(reason string, hasCalls bool) (weft.StopReason, string) {
	switch reason {
	case "stop":
		return weft.StopEndTurn, ""
	case "tool_calls":
		return weft.StopToolCalls, ""
	case "length":
		return weft.StopMaxTokens, ""
	case "":
		// Compatible servers may omit it; buffered calls are the truth.
		if hasCalls {
			return weft.StopToolCalls, ""
		}
		return weft.StopEndTurn, ""
	default:
		return weft.StopEndTurn, reason
	}
}

func toUsage(u openai.CompletionUsage) weft.Usage {
	return weft.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
}

// schemaMap renders a weft.Schema as the plain JSON map the SDK's tool
// parameters expect.
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
