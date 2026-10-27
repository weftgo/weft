// Package dblock is the one-Studio-per-database rule of the weft
// command (B7-fix): `weft studio` and `weft dev` hold an exclusive lock
// on <db>.lock, beside a SQLite database file, for as long as they
// serve it, taken before the port policy runs. A second start on the
// same file — at the same moment, or from another directory whose
// discovery file names another Studio — cannot take it and is refused.
//
// The lock is the operating system's, on an open file: flock(2) on
// unix (LOCK_EX|LOCK_NB), LockFileEx on Windows. Either is released by
// the kernel when the holding process's last descriptor closes — on a
// clean exit, and equally when the process is killed or crashes — so
// a stale lock never outlives its Studio and needs no cleanup. The
// file itself stays (removing it would race the next taker); while
// held it carries the holder's {url, pid}, written once the listener
// is bound, which a refused start reads to name it.
//
// Only a database with a file has a lock: ":memory:" and ClickHouse
// (a server shared by design) are out of scope. On a platform with
// neither flock nor LockFileEx the lock is a no-op.
package dblock

import (
	"encoding/json"
	"errors"
	"io"
	"os"
)

// ErrHeld is Acquire's error when another process (or another handle
// of this one) holds the lock.
var ErrHeld = errors.New("dblock: held")

// Holder is what the lock file says about the Studio holding it.
type Holder struct {
	URL string `json:"url"`
	PID int    `json:"pid"`
}

// Lock is a held lock on <db>.lock.
type Lock struct {
	f    *os.File
	path string
}

// Path is the lock file beside dbPath.
func Path(dbPath string) string { return dbPath + ".lock" }

// Acquire takes the lock beside dbPath without waiting: ErrHeld when it
// is held, another error when the file cannot be opened.
func Acquire(dbPath string) (*Lock, error) {
	p := Path(dbPath)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f, path: p}, nil
}

// SetHolder records the holder's url and pid in the lock file (best
// effort: the lock holds regardless).
func (l *Lock) SetHolder(h Holder) {
	if l == nil {
		return
	}
	b, err := json.Marshal(h)
	if err != nil {
		return
	}
	if l.f.Truncate(0) == nil {
		_, _ = l.f.WriteAt(append(b, '\n'), 0)
	}
}

// Release gives the lock up (closing the file). Nil-safe, idempotent.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = l.f.Truncate(0)
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}

// ReadHolder is the holder the lock file beside dbPath names; ok false
// when it names none (not written yet, released, or unreadable).
func ReadHolder(dbPath string) (Holder, bool) {
	f, err := os.Open(Path(dbPath))
	if err != nil {
		return Holder{}, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return Holder{}, false
	}
	var h Holder
	if json.Unmarshal(b, &h) != nil || h.URL == "" || h.PID <= 0 {
		return Holder{}, false
	}
	return h, true
}
