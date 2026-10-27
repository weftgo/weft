package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/thread"
)

// The app's own turns as debugger targets (plan F4.1): runtime.Sessions
// hands the runtime the app's Session behind a public id, and Studio's
// approvals, steer and cancel on an app-owned run reach it as session
// commands. The thread approvals example's shape: a gated deploy parks
// a turn of a session the app created with a public id.

// deployAgent is the approvals example's agent, wired to the e2e
// pipeline: deploy is gated (RequireApproval), the script calls it,
// then answers.
func deployAgent(e *e2e) *core.Agent {
	return core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
			wefttest.Say("Deployed. The change is live."),
		),
		core.Name("approvals-example"),
		core.TracerProvider(e.p.TracerProvider()),
		core.LoggerProvider(e.p.LoggerProvider()),
		core.Tool("deploy", "Deploy the service.",
			func(ctx context.Context, in struct {
				Env string `json:"env"`
			}) (string, error) {
				call, _ := core.CallFromContext(ctx)
				return "deployed to " + in.Env + " (approved: " + fmt.Sprint(call.Approved) + ")", nil
			},
			core.RequireApproval()),
	)
}

// parkDeploy creates the app's session (public id pub), sends the
// deploy prompt, waits for the park and for Studio to hold the parked
// run's run_finish, and returns the session, the parked turn and the
// pending call's id.
func parkDeploy(t *testing.T, e *e2e, agent *core.Agent, pub string) (*thread.Session, *thread.Turn, string) {
	t.Helper()
	ctx := context.Background()
	s, err := thread.Create(ctx, e.store, agent, thread.PublicID(pub))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("the turn did not park: %+v", res.Pending)
	}
	e.waitStored(t, turn.RunID())
	return s, turn, res.Pending[0].ID
}

// waitStored waits until Studio holds runID's run_finish (the
// approvals route checks the pending set against it, and the run row
// names the service the verb is routed by).
func (e *e2e) waitStored(t *testing.T, runID string) {
	t.Helper()
	if err := e.p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if code, body := e.api(t, http.MethodGet, "/api/runs/"+runID+"/events", ""); code == http.StatusOK && strings.Contains(body, "run_finish") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s never reached Studio", runID)
}

// commandOf decodes a 202's command id.
func commandOf(t *testing.T, body string) string {
	t.Helper()
	var c struct {
		CommandID string `json:"command_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.CommandID == "" || c.State != "queued" {
		t.Fatalf("202 body = %s (%v)", body, err)
	}
	return c.CommandID
}

// TestAppSessionApprovalThroughSessions is F4.1's done line: the app's
// session parks a gated call on a turn the app started; with
// runtime.Sessions resolving the app's live Session, POST
// /api/runs/{run}/approvals answers 202, the command acks finished
// naming the resumed turn, and the turn continues — the tool ran
// approved, the answer is the script's, and the resumed run is the
// app's (its session and turn, never a playground run). Meta reports
// debug_scope own+runtime, /api/runtimes sessions: true.
func TestAppSessionApprovalThroughSessions(t *testing.T) {
	e := newE2E(t)
	agent := deployAgent(e)
	var (
		mu   sync.Mutex
		live *thread.Session
	)
	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		mu.Lock()
		defer mu.Unlock()
		if live == nil || publicID != "pub_f4" {
			return nil, nil, nil // not this app's
		}
		return live, nil, nil // the app's own writer: nothing to release
	}
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Sessions(resolve), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)

	if _, body := e.api(t, http.MethodGet, "/api/runtimes", ""); !strings.Contains(body, `"sessions":true`) {
		t.Errorf("/api/runtimes does not report sessions: true: %s", body)
	}
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"debug_scope":"own+runtime"`) {
		t.Errorf("meta debug_scope is not own+runtime: %s", body)
	}

	s, parked, callID := parkDeploy(t, e, agent, "pub_f4")
	defer func() { _ = s.Close(context.Background()) }()
	mu.Lock()
	live = s
	mu.Unlock()

	// The parked turn runs nothing: a steer or a cancel is refused at
	// once, 409, saying to decide instead (UX-H-19, U33.1-fix).
	for _, verb := range []string{"steer", "cancel"} {
		code, body := e.api(t, http.MethodPost, "/api/runs/"+parked.RunID()+"/"+verb, `{"message":"hurry"}`)
		if code != http.StatusConflict || !strings.Contains(body, "is parked: decide its pending calls (approve/deny) instead") {
			t.Errorf("%s on the parked app turn = %d %s, want 409 decide instead", verb, code, body)
		}
	}
	if p := s.Pending(); len(p) != 1 {
		t.Fatalf("a refused steer or cancel changed the park: %+v", p)
	}

	code, body := e.api(t, http.MethodPost, "/api/runs/"+parked.RunID()+"/approvals",
		`{"call_id":"`+callID+`","decision":"approve"}`)
	if code != http.StatusAccepted {
		t.Fatalf("approvals on the app's turn = %d %s, want 202", code, body)
	}
	row := e.waitCommand(t, commandOf(t, body), "finished")
	var st struct {
		State, RunID, Status string
		Error                *string
	}
	if err := json.Unmarshal([]byte(row), &struct {
		State  *string  `json:"state"`
		RunID  *string  `json:"run_id"`
		Status *string  `json:"status"`
		Error  **string `json:"error"`
	}{&st.State, &st.RunID, &st.Status, &st.Error}); err != nil {
		t.Fatal(err)
	}
	if st.State != "finished" || st.Status != "succeeded" || st.Error != nil {
		t.Fatalf("the decision's command = %s, want finished succeeded", row)
	}

	resumed := parked.Next()
	if resumed == nil {
		t.Fatal("the parked turn has no Next: the decision did not resume it")
	}
	if st.RunID != resumed.RunID() {
		t.Errorf("the finished ack names %q, the resumed turn is %q", st.RunID, resumed.RunID())
	}
	res, err := resumed.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res.Text() != "Deployed. The change is live." {
		t.Errorf("the resumed turn answered %q", res.Text())
	}
	found := false
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok && tr.CallID == callID {
				found = strings.Contains(tr.Content, "deployed to prod (approved: true)") && !tr.IsError
			}
		}
	}
	if !found {
		t.Errorf("the transcript does not carry the approved tool's result: %+v", s.Context())
	}

	// The resumed run is the app's: its session's turn, never a
	// playground run, and Studio does not take it for a runtime-started
	// run (no steer or cancel routes through the runtime's own path).
	e.waitStored(t, resumed.RunID())
	_, runJSON := e.api(t, http.MethodGet, "/api/runs/"+resumed.RunID(), "")
	if !strings.Contains(runJSON, `"session_id":"`+s.ID()+`"`) || !strings.Contains(runJSON, `"public_id":"pub_f4"`) ||
		!strings.Contains(runJSON, `"playground":false`) || !strings.Contains(runJSON, `"turn":2`) {
		t.Errorf("the resumed run is not the app's turn 2: %s", runJSON)
	}
	if _, started := e.srv.Runtime().RuntimeOf(resumed.RunID()); started {
		t.Error("Studio took the resumed app turn for a runtime-started run")
	}
}

// TestAppSessionLockedRefusal: the resolver opens a second Session on
// the app's store while the app's own Session holds the writer lease.
// The runtime claims the lease before it acks, so the command is
// refused — rejected, with exactly "the app is writing this session;
// fork instead" — and nothing is written: the session holds the same
// entries, the call is still pending, and the resolver's release ran.
func TestAppSessionLockedRefusal(t *testing.T) {
	e := newE2E(t)
	agent := deployAgent(e)
	var (
		mu       sync.Mutex
		id       string
		released int
	)
	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		mu.Lock()
		sid := id
		mu.Unlock()
		if sid == "" || publicID != "pub_locked" {
			return nil, nil, nil
		}
		opened, err := thread.Open(ctx, e.store, sid, agent)
		if err != nil {
			return nil, nil, err
		}
		return opened, func() {
			_ = opened.Close(context.Background())
			mu.Lock()
			released++
			mu.Unlock()
		}, nil
	}
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Sessions(resolve), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)

	s, parked, callID := parkDeploy(t, e, agent, "pub_locked")
	defer func() { _ = s.Close(context.Background()) }()
	mu.Lock()
	id = s.ID()
	mu.Unlock()
	_, before, _, err := e.store.Load(context.Background(), s.ID())
	if err != nil {
		t.Fatal(err)
	}

	code, body := e.api(t, http.MethodPost, "/api/runs/"+parked.RunID()+"/approvals",
		`{"call_id":"`+callID+`","decision":"approve"}`)
	if code != http.StatusAccepted {
		t.Fatalf("approvals = %d %s, want 202 (the refusal arrives on the command)", code, body)
	}
	row := e.waitCommand(t, commandOf(t, body), "rejected")
	var st struct {
		State string  `json:"state"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal([]byte(row), &st); err != nil {
		t.Fatal(err)
	}
	if st.State != "rejected" || st.Error == nil || *st.Error != "the app is writing this session; fork instead" {
		t.Fatalf("the command = %s, want rejected with exactly the locked sentence", row)
	}
	_, after, _, err := e.store.Load(context.Background(), s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("a refused command wrote: %d entries, had %d", len(after), len(before))
	}
	if p := s.Pending(); len(p) != 1 || p[0].CallID != callID {
		t.Errorf("the call is no longer pending: %+v", p)
	}
	if parked.Next() != nil {
		t.Error("the parked turn resumed")
	}
	mu.Lock()
	n := released
	mu.Unlock()
	if n != 1 {
		t.Errorf("the resolver's release ran %d times, want 1", n)
	}
}

// TestAppTurnWithoutSessionsIsReadOnly: a runtime installed without
// runtime.Sessions leaves the app's turns read-only. Approvals, steer
// and cancel on an app-owned run answer alike — 403 "app-owned turn"
// with the fix naming runtime.Sessions (H6.1's alignment) — meta's
// debug_scope stays "runtime-started runs", the registration says
// sessions: false, and the turn stays parked.
func TestAppTurnWithoutSessionsIsReadOnly(t *testing.T) {
	e := newE2E(t)
	agent := deployAgent(e)
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)

	if _, body := e.api(t, http.MethodGet, "/api/runtimes", ""); !strings.Contains(body, `"sessions":false`) {
		t.Errorf("/api/runtimes does not report sessions: false: %s", body)
	}
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"debug_scope":"runtime-started runs"`) {
		t.Errorf("meta debug_scope: %s", body)
	}

	s, parked, callID := parkDeploy(t, e, agent, "pub_ro")
	defer func() { _ = s.Close(context.Background()) }()
	for _, verb := range []struct{ path, body string }{
		{"/approvals", `{"call_id":"` + callID + `","decision":"approve"}`},
		{"/steer", `{"message":"hurry"}`},
		{"/cancel", ""},
	} {
		code, body := e.api(t, http.MethodPost, "/api/runs/"+parked.RunID()+verb.path, verb.body)
		if code != http.StatusForbidden || !strings.Contains(body, "app-owned turn") ||
			!strings.Contains(body, `"fix":"set runtime.Sessions(resolver)`) || !strings.Contains(body, `its turns then take approvals and steer"`) {
			t.Errorf("%s on the app's turn = %d %s, want 403 app-owned turn with the runtime.Sessions fix", verb.path, code, body)
		}
	}
	if p := s.Pending(); len(p) != 1 {
		t.Errorf("the turn is no longer parked: %+v", p)
	}
}

// TestAppSessionSteerAndCancel: with runtime.Sessions, a steer on an
// app turn with no turn running now fails "no running turn to steer"
// (thread.Session.Steer: ErrNotRunning, nothing written), a steer on the running turn is
// delivered through the app's Session (thread's Steer policy: the turn
// sees the message and the command finishes), and cancel is refused
// "cancel is not supported on an app turn" — thread has no per-turn
// cancel. A public id the resolver does not know is "no session for
// this public id".
func TestAppSessionSteerAndCancel(t *testing.T) {
	e := newE2E(t)
	gate := make(chan struct{})
	held := make(chan struct{})
	var once sync.Once
	var seen []string
	var seenMu sync.Mutex
	agent := core.New(&steerModel{gate: gate, held: held, once: &once, seen: &seen, mu: &seenMu},
		core.Name("steer-app"),
		core.TracerProvider(e.p.TracerProvider()),
		core.LoggerProvider(e.p.LoggerProvider()),
		core.Tool("lookup", "Look something up.", func(ctx context.Context, in struct{}) (string, error) { return "found", nil }),
	)
	var live *thread.Session
	var liveMu sync.Mutex
	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		liveMu.Lock()
		defer liveMu.Unlock()
		if publicID != "pub_steer" || live == nil {
			return nil, nil, nil
		}
		return live, nil, nil
	}
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Sessions(resolve), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)

	ctx := context.Background()
	s, err := thread.Create(ctx, e.store, agent, thread.PublicID("pub_steer"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	liveMu.Lock()
	live = s
	liveMu.Unlock()
	turn, err := s.Send(ctx, core.User("look it up"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached its held model call")
	}
	// The run is in flight: its records reach Studio as they happen.
	if err := e.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if code, _ := e.api(t, http.MethodGet, "/api/runs/"+turn.RunID(), ""); code == http.StatusOK {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Cancel: refused at once, 409 in the runtime's words (UX-H-19) —
	// no command is queued, nothing changes.
	code, body := e.api(t, http.MethodPost, "/api/runs/"+turn.RunID()+"/cancel", "")
	if code != http.StatusConflict || !strings.Contains(body, "cancel is not supported on an app turn") {
		t.Fatalf("cancel = %d %s, want 409 cancel is not supported on an app turn", code, body)
	}

	// A public id the resolver does not answer, while the turn runs:
	// the runtime's refusal on the command's row.
	liveMu.Lock()
	live = nil
	liveMu.Unlock()
	code, body = e.api(t, http.MethodPost, "/api/runs/"+turn.RunID()+"/steer", `{"message":"anyone?"}`)
	if code != http.StatusAccepted {
		t.Fatalf("steer = %d %s", code, body)
	}
	row := e.waitCommand(t, commandOf(t, body), "rejected")
	if !strings.Contains(row, `"error":"no session for this public id"`) {
		t.Errorf("unknown public id's command = %s", row)
	}
	liveMu.Lock()
	live = s
	liveMu.Unlock()

	// Steer the running turn: delivered at its next drain point.
	code, body = e.api(t, http.MethodPost, "/api/runs/"+turn.RunID()+"/steer", `{"message":"also check the refund"}`)
	if code != http.StatusAccepted {
		t.Fatalf("steer = %d %s", code, body)
	}
	row = e.waitCommand(t, commandOf(t, body), "finished")
	if !strings.Contains(row, `"status":"succeeded"`) {
		t.Fatalf("steer's command = %s", row)
	}
	close(gate)
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	seenMu.Lock()
	got := strings.Join(seen, "|")
	seenMu.Unlock()
	if !strings.Contains(got, "also check the refund") {
		t.Errorf("the turn never saw the steer: %s", got)
	}

	// The turn is over and its row says so: a second steer has nothing
	// to steer into — refused at once, 409 in the runtime's words
	// (UX-H-19), no command queued, nothing written. (A row that still
	// reads running goes to the runtime, whose Session.Steer decides it
	// under the session's lock — sessions_internal_test.go's late steer.)
	e.waitStored(t, turn.RunID())
	entries := len(s.Entries())
	code, body = e.api(t, http.MethodPost, "/api/runs/"+turn.RunID()+"/steer", `{"message":"too late"}`)
	if code != http.StatusConflict || !strings.Contains(body, "no running turn to steer") || !strings.Contains(body, "already finished (succeeded)") {
		t.Fatalf("late steer = %d %s, want 409 no running turn to steer", code, body)
	}
	time.Sleep(20 * time.Millisecond)
	if len(s.Entries()) != entries {
		t.Error("a refused steer wrote to the session")
	}
}

// steerModel calls lookup, then holds its second call (held closed,
// waiting on gate) before answering; every later call answers too. It
// records the user texts of every request — what the turn was fed.
type steerModel struct {
	mu    *sync.Mutex
	seen  *[]string
	calls int
	gate  chan struct{}
	held  chan struct{}
	once  *sync.Once
}

func (m *steerModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "steer"}
}

func (m *steerModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.mu.Lock()
	m.calls++
	n := m.calls
	for _, msg := range req.Messages {
		if msg.Role != core.RoleUser {
			continue
		}
		for _, p := range msg.Content {
			if tp, ok := p.(core.TextPart); ok {
				*m.seen = append(*m.seen, tp.Text)
			}
		}
	}
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		switch n {
		case 1:
			if !yield(core.ModelToolCall{ID: "c_1", Name: "lookup", Args: []byte(`{}`)}, nil) {
				return
			}
			yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
			return
		case 2:
			m.once.Do(func() { close(m.held) })
			select {
			case <-m.gate:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		if !yield(core.ModelTextDelta{Text: fmt.Sprintf("answer %d", n)}, nil) {
			return
		}
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}
