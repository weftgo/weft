package wefttest_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// Raw scripts the model's events verbatim — here a signed reasoning
// block, the shape providers like Anthropic attach a signature to and
// Think cannot express (it scripts unsigned reasoning only).
func ExampleRaw() {
	model := wefttest.Script(
		wefttest.Raw(
			weft.ModelReasoningDelta{Text: "checking the order", Signature: "sig1"},
			weft.ModelTextDelta{Text: "Order 42 arrived broken."},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		),
	)
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			fmt.Println("stream error:", err)
			return
		}
		switch e := ev.(type) {
		case weft.ModelReasoningDelta:
			fmt.Printf("reasoning %q (signed: %v)\n", e.Text, e.Signature != "")
		case weft.ModelTextDelta:
			fmt.Printf("text %q\n", e.Text)
		case weft.ModelFinish:
			fmt.Printf("finish %v\n", e.Reason)
		}
	}
	// Output:
	// reasoning "checking the order" (signed: true)
	// text "Order 42 arrived broken."
	// finish stop
}

type lookupIn struct {
	OrderID int `json:"order_id"`
}

// Args marshals a typed value into a Call's raw-JSON arguments, so the
// scripted call and the tool's input struct cannot drift apart.
func ExampleArgs() {
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "lookup_order", Args: wefttest.Args(lookupIn{OrderID: 42})},
		),
	)
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			fmt.Println("stream error:", err)
			return
		}
		if call, ok := ev.(weft.ModelToolCall); ok {
			fmt.Printf("%s %s\n", call.Name, call.Args)
		}
	}
	// Output: lookup_order {"order_id":42}
}

// Replay answers an agent from what a real model actually said, offline
// and deterministically. The suite holds the recording switch — Record
// spends provider money, so the decision belongs to the suite that
// holds the key, never to a flag wefttest reads. This example shows the
// shape only: Replay and Record take the *testing.T (for the fixture
// directory's name), which an example has no equivalent of.
func ExampleReplay() {
	// The suite's model(t), one line at the call site:
	//
	//	func model(t *testing.T) weft.Model {
	//	    if os.Getenv("WEFT_RECORD") != "" { // the suite's own switch
	//	        return wefttest.Record(t, "testdata/replay", myAdapter)
	//	    }
	//	    return wefttest.Replay(t, "testdata/replay")
	//	}
	//
	//	agt := weft.New(model(t), tools...)
	//	res, err := agt.Generate(context.Background(), weft.Prompt("Refund order 1234."))
	//	fmt.Println(res.Text(), err)
	//
	// A request the recording does not answer fails loudly with
	// wefttest.ErrNoFixture naming the directory and the first user
	// text — re-record with WEFT_RECORD once, commit the fixtures, and
	// every later run replays without a key or a network.
}

// SayThenFail scripts a mid-stream failure: the caller sees the partial
// text, then the stream errors — the shape the loop's partial-turn
// handling and mw.Retry are tested with.
func ExampleSayThenFail() {
	model := wefttest.Script(
		wefttest.SayThenFail("half an answ", errors.New("provider disconnected")),
	)
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			fmt.Println("stream error:", err)
			return
		}
		if d, ok := ev.(weft.ModelTextDelta); ok {
			fmt.Printf("saw %q\n", d.Text)
		}
	}
	// Output:
	// saw "half an answ"
	// stream error: provider disconnected
}

// WithUsage rewrites a scripted turn's finish usage, so a UsageLimit
// overshoot is scripted without arithmetic on the fixed defaults.
func ExampleTurn_WithUsage() {
	model := wefttest.Script(
		wefttest.Say("done").WithUsage(weft.Usage{InputTokens: 900, OutputTokens: 40}),
	)
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			fmt.Println("stream error:", err)
			return
		}
		if f, ok := ev.(weft.ModelFinish); ok {
			fmt.Printf("usage in=%d out=%d\n", f.Usage.InputTokens, f.Usage.OutputTokens)
		}
	}
	// Output: usage in=900 out=40
}

var exampleLookup = weft.Tool("lookup_order", "Look up an order by ID.",
	func(_ context.Context, in lookupIn) (int, error) {
		return in.OrderID, nil
	})

// LastRequest is the one-request shorthand for asserting what the agent
// asked: the advertised tools and the opening prompt, without keeping a
// handle on every request. A model with no requests yet returns the
// zero Request, whose matchers report false and empty.
func ExampleModel_LastRequest() {
	model := wefttest.Script(wefttest.Say("Order 42 shipped on Tuesday."))
	res, err := weft.New(model, exampleLookup).Generate(context.Background(), weft.Prompt("Where is order 42?"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(res.Text())
	req := model.LastRequest()
	fmt.Println(req.HasTool("lookup_order"), req.ToolNames(), req.LastText())
	// Output:
	// Order 42 shipped on Tuesday.
	// true [lookup_order] Where is order 42?
}
