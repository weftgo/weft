// Package sqlite is obsdb's default backend: the observability schema
// in a single SQLite file on the CGO-free modernc.org/sqlite driver
// (thread/sqlite's choice). Open returns an obsdb.DB that also
// implements interface{ Hub() obsdb.Hub } — every Write publishes its
// frames to that in-process hub before returning, which is what setup
// A's live lane runs on (weft/otel's local sink writes, Studio's SSE
// handler subscribes, no network).
//
// The handle shape is one writer connection plus a read pool: the local
// sink's synchronous processors write on the run's goroutine, Studio
// reads concurrently; WAL lets a reader in another process see the same
// data through its own Open.
package sqlite

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/obsdb"

	msqlite "modernc.org/sqlite"

	// CGO-free driver; the import registers it.
	_ "modernc.org/sqlite"
)

// DB is the SQLite backend. Create it with Open; the zero value is not
// usable.
type DB struct {
	writer     *sql.DB
	reads      *sql.DB
	mem        bool // :memory: — one handle serves writer and reads
	keepDeltas bool
	hub        obsdb.Hub
	mu         sync.Mutex
	closed     bool
}

func (d *DB) keepDeltasOn() bool { return d.keepDeltas }

// Option configures Open.
type Option interface{ apply(*openConfig) }

type openConfig struct {
	keepDeltas bool
}

type openOption func(*openConfig)

func (f openOption) apply(c *openConfig) { f(c) }

// KeepDeltas turns delta storage on for debugging (Q4, closed): deltas
// are counted but never stored by default, on their own counter, so
// storing them can never open a hole in the durable event sequence.
func KeepDeltas() Option {
	return openOption(func(c *openConfig) { c.keepDeltas = true })
}

// Open opens (creating if needed) the database at path and brings its
// schema up to date. ":memory:" works — one in-process database. File
// connections carry journal_mode=WAL (a persistent property of the
// file, switched once), synchronous=NORMAL, busy_timeout=30000,
// foreign_keys=ON and _txlock=immediate (preventing deferred-to-writer
// upgrade deadlocks); the writer handle allows a single connection so
// every write is serialized, the read pool serves queries.
func Open(path string, opts ...Option) (obsdb.DB, error) {
	cfg := openConfig{}
	for _, o := range opts {
		o.apply(&cfg)
	}
	db, reads, err := openHandles(path)
	if err != nil {
		return nil, err
	}
	if !db.mem {
		if err := setWAL(db.writer); err != nil {
			_ = db.writer.Close()
			if reads != db.writer {
				_ = reads.Close()
			}
			return nil, err
		}
	}
	if err := migrate(db.writer); err != nil {
		_ = db.writer.Close()
		if reads != db.writer {
			_ = reads.Close()
		}
		return nil, err
	}
	db.reads = reads
	db.keepDeltas = cfg.keepDeltas
	db.hub = obsdb.NewHub()
	return db, nil
}

// setWAL switches the database file to WAL — once, here, not per
// connection (the store's finding): the mode switch needs a brief
// exclusive lock SQLite does not take the busy handler's patience for,
// so concurrent first Opens on a fresh file can each see SQLITE_BUSY.
// The loop converges: whichever process wins writes WAL into the file
// header and every later attempt reads "wal" back and returns.
func setWAL(db *sql.DB) error {
	for try := 0; ; try++ {
		var mode string
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
			return err
		}
		if strings.EqualFold(mode, "wal") {
			return nil
		}
		if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err == nil {
			return nil
		} else if !isBusy(err) || try >= 20 {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// isBusy reports whether err is SQLite's SQLITE_BUSY or SQLITE_LOCKED,
// including their extended codes (the low byte carries the primary).
func isBusy(err error) bool {
	var serr *msqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	switch serr.Code() & 0xff {
	case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
		return true
	}
	return false
}

// uriPath escapes the characters SQLite's URI filename form gives
// meaning to, so a path holding one names the file it says: '%' starts
// an escape, '?' the parameters, '#' a fragment.
var uriPath = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

// fileURI is path as the SQLite URI filename that names it. A path
// opening with "//" would read as "file://authority/...", so it gets
// the empty authority spelled out.
func fileURI(path string) string {
	esc := uriPath.Replace(path)
	if strings.HasPrefix(esc, "//") {
		return "file://" + esc
	}
	return "file:" + esc
}

func openHandles(path string) (*DB, *sql.DB, error) {
	file := fileURI(path)
	dsn := file +
		"?_txlock=immediate" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(30000)" +
		"&_pragma=foreign_keys(ON)"
	if path == ":memory:" {
		// One shared in-memory database: the writer and the read pool
		// must see the same rows, so :memory: gets a single handle.
		dsn = "file::memory:" +
			"?_txlock=immediate" +
			"&_pragma=journal_mode(MEMORY)" +
			"&_pragma=synchronous(OFF)" +
			"&_pragma=busy_timeout(30000)" +
			"&_pragma=foreign_keys(ON)"
		h, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, nil, err
		}
		h.SetMaxOpenConns(1)
		h.SetMaxIdleConns(1)
		return &DB{writer: h, mem: true}, h, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	writer.SetMaxOpenConns(1) // one writer: every Write serialized
	writer.SetMaxIdleConns(1)
	reads, err := sql.Open("sqlite", file+"?_pragma=busy_timeout(30000)&_pragma=foreign_keys(ON)")
	if err != nil {
		_ = writer.Close()
		return nil, nil, err
	}
	reads.SetMaxOpenConns(4)
	reads.SetMaxIdleConns(4)
	return &DB{writer: writer}, reads, nil
}

// Hub returns the DB's in-process live hub: every Write publishes its
// frames to it before returning (D4 — this handle is what setup A's
// studio.DB(otel.LocalDB()) shares).
func (d *DB) Hub() obsdb.Hub { return d.hub }

// Close closes the handles.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.mem {
		return d.writer.Close()
	}
	err := d.writer.Close()
	if err2 := d.reads.Close(); err == nil {
		err = err2
	}
	return err
}

func (d *DB) checkOpen() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return obsdb.ErrClosed
	}
	return nil
}

// closedErr turns the error of a call that raced Close into ErrClosed:
// checkOpen passed, then Close shut the handle under the call, and the
// driver's own "closed" error is not the documented sentinel. Every
// method defers it on its error result.
func (d *DB) closedErr(err *error) {
	if *err != nil && d.checkOpen() != nil {
		*err = obsdb.ErrClosed
	}
}
