package obsdbtest

import (
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// sessionsOldDated (U31.1): a session whose turns are years old — the
// Studio fixtures' 2020 clock — reads the same through the count and
// the page: Sessions' Total is the page's row count when one page holds
// the match, scoped, with a Since and with a limit. A backend with
// retention (ClickHouse's weft_runs TTL) may have dropped the turns —
// then Total and the rows are both zero, whether a merge has run yet or
// not; one without (SQLite) lists both sessions with their turns and
// usage. A count and a page that disagree is a list that says "2
// sessions" over an empty table.
func sessionsOldDated(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		t0 := time.Date(2020, 1, 1, 9, 0, 0, 123000000, time.UTC)
		turns := []struct {
			sid  string
			turn int64
		}{{"s_old", 1}, {"s_old", 2}, {"s_older", 1}}
		for n, tr := range turns {
			id := tr.sid + "-t" + string(rune('0'+tr.turn))
			extra := map[string]any{"weft.session.id": tr.sid, "weft.public_id": "pub_old", "weft.turn": tr.turn}
			recs := []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":2,"output_tokens":1},"steps":1}`, extra),
			}
			for i := range recs {
				recs[i].Time = t0.Add(time.Duration(n)*time.Minute + time.Duration(i)*time.Second)
			}
			if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []obsdb.SessionQuery{
			{},
			{PublicID: "pub_old"},
			{Since: t0.Add(-time.Hour)},
			{Limit: 1},
		} {
			page, err := db.Sessions(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total == 0 && len(page.Sessions) == 0 {
				continue // the backend's retention dropped them: both agree
			}
			want := 2
			if q.Limit == 1 {
				want = 1
			}
			if page.Total != 2 || len(page.Sessions) != want {
				t.Errorf("Sessions(%+v): Total %d, %d rows %+v; want Total 2 and %d rows (or none of either)", q, page.Total, len(page.Sessions), page.Sessions, want)
				continue
			}
			for _, s := range page.Sessions {
				if s.ID == "s_old" && (s.Turns != 2 || s.Usage.InputTokens != 4 || !s.LastSeen.Before(t0.Add(time.Hour))) {
					t.Errorf("Sessions(%+v): s_old = %d turns, usage %+v, last seen %v; want 2 turns, 4 input tokens, its 2020 time", q, s.Turns, s.Usage, s.LastSeen)
				}
			}
		}
	}
}
