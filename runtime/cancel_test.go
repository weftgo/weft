package runtime_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/runtime"
)

// holdModel calls lookup_order on its first call and holds every later
// one until its context is canceled — a run in flight with one step
// behind it, which only a cancel ends.
type holdModel struct {
	mu    sync.Mutex
	calls int
	held  chan struct{} // closed once a call is held
	once  sync.Once
}

func (m *holdModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "hold"}
}

func (m *holdModel) Stream(ctx context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	if n == 1 {
		return func(yield func(core.ModelEvent, error) bool) {
			if !yield(core.ModelToolCall{ID: "c_1", Name: "lookup_order", Args: []byte(`{"order_id":"4411"}`)}, nil) {
				return
			}
			yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
		}
	}
	return func(yield func(core.ModelEvent, error) bool) {
		m.once.Do(func() { close(m.held) })
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}

// TestCancelRuntimeStartedRun is plan H6's done line end to end: a real
// Studio (Playground(true)), the app's pipeline exporting into it, and
// runtime.Install. A playground command starts a run that takes one
// step and then holds at the model; POST /api/runs/{id}/cancel answers
// 202 naming the run, its command and its runtime; the runtime cancels
// the command's context, so the run ends as the core ends a canceled
// run — its command finished failed with the run's error, its row
// failed with the same error and its first step kept (the partial
// transcript), and the live stream that followed the run carried the
// failed run row. A second cancel is 409: nothing is in flight.
func TestCancelRuntimeStartedRun(t *testing.T) {
	e := newE2E(t)
	model := &holdModel{held: make(chan struct{})}
	lookup := core.Tool("lookup_order", "Look up an order.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	e.agent = core.New(model, core.Name("acme-support"),
		core.TracerProvider(e.p.TracerProvider()), core.LoggerProvider(e.p.LoggerProvider()), lookup)

	shutdown := runtime.Install(runtime.Studio(e.ts.URL, ""), runtime.Agents(e.agent), runtime.Enabled(true))
	defer shutdown()
	rt := e.runtimeID(t)

	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", fmt.Sprintf(
		`{"command_id":"cmd_cancel","runtime":%q,"agent":"acme-support","input":"a slow question",
		  "engine":"live","side_effects":"substitute","thread":"ephemeral"}`, rt)); code != http.StatusAccepted {
		t.Fatalf("command = %d %s", code, resp)
	}
	row := e.waitCommand(t, "cmd_cancel", "accepted")
	var st struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(row), &st); err != nil || !strings.HasPrefix(st.RunID, "pg_") {
		t.Fatalf("accepted row = %s (%v)", row, err)
	}
	runID := st.RunID
	select {
	case <-model.held:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached its held model call")
	}

	// Follow the run live before cancelling it.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/api/live?run="+runID+"&kinds=event,run", nil)
	live, err := http.DefaultClient.Do(req)
	if err != nil || live.StatusCode != http.StatusOK {
		t.Fatalf("live = %v %v", err, live)
	}
	defer func() { _ = live.Body.Close() }()
	failedFrame := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(live.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "run":
				if data := strings.TrimPrefix(line, "data: "); strings.Contains(data, `"status":"failed"`) {
					failedFrame <- data
					return
				}
			case line == "":
				event = ""
			}
		}
	}()

	code, resp := e.api(t, http.MethodPost, "/api/runs/"+runID+"/cancel", "")
	if code != http.StatusAccepted {
		t.Fatalf("cancel = %d %s, want 202", code, resp)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(resp), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"run_id": runID, "command_id": "cmd_cancel", "runtime_id": rt, "state": "cancelling"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cancel body = %v, want %v", got, want)
	}
	// The real producer's body has the golden's fields, no more, no fewer.
	b, err := os.ReadFile(filepath.Join("..", "studio", "testdata", "api", "cancel.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]any
	if err := json.Unmarshal(b, &golden); err != nil {
		t.Fatal(err)
	}
	var gk, wk []string
	for k := range golden {
		gk = append(gk, k)
	}
	for k := range got {
		wk = append(wk, k)
	}
	slices.Sort(gk)
	slices.Sort(wk)
	if !slices.Equal(gk, wk) {
		t.Errorf("cancel body fields %v, golden's %v", wk, gk)
	}

	// The command ends failed with the run's own error.
	row = e.waitCommand(t, "cmd_cancel", "finished")
	if !strings.Contains(row, `"status":"failed"`) || !strings.Contains(row, "context canceled") {
		t.Errorf("finished row = %s, want failed with context canceled", row)
	}
	if err := e.p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The run row says so, and keeps the step it took.
	var runRow struct {
		Status string `json:"status"`
		Err    string `json:"err"`
		Steps  int    `json:"steps"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := e.api(t, http.MethodGet, "/api/runs/"+runID, "")
		if json.Unmarshal([]byte(body), &runRow) == nil && runRow.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runRow.Status != "failed" || !strings.Contains(runRow.Err, "context canceled") || runRow.Steps != 1 {
		t.Errorf("run row status %q err %q steps %d, want failed with context canceled after its one step", runRow.Status, runRow.Err, runRow.Steps)
	}
	_, events := e.api(t, http.MethodGet, "/api/runs/"+runID+"/events?after=-1&limit=100", "")
	if !strings.Contains(events, `"step_finish"`) || !strings.Contains(events, "lookup_order") {
		t.Errorf("the canceled run's records lost its first step: %s", events)
	}
	// The run's invoke_agent span names the control and its cause: a
	// Studio cancel, not a shutdown or an interrupt (UX-E-41).
	_, spans := e.api(t, http.MethodGet, "/api/runs/"+runID+"/spans", "")
	for _, want := range []string{`"weft.run.stopped_by":"canceled"`, `"weft.cancel.cause":"studio.cancel"`} {
		if !strings.Contains(strings.ReplaceAll(spans, " ", ""), want) {
			t.Errorf("the canceled run's spans lack %s: %s", want, spans)
		}
	}
	select {
	case data := <-failedFrame:
		if !strings.Contains(data, runID) {
			t.Errorf("live run frame = %s, want the canceled run", data)
		}
	case <-time.After(5 * time.Second):
		t.Error("the live stream never carried the failed run row")
	}

	// Nothing in flight any more: 409.
	if code, resp := e.api(t, http.MethodPost, "/api/runs/"+runID+"/cancel", ""); code != http.StatusConflict {
		t.Errorf("second cancel = %d %s, want 409", code, resp)
	}
}

// TestCancelAppShutdownCause: a runtime-started run still in flight
// when the app shuts the runtime down (runtime.Install's shutdown func)
// ends canceled with the link's own cause — its invoke_agent span reads
// weft.cancel.cause = app.shutdown, the third control UX-E-41 names
// beside studio.cancel and thread.interrupted.
func TestCancelAppShutdownCause(t *testing.T) {
	e := newE2E(t)
	model := &holdModel{held: make(chan struct{})}
	lookup := core.Tool("lookup_order", "Look up an order.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	e.agent = core.New(model, core.Name("acme-support"),
		core.TracerProvider(e.p.TracerProvider()), core.LoggerProvider(e.p.LoggerProvider()), lookup)

	shutdown := runtime.Install(runtime.Studio(e.ts.URL, ""), runtime.Agents(e.agent), runtime.Enabled(true))
	rt := e.runtimeID(t)
	if code, resp := e.api(t, http.MethodPost, "/api/playground/runs", fmt.Sprintf(
		`{"command_id":"cmd_shutdown","runtime":%q,"agent":"acme-support","input":"a slow question",
		  "engine":"live","side_effects":"substitute","thread":"ephemeral"}`, rt)); code != http.StatusAccepted {
		shutdown()
		t.Fatalf("command = %d %s", code, resp)
	}
	row := e.waitCommand(t, "cmd_shutdown", "accepted")
	var st struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(row), &st); err != nil || !strings.HasPrefix(st.RunID, "pg_") {
		shutdown()
		t.Fatalf("accepted row = %s (%v)", row, err)
	}
	select {
	case <-model.held:
	case <-time.After(5 * time.Second):
		shutdown()
		t.Fatal("the run never reached its held model call")
	}

	shutdown() // the app exits: the link cancels every run it started
	if err := e.p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{`"weft.run.stopped_by":"canceled"`, `"weft.cancel.cause":"app.shutdown"`}
	spans := ""
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, spans = e.api(t, http.MethodGet, "/api/runs/"+st.RunID+"/spans", "")
		spans = strings.ReplaceAll(spans, " ", "")
		if strings.Contains(spans, want[0]) && strings.Contains(spans, want[1]) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, w := range want {
		if !strings.Contains(spans, w) {
			t.Errorf("the run dropped at shutdown: spans lack %s: %s", w, spans)
		}
	}
}
