package obsdb

import (
	"encoding/base64"
	"encoding/hex"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// FromOTLPTraces decodes an OTLP/HTTP trace export request into the
// model. The receiver (studio/ingest) and a stock collector's dump
// produce the same rows the local sink does; non-weft spans pass
// through unchanged — a polyglot trace is a first-class citizen.
func FromOTLPTraces(req *coltracepb.ExportTraceServiceRequest) []Span {
	if req == nil {
		return nil
	}
	var out []Span
	for _, rs := range req.ResourceSpans {
		res := attrsOf(rs.GetResource())
		service, _ := res["service.name"].(string)
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if sp == nil {
					continue
				}
				out = append(out, Span{
					TraceID:       hex.EncodeToString(sp.GetTraceId()),
					SpanID:        hex.EncodeToString(sp.GetSpanId()),
					ParentSpanID:  hex.EncodeToString(sp.GetParentSpanId()),
					Name:          sp.GetName(),
					Kind:          int(sp.GetKind()),
					Start:         unixNano(sp.GetStartTimeUnixNano()),
					End:           unixNano(sp.GetEndTimeUnixNano()),
					StatusCode:    int(sp.GetStatus().GetCode()),
					StatusMessage: sp.GetStatus().GetMessage(),
					Service:       service,
					Attrs:         keyValueMap(sp.GetAttributes()),
					Resource:      res,
				})
				for _, ev := range sp.GetEvents() {
					out[len(out)-1].Events = append(out[len(out)-1].Events, SpanEvent{
						Time:  unixNano(ev.GetTimeUnixNano()),
						Name:  ev.GetName(),
						Attrs: keyValueMap(ev.GetAttributes()),
					})
				}
			}
		}
	}
	return out
}

// FromOTLPLogs decodes an OTLP/HTTP log export request into the model.
// weft records (weft.run.id present) and non-weft records (slog lines,
// other services) both pass through; the kind they carry decides where
// Write puts them.
func FromOTLPLogs(req *collogspb.ExportLogsServiceRequest) []Record {
	if req == nil {
		return nil
	}
	var out []Record
	for _, rl := range req.ResourceLogs {
		res := attrsOf(rl.GetResource())
		service, _ := res["service.name"].(string)
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				if lr == nil {
					continue
				}
				out = append(out, Record{
					Time:      unixNano(lr.GetTimeUnixNano()),
					Observed:  unixNano(lr.GetObservedTimeUnixNano()),
					TraceID:   hex.EncodeToString(lr.GetTraceId()),
					SpanID:    hex.EncodeToString(lr.GetSpanId()),
					Severity:  int(lr.GetSeverityNumber()),
					EventName: lr.GetEventName(),
					Body:      valueString(lr.GetBody()),
					Service:   service,
					Attrs:     keyValueMap(lr.GetAttributes()),
					Resource:  res,
				})
			}
		}
	}
	return out
}

func attrsOf(r *resourcepb.Resource) map[string]any {
	if r == nil {
		return nil
	}
	return keyValueMap(r.GetAttributes())
}

// unixNano renders a protobuf fixed64 timestamp; zero stays zero.
func unixNano(n uint64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(n)).UTC()
}

// keyValueMap converts OTLP attributes to the model's map. Values map
// as: string, bool, int64, double; arrays → []any (recursively);
// kvlists → map[string]any; bytes → base64 string. Nil attributes read
// as a nil map, so Derive* sees "absent", not "empty".
func keyValueMap(kvs []*commonpb.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		if v := anyValue(kv.GetValue()); v != nil {
			m[kv.GetKey()] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func anyValue(v *commonpb.AnyValue) any {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		arr := x.ArrayValue.GetValues()
		out := make([]any, 0, len(arr))
		for _, e := range arr {
			if v := anyValue(e); v != nil {
				out = append(out, v)
			}
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		kvs := x.KvlistValue.GetValues()
		out := make(map[string]any, len(kvs))
		for _, kv := range kvs {
			if kv == nil {
				continue
			}
			if v := anyValue(kv.GetValue()); v != nil {
				out[kv.GetKey()] = v
			}
		}
		return out
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	}
	return nil
}

// valueString renders a record body: weft bodies are JSON strings; any
// other value type (a stock bridge may emit structured bodies) renders
// through its AnyValue mapping.
func valueString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	if s, ok := anyValue(v).(string); ok {
		return s
	}
	return ""
}

// Compile-time pins on the OTLP enums the model's int fields carry, so
// a proto bump that renumbers them fails here, not in a stored row.
var (
	_ = tracepb.Span_SPAN_KIND_INTERNAL
	_ = logspb.SeverityNumber_SEVERITY_NUMBER_INFO
)
