package thread

import (
	"context"
	"iter"
	"log/slog"
)

// OpenOption configures a Storage backend at open, one value per
// concern, applied over the defaults. The options live here — in the
// package that owns the Storage contract — so every backend accepts
// the same vocabulary and a caller never learns a backend to say
// Salvage (ADR 0011 §5 names it thread.Salvage).
type OpenOption interface {
	applyOpen(*OpenConfig)
}

// OpenConfig is the resolved configuration of an open: the defaults
// with every option applied. Backends resolve it through ResolveOpen;
// callers never construct it.
type OpenConfig struct {
	// Salvage downgrades a malformed line from a load failure
	// (ErrCorrupt) to a skip reported in the LoadReport. The unknown
	// and the newer stay loud: ErrNewerFormat is never salvaged.
	Salvage bool
	// SyncEveryAppend is the durability cadence: true (the default)
	// fsyncs every Append before it returns; false defers the fsync to
	// the Flusher capability — the turn-end cadence a Session drives.
	SyncEveryAppend bool
	// NoLock turns off the backend's cross-process writer lock (the
	// NoLock option): the caller vouches that one process writes.
	NoLock bool
	// Logger receives the backend's own reports — a repaired torn
	// tail, a taken-over lock. Never nil after ResolveOpen: the
	// default is slog.Default(), read at open.
	Logger *slog.Logger
}

// ResolveOpen folds opts over the defaults. Exported because backends
// outside this package resolve the same option vocabulary into the
// same configuration.
func ResolveOpen(opts ...OpenOption) OpenConfig {
	cfg := OpenConfig{SyncEveryAppend: true}
	for _, o := range opts {
		if o != nil {
			o.applyOpen(&cfg)
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

type salvageOption struct{}

func (salvageOption) applyOpen(c *OpenConfig) { c.Salvage = true }

// Salvage returns the open option that skips malformed lines instead
// of failing the load: each skip is reported in Load's LoadReport
// (Skipped, in file order). A torn final line is always dropped and
// reported — that is a crash, not damage — and data from a newer weft
// still fails with ErrNewerFormat: salvage repairs what a crash wrote,
// never what it cannot read (ADR 0011 §5).
func Salvage() OpenOption { return salvageOption{} }

type syncEveryAppendOption bool

func (o syncEveryAppendOption) applyOpen(c *OpenConfig) { c.SyncEveryAppend = bool(o) }

// FsyncEveryAppend returns the open option that restores the default
// durability: every Append fsyncs before returning, so an accepted
// entry is durable before anything replies on it — the rule behind
// "the prompt is durable before the run starts" (ADR 0011 §4).
func FsyncEveryAppend() OpenOption { return syncEveryAppendOption(true) }

// FsyncOnFlush returns the open option that defers the fsync to the
// Flusher capability — the turn-end cadence, cheaper than an fsync per
// append. A crash between appends and the flush can lose the tail of a
// turn, never a synced one; the file stays readable (a torn final line
// is dropped and reported). The caller who flushes at turn ends — a
// Session does — owns the durability window; a plain Flush after the
// prompt keeps ADR 0011 §4's promise.
func FsyncOnFlush() OpenOption { return syncEveryAppendOption(false) }

type noLockOption struct{}

func (noLockOption) applyOpen(c *OpenConfig) { c.NoLock = true }

// NoLock returns the open option that opens a file backend without its
// cross-process writer lock. It exists for platforms with no advisory
// file lock to take — there jsonl.Open fails unless NoLock says the
// caller knows — and for filesystems whose locks cannot be trusted.
// With it, one-writer-per-session (ADR 0011 §5) is the caller's
// promise instead of the backend's check: goroutines of one Storage
// are still serialized, but a second Storage or a second process
// writing the same session is not refused with ErrLocked and can
// interleave its lines with the first's. Backends whose lock needs no
// platform support (sqlite's lock row, Memory) accept the option and
// keep locking.
func NoLock() OpenOption { return noLockOption{} }

type openLoggerOption struct{ l *slog.Logger }

func (o openLoggerOption) applyOpen(c *OpenConfig) {
	if o.l != nil {
		c.Logger = o.l
	}
}

// OpenLogger returns the open option that names where a backend
// reports what it repairs on its own: a torn tail a crashed writer
// left, truncated before the next append; a dead holder's lock, taken
// over. Each is one Warn line carrying the session id. The default is
// slog.Default() as it stands at open; a nil logger keeps the default.
// What a load had to drop is still the LoadReport's to say — the
// logger covers the write path, where no report is returned.
func OpenLogger(l *slog.Logger) OpenOption { return openLoggerOption{l} }

// Flusher is the optional Storage capability that completes buffered
// durability work — the small-interface rule (ADR 0011 §5: capabilities
// are discovered by type assertion, so Storage never grows). With
// FsyncOnFlush, an append writes without fsyncing and Flush makes the
// session durable; with the default policy Flush is a no-op that still
// returns any error the storage holds for the session. Flushing a
// session the storage does not hold fails with ErrNotFound; a session
// another writer holds but this one never buffered flushes nothing.
type Flusher interface {
	Flush(ctx context.Context, session string) error
}

// Releaser is the optional Storage capability that ends this writer's
// hold on a session — the same small-interface rule. A backend takes
// the one-writer lock on a session's first write and, without Release,
// keeps it for the life of the process (and, on jsonl, an open file
// with it). Release flushes what the writer buffered and lets go: from
// then on another Storage — in this process or another — may write the
// session. The hold is a lease the writer renews by writing: a later
// Append through the releasing Storage re-acquires the lock, and fails
// with ErrLocked if another writer took the session in between.
// Releasing a session this Storage does not hold is a no-op; one the
// storage does not hold at all fails with ErrNotFound. Release must
// not race the session's own Append — it is the last call of a writer
// that is done, which is what Session.Close is.
type Releaser interface {
	Release(ctx context.Context, session string) error
}

// Watcher is the optional Storage capability that tails a session as
// it is appended to — the same small-interface rule; jsonl and sqlite
// implement it. Watch yields the session's entries in arrival order,
// each exactly once, starting after the entry named by after (empty —
// from the beginning), then keeps yielding as appends land, and ends
// when ctx is done. A session the storage does not hold fails with
// ErrNotFound before the first yield, as does an after the session
// does not hold (a plain error). The stream ends with one terminal
// error when the session is deleted, or deleted and created again,
// under the watcher (ErrNotFound: the session being tailed is gone),
// or when it meets data it cannot decode (ErrNewerFormat, or
// ErrCorrupt naming the line — skipped instead under Salvage). A torn
// final line is a write in flight: it is yielded once complete, never
// half. Readers never lock: a watcher is a reader that waits, and the
// consumer is free to call the same Storage from inside the loop.
type Watcher interface {
	Watch(ctx context.Context, session string, after string) (iter.Seq2[Entry, error], error)
}
