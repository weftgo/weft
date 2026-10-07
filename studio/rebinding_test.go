package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// rebindingServer is a Playground(true) Studio over a temp database
// with one connected runtime (registered and holding its command
// stream, so a valid run answers 202), AllowOrigins naming one real
// hostname, and token as the server token ("" is setup A).
func rebindingServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	opts := []Option{
		Open(t.TempDir() + "/rebind.db"), Playground(true),
		AllowOrigins("http://myapp.internal:8080"),
	}
	if token != "" {
		opts = append(opts, Token(token))
	}
	ts := httptest.NewServer(New(opts...).Handler())
	t.Cleanup(ts.Close)

	bearer := func(req *http.Request) {
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	b, _ := json.Marshal(linkruntime.Registration{
		RuntimeID: "rt_rb",
		Agents: []linkruntime.AgentRegistration{{
			Name:     "a",
			Manifest: `{"weft":1,"agents":[{"name":"a","model":{"provider":"p","name":"m"},"policy":{},"tools":[]}]}`,
		}},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/runtime/register", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	bearer(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %v %v", err, resp)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/runtime/commands?runtime=rt_rb", nil)
	bearer(req)
	stream, err := http.DefaultClient.Do(req)
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("commands: %v %v", err, stream)
	}
	t.Cleanup(func() { _ = stream.Body.Close() })
	go func() { _, _ = io.Copy(io.Discard, stream.Body) }()
	return ts
}

// TestDNSRebindingGuard pins the Host rule for setup A (no Token): a
// DNS-rebound page (http://evil.example:7331 resolving to 127.0.0.1)
// is same-origin to the browser, so without the rule it could start
// playground runs, read transcripts and hold the live stream. The
// whole /api tree answers only a loopback Host or one AllowOrigins
// names; the UI shell and panel.js carry no data and stay open. With
// a Token the bearer is the defence and the Host does not matter.
func TestDNSRebindingGuard(t *testing.T) {
	const tok = "tok_rebind"
	const run = `{"runtime":"rt_rb","agent":"a","input":"hi","engine":"live","thread":"ephemeral"}`
	hosts := []string{"127.0.0.1:7331", "localhost:3000", "[::1]:7331", "studio.localhost", "evil.example:7331", "myapp.internal:8080"}
	routes := []struct {
		name, method, path, body string
		open                     int // the status when the request is let through
	}{
		{"runs", http.MethodGet, "/api/runs", "", http.StatusOK},
		{"playground run", http.MethodPost, "/api/playground/runs", run, http.StatusAccepted},
		{"live", http.MethodGet, "/api/live?session=s_1", "", http.StatusOK},
		{"panel.js", http.MethodGet, "/panel.js", "", http.StatusOK},
		{"shell", http.MethodGet, "/", "", http.StatusOK},
	}
	for _, token := range []string{"", tok} {
		ts := rebindingServer(t, token)
		for _, host := range hosts {
			for _, rt := range routes {
				want := rt.open
				refused := token == "" && host == "evil.example:7331" && strings.HasPrefix(rt.path, "/api/")
				if refused {
					want = http.StatusForbidden
				}
				name := "token=" + token + "/" + host + "/" + rt.name
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					req, err := http.NewRequestWithContext(ctx, rt.method, ts.URL+rt.path, strings.NewReader(rt.body))
					if err != nil {
						t.Fatal(err)
					}
					req.Host = host
					if rt.body != "" {
						req.Header.Set("Content-Type", "application/json")
					}
					if token != "" {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = resp.Body.Close() }()
					if resp.StatusCode != want {
						b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
						t.Fatalf("status = %d, want %d: %s", resp.StatusCode, want, b)
					}
					if refused {
						var e struct {
							Error struct{ Code, Message string } `json:"error"`
						}
						b, _ := io.ReadAll(resp.Body)
						if err := json.Unmarshal(b, &e); err != nil {
							t.Fatalf("refusal body is not the error shape: %s", b)
						}
						for _, frag := range []string{"evil.example:7331", "AllowOrigins", "Token"} {
							if !strings.Contains(e.Error.Message, frag) {
								t.Errorf("refusal message %q does not name %q", e.Error.Message, frag)
							}
						}
					}
				})
			}
		}
	}
}

// TestDNSRebindingForwardedHost: proxy headers never vouch for a Host
// — anyone can send them, a rebound page included.
func TestDNSRebindingForwardedHost(t *testing.T) {
	ts := rebindingServer(t, "")
	for _, hdr := range [][2]string{
		{"X-Forwarded-Host", "localhost"},
		{"Forwarded", "host=localhost"},
		{"X-Forwarded-Host", "myapp.internal:8080"},
	} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs", nil)
		req.Host = "evil.example:7331"
		req.Header.Set(hdr[0], hdr[1])
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %s = %d, want 403", hdr[0], hdr[1], resp.StatusCode)
		}
	}
}

// TestDNSRebindingInProcess: a request that never crossed a socket (no
// RemoteAddr — net/http's server always sets one) is weft/runtime's
// runtime.Local transport, whose Host is the placeholder
// weft.studio.local; it passes. The same Host over a socket does not.
func TestDNSRebindingInProcess(t *testing.T) {
	h := New(Open(t.TempDir() + "/inproc.db")).Handler()
	for _, tc := range []struct {
		remote string
		want   int
	}{{"", http.StatusOK}, {"127.0.0.1:5555", http.StatusForbidden}} {
		req := httptest.NewRequest(http.MethodGet, "http://weft.studio.local/api/runs", nil)
		req.RemoteAddr = tc.remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("RemoteAddr %q: %d, want %d (%s)", tc.remote, w.Code, tc.want, w.Body)
		}
	}
}
