//go:build !windows

package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
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
}

// startDev runs `weft dev args… -- <helper>` in the background, the
// helper in mode app with env set.
func startDev(t *testing.T, args []string, env map[string]string) *devRun {
	t.Helper()
	skipWithoutSelfSignal(t)
	t.Setenv("WEFT_DEV_HELPER", "app")
	for k, v := range env {
		t.Setenv(k, v)
	}
	d := &devRun{out: &syncBuffer{}, errb: &syncBuffer{}, code: make(chan int, 1)}
	argv := append(append([]string{"dev"}, args...), "--", os.Args[0])
	go func() { d.code <- run(argv, d.out, d.errb) }()
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
