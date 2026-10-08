package studio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestManifestAuthRegistered: a registered manifest carries system
// prompts exactly as weft.json does, so api/manifest keeps the file's
// identity rules for it — the server token reads it, a read-scoped
// panel token is refused with the hidden badge, a playground-scoped one
// reads it, anonymous is 401 — and registering stays the server
// token's alone (the link is serverOnly).
func TestManifestAuthRegistered(t *testing.T) {
	ts := b4Studio(t, Token("srv"))
	entry, _ := b4Agent(t, "support", "THE SYSTEM PROMPT")
	b4Register(t, ts, "srv", "rt_1", "shop", entry)

	mint := func(playground bool) string {
		t.Helper()
		req := `{"public_id":"pub_a"}`
		if playground {
			req = `{"public_id":"pub_a","playground":true}`
		}
		code, body := b4Do(t, http.MethodPost, ts.URL+"/api/panel-tokens", "srv", req)
		if code != http.StatusOK {
			t.Fatalf("mint: %d %s", code, body)
		}
		var m struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatal(err)
		}
		return m.Token
	}
	read, play := mint(false), mint(true)

	for _, c := range []struct {
		who, bearer string
		want        int
		prompt      bool
	}{
		{"anonymous", "", http.StatusUnauthorized, false},
		{"server token", "srv", http.StatusOK, true},
		{"read panel token", read, http.StatusForbidden, false},
		{"playground panel token", play, http.StatusOK, true},
	} {
		code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", c.bearer, "")
		if code != c.want || strings.Contains(body, "THE SYSTEM PROMPT") != c.prompt {
			t.Errorf("%s: %d %s, want %d (prompt %v)", c.who, code, body, c.want, c.prompt)
		}
		if c.who == "read panel token" && !strings.Contains(body, "hidden") {
			t.Errorf("read panel token: %s, want the hidden badge", body)
		}
	}
	// A panel token cannot register a manifest of its own.
	for _, tok := range []string{read, play} {
		body := `{"runtime_id":"rt_evil","service":"x","agents":[` + entry + `]}`
		if code, _ := b4Do(t, http.MethodPost, ts.URL+"/api/runtime/register", tok, body); code == http.StatusOK {
			t.Errorf("a panel token registered a runtime")
		}
	}
	if m := b4GetManifest(t, ts, "srv"); len(m.Sources) != 1 {
		t.Errorf("sources after the refused registrations: %+v", m.Sources)
	}
}
