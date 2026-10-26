package runtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
