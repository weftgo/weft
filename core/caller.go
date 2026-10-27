package core

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

// remoteCallerKey carries WithCaller's link: the run and tool call of
// another process that this one serves.
type remoteCallerKey struct{}

type remoteCaller struct{ runID, callID string }

// WithCaller returns a copy of ctx naming the run and tool call that
// called this process from outside it — an MCP client's tool call
// (weft/mcp's Serve reads it from the request's _meta), a queue
// message, an RPC: the run started next on ctx records
// weft.caller.run.id and weft.caller.call.id (callID omitted when "")
// on its invoke_agent span and run_start record — a link to its
// caller, never parenthood: no weft.parent.*, and its usage stays its
// own (record contract §2, O11).
//
// The link is that run's alone: a run started inside it (from its
// model chain, or a tool handler's Generate, which carries the
// handler's own call instead) does not inherit it, and a run started
// on a tool call's context keeps that call's link — the in-process
// caller wins. An empty runID returns ctx unchanged. Reporting only:
// the link changes nothing the model sees.
func WithCaller(ctx context.Context, runID, callID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, remoteCallerKey{}, remoteCaller{runID: runID, callID: callID})
}

// remoteCallerAttrs is WithCaller's link as a run's attributes, nil
// when ctx carries none.
func remoteCallerAttrs(ctx context.Context) []attribute.KeyValue {
	rc, ok := ctx.Value(remoteCallerKey{}).(remoteCaller)
	if !ok {
		return nil
	}
	attrs := []attribute.KeyValue{attrCallerRunID.String(rc.runID)}
	if rc.callID != "" {
		attrs = append(attrs, attrCallerCallID.String(rc.callID))
	}
	return attrs
}
