package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestSessionTimelineForks pins the timeline's forks (U15.2, UX-B-07):
// the origin's fork marks, each with the forked session as stored — a
// fork that ran a turn names its turns and status, one that ran none
// reads 0 turns and no status (it has no page of its own: the origin
// says so instead of linking a 404) — past the cap unknown, and hidden
// for a panel token.
func TestSessionTimelineForks(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := func(d time.Duration) time.Time { return fixtureT0.Add(d) }
	turn := func(sid, id string, n int, start time.Duration, meta map[string]any) []obsdb.Record {
		attrs := func(ev string, pos int64) map[string]any {
			m := map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.public_id": "pub_trip",
				"weft.turn": int64(n), "gen_ai.agent.name": "trip-planner", "weft.event.type": ev, "weft.event.pos": pos}
			for k, v := range meta {
				m[k] = v
			}
			return m
		}
		return []obsdb.Record{
			{Time: at(start), EventName: "weft.event", Severity: 9, Service: "svc", Attrs: attrs("run_start", 0),
				Body: `{"type":"run_start","id":"` + id + `","agent":"trip-planner"}`},
			{Time: at(start + time.Second), EventName: "weft.event", Severity: 9, Service: "svc", Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","usage":{"input_tokens":10,"output_tokens":5},"steps":1}`},
		}
	}
	fork := func(d time.Duration, entry, to string) obsdb.Record {
		b, _ := json.Marshal(map[string]any{"kind": "fork", "session": "s_trip", "at": at(d).Format(time.RFC3339Nano), "entry": entry, "new_session": to})
		return obsdb.Record{Time: at(d), EventName: "weft.thread.fork", Body: string(b), Severity: 9, Service: "svc",
			Attrs: map[string]any{"weft.record": obsdb.RecordThread, "weft.run.id": "s_trip-t1", "weft.content": "none",
				"weft.session.id": "s_trip", "weft.public_id": "pub_trip", "gen_ai.agent.name": "trip-planner"}}
	}
	var recs []obsdb.Record
	recs = append(recs, turn("s_trip", "s_trip-t1", 1, 0, nil)...)
	recs = append(recs, turn("s_fork", "s_fork-t1", 1, 20*time.Second, map[string]any{"weft.session.forked_from": "s_trip#e4"})...)
	recs = append(recs, fork(5*time.Second, "e4", "s_fork"), fork(6*time.Second, "e4", "s_never"))
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	type forkView struct {
		SessionID string  `json:"session_id"`
		Entry     string  `json:"entry"`
		RunID     *string `json:"run_id"`
		Turns     int     `json:"turns"`
		Status    string  `json:"status"`
		Hidden    bool    `json:"hidden"`
		Unknown   bool    `json:"unknown"`
	}
	read := func(h http.Handler, tok string) []forkView {
		t.Helper()
		code, _, body := getWith(t, h, "/studio/api/sessions/s_trip/timeline", tok, "")
		if code != http.StatusOK {
			t.Fatalf("timeline: %d %s", code, body)
		}
		var v struct {
			Forks []forkView `json:"forks"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatal(err)
		}
		return v.Forks
	}
	got := read(Handler(DB(db)), "")
	if len(got) != 2 {
		t.Fatalf("forks = %+v, want 2", got)
	}
	if f := got[0]; f.SessionID != "s_fork" || f.Entry != "e4" || f.RunID == nil || *f.RunID != "s_trip-t1" || f.Turns != 1 || f.Status != "succeeded" {
		t.Errorf("the fork that ran = %+v, want s_fork at e4 under s_trip-t1, 1 turn, succeeded", f)
	}
	if f := got[1]; f.SessionID != "s_never" || f.Turns != 0 || f.Status != "" || f.Unknown || f.Hidden {
		t.Errorf("the fork that ran no turn = %+v, want 0 turns and no status", f)
	}
	defer func(n int) { timelineForkCap = n }(timelineForkCap)
	timelineForkCap = 1
	if got := read(Handler(DB(db)), ""); !got[1].Unknown || got[0].Unknown {
		t.Errorf("past the cap = %+v, want the second fork unknown", got)
	}
	timelineForkCap = 20
	// A panel token: the forks named, their state hidden.
	h := Handler(DB(db), Token("srv"))
	tok, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_trip", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(h, tok); len(got) != 2 || !got[0].Hidden || got[0].Turns != 0 || got[0].Status != "" {
		t.Errorf("a panel token's forks = %+v, want both hidden, no turns or status", got)
	}
}
