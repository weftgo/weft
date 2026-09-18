package weft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ToolDef is a named, schema-described tool the model can call. Build one
// with the generic Tool constructor; the agent loop (and any manual
// dispatcher) executes it through Invoke.
//
// A ToolDef is mutable until it is registered with New and frozen
// thereafter: New keeps a deep copy, so mutating the value that was
// passed in — or a copy returned by Agent.Tools — never reaches the
// agent or a running run.
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

	// Per-tool policy, set by ToolOptions. Zero values defer to the
	// agent; capSet distinguishes "no per-tool cap" from
	// MaxResultBytes(0), which removes the cap for this tool, and
	// timeoutSet does the same for Timeout(0).
	timeout    time.Duration
	timeoutSet bool
	resultCap  int
	capSet     bool
	strict     bool
	sequential bool         // barrier: runs alone in its step
	approval   bool         // RequireApproval: never runs unapproved
	replay     ReplayPolicy // checkpoint-restart annotation
	snippet    string       // PromptSnippet: composed into the system prompt
	mw         []ToolMiddleware

	// invoke decodes args (rejecting undeclared fields when strict),
	// runs the handler, and renders the result text. RawTool handlers
	// ignore strict: they receive the raw bytes.
	invoke func(ctx context.Context, args json.RawMessage, strict bool) (string, error)
}

// ToolOption configures one tool at definition time. The options that
// make sense at both levels — MaxResultBytes, Timeout, StrictInput — are
// PolicyOptions: on the agent they set the default for every tool, on a
// tool they override that default for the tool alone.
type ToolOption interface {
	applyTool(*ToolDef)
}

// PolicyOption is accepted by both New and Tool. On an agent it is the
// default policy for every tool call; on a tool it overrides the
// agent's default for that tool. The manifest records both levels.
type PolicyOption interface {
	Option
	ToolOption
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
//	    },
//	    weft.Timeout(10*time.Second))
//
// What the model sees as the result is the text of a string Out, and the
// JSON encoding of any other Out. Inside the handler, CallFromContext
// reports which call is running. Trailing options set per-tool policy:
// Timeout, MaxResultBytes, StrictInput.
//
// The handler shape mirrors the official Go MCP SDK's AddTool[In, Out], so
// a weft tool can be exposed over MCP without an adapter layer.
//
// Tool panics if name is empty, fn is nil, or In is not a struct (or a
// pointer to one): providers and MCP require an object at the top level
// of a tool schema, and a scalar there would fail every real call.
func Tool[In, Out any](name, description string, fn func(ctx context.Context, in In) (Out, error), opts ...ToolOption) *ToolDef {
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
	t := &ToolDef{
		Name:         name,
		Description:  description,
		InputSchema:  schemaFor(inType),
		OutputSchema: outSchema,
		sourceFile:   file,
		sourceLine:   line,
		invoke: func(ctx context.Context, args json.RawMessage, strict bool) (string, error) {
			in, err := decodeInput[In](name, args, strict)
			if err != nil {
				return "", err
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
	for _, o := range opts {
		if o != nil {
			o.applyTool(t)
		}
	}
	return t
}

// decodeInput unmarshals the model's arguments into In. Empty or null
// arguments decode as the empty object, so a tool with no required
// fields accepts a bare call. Failures wrap ErrInvalidToolInput and
// name the offending field in the schema's own vocabulary, so the
// model can map the error back to the schema it was shown.
func decodeInput[In any](name string, args json.RawMessage, strict bool) (In, error) {
	var in In
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "null" {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&in); err != nil {
		return in, invalidInput(name, describeDecodeError(err))
	}
	return in, nil
}

// Codes the loop renders its own tool failures with — model-visible
// contract (ADR 0002, "tool errors have codes").
const (
	codeInvalidInput = "INVALID_INPUT"
	codeNoSuchTool   = "NO_SUCH_TOOL"
)

// invalidInput is the ErrInvalidToolInput failure as the model sees it:
// a coded ToolError whose cause is the sentinel, so errors.Is still
// matches and the result reads `INVALID_INPUT: tool "x": field "days":
// expected integer, got string`.
func invalidInput(name, detail string) error {
	return &ToolError{Code: codeInvalidInput, Message: fmt.Sprintf("tool %q: %s", name, detail), Err: ErrInvalidToolInput}
}

// noSuchTool is the ErrNoSuchTool failure, coded the same way.
func noSuchTool(name string) error {
	return &ToolError{Code: codeNoSuchTool, Message: fmt.Sprintf("no tool named %q", name), Err: ErrNoSuchTool}
}

// describeDecodeError renders an encoding/json failure in schema terms:
// the JSON field name, the expected schema type, and what arrived.
func describeDecodeError(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		want := schemaTypeName(typeErr.Type)
		if typeErr.Field == "" {
			return fmt.Sprintf("expected %s at the top level, got %s", want, typeErr.Value)
		}
		return fmt.Sprintf("field %q: expected %s, got %s", typeErr.Field, want, typeErr.Value)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf("invalid JSON at offset %d: %s", syntaxErr.Offset, syntaxErr.Error())
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "invalid JSON: unexpected end of input"
	}
	// encoding/json reports an undeclared field as a plain error:
	// `json: unknown field "name"`.
	if rest, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		if field, uerr := strconv.Unquote(rest); uerr == nil {
			return fmt.Sprintf("unknown field %q: not in the schema", field)
		}
		return fmt.Sprintf("unknown field %s: not in the schema", rest)
	}
	return err.Error()
}

// schemaTypeName maps a Go type to the JSON Schema type name the tool's
// schema advertises for it — the same mapping schemaFor uses.
func schemaTypeName(t reflect.Type) string {
	t = derefType(t)
	if t == nil {
		return "value"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return t.String()
	}
}

// RawTool defines a tool from an explicit schema instead of reflection —
// for tools defined outside Go source: plugin manifests, MCP remotes,
// gateways. fn receives the model's raw JSON arguments verbatim: no
// unmarshalling, no ErrInvalidToolInput — validation belongs to fn, and
// StrictInput has no effect. A nil schema becomes the empty object
// schema, so the tool accepts any object input. Panics match Tool: empty
// name, nil fn. The manifest records no source line (the definition is
// not in Go source). Trailing options set per-tool policy as for Tool.
func RawTool(name, description string, schema *Schema, fn func(ctx context.Context, args json.RawMessage) (string, error), opts ...ToolOption) *ToolDef {
	if name == "" {
		panic("weft: RawTool called with an empty name")
	}
	if fn == nil {
		panic(fmt.Sprintf("weft: RawTool %q called with a nil handler", name))
	}
	if schema == nil {
		schema = &Schema{Type: "object"}
	}
	t := &ToolDef{
		Name:        name,
		Description: description,
		InputSchema: schema,
		invoke: func(ctx context.Context, args json.RawMessage, _ bool) (string, error) {
			return fn(ctx, args)
		},
	}
	for _, o := range opts {
		if o != nil {
			o.applyTool(t)
		}
	}
	return t
}

// Invoke runs the tool with raw JSON arguments: it unmarshals into the
// handler's input type, calls the handler, and returns the result text the
// model will see (a string output verbatim, anything else JSON-encoded). A
// decode failure returns an error wrapping ErrInvalidToolInput. Invoke
// applies the tool's own StrictInput setting; timeouts and result caps
// are run policy, applied by the loop.
func (t *ToolDef) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	return t.invoke(ctx, args, t.strict)
}

// clone returns a deep copy of the definition: the struct, its
// middleware slice, and the schema trees. The invoke closure is shared,
// which is safe — it is a value that captures only the handler and the
// tool's name.
func (t *ToolDef) clone() *ToolDef {
	c := *t
	c.InputSchema = cloneSchema(t.InputSchema)
	c.OutputSchema = cloneSchema(t.OutputSchema)
	c.mw = slices.Clone(t.mw)
	return &c
}

// cloneSchema deep-copies a schema tree: the node, its Required list,
// its Items and AdditionalProperties subtrees, and its Properties map.
func cloneSchema(s *Schema) *Schema {
	if s == nil {
		return nil
	}
	c := *s
	c.Required = slices.Clone(s.Required)
	c.Items = cloneSchema(s.Items)
	c.AdditionalProperties = cloneSchema(s.AdditionalProperties)
	if s.Properties != nil {
		c.Properties = make(map[string]*Schema, len(s.Properties))
		for k, v := range s.Properties {
			c.Properties[k] = cloneSchema(v)
		}
	}
	return &c
}

// apply registers the tool as a New option. Duplicate names panic at
// construction time — fail loud, fail early. The agent stores a deep
// copy, freezing the definition: later mutations of the caller's value
// cannot reach it.
func (t *ToolDef) apply(a *Agent) {
	if t == nil {
		return
	}
	if _, dup := a.tools[t.Name]; dup {
		panic(fmt.Sprintf("weft: duplicate tool name %q", t.Name))
	}
	def := t.clone()
	a.tools[def.Name] = def
	a.toolList = append(a.toolList, def)
}

// Timeout bounds one tool call. On a tool it is that tool's deadline —
// Timeout(0) on a tool removes the agent's default for that tool alone,
// the timeout analogue of MaxResultBytes(0); on the agent it is the
// default for every tool without its own, and non-positive values are
// ignored there. The handler's ctx carries the deadline; when it expires
// the loop records an error result ("tool X timed out after 10s") the
// model sees and moves on, abandoning the handler's goroutine —
// handlers must honour ctx to release their resources. Without any
// Timeout only the run's ctx bounds a call.
func Timeout(d time.Duration) PolicyOption { return timeoutOption{d} }

type timeoutOption struct{ d time.Duration }

func (o timeoutOption) apply(a *Agent) {
	if o.d > 0 {
		a.toolTimeout = o.d
	}
}

func (o timeoutOption) applyTool(t *ToolDef) {
	if o.d >= 0 {
		t.timeout, t.timeoutSet = o.d, true
	}
}

// StrictInput rejects tool arguments that carry fields the input struct
// does not declare, as an ErrInvalidToolInput result naming the field.
// The default is lenient — undeclared fields are ignored, the
// encoding/json default — because models routinely add stray keys and
// a rejection costs a round trip, not accuracy. Use it where an
// ignored field would be a silent misinterpretation of the call. On the
// agent it applies to every tool; on a tool to that tool alone. RawTool
// handlers are unaffected: they receive the raw arguments.
func StrictInput() PolicyOption { return strictOption{} }

type strictOption struct{}

func (strictOption) apply(a *Agent)       { a.strict = true }
func (strictOption) applyTool(t *ToolDef) { t.strict = true }

// ToolCaller is one link of the tool-call chain: it takes a call and
// returns the result text the model will see, or an error. The
// innermost caller decodes the arguments and runs the handler; every
// ToolMiddleware wraps one.
type ToolCaller func(ctx context.Context, call ToolCallPart) (string, error)

// ToolMiddleware wraps a ToolCaller, the chi shape: it may act before
// next (deny, decorate ctx, log), after it (map errors, audit), or
// instead of it. Panic containment stays outside the chain — a panic in
// middleware is still a tool error result, never a run error.
type ToolMiddleware func(next ToolCaller) ToolCaller

type wrapToolsOption []ToolMiddleware

func (o wrapToolsOption) apply(a *Agent) {
	for _, m := range o {
		if m != nil {
			a.toolMW = append(a.toolMW, m)
		}
	}
}

func (o wrapToolsOption) applyTool(t *ToolDef) {
	for _, m := range o {
		if m != nil {
			t.mw = append(t.mw, m)
		}
	}
}

// WrapTools installs tool middleware. On the agent it wraps every tool
// call the loop (and Agent.CallTool) dispatches; on a tool it wraps
// that tool alone, inside the agent's chain: agent middleware → tool
// middleware → decode → handler. The first middleware listed is the
// outermost, so WrapTools(a, b) runs a(b(call)), and successive
// WrapTools options append inward. Middleware sees CallFromContext and
// may decorate ctx for the handler (see ExampleWrapTools_context); it
// returns the result text or an error — a *ToolError to give the
// model a code, an error wrapping ErrApprovalRequired to park the call
// on RunResult.Pending. The reference set is in package mw: Allow,
// Audit, MapErrors. Nil entries are ignored.
func WrapTools(mw ...ToolMiddleware) PolicyOption { return wrapToolsOption(mw) }

// ReplayPolicy annotates what a checkpoint restart may do with a tool
// call that was interrupted mid-flight. The core records it on the
// tool and in the manifest; the store/runtime layers consume it.
type ReplayPolicy string

const (
	// ReplaySafe marks a tool as idempotent: a restart may run an
	// interrupted call again.
	ReplaySafe ReplayPolicy = "safe"
	// ReplayNever marks a tool whose interrupted calls must not be
	// re-run; a restart synthesizes an error result instead.
	ReplayNever ReplayPolicy = "never"
)

type replayOption struct{ policy ReplayPolicy }

func (o replayOption) applyTool(t *ToolDef) { t.replay = o.policy }

// Replay annotates a tool with its checkpoint-restart policy. It changes
// nothing in the loop today: the annotation rides on the manifest for
// the store and runtime layers, which re-run ReplaySafe tools and
// synthesize interrupted results for ReplayNever ones.
func Replay(policy ReplayPolicy) ToolOption { return replayOption{policy} }

// ReplayPolicy reports the tool's Replay annotation; empty when unset.
func (t *ToolDef) ReplayPolicy() ReplayPolicy { return t.replay }

type snippetOption struct{ text string }

func (o snippetOption) applyTool(t *ToolDef) { t.snippet = o.text }

// PromptSnippet attaches system-prompt lines to a tool. The loop appends
// the snippets of the tools it advertises to the agent's Instructions
// for every model call — one paragraph per tool, in registration order,
// separated by blank lines — so usage rules live with the tool instead
// of in a central prompt that drifts as the set grows. Empty snippets
// add nothing. The manifest records the snippet.
func PromptSnippet(text string) ToolOption { return snippetOption{text} }

// PromptSnippet reports the tool's PromptSnippet text; empty when unset.
func (t *ToolDef) PromptSnippet() string { return t.snippet }

type approvalOption struct{}

func (approvalOption) applyTool(t *ToolDef) { t.approval = true }

// RequireApproval marks a tool whose calls never run without a
// decision. When the model calls it the loop executes the step's other
// tools, then ends the run successfully with the call on
// RunResult.Pending (and RunFinish.Pending) and no result in the
// transcript. Resume with the transcript plus Approve or Deny:
//
//	res, _ := agt.Generate(ctx, weft.Prompt("Refund order 42"))
//	for _, call := range res.Pending { /* ask someone */ }
//	res, _ = agt.Generate(ctx, weft.Messages(res.Messages...), weft.Approve(call.ID))
//
// Approved calls run through the ordinary chain with Call.Approved set;
// denied ones (and pending calls given no decision) become error results
// the model sees. This is a policy and UX seam, not a security
// boundary: the boundary is the sandbox a tool runs in.
func RequireApproval() ToolOption { return approvalOption{} }

// Call identifies the tool invocation a handler is serving. Retrieve it
// with CallFromContext — for audit logs, per-call idempotency keys, or
// progress reporting that must name its call.
type Call struct {
	RunID  string // the run this call belongs to
	Step   int    // zero-based index of the step that requested it
	CallID string // the provider's call identifier
	Name   string // the tool name
	// Approved is set when the call was parked by the approval boundary
	// and is now running under an Approve decision — middleware that
	// defers calls with ErrApprovalRequired reads it to let the
	// approved call through.
	Approved bool
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
