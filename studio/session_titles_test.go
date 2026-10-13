package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// A session row carries its title (thread's weft.session.title, the
// newest turn's) and its first turn's prompt preview — what Studio and
// the panel title it with (lib/title.ts) — on the list, the document
// and a panel token's scoped document alike; ?q= finds sessions by
// either (obsdb.SessionQuery.Q), never by a later turn's prompt; an
// untitled session whose first turn stored no content carries
// content_off, so the list can say "(content not captured)" (U6-fix).
func TestSessionTitlesAndQuery(t *testing.T) {
	const serverTok = "srv-token"
	db, err := sqlite.Open(t.TempDir() + "/titles.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "conv_t-t1", "pub_a", "conv_t", "", map[string]any{obsdb.SessionTitleKey: "Order desk"})
	seedRun(t, db, "conv_p-t1", "pub_b", "conv_p", "", nil)
	// Content-off: the run's events, no messages record.
	now := time.Now().UTC()
	var off []obsdb.Record
	for i, ev := range [][2]string{
		{"run_start", `{"type":"run_start","id":"conv_o-t1","model":{"provider":"wefttest","name":"script"},"agent":"acme-support"}`},
		{"run_finish", `{"type":"run_finish","run_id":"conv_o-t1","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`},
	} {
		off = append(off, obsdb.Record{
			Time: now.Add(time.Duration(i) * time.Millisecond), EventName: "weft.event", Severity: 9, Body: ev[1], Service: "svc",
			Attrs: map[string]any{
				"weft.record": "event", "weft.run.id": "conv_o-t1", "gen_ai.agent.name": "acme-support",
				"weft.public_id": "pub_o", "weft.session.id": "conv_o", "weft.turn": "1",
				"weft.event.type": ev[0], "weft.event.pos": int64(i),
			},
			Resource: map[string]any{"service.name": "svc"},
		})
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: off}); err != nil {
		t.Fatal(err)
	}
	srv := New(DB(db), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	type row struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		FirstPrompt string `json:"first_prompt"`
		ContentOff  bool   `json:"content_off"`
	}
	list := func(path string) map[string]row {
		t.Helper()
		code, out := b4Do(t, http.MethodGet, ts.URL+path, serverTok, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, out)
		}
		var page struct {
			Sessions []row `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatal(err)
		}
		got := map[string]row{}
		for _, s := range page.Sessions {
			got[s.ID] = s
		}
		return got
	}
	all := list("/api/sessions")
	if r := all["conv_t"]; r.Title != "Order desk" || r.FirstPrompt != "secret of pub_a" || r.ContentOff {
		t.Errorf("conv_t = %+v, want its title and first prompt", r)
	}
	if r := all["conv_p"]; r.Title != "" || r.FirstPrompt != "secret of pub_b" || r.ContentOff {
		t.Errorf("conv_p = %+v, want no title, its first prompt", r)
	}
	if r := all["conv_o"]; r.Title != "" || r.FirstPrompt != "" || !r.ContentOff {
		t.Errorf("conv_o = %+v, want no title, no prompt, content_off", r)
	}
	if got := list("/api/sessions?q=order+DESK"); len(got) != 1 || got["conv_t"].ID == "" {
		t.Errorf("?q=order DESK = %v, want conv_t by its title", got)
	}
	if got := list("/api/sessions?q=pub_b"); len(got) != 1 || got["conv_p"].ID == "" {
		t.Errorf("?q=pub_b = %v, want conv_p by its first prompt and public id", got)
	}
	// A panel token's document is recomputed over its turns
	// (scopedSessionRow): the title and the first prompt survive it.
	panel := mintPanel(t, ts.URL, serverTok, "pub_a")
	code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions/conv_t", panel, "")
	if code != http.StatusOK {
		t.Fatalf("panel GET /api/sessions/conv_t = %d %s", code, out)
	}
	var doc row
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Order desk" || doc.FirstPrompt != "secret of pub_a" {
		t.Errorf("panel session doc = %+v, want the title and first prompt", doc)
	}
	// The scoped document keeps the mark too (scopedSessionRow).
	panelO := mintPanel(t, ts.URL, serverTok, "pub_o")
	code, out = b4Do(t, http.MethodGet, ts.URL+"/api/sessions/conv_o", panelO, "")
	if code != http.StatusOK {
		t.Fatalf("panel GET /api/sessions/conv_o = %d %s", code, out)
	}
	doc = row{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.ContentOff || doc.FirstPrompt != "" {
		t.Errorf("panel session doc conv_o = %+v, want content_off", doc)
	}
}

// A panel token's session document (scopedSessionRow) marks content_off
// as lib/title.ts's contentOff does: not while the first turn is
// running or settling — its messages records may still arrive (D7).
func TestScopedSessionRowContentOffGate(t *testing.T) {
	base := obsdb.RunRow{ID: "conv-t1", PublicID: "pub", Turn: 1, EventCount: 2, Status: obsdb.StatusSucceeded}
	for _, c := range []struct {
		name string
		edit func(*obsdb.RunRow)
		want bool
	}{
		{"settled, events, no messages", func(*obsdb.RunRow) {}, true},
		{"running", func(r *obsdb.RunRow) { r.Status = obsdb.StatusRunning }, false},
		{"settling", func(r *obsdb.RunRow) { r.Settling = true }, false},
		{"messages stored", func(r *obsdb.RunRow) { r.MessageCount = 1 }, false},
		{"derived", func(r *obsdb.RunRow) { r.Derived = true }, false},
	} {
		r := base
		c.edit(&r)
		got := scopedSessionRow(obsdb.SessionRow{ID: "conv"}, []obsdb.RunRow{r}, "pub")
		if got.ContentOff != c.want {
			t.Errorf("%s: ContentOff = %v, want %v", c.name, got.ContentOff, c.want)
		}
	}
}
