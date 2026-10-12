package studio

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// The identity rules (S4.6, TestAuthMatrix's table) hold for a derived
// run's id shape, otel-<trace>-<span> (docs/record-contract.md §9): the
// server token reads it; a panel token — read or playground scoped —
// is 403, since a derived run carries no public id (scoping is by
// weft.public_id, which a stock app never sets); without a token, 401;
// an unknown otel- id is 404 to every identity that may ask. A row of
// the matrix per token kind, in a file of its own beside it.
func TestDerivedRunAuth(t *testing.T) {
	const serverTok, ingestTok = "srv-token", "ingest-token"
	srv := New(Open(t.TempDir()+"/derived-auth.db"), Playground(true), Token(serverTok), IngestToken(ingestTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	b, err := os.ReadFile("../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	if code, out := b4Do(t, http.MethodPost, ts.URL+"/v1/traces", ingestTok, string(b)); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, out)
	}
	mint := func(body string) string {
		code, out := b4Do(t, http.MethodPost, ts.URL+"/api/panel-tokens", serverTok, body)
		if code != http.StatusOK {
			t.Fatalf("mint %s: %d %s", body, code, out)
		}
		var m struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatal(err)
		}
		return m.Token
	}
	read := mint(`{"public_id":"pub_a"}`)
	play := mint(`{"public_id":"pub_a","playground":true}`)
	const unknown = "otel-0123456789abcdef0123456789abcdef-0123456789abcdef"

	for _, c := range []struct {
		who, bearer, id string
		want            int
	}{
		{"server", serverTok, derivedOK, http.StatusOK},
		{"panel read", read, derivedOK, http.StatusForbidden},
		{"panel playground", play, derivedOK, http.StatusForbidden},
		{"no token", "", derivedOK, http.StatusUnauthorized},
		{"ingest token", ingestTok, derivedOK, http.StatusUnauthorized},
		{"server, unknown", serverTok, unknown, http.StatusNotFound},
		{"panel read, unknown", read, unknown, http.StatusNotFound},
	} {
		code, out := b4Do(t, http.MethodGet, ts.URL+"/api/runs/"+c.id, c.bearer, "")
		if code != c.want {
			t.Errorf("%s GET /api/runs/%s = %d %s, want %d", c.who, c.id, code, out, c.want)
		}
		if c.want == http.StatusOK && !strings.Contains(out, `"derived":true`) {
			t.Errorf("%s: %s, want the derived row", c.who, out)
		}
	}
	// A panel token's list is its public id's: no derived run in it.
	code, out := b4Do(t, http.MethodGet, ts.URL+"/api/runs", read, "")
	if code != http.StatusOK || strings.Contains(out, "otel-") {
		t.Errorf("panel GET /api/runs = %d %s, want none of the derived runs", code, out)
	}
}

// A derived run's session id is its gen_ai.conversation.id, which any
// stock app chooses: a stock trace that collides with a weft session's
// id joins that session's GROUP BY. A panel token's session is its
// public id's turns — the runs list's rule — so its session view, the
// sessions list and the session's cost neither list nor sum the
// colliding derived runs (docs/record-contract.md §9: a panel token
// never reaches a derived run); the server token still sees all.
func TestDerivedRunSessionCollision(t *testing.T) {
	const serverTok, ingestTok = "srv-token", "ingest-token"
	db, err := sqlite.Open(t.TempDir() + "/collide.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Every model priced (wefttest/script and gpt-5 alike, $1000 per 1M
	// either way), so a session cost that summed the derived runs would
	// differ from r_mine's own: the cost scoping is pinned, not only the
	// rows.
	srv := New(DB(db), Token(serverTok), IngestToken(ingestTok),
		Pricing(PricingTable{Dated: "2026-10-01", Models: []ModelPrice{{Provider: "*", ModelGlob: "*", Input: 1000, Output: 1000}}}))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// The weft turn of pub_a's session conv_7f3a (usage 1/1)…
	seedRun(t, db, "r_mine", "pub_a", "conv_7f3a", "", nil)
	// …and another app's stock trace with the same conversation id.
	b, err := os.ReadFile("../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	if code, out := b4Do(t, http.MethodPost, ts.URL+"/v1/traces", ingestTok, string(b)); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, out)
	}
	code, out := b4Do(t, http.MethodPost, ts.URL+"/api/panel-tokens", serverTok, `{"public_id":"pub_a"}`)
	if code != http.StatusOK {
		t.Fatalf("mint: %d %s", code, out)
	}
	var m struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	type sess struct {
		Turns int `json:"turns"`
		Usage struct {
			InputTokens int64 `json:"input_tokens"`
		} `json:"usage"`
		CostUSD *float64 `json:"cost_usd"`
		Runs    []struct {
			ID      string   `json:"id"`
			CostUSD *float64 `json:"cost_usd"`
		} `json:"runs"`
	}
	// r_mine's own cost: 1 input + 1 output token at $1000 per 1M.
	const mine = 0.002
	costIs := func(c *float64) bool { return c != nil && math.Abs(*c-mine) < 1e-12 }
	read := func(bearer, path string, into any) string {
		t.Helper()
		code, out := b4Do(t, http.MethodGet, ts.URL+path, bearer, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, out)
		}
		if err := json.Unmarshal([]byte(out), into); err != nil {
			t.Fatal(err)
		}
		return out
	}

	var panel sess
	out = read(m.Token, "/api/sessions/conv_7f3a", &panel)
	if strings.Contains(out, "otel-") || strings.Contains(out, "gpt-5") ||
		panel.Turns != 1 || len(panel.Runs) != 1 || panel.Runs[0].ID != "r_mine" || panel.Usage.InputTokens != 1 {
		t.Errorf("panel GET /api/sessions/conv_7f3a = %s, want r_mine alone: 1 turn, input 1", out)
	}
	if !costIs(panel.CostUSD) || len(panel.Runs) != 1 || !costIs(panel.Runs[0].CostUSD) {
		t.Errorf("panel GET /api/sessions/conv_7f3a = %s, want the session's cost_usd r_mine's own, %v", out, mine)
	}
	var list struct {
		Sessions []sess `json:"sessions"`
	}
	out = read(m.Token, "/api/sessions", &list)
	if len(list.Sessions) != 1 || list.Sessions[0].Turns != 1 || list.Sessions[0].Usage.InputTokens != 1 {
		t.Errorf("panel GET /api/sessions = %s, want conv_7f3a with 1 turn, input 1", out)
	}
	if len(list.Sessions) != 1 || !costIs(list.Sessions[0].CostUSD) {
		t.Errorf("panel GET /api/sessions = %s, want conv_7f3a's cost_usd r_mine's own, %v", out, mine)
	}
	var runs struct {
		Runs []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	read(m.Token, "/api/runs?session=conv_7f3a", &runs)
	if len(runs.Runs) != panel.Turns {
		t.Errorf("panel runs?session= lists %d runs, the session %d turns: they must agree", len(runs.Runs), panel.Turns)
	}

	var dev sess
	out = read(serverTok, "/api/sessions/conv_7f3a", &dev)
	if dev.Turns != 3 || len(dev.Runs) != 3 || !strings.Contains(out, "otel-") {
		t.Errorf("server GET /api/sessions/conv_7f3a = %s, want the weft turn and both derived runs", out)
	}
	if dev.CostUSD == nil || *dev.CostUSD <= mine {
		t.Errorf("server GET /api/sessions/conv_7f3a = %s, want a cost_usd above r_mine's own (the derived runs summed)", out)
	}
}

// sessionRunsDB fails the runs walk of one session and finds no run of
// another, as a read error or a session deleted between the sessions
// list and the walk would.
type sessionRunsDB struct {
	obsdb.DB
	fail, empty string
}

func (d sessionRunsDB) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	switch q.SessionID {
	case d.fail:
		return obsdb.RunPage{}, errors.New("disk on fire")
	case d.empty:
		return obsdb.RunPage{}, nil
	}
	return d.DB.Runs(ctx, q)
}
