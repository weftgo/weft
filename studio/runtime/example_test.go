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
		"the scripted engine answers only what the source recorded: run on live")
	fmt.Println(r.Kind, r.Step, r.Components)
	fmt.Println(r.Message)
	fmt.Println(runtime.ReasonOf("weft: run failed at step 0: boom") == nil)
	// Output:
	// scripted_miss 2 [tools]
	// scripted has no recorded answer for step 2: the tool list differs from the recording — run on live
	// true
}

// ExampleScriptedMissReason is the same sentence for a miss predicted
// before Run (the preview's scripted_key_match).
func ExampleScriptedMissReason() {
	fmt.Println(runtime.ScriptedMissReason(0, []string{"thinking"}).Message)
	// Output:
	// scripted has no recorded answer for step 0: the request sets a thinking level the recording does not hold — run on live
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

// ExampleRuntimeServer_Forget removes a runtime that is gone — it
// registered once and holds no command stream — from the list (and,
// built WithStore, from the command store); a connected one is
// refused with ErrConnected.
func ExampleRuntimeServer_Forget() {
	rs := runtime.New()
	mux := http.NewServeMux()
	rs.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reg := `{"runtime_id":"rt_gone","host":"laptop","pid":1,"service":"acme-api",
	         "agents":[{"name":"acme-support","manifest":"{}"}]}`
	resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(reg))
	if err != nil {
		fmt.Println("register:", err)
		return
	}
	_ = resp.Body.Close()

	fmt.Println("listed:", len(rs.Snapshot()), "connected:", rs.Connected("rt_gone"))
	fmt.Println("forget:", rs.Forget("rt_gone"))
	fmt.Println("listed:", len(rs.Snapshot()))
	// Output:
	// listed: 1 connected: false
	// forget: <nil>
	// listed: 0
}

// ExampleRuntimeServer_CommandsOn lists the commands acting on one run
// (GET /api/runs/{id}/commands): here a decision on a parked call of an
// app turn, sent to the runtime that registered runtime.Sessions and
// not acked yet. A row the runtime never acks turns lost after
// AckDeadline, and stays listed on the run.
func ExampleRuntimeServer_CommandsOn() {
	rs := runtime.New()
	mux := http.NewServeMux()
	rs.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reg := `{"runtime_id":"rt_app","host":"laptop","pid":1,"service":"acme-api","sessions":true,
	         "agents":[{"name":"acme-support","manifest":"{}"}]}`
	resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(reg))
	if err != nil {
		fmt.Println("register:", err)
		return
	}
	_ = resp.Body.Close()
	stream, err := http.Get(ts.URL + "/api/runtime/commands?runtime=rt_app")
	if err != nil {
		fmt.Println("commands:", err)
		return
	}
	defer func() { _ = stream.Body.Close() }()

	if _, err := rs.EnqueueSession("rt_app", runtime.SessionCommand{Verb: runtime.VerbDecide,
		PublicID: "pub_1", SessionID: "s_1", RunID: "s_1-t1", CallID: "call_refund", Decision: "approve"}); err != nil {
		fmt.Println("enqueue:", err)
		return
	}
	for _, c := range rs.CommandsOn("s_1-t1") {
		fmt.Println(c.Verb, c.CallID, c.Decision, c.State)
	}
	// Output:
	// decide call_refund approve queued
}

// ExampleRuntimeServer_HoldsInstance tells a link that dialled this
// server from one that dialled another: the registration carries the
// link's random instance.
func ExampleRuntimeServer_HoldsInstance() {
	rs := runtime.New()
	mux := http.NewServeMux()
	rs.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reg := `{"runtime_id":"rt_app","host":"laptop","pid":1,"service":"acme-api","instance":"inst_a",
	         "agents":[{"name":"acme-support","manifest":"{}"}]}`
	resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(reg))
	if err != nil {
		fmt.Println("register:", err)
		return
	}
	_ = resp.Body.Close()
	fmt.Println(rs.HoldsInstance("inst_a"), rs.HoldsInstance("inst_b"))
	// Output:
	// true false
}
