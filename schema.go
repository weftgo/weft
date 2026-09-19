package weft

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Schema is the subset of JSON Schema (draft 2020-12) that weft derives
// from tool input structs. Its shape tracks what the official Go MCP SDK
// derives via google/jsonschema-go, so tools cross over to MCP without
// conversion.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Format      string             `json:"format,omitempty"`
	Description string             `json:"description,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	// AdditionalProperties types a map's values (JSON Schema draft
	// 2020-12), so map[string]int stops being "some object". The
	// boolean false form is not expressible: reflection always has a
	// value type, and hand-written RawTool schemas that need to lock
	// properties down must say so in Description.
	AdditionalProperties *Schema  `json:"additionalProperties,omitempty"`
	Required             []string `json:"required,omitempty"`
	Items                *Schema  `json:"items,omitempty"`
}

// schemaFor derives the input schema for a Go type.
//
// Mapping rules:
//
//	string, []byte          → string
//	bool                    → boolean
//	integers                → integer
//	floats                  → number
//	slice, array            → array with Items
//	map                     → object with AdditionalProperties typing the
//	                         values (map[string]int → object of integers;
//	                         map[string]any → bare object, free-form)
//	struct, time.Time       → object (time.Time → string with format date-time)
//	pointer                 → the pointed-to schema; the field becomes optional
//
// Struct fields use their json tag for the property name (skipping "-");
// the jsonschema tag provides the description. A field is required unless
// it is a pointer or tagged omitempty. Embedded structs contribute their
// fields directly to the enclosing object, with exactly encoding/json's
// dominance rules: a claim at a shallower embedding depth wins over a
// deeper one; at equal depth, exactly one json-tagged claim wins over
// untagged ones and any other tie cancels the name (encoding/json drops
// it from the wire, so the schema must not advertise it). One exception
// is fail-loud, not mirroring: two fields of the same struct claiming
// one JSON name (always both tagged — untagged Go names cannot collide)
// panic at construction, at any *named* nesting depth — each named
// struct derives its own depth-0 space, so its unreachable handler
// field is a bug, not a pattern. The same collision inside an embedded
// struct does not panic: its claims flatten into the parent's dominance
// rules and the name is silently dropped, exactly the drop encoding/json
// makes — schema and wire stay in agreement either way. The json
// ",string" option is
// reflected for the scalar kinds that support it: the property is typed
// string, the quoted wire form.
//
// Known gaps, deliberate for now: union types (oneOf/anyOf) do not exist in
// Go's type system, and interface fields degrade to an unconstrained value.
// A time.Duration field is an integer counting nanoseconds — consistent
// with encoding/json round trips, but a foot-gun for models; prefer a
// string with a provider-appropriate format for model-facing durations.
// A recursive struct (a tree) is cut at the point of recursion: the nested
// occurrence becomes an unconstrained value instead of an infinite schema.
// An optional go:generate step may recover stricter schemas later.
func schemaFor(t reflect.Type) *Schema {
	return schemaOf(t, map[reflect.Type]bool{})
}

// schemaOf derives a schema; visiting holds the struct types on the
// current derivation path, so cycles terminate.
func schemaOf(t reflect.Type, visiting map[reflect.Type]bool) *Schema {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return &Schema{}
	}
	switch t {
	case reflect.TypeOf(time.Time{}):
		return &Schema{Type: "string", Format: "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string"} // []byte marshals as a base64 string
		}
		return &Schema{Type: "array", Items: schemaOf(t.Elem(), visiting)}
	case reflect.Array:
		return &Schema{Type: "array", Items: schemaOf(t.Elem(), visiting)}
	case reflect.Map:
		// An any value type has nothing to say — every value is
		// allowed, which a bare object already means — so
		// map[string]any stays wire-identical to a free-form object
		// instead of carrying an empty additionalProperties:{}.
		if t.Elem().Kind() == reflect.Interface {
			return &Schema{Type: "object"}
		}
		return &Schema{Type: "object", AdditionalProperties: schemaOf(t.Elem(), visiting)}
	case reflect.Struct:
		if visiting[t] {
			return &Schema{} // recursion: unconstrained at this depth
		}
		visiting[t] = true
		defer delete(visiting, t)
		return schemaForStruct(t, visiting)
	default:
		// Interface, func, chan, ...: unconstrained; JSON decides.
		return &Schema{}
	}
}

// fieldClaim is one struct field's claim on a JSON name: the derived
// property schema, whether the field is required, the field's embedding
// depth (0 for a struct's own fields), and whether a json tag named the
// field explicitly.
type fieldClaim struct {
	schema   *Schema
	required bool
	depth    int
	tagged   bool
}

// claimIndex collects claims per JSON name, remembering the order in
// which names are first claimed — declaration order, with an embedded
// struct's contributions at the embedded field's position — so the
// marshalled schema (its Required list) is byte-stable.
type claimIndex struct {
	order  []string
	claims map[string][]fieldClaim
}

func newClaimIndex() *claimIndex {
	return &claimIndex{claims: map[string][]fieldClaim{}}
}

func (x *claimIndex) add(name string, c fieldClaim) {
	if _, seen := x.claims[name]; !seen {
		x.order = append(x.order, name)
	}
	x.claims[name] = append(x.claims[name], c)
}

// collectClaims walks t's fields, flattening embedded structs one depth
// down — including embedded structs whose type name is unexported — and
// records each field's claim. A struct type already on the derivation
// path is skipped, the recursion cut schemaOf performs.
func collectClaims(t reflect.Type, visiting map[reflect.Type]bool, depth int, x *claimIndex) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, rest, _ := strings.Cut(f.Tag.Get("json"), ",")
		tagged := name != "" // an explicit json name tag, the dominance tiebreak's currency
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			ft := derefType(f.Type)
			if ft.Kind() != reflect.Struct || visiting[ft] {
				continue
			}
			visiting[ft] = true
			collectClaims(ft, visiting, depth+1, x)
			delete(visiting, ft)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name // no json name: encoding/json uses the field name
		}
		optional := f.Type.Kind() == reflect.Pointer || hasTagOption(rest, "omitempty")
		p := schemaOf(f.Type, visiting)
		// The ",string" option carries the value inside a JSON string —
		// encoding/json demands quotes — so for the kinds that support
		// it the wire type is string; advertising the bare type would
		// invite exactly the unquoted value decoding rejects.
		if hasTagOption(rest, "string") {
			switch derefType(f.Type).Kind() {
			case reflect.String, reflect.Bool,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64:
				p = &Schema{Type: "string"}
			}
		}
		if desc := f.Tag.Get("jsonschema"); desc != "" {
			p.Description = desc
		}
		x.add(name, fieldClaim{schema: p, required: !optional, depth: depth, tagged: tagged})
	}
}

func schemaForStruct(t reflect.Type, visiting map[reflect.Type]bool) *Schema {
	x := newClaimIndex()
	collectClaims(t, visiting, 0, x)
	s := &Schema{Type: "object", Properties: map[string]*Schema{}}
	for _, name := range x.order {
		claim, state := resolveClaim(name, x.claims[name])
		switch state {
		case claimDropped:
			continue // cancelled: encoding/json drops the name, the schema must not advertise it
		case claimBug:
			// Two of the top-level struct's own fields claim one name —
			// one handler field can never receive a value. The
			// duplicate-name and non-struct-input panics set the
			// precedent: fail loud, fail early (ADR 0003).
			panic(fmt.Sprintf("weft: struct %s declares two fields with the JSON name %q; one of them can never receive a value", t, name))
		}
		s.Properties[name] = claim.schema
		if claim.required {
			s.Required = append(s.Required, name)
		}
	}
	if len(s.Properties) == 0 {
		s.Properties = nil
	}
	return s
}

type claimState int

const (
	claimWon claimState = iota
	claimDropped
	claimBug
)

// resolveClaim applies encoding/json's field dominance rules to one
// name's claims: the shallowest embedding depth wins; at equal depth
// exactly one json-tagged claim wins over untagged ones, and any other
// tie cancels the name — encoding/json drops it from the wire. A
// cancelling tie at depth 0 is the struct's own two fields (untagged
// Go field names cannot collide), reported as claimBug rather than
// mirrored.
func resolveClaim(name string, claims []fieldClaim) (fieldClaim, claimState) {
	min := claims[0].depth
	for _, c := range claims[1:] {
		if c.depth < min {
			min = c.depth
		}
	}
	var atMin []fieldClaim
	for _, c := range claims {
		if c.depth == min {
			atMin = append(atMin, c)
		}
	}
	if len(atMin) == 1 {
		return atMin[0], claimWon
	}
	var tagged []fieldClaim
	for _, c := range atMin {
		if c.tagged {
			tagged = append(tagged, c)
		}
	}
	if len(tagged) == 1 {
		return tagged[0], claimWon
	}
	if min == 0 {
		return fieldClaim{}, claimBug
	}
	return fieldClaim{}, claimDropped
}

// hasTagOption reports whether opt is among a tag's comma-separated
// options.
func hasTagOption(rest, opt string) bool {
	for _, o := range strings.Split(rest, ",") {
		if o == opt {
			return true
		}
	}
	return false
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
