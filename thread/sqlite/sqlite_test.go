package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
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

// The session-level table: the turn machinery's promises that depend
// on what the backend stores — the mixed batch's resume join, a
// queued send restored after a restart — on a file database and on
// ":memory:".
func TestConformanceTurns(t *testing.T) {
	t.Run("file", func(t *testing.T) { threadtest.RunTurns(t, openFile) })
	t.Run("memory", func(t *testing.T) {
		threadtest.RunTurns(t, func(t *testing.T) thread.Storage {
			st, err := sqlite.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			return st
		})
	})
}

// The one-writer sub-table: two Storages over one file are the
// in-process shape of two processes — the second writer is ErrLocked,
// and Release hands the session over.
func TestConformanceTwoWriters(t *testing.T) {
	threadtest.RunTwoWriters(t, func(t *testing.T) (thread.Storage, thread.Storage) {
		path := filepath.Join(t.TempDir(), "sessions.db")
		first, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		second, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return first, second
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
		ID: "e_1", Created: time.Now().UTC(), Message: core.User("held"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(ctx, "s_lock", thread.MessageEntry{
		ID: "e_2", Created: time.Now().UTC(), Message: core.User("second writer"),
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
		ID: "e_1", Created: created.Add(time.Second), Message: core.User("durable"),
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

// A thread file whose versions table (schema_migrations, the name a
// pre-rename binary wrote; thread_migrations after) is ahead of this
// binary fails Open with ErrNewerSchema: a database written by a newer
// weft never runs with nothing said (the store's rule, shared here).
func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, created TEXT NOT NULL, header TEXT NOT NULL);
		 CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
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
// is already committed, and under the default policy already fsynced.
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

// The fsync-policy options are honoured through the shared
// vocabulary: a Session driving a flush cadence gets the same answers
// from every backend (the policy itself is pinned by
// TestFsyncPolicyIsTheSynchronousLevel).
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
		ID: "e_1", Created: time.Now().UTC(), Message: core.User("committed"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, entries, _, err := st.Load(ctx, "s_cadence"); err != nil || len(entries) != 1 {
		t.Errorf("append under FsyncOnFlush: %d entries, err %v", len(entries), err)
	}
}

// An Append after an injected torn row removes it first — the writer's
// repair, the rule jsonl follows with a truncate: the new entry takes
// the torn row's place in the order, the session loads clean, and the
// repair is logged. sqlite's own writes cannot tear (an append is one
// transaction), so the torn row exists only through threadtest's
// Inject; the rule is kept identical anyway, so the conformance table
// has one answer for every backend. Before it, the appended row sat
// behind the torn one and every later Load failed with ErrCorrupt.
func TestAppendAfterTornRepairs(t *testing.T) {
	ctx := context.Background()
	var logged bytes.Buffer
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "torn.db"),
		thread.OpenLogger(slog.New(slog.NewTextHandler(&logged, nil))))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_aftertorn", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	inj := st.(threadtest.RawInjector)
	if err := inj.Inject(ctx, "s_aftertorn", []byte(`{"type":"message","id":"e_t`)); err != nil {
		t.Fatal(err)
	}
	if _, _, report, err := st.Load(ctx, "s_aftertorn"); err != nil || report == nil || report.Torn != 2 {
		t.Fatalf("before the repair: report %+v, err %v; want Torn=2", report, err)
	}
	if err := st.Append(ctx, "s_aftertorn", thread.MessageEntry{
		ID: "e_2", Created: time.Now().UTC(), Message: core.User("after the crash"),
	}); err != nil {
		t.Fatal(err)
	}
	_, entries, report, err := st.Load(ctx, "s_aftertorn")
	if err != nil || report != nil || len(entries) != 1 {
		t.Fatalf("after the repair: %d entries, report %+v, err %v; want the one complete entry and a clean load", len(entries), report, err)
	}
	if !strings.Contains(logged.String(), "removed a torn tail") || !strings.Contains(logged.String(), "session=s_aftertorn") {
		t.Errorf("the repair was not reported:\n%s", logged.String())
	}
}

// A torn row with rows behind it is not something this backend writes
// — its write path removes a torn row before appending — so a database
// that holds one was written by something else, and Load says so:
// ErrCorrupt naming the line, never a silent skip.
func TestTornRowMidSessionIsCorrupt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "midtorn.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_midtorn", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO entries (session, seq, line, torn) VALUES
		('s_midtorn', 0, '{"type":"mess', 1),
		('s_midtorn', 1, '{"type":"message","id":"e_2","created":"2026-09-29T00:00:00Z","message":{"role":"user","content":[]}}', 0)`); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = st.Load(ctx, "s_midtorn")
	var ce *thread.CorruptError
	if !errors.Is(err, thread.ErrCorrupt) || !errors.As(err, &ce) || ce.Line != 2 {
		t.Fatalf("Load over a torn row mid-session: err = %v, want CorruptError on line 2", err)
	}
}

// A database path is a file name, taken literally: '?', '#' and '%'
// are characters of the name, not URI syntax. Unescaped, the '?'
// started the DSN's query — the database landed in a file named for
// the part before it, and the pragmas after it were lost.
func TestOpenPathWithURICharacters(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "what?now#100%.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_named", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("the database is not in the file the caller named: %v (size %d)", err, fi.Size())
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if !strings.HasPrefix(n.Name(), "what?now#100%.db") {
			t.Errorf("a stray file beside the database: %q", n.Name())
		}
	}
	again, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := again.List(ctx, thread.Query{}); err != nil || p.Total != 1 {
		t.Errorf("reopen: total %d, err %v", p.Total, err)
	}
}

// Migration 0003 on a database written before it: the sessions and
// their locks survive, the title is re-derived under the non-empty
// rule, a keyset page works, and a watcher over an old session (the
// empty generation) still sees it replaced.
func TestMigration0003OnExistingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, created TEXT NOT NULL, header TEXT NOT NULL, title TEXT NOT NULL DEFAULT '');
		CREATE INDEX sessions_created ON sessions(created DESC);
		CREATE TABLE entries (
		session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL, line TEXT NOT NULL, torn INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session, seq));
		CREATE TABLE session_locks (
		session TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		host TEXT NOT NULL, owner TEXT NOT NULL, pid INTEGER NOT NULL, taken TEXT NOT NULL);
		CREATE TABLE thread_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO thread_migrations (version, applied_at) VALUES (1, 'x'), (2, 'x')`); err != nil {
		t.Fatal(err)
	}
	// Two sessions sharing one creation time; the first was retitled
	// and then had a metadata-only info entry (an empty title) — which
	// migration 0002's rule had stored as its title — and a line that
	// is not JSON at all.
	for _, id := range []string{"s_a", "s_b"} {
		if _, err := db.Exec(`INSERT INTO sessions (id, created, header, title) VALUES (?,?,?,'')`,
			id, "2026-09-29T12:00:00.000000000Z",
			`{"type":"session","weft":1,"id":"`+id+`","created":"2026-09-29T12:00:00Z"}`); err != nil {
			t.Fatal(err)
		}
	}
	for seq, line := range []string{
		`{"type":"info","id":"e_1","created":"2026-09-29T00:00:00Z","title":"kept title"}`,
		`{"type":"info","id":"e_2","created":"2026-09-29T00:00:00Z","meta":{"k":"v"}}`,
		`not json at all`,
	} {
		if _, err := db.Exec(`INSERT INTO entries (session, seq, line, torn) VALUES ('s_a',?,?,0)`, seq, line); err != nil {
			t.Fatal(err)
		}
	}
	// A lock row from before the migration, its holder long dead.
	if _, err := db.Exec(`INSERT INTO session_locks (session, host, owner, pid, taken) VALUES ('s_b', ?, 'gone/owner', 1073741824, 'x')`,
		hostname(t)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open over a schema-2 database: %v", err)
	}
	if p, err := st.List(ctx, thread.Query{TitleSearch: "kept"}); err != nil || p.Total != 1 || p.Sessions[0].ID != "s_a" {
		t.Errorf("the re-derived title: %+v, err %v; want s_a under its last non-empty title", p, err)
	}
	first, err := st.List(ctx, thread.Query{Limit: 1})
	if err != nil || len(first.Sessions) != 1 || first.Sessions[0].ID != "s_b" || first.Total != 2 {
		t.Fatalf("first keyset page: %+v, err %v", first, err)
	}
	next, err := st.List(ctx, thread.Query{Limit: 1, Before: first.Sessions[0].Created, BeforeID: first.Sessions[0].ID})
	if err != nil || len(next.Sessions) != 1 || next.Sessions[0].ID != "s_a" {
		t.Fatalf("second keyset page through the tie: %+v, err %v", next, err)
	}
	// The old lock row's dead holder is taken over.
	if err := st.Append(ctx, "s_b", thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: core.User("x")}); err != nil {
		t.Errorf("Append over a pre-migration lock row whose holder is dead: %v", err)
	}
}

func hostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
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

// A session header written by a newer weft (envelope ahead of this
// build) fails Load as ErrNewerFormat — the class the format rules
// promise for it (thread.ErrNewerFormat names the header) — not as
// line-1 corruption, which callers could not branch on.
func TestNewerEnvelopeHeaderIsNewerFormat(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "newer-header.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_newer_header", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	newer := `{"type":"session","weft":2,"id":"s_newer_header","created":"2026-09-29T00:00:00Z"}`
	if _, err := db.Exec(`UPDATE sessions SET header = ? WHERE id = ?`, newer, "s_newer_header"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Load(ctx, "s_newer_header"); !errors.Is(err, thread.ErrNewerFormat) {
		t.Errorf("Load on a newer envelope: err = %v, want ErrNewerFormat", err)
	}
	// The session is invisible to List — the documented envelope rule
	// (thread.ErrNewerFormat: "such a session is invisible to an older
	// List; Load names why it cannot open it").
	if p, err := st.List(ctx, thread.Query{}); err != nil || p.Total != 0 {
		t.Errorf("List over a newer envelope: total %d, err %v, want 0", p.Total, err)
	}
}

// Load reads one snapshot: the header and the entries a Load returns
// come from one instant of the database. The churn rebuilds the session
// generation after generation, each generation's header (its Meta) and
// entries (their text) carrying the generation number; a load that
// reads its header before a rebuild and its entries after it answers a
// header from one generation with entries from another — the mixed
// state no committed instant ever held, a read torn across two
// snapshots. A load may legitimately see ErrNotFound (the delete won)
// or a just-created header with no entries yet (Create and Append are
// two commits); what it may never see is the mix.
func TestLoadReadsOneSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const id = "s_snap"
	const generations = 60
	// The filler widens the gap between a Load's header read and its
	// entries read — the header's JSON decodes between them — so a
	// rebuild that commits inside the gap is provoked, not prayed for.
	meta := map[string]string{"gen": "0"}
	for k := 0; k < 20000; k++ {
		meta[fmt.Sprintf("filler%05d", k)] = "x"
	}
	seed := func(gen int) error {
		if err := st.Delete(ctx, id); err != nil && !errors.Is(err, thread.ErrNotFound) {
			return err
		}
		meta["gen"] = fmt.Sprint(gen)
		if err := st.Create(ctx, thread.Header{
			ID: id, Created: time.Now().UTC(), Meta: meta,
		}); err != nil {
			return err
		}
		return st.Append(ctx, id,
			thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: core.User(fmt.Sprintf("gen %d", gen))},
			thread.MessageEntry{ID: "e_2", Created: time.Now().UTC(), Message: core.User(fmt.Sprintf("gen %d", gen))})
	}
	if err := seed(0); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { // the churn: rebuild the session, generation after generation
		defer close(done)
		for gen := 1; gen <= generations; gen++ {
			if err := seed(gen); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() { // the reader: every answer must be one generation's
		for {
			h, entries, _, err := st.Load(ctx, id)
			if err != nil {
				select {
				case <-done:
					return
				default:
					continue
				}
			}
			if len(entries) == 0 {
				continue // the legitimate just-created instant
			}
			got := entries[0].(thread.MessageEntry).Message.Text()
			if want := "gen " + h.Meta["gen"]; got != want {
				t.Errorf("a torn load: header of generation %s answered entries %q — two reads, two snapshots", h.Meta["gen"], got)
				return
			}
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	<-done
	time.Sleep(50 * time.Millisecond) // let the reader see the last generation
}

// The Watch capability: the shared conformance table runs under
// TestConformanceFile and TestConformanceMemory (Run adds RunWatch for
// a thread.Watcher — including the consumer that writes inside the
// loop, which the single working connection used to deadlock); this
// test adds the backend's own shape — the tail reads through WAL while
// another handle keeps writing, the cross-process shape.
func TestWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail.db")
	writer, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := writer.Create(ctx, thread.Header{ID: "s_tail", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	watch, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	seq, err := watch.(thread.Watcher).Watch(wctx, "s_tail", "")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for e, err := range seq {
			if err != nil {
				close(got)
				return
			}
			got <- e.(thread.MessageEntry).Message.Text()
		}
	}()
	if err := writer.Append(ctx, "s_tail", thread.MessageEntry{
		ID: "e_w1", Created: time.Now().UTC(), Message: core.User("committed while watched"),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case txt := <-got:
		if txt != "committed while watched" {
			t.Errorf("tailed %q", txt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tail never saw the other handle's commit")
	}
	cancel()
}

// Migration 0002's backfill: a database as it stood at schema 1 —
// titles living only in info entries, no column — gains the derived
// title once, on Open, and the column keeps up with later appends: the
// last info entry wins.
func TestTitleBackfillAndMaintenance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "title.db")
	// A schema-1 database, hand-built: the sessions and entries tables
	// migration 0001 defined, migration 0002 not yet applied.
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
		return fmt.Sprintf(`{"type":"info","id":%q,"created":"2026-09-29T00:00:00Z","title":%q}`, id, title)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, created, header) VALUES (?,?,?)`,
		"s_title", "2026-09-29T12:00:00.000000000Z",
		`{"type":"session","weft":1,"id":"s_title","created":"2026-09-29T12:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	for seq, line := range []string{info("e_t1", "the old title"), info("e_t2", "the new title")} {
		if _, err := db.Exec(`INSERT INTO entries (session, seq, line, torn) VALUES (?,?,?,0)`,
			"s_title", seq, line); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := sqlite.Open(path) // migration 0002 runs and backfills
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.List(ctx, thread.Query{TitleSearch: "new title"})
	if err != nil || p.Total != 1 {
		t.Errorf("after the backfill, TitleSearch 'new title': total %d, err %v, want 1", p.Total, err)
	}
	if p, err = st.List(ctx, thread.Query{TitleSearch: "old title"}); err != nil || p.Total != 0 {
		t.Errorf("after the backfill, TitleSearch 'old title': total %d, err %v, want 0 — the last info entry wins", p.Total, err)
	}
	// And the column keeps up with appends after the migration.
	if err := st.Append(ctx, "s_title", thread.InfoEntry{
		ID: thread.NewEntryID(), Created: time.Now().UTC(), Title: "the maintained title",
	}); err != nil {
		t.Fatal(err)
	}
	if p, err = st.List(ctx, thread.Query{TitleSearch: "maintained"}); err != nil || p.Total != 1 {
		t.Errorf("maintained TitleSearch: total %d, err %v, want 1", p.Total, err)
	}
}
