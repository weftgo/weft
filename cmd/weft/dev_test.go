//go:build !windows

package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// `weft dev` (plan B1.2). The app is the helper process
// (dev_helper_test.go); Studio listens on a free port; every test ends
// with no process and nothing listening.

// oneLineRe is the one line: studio <link> · app pid <n> · <runtime part>.
var oneLineRe = regexp.MustCompile(`(?m)^studio (\S+) · app pid (\d+) · (.*)$`)

// helperRe is the helper's own line: its pid and what it was handed.
var helperRe = regexp.MustCompile(`(?m)^helper pid=(\d+) WEFT_ENV=(\S*) WEFT_STUDIO_URL=(\S*) WEFT_STUDIO_TOKEN=(\S*) WEFT_DB=(\S*)$`)

// devRun is a `weft dev` running in the background.
type devRun struct {
	out, errb *syncBuffer
	code      chan int
	exited    bool // the exit code was received
}

// startDev runs `weft dev args…` in the background — args without a
// "--" get `-- <helper>`, the helper in mode app with env set. A test
// that fails before weft dev returned still leaves nothing behind: the
// cleanup interrupts it (the app's group goes with it) and waits.
func startDev(t *testing.T, args []string, env map[string]string) *devRun {
	t.Helper()
	skipWithoutSelfSignal(t)
	t.Setenv("WEFT_DEV_HELPER", "app")
	for k, v := range env {
		t.Setenv(k, v)
	}
	d := &devRun{out: &syncBuffer{}, errb: &syncBuffer{}, code: make(chan int, 1)}
	argv := append([]string{"dev"}, args...)
	if !slices.Contains(args, "--") {
		argv = append(argv, "--", os.Args[0])
	}
	go func() { d.code <- run(argv, d.out, d.errb) }()
	t.Cleanup(func() {
		if d.exited {
			return
		}
		signalSelf(t, syscall.SIGINT)
		select {
		case <-d.code:
		case <-time.After(20 * time.Second):
			t.Errorf("cleanup: weft dev never returned")
		}
	})
	return d
}

// waitOut waits until re matches stdout n times and returns the
// matches.
func (d *devRun) waitOut(t *testing.T, re *regexp.Regexp, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if m := re.FindAllStringSubmatch(d.out.String(), -1); len(m) >= n {
			return m
		}
		select {
		case code := <-d.code:
			d.exited = true
			t.Fatalf("weft dev exited %d before %s matched %d times\nstdout:\n%s\nstderr:\n%s", code, re, n, d.out.String(), d.errb.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never matched %d times\nstdout:\n%s\nstderr:\n%s", re, n, d.out.String(), d.errb.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// interrupt is Ctrl-C: SIGINT to this process, which weft dev catches;
// it must exit 0.
func (d *devRun) interrupt(t *testing.T) {
	t.Helper()
	signalSelf(t, syscall.SIGINT)
	d.wait(t, 0)
}

// wait waits for weft dev's exit code.
func (d *devRun) wait(t *testing.T, want int) {
	t.Helper()
	select {
	case code := <-d.code:
		d.exited = true
		if code != want {
			t.Fatalf("weft dev = exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, want, d.out.String(), d.errb.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("weft dev never returned\nstdout:\n%s", d.out.String())
	}
}

// gone waits until pid no longer exists (a zombie reparented to init
// is reaped quickly; the deadline bounds it).
func gone(t *testing.T, what string, pid int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s (pid %d) is still running", what, pid)
		}
	}
}

// notListening asserts nothing accepts on addr.
func notListening(t *testing.T, addr string) {
	t.Helper()
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Errorf("something still listens on %s", addr)
	}
}

func shortRuntimeWait(t *testing.T, d time.Duration) {
	t.Helper()
	old := devRuntimeWait
	devRuntimeWait = d
	t.Cleanup(func() { devRuntimeWait = old })
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDevEnvAndOneLine: the app sees the four variables, the one line
// names its pid, and the link carries a generated token — never a
// fixed one, which the app still receives. Ctrl-C (SIGINT) stops the
// app first with the same signal, then Studio: exit 0, the app gone,
// nothing listening. With no runtime registering, the line says so.
func TestDevEnvAndOneLine(t *testing.T) {
	shortRuntimeWait(t, 300*time.Millisecond)
	for _, c := range []struct {
		name, token string
	}{{"generated", ""}, {"fixed", "fixed-signing-key"}} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "weft.db")
			addr := loop(freeBase(t, 1))
			args := []string{"--addr", addr, "--db", "sqlite://" + db, "--watch", dir}
			if c.token != "" {
				args = append(args, "--token", c.token)
			}
			d := startDev(t, args, map[string]string{"WEFT_STUDIO_TOKEN": "", "WEFT_ENV": ""})
			line := d.waitOut(t, oneLineRe, 1)[0]
			helper := d.waitOut(t, helperRe, 1)[0]

			link, pid := line[1], atoi(t, line[2])
			if hp := atoi(t, helper[1]); hp != pid {
				t.Errorf("the one line names pid %d, the app is %d", pid, hp)
			}
			if helper[2] != "dev" {
				t.Errorf("WEFT_ENV = %q, want dev", helper[2])
			}
			if helper[3] != "http://"+addr {
				t.Errorf("WEFT_STUDIO_URL = %q, want http://%s", helper[3], addr)
			}
			if helper[5] != db {
				t.Errorf("WEFT_DB = %q, want %s", helper[5], db)
			}
			tok := helper[4]
			switch c.token {
			case "":
				if tok == "" || link != "http://"+addr+"/#token="+tok {
					t.Errorf("generated token %q: link %q, want it in the fragment", tok, link)
				}
			default:
				if tok != c.token {
					t.Errorf("WEFT_STUDIO_TOKEN = %q, want the fixed %q", tok, c.token)
				}
				if link != "http://"+addr+"/" || strings.Contains(line[0], c.token) {
					t.Errorf("a fixed token's line %q: want the bare URL", line[0])
				}
			}
			if want := "no runtime registered yet (the app needs runtime.Install; WEFT_ENV=dev is set)"; line[3] != want {
				t.Errorf("runtime part %q, want %q", line[3], want)
			}

			d.interrupt(t)
			if !strings.Contains(d.out.String(), "helper pid="+line[2]+" got interrupt") {
				t.Errorf("the app was not stopped with SIGINT first:\n%s", d.out.String())
			}
			if !strings.Contains(d.out.String(), "studio: shutting down") {
				t.Errorf("studio was not stopped:\n%s", d.out.String())
			}
			gone(t, "the app", pid)
			notListening(t, addr)
		})
	}
}

// TestDevRestartOnSave: a .go write under --watch restarts the app — a
// new pid, the old process and its group (go run's child: here the
// helper's grandchild) gone — and two quick writes are one restart. A
// non-.go file restarts nothing.
func TestDevRestartOnSave(t *testing.T) {
	shortRuntimeWait(t, 200*time.Millisecond)
	dir, pids := t.TempDir(), t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, skip := range []string{"node_modules", "testdata"} {
		if err := os.Mkdir(filepath.Join(dir, skip), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	addr := loop(freeBase(t, 1))
	d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(pids, "weft.db"), "--watch", dir},
		map[string]string{"WEFT_DEV_HELPER_GRANDCHILD": pids})
	first := atoi(t, d.waitOut(t, oneLineRe, 1)[0][2])
	gc1 := grandchild(t, pids, first)

	for i := range 2 {
		if err := os.WriteFile(src, []byte("package main\n// save "+strconv.Itoa(i)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := d.waitOut(t, oneLineRe, 2)
	second := atoi(t, lines[1][2])
	if second == first {
		t.Fatalf("the app's pid did not change: %d", first)
	}
	gone(t, "the old app", first)
	gone(t, "the old app's grandchild", gc1)
	gc2 := grandchild(t, pids, second)

	// Skipped directories and an editor's lock file restart nothing.
	for _, p := range []string{"node_modules/dep.go", "testdata/fixture.go", ".#main.go"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Well past the debounce window: still one restart.
	time.Sleep(4 * devDebounce)
	if n := strings.Count(d.out.String(), "changed; restarting the app"); n != 1 {
		t.Errorf("%d restarts for two quick writes, want 1:\n%s", n, d.out.String())
	}
	if n := len(oneLineRe.FindAllString(d.out.String(), -1)); n != 2 {
		t.Errorf("%d app starts, want 2:\n%s", n, d.out.String())
	}
	if !strings.Contains(d.out.String(), "weft dev: main.go changed") &&
		!strings.Contains(d.out.String(), "weft dev: "+src+" changed") {
		t.Errorf("the restart line does not name the file:\n%s", d.out.String())
	}

	d.interrupt(t)
	gone(t, "the app", second)
	gone(t, "the app's grandchild", gc2)
	notListening(t, addr)
}

// grandchild waits for the pid the app pid's grandchild wrote.
func grandchild(t *testing.T, dir string, app int) int {
	t.Helper()
	path := filepath.Join(dir, strconv.Itoa(app))
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return atoi(t, string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("app %d's grandchild never wrote its pid", app)
		}
	}
}

// TestDevNoWatchExitCode: under --no-watch the app's own exit ends
// weft dev, with the app's exit code, Studio stopped.
func TestDevNoWatchExitCode(t *testing.T) {
	shortRuntimeWait(t, 200*time.Millisecond)
	addr := loop(freeBase(t, 1))
	d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(t.TempDir(), "weft.db"), "--no-watch"},
		map[string]string{"WEFT_DEV_HELPER_EXIT": "3"})
	d.wait(t, 3)
	if !strings.Contains(d.out.String(), "exited (exit code 3)") {
		t.Errorf("the exit is not reported:\n%s", d.out.String())
	}
	notListening(t, addr)
}

// TestDevRuntimeRegistered: an app whose runtime registers with the
// dev Studio (runtime.Install over WEFT_STUDIO_URL / WEFT_STUDIO_TOKEN,
// WEFT_ENV=dev) is named on the one line.
func TestDevRuntimeRegistered(t *testing.T) {
	dir := t.TempDir()
	addr := loop(freeBase(t, 1))
	d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(dir, "weft.db"), "--watch", dir},
		map[string]string{"WEFT_DEV_HELPER_RUNTIME": "1", "WEFT_STUDIO_TOKEN": ""})
	line := d.waitOut(t, oneLineRe, 1)[0]
	if !regexp.MustCompile(`^runtime rt_\S+ registered$`).MatchString(line[3]) {
		t.Errorf("runtime part %q, want runtime rt_… registered", line[3])
	}
	d.interrupt(t)
	gone(t, "the app", atoi(t, line[2]))
	notListening(t, addr)
}

// TestDevEnv pins the child's environment: the three connection
// variables override the shell's, WEFT_ENV is set unless already
// non-empty, WEFT_DB is left alone for a database with no file.
func TestDevEnv(t *testing.T) {
	get := func(env []string, k string) (string, int) {
		v, n := "", 0
		for _, kv := range env {
			if key, val, _ := strings.Cut(kv, "="); key == k {
				v, n = val, n+1
			}
		}
		return v, n
	}
	env := devEnv([]string{"PATH=/bin", "WEFT_ENV=", "WEFT_STUDIO_URL=http://old", "WEFT_STUDIO_TOKEN=old", "WEFT_DB=/old.db"},
		"http://127.0.0.1:7331", "tok", "/new.db")
	for k, want := range map[string]string{"PATH": "/bin", "WEFT_ENV": "dev", "WEFT_STUDIO_URL": "http://127.0.0.1:7331", "WEFT_STUDIO_TOKEN": "tok", "WEFT_DB": "/new.db"} {
		if v, n := get(env, k); v != want || n != 1 {
			t.Errorf("%s = %q (×%d), want %q once", k, v, n, want)
		}
	}
	env = devEnv([]string{"WEFT_ENV=staging", "WEFT_DB=/mine.db"}, "http://x", "tok", "")
	if v, _ := get(env, "WEFT_ENV"); v != "staging" {
		t.Errorf("a set WEFT_ENV was overridden: %q", v)
	}
	if v, _ := get(env, "WEFT_DB"); v != "/mine.db" {
		t.Errorf("WEFT_DB without a Studio file = %q, want the shell's", v)
	}
}

// setVar sets *v for the test.
func setVar[T any](t *testing.T, v *T, val T) {
	t.Helper()
	old := *v
	*v = val
	t.Cleanup(func() { *v = old })
}

// writeGo writes a .go file under dir.
func writeGo(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("package main\n// "+body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDevNewDirRestarts: a directory that arrives with .go files in it
// (a checkout, a mv) is a change — one restart — though no file event
// inside it was ever watched.
func TestDevNewDirRestarts(t *testing.T) {
	shortRuntimeWait(t, 200*time.Millisecond)
	dir, side := t.TempDir(), t.TempDir()
	addr := loop(freeBase(t, 1))
	d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(side, "weft.db"), "--watch", dir}, nil)
	d.waitOut(t, oneLineRe, 1)
	pkg := filepath.Join(side, "pkg")
	if err := os.Mkdir(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	writeGo(t, pkg, "a.go", "moved in")
	if err := os.Rename(pkg, filepath.Join(dir, "pkg")); err != nil {
		t.Fatal(err)
	}
	d.waitOut(t, oneLineRe, 2)
	time.Sleep(4 * devDebounce)
	if n := strings.Count(d.out.String(), "changed; restarting the app"); n != 1 {
		t.Errorf("%d restarts for one directory moved in, want 1:\n%s", n, d.out.String())
	}
	// An empty directory is no change.
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * devDebounce)
	if n := strings.Count(d.out.String(), "changed; restarting the app"); n != 1 {
		t.Errorf("an empty directory restarted the app:\n%s", d.out.String())
	}
	d.interrupt(t)
	notListening(t, addr)
}

// TestDevStopSignals: SIGTERM and SIGHUP (the terminal closed) stop
// weft dev like Ctrl-C — the app first (SIGHUP becomes SIGTERM for
// it: many servers read SIGHUP as reload), then Studio; exit 0.
func TestDevStopSignals(t *testing.T) {
	shortRuntimeWait(t, 200*time.Millisecond)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			dir := t.TempDir()
			addr := loop(freeBase(t, 1))
			d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(dir, "weft.db"), "--watch", dir}, nil)
			pid := atoi(t, d.waitOut(t, oneLineRe, 1)[0][2])
			signalSelf(t, sig)
			d.wait(t, 0)
			if !strings.Contains(d.out.String(), "helper pid="+strconv.Itoa(pid)+" got terminated") {
				t.Errorf("the app was not stopped with SIGTERM:\n%s", d.out.String())
			}
			if !strings.Contains(d.out.String(), "studio: shutting down") {
				t.Errorf("studio was not stopped:\n%s", d.out.String())
			}
			gone(t, "the app", pid)
			notListening(t, addr)
		})
	}
}

// TestDevStartFailure: a command that cannot start is said; in watch
// mode the next save retries it, under --no-watch weft dev exits 127
// (the shell's "command not found") instead of waiting forever.
func TestDevStartFailure(t *testing.T) {
	const missing = "/nonexistent/weft-dev-no-such-command"
	failRe := regexp.MustCompile(`(?m)^weft dev: could not start "` + missing + `"`)

	dir := t.TempDir()
	addr := loop(freeBase(t, 1))
	d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(t.TempDir(), "weft.db"), "--watch", dir, "--", missing}, nil)
	d.waitOut(t, failRe, 1)
	writeGo(t, dir, "main.go", "retry")
	d.waitOut(t, failRe, 2)
	d.interrupt(t)
	notListening(t, addr)

	addr = loop(freeBase(t, 1))
	d = startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(t.TempDir(), "weft.db"), "--no-watch", "--", missing}, nil)
	d.wait(t, 127)
	if !failRe.MatchString(d.out.String()) {
		t.Errorf("the start failure is not said:\n%s", d.out.String())
	}
	notListening(t, addr)
}

// TestDevAppExitThenSave: an app that exits on its own in watch mode is
// reported with its code, and the next save starts it again.
func TestDevAppExitThenSave(t *testing.T) {
	exitRe := regexp.MustCompile(`(?m)^weft dev: app pid \d+ exited \(exit code 3\); a \.go save restarts it$`)
	dir := t.TempDir()
	addr := loop(freeBase(t, 1))
	d := startDev(t, []string{"--addr", addr, "--db", "sqlite://" + filepath.Join(t.TempDir(), "weft.db"), "--watch", dir},
		map[string]string{"WEFT_DEV_HELPER_EXIT": "3"})
	d.waitOut(t, exitRe, 1)
	writeGo(t, dir, "main.go", "fixed")
	d.waitOut(t, exitRe, 2)
	if n := len(helperRe.FindAllString(d.out.String(), -1)); n != 2 {
		t.Errorf("the app ran %d times, want 2:\n%s", n, d.out.String())
	}
	d.interrupt(t)
	notListening(t, addr)
}

// TestDevPortShift: without --addr the port policy applies — a busy
// base port moves Studio to the next one, said in one line, and the
// app's WEFT_STUDIO_URL (and the one line) name the port really used.
func TestDevPortShift(t *testing.T) {
	shortRuntimeWait(t, 200*time.Millisecond)
	base := freeBase(t, 2)
	foreign, err := net.Listen("tcp", loop(base))
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	go func() { _ = hs.Serve(foreign) }()
	defer func() { _ = hs.Close() }()
	setVar(t, &devDefaultAddr, loop(base))
	setVar(t, &devSpan, 2)
	t.Setenv("WEFT_STUDIO_ADDR", "")

	dir := t.TempDir()
	d := startDev(t, []string{"--db", "sqlite://" + filepath.Join(dir, "weft.db"), "--watch", dir}, nil)
	line := d.waitOut(t, oneLineRe, 1)[0]
	helper := d.waitOut(t, helperRe, 1)[0]
	if helper[3] != "http://"+loop(base+1) {
		t.Errorf("WEFT_STUDIO_URL = %q, want the shifted http://%s", helper[3], loop(base+1))
	}
	if !strings.HasPrefix(line[1], "http://"+loop(base+1)+"/") {
		t.Errorf("the one line's link %q is not on %s", line[1], loop(base+1))
	}
	if !strings.Contains(d.out.String(), "studio: "+loop(base)+" is busy") {
		t.Errorf("the shift is not said:\n%s", d.out.String())
	}
	d.interrupt(t)
	notListening(t, loop(base+1))
}

// TestDevReusesRunningStudio: a Studio already serving the same
// database, walled by the fixed token weft dev was given, is reused —
// the reuse line, the app pointed at its URL with that token — and
// weft dev stops only the app: the reused Studio is not its to stop.
func TestDevReusesRunningStudio(t *testing.T) {
	shortRuntimeWait(t, 200*time.Millisecond)
	base := freeBase(t, 1)
	dir := t.TempDir()
	db := "sqlite://" + filepath.Join(dir, "weft.db")
	var running syncBuffer
	stopRunning := startStudio(t, db, base, 1, loop(base), &running)
	setVar(t, &devDefaultAddr, loop(base))
	setVar(t, &devSpan, 1)
	t.Setenv("WEFT_STUDIO_ADDR", "")

	d := startDev(t, []string{"--db", db, "--token", "tok", "--watch", dir}, nil)
	line := d.waitOut(t, oneLineRe, 1)[0]
	helper := d.waitOut(t, helperRe, 1)[0]
	if !strings.Contains(d.out.String(), "studio already running at http://"+loop(base)) {
		t.Errorf("no reuse line:\n%s", d.out.String())
	}
	if helper[3] != "http://"+loop(base) || helper[4] != "tok" {
		t.Errorf("the app got %q / %q, want the running Studio's URL and the fixed token", helper[3], helper[4])
	}
	if line[1] != "http://"+loop(base)+"/" {
		t.Errorf("link %q, want the bare running Studio URL", line[1])
	}
	// One SIGTERM stops both: weft dev (its app first) and the running
	// Studio (its own serveOn) — weft dev must not print a shutdown of
	// a Studio it did not start.
	pid := atoi(t, line[2])
	stopRunning()
	d.wait(t, 0)
	gone(t, "the app", pid)
	if strings.Contains(d.out.String(), "studio: shutting down") {
		t.Errorf("weft dev stopped a Studio it reused:\n%s", d.out.String())
	}
	notListening(t, loop(base))
}

// TestLoopbackAddr: the app dials loopback for a Studio bound to every
// interface (otel refuses plaintext to a non-loopback host).
func TestLoopbackAddr(t *testing.T) {
	for in, want := range map[string]string{
		"0.0.0.0:7331":   "127.0.0.1:7331",
		"[::]:7331":      "127.0.0.1:7331",
		":7331":          "127.0.0.1:7331",
		"127.0.0.1:7331": "127.0.0.1:7331",
		"10.0.0.5:7331":  "10.0.0.5:7331",
	} {
		if got := loopbackAddr(in); got != want {
			t.Errorf("loopbackAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
