package thread_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// carryAgent is the fixture of the steer-inheritance tests: a "submit"
// tool the turn under test may run, a side-effect tool "fire" that
// counts its runs, StopWhen on submit (so a steer taken during the
// submit call meets the intended end and defers), and a Tap recording
// each run's metadata by run id.
type carryAgent struct {
	agent *weft.Agent
	fired atomic.Int32
	mu    sync.Mutex
	md    map[string]map[string]string // run id → the metadata its RunStart saw
}

func newCarryAgent(onToolStart func(weft.ToolStart), turns ...wefttest.Turn) *carryAgent {
	c := &carryAgent{md: map[string]map[string]string{}}
	submit := weft.Tool("submit", "", func(context.Context, struct{}) (string, error) {
		return "submitted", nil
	})
	fire := weft.Tool("fire", "a side effect", func(context.Context, struct{}) (string, error) {
		c.fired.Add(1)
		return "fired", nil
	})
	c.agent = weft.New(wefttest.Script(turns...), submit, fire,
		weft.StopWhen(weft.HasToolCall("submit")),
		weft.Tap(func(ctx context.Context, ev weft.Event) {
			switch e := ev.(type) {
			case weft.RunStart:
				c.mu.Lock()
				c.md[e.ID] = weft.MetadataFromContext(ctx)
				c.mu.Unlock()
			case weft.ToolStart:
				if onToolStart != nil {
					onToolStart(e)
				}
			}
		}))
	return c
}

func (c *carryAgent) metadata(runID string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.md[runID]
}

// A steer that cannot join the running turn — it arrives during the
// call StopWhen ends the turn on — becomes a follow-up turn, and the
// follow-up runs under the run options of the turn the steer was aimed
// at: a turn sent with ParkAllExcept parks the follow-up's side-effect
// call (before the fix it fired, unparked), and the turn's Metadata
// rides along. The steer's own options apply after the inherited ones.
func TestSteerFollowUpInheritsTurnRunOptions(t *testing.T) {
	ctx := context.Background()
	var s *thread.Session
	var once sync.Once
	steerc := make(chan *thread.Turn, 1)
	c := newCarryAgent(func(e weft.ToolStart) {
		if e.Name != "submit" {
			return
		}
		once.Do(func() {
			st, err := s.Send(ctx, weft.User("also fire it"), thread.As(thread.Steer),
				thread.RunOptions(weft.Metadata(map[string]string{"via": "steer"})))
			if err != nil {
				t.Errorf("steer Send: %v", err)
			}
			steerc <- st
		})
	},
		wefttest.ToolCalls(wefttest.Call{Name: "submit", ID: "call_s"}),
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_f"}),
		wefttest.Say("fired"),
	)
	var err error
	s, err = thread.Create(ctx, thread.Memory(), c.agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("submit, nothing else"), thread.RunOptions(
		weft.ParkAllExcept("submit"),
		weft.Metadata(map[string]string{"tenant": "acme", "via": "turn"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	steer := <-steerc
	if _, err := steer.Wait(); err != nil {
		t.Fatalf("steer: %v", err)
	}
	if steer.Outcome() != thread.TurnDeferred || steer.Next() == nil {
		t.Fatalf("steer outcome %v next %v, want a deferred steer with a follow-up", steer.Outcome(), steer.Next())
	}
	follow := steer.Next()
	res, err := follow.Wait()
	if err != nil {
		t.Fatalf("follow-up: %v", err)
	}
	if n := c.fired.Load(); n != 0 {
		t.Errorf("fire ran %d times in the follow-up, want it parked by the steered turn's ParkAllExcept", n)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "fire" {
		t.Errorf("follow-up pending = %+v, want the fire call parked", res.Pending)
	}
	md := c.metadata(follow.RunID())
	if md["tenant"] != "acme" {
		t.Errorf("follow-up metadata tenant = %q, want the steered turn's (acme)", md["tenant"])
	}
	if md["via"] != "steer" {
		t.Errorf("follow-up metadata via = %q, want the steer's own option to win (applied after)", md["via"])
	}
}

// A steer sent while only an approval boundary holds the session — no
// run to deliver into — defers at once; its follow-up runs, after the
// boundary resolves, under the parked turn's run options.
func TestBoundarySteerFollowUpInheritsParkedTurnOptions(t *testing.T) {
	ctx := context.Background()
	c := newCarryAgent(nil,
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_1"}),
		wefttest.Say("not fired"),
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_2"}),
		wefttest.Say("fired"),
	)
	s, err := thread.Create(ctx, thread.Memory(), c.agent)
	if err != nil {
		t.Fatal(err)
	}
	parked, err := s.Send(ctx, weft.User("fire"), thread.RunOptions(
		weft.ParkAllExcept(), weft.Metadata(map[string]string{"tenant": "acme"})))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := parked.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("parked turn: %v, %v", res, err)
	}
	steer, err := s.Send(ctx, weft.User("fire again"), thread.As(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := steer.Wait(); err != nil || steer.Next() == nil {
		t.Fatalf("steer: %v, next %v", err, steer.Next())
	}
	rt, err := s.Decide(ctx, thread.Deny("call_1", "no"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	res, err := steer.Next().Wait()
	if err != nil {
		t.Fatalf("follow-up: %v", err)
	}
	if n := c.fired.Load(); n != 0 {
		t.Errorf("fire ran %d times, want the follow-up's call parked by the parked turn's ParkAllExcept", n)
	}
	if len(res.Pending) != 1 {
		t.Errorf("follow-up pending = %+v, want the fire call parked", res.Pending)
	}
	if md := c.metadata(steer.Next().RunID()); md["tenant"] != "acme" {
		t.Errorf("follow-up tenant = %q, want acme", md["tenant"])
	}
}

// A steer sent to an idle session has no turn to inherit from: it runs
// as an ordinary turn under the options given in that Send.
func TestIdleSteerRunsUnderItsOwnOptions(t *testing.T) {
	ctx := context.Background()
	c := newCarryAgent(nil,
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_1"}),
		wefttest.Say("fired"),
	)
	s, err := thread.Create(ctx, thread.Memory(), c.agent)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := s.Send(ctx, weft.User("fire"), thread.As(thread.Steer), thread.RunOptions(
		weft.ParkAllExcept(), weft.Metadata(map[string]string{"tenant": "acme"})))
	if err != nil {
		t.Fatal(err)
	}
	res, err := tn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if n := c.fired.Load(); n != 0 || len(res.Pending) != 1 {
		t.Errorf("fired %d, pending %+v: want the idle steer's own ParkAllExcept to park the call", n, res.Pending)
	}
	if md := c.metadata(tn.RunID()); md["tenant"] != "acme" {
		t.Errorf("tenant = %q, want acme", md["tenant"])
	}
}

// Every other run started on behalf of a turn carries the turn's run
// options — pinned here beside the steer follow-ups: the resume after
// Decide (AutoResume) and after an explicit Resume, a send queued
// behind a busy turn (its own), and the overflow re-run.
func TestRunsOnBehalfOfATurnKeepItsOptions(t *testing.T) {
	ctx := context.Background()
	for _, explicit := range []bool{false, true} {
		name := "decide-autoresume"
		if explicit {
			name = "explicit-resume"
		}
		t.Run(name, func(t *testing.T) {
			c := newCarryAgent(nil,
				wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_1"}),
				wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_2"}),
				wefttest.Say("fired twice"),
			)
			var opts []thread.SessionOption
			if explicit {
				opts = append(opts, thread.AutoResume(false))
			}
			s, err := thread.Create(ctx, thread.Memory(), c.agent, opts...)
			if err != nil {
				t.Fatal(err)
			}
			parked, err := s.Send(ctx, weft.User("fire"), thread.RunOptions(
				weft.ParkAllExcept(), weft.Metadata(map[string]string{"tenant": "acme"})))
			if err != nil {
				t.Fatal(err)
			}
			if res, err := parked.Wait(); err != nil || len(res.Pending) != 1 {
				t.Fatalf("parked turn: %v, %v", res, err)
			}
			rt, err := s.Decide(ctx, thread.Approve("call_1"))
			if err != nil {
				t.Fatal(err)
			}
			if explicit {
				if rt, err = s.Resume(ctx); err != nil {
					t.Fatal(err)
				}
			}
			res, err := rt.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if n := c.fired.Load(); n != 1 || len(res.Pending) != 1 {
				t.Errorf("fired %d, pending %+v: want the approved call once and call_2 parked again", n, res.Pending)
			}
			if md := c.metadata(rt.RunID()); md["tenant"] != "acme" {
				t.Errorf("resume tenant = %q, want the parked send's (acme)", md["tenant"])
			}
		})
	}

	t.Run("queued-send", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan struct{})
		var once sync.Once
		block := weft.Tool("block", "", func(context.Context, struct{}) (string, error) {
			once.Do(func() { close(started) })
			<-release
			return "ok", nil
		})
		var fired atomic.Int32
		fire := weft.Tool("fire", "", func(context.Context, struct{}) (string, error) {
			fired.Add(1)
			return "fired", nil
		})
		agent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "block", ID: "call_b"}),
			wefttest.Say("unblocked"),
			wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_f"}),
		), block, fire)
		s, err := thread.Create(ctx, thread.Memory(), agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, weft.User("block"))
		if err != nil {
			t.Fatal(err)
		}
		<-started
		t2, err := s.Send(ctx, weft.User("fire"), thread.RunOptions(weft.ParkAllExcept()))
		if err != nil {
			t.Fatal(err)
		}
		close(release)
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		res, err := t2.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if n := fired.Load(); n != 0 || len(res.Pending) != 1 {
			t.Errorf("fired %d, pending %+v: want the queued send's own ParkAllExcept to park", n, res.Pending)
		}
	})

	t.Run("overflow-rerun", func(t *testing.T) {
		col := &mdCollector{}
		agent := weft.New(wefttest.Script(
			wefttest.Say("the first answer"),
			wefttest.Fail(weft.ErrContextOverflow),
			wefttest.Say("the summary of what came before"),
			wefttest.Say("recovered after compaction"),
		), col.tap())
		s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
		if err != nil {
			t.Fatal(err)
		}
		t0, _ := s.Send(ctx, weft.User("a first question"))
		if _, err := t0.Wait(); err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, weft.User("a prompt that overflows"),
			thread.RunOptions(weft.Metadata(map[string]string{"tenant": "acme"})))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		mds := col.snapshot()
		if len(mds) != 3 || mds[1]["tenant"] != "acme" || mds[2]["tenant"] != "acme" {
			t.Errorf("runs' metadata = %v, want the failed attempt and the re-run both under the send's options", mds)
		}
	})
}

// ruleContext returns a context carrying a ParkAllExcept list the way
// a run hands it down — the context of a tool call inside a run sent
// with ParkAllExcept(names...) — detached from that run's end. The
// thread under test sees the rule only on its context, as a pool child
// does.
func ruleContext(t *testing.T, names ...string) context.Context {
	t.Helper()
	var got context.Context
	host := weft.Tool("host", "", func(ctx context.Context, _ struct{}) (string, error) {
		got = context.WithoutCancel(ctx)
		return "ok", nil
	})
	outer := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "host", ID: "call_h"}),
		wefttest.Say("done"),
	), host)
	if _, err := outer.Generate(context.Background(), weft.Prompt("host"), weft.ParkAllExcept(append(names, "host")...)); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("the host tool never ran")
	}
	return got
}

// A turn whose park rule rides its context, not its options, keeps it
// on the runs started on its behalf: the resume a decider's context
// arms (before the fix, the decider's bare context ran the resumed
// steps unparked), and a steer's follow-up sent on another context.
func TestContextRuleSurvivesResumeAndSteer(t *testing.T) {
	ctx := context.Background()
	t.Run("resume", func(t *testing.T) {
		c := newCarryAgent(nil,
			wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_1"}),
			wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_2"}),
			wefttest.Say("fired twice"),
		)
		s, err := thread.Create(ctx, thread.Memory(), c.agent)
		if err != nil {
			t.Fatal(err)
		}
		parked, err := s.Send(ruleContext(t), weft.User("fire"))
		if err != nil {
			t.Fatal(err)
		}
		if res, err := parked.Wait(); err != nil || len(res.Pending) != 1 {
			t.Fatalf("parked turn: %v, %v", res, err)
		}
		rt, err := s.Decide(ctx, thread.Approve("call_1"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := rt.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if n := c.fired.Load(); n != 1 || len(res.Pending) != 1 {
			t.Errorf("fired %d, pending %+v: want call_1 once and call_2 parked by the context's rule", n, res.Pending)
		}
	})
	t.Run("steer", func(t *testing.T) {
		var s *thread.Session
		var once sync.Once
		steerc := make(chan *thread.Turn, 1)
		c := newCarryAgent(func(e weft.ToolStart) {
			if e.Name != "submit" {
				return
			}
			once.Do(func() {
				st, err := s.Send(ctx, weft.User("also fire it"), thread.As(thread.Steer))
				if err != nil {
					t.Errorf("steer Send: %v", err)
				}
				steerc <- st
			})
		},
			wefttest.ToolCalls(wefttest.Call{Name: "submit", ID: "call_s"}),
			wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "call_f"}),
			wefttest.Say("fired"),
		)
		var err error
		s, err = thread.Create(ctx, thread.Memory(), c.agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ruleContext(t, "submit"), weft.User("submit"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		steer := <-steerc
		if _, err := steer.Wait(); err != nil || steer.Next() == nil {
			t.Fatalf("steer: %v, next %v", err, steer.Next())
		}
		res, err := steer.Next().Wait()
		if err != nil {
			t.Fatal(err)
		}
		if n := c.fired.Load(); n != 0 || len(res.Pending) != 1 {
			t.Errorf("fired %d, pending %+v: want the follow-up parked by the steered turn's context rule", n, res.Pending)
		}
	})
}
