// Command getting-started runs a complete weft agent offline: a scripted
// model (from wefttest), one tool, and the full event stream. Swap the
// wefttest model for a real provider adapter and nothing else changes.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// newSupportAgent builds the example's agent. manifest_test.go builds
// the same value, so the committed weft.json can never drift from the
// code.
func newSupportAgent() *weft.Agent {
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

	return weft.New(model,
		weft.Name("getting-started"),
		weft.Instructions("You are a support agent."),
		lookup,
	)
}

func main() {
	agt := newSupportAgent()

	for ev, err := range agt.Stream(context.Background(), weft.Prompt("Where is order 1234?")).Events() {
		if err != nil {
			log.Fatal(err)
		}
		switch ev := ev.(type) {
		case weft.ToolStart:
			fmt.Printf("→ %s(%s)\n", ev.Name, ev.Args)
		case weft.TextDelta:
			fmt.Print(ev.Text)
		case weft.RunFinish:
			fmt.Printf("\n✓ %d steps, %d tokens\n", ev.Steps, ev.Usage.Total())
		}
	}
}
