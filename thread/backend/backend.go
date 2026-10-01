// Package backend is for authors of thread.Storage backends: it
// resolves the open options an application passes — thread.Salvage,
// thread.FsyncOnFlush, thread.NoLock, thread.OpenLogger — into the
// configuration a backend acts on. Applications never import it; they
// pass thread's options to a backend's Open, and every backend accepts
// the same vocabulary because every backend resolves it here.
//
// A backend's Open looks like jsonl's:
//
//	func Open(dir string, opts ...thread.OpenOption) (thread.Storage, error) {
//		cfg := backend.Resolve(opts...)
//		...
//	}
//
// (jsonl and sqlite import the package under another name: each
// already calls its own Storage type backend.)
//
// An option a backend has no use for is accepted and documented as a
// no-op there, never rejected: sqlite commits every append, so the
// fsync cadence means nothing to it. The conformance table a backend
// must pass is thread/threadtest's.
package backend

import (
	"log/slog"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/internal/opencfg"
)

// Config is the resolved configuration of an open: the defaults with
// every option applied. Resolve returns it; a backend reads it once,
// at open.
type Config struct {
	// Salvage downgrades a malformed line from a load failure
	// (thread.ErrCorrupt) to a skip reported in the LoadReport. The
	// unknown and the newer stay loud: thread.ErrNewerFormat is never
	// salvaged.
	Salvage bool
	// SyncEveryAppend is the durability cadence: true (the default)
	// fsyncs every Append before it returns; false defers the fsync to
	// the thread.Flusher capability — the turn-end cadence a Session
	// drives.
	SyncEveryAppend bool
	// NoLock turns off the backend's cross-process writer lock
	// (thread.NoLock): the caller vouches that one process writes.
	NoLock bool
	// Logger receives the backend's own reports — a repaired torn
	// tail, a taken-over lock. Never nil: the default is
	// slog.Default(), read at open.
	Logger *slog.Logger
}

// Resolve folds opts over the defaults — every append fsynced, the
// lock on, no salvage, slog.Default() — in the order given; a nil
// option is skipped.
func Resolve(opts ...thread.OpenOption) Config {
	c := opencfg.Resolve(opts)
	return Config{
		Salvage:         c.Salvage,
		SyncEveryAppend: c.SyncEveryAppend,
		NoLock:          c.NoLock,
		Logger:          c.Logger,
	}
}
