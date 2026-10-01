package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
)

// TestNewServer pins the flag surface: the default database, sqlite://
// paths, clickhouse:// reaching the driver, and the token wall.
func TestNewServer(t *testing.T) {
	dir := t.TempDir()

	// The default: the local sink path, honored through $WEFT_DB.
	t.Setenv("WEFT_DB", dir+"/env.db")
	srv, err := newServer("", "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	assertSetupB(t, srv.Handler(), "tok", dir+"/env.db")

	// sqlite:// picks the file and creates it.
	path := dir + "/sub" + "/explicit.db"
	srv2, err := newServer("sqlite://"+path, "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv2.Close() }()
	assertSetupB(t, srv2.Handler(), "tok", path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("sqlite:// did not create %s: %v", path, err)
	}

	// clickhouse:// reaches the driver: a dead port is the driver's
	// dial error, never a deferral. Anything else is a usage error.
	if _, err := newServer("clickhouse://127.0.0.1:1/default", "tok"); err == nil ||
		strings.Contains(err.Error(), "6b") {
		t.Errorf("clickhouse dead port: %v, want the driver's dial error", err)
	}
	for _, flag := range []string{"postgres://x", "weird"} {
		if _, err := newServer(flag, "tok"); err == nil || !strings.Contains(err.Error(), "--db must be") {
			t.Errorf("%s: %v, want a usage error naming the two forms", flag, err)
		}
	}
}

// TestNewServerClickhouse pins the --db clickhouse:// wiring against a
// live server (WEFT_CLICKHOUSE_DSN; obsdb/clickhouse's README has the
// container recipe): the flag opens the hosted backend on a fresh
// database and api/meta names it — the same dance the clickhouse
// conformance tests use.
func TestNewServerClickhouse(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: --db clickhouse:// needs a server (obsdb/clickhouse README has the recipe)")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "studio_cmd_" + hex.EncodeToString(suffix)
	adminOpts, err := ch.ParseDSN(base)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	admin, err := ch.Open(adminOpts)
	if err != nil {
		t.Fatalf("connect %s: %v", base, err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.Exec(context.Background(),
		fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u.Path = "/" + name
	srv, err := newServer(u.String(), "tok")
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		_ = admin.Exec(context.Background(),
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
	})

	// Setup B on the wire, the hosted backend underneath: the token
	// wall, ingest on, and api/meta naming clickhouse.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	get := func(path, bearer string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(b)
	}
	if code, body := get("/api/meta", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous meta: %d %s", code, body)
	}
	code, body := get("/api/meta", "tok")
	if code != http.StatusOK || !strings.Contains(body, `"db":"clickhouse"`) ||
		!strings.Contains(body, `"ingest_open":true`) || !strings.Contains(body, `"auth"`) {
		t.Errorf("meta: %d %s", code, body)
	}
	if code, body := get("/", ""); code != http.StatusOK || !strings.Contains(body, `<base href="/">`) {
		t.Errorf("shell: %d %.80s", code, body)
	}
}

func assertSetupB(t *testing.T, h http.Handler, token, dbPath string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	get := func(path, bearer string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(b)
	}
	// Setup B on the wire: the API is token-walled, sqlite, ingest on.
	code, body := get("/api/meta", "")
	if code != http.StatusUnauthorized {
		t.Errorf("anonymous meta: %d %s", code, body)
	}
	code, body = get("/api/meta", token)
	if code != http.StatusOK || !strings.Contains(body, `"db":"sqlite"`) ||
		!strings.Contains(body, `"ingest_open":true`) || !strings.Contains(body, `"auth"`) {
		t.Errorf("meta: %d %s", code, body)
	}
	// The UI is served at the root (Base /).
	code, body = get("/", "")
	if code != http.StatusOK || !strings.Contains(body, `<base href="/">`) {
		t.Errorf("shell: %d %.80s", code, body)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database %s not opened: %v", dbPath, err)
	}
}

// TestTokenPrecedence pins --token > WEFT_STUDIO_TOKEN > generated.
func TestTokenPrecedence(t *testing.T) {
	t.Setenv("WEFT_STUDIO_TOKEN", "from-env")
	if got := srvToken(""); got != "from-env" {
		t.Errorf("env token: %q", got)
	}
	if got := srvToken("from-flag"); got != "from-flag" {
		t.Errorf("flag token: %q", got)
	}
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	if got := srvToken(""); got == "" || len(got) < 20 {
		t.Errorf("generated token: %q", got)
	}
	if a, b := srvToken(""), srvToken(""); a == b {
		t.Errorf("generated token repeats: %q", a)
	}
}

// TestBootBannerTokenAuthenticates pins the P0 the programme audit
// found: with no --token and no WEFT_STUDIO_TOKEN the banner's dev
// token must open the API of the very server that printed it. The
// token is resolved once in serveBoot and shared by the wall and the
// banner; the pre-fix code drew srvToken twice and the printed token
// 401'd against /api/meta, breaking setup B's documented hand-off.
func TestBootBannerTokenAuthenticates(t *testing.T) {
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DB", t.TempDir()+"/boot.db")
	var banner strings.Builder
	srv, err := serveBoot("", "127.0.0.1:7331", "", &banner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	// The banner's "dev token <tok>" line names the one token.
	var token string
	for _, line := range strings.Split(banner.String(), "\n") {
		if rest, ok := strings.CutPrefix(line, "studio: dev token "); ok {
			token = strings.TrimSpace(strings.TrimSuffix(rest, "(WEFT_STUDIO_TOKEN fixes it)"))
		}
	}
	if token == "" {
		t.Fatalf("banner carries no dev token:\n%s", banner.String())
	}

	// That token authenticates against this server's API.
	assertSetupB(t, srv.Handler(), token, os.Getenv("WEFT_DB"))

	// And the banner is printed exactly once — one resolution, not a
	// second draw for the wall.
	if n := strings.Count(banner.String(), "dev token"); n != 1 {
		t.Errorf("banner names the dev token %d times, want 1:\n%s", n, banner.String())
	}
}

// TestDBLabelMasksPassword pins the banner's credential rule: a DSN's
// password never reaches stdout (the audit's P2-20), while the host
// and database — the useful part — stay readable.
func TestDBLabelMasksPassword(t *testing.T) {
	if got := dbLabel("clickhouse://default:hunter2@127.0.0.1:9000/weft?secure=1"); got !=
		"clickhouse://default:***@127.0.0.1:9000/weft?secure=1" {
		t.Errorf("dbLabel = %q, want the masked DSN", got)
	}
	// A DSN without a password, and the sqlite/default forms, unchanged.
	for dsn, want := range map[string]string{
		"clickhouse://default@127.0.0.1:9000/weft": "clickhouse://default@127.0.0.1:9000/weft",
		"sqlite:///tmp/weft.db":                    "/tmp/weft.db",
		"":                                         "$WEFT_DB or ./.weft/weft.db (default)",
	} {
		if got := dbLabel(dsn); got != want {
			t.Errorf("dbLabel(%q) = %q, want %q", dsn, got, want)
		}
	}
}

// TestListenShutsDownGracefully pins the P2-20 half serve()'s old bare
// ListenAndServe missed: SIGTERM stops the listener and listen returns
// nil (serve then closes the studio's resources) — the process can
// exit on a signal at all, and the port actually closes.
func TestListenShutsDownGracefully(t *testing.T) {
	// A free port, proven by binding and letting go of it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	httpSrv := &http.Server{Addr: addr, Handler: http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })}
	done := make(chan error, 1)
	go func() { done <- listen(httpSrv, io.Discard) }()

	// The server came up; then SIGTERM (to ourselves — listen's
	// signal.Notify catches it before the default disposition).
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never listened on %s: %v", addr, err)
		}
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listen after SIGTERM: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listen never returned after SIGTERM")
	}
	if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Error("the port still accepts after the graceful shutdown")
	}
}
