package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// A file written before ADR 0028 — migrations 0001 and 0002 applied by
// hand from the embedded bodies, one run row and one record in it —
// opens through 0005: the rows survive, the new run columns read their
// defaults (instructions_hash ” is the not_recorded reading, which the
// read API reports), a saved experiment reads with no command, and the
// recorded version is 5.
func TestUpgradeFrom0002(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{1, 2} {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if _, err := raw.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, finished_ok, steps)
		VALUES ('old1', 'support', 100, 200, 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO records (run_id, kind, pos, time_ns, event_type, step, body, attrs)
		VALUES ('old1', 'messages', 0, 100, '', 0, '[]', '{}')`); err != nil {
		t.Fatal(err)
	}
	// A saved experiment from before 0005: it reads back with the
	// command's fields absent (plan F5.1).
	if _, err := raw.Exec(`INSERT INTO experiments (id, name, agent, created_ns, updated_ns, variants, inputs)
		VALUES ('exp_old', 'old', 'support', 100, 100, '[{"key":"A","overrides":{"model":"m"}}]', '[{"key":"1","text":"hi"}]')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0002 file: %v", err)
	}
	exp, err := db.Experiment(context.Background(), "exp_old")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Name != "old" || len(exp.Variants) != 1 || string(exp.Variants[0].Overrides) != `{"model":"m"}` ||
		exp.Engine != "" || exp.FromStep != nil || exp.TranscriptEdits != nil {
		t.Errorf("pre-0005 experiment = %+v, want the definition and no command", exp)
	}
	// Through the read API: ADR 0028 §10's reading table says
	// not_recorded, and so do the prompt and tools readers.
	run, err := db.Run(context.Background(), "old1")
	if err != nil {
		t.Fatal(err)
	}
	if run.InstructionsHash != "" || run.CatalogHash != "" || run.RequestCount != 0 || run.RequestsHole() != obsdb.HoleNotRecorded || run.Derived {
		t.Errorf("upgraded run = %q %q %d hole %q derived %v; want the defaults, not_recorded, not derived (0008's default)",
			run.InstructionsHash, run.CatalogHash, run.RequestCount, run.RequestsHole(), run.Derived)
	}
	var hole *obsdb.HoleError
	if _, err := db.Tools(context.Background(), "old1", "any"); !errors.Is(err, obsdb.ErrNotFound) ||
		!errors.As(err, &hole) || hole.Hole != obsdb.HoleNotRecorded {
		t.Errorf("Tools of a pre-0028 run = %v; want ErrNotFound, not_recorded", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	ctx := context.Background()
	var version int
	if err := raw.QueryRowContext(ctx, `SELECT MAX(version) FROM obsdb_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Errorf("migration max = %d, want 11 (through 0011 derived root_ended)", version)
	}
	var agent, instructions, catalog string
	var steps, requests int
	if err := raw.QueryRowContext(ctx, `SELECT agent, steps, instructions_hash, catalog_hash, request_count
		FROM runs WHERE run_id = 'old1'`).Scan(&agent, &steps, &instructions, &catalog, &requests); err != nil {
		t.Fatal(err)
	}
	if agent != "support" || steps != 2 || instructions != "" || catalog != "" || requests != 0 {
		t.Errorf("upgraded run row = %q, %d, %q, %q, %d; want support, 2 and the defaults", agent, steps, instructions, catalog, requests)
	}
	var records, step int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*), MAX(step) FROM records WHERE run_id = 'old1'`).Scan(&records, &step); err != nil {
		t.Fatal(err)
	}
	if records != 1 || step != 0 {
		t.Errorf("upgraded records = %d (step %d), want the one row at step 0", records, step)
	}
}

// A file written before search (migrations 0001–0003, a run stored
// through them) opens through 0004 with that run indexed by the Go
// step the migration runs: searchable by prompt, answer, error and tool
// name, its row carrying its prompt preview (the turn's own: an
// input-flagged record's last user message) — and a compaction view's
// text, whatever its reason's type, is no search field.
func TestUpgradeFrom0003IndexesStoredRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{1, 2, 3} {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if _, err := raw.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, failed, err) VALUES ('old1', 'support', 100, 200, 1, 'card declined')`,
		`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns) VALUES ('old2', 'support', 300, 400)`,
		`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns) VALUES ('old3', 'support', 500, 600)`,
		`INSERT INTO records (run_id, kind, pos, time_ns, event_type, step, body, attrs) VALUES
			('old1', 'messages', 0, 100, '', 0, '[{"role":"user","content":[{"type":"text","text":"refund my order"}]}]', '{}'),
			('old1', 'messages', 1, 110, '', 0, '[{"role":"assistant","content":[{"type":"text","text":"working on it"}]}]', '{}'),
			('old1', 'messages', 2, 120, '', 0, '[{"role":"assistant","content":[{"type":"text","text":"a compacted summary"}]}]', '{"weft.messages.reason":"compacted"}'),
			('old1', 'messages', 3, 130, '', 0, '[{"role":"assistant","content":[{"type":"text","text":"a numeric reason"}]}]', '{"weft.messages.reason":7}'),
			('old3', 'messages', 1, 510, '', 0, '[{"role":"tool","content":[{"type":"tool_result","call_id":"c","content":"ok"}]},{"role":"user","content":[{"type":"text","text":"resumed question"}]}]', '{}'),
			('old3', 'messages', 0, 500, '', 0, '[{"role":"user","content":[{"type":"text","text":"parked question"}]},{"role":"assistant","content":[{"type":"tool_call","id":"c","name":"x","args":{}}]}]', '{"weft.messages.input":true}'),
			('old2', 'messages', 0, 300, '', 0, '[{"role":"user","content":[{"type":"text","text":"turn one question"}]},{"role":"assistant","content":[{"type":"text","text":"ok"}]},{"role":"user","content":[{"type":"text","text":"turn two question"}]}]', '{"weft.messages.input":true}'),
			('old1', 'event', 1, 105, 'tool_start', 0, '{"type":"tool_start","name":"issue_refund"}', '{}'),
			('old2', 'event', 0, 300, 'run_start', 0, '{"type":"run_start"}', '{}')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0003 file: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	srch := db.(obsdb.Searcher)
	for text, want := range map[string]string{
		"refund my": "old1", "working": "old1", "declined": "old1", "issue_refund": "old1", "compacted summary": "",
		"numeric reason": "",                     // a reason of any type is a view: the writer's reading (obsdb.DeriveRecord)
		"turn two":       "old2", "turn one": "", // the input record's last user message is the turn's prompt
		"resumed question": "old3", "parked question": "", // a resume's new prompt, in the next record
	} {
		page, err := srch.Search(ctx, obsdb.SearchQuery{Text: `"` + text + `"`})
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(page.Runs) == 1 {
			got = page.Runs[0].ID
		}
		if got != want || len(page.Runs) > 1 {
			t.Errorf("search %q after the upgrade = %d runs (%q), want %q", text, len(page.Runs), got, want)
		}
	}
	run, err := db.Run(ctx, "old1")
	if err != nil {
		t.Fatal(err)
	}
	if run.PromptPreview != "refund my order" {
		t.Errorf("upgraded run's prompt preview = %q", run.PromptPreview)
	}
	if run2, err := db.Run(ctx, "old2"); err != nil || run2.PromptPreview != "turn two question" {
		t.Errorf("upgraded multi-turn run's prompt preview = %q, %v; want the turn's own", run2.PromptPreview, err)
	}
	page, err := srch.Search(ctx, obsdb.SearchQuery{Tool: "issue_refund"})
	if err != nil || len(page.Runs) != 1 {
		t.Errorf("tool filter after the upgrade = %v, %v", page.Runs, err)
	}
}

// A file at 0006 — every migration before the command store applied
// from the embedded bodies, a run and an experiment in it — opens
// through 0007 in place: the rows survive, the four command store
// tables exist and take a write, and the recorded version is 7.
func TestUpgradeFrom0006AddsCommandStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 6; v++ {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		tx, err := raw.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if post := postMigrations[v]; post != nil {
			if err := post(tx); err != nil {
				t.Fatalf("migration %d: %v", v, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns) VALUES ('old1', 'support', 100, 200)`,
		`INSERT INTO experiments (id, name, created_ns, updated_ns) VALUES ('exp_1', 'tone', 100, 100)`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"commands", "run_routes", "breakpoints", "runtimes"} {
		var n int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("a 0006 file already has %s", table)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0006 file: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if run, err := db.Run(ctx, "old1"); err != nil || run.Agent != "support" {
		t.Errorf("upgraded run = %+v, %v", run, err)
	}
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
	var version int
	if err := db.(*DB).reads.QueryRowContext(ctx, `SELECT MAX(version) FROM obsdb_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Errorf("migration max after the upgrade = %d, want 11 (through 0011 derived root_ended)", version)
	}
}

// A file written at 0008 opens through 0009, which backfills from the
// spans already stored (docs/record-contract.md §6): a weft run whose
// invoke_agent span was stored and whose records never were reads
// succeeded (root_ended), with weft_version from the span's resource and
// manifest_hash from its attributes; a run whose root failed stays
// failed; a run with only a chat span and a stale last-seen stays
// interrupted; a version the records gave is kept.
func TestUpgradeTo0009BackfillsRootEnded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at8.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 8; v++ {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		tx, err := raw.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if post := postMigrations[v]; post != nil {
			if err := post(tx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, steps) VALUES
			('r_tracer', 'a', 100, 200, 2), ('r_err', 'a', 100, 200, 1), ('r_chat', 'a', 100, 200, 0), ('r_kept', 'a', 100, 200, 1)`,
		`UPDATE runs SET failed = 1, err = 'boom' WHERE run_id = 'r_err'`,
		`UPDATE runs SET weft_version = 'v0.14.0' WHERE run_id = 'r_kept'`,
		`INSERT INTO spans (trace_id, span_id, parent_span_id, name, kind, start_ns, end_ns, status_code, status_message,
			service, run_id, step, tool_seq, session_id, attrs, resource, events) VALUES
			('t1', 's1', '', 'invoke_agent a', 1, 100, 200, 1, '', 'svc', 'r_tracer', -1, -1, '',
			 '{"gen_ai.operation.name":"invoke_agent","weft.run.id":"r_tracer","weft.manifest.hash":"mh1"}', '{"weft.version":"v0.15.0"}', '[]'),
			('t2', 's2', '', 'invoke_agent a', 1, 100, 200, 2, 'boom', 'svc', 'r_err', -1, -1, '',
			 '{"weft.run.id":"r_err","weft.version":"v0.15.0"}', '{}', '[]'),
			('t3', 's3', '', 'chat m', 1, 100, 200, 0, '', 'svc', 'r_chat', 0, -1, '',
			 '{"gen_ai.operation.name":"chat","weft.run.id":"r_chat"}', '{}', '[]'),
			('t4', 's4', '', 'invoke_agent a', 1, 100, 200, 0, '', 'svc', 'r_kept', -1, -1, '',
			 '{"gen_ai.operation.name":"invoke_agent","weft.run.id":"r_kept","weft.version":"v0.15.0"}', '{}', '[]')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0008 file: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, c := range []struct {
		id, version, manifest string
		rootEnded             bool
		status                obsdb.Status
	}{
		{"r_tracer", "v0.15.0", "mh1", true, obsdb.StatusSucceeded},
		{"r_err", "v0.15.0", "", true, obsdb.StatusFailed}, // the name decides when the attribute is absent
		{"r_chat", "", "", false, obsdb.StatusInterrupted},
		{"r_kept", "v0.14.0", "", true, obsdb.StatusSucceeded},
	} {
		run, err := db.Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if run.RootEnded != c.rootEnded || run.Status != c.status || run.WeftVersion != c.version || run.ManifestHash != c.manifest {
			t.Errorf("%s after 0009 = root_ended %v, %s, version %q, manifest %q; want %v, %s, %q, %q",
				c.id, run.RootEnded, run.Status, run.WeftVersion, run.ManifestHash, c.rootEnded, c.status, c.version, c.manifest)
		}
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{Status: obsdb.StatusSucceeded})
	if err != nil || page.Total != 2 {
		t.Errorf("succeeded filter after 0009 = %d, %v; want r_tracer and r_kept (statusExpr reads root_ended)", page.Total, err)
	}
}

// A file written at 0009 opens through 0010, which backfills stopped_by
// and cancel_cause from what is already stored (docs/record-contract.md
// §6, U3.2): a failed run's from its invoke_agent span alone, a
// succeeded run's from its run_finish body when its span carries none,
// the larger when the two differ; and takes the two keys out of meta,
// where MetaOf's catch-all had put them, leaving caller metadata alone.
func TestUpgradeTo0010BackfillsStoppedBy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at9.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 9; v++ {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		tx, err := raw.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if post := postMigrations[v]; post != nil {
			if err := post(tx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, root_ended, failed, meta) VALUES
			('r_cancel', 'a', 100, 200, 1, 1, '{"weft.run.stopped_by":"canceled","weft.cancel.cause":"studio.cancel","tenant":"acme"}'),
			('r_record', 'a', 100, 200, 0, 0, '{}'),
			('r_none', 'a', 100, 200, 1, 1, '{"tenant":"acme"}')`,
		`UPDATE runs SET finished_ok = 1 WHERE run_id = 'r_record'`,
		`INSERT INTO spans (trace_id, span_id, parent_span_id, name, kind, start_ns, end_ns, status_code, status_message,
			service, run_id, step, tool_seq, session_id, attrs, resource, events) VALUES
			('t1', 's1', '', 'invoke_agent a', 1, 100, 200, 2, 'context canceled', 'svc', 'r_cancel', -1, -1, '',
			 '{"gen_ai.operation.name":"invoke_agent","weft.run.id":"r_cancel","weft.run.stopped_by":"canceled","weft.cancel.cause":"studio.cancel"}', '{}', '[]'),
			('t3', 's3', '', 'invoke_agent a', 1, 100, 200, 2, 'upstream down', 'svc', 'r_none', -1, -1, '',
			 '{"gen_ai.operation.name":"invoke_agent","weft.run.id":"r_none"}', '{}', '[]')`,
		`INSERT INTO records (run_id, kind, pos, time_ns, event_type, body, attrs) VALUES
			('r_record', 'event', 3, 150, 'run_finish', '{"type":"run_finish","steps":1,"stopped_by":"has_tool_call:submit"}', '{}')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0009 file: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, c := range []struct {
		id, stoppedBy, cause string
		meta                 map[string]string
	}{
		{"r_cancel", "canceled", "studio.cancel", map[string]string{"tenant": "acme"}},
		{"r_record", "has_tool_call:submit", "", nil},
		{"r_none", "", "", map[string]string{"tenant": "acme"}},
	} {
		run, err := db.Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if run.StoppedBy != c.stoppedBy || run.CancelCause != c.cause || len(run.Meta) != len(c.meta) || run.Meta["tenant"] != c.meta["tenant"] {
			t.Errorf("%s after 0010 = stopped_by %q, cancel_cause %q, meta %v; want %q, %q, %v",
				c.id, run.StoppedBy, run.CancelCause, run.Meta, c.stoppedBy, c.cause, c.meta)
		}
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{StoppedBy: "has_tool_call:"})
	if err != nil || page.Total != 1 || page.Runs[0].ID != "r_record" {
		t.Errorf("stopped_by=has_tool_call: after 0010 = %+v, %v; want r_record (the prefix form)", page, err)
	}
}

// A file written at 0010 opens through 0011, which sets root_ended on
// the derived rows 0009's backfill skipped (docs/record-contract.md §6's
// root_ended row): every derived run reads it, whenever it was written;
// a weft run's flag is untouched.
func TestUpgradeTo0011DerivedRootEnded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at10.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 10; v++ {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		tx, err := raw.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if post := postMigrations[v]; post != nil {
			if err := post(tx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, finished_ok, derived, root_ended) VALUES
		('otel-old', 'a', 100, 200, 1, 1, 0), ('r_weft', 'a', 100, 200, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0010 file: %v", err)
	}
	defer func() { _ = db.Close() }()
	for id, want := range map[string]bool{"otel-old": true, "r_weft": false} {
		run, err := db.Run(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.RootEnded != want {
			t.Errorf("%s after 0011: root_ended %v, want %v", id, run.RootEnded, want)
		}
	}
}
