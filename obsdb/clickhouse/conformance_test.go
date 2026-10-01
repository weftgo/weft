package clickhouse_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/obsdb/obsdbtest"
)

// The conformance table (S3.5) runs against a live server, gated on
// WEFT_CLICKHOUSE_DSN (S3.6). Each subtest gets a fresh database: the
// helper creates one from the DSN's server, opens the backend on it,
// and drops it on cleanup. See the README for the container recipe.
func TestConformance(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the conformance table needs a clickhouse server (README has the one-line container recipe)")
	}
	obsdbtest.Run(t, func(t *testing.T) obsdb.DB {
		db, _ := openFresh(t)
		return db
	})
}

// openFresh opens the backend on a brand-new database derived from
// WEFT_CLICKHOUSE_DSN, dropping it when the test ends. It returns the
// backend and the DSN of the fresh database (raw-connection tests use
// it to talk to the same database as a stock collector would).
func openFresh(t *testing.T, opts ...clickhouse.Option) (obsdb.DB, string) {
	t.Helper()
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the container recipe)")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_" + hex.EncodeToString(suffix)
	admin := openRaw(t, base)
	if err := admin.Exec(context.Background(),
		fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u.Path = "/" + name
	fresh := u.String()
	db, err := clickhouse.Open(fresh, opts...)
	if err != nil {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", name))
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if err := admin.Exec(context.Background(),
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
		_ = admin.Close()
	})
	return db, fresh
}

func openRaw(t *testing.T, dsn string) ch.Conn {
	t.Helper()
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	conn, err := ch.Open(opts)
	if err != nil {
		t.Fatalf("connect %s: %v", dsn, err)
	}
	return conn
}
