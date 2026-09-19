package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mcp"
	"github.com/weftgo/weft/wefttest"
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
