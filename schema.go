package weft

import (
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
//	                         values (map[string]int → object of integers)
//	struct, time.Time       → object (time.Time → string with format date-time)
//	pointer                 → the pointed-to schema; the field becomes optional
//
// Struct fields use their json tag for the property name (skipping "-");
// the jsonschema tag provides the description. A field is required unless
// it is a pointer or tagged omitempty. Embedded structs contribute their
// fields directly to the enclosing object.
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

func schemaForStruct(t reflect.Type, visiting map[reflect.Type]bool) *Schema {
	s := &Schema{Type: "object", Properties: map[string]*Schema{}}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, rest, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		// Embedded structs contribute their fields to the enclosing object,
		// matching encoding/json — including embedded structs whose type
		// name is unexported.
		if f.Anonymous && name == "" {
			ft := derefType(f.Type)
			if ft.Kind() == reflect.Struct {
				if embedded := schemaOf(ft, visiting); len(embedded.Properties) > 0 {
					for k, v := range embedded.Properties {
						s.Properties[k] = v
					}
					s.Required = append(s.Required, embedded.Required...)
				}
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name // no json name: encoding/json uses the field name
		}
		optional := f.Type.Kind() == reflect.Pointer || strings.Contains(rest, "omitempty")
		prop := schemaOf(f.Type, visiting)
		if desc := f.Tag.Get("jsonschema"); desc != "" {
			prop.Description = desc
		}
		s.Properties[name] = prop
		if !optional {
			s.Required = append(s.Required, name)
		}
	}
	if len(s.Properties) == 0 {
		s.Properties = nil
	}
	return s
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
