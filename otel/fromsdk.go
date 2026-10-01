package otel

import (
	"encoding/hex"

	"github.com/weftgo/weft/obsdb"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// otlpStatusCode maps the SDK's codes (Unset=0, Error=1, Ok=2) onto
// OTLP's (unset=0, ok=1, error=2) — the model is OTLP-shaped, and the
// two numberings differ. The golden test pins the mapping.
func otlpStatusCode(c otelcodes.Code) int {
	switch c {
	case otelcodes.Error:
		return 2
	case otelcodes.Ok:
		return 1
	default:
		return 0
	}
}

// FromSDKSpans converts SDK spans (what a SpanExporter receives) into
// the obsdb model. It lives in this module because it needs the SDK; a
// package under obsdb would put the SDK in obsdb's go.mod (S3.3). For
// the same data it produces exactly the values obsdb.FromOTLPTraces
// does — the golden test pins that against obsdb's OTLP fixtures.
func FromSDKSpans(spans []sdktrace.ReadOnlySpan) []obsdb.Span {
	out := make([]obsdb.Span, 0, len(spans))
	for _, s := range spans {
		res := attrMap(s.Resource().Attributes())
		service, _ := res["service.name"].(string)
		parent := ""
		if pid := s.Parent().SpanID(); pid.IsValid() {
			parent = pid.String()
		}
		sp := obsdb.Span{
			TraceID:       s.SpanContext().TraceID().String(),
			SpanID:        s.SpanContext().SpanID().String(),
			ParentSpanID:  parent,
			Name:          s.Name(),
			Kind:          int(s.SpanKind()),
			Start:         s.StartTime().UTC(),
			End:           s.EndTime().UTC(),
			StatusCode:    otlpStatusCode(s.Status().Code),
			StatusMessage: s.Status().Description,
			Service:       service,
			Attrs:         attrMap(s.Attributes()),
			Resource:      res,
		}
		for _, ev := range s.Events() {
			sp.Events = append(sp.Events, obsdb.SpanEvent{
				Time: ev.Time.UTC(), Name: ev.Name, Attrs: attrMap(ev.Attributes),
			})
		}
		out = append(out, sp)
	}
	return out
}

// FromSDKRecords converts SDK log records (what a log Exporter
// receives) into the obsdb model, matching obsdb.FromOTLPLogs value for
// value on the same data.
func FromSDKRecords(recs []sdklog.Record) []obsdb.Record {
	out := make([]obsdb.Record, 0, len(recs))
	for i := range recs {
		r := &recs[i]
		res := attrMap(r.Resource().Attributes())
		service, _ := res["service.name"].(string)
		var attrs map[string]any
		r.WalkAttributes(func(kv attrKV) bool {
			if attrs == nil {
				attrs = map[string]any{}
			}
			if v := attrValue(kv.Value); v != nil {
				attrs[string(kv.Key)] = v
			}
			return true
		})
		var traceID, spanID string
		if tid := r.TraceID(); tid.IsValid() {
			traceID = hex.EncodeToString(tid[:])
		}
		if sid := r.SpanID(); sid.IsValid() {
			spanID = hex.EncodeToString(sid[:])
		}
		out = append(out, obsdb.Record{
			Time:      r.Timestamp().UTC(),
			Observed:  r.ObservedTimestamp().UTC(),
			TraceID:   traceID,
			SpanID:    spanID,
			Severity:  int(r.Severity()),
			EventName: r.EventName(),
			Body:      bodyString(r.Body()),
			Service:   service,
			Attrs:     attrs,
			Resource:  res,
		})
	}
	return out
}
