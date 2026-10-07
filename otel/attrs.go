package otel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
)

// The attribute-value mapping shared by FromSDKSpans and FromSDKRecords
// (S3.3): string, bool, int64, double; arrays → []any; kvlists →
// map[string]any; bytes → base64 string — the same shapes
// obsdb.FromOTLP* produces, so both paths make identical rows.

type attrKV = attribute.KeyValue

func attrMap(kvs []attribute.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if v := attrValue(kv.Value); v != nil {
			m[string(kv.Key)] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func attrValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.STRING:
		return v.AsString()
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRINGSLICE:
		return anySlice(v.AsStringSlice())
	case attribute.BOOLSLICE:
		return anySlice(v.AsBoolSlice())
	case attribute.INT64SLICE:
		return anySlice(v.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		return anySlice(v.AsFloat64Slice())
	case attribute.BYTESLICE:
		return base64.StdEncoding.EncodeToString(v.AsByteSlice())
	case attribute.SLICE:
		vals := v.AsSlice()
		out := make([]any, 0, len(vals))
		for _, e := range vals {
			if x := attrValue(e); x != nil {
				out = append(out, x)
			}
		}
		return out
	case attribute.MAP:
		kvs := v.AsMap()
		out := make(map[string]any, len(kvs))
		for _, kv := range kvs {
			if x := attrValue(kv.Value); x != nil {
				out[string(kv.Key)] = x
			}
		}
		return out
	}
	return nil
}

func anySlice[S any](s []S) []any {
	if s == nil {
		return []any{}
	}
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}
	return out
}

// bodyString renders a record body: weft bodies are JSON strings; any
// other value type (a stock bridge may emit structured bodies) renders
// through the attribute mapping above, as JSON — exactly what
// obsdb.FromOTLPLogs makes of the same body, so both paths store
// identical rows (S3.3).
func bodyString(v attribute.Value) string {
	switch x := attrValue(v).(type) {
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
