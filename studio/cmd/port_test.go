package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The port policy at the command (plan B2; internal/listen is the
// policy itself and pins each verdict). Every test takes its own run
// of free ports: none depends on 7331 being free here.

// syncBuffer is a stdout two goroutines may share.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// freeBase is a loopback port whose n-1 successors were free too.
func freeBase(t *testing.T, n int) int {
	t.Helper()
	for range 50 {
		var held []net.Listener
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, ln)
		p := ln.Addr().(*net.TCPAddr).Port
		ok := p+n-1 <= 65535
		for i := 1; ok && i < n; i++ {
			l, err := net.Listen("tcp", loop(p+i))
			if err != nil {
				ok = false
				break
			}
			held = append(held, l)
		}
		for _, l := range held {
			_ = l.Close()
		}
		if ok {
			return p
		}
	}
	t.Fatalf("no run of %d free ports", n)
	return 0
}

func loop(port int) string { return "127.0.0.1:" + strconv.Itoa(port) }

// startStudio runs serve in the background on base and waits until
// api/meta answers on wantAt; stop sends SIGTERM (serveOn's graceful
// stop) and waits for serve to return.
func startStudio(t *testing.T, db string, base, span int, wantAt string, out *syncBuffer) (stop func()) {
	t.Helper()
	skipWithoutSelfSignal(t)
	done := make(chan error, 1)
	go func() { done <- serve(db, want{addr: loop(base), span: span}, "tok", out) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+wantAt+"/api/meta", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned before serving on %s: %v (stdout %q)", wantAt, err, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("studio never served on %s: %v (stdout %q)", wantAt, err, out.String())
		}
	}
	return func() {
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
}

// TestSecondStartReuses: two starts in a row on one database — the
// second prints the reuse line, returns nil (exit 0) and serves
// nothing (no banner, no listener).
func TestSecondStartReuses(t *testing.T) {
	base := freeBase(t, 3)
	db := "sqlite://" + filepath.Join(t.TempDir(), "weft.db")
	var first syncBuffer
	stop := startStudio(t, db, base, 3, loop(base), &first)
	defer stop()
	if !strings.HasPrefix(first.String(), "studio: http://"+loop(base)+"/\n") {
		t.Errorf("first start's banner %q, want it on %s", first.String(), loop(base))
	}

	var second strings.Builder
	if err := serve(db, want{addr: loop(base), span: 3}, "tok", &second); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if want := "studio already running at http://" + loop(base) + " (pid " + strconv.Itoa(os.Getpid()) + "), reusing\n"; second.String() != want {
		t.Errorf("second start printed %q, want exactly %q", second.String(), want)
	}
	if conn, err := net.DialTimeout("tcp", loop(base+1), 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Errorf("the reusing start listens on %s", loop(base+1))
	}
}

// TestForeignListenerMovesStudio: something that is not Studio on the
// wanted port moves Studio to the next one, with one line saying which
// and why, and the banner prints the real address.
func TestForeignListenerMovesStudio(t *testing.T) {
	base := freeBase(t, 2)
	foreign, err := net.Listen("tcp", loop(base))
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	go func() { _ = hs.Serve(foreign) }()
	defer func() { _ = hs.Close() }()

	var out syncBuffer
	stop := startStudio(t, "sqlite://"+filepath.Join(t.TempDir(), "weft.db"), base, 2, loop(base+1), &out)
	defer stop()
	lines := strings.Split(out.String(), "\n")
	if want := "studio: " + loop(base) + " is busy (not a weft Studio); listening on " + loop(base+1) + " instead (--addr pins one)"; lines[0] != want {
		t.Errorf("first line %q, want %q", lines[0], want)
	}
	if len(lines) < 2 || lines[1] != "studio: http://"+loop(base+1)+"/" {
		t.Errorf("banner %q, want the real address %s", out.String(), loop(base+1))
	}
}

// TestStudioOnAnotherDBMovesStudio: a Studio on another database holds
// the port — the next one is taken.
func TestStudioOnAnotherDBMovesStudio(t *testing.T) {
	base := freeBase(t, 2)
	var other syncBuffer
	stopOther := startStudio(t, "sqlite://"+filepath.Join(t.TempDir(), "other.db"), base, 1, loop(base), &other)
	defer func() { stopOther() }()

	ln, err := net.Listen("tcp", loop(base+1)) // free it again for the second start
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- serve("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), want{addr: loop(base), span: 2}, "tok", &out)
	}()
	// Both serve until SIGTERM: one signal stops both (each serveOn
	// is notified), so wait for the second to answer HTTP — its port
	// is bound before its signal handler is armed, its first answer
	// after — then stop.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+loop(base+1)+"/api/meta", nil)
		req.Header.Set("Authorization", "Bearer tok")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the second studio never served on the next port")
		}
	}
	stopOther()
	stopOther = func() {}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "studio: "+loop(base)+" is busy (a Studio on another database, ") {
		t.Errorf("stdout %q, want the move explained", out.String())
	}
}

// TestPinnedAddrBusyFails: --addr (and WEFT_STUDIO_ADDR) pins — a busy
// pinned address is an error naming it, no probe, no fallback.
func TestPinnedAddrBusyFails(t *testing.T) {
	base := freeBase(t, 2)
	occupied, err := net.Listen("tcp", loop(base))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	db := "sqlite://" + filepath.Join(t.TempDir(), "weft.db")
	var out strings.Builder
	err = run([]string{"--addr", loop(base), "--db", db, "--token", "tok"}, &out)
	if err == nil || !strings.Contains(err.Error(), loop(base)) {
		t.Errorf("--addr on a busy port: %v, want an error naming %s", err, loop(base))
	}
	t.Setenv("WEFT_STUDIO_ADDR", loop(base))
	err = run([]string{"--db", db, "--token", "tok"}, &out)
	if err == nil || !strings.Contains(err.Error(), loop(base)) {
		t.Errorf("WEFT_STUDIO_ADDR on a busy port: %v, want an error naming %s", err, loop(base))
	}
	if out.String() != "" {
		t.Errorf("a pinned busy start printed %q, want nothing (no banner, no move)", out.String())
	}
}

// TestRangeExhaustedFails: every port of the range busy is an error
// naming the range.
func TestRangeExhaustedFails(t *testing.T) {
	base := freeBase(t, 2)
	for _, p := range []int{base, base + 1} {
		ln, err := net.Listen("tcp", loop(p))
		if err != nil {
			t.Fatal(err)
		}
		hs := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
		go func() { _ = hs.Serve(ln) }()
		defer func() { _ = hs.Close() }()
	}
	err := serve("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), want{addr: loop(base), span: 2}, "tok", &strings.Builder{})
	if want := "every port in " + loop(base) + "–" + strconv.Itoa(base+1) + " is busy"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("range exhausted: %v, want %q", err, want)
	}
}

// TestAddrResolution pins the flag/env mirror: --addr, then
// WEFT_STUDIO_ADDR, both pinned; neither is the default, unpinned.
func TestAddrResolution(t *testing.T) {
	t.Setenv("WEFT_STUDIO_ADDR", "")
	if w := wantAddr("", defaultAddr); w.addr != "127.0.0.1:7331" || w.pinned {
		t.Errorf("default = %+v, want 127.0.0.1:7331 unpinned", w)
	}
	t.Setenv("WEFT_STUDIO_ADDR", "127.0.0.1:9000")
	if w := wantAddr("", defaultAddr); w.addr != "127.0.0.1:9000" || !w.pinned {
		t.Errorf("env = %+v, want 127.0.0.1:9000 pinned", w)
	}
	if w := wantAddr("127.0.0.1:9001", defaultAddr); w.addr != "127.0.0.1:9001" || !w.pinned {
		t.Errorf("flag = %+v, want 127.0.0.1:9001 pinned (over the env)", w)
	}
}

// TestDBFile pins the path the port policy compares with a running
// Studio's db.path: the file obsdb/sqlite opens for --db, absolute —
// "" (never a match) for a fileless or malformed --db.
func TestDBFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wd, err := os.Getwd() // dir as the process sees it (symlinks resolved)
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "abs", "weft.db")
	for _, c := range []struct {
		name, flag, env, want string
	}{
		{"relative sqlite://", "sqlite://weft.db", "", filepath.Join(wd, "weft.db")},
		{"relative sqlite:// in a directory", "sqlite://sub/../x/weft.db", "", filepath.Join(wd, "x", "weft.db")},
		{"absolute sqlite:///", "sqlite://" + abs, "", abs},
		{"sqlite:// wins over $WEFT_DB", "sqlite://" + abs, "env.db", abs},
		{"$WEFT_DB relative", "", "env.db", filepath.Join(wd, "env.db")},
		{"$WEFT_DB absolute", "", abs, abs},
		{"the default", "", "", filepath.Join(wd, ".weft", "weft.db")},
		{"sqlite://:memory:", "sqlite://:memory:", "", ""},
		{"$WEFT_DB=:memory:", "", ":memory:", ""},
		{"empty sqlite://", "sqlite://", "", ""},
		{"clickhouse://", "clickhouse://u:p@127.0.0.1:9000/db", "", ""},
		{"a bare path (not a --db form)", "weft.db", "", ""},
		{"malformed", "postgres://x", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("WEFT_DB", c.env)
			got, err := dbFile(c.flag)
			if err != nil || got != c.want {
				t.Errorf("dbFile(%q) with WEFT_DB=%q = %q, %v; want %q", c.flag, c.env, got, err, c.want)
			}
		})
	}
}

// TestRelativeDBFormsReuse: a Studio started with a relative
// --db sqlite://x.db is reused by a start in the same directory that
// names the same file through $WEFT_DB — the two forms resolve to one
// absolute path.
func TestRelativeDBFormsReuse(t *testing.T) {
	t.Chdir(t.TempDir())
	base := freeBase(t, 2)
	var first syncBuffer
	stop := startStudio(t, "sqlite://x.db", base, 2, loop(base), &first)
	defer stop()

	t.Setenv("WEFT_DB", "x.db")
	var second strings.Builder
	if err := serve("", want{addr: loop(base), span: 2}, "tok", &second); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if want := "studio already running at http://" + loop(base) + " (pid " + strconv.Itoa(os.Getpid()) + "), reusing\n"; second.String() != want {
		t.Errorf("second start printed %q, want %q", second.String(), want)
	}
}
