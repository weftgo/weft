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
