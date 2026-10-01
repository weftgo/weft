package clickhouse_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

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

// The string spelling of weft.turn is the one the real chain produces
// (thread mints it as metadata, the core stamps metadata values as
// attribute.String). The views derive Turn through
// toInt32OrZero over the stringified attrs, so the hosted backend must
// read "3" as 3 — the programme audit's P1-1 read turn 0 on the Go
// side; this pins the backend's half against the live server.
func TestTurnStringAttrDerives(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the one-line container recipe)")
	}
	db, _ := openFresh(t)
	runID := "s_conf-t3"
	turn := map[string]any{
		"weft.session.id": "s_conf", "weft.public_id": "pub_conf",
		"gen_ai.agent.name": "conf", "weft.turn": "3",
	}
	recs := []obsdb.Record{}
	for i, ev := range []string{
		`{"type":"run_start","id":"` + runID + `","model":{"provider":"wefttest","name":"script"},"agent":"conf"}`,
		`{"type":"run_finish","run_id":"` + runID + `","usage":{"input_tokens":9,"output_tokens":3},"steps":1}`,
	} {
		attrs := map[string]any{"weft.record": "event", "weft.run.id": runID, "weft.event.pos": int64(i)}
		if i == 0 {
			attrs["weft.event.type"] = "run_start"
		}
		for k, v := range turn {
			attrs[k] = v
		}
		recs = append(recs, obsdb.Record{
			Time: time.Now().UTC().Add(time.Duration(i) * time.Second),
			EventName: "weft.event", TraceID: "0102030405060708090a0b0c0d0e0f10",
			SpanID: "0102030405060708", Severity: 9, Body: ev, Service: "conf-svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "conf-svc"},
		})
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if det.Turn != 3 {
		t.Errorf("run row turn = %d, want 3 (the string attr parsed by toInt32OrZero)", det.Turn)
	}
}
