package obsdbtest

import (
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// remoteCaller (U33.1-fix; record contract §2): a run started on a
// core.WithCaller context — mcp.Serve reading an MCP client's _meta —
// carries weft.caller.run.id / weft.caller.call.id with
// weft.caller.remote "true": a caller in another process, as it states
// itself. Readers never place a run by it. The remote-linked run r_rem
// names a local run (r_local) and its call as its caller — a spoof as
// far as this database can tell — and stays top-level, off r_local's
// caller-linked runs, its session's own turn (its id is not
// <session>-tN), its session with no parent run; its row's caller
// columns are empty and the remote key is no metadata. A local caller
// link (r_pool, the U14 rule) behaves as before.
func remoteCaller(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		run := func(id string, shift time.Duration, extra map[string]any) []obsdb.Record {
			recs := []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":3,"output_tokens":2},"steps":1}`, extra),
			}
			for i := range recs {
				recs[i].Time = recs[i].Time.Add(shift)
			}
			return recs
		}
		remote := map[string]any{
			"gen_ai.agent.name": "helper", "weft.session.id": "s_rem", "weft.turn": int64(1),
			"weft.caller.run.id": "r_local", "weft.caller.call.id": "c_spoof", "weft.caller.remote": "true",
		}
		var recs []obsdb.Record
		recs = append(recs, run("r_local", 0, map[string]any{"gen_ai.agent.name": "lead"})...)
		recs = append(recs, run("r_pool", time.Second, map[string]any{"gen_ai.agent.name": "worker",
			"weft.caller.run.id": "r_local", "weft.caller.call.id": "c_local"})...)
		recs = append(recs, run("r_rem", 2*time.Second, remote)...)
		span := invokeSpan("r_rem", 1, remote)
		span.Start, span.End = at(2*time.Second), at(3*time.Second)
		if err := db.Write(ctx(), obsdb.Batch{Records: recs, Spans: []obsdb.Span{span}}); err != nil {
			t.Fatal(err)
		}

		got, err := db.Run(ctx(), "r_rem")
		if err != nil {
			t.Fatal(err)
		}
		if got.ParentRunID != "" || got.CallerRunID != "" || got.CallerCallID != "" {
			t.Errorf("r_rem link = parent %q, caller (%q, %q); want none: a remote link places nothing",
				got.ParentRunID, got.CallerRunID, got.CallerCallID)
		}
		for _, k := range []string{"weft.caller.run.id", "weft.caller.call.id", "weft.caller.remote"} {
			if v, ok := got.Meta[k]; ok {
				t.Errorf("r_rem meta[%s] = %q: a contract key, never metadata", k, v)
			}
		}
		if pool, err := db.Run(ctx(), "r_pool"); err != nil || pool.CallerRunID != "r_local" || pool.CallerCallID != "c_local" {
			t.Errorf("r_pool = %+v, %v; want the local caller link (r_local, c_local) kept", pool.RunRow, err)
		}
		top, err := db.Runs(ctx(), obsdb.RunQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(top.Runs); len(ids) != 2 || !slices.Contains(ids, "r_rem") || !slices.Contains(ids, "r_local") {
			t.Errorf("top-level runs = %v, want r_local and r_rem (a remote-linked run is top-level), never r_pool", ids)
		}
		cl, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", CallerRunID: "r_local"})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(cl.Runs); len(ids) != 1 || ids[0] != "r_pool" {
			t.Errorf("caller-linked runs of r_local = %v, want [r_pool]: a stated remote caller id never lands there", ids)
		}
		own, err := db.Runs(ctx(), obsdb.RunQuery{SessionID: "s_rem"})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(own.Runs); len(ids) != 1 || ids[0] != "r_rem" {
			t.Errorf("session s_rem's own turns = %v, want [r_rem]", ids)
		}
		page, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, s := range page.Sessions {
			if s.ID != "s_rem" {
				continue
			}
			found = true
			if s.Turns != 1 || s.ParentRun != "" {
				t.Errorf("s_rem = %+v, want 1 turn and no parent run", s)
			}
		}
		if !found {
			t.Errorf("sessions = %+v, want s_rem", page.Sessions)
		}
	}
}
