package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft/core"
)

// ErrToolError is the cause on a remote isError result — data for the
// model, a sentinel for middleware.
var ErrToolError = errors.New("mcp: tool returned an error")

// Tools lists the tools of a connected MCP session and returns each as
// a weft tool: the server's name and description, its input schema as
// the SDK received it — every keyword preserved, an enum or a oneOf
// the core cannot express reaching the model whole (the SDK's client
// hands over decoded JSON, so the kept bytes are the document in
// encoding/json's canonical key order) — and
// a handler that forwards the model's argument bytes to tools/call. A
// remote result is returned as text (structuredContent's JSON when the
// server sends it, else the text items joined by newlines; non-text
// items render as one-line markers, because weft's transcript carries
// no binary tool results); a remote isError result is a tool error
// carrying the server's text verbatim (errors.Is ErrToolError; an
// empty one reads ErrToolError's own text); a
// transport failure is a tool error reading "mcp: <err>", and so is a
// JSON-RPC error answer (the server's handler failed, the tool is not
// listed). All three are data the model sees, never run errors; their
// execute_tool spans tell them apart by error.type — mcp_tool_error
// (isError), mcp_rpc_error (the server refused the call) and
// mcp_transport (no answer: the connection, the session closed).
//
// Every imported tool names where it came from for the record (O8):
// its Origin is "mcp:<server>" under Server ("mcp" otherwise) and its
// core.OriginDetail the server, the Transport, the Prefix and its name
// on the server, with the tools this listing skipped — so the tools
// record carries source_detail per tool and skipped[] per server, and
// each call's execute_tool span weft.tool.origin. A transport failure
// (error.type mcp_transport), a JSON-RPC error answer (mcp_rpc_error)
// and a remote isError (mcp_tool_error) stay told apart on the span. A
// server that dies mid-call on a transport that never reports it (the
// SDK's in-memory pipe) leaves the call to its deadline: give imported
// tools a Timeout through Policy. A server all of whose tools were
// skipped imports nothing to carry the skips: ImportError.Option
// records them on the agent.
//
// Imported tools are ordinary weft tools: register them with New or
// serve them through ToolSource; Timeout, MaxResultBytes,
// RequireApproval and WrapTools apply through Policy. A tool the
// server does not mark readOnlyHint is Sequential — foreign tools run
// one at a time unless the server says parallel is safe (annotations
// are untrusted hints; the conservative reading is the safe one).
// Progress notifications are not surfaced: the delegating call's
// ToolStart and ToolFinish bracket the remote call. Elicitation is
// not answered: a server that asks fails the call, as a tool error.
//
// The SDK's client decodes the listed schema before Tools sees it, so
// the kept bytes are the document re-encoded: keys in canonical
// order, and an integer beyond 2^53 (a maximum on an id field, say)
// rounded through float64 — the one loss the bridge cannot avoid
// short of the SDK keeping raw bytes.
//
// Tools returns when every page is listed or ctx ends; it returns the
// ctx error unchanged when the deadline hits mid-list. Connect
// several servers concurrently with an errgroup and one deadline; for
// a list that changes, set the SDK's ToolListChangedHandler to
// re-run Tools into a slice you serve through core.ToolSource, under
// a mutex (the SDK runs the handler on its own goroutine, the loop
// reads the source on the run's; ExampleTools_toolSource shows the
// shape) — Tools
// does not own the session and never closes it. The kill switch
// (WEFT_MODEL_REQUESTS) governs model requests; tools/call is not
// one, so imported tools run under deny — they are the caller's
// tools, like any handler that makes an HTTP call.
//
// A tool whose output schema the server sends is recorded on the
// ToolDef when it is an object schema; a non-object output schema
// (legal per the spec) has no weft representation and is not recorded
// — the result text is the tool's output either way.
//
// A tool that cannot be imported — an input schema the core cannot
// key on (a non-object root, a $ref root, an omitted schema), an
// empty name, a name repeated in the listing (the first importable
// occurrence stands: an earlier entry that was itself skipped claims
// nothing), a nil entry — fails that tool, not the listing: the
// importable tools are returned and the error is an *ImportError
// naming each skipped tool and why (ADR 0015's 2026-09-27 amendment).
// The slice is usable whether or not err is nil; the caller decides
// whether a skipped tool is a warning to log or a reason to stop:
//
//	tools, err := mcp.Tools(ctx, sess)
//	var skipped *mcp.ImportError
//	if errors.As(err, &skipped) {
//		slog.Warn("mcp: tools skipped", "err", skipped)
//		err = nil
//	}
//	if err != nil { // a listing failure: transport, ctx
//		return err
//	}
//
// Nothing is dropped silently — a server with one broken tool no
// longer takes its ninety-nine good ones down, and the one is still
// named. A listing failure (transport, the ctx ending) is the ordinary
// error with no tools.
func Tools(ctx context.Context, sess *sdk.ClientSession, opts ...Option) ([]*core.ToolDef, error) {
	cfg := importConfig{}
	for _, o := range opts {
		if o != nil {
			o.apply(&cfg)
		}
	}
	type accepted struct {
		t      *sdk.Tool
		name   string
		schema *core.Schema
	}
	var keep []accepted
	var skipped []SkippedTool
	seen := map[string]bool{}
	index := -1
	for t, err := range sess.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		index++
		if t == nil {
			skipped = append(skipped, SkippedTool{Index: index, Err: errors.New("nil entry in the tool list")})
			continue
		}
		if t.Name == "" {
			// Server-provided content is untrusted: a hostile or buggy
			// server listing {"name": ""} would panic RawTool at import
			// time — and checked on the composed name, as this once
			// was, a Prefix hid it: {"name": ""} under Prefix("gh_")
			// imported as a tool called "gh_" whose handler called the
			// remote tool "". The nil-entry check above is the same rule.
			skipped = append(skipped, SkippedTool{Index: index, Err: errors.New("empty name")})
			continue
		}
		name := cfg.prefix + t.Name
		if seen[name] {
			// A repeated name in one listing is the same untrusted
			// input: both would import and core.New would panic on
			// the duplicate later. The first importable occurrence
			// stands, the repeat is reported — seen is set only when a
			// tool is appended, so a skipped first entry does not block
			// a later valid one under the same name.
			skipped = append(skipped, SkippedTool{Name: t.Name, Index: index, Err: fmt.Errorf("duplicate name %q in the listing", t.Name)})
			continue
		}
		raw, err := fromSDK(t.InputSchema)
		if err != nil {
			skipped = append(skipped, SkippedTool{Name: t.Name, Index: index, Err: err})
			continue
		}
		schema, err := core.ParseSchema(raw)
		if err != nil {
			skipped = append(skipped, SkippedTool{Name: t.Name, Index: index, Err: err})
			continue
		}
		keep = append(keep, accepted{t: t, name: name, schema: schema})
		seen[name] = true
	}
	// The record's identity (O8): every imported tool names the server
	// in its Origin ("mcp:<server>", "mcp" when unnamed) and its parts
	// in OriginDetail, the skipped tools riding along so the tools
	// record lists them (record contract §4).
	origin := "mcp"
	if cfg.server != "" {
		origin += ":" + cfg.server
	}
	gone := skippedImports(skipped)
	out := make([]*core.ToolDef, 0, len(keep)) // non-nil: zero tools is a fact, not an error
	for _, k := range keep {
		t := k.t
		// Annotation default first, the caller's policy after: a
		// missing readOnlyHint cannot be un-Sequentialised, and
		// RequireApproval is added, never removed. The Origin first,
		// so a caller's own can still rename it.
		toolOpts := []core.ToolOption{core.Origin(origin), core.OriginDetail(core.SourceDetail{
			Server: cfg.server, Transport: cfg.transport, Prefix: cfg.prefix, RemoteName: t.Name, Skipped: gone,
		})}
		if t.Annotations == nil || !t.Annotations.ReadOnlyHint {
			toolOpts = append(toolOpts, core.Sequential())
		}
		toolOpts = append(toolOpts, cfg.policy...)
		tool := core.RawTool(k.name, t.Description, k.schema,
			callHandler(sess, t.Name), toolOpts...)
		if t.OutputSchema != nil {
			if raw, err := fromSDK(t.OutputSchema); err == nil {
				if outSchema, err := core.ParseSchema(raw); err == nil {
					tool.OutputSchema = outSchema
				}
			}
		}
		out = append(out, tool)
	}
	if len(skipped) > 0 {
		return out, &ImportError{Skipped: skipped, Source: origin}
	}
	return out, nil
}

// ImportError is Tools' report of the tools it could not import. It
// travels beside the importable tools, not instead of them — the
// partial-result shape *core.RunError uses (ADR 0002): the caller
// reads it with errors.As and decides whether a skipped tool is a
// warning or a stop. Unwrap exposes each tool's cause, so errors.Is
// against a ParseSchema failure still works through the report.
type ImportError struct {
	Skipped []SkippedTool
	// Source is the imported tools' Origin ("mcp:<server>", "mcp"):
	// the tools record's skipped[] names it (U23.2-fix).
	Source string
}

// Option records the skipped tools on an agent that offers none of the
// server's tools — every one was skipped, so no imported tool carries
// them into the tools record (core.SkippedImports, U23.2-fix). Harmless
// beside importable tools: the record lists each skip once.
//
//	tools, err := mcp.Tools(ctx, sess, mcp.Server("billing"))
//	var skipped *mcp.ImportError
//	if errors.As(err, &skipped) {
//		opts = append(opts, skipped.Option())
//	}
func (e *ImportError) Option() core.Option {
	return core.SkippedImports(e.Source, skippedImports(e.Skipped)...)
}

// skippedImports renders the skipped tools for the record: the name on
// the server, the cause's text (the listing entry named when the name
// itself was the problem).
func skippedImports(skipped []SkippedTool) []core.SkippedImport {
	out := make([]core.SkippedImport, 0, len(skipped))
	for _, sk := range skipped {
		reason := "skipped"
		if sk.Err != nil {
			reason = sk.Err.Error()
		}
		if sk.Name == "" {
			reason = fmt.Sprintf("entry %d: %s", sk.Index, reason)
		}
		out = append(out, core.SkippedImport{Name: sk.Name, Reason: reason})
	}
	return out
}

// SkippedTool is one tool Tools left out: its server-side name (empty
// when the name itself was the problem), its position in the listing,
// and why.
type SkippedTool struct {
	Name  string
	Index int
	Err   error
}

func (e *ImportError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mcp: %d tool(s) not imported:", len(e.Skipped))
	for _, s := range e.Skipped {
		if s.Name != "" {
			fmt.Fprintf(&b, " %q: %v;", s.Name, s.Err)
		} else {
			fmt.Fprintf(&b, " entry %d: %v;", s.Index, s.Err)
		}
	}
	return strings.TrimSuffix(b.String(), ";")
}

// Unwrap returns each skipped tool's cause.
func (e *ImportError) Unwrap() []error {
	errs := make([]error, len(e.Skipped))
	for i, s := range e.Skipped {
		errs[i] = s.Err
	}
	return errs
}

// Option configures one Tools call.
type Option interface{ apply(*importConfig) }

type importConfig struct {
	prefix    string
	server    string
	transport string
	policy    []core.ToolOption
}

type serverOption string

func (o serverOption) apply(c *importConfig) { c.server = string(o) }

// Server names the server the tools come from, for the observability
// record only (record contract §3, §4; O8): every imported tool's
// Origin reads "mcp:<name>" — the tools record's source, the
// execute_tool span's weft.tool.origin, the node Studio's call graph
// draws — and its source_detail names the server, the transport, the
// Prefix and the tool's name on the server. Without it the Origin is
// "mcp" and no server is named: the server's own initialize answer
// is untrusted, so weft never names a node after it. The name changes
// nothing the model sees.
func Server(name string) Option { return serverOption(name) }

type transportOption string

func (o transportOption) apply(c *importConfig) { c.transport = string(o) }

// Transport names how the session reaches the server — "stdio",
// "http", "sse", "memory", the caller's word — for the tools record's
// source_detail (O8). The SDK's session does not say which transport
// it was connected over, so the caller who connected it does. Omitted,
// the record names none.
func Transport(kind string) Option { return transportOption(kind) }

type prefixOption string

func (p prefixOption) apply(c *importConfig) { c.prefix = string(p) }

// Prefix prepends p to every imported tool's name — "gh_" turns
// "search" into "gh_search" — so two servers' tools can share one
// agent; the remote call still uses the server's name.
func Prefix(p string) Option { return prefixOption(p) }

type policyOption []core.ToolOption

func (p policyOption) apply(c *importConfig) { c.policy = append(c.policy, p...) }

// Policy applies tool options to every imported tool, after the
// annotation defaults — so Sequential from a missing readOnlyHint
// cannot be undone here, and RequireApproval is added, never removed.
// Timeout, MaxResultBytes, RequireApproval and WrapTools apply as on
// any tool; StrictInput has no effect (a RawTool receives the model's
// raw arguments — validation belongs to the server). Every imported
// tool carries core.Origin("mcp:<server>") ("mcp" without Server; the
// tools record's source and the execute_tool span's weft.tool.origin);
// a core.Origin given here overrides it.
func Policy(opts ...core.ToolOption) Option { return policyOption(opts) }

// callHandler is the imported tool's handler: one tools/call round
// trip, the model's argument bytes forwarded verbatim (a
// json.RawMessage marshals as its own bytes — no map round trip, no
// key reordering). Empty or null arguments go as {} because
// tools/call requires an object.
func callHandler(sess *sdk.ClientSession, remoteName string) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		if trimmed := strings.TrimSpace(string(args)); trimmed == "" || trimmed == "null" {
			args = json.RawMessage("{}")
		}
		res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: remoteName, Arguments: args})
		if err != nil {
			// Transport and protocol failures are data too: the tool
			// failed, the model stops calling it (deep dive ch. 5's
			// two channels, one boundary). A JSON-RPC error answer is
			// the live server refusing the call — its handler failed,
			// the tool is unknown, the arguments invalid — never an
			// unreachable server (U23.2-fix).
			var rpc *jsonrpc.Error
			if errors.As(err, &rpc) {
				return "", &rpcError{err: err}
			}
			return "", &transportError{err: err}
		}
		text := renderContent(res)
		if res.IsError {
			if text == "" {
				// An isError result with nothing in it: the model
				// still needs a sentence, and the sentinel's is the
				// honest one.
				text = ErrToolError.Error()
			}
			return "", &remoteError{text: text}
		}
		return text, nil
	}
}

// remoteError is a remote isError result as an error: the model sees
// the server's own text, middleware sees the sentinel.
type remoteError struct{ text string }

func (e *remoteError) Error() string { return e.text }
func (e *remoteError) Unwrap() error { return ErrToolError }

// ErrorType names the execute_tool span's error.type through core's
// ErrorType() convention (record contract §3): the server answered and
// said its tool failed.
func (e *remoteError) ErrorType() string { return "mcp_tool_error" }

// transportError is a tools/call that got no answer — the connection,
// the stream, a closed session — reading
// "mcp: <err>" as before, the cause unwrapped for errors.Is (a
// cancelled call still reads as the context's error, and core names it
// so). ErrorType is error.type mcp_transport: the server never
// answered with a result.
type transportError struct{ err error }

func (e *transportError) Error() string     { return "mcp: " + e.err.Error() }
func (e *transportError) Unwrap() error     { return e.err }
func (e *transportError) ErrorType() string { return "mcp_transport" }

// rpcError is a tools/call the server answered with a JSON-RPC error —
// its handler returned an error, the tool is not listed, the arguments
// were refused — reading "mcp: <err>" like a transport failure (the
// model's text is unchanged) but typed apart: error.type mcp_rpc_error,
// the server reached and refusing, not unreachable (U23.2-fix).
type rpcError struct{ err error }

func (e *rpcError) Error() string     { return "mcp: " + e.err.Error() }
func (e *rpcError) Unwrap() error     { return e.err }
func (e *rpcError) ErrorType() string { return "mcp_rpc_error" }

// renderContent is the result rendering, pinned by test (ADR 0015):
// structuredContent's JSON when the server sends it (the spec says
// text is the backwards-compatible duplicate), else the text items
// joined by newlines; non-text items become one-line markers — the
// model learns a payload existed, because weft's transcript carries
// no binary tool results and a silent drop is not an option.
func renderContent(res *sdk.CallToolResult) string {
	if res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			return string(b)
		}
	}
	var lines []string
	for _, c := range res.Content {
		switch c := c.(type) {
		case *sdk.TextContent:
			lines = append(lines, c.Text)
		case *sdk.ImageContent:
			// The SDK decodes the wire's base64 into Data, so its
			// length is the payload's own byte count.
			lines = append(lines, fmt.Sprintf("[image %s, %d bytes]", c.MIMEType, len(c.Data)))
		case *sdk.AudioContent:
			lines = append(lines, fmt.Sprintf("[audio %s, %d bytes]", c.MIMEType, len(c.Data)))
		case *sdk.EmbeddedResource:
			uri := ""
			if c.Resource != nil {
				uri = c.Resource.URI
			}
			lines = append(lines, "[resource "+uri+"]")
		case *sdk.ResourceLink:
			lines = append(lines, "[resource "+c.URI+"]")
		default:
			lines = append(lines, "[content]")
		}
	}
	return strings.Join(lines, "\n")
}
