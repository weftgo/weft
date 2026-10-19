package obsdbtest

import (
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// heldMarker (U31.1): a session compaction marker thread emitted at
// Close for want of a run (held: true in its body, no weft.run.id,
// keyed by the session's metadata alone) is read as the thread records
// held for no run are: SessionEvents lists it under no run, of kind
// compaction, beside the session's turn; it names no run row — Runs
// lists the turn alone and there is no run "" — the turn's Compactions
// do not take it, the session's Total and turns are its turns, and a
// retried batch adds nothing.
func heldMarker(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const sid = "s_held_marker"
		id := sid + "-t1"
		ident := map[string]any{"weft.session.id": sid, "weft.public_id": "pub_held", "weft.turn": int64(1)}
		recs := []obsdb.Record{
			record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, ident),
			record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","steps":1}`, ident),
		}
		marker := obsdb.Record{Time: at(5 * time.Second), EventName: "weft.compaction",
			Body: `{"scope":"session","hash":"sha256:held","entry":"e9","reason":"manual","replaced":3,"entries":1,"messages_before":4,"messages_after":2,"tokens_before":90,"tokens_after":30,"held":true}`,
			Attrs: map[string]any{"weft.record": obsdb.RecordCompaction, "weft.compaction.hash": "sha256:held",
				"weft.compaction.scope": "session", "weft.content": "none", "weft.session.id": sid, "weft.public_id": "pub_held"},
			Service: "conf-svc", Severity: 9}
		if err := db.Write(ctx(), obsdb.Batch{Records: append(recs, marker)}); err != nil {
			t.Fatal(err)
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{marker}}); err != nil {
			t.Fatal(err)
		}
		if er, ok := db.(obsdb.SessionEventReader); ok {
			page, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || len(page.Events) != 1 || page.Events[0].RunID != "" ||
				page.Events[0].Kind != obsdb.RecordCompaction || page.Events[0].Name != "weft.compaction" {
				t.Errorf("SessionEvents(%s) = %+v (total %d), want the held marker alone, under no run, once", sid, page.Events, page.Total)
			}
		}
		runs, err := db.Runs(ctx(), obsdb.RunQuery{SessionID: sid, ParentRunID: "*"})
		if err != nil {
			t.Fatal(err)
		}
		if len(runs.Runs) != 1 || runs.Runs[0].ID != id {
			t.Errorf("Runs(session %s) = %+v, want the turn alone (the held marker names no run)", sid, runs.Runs)
		}
		if _, err := db.Run(ctx(), ""); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf(`Run("") = %v, want not found: a runless marker makes no run row`, err)
		}
		cs, err := db.Compactions(ctx(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.Hash == "sha256:held" {
				t.Errorf("Compactions(%s) = %+v, want no held marker: it was filed under no run", id, cs)
			}
		}
		sess, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_held"})
		if err != nil {
			t.Fatal(err)
		}
		if sess.Total != 1 || len(sess.Sessions) != 1 || sess.Sessions[0].ID != sid || sess.Sessions[0].Turns != 1 {
			t.Errorf("Sessions(pub_held) = %+v (total %d), want %s with its one turn", sess.Sessions, sess.Total, sid)
		}
	}
}
