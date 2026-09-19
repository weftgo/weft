package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
)

// AddTools registers weft tools on an MCP server. Each tool is listed
// under its own name, description, input schema and — for a non-string
// output — output schema, and a call runs ToolDef.Invoke: the result
// text is returned as one text content item (and, when the tool has an
// output schema, as structuredContent holding the same JSON), and
// every failure — undecodable arguments, a handler error, a panic — is
// a tool result with isError, carrying the same text weft's loop would
// show its own model (ADR 0002's pinned bytes, over the wire). Run
// policy (Timeout, MaxResultBytes, WrapTools) is not applied: the
// exposition is the tool, not an agent; use Serve to export an agent's
// tools under its chain.
//
// AddTools panics on a nil tool, a duplicate name (the SDK replaces
// same-named tools silently; weft fails loud, like New), or a tool
// built with RequireApproval — MCP has no approval channel, and
// running a gated tool unapproved would be a silent policy bypass.
func AddTools(s *sdk.Server, tools ...*weft.ToolDef) {
	seen := map[string]bool{}
	for _, t := range tools {
		if t == nil {
			panic("weft/mcp: AddTools called with a nil tool")
		}
		if seen[t.Name] {
			panic(fmt.Sprintf("weft/mcp: AddTools: duplicate tool name %q", t.Name))
		}
		if t.RequiresApproval() {
			panic(fmt.Sprintf("weft/mcp: AddTools: tool %q requires approval and MCP has no approval channel; expose it through Serve, whose dispatch answers gated calls with the approval error", t.Name))
		}
		seen[t.Name] = true
		s.AddTool(&sdk.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: toSDK(t.InputSchema),
			OutputSchema: func() any {
				if t.OutputSchema == nil {
					return nil // only InputSchema must be non-nil
				}
				return toSDK(t.OutputSchema)
			}(),
		}, invokeHandler(t))
	}
}

// invokeHandler adapts ToolDef.Invoke to the SDK's raw ToolHandler —
// the raw form, not the generic AddTool[In, Out], which would reflect
// a second schema and decode a second time. The arguments cross as the
// bytes the client sent; Invoke decodes (or hands them to a RawTool
// handler verbatim) and renders the result text the model-facing rule
// already defines. Panic containment matches the loop's
// invokeContained, so a panicking tool is the same isError result over
// MCP it would be inside a run, not a dead server.
func invokeHandler(t *weft.ToolDef) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args, err := json.Marshal(req.Params.Arguments)
		if err != nil {
			return nil, err // arguments that cannot be encoded are a protocol failure
		}
		var out string
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("tool %q panicked: %v", t.Name, p)
				}
			}()
			out, err = t.Invoke(ctx, args)
		}()
		if err != nil {
			return errorResult(err), nil
		}
		res := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}
		if t.OutputSchema != nil {
			res.StructuredContent = json.RawMessage(out)
		}
		return res, nil
	}
}

// Serve exposes an agent over MCP: one tool named after the agent
// (weft.Name is required) that runs it on a prompt and returns its
// final text or submitted output, plus the agent's tools, each
// dispatched through the agent's tool chain (Agent.CallTool) so
// middleware and approval policy apply — an approval-gated tool
// answers with the ErrApprovalRequired text rather than running, loud
// where AddTools refuses outright. Run policy (Timeout,
// MaxResultBytes) is not applied, per CallTool's rule; the SDK's own
// request handling bounds the call.
//
// The agent tool is weft.Subagent under the name: the same
// {"prompt": string} input schema, the same Output handling, the same
// final-text rule — Serve is Subagent over the wire. description is
// what MCP clients (and their models) read to decide when to call the
// agent: it is the whole routing surface, as for Subagent, and it is
// written by the caller, never synthesised.
//
// Serve panics on a nil agent, an unnamed agent (the manifest's rule),
// or a name collision between the agent and one of its tools.
func Serve(s *sdk.Server, a *weft.Agent, description string) {
	if a == nil {
		panic("weft/mcp: Serve called with a nil agent")
	}
	if a.Name() == "" {
		panic("weft/mcp: Serve: the agent has no name; set one with weft.Name — the exposed tool is named after the agent")
	}
	AddTools(s, weft.Subagent(a.Name(), description, a))
	seen := map[string]bool{a.Name(): true}
	for _, t := range a.Tools() {
		if t == nil {
			continue
		}
		if seen[t.Name] {
			panic(fmt.Sprintf("weft/mcp: Serve: tool %q collides with the agent tool or an earlier tool", t.Name))
		}
		seen[t.Name] = true
		s.AddTool(&sdk.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: toSDK(t.InputSchema),
			OutputSchema: func() any {
				if t.OutputSchema == nil {
					return nil
				}
				return toSDK(t.OutputSchema)
			}(),
		}, chainHandler(a, t.Name))
	}
}

// serveCall numbers the calls Serve dispatches, so middleware and
// audit logs see distinct ids exactly as the loop's calls would.
var serveCall atomic.Int64

// chainHandler dispatches one of Serve's tool calls through the
// agent's chain: WrapTools middleware runs (mw.Allow, mw.Audit see the
// call), StrictInput applies at both levels, and a RequireApproval
// tool returns the ErrApprovalRequired error, which becomes an isError
// result reading `weft: tool call requires approval: tool "x"` — loud,
// never a bypass.
func chainHandler(a *weft.Agent, name string) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args, err := json.Marshal(req.Params.Arguments)
		if err != nil {
			return nil, err
		}
		call := weft.ToolCallPart{
			ID:   fmt.Sprintf("mcp_call_%d", serveCall.Add(1)),
			Name: name,
			Args: args,
		}
		var out string
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("tool %q panicked: %v", name, p)
				}
			}()
			out, err = a.CallTool(ctx, call)
		}()
		if err != nil {
			return errorResult(err), nil
		}
		res := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}
		return res, nil
	}
}

// errorResult is every model-recoverable failure as MCP sees it: a
// tool result with isError and the error's own text — data the client
// model reads and corrects, never a protocol error.
func errorResult(err error) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}},
	}
}
