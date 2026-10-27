package core

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// attrCallerRemote marks a caller link that came from another process
// (WithCaller): readers never treat it as a local link — the run stays
// top-level and its session's own turn (record contract §2).
var attrCallerRemote = attribute.Key("weft.caller.remote")

// Caller is a run's caller in another process — an MCP client's tool
// call (weft/mcp's Serve reads it from the request's _meta), a queue
// message, an RPC. RunID and CallID are that process's weft run and
// tool call, as it states them; Span is its span (a W3C traceparent),
// which the run's invoke_agent span links to. Either half may be
// absent.
type Caller struct {
	RunID, CallID string
	Span          trace.SpanContext
}

// remoteCallerKey carries WithCaller's link.
type remoteCallerKey struct{}

// WithCaller returns a copy of ctx naming a caller from another
// process. Every run started directly on ctx — not inside a tool call:
// a call on the context wins, and no run started inside such a run (its
// model chain, its tools' handlers) inherits the link — records:
//
//   - weft.caller.run.id / weft.caller.call.id (callID omitted when "")
//     and weft.caller.remote = "true" on its invoke_agent span and
//     run_start record, when c.RunID is set — a link, never parenthood
//     (no weft.parent.*, its usage its own). Readers ignore a remote
//     link for the run's place: the run stays top-level and its
//     session's own turn, so a caller id a client states cannot move or
//     hide runs (record contract §2, O11);
//   - an OpenTelemetry link from its invoke_agent span to c.Span, when
//     c.Span is valid: the run starts its own trace (or under the span
//     ctx already carries — an HTTP handler's), never as the caller's
//     child, so the caller's sampling decision never decides whether
//     this process records the run.
//
// A Caller with neither half returns ctx unchanged. Reporting only:
// nothing the model sees changes.
func WithCaller(ctx context.Context, c Caller) context.Context {
	if c.RunID == "" && !c.Span.IsValid() {
		return ctx
	}
	if c.RunID == "" {
		c.CallID = ""
	}
	return context.WithValue(ctx, remoteCallerKey{}, c)
}

// remoteCallerOf is WithCaller's caller on ctx, if any.
func remoteCallerOf(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(remoteCallerKey{}).(Caller)
	return c, ok
}

// remoteCallerAttrs is WithCaller's link as a run's attributes, nil
// when ctx carries none (or only a span).
func remoteCallerAttrs(ctx context.Context) []attribute.KeyValue {
	rc, ok := remoteCallerOf(ctx)
	if !ok || rc.RunID == "" {
		return nil
	}
	attrs := []attribute.KeyValue{attrCallerRunID.String(rc.RunID)}
	if rc.CallID != "" {
		attrs = append(attrs, attrCallerCallID.String(rc.CallID))
	}
	return append(attrs, attrCallerRemote.String("true"))
}

// remoteCallerSpan is the span a run started on ctx links to: WithCaller's
// span when the run takes that caller (runLinkAttrs' rule: no tool call
// of its own on ctx); invalid otherwise.
func remoteCallerSpan(ctx context.Context) trace.SpanContext {
	if c, ok := CallFromContext(ctx); ok && !ownCallOf(ctx, c) {
		return trace.SpanContext{}
	}
	rc, _ := remoteCallerOf(ctx)
	return rc.Span
}
