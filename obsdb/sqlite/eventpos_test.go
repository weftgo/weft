package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// An event record without its weft.event.pos (a malformed producer) is
// stored at position -1, never at run_start's 0 (docs/record-contract.md
// §4): run_start, arriving after it, is kept and read back at 0; the
// stray event is counted and never shown.
func TestPoslessEventKeepsRunStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pos.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	stray := rec("r_pos", "event", "step_start", 0, `{"type":"step_start","run_id":"r_pos","index":0}`, nil)
	delete(stray.Attrs, "weft.event.pos")
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{stray}}); err != nil {
		t.Fatal(err)
	}
	start := rec("r_pos", "event", "run_start", 0, `{"type":"run_start","id":"r_pos"}`, nil)
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{start}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx, "r_pos", -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Pos != 0 || string(page.Events[0].Event) != `{"type":"run_start","id":"r_pos"}` {
		t.Fatalf("events = %+v, want run_start alone at 0", page.Events)
	}
	raw, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var pos int64
	if err := raw.QueryRowContext(ctx, `SELECT pos FROM records WHERE run_id = 'r_pos' AND event_type = 'step_start'`).Scan(&pos); err != nil || pos != -1 {
		t.Errorf("the pos-less event is stored at %d (%v), want -1", pos, err)
	}
}
