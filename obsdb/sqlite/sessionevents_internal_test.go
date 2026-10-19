package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestUpgradeTo0022IndexesSessionEvents (U12.2): a file at 0021 holding
// thread records and a compaction marker (plain records then) opens at
// 0022 with them in session_events — the session's, in time order, the
// event name derived from the body (none for a body that does not
// parse) — and a record of another kind, or one carrying no session id,
// left out; a runless thread record an older writer put in other_logs
// moves to session_events (other_logs keeps the app's own lines), at the
// position the writer gives it, so a retried batch is a no-op.
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
	if _, err := raw.Exec(`INSERT INTO other_logs (time_ns, trace_id, span_id, severity, event_name, body, service, attrs, resource) VALUES
		(450, '', '', 9, 'weft.thread.grant.revoke', '{"kind":"grant.revoke","session":"s_up","entry":"e9"}', 'svc', '{"weft.record":"thread","weft.run.id":"","weft.session.id":"s_up","weft.public_id":"pub_up","weft.content":"none"}', '{}'),
		(460, '', '', 9, '', 'an app line', 'svc', '{"weft.session.id":"s_up"}', '{}')`); err != nil {
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
	want := []string{"weft.thread.approval.request||none", "weft.thread.approval.decision|pub_up|full", "weft.compaction||", "||", "weft.thread.grant.revoke|pub_up|none"}
	if !slices.Equal(got, want) || page.Total != 5 {
		t.Errorf("session events after 0022 = %q (total %d), want %q", got, page.Total, want)
	}
	var bodies []string
	rs, err := db.(*DB).reads.Query(`SELECT body FROM other_logs ORDER BY time_ns`)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var b string
		if err := rs.Scan(&b); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	_ = rs.Close()
	if !slices.Equal(bodies, []string{"an app line"}) {
		t.Errorf("other_logs after 0022 = %q, want the app's line alone (the thread record moved)", bodies)
	}
	// The moved record, retried through the writer, lands on its own key.
	retry := obsdb.Record{Time: time.Unix(0, 450).UTC(), EventName: "weft.thread.grant.revoke", Severity: 9, Service: "svc",
		Body:  `{"kind":"grant.revoke","session":"s_up","entry":"e9"}`,
		Attrs: map[string]any{"weft.record": "thread", "weft.run.id": "", "weft.session.id": "s_up", "weft.public_id": "pub_up", "weft.content": "none"}}
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{retry}}); err != nil {
		t.Fatal(err)
	}
	if again, err := db.(obsdb.SessionEventReader).SessionEvents(context.Background(), obsdb.SessionEventQuery{SessionID: "s_up"}); err != nil || again.Total != 5 {
		t.Errorf("after a retried write of the moved record: total %d (%v), want 5", again.Total, err)
	}
}
