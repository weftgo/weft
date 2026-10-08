package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The second audit pass's pins (2026-10-02).

// TestHTTPServerTimeouts: the server was built with no timeouts at
// all — a client that opens a connection and never finishes its
// request headers held it forever (slowloris). The header read and
// idle keep-alives are bounded; the write side is deliberately not
// (/api/live and the runtime's command stream are long-lived; they
// carry their own per-frame write deadlines).
func TestHTTPServerTimeouts(t *testing.T) {
	srv := httpServer("127.0.0.1:0", nil)
	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout is unset: a slow header read holds a connection forever")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout is unset")
	}
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Errorf("WriteTimeout %v / ReadTimeout %v would cut the SSE streams and slow ingest uploads", srv.WriteTimeout, srv.ReadTimeout)
	}
}

// TestBannerDoesNotPrintAFixedToken: the banner printed whatever token
// was in force as "dev token" — including one the operator fixed
// through WEFT_STUDIO_TOKEN or --token, which is also the panel
// tokens' signing key (S4.6 setup C) and has no business in a log.
// Only a generated dev token is printed: nobody knows it otherwise.
func TestBannerDoesNotPrintAFixedToken(t *testing.T) {
	t.Setenv("WEFT_DB", t.TempDir()+"/banner.db")
	for name, set := range map[string]func() string{
		"env":  func() string { t.Setenv("WEFT_STUDIO_TOKEN", "s3cr3t-signing-key"); return "" },
		"flag": func() string { t.Setenv("WEFT_STUDIO_TOKEN", ""); return "s3cr3t-signing-key" },
	} {
		var banner strings.Builder
		srv, err := serveBoot("", "127.0.0.1:7331", set(), &banner)
		if err != nil {
			t.Fatal(err)
		}
		_ = srv.Close()
		if strings.Contains(banner.String(), "s3cr3t-signing-key") {
			t.Errorf("%s: the banner prints the configured token:\n%s", name, banner.String())
		}
		if !strings.Contains(banner.String(), "studio: token from ") {
			t.Errorf("%s: the banner does not say where the token comes from:\n%s", name, banner.String())
		}
	}
}

// TestBadDatabaseIsAnErrorNotAPanic: studio.New panics when it cannot
// open its database (a construction error, for a library). The binary
// turned a mistyped --db path into a goroutine dump instead of
// "studio: …" and exit 1.
func TestBadDatabaseIsAnErrorNotAPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("newServer panicked: %v", r)
		}
	}()
	// /dev/null is not a directory: the file cannot be created under it.
	srv, err := newServer("sqlite:///dev/null/nope/weft.db", "tok")
	if err == nil {
		_ = srv.Close()
		t.Fatal("an unopenable database returned a server")
	}
	if !strings.Contains(err.Error(), "/dev/null/nope/weft.db") {
		t.Errorf("err = %v, want it to name the path", err)
	}
	// An empty path is a usage error, not a silent fall-back to the
	// default file.
	if _, err := newServer("sqlite://", "tok"); err == nil || !strings.Contains(err.Error(), "--db") {
		t.Errorf("sqlite:// with no path: %v, want a usage error", err)
	}
}

// TestBannerLinkCarriesAGeneratedToken: the UI adopts a token from the
// page URL's fragment, so the banner of a server that drew its own dev
// token prints a link that opens the UI already authenticated. The
// fragment never reaches a server or a Referer. A token the operator
// fixed is not printed, in the link either.
func TestBannerLinkCarriesAGeneratedToken(t *testing.T) {
	t.Setenv("WEFT_DB", t.TempDir()+"/link.db")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	var banner strings.Builder
	srv, err := serveBoot("", "127.0.0.1:7331", "", &banner)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.Close()
	var token string
	for _, line := range strings.Split(banner.String(), "\n") {
		if rest, ok := strings.CutPrefix(line, "studio: dev token "); ok {
			token = strings.TrimSpace(strings.TrimSuffix(rest, "(WEFT_STUDIO_TOKEN fixes it)"))
		}
	}
	if token == "" || !strings.Contains(banner.String(), "studio: http://127.0.0.1:7331/#token="+token+"\n") {
		t.Errorf("the banner's link does not carry the dev token %q:\n%s", token, banner.String())
	}

	banner.Reset()
	srv, err = serveBoot("", "127.0.0.1:7331", "fixed-by-the-operator", &banner)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.Close()
	if strings.Contains(banner.String(), "#token=") || !strings.Contains(banner.String(), "studio: http://127.0.0.1:7331/\n") {
		t.Errorf("a fixed token's banner:\n%s", banner.String())
	}
}

// ── the third pass (2026-10-02, second review) ──────────────────────

// TestShutdownEndsOpenStreams: http.Server.Shutdown waits for active
// connections to go idle and never cancels a request's context, so an
// open /api/live tab (or a connected runtime's command stream) held
// every stop for the full five-second grace window before the
// force-close — "an SSE stream ends when its request context cancels"
// was never true. The streams now see the shutdown and end at once.
func TestShutdownEndsOpenStreams(t *testing.T) {
	t.Setenv("WEFT_DB", t.TempDir()+"/stop.db")
	srv, err := serveBoot("", "127.0.0.1:0", "tok", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	done := make(chan error, 1)
	go func() { done <- serveOn(httpServer(addr, srv.Handler()), l, io.Discard) }()

	var stream *http.Response
	for deadline := time.Now().Add(5 * time.Second); ; {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/live?agent=support", nil)
		req.Header.Set("Authorization", "Bearer tok")
		stream, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never came up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = stream.Body.Close() }()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("live = %d", stream.StatusCode)
	}

	start := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listen = %v", err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("shutdown with an open stream took %v: it waited out the grace window", took)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("listen never returned")
	}
}

// TestBannerMasksAPasswordWithAnAt: a DSN's userinfo ends at its last
// '@' (url.Parse's rule, which the clickhouse driver uses), so a
// password holding one — clickhouse://u:p@ss@host/db — is a valid DSN.
// The mask cut at the first '@' and printed the password's tail as the
// host.
func TestBannerMasksAPasswordWithAnAt(t *testing.T) {
	got := maskDSN("clickhouse://default:s3cr@t@127.0.0.1:19000/default")
	if strings.Contains(got, "t@127") || strings.Contains(got, "s3cr") {
		t.Errorf("maskDSN leaks the password: %s", got)
	}
	if got != "clickhouse://default:***@127.0.0.1:19000/default" {
		t.Errorf("maskDSN = %s", got)
	}
}
