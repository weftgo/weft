package weft_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log"
	"slices"
	"strings"
	"sync/atomic"
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

// Fast by default, think on demand: the agent option sets every run's
// default, a run option overrides it for that run alone. The scripted
// model records what each run asked for.
func ExampleThinking() {
	model := wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok"))
	agt := weft.New(model, weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}))
	deep := weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkHigh, Budget: 2048})
	if _, err := agt.Generate(context.Background(), deep, weft.Prompt("hard")); err != nil {
		log.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), weft.Prompt("quick")); err != nil {
		log.Fatal(err)
	}
	for _, req := range model.Requests() {
		fmt.Println(req.Thinking == weft.ThinkingConfig{Level: weft.ThinkHigh, Budget: 2048},
			req.Thinking.Level == weft.ThinkOff)
	}
	// Output:
	// true false
	// false true
}

// Model middleware wraps the agent's model, chi-style: the first listed
// is the outermost. The reference set lives in package mw.
func ExampleWrapModel() {
	logged := func(next weft.Model) weft.Model {
		return loggingModel{next: next}
	}
	agt := weft.New(wefttest.Script(wefttest.Say("hello")), weft.WrapModel(logged))
	res, err := agt.Generate(context.Background(), weft.Prompt("hi"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	// Output:
	// model call: 1 messages
	// hello
}

type loggingModel struct{ next weft.Model }

func (m loggingModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m loggingModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	fmt.Printf("model call: %d messages\n", len(req.Messages))
	return m.next.Stream(ctx, req)
}

// Tool middleware wraps every call the loop dispatches. A middleware
// that returns an error produces an error result the model sees; a
// *weft.ToolError gives it a code.
func ExampleWrapTools() {
	readOnly := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			if strings.HasPrefix(call.Name, "delete_") {
				return "", &weft.ToolError{Code: "DENIED", Message: "this agent is read-only"}
			}
			return next(ctx, call)
		}
	}
	del := weft.Tool("delete_order", "", func(_ context.Context, _ struct{}) (string, error) { return "deleted", nil })
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "delete_order"}),
		wefttest.Say("I cannot do that."),
	), del, weft.WrapTools(readOnly))
	res, err := agt.Generate(context.Background(), weft.Prompt("delete order 1"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	fmt.Println(res.Text())
	// Output:
	// DENIED: this agent is read-only
	// I cannot do that.
}

// The context-decoration convention: middleware that has verified
// something (here, who is calling) adds it to ctx before next, and the
// handler reads it back through a typed accessor — the same shape as
// weft.CallFromContext. Decorate only with data the middleware has
// verified; derive business values in the handler after decode.
func ExampleWrapTools_context() {
	authUser := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			user, err := verifyUser(ctx) // a session lookup, a token check, ...
			if err != nil {
				return "", &weft.ToolError{Code: "UNAUTHENTICATED", Message: "sign in first", Err: err}
			}
			return next(withUser(ctx, user), call)
		}
	}
	myOrders := weft.Tool("my_orders", "List the caller's orders.",
		func(ctx context.Context, _ struct{}) (string, error) {
			u, ok := userFromContext(ctx)
			if !ok {
				return "", weft.Errorf("UNAUTHENTICATED", "no user on the call")
			}
			return "orders for " + u.Name, nil
		})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "my_orders"}),
		wefttest.Say("Here they are."),
	), myOrders, weft.WrapTools(authUser))
	res, err := agt.Generate(context.Background(), weft.Prompt("show my orders"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// orders for ada
}

type exampleUser struct{ Name string }

type userKey struct{}

func withUser(ctx context.Context, u exampleUser) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

func userFromContext(ctx context.Context) (exampleUser, bool) {
	u, ok := ctx.Value(userKey{}).(exampleUser)
	return u, ok
}

func verifyUser(context.Context) (exampleUser, error) { return exampleUser{Name: "ada"}, nil }

// A *ToolError carries a code the model can branch on and a cause it
// never sees.
func ExampleToolError() {
	lookup := weft.Tool("lookup_order", "", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return "", &weft.ToolError{
			Code:    "ORDER_NOT_FOUND",
			Message: "order " + in.ID + " does not exist",
			Err:     errors.New("pg: no rows in result set"), // for logs and middleware only
		}
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":"42"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":42}`}),
		wefttest.Say("No such order."),
	), lookup)
	res, err := agt.Generate(context.Background(), weft.Prompt("order 42?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	fmt.Println(res.Steps[1].Results[0].Content)
	// Output:
	// ORDER_NOT_FOUND: order 42 does not exist
	// INVALID_INPUT: tool "lookup_order": field "id": expected string, got number
}

// A RequireApproval tool parks its calls: the run ends successfully
// with them on Pending, and a later run resumes with a decision.
func ExampleRequireApproval() {
	refund := weft.Tool("refund", "Refund an order.", func(_ context.Context, in struct {
		Order string `json:"order"`
	}) (string, error) {
		return "refunded " + in.Order, nil
	}, weft.RequireApproval())
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "refund", Args: `{"order":"42"}`}),
		wefttest.Say("Done."),
	), refund)

	res, err := agt.Generate(context.Background(), weft.Prompt("refund order 42"))
	if err != nil {
		log.Fatal(err)
	}
	for _, call := range res.Pending {
		fmt.Printf("awaiting approval: %s %s\n", call.Name, call.Args)
	}

	// Someone decided. Resume with the transcript and the decision.
	res, err = agt.Generate(context.Background(), weft.Messages(res.Messages...), weft.Approve("c1"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	// Output:
	// awaiting approval: refund {"order":"42"}
	// Done.
}

// The loop's coded failures are exported contract strings. A denied
// call renders DENIED whether the approval boundary refused it (here,
// on resume) or mw.Allow did — one vocabulary for the model; the same
// rule carries CodeInvalidInput (ExampleToolError) and
// CodeNoSuchTool (below).
func ExampleCodeDenied() {
	refund := weft.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	}, weft.RequireApproval())
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "refund"}),
		wefttest.Say("I could not refund it."),
	), refund)

	res, err := agt.Generate(context.Background(), weft.Prompt("refund order 42"))
	if err != nil {
		log.Fatal(err)
	}
	res, err = agt.Generate(context.Background(),
		weft.Messages(res.Messages...), weft.Deny("c1", "customer withdrew"))
	if err != nil {
		log.Fatal(err)
	}
	// The denied call's result is the model's to read, in the transcript.
	fmt.Println(res.Messages[len(res.Messages)-2].Content[0].(weft.ToolResultPart).Content)
	// Output:
	// DENIED: customer withdrew
}

// A call naming a tool the agent does not have becomes a coded error
// result the model can self-correct from — data, not a run failure.
func ExampleCodeNoSuchTool() {
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}),
		wefttest.Say("I have no such tool."),
	))
	res, err := agt.Generate(context.Background(), weft.Prompt("refund it"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// NO_SUCH_TOOL: no tool named "refund"
}

// PromptSnippet keeps a tool's usage rules next to the tool; the loop
// appends them to the instructions of every model call.
func ExamplePromptSnippet() {
	search := weft.Tool("search", "Search the docs.", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		weft.PromptSnippet("Cite the search result you used."))
	model := wefttest.Script(wefttest.Say("ok"))
	if _, err := weft.New(model, weft.Instructions("You answer questions."), search).Generate(context.Background(), weft.Prompt("hi")); err != nil {
		log.Fatal(err)
	}
	fmt.Println(model.Requests()[0].System)
	// Output:
	// You answer questions.
	//
	// Cite the search result you used.
}

// A Sequential tool is a barrier: it runs alone. The step's calls in
// flight finish first, and the calls after it wait — under any
// parallelism. Results stay in call order either way.
func ExampleSequential() {
	write := weft.Tool("write", "Append to the ledger.", func(_ context.Context, _ struct{}) (string, error) {
		return "written", nil
	}, weft.Sequential())
	check := weft.Tool("check", "Verify the ledger.", func(_ context.Context, _ struct{}) (string, error) {
		return "verified", nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "check"}, wefttest.Call{Name: "write"}, wefttest.Call{Name: "check"}),
		wefttest.Say("done"),
	)
	res, err := weft.New(model, write, check, weft.Parallelism(4)).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range res.Steps[0].Results {
		fmt.Println(r.Name, "->", r.Content)
	}
	// Output:
	// check -> verified
	// write -> written
	// check -> verified
}

// Slow observers (a database write, a network sink) must not run inside
// a Tap: taps are synchronous and run under the step's event-ordering
// lock, so a slow tap delays every tool event of its step. The pattern:
// hand each event to a queue inside the tap — never block — and drain
// it on your own goroutine, which may be as slow as it likes. A bounded
// queue with a visible drop counter keeps a stuck drain from wedging
// the run.
func ExampleTap_async() {
	events := make(chan weft.Event, 1024)
	var dropped atomic.Int64
	done := make(chan int)
	go func() {
		starts := 0
		for ev := range events {
			if _, ok := ev.(weft.ToolStart); ok {
				starts++
			}
		}
		done <- starts
	}()

	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "ping"}),
			wefttest.Say("done"),
		),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			select {
			case events <- ev: // fast: hand to the belt
			default: // full belt: drop, but visibly
				dropped.Add(1)
			}
		}),
		weft.Tool("ping", "", func(_ context.Context, _ struct{}) (string, error) {
			return "pong", nil
		}),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("hi")); err != nil {
		log.Fatal(err)
	}
	close(events)
	fmt.Println("tool starts:", <-done, "dropped:", dropped.Load())
	// Output:
	// tool starts: 1 dropped: 0
}

// Delegating to another agent: a subagent is a tool whose handler runs
// another agent on the prompt alone. The child's events arrive wrapped
// in Nested; its usage rolls into the parent's total.
func ExampleSubagent() {
	researcher := weft.New(wefttest.Script(
		wefttest.Say("order 1234 shipped yesterday"),
	))
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"where is order 1234?"}`}),
		wefttest.Say("Researched."),
	), weft.Subagent("research", "Research a question in depth.", researcher))
	res, err := agt.Generate(context.Background(), weft.Prompt("Where is order 1234?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	fmt.Println("total tokens:", res.Usage.Total())
	// Output:
	// order 1234 shipped yesterday
	// total tokens: 45
}

// A token budget: exceeded, the run fails with ErrUsageLimit before the
// next model call; the partial transcript rides on RunError.Result.
func ExampleUsageLimit() {
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("never reached"),
	), echo, weft.UsageLimit(weft.Usage{OutputTokens: 4}))
	_, err := agt.Generate(context.Background(), weft.Prompt("again"))
	var re *weft.RunError
	if errors.As(err, &re) {
		fmt.Println(errors.Is(err, weft.ErrUsageLimit), "steps kept:", len(re.Result.Steps))
	}
	// Output:
	// true steps kept: 1
}

// A retry hint: the model sees "RETRY: <hint>", fixes the arguments,
// and the loop continues; a tool that cannot be satisfied fails the run
// after MaxModelRetries consecutive asks.
func ExampleModelRetry() {
	var calls atomic.Int32
	parse := weft.Tool("parse_date", "Parse a date.",
		func(_ context.Context, in struct {
			D string `json:"d" jsonschema:"the date, ISO-8601"`
		}) (string, error) {
			if in.D != "2026-09-19" {
				return "", weft.ModelRetry("date must be ISO-8601, e.g. 2026-09-19")
			}
			_ = calls.Add(1)
			return "2026-09-19", nil
		})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "parse_date", Args: `{"d":"tomorrow"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "parse_date", Args: `{"d":"2026-09-19"}`}),
		wefttest.Say("Parsed."),
	), parse)
	res, err := agt.Generate(context.Background(), weft.Prompt("When is it?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("first attempt:", res.Steps[0].Results[0].Content)
	fmt.Println("second attempt:", res.Steps[1].Results[0].Content)
	// Output:
	// first attempt: RETRY: date must be ISO-8601, e.g. 2026-09-19
	// second attempt: 2026-09-19
}

// A stuck model repeating one request: DetectLoops fails the run
// loudly instead of burning the step budget.
func ExampleDetectLoops() {
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	turns := []wefttest.Turn{}
	for range 3 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"i":1}`}))
	}
	agt := weft.New(wefttest.Script(turns...), echo, weft.DetectLoops(3))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	fmt.Println(errors.Is(err, weft.ErrLoopDetected))
	// Output:
	// true
}

// Phased tool exposure: the first step plans with read-only tools; the
// second acts. PrepareStep is the one loop knob — what it returns is
// what the step both advertises and dispatches against.
func ExamplePrepareStep() {
	lookup := weft.Tool("lookup", "Look up an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "order 1234: broken item", nil
	})
	refund := weft.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	phase := func(_ context.Context, step int, req weft.ModelRequest) (weft.ModelRequest, error) {
		if step == 0 { // investigate before acting
			req.Tools = slices.DeleteFunc(req.Tools, func(t *weft.ToolDef) bool { return t.Name == "refund" })
		}
		return req, nil
	}
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}), // not advertised in step 0
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}), // now it is
		wefttest.Say("Done."),
	), lookup, refund, weft.PrepareStep(phase))
	res, err := agt.Generate(context.Background(), weft.Prompt("Refund order 1234."))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	fmt.Println(res.Steps[1].Results[0].Content)
	// Output:
	// NO_SUCH_TOOL: no tool named "refund"
	// refunded
}

// Composing agents: a plugin is func(deps) weft.Option — a family of
// tools and its policy closed over its dependencies as one value.
// Dependencies are parameters, never globals.
func ExampleOptions() {
	orders := func(deps *string) weft.Option {
		return weft.Options(
			weft.Instructions("You handle orders."),
			weft.Tool("lookup_order", "Look up an order by id.",
				func(_ context.Context, in struct {
					ID string `json:"id" jsonschema:"the order id"`
				}) (string, error) {
					return "order " + in.ID + ": " + *deps, nil
				}),
			weft.MaxResultBytes(1024),
		)
	}
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":"42"}`}),
		wefttest.Say("Done."),
	), orders(new(string)))
	res, err := agt.Generate(context.Background(), weft.Prompt("Where is order 42?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// order 42:
}

// ToolOptions composes tool options into one named value, so a package
// of per-tool policy travels under one name.
func ExampleToolOptions() {
	productPolicy := weft.ToolOptions(
		weft.Timeout(5*time.Second),
		weft.MaxResultBytes(1024),
	)
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":"42"}`}),
		wefttest.Say("Done."),
	), weft.Tool("lookup_order", "Look up an order by id.",
		func(_ context.Context, in struct {
			ID string `json:"id" jsonschema:"the order id"`
		}) (string, error) {
			return "order " + in.ID + " shipped", nil
		}, productPolicy))
	res, err := agt.Generate(context.Background(), weft.Prompt("Where is order 42?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// order 42 shipped
}

// Continuing a conversation: feed the transcript back with the next
// question. The agent value is unchanged — the history lives in the
// messages you pass, never in the agent.
func ExampleAgent_conversation() {
	agt := weft.New(wefttest.Script(
		wefttest.Say("Order 1234? It shipped yesterday."),
		wefttest.Say("Order 5678? Still pending."),
	))
	res, err := agt.Generate(context.Background(), weft.Prompt("Where is order 1234?"))
	if err != nil {
		log.Fatal(err)
	}
	res2, err := agt.Generate(context.Background(),
		weft.Messages(res.Messages...), weft.Prompt("And order 5678?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	fmt.Println(res2.Text())
	// Output:
	// Order 1234? It shipped yesterday.
	// Order 5678? Still pending.
}

// ToolChoice forces a step's tool calls — the router shape: classify
// must be the first call, the rest of the run is unconstrained. One
// PrepareStep function rewrites the request's ToolChoice per step; the
// agent-level option would force every step instead.
func ExampleToolChoice() {
	classify := weft.Tool("classify", "Classify the request.",
		func(_ context.Context, _ struct{}) (string, error) { return "billing", nil })
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "classify"}),
			wefttest.Say("This is a billing question."),
		),
		weft.PrepareStep(func(_ context.Context, step int, req weft.ModelRequest) (weft.ModelRequest, error) {
			if step == 0 {
				req.ToolChoice = weft.ToolChoiceConfig{Mode: weft.ToolChoiceNamed, Name: "classify"}
			}
			return req, nil
		}),
		classify,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("Why did my invoice double?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	// Output:
	// This is a billing question.
}

// Params sets per-step sampling: a PrepareStep function turns the
// temperature down for the classifying step and back for drafting —
// one struct, edited per step, no second Model construction.
func ExampleParams() {
	p := func(f float64) *float64 { return &f }
	classify := weft.Tool("classify", "Classify the request.",
		func(_ context.Context, _ struct{}) (string, error) { return "billing", nil })
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "classify"}),
			wefttest.Say("A billing question, answered at temperature 0.9."),
		),
		weft.Params(weft.RequestParams{Temperature: p(0.9)}),
		weft.PrepareStep(func(_ context.Context, step int, req weft.ModelRequest) (weft.ModelRequest, error) {
			if step == 0 {
				req.Params.Temperature = p(0) // cold for classification
			}
			return req, nil
		}),
		classify,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("Why did my invoice double?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	// Output:
	// A billing question, answered at temperature 0.9.
}

// An OutputDecoder renders structured output while it streams: feed it
// the events you already consume and draw the filling-in form.
func ExampleOutputDecoder() {
	type Form struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	turn := wefttest.Raw(
		weft.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `{"name":"Ada",`},
		weft.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `"count":3}`},
		weft.ModelToolCall{ID: "c1", Name: "submit_output", Args: json.RawMessage(`{"name":"Ada","count":3}`)},
		weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 3, OutputTokens: 2}},
	)
	run := weft.New(wefttest.Script(turn), weft.Output[Form]()).Stream(context.Background(), weft.Prompt("fill the form"))
	dec := weft.NewOutputDecoder[Form]()
	for ev, err := range run.Events() {
		if err != nil {
			log.Fatal(err)
		}
		if p, ok := dec.Feed(ev); ok {
			fmt.Printf("render: name=%q count=%d\n", p.Name, p.Count)
		}
	}
	form, err := dec.Result()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("final:  name=%q count=%d\n", form.Name, form.Count)
	// Output:
	// render: name="Ada" count=0
	// render: name="Ada" count=3
	// final:  name="Ada" count=3
}
