package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// served builds an SDK server plus a connected client session over
// in-memory transports, with a stop function that severs the
// connection (the transport-failure case). servedOpts passes
// ServerOptions through — the pagination test pages the list.
func served(t *testing.T, configure func(*sdk.Server)) (*sdk.ClientSession, func()) {
	return servedOpts(t, nil, configure)
}

func servedOpts(t *testing.T, opts *sdk.ServerOptions, configure func(*sdk.Server)) (*sdk.ClientSession, func()) {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "remote", Version: "0"}, opts)
	configure(srv)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	runCtx, stop := context.WithCancel(context.Background())
	go func() { _ = srv.Run(runCtx, serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "importer", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return sess, stop
}

// foreignSchema carries an enum and a oneOf the core Schema type
// cannot express — the bytes must survive the import whole.
const foreignSchema = `{"type":"object","properties":{"q":{"type":"string","enum":["a","b"]},"pick":{"oneOf":[{"type":"string"},{"type":"number"}]}},"required":["q"]}`

func addRemoteTool(srv *sdk.Server, name string, readOnly bool, h sdk.ToolHandler) {
	srv.AddTool(&sdk.Tool{
		Name:        name,
		Description: "a remote tool",
		InputSchema: json.RawMessage(foreignSchema),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly},
	}, h)
}

// C1: every listed tool is imported with the server's name,
// description and input schema bytes verbatim — an enum and a oneOf
// the core cannot express reach the model whole — and the output
// schema when present.
func TestToolsKeepsForeignSchemaWhole(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "search", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "found"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("imported %d tools, want 1", len(tools))
	}
	got := tools[0]
	if got.Name != "search" || got.Description != "a remote tool" {
		t.Errorf("name/description = %q/%q", got.Name, got.Description)
	}
	// The SDK's client hands us the schema as decoded JSON, so the kept
	// bytes are the document in encoding/json's canonical key order —
	// every keyword the server sent, nothing degraded (P6 is about
	// what the model sees, and it sees this consistently).
	b, err := json.Marshal(got.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var gotSchema, wantSchema any
	if err := json.Unmarshal(b, &gotSchema); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(foreignSchema), &wantSchema); err != nil {
		t.Fatal(err)
	}
	if !equalJSON(gotSchema, wantSchema) {
		t.Errorf("schema:\n got  %s\n want %s", b, foreignSchema)
	}

	// The same bytes reach the model: a scripted run's recorded
	// request carries the foreign schema unchanged.
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "search", Args: `{"q":"a"}`}),
		wefttest.Say("done"),
	)
	agt := weft.New(model, weft.Name("parent"), got)
	if _, err := agt.Generate(context.Background(), weft.Prompt("search")); err != nil {
		t.Fatal(err)
	}
	reqSchema, err := json.Marshal(model.Requests()[0].Tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(reqSchema, &gotDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(foreignSchema), &wantDoc); err != nil {
		t.Fatal(err)
	}
	if !equalJSON(gotDoc, wantDoc) {
		t.Errorf("model saw:\n %s\nwant %s", reqSchema, foreignSchema)
	}
}

func equalJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// Review 2026-09-24 §2.5: a server listing a tool with an empty name
// is untrusted input (the SDK's server-side name check only logs, and
// its list filter drops nil tools but not empty names); Tools fails
// loudly instead of panicking inside RawTool.
func TestToolsEmptyNameIsALoudError(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "x"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err == nil {
		t.Fatalf("import of an empty-named tool succeeded: %v", tools)
	}
	if !strings.Contains(err.Error(), "empty name") {
		t.Errorf("error = %q, want it to name the empty name", err.Error())
	}
}

// C1: Tools follows nextCursor across pages. PageSize: 1 splits three
// tools into three server-side pages; the raw ListTools call pins that
// the first page really is partial before Tools is asked to see past
// its cursor and import the whole list.
func TestToolsListsEveryPage(t *testing.T) {
	sess, stop := servedOpts(t, &sdk.ServerOptions{PageSize: 1}, func(srv *sdk.Server) {
		for _, name := range []string{"alpha", "beta", "gamma"} {
			addRemoteTool(srv, name, true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
			})
		}
	})
	defer stop()
	first, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tools) != 1 || first.NextCursor == "" {
		t.Fatalf("page 1 = %d tools, cursor %q; want a paged list to follow", len(first.Tools), first.NextCursor)
	}
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Errorf("imported %d tools, want all 3 across pages: %+v", len(tools), tools)
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !got[name] {
			t.Errorf("tool %q missing from the import", name)
		}
	}
}

// C2: the model's raw argument bytes cross to tools/call verbatim —
// no re-encoding, no key reordering.
func TestToolsCallPassesArgsVerbatim(t *testing.T) {
	var mu sync.Mutex
	var seen []byte
	sess, stop := served(t, func(srv *sdk.Server) {
		srv.AddTool(&sdk.Tool{
			Name:        "echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			mu.Lock()
			seen = append(seen[:0], req.Params.Arguments...)
			mu.Unlock()
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"z":1,"a":[2,3],"nested":{"k":"v"}}`)
	if _, err := tools[0].Invoke(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if string(seen) != string(args) {
		t.Errorf("server saw %s\nwant %s", seen, args)
	}
}

// C3: a remote isError result is a tool error whose text is the
// server's, verbatim, with the sentinel reachable for middleware.
func TestToolsRemoteErrorIsData(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "fail", false, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{IsError: true,
				Content: []sdk.Content{&sdk.TextContent{Text: "rate limited: try again in 30s"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools[0].Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("remote isError must be an error")
	}
	if err.Error() != "rate limited: try again in 30s" {
		t.Errorf("text = %q, want the server's verbatim", err)
	}
	if !errors.Is(err, ErrToolError) {
		t.Errorf("errors.Is(ErrToolError) = false")
	}
}

// C4: a transport failure is a tool error result ("mcp: <err>"),
// never a run error. The call carries its own deadline, the way a real
// run's tool Timeout would bound it: an unbounded call after the server
// side is severed can deadlock — the SDK's Close waits for the
// in-flight handler, the handler waits for a request ctx that cancels
// only when the close finishes, so the client must be the one to give
// up (jsonrpc2 closes the stream after, not before, the in-flight
// wait).
func TestToolsTransportFailureIsData(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "hang", false, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
	})
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	stop() // sever the connection under the running tool
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = tools[0].Invoke(ctx, json.RawMessage(`{}`))
	if err == nil || !strings.HasPrefix(err.Error(), "mcp: ") {
		t.Fatalf("transport failure = %v, want an mcp:-prefixed tool error", err)
	}
}

// C5 + C6: policy applies — Timeout bounds the remote call like any
// tool, and RequireApproval parks it at the run boundary.
func TestToolsAppliesPolicy(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "slow", false, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "finally"}}}, nil
			}
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess, Policy(weft.Timeout(100*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}

	// Timeout is run policy: through the loop, the capped result is an
	// error result the model sees.
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("gave up"),
	)
	agt := weft.New(model, weft.Name("parent"), tools[0])
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Messages[len(res.Messages)-2].Content[0].(weft.ToolResultPart).Content, `tool "slow" timed out after`) {
		t.Errorf("timeout result = %+v", res.Messages[len(res.Messages)-2].Content[0])
	}

	// RequireApproval parks the imported call exactly like a local one.
	tools, err = Tools(context.Background(), sess, Policy(weft.RequireApproval()))
	if err != nil {
		t.Fatal(err)
	}
	model2 := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "slow"}))
	agt2 := weft.New(model2, weft.Name("parent2"), tools[0])
	res2, err := agt2.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Pending) != 1 || res2.Pending[0].Name != "slow" {
		t.Errorf("pending = %+v, want the imported call parked", res2.Pending)
	}
}

// C6, the rest: MaxResultBytes and WrapTools ride through Policy and
// apply through the loop like Timeout and RequireApproval above — the
// cap is run policy (Agent.CallTool returns uncapped output), and the
// tool-level middleware sits inside the agent's chain.
func TestToolsAppliesPolicyRunRules(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "babbler", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", 500)}}}, nil
		})
	})
	defer stop()
	sawCall := make(chan weft.ToolCallPart, 1)
	tools, err := Tools(context.Background(), sess, Policy(
		weft.MaxResultBytes(8),
		weft.WrapTools(func(next weft.ToolCaller) weft.ToolCaller {
			return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
				sawCall <- call
				return next(ctx, call)
			}
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "babbler"}),
		wefttest.Say("enough"),
	)
	agt := weft.New(model, weft.Name("parent"), tools[0])
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-sawCall:
		if call.Name != "babbler" {
			t.Errorf("middleware saw %q", call.Name)
		}
	default:
		t.Error("WrapTools middleware never saw the imported call")
	}
	part := res.Messages[len(res.Messages)-2].Content[0].(weft.ToolResultPart)
	if want := "xxxxxxxx\n…[truncated 492 bytes]"; part.Content != want {
		t.Errorf("capped result = %q, want %q", part.Content, want)
	}
}

// C7: an imported tool without readOnlyHint is Sequential; one with
// it is not. The manifest records the per-tool policy, so this is
// visible without racing two calls.
func TestToolsAnnotationsSetSequential(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "writes", false, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "wrote"}}}, nil
		})
		addRemoteTool(srv, "reads", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "read"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	// The manifest records per-tool policy, so the annotation rule is
	// visible without racing two calls: the un-hinted tool is
	// sequential, the readOnlyHint one is not.
	var doc struct {
		Agents []struct {
			Tools []struct {
				Name       string `json:"name"`
				Sequential bool   `json:"sequential"`
			} `json:"tools"`
		} `json:"agents"`
	}
	agt := weft.New(wefttest.Script(), weft.Name("p"), tools[0], tools[1])
	if b, err := weft.Manifest(agt); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, tool := range doc.Agents[0].Tools {
		byName[tool.Name] = tool.Sequential
	}
	if !byName["writes"] {
		t.Error("un-hinted tool is not Sequential")
	}
	if byName["reads"] {
		t.Error("readOnlyHint tool must stay parallel")
	}
}

// C8: Prefix renames the tool for the model while the remote call
// uses the server's name.
func TestToolsPrefix(t *testing.T) {
	var mu sync.Mutex
	called := ""
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "search", true, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			mu.Lock()
			called = req.Params.Name
			mu.Unlock()
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess, Prefix("gh_"))
	if err != nil {
		t.Fatal(err)
	}
	if tools[0].Name != "gh_search" {
		t.Fatalf("imported name = %q, want gh_search", tools[0].Name)
	}
	if _, err := tools[0].Invoke(context.Background(), json.RawMessage(`{"q":"a"}`)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if called != "search" {
		t.Errorf("remote call used %q, want the server's own name", called)
	}
}

// C9: a ctx that ends before or during the listing fails Tools with
// the ctx error and no tools.
func TestToolsRespectsDeadline(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "search", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
		})
	})
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if tools, err := Tools(ctx, sess); err == nil || tools != nil {
		t.Errorf("cancelled ctx: tools = %v, err = %v; want the ctx error and nothing imported", tools, err)
	}
}

// C10: the manifest lists an imported tool with its name, description
// and raw schema — and no source, the RawTool rule.
func TestImportedToolInManifest(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "search", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	agt := weft.New(wefttest.Script(), weft.Name("p"), tools[0])
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if !strings.Contains(doc, `"name": "search"`) || !strings.Contains(doc, `"enum"`) || !strings.Contains(doc, `"oneOf"`) {
		t.Errorf("manifest missing the imported tool or its foreign schema:\n%s", doc)
	}
	if strings.Contains(doc, `"source"`) {
		t.Errorf("imported tool carries a source line:\n%s", doc)
	}
}

// C11: imported tools are ordinary — the loop dispatches them, a
// ToolSource serves a refreshed list, and mw.Allow's denial is a
// result the model sees, never a run error.
func TestImportedToolsAreOrdinary(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "search", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "found it"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}

	// Through the loop, under Parallelism, with a denying middleware.
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "search", Args: `{"q":"a"}`}),
		wefttest.Say("alright"),
	)
	agt := weft.New(model, weft.Name("p"), weft.Parallelism(4),
		weft.WrapTools(func(next weft.ToolCaller) weft.ToolCaller {
			return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
				if call.Name == "search" {
					return "", errors.New("searching is disabled today")
				}
				return next(ctx, call)
			}
		}),
		tools[0])
	res, err := agt.Generate(context.Background(), weft.Prompt("search"))
	if err != nil {
		t.Fatal(err)
	}
	toolMsg := res.Messages[len(res.Messages)-2]
	part := toolMsg.Content[0].(weft.ToolResultPart)
	if !part.IsError || part.Content != "searching is disabled today" {
		t.Errorf("denial = %+v, want an error result the model sees", part)
	}

	// Through a ToolSource: the refreshed slice is the step's snapshot.
	fetched := 0
	src := weft.New(wefttest.Script(wefttest.Say("idle")), weft.Name("src"),
		weft.ToolSource(func() []*weft.ToolDef {
			fetched++
			return tools
		}))
	if _, err := src.Generate(context.Background(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if fetched == 0 {
		t.Error("ToolSource never fetched the imported list")
	}
}

// The rendering table (ADR 0015): structuredContent's JSON wins, text
// items join with newlines, non-text items become one-line markers,
// and an isError result renders the same way as the error's text.
func TestRenderContent(t *testing.T) {
	if got := renderContent(&sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: `{"a":1}`}},
		StructuredContent: map[string]any{"a": 1},
	}); got != `{"a":1}` {
		t.Errorf("structured wins: %q", got)
	}
	if got := renderContent(&sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: "one"}, &sdk.TextContent{Text: "two"}},
	}); got != "one\ntwo" {
		t.Errorf("text join: %q", got)
	}
	if got := renderContent(&sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 9)}, // Data is the decoded payload
			&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///tmp/x"}},
			&sdk.ResourceLink{URI: "file:///tmp/y", Name: "y"},
		},
	}); got != "[image image/png, 9 bytes]\n[resource file:///tmp/x]\n[resource file:///tmp/y]" {
		t.Errorf("markers: %q", got)
	}
	if got := renderContent(&sdk.CallToolResult{}); got != "" {
		t.Errorf("empty: %q", got)
	}
}

// An isError result with no content at all still gives the model a
// sentence: the sentinel's own text, under the sentinel.
func TestToolsEmptyRemoteErrorHasText(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "mute", true, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{IsError: true}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools[0].Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || err.Error() != "mcp: tool returned an error" || !errors.Is(err, ErrToolError) {
		t.Errorf("empty isError = %v, want the sentinel's text under the sentinel", err)
	}
}

// The size marker counts the payload's own bytes on both sides of the
// wire: the SDK base64-encodes Data on the way out and decodes it on
// the way in, so a 12000-byte image renders as 12000, never as the
// base64 text's length or a DecodedLen of already-decoded bytes.
func TestRenderContentSizeSurvivesTheWire(t *testing.T) {
	res := &sdk.CallToolResult{Content: []sdk.Content{
		&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 12000)},
		&sdk.AudioContent{MIMEType: "audio/wav", Data: make([]byte, 7)},
	}}
	wire, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back sdk.CallToolResult
	if err := json.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	want := "[image image/png, 12000 bytes]\n[audio audio/wav, 7 bytes]"
	if got := renderContent(&back); got != want {
		t.Errorf("after the wire: %q, want %q", got, want)
	}
	if got := renderContent(res); got != want {
		t.Errorf("before the wire: %q, want %q", got, want)
	}
}

// C1, the shapes real servers send: a zod-built TypeScript server
// emits "additionalProperties": false, nullable fields as a type
// array, and $schema on every tool. None of those fit the core's
// Schema fields; all must import (the structured view is lenient) and
// cross to the model whole. Before this pin one such tool failed the
// whole import.
func TestToolsImportsZodStyleSchemas(t *testing.T) {
	const zod = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"path":{"type":"string"},"limit":{"type":["integer","null"],"minimum":1}},"required":["path"],"additionalProperties":false}`
	sess, stop := served(t, func(srv *sdk.Server) {
		srv.AddTool(&sdk.Tool{Name: "read_file", Description: "Read a file.", InputSchema: json.RawMessage(zod)},
			func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "contents"}}}, nil
			})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "read_file" {
		t.Fatalf("imported %+v", tools)
	}
	b, err := json.Marshal(tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(zod), &want); err != nil {
		t.Fatal(err)
	}
	if !equalJSON(got, want) {
		t.Errorf("schema:\n got  %s\n want %s", b, zod)
	}
	out, err := tools[0].Invoke(context.Background(), json.RawMessage(`{"path":"/x"}`))
	if err != nil || out != "contents" {
		t.Errorf("call = %q, %v", out, err)
	}
}

// Edge row: a server with zero tools imports zero tools — an empty,
// non-nil slice, not an error. (A non-object input schema cannot be
// served by the SDK to test the import's refusal — its server panics
// on AddTool first — so that path is unit-pinned by the core's
// TestParseSchemaRejects, and Tools wraps it naming the tool.)
func TestToolsEdges(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {})
	defer stop()
	tools, err := Tools(context.Background(), sess)
	if err != nil || tools == nil || len(tools) != 0 {
		t.Errorf("zero tools: tools = %v (nil = %t), err = %v", tools, tools == nil, err)
	}
}
