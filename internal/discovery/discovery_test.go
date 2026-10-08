package discovery

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// useDirs points the location order at dirs for the test.
func useDirs(t *testing.T, dirs ...string) {
	t.Helper()
	old := Dirs
	Dirs = func() []string { return dirs }
	t.Cleanup(func() { Dirs = old })
}

func fresh(url string) Info {
	return Info{URL: url, Token: "tok", DB: "/x/weft.db", PID: os.Getpid(), Started: time.Now().UTC().Truncate(time.Second), Version: "v0"}
}

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// debugLog captures every record at Debug and above.
func debugLog() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug})), &b
}

// TestWriteShapeModeAndRemove: the file is the documented shape, 0600,
// in the first location (created), and Remove takes only its own.
func TestWriteShapeModeAndRemove(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "a", "weft"), filepath.Join(root, "b")
	useDirs(t, first, second)
	info := fresh("http://127.0.0.1:7331")
	p, err := Write(info)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(first, FileName) {
		t.Errorf("written to %s, want the first location", p)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(p)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"url", "token", "db", "pid", "started", "version"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("field %q missing: %s", k, b)
		}
	}
	if len(raw) != 6 {
		t.Errorf("fields %v, want exactly the six", raw)
	}
	if _, err := time.Parse(time.RFC3339, raw["started"].(string)); err != nil {
		t.Errorf("started %v is not RFC 3339", raw["started"])
	}
	if got := Find("http://127.0.0.1:7331/"); got != p {
		t.Errorf("Find = %q, want %q", got, p)
	}

	// Another Studio's info does not remove it; its own does.
	other := info
	other.PID++
	Remove(p, other)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("Remove took another Studio's file: %v", err)
	}
	Remove(p, info)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("Remove left its own file: %v", err)
	}
}

// TestWriterRemovesStale: a stale file in any location is removed by
// the next writer; a fresh one elsewhere stays.
func TestWriterRemovesStale(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	useDirs(t, a, b)
	old := fresh("http://127.0.0.1:1")
	old.Started = time.Now().Add(-25 * time.Hour)
	writeRaw(t, b, old)
	writeRaw(t, a, Info{URL: "http://127.0.0.1:2", PID: 1 << 30, Started: time.Now()}) // a dead pid
	if _, err := Write(fresh("http://127.0.0.1:3")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b, FileName)); !os.IsNotExist(err) {
		t.Errorf("the stale file in the second location survived: %v", err)
	}
	info, _, ok := Lookup(env(), nil)
	if !ok || info.URL != "http://127.0.0.1:3" {
		t.Errorf("Lookup = %+v %v, want the new file", info, ok)
	}
}

func writeRaw(t *testing.T, dir string, info Info) {
	t.Helper()
	b, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(dir, FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLookupOrder: the reader walks the writer's order and takes the
// first fresh file; stale and malformed ones are skipped with Debug
// lines only.
func TestLookupOrder(t *testing.T) {
	weft, xdg, cache := t.TempDir(), t.TempDir(), t.TempDir()
	useDirs(t, weft, xdg, cache)
	writeRaw(t, cache, fresh("http://cache"))
	if info, _, ok := Lookup(env(), nil); !ok || info.URL != "http://cache" {
		t.Errorf("cache only: %+v %v", info, ok)
	}
	writeRaw(t, xdg, fresh("http://xdg"))
	if info, _, ok := Lookup(env(), nil); !ok || info.URL != "http://xdg" {
		t.Errorf("xdg before cache: %+v %v", info, ok)
	}
	writeRaw(t, weft, fresh("http://weft"))
	info, p, ok := Lookup(env(), nil)
	if !ok || info.URL != "http://weft" || p != filepath.Join(weft, FileName) || info.Token != "tok" {
		t.Errorf("./.weft first: %+v %s %v", info, p, ok)
	}

	// Stale and malformed files ahead of a fresh one: skipped, Debug only.
	stale := fresh("http://weft")
	stale.Started = time.Now().Add(-MaxAge - time.Minute)
	writeRaw(t, weft, stale)
	if err := os.WriteFile(filepath.Join(xdg, FileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	log, buf := debugLog()
	if info, _, ok := Lookup(env(), log); !ok || info.URL != "http://cache" {
		t.Errorf("past stale and malformed: %+v %v", info, ok)
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(line, "level=DEBUG") {
			t.Errorf("a line above Debug: %s", line)
		}
	}
	if n := strings.Count(buf.String(), "level=DEBUG"); n != 2 {
		t.Errorf("%d Debug lines, want 2 (stale, malformed):\n%s", n, buf.String())
	}
}

// TestLookupEscapes: WEFT_STUDIO_URL wins (the file is not read) and
// WEFT_DISCOVERY=off turns the read off; a dead pid or an old file is
// ignored silently.
func TestLookupEscapes(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	writeRaw(t, dir, fresh("http://found"))
	if _, _, ok := Lookup(env("WEFT_STUDIO_URL", "http://explicit"), nil); ok {
		t.Error("WEFT_STUDIO_URL set, and the file was read")
	}
	if _, _, ok := Lookup(env("WEFT_DISCOVERY", "off"), nil); ok {
		t.Error("WEFT_DISCOVERY=off, and the file was read")
	}
	if _, _, ok := Lookup(env("WEFT_DISCOVERY", "on"), nil); !ok {
		t.Error("a fresh file was not read")
	}
	for name, mut := range map[string]func(*Info){
		"dead pid": func(i *Info) { i.PID = 1 << 30 },
		"old":      func(i *Info) { i.Started = time.Now().Add(-MaxAge) },
		"no pid":   func(i *Info) { i.PID = 0 },
	} {
		if name == "dead pid" && runtime.GOOS == "windows" {
			continue // liveness is best effort there
		}
		info := fresh("http://found")
		mut(&info)
		writeRaw(t, dir, info)
		log, buf := debugLog()
		if _, _, ok := Lookup(env(), log); ok {
			t.Errorf("%s: a stale file was trusted", name)
		}
		if strings.Contains(buf.String(), "level=INFO") || strings.Contains(buf.String(), "level=WARN") {
			t.Errorf("%s: noise above Debug: %s", name, buf.String())
		}
		if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
			t.Errorf("%s: the reader removed the file (only a writer does): %v", name, err)
		}
	}
}

// TestDefaultDirs: ./.weft first only when it exists; XDG next when
// set; the user cache directory last.
func TestDefaultDirs(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	cache, cerr := os.UserCacheDir()
	dirs := DefaultDirs()
	if len(dirs) == 0 || dirs[0] != filepath.Join(xdg, "weft") {
		t.Errorf("without ./.weft: %v, want XDG first", dirs)
	}
	if cerr == nil && dirs[len(dirs)-1] != filepath.Join(cache, "weft") {
		t.Errorf("last %v, want the user cache dir", dirs)
	}
	if err := os.Mkdir(".weft", 0o700); err != nil {
		t.Fatal(err)
	}
	dirs = DefaultDirs()
	if real, _ := filepath.EvalSymlinks(dirs[0]); filepath.Base(dirs[0]) != ".weft" || !strings.HasPrefix(real, mustEval(t, wd)) {
		t.Errorf("with ./.weft: %v, want it first", dirs)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	for _, d := range DefaultDirs() {
		if strings.HasPrefix(d, xdg) {
			t.Errorf("XDG unset, still listed: %v", d)
		}
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
