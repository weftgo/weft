package sqlite_test

// The S5 migrations tests (ADR 0024): this module owns its versions
// table, thread_migrations — a file a pre-rename binary wrote, whose
// table was the goose-shaped schema_migrations, is moved across by one
// ALTER TABLE on Open, and only when it is a thread file (the sessions
// table exists): a store database pointed at the same Open keeps its
// own tracking table and its rows whatever happens. Plus the S5 List
// row on this backend: a session created with PublicID is found by
// List{Meta} from the header, the create-time layer.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/sqlite"

	// The tests reach into the file directly to fake legacy and store
	// shapes — the same driver Open registers.
	_ "modernc.org/sqlite"
)

// tableExists reports whether the file at path holds a table by that
// name, and tableCount the rows in it.
func tableExists(t *testing.T, path, name string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// rowsOf returns the rows of a table, as flat strings for comparison.
func rowsOf(t *testing.T, path, table string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rs, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		cols, err := rs.Columns()
		if err != nil {
			t.Fatal(err)
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := ""
		for _, v := range vals {
			b, _ := v.([]byte)
			row += string(b) + "|"
		}
		out = append(out, row)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// buildLegacySchema1 writes a schema-1 thread database by hand — the
// tables migration 0001 defined, the versions table under its
// pre-rename name, version 1 recorded — with one session holding two
// info entries, the shape TestTitleBackfillAndMaintenance pins
// elsewhere.
func buildLegacySchema1(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, created TEXT NOT NULL, header TEXT NOT NULL);
		CREATE INDEX sessions_created ON sessions(created DESC);
		CREATE TABLE entries (
		session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL, line TEXT NOT NULL, torn INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session, seq));
		CREATE TABLE session_locks (
		session TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		host TEXT NOT NULL, owner TEXT NOT NULL, pid INTEGER NOT NULL, taken TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations (version, applied_at) VALUES (1, '2026-09-29T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	info := func(id, title string) string {
		return `{"type":"info","id":"` + id + `","created":"2026-09-29T00:00:00Z","title":"` + title + `"}`
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, created, header) VALUES (?,?,?)`,
		"s_old", "2026-09-29T12:00:00.000000000Z",
		`{"type":"session","weft":1,"id":"s_old","created":"2026-09-29T12:00:00Z","meta":{"weft.public_id":"share-old"}}`); err != nil {
		t.Fatal(err)
	}
	for seq, line := range []string{info("e_t1", "the old title"), info("e_t2", "the new title")} {
		if _, err := db.Exec(`INSERT INTO entries (session, seq, line, torn) VALUES (?,?,?,0)`,
			"s_old", seq, line); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// A file written before the rename opens, is renamed — one ALTER
// TABLE, the versions carried across — and the migrations pick up
// where the recorded version left off (S5).
func TestLegacyMigrationsTableRenamed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	buildLegacySchema1(t, path)

	st, err := sqlite.Open(path) // the rename, then migrations 0002 and 0003
	if err != nil {
		t.Fatalf("Open on a legacy file: %v", err)
	}
	if tableExists(t, path, "schema_migrations") {
		t.Error("the legacy versions table still exists after Open")
	}
	if !tableExists(t, path, "thread_migrations") {
		t.Fatal("thread_migrations missing after Open")
	}
	if got := rowsOf(t, path, "thread_migrations"); len(got) != 3 {
		t.Errorf("thread_migrations rows = %v, want the carried version 1 plus the applied 0002 and 0003", got)
	}
	// The data survived, and the header's create-time public id still
	// finds the session (S5's List row, on a renamed file).
	p, err := st.List(ctx, thread.Query{Meta: map[string]string{"weft.public_id": "share-old"}})
	if err != nil || p.Total != 1 || len(p.Sessions) != 1 || p.Sessions[0].ID != "s_old" {
		t.Errorf("List by the legacy public id: page %+v, err %v", p, err)
	}
	// Migration 0002 ran on top of the carried version: the backfilled
	// title answers the title filter.
	if p, err = st.List(ctx, thread.Query{TitleSearch: "new title"}); err != nil || p.Total != 1 {
		t.Errorf("TitleSearch after the rename: total %d, err %v, want 1", p.Total, err)
	}
	// The second Open is a no-op, not a second rename.
	if _, err = sqlite.Open(path); err != nil {
		t.Fatalf("reopen after the rename: %v", err)
	}
	if tableExists(t, path, "schema_migrations") {
		t.Error("a reopen resurrected the legacy table")
	}
}

// buildStoreFile writes a store-shaped database by hand: the runs and
// run_events tables the store-era 0001 that shared the file defined
// (trimmed to the columns this test reads) and the goose-shaped
// versions table holding store versions — 6, ahead of this binary's
// highest, which the pre-rename code would have refused on.
func buildStoreFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE runs (
		id TEXT PRIMARY KEY, agent TEXT NOT NULL, status TEXT NOT NULL);
		CREATE TABLE run_events (
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL, event TEXT NOT NULL,
		PRIMARY KEY (run_id, seq));
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations (version, applied_at) VALUES (1, '2026-09-28T00:00:00Z');
		INSERT INTO schema_migrations (version, applied_at) VALUES (6, '2026-09-30T00:00:00Z');
		INSERT INTO runs (id, agent, status) VALUES ('run_1', 'support', 'succeeded');
		INSERT INTO run_events (run_id, seq, event) VALUES ('run_1', 1, '{"type":"run_start"}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// A store file is never touched: no sessions table, so no rename —
// the store keeps its versions table (ahead of ours though it is) and
// its rows, while this Open adds this module's own tables beside them
// (the share-a-file rule, S5 / §3.4).
func TestStoreFileNotTouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	buildStoreFile(t, path)
	beforeMigrations := rowsOf(t, path, "schema_migrations")
	beforeRuns := rowsOf(t, path, "runs")
	beforeEvents := rowsOf(t, path, "run_events")

	if _, err := sqlite.Open(path); err != nil {
		t.Fatalf("Open on a store file: %v — its version number must not be read as ours", err)
	}
	if !tableExists(t, path, "schema_migrations") {
		t.Error("the store's versions table was renamed or dropped")
	}
	if got := rowsOf(t, path, "schema_migrations"); len(got) != len(beforeMigrations) {
		t.Errorf("schema_migrations rows changed: %v -> %v", beforeMigrations, got)
	}
	if got := rowsOf(t, path, "runs"); len(got) != len(beforeRuns) {
		t.Errorf("runs rows changed: %v -> %v", beforeRuns, got)
	}
	if got := rowsOf(t, path, "run_events"); len(got) != len(beforeEvents) {
		t.Errorf("run_events rows changed: %v -> %v", beforeEvents, got)
	}
	if !tableExists(t, path, "thread_migrations") {
		t.Error("thread_migrations missing — the module must own its own table beside the store's")
	}
}

// A file holding both a thread and an old store schema is untouched on
// the store side: the one versions table moves to this module's name
// (that is the migration), and the store's tables and rows come
// through the open unchanged.
func TestMixedFileRenamesStoreSideUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.db")
	buildLegacySchema1(t, path)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE runs (
		id TEXT PRIMARY KEY, agent TEXT NOT NULL, status TEXT NOT NULL);
		CREATE TABLE run_events (
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL, event TEXT NOT NULL,
		PRIMARY KEY (run_id, seq));
		INSERT INTO runs (id, agent, status) VALUES ('run_1', 'support', 'succeeded');
		INSERT INTO run_events (run_id, seq, event) VALUES ('run_1', 1, '{"type":"run_start"}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	beforeRuns := rowsOf(t, path, "runs")
	beforeEvents := rowsOf(t, path, "run_events")

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open on a mixed file: %v", err)
	}
	if tableExists(t, path, "schema_migrations") {
		t.Error("the shared legacy table still exists after Open")
	}
	if !tableExists(t, path, "thread_migrations") {
		t.Error("thread_migrations missing after Open")
	}
	if got := rowsOf(t, path, "runs"); len(got) != len(beforeRuns) {
		t.Errorf("the store's runs rows changed: %v -> %v", beforeRuns, got)
	}
	if got := rowsOf(t, path, "run_events"); len(got) != len(beforeEvents) {
		t.Errorf("the store's run_events rows changed: %v -> %v", beforeEvents, got)
	}
	// The thread side still answers: the session the legacy file held
	// loads through the renamed file.
	if _, _, report, err := st.Load(context.Background(), "s_old"); err != nil || report != nil {
		t.Errorf("Load after the rename: report %+v, err %v", report, err)
	}
}

// List finds a session by its create-time public id on this backend
// too, and a SetInfo-added public id never matches (S5's List row,
// third backend).
func TestListByPublicIDFile(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "publicid.db"))
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	with := thread.Header{ID: "s_pid_a", Created: created, Meta: map[string]string{"weft.public_id": "share-a"}}
	if err := st.Create(ctx, with); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_pid_b", Created: created}); err != nil {
		t.Fatal(err)
	}
	p, err := st.List(ctx, thread.Query{Meta: map[string]string{"weft.public_id": "share-a"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if p.Total != 1 || len(p.Sessions) != 1 || p.Sessions[0].ID != "s_pid_a" {
		t.Fatalf("List by share-a = %+v, want exactly s_pid_a", p)
	}
	if err := st.Append(ctx, "s_pid_b", thread.InfoEntry{
		ID: "e_rot", Created: created, Meta: map[string]string{"weft.public_id": "share-b"},
	}); err != nil {
		t.Fatal(err)
	}
	if p, err = st.List(ctx, thread.Query{Meta: map[string]string{"weft.public_id": "share-b"}}); err != nil || p.Total != 0 {
		t.Errorf("List by an info-entry public id: total %d, err %v, want 0 — the header is the filter", p.Total, err)
	}
}
