package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The end-to-end pipe (step 8a's test list): a real Studio over
// SQLite with Playground(true), the app's own pipeline exporting into
// it, a thread session for the source turn, and runtime.Install as
// the app's whole integration.

// spanRecorder is an in-memory span exporter the assertions read the
// §5.2 metadata and weft.override.* attributes from.
type spanRecorder struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (r *spanRecorder) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, spans...)
	return nil
}

func (r *spanRecorder) Shutdown(context.Context) error { return nil }

// playgroundSpan finds the invoke_agent span of the playground run —
// the one carrying weft.playground = true.
func (r *spanRecorder) playgroundSpan(t *testing.T) map[string]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, s := range r.spans {
			if !strings.HasPrefix(s.Name(), "invoke_agent") {
				continue
			}
			attrs := map[string]string{}
			for _, kv := range s.Attributes() {
				attrs[string(kv.Key)] = kv.Value.String()
			}
			if attrs["weft.playground"] == "true" {
				r.mu.Unlock()
				return attrs
			}
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no playground invoke_agent span was exported")
	return nil
}

// dirSnapshot hashes every file under dir, recursively: the thread
// store byte-compare.
func dirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("%x", b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// e2e is one wired-up world.
type e2e struct {
	ts    *httptest.Server
	srv   *studio.Server
	p     *otel.Pipeline
	rec   *spanRecorder
	agent *weft.Agent
	alt   *wefttest.Model
	store thread.Storage
	dir   string
}

func newE2E(t *testing.T, appTurns ...wefttest.Turn) *e2e {
	t.Helper()
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "studio.db")), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })

	rec := &spanRecorder{}
	ctx := context.Background()
	// The app's own pipeline: the Studio destination (content on, the
	// OTLP ingest into the embedded server) plus the recorder.
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.Exporters(rec, nil), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })

	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(ctx context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			return `{"status":"shipped"}`, nil
		})
	refund := weft.Tool("refund", "Refund an order.",
		func(ctx context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			return "refunded", nil
		})
	agent := weft.New(
		wefttest.Script(appTurns...),
		weft.Name("acme-support"),
		weft.Instructions("You are Acme's support agent."),
		weft.MaxSteps(10),
		weft.Parallelism(4),
		weft.TracerProvider(p.TracerProvider()),
		lookup, refund,
	)
	store, err := jsonl.Open(filepath.Join(dir, "threads"))
	if err != nil {
		t.Fatal(err)
	}
	return &e2e{ts: ts, srv: srv, p: p, rec: rec, agent: agent,
		alt:   wefttest.Script(wefttest.Say("It shipped — track it here: https://acme.example/t/4411")),
		store: store, dir: dir}
}

// api is one JSON request against the Studio.
func (e *e2e) api(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// waitRun lets the app run one of its own turns through the thread
// session and returns its run id.
func (e *e2e) appTurn(t *testing.T, prompt string) string {
	t.Helper()
	sess, err := thread.Create(context.Background(), e.store, e.agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := sess.Send(context.Background(), weft.User(prompt))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := e.p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The Studio's 404 check reads its own DB; the flush wrote it, the
	// poll only bridges the read.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if code, _ := e.api(t, http.MethodGet, "/api/runs/"+turn.RunID()+"/transcript", ""); code == http.StatusOK {
			return turn.RunID()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the source run never reached Studio")
	return ""
}

// waitRuntime blocks until a runtime is connected.
func (e *e2e) waitRuntime(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := e.api(t, http.MethodGet, "/api/runtimes", "")
		if strings.Contains(body, "rt_") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no runtime ever registered")
}

// waitCommand polls the lifecycle row until state arrives.
func (e *e2e) waitCommand(t *testing.T, id, state string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := e.api(t, http.MethodGet, "/api/playground/commands/"+id, "")
		if strings.Contains(body, `"state":"`+state+`"`) {
			return body
		}
		if strings.Contains(body, `"state":"rejected"`) || strings.Contains(body, `"state":"lost"`) {
			return body
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, body := e.api(t, http.MethodGet, "/api/playground/commands/"+id, "")
	t.Fatalf("command %s never reached %q: %s", id, state, body)
	return ""
}

// TestPlaygroundEndToEnd is the P0 gate as a test: a curl-shaped
// command produces an ack, then a run with weft.playground,
// weft.experiment.id, weft.forked_from and weft.override.* on its
// span; the same command id twice is refused (409); a budget breach
// rejects the next command of the experiment and never touches the
// app's own runs; an ephemeral run writes nothing to thread storage.
func TestPlaygroundEndToEnd(t *testing.T) {
	e := newE2E(t,
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"4411"}`}),
		wefttest.Say("Your order shipped yesterday."),
		wefttest.Say("The tracking link is in my last message."),
	)
	ctx := context.Background()

	// The app's own turn, through a thread session: this run is the
	// experiment's source.
	runID := e.appTurn(t, "where is my order #4411?")
	if !strings.Contains(runID, "-t1") {
		t.Fatalf("source run id = %q, want a thread turn id", runID)
	}

	threadsDir := filepath.Join(e.dir, "threads")
	before := dirSnapshot(t, threadsDir)

	// The runtime: the app's whole integration is this Install.
	shutdown := runtime.Install(
		runtime.Studio(e.ts.URL, ""),
		runtime.Agents(e.agent),
		runtime.Models(map[string]weft.Model{"glm-5.3-flash": e.alt}),
		runtime.Limits(runtime.Budget{MaxRunsPerExperiment: 1}),
		runtime.AllowSideEffects("lookup_order"),
		runtime.Threads(e.store),
		runtime.Enabled(true),
	)
	defer shutdown()
	e.waitRuntime(t)

	runBody := `{
	  "command_id": "cmd_e2e_1",
	  "runtime": "%s",
	  "agent": "acme-support",
	  "source": {"run_id": %q, "from_step": 1},
	  "input": null,
	  "overrides": {
	    "instructions": "You are Acme's support agent. Always include the tracking link.",
	    "tools_enabled": ["lookup_order"],
	    "model": "glm-5.3-flash",
	    "thinking": "off",
	    "options": {"max_steps": 6, "temperature": 0.2}
	  },
	  "engine": "live",
	  "side_effects": "substitute",
	  "thread": "ephemeral",
	  "experiment_id": "exp_9",
	  "public_id": "pub_7Hk2"
	}`
	_, rtJSON := e.api(t, http.MethodGet, "/api/runtimes", "")
	var runtimes struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(rtJSON), &runtimes); err != nil || len(runtimes.Runtimes) == 0 {
		t.Fatalf("runtimes: %v %s", err, runtimes)
	}
	rtID := runtimes.Runtimes[0].ID

	code, resp := e.api(t, http.MethodPost, "/api/playground/runs",
		fmt.Sprintf(runBody, rtID, runID))
	if code != http.StatusAccepted || !strings.Contains(resp, `"command_id":"cmd_e2e_1"`) {
		t.Fatalf("run = %d %s", code, resp)
	}

	row := e.waitCommand(t, "cmd_e2e_1", "finished")
	if !strings.Contains(row, `"run_id":"pg_`) || !strings.Contains(row, `"error":null`) {
		t.Errorf("finished row = %s, want the pg_ run id and a null error", row)
	}

	// §10.6's P0 gate: the run's span carries the experiment labels
	// and the override fingerprint.
	attrs := e.rec.playgroundSpan(t)
	want := map[string]string{
		"weft.playground":            "true",
		"weft.experiment.id":         "exp_9",
		"weft.playground.command":    "cmd_e2e_1",
		"weft.forked_from":           runID + "#1",
		"weft.public_id":             "pub_7Hk2",
		"weft.playground.actor":      "local",
		"weft.override.instructions": "true",
		"weft.override.tools":        "lookup_order",
		"weft.override.model":        "wefttest/script",
		"weft.override.thinking":     "off",
		"weft.override.max_steps":    "6",
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("span %s = %q, want %q", k, attrs[k], v)
		}
	}
	if h := attrs["weft.override.hash"]; len(h) != 64 {
		t.Errorf("weft.override.hash = %q, want 64 hex chars", h)
	}
	// The command's only enabled tool is opted in: nothing parks (the
	// on-set minus the opted-in is empty — §6 rule 3, TestParkedTools).
	if p := attrs["weft.override.park_on"]; p != "" {
		t.Errorf("weft.override.park_on = %q, want none for an opted-in tool set", p)
	}
	if sid := attrs["weft.session.id"]; sid != "" {
		t.Errorf("an ephemeral run carries weft.session.id = %q (§5.2: never)", sid)
	}

	// The ephemeral run wrote nothing to thread storage: byte-compare.
	if after := dirSnapshot(t, threadsDir); len(after) != len(before) {
		t.Errorf("thread store changed: %d files before, %d after", len(before), len(after))
	} else {
		for path, sum := range before {
			if after[path] != sum {
				t.Errorf("thread file %s changed", path)
			}
		}
	}

	// The same command id twice is refused (409).
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", fmt.Sprintf(runBody, rtID, runID)); code != http.StatusConflict {
		t.Errorf("reused command id = %d %s, want 409", code, resp)
	}

	// The budget cap (one run per experiment): the next command of
	// exp_9 is rejected budget_exceeded.
	breach := strings.Replace(fmt.Sprintf(runBody, rtID, runID), `"cmd_e2e_1"`, `"cmd_e2e_2"`, 1)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", breach); code != http.StatusAccepted {
		t.Fatalf("breach command = %d %s", code, resp)
	}
	row = e.waitCommand(t, "cmd_e2e_2", "rejected")
	if !strings.Contains(row, "budget_exceeded") {
		t.Errorf("breach row = %s, want budget_exceeded", row)
	}

	// The app's own runs are untouched by the breach (§6 rule 6).
	res, err := e.agent.Generate(ctx, weft.Prompt("and the tracking link?"))
	if err != nil {
		t.Fatalf("the app's own run failed after a breach: %v", err)
	}
	if res.Text() == "" {
		t.Error("the app's own run produced no text")
	}
	if err := e.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestPlaygroundParkOnSpan pins §6 rule 3's observable fingerprint
// (§10.1): a command that enables a tool the runtime did not opt in
// runs with weft.override.park_on naming exactly the enabled
// non-opted-in set — the model's call to it parks (the run finishes
// successfully with the call pending) instead of firing the side
// effect. The opted-in-only command's absence is pinned in
// TestPlaygroundEndToEnd.
func TestPlaygroundParkOnSpan(t *testing.T) {
	e := newE2E(t,
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"4411"}`}),
		wefttest.Say("Your order shipped yesterday."), // the app's own turn
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`}),
		wefttest.Say("Refunded."), // never reached: the parked run ends at the call
	)
	ctx := context.Background()
	// Consume the app's own turn (no session: this run is not the
	// experiment's source), so the experiment's run starts at the
	// refund call.
	if _, err := e.agent.Generate(ctx, weft.Prompt("where is my order #4411?")); err != nil {
		t.Fatal(err)
	}

	shutdown := runtime.Install(
		runtime.Studio(e.ts.URL, ""),
		runtime.Agents(e.agent),
		runtime.Models(map[string]weft.Model{"glm-5.3-flash": e.alt}),
		runtime.AllowSideEffects("lookup_order"),
		runtime.Enabled(true),
	)
	defer shutdown()
	e.waitRuntime(t)

	_, rtJSON := e.api(t, http.MethodGet, "/api/runtimes", "")
	var runtimes struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(rtJSON), &runtimes); err != nil || len(runtimes.Runtimes) == 0 {
		t.Fatalf("runtimes: %v %s", err, runtimes)
	}

	// The command enables refund (not opted in; lookup_order is) and
	// turns lookup_order off: only refund parks.
	body := fmt.Sprintf(`{
	  "command_id": "cmd_park",
	  "runtime": %q,
	  "agent": "acme-support",
	  "input": "please refund order #4411",
	  "overrides": {"tools_enabled": ["refund"]},
	  "engine": "live",
	  "side_effects": "substitute",
	  "thread": "ephemeral",
	  "experiment_id": "exp_park"
	}`, runtimes.Runtimes[0].ID)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", body); code != http.StatusAccepted {
		t.Fatalf("park command = %d %s", code, resp)
	}
	if row := e.waitCommand(t, "cmd_park", "finished"); !strings.Contains(row, `"run_id":"pg_`) {
		t.Errorf("park command row = %s, want the run's pg_ id", row)
	}
	if attrs := e.rec.playgroundSpan(t); attrs["weft.override.park_on"] != "refund" {
		t.Errorf("weft.override.park_on = %q, want %q (the enabled tool the runtime did not opt in)",
			attrs["weft.override.park_on"], "refund")
	}
}
