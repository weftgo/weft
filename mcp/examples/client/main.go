// Command client consumes MCP servers as weft tools: it connects two
// in-memory servers concurrently under one deadline, imports their
// tools with per-server prefixes, registers them on a scripted agent,
// runs it, and prints the manifest — the imported tools' raw schemas
// show in it. Swap the in-memory transports for real ones (stdio,
// HTTP) and nothing else changes.
//
// The connection fan-in here uses a WaitGroup and a shared deadline
// because this module imports only the core, the SDK, and stdlib; in
// your own code an errgroup with context is the one-liner shape:
//
//	g, ctx := errgroup.WithContext(ctx)
//	// g.Go(func() error { sess, err := client.Connect(ctx, t, nil); ... })
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mcp"
	"github.com/weftgo/weft/wefttest"
)

// remote builds one in-memory MCP server standing in for a real one.
func remote(name, tool string, answer string) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: name, Version: "0"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        tool,
		Description: name + "'s tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: answer}}}, nil
	})
	return srv
}

// connectAll connects every server concurrently under one deadline —
// the bounded-budget rule (Crush's async init): a slow server fails
// the whole start-up loudly rather than hanging it.
func connectAll(ctx context.Context, servers []*sdk.Server) ([]*sdk.ClientSession, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "weft-importer", Version: "0"}, nil)
	sessions := make([]*sdk.ClientSession, len(servers))
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv *sdk.Server) {
			defer wg.Done()
			serverTransport, clientTransport := sdk.NewInMemoryTransports()
			go func() { _ = srv.Run(ctx, serverTransport) }()
			sess, err := client.Connect(ctx, clientTransport, nil)
			sessions[i], errs[i] = sess, err
		}(i, srv)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("server %d: %w", i, err)
		}
	}
	return sessions, nil
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessions, err := connectAll(ctx, []*sdk.Server{
		remote("github", "search", "repo weftgo/weft found"),
		remote("db", "query", "3 rows"),
	})
	if err != nil {
		return err
	}

	// Import with per-server prefixes so the two can share one agent;
	// the remote calls still use the servers' own tool names.
	var tools []*weft.ToolDef
	tools, err = mcp.Tools(ctx, sessions[0], mcp.Prefix("gh_"))
	if err != nil {
		return err
	}
	more, err := mcp.Tools(ctx, sessions[1], mcp.Prefix("db_"))
	if err != nil {
		return err
	}
	tools = append(tools, more...)

	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "gh_search", Args: `{"q":"weft"}`},
			wefttest.Call{Name: "db_query", Args: `{"q":"orders"}`},
		),
		wefttest.Say("both answered"),
	)
	agt := weft.New(model, weft.Name("importer"),
		weft.Instructions("You answer through the imported tools."),
		tools[0], tools[1])
	res, err := agt.Generate(ctx, weft.Prompt("How many orders, and does weft exist?"))
	if err != nil {
		return err
	}
	fmt.Println(res.Text())

	manifest, err := weft.Manifest(agt)
	if err != nil {
		return err
	}
	fmt.Println(string(manifest))
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
