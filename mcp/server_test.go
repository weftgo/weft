package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// newSession runs s over one side of an in-memory transport and
// connects a client to the other: the whole 7.2 surface is testable
// in-process, offline by construction.
func newSession(t *testing.T, s *sdk.Server) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = s.Run(context.Background(), serverTransport) }()
	// Nothing to clean up: the test process owns both ends of the
	// transport and drops them together.
	client := sdk.NewClient(&sdk.Implementation{Name: "weft-test", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return sess
}

type order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func lookupTool() *weft.ToolDef {
	return weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
		}) (order, error) {
			if in.OrderID == "boom" {
				return order{}, errors.New("warehouse unreachable")
			}
			return order{ID: in.OrderID, Status: "shipped"}, nil
		})
}

func echoTool() *weft.ToolDef {
	return weft.Tool("echo", "Echo the text.",
		func(_ context.Context, in struct {
			Text string `json:"text"`
		}) (string, error) {
			return "heard: " + in.Text, nil
		})
}

// X1: tools/list shows exactly the contract — name, description,
// input schema, and outputSchema for a non-string Out (a string Out
// has none, ADR 0003).
func TestAddToolsListsTheContract(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, lookupTool(), echoTool())
	sess := newSession(t, s)
	var listed []*sdk.Tool
	for tool, err := range sess.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, tool)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d tools, want 2", len(listed))
	}
	byName := map[string]*sdk.Tool{}
	for _, tool := range listed {
		byName[tool.Name] = tool
	}
	lu := byName["lookup_order"]
	if lu == nil || lu.Description != "Look up an order by ID." {
		t.Fatalf("lookup_order not listed with its description: %+v", lu)
	}
	schema, err := json.Marshal(lu.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schema), `"order_id"`) || !strings.Contains(string(schema), `"the order to look up"`) {
		t.Errorf("input schema = %s", schema)
	}
	out, err := json.Marshal(lu.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"status"`) {
		t.Errorf("output schema = %s, want the order object's fields", out)
	}
	echo := byName["echo"]
	if echo == nil {
		t.Fatal("echo not listed")
	}
	if echo.OutputSchema != nil {
		t.Errorf("echo (a string Out) carries an outputSchema: %+v", echo.OutputSchema)
	}
}

// X2: a call returns the tool's result text as one TextContent, and a
// non-string Out additionally returns structuredContent holding the
// same JSON.
func TestAddToolsCallReturnsText(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, echoTool())
	sess := newSession(t, s)
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != "heard: hello" {
		t.Errorf("text = %q", text)
	}
}

func TestAddToolsStructuredContent(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, lookupTool())
	sess := newSession(t, s)
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "lookup_order",
		Arguments: map[string]any{"order_id": "1234"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	want := `{"id":"1234","status":"shipped"}`
	if text := res.Content[0].(*sdk.TextContent).Text; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(structured) != want {
		t.Errorf("structuredContent = %s, want %s", structured, want)
	}
}

// nonJSONOutputTool is the §2.6 shape: a RawTool with an output schema
// whose handler returns plain text — a breach of the tool's own
// contract that, unanswered, wedges the session.
func nonJSONOutputTool() *weft.ToolDef {
	t := weft.RawTool("badge", "Issue a badge.", nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "badge granted", nil
		})
	t.OutputSchema = &weft.Schema{Type: "object"}
	return t
}

// Review 2026-09-24 §2.6: a schema'd tool whose result text is not
// JSON must answer with an isError result naming the breach. As
// shipped, the CallToolResult carries structuredContent the SDK cannot
// serialize, the reply never goes out, and the client blocks in the
// call forever — a successful call wedging the session. The timeout
// keeps a regression a failure, not a hang.
func TestAddToolsNonJSONOutputIsErrorNotWedge(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, nonJSONOutputTool())
	sess := newSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "badge"})
	if err != nil {
		t.Fatalf("call: %v (the session wedged?)", err)
	}
	if !res.IsError {
		t.Fatalf("want an isError result, got %+v", res)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "must be JSON") {
		t.Errorf("text = %q, want it to name the output-schema breach", text)
	}
}

// The Serve path shares the rule: the chain handler's result goes
// through the same gate, so the agent's exposition cannot wedge either.
func TestServeNonJSONOutputIsErrorNotWedge(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	agt := weft.New(wefttest.Script(wefttest.Say("hi")), weft.Name("badger"), nonJSONOutputTool())
	Serve(s, agt, "Issues badges.")
	sess := newSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "badge"})
	if err != nil {
		t.Fatalf("call: %v (the session wedged?)", err)
	}
	if !res.IsError {
		t.Fatalf("want an isError result, got %+v", res)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "must be JSON") {
		t.Errorf("text = %q, want it to name the output-schema breach", text)
	}
}

// X3: every model-recoverable failure is a tool result with isError,
// carrying weft's pinned text verbatim — the same strings weft's own
// model would see (ADR 0002), now over the wire.
func TestAddToolsErrorsAreResults(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, lookupTool(),
		weft.Tool("panic", "Panics.", func(context.Context, struct{}) (string, error) {
			panic("bang")
		}))
	sess := newSession(t, s)

	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "lookup_order",
		Arguments: map[string]any{"order_id": 42}, // wrong type: INVALID_INPUT
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("undecodable arguments were not an error result: %+v", res)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(got, `INVALID_INPUT: tool "lookup_order"`) || !strings.Contains(got, `field "order_id"`) {
		t.Errorf("INVALID_INPUT text = %q", got)
	}

	res, err = sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "lookup_order",
		Arguments: map[string]any{"order_id": "boom"}, // handler error
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.Content[0].(*sdk.TextContent).Text != "warehouse unreachable" {
		t.Errorf("handler error result = %+v", res)
	}

	res, err = sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "panic", // a panicking tool is an error result, not a dead server
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("panic was not an error result: %+v", res)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; got != `tool "panic" panicked: bang` {
		t.Errorf("panic text = %q", got)
	}
}

// X4: cancelling the request ctx cancels the handler — observed from
// inside the handler, not inferred from the client's failure (a leaked
// handler would still fail the client at its deadline).
func TestAddToolsHonoursContext(t *testing.T) {
	started := make(chan struct{})
	ended := make(chan error, 1)
	blocked := weft.Tool("blocked", "Blocks until cancelled.",
		func(ctx context.Context, _ struct{}) (string, error) {
			close(started)
			<-ctx.Done()
			ended <- ctx.Err()
			return "", ctx.Err()
		})
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, blocked)
	sess := newSession(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "blocked"})
	if err == nil {
		t.Fatal("a cancelled call must fail")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}
	select {
	case err := <-ended:
		if err == nil {
			t.Error("handler ctx ended with a nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler ctx was never cancelled")
	}
}

// mustPanic runs fn and reports when it returns without panicking.
// Each case gets its own recover: a recover deferred inside a loop
// would unwind the whole test at the first panic and skip the rest.
func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: did not panic", name)
		}
	}()
	fn()
}

// X5: registration is loud — nil tool, duplicate name, and an
// approval-gated tool all panic, fail-early like New. Every case runs.
func TestAddToolsRefusesApprovalGated(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	gated := weft.Tool("refund", "Refund.", func(context.Context, struct{}) (string, error) { return "", nil },
		weft.RequireApproval())
	ran := 0
	for name, fn := range map[string]func(){
		"approval-gated": func() { AddTools(s, gated) },
		"nil tool":       func() { AddTools(s, nil) },
		"duplicate":      func() { AddTools(s, echoTool(), echoTool()) },
	} {
		ran++
		mustPanic(t, name, fn)
	}
	if ran != 3 {
		t.Errorf("%d cases ran, want 3", ran)
	}
}

// A client that omits arguments hands a RawTool {} — never "null":
// tools/call promises an object, and the consume side sends {} for
// the same reason. The SDK's own client always sends {}, so the raw
// wire form is driven here.
func TestAddToolsNullArgumentsBecomeObject(t *testing.T) {
	seen := make(chan string, 1)
	raw := weft.RawTool("raw", "Echoes its argument bytes.", nil,
		func(_ context.Context, args json.RawMessage) (string, error) {
			seen <- string(args)
			return "ok", nil
		})
	h := invokeHandler(raw)
	for _, in := range []any{nil, json.RawMessage(nil), json.RawMessage("null")} {
		req := &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: "raw", Arguments: nil}}
		if b, ok := in.(json.RawMessage); ok {
			req.Params.Arguments = b
		}
		if _, err := h(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if got := <-seen; got != "{}" {
			t.Errorf("arguments %v: handler saw %q, want {}", in, got)
		}
	}
}

// Serve's tools honour the outputSchema they advertise: a non-string
// Out answers with structuredContent as well as text, the way
// AddTools does — the spec's MUST for a tool with an output schema.
func TestServeStructuredContent(t *testing.T) {
	agt := weft.New(wefttest.Script(), weft.Name("orders"), lookupTool())
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	Serve(s, agt, "Orders agent.")
	sess := newSession(t, s)
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "lookup_order", Arguments: map[string]any{"order_id": "7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("call failed: %+v", res)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	sc, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if res.StructuredContent == nil || !equalJSON(json.RawMessage(sc), json.RawMessage(text)) {
		t.Errorf("structuredContent = %s, text = %s; want the same JSON", sc, text)
	}
}

func servedAgent(t *testing.T) (*sdk.Server, *weft.Agent) {
	t.Helper()
	touched := make(chan string, 4)
	agt := weft.New(wefttest.Script(wefttest.Say("research done")),
		weft.Name("research"),
		weft.Instructions("You research."),
		weft.Tool("note", "Record a note.", func(_ context.Context, _ struct{}) (string, error) {
			touched <- "note"
			return "noted", nil
		}),
		weft.WrapTools(func(next weft.ToolCaller) weft.ToolCaller {
			return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
				touched <- "mw:" + call.Name
				return next(ctx, call)
			}
		}),
	)
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	Serve(s, agt, "Research a topic in depth.")
	return s, agt
}

// X6a: the agent tool runs the agent and returns its final text.
func TestServeRunsTheAgent(t *testing.T) {
	s, _ := servedAgent(t)
	sess := newSession(t, s)
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "research",
		Arguments: map[string]any{"prompt": "the history of denim"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("agent tool failed: %+v", res)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != "research done" {
		t.Errorf("final text = %q", text)
	}
}

// X6b: the agent's tools are dispatched through the agent's chain —
// WrapTools middleware sees the call — and an approval-gated tool
// answers with the ErrApprovalRequired text instead of running.
func TestServeToolsGoThroughTheChain(t *testing.T) {
	s, agt := servedAgent(t)
	sess := newSession(t, s)

	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: "note"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Content[0].(*sdk.TextContent).Text != "noted" {
		t.Fatalf("note result = %+v", res)
	}

	// A gated tool answers loudly under Serve (where AddTools refuses
	// outright): the approval error text is the result, never a bypass.
	gated := weft.New(wefttest.Script(),
		weft.Name("gated"),
		weft.Tool("refund", "Refund.", func(context.Context, struct{}) (string, error) { return "refunded", nil },
			weft.RequireApproval()),
	)
	s2 := sdk.NewServer(&sdk.Implementation{Name: "srv2", Version: "0"}, nil)
	Serve(s2, gated, "Gated agent.")
	sess2 := newSession(t, s2)
	res, err = sess2.CallTool(context.Background(), &sdk.CallToolParams{Name: "refund"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("gated tool ran over MCP: %+v", res)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(got, "weft: tool call requires approval") || !strings.Contains(got, `tool "refund"`) {
		t.Errorf("approval text = %q", got)
	}
	_ = agt
}

// X6c: an Output agent's tool returns the submitted JSON, not the
// final text (the Subagent rule).
func TestServeOutputAgentReturnsJSON(t *testing.T) {
	type verdict struct {
		Answer string `json:"answer"`
	}
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"answer":"yes"}`}),
		wefttest.Say("submitted"),
	), weft.Name("oracle"), weft.Output[verdict]())
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	Serve(s, agt, "Answer yes or no.")
	sess := newSession(t, s)
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "oracle",
		Arguments: map[string]any{"prompt": "is denim blue?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("agent tool failed: %+v", res)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != `{"answer":"yes"}` {
		t.Errorf("submitted JSON = %q", text)
	}
}

// X7: Serve requires a named agent — the tool is named after it.
func TestServeRequiresName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Serve on an unnamed agent did not panic")
		}
	}()
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	Serve(s, weft.New(wefttest.Script()), "unnamed")
}

// X8: no ToolAnnotations are emitted in v1 — the spec's defaults treat
// an un-annotated tool the way weft wants an unknown tool treated.
func TestAddToolsEmitsNoAnnotations(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "srv", Version: "0"}, nil)
	AddTools(s, echoTool())
	sess := newSession(t, s)
	for tool, err := range sess.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Annotations != nil {
			t.Errorf("tool %s carries annotations %+v", tool.Name, tool.Annotations)
		}
	}
}
