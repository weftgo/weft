package mcp

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
)

// TestRoundTripIsLossless is checkable property 3, tested the only way
// it can be: a weft tool exported through AddTools to an in-memory MCP
// server, imported back through Tools, must keep its schema document
// and answer identically — export and import are one shape, and the
// bridge between them loses nothing (ADR 0003's bet, measured).
func TestRoundTripIsLossless(t *testing.T) {
	tool := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
			Verbose bool   `json:"verbose,omitempty"`
		}) (map[string]string, error) {
			return map[string]string{"id": in.OrderID, "status": "shipped"}, nil
		})
	raw := weft.RawTool("parse_invoice", "Parse an invoice.",
		mustParseSchema(t, `{"type":"object","properties":{"uri":{"type":"string","pattern":"^https://"}},"required":["uri"]}`),
		func(_ context.Context, args json.RawMessage) (string, error) {
			return "parsed " + string(args), nil
		})

	srv := sdk.NewServer(&sdk.Implementation{Name: "roundtrip", Version: "0"}, nil)
	AddTools(srv, tool, raw)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "roundtrip-client", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*weft.ToolDef{}
	for _, t := range imported {
		byName[t.Name] = t
	}

	for _, pair := range [][]*weft.ToolDef{{tool, byName["lookup_order"]}, {raw, byName["parse_invoice"]}} {
		orig, back := pair[0], pair[1]
		if back == nil {
			t.Fatalf("%s was not imported", orig.Name)
		}
		// The schema document survives: the imported bytes are the
		// exported document in canonical key order.
		a, err := json.Marshal(orig.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(back.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var av, bv any
		if err := json.Unmarshal(a, &av); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &bv); err != nil {
			t.Fatal(err)
		}
		if !equalJSON(av, bv) {
			t.Errorf("%s: schema drifted across the bridge\n was  %s\n now  %s", orig.Name, a, b)
		}
		// And the call answers identically.
		args := json.RawMessage(`{"order_id":"1234","extra":"ignored"}`)
		if orig.Name == "parse_invoice" {
			args = json.RawMessage(`{"uri":"https://x/i.pdf"}`)
		}
		want, err := orig.Invoke(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		got, err := back.Invoke(context.Background(), args)
		if err != nil {
			t.Fatalf("%s: imported call failed: %v", orig.Name, err)
		}
		if got != want {
			t.Errorf("%s: result drifted\n was  %s\n now  %s", orig.Name, want, got)
		}
	}
}

func mustParseSchema(t *testing.T, doc string) *weft.Schema {
	t.Helper()
	s, err := weft.ParseSchema(json.RawMessage(doc))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
