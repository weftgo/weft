package doctor_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/internal/doctor"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

const tok = "dev-token"

// newStudio serves a setup-B Studio (a dev token) over a temp file.
func newStudio(t *testing.T, opts ...studio.Option) *httptest.Server {
	t.Helper()
	srv := studio.New(append([]studio.Option{studio.Open(filepath.Join(t.TempDir(), "weft.db")), studio.Token(tok)}, opts...)...)
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func run(t *testing.T, url, token string, getenv func(string) string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := doctor.Run(context.Background(), &out, url, token, getenv)
	return out.String(), err
}

// TestNoRuntimeNamesTheEnv is B5's Done line: against a Studio with no
// runtime connected, the doctor's output names the variables to set —
// WEFT_ENV and WEFT_STUDIO_URL — and a missing runtime is a warning,
// not a failure.
func TestNoRuntimeNamesTheEnv(t *testing.T) {
	ts := newStudio(t)
	out, err := run(t, ts.URL, tok, env(nil))
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	for _, want := range []string{
		"warn runtimes  none connected",
		"the playground is off (studio.Playground(true))",
		"WEFT_ENV is unset in this shell: the app opens the runtime link only with WEFT_ENV=dev",
		"WEFT_STUDIO_URL is unset in this shell",
		"set WEFT_STUDIO_URL=" + ts.URL,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// With the playground on and the env set, only the app side is left
	// to check.
	pg := newStudio(t, studio.Playground(true))
	out, _ = run(t, pg.URL, tok, env(map[string]string{"WEFT_ENV": "dev", "WEFT_STUDIO_URL": pg.URL}))
	if strings.Contains(out, "playground is off") || strings.Contains(out, "WEFT_ENV is") ||
		strings.Contains(out, "WEFT_STUDIO_URL is unset") || !strings.Contains(out, "check the app's own environment") {
		t.Errorf("playground on, env set:\n%s", out)
	}
}

// TestEveryLineMapsToMeta: every check line's label is in Lines, in
// Lines' order, every label prints, and every field a line names exists
// in the /api/meta answer of a Studio where each field is present (the
// server token, a manifest, a content-off run with its fix).
func TestEveryLineMapsToMeta(t *testing.T) {
	agent := []core.Option{core.Name("orders")}
	manifest, err := core.Manifest(core.New(wefttest.Script(), agent...))
	if err != nil {
		t.Fatal(err)
	}
	ts := newStudio(t, studio.Manifest(manifest), studio.IngestToken("ing"))
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, "ing", otel.NoContent()), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	a := core.New(wefttest.Script(wefttest.Say("done")), append(agent, core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))...)
	if _, err := a.Generate(ctx, core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	var healthy map[string]any
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		healthy = nil
		if err := json.Unmarshal(b, &healthy); err != nil {
			t.Fatal(err)
		}
		if l, _ := healthy["content"].(map[string]any)["latest"].(map[string]any); l["mark"] == "stripped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("meta never read the stripped run: %s", b)
		}
	}
	// The error fields are omitempty: a second Studio whose reads fail
	// and whose manifest does not parse serves them.
	broken := metaJSON(t, newStudio(t, studio.DB(failingDB{memDB(t)}), studio.Manifest([]byte(`{"weft":`))))
	has := func(doc map[string]any, f string) bool {
		var v any = doc
		for _, k := range strings.Split(f, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				return false
			}
			if v, ok = m[k]; !ok {
				return false
			}
		}
		return true
	}
	for _, l := range doctor.Lines {
		meta := 0
		for _, f := range l.Fields {
			switch {
			case strings.HasPrefix(f, "env:"):
				continue
			case f == "status":
				meta++
				continue
			}
			meta++
			if !has(healthy, f) && !has(broken, f) {
				t.Errorf("line %q reads %s, which /api/meta does not serve: %v / %v", l.Label, f, healthy, broken)
			}
		}
		// Every line reads Studio; the shell's env only beside it.
		if meta == 0 {
			t.Errorf("line %q names no /api/meta field (status or a body field): %v", l.Label, l.Fields)
		}
	}

	out, err := run(t, ts.URL, tok, env(nil))
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	var labels []string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, " ") {
			continue // a continuation of the check above
		}
		f := strings.Fields(line)
		if len(f) < 2 || !slices.Contains([]string{"ok", "warn", "FAIL"}, f[0]) {
			t.Fatalf("line %q is neither a check nor a continuation", line)
		}
		labels = append(labels, f[1])
	}
	var want []string
	for _, l := range doctor.Lines {
		want = append(want, l.Label)
	}
	if !slices.Equal(labels, want) {
		t.Errorf("doctor printed checks %v, want Lines' %v:\n%s", labels, want, out)
	}
	for _, s := range []string{"content   studio stores content as received; latest run", ": stripped", "fix: drop otel.NoContent()",
		"db        sqlite /", "weft.json 1 agents, 1 checked against their latest runs: current"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

// TestUnreachable: a closed port is the clear first line and
// ErrUnhealthy (exit 1), within the timeout; a Studio that never
// answers ends with the caller's deadline, never a hang.
func TestUnreachable(t *testing.T) {
	out, err := run(t, "http://127.0.0.1:1", "", env(nil))
	if !errors.Is(err, doctor.ErrUnhealthy) || !strings.HasPrefix(out, "studio not reachable at http://127.0.0.1:1: ") {
		t.Errorf("closed port: %v\n%s", err, out)
	}

	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(hang.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var b strings.Builder
	start := time.Now()
	err = doctor.Run(ctx, &b, hang.URL, "", env(nil))
	if !errors.Is(err, doctor.ErrUnhealthy) || !strings.HasPrefix(b.String(), "studio not reachable at "+hang.URL+": ") || time.Since(start) > 2*time.Second {
		t.Errorf("hung studio: %v after %v\n%s", err, time.Since(start), b.String())
	}
}

// TestToken: no token against a Studio that wants one, and a wrong
// one, are failed token lines naming WEFT_STUDIO_TOKEN; setup A (no
// Token, loopback) needs none.
func TestToken(t *testing.T) {
	ts := newStudio(t)
	for _, c := range []struct{ token, want string }{
		{"", "FAIL token     studio requires one: set WEFT_STUDIO_TOKEN or --token"},
		{"wrong", "FAIL token     refused: it is not this studio's token (WEFT_STUDIO_TOKEN / --token)"},
	} {
		out, err := run(t, ts.URL, c.token, env(nil))
		if !errors.Is(err, doctor.ErrUnhealthy) || !strings.Contains(out, c.want) {
			t.Errorf("token %q: %v\n%s", c.token, err, out)
		}
	}
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "weft.db")))
	t.Cleanup(func() { _ = srv.Close() })
	open := httptest.NewServer(srv.Handler())
	t.Cleanup(open.Close)
	out, err := run(t, open.URL, "", env(nil))
	if err != nil || !strings.Contains(out, "ok   token     not required (no Token configured)") || !strings.Contains(out, "ok   db        sqlite /") {
		t.Errorf("setup A: %v\n%s", err, out)
	}
}

// metaJSON reads a test Studio's /api/meta as the server token.
func metaJSON(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// memDB is an empty in-memory obsdb.
func memDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// failingDB fails every run read.
type failingDB struct{ obsdb.DB }

func (failingDB) Runs(context.Context, obsdb.RunQuery) (obsdb.RunPage, error) {
	return obsdb.RunPage{}, errors.New("disk on fire")
}

// TestReadErrorsWarn: a Studio whose reads fail, or whose manifest does
// not parse, is a warn line carrying meta's error — not "no run stored
// yet" — and the doctor still passes (warnings do not fail it).
func TestReadErrorsWarn(t *testing.T) {
	ts := newStudio(t, studio.DB(failingDB{memDB(t)}), studio.Manifest([]byte(`{"weft":`)))
	out, err := run(t, ts.URL, tok, env(nil))
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	for _, want := range []string{
		"warn content   studio stores content as received; the latest run could not be read: content: read the latest run: disk on fire",
		"warn weft.json the check failed: manifest_check: the manifest does not parse",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no run stored yet") {
		t.Errorf("a failed read reads as an empty database:\n%s", out)
	}
}

// TestStaleManifest: the stale line names both sides — weft.json or the
// app may be the newer one.
func TestStaleManifest(t *testing.T) {
	older, err := core.Manifest(core.New(wefttest.Script(), core.Name("orders"), core.Instructions("v0")))
	if err != nil {
		t.Fatal(err)
	}
	ts := newStudio(t, studio.Manifest(older), studio.IngestToken("ing"))
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, "ing"), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	a := core.New(wefttest.Script(wefttest.Say("done")), core.Name("orders"), core.Instructions("v1"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := a.Generate(ctx, core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	want := "warn weft.json stale for orders: weft.json and the latest runs disagree: regenerate weft.json, or redeploy the app if weft.json is newer"
	var out string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		out, _ = run(t, ts.URL, tok, env(nil))
		if strings.Contains(out, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestRedirectNotFollowed: the doctor talks to --url only — a Studio
// URL that redirects elsewhere is a failed line naming the target, and
// the target is never asked.
func TestRedirectNotFollowed(t *testing.T) {
	asked := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { asked = true }))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/meta", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	out, err := run(t, redirect.URL, tok, env(nil))
	if !errors.Is(err, doctor.ErrUnhealthy) || asked ||
		!strings.Contains(out, "FAIL token     api/meta answered 302: a redirect to "+target.URL+"/api/meta, not followed") {
		t.Errorf("redirect: %v (target asked: %v)\n%s", err, asked, out)
	}
}

// TestTokenNotRequired: a token sent to a Studio with no Token is never
// read, so the doctor does not call it accepted.
func TestTokenNotRequired(t *testing.T) {
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "weft.db")))
	t.Cleanup(func() { _ = srv.Close() })
	open := httptest.NewServer(srv.Handler())
	t.Cleanup(open.Close)
	out, err := run(t, open.URL, "some-token", env(nil))
	if err != nil || strings.Contains(out, "accepted") || !strings.Contains(out, "ok   token     not required (no Token configured)") {
		t.Errorf("token to an open studio: %v\n%s", err, out)
	}
}
