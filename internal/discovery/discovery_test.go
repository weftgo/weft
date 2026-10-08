package discovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
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

// onlyDebug fails on any captured line above Debug.
func onlyDebug(t *testing.T, what string, buf *bytes.Buffer) {
	t.Helper()
	for _, lvl := range []string{"level=INFO", "level=WARN", "level=ERROR"} {
		if strings.Contains(buf.String(), lvl) {
			t.Errorf("%s: a line above Debug: %s", what, buf.String())
		}
	}
}

func writeRaw(t *testing.T, dir string, info Info) {
	t.Helper()
	b, _ := json.Marshal(info)
	writeBytes(t, dir, b)
}

func writeBytes(t *testing.T, dir string, b []byte) {
	t.Helper()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestWriteShapeModeAndRemove: the file is the documented shape, 0600,
// in the first location (created), and Remove takes only its own.
func TestWriteShapeModeAndRemove(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "a", "weft"), filepath.Join(root, "b")
	useDirs(t, first, second)
	info := fresh("http://127.0.0.1:7331")
	w, err := Write(info)
	if err != nil {
		t.Fatal(err)
	}
	p := w.Path
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
	if _, err := os.Stat(filepath.Join(first, ".gitignore")); err == nil {
		t.Error("a .gitignore in a directory that is not .weft")
	}

	// A handle for another Studio does not remove it; its own does.
	other := &Written{Path: p, Info: info}
	other.Info.PID++
	other.Remove()
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("Remove took another Studio's file: %v", err)
	}
	w.Remove()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("Remove left its own file: %v", err)
	}
	(*Written)(nil).Remove() // nil-safe
}

// TestTwoLiveWriters: a second live Studio displaces the first's file;
// when it exits the first's file is put back, never erased; when the
// displaced Studio is gone by then, the file is simply removed.
func TestTwoLiveWriters(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	a := fresh("http://127.0.0.1:7331")
	wa, err := Write(a)
	if err != nil {
		t.Fatal(err)
	}
	b := fresh("http://127.0.0.1:7332")
	b.PID = os.Getppid() // another live process
	wb, err := Write(b)
	if err != nil {
		t.Fatal(err)
	}
	if info, _, ok := Lookup(env(), nil); !ok || info.URL != b.URL {
		t.Fatalf("the newer Studio is not the one found: %+v", info)
	}
	wb.Remove()
	if info, _, ok := Lookup(env(), nil); !ok || info.URL != a.URL {
		t.Errorf("after B exits, A is not discoverable: %+v %v", info, ok)
	}
	wa.Remove()
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("A's exit left a file: %v", err)
	}

	// The displaced Studio died meanwhile: nothing is put back.
	dead := fresh("http://127.0.0.1:7333")
	writeRaw(t, dir, dead)
	wc, err := Write(fresh("http://127.0.0.1:7334"))
	if err != nil {
		t.Fatal(err)
	}
	wc.prev.PID = 1 << 30 // the displaced one is gone now
	wc.Remove()
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("a dead Studio's file was put back: %v", err)
	}
}

// TestWriterRemovesStale: an untrusted file in any location is removed
// by the next writer.
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

// TestGuardDir: the writer drops .weft/.gitignore ("*") when there is
// none, and never touches an existing one.
func TestGuardDir(t *testing.T) {
	weft := filepath.Join(t.TempDir(), ".weft")
	useDirs(t, weft)
	if _, err := Write(fresh("http://127.0.0.1:1")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(weft, ".gitignore"))
	if err != nil || !strings.Contains(string(b), "\n*\n") {
		t.Errorf(".gitignore %q, %v; want one ignoring everything", b, err)
	}
	if err := os.WriteFile(filepath.Join(weft, ".gitignore"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	GuardDir(weft)
	if b, _ := os.ReadFile(filepath.Join(weft, ".gitignore")); string(b) != "mine\n" {
		t.Errorf("an existing .gitignore was rewritten: %q", b)
	}
}

// TestLookupOrder: the reader walks the writer's order and takes the
// first trusted file; untrusted ones are skipped with Debug lines only.
func TestLookupOrder(t *testing.T) {
	weft, xdg, cache := t.TempDir(), t.TempDir(), t.TempDir()
	useDirs(t, weft, xdg, cache)
	writeRaw(t, cache, fresh("http://127.0.0.1:3"))
	if info, _, ok := Lookup(env(), nil); !ok || info.URL != "http://127.0.0.1:3" {
		t.Errorf("cache only: %+v %v", info, ok)
	}
	writeRaw(t, xdg, fresh("http://127.0.0.1:2"))
	if info, _, ok := Lookup(env(), nil); !ok || info.URL != "http://127.0.0.1:2" {
		t.Errorf("xdg before cache: %+v %v", info, ok)
	}
	writeRaw(t, weft, fresh("http://localhost:1"))
	info, p, ok := Lookup(env(), nil)
	if !ok || info.URL != "http://localhost:1" || p != filepath.Join(weft, FileName) || info.Token != "tok" {
		t.Errorf("./.weft first: %+v %s %v", info, p, ok)
	}

	stale := fresh("http://127.0.0.1:1")
	stale.Started = time.Now().Add(-MaxAge - time.Minute)
	writeRaw(t, weft, stale)
	writeBytes(t, xdg, []byte("{nope"))
	log, buf := debugLog()
	if info, _, ok := Lookup(env(), log); !ok || info.URL != "http://127.0.0.1:3" {
		t.Errorf("past stale and malformed: %+v %v", info, ok)
	}
	onlyDebug(t, "order", buf)
	if n := strings.Count(buf.String(), "level=DEBUG"); n != 2 {
		t.Errorf("%d Debug lines, want 2 (stale, malformed):\n%s", n, buf.String())
	}
}

// TestLookupEscapes: WEFT_STUDIO_URL wins (the file is not read) and
// WEFT_DISCOVERY=off turns the read off; a dead pid or an old file is
// ignored silently, and the reader never removes it.
func TestLookupEscapes(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	writeRaw(t, dir, fresh("http://127.0.0.1:9"))
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
		info := fresh("http://127.0.0.1:9")
		mut(&info)
		writeRaw(t, dir, info)
		log, buf := debugLog()
		if _, _, ok := Lookup(env(), log); ok {
			t.Errorf("%s: a stale file was trusted", name)
		}
		onlyDebug(t, name, buf)
		if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
			t.Errorf("%s: the reader removed the file (only a writer does): %v", name, err)
		}
	}
}

// TestUntrustedFiles (review finding 2): a url that is not loopback is
// never trusted — a committed .weft/studio.json naming
// https://evil.example with pid 1 (EPERM: "alive") exports nothing —
// and, on unix, neither is a file that is not 0600 or not this user's.
// Each is one Debug line.
func TestUntrustedFiles(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	for _, u := range []string{"https://evil.example", "http://10.0.0.1:7331", "http://studio.local:7331", "ftp://127.0.0.1", "http://user:pw@127.0.0.1:1", "127.0.0.1:7331"} {
		info := fresh(u)
		info.PID = 1
		writeRaw(t, dir, info)
		log, buf := debugLog()
		if got, _, ok := Lookup(env(), log); ok {
			t.Errorf("url %q trusted: %+v", u, got)
		}
		if Find(u) != "" {
			t.Errorf("Find trusted url %q", u)
		}
		onlyDebug(t, u, buf)
		if !strings.Contains(buf.String(), "level=DEBUG") {
			t.Errorf("url %q: no Debug line", u)
		}
	}
	for _, u := range []string{"http://127.0.0.2:1", "http://[::1]:1", "https://localhost"} {
		writeRaw(t, dir, fresh(u))
		if _, _, ok := Lookup(env(), nil); !ok {
			t.Errorf("loopback url %q not trusted", u)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	writeRaw(t, dir, fresh("http://127.0.0.1:1"))
	p := filepath.Join(dir, FileName)
	for _, mode := range []os.FileMode{0o644, 0o640, 0o400, 0o700} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		log, buf := debugLog()
		if _, _, ok := Lookup(env(), log); ok {
			t.Errorf("mode %#o trusted", mode)
		}
		if !strings.Contains(buf.String(), "not 0600") {
			t.Errorf("mode %#o: Debug line %q", mode, buf.String())
		}
		onlyDebug(t, "mode", buf)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Lookup(env(), nil); !ok {
		t.Error("back to 0600, still untrusted")
	}
	// The owner rule: checkFile on another uid's stat refuses it.
	fi, _ := os.Stat(p)
	if err := checkFile(foreignOwner{fi}); err == nil || !strings.Contains(err.Error(), "not this user") {
		t.Errorf("another user's file: %v", err)
	}
	// A FIFO in the file's place: refused at once, never opened.
	_ = os.Remove(p)
	if err := mkfifo(p); err == nil {
		done := make(chan bool, 1)
		go func() { _, _, ok := Lookup(env(), nil); done <- ok }()
		select {
		case ok := <-done:
			if ok {
				t.Error("a FIFO was trusted")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a FIFO blocked the reader")
		}
	}
}

// TestMalformedFiles (review finding 8): null, [], truncated, a huge
// or negative pid, a future start, an oversized file — never a panic,
// never trusted, Debug only.
func TestMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	now := time.Now().UTC().Format(time.RFC3339)
	for name, body := range map[string]string{
		"null":      `null`,
		"array":     `[]`,
		"string":    `"x"`,
		"empty":     ``,
		"truncated": `{"url":"http://127.0.0.1:1","pid":`,
		"huge pid":  `{"url":"http://127.0.0.1:1","pid":1099511627776,"started":"` + now + `"}`,
		"neg pid":   `{"url":"http://127.0.0.1:1","pid":-1,"started":"` + now + `"}`,
		"future":    `{"url":"http://127.0.0.1:1","pid":` + itoa(os.Getpid()) + `,"started":"2999-01-01T00:00:00Z"}`,
		"bad time":  `{"url":"http://127.0.0.1:1","pid":` + itoa(os.Getpid()) + `,"started":"yesterday"}`,
		"oversized": `{"url":"http://127.0.0.1:1","pad":"` + strings.Repeat("x", maxFileBytes) + `"}`,
	} {
		writeBytes(t, dir, []byte(body))
		log, buf := debugLog()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic %v", name, r)
				}
			}()
			if info, _, ok := Lookup(env(), log); ok {
				t.Errorf("%s: trusted %+v", name, info)
			}
			_ = Find("http://127.0.0.1:1")
		}()
		onlyDebug(t, name, buf)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
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

// TestRefreshKeepsFresh (round 2, finding 2): a Studio running past
// MaxAge stays discoverable once it refreshes; without a refresh its
// file goes stale; a file another Studio wrote since is not touched.
func TestRefreshKeepsFresh(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	w, err := Write(fresh("http://127.0.0.1:7331"))
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(MaxAge + time.Hour)
	old := now
	now = func() time.Time { return later }
	t.Cleanup(func() { now = old })
	if _, _, ok := Lookup(env(), nil); ok {
		t.Fatal("an unrefreshed file is still trusted past MaxAge (test bug)")
	}
	if err := w.Refresh(); err != nil {
		t.Fatal(err)
	}
	if info, _, ok := Lookup(env(), nil); !ok || !info.Started.Equal(later.UTC().Truncate(time.Second)) {
		t.Errorf("after Refresh: %+v %v, want fresh, started re-stamped", info, ok)
	}
	// Another Studio's file: Refresh leaves it alone.
	other := fresh("http://127.0.0.1:7332")
	other.Started = later
	writeRaw(t, dir, other)
	before, _ := os.ReadFile(filepath.Join(dir, FileName))
	_ = w.Refresh()
	if after, _ := os.ReadFile(filepath.Join(dir, FileName)); string(after) != string(before) {
		t.Error("Refresh rewrote another Studio's file")
	}
	var nilW *Written
	if nilW.Refresh() != nil {
		t.Error("nil Refresh")
	}
}

// TestRefreshRewritesMissing: a Studio whose file has gone (removed by
// hand, or by a racing writer's cleanup) writes it again on its next
// Refresh — this pid and url, 0600 — and stays discoverable.
func TestRefreshRewritesMissing(t *testing.T) {
	dir := t.TempDir()
	useDirs(t, dir)
	w, err := Write(fresh("http://127.0.0.1:7331"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, FileName)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := w.Refresh(); err != nil {
		t.Fatal(err)
	}
	info, path, ok := Lookup(env(), nil)
	if !ok || path != p || info.PID != os.Getpid() || info.URL != "http://127.0.0.1:7331" || info.Token != "tok" {
		t.Fatalf("after Refresh of a missing file: %+v %q %v, want this Studio's file back", info, path, ok)
	}
	if fi, err := os.Stat(p); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Errorf("rewritten file: %v %v, want 0600", fi.Mode(), err)
	}
}

// TestCleanupErrorClasses (round 2, finding 4): the writer's cleanup
// removes a file it decoded as stale or rejected for good, and leaves
// it on a transient failure (another Studio's rename mid-read, an open
// error) — two Studios starting together never erase each other.
func TestCleanupErrorClasses(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		info    Info
		removed bool
	}{
		{"changed while being read", errors.New("changed while being read"), Info{}, false},
		{"open error", fs.ErrPermission, Info{}, false},
		{"mode", permanent(errors.New("mode 0644, not 0600")), Info{}, true},
		{"owner", permanent(errors.New("owned by uid 0, not this user")), Info{}, true},
		{"not regular", permanent(errors.New("not a regular file")), Info{}, true},
		{"parse", permanent(errors.New("does not parse")), Info{}, true},
		{"decoded, stale", nil, Info{URL: "http://127.0.0.1:1", PID: 1 << 30, Started: time.Now()}, true},
		{"decoded, fresh", nil, fresh("http://127.0.0.1:1"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := t.TempDir(), t.TempDir()
			useDirs(t, a, b)
			writeRaw(t, b, fresh("http://127.0.0.1:9")) // the file under test, second location
			p := filepath.Join(b, FileName)
			old := loadFile
			loadFile = func(path string) (Info, error) {
				if path == p {
					return c.info, c.err
				}
				return old(path)
			}
			t.Cleanup(func() { loadFile = old })
			if _, err := Write(fresh("http://127.0.0.1:2")); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(p)
			if removed := os.IsNotExist(err); removed != c.removed {
				t.Errorf("removed = %v, want %v", removed, c.removed)
			}
		})
	}
}
