package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// A database written before ADR 0028 — migrations 0001 to 0003 applied
// from the embedded bodies, runs written by a core that emitted no
// instructions hash — opens through 0004 on a live server: the run
// reads InstructionsHash "" (ADR 0028 §10's not_recorded), its prompt
// and tools answer ErrNotFound as a not_recorded HoleError, and the
// messages rows written before weft_records.Input existed read Input
// -1: their index 0 is inferred and marked derived, as before.
func TestUpgradeFromPreRequestRecord(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the pre-0004 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for _, v := range []int{1, 2, 3} {
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
	n := int64(0)
	rec := func(run, kind string, attrs map[string]any, body string) obsdb.Record {
		n++
		a := map[string]any{"weft.record": kind, "weft.run.id": run}
		for k, v := range attrs {
			a[k] = v
		}
		return obsdb.Record{
			Time: time.Unix(0, 1790845923120000000+n).UTC(), EventName: "weft." + kind,
			Severity: 9, Body: body, Service: "conf-svc", Attrs: a,
			Resource: map[string]any{"service.name": "conf-svc"},
		}
	}
	user := `[{"role":"user","content":[{"type":"text","text":"q"}]}]`
	reply := `[{"role":"assistant","content":[{"type":"text","text":"a"}]}]`
	old := &DB{conn: conn}
	if err := old.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("old1", "event", map[string]any{"weft.event.type": "run_start", "weft.event.pos": int64(0)}, `{"type":"run_start"}`),
		rec("old1", "messages", map[string]any{"weft.messages.index": int64(0), "weft.messages.input": true}, user),
		rec("old1", "messages", map[string]any{"weft.messages.index": int64(1)}, reply),
		rec("old1", "event", map[string]any{"weft.event.type": "run_finish", "weft.event.pos": int64(1)}, `{"type":"run_finish","steps":1}`),
		rec("old2", "event", map[string]any{"weft.event.type": "run_start", "weft.event.pos": int64(0)}, `{"type":"run_start"}`),
		rec("old2", "messages", map[string]any{"weft.messages.index": int64(0)}, reply),
	}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a pre-0004 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	run, err := db.Run(ctx, "old1")
	if err != nil {
		t.Fatal(err)
	}
	if run.InstructionsHash != "" || run.CatalogHash != "" || run.RequestCount != 0 || run.RequestsHole() != obsdb.HoleNotRecorded {
		t.Errorf("upgraded run = %q %q %d hole %q; want the defaults, not_recorded",
			run.InstructionsHash, run.CatalogHash, run.RequestCount, run.RequestsHole())
	}
	var hole *obsdb.HoleError
	if _, err := db.Prompt(ctx, "old1", "any"); !errors.Is(err, obsdb.ErrNotFound) || !errors.As(err, &hole) || hole.Hole != obsdb.HoleNotRecorded {
		t.Errorf("Prompt of a pre-0004 run = %v; want ErrNotFound, not_recorded", err)
	}
	if reqs, err := db.Requests(ctx, "old1", obsdb.RequestQuery{}); err != nil || len(reqs) != 0 {
		t.Errorf("Requests of a pre-0004 run = %+v, %v; want none", reqs, err)
	}
	for runID, want := range map[string][]bool{"old1": {true, false}, "old2": {false}} {
		got, err := db.TranscriptBatches(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d batches, want %d", runID, len(got), len(want))
		}
		for i, b := range got {
			if b.Input != want[i] || b.InputDerived != (i == 0) || b.Step != -1 {
				t.Errorf("%s batch %d = input %v derived %v step %d; want input %v, derived only on index 0, step -1",
					runID, i, b.Input, b.InputDerived, b.Step, want[i])
			}
		}
	}
}

func rawConn(t *testing.T, dsn string) ch.Conn {
	t.Helper()
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ch.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// A database at 0007 — every migration before the command store applied
// from the embedded bodies, a saved experiment in it — opens through
// 0008 in place on a live server: the experiment survives, the four
// command store tables exist and take a write, and obsdb_migrations
// records 0008.
func TestUpgradeFrom0007AddsCommandStore(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0007 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 7; v++ {
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
	if err := (&DB{conn: conn}).SaveExperiment(ctx, obsdb.Experiment{ID: "exp_1", Name: "tone"}); err != nil {
		t.Fatal(err)
	}
	var tables uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM system.tables WHERE database = currentDatabase()
		AND name IN ('commands', 'run_routes', 'breakpoints', 'runtimes')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("a 0007 database already has %d command store tables", tables)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0007 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if e, err := db.Experiment(ctx, "exp_1"); err != nil || e.Name != "tone" {
		t.Errorf("upgraded experiment = %+v, %v", e, err)
	}
	cs := db.(obsdb.CommandStore)
	if err := cs.PutCommand(ctx, obsdb.StoredCommand{ID: "cmd_1", RuntimeID: "rt_1", State: obsdb.CommandQueued}); err != nil {
		t.Fatal(err)
	}
	if rows, err := cs.Commands(ctx, obsdb.CommandQuery{}); err != nil || len(rows) != 1 {
		t.Errorf("Commands after the upgrade = %v, %v", rows, err)
	}
	if err := cs.PutRoute(ctx, obsdb.RunRoute{RunID: "run_1", RuntimeID: "rt_1"}); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutBreakpoints(ctx, "rt_1", []string{"refund"}); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutRuntime(ctx, obsdb.StoredRuntime{ID: "rt_1"}); err != nil {
		t.Fatal(err)
	}
	var version uint32
	if err := db.(*DB).conn.QueryRow(ctx, `SELECT max(version) FROM obsdb_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != WantSchemaVersion {
		t.Errorf("migration max after the upgrade = %d, want %d (%s)", version, WantSchemaVersion, WantSchemaThrough)
	}
}

// A database at 0009 — spans of a tracer-only run already stored, so
// weft_runs holds the 0004 view's rows without RootEnded — opens through
// 0010 on a live server, whose backfill marks the run root-ended from
// its stored invoke_agent span: it reads succeeded, not interrupted,
// with weft_version from the span's resource (docs/record-contract.md
// §6). A run with a chat span alone stays interrupted.
func TestUpgradeTo0010BackfillsRootEnded(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0010 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 9; v++ {
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
	old := time.Now().UTC().Add(-time.Hour)
	span := func(run, id, name, op string) obsdb.Span {
		return obsdb.Span{TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: id, Name: name, Kind: 1,
			Start: old, End: old.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs:    map[string]any{"gen_ai.operation.name": op, "weft.run.id": run, "weft.manifest.hash": "sha256:mh"},
			Resource: map[string]any{"service.name": "svc", "weft.version": "v0.15.0"}}
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: []obsdb.Span{
		span("r_tracer", "0a0b0c0d0e0f0101", "invoke_agent a", "invoke_agent"),
		span("r_chat", "0a0b0c0d0e0f0102", "chat m", "chat"),
	}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0009 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, c := range []struct {
		id      string
		ended   bool
		status  obsdb.Status
		version string
	}{
		{"r_tracer", true, obsdb.StatusSucceeded, "v0.15.0"},
		// The 0004 view never read the resource; 0010's backfill reads it
		// from every weft span, as obsdb/sqlite's 0009 does.
		{"r_chat", false, obsdb.StatusInterrupted, "v0.15.0"},
	} {
		r, err := db.Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if r.RootEnded != c.ended || r.Status != c.status || r.WeftVersion != c.version || r.ManifestHash != "sha256:mh" {
			t.Errorf("%s after 0010 = root_ended %v, %s, version %q, manifest %q; want %v, %s, %q, sha256:mh",
				c.id, r.RootEnded, r.Status, r.WeftVersion, r.ManifestHash, c.ended, c.status, c.version)
		}
	}
}

// A database at 0010 — a canceled run's invoke_agent span and a
// succeeded run's run_finish already stored, through views that read
// neither value — opens through 0011 on a live server, whose backfill
// fills StoppedBy and CancelCause from both signals
// (docs/record-contract.md §6, U3.2), and the stopped_by filter reads
// them, the prefix form included.
func TestUpgradeTo0011BackfillsStoppedBy(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0011 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 10; v++ {
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
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{
		Spans: []obsdb.Span{{
			TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0a0b0c0d0e0f0201", Name: "invoke_agent a", Kind: 1,
			Start: at, End: at.Add(time.Second), StatusCode: 2, StatusMessage: "context canceled", Service: "svc",
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "r_cancel",
				"weft.run.stopped_by": "canceled", "weft.cancel.cause": "thread.interrupted",
				// Caller metadata beside the keys U3.1 leaked into Meta: the
				// reader drops the leaked keys and keeps this one.
				"tenant": "acme"},
			Resource: map[string]any{"service.name": "svc"},
		}},
		Records: []obsdb.Record{{
			Time: at, EventName: "weft.event", TraceID: "0102030405060708090a0b0c0d0e0f11", SpanID: "0102030405060708",
			Severity: 9, Service: "svc", Resource: map[string]any{"service.name": "svc"},
			Body: `{"type":"run_finish","run_id":"r_submit","steps":1,"stopped_by":"has_tool_call:submit"}`,
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": "r_submit",
				"weft.event.type": "run_finish", "weft.event.pos": int64(1)},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0010 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, c := range []struct{ id, by, cause string }{
		{"r_cancel", "canceled", "thread.interrupted"},
		{"r_submit", "has_tool_call:submit", ""},
	} {
		r, err := db.Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if r.StoppedBy != c.by || r.CancelCause != c.cause {
			t.Errorf("%s after 0011 = stopped_by %q, cancel_cause %q; want %q, %q", c.id, r.StoppedBy, r.CancelCause, c.by, c.cause)
		}
		if c.id == "r_cancel" && !reflect.DeepEqual(r.Meta, map[string]string{"tenant": "acme"}) {
			t.Errorf("r_cancel's meta after 0011 = %v, want only the caller's {tenant: acme} (the leaked stopped_by/cancel.cause dropped)", r.Meta)
		}
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{StoppedBy: "has_tool_call:"})
	if err != nil || page.Total != 1 || page.Runs[0].ID != "r_submit" {
		t.Errorf("stopped_by=has_tool_call: after 0011 = %+v, %v; want r_submit", page, err)
	}
}

// A database at 0011 — a derived row written before 0010 set RootEnded
// on derived writes, and a weft run whose chat span's resource names
// another weft — opens through 0012 on a live server: the backfill sets
// RootEnded on the derived run alone, and the restated traces view takes
// the version from the run's root span alone from then on
// (docs/record-contract.md §6's weft_version and root_ended rows).
func TestUpgradeTo0012RootVersionAndDerived(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0012 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 11; v++ {
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
	if err := conn.Exec(ctx, `INSERT INTO weft_runs (RunId, Started, LastSeen, FinishedOK, Derived, RootEnded)
		VALUES ('otel-old', ?, ?, 1, 1, 0), ('r_plain', ?, ?, 0, 0, 0)`, at, at, at, at); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0011 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	for id, want := range map[string]bool{"otel-old": true, "r_plain": false} {
		r, err := db.Run(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.RootEnded != want {
			t.Errorf("%s after 0012: RootEnded %v, want %v", id, r.RootEnded, want)
		}
	}
	span := func(id, op string, n byte, attrs, res map[string]any) obsdb.Span {
		a := map[string]any{"gen_ai.operation.name": op, "weft.run.id": id}
		for k, v := range attrs {
			a[k] = v
		}
		r := map[string]any{"service.name": "svc"}
		for k, v := range res {
			r[k] = v
		}
		return obsdb.Span{TraceID: "0102030405060708090a0b0c0d0e0f12", SpanID: "0a0b0c0d0e0f030" + string(n),
			Name: op + " a", Kind: 1, Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs: a, Resource: r}
	}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{
		span("r_ver", "chat", '1', nil, map[string]any{"weft.version": "v0.9.0"}),
		span("r_ver", "invoke_agent", '2', nil, map[string]any{"weft.version": "v0.15.0"}),
	}}); err != nil {
		t.Fatal(err)
	}
	r, err := db.Run(ctx, "r_ver")
	if err != nil {
		t.Fatal(err)
	}
	if r.WeftVersion != "v0.15.0" {
		t.Errorf("weft_version after 0012 = %q, want the root's resource v0.15.0, never the chat span's", r.WeftVersion)
	}
}

// A database at 0012 — a root span carrying weft.run.events and
// weft.run.messages already stored, through a view that read neither —
// opens through 0013 on a live server: the backfill fills EventsEmitted
// and MessagesEmitted, meta keeps neither key, and a root written after
// the upgrade fills them through the restated view
// (docs/record-contract.md §6, U5).
func TestUpgradeTo0013BackfillsEmitted(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0013 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 12; v++ {
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
	root := func(id string, n byte, events, messages int64) obsdb.Span {
		return obsdb.Span{
			TraceID: "0102030405060708090a0b0c0d0e0f13", SpanID: "0a0b0c0d0e0f040" + string(n), Name: "invoke_agent a", Kind: 1,
			Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": id,
				"weft.run.events": events, "weft.run.messages": messages, "tenant": "acme"},
			Resource: map[string]any{"service.name": "svc"},
		}
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: []obsdb.Span{root("r_old", '1', 9, 4)}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0012 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{root("r_new", '2', 12, 5)}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id           string
		events, msgs int64
	}{{"r_old", 9, 4}, {"r_new", 12, 5}} {
		r, err := db.Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if r.EventsEmitted != c.events || r.MessagesEmitted != c.msgs {
			t.Errorf("%s after 0013 = emitted %d events / %d messages, want %d / %d", c.id, r.EventsEmitted, r.MessagesEmitted, c.events, c.msgs)
		}
		if len(r.Meta) != 1 || r.Meta["tenant"] != "acme" {
			t.Errorf("%s's meta after 0013 = %v, want only the caller's {tenant: acme}", c.id, r.Meta)
		}
	}
}

// A database at 0014 — a failed run's spans already stored through views
// that read no error type and no problem: a cancelled root denying one
// call, a failed attempt, a chat span answered by another model, a tool
// error, a policy denial and a cut result — opens through 0015 on a
// live server: the backfill fills ErrorType (context.Canceled read as
// cancelled) and the problem set by obsdb.SpanProblems' rules, the same
// spans written after the upgrade give the same counters through the
// restated view, meta keeps none of the new keys, and the error_type
// filter finds both (docs/record-contract.md §6, U4).
func TestUpgradeTo0015BackfillsProblems(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0015 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 14; v++ {
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
	spans := func(run string, trace byte) []obsdb.Span {
		mk := func(n byte, spanName string, status int, attrs map[string]any) obsdb.Span {
			attrs["weft.run.id"] = run
			return obsdb.Span{
				TraceID: "0102030405060708090a0b0c0d0e0f1" + string(trace), SpanID: "0a0b0c0d0e0f05" + string(trace) + string(n),
				Name: spanName, Kind: 1, Start: at, End: at.Add(time.Second), StatusCode: status, Service: "svc",
				Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
			}
		}
		return []obsdb.Span{
			mk('1', "invoke_agent a", 2, map[string]any{"gen_ai.operation.name": "invoke_agent", "tenant": "acme",
				"error.type": "context.Canceled", "weft.run.denied": int64(1)}),
			mk('2', "attempt", 2, map[string]any{"weft.attempt.index": int64(1), "error.type": "model_error"}),
			mk('3', "attempt", 1, map[string]any{"weft.attempt.index": int64(2)}),
			mk('4', "chat primary", 1, map[string]any{"gen_ai.operation.name": "chat",
				"gen_ai.request.model": "primary", "gen_ai.response.model": "backup"}),
			mk('5', "execute_tool boom", 2, map[string]any{"gen_ai.operation.name": "execute_tool",
				"error.type": "panic", "weft.panic.layer": "handler"}),
			mk('6', "execute_tool rm", 2, map[string]any{"gen_ai.operation.name": "execute_tool", "error.type": "DENIED"}),
			mk('7', "execute_tool big", 1, map[string]any{"gen_ai.operation.name": "execute_tool", "weft.tool.truncated_bytes": int64(90)}),
			mk('8', "execute_tool strict", 2, map[string]any{"gen_ai.operation.name": "execute_tool",
				"error.type": "INVALID_INPUT", "weft.tool.input_error": "strict"}),
		}
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: spans("r_old", '1')}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0014 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Write(ctx, obsdb.Batch{Spans: spans("r_new", '2')}); err != nil {
		t.Fatal(err)
	}
	// Open applies 0017 on the way: U4's tokens stay in the set and
	// count nowhere, the re-derived ones count — the chat span answered
	// by another model name is no fallback (U4-fix).
	want := obsdb.Problems{AttemptsRetried: 1, ToolErrors: 2, Denied: 2, Truncated: 1}
	for _, id := range []string{"r_old", "r_new"} {
		r, err := db.Run(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.ErrorType != "cancelled" || r.Problems != want {
			t.Errorf("%s after 0015 = error_type %q, problems %+v; want cancelled, %+v", id, r.ErrorType, r.Problems, want)
		}
		// Meta through the restated view only: rows the pre-0015 traces view (0013's) wrote
		// carry U4's keys in their meta (no producer wrote them before
		// U4; this fixture does), and max(Meta) may pick such a row.
		if id == "r_new" && (len(r.Meta) != 1 || r.Meta["tenant"] != "acme") {
			t.Errorf("%s's meta after 0015 = %v, want only the caller's {tenant: acme}", id, r.Meta)
		}
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{ErrorType: "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 2 {
		t.Errorf("error_type=cancelled = %d runs, want 2", len(page.Runs))
	}
}

// A database at 0016 — U4-fix's span shapes stored through 0015's view,
// whose tokens over-count (every failed attempt retried, a model-name
// fallback, RETRY a tool error, a count-keyed denied_approval that two
// roots of one run collapse) — opens through 0017 on a live server: the
// backfill adds the re-derived tokens, U4's stay in the set and count
// nowhere, and the same spans written after the upgrade through the
// restated view read the same counters (docs/record-contract.md §6).
func TestUpgradeTo0017RederivesProblems(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0017 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 16; v++ {
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
	spans := func(run string, trace byte) []obsdb.Span {
		id := func(n byte) string { return "0a0b0c0d0e0f06" + string(trace) + string(n) }
		mk := func(n, parent byte, spanName string, status int, attrs map[string]any) obsdb.Span {
			attrs["weft.run.id"] = run
			s := obsdb.Span{
				TraceID: "0102030405060708090a0b0c0d0e0f2" + string(trace), SpanID: id(n),
				Name: spanName, Kind: 1, Start: at, End: at.Add(time.Second), StatusCode: status, Service: "svc",
				Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
			}
			if parent != 0 {
				s.ParentSpanID = id(parent)
			}
			return s
		}
		chat := func(n byte, status int) obsdb.Span {
			return mk(n, '0', "chat m", status, map[string]any{"gen_ai.operation.name": "chat",
				"gen_ai.request.model": "m", "gen_ai.response.model": "other"})
		}
		try := func(n, parent byte, index int64, failed bool, layer string) obsdb.Span {
			attrs := map[string]any{"weft.attempt.index": index, "gen_ai.request.model": "m"}
			status := 1
			if failed {
				status, attrs["error.type"] = 2, "model_error"
			}
			if layer != "" {
				attrs["weft.attempt.layer"] = layer
			}
			return mk(n, parent, "attempt", status, attrs)
		}
		return []obsdb.Span{
			mk('0', 0, "invoke_agent a", 1, map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.denied": int64(1)}),
			mk('9', 0, "invoke_agent a", 1, map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.denied": int64(1)}),
			chat('1', 1), try('a', '1', 1, true, ""), try('b', '1', 2, true, ""), try('c', '1', 3, false, ""),
			chat('2', 2), try('d', '2', 1, true, ""), try('e', '2', 2, true, ""),
			chat('3', 1), try('f', '3', 1, true, ""), try('4', '3', 2, false, "fallback"),
			mk('5', '0', "execute_tool fix", 2, map[string]any{"gen_ai.operation.name": "execute_tool", "error.type": "RETRY"}),
			mk('6', '0', "execute_tool nope", 2, map[string]any{"gen_ai.operation.name": "execute_tool", "error.type": "NO_SUCH_TOOL"}),
		}
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: spans("r_old", '1')}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0016 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Write(ctx, obsdb.Batch{Spans: spans("r_new", '2')}); err != nil {
		t.Fatal(err)
	}
	// Retried 2 + 1 + 1, one fallback answer, one tool error
	// (NO_SUCH_TOOL; RETRY none), two denied (one per root).
	want := obsdb.Problems{AttemptsRetried: 4, Fallbacks: 1, ToolErrors: 1, Denied: 2}
	for _, id := range []string{"r_old", "r_new"} {
		r, err := db.Run(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Problems != want {
			t.Errorf("%s after 0017 = problems %+v; want %+v", id, r.Problems, want)
		}
		if id == "r_new" && len(r.Meta) != 0 {
			t.Errorf("%s's meta after 0017 = %v, want none (weft.attempt.layer is contract)", id, r.Meta)
		}
	}
}

// A database at 0017 — spans from a producer before U32.1: an
// invoke_agent span with weft.metadata.dropped (which predates 0018),
// no weft.tap.panics, no retry marks — opens through 0018 on a live
// server: the backfill derives metadata_dropped from the stored span,
// and the spans written after the upgrade through the restated traces
// view count tap_panics and metadata_dropped, the three new keys staying
// out of the run's meta (docs/record-contract.md §6).
func TestUpgradeTo0018BackfillsMetadataDropped(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0018 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 17; v++ {
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
	spans := func(run string, trace byte, u32 bool) []obsdb.Span {
		id := func(n byte) string { return "0a0b0c0d0e0f07" + string(trace) + string(n) }
		mk := func(n, parent byte, spanName string, status int, attrs map[string]any) obsdb.Span {
			attrs["weft.run.id"] = run
			s := obsdb.Span{
				TraceID: "0102030405060708090a0b0c0d0e0f3" + string(trace), SpanID: id(n),
				Name: spanName, Kind: 1, Start: at, End: at.Add(time.Second), StatusCode: status, Service: "svc",
				Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
			}
			if parent != 0 {
				s.ParentSpanID = id(parent)
			}
			return s
		}
		root := map[string]any{"gen_ai.operation.name": "invoke_agent", "tenant": "acme", "weft.metadata.dropped": int64(3)}
		tool := map[string]any{"gen_ai.operation.name": "execute_tool", "error.type": "RETRY"}
		if u32 {
			root["weft.tap.panics"] = int64(2)
			tool["weft.tool.retry"], tool["weft.tool.retry_budget"] = int64(1), int64(2)
		}
		return []obsdb.Span{
			mk('0', 0, "invoke_agent a", 1, root),
			mk('1', '0', "execute_tool flaky", 2, tool),
		}
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: spans("r_old", '1', false)}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0017 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Write(ctx, obsdb.Batch{Spans: spans("r_new", '2', true)}); err != nil {
		t.Fatal(err)
	}
	// RETRY is no tool error; the stored span's metadata_dropped is
	// backfilled; the new span counts its tap panics too.
	for id, want := range map[string]obsdb.Problems{
		"r_old": {MetadataDropped: 3},
		"r_new": {MetadataDropped: 3, TapPanics: 2},
	} {
		r, err := db.Run(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Problems != want {
			t.Errorf("%s after 0018 = problems %+v; want %+v", id, r.Problems, want)
		}
		if id == "r_new" && (len(r.Meta) != 1 || r.Meta["tenant"] != "acme") {
			t.Errorf("%s's meta after 0018 = %v, want only the caller's {tenant: acme} (the retry and tap keys are contract)", id, r.Meta)
		}
	}
}

// A database at 0018 holding a parked run (pending 2) opens through
// 0019 on a live server: a resumer written after the upgrade, naming
// it through weft.resumed_from, reads resumed_from on its own row and
// the parked run reads resumed_by it — the link is read time, so the
// row stored before the column existed links without a backfill; the
// link key stays out of the resumer's meta (docs/record-contract.md
// §6, U13.1-fix).
func TestUpgradeTo0019LinksResumes(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0019 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 18; v++ {
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
	root := func(run string, trace byte, start time.Time, attrs map[string]any) obsdb.Span {
		attrs["weft.run.id"] = run
		attrs["gen_ai.operation.name"] = "invoke_agent"
		return obsdb.Span{
			TraceID: "0102030405060708090a0b0c0d0e0f4" + string(trace), SpanID: "0a0b0c0d0e0f08" + string(trace) + "0",
			Name: "invoke_agent a", Kind: 1, Start: start, End: start.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
		}
	}
	parked := root("r_parked", '1', at, map[string]any{"weft.run.pending": int64(2)})
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: []obsdb.Span{parked}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0018 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	resumer := root("r_resume", '2', at.Add(2*time.Second), map[string]any{
		"weft.resumed_from": "r_parked", "weft.resume.calls": "c1=approved,c2=denied", "tenant": "acme",
	})
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{resumer}}); err != nil {
		t.Fatal(err)
	}
	p, err := db.Run(ctx, "r_parked")
	if err != nil {
		t.Fatal(err)
	}
	if p.ResumedBy != "r_resume" || p.ResumedFrom != "" || p.Pending != 2 {
		t.Errorf("parked after 0019: resumed_by %q, resumed_from %q, pending %d; want r_resume, none, 2", p.ResumedBy, p.ResumedFrom, p.Pending)
	}
	r, err := db.Run(ctx, "r_resume")
	if err != nil {
		t.Fatal(err)
	}
	if r.ResumedFrom != "r_parked" || r.ResumedBy != "" {
		t.Errorf("resume after 0019: resumed_from %q, resumed_by %q; want r_parked, none", r.ResumedFrom, r.ResumedBy)
	}
	if len(r.Meta) != 1 || r.Meta["tenant"] != "acme" {
		t.Errorf("resume's meta after 0019 = %v, want only the caller's {tenant: acme} (the link keys are contract)", r.Meta)
	}
}

// A database at 0019 holding a parent run and its Subagent child —
// rows and spans written before idx_parent_run existed — opens through
// 0020 on a live server (U9.1-fix): the index exists, materialized
// over the parts written before it, and the run tree's reads answer
// those rows: ChildRuns names the child, RunTreeSpans holds both runs'
// spans keyed by run, the child under its parent.
func TestUpgradeTo0020ReadsRunTrees(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0020 upgrade needs a clickhouse server (README has the one-line container recipe)")
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
	for v := 1; v <= 19; v++ {
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
	const trace = "0102030405060708090a0b0c0d0e0f50"
	span := func(spanID, parentSpan, run, parentRun string, start time.Time) obsdb.Span {
		attrs := map[string]any{"weft.run.id": run, "gen_ai.operation.name": "invoke_agent"}
		if parentRun != "" {
			attrs["weft.parent.run.id"] = parentRun
		}
		return obsdb.Span{TraceID: trace, SpanID: spanID, ParentSpanID: parentSpan, Name: "invoke_agent a", Kind: 1,
			Start: start, End: start.Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "svc"}}
	}
	if err := (&DB{conn: conn}).Write(ctx, obsdb.Batch{Spans: []obsdb.Span{
		span("0a0b0c0d0e0f0901", "", "r_up", "", at),
		span("0a0b0c0d0e0f0902", "0a0b0c0d0e0f0901", "r_up/0/c", "r_up", at.Add(time.Millisecond)),
	}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open a 0019 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	var idx uint64
	if err := db.(*DB).conn.QueryRow(ctx, `SELECT count() FROM system.data_skipping_indices
		WHERE database = currentDatabase() AND table = 'weft_runs' AND name = 'idx_parent_run'`).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Errorf("idx_parent_run after 0020: %d, want 1", idx)
	}
	kids, err := db.(obsdb.TreeReader).ChildRuns(ctx, []string{"r_up"})
	if err != nil || len(kids["r_up"]) != 1 || kids["r_up"][0].ID != "r_up/0/c" {
		t.Errorf("ChildRuns after 0020 = %v, %v; want r_up/0/c", kids, err)
	}
	tree, err := db.(obsdb.TreeReader).RunTreeSpans(ctx, "r_up")
	if err != nil || len(tree.Spans) != 2 || tree.Spans[0].RunID != "r_up" || tree.Spans[1].RunID != "r_up/0/c" ||
		tree.Spans[1].ParentRunID != "r_up" || tree.Truncated || len(tree.Missing) != 0 {
		t.Errorf("RunTreeSpans after 0020 = %+v, %v; want r_up's span and its child's under it", tree, err)
	}
}
