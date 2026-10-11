package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
