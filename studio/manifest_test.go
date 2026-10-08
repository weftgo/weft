package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// The registered manifests (plan B4): a runtime's registration carries
// each agent's core.Manifest; Studio remembers it by (service, manifest
// hash), serves it at api/manifest when no weft.json is configured, and
// says which hash a connected runtime holds now.

// b4Agent is one agent's registration entry: its real core.Manifest.
func b4Agent(t *testing.T, name, instructions string) (entry, manifest string) {
	t.Helper()
	b, err := core.Manifest(core.New(wefttest.Script(), core.Name(name), core.Instructions(instructions)))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := json.Marshal(map[string]any{"name": name, "manifest": string(b), "limits": map[string]int{"max_steps": 10, "parallelism": 4}})
	return string(e), string(b)
}

// b4Studio is a playground Studio over a temp database, with an
// optional token and file manifest.
func b4Studio(t *testing.T, opts ...Option) *httptest.Server {
	t.Helper()
	srv := New(append([]Option{Open(filepath.Join(t.TempDir(), "weft.db")), Playground(true)}, opts...)...)
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// b4Do sends one request with an optional bearer.
func b4Do(t *testing.T, method, url, bearer, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// b4Register registers runtime id for service with the agent entries.
func b4Register(t *testing.T, ts *httptest.Server, bearer, id, service string, agents ...string) {
	t.Helper()
	body := `{"runtime_id":"` + id + `","service":"` + service + `","agents":[` + strings.Join(agents, ",") + `]}`
	if code, b := b4Do(t, http.MethodPost, ts.URL+"/api/runtime/register", bearer, body); code != http.StatusOK {
		t.Fatalf("register %s: %d %s", id, code, b)
	}
}

// b4Connect opens the runtime's command stream; the returned func
// closes it (the runtime disconnects).
func b4Connect(t *testing.T, ts *httptest.Server, bearer, id string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/runtime/commands?runtime="+id, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, resp.Body) }()
	stop := func() { cancel(); _ = resp.Body.Close() }
	t.Cleanup(stop)
	return stop
}

type b4Manifest struct {
	Weft   int `json:"weft"`
	Agents []struct {
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	} `json:"agents"`
	Sources []struct {
		Source       string     `json:"source"`
		ManifestHash string     `json:"manifest_hash"`
		Service      string     `json:"service"`
		Live         *bool      `json:"live"`
		RegisteredAt *time.Time `json:"registered_at"`
		RuntimeID    string     `json:"runtime_id"`
		Agents       []struct {
			Name         string `json:"name"`
			ManifestHash string `json:"manifest_hash"`
		} `json:"agents"`
	} `json:"sources"`
}

func b4GetManifest(t *testing.T, ts *httptest.Server, bearer string) b4Manifest {
	t.Helper()
	code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", bearer, "")
	if code != http.StatusOK {
		t.Fatalf("api/manifest: %d %s", code, body)
	}
	var m b4Manifest
	decode(t, body, &m)
	return m
}

// b4WaitLive polls api/manifest until the sources' live flags read
// want (one per source, in order).
func b4WaitLive(t *testing.T, ts *httptest.Server, bearer string, want ...bool) b4Manifest {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		m := b4GetManifest(t, ts, bearer)
		got := make([]bool, 0, len(m.Sources))
		for _, s := range m.Sources {
			got = append(got, s.Live != nil && *s.Live)
		}
		if equalBools(got, want) {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("sources live = %v, want %v", got, want)
		}
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestManifestFromRegistration: with no weft.json, a registration's
// manifest is api/manifest — live while the runtime holds its stream,
// remembered (live false) after it disconnects; each agent's hash is
// the one its runs carry (sha256 of its registered manifest), and the
// source's hash is that of the agents as one core.Manifest document.
func TestManifestFromRegistration(t *testing.T) {
	ts := b4Studio(t)
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/manifest", "", ""); code != http.StatusNotFound ||
		!strings.Contains(body, "no runtime has registered") {
		t.Errorf("before any registration: %d %s, want 404 saying why", code, body)
	}
	entry, one := b4Agent(t, "support", "You help.")
	b4Register(t, ts, "", "rt_1", "shop", entry)
	stop := b4Connect(t, ts, "", "rt_1")
	m := b4WaitLive(t, ts, "", true)
	if len(m.Agents) != 1 || m.Agents[0].Name != "support" || m.Agents[0].Instructions != "You help." || m.Weft != 1 {
		t.Errorf("agents = %+v, want the registered support agent", m.Agents)
	}
	src := m.Sources[0]
	whole, err := core.Manifest(core.New(wefttest.Script(), core.Name("support"), core.Instructions("You help.")))
	if err != nil {
		t.Fatal(err)
	}
	if src.Source != "runtime" || src.Service != "shop" || src.RuntimeID != "rt_1" || src.RegisteredAt == nil ||
		src.ManifestHash != sha(string(whole)) || len(src.Agents) != 1 || src.Agents[0].ManifestHash != sha(one) {
		t.Errorf("source = %+v, want runtime shop rt_1 with core's hashes", src)
	}
	stop()
	m = b4WaitLive(t, ts, "", false)
	if len(m.Agents) != 1 || m.Sources[0].ManifestHash != src.ManifestHash {
		t.Errorf("after disconnect: %+v, want the same manifest remembered", m)
	}
}

// TestManifestSources: two services are two entries; a service that
// re-registers with a new hash keeps both, the new one live; a file
// manifest wins the agents and leads sources.
func TestManifestSources(t *testing.T) {
	ts := b4Studio(t)
	a1, _ := b4Agent(t, "support", "v1")
	a2, _ := b4Agent(t, "support", "v2")
	b, _ := b4Agent(t, "billing", "bills")
	b4Register(t, ts, "", "rt_a", "shop", a1)
	stopA := b4Connect(t, ts, "", "rt_a")
	b4Register(t, ts, "", "rt_b", "ledger", b)
	b4Connect(t, ts, "", "rt_b")
	m := b4WaitLive(t, ts, "", true, true)
	if svc := []string{m.Sources[0].Service, m.Sources[1].Service}; svc[0] == svc[1] {
		t.Errorf("two services: %v", svc)
	}
	// The shop's app restarts with a new build: a new runtime, a new hash.
	stopA()
	b4WaitLive(t, ts, "", true, false)
	time.Sleep(5 * time.Millisecond) // registered_at orders the remembered ones
	b4Register(t, ts, "", "rt_a2", "shop", a2)
	b4Connect(t, ts, "", "rt_a2")
	m = b4WaitLive(t, ts, "", true, true, false)
	shop := map[bool]string{}
	for _, s := range m.Sources {
		if s.Service == "shop" {
			shop[*s.Live] = s.RuntimeID
		}
	}
	if shop[true] != "rt_a2" || shop[false] != "rt_a" {
		t.Errorf("shop sources = %v, want rt_a2 live and rt_a remembered", shop)
	}
	instr := map[string]string{}
	for _, a := range m.Agents {
		instr[a.Name] = a.Instructions
	}
	if len(m.Agents) != 2 || instr["support"] != "v2" || instr["billing"] != "bills" {
		t.Errorf("agents = %+v, want support from the live build (v2) and billing, once each", m.Agents)
	}

	// A file manifest wins: its agents, then every registered source.
	file := New(Open(filepath.Join(t.TempDir(), "weft.db")), Playground(true), Manifest([]byte(fixtureManifest)))
	t.Cleanup(func() { _ = file.Close() })
	fts := httptest.NewServer(file.Handler())
	t.Cleanup(fts.Close)
	b4Register(t, fts, "", "rt_f", "shop", a1)
	b4Connect(t, fts, "", "rt_f")
	m = b4WaitLive(t, fts, "", false, true)
	if len(m.Agents) != 1 || m.Agents[0].Name != "orders" {
		t.Errorf("file configured: agents %+v, want the file's", m.Agents)
	}
	if m.Sources[0].Source != "file" || m.Sources[0].Live != nil || m.Sources[0].ManifestHash != sha(fixtureManifest) ||
		m.Sources[1].Source != "runtime" || m.Sources[1].Service != "shop" {
		t.Errorf("file configured: sources %+v, want the file then the registration", m.Sources)
	}
}

// TestMetaRegisteredManifest: has_manifest and manifest_sources count
// a registration alone; manifest_check then checks the registered
// agents (source runtime); capabilities_off is absent when nothing is
// off and names the option and flag when the playground is.
func TestMetaRegisteredManifest(t *testing.T) {
	ts := b4Studio(t, Token("srv"))
	meta := func(ts *httptest.Server, bearer string) metaDoc {
		t.Helper()
		code, body := b4Do(t, http.MethodGet, ts.URL+"/api/meta", bearer, "")
		if code != http.StatusOK {
			t.Fatalf("meta: %d %s", code, body)
		}
		if strings.Contains(body, `"capabilities_off"`) != (bearer == "") {
			t.Errorf("capabilities_off presence in %s", body)
		}
		var d metaDoc
		decode(t, body, &d)
		return d
	}
	if d := meta(ts, "srv"); d.HasManifest || d.ManifestSources != 0 || d.ManifestCheck != nil || d.CapabilitiesOff != nil {
		t.Errorf("before registering: %+v", d)
	}
	entry, _ := b4Agent(t, "support", "You help.")
	b4Register(t, ts, "srv", "rt_1", "shop", entry)
	d := meta(ts, "srv")
	if !d.HasManifest || d.ManifestSources != 1 || d.ManifestCheck == nil ||
		d.ManifestCheck.Source != "runtime" || d.ManifestCheck.Agents != 1 {
		t.Errorf("after registering: has_manifest %v, sources %d, check %+v", d.HasManifest, d.ManifestSources, d.ManifestCheck)
	}

	// The playground off: the four capabilities it carries are off, the
	// reason naming the option and the CLI flag; api/manifest's 404
	// says the same.
	off := New(Open(filepath.Join(t.TempDir(), "weft.db")))
	t.Cleanup(func() { _ = off.Close() })
	ots := httptest.NewServer(off.Handler())
	t.Cleanup(ots.Close)
	d = meta(ots, "")
	for _, c := range []string{"playground", "runtimes", "breakpoints", "steer"} {
		if r := d.CapabilitiesOff[c]; !strings.Contains(r, "studio.Playground(false)") || !strings.Contains(r, "--no-playground") {
			t.Errorf("capabilities_off[%s] = %q", c, r)
		}
	}
	if d.HasManifest || d.ManifestSources != 0 {
		t.Errorf("playground off: has_manifest %v, sources %d", d.HasManifest, d.ManifestSources)
	}
	if code, body := b4Do(t, http.MethodGet, ots.URL+"/api/manifest", "", ""); code != http.StatusNotFound || !strings.Contains(body, "--no-playground") {
		t.Errorf("playground off: api/manifest %d %s, want 404 naming --no-playground", code, body)
	}
}
