// Command approval shows the approval boundary end to end, offline: a
// tool marked RequireApproval parks its call, the run ends with the
// call on Pending, a person decides, and a second run resumes with the
// transcript plus the decision. Nothing is persisted here — the
// transcript is the only state, so the same two calls work across a
// queue, a database, or an HTTP round trip.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

type RefundInput struct {
	OrderID string  `json:"order_id" jsonschema:"the order to refund"`
	Amount  float64 `json:"amount" jsonschema:"amount in dollars"`
}

func newAgent() *weft.Agent {
	refund := weft.Tool("refund_order", "Refund an order. Requires a human decision.",
		func(_ context.Context, in RefundInput) (string, error) {
			return fmt.Sprintf("refunded $%.2f on order %s", in.Amount, in.OrderID), nil
		},
		weft.RequireApproval(),
	)
	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (map[string]any, error) {
			return map[string]any{"id": in.OrderID, "total": 129.99, "status": "delivered"}, nil
		})

	// A scripted model, so the example runs without a key: it looks the
	// order up and asks for the refund in one step, then wraps up once
	// it sees the refund's result.
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "c1", Name: "lookup_order", Args: `{"order_id":"1234"}`},
			wefttest.Call{ID: "c2", Name: "refund_order", Args: `{"order_id":"1234","amount":129.99}`},
		),
		wefttest.Say("All set — I have processed the refund for order 1234."),
	)
	return weft.New(model,
		weft.Name("refund-desk"),
		weft.Instructions("You are a support agent. Refund when the customer asks."),
		lookup, refund,
	)
}

func main() {
	ctx := context.Background()
	agt := newAgent()

	res, err := agt.Generate(ctx, weft.Prompt("Please refund order 1234 in full."))
	if err != nil {
		log.Fatal(err)
	}
	if len(res.Pending) == 0 {
		fmt.Println(res.Text())
		return
	}

	// The run ended successfully, with work parked. The other tool in
	// the step (lookup_order) already ran; its result is in the
	// transcript.
	var opts []weft.RunOption
	in := bufio.NewReader(os.Stdin)
	for _, call := range res.Pending {
		fmt.Printf("The agent wants to call %s with %s\nApprove? [y/N] ", call.Name, call.Args)
		line, _ := in.ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(line), "y") {
			opts = append(opts, weft.Approve(call.ID))
		} else {
			opts = append(opts, weft.Deny(call.ID, "declined by the operator"))
		}
	}

	// Resume: the transcript plus the decisions. Approved calls run,
	// denied ones become DENIED results, and the model continues.
	res, err = agt.Generate(ctx, append([]weft.RunOption{weft.Messages(res.Messages...)}, opts...)...)
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok && r.Name == "refund_order" {
				fmt.Printf("refund_order → %s\n", r.Content)
			}
		}
	}
	fmt.Println(res.Text())
}

// runSQLAgent parks a query for a human to execute outside the
// process — the Resolve half of the boundary: the operator runs the
// SQL in prod, pastes what happened, and the next model call sees it.
// The handler below would refuse anyway; under Resolve it never runs.
func runSQLAgent() *weft.Agent {
	runSQL := weft.Tool("run_sql", "Run a read-only SQL query. A human executes it.",
		func(_ context.Context, in struct {
			SQL string `json:"sql"`
		}) (string, error) {
			return "", &weft.ToolError{Code: "NOT_EXECUTED", Message: "this tool only runs through a human"}
		},
		weft.RequireApproval(),
	)
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "q1", Name: "run_sql", Args: `{"sql":"SELECT COUNT(*) FROM orders"}`}),
		wefttest.Say("There are 42 orders."),
	)
	return weft.New(model, weft.Name("sql-desk"), runSQL)
}
