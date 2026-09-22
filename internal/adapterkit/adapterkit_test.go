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

func TestMergeBody(t *testing.T) {
	dest, err := MergeBody([]byte(`{"a":1,"nested":{"x":1,"y":2},"arr":[1,2]}`), map[string]any{
		"a":      2,                              // colliding scalar: caller wins
		"nested": map[string]any{"y": 9, "z": 3}, // nested map: deep merge
		"arr":    []any{3},                       // arrays replace, never merge
		"new":    true,                           // new key: added
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":2,"arr":[3],"nested":{"x":1,"y":9,"z":3},"new":true}`
	if string(dest) != want {
		t.Errorf("merged = %s, want %s", dest, want)
	}

	// A non-object body fails rather than being replaced.
	if _, err := MergeBody([]byte(`[1,2]`), map[string]any{"a": 1}); err == nil {
		t.Error("array body merged without error")
	}
	if _, err := MergeBody([]byte(`not json`), map[string]any{"a": 1}); err == nil {
		t.Error("garbage body merged without error")
	}
}
