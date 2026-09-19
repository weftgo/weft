package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/weftgo/weft"
)

// The 7.1 corpus: real tool input struct shapes — one per row of the
// derivation rules ADR 0003 pins — measuring weft's reflector against
// google/jsonschema-go v0.4.3's For[T], the reflector behind the
// official MCP SDK. The verdict (ADR 0003's amendment: keep the
// hand-rolled, zero-dependency core) rests on this table.
//
// Rows 26–28 mirror shapes the repo owns but cannot import here
// (subagentInput in the core, probeIn in wefttest/conformance — both
// unexported); the mirror keeps the corpus self-contained.
type corpusInner struct {
	Tag string `json:"tag" jsonschema:"a label"`
}

type corpusIface interface{ Foo() }

type corpusBase struct {
	Lang string `json:"lang"`
}

// CorpusTaggedEmbed is exported on purpose: the corpus measures the
// common tagged-embed case; the unexported-type variant is a separate
// regression test in the core (schema_conflict_test.go).
type CorpusTaggedEmbed struct {
	Kind string `json:"kind"`
}

type corpusShadow struct {
	Lang string `json:"lang"`
}

// The same-depth cancellation row lives in the core's
// schema_conflict_test.go instead: declaring the conflicting embeds
// statically is not possible under the vet gate — `struct field Lang
// repeats json tag "lang"` fires on both the same-name and the
// same-tag-different-name variants (verified 2026-09-19) — and the
// workaround (reflect.StructOf) cannot feed a generic row here.
type corpusNode struct {
	Children []*corpusNode `json:"children,omitempty"`
}

// rowTest is one corpus row's measurement: weft's reflected schema and
// jsonschema-go's For[T] schema for the same input type, marshalled.
type rowTest struct {
	name string
	weft json.RawMessage
	gjs  json.RawMessage
	err  error // For[T] failed (expected on the recursive rows)
}

// runRow measures one input type. Generic instantiation cannot sit in
// a slice literal, so each row is one call in corpus().
func runRow[In any](name string) rowTest {
	rt := rowTest{name: name}
	tool := weft.Tool(name, "corpus probe",
		func(context.Context, In) (string, error) { return "", nil })
	b, err := json.Marshal(tool.InputSchema)
	if err != nil {
		panic(err)
	}
	rt.weft = b
	s, err := jsonschema.For[In](nil)
	if err != nil {
		rt.err = err
		return rt
	}
	if b, err = json.Marshal(s); err != nil {
		panic(err)
	}
	rt.gjs = b
	return rt
}

// corpus builds the rows — 23, packing the plan's 30 shapes (TODO §7.1: "thirty real tool input structs";
// several of the plan's numbered rows pack into one struct where the
// shapes are homogeneous, so every derivation rule is measured).
func corpus() []rowTest {
	type scalars struct {
		S   string  `json:"s"`
		B   bool    `json:"b"`
		I   int     `json:"i"`
		I64 int64   `json:"i64"`
		U8  uint8   `json:"u8"`
		F32 float32 `json:"f32"`
		F64 float64 `json:"f64"`
	}
	type ptrField struct {
		Days *int `json:"days"`
	}
	type omitemptyField struct {
		Reason string `json:"reason,omitempty"`
	}
	type naming struct {
		Query   string `json:"-"`
		Plain   string
		Renamed string `json:"renamed"`
	}
	type described struct {
		City string `json:"city" jsonschema:"the city to look up"`
	}
	type arrays struct {
		SS []string `json:"ss"`
		AI [3]int   `json:"ai"`
		MI [][]int  `json:"mi"`
	}
	type maps struct {
		MI map[string]int         `json:"mi"`
		MA map[string]any         `json:"ma"`
		MM map[string]corpusInner `json:"mm"`
	}
	type nested struct {
		Inner corpusInner  `json:"inner"`
		P     *corpusInner `json:"p"`
	}
	type times struct {
		T  time.Time  `json:"t"`
		TP *time.Time `json:"tp"`
	}
	type bytesField struct {
		B []byte `json:"b"`
	}
	type anyField struct {
		A any `json:"a"`
	}
	type ifaceField struct {
		I corpusIface `json:"i"`
	}
	type embedUntagged struct {
		corpusBase
		Query string `json:"query"`
	}
	type embedTagged struct {
		CorpusTaggedEmbed `json:"te"`
		Other             string `json:"other"`
	}
	type embedShadow struct {
		corpusShadow        // claims "lang" at depth 1
		Lang         string `json:"lang"` // wins at depth 0
	}
	type stringTags struct {
		I int  `json:"i,string"`
		B bool `json:"b,string"`
	}
	type pointerIn = *struct {
		Q string `json:"q"`
	}
	// Rows 26–28: the repo's own shapes, mirrored (the originals are
	// unexported in their packages).
	type subagentIn struct {
		Prompt string `json:"prompt" jsonschema:"The task, stated in full: the agent sees only this prompt, not the conversation."`
	}
	type lookupIn struct {
		OrderID string `json:"order_id" jsonschema:"the order to look up"`
	}
	type probeIn struct {
		N    int               `json:"n"`
		Note string            `json:"note,omitempty"`
		Meta map[string]string `json:"meta,omitempty"`
		When time.Time         `json:"when"`
	}
	type empty struct{}
	type kitchen struct {
		S    string         `json:"s" jsonschema:"a scalar"`
		P    *int           `json:"p"`
		Opt  string         `json:"opt,omitempty"`
		Arr  []corpusInner  `json:"arr"`
		M    map[string]int `json:"m"`
		Nest corpusInner    `json:"nest"`
		T    time.Time      `json:"t"`
		B    []byte         `json:"b"`
		Any  any            `json:"any"`
		Emb  corpusBase     `json:"-"`
		Tree *corpusNode    `json:"tree"`
		Free map[string]any `json:"free"`
	}

	return []rowTest{
		runRow[scalars]("scalars"),
		runRow[ptrField]("pointer field"),
		runRow[omitemptyField]("omitempty field"),
		runRow[naming]("naming: json:\"-\" and untagged"),
		runRow[described]("jsonschema description tag"),
		runRow[arrays]("arrays"),
		runRow[maps]("maps"),
		runRow[nested]("nested struct value and pointer"),
		runRow[times]("time.Time value and pointer"),
		runRow[bytesField]("[]byte"),
		runRow[anyField]("any field"),
		runRow[ifaceField]("interface field"),
		runRow[embedUntagged]("embedded untagged"),
		runRow[embedTagged]("embedded tagged"),
		runRow[embedShadow]("embedded shadowing, different depth"),
		runRow[stringTags]("json:\",string\" scalars"),
		runRow[corpusNode]("recursive type"),
		runRow[pointerIn]("pointer-to-struct In"),
		runRow[subagentIn]("subagentInput (mirrored)"),
		runRow[lookupIn]("getting-started LookupInput (mirrored)"),
		runRow[probeIn]("conformance probeIn (mirrored)"),
		runRow[empty]("empty struct"),
		runRow[kitchen]("kitchen sink"),
	}
}

// isClosing reports whether an additionalProperties value is a struct
// being closed (false, or {"not": {}}) — jsonschema-go's documented
// policy; weft leaves struct objects open because the loop decodes
// leniently and a schema must not advertise a constraint the decoder
// does not enforce (ADR 0003; StrictInput is the opt-in). Typed map
// values — a schema — are weft's own rule and not closing.
func isClosing(v any) bool {
	if b, ok := v.(bool); ok {
		return !b
	}
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		if inner, ok := m["not"].(map[string]any); ok && len(inner) == 0 {
			return true
		}
	}
	return false
}

func isEmptyMap(v any) bool {
	m, ok := v.(map[string]any)
	return ok && len(m) == 0
}

// nullable unwraps jsonschema-go's ["null", X] rendering to X,
// reporting whether it was nullable. Its pointer and slice fields stay
// required but accept null; weft's rule is the documented ADR 0003
// decision — pointer or omitempty ⇒ optional, the bare type.
func nullable(v any) (any, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 || arr[0] != "null" {
		return v, false
	}
	return arr[1], true
}

// stripDateFormats removes weft's "format": "date-time" annotations
// (ADR 0003's 2026-09-09 amendment) before comparison: jsonschema-go
// v0.4.3 emits a plain string for time.Time. A weft policy choice the
// corpus records, not a fidelity gap — the format only narrows what a
// provider may display; decode accepts either.
func stripDateFormats(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if k == "format" && val == "date-time" {
				continue
			}
			out[k] = stripDateFormats(val)
		}
		return out
	case []any:
		for i := range v {
			v[i] = stripDateFormats(v[i])
		}
		return v
	default:
		return v
	}
}

// normalize adapts jsonschema-go's rendering to weft's documented
// choices so the residual comparison shows only real differences:
//
//   - closing forms (additionalProperties: false / {"not":{}}) and
//     additionalProperties: true dropped — both ≡ the absent form;
//   - "properties": {} counts as absent;
//   - optional narrowing bounds dropped (minimum, maximum, minItems,
//     maxItems — weft omits them; decode still rejects out-of-range
//     values, so nothing is silently accepted);
//   - nullable types unwrapped to the bare type, and a property
//     unwrapped that way leaves the required list exactly when weft's
//     side does not require it (weft: pointer ⇒ optional;
//     jsonschema-go: required-but-nullable);
//   - a bare true schema counts as the empty schema — both mean
//     "anything".
//
// w is the weft document, walked in parallel for the required-list
// rule.
func normalize(gjs, w any) any {
	gm, ok := gjs.(map[string]any)
	if !ok {
		if b, isBool := gjs.(bool); isBool && b {
			return map[string]any{} // true ≡ {}: anything
		}
		return gjs
	}
	wm, _ := w.(map[string]any)
	out := make(map[string]any, len(gm))
	for k, v := range gm {
		switch {
		case k == "additionalProperties" && (isClosing(v) || v == true):
			continue
		case k == "properties" && isEmptyMap(v):
			continue
		case slices.Contains([]string{"minimum", "maximum", "minItems", "maxItems"}, k):
			continue
		}
		if k == "type" {
			if bare, was := nullable(v); was {
				out[k] = bare
				continue
			}
		}
		out[k] = v
	}
	// Recurse over the structural keys, pairing each child with the
	// weft side's, and remember which properties were unwrapped from
	// nullable — their required entries go exactly where weft's are.
	unwrapped := map[string]bool{}
	if props, ok := out["properties"].(map[string]any); ok {
		var wprops map[string]any
		if wm != nil {
			wprops, _ = wm["properties"].(map[string]any)
		}
		for name, cv := range props {
			var wv any
			if wprops != nil {
				wv = wprops[name]
			}
			if pm, ok := cv.(map[string]any); ok {
				if tv, isType := pm["type"]; isType {
					if _, was := nullable(tv); was {
						unwrapped[name] = true
					}
				}
			}
			props[name] = normalize(cv, wv)
		}
	}
	for _, k := range []string{"items", "additionalProperties"} {
		if child, ok := out[k].(map[string]any); ok {
			var wv any
			if wm != nil {
				wv = wm[k]
			}
			out[k] = normalize(child, wv)
		}
	}
	if req, ok := out["required"].([]any); ok && wm != nil {
		var wreq []any
		if wr, ok := wm["required"].([]any); ok {
			wreq = wr
		}
		kept := req[:0]
		for _, r := range req {
			name, _ := r.(string)
			if slices.ContainsFunc(wreq, func(x any) bool { return x == r }) || !unwrapped[name] {
				kept = append(kept, r)
			}
		}
		if len(kept) == 0 {
			delete(out, "required")
		} else {
			out["required"] = kept
		}
	}
	return out
}

// TestSchemaCorpus is 7.1's measurement. Every row prints under -v
// with its verdict; the test fails only on a row the decision rule
// cannot classify — the table is the evidence, the ADR carries the
// verdict.
func TestSchemaCorpus(t *testing.T) {
	for _, r := range corpus() {
		if r.err != nil {
			if strings.Contains(r.err.Error(), "cycle detected") &&
				slices.Contains([]string{"recursive type", "kitchen sink"}, r.name) {
				t.Logf("%-42s jsonschema-go cannot derive recursive types at all; weft terminates with the bare object the loop decodes into — weft wire-right", r.name)
				continue
			}
			t.Errorf("%s: jsonschema.For failed: %v", r.name, r.err)
			continue
		}
		var w, g any
		if err := json.Unmarshal(r.weft, &w); err != nil {
			t.Errorf("%s: weft schema not JSON: %v", r.name, err)
			continue
		}
		if err := json.Unmarshal(r.gjs, &g); err != nil {
			t.Errorf("%s: jsonschema-go schema not JSON: %v", r.name, err)
			continue
		}
		w = stripDateFormats(w)
		verdict, ok, residue := classify(r.name, g, w)
		switch {
		case !ok:
			t.Errorf("%s: UNCLASSIFIED difference\n weft: %s\n gjs:  %s", r.name, r.weft, r.gjs)
		case residue:
			t.Errorf("%s: %s — but the claimed difference did not verify\n weft: %s\n gjs:  %s", r.name, verdict, r.weft, r.gjs)
		default:
			t.Logf("%-42s %s", r.name, verdict)
		}
	}
}

// classify recognises the row classes the decision rule names, each
// verified structurally rather than trusted by name. ok reports a
// recognised class; residue reports that the claimed difference did
// not reduce as stated.
func classify(name string, gjsDoc, weftDoc any) (verdict string, ok bool, residue bool) {
	switch name {
	case "[]byte":
		// jsonschema-go renders []byte as an array of 0..255 integers;
		// encoding/json carries []byte as a base64 string, so weft's
		// string is the wire form — the array schema rejects every
		// value the decoder actually accepts.
		w, g := prop(weftDoc, "b"), prop(gjsDoc, "b")
		gt, _ := nullable(g["type"]) // the array itself is nullable in gjs
		return "fidelity, weft wire-right: string (base64, the encoding/json wire form) vs array of 0..255 integers",
			true, w == nil || g == nil || w["type"] != "string" || gt != "array"
	case "json:\",string\" scalars":
		// jsonschema-go ignores the ,string option and advertises the
		// bare type; encoding/json demands the quoted form, so weft's
		// string (ADR 0003's 2026-09-18 amendment) is the wire form.
		wi, gi := prop(weftDoc, "i"), prop(gjsDoc, "i")
		wb, gb := prop(weftDoc, "b"), prop(gjsDoc, "b")
		return "fidelity, weft wire-right: string (the quoted wire form) vs the bare type",
			true, wi["type"] != "string" || gi["type"] != "integer" || wb["type"] != "string" || gb["type"] != "boolean"
	case "embedded tagged":
		// jsonschema-go flattens an embedded struct even when it carries
		// a json name tag; encoding/json nests it under the tag.
		return "fidelity, weft wire-right: tagged embed nests under its json name vs flattened fields",
			true, !nestsVsFlattens(weftDoc, gjsDoc)
	default:
		if reflect.DeepEqual(normalize(gjsDoc, weftDoc), weftDoc) {
			return "identical for the supported subset (policy differences only: open objects, pointer⇒optional, date-time format, narrowing bounds omitted)", true, false
		}
		return "", false, true
	}
}

// prop returns doc's property schema by name, or nil.
func prop(doc any, name string) map[string]any {
	m, _ := doc.(map[string]any)
	if m == nil {
		return nil
	}
	p, _ := m["properties"].(map[string]any)
	if p == nil {
		return nil
	}
	pm, _ := p[name].(map[string]any)
	return pm
}

func nestsVsFlattens(weftDoc, gjsDoc any) bool {
	wp, _ := props(weftDoc)
	gp, _ := props(gjsDoc)
	if wp == nil || gp == nil {
		return false
	}
	_, weftHasTe := wp["te"]
	_, gjsHasKind := gp["kind"]
	_, gjsHasTe := gp["te"]
	return weftHasTe && gjsHasKind && !gjsHasTe
}

func props(doc any) (map[string]any, bool) {
	m, _ := doc.(map[string]any)
	if m == nil {
		return nil, false
	}
	p, _ := m["properties"].(map[string]any)
	return p, p != nil
}

// TestToSDKRoundTrips pins the bridge for every corpus shape: the
// marshalled weft schema (what toSDK hands the SDK) comes back through
// an SDK-decoded value (the client's map form) and weft.ParseSchema,
// and must re-marshal to the same document. Both sides are compared
// as decoded JSON — struct encoding orders keys by field, map encoding
// sorts them, so only the semantic compare is fair. If this needed a
// switch on schema keywords, ADR 0003's shape bet would have failed.
func TestToSDKRoundTrips(t *testing.T) {
	if back, err := fromSDK(toSDK(nil)); err != nil || string(back) != `{"type":"object"}` {
		t.Errorf("nil schema: back = %s, err = %v; want the empty object schema", back, err)
	}
	same := func(a, b json.RawMessage) bool {
		var x, y any
		return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
	}
	for _, r := range corpus() {
		var sdkValue any // the SDK's client side holds the schema as decoded JSON
		if err := json.Unmarshal(r.weft, &sdkValue); err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		back, err := fromSDK(sdkValue)
		if err != nil {
			t.Errorf("%s: fromSDK: %v", r.name, err)
			continue
		}
		s, err := weft.ParseSchema(back)
		if err != nil {
			t.Errorf("%s: ParseSchema rejected the round-tripped schema: %v (%s)", r.name, err, back)
			continue
		}
		if remarshalled, err := json.Marshal(s); err != nil || !same(remarshalled, r.weft) {
			t.Errorf("%s: round trip\n got  %s\n want %s", r.name, remarshalled, r.weft)
		}
	}
}
