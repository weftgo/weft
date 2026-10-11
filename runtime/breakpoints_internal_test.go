package runtime

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
)

// TestBreakpointsOptionAbsentChangesNothing is runtime.Breakpoints'
// behaviour-identity golden (§1.1): the same app turn — a lookup and an
// answer — is byte-identical in events, entries, transcript, usage, run
// metadata and every span's weft.* attributes whether the agent was
// built without the option; with it and no Install; with it and an
// Install not enabled; or with it and a live, connected runtime holding
// no breakpoint set — and with a set that names no tool the turn calls.
func TestBreakpointsOptionAbsentChangesNothing(t *testing.T) {
	noInstall := func(*core.Agent) func() { return func() {} }
	connected := func(set ...string) func(agent *core.Agent) func() {
		return func(agent *core.Agent) func() {
			srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "studio.db")), studio.Playground(true))
			shutdown := Install(Local(srv), Agents(agent), Enabled(true))
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				up := false
				for _, v := range srv.Runtime().Snapshot() {
					up = up || v.Connected
				}
				if up {
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
			if len(set) > 0 {
				liveLinks.mu.Lock()
				for l := range liveLinks.links {
					l.setBreakpoints(set)
				}
				liveLinks.mu.Unlock()
				if got := breakpointSource(context.Background()); !reflect.DeepEqual(got, set) {
					t.Fatalf("source = %v, want %v", got, set)
				}
			} else if got := breakpointSource(context.Background()); got != nil {
				t.Fatalf("a link with no breakpoints answers %v", got)
			}
			return func() { shutdown(); _ = srv.Close() }
		}
	}
	plain := runAppTurn(t, noInstall)
	if len(plain.Events) == 0 || len(plain.Entries) == 0 || len(plain.Spans) == 0 || plain.ToolMetadata == nil {
		t.Fatalf("nothing recorded: %+v", plain)
	}
	for name, run := range map[string]identityRun{
		"option, no Install": runAppTurn(t, noInstall, Breakpoints()),
		"option, Install not enabled": runAppTurn(t, func(agent *core.Agent) func() {
			srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "s.db")))
			shutdown := Install(Local(srv), Agents(agent), Enabled(false))
			return func() { shutdown(); _ = srv.Close() }
		}, Breakpoints()),
		"option, connected, no breakpoints":    runAppTurn(t, connected(), Breakpoints()),
		"option, connected, an unrelated tool": runAppTurn(t, connected("refund"), Breakpoints()),
	} {
		if !reflect.DeepEqual(plain, run) {
			t.Errorf("%s changed the app's turn:\nplain %+v\ngot   %+v", name, plain, run)
		}
	}
	if breakpointSource(context.Background()) != nil {
		t.Error("a stopped link still answers breakpoints")
	}
}

// TestBreakpointParkDecisionIsTheAppsDecision is the acting path's
// identity golden for a breakpoint park (the F4.1 pattern): an app turn
// parked by a breakpoint and approved through runtime.Sessions resumes
// exactly as one the app approved itself with s.Decide — events,
// entries (who decided aside), run metadata and weft.* span attributes.
func TestBreakpointParkDecisionIsTheAppsDecision(t *testing.T) {
	ctx := context.Background()
	own := parkAndDecideBy(t, true, func(s *thread.Session, _ *thread.Turn, callID string) *thread.Turn {
		turn, err := s.Decide(ctx, thread.Approve(callID))
		if err != nil {
			t.Fatal(err)
		}
		return turn
	})
	viaRuntime := parkAndDecideBy(t, true, func(s *thread.Session, parked *thread.Turn, callID string) *thread.Turn {
		f := newFakeStudio(t)
		ts := httptest.NewServer(f.handler())
		t.Cleanup(ts.Close)
		newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return s, nil, nil })
		f.frames <- sessionFrame(sessionCommand{CommandID: "cmd_same", Verb: verbDecide, PublicID: "pub_park",
			SessionID: s.ID(), RunID: parked.RunID(), CallID: callID, Decision: "approve", Actor: "devtools"})
		f.waitAck(t, 2)
		f.mu.Lock()
		fin := f.acks[1]
		f.mu.Unlock()
		if fin.Status != "succeeded" {
			t.Fatalf("decision ack = %+v", fin)
		}
		return parked.Next()
	})
	if len(own.Spans) == 0 || len(own.Metadata) != 2 {
		t.Fatalf("nothing recorded: %+v", own)
	}
	for name, pair := range map[string][2]any{
		"events": {own.Events, viaRuntime.Events}, "entries": {own.Entries, viaRuntime.Entries},
		"metadata": {own.Metadata, viaRuntime.Metadata}, "spans": {own.Spans, viaRuntime.Spans},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("the runtime's decision differs from the app's in its %s:\napp     %v\nruntime %v", name, pair[0], pair[1])
		}
	}
}

// TestBreakpointsRegistration: the registration reports breakpoints_own
// only when an agent given to Agents carries Breakpoints() — recognised
// among its other park sources — and nothing else in it changes.
func TestBreakpointsRegistration(t *testing.T) {
	other := core.ParkSource(func(context.Context) []string { return nil })
	reg := func(opts ...core.Option) registration {
		agent := core.New(wefttest.Script(), append([]core.Option{core.Name("acme-support")}, opts...)...)
		cfg := &config{agents: []*core.Agent{agent}}
		r := newRegistry(cfg).registration("rt_x")
		r.Agents = nil // the manifest hash differs by construction; the agents are the manifest's
		return r
	}
	without, otherSrc, with := reg(), reg(other), reg(other, Breakpoints())
	if without.BreakpointsOwn || otherSrc.BreakpointsOwn || !with.BreakpointsOwn {
		t.Fatalf("breakpoints_own: without %v, another source %v, with %v; want false, false, true",
			without.BreakpointsOwn, otherSrc.BreakpointsOwn, with.BreakpointsOwn)
	}
	with.BreakpointsOwn = false
	if !reflect.DeepEqual(without, with) {
		t.Errorf("the option changed more of the registration:\nwithout %+v\nwith    %+v", without, with)
	}
	if carriesBreakpoints(nil) {
		t.Error("a nil agent carries breakpoints")
	}
}

// playgroundLink is a link (not started) exposing agent, with allow
// opted in for the named tools, live in the registry with the
// breakpoint set.
func playgroundLink(t *testing.T, agent *core.Agent, allow []string, breaks ...string) *link {
	t.Helper()
	cfg := &config{agents: []*core.Agent{agent}, models: map[string]core.Model{}, allow: map[string]bool{}}
	for _, a := range allow {
		cfg.allow[a] = true
	}
	l := newLink(cfg, newRegistry(cfg), "http://unused.invalid", "")
	l.setBreakpoints(breaks)
	addLiveLink(l)
	t.Cleanup(func() { removeLiveLink(l) })
	return l
}

// allowCommand is a live ephemeral playground command under
// side_effects allow.
func allowCommand(t *testing.T, l *link, id string) command {
	t.Helper()
	in := "go"
	cmd := command{CommandID: id, Agent: "acme-support", Input: &in, Engine: "live", SideEffects: "allow", Thread: "ephemeral"}
	if reason, ok := l.validate(context.Background(), &cmd); !ok {
		t.Fatalf("validate: %s", reason)
	}
	return cmd
}

// TestBreakpointSourceSkipsPlaygroundRuns: a playground run keeps its
// pre-F4.2 breakpoint rule — the link's set filtered to the agent's
// static tools, as a ParkOn on its override fingerprint — even when the
// agent carries Breakpoints(): under allow, a breakpoint on a tool only
// a ToolSource supplies does not park it (the source answers nothing
// for a run carrying weft.playground), and neither does a Subagent
// child built with the option re-read the set under a playground
// parent.
func TestBreakpointSourceSkipsPlaygroundRuns(t *testing.T) {
	ctx := context.Background()
	if got := breakpointSource(core.WithMetadata(ctx, map[string]string{"weft.playground": "true"})); got != nil {
		t.Fatalf("source under weft.playground = %v, want nil", got)
	}

	t.Run("ToolSource tool", func(t *testing.T) {
		var wired atomic.Int64
		wire := core.Tool("wire_money", "Send a payment.", func(context.Context, struct{}) (string, error) {
			wired.Add(1)
			return "sent", nil
		})
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wire_money", ID: "c1"}),
			wefttest.Say("paid"),
		), core.Name("acme-support"), core.ToolSource(func() []*core.ToolDef { return []*core.ToolDef{wire} }), Breakpoints())
		l := playgroundLink(t, agent, []string{"wire_money"}, "wire_money")
		status, _, errText := l.execute(ctx, allowCommand(t, l, "cmd_ts"), "pg_ts")
		if status != "succeeded" {
			t.Fatalf("run = %s %s", status, errText)
		}
		if wired.Load() != 1 || l.parked["pg_ts"] != nil {
			t.Errorf("wire_money ran %d times, parked %v; want it run (the playground's own rule, unchanged)", wired.Load(), l.parked["pg_ts"] != nil)
		}
	})

	t.Run("Subagent child", func(t *testing.T) {
		var wired atomic.Int64
		child := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wire_money", ID: "k1"}),
			wefttest.Say("wired"),
		), core.Tool("wire_money", "Send a payment.", func(context.Context, struct{}) (string, error) {
			wired.Add(1)
			return "sent", nil
		}), Breakpoints())
		parent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "treasury", Args: `{"prompt":"pay"}`, ID: "c1"}),
			wefttest.Say("paid"),
		), core.Name("acme-support"), core.Subagent("treasury", "Moves money.", child))
		l := playgroundLink(t, parent, []string{"treasury", "wire_money"}, "wire_money")
		status, _, errText := l.execute(ctx, allowCommand(t, l, "cmd_sub"), "pg_sub")
		if status != "succeeded" {
			t.Fatalf("run = %s %s", status, errText)
		}
		if wired.Load() != 1 || l.parked["pg_sub"] != nil {
			t.Errorf("the child's wire_money ran %d times, parked %v; want it run under the playground parent", wired.Load(), l.parked["pg_sub"] != nil)
		}
	})
}
