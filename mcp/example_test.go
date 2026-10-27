package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mcp"
	"github.com/weftgo/weft/wefttest"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
)

// AddTools registers weft tools on an MCP server: the listing and the
// calls run the tool's own contract — no adapter layer, the tool is
// the exposition.
func ExampleAddTools() {
	echo := weft.Tool("echo", "Echo the text.",
		func(_ context.Context, in struct {
			Text string `json:"text"`
		}) (string, error) {
			return "heard: " + in.Text, nil
		})
	srv := sdk.NewServer(&sdk.Implementation{Name: "demo", Version: "0"}, nil)
	mcp.AddTools(srv, echo)

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "demo-client", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content[0].(*sdk.TextContent).Text)
	// Output: heard: hello
}

// Serve exposes a whole agent as one MCP tool — named after the agent,
// described for routing — plus the agent's tools under its chain.
func ExampleServe() {
	agt := weft.New(wefttest.Script(wefttest.Say("research complete")),
		weft.Name("research"),
		weft.Instructions("You research."),
	)
	srv := sdk.NewServer(&sdk.Implementation{Name: "demo", Version: "0"}, nil)
	mcp.Serve(srv, agt, "Research a topic in depth.")

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "demo-client", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "research",
		Arguments: map[string]any{"prompt": "the history of denim"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content[0].(*sdk.TextContent).Text)
	// Output: research complete
}

// A caller not built on weft links its call to the served run by
// params._meta: the W3C traceparent of its own span (the served run
// joins that trace) and MetaCaller, the run and call it acts for (the
// run records them as weft.caller.run.id / weft.caller.call.id).
// Tools sends both on every call by itself.
func ExampleServe_callerLink() {
	agt := weft.New(wefttest.Script(wefttest.Say("linked")), weft.Name("research"))
	srv := sdk.NewServer(&sdk.Implementation{Name: "demo", Version: "0"}, nil)
	mcp.Serve(srv, agt, "Research a topic in depth.")
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "demo-client", Version: "0"}, nil).
		Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Meta: sdk.Meta{
			"traceparent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			mcp.MetaCaller: map[string]string{"run_id": "orchestrator-run-1", "call_id": "call_7"},
		},
		Name:      "research",
		Arguments: map[string]any{"prompt": "the history of denim"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content[0].(*sdk.TextContent).Text)
	// Output: linked
}

// Tools imports a server's tools as ordinary weft tools: the server's
// schema bytes kept whole, its annotations mapped to per-tool policy,
// and the calls forwarded verbatim.
func ExampleTools() {
	// Stand up a remote server standing in for a real one.
	srv := sdk.NewServer(&sdk.Implementation{Name: "remote", Version: "0"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        "search",
		Description: "Search the archive.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "3 hits"}}}, nil
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "demo", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}

	tools, err := mcp.Tools(context.Background(), sess, mcp.Prefix("arch_"))
	if err != nil {
		panic(err)
	}
	out, err := tools[0].Invoke(context.Background(), []byte(`{"q":"denim"}`))
	if err != nil {
		panic(err)
	}
	fmt.Println(tools[0].Name, "->", out)
	// Output: arch_search -> 3 hits
}

// ExampleTools_toolSource is the listChanged pattern: the SDK's
// handler re-runs Tools into a slice the agent serves through
// weft.ToolSource — no registry, no owned goroutine; the list is the
// caller's value and the loop refetches it every step.
func ExampleTools_toolSource() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "remote", Version: "0"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        "search",
		Description: "Search.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "demo", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}

	// The SDK runs ToolListChangedHandler on its own goroutine and the
	// loop reads the source on the run's, so the slice lives under a
	// mutex: refresh writes it, the source reads it.
	var (
		mu      sync.Mutex
		current []*weft.ToolDef
	)
	refresh := func() {
		tools, err := mcp.Tools(context.Background(), sess)
		if err == nil {
			mu.Lock()
			current = tools
			mu.Unlock()
		}
	}
	refresh()
	// A real client sets ClientOptions.ToolListChangedHandler to call
	// refresh; the served list is whatever the last refresh produced.
	agt := weft.New(wefttest.Script(wefttest.Say("ready")),
		weft.Name("importer"),
		weft.ToolSource(func() []*weft.ToolDef {
			mu.Lock()
			defer mu.Unlock()
			return current
		}))
	res, err := agt.Generate(context.Background(), weft.Prompt("status?"))
	if err != nil {
		panic(err)
	}
	mu.Lock()
	n := len(current)
	mu.Unlock()
	fmt.Println(res.Text(), "tools fetched:", n)
	// Output: ready tools fetched: 1
}

// toolsRecord is ExampleServer's LoggerProvider: it keeps the tools
// record's body (the catalog each run records, ADR 0028 §5).
type toolsRecord struct {
	embedded.LoggerProvider
	mu   sync.Mutex
	body string
}

type toolsLogger struct {
	embedded.Logger
	p *toolsRecord
}

func (p *toolsRecord) Logger(string, ...log.LoggerOption) log.Logger    { return toolsLogger{p: p} }
func (toolsLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }
func (l toolsLogger) Emit(_ context.Context, r log.Record) {
	if r.EventName() == "weft.tools" {
		l.p.mu.Lock()
		l.p.body = r.Body().AsString()
		l.p.mu.Unlock()
	}
}

// Server names the server an import comes from, for the record: each
// imported tool's source is "mcp:<name>" — the node Studio's call
// graph draws, each call's weft.tool.origin — and its source_detail
// says the server, the transport, the prefix and its name on the
// server. Nothing the model sees changes.
func ExampleServer() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "billing-service", Version: "1"}, nil)
	srv.AddTool(&sdk.Tool{
		Name: "get_invoice", Description: "Status of an invoice.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "unpaid"}}}, nil
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "front-desk", Version: "1"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}
	tools, err := mcp.Tools(context.Background(), sess,
		mcp.Server("billing"), mcp.Transport("memory"), mcp.Prefix("svc_"))
	if err != nil {
		panic(err)
	}

	rec := &toolsRecord{}
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.LoggerProvider(rec), weft.Content(true), tools[0])
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		panic(err)
	}
	var body struct {
		Tools []struct {
			Name         string            `json:"name"`
			Source       string            `json:"source"`
			SourceDetail map[string]string `json:"source_detail"`
		} `json:"tools"`
	}
	rec.mu.Lock()
	_ = json.Unmarshal([]byte(rec.body), &body)
	rec.mu.Unlock()
	t := body.Tools[0]
	fmt.Println(t.Name, t.Source, t.SourceDetail["transport"], t.SourceDetail["remote_name"])
	// Output: svc_get_invoice mcp:billing memory get_invoice
}

// ImportError.Option: a server whose every tool was skipped imports
// nothing, so the agent records the skips itself — the tools record's
// skipped[] names them under the server's origin.
func ExampleImportError_Option() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "ledger", Version: "1"}, nil)
	odd := &sdk.Tool{Name: "balance", Description: "Balance.", InputSchema: json.RawMessage(`{"type":"object"}`)}
	srv.AddTool(odd, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return nil, nil })
	odd.InputSchema = json.RawMessage(`{"type":["object","null"]}`) // a root the core cannot key on
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "app", Version: "1"}, nil).
		Connect(context.Background(), clientTransport, nil)
	if err != nil {
		panic(err)
	}
	tools, err := mcp.Tools(context.Background(), sess, mcp.Server("ledger"))
	opts := []weft.Option{weft.Tool("lookup", "Look up.", func(context.Context, struct{}) (string, error) { return "", nil })}
	var skipped *mcp.ImportError
	if errors.As(err, &skipped) {
		opts = append(opts, skipped.Option())
	}
	fmt.Println(len(tools), "imported")

	rec := &toolsRecord{}
	opts = append(opts, weft.LoggerProvider(rec), weft.Content(true))
	if _, err := weft.New(wefttest.Script(wefttest.Say("ok")), opts...).Generate(context.Background(), weft.Prompt("x")); err != nil {
		panic(err)
	}
	var body struct {
		Skipped []struct{ Name, Source string } `json:"skipped"`
	}
	rec.mu.Lock()
	_ = json.Unmarshal([]byte(rec.body), &body)
	rec.mu.Unlock()
	fmt.Println("skipped:", body.Skipped[0].Name, "from", body.Skipped[0].Source)
	// Output:
	// 0 imported
	// skipped: balance from mcp:ledger
}
