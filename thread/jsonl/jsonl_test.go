package jsonl_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/threadtest"
)

// open is the conformance factory: a fresh directory per subtest.
func open(t *testing.T) thread.Storage {
	t.Helper()
	st, err := jsonl.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// Conformance runs the shared table — including the loud rows, which
// the RawInjector hooks unlock for a backend that holds real files,
// and the Watch table, which Run adds for a thread.Watcher.
func TestConformance(t *testing.T) {
	threadtest.Run(t, open)
}

// The session-level table: the turn machinery's promises that depend
// on what the backend stores — the mixed batch's resume join, a
// queued send restored after a restart.
func TestConformanceTurns(t *testing.T) {
	threadtest.RunTurns(t, open)
}

// The one-writer sub-table: two Storages over one directory are the
// in-process shape of two processes — the second writer is ErrLocked,
// and Release hands the session over.
func TestConformanceTwoWriters(t *testing.T) {
	threadtest.RunTwoWriters(t, func(t *testing.T) (thread.Storage, thread.Storage) {
		dir := t.TempDir()
		first, err := jsonl.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		second, err := jsonl.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return first, second
	})
}

// The file shape is the format (ADR 0011 §2, §5): the directory 0700,
// the session file 0600 named <id>.jsonl, the header its first line
// and every entry one newline-terminated line after it.
func TestFileShape(t *testing.T) {
	// A directory Open itself creates is exactly 0700; one the caller
	// already had keeps its own permissions.
	dir := filepath.Join(t.TempDir(), "sessions")
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := thread.Header{ID: "s_shape", Created: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	if err := st.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, h.ID, thread.MessageEntry{
		ID:      "e_shape1",
		Created: h.Created.Add(time.Second),
		Message: weft.User("hello"),
	}); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 700", perm)
	}
	path := filepath.Join(dir, "s_shape.jsonl")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("session file mode = %o, want 600", perm)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"session","weft":1,"id":"s_shape","created":"2026-09-28T12:00:00Z"}` + "\n" +
		`{"type":"message","id":"e_shape1","created":"2026-09-28T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"
	if string(raw) != want {
		t.Errorf("file bytes:\n got %s\nwant %s", raw, want)
	}
	// Reopening an existing directory keeps the caller's permissions.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := jsonl.Open(dir); err != nil {
		t.Fatal(err)
	}
	if di, _ = os.Stat(dir); di.Mode().Perm() != 0o755 {
		t.Errorf("reopen changed the directory mode to %o", di.Mode().Perm())
	}
}

// The committed format-1 golden session file loads through the real
// backend — the "read every golden" promise exercised end to end.
func TestLoadsGoldenSessionFile(t *testing.T) {
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "format1", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s_01J8X9M2K7QW4R5N8T6V2B3C4D.jsonl"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, entries, report, err := st.Load(context.Background(), "s_01J8X9M2K7QW4R5N8T6V2B3C4D")
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Errorf("the golden session reported repairs: %+v", report)
	}
	if h.ID != "s_01J8X9M2K7QW4R5N8T6V2B3C4D" || len(entries) != 9 {
		t.Errorf("header %+v, %d entries; want the golden session and its 9 kinds", h, len(entries))
	}
}

// The one-writer rule's shape (ADR 0011 §5): a second writer — another
// Storage instance, this process or another — gets ErrLocked on its
// first write; readers never lock, and a deleted session's lock is
// released for the next creator.
func TestOneWriterPerSession(t *testing.T) {
	dir := t.TempDir()
	first, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := thread.Header{ID: "s_lock", Created: time.Now().UTC()}
	if err := first.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := first.Append(ctx, h.ID, thread.MessageEntry{ID: "e_lock", Message: weft.User("one")}); err != nil {
		t.Fatal(err)
	}
	err = second.Append(ctx, h.ID, thread.MessageEntry{ID: "e_lock2", Message: weft.User("two")})
	if !errors.Is(err, thread.ErrLocked) {
		t.Fatalf("second writer: err = %v, want ErrLocked", err)
	}
	err = second.Delete(ctx, h.ID)
	if !errors.Is(err, thread.ErrLocked) {
		t.Fatalf("second deleter: err = %v, want ErrLocked", err)
	}
	// Readers never lock.
	if _, _, _, err := second.Load(ctx, h.ID); err != nil {
		t.Fatalf("a reader was locked out: %v", err)
	}
	if _, err := second.List(ctx, thread.Query{}); err != nil {
		t.Fatalf("List was locked out: %v", err)
	}
	// Create over a locked session file is an exists error, not a
	// replacement.
	if err := second.Create(ctx, h); err == nil || errors.Is(err, thread.ErrLocked) {
		t.Errorf("Create over the held file: err = %v, want a plain exists error", err)
	}
	// Delete releases; the next creator gets a clean slate.
	if err := first.Delete(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if err := second.Create(ctx, h); err != nil {
		t.Fatalf("Create after the lock was released: %v", err)
	}
}

// Salvage (ADR 0011 §5): malformed lines are skipped and reported; the
// unknown and the newer stay loud even under salvage; a torn tail is
// always a report, never an error.
func TestSalvage(t *testing.T) {
	dir := t.TempDir()
	strict, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	salvaging, err := jsonl.Open(dir, thread.Salvage())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := thread.Header{ID: "s_salvage", Created: time.Now().UTC()}
	if err := strict.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := strict.Append(ctx, h.ID,
		thread.MessageEntry{ID: "e_k1", Message: weft.User("keep me")},
		thread.MessageEntry{ID: "e_k2", Message: weft.User("me too")},
	); err != nil {
		t.Fatal(err)
	}
	inj := strict.(threadtest.RawInjector)
	if err := inj.Inject(ctx, h.ID, []byte("{ this line is not json\n")); err != nil {
		t.Fatal(err)
	}
	if err := inj.Inject(ctx, h.ID, []byte(`{"type":"message","id":"e_k3","message":{"role":"user","content":[{"type":"text","text":"after"}]}}`+"\n")); err != nil {
		t.Fatal(err)
	}

	// Strict: the malformed line fails the load naming the line.
	_, _, _, err = strict.Load(ctx, h.ID)
	if !errors.Is(err, thread.ErrCorrupt) {
		t.Fatalf("strict load: err = %v, want ErrCorrupt", err)
	}
	var ce *thread.CorruptError
	if !errors.As(err, &ce) || ce.Line != 4 {
		t.Fatalf("strict load: err = %v, want a CorruptError carrying line 4", err)
	}
	// Salvage: the line is skipped and reported; the rest loads.
	_, entries, report, err := salvaging.Load(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("%d entries under salvage, want 3", len(entries))
	}
	if report == nil || len(report.Skipped) != 1 || report.Skipped[0] != 4 {
		t.Errorf("report = %+v, want Skipped=[4]", report)
	}
	// The unknown kind stays loud under salvage.
	if err := inj.Inject(ctx, h.ID, []byte(`{"type":"approval","id":"e_x"}`+"\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := salvaging.Load(ctx, h.ID); !errors.Is(err, thread.ErrNewerFormat) {
		t.Errorf("unknown kind under salvage: err = %v, want ErrNewerFormat", err)
	}
}

// List reads headers only, bounded: a file whose first megabyte holds
// no newline is skipped, and one corrupt file never blocks the others.
func TestListBoundedSkipsOversized(t *testing.T) {
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := thread.Header{ID: "s_listable", Created: time.Now().UTC()}
	if err := st.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	// An oversized non-session file: no newline in the first MiB.
	big := make([]byte, 1<<20+64)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(dir, "s_oversized.jsonl"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	// A corrupt-header file and a non-session file beside them.
	if err := os.WriteFile(filepath.Join(dir, "s_corrupt.jsonl"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := st.List(ctx, thread.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 || len(p.Sessions) != 1 || p.Sessions[0].ID != "s_listable" {
		t.Errorf("List = %+v, want only s_listable", p)
	}
}

// Two goroutines on one Storage racing to a session's first write share
// the held file: the instance's own concurrency must never read as
// ErrLocked — that error belongs to the second writer in another
// process or another Storage instance (ADR 0011 §5's one-writer rule is
// about writers, not goroutines of one writer).
func TestConcurrentFirstTouchOneSession(t *testing.T) {
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const workers = 32
	const rounds = 25
	for r := 0; r < rounds; r++ {
		id := fmt.Sprintf("s_touch%02d", r)
		if err := st.Create(ctx, thread.Header{ID: id, Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				errs <- st.Append(ctx, id, thread.MessageEntry{
					ID:      fmt.Sprintf("e_touch%02d_%02d", r, w),
					Message: weft.User("first touch"),
				})
			}(w)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("round %d: %v", r, err)
			}
		}
		_, loaded, _, err := st.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded) != workers {
			t.Fatalf("round %d: %d entries, want %d", r, len(loaded), workers)
		}
	}
}

// Create racing its own session's first appends: an append that gets
// in before the header is durable must not be able to take the file's
// lock out from under Create (Create would fail spuriously and its
// cleanup would unlink the appender's file), and one that lands must
// land after the header. The contract: Create never fails against its
// own instance's appends; every append returns nil or — it ran before
// the session existed — ErrNotFound; and the load afterwards is clean,
// header first, holding exactly the appends that returned nil.
func TestCreateRacingAppends(t *testing.T) {
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const appenders = 8
	const rounds = 20
	for r := 0; r < rounds; r++ {
		id := fmt.Sprintf("s_race%02d", r)
		h := thread.Header{ID: id, Created: time.Now().UTC()}
		var wg sync.WaitGroup
		createErr := make(chan error, 1)
		appendErrs := make(chan error, appenders)
		wg.Add(1)
		go func() {
			defer wg.Done()
			createErr <- st.Create(ctx, h)
		}()
		for a := 0; a < appenders; a++ {
			wg.Add(1)
			go func(a int) {
				defer wg.Done()
				appendErrs <- st.Append(ctx, id, thread.MessageEntry{
					ID:      fmt.Sprintf("e_race%02d_%02d", r, a),
					Message: weft.User("racing"),
				})
			}(a)
		}
		wg.Wait()
		close(createErr)
		close(appendErrs)
		if err := <-createErr; err != nil {
			t.Fatalf("round %d: Create racing its own appends failed: %v", r, err)
		}
		landed := 0
		for err := range appendErrs {
			switch {
			case err == nil:
				landed++
			case errors.Is(err, thread.ErrNotFound):
				// Ran before the session existed — the honest answer.
			default:
				t.Fatalf("round %d: Append: %v, want nil or ErrNotFound", r, err)
			}
		}
		got, entries, report, err := st.Load(ctx, id)
		if err != nil {
			t.Fatalf("round %d: Load: %v", r, err)
		}
		if report != nil {
			t.Fatalf("round %d: the load reported repairs: %+v", r, report)
		}
		if got.ID != id || len(entries) != landed {
			t.Fatalf("round %d: %d entries for %d landed appends — a nil append was lost", r, len(entries), landed)
		}
	}
}

// Two Opens racing to create the same missing directory both succeed:
// the loser of os.Mkdir sees EEXIST for a directory that is exactly
// what it wanted.
func TestOpenConcurrentCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := jsonl.Open(dir)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("open: %v", err)
		}
	}
	if di, err := os.Stat(dir); err != nil || !di.IsDir() {
		t.Fatalf("stat %s: %v, want the directory", dir, err)
	}
}

// Inject is a test hook, not an escape hatch: an id that is not one
// path component is rejected like every other entry point, so the hook
// cannot reach a file outside the session directory.
func TestInjectValidatesID(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sessions")
	// A decoy one level up: without validation, ../escape names it.
	decoy := filepath.Join(base, "escape.jsonl")
	if err := os.WriteFile(decoy, []byte("do not touch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Create(ctx, thread.Header{ID: "s_inject", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	inj := st.(threadtest.RawInjector)
	for _, id := range []string{"../escape", "a/b", ""} {
		if err := inj.Inject(ctx, id, []byte("x")); err == nil {
			t.Errorf("Inject(%q) succeeded, want rejected", id)
		}
	}
	if raw, err := os.ReadFile(decoy); err != nil || string(raw) != "do not touch\n" {
		t.Errorf("the decoy file changed: %q, %v", raw, err)
	}
}

// Both durability policies produce identical files; FsyncOnFlush's
// appends complete at Flush (the Flusher capability), and Flush on an
// unknown session is ErrNotFound.
func TestSyncPolicies(t *testing.T) {
	ctx := context.Background()
	created := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	h := thread.Header{ID: "s_sync", Created: created}
	mk := func(t *testing.T, opts ...thread.OpenOption) (thread.Storage, []byte) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "sessions")
		st, err := jsonl.Open(dir, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Create(ctx, h); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, h.ID, thread.MessageEntry{ID: "e_s1", Created: created.Add(time.Second), Message: weft.User("durable")}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "s_sync.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return st, raw
	}
	_, eager := mk(t, thread.FsyncEveryAppend())
	lazy, lazyRaw := mk(t, thread.FsyncOnFlush())
	if string(eager) != string(lazyRaw) {
		t.Errorf("policies disagree on bytes:\n eager %s\n lazy  %s", eager, lazyRaw)
	}
	if err := lazy.(thread.Flusher).Flush(ctx, "s_sync"); err != nil {
		t.Errorf("Flush: %v", err)
	}
	if err := lazy.(thread.Flusher).Flush(ctx, "s_missing"); !errors.Is(err, thread.ErrNotFound) {
		t.Errorf("Flush on unknown session: err = %v, want ErrNotFound", err)
	}
}

// A session file whose header line carries a newer envelope fails Load
// as ErrNewerFormat — the class thread.ErrNewerFormat names the header
// for — not as line-1 corruption; the class is what a caller branches
// on. Found by the 4.1 review: sqlite answered the envelope correctly
// only after its own review fix, and the backends must not differ on a
// format rule.
func TestNewerEnvelopeHeaderIsNewerFormat(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_newer_header", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// A newer weft's first line, over the header this build wrote.
	newer := `{"type":"session","weft":2,"id":"s_newer_header","created":"2026-09-29T00:00:00Z"}` + "\n"
	file := filepath.Join(dir, "s_newer_header.jsonl")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Everything after this build's header line, kept under the newer one.
	if err := os.WriteFile(file, append([]byte(newer), raw[bytes.IndexByte(raw, '\n')+1:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Load(ctx, "s_newer_header"); !errors.Is(err, thread.ErrNewerFormat) {
		t.Errorf("Load on a newer envelope: err = %v, want ErrNewerFormat", err)
	}
}

// The Watch capability: the shared conformance table (TestConformance
// runs RunWatch, the backend being a thread.Watcher) covers order,
// exactly-once, the resume point and the loud failures; this test adds
// the backend's own shape — the tail sees appends from another handle
// over the same directory, the cross-process shape.
func TestWatch(t *testing.T) {
	dir := t.TempDir()
	first, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := first.Create(ctx, thread.Header{ID: "s_tail", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	watch, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	wctx, cancel := context.WithCancel(ctx)
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
	// The writer is another handle entirely — the two-process shape.
	if err := first.Append(ctx, "s_tail", thread.MessageEntry{
		ID: "e_w1", Created: time.Now().UTC(), Message: weft.User("from the other handle"),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case txt := <-got:
		if txt != "from the other handle" {
			t.Errorf("tailed %q", txt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tail never saw the other handle's append")
	}
	cancel()
}
