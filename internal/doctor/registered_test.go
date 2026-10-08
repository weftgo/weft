package doctor_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/studio"
)

// TestRegisteredManifest (plan B4): with no weft.json, the weft.json
// line reads the manifest a runtime registered with — it says none is
// configured and that the agents come from the registration, never
// "configured".
func TestRegisteredManifest(t *testing.T) {
	ts := newStudio(t, studio.Playground(true))
	m, err := core.Manifest(core.New(wefttest.Script(), core.Name("orders")))
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := json.Marshal(map[string]any{"name": "orders", "manifest": string(m)})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/runtime/register",
		strings.NewReader(`{"runtime_id":"rt_1","service":"shop","agents":[`+string(agent)+`]}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	out, _ := run(t, ts.URL, tok, env(nil))
	if want := "ok   weft.json none configured; 1 agents from a runtime's registration, 0 checked against their latest runs"; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}

	// A panel token reads no manifest_check: the line says a manifest
	// is present without claiming a weft.json is configured.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/panel-tokens", strings.NewReader(`{"public_id":"pub_a"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var minted struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&minted)
	_ = resp.Body.Close()
	if err != nil || minted.Token == "" {
		t.Fatalf("mint: %d %v", resp.StatusCode, err)
	}
	out, _ = run(t, ts.URL, minted.Token, env(nil))
	if want := "ok   weft.json a manifest is present (weft.json or a runtime's registration); not checked for this caller"; !strings.Contains(out, want) {
		t.Errorf("panel token: output lacks %q:\n%s", want, out)
	}
}
