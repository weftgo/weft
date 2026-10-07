package sqlite

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// synchronousLevel reads the working connection's synchronous pragma:
// 1 is NORMAL, 2 is FULL.
func synchronousLevel(t *testing.T, st thread.Storage) int {
	t.Helper()
	var level int
	if err := st.(*backend).db.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&level); err != nil {
		t.Fatal(err)
	}
	return level
}

// The fsync policy is SQLite's synchronous level: the default — every
// append durable before it returns — is FULL, which fsyncs the log at
// each commit; FsyncOnFlush is NORMAL, which leaves the sync to Flush.
// The backend used to run NORMAL whatever was asked, so "durable
// before anything replies on it" held against a dead process and not
// against a dead machine.
func TestFsyncPolicyIsTheSynchronousLevel(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []thread.OpenOption
		want int
	}{
		{"default", nil, 2},
		{"FsyncEveryAppend", []thread.OpenOption{thread.FsyncEveryAppend()}, 2},
		{"FsyncOnFlush", []thread.OpenOption{thread.FsyncOnFlush()}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open(filepath.Join(t.TempDir(), "sessions.db"), tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := synchronousLevel(t, st); got != tc.want {
				t.Errorf("PRAGMA synchronous = %d, want %d", got, tc.want)
			}
			// The level is the connection's for good: a write does not
			// change it.
			ctx := context.Background()
			if err := st.Create(ctx, thread.Header{ID: "s_sync", Created: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if got := synchronousLevel(t, st); got != tc.want {
				t.Errorf("PRAGMA synchronous after a write = %d, want %d", got, tc.want)
			}
		})
	}
}

// Under FsyncOnFlush, Flush is the sync point: it checkpoints the
// write-ahead log into the database file (SQLite fsyncs the log
// first, then the file). Seen from outside: before Flush the main
// file alone does not hold the entry — it is in the log — and after
// Flush it does.
func TestFlushCheckpointsUnderFsyncOnFlush(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")
	st, err := Open(path, thread.FsyncOnFlush())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_ckpt", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.(thread.Flusher).Flush(ctx, "s_ckpt"); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_ckpt", thread.MessageEntry{
		ID: "e_1", Created: time.Now().UTC(), Message: core.User("in the log"),
	}); err != nil {
		t.Fatal(err)
	}
	// mainFileEntries loads the session from a copy of the main
	// database file alone — no -wal beside it: what the file itself
	// holds.
	mainFileEntries := func(name string) int {
		t.Helper()
		copyDir := filepath.Join(dir, name)
		if err := os.Mkdir(copyDir, 0o700); err != nil {
			t.Fatal(err)
		}
		src, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = src.Close() }()
		dst, err := os.Create(filepath.Join(copyDir, "sessions.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			t.Fatal(err)
		}
		other, err := Open(filepath.Join(copyDir, "sessions.db"))
		if err != nil {
			t.Fatal(err)
		}
		_, entries, _, err := other.Load(ctx, "s_ckpt")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	if n := mainFileEntries("before"); n != 0 {
		t.Fatalf("the main file holds %d entries before Flush: the append was not left in the log, the test proves nothing", n)
	}
	if err := st.(thread.Flusher).Flush(ctx, "s_ckpt"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n := mainFileEntries("after"); n != 1 {
		t.Fatalf("the main file holds %d entries after Flush, want 1: Flush did not checkpoint", n)
	}
	// The default policy syncs at every commit and leaves the log to
	// SQLite: its Flush checkpoints nothing.
	strict, err := Open(filepath.Join(dir, "strict.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strict.(*backend).syncOnFlush {
		t.Error("the default policy defers its sync to Flush")
	}
}
