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
	defer resp.Body.Close()
	fmt.Println("register:", resp.StatusCode)

	views := rs.Snapshot()
	fmt.Println("runtimes:", len(views), views[0].ID, views[0].Agents[0].Name)
	// Output:
	// register: 200
	// runtimes: 1 rt_demo acme-support
}
