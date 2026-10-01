package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

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
