package obsdbtest

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// poolChild (U14; O11, D3; record contract §2, §6): a thread/pool child
// session is a session of its own. The lead's turn s_lead-t1 has a core
// Subagent child (weft.parent.*, the lead's session id inherited), a
// Generate inside a handler (weft.caller.*, the session id inherited)
// and a pool child session's turn s_child-t1 (weft.caller.* and its own
// session id, its lineage in weft.session.parent / parent_call); a
// child recorded before weft 0.18 (s_old-t1) names the lead's turn in
// weft.parent.run.id instead. A session is grouped over its own turns:
// the lead's is one turn, its usage its own; the child sessions
// exist, carry their lineage and resolve; SessionQuery.Parents lists
// them, a whole tree level per read; a session's top-level runs
// (RunQuery.SessionID) are its own turns; RunQuery.CallerRunID lists the caller-linked runs; the caller
// keys never land in a row's meta.
func poolChild(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		run := func(id string, shift time.Duration, in int, extra map[string]any) []obsdb.Record {
			recs := []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":`+strconv.Itoa(in)+`,"output_tokens":2},"steps":1}`, extra),
			}
			for i := range recs {
				recs[i].Time = recs[i].Time.Add(shift)
			}
			return recs
		}
		lead := map[string]any{"weft.session.id": "s_lead", "weft.public_id": "pub_lead", "weft.turn": int64(1), "gen_ai.agent.name": "investment-lead"}
		with := func(base map[string]any, kv ...any) map[string]any {
			out := map[string]any{}
			for k, v := range base {
				out[k] = v
			}
			for i := 0; i+1 < len(kv); i += 2 {
				out[kv[i].(string)] = kv[i+1]
			}
			return out
		}
		child := map[string]any{
			"weft.session.id": "s_child", "weft.public_id": "pub_lead", "weft.turn": int64(1),
			"gen_ai.agent.name":   "market-analyst",
			"weft.session.parent": "s_lead", "weft.session.parent_call": "d1",
			"weft.caller.run.id": "s_lead-t1", "weft.caller.call.id": "d1",
		}
		var recs []obsdb.Record
		recs = append(recs, run("s_lead-t1", 0, 20, lead)...)
		recs = append(recs, run("s_lead-t1/0/c_sub", time.Second, 7,
			with(lead, "gen_ai.agent.name", "researcher", "weft.parent.run.id", "s_lead-t1", "weft.parent.call.id", "c_sub"))...)
		recs = append(recs, run("r_nested", 2*time.Second, 5,
			with(lead, "gen_ai.agent.name", "helper", "weft.caller.run.id", "s_lead-t1", "weft.caller.call.id", "c_n"))...)
		recs = append(recs, run("s_child-t1", 3*time.Second, 30, child)...)
		recs = append(recs, run("s_old-t1", 4*time.Second, 40, map[string]any{
			"weft.session.id": "s_old", "weft.turn": int64(1), "gen_ai.agent.name": "market-analyst",
			"weft.session.parent": "s_lead", "weft.session.parent_call": "d2",
			"weft.parent.run.id": "s_lead-t1", "weft.parent.call.id": "d2",
		})...)
		// A session the child delegated in turn (one level deeper).
		recs = append(recs, run("s_grand-t1", 5*time.Second, 9, map[string]any{
			"weft.session.id": "s_grand", "weft.public_id": "pub_lead", "weft.turn": int64(1), "gen_ai.agent.name": "fact-checker",
			"weft.session.parent": "s_child", "weft.session.parent_call": "g1",
			"weft.caller.run.id": "s_child-t1", "weft.caller.call.id": "g1",
		})...)
		span := invokeSpan("s_child-t1", 1, with(child, "gen_ai.usage.input_tokens", int64(30), "gen_ai.usage.output_tokens", int64(2)))
		span.Start, span.End = at(3*time.Second), at(5*time.Second)
		if err := db.Write(ctx(), obsdb.Batch{Records: recs, Spans: []obsdb.Span{span}}); err != nil {
			t.Fatal(err)
		}

		// The child's run row: a caller link, no parent; the keys are
		// columns, not metadata.
		got, err := db.Run(ctx(), "s_child-t1")
		if err != nil {
			t.Fatal(err)
		}
		if got.ParentRunID != "" || got.CallerRunID != "s_lead-t1" || got.CallerCallID != "d1" {
			t.Errorf("s_child-t1 link = parent %q, caller (%q, %q); want no parent, caller (s_lead-t1, d1)",
				got.ParentRunID, got.CallerRunID, got.CallerCallID)
		}
		for _, k := range []string{"weft.caller.run.id", "weft.caller.call.id"} {
			if v, ok := got.Meta[k]; ok {
				t.Errorf("meta[%s] = %q: a contract key, never metadata", k, v)
			}
		}
		if got.Meta["weft.session.parent"] != "s_lead" {
			t.Errorf("meta = %v, want the lineage weft.session.parent kept", got.Meta)
		}
		// The lead's Children are its Subagent child alone.
		det, err := db.Run(ctx(), "s_lead-t1")
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(det.Children); !slices.Contains(ids, "s_lead-t1/0/c_sub") || slices.Contains(ids, "s_child-t1") || slices.Contains(ids, "r_nested") {
			t.Errorf("s_lead-t1 children = %v, want its Subagent child, never a caller-linked run", ids)
		}
		// RunQuery.CallerRunID: the runs started from its calls.
		cp, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", CallerRunID: "s_lead-t1"})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(cp.Runs); len(ids) != 2 || !slices.Contains(ids, "s_child-t1") || !slices.Contains(ids, "r_nested") {
			t.Errorf("caller-linked runs of s_lead-t1 = %v, want s_child-t1 and r_nested", ids)
		}
		// RunQuery.Oldest: start order, the cursor paging forward.
		first, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", CallerRunID: "s_lead-t1", Oldest: true, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(first.Runs); len(ids) != 1 || ids[0] != "r_nested" || first.NextBefore == nil || first.Total != 2 {
			t.Fatalf("oldest caller-linked run of s_lead-t1 = %v (total %d), want [r_nested] and a cursor", ids, first.Total)
		}
		next, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", CallerRunID: "s_lead-t1", Oldest: true, Limit: 1,
			Before: *first.NextBefore, BeforeID: first.NextBeforeID})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(next.Runs); len(ids) != 1 || ids[0] != "s_child-t1" {
			t.Errorf("next oldest caller-linked run = %v, want [s_child-t1]", ids)
		}

		// Top-level runs: started from no run — no parent, no caller.
		top, err := db.Runs(ctx(), obsdb.RunQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(top.Runs); len(ids) != 1 || ids[0] != "s_lead-t1" || top.Total != 1 {
			t.Errorf("top-level runs = %v (total %d), want [s_lead-t1]: a caller-linked run is on its caller's page", ids, top.Total)
		}

		// A session's top-level runs are its own turns (D3), a delegated
		// session's caller-linked turn included; the lead's Subagent
		// child and nested Generate are not.
		for sid, want := range map[string]string{"s_child": "s_child-t1", "s_lead": "s_lead-t1"} {
			sp, err := db.Runs(ctx(), obsdb.RunQuery{SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			if ids := idsOf(sp.Runs); len(ids) != 1 || ids[0] != want || sp.Total != 1 {
				t.Errorf("top-level runs of session %s = %v (total %d), want [%s]: its own turns", sid, ids, sp.Total, want)
			}
		}

		// Sessions: the lead's, the child's, the pre-0.18 child's, the
		// grandchild's.
		page, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]obsdb.SessionRow{}
		for _, s := range page.Sessions {
			byID[s.ID] = s
		}
		if page.Total != 4 || len(byID) != 4 {
			t.Fatalf("sessions = %+v (total %d), want s_lead, s_child, s_old and s_grand", page.Sessions, page.Total)
		}
		ls := byID["s_lead"]
		if ls.Turns != 1 || ls.Usage.InputTokens != 20 || ls.ParentSession != "" || ls.ParentRun != "" || ls.ParentCall != "" {
			t.Errorf("s_lead = %+v, want 1 turn, input 20 (its own turn alone), no lineage", ls)
		}
		cs := byID["s_child"]
		if cs.Turns != 1 || cs.Usage.InputTokens != 30 || cs.Agent != "market-analyst" ||
			cs.ParentSession != "s_lead" || cs.ParentCall != "d1" || cs.ParentRun != "s_lead-t1" {
			t.Errorf("s_child = %+v, want 1 turn, input 30, lineage (s_lead, d1, s_lead-t1)", cs)
		}
		if os := byID["s_old"]; os.Turns != 1 || os.ParentSession != "s_lead" || os.ParentCall != "d2" || os.ParentRun != "s_lead-t1" {
			t.Errorf("s_old = %+v, want 1 turn, lineage (s_lead, d2, s_lead-t1 from its parent id)", os)
		}

		// The child session resolves; the lead's turns are its own.
		sd, err := db.Session(ctx(), "s_child")
		if err != nil {
			t.Fatalf("Session(s_child): %v — a pool child session must resolve", err)
		}
		if ids := idsOf(sd.Runs); len(ids) != 1 || ids[0] != "s_child-t1" || sd.ParentSession != "s_lead" || sd.ParentRun != "s_lead-t1" {
			t.Errorf("Session(s_child) = runs %v, lineage (%q, %q); want [s_child-t1] under s_lead / s_lead-t1", ids, sd.ParentSession, sd.ParentRun)
		}
		ld, err := db.Session(ctx(), "s_lead")
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(ld.Runs); len(ids) != 1 || ids[0] != "s_lead-t1" || ld.Usage.InputTokens != 20 {
			t.Errorf("Session(s_lead) = runs %v, input %d; want [s_lead-t1], 20", ids, ld.Usage.InputTokens)
		}

		// SessionQuery.Parents: the sessions it delegated.
		kids, err := db.Sessions(ctx(), obsdb.SessionQuery{Parents: []string{"s_lead"}})
		if err != nil {
			t.Fatal(err)
		}
		var kidIDs []string
		for _, s := range kids.Sessions {
			kidIDs = append(kidIDs, s.ID)
		}
		slices.Sort(kidIDs)
		if kids.Total != 2 || !slices.Equal(kidIDs, []string{"s_child", "s_old"}) {
			t.Errorf("sessions delegated by s_lead = %v (total %d), want [s_child s_old]", kidIDs, kids.Total)
		}
		if none, err := db.Sessions(ctx(), obsdb.SessionQuery{Parents: []string{"s_grand"}}); err != nil || none.Total != 0 {
			t.Errorf("sessions delegated by s_grand = %+v (%v), want none", none.Sessions, err)
		}
		// One read lists a whole tree level: every session delegated by
		// any of them.
		level, err := db.Sessions(ctx(), obsdb.SessionQuery{Parents: []string{"s_child", "s_old", "s_lead"}})
		if err != nil {
			t.Fatal(err)
		}
		var levelIDs []string
		for _, s := range level.Sessions {
			levelIDs = append(levelIDs, s.ID)
		}
		slices.Sort(levelIDs)
		if level.Total != 3 || !slices.Equal(levelIDs, []string{"s_child", "s_grand", "s_old"}) {
			t.Errorf("sessions delegated by [s_child s_old s_lead] = %v (total %d), want [s_child s_grand s_old]", levelIDs, level.Total)
		}
		if scoped, err := db.Sessions(ctx(), obsdb.SessionQuery{Parents: []string{"s_child", "s_lead"}, PublicID: "pub_lead"}); err != nil || scoped.Total != 2 {
			t.Errorf("sessions delegated by [s_child s_lead] under pub_lead = %+v (%v), want s_child and s_grand", scoped.Sessions, err)
		}
		if scoped, err := db.Sessions(ctx(), obsdb.SessionQuery{Parents: []string{"s_lead"}, PublicID: "pub_lead"}); err != nil || scoped.Total != 1 || scoped.Sessions[0].ID != "s_child" {
			t.Errorf("sessions delegated by s_lead under pub_lead = %+v (%v), want s_child alone", scoped.Sessions, err)
		}
	}
}
