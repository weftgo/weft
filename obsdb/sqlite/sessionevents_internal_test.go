package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// TestUpgradeTo0022IndexesSessionEvents (U12.2): a file at 0021 holding
// thread records and a compaction marker (plain records then) opens at
// 0022 with them in session_events — the session's, in time order, the
// event name derived from the body (none for a body that does not
// parse) — and a record of another kind, or one carrying no session id,
// left out.
func TestUpgradeTo0022IndexesSessionEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at21.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, v := range sortedVersions() {
		if v > 21 {
			break
		}
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
	if _, err := raw.Exec(`INSERT INTO records (run_id, kind, pos, time_ns, body, attrs) VALUES
		('s_up-t1', 'thread', 7, 200, '{"kind":"approval.decision","session":"s_up","entry":"e4"}', '{"weft.record":"thread","weft.run.id":"s_up-t1","weft.session.id":"s_up","weft.public_id":"pub_up","weft.content":"full"}'),
		('s_up-t1', 'thread', 3, 100, '{"kind":"approval.request","session":"s_up","entry":"e3"}', '{"weft.record":"thread","weft.run.id":"s_up-t1","weft.session.id":"s_up","weft.content":"none"}'),
		('s_up-t1', 'compaction', 9, 300, '{"scope":"session","hash":"h"}', '{"weft.record":"compaction","weft.run.id":"s_up-t1","weft.session.id":"s_up"}'),
		('s_up-t1', 'thread', 11, 400, 'not json', '{"weft.record":"thread","weft.run.id":"s_up-t1","weft.session.id":"s_up"}'),
		('s_up-t1', 'thread', 12, 500, '{"kind":"label"}', '{"weft.record":"thread","weft.run.id":"s_up-t1"}'),
		('s_up-t1', 'event', 0, 50, '{"type":"run_start"}', '{"weft.record":"event","weft.session.id":"s_up"}')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open a 0021 file: %v", err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.(obsdb.SessionEventReader).SessionEvents(context.Background(), obsdb.SessionEventQuery{SessionID: "s_up"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Events {
		got = append(got, e.Name+"|"+e.PublicID+"|"+e.Content)
	}
	want := []string{"weft.thread.approval.request||none", "weft.thread.approval.decision|pub_up|full", "weft.compaction||", "||"}
	if !slices.Equal(got, want) || page.Total != 4 {
		t.Errorf("session events after 0022 = %q (total %d), want %q", got, page.Total, want)
	}
}
