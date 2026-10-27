package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// Migration 0031 (I6, docs/record-contract.md §9): a database at 0030
// holds stock spans the old rule left without a row — a top-level
// invoke_agent span without gen_ai.conversation.id, a chat span with
// message content under it. Opening it adds DerivedMessages and
// re-derives every stock trace: the run is there, with no session, its
// message count read back, and the trace list names it.
func TestUpgradeTo0031RederivesStock(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0031 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 30; v++ {
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
	const tr = "7e000000000000000000000000000001"
	sp := func(id, parent, spanName string, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: spanName, Kind: 1,
			Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "ts-planner",
			Attrs: attrs, Resource: map[string]any{"service.name": "ts-planner"}}
	}
	// The spans as an older weft stored them: inserted, never derived.
	if err := (&DB{conn: conn}).insertSpans(ctx, []obsdb.Span{
		sp("7e00000000000001", "", "POST /plan", map[string]any{}),
		sp("7e00000000000002", "7e00000000000001", "invoke_agent planner",
			map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "planner"}),
		sp("7e00000000000003", "7e00000000000002", "chat m",
			map[string]any{"gen_ai.operation.name": "chat", "gen_ai.output.messages": `[]`}),
	}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0030 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	id := obsdb.StockRunID(tr, "7e00000000000002")
	r, err := db.Run(ctx, id)
	if err != nil || !r.Derived || r.SessionID != "" || r.Steps != 1 || r.MessageCount != 1 {
		t.Fatalf("after 0031 %s = %+v, %v; want a derived run with no session, 1 step, 1 chat span with messages", id, r.RunRow, err)
	}
	page, err := db.Traces(ctx, obsdb.TraceQuery{})
	if err != nil || len(page.Traces) != 1 || len(page.Traces[0].RunIDs) != 1 || page.Traces[0].RunIDs[0] != id {
		t.Errorf("traces after 0031 = %+v (%v), want the trace naming %s", page.Traces, err, id)
	}
}
