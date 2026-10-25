package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/obsdb/sqlite"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestMain points the discovery rung at an empty directory of this
// process's own: no test of this package joins a Studio the developer
// happens to be running.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "weft-otel-discovery-")
	if err != nil {
		panic(err)
	}
	discovery.Dirs = func() []string { return []string{dir} }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// discoveryFile writes a fresh discovery file naming url into a
// directory of the test's own and points the locations at it.
func discoveryFile(t *testing.T, url, token string, pid int, started time.Time) {
	t.Helper()
	dir := t.TempDir()
	old := discovery.Dirs
	discovery.Dirs = func() []string { return []string{dir} }
	t.Cleanup(func() { discovery.Dirs = old })
	b, _ := json.Marshal(discovery.Info{URL: url, Token: token, PID: pid, Started: started, Version: "v0"})
	if err := os.WriteFile(filepath.Join(dir, discovery.FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// captureLog makes slog's default a Debug-level text handler for the
// test and returns its buffer.
func captureLog(t *testing.T) *syncBuf {
	t.Helper()
	var b syncBuf
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &b
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func lines(s, level string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "level="+level) {
			out = append(out, l)
		}
	}
	return out
}

// TestDiscoveryJoinsTheRunningStudio: with no environment and a fresh
// discovery file, Start configures the Studio destination the file
// names — its url and token — and says so in exactly one INFO line.
func TestDiscoveryJoinsTheRunningStudio(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	discoveryFile(t, srv.URL, "disc-token", os.Getpid(), time.Now())
	logs := captureLog(t)

	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	if u, tok := p.StudioEndpoint(); u != srv.URL || tok != "disc-token" {
		t.Errorf("StudioEndpoint = %q, %q; want the discovery file's %q, disc-token", u, tok, srv.URL)
	}
	info := lines(logs.String(), "INFO")
	if len(info) != 1 || !strings.Contains(info[0], "joined the running Studio") || !strings.Contains(info[0], srv.URL) {
		t.Errorf("INFO lines %q, want exactly one naming %s", info, srv.URL)
	}
}

// TestDiscoveryInstallJoins: the Done line's app — Install with no
// options and no environment — exports to the running Studio, which
// otel.StudioEndpoint (what weft/runtime dials) reports.
func TestDiscoveryInstallJoins(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	discoveryFile(t, srv.URL, "disc-token", os.Getpid(), time.Now())
	logs := captureLog(t)
	stop := Install()
	u, tok := StudioEndpoint()
	stop()
	if u != srv.URL || tok != "disc-token" {
		t.Errorf("otel.StudioEndpoint = %q, %q; want %q", u, tok, srv.URL)
	}
	if n := len(lines(logs.String(), "INFO")); n != 1 {
		t.Errorf("%d INFO lines, want 1:\n%s", n, logs.String())
	}
}

// TestDiscoveryEscapes: WEFT_STUDIO_URL wins over the file (and no INFO
// line claims a join); WEFT_DISCOVERY=off and NoEnv ignore it; a stale
// file (a dead pid, or 24 hours old) is ignored with no line above
// Debug.
func TestDiscoveryEscapes(t *testing.T) {
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("WEFT_DISCOVERY", "")
	t.Setenv("WEFT_DB", filepath.Join(t.TempDir(), "local.db"))
	const fileURL = "http://127.0.0.1:1"

	t.Run("WEFT_STUDIO_URL wins", func(t *testing.T) {
		discoveryFile(t, fileURL, "disc", os.Getpid(), time.Now())
		t.Setenv("WEFT_STUDIO_URL", "http://127.0.0.1:2")
		logs := captureLog(t)
		p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = p.Shutdown(testCtx(t)) }()
		if u, _ := p.StudioEndpoint(); u != "http://127.0.0.1:2" {
			t.Errorf("StudioEndpoint = %q, want the env's", u)
		}
		if strings.Contains(logs.String(), "joined") {
			t.Errorf("a join was claimed: %s", logs.String())
		}
	})
	t.Run("WEFT_DISCOVERY=off", func(t *testing.T) {
		discoveryFile(t, fileURL, "disc", os.Getpid(), time.Now())
		t.Setenv("WEFT_STUDIO_URL", "")
		t.Setenv("WEFT_DISCOVERY", "off")
		if _, err := Start(testCtx(t), NoGlobal(), Heartbeat(0)); err != errNoDestinations {
			t.Errorf("Start = %v, want no destinations (the file ignored)", err)
		}
	})
	t.Run("NoEnv", func(t *testing.T) {
		discoveryFile(t, fileURL, "disc", os.Getpid(), time.Now())
		t.Setenv("WEFT_STUDIO_URL", "")
		if _, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0)); err != errNoDestinations {
			t.Errorf("Start = %v, want no destinations (the file ignored)", err)
		}
	})
	for name, f := range map[string]struct {
		pid     int
		started time.Time
	}{
		"dead pid": {1 << 30, time.Now()},
		"old":      {os.Getpid(), time.Now().Add(-25 * time.Hour)},
	} {
		t.Run("stale: "+name, func(t *testing.T) {
			discoveryFile(t, fileURL, "disc", f.pid, f.started)
			t.Setenv("WEFT_STUDIO_URL", "")
			logs := captureLog(t)
			if _, err := Start(testCtx(t), NoGlobal(), Heartbeat(0)); err != errNoDestinations {
				t.Errorf("Start = %v, want no destinations (the stale file ignored)", err)
			}
			for _, lvl := range []string{"INFO", "WARN", "ERROR"} {
				if l := lines(logs.String(), lvl); len(l) > 0 {
					t.Errorf("a stale file logged above Debug: %q", l)
				}
			}
		})
	}
}

// TestDiscoveryExplicitStudioWins (review finding 1): explicit > env >
// file — a Studio named in code switches the discovery read off: the
// pipeline has the explicit destination alone, StudioEndpoint (what
// runtime.Install dials) is the explicit one, and no join is claimed.
func TestDiscoveryExplicitStudioWins(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	var mu sync.Mutex
	hits := map[string]int{}
	sink := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hits[name]++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
	}
	discovered, explicit := sink("discovered"), sink("explicit")
	defer discovered.Close()
	defer explicit.Close()
	discoveryFile(t, discovered.URL, "disc-token", os.Getpid(), time.Now())
	logs := captureLog(t)

	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), Studio(explicit.URL, "explicit-token"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.dests); n != 1 {
		t.Errorf("%d destinations, want the explicit Studio alone", n)
	}
	if u, tok := p.StudioEndpoint(); u != explicit.URL || tok != "explicit-token" {
		t.Errorf("StudioEndpoint = %q, %q; want the explicit %q", u, tok, explicit.URL)
	}
	_ = p.Shutdown(testCtx(t))
	mu.Lock()
	defer mu.Unlock()
	if hits["discovered"] != 0 {
		t.Errorf("the discovered Studio got %d requests", hits["discovered"])
	}
	if strings.Contains(logs.String(), "joined") {
		t.Errorf("a join was claimed: %s", logs.String())
	}
}

// runtimeDirDiscoveryFile writes a fresh discovery file naming url
// where a running `weft studio` writes it on Linux —
// $XDG_RUNTIME_DIR/weft/studio.json, XDG_RUNTIME_DIR a temporary
// directory of the test's own — and points the location order at the
// documented one (DefaultDirs) restricted to that directory, so the
// developer's own ./.weft or cache directory never answers.
func runtimeDirDiscoveryFile(t *testing.T, url, token string) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	dir := filepath.Join(xdg, "weft")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := discovery.Dirs
	discovery.Dirs = func() []string {
		var out []string
		for _, d := range discovery.DefaultDirs() {
			if d == dir {
				out = append(out, d)
			}
		}
		return out
	}
	t.Cleanup(func() { discovery.Dirs = old })
	b, _ := json.Marshal(discovery.Info{URL: url, Token: token, PID: os.Getpid(), Started: time.Now(), Version: "v0"})
	if err := os.WriteFile(filepath.Join(dir, discovery.FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, path, ok := discovery.Lookup(func(string) string { return "" }, nil); !ok || path != filepath.Join(dir, discovery.FileName) {
		t.Fatalf("the discovery file is not found (%q, %v): the test proves nothing", path, ok)
	}
}

// countingSink is an OTLP/HTTP endpoint that counts the requests it got.
type countingSink struct {
	*httptest.Server
	mu sync.Mutex
	n  int
}

func newCountingSink(t *testing.T) *countingSink {
	t.Helper()
	s := &countingSink{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.n++
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *countingSink) hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// clearDestinationEnv empties every variable that adds a destination
// or skips the discovery read.
func clearDestinationEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"WEFT_STUDIO_URL", "WEFT_STUDIO_TOKEN", "WEFT_DISCOVERY", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS"} {
		t.Setenv(k, "")
	}
}

// generateOne runs one scripted agent on the global providers (what
// Install registered) or on p's, and returns the run id.
func generateOne(t *testing.T, p *Pipeline) string {
	t.Helper()
	opts := []core.Option{core.Name("discovery-demo")}
	if p != nil {
		opts = append(opts, core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	}
	res, err := core.New(wefttest.Script(wefttest.Say("ok")), opts...).Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	return res.ID
}

// localHasRun reports whether the Local store at path holds run id's
// spans.
func localHasRun(t *testing.T, path, id string) bool {
	t.Helper()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	spans, err := db.RunSpans(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return len(spans) > 0
}

// TestDiscoveryLocalOnlyWritesOnlyLocal (issue 001, I1): an explicit
// Local switches the discovery read off, as an explicit Studio does —
// otel.Install(otel.Local(p)) with a fresh discovery file naming a
// running Studio writes the run to p and sends the Studio nothing,
// reports no Studio endpoint and claims no join.
func TestDiscoveryLocalOnlyWritesOnlyLocal(t *testing.T) {
	clearDestinationEnv(t)
	studio := newCountingSink(t)
	runtimeDirDiscoveryFile(t, studio.URL, "disc-token")
	logs := captureLog(t)
	path := filepath.Join(t.TempDir(), "local.db")

	stop := Install(Local(path), Heartbeat(0))
	if u, _ := StudioEndpoint(); u != "" {
		t.Errorf("otel.StudioEndpoint = %q, want none (the file is not read)", u)
	}
	id := generateOne(t, nil)
	stop()

	if n := studio.hits(); n != 0 {
		t.Errorf("the discovered Studio got %d requests, want 0", n)
	}
	if !localHasRun(t, path, id) {
		t.Errorf("the Local store has no spans for run %s", id)
	}
	if strings.Contains(logs.String(), "joined") {
		t.Errorf("a join was claimed: %s", logs.String())
	}
}

// TestDiscoveryNoDiscoveryKeepsEnv (I1, option B): NoDiscovery skips
// the discovery file and nothing else — the OTEL_* destination stays,
// the discovered Studio gets nothing. Without it the same environment
// joins the file's Studio (the file is live).
func TestDiscoveryNoDiscoveryKeepsEnv(t *testing.T) {
	clearDestinationEnv(t)
	studio, collector := newCountingSink(t), newCountingSink(t)
	runtimeDirDiscoveryFile(t, studio.URL, "disc-token")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	control, err := Start(testCtx(t), NoGlobal(), Heartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(control.dests); n != 2 {
		t.Errorf("without NoDiscovery: %d destinations, want 2 (env OTLP + the file's Studio)", n)
	}
	_ = control.Shutdown(testCtx(t))
	before := studio.hits()

	logs := captureLog(t)
	p, err := Start(testCtx(t), NoGlobal(), NoDiscovery(), Heartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.dests); n != 1 {
		t.Errorf("NoDiscovery: %d destinations, want the env OTLP one alone", n)
	}
	if u, _ := p.StudioEndpoint(); u != "" {
		t.Errorf("StudioEndpoint = %q, want none", u)
	}
	generateOne(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if n := studio.hits() - before; n != 0 {
		t.Errorf("the discovered Studio got %d requests under NoDiscovery", n)
	}
	if collector.hits() == 0 {
		t.Error("the OTEL_EXPORTER_OTLP_ENDPOINT collector got nothing: NoDiscovery dropped the environment")
	}
	if strings.Contains(logs.String(), "joined") {
		t.Errorf("a join was claimed: %s", logs.String())
	}
}

// TestDiscoveryLocalPlusStudio (I1): a caller who wants the Local store
// and a Studio names both — the run reaches both, the discovered one
// (a different Studio) nothing.
func TestDiscoveryLocalPlusStudio(t *testing.T) {
	clearDestinationEnv(t)
	discovered, explicit := newCountingSink(t), newCountingSink(t)
	runtimeDirDiscoveryFile(t, discovered.URL, "disc-token")
	path := filepath.Join(t.TempDir(), "local.db")

	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), Local(path), Studio(explicit.URL, "tok"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.dests); n != 2 {
		t.Errorf("%d destinations, want Local + Studio", n)
	}
	id := generateOne(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if !localHasRun(t, path, id) {
		t.Errorf("the Local store has no spans for run %s", id)
	}
	if explicit.hits() == 0 {
		t.Error("the explicit Studio got nothing")
	}
	if n := discovered.hits(); n != 0 {
		t.Errorf("the discovered Studio got %d requests", n)
	}
}

// TestDiscoveryWeftDevShape (I1-fix): the `weft dev` app — an explicit
// Local, WEFT_STUDIO_URL naming the command's Studio, and a discovery
// file naming a different one — has two destinations: the Local and
// the env Studio, which StudioEndpoint reports; the file's Studio gets
// nothing.
func TestDiscoveryWeftDevShape(t *testing.T) {
	clearDestinationEnv(t)
	dev, other := newCountingSink(t), newCountingSink(t)
	runtimeDirDiscoveryFile(t, other.URL, "other-token")
	t.Setenv("WEFT_STUDIO_URL", dev.URL)
	t.Setenv("WEFT_STUDIO_TOKEN", "dev-token")
	path := filepath.Join(t.TempDir(), "local.db")

	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), Local(path))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.dests); n != 2 {
		t.Errorf("%d destinations, want Local + the env Studio", n)
	}
	if u, tok := p.StudioEndpoint(); u != dev.URL || tok != "dev-token" {
		t.Errorf("StudioEndpoint = %q, %q; want the env's %q, dev-token", u, tok, dev.URL)
	}
	id := generateOne(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if !localHasRun(t, path, id) {
		t.Errorf("the Local store has no spans for run %s", id)
	}
	if dev.hits() == 0 {
		t.Error("the WEFT_STUDIO_URL Studio got nothing")
	}
	if n := other.hits(); n != 0 {
		t.Errorf("the discovery file's Studio got %d requests", n)
	}
}

// TestDiscoveryNoDiscoveryExporters (I1-fix): an Exporters destination
// does not switch the discovery read off — without NoDiscovery the
// pipeline joins the file's Studio as well (and sends it the run);
// with it the run reaches the exporter alone.
func TestDiscoveryNoDiscoveryExporters(t *testing.T) {
	clearDestinationEnv(t)
	studio := newCountingSink(t)
	runtimeDirDiscoveryFile(t, studio.URL, "disc-token")

	for _, tc := range []struct {
		name      string
		opts      []Option
		dests     int
		toStudio  bool
		studioURL string
	}{
		{"without NoDiscovery", nil, 2, true, studio.URL},
		{"NoDiscovery", []Option{NoDiscovery()}, 1, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := tracetest.NewInMemoryExporter()
			before := studio.hits()
			p, err := Start(testCtx(t), append([]Option{NoGlobal(), Heartbeat(0), Exporters(spans, nil)}, tc.opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(p.dests); n != tc.dests {
				t.Errorf("%d destinations, want %d", n, tc.dests)
			}
			if u, _ := p.StudioEndpoint(); u != tc.studioURL {
				t.Errorf("StudioEndpoint = %q, want %q", u, tc.studioURL)
			}
			generateOne(t, p)
			if err := p.ForceFlush(testCtx(t)); err != nil {
				t.Fatal(err)
			}
			if len(spans.GetSpans()) == 0 {
				t.Error("the exporter got no spans")
			}
			if err := p.Shutdown(testCtx(t)); err != nil {
				t.Fatal(err)
			}
			if got := studio.hits() > before; got != tc.toStudio {
				t.Errorf("the discovered Studio got requests: %v, want %v", got, tc.toStudio)
			}
		})
	}
}
