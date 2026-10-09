package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/runtime"
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
