package runtime

import (
	"net/http/httptest"
	"testing"
)

// TestAnyOwnRuns pins plan F4.2's half of debug_scope: a connected
// runtime acts on the app's own runs when it registered Sessions or
// breakpoints_own (an agent built with runtime.Breakpoints()); the view
// carries the flag; a runtime holding no stream counts for neither.
func TestAnyOwnRuns(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()

	plain := regBody("rt_plain")
	own := regBody("rt_own")
	own.BreakpointsOwn = true
	for _, reg := range []Registration{plain, own} {
		register(t, mux(rs), reg)
	}
	if rs.AnyOwnRuns() {
		t.Error("AnyOwnRuns with no stream open")
	}
	_, closePlain := subscribe(t, rs, ts.URL, "rt_plain", "")
	defer closePlain()
	if rs.AnyOwnRuns() {
		t.Error("AnyOwnRuns with only a plain runtime connected")
	}
	_, closeOwn := subscribe(t, rs, ts.URL, "rt_own", "")
	defer closeOwn()
	if !rs.AnyOwnRuns() {
		t.Error("AnyOwnRuns = false with rt_own (breakpoints_own) connected")
	}
	if rs.AnySessions() {
		t.Error("AnySessions read breakpoints_own as sessions")
	}
	for _, v := range rs.Snapshot() {
		if v.BreakpointsOwn != (v.ID == "rt_own") {
			t.Errorf("view %s breakpoints_own = %v", v.ID, v.BreakpointsOwn)
		}
	}
}
