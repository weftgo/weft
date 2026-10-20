package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestDeletedResumeReadsParked (P9-pre-go-fix5) is SQLite's side of
// the ClickHouse retention cut's resumer rule: SQLite cuts nothing, so
// the analogue of a cut resume is a deleted one. After DeleteRun of the
// resume, the parked run reads parked — no resumed_by, in Runs and
// Search status=parked — and a session whose newest turn was that
// resume awaits a decision (Sessions Awaiting, Status, Pending,
// Outcomes; Session), as ClickHouse's TestCutConsistency reads a kept
// park whose resume is past weft_runs' TTL.
func TestDeletedResumeReadsParked(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	span := func(id string, at time.Time, extra map[string]any) obsdb.Span {
		attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "rd", "weft.run.id": id}
		for k, v := range extra {
			attrs[k] = v
		}
		return obsdb.Span{TraceID: "t_" + id, SpanID: "s_" + id, Name: "invoke_agent rd", Kind: 1, Start: at,
			End: at.Add(time.Second), StatusCode: 1, Service: "rd", Attrs: attrs}
	}
	turn := func(n int64, at time.Time, from string, pending bool) []obsdb.Record {
		id := "s_pk-t" + string(rune('0'+n))
		attrs := func(ev string, pos int64) map[string]any {
			a := map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": "s_pk", "weft.turn": n,
				"weft.public_id": "pub_pk", "gen_ai.agent.name": "rd", "weft.event.type": ev, "weft.event.pos": pos}
			if from != "" {
				a["weft.resumed_from"] = from
			}
			return a
		}
		park := ""
		if pending {
			park = `,"pending":[{"type":"tool_call","id":"call_p","name":"refund","args":{}}]`
		}
		return []obsdb.Record{
			{Time: at, EventName: "weft.event", Severity: 9, Service: "rd", Attrs: attrs("run_start", 0), Body: `{"type":"run_start","id":"` + id + `"}`},
			{Time: at.Add(time.Second), EventName: "weft.event", Severity: 9, Service: "rd", Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","steps":1` + park + `}`},
		}
	}
	recs := append(turn(1, now, "", true), turn(2, now.Add(2*time.Second), "s_pk-t1", false)...)
	if err := db.Write(ctx, obsdb.Batch{
		Spans: []obsdb.Span{
			span("rd_parked", now, map[string]any{"weft.run.pending": 2}),
			span("rd_resume", now.Add(2*time.Second), map[string]any{"weft.resumed_from": "rd_parked"}),
		},
		Records: recs,
	}); err != nil {
		t.Fatal(err)
	}
	parked := []string{"rd_parked", "s_pk-t1"}
	check := func(when string, resumers map[string]string, wantParked []string, awaiting bool) {
		t.Helper()
		for id, by := range resumers {
			d, err := db.Run(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.ResumedBy != by || d.Pending == 0 {
				t.Errorf("%s: Run(%s) resumed_by %q (pending %d), want %q", when, id, d.ResumedBy, d.Pending, by)
			}
		}
		q := obsdb.RunQuery{Status: obsdb.StatusParked, ParentRunID: "*"}
		page, err := db.Runs(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedIDs(page.Runs); !slices.Equal(got, wantParked) || page.Total != len(wantParked) {
			t.Errorf("%s: Runs{parked} = %v (Total %d), want %v", when, got, page.Total, wantParked)
		}
		sp, err := db.(obsdb.Searcher).Search(ctx, obsdb.SearchQuery{RunQuery: q})
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedIDs(sp.Runs); !slices.Equal(got, wantParked) {
			t.Errorf("%s: Search{parked} = %v, want %v", when, got, wantParked)
		}
		aw, err := db.Sessions(ctx, obsdb.SessionQuery{Awaiting: true})
		if err != nil {
			t.Fatal(err)
		}
		succ, err := db.Sessions(ctx, obsdb.SessionQuery{Status: obsdb.StatusSucceeded})
		if err != nil {
			t.Fatal(err)
		}
		sd, err := db.Session(ctx, "s_pk")
		if err != nil {
			t.Fatal(err)
		}
		if awaiting {
			if aw.Total != 1 || len(aw.Sessions) != 1 || succ.Total != 0 {
				t.Errorf("%s: Sessions{Awaiting} %d, {Status succeeded} %d; want 1 and 0", when, aw.Total, succ.Total)
			}
			for _, s := range append(aw.Sessions, sd.SessionRow) {
				if s.Turns != 1 || s.Pending != 1 || s.Resumes != 0 || !slices.Equal(s.Outcomes, []obsdb.TurnOutcome{obsdb.OutcomeParked}) {
					t.Errorf("%s: s_pk turns %d, pending %d, resumes %d, outcomes %v; want 1, 1, 0, [parked]",
						when, s.Turns, s.Pending, s.Resumes, s.Outcomes)
				}
			}
		} else if aw.Total != 0 || succ.Total != 1 || sd.Pending != 0 {
			t.Errorf("%s: Sessions{Awaiting} %d, {Status succeeded} %d, s_pk pending %d; want 0, 1, 0", when, aw.Total, succ.Total, sd.Pending)
		}
	}
	check("resumed", map[string]string{"rd_parked": "rd_resume", "s_pk-t1": "s_pk-t2"}, nil, false)
	for _, id := range []string{"rd_resume", "s_pk-t2"} {
		if err := db.DeleteRun(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	check("the resumes deleted", map[string]string{"rd_parked": "", "s_pk-t1": ""}, parked, true)
}

func sortedIDs(rows []obsdb.RunRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
