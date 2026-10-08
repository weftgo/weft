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

	var meta map[string]any
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		meta = nil
		if err := json.Unmarshal(b, &meta); err != nil {
			t.Fatal(err)
		}
		if l, _ := meta["content"].(map[string]any)["latest"].(map[string]any); l["mark"] == "stripped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("meta never read the stripped run: %s", b)
		}
	}
	for _, l := range doctor.Lines {
		for _, f := range l.Fields {
			if f == "status" || strings.HasPrefix(f, "env:") {
				continue
			}
			var v any = meta
			for _, k := range strings.Split(f, ".") {
				m, ok := v.(map[string]any)
				if !ok {
					t.Fatalf("line %q: field %s: %s is not an object in meta", l.Label, f, k)
				}
				if v, ok = m[k]; !ok {
					t.Errorf("line %q reads %s, which /api/meta does not serve: %v", l.Label, f, meta)
					break
				}
			}
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
	if err != nil || !strings.Contains(out, "ok   token     none needed") || !strings.Contains(out, "ok   db        sqlite /") {
		t.Errorf("setup A: %v\n%s", err, out)
	}
}
