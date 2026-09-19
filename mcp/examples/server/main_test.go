package main

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The example program is exercised end to end over in-memory
// transports: the same server main builds, connected the way a foreign
// client would connect it. Both the agent tool and its own tool are
// called.
func TestServerServesAgentAndTools(t *testing.T) {
	srv := newServer()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The agent itself, as one tool.
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "getting-started",
		Arguments: map[string]any{"prompt": "Where is order 1234?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("agent tool: %+v", res)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; got != "Order 1234 shipped and is on its way." {
		t.Errorf("agent answer = %q", got)
	}

	// Its tool, dispatched through the agent's chain.
	res, err = sess.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "lookup_order",
		Arguments: map[string]any{"order_id": "5678"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("lookup_order: %+v", res)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(res.Content[0].(*sdk.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out["id"] != "5678" || out["status"] != "shipped" {
		t.Errorf("lookup result = %v", out)
	}
}
