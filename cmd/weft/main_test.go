package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/internal/doctor"
	"github.com/weftgo/weft/version"
)

// TestVersion pins `weft version` (and --version, the old studio
// binary's flag): the one version (version.Runtime) and a newline on
// stdout, exit 0, and no server.
func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var out, errb strings.Builder
		if code := run(args, &out, &errb); code != 0 {
			t.Fatalf("weft %v = exit %d: %s", args, code, errb.String())
		}
		if want := version.Runtime() + "\n"; out.String() != want {
			t.Errorf("weft %v printed %q, want %q", args, out.String(), want)
		}
	}
}

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
	if code != http.StatusOK || !strings.Contains(body, `"db":{"kind":"clickhouse"`) ||
		!strings.Contains(body, `"ingest_open":true`) || !strings.Contains(body, `"auth"`) {
		t.Errorf("meta: %d %s", code, body)
	}
	if code, body := get("/", ""); code != http.StatusOK || !strings.Contains(body, `<base href="/">`) {
		t.Errorf("shell: %d %.80s", code, body)
	}

	// The binary opened this handle, so the binary closes it: studio's
	// own Close leaves a DB(...) handle to its owner, and the shutdown
	// path used to leave the ClickHouse connections open.
	if code, body := get("/api/runs", "tok"); code != http.StatusOK {
		t.Fatalf("runs before Close: %d %s", code, body)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if code, body := get("/api/runs", "tok"); code != http.StatusInternalServerError {
		t.Errorf("runs after Close: %d %s, want 500 (the handle is closed)", code, body)
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
	if code != http.StatusOK || !strings.Contains(body, `"db":{"kind":"sqlite"`) ||
		!strings.Contains(body, `"ingest_open":true`) || !strings.Contains(body, `"auth"`) {
		t.Errorf("meta: %d %s", code, body)
	}
	// The UI is served at the root (Base /).
	code, body = get("/", "")
	if code != http.StatusOK || !strings.Contains(body, `<base href="/">`) {
		t.Errorf("shell: %d %.80s", code, body)
	}
	if dbPath == "" {
		return // a database with no file
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database %s not opened: %v", dbPath, err)
	}
}

// TestTokenPrecedence pins --token > WEFT_STUDIO_TOKEN > the
// database's stable token (plan B3) > generated (a database with no
// file).
func TestTokenPrecedence(t *testing.T) {
	db := "sqlite://" + filepath.Join(t.TempDir(), "p.db")
	resolve := func(dbFlag, flag string) resolvedToken {
		t.Helper()
		tok, err := resolveToken(dbFlag, flag, false)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	t.Setenv("WEFT_STUDIO_TOKEN", "from-env")
	if got := resolve(db, ""); got.value != "from-env" || got.source != tokenFromEnv {
		t.Errorf("env token: %+v", got)
	}
	if got := resolve(db, "from-flag"); got.value != "from-flag" || got.source != tokenFromFlag {
		t.Errorf("flag token: %+v", got)
	}
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	stable := resolve(db, "")
	if stable.source != tokenFromFile || len(stable.value) < 40 || !stable.created || stable.printable() {
		t.Errorf("stable token: %+v", stable)
	}
	if again := resolve(db, ""); again.value != stable.value || again.created {
		t.Errorf("the stable token moved: %+v, then %+v", stable, again)
	}
	mem := "sqlite://:memory:"
	if got := resolve(mem, ""); got.value == "" || len(got.value) < 20 || !got.printable() {
		t.Errorf("generated token: %+v", got)
	}
	if a, b := resolve(mem, ""), resolve(mem, ""); a.value == b.value {
		t.Errorf("generated token repeats: %q", a.value)
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
	t.Setenv("WEFT_DB", ":memory:") // no file: a token generated for this process
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
	assertSetupB(t, srv.Handler(), token, "")

	// And the banner is printed exactly once — one resolution, not a
	// second draw for the wall.
	if n := strings.Count(banner.String(), "dev token"); n != 1 {
		t.Errorf("banner names the dev token %d times, want 1:\n%s", n, banner.String())
	}
}

// TestStableTokenAuthenticatesUnprinted: on a database file the token
// is the one in <db>.token — it opens the API, and the banner never
// prints it (it is the panel tokens' signing key), naming the file
// instead.
func TestStableTokenAuthenticatesUnprinted(t *testing.T) {
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	db := filepath.Join(t.TempDir(), "boot.db")
	t.Setenv("WEFT_DB", db)
	var banner strings.Builder
	srv, err := serveBoot("", "127.0.0.1:7331", "", &banner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	b, err := os.ReadFile(db + ".token")
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(db + ".token"); fi.Mode().Perm() != 0o600 {
		t.Errorf("token file mode %v, want 0600", fi.Mode().Perm())
	}
	token := strings.TrimSpace(string(b))
	assertSetupB(t, srv.Handler(), token, db)
	if strings.Contains(banner.String(), token) || strings.Contains(banner.String(), "#token=") {
		t.Errorf("the banner prints the stable token:\n%s", banner.String())
	}
	if want := "studio: token created in " + db + ".token"; !strings.Contains(banner.String(), want) {
		t.Errorf("banner %q, want it to name the file (%q)", banner.String(), want)
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
	skipWithoutSelfSignal(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	httpSrv := &http.Server{Addr: addr, Handler: http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })}
	done := make(chan error, 1)
	go func() { done <- serveOn(httpSrv, ln, io.Discard) }()

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
	signalSelf(t, syscall.SIGTERM)
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

// TestDoctorSubcommand pins `weft doctor`'s wiring (internal/doctor
// is the checking): --url picks the Studio, an unreachable one is the
// first line and exit 1 without a second message, and WEFT_STUDIO_URL /
// WEFT_STUDIO_TOKEN are the flags' defaults.
func TestDoctorSubcommand(t *testing.T) {
	var out, errb strings.Builder
	code := run([]string{"doctor", "--url", "http://127.0.0.1:1"}, &out, &errb)
	if code != 1 || !strings.HasPrefix(out.String(), "studio not reachable at http://127.0.0.1:1: ") || errb.String() != "" {
		t.Errorf("unreachable: exit %d, stderr %q\n%s", code, errb.String(), out.String())
	}

	srv, err := newServer("sqlite://"+t.TempDir()+"/weft.db", "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	t.Setenv("WEFT_STUDIO_URL", ts.URL)
	t.Setenv("WEFT_STUDIO_TOKEN", "tok")
	t.Setenv("WEFT_ENV", "")
	out.Reset()
	if code := run([]string{"doctor"}, &out, &errb); code != 0 {
		t.Fatalf("doctor from the env: exit %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "ok   token     accepted") || !strings.Contains(out.String(), "WEFT_ENV is unset") {
		t.Errorf("doctor from the env:\n%s", out.String())
	}
	errb.Reset()
	if code := run([]string{"doctor", "extra"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "doctor takes no arguments") {
		t.Errorf("doctor with an argument: exit %d, stderr %q, want 2 and a usage line", code, errb.String())
	}
	// A doctor error must not be mistaken for ErrUnhealthy's silence.
	if !errors.Is(doctor.Run(context.Background(), io.Discard, "http://127.0.0.1:1", "", os.Getenv), doctor.ErrUnhealthy) {
		t.Error("doctor.Run on an unreachable Studio is not ErrUnhealthy")
	}
}

// TestManifestFlag pins --manifest (plan B5 review) and the upward
// search (plan B1): the flag, else WEFT_MANIFEST, else the nearest
// weft.json from the working directory upward, read once at start into
// studio.Manifest — the flag wins; an unreadable named file is a start
// error naming the flag; the note says which file was loaded, or that
// none was found.
func TestManifestFlag(t *testing.T) {
	dir := t.TempDir()
	flagFile, envFile := dir+"/flag.json", dir+"/env.json"
	if err := os.WriteFile(flagFile, []byte(`{"weft":1,"agents":[{"name":"from-flag"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte(`{"weft":1,"agents":[{"name":"from-env"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The upward search starts in a directory that has no weft.json; its
	// grandparent has one.
	tree := t.TempDir()
	deep := filepath.Join(tree, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	found := filepath.Join(tree, "weft.json")
	if err := os.WriteFile(found, []byte(`{"weft":1,"agents":[{"name":"found-upward"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir() // nothing above a temp dir names weft.json
	served := func(flagPath, wd string) (string, string) {
		t.Helper()
		opts, note, err := manifestOptions(flagPath, wd)
		if err != nil {
			t.Fatal(err)
		}
		srv, err := newServer("sqlite://"+t.TempDir()+"/weft.db", "tok", opts...)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = srv.Close() }()
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/manifest", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("%d %s", resp.StatusCode, b), note
	}
	t.Setenv("WEFT_MANIFEST", "")
	if findUpward(empty, "weft.json") != "" {
		t.Skip("a weft.json above the temp directory: the no-manifest case cannot be pinned here")
	}
	if got, note := served("", empty); !strings.HasPrefix(got, "404") ||
		note != "studio: no weft.json from "+empty+" upward; no manifest (--manifest names one)" {
		t.Errorf("no flag, no env, none upward: %s (note %q), want 404 (no manifest) and the note", got, note)
	}
	if got, note := served("", deep); !strings.Contains(got, "found-upward") || note != "studio: manifest "+found+" (found upward)" {
		t.Errorf("upward: %s (note %q), want the grandparent's weft.json", got, note)
	}
	t.Setenv("WEFT_MANIFEST", envFile)
	if got, note := served("", deep); !strings.Contains(got, "from-env") || note != "studio: manifest "+envFile+" (WEFT_MANIFEST)" {
		t.Errorf("WEFT_MANIFEST over the upward search: %s (note %q)", got, note)
	}
	if got, note := served(flagFile, deep); !strings.Contains(got, "from-flag") || note != "studio: manifest "+flagFile+" (--manifest)" {
		t.Errorf("--manifest beside WEFT_MANIFEST: %s (note %q), want the flag's file", got, note)
	}
	// A file that does not parse is a start error naming it, named or
	// found upward — never served verbatim.
	bad := filepath.Join(t.TempDir(), "weft.json")
	if err := os.WriteFile(bad, []byte(`{"weft":1,"agents":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEFT_MANIFEST", "")
	if _, _, err := manifestOptions(bad, empty); err == nil || !strings.HasPrefix(err.Error(), "manifest "+bad+": does not parse: ") {
		t.Errorf("a malformed --manifest: %v, want \"manifest %s: does not parse: …\"", err, bad)
	}
	if _, _, err := manifestOptions("", filepath.Dir(bad)); err == nil || !strings.HasPrefix(err.Error(), "manifest "+bad+": does not parse: ") {
		t.Errorf("a malformed weft.json found upward: %v", err)
	}
	if err := os.WriteFile(bad, []byte(`[1,2]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manifestOptions(bad, empty); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("a manifest that is not an object: %v", err)
	}
	var errb strings.Builder
	if code := run([]string{"studio", "--manifest", dir + "/missing.json"}, io.Discard, &errb); code != 1 || !strings.Contains(errb.String(), "--manifest") {
		t.Errorf("missing manifest: exit %d, stderr %q, want 1 and a start error naming --manifest", code, errb.String())
	}
}
