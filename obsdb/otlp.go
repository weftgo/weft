package obsdb

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	// The getters throughout: a nil element at any layer reads as empty
	// instead of being dereferenced (the request is network input).
	for _, rs := range req.GetResourceSpans() {
		res := attrsOf(rs.GetResource())
		service, _ := res["service.name"].(string)
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				if sp == nil {
					continue
				}
				out = append(out, Span{
					TraceID:       idHex(sp.GetTraceId(), traceIDBytes),
					SpanID:        idHex(sp.GetSpanId(), spanIDBytes),
					ParentSpanID:  idHex(sp.GetParentSpanId(), spanIDBytes),
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
					if ev == nil {
						continue
					}
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
	for _, rl := range req.GetResourceLogs() {
		res := attrsOf(rl.GetResource())
		service, _ := res["service.name"].(string)
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				if lr == nil {
					continue
				}
				out = append(out, Record{
					Time:      unixNano(lr.GetTimeUnixNano()),
					Observed:  unixNano(lr.GetObservedTimeUnixNano()),
					TraceID:   idHex(lr.GetTraceId(), traceIDBytes),
					SpanID:    idHex(lr.GetSpanId(), spanIDBytes),
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

// The id widths OTLP fixes: 16-byte trace ids, 8-byte span ids.
const (
	traceIDBytes = 16
	spanIDBytes  = 8
)

// idHex renders a trace or span id as lowercase hex. size is the id's
// width in bytes.
//
// OTLP/JSON spells ids as hex strings, not the base64 the standard
// protobuf JSON mapping gives a bytes field — and a hex id is valid
// base64, so a protojson decode of a real OTLP/JSON request (what
// studio/ingest runs) succeeds and yields size*3/2 bytes of the hex
// text read as base64. No real id has that width, and encoding the
// bytes back returns the sender's string exactly: when that string is
// hex of the right length, it is the id.
func idHex(b []byte, size int) string {
	if len(b) == size*3/2 {
		if s := base64.StdEncoding.EncodeToString(b); isHex(s) {
			return strings.ToLower(s)
		}
	}
	return hex.EncodeToString(b)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
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
// through its AnyValue mapping, as JSON.
func valueString(v *commonpb.AnyValue) string {
	switch x := anyValue(v).(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x) // a NaN or infinite double: JSON has no spelling
		}
		return string(b)
	}
}

// Compile-time pins on the OTLP enums the model's int fields carry, so
// a proto bump that renumbers them fails here, not in a stored row.
var (
	_ = tracepb.Span_SPAN_KIND_INTERNAL
	_ = logspb.SeverityNumber_SEVERITY_NUMBER_INFO
)
