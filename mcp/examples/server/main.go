// Command server exposes a weft agent as an MCP server over stdio —
// the getting-started agent plus its lookup tool, both directions of
// §7 in one process:
//
//	mcp.Serve(srv, agt, "Support agent.")
//
// Point any MCP client (Claude Code, Crush, another weft agent through
// weft/mcp's Tools) at this binary and the agent and its tool appear.
// The model is wefttest's scripted one, so the program runs offline;
// swap it for a provider adapter and the exposition is unchanged.
package main

import (
	"context"
	"log"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mcp"
	"github.com/weftgo/weft/wefttest"
)

func newServer() *sdk.Server {
	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
		}) (map[string]string, error) {
			return map[string]string{"id": in.OrderID, "status": "shipped"}, nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
		wefttest.Say("Order 1234 shipped and is on its way."),
	)
	agt := weft.New(model,
		weft.Name("getting-started"),
		weft.Instructions("You are a support agent."),
		lookup,
	)
	srv := sdk.NewServer(&sdk.Implementation{Name: "weft-getting-started", Version: "0"}, nil)
	mcp.Serve(srv, agt, "Answer where a customer's order is.")
	return srv
}

func main() {
	if err := newServer().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
	log.Println("server exited")
}
