package listen_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/internal/listen"
	"github.com/weftgo/weft/studio"
)

// freeBase returns a loopback address whose port and the n-1 ports
// after it were all free a moment ago: the tests never depend on 7331.
func freeBase(t testing.TB, n int) (host string, port int) {
	t.Helper()
	for range 50 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		held := []net.Listener{ln}
		ok := p+n-1 <= 65535
		for i := 1; ok && i < n; i++ {
			l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p+i))
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
			return "127.0.0.1", p
		}
	}
	t.Fatalf("no run of %d free ports found", n)
	return "", 0
}

func addr(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// occupy holds addr with a plain listener that accepts and never
// answers — not an HTTP server at all.
func occupy(t testing.TB, a string) {
	t.Helper()
	ln, err := net.Listen("tcp", a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

// serveStudio serves a Studio on a over path (token "" = none) and
// returns once it has answered GET /api/meta: a fresh Studio's first
// meta does one-time work (the embedded panel's version stamp is read
// on first use — past 500 ms under -race on a loaded CI runner), and
// the tests pin the port policy's verdicts, not a cold start's speed.
func serveStudio(t testing.TB, a, path, token string) {
	t.Helper()
	ln, err := net.Listen("tcp", a)
	if err != nil {
		t.Fatal(err)
	}
	opts := []studio.Option{studio.Base("/"), studio.Open(path)}
	if token != "" {
		opts = append(opts, studio.Token(token))
	}
	srv := studio.New(opts...)
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: time.Second}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close(); _ = srv.Close() })
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		req, err := http.NewRequest(http.MethodGet, "http://"+a+"/api/meta", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Studio on %s never answered /api/meta: %v", a, err)
		}
	}
}

// choose runs listen.Choose with r's Timeout, else ProbeTimeout: a
// probe that must hear a Studio's answer never races a short deadline.
// A test that waits a probe out (a listener that never answers) sets
// its own short Timeout.
func choose(t testing.TB, r listen.Request) (listen.Choice, error) {
	t.Helper()
	c, err := listen.Choose(context.Background(), r)
	if c.Listener != nil {
		t.Cleanup(func() { _ = c.Listener.Close() })
	}
	return c, err
}

// TestFreeAddressIsBound: the wanted address, free, is bound with no note.
func TestFreeAddressIsBound(t *testing.T) {
	host, port := freeBase(t, 1)
	c, err := choose(t, listen.Request{Addr: addr(host, port), Span: 2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Reuse || c.Listener == nil || c.Addr != addr(host, port) || c.Note != "" {
		t.Errorf("free address: %+v, want it bound with no note", c)
	}
}

// TestSameDBIsReused pins the reuse verdict: a Studio on the wanted
// address serving the same database file — read with the dev token,
// or on loopback with no token — is reused, nothing is bound, and the
// verdict names its URL and pid.
func TestSameDBIsReused(t *testing.T) {
	for _, token := range []string{"tok", ""} {
		t.Run("token="+token, func(t *testing.T) {
			host, port := freeBase(t, 2)
			path := filepath.Join(t.TempDir(), "weft.db")
			serveStudio(t, addr(host, port), path, token)
			c, err := choose(t, listen.Request{Addr: addr(host, port), Span: 2, DBPath: path, Token: token})
			if err != nil {
				t.Fatal(err)
			}
			if !c.Reuse || c.Listener != nil || c.Addr != addr(host, port) || c.PID != os.Getpid() {
				t.Fatalf("same db: %+v, want a reuse of %s with pid %d", c, addr(host, port), os.Getpid())
			}
			want := "studio already running at http://" + addr(host, port) + " (pid " + strconv.Itoa(os.Getpid()) + "), reusing"
			if c.ReuseLine() != want {
				t.Errorf("reuse line %q, want %q", c.ReuseLine(), want)
			}
		})
	}
}

// TestBusyAddressMovesOn pins each "not this Studio" case: the next
// port is bound and the note names the skipped address and why.
func TestBusyAddressMovesOn(t *testing.T) {
	for _, c := range []struct {
		name   string
		hold   func(t *testing.T, a string)
		req    listen.Request
		reason string
	}{
		{"a foreign listener", func(t *testing.T, a string) { occupy(t, a) },
			listen.Request{DBPath: "/x/weft.db", Timeout: 200 * time.Millisecond}, "not a weft Studio"},
		{"a foreign HTTP server", func(t *testing.T, a string) {
			ln, err := net.Listen("tcp", a)
			if err != nil {
				t.Fatal(err)
			}
			hs := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
			go func() { _ = hs.Serve(ln) }()
			t.Cleanup(func() { _ = hs.Close() })
		}, listen.Request{DBPath: "/x/weft.db"}, "not a weft Studio"},
		{"a Studio on another database", func(t *testing.T, a string) {
			serveStudio(t, a, filepath.Join(t.TempDir(), "other.db"), "tok")
		}, listen.Request{DBPath: "/x/weft.db", Token: "tok"}, "a Studio on another database, "},
		{"a Studio with another token", func(t *testing.T, a string) {
			serveStudio(t, a, filepath.Join(t.TempDir(), "weft.db"), "theirs")
		}, listen.Request{DBPath: "/x/weft.db", Token: "mine"}, "a Studio with another token"},
		{"a Studio that requires a token", func(t *testing.T, a string) {
			serveStudio(t, a, filepath.Join(t.TempDir(), "weft.db"), "theirs")
		}, listen.Request{DBPath: "/x/weft.db"}, "set WEFT_STUDIO_TOKEN"},
		{"a fileless command database never matches", func(t *testing.T, a string) {
			serveStudio(t, a, filepath.Join(t.TempDir(), "weft.db"), "tok")
		}, listen.Request{Token: "tok"}, "a Studio on another database"},
	} {
		t.Run(c.name, func(t *testing.T) {
			host, port := freeBase(t, 2)
			c.hold(t, addr(host, port))
			r := c.req
			r.Addr, r.Span = addr(host, port), 2
			got, err := choose(t, r)
			if err != nil {
				t.Fatal(err)
			}
			next := addr(host, port+1)
			if got.Reuse || got.Listener == nil || got.Addr != next {
				t.Fatalf("%+v, want %s bound", got, next)
			}
			if !strings.HasPrefix(got.Note, addr(host, port)+" is busy (") || !strings.Contains(got.Note, c.reason) ||
				!strings.HasSuffix(got.Note, "listening on "+next+" instead (--addr pins one)") || strings.Contains(got.Note, "\n") {
				t.Errorf("note %q, want one line: %s busy (%s…), listening on %s", got.Note, addr(host, port), c.reason, next)
			}
		})
	}
}

// TestPinnedBusyIsAnError: a pinned address is never probed or moved
// off: busy is an error naming it.
func TestPinnedBusyIsAnError(t *testing.T) {
	host, port := freeBase(t, 2)
	path := filepath.Join(t.TempDir(), "weft.db")
	// Even the same Studio is not reused: a pin is a pin.
	serveStudio(t, addr(host, port), path, "tok")
	c, err := choose(t, listen.Request{Addr: addr(host, port), Pinned: true, Span: 2, DBPath: path, Token: "tok"})
	if err == nil || !strings.Contains(err.Error(), addr(host, port)) || !strings.Contains(err.Error(), "pinned") || c.Listener != nil || c.Reuse {
		t.Errorf("pinned busy: %+v, %v; want an error naming %s", c, err, addr(host, port))
	}
}

// TestRangeExhausted: every port of the span busy is an error naming
// the range.
func TestRangeExhausted(t *testing.T) {
	host, port := freeBase(t, 2)
	occupy(t, addr(host, port))
	occupy(t, addr(host, port+1))
	_, err := choose(t, listen.Request{Addr: addr(host, port), Span: 2, Timeout: 100 * time.Millisecond})
	want := "every port in " + addr(host, port) + "–" + strconv.Itoa(port+1) + " is busy"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("range exhausted: %v, want %q", err, want)
	}
}

// TestSameFileThroughASymlink: db.path and the command's path name one
// file through a symlink (macOS's /tmp): reused.
func TestSameFileThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "real", "weft.db")
	link := filepath.Join(dir, "link")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(path), link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	host, port := freeBase(t, 2)
	serveStudio(t, addr(host, port), path, "tok")
	c, err := choose(t, listen.Request{Addr: addr(host, port), Span: 2, DBPath: filepath.Join(link, "weft.db"), Token: "tok"})
	if err != nil || !c.Reuse {
		t.Errorf("symlinked db path: %+v, %v; want a reuse", c, err)
	}
}

// TestFixedTokenBeatsTokenFor: a fixed Token is always the probe's
// bearer — TokenFor is never consulted then — and with no Token,
// TokenFor's answer is sent.
func TestFixedTokenBeatsTokenFor(t *testing.T) {
	for _, c := range []struct{ token, want string }{{"fixed", "Bearer fixed"}, {"", "Bearer stable"}} {
		host, port := freeBase(t, 2)
		ln, err := net.Listen("tcp", addr(host, port))
		if err != nil {
			t.Fatal(err)
		}
		got := make(chan string, 4)
		hs := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- r.Header.Get("Authorization")
			http.NotFound(w, r)
		})}
		go func() { _ = hs.Serve(ln) }()
		asked := false
		_, err = choose(t, listen.Request{Addr: addr(host, port), Span: 2, Token: c.token,
			TokenFor: func(string) string { asked = true; return "stable" }})
		_ = hs.Close()
		if err != nil {
			t.Fatal(err)
		}
		if a := <-got; a != c.want {
			t.Errorf("Token %q: the probe sent %q, want %q", c.token, a, c.want)
		}
		if c.token != "" && asked {
			t.Errorf("Token %q: TokenFor was consulted", c.token)
		}
	}
}

// TestKnownStudio (B7, UX-S5a-13): a Studio the discovery file names
// (Request.Known) is asked first, whatever its port: the same database
// there is the reuse verdict even with the wanted address free, pinned
// or not; a Studio there on another database leaves the policy as it
// was — the wanted address is bound.
func TestKnownStudio(t *testing.T) {
	host, port := freeBase(t, 2)
	known := addr(host, port+1)
	dir := t.TempDir()
	db := filepath.Join(dir, "weft.db")
	serveStudio(t, known, db, "")
	for _, pinned := range []bool{false, true} {
		c, err := listen.Choose(context.Background(), listen.Request{Addr: addr(host, port), Pinned: pinned, DBPath: db, Known: known, Timeout: 5 * time.Second})
		if err != nil || !c.Reuse || c.Addr != known || c.PID != os.Getpid() || c.Listener != nil {
			t.Errorf("pinned %v: same database at the known address = %+v %v, want its reuse", pinned, c, err)
		}
	}
	c, err := listen.Choose(context.Background(), listen.Request{Addr: addr(host, port), DBPath: filepath.Join(dir, "other.db"), Known: known, Timeout: 5 * time.Second})
	if err != nil || c.Reuse || c.Addr != addr(host, port) || c.Listener == nil {
		t.Fatalf("another database = %+v %v, want the wanted address bound", c, err)
	}
	_ = c.Listener.Close()
	// A known address nothing answers on is no verdict either.
	c, err = listen.Choose(context.Background(), listen.Request{Addr: addr(host, port), DBPath: db, Known: "127.0.0.1:1", Timeout: 5 * time.Second})
	if err != nil || c.Reuse || c.Listener == nil {
		t.Fatalf("a dead known address = %+v %v, want the wanted address bound", c, err)
	}
	_ = c.Listener.Close()
}
