package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// registrationEntry is a runtime registration's agent entry carrying
// agt's real core.Manifest.
func registrationEntry(t *testing.T, agt *core.Agent, name string) (entry, manifest string) {
	t.Helper()
	b, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := json.Marshal(map[string]any{"name": name, "manifest": string(b), "limits": map[string]int{"max_steps": 10, "parallelism": 4}})
	return string(e), string(b)
}

// sameJSON: the route re-encodes the stored document (compact); the
// store keeps its bytes (the obsdbtest Manifests case pins those).
func sameJSON(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// TestRunResolvesItsRegisteredManifest (U27; UX-E-16): a real run of an
// agent a runtime registered carries the agent's weft.manifest.hash;
// Studio stored the registration's manifest under that hash, so GET
// /api/runs/{id} says manifest_available and GET /api/manifests/{hash}
// answers the configuration the run ran with — verbatim — after the
// runtime registered a changed one (current_manifest_hash then names
// the new configuration, the run's still resolves).
func TestRunResolvesItsRegisteredManifest(t *testing.T) {
	ts := b4Studio(t)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	opts := func(instructions string) []core.Option {
		return []core.Option{core.Name("support"), core.Instructions(instructions),
			core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	}
	agt := core.New(wefttest.Script(wefttest.Say("Hello.")), opts("Be brief.")...)
	entry, manifest := registrationEntry(t, agt, "support")
	hash := obsdb.ManifestHash([]byte(manifest))
	b4Register(t, ts, "", "rt_mf", "svc_mf", entry)

	res, err := agt.Generate(ctx, core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// The registration's write goes through the runtime link's ordered
	// writer, off the request path: poll for it, bounded.
	get := func(path string, want int) string {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			code, body := b4Do(t, http.MethodGet, ts.URL+path, "", "")
			if code == want {
				return body
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s = %d %s, want %d", path, code, body, want)
			}
		}
	}
	var run struct {
		ManifestHash      string `json:"manifest_hash"`
		ManifestAvailable bool   `json:"manifest_available"`
	}
	get("/api/manifests/"+hash, http.StatusOK)
	decode(t, get("/api/runs/"+res.ID, http.StatusOK), &run)
	if run.ManifestHash != hash || !run.ManifestAvailable {
		t.Fatalf("run document = manifest_hash %q available %v; want the registered %s, available", run.ManifestHash, run.ManifestAvailable, hash)
	}
	var got storedManifestView
	body := get("/api/manifests/"+hash, http.StatusOK)
	decode(t, body, &got)
	if got.ManifestHash != hash || got.Agent != "support" || got.Source != obsdb.ManifestSourceRuntime ||
		got.CurrentManifestHash != hash || got.FirstSeen.IsZero() || !sameJSON(t, got.Manifest, manifest) {
		t.Errorf("GET /api/manifests/%s = %s; want the registered document, agent support, source runtime, current", hash, body)
	}
	golden(t, "manifest-stored.golden.json",
		regexp.MustCompile(`"first_seen":"[^"]+"`).ReplaceAllString(body, `"first_seen":"(time)"`))

	// The runtime moves on: its new configuration is the current one,
	// the run's still resolves — "config at run time" against current.
	changed := core.New(wefttest.Script(), core.Name("support"), core.Instructions("Be thorough."))
	entry2, manifest2 := registrationEntry(t, changed, "support")
	b4Register(t, ts, "", "rt_mf", "svc_mf", entry2)
	get("/api/manifests/"+obsdb.ManifestHash([]byte(manifest2)), http.StatusOK)
	decode(t, get("/api/manifests/"+hash, http.StatusOK), &got)
	if got.CurrentManifestHash != obsdb.ManifestHash([]byte(manifest2)) || !strings.Contains(string(got.Manifest), "Be brief.") {
		t.Errorf("after a changed registration: current %q, manifest %s; want the new hash current and the run's own document", got.CurrentManifestHash, got.Manifest)
	}

	// An unknown hash is 404; a run whose hash resolves nowhere carries
	// no manifest_available.
	if code, b := b4Do(t, http.MethodGet, ts.URL+"/api/manifests/"+strings.Repeat("0", 64), "", ""); code != http.StatusNotFound {
		t.Errorf("unknown hash = %d %s, want 404", code, b)
	}
}

// TestFileManifestIsStored (U27): the weft.json Manifest(...) serves is
// stored at New, each agent under the hash its runs carry — a Studio
// without the playground still resolves them. Kept forever: deleting
// the run, and a retention sweep, leave it.
func TestFileManifestIsStored(t *testing.T) {
	agt := core.New(wefttest.Script(), core.Name("support"), core.Instructions("Be brief."))
	file, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	db := fixtureDB(t)
	srv := New(DB(db), Manifest(file))
	t.Cleanup(func() { _ = srv.Close() })
	hash := obsdb.ManifestHash(file) // one agent: the file is its document
	seedRun(t, db, "r_mf", "", "", "", map[string]any{"weft.manifest.hash": hash})
	h := srv.Handler()
	code, _, body := get(t, h, "/studio/api/manifests/"+hash)
	var got storedManifestView
	decode(t, body, &got)
	if code != http.StatusOK || got.Source != obsdb.ManifestSourceFile || !sameJSON(t, got.Manifest, string(file)) || got.CurrentManifestHash != hash {
		t.Fatalf("file manifest = %d %s; want the file's agent document, source file, current", code, body)
	}
	var run struct {
		ManifestAvailable bool `json:"manifest_available"`
	}
	_, _, rb := get(t, h, "/studio/api/runs/r_mf")
	decode(t, rb, &run)
	if !run.ManifestAvailable {
		t.Errorf("run r_mf = %s; want manifest_available", rb)
	}
	if err := db.DeleteRun(context.Background(), "r_mf"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Sweep(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if code, _, body := get(t, h, "/studio/api/manifests/"+hash); code != http.StatusOK {
		t.Errorf("after DeleteRun and Sweep: %d %s; manifests are kept forever", code, body)
	}
}
