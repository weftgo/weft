package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A file written before ADR 0028 — migrations 0001 and 0002 applied by
// hand from the embedded bodies, one run row and one record in it —
// opens through 0003: the rows survive, the new run columns read their
// defaults (instructions_hash ” is the not_recorded reading), and the
// recorded version is 3.
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
	if version != 3 {
		t.Errorf("migration max = %d, want 3", version)
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
