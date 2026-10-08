package otel

import (
	"bytes"
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

	"github.com/weftgo/weft/internal/discovery"
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
