package runtime

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
	"github.com/weftgo/weft/studio"
)

// TestMain points the discovery rung at an empty directory of this
// process's own: no test of this package dials a Studio the developer
// happens to be running.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "weft-runtime-discovery-")
	if err != nil {
		panic(err)
	}
	discovery.Dirs = func() []string { return []string{dir} }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestInstallJoinsDiscoveredStudio: WEFT_ENV=dev, no WEFT_STUDIO_URL,
// no Studio option and no otel Studio destination — Install dials the
// Studio the discovery file names, with its token, registers there,
// and says so in one INFO line. With WEFT_DISCOVERY=off it opens
// nothing.
func TestInstallJoinsDiscoveredStudio(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_DISCOVERY", "")
	const tok = "disc-token"
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "studio.db")), studio.Playground(true),
		studio.Token(tok), studio.Base("/"))
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	dir := t.TempDir()
	old := discovery.Dirs
	discovery.Dirs = func() []string { return []string{dir} }
	t.Cleanup(func() { discovery.Dirs = old })
	b, _ := json.Marshal(discovery.Info{URL: ts.URL, Token: tok, PID: os.Getpid(), Started: time.Now(), Version: "v0"})
	if err := os.WriteFile(filepath.Join(dir, discovery.FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}

	var logs lockedBuf
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	runtimes := func() []string {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runtimes", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Runtimes []struct {
				ID string `json:"id"`
			} `json:"runtimes"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		var ids []string
		for _, r := range out.Runtimes {
			ids = append(ids, r.ID)
		}
		return ids
	}

	t.Run("WEFT_DISCOVERY=off", func(t *testing.T) {
		t.Setenv("WEFT_DISCOVERY", "off")
		stop := Install(Agents(testAgent("disc-agent")))
		time.Sleep(100 * time.Millisecond)
		stop()
		if ids := runtimes(); len(ids) != 0 {
			t.Errorf("WEFT_DISCOVERY=off, and a runtime registered: %v", ids)
		}
	})

	// WEFT_ENV unset and no Enabled: a fresh file opens no link (§6
	// rule 1 holds whatever the file says).
	t.Run("WEFT_ENV unset", func(t *testing.T) {
		t.Setenv("WEFT_ENV", "")
		stop := Install(Agents(testAgent("disc-agent")))
		time.Sleep(100 * time.Millisecond)
		stop()
		if ids := runtimes(); len(ids) != 0 {
			t.Errorf("WEFT_ENV unset, and a runtime registered: %v", ids)
		}
		if strings.Contains(logs.String(), "joined") {
			t.Errorf("a join was claimed with the link off: %s", logs.String())
		}
	})

	stop := Install(Agents(testAgent("disc-agent")))
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
	for len(runtimes()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no runtime registered with the discovered Studio\nlogs:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var info []string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "level=INFO") && strings.Contains(l, "joined the running Studio") {
			info = append(info, l)
		}
	}
	if len(info) != 1 || !strings.Contains(info[0], ts.URL) {
		t.Errorf("join lines %q, want exactly one naming %s", info, ts.URL)
	}
}
