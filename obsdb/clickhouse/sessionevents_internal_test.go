package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestUpgradeTo0025BackfillsSessionEvents (U12.2): a database at 0024
// holding thread records — one filed under no run — and a compaction
// marker in otel_logs opens at 0025 with them in weft_session_events
// (the backfill), in time order, read once each though the backfill and
// a later retried write both name them; a record of another kind is
// left out.
func TestUpgradeTo0025BackfillsSessionEvents(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0025 upgrade needs a clickhouse server (README has the one-line container recipe)")
	}
	ctx := context.Background()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_up_" + hex.EncodeToString(suffix)
	admin := rawConn(t, base)
	if err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
		_ = admin.Close()
	})
	u.Path = "/" + name
	dsn := u.String()
	conn := rawConn(t, dsn)
	if err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS obsdb_migrations (
		version UInt32, name String, applied_at DateTime DEFAULT now()
	) ENGINE = MergeTree ORDER BY version`); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 24; v++ {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if err := conn.Exec(ctx, stmt); err != nil {
				t.Fatalf("migration %d: %v", v, err)
			}
		}
		if err := conn.Exec(ctx, `INSERT INTO obsdb_migrations (version, name) VALUES (?, ?)`, uint32(v), migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Add(-time.Hour)
	rec := func(run, kind, event, body string, d time.Duration) obsdb.Record {
		return obsdb.Record{Time: at.Add(d), EventName: event, Body: body, Service: "svc", Severity: 9,
			Attrs: map[string]any{"weft.record": kind, "weft.run.id": run, "weft.session.id": "s_up", "weft.content": "none"}}
	}
	recs := []obsdb.Record{
		rec("s_up-t1", "thread", "weft.thread.approval.request", `{"kind":"approval.request","session":"s_up","entry":"e3"}`, 0),
		rec("s_up-t1", "compaction", "weft.compaction", `{"scope":"session","hash":"h"}`, time.Second),
		rec("", "thread", "weft.thread.grant.revoke", `{"kind":"grant.revoke","session":"s_up","entry":"e9"}`, 2*time.Second),
		rec("s_up-t1", "event", "weft.event", `{"type":"run_start"}`, -time.Second),
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0024 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	// A retried batch after the upgrade: the view copies it again; the
	// key collapses the copies.
	if err := db.Write(ctx, obsdb.Batch{Records: recs[:3]}); err != nil {
		t.Fatal(err)
	}
	page, err := db.(obsdb.SessionEventReader).SessionEvents(ctx, obsdb.SessionEventQuery{SessionID: "s_up"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Events {
		got = append(got, e.RunID+"|"+e.Name)
	}
	want := []string{"s_up-t1|weft.thread.approval.request", "s_up-t1|weft.compaction", "|weft.thread.grant.revoke"}
	if !slices.Equal(got, want) || page.Total != 3 {
		t.Errorf("session events after 0025 = %q (total %d), want %q", got, page.Total, want)
	}
}

// TestRunlessSessionRecordsFollowTheirSession (U12.2-fix): a thread
// record filed under no run, riding a run's trace and span, stays in
// otel_logs and weft_session_events alike when DeleteRun takes that run
// (it is the session's, not the run's app line), and goes from both
// when Sweep finds its session with no run left; Vacuum merges
// weft_session_events with the other tables deletions mutate.
func TestRunlessSessionRecordsFollowTheirSession(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the one-line container recipe)")
	}
	ctx := context.Background()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_rl_" + hex.EncodeToString(suffix)
	admin := rawConn(t, base)
	if err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
		_ = admin.Close()
	})
	u.Path = "/" + name
	db, err := Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	conn := rawConn(t, u.String())
	defer func() { _ = conn.Close() }()

	const trace, span, sid, run = "a1a2a3a4a5a6a7a8a9aaabacadaeaf10", "1000000000000001", "s_rl", "s_rl-t1"
	t0 := time.Now().UTC().Add(-time.Minute)
	ev := func(typ string, pos int64, body string) obsdb.Record {
		return obsdb.Record{Time: t0, TraceID: trace, SpanID: span, Severity: 9, Body: body, Service: "svc",
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": run, "weft.event.type": typ, "weft.event.pos": pos, "weft.session.id": sid, "weft.turn": int64(1)}}
	}
	batch := obsdb.Batch{
		Spans: []obsdb.Span{{TraceID: trace, SpanID: span, Name: "invoke_agent a", Kind: 1, Start: t0, End: t0.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": run, "weft.session.id": sid, "weft.turn": int64(1)}}},
		Records: []obsdb.Record{
			ev("run_start", 0, `{"type":"run_start","id":"`+run+`"}`),
			ev("run_finish", 1, `{"type":"run_finish","run_id":"`+run+`","steps":1}`),
			{Time: t0, TraceID: trace, SpanID: span, Severity: 13, EventName: "weft.thread.approval.refused", Service: "svc",
				Body:  `{"kind":"approval.refused","session":"s_rl","entry":"e2"}`,
				Attrs: map[string]any{"weft.record": "thread", "weft.run.id": "", "weft.session.id": sid, "weft.content": "none"}},
			{Time: t0, TraceID: trace, SpanID: span, Severity: 9, Body: "an app line", Service: "svc"},
		},
	}
	if err := db.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	counts := func() (logs, events uint64) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT count() FROM otel_logs WHERE LogAttributes['weft.record'] = 'thread' AND LogAttributes['weft.session.id'] = ?`, sid).Scan(&logs); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT count() FROM weft_session_events FINAL WHERE SessionId = ?`, sid).Scan(&events); err != nil {
			t.Fatal(err)
		}
		return logs, events
	}
	if err := db.DeleteRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	var app uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM otel_logs WHERE Body = 'an app line'`).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if l, e := counts(); l != 1 || e != 1 || app != 0 {
		t.Errorf("after DeleteRun: %d thread rows in otel_logs, %d session events, %d app lines; want 1, 1, 0 (the app line goes with its run, the session's record stays whole)", l, e, app)
	}
	if _, err := db.Sweep(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if l, e := counts(); l != 0 || e != 0 {
		t.Errorf("after Sweep: %d thread rows in otel_logs, %d session events; want none (the session has no run left)", l, e)
	}
	if err := db.Vacuum(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(vacuumTables, "weft_session_events") {
		t.Errorf("vacuumTables = %v, want weft_session_events among them", vacuumTables)
	}
}
