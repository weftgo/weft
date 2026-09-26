package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
// AddTools panics on a nil tool, a duplicate name within one call (the
// SDK replaces same-named tools silently and exposes no lookup, so a
// name already registered by an earlier call is replaced, not
// refused), or a tool built with RequireApproval — MCP has no approval channel, and
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
		s.AddTool(sdkTool(t), invokeHandler(t))
	}
}

// sdkTool is a ToolDef as the SDK's Tool: name, description, the input
// schema (never nil — the SDK panics on nil, and a schema-less tool
// takes every object, which the empty object schema says exactly), and
// the output schema only when the tool has one.
func sdkTool(t *weft.ToolDef) *sdk.Tool {
	out := &sdk.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: toSDK(t.InputSchema),
	}
	if t.OutputSchema != nil {
		out.OutputSchema = toSDK(t.OutputSchema)
	}
	return out
}

// containedInvoke runs fn with the loop's panic containment
// (invokeContained's shape), so a panicking tool is the same isError
// result over MCP it would be inside a run, not a dead server.
func containedInvoke(name string, fn func() (string, error)) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool %q panicked: %v", name, p)
		}
	}()
	return fn()
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
		args := argumentBytes(req)
		out, err := containedInvoke(t.Name, func() (string, error) { return t.Invoke(ctx, args) })
		if err != nil {
			return errorResult(err), nil
		}
		return toolResult(t, out), nil
	}
}

// argumentBytes is the call's arguments as the bytes a weft handler
// receives — the client's own bytes, verbatim: Params.Arguments is the
// raw wire value, and re-marshalling it would compact whitespace and
// escape <, >, & (a RawTool that echoes or hashes args would see
// different bytes than the client sent). A client that omits arguments
// (the SDK's own always sends {}) would otherwise hand a RawTool
// "null" where tools/call promises an object, so empty and null
// become {} — the consume side's rule.
func argumentBytes(req *sdk.CallToolRequest) json.RawMessage {
	args := json.RawMessage(strings.TrimSpace(string(req.Params.Arguments)))
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	return args
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
// The tools exposed are the agent's registered ones (Agent.Tools);
// a ToolSource's snapshot is a per-step, runtime list and is not
// enumerated — its tools still run inside the agent tool's own steps.
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
	state := &serveState{}
	seen := map[string]bool{a.Name(): true}
	for _, t := range a.Tools() {
		if t == nil {
			continue
		}
		if seen[t.Name] {
			panic(fmt.Sprintf("weft/mcp: Serve: tool %q collides with the agent tool or an earlier tool", t.Name))
		}
		seen[t.Name] = true
		s.AddTool(sdkTool(t), chainHandler(a, t, state))
	}
}

// serveState numbers the calls one Serve dispatches, so middleware and
// audit logs see distinct ids exactly as the loop's calls would. It
// hangs off the Serve call, not the package: two servers in one process
// number independently, like two runs.
type serveState struct{ seq atomic.Int64 }

// chainHandler dispatches one of Serve's tool calls through the
// agent's chain: WrapTools middleware runs (mw.Allow, mw.Audit see the
// call), StrictInput applies at both levels, and a RequireApproval
// tool returns the ErrApprovalRequired error, which becomes an isError
// result reading `weft: tool call requires approval: tool "x"` — loud,
// never a bypass.
func chainHandler(a *weft.Agent, t *weft.ToolDef, state *serveState) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args := argumentBytes(req)
		call := weft.ToolCallPart{
			ID:   fmt.Sprintf("mcp_call_%d", state.seq.Add(1)),
			Name: t.Name,
			Args: args,
		}
		out, err := containedInvoke(t.Name, func() (string, error) { return a.CallTool(ctx, call) })
		if err != nil {
			return errorResult(err), nil
		}
		return toolResult(t, out), nil
	}
}

// toolResult is a successful tool text as MCP sees it: one text item,
// plus structuredContent — the same JSON the text carries — when the
// tool advertises an output schema. A schema'd tool whose text is not
// JSON is a breach of the tool's own contract; it becomes an isError
// result naming the breach (data the client model reads), because a
// CallToolResult carrying unserializable structuredContent is a reply
// that never goes out — the client blocks in the call forever.
func toolResult(t *weft.ToolDef, out string) *sdk.CallToolResult {
	res := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}
	if t.OutputSchema == nil {
		return res
	}
	if json.Valid([]byte(out)) {
		res.StructuredContent = json.RawMessage(out)
		return res
	}
	return errorResult(fmt.Errorf("tool %q advertised an output schema, so its result must be JSON; got non-JSON text", t.Name))
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
