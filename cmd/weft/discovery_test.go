package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/version"
)

// Plan B3 at the command: the discovery file and the stable per-DB
// token. Every test points the discovery locations at its own
// directory.

// discoveryDir points discovery's locations at a fresh directory.
func discoveryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := discovery.Dirs
	discovery.Dirs = func() []string { return []string{dir} }
	t.Cleanup(func() { discovery.Dirs = old })
	return dir
}

// bareStudio runs serveWith with tokenFlag (may be "") in the
// background and waits until something answers HTTP on wantAt; stop
// sends SIGTERM and waits for it to return.
func bareStudio(t *testing.T, db string, base, span int, wantAt, tokenFlag string, rotate bool, out *syncBuffer) (stop func()) {
	t.Helper()
	skipWithoutSelfSignal(t)
	done := make(chan error, 1)
	go func() {
		done <- serveWith(db, want{addr: loop(base), span: span}, tokenFlag, out, afterBoot{rotate: rotate})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Get("http://" + wantAt + "/api/meta")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned before serving on %s: %v (stdout %q)", wantAt, err, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("studio never served on %s (stdout %q)", wantAt, out.String())
		}
	}
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		signalSelf(t, syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve after SIGTERM: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("serve never returned after SIGTERM")
		}
	}
	t.Cleanup(stop)
	return stop
}

// metaStatus is GET /api/meta's status at addr with bearer tok.
func metaStatus(t *testing.T, addr, tok string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/meta", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func readDiscovery(t *testing.T, dir string) (discovery.Info, map[string]any) {
	t.Helper()
	p := filepath.Join(dir, discovery.FileName)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("no discovery file: %v", err)
	}
	if fi, _ := os.Stat(p); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("discovery file mode %v, want 0600", fi.Mode().Perm())
	}
	var info discovery.Info
	var raw map[string]any
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &raw)
	return info, raw
}

// TestDiscoveryFileWrittenAndRemoved: a bare start writes the file once
// bound — the real address, its stable token, the database, this pid,
// the version — 0600 and exactly the six fields; the banner names it; a
// second start on the same database (no token flag, no env) reuses the
// running Studio — possible now that the token is stable, the B2 proof —
// and writes nothing; the clean exit removes the file.
func TestDiscoveryFileWrittenAndRemoved(t *testing.T) {
	dir := discoveryDir(t)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	base := freeBase(t, 2)
	dbPath := filepath.Join(t.TempDir(), "weft.db")
	var first syncBuffer
	stop := bareStudio(t, "sqlite://"+dbPath, base, 2, loop(base), "", false, &first)

	info, raw := readDiscovery(t, dir)
	tok, _ := os.ReadFile(dbPath + ".token")
	if info.URL != "http://"+loop(base) || info.Token != strings.TrimSpace(string(tok)) || info.Token == "" ||
		info.DB != dbPath || info.PID != os.Getpid() || info.Version != version.Runtime() ||
		time.Since(info.Started) > time.Minute {
		t.Errorf("discovery file %+v", info)
	}
	if len(raw) != 6 {
		t.Errorf("discovery fields %v, want url, token, db, pid, started, version", raw)
	}
	if s, _ := raw["started"].(string); s == "" {
		t.Errorf("started %v", raw["started"])
	} else if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("started %q is not RFC 3339", s)
	}
	if !strings.Contains(first.String(), "studio: apps find this Studio through "+filepath.Join(dir, discovery.FileName)) {
		t.Errorf("the banner does not name the discovery file:\n%s", first.String())
	}
	if metaStatus(t, loop(base), info.Token) != http.StatusOK {
		t.Error("the discovery file's token does not open the API")
	}

	// The second bare start: reuse, nothing written.
	before, _ := os.ReadFile(filepath.Join(dir, discovery.FileName))
	var second syncBuffer
	if err := serveWith("sqlite://"+dbPath, want{addr: loop(base), span: 2}, "", &second, afterBoot{}); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if want := "studio already running at http://" + loop(base) + " (pid " + strconv.Itoa(os.Getpid()) + "), reusing\n"; second.String() != want {
		t.Errorf("second bare start printed %q, want exactly %q", second.String(), want)
	}
	after, _ := os.ReadFile(filepath.Join(dir, discovery.FileName))
	if string(before) != string(after) {
		t.Error("the reusing start rewrote the discovery file")
	}

	stop()
	if _, err := os.Stat(filepath.Join(dir, discovery.FileName)); !os.IsNotExist(err) {
		t.Errorf("the discovery file outlived the Studio: %v", err)
	}
}

// TestReuseSaysMissingDiscovery: a reused Studio whose file is gone is
// said in one line.
func TestReuseSaysMissingDiscovery(t *testing.T) {
	dir := discoveryDir(t)
	base := freeBase(t, 2)
	db := "sqlite://" + filepath.Join(t.TempDir(), "weft.db")
	var first syncBuffer
	bareStudio(t, db, base, 2, loop(base), "tok", false, &first)
	if err := os.Remove(filepath.Join(dir, discovery.FileName)); err != nil {
		t.Fatal(err)
	}
	var second syncBuffer
	if err := serveWith(db, want{addr: loop(base), span: 2}, "tok", &second, afterBoot{rotate: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"reusing\n",
		"studio: --rotate-token not applied: the running Studio keeps its token",
		"studio: the running Studio wrote no discovery file; apps need WEFT_STUDIO_URL=http://" + loop(base) + "\n",
	} {
		if !strings.Contains(second.String(), want) {
			t.Errorf("reuse output %q, want %q", second.String(), want)
		}
	}
}

// TestDiscoveryFollowsTheMove: the B2 proof — a foreign process on the
// base port moves Studio to the next one, and the discovery file names
// that port.
func TestDiscoveryFollowsTheMove(t *testing.T) {
	dir := discoveryDir(t)
	base := freeBase(t, 2)
	foreign, err := net.Listen("tcp", loop(base))
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	go func() { _ = hs.Serve(foreign) }()
	defer func() { _ = hs.Close() }()

	var out syncBuffer
	bareStudio(t, "sqlite://"+filepath.Join(t.TempDir(), "weft.db"), base, 2, loop(base+1), "", false, &out)
	if !strings.HasPrefix(out.String(), "studio: "+loop(base)+" is busy (not a weft Studio)") {
		t.Errorf("stdout %q, want the move explained", out.String())
	}
	if info, _ := readDiscovery(t, dir); info.URL != "http://"+loop(base+1) {
		t.Errorf("discovery url %q, want http://%s", info.URL, loop(base+1))
	}
}

// TestWriterReplacesStale: a stale file (a dead pid) is replaced by the
// next start.
func TestWriterReplacesStale(t *testing.T) {
	dir := discoveryDir(t)
	stale := `{"url":"http://127.0.0.1:1","token":"old","db":"","pid":1073741824,"started":"` +
		time.Now().UTC().Format(time.RFC3339) + `","version":"v0"}`
	if err := os.WriteFile(filepath.Join(dir, discovery.FileName), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	base := freeBase(t, 1)
	var out syncBuffer
	bareStudio(t, "sqlite://"+filepath.Join(t.TempDir(), "weft.db"), base, 1, loop(base), "tok", false, &out)
	if info, _ := readDiscovery(t, dir); info.URL != "http://"+loop(base) || info.PID != os.Getpid() {
		t.Errorf("the stale file was not replaced: %+v", info)
	}
}

// TestStableTokenSurvivesRestart: two starts on one database, the
// second after the first stopped, serve the same token; --rotate-token
// changes it (the old one 401s); --token overrides it and leaves the
// file alone.
func TestStableTokenSurvivesRestart(t *testing.T) {
	discoveryDir(t)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	dbPath := filepath.Join(t.TempDir(), "weft.db")
	db := "sqlite://" + dbPath
	base := freeBase(t, 1)
	start := func(tokenFlag string, rotate bool) (*syncBuffer, func()) {
		var out syncBuffer
		return &out, bareStudio(t, db, base, 1, loop(base), tokenFlag, rotate, &out)
	}
	fileTok := func() string {
		b, err := os.ReadFile(dbPath + ".token")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}

	out, stop := start("", false)
	first := fileTok()
	if metaStatus(t, loop(base), first) != http.StatusOK || strings.Contains(out.String(), first) {
		t.Errorf("first start: token %q, banner %q", first, out.String())
	}
	stop()

	out, stop = start("", false)
	if got := fileTok(); got != first || metaStatus(t, loop(base), first) != http.StatusOK {
		t.Errorf("the token changed across a restart: %q, then %q", first, got)
	}
	if !strings.Contains(out.String(), "studio: token from "+dbPath+".token (stable for this database") {
		t.Errorf("second banner %q", out.String())
	}
	stop()

	out, stop = start("", true)
	rotated := fileTok()
	if rotated == first || metaStatus(t, loop(base), rotated) != http.StatusOK || metaStatus(t, loop(base), first) != http.StatusUnauthorized {
		t.Errorf("--rotate-token: %q (was %q)", rotated, first)
	}
	if !strings.Contains(out.String(), "studio: token rotated: a new one in "+dbPath+".token") || strings.Contains(out.String(), rotated) {
		t.Errorf("rotate banner %q", out.String())
	}
	stop()

	_, stop = start("override", false)
	if fileTok() != rotated || metaStatus(t, loop(base), "override") != http.StatusOK || metaStatus(t, loop(base), rotated) != http.StatusUnauthorized {
		t.Error("--token did not override the stable token, or touched its file")
	}
	stop()
}
