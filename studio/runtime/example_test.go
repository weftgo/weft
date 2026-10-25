package runtime_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/weftgo/weft/studio/runtime"
)

// ExampleRuntimeServer mounts the runtime link's three routes — the
// block a Studio with Playground(true) serves — and answers one
// registration.
func ExampleRuntimeServer() {
	rs := runtime.New()
	mux := http.NewServeMux()
	rs.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reg := `{"runtime_id":"rt_demo","host":"laptop","pid":1,"service":"acme-api","env":"dev",
	         "weft_version":"v0.6.0",
	         "budget":{"max_tokens_per_experiment":200000,"max_runs_per_experiment":60},
	         "threads":false,
	         "agents":[{"name":"acme-support","manifest":"{}","models":["glm-5.3-flash"],
	                    "limits":{"max_steps":10,"parallelism":4},
	                    "side_effects":{"lookup_order":"never"},
	                    "allow":["lookup_order"]}]}`
	resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(reg))
	if err != nil {
		fmt.Println("register:", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	fmt.Println("register:", resp.StatusCode)

	views := rs.Snapshot()
	fmt.Println("runtimes:", len(views), views[0].ID, views[0].Agents[0].Name)
	// Output:
	// register: 200
	// runtimes: 1 rt_demo acme-support
}

// ExampleReasonOf reads a failed scripted command's error — weft/runtime's
// miss, wrapped by the run error — into the structured why and the one
// sentence both Studio surfaces render.
func ExampleReasonOf() {
	r := runtime.ReasonOf("weft: run failed at step 0: model stream: weft/runtime: no recorded turn for step 2 " +
		"(scripted engine, key 700b66abcf4561ab): the tool list differs from the recording [tools]; " +
		"the scripted engine answers only what the source recorded: re-run on engine live")
	fmt.Println(r.Kind, r.Step, r.Components)
	fmt.Println(r.Message)
	fmt.Println(runtime.ReasonOf("weft: run failed at step 0: boom") == nil)
	// Output:
	// scripted_miss 2 [tools]
	// scripted has no recorded answer for step 2: the tool list differs from the recording — re-run on engine live
	// true
}

// ExampleScriptedMissReason is the same sentence for a miss predicted
// before Run (the preview's scripted_key_match).
func ExampleScriptedMissReason() {
	fmt.Println(runtime.ScriptedMissReason(0, []string{"thinking"}).Message)
	// Output:
	// scripted has no recorded answer for step 0: the request sets a thinking level the recording does not hold — re-run on engine live
}

// ExampleAgentRegistration_ManifestModelProvider reads the provider of
// a registered agent's own model: "wefttest" says engine live calls the
// app's test script, not a provider (GET /api/runtimes' model_provider,
// UX-C-09).
func ExampleAgentRegistration_ManifestModelProvider() {
	a := runtime.AgentRegistration{Name: "director",
		Manifest: `{"weft":1,"agents":[{"name":"director","model":{"provider":"wefttest","name":"script"}}]}`}
	fmt.Println(a.ManifestModelProvider(), a.ManifestModelName())
	// Output: wefttest script
}
