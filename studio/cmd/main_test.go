package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestNewServer pins the flag surface: the default database, sqlite://
// paths, the clickhouse deferral to step 6b, and the token wall.
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

	// clickhouse:// and anything else are errors naming step 6b.
	for _, flag := range []string{"clickhouse://host:9444", "postgres://x", "weird"} {
		if _, err := newServer(flag, "tok"); err == nil || !strings.Contains(err.Error(), "6b") {
			t.Errorf("%s: %v, want a step-6b deferral", flag, err)
		}
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
