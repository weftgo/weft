package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestStudioAgentsFromRuntime pins plan B4 end to end: a bare `weft
// studio` (no weft.json anywhere, a temp database, a free port) and an
// app whose runtime.Install dials it — this test binary re-executed as
// `weft dev`'s helper app (dev_helper_test.go; WEFT_ENV=dev opens its
// runtime link) — serve the Agents page from the registration alone
// (api/manifest lists the agent, live), the playground and the
// debugger (their capabilities). With --no-playground all three are
// gone and meta's capabilities_off says why, naming the flag.
func TestStudioAgentsFromRuntime(t *testing.T) {
	skipWithoutSelfSignal(t)
	wd := t.TempDir()
	if findUpward(wd, "weft.json") != "" {
		t.Skip("a weft.json above the temp directory: the no-manifest case cannot be pinned here")
	}
	t.Chdir(wd)
	t.Setenv("WEFT_MANIFEST", "")
	t.Setenv("WEFT_STUDIO_ADDR", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")

	type metaDoc struct {
		HasManifest     bool              `json:"has_manifest"`
		ManifestSources int               `json:"manifest_sources"`
		Capabilities    []string          `json:"capabilities"`
		CapabilitiesOff map[string]string `json:"capabilities_off"`
	}
	for _, c := range []struct {
		name       string
		extra      []string
		playground bool
	}{
		{"default", nil, true},
		{"no-playground", []string{"--no-playground"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr := loop(freeBase(t, 1))
			db := "sqlite://" + filepath.Join(t.TempDir(), "weft.db")
			var out syncBuffer
			done := make(chan int, 1)
			args := append([]string{"studio", "--addr", addr, "--db", db, "--token", "tok"}, c.extra...)
			go func() { done <- run(args, &out, io.Discard) }()
			waitMeta(t, addr, done, &out)

			app := exec.Command(os.Args[0])
			app.Env = append(os.Environ(),
				"WEFT_DEV_HELPER=app", "WEFT_DEV_HELPER_RUNTIME=1", "WEFT_ENV=dev",
				"WEFT_STUDIO_URL=http://"+addr, "WEFT_STUDIO_TOKEN=tok")
			var appOut syncBuffer
			app.Stdout, app.Stderr = &appOut, &appOut
			if err := app.Start(); err != nil {
				t.Fatal(err)
			}
			appDone := make(chan struct{})
			go func() { _ = app.Wait(); close(appDone) }()
			stopApp := func() {
				_ = app.Process.Signal(os.Interrupt)
				select {
				case <-appDone:
				case <-time.After(10 * time.Second):
					_ = app.Process.Kill()
					<-appDone
					t.Errorf("the helper app ignored the interrupt (output %q)", appOut.String())
				}
			}

			get := func(path string) (int, []byte) {
				t.Helper()
				req, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
				req.Header.Set("Authorization", "Bearer tok")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				b, _ := io.ReadAll(resp.Body)
				return resp.StatusCode, b
			}
			meta := func() metaDoc {
				t.Helper()
				code, b := get("/api/meta")
				var m metaDoc
				if code != http.StatusOK || json.Unmarshal(b, &m) != nil {
					t.Fatalf("api/meta: %d %s", code, b)
				}
				return m
			}

			if c.playground {
				// The registration alone makes the Agents page: the
				// helper's agent, its manifest live.
				var body []byte
				for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
					code, b := get("/api/manifest")
					if code == http.StatusOK && strings.Contains(string(b), `"live":true`) {
						body = b
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("api/manifest never listed the live registration: %d %s (app %q)", code, b, appOut.String())
					}
				}
				var m struct {
					Agents []struct {
						Name string `json:"name"`
					} `json:"agents"`
					Sources []struct {
						Source string `json:"source"`
						Live   bool   `json:"live"`
					} `json:"sources"`
				}
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatal(err)
				}
				if len(m.Agents) != 1 || m.Agents[0].Name != "dev-helper" || len(m.Sources) != 1 ||
					m.Sources[0].Source != "runtime" || !m.Sources[0].Live {
					t.Errorf("api/manifest = %s, want the dev-helper agent from one live runtime source", body)
				}
				md := meta()
				if !md.HasManifest || md.ManifestSources != 1 {
					t.Errorf("meta has_manifest %v, manifest_sources %d, want true and 1", md.HasManifest, md.ManifestSources)
				}
				for _, capability := range []string{"playground", "runtimes", "breakpoints", "steer"} {
					if !slices.Contains(md.Capabilities, capability) {
						t.Errorf("capabilities %v lack %s", md.Capabilities, capability)
					}
				}
				if len(md.CapabilitiesOff) != 0 {
					t.Errorf("capabilities_off = %v, want none", md.CapabilitiesOff)
				}
			} else {
				// The app's runtime dials a Studio with no runtime link:
				// give it the time a registration takes, then nothing
				// is there, and meta says why.
				time.Sleep(300 * time.Millisecond)
				md := meta()
				for _, capability := range []string{"playground", "runtimes", "breakpoints", "steer"} {
					if slices.Contains(md.Capabilities, capability) {
						t.Errorf("--no-playground: capabilities %v carry %s", md.Capabilities, capability)
					}
					if why := md.CapabilitiesOff[capability]; !strings.Contains(why, "--no-playground") {
						t.Errorf("--no-playground: capabilities_off[%s] = %q, want the flag named", capability, why)
					}
				}
				if md.HasManifest || md.ManifestSources != 0 {
					t.Errorf("--no-playground: has_manifest %v, manifest_sources %d", md.HasManifest, md.ManifestSources)
				}
				if code, b := get("/api/manifest"); code != http.StatusNotFound || !strings.Contains(string(b), "--no-playground") {
					t.Errorf("--no-playground: api/manifest %d %s, want 404 naming the flag", code, b)
				}
			}
			stopApp()
			if code := stopStudio(t, done); code != 0 {
				t.Errorf("weft studio exited %d", code)
			}
			notListening(t, addr)
		})
	}
}
