package obsdbtest

import (
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// emptyIdentity: an identity key carried with an empty value is no
// value (core.Metadata's rule — a session stamps its own weft.public_id
// and pool lineage, "" when it has none). A run whose records and spans
// say weft.public_id = "" has no public id: no panel scope lists it,
// its session row has none, and ResolvePublicID("") resolves nothing; a
// weft.session.parent = "" is not in the row's meta.
func emptyIdentity(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		empty := map[string]any{
			"weft.session.id": "s_noid", "weft.public_id": "",
			"weft.session.parent": "", "weft.session.parent_call": "", "weft.session.forked_from": "",
			// On the span too: ClickHouse's run meta is one contributing
			// row's map (max(Meta)), not a merge, so the span carries
			// the caller's pair the record does.
			"tenant": "conftest",
		}
		recs := []obsdb.Record{
			record("r_noid", "event", "run_start", 0, `{"type":"run_start","id":"r_noid"}`, empty),
			record("r_noid", "event", "run_finish", 1, `{"type":"run_finish","run_id":"r_noid","usage":{"input_tokens":10,"output_tokens":2},"steps":1}`, empty),
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs, Spans: []obsdb.Span{invokeSpan("r_noid", 1, empty)}}); err != nil {
			t.Fatal(err)
		}
		run, err := db.Run(ctx(), "r_noid")
		if err != nil {
			t.Fatal(err)
		}
		if run.PublicID != "" || run.SessionID != "s_noid" {
			t.Errorf("run identity = (session %q, public %q), want s_noid and no public id", run.SessionID, run.PublicID)
		}
		for _, k := range []string{"weft.session.parent", "weft.session.parent_call", "weft.session.forked_from", "weft.public_id"} {
			if v, ok := run.Meta[k]; ok {
				t.Errorf("meta[%s] = %q, want absent: an empty value names nothing", k, v)
			}
		}
		if run.Meta["tenant"] != "conftest" {
			t.Errorf("meta = %v, want the caller's tenant kept", run.Meta)
		}
		if p, err := db.Runs(ctx(), obsdb.RunQuery{PublicID: "pub_conf"}); err != nil || p.Total != 0 {
			t.Errorf("runs of pub_conf = %v (%v), want none", idsOf(p.Runs), err)
		}
		sp, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil || len(sp.Sessions) != 1 || sp.Sessions[0].ID != "s_noid" || sp.Sessions[0].PublicID != "" {
			t.Errorf("sessions = %+v (%v), want s_noid with no public id", sp.Sessions, err)
		}
		if _, err := db.ResolvePublicID(ctx(), ""); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("ResolvePublicID(\"\") err = %v, want ErrNotFound: \"\" is no public id", err)
		}
	}
}

// sessionsScoped: SessionQuery.PublicID scopes the runs before the
// grouping. Session s_col holds pub_a's weft turn and another app's
// derived run colliding on the session id (gen_ai.conversation.id),
// newer and heavier. Scoped to pub_a the session is one turn, pub_a's
// usage and last seen — the order and the cursor follow it — and Total
// counts only sessions with a pub_a turn; unscoped, both runs count.
func sessionsScoped(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		weftRun := func(id, session, pub string, shift time.Duration) []obsdb.Record {
			ex := map[string]any{"weft.session.id": session, "weft.public_id": pub}
			recs := []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, ex),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":10,"output_tokens":2},"steps":1}`, ex),
			}
			for i := range recs {
				recs[i].Time = recs[i].Time.Add(shift)
			}
			return recs
		}
		var recs []obsdb.Record
		recs = append(recs, weftRun("col-t1", "s_col", "pub_a", 0)...)           // last seen at(1s)
		recs = append(recs, weftRun("a2-t1", "s_a2", "pub_a", 4*time.Second)...) // last seen at(5s)
		recs = append(recs, weftRun("b-t1", "s_b", "pub_b", 20*time.Second)...)
		derived := func(trace, span, conv string, start time.Duration) obsdb.Span {
			s := stockSpan(span, "", "invoke_agent other", start, start+10*time.Second, map[string]any{
				"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "other",
				"gen_ai.conversation.id":    conv,
				"gen_ai.usage.input_tokens": int64(500), "gen_ai.usage.output_tokens": int64(50),
			})
			s.TraceID = trace
			return s
		}
		spans := []obsdb.Span{
			derived("5c0c0ffee0000000000000000000c001", "5c00000000000001", "s_col", 30*time.Second), // last seen at(40s)
			derived("5c0c0ffee0000000000000000000c002", "5c00000000000002", "s_der", 50*time.Second),
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs, Spans: spans}); err != nil {
			t.Fatal(err)
		}

		// Unscoped: the collision counts.
		all, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if all.Total != 4 {
			t.Errorf("unscoped total = %d, want 4 (s_col, s_a2, s_b, s_der)", all.Total)
		}
		var col obsdb.SessionRow
		for _, s := range all.Sessions {
			if s.ID == "s_col" {
				col = s
			}
		}
		if col.Turns != 2 || col.Usage.InputTokens != 510 || !col.LastSeen.Equal(at(40*time.Second)) {
			t.Errorf("unscoped s_col = %+v, want 2 turns, input 510, last seen the derived run's", col)
		}

		// Scoped to pub_a: one page.
		scoped, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_a"})
		if err != nil {
			t.Fatal(err)
		}
		if scoped.Total != 2 || len(scoped.Sessions) != 2 {
			t.Fatalf("scoped page = %+v (total %d), want s_a2 and s_col", scoped.Sessions, scoped.Total)
		}
		if scoped.Sessions[0].ID != "s_a2" || scoped.Sessions[1].ID != "s_col" {
			t.Errorf("scoped order = [%s %s], want [s_a2 s_col]: pub_a's last seen, not the derived run's",
				scoped.Sessions[0].ID, scoped.Sessions[1].ID)
		}
		sc := scoped.Sessions[1]
		if sc.Turns != 1 || sc.Usage.InputTokens != 10 || sc.Usage.OutputTokens != 2 ||
			!sc.LastSeen.Equal(at(time.Second)) || !sc.FirstSeen.Equal(at(0)) ||
			sc.PublicID != "pub_a" || sc.Agent != "conf" || sc.Status != obsdb.StatusSucceeded {
			t.Errorf("scoped s_col = %+v, want 1 turn, input 10/2, seen at(0)..at(1s), pub_a, agent conf, succeeded", sc)
		}

		// Scoped, a page at a time: the cursor is pub_a's last seen.
		first, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_a", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if first.Total != 2 || len(first.Sessions) != 1 || first.Sessions[0].ID != "s_a2" || first.NextBefore == nil {
			t.Fatalf("scoped first page = %+v (total %d), want s_a2 and a cursor", first.Sessions, first.Total)
		}
		if !first.NextBefore.Equal(at(5*time.Second)) || first.NextBeforeID != "s_a2" {
			t.Errorf("cursor = (%v, %s), want s_a2's at(5s)", first.NextBefore, first.NextBeforeID)
		}
		second, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_a", Limit: 1, Before: *first.NextBefore, BeforeID: first.NextBeforeID})
		if err != nil {
			t.Fatal(err)
		}
		if second.Total != 2 || len(second.Sessions) != 1 || second.Sessions[0].ID != "s_col" || second.Sessions[0].Turns != 1 {
			t.Errorf("scoped second page = %+v (total %d), want s_col with 1 turn", second.Sessions, second.Total)
		}
		if second.NextBefore == nil || !second.NextBefore.Equal(at(time.Second)) {
			t.Errorf("second cursor = %v, want pub_a's at(1s), never the derived run's at(40s)", second.NextBefore)
		}
		// A public id nobody holds lists nothing; the agent filter composes.
		if none, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_none"}); err != nil || none.Total != 0 || len(none.Sessions) != 0 {
			t.Errorf("pub_none = %+v (%v), want empty", none, err)
		}
		if other, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_a", Agent: "other"}); err != nil || other.Total != 0 {
			t.Errorf("pub_a + agent other = %+v (%v), want empty: the derived agent is not pub_a's", other, err)
		}
	}
}
