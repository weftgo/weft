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
}
