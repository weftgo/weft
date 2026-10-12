package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
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
	if version != 10 {
		t.Errorf("migration max after the upgrade = %d, want 10 (through 0010 root ended)", version)
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
