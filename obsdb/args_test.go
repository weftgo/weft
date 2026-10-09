package obsdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// TestCheckToolArgs pins the tool_args edit check (ADR 0029 §8): the
// loop's INVALID_INPUT wording, the field named by its JSON path, and
// a schema Go's reflector wrote checked as the tool advertises it.
func TestCheckToolArgs(t *testing.T) {
	type item struct {
		SKU string `json:"sku"`
		Qty int    `json:"qty"`
	}
	type in struct {
		OrderID string            `json:"order_id"`
		Days    int               `json:"days,omitempty"`
		Items   []item            `json:"items,omitempty"`
		Tags    map[string]string `json:"tags,omitempty"`
	}
	def := core.Tool("lookup_order", "", func(context.Context, in) (string, error) { return "", nil })
	schema, err := json.Marshal(def.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	closed := json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["fast","slow"]},"n":{"type":["integer","null"]}},"additionalProperties":false}`)
	for _, c := range []struct {
		schema json.RawMessage
		args   string
		want   string
	}{
		{schema, `{"order_id":"42"}`, ""},
		{schema, ``, `INVALID_INPUT: tool "lookup_order": missing required field "order_id"`},
		{schema, `{"order_id":"42","days":"3"}`, `INVALID_INPUT: tool "lookup_order": field "days": expected integer, got string`},
		{schema, `{"order_id":"42","days":1.5}`, `INVALID_INPUT: tool "lookup_order": field "days": expected integer, got number 1.5`},
		{schema, `{"order_id":"42","days":3.0}`, `INVALID_INPUT: tool "lookup_order": field "days": expected integer, got number 3.0`},
		{schema, `{"order_id":"42","days":3e0}`, `INVALID_INPUT: tool "lookup_order": field "days": expected integer, got number 3e0`},
		// A null field is a no-op, as encoding/json decodes it — a
		// required one included (present, null).
		{schema, `{"order_id":null,"days":null,"items":null,"tags":null}`, ""},
		{schema, `{"order_id":"42","items":[{"sku":null,"qty":1}]}`, ""},
		{schema, `{"order_id":"42","items":[{"sku":"a","qty":1},{"sku":"b","qty":"2"}]}`, `INVALID_INPUT: tool "lookup_order": field "items.1.qty": expected integer, got string`},
		{schema, `{"order_id":"42","tags":{"k":1}}`, `INVALID_INPUT: tool "lookup_order": field "tags.k": expected string, got number`},
		{schema, `["42"]`, `INVALID_INPUT: tool "lookup_order": expected object at the top level, got array`},
		{schema, `{"order_id":"42"} {}`, `INVALID_INPUT: tool "lookup_order": trailing data after the JSON arguments`},
		{schema, `{"order_id":`, `INVALID_INPUT: tool "lookup_order": invalid JSON: unexpected end of input`},
		{closed, `{"mode":"fast","n":null}`, ""},
		{closed, `{"mode":"medium"}`, `INVALID_INPUT: tool "lookup_order": field "mode": "medium" is not one of the schema's values`},
		{closed, `{"n":"1"}`, `INVALID_INPUT: tool "lookup_order": field "n": expected integer or null, got string`},
		{closed, `{"x":1}`, `INVALID_INPUT: tool "lookup_order": unknown field "x": not in the schema`},
		{nil, `{"anything":[1]}`, ""},
		{nil, `{"a":}`, `INVALID_INPUT: tool "lookup_order": invalid JSON at offset 6: invalid character '}' looking for beginning of value`},
		{nil, `"x"`, `INVALID_INPUT: tool "lookup_order": expected object at the top level, got string`},
	} {
		err := obsdb.CheckToolArgs("lookup_order", c.schema, json.RawMessage(c.args))
		got := ""
		if err != nil {
			got = err.Error()
			if !errors.Is(err, core.ErrInvalidToolInput) {
				t.Errorf("%s: %v is not ErrInvalidToolInput", c.args, err)
			}
		}
		if got != c.want {
			t.Errorf("CheckToolArgs(%s) = %q, want %q", c.args, got, c.want)
		}
	}
}

// A playground tool_args edit is checked against the schema the run's
// tools record stored before the replay is accepted: a wrong type is
// refused in the loop's own words, naming the field.
func ExampleCheckToolArgs() {
	schema := json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`)
	fmt.Println(obsdb.CheckToolArgs("lookup_order", schema, json.RawMessage(`{"order_id":"7"}`)))
	fmt.Println(obsdb.CheckToolArgs("lookup_order", schema, json.RawMessage(`{"order_id":7}`)))
	// Output:
	// <nil>
	// INVALID_INPUT: tool "lookup_order": field "order_id": expected string, got number
}
