package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core/wefttest"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// TestCancelRoute pins POST /api/runs/{id}/cancel (plan H6) against
// the fake runtime: a runtime-started run in flight is 202 with the
// golden body and the runtime receives a cancel frame for the command
// holding the run; the same run once its command finished is 409; a
// run whose stored row is over is 409 although its command still
// reads accepted; a parked run is 409 saying to decide its pending
// calls instead; an app's own run is 409 (F4); an unknown id is 404;
// a run whose runtime dropped its stream is 503. The route answers
// only under Playground(true): without it, no route.
func TestCancelRoute(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	cancel := func(runID string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/runs/"+runID+"/cancel", nil)
		pt.auth(req)
		return pt.do(t, req)
	}
	start := func(cmdID, runID string) {
		t.Helper()
		body := `{"runtime":"rt_test","agent":"acme-support","command_id":"` + cmdID + `","input":"go","public_id":"pub_7Hk2"}`
		if code, out := pt.post(t, body); code != http.StatusAccepted {
			t.Fatalf("enqueue %s = %d %s", cmdID, code, out)
		}
		pt.waitCommand(t, cmdID)
		pt.ack(t, `{"command_id":"`+cmdID+`","state":"accepted","run_id":"`+runID+`"}`)
	}

	// In flight: 202, and the cancel frame names the command.
	start("cmd_cancel_1", "pg_cancel_1")
	code, body := cancel("pg_cancel_1")
	if code != http.StatusAccepted {
		t.Fatalf("cancel = %d %s, want 202", code, body)
	}
	wefttest.Golden(t, "testdata/api/cancel.golden.json", []byte(indent(t, body)))
	select {
	case id := <-pt.cancels:
		if id != "cmd_cancel_1" {
			t.Errorf("cancel frame for %q, want cmd_cancel_1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the runtime never received the cancel frame")
	}

	// Its command finished: nothing in flight, 409.
	pt.ack(t, `{"command_id":"cmd_cancel_1","state":"finished","run_id":"pg_cancel_1","status":"failed","error":"context canceled"}`)
	if code, body := cancel("pg_cancel_1"); code != http.StatusConflict || !strings.Contains(body, "already finished") {
		t.Errorf("cancel after finish = %d %s, want 409 already finished", code, body)
	}

	// The stored row is over before the finished ack lands: 409.
	start("cmd_cancel_2", "pg_cancel_2")
	seedRun(t, pt.db, "pg_cancel_2", "pub_7Hk2", "", "", nil)
	if code, body := cancel("pg_cancel_2"); code != http.StatusConflict || !strings.Contains(body, "already finished (succeeded)") {
		t.Errorf("cancel of a stored finished run = %d %s, want 409", code, body)
	}

	// A parked run (its calls pending at the approval boundary): 409
	// saying to decide instead, before "already finished".
	start("cmd_cancel_3", "pg_cancel_3")
	seedRun(t, pt.db, "pg_cancel_3", "pub_7Hk2", "",
		`{"type":"run_finish","run_id":"pg_cancel_3","usage":{},"steps":1,"pending":[{"type":"tool_call","id":"call_1","name":"refund","args":{}}]}`, nil)
	if code, body := cancel("pg_cancel_3"); code != http.StatusConflict ||
		!strings.Contains(body, "run pg_cancel_3 is parked: decide its pending calls (approve/deny) instead") {
		t.Errorf("cancel of a parked run = %d %s, want 409 saying decide instead", code, body)
	}

	// The app's own run, and an unknown id.
	seedRun(t, pt.db, "app_run", "", "", "", nil)
	// No runtime of the run's service is connected at all: 403
	// "app-owned turn" with the fix that opens the link (plan F4.3: the
	// three verbs answer alike; one connected without Sessions is
	// runtime/sessions_test.go's TestAppTurnWithoutSessionsIsReadOnly).
	if code, body := cancel("app_run"); code != http.StatusForbidden ||
		!strings.Contains(body, `"message":"app-owned turn · read-only: no runtime of service svc is connected"`) ||
		!strings.Contains(body, `"fix":"open the runtime link in the app: WEFT_ENV=dev (or runtime.Enabled(true)) at runtime.Install, with runtime.Sessions(resolver)"`) {
		t.Errorf("cancel of the app's own run = %d %s, want 403 app-owned turn with the open-the-link fix", code, body)
	}
	if code, body := cancel("nope"); code != http.StatusNotFound {
		t.Errorf("cancel of an unknown run = %d %s, want 404", code, body)
	}

	// A runtime that dropped its stream holds the run: 503.
	reg, _ := json.Marshal(linkruntime.Registration{
		RuntimeID: "rt_gone",
		Agents: []linkruntime.AgentRegistration{{
			Name: "acme-support",
			Manifest: `{"weft":1,"agents":[{"name":"acme-support","model":{"provider":"wefttest","name":"script"},` +
				`"policy":{},"tools":[]}]}`,
			Limits:   linkruntime.AgentLimits{MaxSteps: 10, Parallelism: 4},
			Defaults: linkruntime.AgentDefaults{MaxSteps: 10, Parallelism: 4, ToolChoice: linkruntime.ToolChoice{Mode: "auto"}},
		}},
	})
	req, _ := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/runtime/register", strings.NewReader(string(reg)))
	req.Header.Set("Content-Type", "application/json")
	if code, out := pt.do(t, req); code != http.StatusOK {
		t.Fatalf("register rt_gone = %d %s", code, out)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	sreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, pt.ts.URL+"/api/runtime/commands?runtime=rt_gone", nil)
	stream, err := http.DefaultClient.Do(sreq)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, stream.Body) }()
	body = `{"runtime":"rt_gone","agent":"acme-support","command_id":"cmd_gone","input":"go"}`
	if code, out := pt.post(t, body); code != http.StatusAccepted {
		t.Fatalf("enqueue cmd_gone = %d %s", code, out)
	}
	pt.ack(t, `{"command_id":"cmd_gone","state":"accepted","run_id":"pg_gone"}`)
	stop()
	_ = stream.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for pt.rs.Connected("rt_gone") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if code, body := cancel("pg_gone"); code != http.StatusServiceUnavailable || !strings.Contains(body, "rt_gone") {
		t.Errorf("cancel with the runtime gone = %d %s, want 503 naming it", code, body)
	}
}
