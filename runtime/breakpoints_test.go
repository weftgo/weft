package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/thread"
)

// runtime.Breakpoints (plan F4.2): the debugger's breakpoints park
// tools on the app's own runs of an agent built with the option — a
// core.ParkSource (ADR 0030) bound lazily to the process's link.

// breakAgent is the approvals example's agent without its gate: deploy
// is an ordinary tool, parked only by a breakpoint. It counts its runs.
func breakAgent(e *e2e, ran *atomic.Int64, turns ...wefttest.Turn) *core.Agent {
	if len(turns) == 0 {
		turns = []wefttest.Turn{
			wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
			wefttest.Say("Deployed. The change is live."),
		}
	}
	return core.New(
		wefttest.Script(turns...),
		core.Name("approvals-example"),
		core.TracerProvider(e.p.TracerProvider()),
		core.LoggerProvider(e.p.LoggerProvider()),
		core.Tool("deploy", "Deploy the service.",
			func(ctx context.Context, in struct {
				Env string `json:"env"`
			}) (string, error) {
				ran.Add(1)
				call, _ := core.CallFromContext(ctx)
				return "deployed to " + in.Env + " (approved: " + fmt.Sprint(call.Approved) + ")", nil
			}),
		runtime.Breakpoints(),
	)
}

// setBreakpoints PUTs the debugger's set and waits until the link holds
// it (the frame reaches the runtime over its command stream).
func (e *e2e) setBreakpoints(t *testing.T, rt string, tools ...string) {
	t.Helper()
	b, _ := json.Marshal(map[string][]string{"tools": append([]string{}, tools...)})
	if code, resp := e.api(t, http.MethodPut, "/api/runtimes/"+rt+"/breakpoints", string(b)); code != http.StatusOK {
		t.Fatalf("PUT breakpoints %v = %d %s", tools, code, resp)
	}
	want := slices.Sorted(slices.Values(tools))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := runtime.BreakpointSource(context.Background()); slices.Equal(got, want) || (len(want) == 0 && got == nil) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the link never held breakpoints %v (has %v)", want, runtime.BreakpointSource(context.Background()))
}

// TestBreakpointParksTheAppsOwnRun is F4.2's share of the done line: an
// agent built with runtime.Breakpoints(), a breakpoint PUT on deploy, an
// app-started thread turn — the run parks on deploy (RunFinish.Pending),
// the session holds it pending, POST /api/runs/{run}/approvals approves
// it through runtime.Sessions, and the turn continues: deploy ran once,
// approved. /api/runtimes reports breakpoints_own: true and meta
// debug_scope own+runtime.
func TestBreakpointParksTheAppsOwnRun(t *testing.T) {
	e := newE2E(t)
	var ran atomic.Int64
	agent := breakAgent(e, &ran)
	var (
		mu   sync.Mutex
		live *thread.Session
	)
	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		mu.Lock()
		defer mu.Unlock()
		if live == nil || publicID != "pub_bp" {
			return nil, nil, nil
		}
		return live, nil, nil
	}
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Sessions(resolve), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	if _, body := e.api(t, http.MethodGet, "/api/runtimes", ""); !strings.Contains(body, `"breakpoints_own":true`) {
		t.Errorf("/api/runtimes does not report breakpoints_own: true: %s", body)
	}
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"debug_scope":"own+runtime"`) {
		t.Errorf("meta debug_scope is not own+runtime: %s", body)
	}
	e.setBreakpoints(t, e.runtimeID(t), "deploy")

	ctx := context.Background()
	s, err := thread.Create(ctx, e.store, agent, thread.PublicID("pub_bp"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	mu.Lock()
	live = s
	mu.Unlock()
	var finish *core.RunFinish
	parked, err := s.Send(ctx, core.User("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	for ev, err := range parked.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(core.RunFinish); ok {
			finish = &f
		}
	}
	res, err := parked.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if finish == nil || len(finish.Pending) != 1 || finish.Pending[0].Name != "deploy" || len(res.Pending) != 1 {
		t.Fatalf("the app's turn did not park on the breakpoint: finish %+v, pending %+v", finish, res.Pending)
	}
	if ran.Load() != 0 {
		t.Fatal("deploy ran through its breakpoint")
	}
	if p := s.Pending(); len(p) != 1 || p[0].CallID != res.Pending[0].ID {
		t.Fatalf("the session's pending = %+v, want the parked deploy", p)
	}
	callID := res.Pending[0].ID
	e.waitStored(t, parked.RunID())

	code, body := e.api(t, http.MethodPost, "/api/runs/"+parked.RunID()+"/approvals",
		`{"call_id":"`+callID+`","decision":"approve"}`)
	if code != http.StatusAccepted {
		t.Fatalf("approvals on the app's parked turn = %d %s, want 202", code, body)
	}
	if row := e.waitCommand(t, commandOf(t, body), "finished"); !strings.Contains(row, `"status":"succeeded"`) {
		t.Fatalf("the decision's command = %s, want finished succeeded", row)
	}
	resumed := parked.Next()
	if resumed == nil {
		t.Fatal("the parked turn has no Next: the decision did not resume it")
	}
	res2, err := resumed.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if res2.Text() != "Deployed. The change is live." || len(res2.Pending) != 0 {
		t.Errorf("the resumed turn answered %q, pending %+v", res2.Text(), res2.Pending)
	}
	if ran.Load() != 1 {
		t.Errorf("deploy ran %d times, want exactly once", ran.Load())
	}
	found := false
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok && tr.CallID == callID {
				found = !tr.IsError && strings.Contains(tr.Content, "deployed to prod (approved: true)")
			}
		}
	}
	if !found {
		t.Errorf("the transcript does not carry the approved deploy's result: %+v", s.Context())
	}
}

// TestBreakpointClearedParksNothing: a set breakpoint parks the app's
// run; cleared, the next app run of the same agent does not park. The
// runtime here has no Sessions: breakpoints_own alone makes debug_scope
// own+runtime.
func TestBreakpointClearedParksNothing(t *testing.T) {
	e := newE2E(t)
	var ran atomic.Int64
	agent := breakAgent(e, &ran,
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.Say("Deployed."))
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"debug_scope":"own+runtime"`) {
		t.Errorf("meta debug_scope with breakpoints_own alone is not own+runtime: %s", body)
	}
	rt := e.runtimeID(t)
	ctx := context.Background()

	e.setBreakpoints(t, rt, "deploy")
	res, err := agent.Generate(ctx, core.Prompt("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || ran.Load() != 0 {
		t.Fatalf("with the breakpoint set: pending %+v, ran %d; want deploy parked", res.Pending, ran.Load())
	}

	e.setBreakpoints(t, rt)
	res, err = agent.Generate(ctx, core.Prompt("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 0 || ran.Load() != 1 || res.Text() != "Deployed." {
		t.Errorf("after the clear: pending %+v, ran %d, text %q; want deploy run, nothing parked", res.Pending, ran.Load(), res.Text())
	}

	// A stopped link's set no longer parks: the runtime cannot decide.
	e.setBreakpoints(t, rt, "deploy")
	shutdown()
	if got := runtime.BreakpointSource(ctx); got != nil {
		t.Errorf("after the link stopped the source answers %v, want nil", got)
	}
}

// TestBreakpointOnPlainGenerate: an app run outside a thread session
// parks under a breakpoint with res.Pending, and only the app resumes it
// — Generate again with weft.Approve. The tool runs once.
func TestBreakpointOnPlainGenerate(t *testing.T) {
	e := newE2E(t)
	var ran atomic.Int64
	agent := breakAgent(e, &ran)
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	e.setBreakpoints(t, e.runtimeID(t), "deploy")

	ctx := context.Background()
	res, err := agent.Generate(ctx, core.Prompt("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "deploy" || ran.Load() != 0 {
		t.Fatalf("pending %+v, ran %d; want deploy parked", res.Pending, ran.Load())
	}
	res, err = agent.Generate(ctx, core.Messages(res.Messages...), core.Approve(res.Pending[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 1 || len(res.Pending) != 0 || res.Text() != "Deployed. The change is live." {
		t.Errorf("after the app's Approve: ran %d, pending %+v, text %q", ran.Load(), res.Pending, res.Text())
	}
}

// TestBreakpointWithoutSessionsSkipsThreadTurns is the wedge's
// regression test: Breakpoints without runtime.Sessions (and, below,
// with Sessions but a session with no public id). A thread turn
// would park with nobody able to decide it — Studio refuses an
// app-owned turn without Sessions, the app has no approval UI — and
// every later Send would queue behind the open boundary (ADR 0021 §5),
// clearing the breakpoint releasing nothing. So the source answers
// nothing for a run carrying weft.session.id: the turn runs deploy,
// nothing pending, nothing queued, and the next Send runs too. A plain
// Generate (no session id) still parks: the app resumes it.
func TestBreakpointWithoutSessionsSkipsThreadTurns(t *testing.T) {
	e := newE2E(t)
	var ran atomic.Int64
	agent := breakAgent(e, &ran,
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.Say("Deployed."),
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.Say("Deployed again."),
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
	)
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	e.setBreakpoints(t, e.runtimeID(t), "deploy")
	ctx := context.Background()
	if got := runtime.BreakpointSource(core.WithMetadata(ctx, map[string]string{"weft.session.id": "s1"})); got != nil {
		t.Errorf("the source answers %v for a session run without Sessions, want nil", got)
	}

	s, err := thread.Create(ctx, e.store, agent, thread.PublicID("pub_wedge"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	for i, want := range []string{"Deployed.", "Deployed again."} {
		turn, err := s.Send(ctx, core.User("Deploy to prod."))
		if err != nil {
			t.Fatal(err)
		}
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Pending) != 0 || res.Text() != want || int(ran.Load()) != i+1 {
			t.Fatalf("turn %d: pending %+v, text %q, deploy ran %d; want no park", i+1, res.Pending, res.Text(), ran.Load())
		}
		if err := s.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		if p, q := s.Pending(), s.Queue(); len(p) != 0 || len(q) != 0 {
			t.Fatalf("turn %d: the session holds pending %d, queue %d; want 0, 0", i+1, len(p), len(q))
		}
	}

	// The app's plain Generate still parks: the app resumes it itself.
	res, err := agent.Generate(ctx, core.Prompt("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "deploy" || ran.Load() != 2 {
		t.Errorf("plain Generate: pending %+v, ran %d; want deploy parked", res.Pending, ran.Load())
	}
}

// TestBreakpointSessionWithoutPublicIDSkipped: Sessions set, but the
// session has no thread.PublicID — Studio decides app turns by public
// id, so nobody outside the app could decide the park: the source
// answers nothing and the turn runs deploy, nothing pending or queued.
// The same process's session with a public id still parks (as in
// TestBreakpointParksTheAppsOwnRun, which decides it through Sessions).
func TestBreakpointSessionWithoutPublicIDSkipped(t *testing.T) {
	e := newE2E(t)
	var ran atomic.Int64
	agent := breakAgent(e, &ran,
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.Say("Deployed."),
		wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
	)
	resolve := func(context.Context, string) (*thread.Session, func(), error) { return nil, nil, nil }
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Sessions(resolve), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	e.setBreakpoints(t, e.runtimeID(t), "deploy")
	ctx := context.Background()
	if got := runtime.BreakpointSource(core.WithMetadata(ctx, map[string]string{"weft.session.id": "s1"})); got != nil {
		t.Errorf("the source answers %v for a session run with no public id, want nil", got)
	}
	if got := runtime.BreakpointSource(core.WithMetadata(ctx, map[string]string{"weft.session.id": "s1", "weft.public_id": "pub_x"})); !slices.Equal(got, []string{"deploy"}) {
		t.Errorf("the source answers %v for a session run with a public id under Sessions, want [deploy]", got)
	}

	s, err := thread.Create(ctx, e.store, agent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	turn, err := s.Send(ctx, core.User("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if p, q := s.Pending(), s.Queue(); len(res.Pending) != 0 || res.Text() != "Deployed." || ran.Load() != 1 || len(p) != 0 || len(q) != 0 {
		t.Fatalf("no public id: pending %+v, text %q, ran %d, session pending %d queue %d; want no park", res.Pending, res.Text(), ran.Load(), len(p), len(q))
	}

	pub, err := thread.Create(ctx, e.store, agent, thread.PublicID("pub_parks"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pub.Close(ctx) }()
	turn, err = pub.Send(ctx, core.User("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	if res, err = turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "deploy" || ran.Load() != 1 {
		t.Errorf("with a public id: pending %+v, ran %d; want deploy parked", res.Pending, ran.Load())
	}
}
