package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb/sqlite"
)

// twoPublicIDSession serves a session whose turns carry two public ids
// (hand-set session metadata, a colliding app): conv_s holds r_a
// (pub_a) and r_b (pub_b).
func twoPublicIDSession(t *testing.T) (*httptest.Server, http.Handler, string) {
	t.Helper()
	const serverTok = "srv-token"
	db, err := sqlite.Open(t.TempDir() + "/two.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "r_a", "pub_a", "conv_s", "", nil)
	seedRun(t, db, "r_b", "pub_b", "conv_s", "", nil)
	// Each run's 1 + 1 tokens cost $2 at these rates: a row's cost says
	// how many runs it priced.
	perToken := PricingTable{Dated: "2026-10-12", Models: []ModelPrice{{Provider: "wefttest", ModelGlob: "script", Input: 1e6, Output: 1e6}}}
	srv := New(DB(db), Token(serverTok), Pricing(perToken))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv.Handler(), serverTok
}

type scopedSessRow struct {
	ID       string   `json:"id"`
	PublicID string   `json:"public_id"`
	Turns    int      `json:"turns"`
	CostUSD  *float64 `json:"cost_usd"`
	Runs     []struct {
		ID string `json:"id"`
	} `json:"runs"`
}

// The server token's ?public_id= row is one scope throughout: its turns,
// its usage and its cost are that public id's runs. The cost walk used
// to read the whole session (the server token has no panel public id),
// so pub_a's row priced r_b too.
func TestServerSessionsPublicIDRowCostScoped(t *testing.T) {
	ts, _, serverTok := twoPublicIDSession(t)
	for _, c := range []struct{ pub, own, other string }{{"pub_a", "r_a", "r_b"}, {"pub_b", "r_b", "r_a"}} {
		code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions?public_id="+c.pub, serverTok, "")
		if code != http.StatusOK {
			t.Fatalf("GET ?public_id=%s = %d %s", c.pub, code, out)
		}
		var page struct {
			Sessions []scopedSessRow `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Sessions) != 1 || page.Sessions[0].Turns != 1 {
			t.Fatalf("?public_id=%s = %+v, want conv_s with %s's one turn", c.pub, page.Sessions, c.pub)
		}
		r := page.Sessions[0]
		if r.CostUSD == nil || *r.CostUSD != 2 {
			t.Errorf("?public_id=%s cost_usd = %v, want 2: %s's run %s alone, never %s", c.pub, r.CostUSD, c.pub, c.own, c.other)
		}
	}
}

// A panel token may open every session its list shows: the session view
// and the live session= selector admit it when any turn of the session
// carries its public id (the list's WHERE), and the view serves its
// turns alone. The rule used to be the session's MAX(public_id) — pub_b
// here — so pub_a's token listed conv_s and got 403 opening it.
func TestPanelSessionTwoPublicIDsListedAndOpened(t *testing.T) {
	ts, h, serverTok := twoPublicIDSession(t)
	for _, c := range []struct{ pub, own string }{{"pub_a", "r_a"}, {"pub_b", "r_b"}} {
		panel := mintPanel(t, ts.URL, serverTok, c.pub)
		code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions", panel, "")
		if code != http.StatusOK || !strings.Contains(out, `"conv_s"`) {
			t.Fatalf("%s GET /api/sessions = %d %s, want conv_s listed", c.pub, code, out)
		}
		code, out = b4Do(t, http.MethodGet, ts.URL+"/api/sessions/conv_s", panel, "")
		if code != http.StatusOK {
			t.Fatalf("%s GET /api/sessions/conv_s = %d %s, want 200: the list shows it", c.pub, code, out)
		}
		var doc scopedSessRow
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.PublicID != c.pub || doc.Turns != 1 || len(doc.Runs) != 1 || doc.Runs[0].ID != c.own {
			t.Errorf("%s session doc = %+v, want its one turn %s alone", c.pub, doc, c.own)
		}
		if code, _, out := getWith(t, h, "/studio/api/live?session=conv_s", panel, ""); code != http.StatusOK {
			t.Errorf("%s live session=conv_s = %d %s, want admitted", c.pub, code, out)
		}
	}
	other := mintPanel(t, ts.URL, serverTok, "pub_c")
	if code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions/conv_s", other, ""); code != http.StatusForbidden {
		t.Errorf("pub_c GET /api/sessions/conv_s = %d %s, want 403", code, out)
	}
	if code, _, out := getWith(t, h, "/studio/api/live?session=conv_s", other, ""); code != http.StatusForbidden {
		t.Errorf("pub_c live session=conv_s = %d %s, want 403", code, out)
	}
}
