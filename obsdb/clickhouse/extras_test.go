package clickhouse_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
)

func ctx() context.Context { return context.Background() }

func deltaBatch(runID string, positions ...int64) obsdb.Batch {
	var records []obsdb.Record
	for _, p := range positions {
		records = append(records, obsdb.Record{
			Time:      time.Unix(0, 1790845923120000000+int64(p)*int64(time.Second)).UTC(),
			EventName: "weft.event",
			TraceID:   "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708",
			Severity: 9, Body: `{"type":"text_delta","text":"x"}`, Service: "conf-svc",
			Attrs: map[string]any{
				"weft.record": "delta", "weft.run.id": runID,
				"weft.event.type": "text_delta", "weft.delta.pos": p,
			},
			Resource: map[string]any{"service.name": "conf-svc"},
		})
	}
	return obsdb.Batch{Records: records}
}

// The delta rule's storage half: counted, never stored by default
// (Q4); KeepDeltas turns direct weft_deltas rows on, on their own
// counter, and nothing else feeds that table.
func TestKeepDeltasStorage(t *testing.T) {
	db, dsn := openFresh(t)
	if err := db.Write(ctx(), deltaBatch("c1", 0, 1)); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if det.DeltaCount != 2 {
		t.Fatalf("delta high-water = %d, want 2 (counted without storage)", det.DeltaCount)
	}
	// Default: no rows in weft_deltas.
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	var n uint64
	if err := conn.QueryRow(ctx(), "SELECT count() FROM weft_deltas FINAL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("weft_deltas rows without KeepDeltas = %d, want 0", n)
	}

	kept, keptDSN := openFresh(t, clickhouse.KeepDeltas())
	if err := kept.Write(ctx(), deltaBatch("c2", 0, 1)); err != nil {
		t.Fatal(err)
	}
	conn2 := openRaw(t, keptDSN)
	defer func() { _ = conn2.Close() }()
	var keptRows uint64
	if err := conn2.QueryRow(ctx(), "SELECT count() FROM weft_deltas FINAL WHERE RunId = 'c2'").Scan(&keptRows); err != nil {
		t.Fatal(err)
	}
	if keptRows != 2 {
		t.Fatalf("weft_deltas rows with KeepDeltas = %d, want 2", keptRows)
	}
	kd, err := kept.Run(ctx(), "c2")
	if err != nil {
		t.Fatal(err)
	}
	if kd.DeltaCount != 2 {
		t.Errorf("kept delta high-water = %d, want 2", kd.DeltaCount)
	}
}

// TTL(...) overrides both windows through MODIFY TTL; the defaults come
// from the migration. SHOW CREATE TABLE is the proof the schema keeps.
func TestTTLOptionModifiesTables(t *testing.T) {
	db, dsn := openFresh(t, clickhouse.TTL(time.Hour, 48*time.Hour))
	_ = db
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	for _, c := range []struct{ table, want string }{
		{"otel_logs", "toIntervalSecond(3600)"},
		{"weft_records", "toIntervalSecond(3600)"},
		{"otel_traces", "toIntervalSecond(172800)"},
		{"weft_runs", "toIntervalSecond(172800)"},
	} {
		var ddl string
		if err := conn.QueryRow(ctx(),
			"SELECT create_table_query FROM system.tables WHERE database = currentDatabase() AND name = ?",
			c.table).Scan(&ddl); err != nil {
			t.Fatalf("show %s: %v", c.table, err)
		}
		if !strings.Contains(ddl, c.want) {
			t.Errorf("%s: ttl %q missing %q", c.table, ddl, c.want)
		}
	}
}

// The migration numbering rule, shared with SQLite: a database whose
// obsdb_migrations is ahead of this binary refuses to open.
func TestErrNewerSchema(t *testing.T) {
	db, dsn := openFresh(t)
	if err := db.Write(ctx(), deltaBatch("c1", 0)); err != nil {
		t.Fatal(err)
	}
	conn := openRaw(t, dsn)
	if err := conn.Exec(ctx(),
		"INSERT INTO obsdb_migrations (version, name) VALUES (999, 'from_the_future')"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := clickhouse.Open(dsn)
	if !errors.Is(err, clickhouse.ErrNewerSchema) {
		t.Fatalf("reopen a newer schema = %v, want ErrNewerSchema", err)
	}
}

// Reopening the same database applies nothing new (CREATE ... IF NOT
// EXISTS everywhere, versions recorded) and stays usable.
func TestReopenIsANoop(t *testing.T) {
	db, dsn := openFresh(t)
	if err := db.Write(ctx(), deltaBatch("c1", 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := clickhouse.Open(dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = again.Close() }()
	det, err := again.Run(ctx(), "c1")
	if err != nil || det.DeltaCount != 1 {
		t.Fatalf("after reopen: run = %+v, %v", det, err)
	}
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	var versions uint64
	if err := conn.QueryRow(ctx(), "SELECT count() FROM obsdb_migrations").Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Errorf("obsdb_migrations rows after reopen = %d, want 1", versions)
	}
}

// Writes after Close fail with the sentinel every backend shares.
func TestErrClosed(t *testing.T) {
	db, _ := openFresh(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx(), deltaBatch("c1", 0)); !errors.Is(err, obsdb.ErrClosed) {
		t.Errorf("Write after Close = %v, want ErrClosed", err)
	}
	if _, err := db.Runs(ctx(), obsdb.RunQuery{}); !errors.Is(err, obsdb.ErrClosed) {
		t.Errorf("Runs after Close = %v, want ErrClosed", err)
	}
}

// Write is idempotent on (trace, span) — S3.2, the promise doc.go
// makes ("FINAL or LIMIT 1 BY on the dedup key") and sqlite keeps with
// INSERT OR IGNORE. otel_traces is a plain MergeTree and keeps the
// retried batch's duplicate rows, so this pins the read-side dedup:
// the identical batch written twice yields one copy of each span from
// both RunSpans and Trace, and distinct spans are never collapsed.
// (obsdbtest's Idempotence subtest never reads spans back; merge-B is
// asked to add that read — notes-lane-b2 §9. This is the pin until
// then.)
func TestSpanWritesAreIdempotent(t *testing.T) {
	db, _ := openFresh(t)
	at := time.Unix(0, 1790845923120000000).UTC()
	span := func(id string) obsdb.Span {
		return obsdb.Span{
			TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: id,
			Name: "invoke_agent conf", Kind: 1, Start: at, End: at.Add(9 * time.Second),
			StatusCode: 1, Service: "conf-svc",
			Attrs: map[string]any{
				"gen_ai.operation.name": "invoke_agent", "weft.run.id": "c1",
				"gen_ai.agent.name": "conf", "weft.session.id": "s_conf", "weft.turn": int64(1),
			},
			Resource: map[string]any{"service.name": "conf-svc"},
		}
	}
	batch := obsdb.Batch{
		Spans: []obsdb.Span{span("0a0b0c0d0e0f0102"), span("0a0b0c0d0e0f0304")},
		Records: []obsdb.Record{{
			Time: at, EventName: "weft.event", Body: `{"type":"run_start","id":"c1"}`,
			Service: "conf-svc",
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": "c1",
				"weft.event.type": "run_start", "weft.event.pos": int64(0)},
		}},
	}
	for i := 0; i < 2; i++ {
		if err := db.Write(ctx(), batch); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.RunSpans(ctx(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("RunSpans after a rewrite = %d spans, want 2 (one per span id)", len(got))
	}
	tr, err := db.Trace(ctx(), "0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) != 2 {
		t.Fatalf("Trace after a rewrite = %d spans, want 2 (one per span id)", len(tr))
	}
	if got[0].SpanID == got[1].SpanID {
		t.Errorf("RunSpans returned the same span id twice: %s", got[0].SpanID)
	}
}

// A write is visible to the very next read: wait_for_async_insert=1 on
// the connection (S3.6), pinned here so a settings regression on the
// driver or server cannot quietly make writes lag.
func TestAsyncInsertsAreWaited(t *testing.T) {
	db, dsn := openFresh(t)
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{{
		Time: time.Unix(0, 1790845923120000000).UTC(), EventName: "weft.event",
		Body: `{"type":"run_start","id":"c1"}`, Service: "conf-svc",
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": "c1", "weft.event.type": "run_start", "weft.event.pos": int64(0)},
	}}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Runs(ctx(), obsdb.RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("runs after write = %d, want 1 (async insert waited)", page.Total)
	}
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	var value string
	if err := conn.QueryRow(ctx(),
		"SELECT value FROM system.settings WHERE name = 'wait_for_async_insert'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "1" {
		t.Errorf("wait_for_async_insert = %q, want 1", value)
	}
}
