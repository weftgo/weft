package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestRunsTurnBounds (U15.1-fix, UX-B-21): a turn page's stepper reads
// its neighbours from GET /api/runs with session, turn_below /
// turn_above and limit=1 — one row each, whatever the session's length
// (a 1,000-turn session here), and the pages' totals place the turn
// ("turn 500 of 1000") — never the whole session document.
func TestRunsTurnBounds(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const sid, n = "s_long", 1000
	var recs []obsdb.Record
	for turn := 1; turn <= n; turn++ {
		id := fmt.Sprintf("%s-t%d", sid, turn)
		recs = append(recs, obsdb.Record{Time: fixtureT0.Add(time.Duration(turn) * time.Second), EventName: "weft.event", Severity: 9,
			Body: `{"type":"run_start","id":"` + id + `","agent":"long"}`, Service: "svc",
			Attrs: map[string]any{"weft.record": "event", "weft.event.type": "run_start", "weft.event.pos": int64(0), "weft.run.id": id,
				"weft.session.id": sid, "weft.turn": int64(turn), "gen_ai.agent.name": "long"}})
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))
	read := func(q string) (ids []string, total int) {
		t.Helper()
		code, _, body := get(t, h, "/studio/api/runs?session="+sid+"&limit=1&"+q)
		if code != http.StatusOK {
			t.Fatalf("runs?%s: %d %s", q, code, body)
		}
		var p struct {
			Total int `json:"total"`
			Runs  []struct {
				ID string `json:"id"`
			} `json:"runs"`
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		for _, r := range p.Runs {
			ids = append(ids, r.ID)
		}
		return ids, p.Total
	}
	prev, before := read("turn_below=500")
	next, after := read("turn_above=500&oldest=1")
	if len(prev) != 1 || prev[0] != sid+"-t499" || before != 499 {
		t.Errorf("turn_below=500 = %v (total %d), want [%s-t499] (499)", prev, before, sid)
	}
	if len(next) != 1 || next[0] != sid+"-t501" || after != 500 {
		t.Errorf("turn_above=500 = %v (total %d), want [%s-t501] (500)", next, after, sid)
	}
	if first, total := read("turn_below=1"); len(first) != 0 || total != 0 {
		t.Errorf("turn_below=1 = %v (total %d), want none", first, total)
	}
	for _, q := range []string{"turn_below=0", "turn_above=x", "oldest=maybe"} {
		if code, _, _ := get(t, h, "/studio/api/runs?session="+sid+"&"+q); code != http.StatusBadRequest {
			t.Errorf("runs?%s = %d, want 400", q, code)
		}
	}
}
