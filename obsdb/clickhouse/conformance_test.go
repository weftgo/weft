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
			Time:      time.Now().UTC().Add(time.Duration(i) * time.Second),
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

// Cursors must be exact to the nanosecond: the driver renders a
// positional time.Time bind at Seconds scale, so a `Started < ?`
// cursor carrying S.<nanos> compared against S.000000000 and every
// row in the same second before the boundary was skipped forever —
// rows silently dropped at essentially every page boundary (the
// programme audit's P1-4). The fix compares in integer nanoseconds;
// this pin writes two runs (and two sessions) started within one wall
// second with distinct nanos and pages through the boundary.
func TestCursorSubSecondPaging(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the one-line container recipe)")
	}
	db, _ := openFresh(t)
	ctx := context.Background()

	// writeRun leaves one run whose Started (min over records) and
	// LastSeen (max) sit at base+off, all within the same wall second.
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	writeRun := func(runID, session string, off time.Duration) {
		t.Helper()
		at := base.Add(off)
		attrs := map[string]any{
			"weft.record": "event", "weft.run.id": runID,
			"weft.event.type": "run_start", "weft.event.pos": int64(0),
			"weft.session.id": session, "gen_ai.agent.name": "conf",
		}
		rec := obsdb.Record{
			Time: at, EventName: "weft.event", Severity: 9,
			Body:    `{"type":"run_start","id":"` + runID + `","model":{"provider":"wefttest","name":"script"},"agent":"conf"}`,
			Service: "conf-svc", Attrs: attrs, Resource: map[string]any{"service.name": "conf-svc"},
		}
		if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{rec}}); err != nil {
			t.Fatal(err)
		}
	}
	writeRun("sub_late", "sess_late", 700*time.Millisecond)
	writeRun("sub_early", "sess_early", 100*time.Millisecond)

	// Runs: page one holds the later run; the cursor it hands back is
	// the full-precision Started. Page two must still return the
	// earlier run — the seconds-floor cursor excluded it
	// (S.100ms >= S.000ms).
	p1, err := db.Runs(ctx, obsdb.RunQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Runs) != 1 || p1.Runs[0].ID != "sub_late" || p1.NextBefore == nil {
		t.Fatalf("page one = %+v (next %v)", p1.Runs, p1.NextBefore)
	}
	p2, err := db.Runs(ctx, obsdb.RunQuery{Limit: 1, Before: *p1.NextBefore})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Runs) != 1 || p2.Runs[0].ID != "sub_early" {
		t.Fatalf("page two = %v, want sub_early (the sub-second band must not be skipped)", idsOf(p2.Runs))
	}
	if !p2.Runs[0].Started.After(base) && p2.Runs[0].Started.Before(base.Add(time.Second)) {
		t.Fatalf("fixture drifted out of its wall second: %v", p2.Runs[0].Started)
	}

	// Sessions page on max(LastSeen) — the same boundary.
	s1, err := db.Sessions(ctx, obsdb.SessionQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(s1.Sessions) != 1 || s1.Sessions[0].ID != "sess_late" || s1.NextBefore == nil {
		t.Fatalf("sessions page one = %+v (next %v)", s1.Sessions, s1.NextBefore)
	}
	s2, err := db.Sessions(ctx, obsdb.SessionQuery{Limit: 1, Before: *s1.NextBefore})
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Sessions) != 1 || s2.Sessions[0].ID != "sess_early" {
		t.Fatalf("sessions page two = %+v, want sess_early", s2.Sessions)
	}
}

func idsOf(rows []obsdb.RunRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// Session(id) reads the session's own row directly on the hosted
// backend too (the P2-1 fix): a session older than the newest 500
// must still resolve while the list still shows it.
func TestSessionBeyondNewestPage(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the one-line container recipe)")
	}
	db, _ := openFresh(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour)
	const target = "s_old_target"
	var batch []obsdb.Record
	session := func(id string, at time.Duration) {
		attrs := map[string]any{
			"weft.record": "event", "weft.run.id": id + "-t1",
			"weft.event.type": "run_start", "weft.event.pos": int64(0),
			"weft.session.id": id, "gen_ai.agent.name": "conf",
		}
		batch = append(batch, obsdb.Record{
			Time: base.Add(at), EventName: "weft.event", Severity: 9,
			Body:    `{"type":"run_start","id":"` + id + `-t1","model":{"provider":"wefttest","name":"script"},"agent":"conf"}`,
			Service: "conf-svc", Attrs: attrs, Resource: map[string]any{"service.name": "conf-svc"},
		})
	}
	session(target, 0)
	for i := 0; i < 501; i++ {
		session(fmt.Sprintf("s_new_%03d", i), time.Duration(i+2)*time.Minute)
	}
	if err := db.Write(ctx, obsdb.Batch{Records: batch}); err != nil {
		t.Fatal(err)
	}
	list, err := db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 502 {
		t.Fatalf("sessions total = %d, want 502", list.Total)
	}
	det, err := db.Session(ctx, target)
	if err != nil {
		t.Fatalf("Session(%s): %v (a session older than the newest page must still resolve)", target, err)
	}
	if det.ID != target || det.Turns != 1 {
		t.Errorf("detail = %+v", det.SessionRow)
	}
	if _, err := db.Session(ctx, "nope"); err == nil {
		t.Error("unknown session resolved")
	}
}
