package weft

// Embedded-name conflicts, the half of the shadowing rules that cannot
// be written as a static test type: declaring a struct that embeds two
// same-named fields trips go vet's structtag check, which reports the
// duplication these tests exist to exercise. The types are assembled
// with reflect.StructOf instead, which vet cannot see. The shadowing
// half — a struct's own field versus an embedded one — stays static in
// schema_test.go (foreign embedded type plus own field is vet-clean).

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/weftgo/weft/internal/jsonconflict"
)

// clashing redeclares the JSON name "name" that jsonconflict.Base
// declares, as an int, so embedding the two together conflicts at one
// depth.
type clashing struct {
	Name int `json:"name"`
}

// depthB embeds depthA, so a struct embedding both depthB and depthA
// sees "x" claimed at two different depths.
type depthA struct {
	X int `json:"x"`
}

type depthB struct {
	depthA
	Y int `json:"y"`
}

// The same JSON name from two embedded structs cancels — encoding/json
// drops the pair, and the schema must not advertise a name decoding
// ignores. The unconflicting sibling survives.
func TestSchemaEmbeddedConflictCancels(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Base", Anonymous: true, Type: reflect.TypeOf(jsonconflict.Base{})},
		{Name: "Clashing", Anonymous: true, Type: reflect.TypeOf(clashing{})},
	})
	s := schemaFor(typ)
	if _, still := s.Properties["name"]; still {
		t.Error(`"name" must not be advertised: encoding/json drops the conflicting pair`)
	}
	if p := s.Properties["keep"]; p == nil || p.Type != "string" {
		t.Errorf(`the unconflicting "keep" must survive, got %v`, p)
	}
	if !slices.Equal(s.Required, []string{"keep"}) {
		t.Errorf("required = %v, want [keep]", s.Required)
	}
	// encoding/json agrees: the conflicting pair is dropped on the wire.
	b, err := json.Marshal(reflect.New(typ).Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"keep":""}` {
		t.Errorf("json.Marshal = %s, want the conflict dropped", got)
	}
}

// A field of the struct itself rescues a name two embedded structs
// conflict over — depth 0 beats the pair at depth 1 — with the outer
// field's schema and required flag, encoding/json's rule.
func TestSchemaOwnFieldRescuesConflict(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Base", Anonymous: true, Type: reflect.TypeOf(jsonconflict.Base{})},
		{Name: "Clashing", Anonymous: true, Type: reflect.TypeOf(clashing{})},
		{Name: "Name", Type: reflect.TypeOf(false), Tag: `json:"name"`},
	})
	s := schemaFor(typ)
	if p := s.Properties["name"]; p == nil || p.Type != "boolean" {
		t.Errorf(`the struct's own "name" must win the schema, got %v`, p)
	}
	if !slices.Equal(s.Required, []string{"name", "keep"}) {
		t.Errorf("required = %v, want [name keep]", s.Required)
	}
	b, err := json.Marshal(reflect.New(typ).Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"keep":"","name":false}` {
		t.Errorf("json.Marshal = %s, want the outer field to win", got)
	}
}

// The same JSON name claimed at two embedding depths does not cancel:
// encoding/json keeps the shallower contribution (depthA's x at depth 1
// over depthB's inherited x at depth 2), and the schema must agree —
// this was the unequal-depth desync where the schema dropped a name the
// wire still carries.
func TestSchemaUnequalDepthEmbeddedKeepsShallower(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "DepthB", Anonymous: true, Type: reflect.TypeOf(depthB{})},
		{Name: "DepthA", Anonymous: true, Type: reflect.TypeOf(depthA{})},
	})
	s := schemaFor(typ)
	if p := s.Properties["x"]; p == nil || p.Type != "integer" {
		t.Errorf(`"x" must survive with the shallower contribution's schema, got %v`, p)
	}
	if p := s.Properties["y"]; p == nil || p.Type != "integer" {
		t.Errorf(`"y" must survive, got %v`, p)
	}
	if !slices.Equal(s.Required, []string{"x", "y"}) {
		t.Errorf("required = %v, want [x y]", s.Required)
	}
	// encoding/json agrees: both names are on the wire.
	b, err := json.Marshal(reflect.New(typ).Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"y":0,"x":0}` {
		t.Errorf("json.Marshal = %s, want both names on the wire", got)
	}
}

// At equal depth, exactly one json-tagged claim wins over untagged ones
// — encoding/json's tiebreak. The schema must advertise the tagged
// field's type, not the last writer's: for this shape the pre-fix schema
// said boolean while the wire carried the tagged integer.
func TestSchemaTaggedClaimBeatsUntaggedAtEqualDepth(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Tagged", Type: reflect.TypeOf(int(0)), Tag: `json:"Name"`},
		{Name: "Name", Type: reflect.TypeOf(false)},
	})
	s := schemaFor(typ)
	if p := s.Properties["Name"]; p == nil || p.Type != "integer" {
		t.Errorf(`the tagged claim must win the schema, got %v`, p)
	}
	if !slices.Equal(s.Required, []string{"Name"}) {
		t.Errorf("required = %v, want [Name]", s.Required)
	}
	v := reflect.New(typ).Elem()
	v.FieldByName("Tagged").SetInt(9)
	b, err := json.Marshal(v.Interface())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"Name":9}` {
		t.Errorf("json.Marshal = %s, want the tagged field on the wire", got)
	}
}

// Two of the struct's own fields claiming one JSON name (both tagged —
// untagged Go names cannot collide) panic at construction: one handler
// field can never receive a value, the duplicate-name and
// non-struct-input precedent (ADR 0003). encoding/json's behaviour —
// dropping the pair — is why mirroring it would be worse: the schema
// would advertise a field no decode can ever deliver.
func TestSchemaOwnFieldCollisionPanics(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeOf(int(0)), Tag: `json:"x"`},
		{Name: "B", Type: reflect.TypeOf(""), Tag: `json:"x"`},
	})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("schemaFor did not panic on two own fields sharing a JSON name")
		}
		if !strings.Contains(fmt.Sprint(r), `two fields with the JSON name "x"`) {
			t.Fatalf("panic = %v, want it to name the collision", r)
		}
		// encoding/json drops the pair; the panic is weft's refusal to
		// advertise a field the decoder can never fill.
		b, err := json.Marshal(reflect.New(typ).Elem().Interface())
		if err != nil {
			t.Fatal(err)
		}
		if got := string(b); got != `{}` {
			t.Errorf("json.Marshal = %s, want the pair dropped", got)
		}
	}()
	schemaFor(typ)
}

// The same collision inside a nested struct type panics too: a nested
// struct is derived as its own object, and its unreachable handler
// field is no less a bug than the input struct's. The silent drop stays
// reserved for conflicts BETWEEN embedded structs (above), which is
// encoding/json's diamond — legitimate, and mirrored.
func TestSchemaNestedOwnCollisionPanics(t *testing.T) {
	nested := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeOf(int(0)), Tag: `json:"x"`},
		{Name: "B", Type: reflect.TypeOf(""), Tag: `json:"x"`},
	})
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "N", Type: nested},
	})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("schemaFor did not panic on a nested struct's own-name collision")
		}
		if !strings.Contains(fmt.Sprint(r), `two fields with the JSON name "x"`) {
			t.Fatalf("panic = %v, want it to name the collision", r)
		}
	}()
	schemaFor(typ)
}

// The panic above is scoped to *named* nesting: each named struct
// derives its own depth-0 space, so a nested struct's collision is its
// own bug. The same collision inside an EMBEDDED struct behaves
// differently — its claims flatten into the parent's space at depth 1
// and cancel under the dominance rules, so the name is dropped, not a
// panic: exactly encoding/json's drop, which the marshalled instance
// agrees with.
func TestSchemaEmbeddedOwnCollisionDrops(t *testing.T) {
	inner := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeOf(int(0)), Tag: `json:"x"`},
		{Name: "B", Type: reflect.TypeOf(""), Tag: `json:"x"`},
	})
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Inner", Anonymous: true, Type: inner},
	})
	s := schemaFor(typ)
	if _, still := s.Properties["x"]; still {
		t.Error(`"x" must be dropped: the embedded collision flattens into the parent's dominance rules`)
	}
	if len(s.Required) != 0 {
		t.Errorf("required = %v, want none", s.Required)
	}
	// encoding/json agrees: the conflicting pair is dropped on the wire.
	b, err := json.Marshal(reflect.New(typ).Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{}` {
		t.Errorf("json.Marshal = %s, want the embedded pair dropped", got)
	}
}

// A diamond — the same contributor embedded through two siblings — is
// between-embedded at every level, encoding/json's classic drop. The
// schema cancels both names even where encoding/json's breadth-first
// resolver keeps one (it expands a struct type once, so a field two
// levels below the fork escapes its doubling): the cancelled side is
// the conservative one — the wire name is ambiguous on decode, and a
// schema must not advertise what it cannot reliably deliver.
func TestSchemaDiamondEmbeddedConflictCancels(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Left", Anonymous: true, Type: reflect.TypeOf(depthB{})},
		{Name: "Right", Anonymous: true, Type: reflect.TypeOf(depthB{})},
	})
	s := schemaFor(typ)
	if _, still := s.Properties["x"]; still {
		t.Error(`"x" is claimed through equal-depth embeds on both sides; the schema drops the ambiguous name`)
	}
	if _, still := s.Properties["y"]; still {
		t.Error(`"y" is claimed through equal-depth embeds on both sides; the schema drops the ambiguous name`)
	}
	if len(s.Required) != 0 {
		t.Errorf("required = %v, want none", s.Required)
	}
}

// A tagged embed whose type name is unexported is an ordinary named
// field on the wire — encoding/json marshals it — so the schema must
// advertise it as a nested object. Found by the §7.1 corpus (2026-09
// —19); the schema used to drop the name entirely (the IsExported skip
// applied to anonymous fields too), leaving the model unable to see a
// field the decoder accepts (ADR 0003's amendment).
func TestTaggedEmbedOfUnexportedTypeIsAdvertised(t *testing.T) {
	type hidden struct {
		Kind string `json:"kind"`
	}
	type input struct {
		hidden  `json:"cfg"`
		Visible string `json:"visible"`
	}
	wire, err := json.Marshal(input{Visible: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"cfg":{"kind":""}`) {
		t.Fatalf("precondition: wire = %s, want cfg nested", wire)
	}
	tool := Tool("t", "", func(_ context.Context, _ input) (string, error) {
		return "", nil
	})
	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"cfg":{"type":"object","properties":{"kind":{"type":"string"}},"required":["kind"]},"visible":{"type":"string"}},"required":["cfg","visible"]}`
	if string(got) != want {
		t.Errorf("schema:\n got  %s\n want %s", got, want)
	}
}
