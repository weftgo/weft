package dblock_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/weftgo/weft/internal/dblock"
)

// TestExclusive: one holder at a time — a second Acquire (another
// handle, as another process would hold it) is ErrHeld and reads the
// holder; after Release the lock is free again and names nobody.
func TestExclusive(t *testing.T) {
	db := filepath.Join(t.TempDir(), "weft.db")
	l, err := dblock.Acquire(db)
	if err != nil {
		t.Fatal(err)
	}
	l.SetHolder(dblock.Holder{URL: "http://127.0.0.1:7347", PID: os.Getpid()})
	if _, err := dblock.Acquire(db); !errors.Is(err, dblock.ErrHeld) {
		t.Fatalf("second Acquire = %v, want ErrHeld", err)
	}
	if h, ok := dblock.ReadHolder(db); !ok || h.URL != "http://127.0.0.1:7347" || h.PID != os.Getpid() {
		t.Errorf("holder = %+v %v", h, ok)
	}
	l.Release()
	l.Release() // idempotent
	if _, ok := dblock.ReadHolder(db); ok {
		t.Error("a released lock still names a holder")
	}
	again, err := dblock.Acquire(db)
	if err != nil {
		t.Fatalf("Acquire after Release = %v", err)
	}
	again.Release()
	var nilLock *dblock.Lock
	nilLock.Release()
	nilLock.SetHolder(dblock.Holder{})
}

// TestHolderTrust (B7-fix2): the lock file names a holder only when it
// is 0600 and this user's, its url a loopback IP and its pid alive —
// it is handed the database's token — and Acquire empties a crashed
// holder's words the moment it takes the lock.
func TestHolderTrust(t *testing.T) {
	db := filepath.Join(t.TempDir(), "weft.db")
	write := func(body string, mode os.FileMode) {
		t.Helper()
		_ = os.Remove(dblock.Path(db))
		if err := os.WriteFile(dblock.Path(db), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(dblock.Path(db), mode)
	}
	me := strconv.Itoa(os.Getpid())
	write(`{"url":"http://127.0.0.1:7347","pid":`+me+`}`, 0o600)
	if _, ok := dblock.ReadHolder(db); !ok {
		t.Fatal("a trusted holder is not read")
	}
	for name, body := range map[string]string{
		"not loopback": `{"url":"http://203.0.113.9:80","pid":` + me + `}`,
		"a name":       `{"url":"http://localhost:7347","pid":` + me + `}`,
		"https":        `{"url":"https://127.0.0.1:7347","pid":` + me + `}`,
		"dead pid":     `{"url":"http://127.0.0.1:1","pid":999999999}`,
	} {
		write(body, 0o600)
		if h, ok := dblock.ReadHolder(db); ok {
			t.Errorf("%s: names %+v", name, h)
		}
	}
	if runtime.GOOS != "windows" {
		write(`{"url":"http://127.0.0.1:7347","pid":`+me+`}`, 0o644)
		if _, ok := dblock.ReadHolder(db); ok {
			t.Error("a 0644 lock file names a holder")
		}
	}
	// A crashed holder's leftover: Acquire empties it at once.
	write(`{"url":"http://127.0.0.1:7347","pid":`+me+`}`, 0o600)
	l, err := dblock.Acquire(db)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if b, _ := os.ReadFile(dblock.Path(db)); len(b) != 0 {
		t.Errorf("Acquire left %q", b)
	}
}

// TestPlantedSymlinkRefused: a symlink planted at <db>.lock is never
// followed — Acquire refuses it (ErrNotRegular), and the file it points
// at keeps its bytes and its mode (no Truncate, no Chmod through it).
// A directory there is refused the same way.
func TestPlantedSymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "precious.txt")
	if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "weft.db")
	if err := os.Symlink(target, dblock.Path(db)); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if l, err := dblock.Acquire(db); !errors.Is(err, dblock.ErrNotRegular) {
		l.Release()
		t.Fatalf("Acquire through a planted symlink = %v, want ErrNotRegular", err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep me" {
		t.Errorf("the symlink's target now reads %q (%v), want it untouched", b, err)
	}
	if fi, err := os.Stat(target); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644) {
		t.Errorf("the symlink's target mode = %v (%v), want 0644 kept", fi.Mode(), err)
	}
	// A dangling symlink is not created through either.
	db2 := filepath.Join(dir, "other.db")
	dangling := filepath.Join(dir, "would-be-created")
	if err := os.Symlink(dangling, dblock.Path(db2)); err != nil {
		t.Fatal(err)
	}
	if _, err := dblock.Acquire(db2); !errors.Is(err, dblock.ErrNotRegular) {
		t.Errorf("Acquire through a dangling symlink = %v, want ErrNotRegular", err)
	}
	if _, err := os.Lstat(dangling); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dangling symlink's target was created: %v", err)
	}
	db3 := filepath.Join(dir, "dir.db")
	if err := os.Mkdir(dblock.Path(db3), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := dblock.Acquire(db3); err == nil {
		t.Error("Acquire over a directory succeeded, want refused")
	}
}
