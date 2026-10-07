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

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
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
	lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "order shipped", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.Say("Order 42 shipped this morning."),
	), core.Name("orders"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup)
	// The keys thread stamps on a session's turns (block 8).
	res, err := agent.Generate(ctx, core.Prompt("where is order 42?"),
		core.Metadata(map[string]string{"weft.public_id": "pub_x", "weft.session.id": "s_x", "weft.turn": "1"}))
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

// TestTranscriptStepsFromARealRun: a run with a steered message and a
// parked call, then the run that resumes it, both driven through the
// real pipeline (otel → OTLP ingest → obsdb). Every transcript batch
// carries the step the core stamped on its messages record (ADR 0028
// §8) and the input flag of its record — never a step derived from the
// batches' order: the steered batch joins the step that just finished,
// and the resumed run's rebuilt tool message joins its step 0.
func TestTranscriptStepsFromARealRun(t *testing.T) {
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
	type orderIn struct {
		OrderID string `json:"order_id"`
	}
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, orderIn) (string, error) {
		return "order shipped", nil
	})
	refund := core.Tool("refund", "Refund an order.", func(context.Context, orderIn) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agent := core.New(wefttest.Script(
		// run 1: step 0 looks the order up; a steer arrives; step 1 asks
		// for the refund, which parks.
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c_lookup"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"42"}`, ID: "c_refund"}),
		// run 2 (the resume): step 0 looks again, step 1 answers.
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c_again"}),
		wefttest.Say("Refunded order 42."),
	), core.Name("orders"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup, refund)

	first, err := agent.Generate(ctx, core.Prompt("where is order 42?"),
		wefttest.NewSteers().At(0, core.User("and refund it")).Option())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Pending) != 1 || first.Pending[0].ID != "c_refund" {
		t.Fatalf("first run pending = %+v, want the refund parked", first.Pending)
	}
	second, err := agent.Generate(ctx, core.Messages(first.Messages...), core.Approve("c_refund"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	type batch struct {
		Index    int64             `json:"index"`
		Step     int               `json:"step"`
		Input    bool              `json:"input"`
		Badge    *string           `json:"badge"`
		Messages []json.RawMessage `json:"messages"`
	}
	read := func(path string, doc any) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if err := json.Unmarshal(b, doc); err != nil {
					t.Fatal(err)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
			}
		}
	}
	roleOf := func(raw json.RawMessage) string {
		var m struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(raw, &m)
		return m.Role
	}
	type want struct {
		step  int
		input bool
		roles string
	}
	check := func(run string, wants []want) []batch {
		t.Helper()
		var doc struct {
			Batches []batch `json:"batches"`
		}
		read("/api/runs/"+run+"/transcript", &doc)
		if len(doc.Batches) != len(wants) {
			t.Fatalf("%s: %d batches, want %d: %+v", run, len(doc.Batches), len(wants), doc.Batches)
		}
		for i, b := range doc.Batches {
			var roles []string
			for _, m := range b.Messages {
				roles = append(roles, roleOf(m))
			}
			got := want{b.Step, b.Input, strings.Join(roles, ",")}
			if b.Index != int64(i) || got != wants[i] || b.Badge != nil {
				t.Errorf("%s batch %d = index %d %+v badge %v, want %+v and no badge", run, i, b.Index, got, b.Badge, wants[i])
			}
		}
		return doc.Batches
	}
	check(first.ID, []want{
		{0, true, "user"},       // the input
		{0, false, "assistant"}, // step 0: the lookup call
		{0, false, "tool"},      // its result
		{0, false, "user"},      // the steer, delivered after step 0
		{1, false, "assistant"}, // step 1: the refund call, parked
	})
	check(second.ID, []want{
		{0, true, "user,assistant,tool,user,assistant"}, // run 1's transcript, fed back
		{0, false, "tool"},      // the approved refund's result, joined at step 0
		{0, false, "assistant"}, // step 0: the second lookup
		{0, false, "tool"},
		{1, false, "assistant"}, // step 1: the answer
	})

	// The steered batch's step is the one the Steered event names.
	var events struct {
		Events []struct {
			Event struct {
				Type string `json:"type"`
				Step int    `json:"step"`
			} `json:"event"`
		} `json:"events"`
	}
	read("/api/runs/"+first.ID+"/events", &events)
	steered := -1
	for _, e := range events.Events {
		if e.Event.Type == "steered" {
			steered = e.Event.Step
		}
	}
	if steered != 0 {
		t.Errorf("steered event step = %d, want 0 (the step its batch is stored under)", steered)
	}
}
