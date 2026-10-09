package obsdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/weftgo/weft/core"
)

// CheckToolArgs checks a tool call's arguments against the tool's
// input schema as a tools record stores it (ToolEntry.Schema): the
// check a playground `tool_args` transcript edit passes before it is
// accepted (ADR 0029 §8), on both of its surfaces — weft/runtime's
// authoritative copy and Studio's mirror — so the two refuse in one
// wording. A nil error means the arguments fit.
//
// The failure is a *core.ToolError coded INVALID_INPUT whose cause is
// core.ErrInvalidToolInput, worded like the loop where the loop has a
// sentence for it — `INVALID_INPUT: tool "lookup_order": field "days":
// expected integer, got string` — naming the field by its JSON path
// (dotted; an array element by its index). The loop's decode does not
// check required fields or enums; their sentences ("missing required
// field …", "… is not one of the schema's values") are this check's
// own. A null field is a no-op, as encoding/json decodes it; an
// integer must be written as one (3.0 is refused, as the loop refuses
// it).
//
// The arguments must be one JSON object (empty or null reads as {}, as
// the loop decodes them). The schema walk covers the vocabulary weft's
// reflector and a hand-written schema share: type (one or a list),
// properties, required, additionalProperties (false refuses an
// undeclared field; a schema checks each extra one), items and enum.
// Any other keyword is not checked. An empty or unreadable schema
// checks the object shape alone.
func CheckToolArgs(tool string, schema, args json.RawMessage) error {
	fail := func(format string, a ...any) error {
		return &core.ToolError{Code: core.CodeInvalidInput,
			Message: fmt.Sprintf("tool %q: %s", tool, fmt.Sprintf(format, a...)),
			Err:     core.ErrInvalidToolInput}
	}
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		trimmed = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// The loop's own words (core's describeDecodeError).
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return fail("invalid JSON at offset %d: %s", syn.Offset, syn.Error())
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return fail("invalid JSON: unexpected end of input")
		}
		return fail("invalid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail("trailing data after the JSON arguments")
	}
	if _, ok := v.(map[string]any); !ok {
		return fail("expected object at the top level, got %s", jsonKind(v))
	}
	var s map[string]any
	if len(bytes.TrimSpace(schema)) == 0 || json.Unmarshal(schema, &s) != nil {
		return nil
	}
	if msg := checkValue("", s, v); msg != "" {
		return fail("%s", msg)
	}
	return nil
}

// checkValue walks one value against its schema and returns the first
// mismatch, worded, or "".
func checkValue(path string, s map[string]any, v any) string {
	if v == nil && path != "" {
		// encoding/json's rule, which the loop decodes by: a null field
		// is a no-op whatever its type (a strict-mode model writes null
		// for an optional field).
		return ""
	}
	at := func(want, got string) string {
		if path == "" {
			return fmt.Sprintf("expected %s at the top level, got %s", want, got)
		}
		return fmt.Sprintf("field %q: expected %s, got %s", path, want, got)
	}
	if types := schemaTypes(s["type"]); len(types) > 0 {
		match := false
		for _, t := range types {
			if typeMatches(t, v) {
				match = true
				break
			}
		}
		if !match {
			got := jsonKind(v)
			if n, ok := v.(json.Number); ok && slices.Contains(types, "integer") {
				got = "number " + n.String()
			}
			return at(strings.Join(types, " or "), got)
		}
	}
	if enum, ok := s["enum"].([]any); ok && len(enum) > 0 {
		have := canonical(v)
		found := false
		for _, e := range enum {
			if canonical(e) == have {
				found = true
				break
			}
		}
		if !found {
			if path == "" {
				return "the arguments are not one of the schema's values"
			}
			return fmt.Sprintf("field %q: %s is not one of the schema's values", path, have)
		}
	}
	switch v := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, has := v[name]; name != "" && !has {
					return fmt.Sprintf("missing required field %q", join(path, name))
				}
			}
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := props[k].(map[string]any); ok {
				if msg := checkValue(join(path, k), ps, v[k]); msg != "" {
					return msg
				}
				continue
			}
			if _, declared := props[k]; declared {
				continue // a boolean schema (true): anything goes
			}
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					return fmt.Sprintf("unknown field %q: not in the schema", join(path, k))
				}
			case map[string]any:
				if msg := checkValue(join(path, k), ap, v[k]); msg != "" {
					return msg
				}
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range v {
				if msg := checkValue(join(path, strconv.Itoa(i)), items, x); msg != "" {
					return msg
				}
			}
		}
	}
	return ""
}

// schemaTypes reads a schema's type keyword: one name or a list.
func schemaTypes(t any) []string {
	switch t := t.(type) {
	case string:
		if t != "" {
			return []string{t}
		}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// typeMatches reports whether v is a value of JSON Schema type t. An
// unknown type name matches anything (not checked).
func typeMatches(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		// The literal must be integral, as the loop's decode into an
		// int reads it: 3.0 and 3e0 are refused there, so here.
		_, err := n.Int64()
		return err == nil
	}
	return true
}

// jsonKind names a decoded value's JSON kind in encoding/json's words
// (what the loop's own INVALID_INPUT says after "got").
func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number:
		return "number"
	case nil:
		return "null"
	}
	return "value"
}

// canonical is a value's compact JSON (map keys sorted), for the enum
// comparison.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}
