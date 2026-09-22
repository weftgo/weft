package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/adapterkit"
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
			// An assistant message with nothing sendable (only dropped
			// reasoning, say) is skipped: Chat Completions rejects a
			// message with neither content nor tool_calls — the
			// empty-content rules of ADR 0013's 2026-09-14 amendment.
			if am, ok := assistantMessage(msg); ok {
				p.Messages = append(p.Messages, am)
			}
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
	// Converted per request: a 2026-09-18 measurement put one
	// conversion at ~2.4µs (openai.BenchmarkConvertTool) against the
	// network round trip every request pays, and the pointer-keyed
	// cache this replaced never evicted — an unbounded leak under a
	// ToolSource that rebuilds its snapshot per step (ADR 0013).
	for _, t := range req.Tools {
		p.Tools = append(p.Tools, convertTool(t))
	}
	if m.maxTokens > 0 {
		p.MaxCompletionTokens = openai.Int(int64(m.maxTokens))
	}
	if m.tempSet {
		p.Temperature = openai.Float(m.temperature)
	}
	// The API rejects parallel-tool-call hints without a tools list on
	// several OpenAI-compatible servers, so the hint is sent only
	// alongside a catalog — an agent without tools has nothing to
	// serialize anyway (the anthropic adapter's guard, ported).
	if req.SequentialTools && len(req.Tools) > 0 {
		p.ParallelToolCalls = openai.Bool(false)
	}
	// Tool-choice forcing (TODO §2a.1): the zero config sends nothing.
	// "required" is Chat Completions' any-tool form; a named choice is
	// the function variant of the same union.
	switch req.ToolChoice.Mode {
	case weft.ToolChoiceAny:
		p.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("required")}
	case weft.ToolChoiceNamed:
		p.ToolChoice = openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(
			openai.ChatCompletionNamedToolChoiceFunctionParam{Name: req.ToolChoice.Name})
	case weft.ToolChoiceNone:
		p.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("none")}
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
// wrapping ErrUnsupported — Chat Completions accepts images only. An
// empty text part carries nothing and is never emitted, and a message
// whose every part was empty keeps a visible placeholder: an empty
// content array is API-rejected.
func userParts(msg weft.Message) ([]openai.ChatCompletionContentPartUnionParam, error) {
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			if p.Text == "" {
				continue
			}
			parts = append(parts, openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{Text: p.Text},
			})
		case weft.FilePart:
			if !strings.HasPrefix(p.MediaType, "image/") {
				return nil, fmt.Errorf("%w: chat completions accept image files, got %q", weft.ErrUnsupported, p.MediaType)
			}
			if err := adapterkit.FilePartSource(p); err != nil {
				return nil, err
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
	if len(parts) == 0 {
		parts = append(parts, openai.ChatCompletionContentPartUnionParam{
			OfText: &openai.ChatCompletionContentPartTextParam{Text: "(empty message)"},
		})
	}
	return parts, nil
}

// assistantMessage converts an assistant message: text parts join into
// the content string, tool calls become tool_calls entries, and
// reasoning parts are dropped — Chat Completions has no reasoning
// input to send back. ok is false when nothing sendable remains (no
// text, no calls): the caller skips the message rather than emitting
// `{"role":"assistant"}`, which the API rejects.
func assistantMessage(msg weft.Message) (openai.ChatCompletionMessageParamUnion, bool) {
	am := &openai.ChatCompletionAssistantMessageParam{}
	var sb strings.Builder
	for _, part := range msg.Content {
		switch p := part.(type) {
		case weft.TextPart:
			sb.WriteString(p.Text)
		case weft.ToolCallPart:
			// The API requires valid JSON arguments; a hand-built call
			// with nil args would travel as arguments:"" and be
			// rejected. The stream side and the other adapters
			// normalize empty arguments to {} — this side now matches.
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			am.ToolCalls = append(am.ToolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: p.ID,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      p.Name,
					Arguments: string(args),
				},
			})
		}
	}
	if sb.Len() > 0 {
		am.Content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(sb.String())}
	}
	if sb.Len() == 0 && len(am.ToolCalls) == 0 {
		return openai.ChatCompletionMessageParamUnion{}, false
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: am}, true
}

// convertTool converts a ToolDef once; results are cached by pointer on
// the model (ToolDef is immutable after construction).
func convertTool(t *weft.ToolDef) openai.ChatCompletionToolParam {
	fn := shared.FunctionDefinitionParam{Name: t.Name}
	if t.Description != "" {
		fn.Description = openai.String(t.Description)
	}
	if t.InputSchema != nil {
		fn.Parameters = shared.FunctionParameters(adapterkit.SchemaMap(t.InputSchema))
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
