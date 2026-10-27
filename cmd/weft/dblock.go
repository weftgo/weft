package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	"github.com/weftgo/weft/internal/dblock"
	"github.com/weftgo/weft/internal/discovery"
)

// One Studio per database file (B7-fix, UX-S5a-13): `weft studio` and
// `weft dev` take <db>.lock (internal/dblock: flock / LockFileEx,
// released by the kernel when the process ends, however it ends)
// before the port policy and hold it while they serve. A start that
// cannot take it is refused — `weft dev` reuses the holder instead.
// A SQLite file only: ":memory:" has no file, and a ClickHouse
// database is a shared server by design (out of scope).

// lockDB takes dbPath's lock; nil, nil for a database with no file.
func lockDB(dbPath string) (*dblock.Lock, error) {
	if dbPath == "" {
		return nil, nil
	}
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	discovery.GuardDir(dir)
	l, err := dblock.Acquire(dbPath)
	switch {
	case errors.Is(err, dblock.ErrHeld):
		return nil, errHeld
	case err != nil:
		return nil, fmt.Errorf("lock %s: %w", dblock.Path(dbPath), err)
	}
	return l, nil
}

// errHeld is lockDB's verdict when another Studio holds the file.
var errHeld = errors.New("held")

// dbHolder is the Studio holding dbPath's lock, as far as anything
// says: the lock file's {url, pid}, else a trusted discovery file
// naming that database. ok false when neither does.
func dbHolder(dbPath string) (addr string, pid int, ok bool) {
	if h, ok := dblock.ReadHolder(dbPath); ok {
		if u, err := url.Parse(h.URL); err == nil && u.Host != "" {
			return u.Host, h.PID, true
		}
	}
	info, _, found := discovery.Lookup(func(k string) string {
		if k == "WEFT_STUDIO_URL" {
			return ""
		}
		return os.Getenv(k)
	}, slog.New(slog.DiscardHandler))
	if found && info.DB != "" && filepath.Clean(info.DB) == filepath.Clean(dbPath) {
		if u, err := url.Parse(info.URL); err == nil && u.Host != "" {
			return u.Host, info.PID, true
		}
	}
	return "", 0, false
}

// heldError is the refusal of a start whose database another Studio
// holds: its URL and pid when known.
func heldError(dbPath string) error {
	if addr, pid, ok := dbHolder(dbPath); ok {
		return alreadyRunning(addr, pid, dbPath)
	}
	return fmt.Errorf("another Studio holds %s (its lock, %s): one Studio per database file — stop it first, or start this one on another --db",
		dbPath, dblock.Path(dbPath))
}

// alreadyRunning is the refusal naming the running Studio.
func alreadyRunning(addr string, pid int, dbPath string) error {
	p := "pid unknown"
	if pid > 0 {
		p = fmt.Sprintf("pid %d", pid)
	}
	return fmt.Errorf("studio already running at http://%s (%s) on %s: one Studio per database file — sign in to it with `weft open --with-token`, or stop it first",
		loopbackAddr(addr), p, dbPath)
}
