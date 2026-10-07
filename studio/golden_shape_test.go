package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/wefttest"
)

// shapePaths flattens a JSON document into the set of its field paths
// and leaf types: arrays collapse to [], an object with a "type" names
// its fields per type (events and message parts), attribute maps
// collapse to one <attr> key (their keys are the data, not the shape).
func shapePaths(prefix string, v any, out map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		out[prefix+"{}"] = true
		for k, x := range v {
			p := prefix + "." + k
			if strings.HasSuffix(prefix, ".attrs") {
				p = prefix + ".<attr>"
			}
			if typ, ok := v["type"].(string); ok && k != "type" {
				p = prefix + "[" + typ + "]." + k
			}
			shapePaths(p, x, out)
		}
	case []any:
		out[prefix+"[]"] = true
		for _, x := range v {
			shapePaths(prefix+"[]", x, out)
		}
	default:
		out[prefix+"="+fmt.Sprintf("%T", v)] = true
	}
}

// TestGoldensMatchARealRun: the testdata/api goldens are built from a
// hand-written fixture database, and the web client's tests are built
// on the goldens. This drives one real run through the real pipeline
// (otel → OTLP ingest → obsdb) and reads the same routes: every field
// a golden pins must exist in the real answer with the same JSON type —
// a golden in a shape the real producer never emits would let the
// client tests pass against a server that does not exist.
func TestGoldensMatchARealRun(t *testing.T) {
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	lookup := weft.Tool("lookup_order", "Look up an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "order shipped", nil
	})
	agent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.Say("Order 42 shipped this morning."),
	), weft.Name("orders"), weft.TracerProvider(p.TracerProvider()), weft.LoggerProvider(p.LoggerProvider()), lookup)
	// The keys thread stamps on a session's turns (block 8).
	res, err := agent.Generate(ctx, weft.Prompt("where is order 42?"),
		weft.Metadata(map[string]string{"weft.public_id": "pub_x", "weft.session.id": "s_x", "weft.turn": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil { // flushes both signals
		t.Fatal(err)
	}

	fetch := func(path string) any {
		t.Helper()
		var doc any
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if err := json.Unmarshal(b, &doc); err != nil {
					t.Fatal(err)
				}
				return doc
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
			}
		}
	}
	trace, _ := fetch("/api/runs/" + res.ID).(map[string]any)["trace_id"].(string)
	for _, c := range []struct {
		golden, path string
		// what the fixture holds that this run does not: a subagent
		// child, a crash orphan's open finish, a metadata key of its own.
		absent []string
	}{
		{"run-sub.golden.json", "/api/runs/" + res.ID, []string{".children[]", ".meta.cwd"}},
		{"runs.golden.json", "/api/runs", []string{".runs[].finished=<nil>", ".runs[].meta.cwd"}},
		{"events-ok.golden.json", "/api/runs/" + res.ID + "/events", nil},
		{"events-ok-paged.golden.json", "/api/runs/" + res.ID + "/events?after=2&limit=3", nil},
		{"transcript-ok.golden.json", "/api/runs/" + res.ID + "/transcript", nil},
		{"spans-sub.golden.json", "/api/runs/" + res.ID + "/spans", nil},
		{"trace.golden.json", "/api/traces/" + trace, nil},
		{"sessions.golden.json", "/api/sessions", nil},
		{"session-orders.golden.json", "/api/sessions/s_x", []string{".runs[].finished=<nil>", ".runs[].meta.cwd"}},
		{"public.golden.json", "/api/public/pub_x", nil},
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "api", c.golden))
		if err != nil {
			t.Fatal(err)
		}
		var g any
		if err := json.Unmarshal(b, &g); err != nil {
			t.Fatal(err)
		}
		want, got := map[string]bool{}, map[string]bool{}
		shapePaths("", g, want)
		shapePaths("", fetch(c.path), got)
		var missing []string
	next:
		for k := range want {
			if got[k] {
				continue
			}
			for _, a := range c.absent {
				if strings.HasPrefix(k, a) {
					continue next
				}
			}
			missing = append(missing, k)
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s pins fields a real run's %s does not carry: %v", c.golden, c.path, missing)
		}
	}
}
