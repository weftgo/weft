package doctor_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/internal/discovery"
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
	err := doctor.Run(context.Background(), &out, doctor.Target{URL: url, Token: token}, getenv)
	return out.String(), err
}

// TestNoRuntimeNamesTheEnv is B5's Done line: against a Studio with the
// playground on and no runtime connected, the doctor's output names the
// variables to set — WEFT_ENV and WEFT_STUDIO_URL — and a missing
// runtime is a warning, not a failure. With the playground off
// (UX-S5a-08) the line says so with the flag that did it, and the
// shell's variables, which cannot help, are not named.
func TestNoRuntimeNamesTheEnv(t *testing.T) {
	off := newStudio(t)
	out, err := run(t, off.URL, tok, env(nil))
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "ok   runtimes  none: studio has no runtime link (the playground is off: turn it on with studio.Playground(true); weft studio --no-playground turns it off)") ||
		strings.Contains(out, "WEFT_ENV is") {
		t.Errorf("playground off:\n%s", out)
	}
	ts := newStudio(t, studio.Playground(true))
	out, err = run(t, ts.URL, tok, env(nil))
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	for _, want := range []string{
		"warn runtimes  none connected",
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
	if strings.Contains(out, "runtime link (") || strings.Contains(out, "WEFT_ENV is") ||
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
	// A discovery file naming this process: meta serves discovery_file.
	discPath := writeDiscovery(t, discovery.Info{URL: "http://127.0.0.1:1"})
	ts := newStudio(t, studio.Manifest(manifest), studio.IngestToken("ing"), studio.DiscoveryFile(discPath))
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
	// One refused batch (no ingest token): ingest.last_error / last_at
	// are omitempty, served once a refusal happened.
	refused, err := http.Post(ts.URL+"/v1/traces", "application/x-protobuf", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	_ = refused.Body.Close()
	if refused.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an ingest POST with no token: %s, want 401", refused.Status)
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
			case strings.HasPrefix(f, "env:"), strings.HasPrefix(f, "local:"):
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
		"ok   data      newest run ", " by weft ", "ok   otlp      walled: OTLP at " + ts.URL + "/v1/traces",
		"warn discovery this Studio wrote " + discPath + ", which this shell does not find",
		"db        sqlite /", "manifest  weft.json: 1 agent, 1 checked against their latest runs: current",
		// The ingest counters (UX-E-37): the app's batches accepted, the
		// one refused above, and the refusal with its fix.
		"warn ingest    ", " OTLP batches accepted, 1 refused; no app reported lost records",
		"- last refused at ", ": 401 ", "fix: studio's log names each refused batch with its request id"} {
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
	err = doctor.Run(ctx, &b, doctor.Target{URL: hang.URL}, env(nil))
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
		{"", "FAIL token     studio requires one: it is in <db>.token beside the database (the start banner names the file); set WEFT_STUDIO_TOKEN or --token to it\n               - or run `weft open --with-token` from the directory the Studio started in"},
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
		"warn manifest  the check failed: manifest_check: the manifest does not parse",
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
	want := "warn manifest  weft.json: stale for orders: weft.json and the latest runs disagree (the latest run of orders ran manifest "
	want2 := "): regenerate weft.json, or redeploy the app if weft.json is newer"
	var out string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		out, _ = run(t, ts.URL, tok, env(nil))
		if strings.Contains(out, want) && strings.Contains(out, want2) {
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
	if err != nil || strings.Contains(out, "token     accepted") || !strings.Contains(out, "ok   token     not required (no Token configured)") {
		t.Errorf("token to an open studio: %v\n%s", err, out)
	}
}

// TestIngestRefusalWindow: a refused batch warns while the last refusal
// is within the hour; an older one reads ok, its line still printed.
func TestIngestRefusalWindow(t *testing.T) {
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{{2 * time.Minute, "warn ingest    "}, {2 * time.Hour, "ok   ingest    "}} {
		at := time.Now().Add(-c.ago).UTC().Format(time.RFC3339)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"auth_required":true,"weft_version":"v","studio_version":"s","panel_version":"s",`+
				`"db":{"kind":"sqlite"},"content":{"ingest":"as_received"},`+
				`"ingest":{"dropped":[],"accepted":7,"refused":1,"last_error":"401 unauthorized: no token","last_at":"`+at+`"}}`)
		}))
		out, _ := run(t, ts.URL, tok, env(nil))
		ts.Close()
		if !strings.Contains(out, c.want+"7 OTLP batches accepted, 1 refused") || !strings.Contains(out, "- last refused at "+at) {
			t.Errorf("last refusal %s ago: want %q\n%s", c.ago, c.want, out)
		}
	}
}

// TestControlCharactersStripped: what the doctor prints comes from
// Studio — a service name an OTLP sender chose among it — and a control
// character in it never reaches the terminal raw.
func TestControlCharactersStripped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"auth_required":true,"weft_version":"v\u001b[2J","studio_version":"s","panel_version":"s",`+
			`"db":{"kind":"sqlite"},"content":{"ingest":"as_received"},`+
			`"ingest":{"dropped":[{"service":"evil\u001b]0;owned\u0007\rsvc","records":3,"last_heard":"2026-10-13T00:00:00Z"}],`+
			`"accepted":7,"refused":2,"last_error":"401 unauthorized: bad\u001b[2Jtoken","last_at":"2026-10-13T00:00:01Z"}}`)
	}))
	t.Cleanup(ts.Close)
	out, _ := run(t, ts.URL, tok, env(nil))
	if strings.ContainsAny(out, "\x1b\x07\r") {
		t.Errorf("control characters reached the output: %q", out)
	}
	if !strings.Contains(out, "warn ingest    7 OTLP batches accepted, 2 refused; 1 service(s) reported records lost") ||
		!strings.Contains(out, "- last refused at 2026-10-13T00:00:01Z: 401 unauthorized: bad [2Jtoken") {
		t.Errorf("ingest counters = %q, want the counts and the last refusal, its control characters as spaces", out)
	}
	if !strings.Contains(out, "evil ]0;owned  svc: 3 records dropped") {
		t.Errorf("service line = %q, want its control characters as spaces", out)
	}
	// weft.records.dropped counts failed exports too: the fix names both.
	if !strings.Contains(out, "fix: queue full or exports failing — the app's WARN names which") {
		t.Errorf("ingest fix = %q, want it to name the queue and failing exports", out)
	}
}

// writeDiscovery writes a discovery file naming this process (fresh,
// 0600) into a directory of the test's own and returns its path.
func writeDiscovery(t *testing.T, info discovery.Info) string {
	t.Helper()
	dir := t.TempDir()
	old := discovery.Dirs
	discovery.Dirs = func() []string { return []string{dir} }
	t.Cleanup(func() { discovery.Dirs = old })
	info.PID = os.Getpid()
	info.Started = time.Now().UTC()
	w, err := discovery.Write(info)
	if err != nil {
		t.Fatal(err)
	}
	return w.Path
}

// TestDiscoveryLine (UX-S5a-01, UX-S5a-08): the discovery line says
// which Studio the file this shell reads names — this one, another
// (and two on one database), none, or none read under
// WEFT_DISCOVERY=off. The same Studio reached by another spelling of
// its host is this Studio (its pid). The source is the command's
// stderr line, never repeated on stdout.
func TestDiscoveryLine(t *testing.T) {
	db := filepath.Join(t.TempDir(), "weft.db")
	srv := studio.New(studio.Open(db), studio.Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	check := func(d doctor.Discovery, want ...string) {
		t.Helper()
		var out strings.Builder
		err := doctor.Run(context.Background(), &out, doctor.Target{URL: ts.URL, Token: tok, Discovery: d}, env(nil))
		if err != nil {
			t.Fatalf("doctor: %v\n%s", err, out.String())
		}
		for _, w := range want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("discovery %+v: output lacks %q:\n%s", d, w, out.String())
			}
		}
	}
	check(doctor.Discovery{Path: "/run/weft/studio.json", URL: ts.URL, DB: db, PID: 7},
		"ok   studio    reachable at "+ts.URL+" (weft ",
		"ok   discovery /run/weft/studio.json -> "+ts.URL+" (this Studio)")
	other := strings.Replace(ts.URL, "127.0.0.1", "localhost", 1)
	check(doctor.Discovery{Path: "/run/weft/studio.json", URL: other, DB: db, PID: os.Getpid()},
		"ok   discovery /run/weft/studio.json -> "+other+" (this Studio)")
	check(doctor.Discovery{Path: "/run/weft/studio.json", URL: "http://127.0.0.1:7347", DB: db, PID: 42},
		"warn discovery /run/weft/studio.json -> http://127.0.0.1:7347, ANOTHER Studio (pid 42, db "+db+")",
		"- two Studios serve "+db+": stop one")
	check(doctor.Discovery{Off: true}, "ok   discovery WEFT_DISCOVERY=off in this shell")
	check(doctor.Discovery{}, "warn discovery no discovery file")

	var out strings.Builder
	err := doctor.Run(context.Background(), &out, doctor.Target{URL: "http://127.0.0.1:1"}, env(nil))
	if !errors.Is(err, doctor.ErrUnhealthy) || !strings.HasPrefix(out.String(), "studio not reachable at http://127.0.0.1:1: ") {
		t.Errorf("unreachable default: %v\n%s", err, out.String())
	}
}

// TestTokenAdviceAndNoPlaygroundWords (B7-fix): a 401 with no token
// names the file and how to reach it without the resolver
// (--token "$(cat <db>.token)"), not a command that shares it; the
// runtimes line says the CLI flag in the host's words when the host
// set them (studio.OffReason).
func TestTokenAdviceAndNoPlaygroundWords(t *testing.T) {
	out, _ := run(t, newStudio(t).URL, "", env(nil))
	if !strings.Contains(out, "from the directory the Studio started in (where its discovery file is found), or pass --token \"$(cat <db>.token)\"") {
		t.Errorf("401 advice:\n%s", out)
	}
	ts := newStudio(t, studio.OffReason("playground", "weft studio was started with --no-playground: drop the flag to turn the playground on"))
	out, _ = run(t, ts.URL, tok, env(nil))
	if !strings.Contains(out, "ok   runtimes  none: studio has no runtime link (weft studio was started with --no-playground: drop the flag to turn the playground on)") {
		t.Errorf("runtimes:\n%s", out)
	}
}

// TestDataOtlpAndPanelLines (UX-S5a-08): the newest run from another
// weft warns with both versions; a walled, open and switched-off OTLP
// door each read as such; a stale panel bundle names the reinstall, not
// a repo make target alone.
func TestDataOtlpAndPanelLines(t *testing.T) {
	serve := func(body string) string {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		t.Cleanup(ts.Close)
		out, _ := run(t, ts.URL, tok, env(nil))
		return out
	}
	at := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	out := serve(`{"auth_required":true,"weft_version":"v0.19.0","studio_version":"v0.19.0","panel_version":"v0.18.0",` +
		`"db":{"kind":"sqlite"},"capabilities":["ingest"],"ingest_open":true,` +
		`"content":{"ingest":"as_received","latest":{"run_id":"r1","mark":"full","note":"n","started":"` + at + `","weft_version":"v0.15.0","service":"shop"}}}`)
	for _, want := range []string{
		"warn data      newest run 2m0s ago by weft v0.15.0 (service shop), this Studio is weft v0.19.0: the versions differ",
		"ok   otlp      open: OTLP at ",
		"FAIL panel     /panel.js is v0.18.0, studio is v0.19.0: this weft build embeds a stale panel bundle",
		"fix: install weft v0.19.0 again (go install github.com/weftgo/weft/cmd/weft@v0.19.0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	out = serve(`{"auth_required":true,"weft_version":"v","studio_version":"s","panel_version":"s","db":{"kind":"sqlite"},` +
		`"capabilities":[],"capabilities_off":{"ingest":"OTLP ingest is off: studio.NoIngest() turned it off"},"content":{"ingest":"as_received"}}`)
	if !strings.Contains(out, "warn otlp      OTLP ingest is off: studio.NoIngest() turned it off: an app's spans cannot reach this Studio") ||
		!strings.Contains(out, "ok   data      no run stored yet") {
		t.Errorf("ingest off:\n%s", out)
	}
}
