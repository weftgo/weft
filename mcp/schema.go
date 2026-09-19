package mcp

import (
	"encoding/json"

	"github.com/weftgo/weft"
)

// toSDK renders a weft schema for the SDK's Tool.InputSchema /
// OutputSchema fields (typed `any` on the pinned v1.8.0: any value that
// JSON-marshals to valid schema). Nil becomes the empty object schema —
// the SDK panics on a nil input schema, and a schema-less tool takes
// every object, which the empty schema says exactly (RawTool's own
// rule). Marshal honours Schema.MarshalJSON, so a schema parsed with
// weft.ParseSchema crosses as its verbatim bytes.
func toSDK(s *weft.Schema) any {
	if s == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	b, err := json.Marshal(s)
	if err != nil {
		// Unreachable: every Schema field is a string, slice, or map,
		// and ParseSchema validated the raw bytes before storing them.
		return json.RawMessage(`{"type":"object"}`)
	}
	return json.RawMessage(b)
}

// fromSDK turns the SDK's schema value — whatever Go type the pinned
// version's client fills in, a map on v1.8.0 — into the bytes
// weft.ParseSchema reads.
func fromSDK(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
