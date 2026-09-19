package adapterkit

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
)

// SchemaMap renders through json.Marshal, so a foreign schema parsed
// with weft.ParseSchema reaches the provider as its verbatim bytes —
// enum, minimum, oneOf and all — where the hand-built map this
// replaced degraded them to the struct's own vocabulary.
func TestSchemaMapKeepsForeignSchema(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{"units":{"type":"string","enum":["c","f"]},"n":{"type":"integer","minimum":0}},"required":["units"]}`)
	s, err := weft.ParseSchema(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(SchemaMap(s))
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json sorts map keys, so the comparison is against the
	// key-sorted form of the input, not the input's own order.
	want := `{"properties":{"n":{"minimum":0,"type":"integer"},"units":{"enum":["c","f"],"type":"string"}},"required":["units"],"type":"object"}`
	if string(got) != want {
		t.Errorf("map:\n got  %s\n want %s", got, want)
	}
}

// A reflected schema renders field for field as the hand-built map
// always did — except one named change: an unconstrained node (a
// recursion cut, an interface field) used to render as {"type": ""}
// and now renders as {} (ADR 0003's 2026-09-19 amendment). This pins
// both halves.
func TestSchemaMapReflected(t *testing.T) {
	got, err := json.Marshal(SchemaMap(&weft.Schema{
		Type: "object",
		Properties: map[string]*weft.Schema{
			"n":   {Type: "integer", Description: "count"},
			"any": {},
		},
		Required: []string{"n"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{"any":{},"n":{"description":"count","type":"integer"}},"required":["n"],"type":"object"}`
	if string(got) != want {
		t.Errorf("map:\n got  %s\n want %s", got, want)
	}
}

func TestSchemaMapNil(t *testing.T) {
	if SchemaMap(nil) != nil {
		t.Errorf("nil schema must render nil")
	}
}
