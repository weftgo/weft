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
// file itself stays (removing it would race the next taker): Acquire
// empties it the moment it takes the lock, so a crashed holder's words
// never name the new one, and the holder writes its {url, pid} once it
// serves. A refused start reads them only under ReadHolder's rules:
// the file 0600 and this user's (unix), a loopback IP url and a live
// pid — anything else names nobody.
//
// One file, one lock: the caller passes the database path with its
// symlinks resolved (cmd/weft does). Hard links, and two spellings of
// one name on a case-insensitive filesystem, are not detected.
//
// Only a database with a file has a lock: ":memory:" and ClickHouse
// (a server shared by design) are out of scope. On a platform with
// neither flock nor LockFileEx the lock is a no-op.
package dblock

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
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
	// A crashed holder's {url, pid} is not this holder's: emptied at
	// once, and the file kept 0600 whatever created it.
	_ = f.Truncate(0)
	_ = f.Chmod(0o600)
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
// when it names none — not written yet, released, unreadable — or
// names one not to be trusted: a file that is not a regular one of
// mode 0600 owned by this user (unix; Windows skips the owner check),
// a url whose host is not a loopback IP (the holder is handed the
// database's token), or a pid not running (a crashed holder's leftover).
func ReadHolder(dbPath string) (Holder, bool) {
	p := Path(dbPath)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || checkOwner(fi) != nil {
		return Holder{}, false
	}
	f, err := os.Open(p)
	if err != nil {
		return Holder{}, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return Holder{}, false
	}
	var h Holder
	if json.Unmarshal(b, &h) != nil || h.PID <= 0 || !loopbackIP(h.URL) || !alive(h.PID) {
		return Holder{}, false
	}
	return h, true
}

// loopbackIP reports whether raw is an http URL whose host is a
// loopback IP (127.0.0.0/8, ::1) — not a name to resolve.
func loopbackIP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
