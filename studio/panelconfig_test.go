package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/weftgo/weft/version"
)

// TestPanelConfig pins GET <base>/panel-config.json (plan B3): the
// endpoint as the request reached it, the version, and api/meta's
// capabilities; open on a loopback Host whatever the token; a 404 —
// never a 403 — for a non-loopback Host or a foreign Origin.
func TestPanelConfig(t *testing.T) {
	for _, c := range []struct {
		name  string
		opts  []Option
		mount string
	}{
		{"setup A (mounted under /studio/)", nil, "/studio"},
		{"setup B (token, at the root)", []Option{Token("srv"), Base("/")}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := New(append([]Option{Open(filepath.Join(t.TempDir(), "p.db")), Playground(true)}, c.opts...)...)
			t.Cleanup(func() { _ = srv.Close() })
			h := srv.Handler()
			if c.mount != "" {
				mux := http.NewServeMux()
				mux.Handle(c.mount+"/", http.StripPrefix(c.mount, h))
				h = mux
			}
			ts := httptest.NewServer(h)
			t.Cleanup(ts.Close)

			do := func(host, origin string) (int, panelConfig) {
				t.Helper()
				req, _ := http.NewRequest(http.MethodGet, ts.URL+c.mount+"/panel-config.json", nil)
				if host != "" {
					req.Host = host
				}
				if origin != "" {
					req.Header.Set("Origin", origin)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				var doc panelConfig
				if resp.StatusCode == http.StatusOK {
					if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
						t.Fatal(err)
					}
				}
				return resp.StatusCode, doc
			}
			code, doc := do("", "")
			if code != http.StatusOK {
				t.Fatalf("loopback: %d", code)
			}
			if want := ts.URL + c.mount + "/"; doc.Endpoint != want {
				t.Errorf("endpoint %q, want %q", doc.Endpoint, want)
			}
			if doc.Version != version.Runtime() {
				t.Errorf("version %q, want %q", doc.Version, version.Runtime())
			}
			req, _ := http.NewRequest(http.MethodGet, ts.URL+c.mount+"/api/meta", nil)
			req.Header.Set("Authorization", "Bearer srv")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var meta struct {
				Capabilities []string `json:"capabilities"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&meta)
			_ = resp.Body.Close()
			if !slices.Equal(doc.Capabilities, meta.Capabilities) || !slices.Contains(doc.Capabilities, "panel-config") {
				t.Errorf("capabilities %v, want meta's %v (panel-config among them)", doc.Capabilities, meta.Capabilities)
			}
			if code, _ := do("", ts.URL); code != http.StatusOK {
				t.Errorf("same-origin Origin: %d", code)
			}
			if code, _ := do("", "http://localhost:3000"); code != http.StatusOK {
				t.Errorf("a loopback Origin: %d", code)
			}
			for _, o := range []string{"http://[::1]:3000", "http://127.0.0.2:3000", "https://app.localhost"} {
				if code, _ := do("", o); code != http.StatusOK {
					t.Errorf("loopback Origin %s: %d", o, code)
				}
			}
			if code, _ := do("studio.example.com", ""); code != http.StatusNotFound {
				t.Errorf("non-loopback Host: %d, want 404", code)
			}
			if code, _ := do("", "https://evil.example"); code != http.StatusNotFound {
				t.Errorf("foreign Origin: %d, want 404", code)
			}
		})
	}
}

// TestPanelConfigLoopbackOriginWithAllowOrigins: a loopback Origin
// passes the route's guard even when AllowOrigins is set — the
// loopback rule is checked before the list — while an origin neither
// loopback nor listed is still a 404.
func TestPanelConfigLoopbackOriginWithAllowOrigins(t *testing.T) {
	srv := New(Open(filepath.Join(t.TempDir(), "p.db")), AllowOrigins("https://app.example"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	for origin, want := range map[string]int{
		"http://localhost:3000":   http.StatusOK,
		"http://[::1]:3000":       http.StatusOK,
		"http://127.0.0.2:5173":   http.StatusOK,
		"http://dev.localhost":    http.StatusOK,
		"https://app.example":     http.StatusOK,
		"https://evil.example":    http.StatusNotFound,
		"http://localhost.evil.x": http.StatusNotFound,
	} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/panel-config.json", nil)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Origin %s: %d, want %d", origin, resp.StatusCode, want)
		}
	}
}
