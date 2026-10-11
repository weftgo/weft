package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// ExampleInstall is the whole integration: one deferred call in main.
// Enabled(false) keeps the example inert — in a real dev binary the
// link opens because WEFT_ENV=dev (or Enabled(true)).
func ExampleInstall() {
	support := weft.New(
		wefttest.Script(wefttest.Say("Your order shipped yesterday.")),
		weft.Name("acme-support"),
	)
	glmFlash := wefttest.Script(wefttest.Say("…"))
	shutdown := runtime.Install(
		runtime.Studio("http://127.0.0.1:7331", ""),
		runtime.Agents(support),
		runtime.Models(map[string]weft.Model{"glm-5.3-flash": glmFlash}),
		runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000, MaxRunsPerExperiment: 60}),
		runtime.AllowSideEffects("send_email"), // for real only under side_effects "allow"
		runtime.Enabled(false),                 // dev-only by default: WEFT_ENV=dev
	)
	defer shutdown()
	fmt.Println("link:", "closed")
	// Output: link: closed
}

// ExampleModelResolver lets the playground try a model the app never
// listed in runtime.Models: a command's provider-qualified name reaches
// the resolver before its ack, and the app decides whether it exists —
// it builds the client from its own credentials (here a scripted model
// stands in for anthropic.New) and refuses everything else. The error
// text is the rejected command's reason, so it names no key or URL.
func ExampleModelResolver() {
	support := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("acme-support"))
	resolve := func(ctx context.Context, name string) (weft.Model, error) {
		provider, model, ok := strings.Cut(name, "/")
		if !ok || provider != "anthropic" || !strings.HasPrefix(model, "claude-") {
			return nil, errors.New("this app serves anthropic/claude-* models only")
		}
		return wefttest.Script(wefttest.Say("…")), nil // anthropic.New(anthropic.Model(model)) in a real app
	}
	shutdown := runtime.Install(
		runtime.Agents(support),
		runtime.ModelResolver(resolve),
		runtime.Enabled(false), // dev-only by default: WEFT_ENV=dev
	)
	defer shutdown()
	_, err := resolve(context.Background(), "openai/gpt-9")
	fmt.Println(err)
	// Output: this app serves anthropic/claude-* models only
}

// ExampleSessions makes the app's own turns targets for the debugger:
// approvals, steer and cancel on a turn of a session the app created
// with thread.PublicID reach the runtime, which acts through the
// Session the resolver answers. Here the app hands its own live
// Session (no release: the app keeps writing it); an app that keeps
// none in memory opens one from its store and returns its Close —
// the runtime claims the writer lease before it acks, so while the
// app's own writer holds the session the command is refused "the app
// is writing this session; fork instead" and nothing is written.
func ExampleSessions() {
	ctx := context.Background()
	support := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("acme-support"))
	live := map[string]*thread.Session{} // the app's open sessions, by public id
	s, _ := thread.Create(ctx, thread.Memory(), support, thread.PublicID("pub_7Hk2"))
	defer func() { _ = s.Close(ctx) }()
	live["pub_7Hk2"] = s

	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		if s, ok := live[publicID]; ok {
			return s, nil, nil
		}
		return nil, nil, nil // not this app's: refused "no session for this public id"
	}
	shutdown := runtime.Install(
		runtime.Agents(support),
		runtime.Sessions(resolve),
		runtime.Enabled(false), // dev-only by default: WEFT_ENV=dev
	)
	defer shutdown()
	got, _, _ := resolve(ctx, "pub_7Hk2")
	fmt.Println(got == s)
	// Output: true
}

// ExampleBreakpoints builds the agent with runtime.Breakpoints() so the
// debugger's breakpoints park tools on the app's own runs too — here
// inert: with no link open (Enabled(false)) there is no breakpoint set,
// and the run is exactly what it would be without the option.
func ExampleBreakpoints() {
	refund := weft.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	})
	support := weft.New(
		wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("Refunded.")),
		weft.Name("acme-support"),
		refund,
		runtime.Breakpoints(), // the debugger's breakpoints reach this agent's own runs
	)
	shutdown := runtime.Install(runtime.Agents(support), runtime.Enabled(false))
	defer shutdown()
	res, err := support.Generate(context.Background(), weft.Prompt("Refund order 4411."))
	if err != nil {
		return
	}
	// With a breakpoint on refund, res.Pending would hold the call: a
	// thread turn is decided from the panel (runtime.Sessions), a plain
	// Generate by the app — Generate again with weft.Approve(id).
	fmt.Println(res.Text(), len(res.Pending))
	// Output: Refunded. 0
}
