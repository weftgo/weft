package studio

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/scope"
)

// handoffServer is a setup C Studio (a Token, so panel tokens exist)
// mounted under /studio/ with its clock fixed at now.
func handoffServer(t *testing.T, now time.Time) (*Server, *httptest.Server) {
	t.Helper()
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token("srv-token"))
	srv.now = func() time.Time { return now }
	t.Cleanup(func() { _ = srv.Close() })
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", srv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	seedRun(t, srv.db, "run_a", "pub_a", "", "", nil)
	seedRun(t, srv.db, "run_b", "pub_b", "", "", nil)
	return srv, ts
}

// handoffGet asks for a hand-off as tok, never following the redirect.
func handoffGet(t *testing.T, ts *httptest.Server, rawQuery, tok, accept string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/studio/api/panel-handoff?"+rawQuery, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// apiStatus is GET path's status as tok.
func apiStatus(t *testing.T, ts *httptest.Server, path, tok string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/studio"+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestPanelHandoff pins plan G3's setup C hand-off: a playground-scoped
// panel token is redirected (303) to <base><to>#token=<minted>, the
// minted token read-scoped to the same public id for five minutes —
// it reads that public id's runs and no other's, and cannot hand off
// itself — and the hand-off is logged without the token; the server
// token is redirected with no fragment; a read-scoped token is 403;
// anonymous is 401.
func TestPanelHandoff(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv, ts := handoffServer(t, now)
	sign := func(c panelClaims) string {
		tok, err := signPanelToken([]byte("srv-token"), c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	pg := sign(panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: now.Add(time.Hour)})
	read := sign(panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: now.Add(time.Hour)})

	logs := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	to := "/runs/run_a?step=1"
	resp := handoffGet(t, ts, "to="+url.QueryEscape(to), pg, "")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("playground hand-off = %d, want 303", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (the Location carries a token)", cc)
	}
	// The Location's shape: a path under the base, the query kept, the
	// minted token in the fragment alone.
	loc := resp.Header.Get("Location")
	path, frag, ok := strings.Cut(loc, "#")
	if !ok || path != "/studio/runs/run_a?step=1" || !strings.HasPrefix(frag, "token="+panelTokenPrefix) {
		t.Fatalf("Location = %q, want /studio/runs/run_a?step=1#token=%s…", loc, panelTokenPrefix)
	}
	minted := strings.TrimPrefix(frag, "token=")
	claims, err := parsePanelToken([]byte("srv-token"), minted)
	if err != nil {
		t.Fatalf("the minted token does not verify: %v", err)
	}
	if claims.PublicID != "pub_a" || claims.Scope != scopeRead || !claims.Exp.Equal(now.Add(handoffTTL)) {
		t.Errorf("minted claims = %+v, want pub_a, read, exp %v (now + 5 min)", claims, now.Add(handoffTTL))
	}
	if handoffTTL != 5*time.Minute {
		t.Errorf("handoffTTL = %v, want 5m", handoffTTL)
	}
	// The minted token is a read token for exactly that conversation.
	if code := apiStatus(t, ts, "/api/runs?public_id=pub_a", minted); code != http.StatusOK {
		t.Errorf("minted: runs?public_id=pub_a = %d, want 200", code)
	}
	if code := apiStatus(t, ts, "/api/runs?public_id=pub_b", minted); code != http.StatusForbidden {
		t.Errorf("minted: runs?public_id=pub_b = %d, want 403", code)
	}
	if code := apiStatus(t, ts, "/api/runs/run_b", minted); code != http.StatusForbidden {
		t.Errorf("minted: runs/run_b = %d, want 403", code)
	}
	if r := handoffGet(t, ts, "to=/runs/run_a", minted, ""); r.StatusCode != http.StatusForbidden {
		t.Errorf("the minted read token handing off = %d, want 403 (it cannot widen)", r.StatusCode)
	}
	// Logged as a hand-off, never with the token.
	out := logs.String()
	if !strings.Contains(out, "panel hand-off") || !strings.Contains(out, "public_id=pub_a") || !strings.Contains(out, `to="`+to+`"`) {
		t.Errorf("hand-off log line missing: %q", out)
	}
	if strings.Contains(out, minted) || strings.Contains(out, pg) || strings.Contains(out, panelTokenPrefix) {
		t.Errorf("the log carries a token: %q", out)
	}

	// Never past the asking token's own expiry.
	short := sign(panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: now.Add(2 * time.Minute)})
	r := handoffGet(t, ts, "to=/runs/run_a", short, "")
	_, f, _ := strings.Cut(r.Header.Get("Location"), "#token=")
	if c, err := parsePanelToken([]byte("srv-token"), f); err != nil || !c.Exp.Equal(now.Add(2*time.Minute)) {
		t.Errorf("hand-off from a token expiring in 2m: exp %v (%v), want capped at %v", c.Exp, err, now.Add(2*time.Minute))
	}

	// The JSON answer (the panel's fetch cannot read a redirect).
	r = handoffGet(t, ts, "to=/runs/run_a", pg, "application/json")
	var doc struct {
		Location string    `json:"location"`
		Exp      time.Time `json:"exp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("JSON hand-off = %d (%v)", r.StatusCode, err)
	}
	if !strings.HasPrefix(doc.Location, "/studio/runs/run_a#token="+panelTokenPrefix) || !doc.Exp.Equal(now.Add(handoffTTL)) {
		t.Errorf("JSON hand-off = %+v", doc)
	}

	// The server token needs no token in the page: 303, no fragment.
	r = handoffGet(t, ts, "to=/runs/run_a", "srv-token", "")
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/studio/runs/run_a" {
		t.Errorf("server token: %d %q, want 303 /studio/runs/run_a", r.StatusCode, r.Header.Get("Location"))
	}
	// A read-scoped token cannot widen; anonymous is walled.
	if r := handoffGet(t, ts, "to=/runs/run_a", read, ""); r.StatusCode != http.StatusForbidden {
		t.Errorf("read token = %d, want 403", r.StatusCode)
	} else if b, _ := io.ReadAll(r.Body); !strings.Contains(string(b), "cannot widen") {
		t.Errorf("read token's refusal = %s, want it to say it cannot widen", b)
	}
	if r := handoffGet(t, ts, "to=/runs/run_a", "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", r.StatusCode)
	}
	_ = srv
}

// TestPanelHandoffOpenRedirect: to is a path under the base or the
// request is 400 — for the server token and a playground token alike;
// nothing a browser could resolve to another origin or above the base.
func TestPanelHandoffOpenRedirect(t *testing.T) {
	now := time.Now().UTC()
	_, ts := handoffServer(t, now)
	pg, err := signPanelToken([]byte("srv-token"), panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"", "//evil.example", "//evil.example/x", "http://evil.example", "https://evil.example/runs",
		"javascript:alert(1)", "/../x", "/runs/../../x", "/./x", "/%2e%2e/x", "/%2E%2e/x", "/runs/%2e",
		"/\\evil.example", "/\\/evil.example", "\\\\evil.example", "/runs/x#frag", "runs/x", "/runs/ x",
		"/runs/\tx", "/runs/\nx", "/%zz", "/" + strings.Repeat("a", maxHandoffTo),
	}
	for _, tok := range []string{"srv-token", pg} {
		for _, to := range bad {
			if r := handoffGet(t, ts, "to="+url.QueryEscape(to), tok, ""); r.StatusCode != http.StatusBadRequest {
				t.Errorf("to=%q = %d (Location %q), want 400", to, r.StatusCode, r.Header.Get("Location"))
			}
		}
		if r := handoffGet(t, ts, "", tok, ""); r.StatusCode != http.StatusBadRequest {
			t.Errorf("no to = %d, want 400", r.StatusCode)
		}
	}
	for _, to := range []string{"/", "/runs/run_a", "/runs/run_b/0/call_1", "/runs/r%2F1?step=2&x=..", "/sessions/s_1", "/runs/a..b"} {
		r := handoffGet(t, ts, "to="+url.QueryEscape(to), "srv-token", "")
		if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/studio"+to {
			t.Errorf("to=%q = %d %q, want 303 /studio%s", to, r.StatusCode, r.Header.Get("Location"), to)
		}
	}
}

// Without a Token there are no panel tokens: no hand-off route, and
// capabilities_off says why.
func TestPanelHandoffNeedsAToken(t *testing.T) {
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")))
	t.Cleanup(func() { _ = srv.Close() })
	if off := srv.capabilitiesOff()["handoff"]; !strings.Contains(off, "no Token") {
		t.Errorf("capabilities_off.handoff = %q", off)
	}
	code, _, _ := get(t, srv.Handler(), "/api/panel-handoff?to=/runs/x")
	if code != http.StatusNotFound {
		t.Errorf("no Token: hand-off = %d, want 404 (no route)", code)
	}
	withTok := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token("t"))
	t.Cleanup(func() { _ = withTok.Close() })
	if !strings.Contains(strings.Join(withTok.capabilityList(), ","), "handoff") {
		t.Errorf("with a Token: capabilities = %v, want handoff", withTok.capabilityList())
	}
}

// TestPageURLOnTheRunRow is the Studio half of plan G3's "open in app":
// a run its handler starts under scope.Header carries weft.page_url, and
// /api/runs/{id}'s meta serves it — to the server token and to a
// read-scoped panel token of the run's own public id (the page its own
// conversation lives on: nothing it does not already reach).
func TestPageURLOnTheRunRow(t *testing.T) {
	dir := t.TempDir()
	srv := New(Open(filepath.Join(dir, "weft.db")), Token("srv-token"))
	t.Cleanup(func() { _ = srv.Close() })
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", srv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL+"/studio", ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	agent := core.New(wefttest.Script(wefttest.Say("hello")), core.Name("support"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	var runID string
	app := scope.Header(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := agent.Generate(r.Context(), core.Prompt("hi"),
			core.Metadata(map[string]string{"weft.public_id": "pub_page"}))
		if err != nil {
			t.Error(err)
			return
		}
		runID = res.ID
	}), func(*http.Request) scope.Scope { return scope.Scope{PublicID: "pub_page"} })
	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	req.Header.Set("Referer", "https://app.example/support/chat?draft=secret#x")
	app.ServeHTTP(httptest.NewRecorder(), req)
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	readTok, err := signPanelToken([]byte("srv-token"), panelClaims{PublicID: "pub_page", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"srv-token", readTok} {
		var doc struct {
			Meta map[string]string `json:"meta"`
		}
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/studio/api/runs/"+runID, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if err := json.Unmarshal(b, &doc); err != nil {
					t.Fatal(err)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET /api/runs/%s = %d %s", runID, resp.StatusCode, b)
			}
		}
		if got := doc.Meta[scope.PageURLKey]; got != "https://app.example/support/chat" {
			t.Errorf("meta[%s] = %q, want the page without its query or fragment", scope.PageURLKey, got)
		}
		if tok == "srv-token" {
			b, _ := json.Marshal(doc.Meta)
			golden(t, "run-page-url-meta.golden.json", string(b))
		}
	}
}
