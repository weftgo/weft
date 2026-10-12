package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// mintPanel mints a panel token for pub with the server token.
func mintPanel(t *testing.T, base, serverTok, pub string) string {
	t.Helper()
	code, out := b4Do(t, http.MethodPost, base+"/api/panel-tokens", serverTok, `{"public_id":"`+pub+`"}`)
	if code != http.StatusOK {
		t.Fatalf("mint: %d %s", code, out)
	}
	var m struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	return m.Token
}

// A panel token's sessions list is scoped in the query (obsdb's
// SessionQuery.PublicID): a row's aggregates are its public id's runs
// whether or not the cost walk can read them, so a row whose walk fails
// (or finds nothing — a session deleted between the list and the walk)
// is served as the server token's is: the row, with an unknown cost.
// It used to be skipped (TestPanelSessionsSkipUnscopable), because its
// aggregates were then the unscoped GROUP BY's and could not be
// recomputed; the page no longer comes back short of what it counts.
func TestPanelSessionsScopedRowsServedWhole(t *testing.T) {
	const serverTok = "srv-token"
	db, err := sqlite.Open(t.TempDir() + "/skip.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "r_ok", "pub_a", "conv_ok", "", nil)
	seedRun(t, db, "r_bad", "pub_a", "conv_bad", "", nil)
	seedRun(t, db, "r_gone", "pub_a", "conv_gone", "", nil)
	seedRun(t, db, "r_b", "pub_b", "conv_b", "", nil)
	srv := New(DB(sessionRunsDB{DB: db, fail: "conv_bad", empty: "conv_gone"}), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	panel := mintPanel(t, ts.URL, serverTok, "pub_a")

	type row struct {
		ID        string   `json:"id"`
		PublicID  string   `json:"public_id"`
		Turns     int      `json:"turns"`
		CostUSD   *float64 `json:"cost_usd"`
		CostBadge string   `json:"cost_badge"`
	}
	list := func(bearer string) (int, map[string]row) {
		t.Helper()
		code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions", bearer, "")
		if code != http.StatusOK {
			t.Fatalf("GET /api/sessions = %d %s, want 200", code, out)
		}
		var page struct {
			Total    int   `json:"total"`
			Sessions []row `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatal(err)
		}
		got := map[string]row{}
		for _, s := range page.Sessions {
			got[s.ID] = s
		}
		return page.Total, got
	}
	total, got := list(panel)
	if total != 3 || len(got) != 3 {
		t.Fatalf("panel GET /api/sessions = total %d, %v; want pub_a's three sessions, counted and listed", total, got)
	}
	for id, r := range got {
		if r.PublicID != "pub_a" || r.Turns != 1 {
			t.Errorf("panel row %s = %+v, want pub_a's one turn", id, r)
		}
	}
	if bad := got["conv_bad"]; bad.CostUSD != nil || bad.CostBadge != "unknown" {
		t.Errorf("panel row conv_bad = %+v, want its cost unknown: its runs could not be read", bad)
	}
	if total, got := list(serverTok); total != 4 || len(got) != 4 {
		t.Errorf("server GET /api/sessions = total %d, %v; want all four sessions", total, got)
	}
}

// A panel token's sessions page — its order, total and cursor — is its
// public id's view: a newer derived run colliding on a pub_a session's
// id neither moves that session to the top nor becomes the cursor, and
// sessions without a pub_a turn are not counted. Walking the panel's
// pages one at a time reads pub_a's sessions newest first, each once.
func TestPanelSessionsPageScopedOrderAndCursor(t *testing.T) {
	const serverTok = "srv-token"
	db, err := sqlite.Open(t.TempDir() + "/cursor.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "r_x", "pub_a", "conv_x", "", nil)
	time.Sleep(5 * time.Millisecond)
	seedRun(t, db, "r_y", "pub_a", "conv_y", "", nil)
	seedRun(t, db, "r_b", "pub_b", "conv_b", "", nil)
	// Another app's stock root on conv_x, an hour newer than any turn.
	later := time.Now().UTC().Add(time.Hour)
	derived := obsdb.Span{
		TraceID: "5c0c0ffee0000000000000000000c001", SpanID: "5c00000000000001", Name: "invoke_agent other", Kind: 1,
		Start: later, End: later.Add(time.Second), StatusCode: 1, Service: "py-other",
		Attrs: map[string]any{
			"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "other",
			"gen_ai.conversation.id": "conv_x", "gen_ai.usage.input_tokens": int64(500),
		},
		Resource: map[string]any{"service.name": "py-other"},
	}
	if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{derived}}); err != nil {
		t.Fatal(err)
	}
	srv := New(DB(db), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	panel := mintPanel(t, ts.URL, serverTok, "pub_a")

	type page struct {
		Total    int `json:"total"`
		Sessions []struct {
			ID       string    `json:"id"`
			Turns    int       `json:"turns"`
			LastSeen time.Time `json:"last_seen"`
		} `json:"sessions"`
		NextBefore   *time.Time `json:"next_before"`
		NextBeforeID *string    `json:"next_before_id"`
	}
	get := func(bearer, path string) page {
		t.Helper()
		code, out := b4Do(t, http.MethodGet, ts.URL+path, bearer, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, out)
		}
		var p page
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The server token's page is the unscoped view: the derived run
	// makes conv_x the newest, with two turns.
	if dev := get(serverTok, "/api/sessions?limit=1"); len(dev.Sessions) != 1 || dev.Sessions[0].ID != "conv_x" || dev.Sessions[0].Turns != 2 || dev.Total != 3 {
		t.Errorf("server page 1 = %+v, want conv_x (the derived run's last seen), 2 turns, total 3", dev)
	}
	first := get(panel, "/api/sessions?limit=1")
	if first.Total != 2 || len(first.Sessions) != 1 || first.Sessions[0].ID != "conv_y" || first.NextBefore == nil || first.NextBeforeID == nil {
		t.Fatalf("panel page 1 = %+v, want conv_y (pub_a's newest), total 2, a cursor", first)
	}
	if !first.NextBefore.Equal(first.Sessions[0].LastSeen) || first.NextBefore.After(later.Add(-time.Minute)) {
		t.Errorf("panel cursor = %v, want conv_y's own last seen, never the derived run's %v", first.NextBefore, later)
	}
	second := get(panel, "/api/sessions?limit=1&before="+url.QueryEscape(first.NextBefore.Format(time.RFC3339Nano))+
		"&before_id="+url.QueryEscape(*first.NextBeforeID))
	if second.Total != 2 || len(second.Sessions) != 1 || second.Sessions[0].ID != "conv_x" || second.Sessions[0].Turns != 1 {
		t.Fatalf("panel page 2 = %+v, want conv_x with pub_a's one turn", second)
	}
	if second.Sessions[0].LastSeen.After(later.Add(-time.Minute)) {
		t.Errorf("panel conv_x last_seen = %v, want r_x's, not the derived run's %v", second.Sessions[0].LastSeen, later)
	}
}
