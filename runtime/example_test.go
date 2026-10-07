package runtime_test

import (
	"fmt"

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
