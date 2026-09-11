package weft_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// A tool is a plain function; the input schema is derived from the struct.
func ExampleTool() {
	type WeatherInput struct {
		City string `json:"city" jsonschema:"the city to look up"`
		Days *int   `json:"days,omitempty"`
	}
	getWeather := weft.Tool("get_weather", "Get a forecast.",
		func(_ context.Context, in WeatherInput) (string, error) {
			return "sunny in " + in.City, nil
		})

	b, err := json.MarshalIndent(getWeather.InputSchema, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(getWeather.Name)
	fmt.Println(string(b))
	// Output:
	// get_weather
	// {
	//   "type": "object",
	//   "properties": {
	//     "city": {
	//       "type": "string",
	//       "description": "the city to look up"
	//     },
	//     "days": {
	//       "type": "integer"
	//     }
	//   },
	//   "required": [
	//     "city"
	//   ]
	// }
}

func ExampleAgent_generate() {
	echo := weft.Tool("echo", "Echo a message.",
		func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return "echo: " + in.Msg, nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hello"}`}),
		wefttest.Say("I echoed your message."),
	)
	agt := weft.New(model, weft.Instructions("You echo things."), echo)

	res, err := agt.Generate(context.Background(), weft.Prompt("Echo hello."))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	fmt.Println("steps:", res.NumSteps(), "tokens:", res.Usage.Total())
	// Output:
	// I echoed your message.
	// steps: 2 tokens: 30
}

// Provider reasoning round-trips: it is preserved in the transcript,
// placed before the text of the same assistant turn.
// A prompt can mix text and files with UserParts; the core carries the
// bytes and the adapter maps them to the provider's image block.
func ExampleUserParts() {
	msg := weft.UserParts(
		weft.TextPart{Text: "What is this?"},
		weft.FilePart{MediaType: "image/png", URL: "https://example.com/cat.png"},
	)
	b, err := json.Marshal(msg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(b))
	// Output:
	// {"role":"user","content":[{"type":"text","text":"What is this?"},{"type":"file","media_type":"image/png","url":"https://example.com/cat.png"}]}
}

// A partial transcript (the run was interrupted mid-step) becomes valid
// model input: the missing result is synthesised, visibly.
func ExampleRepair() {
	msgs := []weft.Message{
		weft.User("Where is order 1234?"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "c1", Name: "lookup_order", Args: json.RawMessage(`{"order_id":"1234"}`)},
		}},
	}
	for _, m := range weft.Repair(msgs) {
		fmt.Println(m.Role)
	}
	// Output:
	// user
	// assistant
	// tool
}

// The manifest is generated output: one committed, diffable description
// of every agent and tool. Gate it with a golden test so it cannot go
// stale.
func ExampleManifest() {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Name("support-bot"),
		weft.Instructions("You are a support agent."),
		weft.Tool("refund_order", "Refund a customer's order.",
			func(_ context.Context, _ struct {
				OrderID string `json:"order_id"`
			}) (string, error) {
				return "refunded", nil
			}),
	)
	b, err := weft.Manifest(agt)
	if err != nil {
		log.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	fmt.Println(lines[0])
	fmt.Println(lines[1])
	// Output:
	// {
	//   "weft": 1,
}

// Recorded event streams decode back into typed events: store writes
// them, the Inspector replays them.
func ExampleUnmarshalEvent() {
	ev, err := weft.UnmarshalEvent([]byte(`{"type":"tool_start","seq":5,"call_id":"c1","name":"echo","args":{"m":"x"}}`))
	if err != nil {
		log.Fatal(err)
	}
	start := ev.(weft.ToolStart)
	fmt.Println(start.Name, start.CallID, start.Seq, string(start.Args))
	// Output:
	// echo c1 5 {"m":"x"}
}

func ExampleReasoningPart() {
	model := wefttest.Script(
		wefttest.Think("The user greets; reply in kind.", wefttest.Say("Hello!")),
	)
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("Hi."))
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range res.Messages[1].Content {
		fmt.Printf("%T\n", p)
	}
	// Output:
	// weft.ReasoningPart
	// weft.TextPart
}

// Tap observes every event of every run — including Generate, which has
// no stream to range over. Taps see; the middleware seams change.
func ExampleTap() {
	var calls int
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "roll_dice"}),
			wefttest.Say("rolled"),
		),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.ToolStart); ok {
				calls++
			}
		}),
		weft.Tool("roll_dice", "Roll a die.",
			func(_ context.Context, _ struct{}) (int, error) { return 4, nil }),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("Roll.")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("tool calls:", calls)
	// Output:
	// tool calls: 1
}

func ExampleAgent_stream() {
	roll := weft.Tool("roll_dice", "Roll a six-sided die.",
		func(_ context.Context, _ struct{}) (int, error) {
			return 4, nil // deterministic for the example
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "roll_dice"}),
		wefttest.Say("You rolled a 4!"),
	)
	agt := weft.New(model, roll)

	for ev, err := range agt.Stream(context.Background(), weft.Prompt("Roll a die.")).Events() {
		if err != nil {
			log.Fatal(err)
		}
		switch ev := ev.(type) {
		case weft.ToolStart:
			fmt.Println("tool:", ev.Name)
		case weft.TextDelta:
			fmt.Println("text:", ev.Text)
		case weft.RunFinish:
			fmt.Println("done in", ev.Steps, "steps")
		}
	}
	// Output:
	// tool: roll_dice
	// text: You rolled a 4!
	// done in 2 steps
}

// Structured output: Output constrains the final answer to a struct, and
// GenerateAs returns it decoded. An invalid submission is an ordinary
// tool error the model repairs; a valid one ends the run.
func ExampleGenerateAs() {
	type Verdict struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason" jsonschema:"one sentence"`
	}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"approved":true,"reason":"within policy"}`}),
	)
	agt := weft.New(model, weft.Instructions("Review refund requests."), weft.Output[Verdict]())

	v, res, err := weft.GenerateAs[Verdict](context.Background(), agt, weft.Prompt("Refund order 42?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(v.Approved, v.Reason)
	fmt.Println("steps:", res.NumSteps())
	// Output:
	// true within policy
	// steps: 1
}

// Per-tool policy: trailing options on Tool override the agent's
// defaults for that tool alone. Here a slow tool times out into an error
// result the model sees, and the run carries on.
func ExampleTimeout() {
	slow := weft.Tool("slow", "Takes a while.",
		func(ctx context.Context, _ struct{}) (string, error) {
			<-ctx.Done() // a well-behaved handler honours the deadline
			return "", ctx.Err()
		},
		weft.Timeout(10*time.Millisecond))
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("It did not answer in time."),
	)
	agt := weft.New(model, slow)

	res, err := agt.Generate(context.Background(), weft.Prompt("Try the slow tool."))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	fmt.Println(res.Text())
	// Output:
	// tool "slow" timed out after 10ms
	// It did not answer in time.
}
