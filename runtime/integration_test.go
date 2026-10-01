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
	"sync/atomic"
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
		weft.LoggerProvider(p.LoggerProvider()), // the messages records — replay-grade, D1
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

// TestPlaygroundApprovalVerbs pins P1's approval controls end to end
// (WEFT-DEVTOOLS §8.2): a parked experiment run's continue / skip /
// resolve go through POST /api/runs/{id}/approvals to the runtime that
// started the run and land as ADR 0007's own verbs — approve runs the
// handler for real, resolve pastes a content computed outside the
// process. A run no runtime started (the app's own turn) is refused:
// viewer-only, PQ7.
func TestPlaygroundApprovalVerbs(t *testing.T) {
	var refundRan atomic.Bool
	e := newE2E(t,
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`, ID: "call_refund"}),
		wefttest.Say("refunded, anything else?"),
	)
	// A second, uninstrumented refund tool so the handler's "ran for
	// real" is observable without side effects.
	refund := weft.Tool("refund", "Refund an order.",
		func(ctx context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			refundRan.Store(true)
			return "refunded", nil
		})
	// One shared script across every run below: the app's own turn
	// (call + reply), the first park (the model re-issues the call on
	// the kept prefix), the resolve's continuation, the second park
	// (a fresh input re-issues it), and the approve's continuation.
	script := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`, ID: "call_refund"}),
		wefttest.Say("refunded, anything else?"),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`, ID: "call_refund"}),
		wefttest.Say("resolved then."),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`, ID: "call_refund"}),
		wefttest.Say("approved then."),
	)
	e.agent = weft.New(script, weft.Name("acme-support"), weft.TracerProvider(e.p.TracerProvider()), refund)
	runID := e.appTurn(t, "please refund order #4411")
	refundRan.Store(false) // the app's own turn really ran it; the experiment must not

	shutdown := runtime.Install(
		runtime.Studio(e.ts.URL, ""),
		runtime.Agents(e.agent),
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
		t.Fatalf("runtimes: %v %s", err, rtJSON)
	}

	body := fmt.Sprintf(`{
	  "command_id": "cmd_appr_1",
	  "runtime": %q,
	  "agent": "acme-support",
	  "source": {"run_id": %q, "from_step": 1},
	  "engine": "live", "side_effects": "park", "thread": "ephemeral"
	}`, runtimes.Runtimes[0].ID, runID)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", body); code != http.StatusAccepted {
		t.Fatalf("park command = %d %s", code, resp)
	}
	row := e.waitCommand(t, "cmd_appr_1", "finished")
	var st struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(row), &st); err != nil || !strings.HasPrefix(st.RunID, "pg_") {
		t.Fatalf("finished row = %s", row)
	}
	if refundRan.Load() {
		t.Fatal("the refund handler ran although the run parks")
	}

	// The app's own turn is not decidable (PQ7): viewer-only.
	if code, _ := e.api(t, http.MethodPost, "/api/runs/"+runID+"/approvals",
		`{"call_id":"x","decision":"approve"}`); code != http.StatusForbidden {
		t.Errorf("decision on the app's own run = %d, want 403 (viewer-only, PQ7)", code)
	}

	// Resolve pastes a result computed outside the process: the handler
	// still never runs, and the model sees the pasted content.
	resolveCode, resolveResp := e.api(t, http.MethodPost, "/api/runs/"+st.RunID+"/approvals",
		`{"call_id":"call_refund","decision":"resolve","content":"REFUNDED (resolved from the panel)"}`)
	if resolveCode != http.StatusAccepted {
		t.Fatalf("resolve = %d %s", resolveCode, resolveResp)
	}
	var resolveCmd struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal([]byte(resolveResp), &resolveCmd); err != nil {
		t.Fatal(err)
	}
	e.waitCommand(t, resolveCmd.CommandID, "finished")
	if refundRan.Load() {
		t.Error("resolve ran the handler although a result was pasted")
	}

	// Approve (continue) runs the handler for real: prove it on a
	// second park of the same experiment.
	approveBody := fmt.Sprintf(`{
	  "command_id": "cmd_appr_2",
	  "runtime": %q,
	  "agent": "acme-support",
	  "input": "refund it again please",
	  "overrides": {"tools_enabled": ["refund"]},
	  "engine": "live", "side_effects": "park", "thread": "ephemeral"
	}`, runtimes.Runtimes[0].ID)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", approveBody); code != http.StatusAccepted {
		t.Fatalf("second park = %d %s", code, resp)
	}
	row2 := e.waitCommand(t, "cmd_appr_2", "finished")
	var st2 struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(row2), &st2); err != nil || !strings.HasPrefix(st2.RunID, "pg_") {
		t.Fatalf("second finished row = %s", row2)
	}
	approveCode, approveResp := e.api(t, http.MethodPost, "/api/runs/"+st2.RunID+"/approvals",
		`{"call_id":"call_refund","decision":"approve"}`)
	if approveCode != http.StatusAccepted {
		t.Fatalf("approve = %d %s", approveCode, approveResp)
	}
	var cmdID struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal([]byte(approveResp), &cmdID); err != nil {
		t.Fatal(err)
	}
	e.waitCommand(t, cmdID.CommandID, "finished")
	if !refundRan.Load() {
		t.Error("approve (continue) did not run the handler for real")
	}
}

// TestPlaygroundContinueFromStepWithEdits is §10.6's P2 gate: continue
// from step 2 with a patched tool result (the model sees the
// counterfactual, not the record), and a never tool provably does not
// re-fire in substitute mode — a counting refund's handler stays at
// zero while the model still receives the recorded result.
func TestPlaygroundContinueFromStepWithEdits(t *testing.T) {
	var refunds atomic.Int64
	refund := weft.Tool("refund", "Refund an order.",
		func(ctx context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			refunds.Add(1)
			return "refunded-for-real", nil
		})
	lookup := weft.Tool("lookup_order", "Look up an order.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "shipped", nil
	}, weft.Replay(weft.ReplaySafe))
	// The app's own turn: lookup (step 0), refund (step 1), reply
	// (step 2). The experiment continues from step 2 with the refund
	// result patched, and — the counting case — re-runs step 1 fresh
	// under substitute.
	script := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"4411"}`, ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`, ID: "c2"}),
		wefttest.Say("Refunded — anything else?"),                                                // the app's own turn ends
		wefttest.Say("The refund failed with 429."),                                              // from_step 2's fresh step 2
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4411"}`, ID: "c2"}), // from_step 1 re-runs it
		wefttest.Say("Refunded, on the record."),                                                 // the substitute chain's continuation
	)
	e := newE2E(t)
	e.agent = weft.New(script, weft.Name("acme-support"),
		weft.Instructions("You are Acme's support agent."),
		weft.TracerProvider(e.p.TracerProvider()), weft.LoggerProvider(e.p.LoggerProvider()), lookup, refund)
	runID := e.appTurn(t, "refund order #4411 please")
	// The Studio destination batches logs; the transcript route can
	// answer before the turn's last records land. Wait for the final
	// reply so the edit validates against the whole turn.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := e.api(t, http.MethodGet, "/api/runs/"+runID+"/transcript", "")
		if strings.Contains(body, "anything else") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	refunds.Store(0) // the app's own turn really refunded; the experiments must not

	shutdown := runtime.Install(
		runtime.Studio(e.ts.URL, ""),
		runtime.Agents(e.agent),
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
		t.Fatalf("runtimes: %v %s", err, rtJSON)
	}
	rt := runtimes.Runtimes[0].ID

	// Continue from step 2 with the refund result patched to 429: the
	// fresh step 2's model turn answers the counterfactual.
	body := fmt.Sprintf(`{
	  "command_id": "cmd_edit_1", "runtime": %q, "agent": "acme-support",
	  "source": {"run_id": %q, "from_step": 2},
	  "transcript_edits": [{"step": 1, "tool_result": "429 Too Many Requests", "call_id": "c2"}],
	  "engine": "live", "side_effects": "substitute", "thread": "ephemeral"
	}`, rt, runID)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", body); code != http.StatusAccepted {
		t.Fatalf("edited continue = %d %s", code, resp)
	}
	row := e.waitCommand(t, "cmd_edit_1", "finished")
	var st struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(row), &st); err != nil || !strings.HasPrefix(st.RunID, "pg_") {
		t.Fatalf("finished row = %s", row)
	}
	// The patched counterfactual reached the model: the fresh step 2
	// answered it.
	e.p.ForceFlush(context.Background())
	if text := e.commandText(t, st.RunID); !strings.Contains(text, "429") {
		t.Errorf("the fresh step's reply = %q, want it to answer the patched 429", text)
	}

	// A bad edit is a 400 before any run: a call the prefix does not hold.
	bad := fmt.Sprintf(`{
	  "runtime": %q, "agent": "acme-support",
	  "source": {"run_id": %q, "from_step": 2},
	  "transcript_edits": [{"step": 1, "tool_result": "x", "call_id": "c_nope"}],
	  "engine": "live", "thread": "ephemeral"
	}`, rt, runID)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", bad); code != http.StatusBadRequest {
		t.Errorf("unknown call edit = %d %s, want 400", code, resp)
	}

	// The counting case: from_step 1 re-runs step 1 fresh — the model
	// re-issues the refund call, the run parks (never), substitute
	// answers it with the recorded result, and the handler never fires.
	count := fmt.Sprintf(`{
	  "command_id": "cmd_edit_2", "runtime": %q, "agent": "acme-support",
	  "source": {"run_id": %q, "from_step": 1},
	  "engine": "live", "side_effects": "substitute", "thread": "ephemeral"
	}`, rt, runID)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", count); code != http.StatusAccepted {
		t.Fatalf("counting command = %d %s", code, resp)
	}
	e.waitCommand(t, "cmd_edit_2", "finished")
	if n := refunds.Load(); n != 0 {
		t.Errorf("the refund handler fired %d times in substitute mode — a never tool must not re-fire", n)
	}
}

// commandText reads a run's final reply out of Studio's transcript.
func (e *e2e) commandText(t *testing.T, runID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, body := e.api(t, http.MethodGet, "/api/runs/"+runID+"/transcript", "")
		if code == http.StatusOK && strings.Contains(body, "assistant") {
			var doc struct {
				Batches []struct {
					Messages []struct {
						Role    string `json:"role"`
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"messages"`
				} `json:"batches"`
			}
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, b := range doc.Batches {
				for _, m := range b.Messages {
					if m.Role != "assistant" {
						continue
					}
					for _, p := range m.Content {
						if p.Type == "text" && p.Text != "" {
							out = append(out, p.Text)
						}
					}
				}
			}
			if len(out) > 0 {
				return strings.Join(out, "\n")
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the transcript of %s never carried an assistant reply", runID)
	return ""
}
