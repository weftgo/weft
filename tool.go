package weft

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
)

// ToolDef is a named, schema-described tool the model can call. Build one
// with the generic Tool constructor; the agent loop (and any manual
// dispatcher) executes it through Invoke.
type ToolDef struct {
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	InputSchema  *Schema `json:"input_schema,omitempty"`
	OutputSchema *Schema `json:"output_schema,omitempty"`

	// sourceFile and sourceLine locate the Tool(...) call that defined
	// this tool, for the manifest. Unexported: they are metadata, not
	// configuration.
	sourceFile string
	sourceLine int

	invoke func(ctx context.Context, args json.RawMessage) (string, error)
}

// Tool defines a tool from a plain function. In and Out are inferred from
// the handler and the input schema is reflected from In's struct tags, so
// the compiler checks the handler's shape and nothing is written twice:
//
//	type RefundInput struct {
//	    OrderID string `json:"order_id" jsonschema:"the order to refund"`
//	    Reason  string `json:"reason,omitempty"`
//	}
//
//	weft.Tool("refund_order", "Refund a customer's order",
//	    func(ctx context.Context, in RefundInput) (Receipt, error) {
//	        return billing.Refund(ctx, in.OrderID, in.Reason)
//	    })
//
// What the model sees as the result is the text of a string Out, and the
// JSON encoding of any other Out. Inside the handler, CallFromContext
// reports which call is running.
//
// The handler shape mirrors the official Go MCP SDK's AddTool[In, Out], so
// a weft tool can be exposed over MCP without an adapter layer.
//
// Tool panics if name is empty, fn is nil, or In is not a struct (or a
// pointer to one): providers and MCP require an object at the top level
// of a tool schema, and a scalar there would fail every real call.
func Tool[In, Out any](name, description string, fn func(ctx context.Context, in In) (Out, error)) *ToolDef {
	if name == "" {
		panic("weft: Tool called with an empty name")
	}
	if fn == nil {
		panic(fmt.Sprintf("weft: Tool %q called with a nil handler", name))
	}
	inType := reflect.TypeOf(new(In)).Elem()
	if k := inType.Kind(); k != reflect.Struct && (k != reflect.Pointer || inType.Elem().Kind() != reflect.Struct) {
		panic(fmt.Sprintf("weft: Tool %q: input must be a struct or a pointer to one, got %s", name, k))
	}
	// The defining call site, for the manifest (TODO §2.9). One frame up:
	// the caller of Tool.
	_, file, line, _ := runtime.Caller(1)
	// A string Out is sent verbatim (below), so it has no output
	// schema; everything else carries its reflected schema, which also
	// serves MCP exposition later (§7.3).
	var outSchema *Schema
	if outType := reflect.TypeOf(new(Out)).Elem(); outType != reflect.TypeOf("") {
		outSchema = schemaFor(outType)
	}
	return &ToolDef{
		Name:         name,
		Description:  description,
		InputSchema:  schemaFor(inType),
		OutputSchema: outSchema,
		sourceFile:   file,
		sourceLine:   line,
		invoke: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in In
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("%w: tool %q: %v", ErrInvalidToolInput, name, err)
			}
			out, err := fn(ctx, in)
			if err != nil {
				return "", err
			}
			if s, ok := any(out).(string); ok {
				return s, nil
			}
			b, err := json.Marshal(out)
			if err != nil {
				return "", fmt.Errorf("marshal output of tool %q: %w", name, err)
			}
			return string(b), nil
		},
	}
}

// RawTool defines a tool from an explicit schema instead of reflection —
// for tools defined outside Go source: plugin manifests, MCP remotes,
// gateways. fn receives the model's raw JSON arguments verbatim: no
// unmarshalling, no ErrInvalidToolInput — validation belongs to fn. A
// nil schema becomes the empty object schema, so the tool accepts any
// object input. Panics match Tool: empty name, nil fn. The manifest
// records no source line (the definition is not in Go source).
func RawTool(name, description string, schema *Schema, fn func(ctx context.Context, args json.RawMessage) (string, error)) *ToolDef {
	if name == "" {
		panic("weft: RawTool called with an empty name")
	}
	if fn == nil {
		panic(fmt.Sprintf("weft: RawTool %q called with a nil handler", name))
	}
	if schema == nil {
		schema = &Schema{Type: "object"}
	}
	return &ToolDef{
		Name:        name,
		Description: description,
		InputSchema: schema,
		invoke:      fn,
	}
}

// Invoke runs the tool with raw JSON arguments: it unmarshals into the
// handler's input type, calls the handler, and returns the result text the
// model will see (a string output verbatim, anything else JSON-encoded). A
// decode failure returns an error wrapping ErrInvalidToolInput.
func (t *ToolDef) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	return t.invoke(ctx, args)
}

// apply registers the tool as a New option. Duplicate names panic at
// construction time — fail loud, fail early.
func (t *ToolDef) apply(a *Agent) {
	if t == nil {
		return
	}
	if _, dup := a.tools[t.Name]; dup {
		panic(fmt.Sprintf("weft: duplicate tool name %q", t.Name))
	}
	a.tools[t.Name] = t
	a.toolList = append(a.toolList, t)
}

// Call identifies the tool invocation a handler is serving. Retrieve it
// with CallFromContext — for audit logs, per-call idempotency keys, or
// progress reporting that must name its call.
type Call struct {
	RunID  string // the run this call belongs to
	Step   int    // zero-based index of the step that requested it
	CallID string // the provider's call identifier
	Name   string // the tool name
}

type callKey struct{}

// CallFromContext returns the Call a tool handler is serving. ok is false
// when ctx did not come from the agent loop (a direct Invoke, for example).
func CallFromContext(ctx context.Context) (c Call, ok bool) {
	c, ok = ctx.Value(callKey{}).(Call)
	return c, ok
}

func withCall(ctx context.Context, c Call) context.Context {
	return context.WithValue(ctx, callKey{}, c)
}
