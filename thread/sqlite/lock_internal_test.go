package sqlite

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

func openBackend(t *testing.T, path string, opts ...thread.OpenOption) *backend {
	t.Helper()
	st, err := Open(path, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return st.(*backend)
}

func entry(id string) thread.Entry {
	return thread.MessageEntry{ID: id, Created: time.Now().UTC(), Message: weft.User(id)}
}

// otherProcess makes b look like a Storage opened by a different
// process: its own process token, the given pid and start time.
func otherProcess(b *backend, pid int, started string) *backend {
	b.proc = "another-process-" + b.owner
	b.pid = pid
	b.started = started
	return b
}

// The lock row's takeover rules, unit-level (the cross-process
// behavior — a real second process, a real kill — is crash_test.go's).
// A pid is not an identity: the row also carries the holder's process
// token and start time, and the rules use all three.
func TestLockTakeoverRules(t *testing.T) {
	ctx := context.Background()
	const holderPID, holderStart = 4242, "boot:1000"
	// setup creates a session held by a Storage that belongs to another
	// process (holderPID, started holderStart) and returns the path.
	setup := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "locks.db")
		holder := otherProcess(openBackend(t, path), holderPID, holderStart)
		if err := holder.Create(ctx, thread.Header{ID: "s_rules", Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		return path
	}
	answer := func(b *backend, alive bool, start string) *backend {
		b.alive = func(int) bool { return alive }
		b.startOf = func(int) string { return start }
		return b
	}

	t.Run("LiveHolderIsLocked", func(t *testing.T) {
		b := answer(openBackend(t, setup(t)), true, holderStart)
		if err := b.Append(ctx, "s_rules", entry("e_1")); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("a live holder, same start time: err = %v, want ErrLocked", err)
		}
	})
	t.Run("DeadPidIsTakenOver", func(t *testing.T) {
		var logged bytes.Buffer
		b := answer(openBackend(t, setup(t), thread.OpenLogger(slog.New(slog.NewTextHandler(&logged, nil)))), false, "")
		if err := b.Append(ctx, "s_rules", entry("e_1")); err != nil {
			t.Fatalf("a dead holder on this host must be taken over: %v", err)
		}
		if _, entries, _, err := b.Load(ctx, "s_rules"); err != nil || len(entries) != 1 {
			t.Errorf("after the takeover: %d entries, err %v", len(entries), err)
		}
		if !strings.Contains(logged.String(), "took over a dead writer's lock") || !strings.Contains(logged.String(), "session=s_rules") {
			t.Errorf("the takeover was not reported:\n%s", logged.String())
		}
	})
	t.Run("ReusedPidIsTakenOver", func(t *testing.T) {
		// The holder's pid is live — but the process wearing it started
		// at another time: it is not the process that took the lock.
		b := answer(openBackend(t, setup(t)), true, "boot:2000")
		if err := b.Append(ctx, "s_rules", entry("e_1")); err != nil {
			t.Fatalf("a reused pid must not keep a dead holder's session locked: %v", err)
		}
	})
	t.Run("UnknownStartTimeStaysLocked", func(t *testing.T) {
		// A live pid whose start time the platform cannot report cannot
		// be told from the holder: ErrLocked, the safe side.
		b := answer(openBackend(t, setup(t)), true, "")
		if err := b.Append(ctx, "s_rules", entry("e_1")); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("a live pid of unknown start: err = %v, want ErrLocked", err)
		}
	})
	t.Run("OwnPidFromAnEarlierProcessIsTakenOver", func(t *testing.T) {
		// The restarted container: same hostname, PID 1 again. The row
		// names our own pid — which is alive, it is us — under another
		// process's token. Liveness is never asked: before the process
		// token, kill(pid, 0) on our own pid answered "alive" and the
		// session stayed ErrLocked forever.
		path := filepath.Join(t.TempDir(), "restart.db")
		previous := otherProcess(openBackend(t, path), os.Getpid(), "boot:1")
		if err := previous.Create(ctx, thread.Header{ID: "s_rules", Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		b := openBackend(t, path)                        // the real pid, the real liveness check
		b.startOf = func(int) string { return "boot:1" } // even a matching start time does not save the old row
		if err := b.Append(ctx, "s_rules", entry("e_1")); err != nil {
			t.Fatalf("our own pid under an earlier process's token must be taken over: %v", err)
		}
	})
	t.Run("AnotherStorageInThisProcessIsLocked", func(t *testing.T) {
		// Two Storages in one process share pid, start time and process
		// token; only the owner differs — and the other one is exactly
		// as alive as this one. No liveness answer changes that.
		path := filepath.Join(t.TempDir(), "same.db")
		holder := openBackend(t, path)
		if err := holder.Create(ctx, thread.Header{ID: "s_rules", Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		b := answer(openBackend(t, path), false, "boot:other")
		if err := b.Append(ctx, "s_rules", entry("e_1")); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("a second Storage in one process: err = %v, want ErrLocked", err)
		}
		// Until the holder releases.
		if err := holder.Release(ctx, "s_rules"); err != nil {
			t.Fatal(err)
		}
		if err := b.Append(ctx, "s_rules", entry("e_1")); err != nil {
			t.Errorf("after the holder released: %v", err)
		}
	})
	t.Run("ForeignHostIsNeverJudgedDead", func(t *testing.T) {
		// A foreign host is never judged dead from here, even when the
		// pid is gone on this machine: the database would have to be on
		// a shared filesystem for the row to exist, which SQLite does
		// not support, and the lock refuses to guess.
		b := answer(openBackend(t, setup(t)), false, "")
		b.owner = "other-host/" + b.owner
		if err := b.Append(ctx, "s_rules", entry("e_1")); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("a foreign-host holder: err = %v, want ErrLocked", err)
		}
	})
	t.Run("RowFromBeforeTheMigrationIsJudgedByPid", func(t *testing.T) {
		// A lock row written before the process and start-time columns
		// existed carries neither: a live pid stays locked, a dead one
		// is taken over — the old rule, kept for old rows.
		path := setup(t)
		raw := openBackend(t, path)
		if _, err := raw.db.Exec(`UPDATE session_locks SET process = '', started = ''`); err != nil {
			t.Fatal(err)
		}
		if err := answer(openBackend(t, path), true, "boot:9").Append(ctx, "s_rules", entry("e_1")); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("an old row, live pid: err = %v, want ErrLocked", err)
		}
		if err := answer(openBackend(t, path), false, "").Append(ctx, "s_rules", entry("e_1")); err != nil {
			t.Errorf("an old row, dead pid: %v", err)
		}
	})
}

// A pid of 0 or below is never a live holder, whatever the platform
// check would answer for it.
func TestPidZeroIsDead(t *testing.T) {
	if pidAlive(0) || pidAlive(-1) {
		t.Error("pid 0 and -1 must read dead: no real holder wears them")
	}
}

// The platform's start-time probe answers for this process, the same
// way twice, and has nothing to say about a pid no process wears. (On
// a platform with no probe it answers "" throughout, and the lock
// falls back to the process token and the pid.)
func TestProcStart(t *testing.T) {
	self := procStart(os.Getpid())
	if again := procStart(os.Getpid()); again != self {
		t.Errorf("procStart is not stable for one process: %q then %q", self, again)
	}
	if got := procStart(1 << 30); got != "" {
		t.Errorf("procStart of a pid no process wears = %q, want empty", got)
	}
	if b := openBackend(t, filepath.Join(t.TempDir(), "self.db")); b.started != self || b.proc == "" {
		t.Errorf("the backend's identity: started %q (probe says %q), process token %q", b.started, self, b.proc)
	}
}

// Every Storage in one process carries the same process token, and it
// is not empty: that sameness is what lets a lock row tell a sibling
// Storage from an earlier process.
func TestProcessTokenIsPerProcess(t *testing.T) {
	a := openBackend(t, filepath.Join(t.TempDir(), "a.db"))
	b := openBackend(t, filepath.Join(t.TempDir(), "b.db"))
	if a.proc == "" || a.proc != b.proc {
		t.Errorf("process tokens %q and %q: want one non-empty token per process", a.proc, b.proc)
	}
	if a.owner == b.owner {
		t.Error("two Storages share an owner id")
	}
}

// A ":memory:" database survives database/sql replacing its
// connection. The pool discards a connection the driver reports bad
// (and may retire one for its own reasons); a plain in-memory database
// is private to its connection and vanished with it — every session
// gone, the schema with them, "no such table" from then on.
func TestMemoryDatabaseSurvivesConnectionReplacement(t *testing.T) {
	ctx := context.Background()
	b := openBackend(t, ":memory:")
	if err := b.Create(ctx, thread.Header{ID: "s_mem", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(ctx, "s_mem", entry("e_1")); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		if _, entries, _, err := b.Load(ctx, "s_mem"); err != nil || len(entries) != 1 {
			t.Fatalf("%s: %d entries, err %v — the in-memory database was lost", when, len(entries), err)
		}
	}

	// A transaction abandoned by its context: rolled back by the pool,
	// its connection returned or discarded as the pool sees fit.
	cctx, cancel := context.WithCancel(ctx)
	tx, err := b.db.BeginTx(cctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond) // let the pool's rollback land
	_ = tx.Rollback()
	check("after a canceled transaction")

	// A connection the driver calls bad: discarded outright.
	conn, err := b.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
	check("after a bad connection")

	// And the storage still writes.
	if err := b.Append(ctx, "s_mem", entry("e_2")); err != nil {
		t.Fatalf("Append after the replacements: %v", err)
	}
	// Two in-memory Opens are two databases.
	other := openBackend(t, ":memory:")
	if p, err := other.List(ctx, thread.Query{}); err != nil || p.Total != 0 {
		t.Errorf("a second :memory: Open sees %d sessions (err %v), want its own empty database", p.Total, err)
	}
}

// The embedded migrations are read on Open and their mistakes are
// errors Open returns — a file without a numeric version, two files
// claiming one — never a panic at package init.
func TestMigrationsParseErrors(t *testing.T) {
	entries := func(names ...string) []os.DirEntry {
		fsys := fstest.MapFS{}
		for _, n := range names {
			fsys[n] = &fstest.MapFile{}
		}
		out, err := fsys.ReadDir(".")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := parseMigrations(entries("0001_a.sql", "abc_b.sql")); err == nil || !strings.Contains(err.Error(), "abc_b.sql") {
		t.Errorf("a migration without a numeric version: err = %v", err)
	}
	if _, err := parseMigrations(entries("0001_a.sql", "1_b.sql")); err == nil || !strings.Contains(err.Error(), "same version") {
		t.Errorf("two migrations claiming one version: err = %v", err)
	}
	got, err := parseMigrations(entries("0001_a.sql", "0002_b.sql", "README.md"))
	if err != nil || len(got) != 2 || got[2] != "0002_b.sql" {
		t.Errorf("parseMigrations = %v, %v", got, err)
	}
	real, err := loadMigrations()
	if err != nil || len(real) < 3 {
		t.Errorf("the embedded migrations: %v, %v", real, err)
	}
}

// The page query is an index range read: List's ORDER BY and keyset
// are answered by the (created, id) index, with no sort step and no
// scan of the fleet to return one page.
func TestListPageUsesTheIndex(t *testing.T) {
	b := openBackend(t, filepath.Join(t.TempDir(), "plan.db"))
	rows, err := b.db.Query(`EXPLAIN QUERY PLAN
		SELECT header FROM sessions
		 WHERE envelope = ?
		   AND (created < ? OR (created = ? AND id < ?))
		 ORDER BY created DESC, id DESC LIMIT ?`, 1, "x", "x", "y", 50)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "; ")
	if !strings.Contains(joined, "sessions_order") || strings.Contains(joined, "TEMP B-TREE") {
		t.Errorf("the page query does not walk the (created, id) index in order: %s", joined)
	}
}
