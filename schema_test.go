package weft_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/jsonconflict"
	"github.com/weftgo/weft/wefttest"
)

func TestSchemaDerivation(t *testing.T) {
	type inner struct {
		Tag string `json:"tag" jsonschema:"a label"`
	}
	type input struct {
		City    string   `json:"city" jsonschema:"the city to look up"`
		Days    *int     `json:"days,omitempty"`
		Units   string   `json:"units"`
		Labels  []string `json:"labels,omitempty"`
		Inner   inner    `json:"inner"`
		Ignored string   `json:"-"`
	}
	tool := weft.Tool("lookup", "Look up weather.",
		func(_ context.Context, in input) (string, error) {
			if in.City == "" {
				return "which city?", nil
			}
			return "sunny", nil
		})

	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"city":{"type":"string","description":"the city to look up"},"days":{"type":"integer"},"inner":{"type":"object","properties":{"tag":{"type":"string","description":"a label"}},"required":["tag"]},"labels":{"type":"array","items":{"type":"string"}},"units":{"type":"string"}},"required":["city","units","inner"]}`
	if string(got) != want {
		t.Errorf("schema:\n got  %s\n want %s", got, want)
	}
}

func TestSchemaEmbeddedStruct(t *testing.T) {
	type base struct {
		Lang string `json:"lang"`
	}
	type ext struct {
		base
		Query string `json:"query"`
	}
	tool := weft.Tool("search", "Search.",
		func(_ context.Context, _ ext) (string, error) { return "", nil })

	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"lang":{"type":"string"},"query":{"type":"string"}},"required":["lang","query"]}`
	if string(got) != want {
		t.Errorf("schema:\n got  %s\n want %s", got, want)
	}
}

// A field of the struct itself shadows an embedded one with the same
// JSON name — encoding/json's depth rule — for the property's schema
// and its required flag both, whatever the declaration order. The
// colliding embedded types come from internal/jsonconflict: defined
// here, go vet's structtag check would report the duplication this
// test exists to exercise.
func TestSchemaEmbeddedShadowing(t *testing.T) {
	shadow := weft.Tool("shadow", "", func(_ context.Context, in struct {
		jsonconflict.Base // declares "name": string, required
		// Shallower and omitempty: wins the schema, drops the required.
		Name int `json:"name,omitempty"`
	}) (string, error) {
		return "", nil
	})
	got, err := json.Marshal(shadow.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"keep":{"type":"string"},"name":{"type":"integer"}},"required":["keep"]}`
	if string(got) != want {
		t.Errorf("shadow schema:\n got  %s\n want %s", got, want)
	}
	// The schema must not lie about what decoding does: the outer field
	// is the one encoding/json decodes into.
	var in struct {
		jsonconflict.Base
		Name int `json:"name,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{"name":"str"}`), &in); err == nil {
		t.Error("encoding/json let a string into the shadowing int field?!")
	}

	// Declaration order the other way around: the outer field declared
	// before the embedded struct must still win.
	flipped := weft.Tool("flipped", "", func(_ context.Context, in struct {
		Name int    `json:"name"`
		Keep string `json:"keep"`
		jsonconflict.Base
	}) (string, error) {
		return "", nil
	})
	got, err = json.Marshal(flipped.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want = `{"type":"object","properties":{"keep":{"type":"string"},"name":{"type":"integer"}},"required":["name","keep"]}`
	if string(got) != want {
		t.Errorf("flipped schema:\n got  %s\n want %s", got, want)
	}
}

// The json ",string" option is reflected: the property is typed string —
// the quoted wire form encoding/json demands — so a schema-following
// model's arguments decode.
func TestSchemaStringOption(t *testing.T) {
	type in struct {
		N    int    `json:"n,string"`
		Flag bool   `json:"flag,string"`
		Note string `json:"note"`
	}
	tool := weft.Tool("x", "", func(_ context.Context, in in) (string, error) {
		return "ok", nil
	})
	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"flag":{"type":"string"},"n":{"type":"string"},"note":{"type":"string"}},"required":["n","flag","note"]}`
	if string(got) != want {
		t.Errorf("schema:\n got  %s\n want %s", got, want)
	}
	// The schema-conformant form — quoted values — must decode.
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"n":"42","flag":"true","note":"hi"}`)); err != nil {
		t.Fatalf("quoted, schema-conformant arguments rejected: %v", err)
	}
}

func TestSchemaTimeTimeIsDateTimeString(t *testing.T) {
	tool := weft.Tool("sched", "", func(_ context.Context, in struct {
		When time.Time `json:"when"`
	}) (string, error) {
		return "", nil
	})
	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"when":{"type":"string","format":"date-time"}},"required":["when"]}`
	if string(got) != want {
		t.Errorf("schema:\n got  %s\n want %s", got, want)
	}
}

func TestToolInvoke(t *testing.T) {
	type in struct {
		Q string `json:"q"`
	}
	type out struct {
		N int `json:"n"`
	}
	tool := weft.Tool("count", "Count characters.",
		func(_ context.Context, in in) (out, error) {
			return out{N: len(in.Q)}, nil
		})

	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"q":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"n":5}` {
		t.Errorf("Invoke output = %s, want {\"n\":5}", raw)
	}

	// Malformed arguments wrap ErrInvalidToolInput.
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"q":42}`)); !errors.Is(err, weft.ErrInvalidToolInput) {
		t.Errorf("type-mismatch error = %v, want ErrInvalidToolInput", err)
	}

	// Missing args decode to the zero value.
	raw, err = tool.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"n":0}` {
		t.Errorf("empty-args output = %s, want {\"n\":0}", raw)
	}
}

// Map values carry their type through AdditionalProperties:
// map[string]int stops being "some object" (Fix 10). An any value
// type says nothing — map[string]any stays a bare object rather than
// carrying an empty additionalProperties:{}.
func TestSchemaMapValuesTyped(t *testing.T) {
	type inner struct {
		Tag string `json:"tag"`
	}
	type input struct {
		Scores   map[string]int      `json:"scores"`
		Nested   map[string][]string `json:"nested,omitempty"`
		Ancestry map[string]*inner   `json:"ancestry,omitempty"`
		Free     map[string]any      `json:"free,omitempty"`
	}
	tool := weft.Tool("maps", "", func(_ context.Context, in input) (string, error) {
		return "ok", nil
	})
	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"ancestry":{"type":"object","additionalProperties":{"type":"object","properties":{"tag":{"type":"string"}},"required":["tag"]}},"free":{"type":"object"},"nested":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},"scores":{"type":"object","additionalProperties":{"type":"integer"}}},"required":["scores"]}`
	if string(got) != want {
		t.Errorf("schema:\n got  %s\n want %s", got, want)
	}
}

// A foreign schema parsed with ParseSchema keeps its bytes: the
// structured fields the core can express are populated for readers
// that walk the tree, and MarshalJSON re-emits the document verbatim —
// an enum or a oneOf the Schema type cannot express still reaches the
// model exactly as the server wrote it (TODO §7.1; ADR 0003).
func TestParseSchemaKeepsForeignBytes(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{"units":{"type":"string","enum":["c","f"]},"when":{"oneOf":[{"type":"string"},{"type":"number"}]}},"required":["units"],"$schema":"https://json-schema.org/draft/2020-12/schema"}`)
	s, err := weft.ParseSchema(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" || len(s.Properties) != 2 || s.Required[0] != "units" {
		t.Errorf("structured fields not populated: %+v", s)
	}
	if s.Properties["units"].Type != "string" {
		t.Errorf("units type = %q", s.Properties["units"].Type)
	}
	got, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Errorf("marshal:\n got  %s\n want %s", got, in)
	}
	// A RawTool built on it marshals the same bytes, and the manifest's
	// input_schema (which serialises the *Schema directly) shows them too.
	tool := weft.RawTool("foreign", "a foreign tool", s,
		func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil })
	got, err = json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Errorf("tool schema:\n got  %s\n want %s", got, in)
	}
	// The manifest indents its JSON, so the document's bytes are
	// reformatted there but nothing is lost: the input_schema node is
	// semantically the parsed document, oneOf and $schema included.
	b, err := weft.Manifest(weft.New(wefttest.Script(), weft.Name("m"), tool))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Agents []struct {
			Tools []struct {
				InputSchema json.RawMessage `json:"input_schema"`
			} `json:"tools"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var gotNode, wantNode any
	if err := json.Unmarshal(doc.Agents[0].Tools[0].InputSchema, &gotNode); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(in, &wantNode); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotNode, wantNode) {
		t.Errorf("manifest input_schema = %v, want %v", gotNode, wantNode)
	}
}

// The verbatim bytes survive the copies the core makes: New clones the
// tool (and its schema tree) into its frozen registry, and Tools
// returns clones — every copy must still marshal what the server sent.
func TestParseSchemaSurvivesClones(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string","minLength":1}}}`)
	s, err := weft.ParseSchema(in)
	if err != nil {
		t.Fatal(err)
	}
	tool := weft.RawTool("foreign", "", s,
		func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil })
	agt := weft.New(wefttest.Script(), tool)
	got, err := json.Marshal(agt.Tools()[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Errorf("cloned schema:\n got  %s\n want %s", got, in)
	}
}

// ParseSchema is lenient about keyword shapes the Schema type cannot
// hold: a boolean additionalProperties (every zod-built TypeScript MCP
// server emits "additionalProperties": false), a type array, tuple or
// boolean items, a non-string description. Each leaves its field zero;
// none rejects the document; the bytes still cross whole. Before this
// pin, one such tool failed the whole mcp.Tools import.
func TestParseSchemaToleratesForeignShapes(t *testing.T) {
	in := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"q":{"type":["string","null"],"description":"query"},"tags":{"type":"array","items":[{"type":"string"}]},"any":true,"meta":{"type":"object","additionalProperties":{"type":"string"},"description":5}},"required":["q"],"additionalProperties":false}`)
	s, err := weft.ParseSchema(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" || s.AdditionalProperties != nil || len(s.Required) != 1 || s.Required[0] != "q" {
		t.Errorf("top level = %+v", s)
	}
	if q := s.Properties["q"]; q == nil || q.Type != "" || q.Description != "query" {
		t.Errorf("q = %+v, want an unconstrained type with its description", q)
	}
	if tags := s.Properties["tags"]; tags == nil || tags.Type != "array" || tags.Items != nil {
		t.Errorf("tags = %+v, want array with tuple items left nil", tags)
	}
	if any := s.Properties["any"]; any == nil || any.Type != "" {
		t.Errorf("any = %+v, want an unconstrained node for a boolean schema", any)
	}
	if meta := s.Properties["meta"]; meta == nil || meta.AdditionalProperties == nil || meta.AdditionalProperties.Type != "string" || meta.Description != "" {
		t.Errorf("meta = %+v, want typed additionalProperties and a dropped non-string description", meta)
	}
	got, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Errorf("marshal:\n got  %s\n want %s", got, in)
	}
}

func TestParseSchemaRejects(t *testing.T) {
	for name, b := range map[string]string{
		"empty":         ``,
		"not json":      `{`,
		"trailing data": `{} {}`,
		"array":         `["object"]`,
		"scalar top":    `{"type":"string"}`,
		"no type":       `{"properties":{}}`,
		"null":          `null`,
		"type array":    `{"type":["object","null"]}`,
		"bool schema":   `true`,
	} {
		if _, err := weft.ParseSchema(json.RawMessage(b)); err == nil {
			t.Errorf("%s: ParseSchema accepted %s", name, b)
		}
	}
}

// A reflected schema (no parsed bytes) marshals exactly as before the
// raw field existed — every committed golden depends on this.
func TestSchemaMarshalUnchangedWithoutRaw(t *testing.T) {
	tool := weft.Tool("t", "", func(_ context.Context, in struct {
		A string `json:"a"`
	}) (string, error) {
		return in.A, nil
	})
	got, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`
	if string(got) != want {
		t.Errorf("marshal:\n got  %s\n want %s", got, want)
	}
}
