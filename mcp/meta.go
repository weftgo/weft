package mcp

import (
	"context"
	"encoding/hex"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
)

// The caller link over the wire (U33.1, UX-S2-08's remainder): a
// tools/call carries its caller in params._meta, both directions over
// the SDK's Meta map —
//
//   - "traceparent" (and "tracestate"), W3C trace context: MCP's trace
//     propagation convention, the same header bytes HTTP carries. An
//     imported tool sends its execute_tool span's; a served call starts
//     under it as a remote parent, so the served run's spans join the
//     caller's trace, as a tool call's work does in one process.
//   - MetaCaller, {"run_id", "call_id"}: the weft run and tool call the
//     request came from (core.CallFromContext). A served call's run
//     records them as weft.caller.run.id / weft.caller.call.id
//     (core.WithCaller) — a link to the caller, never parenthood: no
//     weft.parent.*, its usage its own (record contract §2, O11).
//
// Reporting only: a malformed or missing entry is ignored, never a
// refused call, and nothing the model sees changes. The client's ids
// are taken as the client states them — a link, not an identity.

// MetaCaller is the params._meta key a tools/call names its weft
// caller under: {"run_id": "<run>", "call_id": "<tool call>"}.
const MetaCaller = "dev.weftgo/caller"

// metaIDMax bounds a caller id read from _meta, in bytes (core's
// metadata value bound): a longer one is ignored.
const metaIDMax = 1024

// callerMeta is the _meta an imported tool's tools/call carries: the
// trace context of the span ctx carries and the weft call it serves;
// nil when ctx has neither.
func callerMeta(ctx context.Context) sdk.Meta {
	var m sdk.Meta
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		m = sdk.Meta{"traceparent": fmt.Sprintf("00-%s-%s-%s", sc.TraceID(), sc.SpanID(), sc.TraceFlags())}
		if ts := sc.TraceState().String(); ts != "" {
			m["tracestate"] = ts
		}
	}
	if c, ok := core.CallFromContext(ctx); ok && c.RunID != "" {
		if m == nil {
			m = sdk.Meta{}
		}
		m[MetaCaller] = map[string]string{"run_id": c.RunID, "call_id": c.CallID}
	}
	return m
}

// servedContext is ctx with a served call's caller from its _meta: the
// caller's span as the remote parent (traceparent) and its weft run and
// call as the next run's caller link (core.WithCaller). Anything
// missing or malformed is skipped.
func servedContext(ctx context.Context, req *sdk.CallToolRequest) context.Context {
	if req == nil || req.Params == nil || len(req.Params.Meta) == 0 {
		return ctx
	}
	meta := req.Params.Meta
	if tp, ok := meta["traceparent"].(string); ok {
		ts, _ := meta["tracestate"].(string)
		if sc, ok := parseTraceparent(tp, ts); ok {
			ctx = trace.ContextWithRemoteSpanContext(ctx, sc)
		}
	}
	if c, ok := meta[MetaCaller].(map[string]any); ok {
		run, _ := c["run_id"].(string)
		call, _ := c["call_id"].(string)
		if run != "" && len(run) <= metaIDMax && len(call) <= metaIDMax {
			ctx = core.WithCaller(ctx, run, call)
		}
	}
	return ctx
}

// parseTraceparent reads a W3C traceparent ("00-<32 hex>-<16 hex>-<2
// hex>"; a later version's first four fields) and its tracestate; ok
// is false for anything else or an all-zero id.
func parseTraceparent(tp, ts string) (trace.SpanContext, bool) {
	if len(tp) < 55 || tp[2] != '-' || tp[35] != '-' || tp[52] != '-' || tp[:2] == "ff" ||
		(tp[:2] == "00" && len(tp) != 55) || (len(tp) > 55 && tp[55] != '-') {
		return trace.SpanContext{}, false
	}
	if !isLowerHex(tp[:2]) || !isLowerHex(tp[53:55]) {
		return trace.SpanContext{}, false
	}
	tid, err := trace.TraceIDFromHex(tp[3:35])
	if err != nil {
		return trace.SpanContext{}, false
	}
	sid, err := trace.SpanIDFromHex(tp[36:52])
	if err != nil {
		return trace.SpanContext{}, false
	}
	flags, err := hex.DecodeString(tp[53:55])
	if err != nil {
		return trace.SpanContext{}, false
	}
	cfg := trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.TraceFlags(flags[0]) & trace.FlagsSampled, Remote: true}
	if ts != "" {
		if state, err := trace.ParseTraceState(ts); err == nil {
			cfg.TraceState = state
		}
	}
	sc := trace.NewSpanContext(cfg)
	return sc, sc.IsValid()
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
