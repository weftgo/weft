package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// sessionCountDB counts the session reads the timeline makes (U15.2-fix:
// the fork lookup is one Sessions query, never a session document per
// fork mark) and keeps the backend's session-event reader.
type sessionCountDB struct {
	obsdb.DB
	er       obsdb.SessionEventReader
	sessions atomic.Int64 // Sessions calls
	session  atomic.Int64 // Session calls
}

func (c *sessionCountDB) SessionEvents(ctx context.Context, q obsdb.SessionEventQuery) (obsdb.SessionEventPage, error) {
	return c.er.SessionEvents(ctx, q)
}

func (c *sessionCountDB) Sessions(ctx context.Context, q obsdb.SessionQuery) (obsdb.SessionPage, error) {
	c.sessions.Add(1)
	return c.DB.Sessions(ctx, q)
}

func (c *sessionCountDB) Session(ctx context.Context, id string) (obsdb.SessionDetail, error) {
	c.session.Add(1)
	return c.DB.Session(ctx, id)
}

// TestSessionTimelineForks pins the timeline's forks (U15.2, UX-B-07;
// U15.2-fix) on both backends: the origin's fork marks, each with the
// forked session as stored — a fork that ran a turn names its turns and
// status, one that ran none reads 0 turns and no status (it has no page
// of its own: the origin says so instead of linking a 404) — the
// distinct forked sessions read in one Sessions query whatever the
// number of marks (two marks naming one session: one id), past the cap
// unknown, and hidden for a panel token (no read at all).
func TestSessionTimelineForks(t *testing.T) {
	backends := map[string]func(t *testing.T) obsdb.DB{
		"sqlite": func(t *testing.T) obsdb.DB {
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "weft.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		},
	}
	if os.Getenv("WEFT_CLICKHOUSE_DSN") != "" {
		backends["clickhouse"] = freshClickHouse
	}
	for name, open := range backends {
		t.Run(name, func(t *testing.T) { timelineForks(t, open(t)) })
	}
}

func timelineForks(t *testing.T, base obsdb.DB) {
	// A recent clock: on ClickHouse a session list of fixtureT0-dated
	// (2020) turns counts them in Total but pages no row.
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	at := func(d time.Duration) time.Time { return t0.Add(d) }
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
	recs = append(recs, turn("s_fork", "s_fork-t2", 2, 22*time.Second, map[string]any{"weft.session.forked_from": "s_trip#e4"})...)
	// Three marks, two sessions: s_fork forked twice (at e4, then e6).
	recs = append(recs, fork(5*time.Second, "e4", "s_fork"), fork(6*time.Second, "e4", "s_never"), fork(7*time.Second, "e6", "s_fork"))
	if err := base.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	er, ok := base.(obsdb.SessionEventReader)
	if !ok {
		t.Fatalf("%T reads no session events", base)
	}
	db := &sessionCountDB{DB: base, er: er}
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
	reads := func() (int64, int64) { return db.sessions.Swap(0), db.session.Swap(0) }
	got := read(Handler(DB(db)), "")
	if len(got) != 3 {
		t.Fatalf("forks = %+v, want 3 (one per mark)", got)
	}
	if many, one := reads(); many != 1 || one != 1 {
		t.Errorf("one timeline read %d Sessions and %d Session queries, want 1 and 1 (the origin's own)", many, one)
	}
	for _, i := range []int{0, 2} {
		if f := got[i]; f.SessionID != "s_fork" || f.RunID == nil || *f.RunID != "s_trip-t1" || f.Turns != 2 || f.Status != "succeeded" {
			t.Errorf("fork %d (s_fork) = %+v, want under s_trip-t1, 2 turns, succeeded", i, f)
		}
	}
	if got[0].Entry != "e4" || got[2].Entry != "e6" {
		t.Errorf("fork entries = %q, %q; want e4, e6", got[0].Entry, got[2].Entry)
	}
	if f := got[1]; f.SessionID != "s_never" || f.Turns != 0 || f.Status != "" || f.Unknown || f.Hidden {
		t.Errorf("the fork that ran no turn = %+v, want 0 turns and no status", f)
	}
	defer func(n int) { timelineForkCap = n }(timelineForkCap)
	timelineForkCap = 1
	if got := read(Handler(DB(db)), ""); got[0].Unknown || got[2].Unknown || !got[1].Unknown || got[0].Turns != 2 {
		t.Errorf("past the cap = %+v, want s_fork (both marks) looked up, s_never unknown", got)
	}
	timelineForkCap = 20
	reads()
	// A panel token: the forks named, their state hidden — and not read.
	h := Handler(DB(db), Token("srv"))
	tok, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_trip", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(h, tok); len(got) != 3 || !got[0].Hidden || got[0].Turns != 0 || got[0].Status != "" {
		t.Errorf("a panel token's forks = %+v, want all hidden, no turns or status", got)
	}
	if many, _ := reads(); many != 0 {
		t.Errorf("a panel token's timeline read %d Sessions queries, want none", many)
	}
}
