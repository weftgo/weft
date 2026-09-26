package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
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
// transport failure is a tool error reading "mcp: <err>". Both are
// data the model sees, never run errors.
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
// re-run Tools into a slice you serve through weft.ToolSource, under
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
// empty name, a nil entry — fails that tool, not the listing: the
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
func Tools(ctx context.Context, sess *sdk.ClientSession, opts ...Option) ([]*weft.ToolDef, error) {
	cfg := importConfig{}
	for _, o := range opts {
		if o != nil {
			o.apply(&cfg)
		}
	}
	out := []*weft.ToolDef{} // non-nil: zero tools is a fact, not an error
	var skipped []SkippedTool
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
		name := cfg.prefix + t.Name
		if name == "" {
			// Server-provided content is untrusted: a hostile or buggy
			// server listing {"name": ""} would panic RawTool at import
			// time. The nil-entry check above is the same rule.
			skipped = append(skipped, SkippedTool{Index: index, Err: errors.New("empty name")})
			continue
		}
		raw, err := fromSDK(t.InputSchema)
		if err != nil {
			skipped = append(skipped, SkippedTool{Name: t.Name, Index: index, Err: err})
			continue
		}
		schema, err := weft.ParseSchema(raw)
		if err != nil {
			skipped = append(skipped, SkippedTool{Name: t.Name, Index: index, Err: err})
			continue
		}
		// Annotation default first, the caller's policy after: a
		// missing readOnlyHint cannot be un-Sequentialised, and
		// RequireApproval is added, never removed.
		toolOpts := []weft.ToolOption{}
		if t.Annotations == nil || !t.Annotations.ReadOnlyHint {
			toolOpts = append(toolOpts, weft.Sequential())
		}
		toolOpts = append(toolOpts, cfg.policy...)
		tool := weft.RawTool(name, t.Description, schema,
			callHandler(sess, t.Name), toolOpts...)
		if t.OutputSchema != nil {
			if raw, err := fromSDK(t.OutputSchema); err == nil {
				if outSchema, err := weft.ParseSchema(raw); err == nil {
					tool.OutputSchema = outSchema
				}
			}
		}
		out = append(out, tool)
	}
	if len(skipped) > 0 {
		return out, &ImportError{Skipped: skipped}
	}
	return out, nil
}

// ImportError is Tools' report of the tools it could not import. It
// travels beside the importable tools, not instead of them — the
// partial-result shape *weft.RunError uses (ADR 0002): the caller
// reads it with errors.As and decides whether a skipped tool is a
// warning or a stop. Unwrap exposes each tool's cause, so errors.Is
// against a ParseSchema failure still works through the report.
type ImportError struct {
	Skipped []SkippedTool
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
	prefix string
	policy []weft.ToolOption
}

type prefixOption string

func (p prefixOption) apply(c *importConfig) { c.prefix = string(p) }

// Prefix prepends p to every imported tool's name — "gh_" turns
// "search" into "gh_search" — so two servers' tools can share one
// agent; the remote call still uses the server's name.
func Prefix(p string) Option { return prefixOption(p) }

type policyOption []weft.ToolOption

func (p policyOption) apply(c *importConfig) { c.policy = append(c.policy, p...) }

// Policy applies tool options to every imported tool, after the
// annotation defaults — so Sequential from a missing readOnlyHint
// cannot be undone here, and RequireApproval is added, never removed.
// Timeout, MaxResultBytes, RequireApproval and WrapTools apply as on
// any tool; StrictInput has no effect (a RawTool receives the model's
// raw arguments — validation belongs to the server).
func Policy(opts ...weft.ToolOption) Option { return policyOption(opts) }

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
			// two channels, one boundary).
			return "", fmt.Errorf("mcp: %w", err)
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
