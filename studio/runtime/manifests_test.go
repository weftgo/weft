package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestManifestsRemembered: a registration's manifest is remembered by
// (service, hash) — re-registering the same one refreshes it, a new
// service is a new entry, an agent whose manifest does not parse is
// left out — and the store forgets the oldest past maxManifests.
func TestManifestsRemembered(t *testing.T) {
	rs := New()
	ts := httptest.NewServer(mux(rs))
	t.Cleanup(ts.Close)
	post := func(reg Registration) {
		t.Helper()
		b, _ := json.Marshal(reg)
		resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("register: %d", resp.StatusCode)
		}
	}
	post(regBody("rt_1"))
	post(regBody("rt_1")) // a reconnect re-registers: the same entry
	other := regBody("rt_2")
	other.Service = "other-api"
	other.Agents = append(other.Agents, AgentRegistration{Name: "broken", Manifest: "{"})
	post(other)
	got := rs.Manifests()
	if len(got) != 2 {
		t.Fatalf("manifests = %+v, want two (one per service)", got)
	}
	for _, m := range got {
		if m.Live || len(m.Agents) != 1 || m.Agents[0].Name != "acme-support" || m.ManifestHash == "" {
			t.Errorf("manifest %+v: want one parsed agent, not live (no stream)", m)
		}
	}
	if got[0].ManifestHash != got[1].ManifestHash {
		t.Errorf("the same agents hash alike across services: %s vs %s", got[0].ManifestHash, got[1].ManifestHash)
	}
	for i := range maxManifests + 5 {
		reg := regBody(fmt.Sprintf("rt_x%d", i))
		reg.Service = fmt.Sprintf("svc-%d", i)
		post(reg)
	}
	if n := len(rs.Manifests()); n != maxManifests {
		t.Errorf("remembered %d, want the bound %d", n, maxManifests)
	}
}
