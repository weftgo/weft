package thread

import (
	"context"
	"iter"
	"log/slog"
	"time"

	"github.com/weftgo/weft/thread/internal/opencfg"
)

// OpenOption configures a Storage backend at open, one value per
// concern, applied over the defaults. The options live here — in the
// package that owns the Storage contract — so every backend accepts
// the same vocabulary and a caller never learns a backend to say
// Salvage (ADR 0011 §5 names it thread.Salvage). Memory, jsonl.Open
// and sqlite.Open all take them; a backend an option means nothing to
// accepts it and says so. The set is sealed: the options are the ones
// this package returns. Backend authors resolve them with
// thread/backend.Resolve.
type OpenOption interface {
	openOption()
}

// resolveOpen folds opts over the defaults for the in-package backend
// (Memory); thread/backend.Resolve is the same fold for the others.
func resolveOpen(opts []OpenOption) opencfg.Config {
	return opencfg.Resolve(opts)
}

type salvageOption struct{}

func (salvageOption) openOption() {}

// ApplyOpen is the option's effect (opencfg.Applier).
func (salvageOption) ApplyOpen(c *opencfg.Config) { c.Salvage = true }

// Salvage returns the open option that skips malformed lines instead
// of failing the load: each skip is reported in Load's LoadReport
// (Skipped, in file order). A torn final line is always dropped and
// reported — that is a crash, not damage — and data from a newer weft
// still fails with ErrNewerFormat: salvage repairs what a crash wrote,
// never what it cannot read (ADR 0011 §5).
func Salvage() OpenOption { return salvageOption{} }

type syncEveryAppendOption bool

func (syncEveryAppendOption) openOption() {}

// ApplyOpen is the option's effect (opencfg.Applier).
func (o syncEveryAppendOption) ApplyOpen(c *opencfg.Config) { c.SyncEveryAppend = bool(o) }

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

func (noLockOption) openOption() {}

// ApplyOpen is the option's effect (opencfg.Applier).
func (noLockOption) ApplyOpen(c *opencfg.Config) { c.NoLock = true }

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

func (openLoggerOption) openOption() {}

// ApplyOpen is the option's effect (opencfg.Applier).
func (o openLoggerOption) ApplyOpen(c *opencfg.Config) {
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
// that is done. Release speaks for the whole Storage value: it ends a
// Leaser lease whoever holds it, which is why a Session — one writer
// among possibly several on the value — closes through Yield instead
// when the backend offers it.
type Releaser interface {
	Release(ctx context.Context, session string) error
}

// Leaser is the optional Storage capability that makes the one-writer
// rule hold between Session values sharing one Storage value — the
// same small-interface rule. The backend's own lock tells Storage
// values and processes apart; it cannot tell two Sessions on one
// Storage value apart, because Append names a session and not who is
// writing. A lease can: the writer names itself with holder, an opaque
// comparable token — a pointer the writer owns — and the storage
// remembers which holder has the session.
//
// A Session takes the lease before every write, so the first write is
// what makes it the session's writer, and it stays so until its Close
// yields. Reading never takes it: Load, List and Watch — and so Open
// and every read of a Session — are not refused by a lease and do not
// stand in a writer's way.
//
// The lease is bookkeeping over the backend's lock, not a second
// lock: the Storage methods do not consult it. A Storage value used
// directly, without Sessions, enforces one writer per instance;
// Sessions enforce one writer per Session.
type Leaser interface {
	// Acquire takes the session's writer lease for holder and reports
	// how many complete entry lines the storage holds for the session
	// at that moment: every line a Load accounts for — the entries it
	// returns and the lines it skips under Salvage — and never a torn
	// tail. A writer that knows how many it has loaded and written
	// compares: a different number means the session changed behind
	// its view (ErrStale is the Session's answer).
	//
	// created is the stored header's Created — which session this is,
	// not only how long: a session deleted and created again under
	// the same id carries a new one, so a writer that loaded the old
	// session is told apart even when the two happen to hold the same
	// number of entries. It is the zero time when the storage cannot
	// read the header's (a header line this build does not decode);
	// the writer then has only the count to go by.
	//
	// Acquire takes the backend's cross-instance lock exactly as a
	// first Append does — ErrLocked when another Storage or process
	// holds the session, a torn tail repaired — and then the lease:
	// ErrLocked naming the session when a different holder has it on
	// this Storage value. For the holder that already has it Acquire
	// is idempotent and cheap: no I/O. A session the storage does not
	// hold fails with ErrNotFound, a nil holder with a plain error,
	// and nothing is taken either way.
	//
	// The lease ends when its holder yields, when the session is
	// released or deleted through this Storage value, or with the
	// process.
	Acquire(ctx context.Context, session string, holder any) (entries int, created time.Time, err error)

	// Yield ends holder's lease and the storage's hold with it, as
	// Release does. It is Release for a writer that must not let go
	// of what is not its own: when a different holder has the lease,
	// Yield releases nothing and returns nil. When no holder has it —
	// the session is held by this Storage value's direct use, or not
	// held — Yield is Release. A session the storage does not hold
	// fails with ErrNotFound.
	Yield(ctx context.Context, session string, holder any) error
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
