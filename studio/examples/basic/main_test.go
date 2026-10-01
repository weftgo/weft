package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb/sqlite"
)

// The example's acceptance shape (plan §7): record the demo runs into
// an in-memory database, serve the handler over httptest, and both the
// API and the shell answer — a deep link gets the shell with the base
// rewrite.
func TestServe(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := record(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler(db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/studio/api/runs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api/runs: %d", resp.StatusCode)
	}
	for _, want := range []string{`"total":3`, `"status":"failed"`, `"status":"succeeded"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("api/runs missing %s: %s", want, body)
		}
	}

	page, err := http.Get(srv.URL + "/studio/runs/whatever-deep-link")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = page.Body.Close() }()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("deep link: %d", page.StatusCode)
	}
	shell, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shell), `<base href="/studio/">`) {
		t.Error("deep link does not serve the shell with the base rewrite")
	}
}
