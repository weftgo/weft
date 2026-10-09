package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/studio"
)

// TestRuntimesGoldenMatchesARealRuntime: testdata/api/runtimes.golden.json
// is the GET /api/runtimes shape the web client builds the option lab
// on (plan F3: each agent's resolver flag and run defaults). This
// connects a real weft/runtime — run defaults set, a ModelResolver
// installed — to a real Studio and reads the route: every field the
// golden pins must exist in the real answer with the same JSON type.
func TestRuntimesGoldenMatchesARealRuntime(t *testing.T) {
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "weft.db")), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })

	temp, topP, maxTok, seed := 0.2, 0.9, 1024, int64(7)
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, struct{}) (string, error) { return "", nil },
		core.Replay(core.ReplaySafe))
	refund := core.Tool("refund", "Refund an order.", func(context.Context, struct{}) (string, error) { return "", nil })
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("acme-support"),
		core.Instructions("You are Acme's support agent."),
		core.MaxSteps(10), core.Parallelism(4),
		core.Thinking(core.ThinkingConfig{Level: core.ThinkLow}),
		core.Params(core.RequestParams{Temperature: &temp, TopP: &topP, MaxTokens: &maxTok, Seed: &seed, Stop: []string{"END"}}),
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "lookup_order"}),
		lookup, refund)
	stop := runtime.Install(runtime.Local(srv), runtime.Enabled(true), runtime.Agents(agent),
		runtime.Models(map[string]core.Model{"glm-5.3-flash": wefttest.Script(wefttest.Say("alt"))}),
		runtime.ModelResolver(func(context.Context, string) (core.Model, error) { return nil, errors.New("no") }),
		runtime.AllowSideEffects("refund"))
	t.Cleanup(stop)

	var got any
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(ts.URL + "/api/runtimes")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var doc struct {
			Runtimes []json.RawMessage `json:"runtimes"`
		}
		if json.Unmarshal(b, &doc) == nil && len(doc.Runtimes) > 0 {
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no runtime registered: %s", b)
		}
		time.Sleep(5 * time.Millisecond)
	}

	b, err := os.ReadFile(filepath.Join("testdata", "api", "runtimes.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	want, have := map[string]bool{}, map[string]bool{}
	shapePaths("", g, want)
	shapePaths("", got, have)
	var missing []string
	for k := range want {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		gotJSON, _ := json.Marshal(got)
		t.Errorf("runtimes.golden.json pins fields a real runtime's /api/runtimes does not carry: %v\nreal: %s", missing, gotJSON)
	}
}
