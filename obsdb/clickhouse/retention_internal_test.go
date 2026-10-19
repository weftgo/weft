package clickhouse

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestSessionsCutAtTheRunsTTL (U31.1): a session whose runs are past
// weft_runs' TTL is neither counted nor paged while the expired rows
// are still on disk — merges stopped, so no TTL merge has dropped them
// — and Session reads it not found: the count and the page agree
// whatever a merge does between them. A fresh session beside it is
// listed, Total 1 over one row. The window is the table's own clause.
func TestSessionsCutAtTheRunsTTL(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	if got := db.runsTTLSeconds(ctx); got != ttlSeconds(DefaultMetaTTL) {
		t.Fatalf("weft_runs TTL read as %d s, want %d", got, ttlSeconds(DefaultMetaTTL))
	}
	for _, stmt := range []string{"SYSTEM STOP MERGES weft_runs", "SYSTEM STOP TTL MERGES weft_runs"} {
		if err := db.conn.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = db.conn.Exec(context.Background(), "SYSTEM START MERGES weft_runs")
		_ = db.conn.Exec(context.Background(), "SYSTEM START TTL MERGES weft_runs")
	})
	write := func(sid string, at time.Time) {
		t.Helper()
		id := sid + "-t1"
		attrs := func(ev string, pos int64) map[string]any {
			return map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.turn": int64(1),
				"weft.event.type": ev, "weft.event.pos": pos}
		}
		recs := []obsdb.Record{
			{Time: at, EventName: "weft.event", Severity: 9, Attrs: attrs("run_start", 0), Body: `{"type":"run_start","id":"` + id + `"}`},
			{Time: at.Add(time.Second), EventName: "weft.event", Severity: 9, Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`},
		}
		if err := db.Write(ctx, obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
	}
	write("s_old", time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC))
	write("s_new", time.Now().Add(-time.Minute).UTC())
	var onDisk uint64
	if err := db.conn.QueryRow(ctx, `SELECT count() FROM weft_runs WHERE RunId = 's_old-t1'`).Scan(&onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk == 0 {
		t.Log("the expired rows were dropped at insert: the cut is proven on the fresh session alone")
	}
	page, err := db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].ID != "s_new" {
		t.Errorf("Sessions = Total %d, rows %+v; want Total 1 and s_new alone (s_old past the TTL, %d rows on disk)", page.Total, page.Sessions, onDisk)
	}
	if _, err := db.Session(ctx, "s_old"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Session(s_old) = %v, want not found (past the TTL)", err)
	}
	if _, err := db.Session(ctx, "s_new"); err != nil {
		t.Errorf("Session(s_new) = %v", err)
	}

	// P9-pre-go-fix2: one session, an expired failed t1 beside a fresh
	// t2, the expired rows still on disk. Every read cuts t1 alike: the
	// row's turn facts, the Failed filter, Session's runs, Runs and Run.
	writeTurn := func(sid string, turn int64, at time.Time, failed bool) {
		t.Helper()
		id := sid + "-t" + string(rune('0'+turn))
		attrs := func(ev string, pos int64) map[string]any {
			return map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.turn": turn,
				"weft.event.type": ev, "weft.event.pos": pos}
		}
		recs := []obsdb.Record{{Time: at, EventName: "weft.event", Severity: 9, Attrs: attrs("run_start", 0), Body: `{"type":"run_start","id":"` + id + `"}`}}
		var spans []obsdb.Span
		if failed {
			spans = append(spans, obsdb.Span{TraceID: "e7000000000000000000000000000001", SpanID: "e700000000000001",
				Name: "invoke_agent mix", Start: at, End: at.Add(time.Second), StatusCode: 2, StatusMessage: "boom",
				Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": id, "weft.session.id": sid,
					"weft.turn": turn, "error.type": "model_error"}})
		} else {
			recs = append(recs, obsdb.Record{Time: at.Add(time.Second), EventName: "weft.event", Severity: 9, Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","steps":1}`})
		}
		if err := db.Write(ctx, obsdb.Batch{Spans: spans, Records: recs}); err != nil {
			t.Fatal(err)
		}
	}
	writeTurn("s_mix", 1, time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC), true)
	writeTurn("s_mix", 2, time.Now().Add(-time.Minute).UTC(), false)
	if err := db.conn.QueryRow(ctx, `SELECT count() FROM weft_runs WHERE RunId = 's_mix-t1'`).Scan(&onDisk); err != nil {
		t.Fatal(err)
	}
	t.Logf("s_mix-t1: %d expired rows on disk", onDisk)
	mix, err := db.Sessions(ctx, obsdb.SessionQuery{IDs: []string{"s_mix"}})
	if err != nil {
		t.Fatal(err)
	}
	if mix.Total != 1 || len(mix.Sessions) != 1 {
		t.Fatalf("Sessions{s_mix} = Total %d, %d rows", mix.Total, len(mix.Sessions))
	}
	if r := mix.Sessions[0]; r.Turns != 1 || r.FailedTurns != 0 || len(r.Outcomes) != 1 || r.Outcomes[0] != obsdb.OutcomeSucceeded {
		t.Errorf("s_mix row = turns %d, failed_turns %d, outcomes %v; want 1, 0, [succeeded] (t1 past the TTL)", r.Turns, r.FailedTurns, r.Outcomes)
	}
	failedPage, err := db.Sessions(ctx, obsdb.SessionQuery{Failed: true})
	if err != nil {
		t.Fatal(err)
	}
	if failedPage.Total != 0 {
		t.Errorf("Sessions{Failed} Total = %d, want 0", failedPage.Total)
	}
	det, err := db.Session(ctx, "s_mix")
	if err != nil {
		t.Fatal(err)
	}
	if det.Turns != 1 || len(det.Runs) != 1 {
		t.Errorf("Session(s_mix) = turns %d over %d runs, want 1 over 1", det.Turns, len(det.Runs))
	}
	runs, err := db.Runs(ctx, obsdb.RunQuery{SessionID: "s_mix"})
	if err != nil {
		t.Fatal(err)
	}
	if runs.Total != 1 || len(runs.Runs) != 1 {
		t.Errorf("Runs{SessionID s_mix} = Total %d, %d rows; want 1", runs.Total, len(runs.Runs))
	}
	below, err := db.Runs(ctx, obsdb.RunQuery{SessionID: "s_mix", TurnBelow: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if below.Total != 0 {
		t.Errorf("Runs{TurnBelow 2} Total = %d, want 0: the turn stepper's neighbour is cut too", below.Total)
	}
	if _, err := db.Run(ctx, "s_mix-t1"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Run(s_mix-t1) = %v, want not found (past the TTL)", err)
	}
	if _, err := db.Search(ctx, obsdb.SearchQuery{RunQuery: obsdb.RunQuery{SessionID: "s_mix"}}); err != nil {
		t.Errorf("Search: %v", err)
	} else if sp, _ := db.Search(ctx, obsdb.SearchQuery{RunQuery: obsdb.RunQuery{SessionID: "s_mix"}}); sp.Total != 1 {
		t.Errorf("Search{SessionID s_mix} Total = %d, want 1", sp.Total)
	}
}
