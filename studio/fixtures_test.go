package studio_test

// "Save as fixture" (WEFT-PLAYGROUND §7 P4, D4): a recorded run turns
// into wefttest replay fixtures. The pin that matters is the
// round-trip — the files this endpoint returns, dropped into a
// testdata directory, are loaded by wefttest.Replay and answer a new
// agent's calls byte-for-byte. That is the whole promise: the loop you
// just lived becomes a CI regression test.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

// TestFixtureRoundTripThroughReplay runs one real agent turn through a
// Studio-fed pipeline, asks the endpoint for the run's fixtures,
// writes them out the way a suite would, and replays them with
// wefttest.Replay.
func TestFixtureRoundTripThroughReplay(t *testing.T) {
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "weft.db")), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })

	lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "order shipped", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.Say("Order 42 shipped this morning."),
	), core.Name("support"), core.Instructions("You are a support agent."),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup)

	res, err := agent.Generate(ctx, core.Prompt("where is order 42?"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}

	// The fixtures, over the API. The pipeline's batches land
	// asynchronously; poll until the transcript is readable.
	var doc struct {
		Files []struct {
			Name string `json:"name"`
			Body string `json:"body"`
		} `json:"files"`
	}
	body := `{"run_id": "` + res.ID + `", "tools": ["lookup_order"]}`
	for i := 0; i < 200 && len(doc.Files) == 0; i++ {
		resp, err := http.Post(ts.URL+"/api/playground/fixtures", "application/json", strings.NewReader(body))
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
					t.Fatal(err)
				}
			}
			_ = resp.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(doc.Files) != 2 {
		t.Fatalf("files = %d, want one per recorded assistant turn (the call, the reply)", len(doc.Files))
	}
	if !strings.HasPrefix(doc.Files[0].Name, "00001-") || !strings.HasSuffix(doc.Files[0].Name, ".json") {
		t.Errorf("fixture name = %q, want wefttest's <seq>-<key>.json", doc.Files[0].Name)
	}
	if !strings.Contains(doc.Files[0].Body, `"tool_call"`) {
		t.Errorf("the first fixture lacks the tool call:\n%s", doc.Files[0].Body)
	}

	// A suite's move: the files into testdata, Replay answers.
	fixtures := filepath.Join(dir, "testdata", t.Name())
	if err := os.MkdirAll(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range doc.Files {
		if err := os.WriteFile(filepath.Join(fixtures, f.Name), []byte(f.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	replayed := core.New(wefttest.Replay(t, filepath.Join(dir, "testdata")), core.Name("support"), lookup)
	out, err := replayed.Generate(ctx, core.Prompt("where is order 42?"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if out.Text() != res.Text() {
		t.Errorf("replayed reply = %q, want the recorded %q", out.Text(), res.Text())
	}
	if got := out.Steps[0].Results[0].Content; got != "order shipped" {
		t.Errorf("the replayed run executed its tool for real (%q) — the fixture replays the call", got)
	}
}
