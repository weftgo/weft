package core_test

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

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// A tool is a plain function; the input schema is derived from the struct.
func ExampleTool() {
	type WeatherInput struct {
		City string `json:"city" jsonschema:"the city to look up"`
		Days *int   `json:"days,omitempty"`
	}
	getWeather := core.Tool("get_weather", "Get a forecast.",
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
	echo := core.Tool("echo", "Echo a message.",
		func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return "echo: " + in.Msg, nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hello"}`}),
		wefttest.Say("I echoed your message."),
	)
	agt := core.New(model, core.Instructions("You echo things."), echo)

	res, err := agt.Generate(context.Background(), core.Prompt("Echo hello."))
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
	msg := core.UserParts(
		core.TextPart{Text: "What is this?"},
		core.FilePart{MediaType: "image/png", URL: "https://example.com/cat.png"},
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
	msgs := []core.Message{
		core.User("Where is order 1234?"),
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "c1", Name: "lookup_order", Args: json.RawMessage(`{"order_id":"1234"}`)},
		}},
	}
	for _, m := range core.Repair(msgs) {
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
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.Name("support-bot"),
		core.Instructions("You are a support agent."),
		core.Tool("refund_order", "Refund a customer's order.",
			func(_ context.Context, _ struct {
				OrderID string `json:"order_id"`
			}) (string, error) {
				return "refunded", nil
			}),
	)
	b, err := core.Manifest(agt)
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
	ev, err := core.UnmarshalEvent([]byte(`{"type":"tool_start","seq":5,"call_id":"c1","name":"echo","args":{"m":"x"}}`))
	if err != nil {
		log.Fatal(err)
	}
	start := ev.(core.ToolStart)
	fmt.Println(start.Name, start.CallID, start.Seq, string(start.Args))
	// Output:
	// echo c1 5 {"m":"x"}
}

func ExampleReasoningPart() {
	model := wefttest.Script(
		wefttest.Think("The user greets; reply in kind.", wefttest.Say("Hello!")),
	)
	res, err := core.New(model).Generate(context.Background(), core.Prompt("Hi."))
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range res.Messages[1].Content {
		fmt.Printf("%T\n", p)
	}
	// Output:
	// core.ReasoningPart
	// core.TextPart
}

// Tap observes every event of every run — including Generate, which has
// no stream to range over. Taps see; the middleware seams change.
func ExampleTap() {
	var calls int
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "roll_dice"}),
			wefttest.Say("rolled"),
		),
		core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				calls++
			}
		}),
		core.Tool("roll_dice", "Roll a die.",
			func(_ context.Context, _ struct{}) (int, error) { return 4, nil }),
	)
	if _, err := agt.Generate(context.Background(), core.Prompt("Roll.")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("tool calls:", calls)
	// Output:
	// tool calls: 1
}

func ExampleAgent_stream() {
	roll := core.Tool("roll_dice", "Roll a six-sided die.",
		func(_ context.Context, _ struct{}) (int, error) {
			return 4, nil // deterministic for the example
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "roll_dice"}),
		wefttest.Say("You rolled a 4!"),
	)
	agt := core.New(model, roll)

	for ev, err := range agt.Stream(context.Background(), core.Prompt("Roll a die.")).Events() {
		if err != nil {
			log.Fatal(err)
		}
		switch ev := ev.(type) {
		case core.ToolStart:
			fmt.Println("tool:", ev.Name)
		case core.TextDelta:
			fmt.Println("text:", ev.Text)
		case core.RunFinish:
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
	agt := core.New(model, core.Instructions("Review refund requests."), core.Output[Verdict]())

	v, res, err := core.GenerateAs[Verdict](context.Background(), agt, core.Prompt("Refund order 42?"))
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
	slow := core.Tool("slow", "Takes a while.",
		func(ctx context.Context, _ struct{}) (string, error) {
			<-ctx.Done() // a well-behaved handler honours the deadline
			return "", ctx.Err()
		},
		core.Timeout(10*time.Millisecond))
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("It did not answer in time."),
	)
	agt := core.New(model, slow)

	res, err := agt.Generate(context.Background(), core.Prompt("Try the slow tool."))
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
	agt := core.New(model, core.Thinking(core.ThinkingConfig{Level: core.ThinkOff}))
	deep := core.Thinking(core.ThinkingConfig{Level: core.ThinkHigh, Budget: 2048})
	if _, err := agt.Generate(context.Background(), deep, core.Prompt("hard")); err != nil {
		log.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), core.Prompt("quick")); err != nil {
		log.Fatal(err)
	}
	for _, req := range model.Requests() {
		fmt.Println(req.Thinking == core.ThinkingConfig{Level: core.ThinkHigh, Budget: 2048},
			req.Thinking.Level == core.ThinkOff)
	}
	// Output:
	// true false
	// false true
}

// Model middleware wraps the agent's model, chi-style: the first listed
// is the outermost. The reference set lives in package mw.
func ExampleWrapModel() {
	logged := func(next core.Model) core.Model {
		return loggingModel{next: next}
	}
	agt := core.New(wefttest.Script(wefttest.Say("hello")), core.WrapModel(logged))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	// Output:
	// model call: 1 messages
	// hello
}

type loggingModel struct{ next core.Model }

func (m loggingModel) Info() core.ModelInfo { return core.InfoOf(m.next) }

func (m loggingModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	fmt.Printf("model call: %d messages\n", len(req.Messages))
	return m.next.Stream(ctx, req)
}

// Tool middleware wraps every call the loop dispatches. A middleware
// that returns an error produces an error result the model sees; a
// *core.ToolError gives it a code.
func ExampleWrapTools() {
	readOnly := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			if strings.HasPrefix(call.Name, "delete_") {
				return "", &core.ToolError{Code: "DENIED", Message: "this agent is read-only"}
			}
			return next(ctx, call)
		}
	}
	del := core.Tool("delete_order", "", func(_ context.Context, _ struct{}) (string, error) { return "deleted", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "delete_order"}),
		wefttest.Say("I cannot do that."),
	), del, core.WrapTools(readOnly))
	res, err := agt.Generate(context.Background(), core.Prompt("delete order 1"))
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
// core.CallFromContext. Decorate only with data the middleware has
// verified; derive business values in the handler after decode.
func ExampleWrapTools_context() {
	authUser := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			user, err := verifyUser(ctx) // a session lookup, a token check, ...
			if err != nil {
				return "", &core.ToolError{Code: "UNAUTHENTICATED", Message: "sign in first", Err: err}
			}
			return next(withUser(ctx, user), call)
		}
	}
	myOrders := core.Tool("my_orders", "List the caller's orders.",
		func(ctx context.Context, _ struct{}) (string, error) {
			u, ok := userFromContext(ctx)
			if !ok {
				return "", core.Errorf("UNAUTHENTICATED", "no user on the call")
			}
			return "orders for " + u.Name, nil
		})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "my_orders"}),
		wefttest.Say("Here they are."),
	), myOrders, core.WrapTools(authUser))
	res, err := agt.Generate(context.Background(), core.Prompt("show my orders"))
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
	lookup := core.Tool("lookup_order", "", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return "", &core.ToolError{
			Code:    "ORDER_NOT_FOUND",
			Message: "order " + in.ID + " does not exist",
			Err:     errors.New("pg: no rows in result set"), // for logs and middleware only
		}
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":"42"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":42}`}),
		wefttest.Say("No such order."),
	), lookup)
	res, err := agt.Generate(context.Background(), core.Prompt("order 42?"))
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
	refund := core.Tool("refund", "Refund an order.", func(_ context.Context, in struct {
		Order string `json:"order"`
	}) (string, error) {
		return "refunded " + in.Order, nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "refund", Args: `{"order":"42"}`}),
		wefttest.Say("Done."),
	), refund)

	res, err := agt.Generate(context.Background(), core.Prompt("refund order 42"))
	if err != nil {
		log.Fatal(err)
	}
	for _, call := range res.Pending {
		fmt.Printf("awaiting approval: %s %s\n", call.Name, call.Args)
	}

	// Someone decided. Resume with the transcript and the decision.
	res, err = agt.Generate(context.Background(), core.Messages(res.Messages...), core.Approve("c1"))
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
	refund := core.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "refund"}),
		wefttest.Say("I could not refund it."),
	), refund)

	res, err := agt.Generate(context.Background(), core.Prompt("refund order 42"))
	if err != nil {
		log.Fatal(err)
	}
	res, err = agt.Generate(context.Background(),
		core.Messages(res.Messages...), core.Deny("c1", "customer withdrew"))
	if err != nil {
		log.Fatal(err)
	}
	// The denied call's result is the model's to read, in the transcript.
	fmt.Println(res.Messages[len(res.Messages)-2].Content[0].(core.ToolResultPart).Content)
	// Output:
	// DENIED: customer withdrew
}

// A call naming a tool the agent does not have becomes a coded error
// result the model can self-correct from — data, not a run failure.
func ExampleCodeNoSuchTool() {
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}),
		wefttest.Say("I have no such tool."),
	))
	res, err := agt.Generate(context.Background(), core.Prompt("refund it"))
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
	search := core.Tool("search", "Search the docs.", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.PromptSnippet("Cite the search result you used."))
	model := wefttest.Script(wefttest.Say("ok"))
	if _, err := core.New(model, core.Instructions("You answer questions."), search).Generate(context.Background(), core.Prompt("hi")); err != nil {
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
	write := core.Tool("write", "Append to the ledger.", func(_ context.Context, _ struct{}) (string, error) {
		return "written", nil
	}, core.Sequential())
	check := core.Tool("check", "Verify the ledger.", func(_ context.Context, _ struct{}) (string, error) {
		return "verified", nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "check"}, wefttest.Call{Name: "write"}, wefttest.Call{Name: "check"}),
		wefttest.Say("done"),
	)
	res, err := core.New(model, write, check, core.Parallelism(4)).Generate(context.Background(), core.Prompt("x"))
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
	events := make(chan core.Event, 1024)
	var dropped atomic.Int64
	done := make(chan int)
	go func() {
		starts := 0
		for ev := range events {
			if _, ok := ev.(core.ToolStart); ok {
				starts++
			}
		}
		done <- starts
	}()

	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "ping"}),
			wefttest.Say("done"),
		),
		core.Tap(func(_ context.Context, ev core.Event) {
			select {
			case events <- ev: // fast: hand to the belt
			default: // full belt: drop, but visibly
				dropped.Add(1)
			}
		}),
		core.Tool("ping", "", func(_ context.Context, _ struct{}) (string, error) {
			return "pong", nil
		}),
	)
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
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
	researcher := core.New(wefttest.Script(
		wefttest.Say("order 1234 shipped yesterday"),
	))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"where is order 1234?"}`}),
		wefttest.Say("Researched."),
	), core.Subagent("research", "Research a question in depth.", researcher))
	res, err := agt.Generate(context.Background(), core.Prompt("Where is order 1234?"))
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
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("never reached"),
	), echo, core.UsageLimit(core.Usage{OutputTokens: 4}))
	_, err := agt.Generate(context.Background(), core.Prompt("again"))
	var re *core.RunError
	if errors.As(err, &re) {
		fmt.Println(errors.Is(err, core.ErrUsageLimit), "steps kept:", len(re.Result.Steps))
	}
	// Output:
	// true steps kept: 1
}

// A retry hint: the model sees "RETRY: <hint>", fixes the arguments,
// and the loop continues; a tool that cannot be satisfied fails the run
// after MaxModelRetries consecutive asks.
func ExampleModelRetry() {
	var calls atomic.Int32
	parse := core.Tool("parse_date", "Parse a date.",
		func(_ context.Context, in struct {
			D string `json:"d" jsonschema:"the date, ISO-8601"`
		}) (string, error) {
			if in.D != "2026-09-19" {
				return "", core.ModelRetry("date must be ISO-8601, e.g. 2026-09-19")
			}
			_ = calls.Add(1)
			return "2026-09-19", nil
		})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "parse_date", Args: `{"d":"tomorrow"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "parse_date", Args: `{"d":"2026-09-19"}`}),
		wefttest.Say("Parsed."),
	), parse)
	res, err := agt.Generate(context.Background(), core.Prompt("When is it?"))
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
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	turns := []wefttest.Turn{}
	for range 3 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"i":1}`}))
	}
	agt := core.New(wefttest.Script(turns...), echo, core.DetectLoops(3))
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	fmt.Println(errors.Is(err, core.ErrLoopDetected))
	// Output:
	// true
}

// Phased tool exposure: the first step plans with read-only tools; the
// second acts. PrepareStep is the one loop knob — what it returns is
// what the step both advertises and dispatches against.
func ExamplePrepareStep() {
	lookup := core.Tool("lookup", "Look up an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "order 1234: broken item", nil
	})
	refund := core.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	phase := func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
		if step == 0 { // investigate before acting
			req.Tools = slices.DeleteFunc(req.Tools, func(t *core.ToolDef) bool { return t.Name == "refund" })
		}
		return req, nil
	}
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}), // not advertised in step 0
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}), // now it is
		wefttest.Say("Done."),
	), lookup, refund, core.PrepareStep(phase))
	res, err := agt.Generate(context.Background(), core.Prompt("Refund order 1234."))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	fmt.Println(res.Steps[1].Results[0].Content)
	// Output:
	// NO_SUCH_TOOL: no tool named "refund"
	// refunded
}

// Composing agents: a plugin is func(deps) core.Option — a family of
// tools and its policy closed over its dependencies as one value.
// Dependencies are parameters, never globals.
func ExampleOptions() {
	orders := func(deps *string) core.Option {
		return core.Options(
			core.Instructions("You handle orders."),
			core.Tool("lookup_order", "Look up an order by id.",
				func(_ context.Context, in struct {
					ID string `json:"id" jsonschema:"the order id"`
				}) (string, error) {
					return "order " + in.ID + ": " + *deps, nil
				}),
			core.MaxResultBytes(1024),
		)
	}
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":"42"}`}),
		wefttest.Say("Done."),
	), orders(new(string)))
	res, err := agt.Generate(context.Background(), core.Prompt("Where is order 42?"))
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
	productPolicy := core.ToolOptions(
		core.Timeout(5*time.Second),
		core.MaxResultBytes(1024),
	)
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"id":"42"}`}),
		wefttest.Say("Done."),
	), core.Tool("lookup_order", "Look up an order by id.",
		func(_ context.Context, in struct {
			ID string `json:"id" jsonschema:"the order id"`
		}) (string, error) {
			return "order " + in.ID + " shipped", nil
		}, productPolicy))
	res, err := agt.Generate(context.Background(), core.Prompt("Where is order 42?"))
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
	agt := core.New(wefttest.Script(
		wefttest.Say("Order 1234? It shipped yesterday."),
		wefttest.Say("Order 5678? Still pending."),
	))
	res, err := agt.Generate(context.Background(), core.Prompt("Where is order 1234?"))
	if err != nil {
		log.Fatal(err)
	}
	res2, err := agt.Generate(context.Background(),
		core.Messages(res.Messages...), core.Prompt("And order 5678?"))
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
	classify := core.Tool("classify", "Classify the request.",
		func(_ context.Context, _ struct{}) (string, error) { return "billing", nil })
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "classify"}),
			wefttest.Say("This is a billing question."),
		),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step == 0 {
				req.ToolChoice = core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "classify"}
			}
			return req, nil
		}),
		classify,
	)
	res, err := agt.Generate(context.Background(), core.Prompt("Why did my invoice double?"))
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
	classify := core.Tool("classify", "Classify the request.",
		func(_ context.Context, _ struct{}) (string, error) { return "billing", nil })
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "classify"}),
			wefttest.Say("A billing question, answered at temperature 0.9."),
		),
		core.Params(core.RequestParams{Temperature: p(0.9)}),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step == 0 {
				req.Params.Temperature = p(0) // cold for classification
			}
			return req, nil
		}),
		classify,
	)
	res, err := agt.Generate(context.Background(), core.Prompt("Why did my invoice double?"))
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
		core.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `{"name":"Ada",`},
		core.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `"count":3}`},
		core.ModelToolCall{ID: "c1", Name: "submit_output", Args: json.RawMessage(`{"name":"Ada","count":3}`)},
		core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 3, OutputTokens: 2}},
	)
	run := core.New(wefttest.Script(turn), core.Output[Form]()).Stream(context.Background(), core.Prompt("fill the form"))
	dec := core.NewOutputDecoder[Form]()
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

// OnRunEnd is the outcome observer: unlike Tap it sees the run's end
// even when the run fails, because a failed run emits no event after
// its last delivered one. The store's Record option pairs the two.
func ExampleOnRunEnd() {
	agt := core.New(
		wefttest.Script(wefttest.Fail(errors.New("provider down"))),
		core.OnRunEnd(func(_ context.Context, res *core.RunResult, err error) {
			fmt.Printf("run ended: has id=%v failed=%v steps=%d\n", res.ID != "", err != nil, res.NumSteps())
		}),
	)
	_, _ = agt.Generate(context.Background(), core.Prompt("hi"))
	// Output:
	// run ended: has id=true failed=true steps=0
}

// countingWrapper stands in for any model middleware: it forwards the
// model and declares it with Unwrap, the convention beside Info.
type countingWrapper struct{ next core.Model }

func (w countingWrapper) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return w.next.Stream(ctx, req)
}
func (w countingWrapper) Info() core.ModelInfo { return core.InfoOf(w.next) }
func (w countingWrapper) Unwrap() core.Model   { return w.next }

// Model returns the model as the loop calls it — WrapModel middleware
// included — and Unwrap walks one level into a wrapper, so a caller
// sees the chain without knowing the wrapper types. The session layer
// (thread) reaches the session agent's own model this way to summarize
// with it (ADR 0020 §2).
func ExampleAgent_Model() {
	base := wefttest.Script(wefttest.Say("hi"))
	agt := core.New(base, core.WrapModel(func(next core.Model) core.Model {
		return countingWrapper{next}
	}))
	fmt.Println(core.InfoOf(agt.Model()))
	fmt.Println(core.InfoOf(core.Unwrap(agt.Model())))
	fmt.Println(core.Unwrap(base) == nil)
	// Output:
	// {wefttest script}
	// {wefttest script}
	// true
}

// Steering delivers a user's message to a running turn at a safe
// point: after the tool batch (every call paired with its result), or
// at what would have been the final step, which the steer redirects
// into one more step. The delivered message is ordinary transcript —
// the model, the record, and the next turn all see it — reported as a
// Steered event between StepFinish and the next StepStart (ADR 0019).
func ExampleSteering() {
	lookup := core.Tool("lookup", "Look up an order.",
		func(_ context.Context, _ struct{}) (string, error) { return "shipped yesterday", nil })
	src := wefttest.NewSteers().At(0, core.User("That is order 1234 — I meant 5678."))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup"}),
		wefttest.Say("Order 5678 is still pending."),
	), lookup)
	res, err := agt.Generate(context.Background(), core.Prompt("Where is my order?"), src.Option())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.NumSteps(), "steps")
	fmt.Println(res.Text())
	last := res.Messages[len(res.Messages)-2] // the steer, an ordinary user message
	fmt.Println(last.Role, last.Text())
	// Output:
	// 2 steps
	// Order 5678 is still pending.
	// user That is order 1234 — I meant 5678.
}

// Metadata attaches caller key/value pairs to one run: every span and
// record of the run carries them, and a Subagent's child run inherits
// them through the context. Keys under "weft." are the weft modules'
// namespace — thread stamps weft.session.id this way.
func ExampleMetadata() {
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); !ok {
				return
			}
			md := core.MetadataFromContext(ctx)
			fmt.Println("tenant =", md["tenant"], "session =", md["weft.session.id"])
		}))
	_, _ = agt.Generate(context.Background(),
		core.Metadata(map[string]string{
			"tenant":          "acme",
			"weft.session.id": "s_01",
		}),
		core.Prompt("hello"))
	// Output:
	// tenant = acme session = s_01
}

// StripContent empties every content field of an event — what a
// content-off destination receives. Identity survives; content does not.
func ExampleStripContent() {
	ev := core.ToolFinish{RunID: "r", Seq: 3, CallID: "c1", Name: "lookup", Content: `{"status":"shipped"}`}
	b, _ := json.Marshal(core.StripContent(ev))
	fmt.Println(string(b))
	// Output:
	// {"type":"tool_finish","run_id":"r","seq":3,"call_id":"c1","name":"lookup","content":"","is_error":false}
}

// The playground's per-run configuration (WEFT-PLAYGROUND §10.1): one
// run of an immutable agent, changed without rebuilding it. OnlyTools
// narrows to registered tools; UseModel swaps in an allowed alternate.
func ExampleOnlyTools() {
	lookup := core.Tool("lookup", "Look up an order.", func(ctx context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return `{"status":"shipped"}`, nil
	})
	agt := core.New(wefttest.Script(wefttest.Say("order shipped")),
		core.Name("support"), lookup)
	_, _ = agt.Generate(context.Background(),
		core.Prompt("where is order 4411?"),
		core.OnlyTools("lookup"),                       // narrowing; unknown name → ErrInvalidRunOption
		core.Instructions("Answer in one short line."), // this run's prompt
	)
	// Output:
}

// ParkOn parks the named tool's calls at the approval boundary, exactly
// as RequireApproval would — the breakpoint that reaches a run without
// touching the immutable agent. Approve resumes it.
func ExampleParkOn() {
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.Say("refunded"),
	), refund)
	res, err := agt.Generate(context.Background(),
		core.Prompt("refund order 4411"), core.ParkOn("refund"))
	if err != nil {
		return
	}
	fmt.Println("pending:", len(res.Pending))
	// A human decides; the next run resumes:
	_, _ = agt.Generate(context.Background(),
		core.Messages(res.Messages...), core.Approve("c1"))
	// Output:
	// pending: 1
}

// ParkAllExcept is the default-deny park rule: the caller names what may
// run, and every other tool call parks — including a tool only a
// ToolSource supplies, which no list built from Agent.Tools could name.
func ExampleParkAllExcept() {
	lookup := core.Tool("lookup", "Look up an order.", func(context.Context, struct{}) (string, error) {
		return "shipped", nil
	})
	wire := core.Tool("wire_money", "Send a payment.", func(context.Context, struct{}) (string, error) {
		return "sent", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "lookup", ID: "c1"},
			wefttest.Call{Name: "wire_money", ID: "c2"},
		),
		wefttest.Say("paid"),
	), core.ToolSource(func() []*core.ToolDef { return []*core.ToolDef{lookup, wire} }))
	res, err := agt.Generate(context.Background(),
		core.Prompt("pay invoice 4411"), core.ParkAllExcept("lookup"))
	if err != nil {
		return
	}
	fmt.Println("ran:", res.Steps[0].Results[0].Name)
	fmt.Println("pending:", res.Pending[0].Name)
	// A human decides; the next run resumes under the same rule:
	_, _ = agt.Generate(context.Background(),
		core.Messages(res.Messages...), core.Approve("c2"), core.ParkAllExcept("lookup"))
	// Output:
	// ran: lookup
	// pending: wire_money
}

// Replay declares a tool's side-effect class for re-runs: safe vouches
// the call is idempotent (a re-run may execute it for real); every
// unannotated tool counts as never — substituted or parked, never
// silently re-fired (WEFT-PLAYGROUND.md §6 rule 3).
func ExampleReplay() {
	lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	refund := core.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	fmt.Println("lookup:", lookup.ReplayPolicy())
	fmt.Println("refund:", refund.ReplayPolicy())
	// Output:
	// lookup: safe
	// refund: never
}
