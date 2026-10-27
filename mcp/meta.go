package mcp

import (
	"context"
	"encoding/hex"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
)

// The caller link over the wire (U33.1, UX-S2-08's remainder; U33.1-fix):
// a tools/call can carry its caller in params._meta, both directions
// over the SDK's Meta map —
//
//   - "traceparent" (and "tracestate"), W3C trace context: MCP's trace
//     propagation convention, the same header bytes HTTP carries. A
//     served call's run links its invoke_agent span to that span (an
//     OpenTelemetry link) and starts its own trace — never the caller's
//     child, so the client's sampled flag never decides whether this
//     process records the run.
//   - MetaCaller, {"run_id", "call_id"}: the weft run and tool call the
//     request came from. A served call's run records them as
//     weft.caller.run.id / weft.caller.call.id with weft.caller.remote
//     (core.WithCaller) — a link, never parenthood, and readers keep the
//     run top-level, so a stated id cannot move or hide runs (record
//     contract §2, O11).
//
// The client sends both only under the CallerMeta option (off by
// default: the ids name your runs to the server). Reporting only: a
// malformed or missing entry is ignored, never a refused call, and
// nothing the model sees changes.

// MetaCaller is the params._meta key a tools/call names its weft
// caller under: {"run_id": "<run>", "call_id": "<tool call>"}.
const MetaCaller = "dev.weftgo/caller"

// metaIDMax bounds a caller id read from _meta, in bytes (core's
// metadata value bound): a longer one is ignored.
const metaIDMax = 1024

// callerMeta is the _meta an imported tool's tools/call carries under
// CallerMeta: the trace context of the span ctx carries and the weft
// call it serves; nil when ctx has neither.
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

// servedContext is ctx with a served call's caller from its _meta
// (core.WithCaller): its weft run and call, and its span to link to.
// Anything missing or malformed is skipped; the span is never made the
// served run's parent.
func servedContext(ctx context.Context, req *sdk.CallToolRequest) context.Context {
	if req == nil || req.Params == nil || len(req.Params.Meta) == 0 {
		return ctx
	}
	meta := req.Params.Meta
	var c core.Caller
	if tp, ok := meta["traceparent"].(string); ok {
		ts, _ := meta["tracestate"].(string)
		if sc, ok := parseTraceparent(tp, ts); ok {
			c.Span = sc
		}
	}
	if m, ok := meta[MetaCaller].(map[string]any); ok {
		run, _ := m["run_id"].(string)
		call, _ := m["call_id"].(string)
		if run != "" && len(run) <= metaIDMax && len(call) <= metaIDMax {
			c.RunID, c.CallID = run, call
		}
	}
	return core.WithCaller(ctx, c)
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
