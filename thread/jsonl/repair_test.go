package jsonl_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// seedSession creates a session with one entry through a throwaway
// Storage and returns the file's path. The seeding instance releases
// the session, so the test's own instances are its first writers — the
// shape of a process that exited.
func seedSession(t *testing.T, dir, id string) string {
	t.Helper()
	ctx := context.Background()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: id, Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, id, thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: weft.User("one")}); err != nil {
		t.Fatal(err)
	}
	if err := st.(thread.Releaser).Release(ctx, id); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, id+".jsonl")
}

// appendRaw writes bytes to the end of a file behind the backend's
// back — what a writer that died mid-write left.
func appendRaw(t *testing.T, path string, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A torn tail a previous writer left is removed by the next writer
// before its first append: the new entry is its own line, later loads
// are clean and hold every complete entry, the file ends on a newline,
// and the repair is reported through the storage's logger — once, as a
// warning naming the session. Before this rule the append glued itself
// onto the torn bytes and every later Load failed with ErrCorrupt.
func TestTornTailRepairedBeforeAppend(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := seedSession(t, dir, "s_torn")
	appendRaw(t, path, `{"type":"message","created":"2026-09-28T12:00:02.5`)

	var logged bytes.Buffer
	st, err := jsonl.Open(dir, thread.OpenLogger(slog.New(slog.NewTextHandler(&logged, nil))))
	if err != nil {
		t.Fatal(err)
	}
	// Until a writer arrives the tail stands, and Load reports it.
	if _, entries, report, err := st.Load(ctx, "s_torn"); err != nil || len(entries) != 1 || report == nil || report.Torn != 3 {
		t.Fatalf("before the repair: %d entries, report %+v, err %v", len(entries), report, err)
	}
	if logged.Len() != 0 {
		t.Fatalf("a read logged a repair: %s", logged.String())
	}
	for _, id := range []string{"e_2", "e_3"} {
		if err := st.Append(ctx, "s_torn", thread.MessageEntry{ID: id, Created: time.Now().UTC(), Message: weft.User(id)}); err != nil {
			t.Fatalf("Append over a torn tail: %v", err)
		}
	}
	if n := strings.Count(logged.String(), "removed a torn tail"); n != 1 ||
		!strings.Contains(logged.String(), "level=WARN") || !strings.Contains(logged.String(), "session=s_torn") {
		t.Errorf("the repair must be logged once, as a warning naming the session; got %d:\n%s", n, logged.String())
	}

	// The next process: a clean load with all three complete entries.
	fresh, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, entries, report, err := fresh.Load(ctx, "s_torn")
	if err != nil {
		t.Fatalf("Load after the repair: %v", err)
	}
	if report != nil || len(entries) != 3 {
		t.Fatalf("after the repair: %d entries, report %+v; want 3 and a clean load", len(entries), report)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte("\n")) || bytes.Count(raw, []byte("\n")) != 4 || bytes.Contains(raw, []byte("12:00:02.5")) {
		t.Errorf("the file is not header + 3 whole lines with the torn bytes gone:\n%s", raw)
	}
}

// The same repair under Salvage: without it the glued line was skipped
// and the whole post-crash append vanished with it. With the repair
// there is nothing to salvage.
func TestTornTailRepairedUnderSalvage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := seedSession(t, dir, "s_torn")
	appendRaw(t, path, `{"type":"mess`)
	st, err := jsonl.Open(dir, thread.Salvage(), thread.OpenLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_torn", thread.MessageEntry{ID: "e_2", Created: time.Now().UTC(), Message: weft.User("after the crash")}); err != nil {
		t.Fatal(err)
	}
	_, entries, report, err := st.Load(ctx, "s_torn")
	if err != nil || report != nil || len(entries) != 2 {
		t.Fatalf("under Salvage: %d entries, report %+v, err %v; want both entries and nothing skipped", len(entries), report, err)
	}
}

// A file with no complete header line is not a session a writer may
// append to: Append fails with ErrCorrupt on line 1 and leaves the
// bytes alone — truncating them would turn a corpse into an empty file
// that reads as nothing at all.
func TestAppendToTornHeaderIsCorrupt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"s_tornheader": `{"type":"session","weft":1,"id":"s_tor`,
		"s_empty":      "",
	} {
		path := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		err := st.Append(ctx, name, thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: weft.User("x")})
		var ce *thread.CorruptError
		if !errors.Is(err, thread.ErrCorrupt) || !errors.As(err, &ce) || ce.Line != 1 {
			t.Errorf("%s: Append = %v, want a CorruptError on line 1", name, err)
		}
		if raw, _ := os.ReadFile(path); string(raw) != content {
			t.Errorf("%s: the file changed: %q", name, raw)
		}
		// The failed append holds nothing: the corpse can be deleted.
		if err := st.Delete(ctx, name); err != nil {
			t.Errorf("%s: Delete after the refused append: %v", name, err)
		}
	}
}

// Watch on a file with no complete header line — empty, or a torn
// first line — fails with ErrCorrupt naming line 1 before the first
// yield. It used to index past an empty slice and panic.
func TestWatchWithoutHeaderIsCorrupt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"s_empty":      "",
		"s_tornheader": `{"type":"session","weft":1,"id":"s_tor`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := st.(thread.Watcher).Watch(ctx, name, "")
		var ce *thread.CorruptError
		if !errors.Is(err, thread.ErrCorrupt) || !errors.As(err, &ce) || ce.Line != 1 {
			t.Errorf("%s: Watch = %v, want a CorruptError on line 1", name, err)
		}
		// List with a title search walks the same file and skips it.
		if p, err := st.List(ctx, thread.Query{TitleSearch: "x"}); err != nil || p.Total != 0 {
			t.Errorf("%s: List = total %d, err %v", name, p.Total, err)
		}
	}
}

// Under Salvage a malformed line is skipped by the tail as Load skips
// it; without it the stream ends with a CorruptError (the conformance
// table's row). Either way the entries around it arrive.
func TestWatchSalvageSkipsMalformed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := seedSession(t, dir, "s_salvage")
	appendRaw(t, path, "this is not json\n")
	st, err := jsonl.Open(dir, thread.Salvage())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_salvage", thread.MessageEntry{ID: "e_2", Created: time.Now().UTC(), Message: weft.User("two")}); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	seq, err := st.(thread.Watcher).Watch(wctx, "s_salvage", "")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for e, err := range seq {
		if err != nil {
			t.Fatalf("the salvaging tail failed: %v", err)
		}
		texts = append(texts, e.(thread.MessageEntry).Message.Text())
		if len(texts) == 2 {
			break
		}
	}
	if texts[0] != "one" || texts[1] != "two" {
		t.Errorf("salvaging tail yielded %v, want [one two]", texts)
	}
}

// NoLock is the caller's promise in place of the backend's check: two
// Storages over one directory both write, neither is refused — the
// shape a platform without file locks runs in, by explicit choice.
func TestNoLockDoesNotRefuseASecondWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := jsonl.Open(dir, thread.NoLock())
	if err != nil {
		t.Fatal(err)
	}
	second, err := jsonl.Open(dir, thread.NoLock())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Create(ctx, thread.Header{ID: "s_nolock", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for i, st := range []thread.Storage{first, second, first} {
		if err := st.Append(ctx, "s_nolock", thread.MessageEntry{
			ID: thread.NewEntryID(), Created: time.Now().UTC(), Message: weft.User("x"),
		}); err != nil {
			t.Fatalf("append %d under NoLock: %v", i, err)
		}
	}
	if _, entries, report, err := second.Load(ctx, "s_nolock"); err != nil || report != nil || len(entries) != 3 {
		t.Errorf("under NoLock: %d entries, report %+v, err %v", len(entries), report, err)
	}
}
