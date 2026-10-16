package studio

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestStatusFilterWords pins issue 003 (I3) on both routes and both
// backends: /api/runs and /api/search share one status parser —
// ?status=bogus is a 400 naming the allowed words on both, never an
// empty 200 — and the read-time words filter server-side: parked lists
// the contract's `parked` run (an open park) and not `resumed`'s parked
// run (decided: a resumer names it), cancelled lists `canceled`'s run.
// Paging and total describe the filtered set.
func TestStatusFilterWords(t *testing.T) {
	backends := map[string]func(t *testing.T) obsdb.DB{
		"sqlite": func(t *testing.T) obsdb.DB {
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "weft.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		},
	}
	if os.Getenv("WEFT_CLICKHOUSE_DSN") != "" {
		backends["clickhouse"] = freshClickHouse
	}
	for name, open := range backends {
		t.Run(name, func(t *testing.T) {
			serve := func(t *testing.T, fixture string) *httptest.Server {
				t.Helper()
				srv := New(DB(open(t)))
				ts := httptest.NewServer(srv.Handler())
				t.Cleanup(ts.Close)
				t.Cleanup(func() { _ = srv.Close() })
				for _, kind := range []string{"traces", "logs"} {
					b, err := os.ReadFile(filepath.Join("..", "obsdb", "testdata", "contract", fixture, kind+".json"))
					if err != nil {
						t.Fatal(err)
					}
					resp, err := http.Post(ts.URL+"/v1/"+kind, "application/json", bytes.NewReader(b))
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("ingest %s %s: %d %s", fixture, kind, resp.StatusCode, body)
					}
				}
				return ts
			}
			ids := func(t *testing.T, ts *httptest.Server, path string) ([]string, int) {
				t.Helper()
				var page struct {
					Total int `json:"total"`
					Runs  []struct {
						ID string `json:"id"`
					} `json:"runs"`
				}
				decode(t, fetchJSON(t, ts, path, nil), &page)
				var out []string
				for _, r := range page.Runs {
					out = append(out, r.ID)
				}
				return out, page.Total
			}
			const run = "a1100000000000000000000000000001"
			for _, c := range []struct {
				fixture, status string
				want            []string
			}{
				{"parked", "parked", []string{run}},
				{"parked", "succeeded", []string{run}},
				{"resumed", "parked", nil},
				{"canceled", "cancelled", []string{run}},
				{"canceled", "failed", []string{run}},
				{"failed", "cancelled", nil},
			} {
				t.Run(c.fixture+"/"+c.status, func(t *testing.T) {
					ts := serve(t, c.fixture)
					for _, route := range []string{"/api/runs?all=1&status=", "/api/search?all=1&status="} {
						got, total := ids(t, ts, route+c.status)
						if strings.Join(got, ",") != strings.Join(c.want, ",") || total != len(c.want) {
							t.Errorf("%s%s on %s = %v (total %d), want %v", route, c.status, c.fixture, got, total, c.want)
						}
					}
				})
			}
			t.Run("bogus", func(t *testing.T) {
				ts := serve(t, "parked")
				for _, route := range []string{"/api/runs?status=bogus", "/api/search?status=bogus", "/api/runs?status=completed"} {
					resp, err := http.Get(ts.URL + route)
					if err != nil {
						t.Fatal(err)
					}
					var doc struct {
						Error struct{ Code, Message string } `json:"error"`
					}
					_ = json.NewDecoder(resp.Body).Decode(&doc)
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusBadRequest || !strings.Contains(doc.Error.Message, "running, succeeded, failed, interrupted, parked or cancelled") {
						t.Errorf("GET %s = %d %q, want a 400 naming the allowed words", route, resp.StatusCode, doc.Error.Message)
					}
				}
			})
		})
	}
}

// freshClickHouse opens the ClickHouse backend on a brand-new database
// derived from WEFT_CLICKHOUSE_DSN, dropped when the test ends (the
// helper studio/ingest's tests use, copied: another test package).
func freshClickHouse(t *testing.T) obsdb.DB {
	t.Helper()
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_" + hex.EncodeToString(suffix)
	opts, err := ch.ParseDSN(base)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	admin, err := ch.Open(opts)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := admin.Exec(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u.Path = "/" + name
	db, err := clickhouse.Open(u.String())
	if err != nil {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", name))
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if err := admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
		_ = admin.Close()
	})
	return db
}
