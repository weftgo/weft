package studio

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// getWith issues a GET with a bearer token and optional Origin. It
// never drains the body of an SSE response: those stream forever, so
// the status is read and the connection closed.
func getWith(t *testing.T, h http.Handler, path, bearer, origin string) (int, http.Header, string) {
	t.Helper()
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp.StatusCode, resp.Header, ""
	}
	return resp.StatusCode, resp.Header, readAll(t, resp)
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestTokenAuth pins setup B: with a Token configured the API refuses
// anonymous and wrong bearers; without one it is open (setup A).
func TestTokenAuth(t *testing.T) {
	closed := Handler(DB(fixtureDB(t)), Token("dev-secret"))
	if code, _, body := getWith(t, closed, "/studio/api/runs", "", ""); code != http.StatusUnauthorized ||
		!strings.Contains(body, `"unauthorized"`) {
		t.Errorf("anonymous: %d %s", code, body)
	}
	if code, _, _ := getWith(t, closed, "/studio/api/runs", "wrong", ""); code != http.StatusUnauthorized {
		t.Errorf("wrong bearer: %d", code)
	}
	if code, _, _ := getWith(t, closed, "/studio/api/meta", "", ""); code != http.StatusUnauthorized {
		t.Errorf("meta anonymous: %d", code)
	}
	if code, _, body := getWith(t, closed, "/studio/api/runs", "dev-secret", ""); code != http.StatusOK ||
		!strings.Contains(body, `"total":4`) {
		t.Errorf("right bearer: %d %s", code, body)
	}
	// The query parameter carries the token where headers cannot
	// (EventSource): the live stream's door.
	if code, _, _ := getWith(t, closed, "/studio/api/live?run=r_ok&token=dev-secret", "", ""); code != http.StatusOK {
		t.Errorf("token query parameter: %d", code)
	}
	// The auth capability appears with the group.
	_, _, meta := getWith(t, closed, "/studio/api/meta", "dev-secret", "")
	if !strings.Contains(meta, `"auth"`) {
		t.Errorf("meta capabilities with a token: %s", meta)
	}
	// Setup A: open.
	if code, _, _ := getWith(t, Handler(DB(fixtureDB(t))), "/studio/api/runs", "", ""); code != http.StatusOK {
		t.Errorf("open setup: %d", code)
	}
}

// mintToken mints a panel token through the API with the server
// token, the way a backend does (S4.6 setup C).
func mintToken(t *testing.T, h http.Handler, serverTok string, req string) (int, string) {
	t.Helper()
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	r, err := http.NewRequest(http.MethodPost, srv.URL+"/studio/api/panel-tokens", strings.NewReader(req))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+serverTok)
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, readAll(t, resp)
}

func TestPanelTokenMint(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Token("srv"))

	// Anonymous and panel bearers cannot mint; only the server token.
	if code, _ := mintToken(t, h, "", `{"public_id":"pub_orders"}`); code != http.StatusUnauthorized {
		t.Errorf("anonymous mint: %d", code)
	}
	code, body := mintToken(t, h, "srv", `{"public_id":"pub_orders","ttl":"1h"}`)
	if code != http.StatusOK {
		t.Fatalf("mint: %d %s", code, body)
	}
	var minted struct {
		Token    string    `json:"token"`
		PublicID string    `json:"public_id"`
		Scope    string    `json:"scope"`
		Exp      time.Time `json:"exp"`
	}
	if err := json.Unmarshal([]byte(body), &minted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(minted.Token, "weft_pt.") || minted.PublicID != "pub_orders" ||
		minted.Scope != "read" || minted.Exp.IsZero() {
		t.Fatalf("mint shape: %s", body)
	}
	// A playground mint widens the scope.
	if _, pg := mintToken(t, h, "srv", `{"public_id":"pub_orders","playground":true}`); !strings.Contains(pg, `"scope":"playground"`) {
		t.Errorf("playground mint: %s", pg)
	}
	// Bad requests are 400s.
	for _, req := range []string{`{}`, `{"public_id":"x","ttl":"-1h"}`, `{"public_id":"x","ttl":"forever"}`, `not json`} {
		if code, _ := mintToken(t, h, "srv", req); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", req, code)
		}
	}
	// Without the route (no token configured) the mint is a 404.
	if code, _ := mintToken(t, Handler(DB(fixtureDB(t))), "", `{"public_id":"x"}`); code != http.StatusNotFound {
		t.Errorf("mint without a token configured: %d", code)
	}
}

// TestPanelTokenScoping is the S4.6 gate: a panel token for pub_A
// gets 403 on pub_B's runs, events, spans and live, and pub_A's data
// answers.
func TestPanelTokenScoping(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Token("srv"))
	code, body := mintToken(t, h, "srv", `{"public_id":"pub_orders","ttl":"1h"}`)
	if code != http.StatusOK {
		t.Fatalf("mint: %d %s", code, body)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &minted); err != nil {
		t.Fatal(err)
	}
	tok := minted.Token

	// pub_orders answers.
	for _, path := range []string{
		"/studio/api/runs",
		"/studio/api/runs/r_ok",
		"/studio/api/runs/r_ok/events",
		"/studio/api/runs/r_ok/transcript",
		"/studio/api/runs/r_ok/spans",
		"/studio/api/runs?public_id=pub_orders",
		"/studio/api/sessions",
		"/studio/api/sessions/s_orders",
		"/studio/api/public/pub_orders",
		"/studio/api/live?public_id=pub_orders",
		"/studio/api/live?run=r_ok",
		"/studio/api/live?session=s_orders",
	} {
		if code, _, b := getWith(t, h, path, tok, ""); code != http.StatusOK {
			t.Errorf("%s: %d %s", path, code, b)
		}
	}
	// The runs list is forced onto the token's public id: only the
	// s_orders turns, never s_research's.
	_, _, list := getWith(t, h, "/studio/api/runs", tok, "")
	if !strings.Contains(list, `"total":3`) || strings.Contains(list, `"id":"r_sub"`) {
		t.Errorf("scoped runs list: %s", list)
	}

	// Everything that reaches pub_research (or nothing at all) is a
	// 403: the other public id's runs, events, transcript, spans,
	// sessions, resolution and live.
	for _, path := range []string{
		"/studio/api/runs?public_id=pub_research",
		"/studio/api/runs/r_sub",
		"/studio/api/runs/r_sub/events",
		"/studio/api/runs/r_sub/transcript",
		"/studio/api/runs/r_sub/spans",
		"/studio/api/sessions?public_id=pub_research",
		"/studio/api/sessions/s_research",
		"/studio/api/public/pub_research",
		"/studio/api/live?public_id=pub_research",
		"/studio/api/live?run=r_sub",
		"/studio/api/live?session=s_research",
		"/studio/api/live?agent=researcher", // an agent selector cannot be scoped
	} {
		if code, _, b := getWith(t, h, path, tok, ""); code != http.StatusForbidden || !strings.Contains(b, `"forbidden"`) {
			t.Errorf("%s: %d %s, want 403", path, code, b)
		}
	}
	// The fixture trace spans both public ids (s_orders and
	// s_research runs share it): a panel token refuses the whole
	// trace rather than leak the other half. (Its own public id's
	// spans alone would pass — pinned above through runs/{id}/spans.)
	if code, _, _ := getWith(t, h, "/studio/api/traces/"+fxTrace, tok, ""); code != http.StatusForbidden {
		t.Errorf("mixed trace: %d, want 403", code)
	}
}

// TestPanelTokenValidity pins the signature and expiry rules.
func TestPanelTokenValidity(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Token("srv"))
	key := []byte("srv")

	// A tampered signature is a 401.
	claims := panelClaims{PublicID: "pub_orders", Scope: scopeRead, Exp: time.Now().Add(time.Hour).UTC()}
	tok, err := signPanelToken(key, claims)
	if err != nil {
		t.Fatal(err)
	}
	tampered := tok[:len(tok)-2] + "aa"
	if code, _, _ := getWith(t, h, "/studio/api/runs", tampered, ""); code != http.StatusUnauthorized {
		t.Errorf("tampered: %d", code)
	}
	// An expired token is a 401.
	claims.Exp = time.Now().Add(-time.Minute)
	expired, err := signPanelToken(key, claims)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := getWith(t, h, "/studio/api/runs", expired, ""); code != http.StatusUnauthorized {
		t.Errorf("expired: %d", code)
	}
	// Signed with the wrong key: a 401.
	other, err := signPanelToken([]byte("other"), panelClaims{PublicID: "pub_orders", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := getWith(t, h, "/studio/api/runs", other, ""); code != http.StatusUnauthorized {
		t.Errorf("wrong key: %d", code)
	}
	if _, err := parsePanelToken(key, tok); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
}

// TestCORS pins S4.6's origins: the localhost default when a token is
// configured, explicit AllowOrigins, nothing same-origin (setup A),
// and preflights answered before routing.
func TestCORS(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Token("dev"))
	preflight := func(h http.Handler, path, origin string) (int, http.Header) {
		srv := httptest.NewServer(mounted(h))
		t.Cleanup(srv.Close)
		req, err := http.NewRequest(http.MethodOptions, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode, resp.Header
	}

	// Any localhost port, http or https.
	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:5173", "https://localhost"} {
		code, hdr := preflight(h, "/studio/api/runs", origin)
		if code != http.StatusNoContent || hdr.Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("%s: %d %v", origin, code, hdr)
		}
		if vary := hdr.Get("Vary"); !strings.Contains(vary, "Origin") {
			t.Errorf("Vary: %q", vary)
		}
	}
	// A foreign origin gets no CORS headers; the preflight falls
	// through to the API's own answer (here the token check's 401),
	// which fails the browser's preflight exactly as intended.
	code, hdr := preflight(h, "/studio/api/runs", "https://evil.example")
	if hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("foreign origin allowed: %v", hdr)
	}
	if code == http.StatusNoContent {
		t.Errorf("foreign preflight answered 204: %v", hdr)
	}
	// An actual GET with an allowed origin carries the header; the
	// bearer still applies.
	code, hdr, _ = getWith(t, h, "/studio/api/meta", "dev", "http://localhost:3000")
	if code != http.StatusOK || hdr.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("GET with origin: %d %v", code, hdr)
	}
	// Explicit AllowOrigins replace the default.
	custom := Handler(DB(fixtureDB(t)), Token("dev"), AllowOrigins("https://app.example"))
	if code, hdr := preflight(custom, "/studio/api/runs", "https://app.example"); code != http.StatusNoContent ||
		hdr.Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Errorf("explicit origin: %d %v", code, hdr)
	}
	if _, hdr := preflight(custom, "/studio/api/runs", "http://localhost:3000"); hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("default leaked past explicit list: %v", hdr)
	}
	// Setup A: same origin, no CORS at all.
	open := Handler(DB(fixtureDB(t)))
	if _, hdr := preflight(open, "/studio/api/runs", "http://localhost:3000"); hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("open handler sent CORS: %v", hdr)
	}
}
