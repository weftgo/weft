package obsdbtest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// sessionsPartlyExpired (P9-pre-go-fix2; SessionsOldDated's sibling):
// one session whose first turn failed years ago — past a backend's
// retention (ClickHouse's weft_runs TTL), on disk or merged away — and
// whose second turn is fresh. Every read sees the same turns: the
// Sessions row's Turns, FailedTurns and Outcomes, the Failed filter,
// Session's Turns and len(Runs), and Runs{SessionID}'s Total. A backend
// with retention keeps the fresh turn alone (Turns 1, no failure, the
// session out of the Failed filter); one without (SQLite) keeps both
// (Turns 2, the failure counted and filtered). A row counting the
// expired failure beside a Failed filter that cut it is a list saying
// "1 failed turn" that its own filter denies.
func sessionsPartlyExpired(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const sid = "s_mix"
		old := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
		fresh := time.Now().UTC().Add(-time.Minute)
		var b obsdb.Batch
		for n, tr := range []struct {
			at     time.Time
			status int // the root span's: 1 ok, 2 error
		}{{old, 2}, {fresh, 1}} {
			turn := int64(n + 1)
			id := fmt.Sprintf("%s-t%d", sid, turn)
			ident := map[string]any{"weft.session.id": sid, "weft.public_id": "pub_mix", "weft.turn": turn}
			attrs := map[string]any{}
			if tr.status == 2 {
				attrs["error.type"] = "model_error"
			}
			for k, v := range ident {
				attrs[k] = v
			}
			s := invokeSpan(id, tr.status, attrs)
			s.TraceID, s.SpanID = fmt.Sprintf("e7%030x", turn), fmt.Sprintf("e7%014x", turn)
			s.Start, s.End = tr.at, tr.at.Add(time.Second)
			r := record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, ident)
			r.Time = tr.at
			b.Spans, b.Records = append(b.Spans, s), append(b.Records, r)
		}
		if err := db.Write(ctx(), b); err != nil {
			t.Fatal(err)
		}
		page, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || len(page.Sessions) != 1 {
			t.Fatalf("Sessions = Total %d, %d rows; want %s alone (its fresh turn keeps it)", page.Total, len(page.Sessions), sid)
		}
		row := page.Sessions[0]
		det, err := db.Session(ctx(), sid)
		if err != nil {
			t.Fatal(err)
		}
		no := false
		runs, err := db.Runs(ctx(), obsdb.RunQuery{SessionID: sid, Playground: &no})
		if err != nil {
			t.Fatal(err)
		}
		n := len(det.Runs)
		switch {
		case n == 2:
			// No retention: both turns, the failure counted.
			if row.FailedTurns != 1 || !slices.Equal(row.Outcomes, []obsdb.TurnOutcome{obsdb.OutcomeFailed, obsdb.OutcomeSucceeded}) {
				t.Errorf("both turns kept: row failed_turns %d, outcomes %v; want 1, [failed succeeded]", row.FailedTurns, row.Outcomes)
			}
		case n == 1 && det.Runs[0].ID == sid+"-t2":
			// The expired turn cut: the fresh one alone, no failure.
			if row.FailedTurns != 0 || !slices.Equal(row.Outcomes, []obsdb.TurnOutcome{obsdb.OutcomeSucceeded}) {
				t.Errorf("the expired turn cut: row failed_turns %d, outcomes %v; want 0, [succeeded]", row.FailedTurns, row.Outcomes)
			}
		default:
			t.Fatalf("Session(%s) runs = %+v; want both turns or t2 alone", sid, det.Runs)
		}
		if row.Turns != n || det.Turns != n || runs.Total != n || len(runs.Runs) != n {
			t.Errorf("turns: the row's %d, Session's %d over %d runs, Runs{SessionID} Total %d (%d rows); want one count on every read",
				row.Turns, det.Turns, n, runs.Total, len(runs.Runs))
		}
		fp, err := db.Sessions(ctx(), obsdb.SessionQuery{Failed: true})
		if err != nil {
			t.Fatal(err)
		}
		if listed := fp.Total == 1 && len(fp.Sessions) == 1; listed != (row.FailedTurns > 0) {
			t.Errorf("Sessions{Failed} = Total %d, %d rows; the row says %d failed turns", fp.Total, len(fp.Sessions), row.FailedTurns)
		}
		CheckTurnFactFilters(t, db)
	}
}
