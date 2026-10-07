package clickhouse_test

import (
	"context"
	"errors"
	"fmt"
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
	// One row per migration: 0001 (init), 0002 (experiments), 0003
	// (status spelling) and 0004 (request record) — the same count TestMigrationPinsExporterVersion
	// pins as the highest version. (Step 8b review fix 1: this read still
	// wanted 1 after 0002 landed, failing only on a real server, where
	// the gated suite first ran.)
	if versions != 4 {
		t.Errorf("obsdb_migrations rows after reopen = %d, want 4 (0001, 0002, 0003, 0004)", versions)
	}
}

// TestExperimentsOrderByWriteTime pins the list order against the
// binding the step 8b review found live (review fix 2): Updated goes
// in through a bound time.Time parameter, which the driver writes at
// whole-second precision — saves within one second tie on Updated, so
// the newest-first list must order by the engine's own write time
// (InsertTime, DEFAULT now64(9), microsecond-precise), never fall
// back to the id. The conformance subtest pins the same rule through
// the public write path; the second leg here removes the timing luck
// with rows whose Updated is identical by construction.
func TestExperimentsOrderByWriteTime(t *testing.T) {
	db, _ := openFresh(t)
	// The review's probe shape: three saves 80 ms apart, the newest
	// write on exp_1.
	saves := []obsdb.Experiment{
		{ID: "exp_0", Name: "older"},
		{ID: "exp_1", Name: "newer"},
		{ID: "exp_1", Name: "newer refresh"},
	}
	for _, e := range saves {
		if err := db.SaveExperiment(ctx(), e); err != nil {
			t.Fatal(err)
		}
		time.Sleep(80 * time.Millisecond)
	}
	list, err := db.Experiments(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "exp_1" || list[0].Name != "newer refresh" {
		t.Errorf("list = %+v, want exp_1's newest write first", list)
	}

	// The hard pin: two rows whose Updated is identical at whole-second
	// precision (exactly what the bound parameter writes), the older
	// write on the alphabetically-first id — InsertTime decides. The
	// timestamps are SQL literals so sub-second precision survives by
	// construction (a bound time.Time is the coarse path under test).
	db2, dsn := openFresh(t)
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	if err := conn.Exec(ctx(), `INSERT INTO experiments
		(Id, Name, Agent, Created, Updated, Variants, Inputs, InsertTime) VALUES
		('exp_a', 'written first', 'ag', toDateTime64('2026-09-17 00:00:00', 9, 'UTC'), toDateTime64('2026-09-17 00:00:00', 9, 'UTC'), '[]', '[]', toDateTime64('2026-09-17 00:00:00.010', 9, 'UTC')),
		('exp_z', 'written last',  'ag', toDateTime64('2026-09-17 00:00:00', 9, 'UTC'), toDateTime64('2026-09-17 00:00:00', 9, 'UTC'), '[]', '[]', toDateTime64('2026-09-17 00:00:00.090', 9, 'UTC'))`); err != nil {
		t.Fatal(err)
	}
	raw, err := db2.Experiments(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 2 || raw[0].ID != "exp_z" {
		t.Errorf("list with tied Updated = %+v, want exp_z (the newest write) before exp_a — InsertTime, never the id, breaks the tie", raw)
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

// The behavioural pin of Runs(Meta:) — the offline test pins only the
// SQL shape (TestRunsInnerMetaFilter); sqlite pins the behaviour
// (sqlite's TestRunsMetaFilter). Two tenants' runs, a subset filter
// returns the matching rows with their Meta present. The same
// fixtures pin the Playground pointer bind: &false scopes the list to
// non-playground runs.
func TestRunsMetaFilterBehaviour(t *testing.T) {
	db, _ := openFresh(t)
	at := time.Unix(0, 1790845923120000000).UTC()
	rec := func(id string, extra map[string]any) obsdb.Record {
		attrs := map[string]any{
			"weft.record": "event", "weft.run.id": id,
			"weft.event.type": "run_start", "weft.event.pos": int64(0),
		}
		for k, v := range extra {
			attrs[k] = v
		}
		return obsdb.Record{
			Time: at, EventName: "weft.event",
			Body: `{"type":"run_start","id":"` + id + `"}`, Service: "conf-svc",
			Attrs: attrs,
		}
	}
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
		rec("r1", map[string]any{"tenant": "acme", "pr": "77"}),
		rec("r2", map[string]any{"tenant": "globex"}),
		rec("r3", map[string]any{"tenant": "acme", "weft.playground": true}),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Runs(ctx(), obsdb.RunQuery{Meta: map[string]string{"tenant": "acme"}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Runs) != 2 {
		t.Fatalf("meta filter = total %d / %d rows, want the two acme runs", page.Total, len(page.Runs))
	}
	for _, r := range page.Runs {
		if r.ID == "r1" {
			if r.Meta["tenant"] != "acme" || r.Meta["pr"] != "77" {
				t.Errorf("r1 meta = %v, want tenant=acme pr=77", r.Meta)
			}
		}
	}
	notPlayground := false
	page, err = db.Runs(ctx(), obsdb.RunQuery{
		Meta:       map[string]string{"tenant": "acme"},
		Playground: &notPlayground,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Runs) != 1 || page.Runs[0].ID != "r1" {
		t.Fatalf("meta + Playground=&false = total %d / %d rows, want only r1", page.Total, len(page.Runs))
	}
}

// A span event's attributes round-trip typed (S3.1/S3.3): an int64
// event attribute written through Write reads back int64 from both
// span reads, exactly like the top-level span attributes — the
// WeftEvents JSON column decoded with the UseNumber rule. (T17 review
// fix 2: the plain json.Unmarshal decode read int64(3) back as
// float64(3); sqlite keeps int64.)
func TestSpanEventAttrsTypedRoundTrip(t *testing.T) {
	db, _ := openFresh(t)
	at := time.Unix(0, 1790845923120000000).UTC()
	span := obsdb.Span{
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0a0b0c0d0e0f0102",
		Name: "invoke_agent conf", Kind: 1, Start: at, End: at.Add(time.Second),
		StatusCode: 1, Service: "conf-svc",
		Attrs:    map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "c1"},
		Resource: map[string]any{"service.name": "conf-svc"},
		Events: []obsdb.SpanEvent{{
			Time: at.Add(100 * time.Millisecond), Name: "gen_ai.content.prompt",
			Attrs: map[string]any{"retry.count": int64(3), "ratio": 0.5},
		}},
	}
	if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{span}}); err != nil {
		t.Fatal(err)
	}
	for _, spans := range [][]obsdb.Span{
		mustRunSpans(t, db, "c1"),
		mustTrace(t, db, span.TraceID),
	} {
		if len(spans) != 1 || len(spans[0].Events) != 1 {
			t.Fatalf("span events = %d spans / %d events, want 1/1", len(spans), len(spans[0].Events))
		}
		ev := spans[0].Events[0]
		if n, ok := ev.Attrs["retry.count"].(int64); !ok || n != 3 {
			t.Errorf("int64 event attr = %#v, want int64(3)", ev.Attrs["retry.count"])
		}
		if f, ok := ev.Attrs["ratio"].(float64); !ok || f != 0.5 {
			t.Errorf("float event attr = %#v, want float64(0.5)", ev.Attrs["ratio"])
		}
		if ev.Name != "gen_ai.content.prompt" {
			t.Errorf("event name = %q", ev.Name)
		}
	}
}

func mustRunSpans(t *testing.T, db obsdb.DB, runID string) []obsdb.Span {
	t.Helper()
	spans, err := db.RunSpans(ctx(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return spans
}

func mustTrace(t *testing.T, db obsdb.DB, traceID string) []obsdb.Span {
	t.Helper()
	spans, err := db.Trace(ctx(), traceID)
	if err != nil {
		t.Fatal(err)
	}
	return spans
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

// Created survives an update (the audit's P2-4): the save's prior
// read keeps the row's own Created, and only ErrNotFound means "first
// save" — any other read error must surface instead of silently
// resetting the created-at to the save time. The distinctive literal
// makes survival deterministic; a reset would read as now.
func TestExperimentCreatedSurvivesUpdate(t *testing.T) {
	db, dsn := openFresh(t)
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	first := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	if err := conn.Exec(ctx(), `INSERT INTO experiments
		(Id, Name, Agent, Created, Updated, Variants, Inputs) VALUES
		('exp_c', 'first', 'ag', toDateTime64('2026-09-17 00:00:00', 9, 'UTC'),
		 toDateTime64('2026-09-17 00:00:00', 9, 'UTC'), '[]', '[]')`); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveExperiment(ctx(), obsdb.Experiment{ID: "exp_c", Name: "refresh"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Experiment(ctx(), "exp_c")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "refresh" {
		t.Errorf("name = %q, want the update", got.Name)
	}
	if !got.Created.Equal(first) {
		t.Errorf("created = %v, want the first save's %v — an update must not reset it", got.Created, first)
	}
	// A genuinely new id stamps now (ErrNotFound is the reset case).
	if err := db.SaveExperiment(ctx(), obsdb.Experiment{ID: "exp_new", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	gotNew, err := db.Experiment(ctx(), "exp_new")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(gotNew.Created) > time.Minute {
		t.Errorf("a first save's created = %v, want now", gotNew.Created)
	}
}

// Migration 0004 (ADR 0028) on a live server: weft_runs carries the
// request record's three columns at their "not recorded" defaults, and
// weft_records_mv keeps request, prompt and tools records under their
// own per-run index, with weft.step.index in Step.
func TestRequestRecordSchema(t *testing.T) {
	db, dsn := openFresh(t)
	rec := func(kind, posKey string, pos, step int64) obsdb.Record {
		attrs := map[string]any{"weft.record": kind, "weft.run.id": "q1", posKey: pos}
		if step >= 0 {
			attrs["weft.step.index"] = step
		}
		return obsdb.Record{
			Time: time.Unix(0, 1790845923120000000+pos).UTC(), EventName: "weft." + kind,
			Severity: 9, Body: `{}`, Service: "conf-svc", Attrs: attrs,
			Resource: map[string]any{"service.name": "conf-svc"},
		}
	}
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
		rec("prompt", "weft.prompt.index", 0, -1),
		rec("tools", "weft.tools.index", 0, -1),
		rec("request", "weft.request.index", 0, 1),
		rec("request", "weft.request.index", 1, 2),
		rec("messages", "weft.messages.index", 2, 1),
	}}); err != nil {
		t.Fatal(err)
	}
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	var instructions, catalog string
	var requests int64
	if err := conn.QueryRow(ctx(), `SELECT max(InstructionsHash), max(CatalogHash), max(RequestCount)
		FROM weft_runs WHERE RunId = 'q1'`).Scan(&instructions, &catalog, &requests); err != nil {
		t.Fatal(err)
	}
	if instructions != "" || catalog != "" || requests != 0 {
		t.Errorf("run columns = %q, %q, %d; want the defaults until the emission ships", instructions, catalog, requests)
	}
	rows, err := conn.Query(ctx(), `SELECT Kind, Pos, Step FROM weft_records FINAL
		WHERE RunId = 'q1' ORDER BY Kind, Pos`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var kind string
		var pos int64
		var step int32
		if err := rows.Scan(&kind, &pos, &step); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s/%d/%d", kind, pos, step))
	}
	want := "messages/2/1 prompt/0/-1 request/0/1 request/1/2 tools/0/-1"
	if strings.Join(got, " ") != want {
		t.Errorf("weft_records = %v, want %s", got, want)
	}
}
