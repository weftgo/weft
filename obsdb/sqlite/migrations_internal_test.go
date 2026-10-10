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
// opens through 0004: the rows survive, the new run columns read their
// defaults (instructions_hash ” is the not_recorded reading, which the
// read API reports), and the recorded version is 4.
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
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0002 file: %v", err)
	}
	// Through the read API: ADR 0028 §10's reading table says
	// not_recorded, and so do the prompt and tools readers.
	run, err := db.Run(context.Background(), "old1")
	if err != nil {
		t.Fatal(err)
	}
	if run.InstructionsHash != "" || run.CatalogHash != "" || run.RequestCount != 0 || run.RequestsHole() != obsdb.HoleNotRecorded {
		t.Errorf("upgraded run = %q %q %d hole %q; want the defaults, not_recorded",
			run.InstructionsHash, run.CatalogHash, run.RequestCount, run.RequestsHole())
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
	if version != 4 {
		t.Errorf("migration max = %d, want 4 (through 0004 search)", version)
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
