package jsonl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

func openBackend(t *testing.T, dir string, opts ...thread.OpenOption) *backend {
	t.Helper()
	st, err := Open(dir, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return st.(*backend)
}

func entry(id string) thread.Entry {
	return thread.MessageEntry{ID: id, Created: time.Now().UTC(), Message: weft.User(id)}
}

// A platform with no file lock opens only by explicit choice: without
// NoLock, Open's gate fails wrapping errors.ErrUnsupported; with it,
// or on a platform that has the lock, it passes.
func TestLockSupportGate(t *testing.T) {
	if err := checkLockSupport(false, false); !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "NoLock") {
		t.Errorf("no lock, no NoLock: err = %v, want ErrUnsupported naming the option", err)
	}
	if err := checkLockSupport(false, true); err != nil {
		t.Errorf("no lock, NoLock: %v", err)
	}
	if err := checkLockSupport(true, false); err != nil {
		t.Errorf("lock supported: %v", err)
	}
}

// A session's setup — Create's two fsyncs, a first touch's open, lock
// and tail check — runs outside the instance mutex: while one
// session's setup is stuck, other sessions are created, appended to,
// loaded and listed. A goroutine that wants the stuck session waits
// for that reservation and shares its outcome.
func TestSlowSetupBlocksOnlyItsOwnSession(t *testing.T) {
	ctx := context.Background()
	b := openBackend(t, t.TempDir())
	entered, release := make(chan struct{}), make(chan struct{})
	slow := make(chan error, 1)
	go func() {
		_, _, err := b.hold(ctx, "s_slow", func(s *session) error {
			close(entered)
			<-release // a create stuck in its fsync
			return b.createFile(s, "s_slow", []byte(`{"type":"session","weft":1,"id":"s_slow","created":"2026-09-28T12:00:00Z"}`+"\n"))
		})
		slow <- err
	}()
	<-entered

	others := make(chan error, 1)
	go func() {
		if err := b.Create(ctx, thread.Header{ID: "s_other", Created: time.Now().UTC()}); err != nil {
			others <- err
			return
		}
		if err := b.Append(ctx, "s_other", entry("e_1")); err != nil {
			others <- err
			return
		}
		_, err := b.List(ctx, thread.Query{})
		others <- err
	}()
	select {
	case err := <-others:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another session waited on a slow session's setup: the I/O ran under the instance mutex")
	}

	// The stuck session's own callers wait for it — and a canceled
	// wait gives up without disturbing the reservation.
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := b.Append(cctx, "s_slow", entry("e_early")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Append waiting on a reservation under an expiring context: err = %v", err)
	}
	waiter := make(chan error, 1)
	go func() { waiter <- b.Append(ctx, "s_slow", entry("e_1")) }()
	dup := make(chan error, 1)
	go func() { dup <- b.Create(ctx, thread.Header{ID: "s_slow", Created: time.Now().UTC()}) }()
	select {
	case err := <-waiter:
		t.Fatalf("Append did not wait for the session's reservation: %v", err)
	case err := <-dup:
		t.Fatalf("Create did not wait for the session's reservation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; err != nil {
		t.Errorf("Append after the reservation completed: %v", err)
	}
	if err := <-dup; !errors.Is(err, thread.ErrExists) {
		t.Errorf("Create over a completed reservation: err = %v, want ErrExists", err)
	}
	if _, entries, _, err := b.Load(ctx, "s_slow"); err != nil || len(entries) != 1 {
		t.Errorf("the slow session: %d entries, err %v", len(entries), err)
	}
}

// A failed setup leaves nothing behind: the reservation is gone, the
// waiter retries on its own and reports its own answer.
func TestFailedSetupIsRetriedByWaiters(t *testing.T) {
	ctx := context.Background()
	b := openBackend(t, t.TempDir())
	entered, release := make(chan struct{}), make(chan struct{})
	boom := errors.New("setup failed")
	first := make(chan error, 1)
	go func() {
		_, _, err := b.hold(ctx, "s_fail", func(*session) error {
			close(entered)
			<-release
			return boom
		})
		first <- err
	}()
	<-entered
	waiter := make(chan error, 1)
	go func() { waiter <- b.Create(ctx, thread.Header{ID: "s_fail", Created: time.Now().UTC()}) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-first; !errors.Is(err, boom) {
		t.Fatalf("the failing setup: err = %v", err)
	}
	if err := <-waiter; err != nil {
		t.Fatalf("Create after another caller's failed reservation: %v", err)
	}
}

// Release is what turns the writer's hold from a lifetime into a
// lease: after it this instance keeps no file and no lock for the
// session — a process that touches many sessions and releases them
// holds no descriptors — and a failed first touch never leaves one
// either.
func TestReleaseDropsTheFile(t *testing.T) {
	ctx := context.Background()
	b := openBackend(t, t.TempDir(), thread.FsyncOnFlush())
	for i := 0; i < 50; i++ {
		id := "s_rel" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := b.Create(ctx, thread.Header{ID: id, Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := b.Append(ctx, id, entry("e_1")); err != nil {
			t.Fatal(err)
		}
		if err := b.Release(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Append(ctx, "s_never", entry("e_1")); !errors.Is(err, thread.ErrNotFound) {
		t.Fatalf("Append to a missing session: %v", err)
	}
	b.mu.Lock()
	held := len(b.sessions)
	b.mu.Unlock()
	if held != 0 {
		t.Errorf("after releasing every session the instance still holds %d", held)
	}
}

// A failed write leaves the session suspect: the next write through
// the same instance checks the tail and repairs it before appending,
// so a short write (a full disk) cannot poison the appends after it.
func TestSuspectTailIsRepairedBySameInstance(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openBackend(t, dir)
	if err := b.Create(ctx, thread.Header{ID: "s_short", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(ctx, "s_short", entry("e_1")); err != nil {
		t.Fatal(err)
	}
	// What a short write leaves: half a line in the file, the session
	// marked suspect.
	s, err := b.sessionFor(ctx, "s_short")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if _, err := s.f.WriteString(`{"type":"message","id":"e_ha`); err != nil {
		t.Fatal(err)
	}
	s.suspect = true
	s.mu.Unlock()
	if err := b.Append(ctx, "s_short", entry("e_2")); err != nil {
		t.Fatal(err)
	}
	_, entries, report, err := b.Load(ctx, "s_short")
	if err != nil || report != nil || len(entries) != 2 {
		t.Fatalf("after a short write and a retry: %d entries, report %+v, err %v", len(entries), report, err)
	}
}

// The tail reads the bytes appended since its last poll, not the file:
// idle polls over a large session cost a stat and a mark-sized read.
func TestWatchPollReadsOnlyNewBytes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openBackend(t, dir)
	if err := b.Create(ctx, thread.Header{ID: "s_big", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	big := thread.MessageEntry{ID: "e_big", Created: time.Now().UTC(), Message: weft.User(strings.Repeat("x", 4<<20))}
	if err := b.Append(ctx, "s_big", big); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s_big.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tl := &tail{path: path, info: info, offset: int64(len(raw)), mark: append([]byte(nil), raw[len(raw)-markLen:]...)}
	raw = nil

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const polls = 50
	for i := 0; i < polls; i++ {
		lines, err := tl.poll()
		if err != nil || len(lines) != 0 {
			t.Fatalf("idle poll %d: %d lines, err %v", i, len(lines), err)
		}
	}
	runtime.ReadMemStats(&after)
	if perPoll := (after.TotalAlloc - before.TotalAlloc) / polls; perPoll > 64<<10 {
		t.Errorf("an idle poll over a 4 MiB session allocated %d bytes: the tail is re-reading the file", perPoll)
	}

	if err := b.Append(ctx, "s_big", entry("e_new")); err != nil {
		t.Fatal(err)
	}
	lines, err := tl.poll()
	if err != nil || len(lines) != 1 || entryID(lines[0]) != "e_new" {
		t.Fatalf("poll after an append: %d lines, err %v", len(lines), err)
	}
	if lines, err := tl.poll(); err != nil || len(lines) != 0 {
		t.Fatalf("the entry was served twice: %d lines, err %v", len(lines), err)
	}
}

// titleOf on a file with no complete line answers no title; it used to
// slice past an empty line list.
func TestTitleOfWithoutHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s_empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := titleOf(path); got != "" {
		t.Errorf("titleOf(empty file) = %q", got)
	}
}

// Release racing the instance's own appends is not a correct program —
// Release is a writer's last call — but it must stay safe: an append
// that finds its session released under it re-acquires, so every
// append lands, none is refused as if another writer held the lock,
// and the file holds every line whole.
func TestReleaseRacingAppendsLosesNothing(t *testing.T) {
	ctx := context.Background()
	b := openBackend(t, t.TempDir(), thread.FsyncOnFlush())
	if err := b.Create(ctx, thread.Header{ID: "s_race", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	const writers, each = 4, 100
	errs := make(chan error, writers+1)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				errs <- nil
				return
			default:
			}
			if err := b.Release(ctx, "s_race"); err != nil {
				errs <- err
				return
			}
			if err := b.Flush(ctx, "s_race"); err != nil {
				errs <- err
				return
			}
		}
	}()
	done := make(chan error, writers)
	for w := 0; w < writers; w++ {
		go func() {
			for i := 0; i < each; i++ {
				if err := b.Append(ctx, "s_race", entry(thread.NewEntryID())); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for w := 0; w < writers; w++ {
		if err := <-done; err != nil {
			t.Errorf("an append racing Release: %v", err)
		}
	}
	close(stop)
	if err := <-errs; err != nil {
		t.Errorf("Release/Flush racing appends: %v", err)
	}
	_, entries, report, err := b.Load(ctx, "s_race")
	if err != nil || report != nil || len(entries) != writers*each {
		t.Fatalf("after the race: %d entries, report %+v, err %v; want %d", len(entries), report, err, writers*each)
	}
}

// Delete racing the instance's own appends: every append answers nil
// (it landed before the delete) or ErrNotFound (it arrived after) —
// never ErrLocked, and never a write into a file that is gone.
func TestDeleteRacingAppends(t *testing.T) {
	ctx := context.Background()
	for round := 0; round < 20; round++ {
		b := openBackend(t, t.TempDir())
		if err := b.Create(ctx, thread.Header{ID: "s_race", Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 4)
		for w := 0; w < 4; w++ {
			go func() {
				for i := 0; i < 20; i++ {
					if err := b.Append(ctx, "s_race", entry(thread.NewEntryID())); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
		}
		if err := b.Delete(ctx, "s_race"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		for w := 0; w < 4; w++ {
			if err := <-done; err != nil && !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("an append racing Delete: %v, want nil or ErrNotFound", err)
			}
		}
		if _, _, _, err := b.Load(ctx, "s_race"); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Load after Delete: %v", err)
		}
	}
}
