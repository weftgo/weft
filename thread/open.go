package thread

import (
	"context"
	"iter"
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
// is dropped and reported). The caller who flushes at turn ends —
// Session, from v0.1 commit 7 — owns the durability window; a plain
// Flush after the prompt keeps ADR 0011 §4's promise.
func FsyncOnFlush() OpenOption { return syncEveryAppendOption(false) }

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

// Watcher is the optional Storage capability that tails a session as
// it is appended to — the same small-interface rule. It arrives in
// v0.4 (plan §7, the live tail); it is declared now, unimplemented,
// so the capability pattern and its vocabulary ship with the first
// release instead of being retrofitted. Watch yields the session's
// entries in arrival order, starting after the entry named by after
// (empty — from the beginning), and ends when ctx is done; a session
// the storage does not hold fails with ErrNotFound before the first
// yield. Readers never lock: a watcher is a reader that waits.
type Watcher interface {
	Watch(ctx context.Context, session string, after string) (iter.Seq2[Entry, error], error)
}
