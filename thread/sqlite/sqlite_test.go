package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/sqlite"
	"github.com/weftgo/weft/thread/threadtest"

	// The test reaches into the file directly to fake a newer schema —
	// the same driver Open registers.
	_ "modernc.org/sqlite"
)

// openFile opens a backend on a fresh database file under t's temp dir.
func openFile(t *testing.T) thread.Storage {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The conformance table (ADR 0011 §5's executable promises) runs whole
// against a file database — corruption rows included, through Inject.
func TestConformanceFile(t *testing.T) {
	threadtest.Run(t, openFile)
}

// The same table against ":memory:": the in-process DSN branch answers
// with the same rules — the reference backends (Memory, jsonl) already
// pin that backends do not get to differ per location.
func TestConformanceMemory(t *testing.T) {
	threadtest.Run(t, func(t *testing.T) thread.Storage {
		st, err := sqlite.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		return st
	})
}

// A second Storage over the same file — the same process shape of
// another writer — is ErrLocked on Append and Delete, while Load and
// List keep working: readers never lock (ADR 0011 §5).
func TestSecondWriterInOneProcess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Create(ctx, thread.Header{ID: "s_lock", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := a.Append(ctx, "s_lock", thread.MessageEntry{
		ID: "e_1", Created: time.Now().UTC(), Message: weft.User("held"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(ctx, "s_lock", thread.MessageEntry{
		ID: "e_2", Created: time.Now().UTC(), Message: weft.User("second writer"),
	}); !errors.Is(err, thread.ErrLocked) {
		t.Errorf("second writer Append: err = %v, want ErrLocked", err)
	}
	if err := b.Delete(ctx, "s_lock"); !errors.Is(err, thread.ErrLocked) {
		t.Errorf("second writer Delete: err = %v, want ErrLocked", err)
	}
	if _, entries, report, err := b.Load(ctx, "s_lock"); err != nil || len(entries) != 1 || report != nil {
		t.Errorf("Load under a foreign hold: %d entries, report %+v, err %v — readers never lock", len(entries), report, err)
	}
	if p, err := b.List(ctx, thread.Query{}); err != nil || p.Total != 1 {
		t.Errorf("List under a foreign hold: total %d, err %v", p.Total, err)
	}
	// The holder deletes: the lock row goes with the session, and the
	// name is free for the second handle.
	if err := a.Delete(ctx, "s_lock"); err != nil {
		t.Fatal(err)
	}
	if err := b.Create(ctx, thread.Header{ID: "s_lock", Created: time.Now().UTC()}); err != nil {
		t.Fatalf("Create after the holder released: %v", err)
	}
}

// A fresh Open of an existing file reads what the old one wrote — the
// reopen shape every restart is — and migrations on the current schema
// are a no-op, not a rewrite.
func TestReopenReadsWhatWasWritten(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 29, 9, 30, 0, 0, time.UTC)
	if err := first.Create(ctx, thread.Header{ID: "s_reopen", Created: created, Meta: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	if err := first.Append(ctx, "s_reopen", thread.MessageEntry{
		ID: "e_1", Created: created.Add(time.Second), Message: weft.User("durable"),
	}); err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h, entries, report, err := second.Load(ctx, "s_reopen")
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Errorf("clean reopen reported repairs: %+v", report)
	}
	if h.ID != "s_reopen" || h.Meta["k"] != "v" || len(entries) != 1 ||
		entries[0].(thread.MessageEntry).Message.Text() != "durable" {
		t.Errorf("reopen: header %+v, %d entries — the file changed between opens", h, len(entries))
	}
}

// A schema_migrations ahead of this binary fails Open with
// ErrNewerSchema: a database written by a newer weft never runs with
// nothing said (the store's rule, shared here).
func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		 INSERT INTO schema_migrations (version, applied_at) VALUES (999, '2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(path); !errors.Is(err, sqlite.ErrNewerSchema) {
		t.Errorf("Open on a newer schema: err = %v, want ErrNewerSchema", err)
	}
}

// Salvage downgrades a malformed line to a skip reported in the
// LoadReport; the unknown kind and the newer version stay loud, salvage
// or not (ADR 0011 §5).
func TestSalvageSkipsMalformedLines(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "salvage.db"), thread.Salvage())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_salvage", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	inj := st.(threadtest.RawInjector)
	for _, raw := range []string{
		"{\"type\":\"message\",\"id\":\"e_1\",\"created\":\"2026-09-29T09:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"kept\"}]}}\n",
		"this is not json\n",
	} {
		if err := inj.Inject(ctx, "s_salvage", []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	_, entries, report, err := st.Load(ctx, "s_salvage")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].(thread.MessageEntry).Message.Text() != "kept" {
		t.Errorf("salvaged load: %d entries — the decodable line must survive", len(entries))
	}
	if report == nil || len(report.Skipped) != 1 || report.Skipped[0] != 3 {
		t.Errorf("report = %+v, want the malformed line 3 skipped", report)
	}
	// The loud rows never salvage.
	if err := inj.Inject(ctx, "s_salvage", []byte("{\"type\":\"approval\",\"id\":\"e_x\"}\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Load(ctx, "s_salvage"); !errors.Is(err, thread.ErrNewerFormat) {
		t.Errorf("unknown kind under Salvage: err = %v, want ErrNewerFormat", err)
	}
}

// The Flusher capability exists and answers existence: everything here
// is already committed, so Flush has nothing to buffer.
func TestFlusher(t *testing.T) {
	ctx := context.Background()
	st := openFile(t)
	flush, ok := st.(thread.Flusher)
	if !ok {
		t.Fatal("the sqlite backend does not implement thread.Flusher")
	}
	if err := flush.Flush(ctx, "s_missing"); !errors.Is(err, thread.ErrNotFound) {
		t.Errorf("Flush on a missing session: err = %v, want ErrNotFound", err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_flush", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := flush.Flush(ctx, "s_flush"); err != nil {
		t.Errorf("Flush on a held session: %v", err)
	}
}

// The fsync-policy options are accepted: the shared vocabulary stays
// portable across backends, and a Session driving a flush cadence gets
// the same answers from every one of them.
func TestFsyncOptionsAccepted(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "cadence.db"), thread.FsyncOnFlush())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_cadence", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_cadence", thread.MessageEntry{
		ID: "e_1", Created: time.Now().UTC(), Message: weft.User("committed"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, entries, _, err := st.Load(ctx, "s_cadence"); err != nil || len(entries) != 1 {
		t.Errorf("append under FsyncOnFlush: %d entries, err %v", len(entries), err)
	}
}

// Appends after an injected torn row corrupt the session, exactly as
// appending after a torn tail corrupts a jsonl file: the crash's bytes
// are mid-session now, and Load says so with the line.
func TestAppendAfterTornCorruptsLikeJsonl(t *testing.T) {
	ctx := context.Background()
	st := openFile(t)
	if err := st.Create(ctx, thread.Header{ID: "s_aftertorn", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	inj := st.(threadtest.RawInjector)
	if err := inj.Inject(ctx, "s_aftertorn", []byte(`{"type":"message","id":"e_t`)); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_aftertorn", thread.MessageEntry{
		ID: "e_2", Created: time.Now().UTC(), Message: weft.User("after the crash"),
	}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := st.Load(ctx, "s_aftertorn")
	var ce *thread.CorruptError
	if !errors.Is(err, thread.ErrCorrupt) || !errors.As(err, &ce) || ce.Line != 2 {
		t.Fatalf("Load over a torn line mid-session: err = %v, want CorruptError on line 2", err)
	}
}

// An empty database migrates on Open; a second Open over the migrated
// file is a no-op that still answers — the two states every deployment
// actually runs (first start, every later start).
func TestMigrationEmptyThenExisting(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migrate.db")
	for round := 0; round < 2; round++ {
		st, err := sqlite.Open(path)
		if err != nil {
			t.Fatalf("open round %d: %v", round, err)
		}
		id := fmt.Sprintf("s_migrate%d", round)
		if err := st.Create(ctx, thread.Header{ID: id, Created: time.Now().UTC()}); err != nil {
			t.Fatalf("create round %d: %v", round, err)
		}
	}
	third, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := third.List(ctx, thread.Query{}); err != nil || p.Total != 2 {
		t.Errorf("List after two opens: total %d, err %v — a no-op migration must not lose rows", p.Total, err)
	}
}
