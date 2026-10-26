package runtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// viewOf is the runtime's row in the snapshot (ok false: not listed).
func viewOf(rs *RuntimeServer, id string) (RuntimeView, bool) {
	for _, v := range rs.Snapshot() {
		if v.ID == id {
			return v, true
		}
	}
	return RuntimeView{}, false
}

// TestBreakpointsByAndForget pins U20.1's two facts on the runtimes
// view: who set a runtime's breakpoints (this Studio's PUT, the
// runtime's own registration, the store after a restart) and Forget —
// only a runtime that is gone and holds no live command leaves the
// list and the store; its commands stay.
func TestBreakpointsByAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.db")
	db, cs := openStore(t, path)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	rs := New(WithStore(cs))
	ts := httptest.NewServer(mux(rs))
	t.Cleanup(ts.Close)
	register(t, mux(rs), regBody("rt_a"))
	_, closeA := subscribe(t, rs, ts.URL, "rt_a", "")

	if v, _ := viewOf(rs, "rt_a"); v.BreakpointsBy != "" {
		t.Errorf("an empty set's breakpoints_by = %q, want absent", v.BreakpointsBy)
	}
	if err := rs.SetBreakpoints("rt_a", []string{"refund"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := viewOf(rs, "rt_a"); v.BreakpointsBy != BreakpointsByStudio || v.BreakpointsSetAt == nil {
		t.Errorf("after PUT: breakpoints_by = %q, set_at = %v; want studio and a time", v.BreakpointsBy, v.BreakpointsSetAt)
	}

	if err := rs.Forget("rt_a"); !errors.Is(err, ErrConnected) {
		t.Errorf("Forget of a connected runtime = %v, want ErrConnected", err)
	}
	if err := rs.Forget("rt_none"); !errors.Is(err, ErrUnknownRuntime) {
		t.Errorf("Forget of an unknown runtime = %v, want ErrUnknownRuntime", err)
	}
	closeA()
	waitDisconnected(t, rs, "rt_a")

	// A queued command addressed to it: its fate is still open.
	rs.mu.Lock()
	rs.commands["cmd_live"] = &commandRow{Command: Command{CommandID: "cmd_live", Runtime: "rt_a"}, state: StateQueued,
		created: rs.now(), updated: rs.now()}
	rs.mu.Unlock()
	if err := rs.Forget("rt_a"); !errors.Is(err, ErrRuntimeBusy) {
		t.Errorf("Forget with a queued command = %v, want ErrRuntimeBusy", err)
	}
	rs.mu.Lock()
	rs.commands["cmd_live"].state = StateFinished
	rs.mu.Unlock()
	if err := rs.Forget("rt_a"); err != nil {
		t.Fatalf("Forget of a gone runtime = %v", err)
	}
	if _, ok := viewOf(rs, "rt_a"); ok {
		t.Error("a forgotten runtime is still listed")
	}
	if _, ok := rs.CommandOf("cmd_live"); !ok {
		t.Error("Forget dropped a command addressed to the runtime: commands are history")
	}
	flush(t, rs)
	if rts, err := cs.Runtimes(ctx); err != nil || len(rts) != 0 {
		t.Errorf("stored runtimes after Forget = %+v, %v; want none", rts, err)
	}
	if _, ok, err := cs.Breakpoints(ctx, "rt_a"); err != nil || ok {
		t.Errorf("stored breakpoints after Forget = %v, %v; want none", ok, err)
	}

	// A registration carrying its own set: adopted, by the runtime.
	reg := regBody("rt_b")
	reg.Pid, reg.Breakpoints = 4122, []string{"lookup_order"}
	register(t, mux(rs), reg)
	if v, _ := viewOf(rs, "rt_b"); v.BreakpointsBy != BreakpointsByRuntime || v.BreakpointsSetAt != nil {
		t.Errorf("an adopted set: breakpoints_by = %q, set_at = %v; want runtime, no time", v.BreakpointsBy, v.BreakpointsSetAt)
	}
	flush(t, rs)
	rs.Close()

	// A restart: listed restored, the set the store's.
	rs2 := New(WithStore(cs))
	v, ok := viewOf(rs2, "rt_b")
	if !ok || !v.Restored || v.Connected || v.BreakpointsBy != BreakpointsByStore {
		t.Errorf("after a restart: %+v (listed %v); want restored, disconnected, breakpoints_by store", v, ok)
	}
	if _, ok := viewOf(rs2, "rt_a"); ok {
		t.Error("a forgotten runtime came back after a restart")
	}
	if err := rs2.Forget("rt_b"); err != nil {
		t.Fatalf("Forget of a restored runtime = %v", err)
	}
	flush(t, rs2)
	rs2.Close()
	rs3 := New(WithStore(cs))
	t.Cleanup(rs3.Close)
	if _, ok := viewOf(rs3, "rt_b"); ok {
		t.Error("a forgotten restored runtime came back after the next restart")
	}
}

// gateStore holds every runtime-row and breakpoint write while its gate
// is shut, so a test can act while the writer is mid-queue.
type gateStore struct {
	obsdb.CommandStore
	mu   sync.Mutex
	gate chan struct{} // nil: open
}

func (g *gateStore) wait() {
	g.mu.Lock()
	c := g.gate
	g.mu.Unlock()
	if c != nil {
		<-c
	}
}
func (g *gateStore) shut() { g.mu.Lock(); g.gate = make(chan struct{}); g.mu.Unlock() }
func (g *gateStore) open() {
	g.mu.Lock()
	if g.gate != nil {
		close(g.gate)
		g.gate = nil
	}
	g.mu.Unlock()
}
func (g *gateStore) PutRuntime(ctx context.Context, r obsdb.StoredRuntime) error {
	g.wait()
	return g.CommandStore.PutRuntime(ctx, r)
}
func (g *gateStore) PutBreakpoints(ctx context.Context, id string, tools []string) error {
	g.wait()
	return g.CommandStore.PutBreakpoints(ctx, id, tools)
}
func (g *gateStore) DeleteRuntime(ctx context.Context, id string) error {
	g.wait()
	return g.CommandStore.DeleteRuntime(ctx, id)
}

// TestForgetThenReregisterBeforeTheWriterDrains (U20.1-fix): a runtime
// forgotten and registering again before the store applied the delete
// ends present, with its own registration's breakpoint set — in memory,
// in the store and after a restart — whether the delete is held in
// flight (the writer blocked on it) or superseded in the queue by the
// new registration's row.
func TestForgetThenReregisterBeforeTheWriterDrains(t *testing.T) {
	for _, inFlight := range []bool{true, false} {
		t.Run(map[bool]string{true: "delete in flight", false: "delete superseded"}[inFlight], func(t *testing.T) {
			db, cs := openStore(t, filepath.Join(t.TempDir(), "weft.db"))
			t.Cleanup(func() { _ = db.Close() })
			g := &gateStore{CommandStore: cs}
			rs := New(WithStore(g))
			ts := httptest.NewServer(mux(rs))
			t.Cleanup(ts.Close)
			register(t, mux(rs), regBody("rt_a"))
			_, closeA := subscribe(t, rs, ts.URL, "rt_a", "")
			if err := rs.SetBreakpoints("rt_a", []string{"refund"}); err != nil {
				t.Fatal(err)
			}
			closeA()
			waitDisconnected(t, rs, "rt_a")
			flush(t, rs)

			g.shut()
			if !inFlight {
				// Hold the writer on another row first, so the delete
				// still waits in the queue when the registration lands.
				reg := regBody("rt_other")
				reg.Pid = 9
				register(t, mux(rs), reg)
				time.Sleep(20 * time.Millisecond)
			}
			if err := rs.Forget("rt_a"); err != nil {
				t.Fatal(err)
			}
			if inFlight {
				time.Sleep(20 * time.Millisecond) // the writer takes the delete
			}
			back := regBody("rt_a")
			back.Breakpoints = []string{"lookup_order"}
			register(t, mux(rs), back)
			g.open()
			flush(t, rs)

			v, ok := viewOf(rs, "rt_a")
			if !ok || len(v.Breakpoints) != 1 || v.Breakpoints[0] != "lookup_order" || v.BreakpointsBy != BreakpointsByRuntime {
				t.Errorf("memory after forget + re-register = %+v (listed %v); want its own set, by runtime", v, ok)
			}
			ctx := context.Background()
			if bp, ok, err := cs.Breakpoints(ctx, "rt_a"); err != nil || !ok || len(bp) != 1 || bp[0] != "lookup_order" {
				t.Errorf("stored breakpoints = %v, %v, %v; want [lookup_order]", bp, ok, err)
			}
			found := false
			if rts, err := cs.Runtimes(ctx); err == nil {
				for _, r := range rts {
					found = found || r.ID == "rt_a"
				}
			}
			if !found {
				t.Error("the re-registered runtime's row is not stored")
			}
			rs.Close()
			rs2 := New(WithStore(cs))
			t.Cleanup(rs2.Close)
			if v, ok := viewOf(rs2, "rt_a"); !ok || len(v.Breakpoints) != 1 || v.Breakpoints[0] != "lookup_order" {
				t.Errorf("after a restart = %+v (listed %v); want rt_a with [lookup_order]", v, ok)
			}
		})
	}
}
