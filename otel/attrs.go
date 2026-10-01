package otel

import (
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

// bodyString renders a record body: weft bodies are JSON strings.
func bodyString(v attribute.Value) string {
	if v.Type() == attribute.STRING {
		return v.AsString()
	}
	return ""
}
