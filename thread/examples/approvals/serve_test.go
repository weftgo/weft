package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/scope"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread/jsonl"
)

// The served mode's proof (plan F4.3, F4's done line): the example's
// own agent and app, under the API the panel drives — Studio embedded
// with the playground, the app's pipeline exporting to it, the runtime
// link in-process. With the two options a breakpoint parks the app's
// turn and POST /api/runs/{run}/approvals decides it: 202, the command
// finishes naming the resumed turn, the turn continues. Without
// runtime.Sessions the same verb is the 403 with the one-line fix.

// served is one Studio and the example's app wired to it.
type served struct {
	ts  *httptest.Server
	srv *studio.Server
	p   *otel.Pipeline
	app *app
}

func newServed(t *testing.T, build func(...weft.Option) *weft.Agent) *served {
	t.Helper()
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "studio.db")), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	st, err := jsonl.Open(filepath.Join(dir, "threads"))
	if err != nil {
		t.Fatal(err)
	}
	agent := build(weft.TracerProvider(p.TracerProvider()), weft.LoggerProvider(p.LoggerProvider()))
	return &served{ts: ts, srv: srv, p: p, app: &app{st: st, agent: agent}}
}

func (e *served) api(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// eventually polls f every few ms for up to 5 s.
func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// runtimeID waits for the connected runtime (registered and holding
// its command stream) and answers its id.
func (e *served) runtimeID(t *testing.T) string {
	t.Helper()
	id := ""
	eventually(t, "a runtime connects", func() bool {
		_, body := e.api(t, http.MethodGet, "/api/runtimes", "")
		var rts struct {
			Runtimes []struct {
				ID string `json:"id"`
			} `json:"runtimes"`
		}
		if json.Unmarshal([]byte(body), &rts) == nil {
			for _, rt := range rts.Runtimes {
				if e.srv.Runtime().Connected(rt.ID) {
					id = rt.ID
					return true
				}
			}
		}
		return false
	})
	return id
}

// ask is the page's form: POST /ask through the example's handler; the
// answer and the turn's run id (its Weft-Scope header).
func (e *served) ask(t *testing.T, question string) (string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.app.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ask", strings.NewReader(question)))
	run := scope.Parse(rec.Header().Get(scope.HeaderName)).RunID
	if rec.Code != http.StatusOK || run == "" {
		t.Fatalf("ask %q = %d %q (scope %q)", question, rec.Code, rec.Body.String(), rec.Header().Get(scope.HeaderName))
	}
	return rec.Body.String(), run
}

// stored waits until Studio holds the run's run_finish.
func (e *served) stored(t *testing.T, runID string) {
	t.Helper()
	if err := e.p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "run "+runID+" reaches Studio", func() bool {
		code, body := e.api(t, http.MethodGet, "/api/runs/"+runID+"/events", "")
		return code == http.StatusOK && strings.Contains(body, "run_finish")
	})
}

type command struct {
	State  string  `json:"state"`
	RunID  string  `json:"run_id"`
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

// decide posts one decision as the panel does and polls the command
// to its end, as the panel does.
func (e *served) decide(t *testing.T, runID, body string) command {
	t.Helper()
	code, resp := e.api(t, http.MethodPost, "/api/runs/"+runID+"/approvals", body)
	var queued struct {
		CommandID string `json:"command_id"`
		State     string `json:"state"`
		RunID     string `json:"run_id"`
	}
	if code != http.StatusAccepted || json.Unmarshal([]byte(resp), &queued) != nil || queued.State != "queued" || queued.RunID != runID {
		t.Fatalf("approvals on the app's turn = %d %s, want 202 queued", code, resp)
	}
	var c command
	eventually(t, "the decision's command ends", func() bool {
		_, row := e.api(t, http.MethodGet, "/api/playground/commands/"+queued.CommandID, "")
		return json.Unmarshal([]byte(row), &c) == nil && (c.State == "finished" || c.State == "rejected" || c.State == "lost")
	})
	return c
}

// TestServedTurnDecidedFromStudio: the example's served app with both
// options. A breakpoint PUT on status parks the app's status turn; the
// approve verb resumes it — the turn continues as the session's next
// run and the tool ran. Then the gated deploy parks, and a deny with a
// reason reaches the model.
func TestServedTurnDecidedFromStudio(t *testing.T) {
	e := newServed(t, servedAgent)
	defer e.app.install(runtime.Local(e.srv), runtime.Enabled(true))()
	rt := e.runtimeID(t)
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"debug_scope":"own+runtime"`) {
		t.Fatalf("meta debug_scope is not own+runtime: %s", body)
	}
	if _, body := e.api(t, http.MethodGet, "/api/runtimes", ""); !strings.Contains(body, `"sessions":true`) || !strings.Contains(body, `"breakpoints_own":true`) {
		t.Fatalf("/api/runtimes: %s", body)
	}
	if code, body := e.api(t, http.MethodPut, "/api/runtimes/"+rt+"/breakpoints", `{"tools":["status"]}`); code != http.StatusOK {
		t.Fatalf("PUT breakpoints = %d %s", code, body)
	}
	// The set reaches the link over its command stream: a plain
	// Generate of the same agent parks once it holds it.
	eventually(t, "the breakpoint reaches the app", func() bool {
		res, err := e.app.agent.Generate(context.Background(), weft.Prompt("status?"))
		return err == nil && len(res.Pending) == 1 && res.Pending[0].Name == "status"
	})

	answer, parked := e.ask(t, "status?")
	if !strings.Contains(answer, "awaiting approval: status (call call_status_1)") {
		t.Fatalf("the turn did not park on the breakpoint: %q", answer)
	}
	e.stored(t, parked)
	s := e.app.s
	defer func() { _ = s.Close(context.Background()) }()

	c := e.decide(t, parked, `{"call_id":"call_status_1","decision":"approve"}`)
	if c.State != "finished" || c.Status != "succeeded" || c.Error != nil {
		t.Fatalf("the approval = %+v, want finished succeeded", c)
	}
	if want := s.ID() + "-t2"; c.RunID != want {
		t.Errorf("the finished ack names %q, want the resumed turn %q", c.RunID, want)
	}
	if err := s.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lastText(s.Context()); got != "Done: all green." {
		t.Errorf("the resumed turn answered %q", got)
	}
	e.stored(t, c.RunID)
	if _, row := e.api(t, http.MethodGet, "/api/runs/"+c.RunID, ""); !strings.Contains(row, `"public_id":"pub_approvals"`) ||
		!strings.Contains(row, `"playground":false`) || !strings.Contains(row, `"turn":2`) {
		t.Errorf("the resumed run is not the app's turn 2: %s", row)
	}

	// The gate: deploy parks with no breakpoint; the deny's reason is
	// what the model reads.
	if code, body := e.api(t, http.MethodPut, "/api/runtimes/"+rt+"/breakpoints", `{"tools":[]}`); code != http.StatusOK {
		t.Fatalf("PUT breakpoints = %d %s", code, body)
	}
	answer, gated := e.ask(t, "deploy to prod")
	if !strings.Contains(answer, "awaiting approval: deploy (call call_deploy_2)") {
		t.Fatalf("deploy did not park: %q", answer)
	}
	e.stored(t, gated)
	c = e.decide(t, gated, `{"call_id":"call_deploy_2","decision":"deny","reason":"not on a Friday"}`)
	if c.State != "finished" || c.Error != nil {
		t.Fatalf("the denial = %+v", c)
	}
	if err := s.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lastText(s.Context()); !strings.Contains(got, "not on a Friday") {
		t.Errorf("the model did not read the denial's reason: %q", got)
	}
}

// TestServedWithoutSessionsIsReadOnly: the same app with neither
// option — the agent without runtime.Breakpoints(), runtime.Install
// without runtime.Sessions. Its turns stay the app's own: debug_scope
// says runtime-started runs and the verb is the 403 with the one-line
// fix the panel shows.
func TestServedWithoutSessionsIsReadOnly(t *testing.T) {
	e := newServed(t, opsAgent)
	defer runtime.Install(runtime.Local(e.srv), runtime.Agents(e.app.agent), runtime.Enabled(true))()
	e.runtimeID(t)
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"debug_scope":"runtime-started runs"`) {
		t.Fatalf("meta debug_scope: %s", body)
	}
	answer, parked := e.ask(t, "deploy to prod")
	if !strings.Contains(answer, "awaiting approval: deploy (call call_deploy_1)") {
		t.Fatalf("deploy did not park: %q", answer)
	}
	e.stored(t, parked)
	defer func() { _ = e.app.s.Close(context.Background()) }()
	code, body := e.api(t, http.MethodPost, "/api/runs/"+parked+"/approvals", `{"call_id":"call_deploy_1","decision":"approve"}`)
	var refusal struct {
		Error struct{ Code, Message string } `json:"error"`
		Fix   string                         `json:"fix"`
	}
	if code != http.StatusForbidden || json.Unmarshal([]byte(body), &refusal) != nil ||
		!strings.HasPrefix(refusal.Error.Message, "app-owned turn · read-only") ||
		!strings.HasPrefix(refusal.Fix, "set runtime.Sessions(resolver) at runtime.Install in the app") {
		t.Fatalf("approvals without Sessions = %d %s, want the 403 with the fix", code, body)
	}
	if p := e.app.s.Pending(); len(p) != 1 {
		t.Errorf("the turn is no longer parked: %+v", p)
	}
}

// lastText is the transcript's last assistant text.
func lastText(msgs []weft.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == weft.RoleAssistant {
			if txt := msgs[i].Text(); txt != "" {
				return txt
			}
		}
	}
	return ""
}
