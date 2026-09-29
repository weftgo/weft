package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft/thread"
)

// The lock row's takeover rules, unit-level: a live foreign holder is
// ErrLocked whatever its host; a dead one on this host is taken over; a
// dead one on another host is ErrLocked all the same, because liveness
// cannot be judged across machines (the cross-process behavior — a real
// second process, a real kill — is crash_test.go's).
func TestLockTakeoverRules(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locks.db")
	open := func(t *testing.T) *backend {
		t.Helper()
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return st.(*backend)
	}
	holder := open(t)
	if err := holder.Create(ctx, thread.Header{ID: "s_rules", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	live := open(t)
	live.alive = func(int) bool { return true }
	err := live.Append(ctx, "s_rules", thread.MessageEntry{ID: "e_live", Created: time.Now().UTC()})
	if !errors.Is(err, thread.ErrLocked) {
		t.Errorf("live holder: err = %v, want ErrLocked", err)
	}

	dead := open(t)
	dead.alive = func(int) bool { return false }
	if err := dead.Append(ctx, "s_rules", thread.MessageEntry{ID: "e_dead", Created: time.Now().UTC()}); err != nil {
		t.Fatalf("dead holder on this host must be taken over: %v", err)
	}
	if _, entries, _, err := dead.Load(ctx, "s_rules"); err != nil || len(entries) != 1 {
		t.Errorf("after takeover: %d entries, err %v", len(entries), err)
	}

	// The takeover made dead the holder; a third handle with the real
	// liveness answer sees its own pid — alive — and is locked out,
	// which is what two Storages in one process must see.
	third := open(t)
	if err := third.Append(ctx, "s_rules", thread.MessageEntry{ID: "e_third", Created: time.Now().UTC()}); !errors.Is(err, thread.ErrLocked) {
		t.Errorf("third writer in one process: err = %v, want ErrLocked", err)
	}

	// A foreign host is never judged dead from here, even when the pid
	// is gone on this machine: the database would have to be on a
	// shared filesystem for the row to exist, which SQLite does not
	// support, and the lock refuses to guess.
	foreign := open(t)
	foreign.owner = "other-host/" + foreign.owner
	foreign.alive = func(int) bool { return false }
	if err := foreign.Append(ctx, "s_rules", thread.MessageEntry{ID: "e_foreign", Created: time.Now().UTC()}); !errors.Is(err, thread.ErrLocked) {
		t.Errorf("foreign-host holder: err = %v, want ErrLocked", err)
	}
}

// A pid of 0 or below is never a live holder, whatever the platform
// check would answer for it.
func TestPidZeroIsDead(t *testing.T) {
	if pidAlive(0) || pidAlive(-1) {
		t.Error("pid 0 and -1 must read dead: no real holder wears them")
	}
}
