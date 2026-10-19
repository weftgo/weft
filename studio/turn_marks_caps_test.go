package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// What ended a busy-felled turn rides its row (U16-fix): the
// interrupt record filed under it names by_run / by_turn and the
// policy — thread_busy's t6 interrupted by t7, t8 rolled back by t9 —
// so the panel's chip never guesses the next turn.
func TestRunsTurnMarksEndedBy(t *testing.T) {
	type row struct {
		ID          string `json:"id"`
		EndedBy     string `json:"ended_by"`
		EndedByTurn int    `json:"ended_by_turn"`
		EndedKind   string `json:"ended_kind"`
	}
	srv := New(Open(t.TempDir() + "/ended.db"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/thread_busy/traces.json")
	ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/thread_busy/logs.json")
	const sid = "01K00000000000000000000001"
	_, _, body := get(t, h, "/studio/api/runs?session="+sid)
	var page struct {
		Runs   []row           `json:"runs"`
		Capped json.RawMessage `json:"turn_marks_capped"`
	}
	decode(t, body, &page)
	want := map[string]row{
		sid + "-t6": {ID: sid + "-t6", EndedBy: sid + "-t7", EndedByTurn: 7, EndedKind: "interrupt"},
		sid + "-t8": {ID: sid + "-t8", EndedBy: sid + "-t9", EndedByTurn: 9, EndedKind: "rollback"},
	}
	seen := 0
	for _, r := range page.Runs {
		if w, ok := want[r.ID]; ok {
			seen++
			if r != w {
				t.Errorf("%s = %+v; want %+v", r.ID, r, w)
			}
		} else if r.EndedKind != "" {
			t.Errorf("%s carries an end it never had: %+v", r.ID, r)
		}
	}
	if seen != len(want) {
		t.Errorf("listed %d of the felled turns: %+v", seen, page.Runs)
	}
	if len(page.Capped) != 0 {
		t.Errorf("turn_marks_capped = %s on a fully read page", page.Capped)
	}
}

// The marks are never silently capped (U16-fix): a session's marks
// past one SessionEvents page (1,000) are read by offset, and other
// kinds of session events never crowd them out (the read names its
// kinds); a page of more sessions than turnMarksSessions marks the
// first ones and says how many turns it left unmarked — the
// turn_marks_capped hole; a page the request did not narrow carries
// no marks, a panel token's scope notwithstanding.
func TestRunsTurnMarksPastTheCaps(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/caps.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	base := time.Now().UTC().Add(-time.Hour)
	mark := func(kind, runID, sid, pub string, i int) obsdb.Record {
		attrs := map[string]any{"weft.record": kind, "weft.run.id": runID, "weft.session.id": sid,
			"weft.public_id": pub, "weft.content": "none"}
		body := fmt.Sprintf(`{"kind":"label","session":%q,"entry":"e%d","name":"n%d"}`, sid, i, i)
		name := "weft.thread.label"
		if kind == obsdb.RecordCompaction {
			attrs["weft.compaction.scope"], attrs["weft.compaction.hash"] = "session", fmt.Sprintf("sha256:%d", i)
			body = fmt.Sprintf(`{"scope":"session","hash":"sha256:%d","entry":"e%d","replaced":1}`, i, i)
			name = "weft.compaction"
		}
		return obsdb.Record{Time: base.Add(time.Duration(i) * time.Millisecond), EventName: name, Body: body,
			Attrs: attrs, Service: "svc", Severity: 9, Resource: map[string]any{"service.name": "svc"}}
	}
	// One session: 1,100 labels under t1 first, then 1,005 compaction
	// markers under t1 and one under t2 — every one past the first page.
	seedRun(t, db, "s_big-t1", "pub_big", "s_big", "", nil)
	seedRun(t, db, "s_big-t2", "pub_big", "s_big", "", map[string]any{"weft.turn": "2"})
	var b obsdb.Batch
	i := 0
	for range 1100 {
		i++
		b.Records = append(b.Records, mark(obsdb.RecordThread, "s_big-t1", "s_big", "pub_big", i))
	}
	for range 1005 {
		i++
		b.Records = append(b.Records, mark(obsdb.RecordCompaction, "s_big-t1", "s_big", "pub_big", i))
	}
	i++
	b.Records = append(b.Records, mark(obsdb.RecordCompaction, "s_big-t2", "s_big", "pub_big", i))
	// More sessions than one page marks, under one public id, a marker
	// under each one's turn.
	const many = turnMarksSessions + 5
	for n := range many {
		sid := fmt.Sprintf("s_many%02d", n)
		seedRun(t, db, sid+"-t1", "pub_many", sid, "", nil)
		i++
		b.Records = append(b.Records, mark(obsdb.RecordCompaction, sid+"-t1", sid, "pub_many", i))
	}
	if err := db.Write(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	const serverTok = "srv-token"
	srv := New(DB(db), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	type row struct {
		ID                 string `json:"id"`
		SessionCompactions int    `json:"session_compactions"`
	}
	type page struct {
		Runs   []row     `json:"runs"`
		Capped *treeHole `json:"turn_marks_capped"`
	}
	read := func(tok, path string) page {
		t.Helper()
		code, out := b4Do(t, http.MethodGet, ts.URL+path, tok, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, out)
		}
		var p page
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	big := read(serverTok, "/api/runs?session=s_big")
	if len(big.Runs) != 2 {
		t.Fatalf("s_big's page = %+v; want its two turns", big.Runs)
	}
	for _, r := range big.Runs {
		want := map[string]int{"s_big-t1": 1005, "s_big-t2": 1}[r.ID]
		if r.SessionCompactions != want {
			t.Errorf("%s: %d compactions; want %d (every page read, the labels never counted)", r.ID, r.SessionCompactions, want)
		}
	}
	if big.Capped != nil {
		t.Errorf("s_big's page capped: %+v", big.Capped)
	}
	wide := read(serverTok, fmt.Sprintf("/api/runs?public_id=pub_many&limit=%d", many))
	marked := 0
	for _, r := range wide.Runs {
		marked += r.SessionCompactions
	}
	if len(wide.Runs) != many || marked != turnMarksSessions {
		t.Errorf("pub_many: %d rows, %d marked; want %d rows, %d marked", len(wide.Runs), marked, many, turnMarksSessions)
	}
	if c := wide.Capped; c == nil || c.Hole != "truncated" || c.Cause != "response_cap" || c.Count != many-turnMarksSessions || c.Reason == "" {
		t.Errorf("pub_many's turn_marks_capped = %+v; want truncated · response_cap · count %d", c, many-turnMarksSessions)
	}
	// A panel token's scope sets the public id on every page: its plain
	// list is not a conversation's page and reads no marks.
	pt := mintPanel(t, ts.URL, serverTok, "pub_many")
	if p := read(pt, "/api/runs?limit=5"); len(p.Runs) == 0 || p.Capped != nil || p.Runs[0].SessionCompactions != 0 {
		t.Errorf("a panel token's unnarrowed page = %+v, capped %+v; want rows and no marks", p.Runs, p.Capped)
	}
	if p := read(pt, "/api/runs?public_id=pub_many&limit=5"); len(p.Runs) == 0 || p.Runs[0].SessionCompactions != 1 {
		t.Errorf("a panel token's narrowed page = %+v; want its marks", p.Runs)
	}
}

// failEvents fails every SessionEvents read: the turn marks' first page.
type failEvents struct{ obsdb.DB }

func (f failEvents) SessionEvents(context.Context, obsdb.SessionEventQuery) (obsdb.SessionEventPage, error) {
	return obsdb.SessionEventPage{}, errors.New("session events unreadable")
}

// TestRunsTurnMarksReadFails (P9-pre-go-fix2): a SessionEvents read
// that fails on its first page leaves the session's turns unmarked and
// says so — the turn_marks_capped hole counting them — never a page
// that reads as fully marked.
func TestRunsTurnMarksReadFails(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/fail.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_f-t1", "pub_f", "s_f", "", nil)
	seedRun(t, db, "s_f-t2", "pub_f", "s_f", "", map[string]any{"weft.turn": "2"})
	h := Handler(DB(failEvents{db}))
	_, _, body := get(t, h, "/studio/api/runs?session=s_f")
	var page struct {
		Runs   []json.RawMessage `json:"runs"`
		Capped *treeHole         `json:"turn_marks_capped"`
	}
	decode(t, body, &page)
	if len(page.Runs) != 2 {
		t.Fatalf("s_f's page = %s; want its two turns", body)
	}
	if c := page.Capped; c == nil || c.Hole != "truncated" || c.Count != 2 {
		t.Errorf("turn_marks_capped = %+v; want truncated counting the 2 unmarked turns", c)
	}
}
