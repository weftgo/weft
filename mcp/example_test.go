package mcp_test

import (
	"context"
	"fmt"

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
