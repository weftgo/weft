package studio

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core/wefttest"
)

// proposalFile is a weft.json with two agents, spaced the way a
// generated file is, so the verbatim splice shows.
const proposalFile = `{
  "weft": 1,
  "agents": [
    {"name": "support", "instructions": "You help."},
    {"name": "triage", "instructions": "You sort."}
  ]
}
`

var sinceRe = regexp.MustCompile(`"since":"[^"]*"`)

// TestProposalFileManifest pins plan F6's proposed prompt over a file
// manifest (setup A, loopback, no Token): PUT stores it and answers it
// with its sha256 instructions_hash (ADR 0028 §4's hash); GET
// /api/manifest carries it as the agent's "proposed" beside its
// registered "instructions", every other byte of the file verbatim;
// DELETE forgets it and the manifest is the file again; a second
// DELETE, an unknown agent and a bad body are refused.
func TestProposalFileManifest(t *testing.T) {
	ts := b4Studio(t, Manifest([]byte(proposalFile)))
	before := func() string {
		code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", "", "")
		if code != http.StatusOK {
			t.Fatalf("manifest: %d %s", code, body)
		}
		return body
	}
	plain := before()

	code, body := b4Do(t, http.MethodPut, ts.URL+"/api/agents/support/proposal", "", `{"instructions":"You help, tersely."}`)
	if code != http.StatusOK || !strings.Contains(body, `"instructions_hash":"`+sha("You help, tersely.")+`"`) ||
		!strings.Contains(body, `"agent":"support"`) {
		t.Fatalf("PUT = %d %s, want the proposal with its sha256 hash", code, body)
	}
	with := before()
	if !strings.Contains(with, `{"name": "support", "instructions": "You help.","proposed":{"instructions":"You help, tersely.","instructions_hash":"`+sha("You help, tersely.")+`","since":"`) ||
		!strings.Contains(with, `{"name": "triage", "instructions": "You sort."}`) {
		t.Errorf("manifest with a proposal:\n%s", with)
	}
	wefttest.Golden(t, "testdata/api/manifest-proposed.golden.json", []byte(sinceRe.ReplaceAllString(with, `"since":"(time)"`)))

	if code, body := b4Do(t, http.MethodDelete, ts.URL+"/api/agents/support/proposal", "", ""); code != http.StatusOK || !strings.Contains(body, `"proposed":null`) {
		t.Errorf("DELETE = %d %s", code, body)
	}
	if got := before(); got != plain {
		t.Errorf("after DELETE the manifest is not the file again:\n%s\nwant\n%s", got, plain)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodDelete, "/api/agents/support/proposal", "", http.StatusNotFound},
		{http.MethodPut, "/api/agents/nobody/proposal", `{"instructions":"x"}`, http.StatusNotFound},
		{http.MethodPut, "/api/agents/support/proposal", `{}`, http.StatusBadRequest},
		{http.MethodPut, "/api/agents/support/proposal", `{"instructions":`, http.StatusBadRequest},
		{http.MethodGet, "/api/agents/support/proposal", "", http.StatusNotFound}, // no such route: the API's 404
	} {
		if code, body := b4Do(t, c.method, ts.URL+c.path, "", c.body); code != c.want {
			t.Errorf("%s %s %s = %d %s, want %d", c.method, c.path, c.body, code, body, c.want)
		}
	}
	// An empty string is a proposal: no system prompt.
	if code, body := b4Do(t, http.MethodPut, ts.URL+"/api/agents/triage/proposal", "", `{"instructions":""}`); code != http.StatusOK ||
		!strings.Contains(body, `"instructions_hash":"`+sha("")+`"`) {
		t.Errorf("empty proposal = %d %s", code, body)
	}
}

// TestProposalRegisteredManifest: with no file, a registered agent's
// entry carries its proposal; the capability is reported; a server
// with a Token answers the server token and refuses a loopback request
// without it, and a playground panel token reads the proposal on the
// manifest (it reads prompts) but may not set one.
func TestProposalRegisteredManifest(t *testing.T) {
	const tok = "srv"
	ts := b4Studio(t, Token(tok))
	entry, _ := b4Agent(t, "support", "You help.")
	b4Register(t, ts, tok, "rt_1", "shop", entry)

	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/meta", tok, ""); code != http.StatusOK || !strings.Contains(body, `"proposals"`) {
		t.Errorf("meta = %d, want the proposals capability:\n%s", code, body)
	}
	if code, _ := b4Do(t, http.MethodPut, ts.URL+"/api/agents/support/proposal", "", `{"instructions":"x"}`); code != http.StatusUnauthorized {
		t.Errorf("PUT without the token = %d, want 401", code)
	}
	if code, body := b4Do(t, http.MethodPut, ts.URL+"/api/agents/support/proposal", tok, `{"instructions":"You help, tersely."}`); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", tok, "")
	if code != http.StatusOK || !strings.Contains(body, `"proposed":{"instructions":"You help, tersely.","instructions_hash":"`+sha("You help, tersely.")+`"`) ||
		!strings.Contains(body, `"instructions":"You help."`) {
		t.Errorf("registered manifest = %d %s, want the proposal beside the registered instructions", code, body)
	}

	pg, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", pg, ""); code != http.StatusOK || !strings.Contains(body, `"proposed":{"instructions":"You help, tersely."`) {
		t.Errorf("playground token's manifest = %d %s", code, body)
	}
	read, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// A read-scoped token never reads a system prompt, proposed or
	// registered: the whole manifest is refused it (403, hidden).
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", read, ""); code != http.StatusForbidden || !strings.Contains(body, `"badge":"hidden"`) ||
		strings.Contains(body, "tersely") {
		t.Errorf("read token's manifest = %d %s, want 403 hidden", code, body)
	}
	for _, bearer := range []string{pg, read} {
		if code, _ := b4Do(t, http.MethodPut, ts.URL+"/api/agents/support/proposal", bearer, `{"instructions":"y"}`); code != http.StatusForbidden {
			t.Errorf("panel token PUT = %d, want 403", code)
		}
		if code, _ := b4Do(t, http.MethodDelete, ts.URL+"/api/agents/support/proposal", bearer, ""); code != http.StatusForbidden {
			t.Errorf("panel token DELETE = %d, want 403", code)
		}
	}
}

// TestProposalsBound: the store holds maxProposals agents; one more
// forgets the oldest.
func TestProposalsBound(t *testing.T) {
	var p proposals
	t0 := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i := range maxProposals + 1 {
		p.put(fmt.Sprintf("agent_%03d", i), proposal{Since: t0.Add(time.Duration(i) * time.Second)})
	}
	snap := p.snapshot()
	if _, ok := snap["agent_000"]; len(snap) != maxProposals || ok {
		t.Errorf("held %d (oldest kept: %v), want %d without the oldest", len(snap), ok, maxProposals)
	}
	if _, ok := snap[fmt.Sprintf("agent_%03d", maxProposals)]; !ok {
		t.Error("the newest proposal was dropped")
	}
	// Replacing a held agent's proposal never evicts another.
	p.put("agent_001", proposal{Since: t0.Add(time.Hour)})
	if len(p.snapshot()) != maxProposals {
		t.Errorf("a replacement changed the count to %d", len(p.snapshot()))
	}
}
