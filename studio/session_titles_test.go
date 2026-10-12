package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// A session row carries its title (thread's weft.session.title, the
// newest turn's) and its first turn's prompt preview — what Studio and
// the panel title it with (lib/title.ts) — on the list, the document
// and a panel token's scoped document alike; ?q= finds sessions by
// either (obsdb.SessionQuery.Q), never by a later turn's prompt.
func TestSessionTitlesAndQuery(t *testing.T) {
	const serverTok = "srv-token"
	db, err := sqlite.Open(t.TempDir() + "/titles.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "conv_t-t1", "pub_a", "conv_t", "", map[string]any{obsdb.SessionTitleKey: "Order desk"})
	seedRun(t, db, "conv_p-t1", "pub_b", "conv_p", "", nil)
	srv := New(DB(db), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	type row struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		FirstPrompt string `json:"first_prompt"`
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
	if r := all["conv_t"]; r.Title != "Order desk" || r.FirstPrompt != "secret of pub_a" {
		t.Errorf("conv_t = %+v, want its title and first prompt", r)
	}
	if r := all["conv_p"]; r.Title != "" || r.FirstPrompt != "secret of pub_b" {
		t.Errorf("conv_p = %+v, want no title, its first prompt", r)
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
}
