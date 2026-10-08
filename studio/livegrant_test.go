package studio

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// grantAt asks srvURL's POST /api/live-grant?<query> with bearer ("" =
// none) and returns the status, the sig and the raw body.
func grantAt(t *testing.T, srvURL, bearer, query string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+"/api/live-grant?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var g struct {
		Sig string `json:"sig"`
	}
	_ = json.Unmarshal(b, &g)
	return resp.StatusCode, g.Sig, string(b)
}

// mustGrant mints a grant for query on a handler mounted the way
// users mount it (/studio), failing the test unless it is granted.
func mustGrant(t *testing.T, h http.Handler, bearer, query string) string {
	t.Helper()
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	code, sig, body := grantAt(t, srv.URL+"/studio", bearer, query)
	if code != http.StatusOK || sig == "" {
		t.Fatalf("live grant %s: %d %s", query, code, body)
	}
	return sig
}

// decodeResp decodes and closes a response body.
func decodeResp(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// liveStatus opens GET <base>/api/live?<query> with no credentials but
// what the query carries, and returns the status.
func liveStatus(t *testing.T, base, query string) int {
	t.Helper()
	resp, err := http.Get(base + "/api/live?" + query)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestLiveGrantReplayAfterExpiry pins plan C5's "a leaked URL expires":
// the stream opens with a fresh grant, and the very same URL replayed
// after the grant's expiry is a 401.
func TestLiveGrantReplayAfterExpiry(t *testing.T) {
	old := liveGrantTTL
	liveGrantTTL = 300 * time.Millisecond
	t.Cleanup(func() { liveGrantTTL = old })
	srv := New(DB(fixtureDB(t)), Token("srv"))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, sig, body := grantAt(t, ts.URL, "srv", "run=r_ok")
	if code != http.StatusOK {
		t.Fatalf("grant: %d %s", code, body)
	}
	var g struct {
		Exp time.Time `json:"exp"`
	}
	decode(t, body, &g)
	if got := liveStatus(t, ts.URL, "run=r_ok&sig="+sig); got != http.StatusOK {
		t.Fatalf("fresh grant: %d", got)
	}
	// A panel token's grant, the same way: the token outlives the
	// grant, the URL does not.
	pt, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, psig, pbody := grantAt(t, ts.URL, pt, "public_id=pub_a&kinds=event")
	if code != http.StatusOK {
		t.Fatalf("panel grant: %d %s", code, pbody)
	}
	if got := liveStatus(t, ts.URL, "public_id=pub_a&kinds=event&sig="+psig); got != http.StatusOK {
		t.Fatalf("fresh panel grant: %d", got)
	}
	time.Sleep(time.Until(g.Exp) + 50*time.Millisecond)
	if got := liveStatus(t, ts.URL, "run=r_ok&sig="+sig); got != http.StatusUnauthorized {
		t.Errorf("replayed after expiry: %d, want 401", got)
	}
	var pg struct {
		Exp time.Time `json:"exp"`
	}
	decode(t, pbody, &pg)
	time.Sleep(time.Until(pg.Exp) + 50*time.Millisecond)
	if got := liveStatus(t, ts.URL, "public_id=pub_a&kinds=event&sig="+psig); got != http.StatusUnauthorized {
		t.Errorf("panel grant replayed after expiry: %d, want 401", got)
	}
}

// TestLiveGrantKindsEquivalence: kinds is a set on both sides — spaces,
// a trailing comma, a repeat and the order spell the same set, and an
// empty kinds is the omitted default (event,run), at grant time and at
// verify time alike.
func TestLiveGrantKindsEquivalence(t *testing.T) {
	ts := httptest.NewServer(New(DB(fixtureDB(t)), Token("srv")).Handler())
	t.Cleanup(ts.Close)
	same := []string{"kinds=event,run", "kinds=event,%20run", "kinds=event,run,", "kinds=event,event,run", "kinds=run,event", "kinds=", ""}
	for _, grantQ := range same {
		code, sig, body := grantAt(t, ts.URL, "srv", "run=r_ok&"+grantQ)
		if code != http.StatusOK {
			t.Fatalf("grant %q: %d %s", grantQ, code, body)
		}
		for _, openQ := range same {
			if got := liveStatus(t, ts.URL, "run=r_ok&"+openQ+"&sig="+sig); got != http.StatusOK {
				t.Errorf("granted %q, opened %q: %d, want 200", grantQ, openQ, got)
			}
		}
		if got := liveStatus(t, ts.URL, "run=r_ok&kinds=event,run,messages&sig="+sig); got != http.StatusUnauthorized {
			t.Errorf("granted %q, opened another set: %d, want 401", grantQ, got)
		}
	}
}

// TestLiveStreamEndsAtTokenExpiry pins the split (plan C5): a grant
// bounds opening a stream; a panel token's stream — opened with a
// grant or with the bearer — ends at the token's own expiry with one
// `event: expired` frame, then closes; a server token's stream
// outlives its grant.
func TestLiveStreamEndsAtTokenExpiry(t *testing.T) {
	old := liveGrantTTL
	liveGrantTTL = 200 * time.Millisecond
	t.Cleanup(func() { liveGrantTTL = old })
	h := Handler(DB(fixtureDB(t)), Token("srv"))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	open := func(query, bearer string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/live?"+query, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("open %s: %d", query, resp.StatusCode)
		}
		return resp
	}
	for _, via := range []string{"grant", "bearer"} {
		exp := time.Now().Add(700 * time.Millisecond)
		pt, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: exp})
		if err != nil {
			t.Fatal(err)
		}
		var resp *http.Response
		if via == "grant" {
			_, sig, _ := grantAt(t, ts.URL, pt, "public_id=pub_a")
			resp = open("public_id=pub_a&sig="+sig, "")
		} else {
			resp = open("public_id=pub_a", pt)
		}
		frames := readSSE(t, resp, 2, 5*time.Second) // returns at EOF
		if len(frames) != 1 || frames[0].event != "expired" {
			t.Errorf("%s: frames = %+v, want exactly one expired frame, then the end", via, frames)
		}
		if late := time.Since(exp); late < 0 || late > 2*time.Second {
			t.Errorf("%s: the stream ended %v after the token's expiry", via, late)
		}
	}

	// The server token's stream is not bounded by its grant.
	_, sig, _ := grantAt(t, ts.URL, "srv", "run=r_ok")
	resp := open("run=r_ok&sig="+sig, "")
	if frames := readSSE(t, resp, 1, 3*liveGrantTTL); len(frames) != 0 {
		t.Errorf("server-token stream past its grant's expiry: %+v, want still open and silent", frames)
	}
}

// TestLiveGrantBinding pins what a sig is bound to: one selector, one
// kinds set (order-free; an omitted kinds is the default set), one
// key — and never a panel token's lifetime past its own expiry.
func TestLiveGrantBinding(t *testing.T) {
	srv := New(DB(fixtureDB(t)), Token("srv"))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	_, sig, _ := grantAt(t, ts.URL, "srv", "session=s_1")
	for q, want := range map[string]int{
		"session=s_1&sig=" + sig:                       http.StatusOK,
		"session=s_1&kinds=run,event&sig=" + sig:       http.StatusOK, // the default set, spelled out
		"session=s_2&sig=" + sig:                       http.StatusUnauthorized,
		"run=s_1&sig=" + sig:                           http.StatusUnauthorized,
		"public_id=s_1&sig=" + sig:                     http.StatusUnauthorized,
		"agent=s_1&sig=" + sig:                         http.StatusUnauthorized,
		"session=s_1&kinds=event&sig=" + sig:           http.StatusUnauthorized,
		"session=s_1&kinds=event,run,delta&sig=" + sig: http.StatusUnauthorized,
		"session=s_1&kinds=bogus&sig=" + sig:           http.StatusUnauthorized, // no stream any grant names
		"session=s_1&run=r_1&sig=" + sig:               http.StatusUnauthorized,
	} {
		if got := liveStatus(t, ts.URL, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}

	// Another Studio's key (another Token) does not verify it.
	other := httptest.NewServer(New(DB(fixtureDB(t)), Token("other")).Handler())
	t.Cleanup(other.Close)
	if got := liveStatus(t, other.URL, "session=s_1&sig="+sig); got != http.StatusUnauthorized {
		t.Errorf("another Token's server: %d, want 401", got)
	}

	// A panel token's grant never outlives the token.
	soon := time.Now().Add(5 * time.Second).UTC()
	pt, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: soon})
	if err != nil {
		t.Fatal(err)
	}
	code, _, body := grantAt(t, ts.URL, pt, "public_id=pub_a")
	var g struct {
		Exp time.Time `json:"exp"`
	}
	decode(t, body, &g)
	if code != http.StatusOK || !g.Exp.Equal(soon) {
		t.Errorf("panel grant: %d exp %v, want the token's own expiry %v", code, g.Exp, soon)
	}
	// And a panel token is never granted outside its public id.
	if code, _, _ := grantAt(t, ts.URL, pt, "public_id=pub_b"); code != http.StatusForbidden {
		t.Errorf("panel grant outside its public id: %d, want 403", code)
	}

	// A grant key is never a panel-token key: a panel token's MAC does
	// not verify as a grant, nor the reverse.
	if _, err := parsePanelToken([]byte("srv"), sig); err == nil {
		t.Error("a live grant parsed as a panel token")
	}
}

// TestLiveGrantSetupA: without a Token the same mechanism runs on a
// per-process key — one code path: a grant opens its stream, and a
// presented sig must be valid even though the open API would let the
// bare request through. The Host guard still comes first.
func TestLiveGrantSetupA(t *testing.T) {
	ts := httptest.NewServer(New(DB(fixtureDB(t))).Handler())
	t.Cleanup(ts.Close)
	code, sig, body := grantAt(t, ts.URL, "", "run=r_ok&kinds=event")
	if code != http.StatusOK || !strings.HasPrefix(sig, liveGrantPrefix) {
		t.Fatalf("setup A grant: %d %s", code, body)
	}
	if got := liveStatus(t, ts.URL, "run=r_ok&kinds=event&sig="+sig); got != http.StatusOK {
		t.Errorf("setup A stream with its grant: %d", got)
	}
	if got := liveStatus(t, ts.URL, "run=r_ok&kinds=event,run&sig="+sig); got != http.StatusUnauthorized {
		t.Errorf("setup A, sig for another kinds set: %d, want 401", got)
	}
	// Another process's key: random per Server.
	other := httptest.NewServer(New(DB(fixtureDB(t))).Handler())
	t.Cleanup(other.Close)
	if got := liveStatus(t, other.URL, "run=r_ok&kinds=event&sig="+sig); got != http.StatusUnauthorized {
		t.Errorf("another setup-A server: %d, want 401", got)
	}
	// A token in the URL is refused here too (every /api route, every
	// setup), naming the grant, never echoing it; so is the no-sig
	// stream's hint, under a Token, which names the grant as well.
	for _, path := range []string{"/api/runs?token=s3cret-in-url", "/api/live?run=r_ok&token=s3cret-in-url"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(b), "s3cret") || !strings.Contains(string(b), "POST /api/live-grant") {
			t.Errorf("setup A %s: %d %s, want 401 naming POST /api/live-grant", path, resp.StatusCode, b)
		}
	}
	closed := httptest.NewServer(New(DB(fixtureDB(t)), Token("srv")).Handler())
	t.Cleanup(closed.Close)
	if resp, err := http.Get(closed.URL + "/api/live?run=r_ok"); err != nil {
		t.Fatal(err)
	} else {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(b), "POST /api/live-grant") {
			t.Errorf("no-sig stream under a Token: %d %s, want 401 naming POST /api/live-grant", resp.StatusCode, b)
		}
	}
	// A foreign Host is refused before the sig is read.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/live?run=r_ok&kinds=event&sig="+sig, nil)
	req.Host = "evil.example:7331"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host with a sig: %d, want 403", resp.StatusCode)
	}
}

// grantSigRe and grantExpRe normalize the grant's two values.
var (
	grantSigRe = regexp.MustCompile(`"sig": "weft_lg\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+"`)
	grantExpRe = regexp.MustCompile(`"exp": "[^"]*"`)
)

// TestLiveGrantGolden pins the grant's response shape.
func TestLiveGrantGolden(t *testing.T) {
	ts := httptest.NewServer(New(DB(fixtureDB(t)), Token("srv")).Handler())
	t.Cleanup(ts.Close)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/live-grant", strings.NewReader(`{"run":"r_ok","kinds":"event,messages"}`))
	req.Header.Set("Authorization", "Bearer srv")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("grant: %d %q %s", resp.StatusCode, resp.Header.Get("Cache-Control"), b)
	}
	out := pretty(t, string(b))
	if !grantSigRe.MatchString(out) {
		t.Fatalf("sig shape: %s", out)
	}
	out = grantSigRe.ReplaceAllString(out, `"sig": "(sig)"`)
	golden(t, "live-grant.golden.json", grantExpRe.ReplaceAllString(out, `"exp": "(time)"`))
}

// TestNoTokenOrSigInLogs: no log line — slog's default logger, the
// standard logger net/http's server errors go to — and no error body
// carries the token or a sig, whatever the request did with them.
func TestNoTokenOrSigInLogs(t *testing.T) {
	var logs bytes.Buffer
	prevSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	const tok = "srv-secret-token-value"
	old := liveGrantTTL
	liveGrantTTL = 200 * time.Millisecond
	t.Cleanup(func() { liveGrantTTL = old })
	ts := httptest.NewServer(New(DB(fixtureDB(t)), Token(tok)).Handler())
	ts.Config.ErrorLog = log.New(&logs, "", 0)
	t.Cleanup(ts.Close)
	pt, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	var bodies bytes.Buffer
	call := func(method, path, bearer string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			_ = resp.Body.Close()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		bodies.Write(b)
	}
	_, sig, _ := grantAt(t, ts.URL, tok, "run=r_ok")
	_, psig, _ := grantAt(t, ts.URL, pt, "public_id=pub_a")
	call(http.MethodGet, "/api/live?run=r_ok&sig="+sig, "")             // accepted
	call(http.MethodGet, "/api/live?run=r_other&sig="+sig, "")          // another stream
	call(http.MethodGet, "/api/live?public_id=pub_a&sig="+psig+"x", "") // tampered
	call(http.MethodGet, "/api/live?public_id=pub_b&sig="+psig, "")     // another selector
	call(http.MethodGet, "/api/runs?token="+tok, "")                    // a token in the URL
	call(http.MethodGet, "/api/runs?token="+pt, tok)                    // beside a good bearer
	call(http.MethodGet, "/api/live?run=r_ok&token="+tok, "")           // the old door
	call(http.MethodGet, "/api/runs", tok+"x")                          // a wrong bearer
	call(http.MethodGet, "/api/runs/r_nope", pt)                        // a panel token's 404
	call(http.MethodPost, "/api/live-grant?public_id=pub_b", pt)        // refused at grant time
	call(http.MethodPost, "/api/live-grant?agent=a", pt)                // refused at grant time
	time.Sleep(liveGrantTTL + 50*time.Millisecond)
	call(http.MethodGet, "/api/live?run=r_ok&sig="+sig, "") // expired
	ts.Close()

	for name, secret := range map[string]string{"server token": tok, "panel token": pt, "sig": sig, "panel sig": psig} {
		for where, text := range map[string]string{"logs": logs.String(), "error bodies": bodies.String()} {
			if strings.Contains(text, secret) {
				t.Errorf("the %s appears in the %s:\n%s", name, where, text)
			}
		}
	}
	// The refusals were made (the bodies are the errors above).
	if !strings.Contains(bodies.String(), "live-grant") || !strings.Contains(bodies.String(), `"unauthorized"`) {
		t.Errorf("error bodies lack the refusals: %s", bodies.String())
	}
}
