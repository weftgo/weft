package studio

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The devtools panel's Go side (WEFT-DEVTOOLS.md §5.1, S4.2): the
// bundle served at /panel.js through the route group studio/panel.go
// registers. The panel's behaviour — attributes, surfaces, fail-silent
// — is pinned by the web suite (web/src/panel/*.test.ts); these tests
// pin what the Go handler promises.

// newPanelServer builds a fixture-backed server with a token, the
// shape setups B and C see.
func newPanelServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(DB(fixtureDB(t)), Token("server-tok")))
	t.Cleanup(srv.Close)
	return srv
}

// TestPanelJSRoute pins S4.2's /panel.js row: served by Handler, GET,
// no auth (a page on another origin loads it; the panel carries the
// API token, not the script URL), the committed bundle's bytes, and
// the caching headers a static versioned artifact carries.
func TestPanelJSRoute(t *testing.T) {
	srv := httptest.NewServer(New(DB(fixtureDB(t))))
	defer srv.Close()

	// No token: still served (static, not API).
	res, err := http.Get(srv.URL + "/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /panel.js: %s", res.Status)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	want, err := panelJS.ReadFile("dist/panel/panel.js")
	if err != nil {
		t.Fatalf("embedded bundle: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := res.Body.Read(got); err != nil && err.Error() != "EOF" {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(want) {
		t.Error("served panel.js is not the embedded bundle's bytes")
	}

	// The panel group registers under its own name and adds no
	// capability: the panel is a client, not a verb (§8.1).
	s := New(DB(fixtureDB(t)))
	defer func() { _ = s.Close() }()
	found := false
	for _, g := range s.groups {
		if g.name == "panel" {
			found = true
		}
		if g.capability != "" && g.capability == "panel" {
			t.Error("the panel group must not name a capability")
		}
	}
	if !found {
		t.Error("the panel group is not registered")
	}
	if caps := s.capabilityList(); !contains(caps, "live") {
		t.Errorf("capabilityList = %v, want live present (rung 1)", caps)
	}
}

// (The package's contains — playground.go — serves this file too; the
// lanes each declared one, the merge keeps the production copy.)

// TestPanelJSHead pins the HEAD shape: headers without the body.
func TestPanelJSHead(t *testing.T) {
	srv := httptest.NewServer(New(DB(fixtureDB(t))))
	defer srv.Close()
	res, err := http.Head(srv.URL + "/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("HEAD /panel.js: %s", res.Status)
	}
	if res.ContentLength == 0 {
		t.Error("HEAD /panel.js reports no length")
	}
}

// TestPanelJSMissingBuild pins the loud failure: a build without the
// bundle answers 500 in the API error shape, never an empty 200 a
// page would cache.
func TestPanelJSMissingBuild(t *testing.T) {
	missing := embed.FS{} // no dist/panel/panel.js inside
	if _, err := missing.ReadFile("dist/panel/panel.js"); err == nil {
		t.Fatal("an empty FS must not read the bundle")
	}
	s := New(DB(fixtureDB(t)))
	defer func() { _ = s.Close() }()
	orig := panelJS
	panelJS = missing
	defer func() { panelJS = orig }()
	rec := httptest.NewRecorder()
	s.servePanelJS(rec, httptest.NewRequest(http.MethodGet, "/panel.js", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "panel.js is missing") {
		t.Errorf("body = %q, want the missing-build message", rec.Body.String())
	}
}

// TestPanelEmbedMatchesCommitted pins the artifact contract (§5.1):
// the embedded bundle is the committed dist/panel/panel.js, and it
// was built by this module's own panel config — the studio version it
// understands is stamped into it and must equal studio.Version, or
// the panel would refuse the Studio it shipped with (§5.1's version
// check would see "newer").
func TestPanelEmbedMatchesCommitted(t *testing.T) {
	b, err := panelJS.ReadFile("dist/panel/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, Version) {
		t.Errorf("panel.js does not carry the studio version %q it was built against", Version)
	}
	if strings.Contains(body, "react") {
		t.Error("panel.js contains 'react': a component import leaked into the panel build")
	}
}

// TestPanelTokenMintExample pins the setup-C flow end to end against
// the fixture server: a backend holding the server token mints a
// panel token for one public id through POST /api/panel-tokens
// (S4.6), and the scoped token reads that conversation but is refused
// another's runs — the Dv2 gate's second half, runnable offline.
func TestPanelTokenMintExample(t *testing.T) {
	srv := newPanelServer(t)

	// The backend's mint call (your page handler does exactly this).
	mint, err := http.NewRequest(http.MethodPost, srv.URL+"/api/panel-tokens",
		strings.NewReader(`{"public_id":"pub_orders","ttl":"1h"}`))
	if err != nil {
		t.Fatal(err)
	}
	mint.Header.Set("Authorization", "Bearer server-tok")
	mint.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(mint)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mint: %s", res.Status)
	}
	var tok struct {
		Token    string `json:"token"`
		PublicID string `json:"public_id"`
		Scope    string `json:"scope"`
		Exp      string `json:"exp"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	if tok.PublicID != "pub_orders" || tok.Scope != "read" {
		t.Fatalf("mint = %+v, want pub_orders read", tok)
	}
	if !strings.HasPrefix(tok.Token, "weft_pt.") {
		t.Fatalf("token = %q, want the weft_pt. prefix", tok.Token)
	}

	// The panel reads its own conversation…
	get := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res = get("/api/runs?public_id=pub_orders")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("scoped runs: %s", res.Status)
	}

	// …and cannot read another public id's runs, events, spans or
	// live selector (S4.6: the scoping that makes a page token safe).
	for _, path := range []string{
		"/api/runs?public_id=pub_research",
		"/api/runs/r_sub/events",
		"/api/runs/r_sub/spans",
		"/api/live?public_id=pub_research",
	} {
		res := get(path)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s with a pub_orders token: %d, want 403", path, res.StatusCode)
		}
	}
}
