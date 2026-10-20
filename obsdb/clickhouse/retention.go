package clickhouse

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// The run reads' retention cut (U31.1). weft_runs' TTL drops a row
// past its window only when ClickHouse merges the part that holds it —
// seconds or hours after it expired, at a moment no reader chooses — so
// the session list's two queries (the count, then the page) could
// straddle a merge: Total counted a session the page no longer held
// ("1 session" over an empty table), and a session row whose turns were
// cut still counted the expired turn's failure from an uncut read. Every
// read that lists or opens runs therefore cuts at the table's own window
// itself: a collapsed run whose LastSeen is past the TTL is neither
// counted nor paged nor opened, merged away or not, so every read agrees
// with every other — Runs and Search (runsInner, every RunQuery filter:
// the session's, the turn neighbours' TurnBelow / TurnAbove), Sessions,
// Session, its runs and the turn facts (sessionRuns), Run and its
// children, ChildRuns (childRunsSQL), the run tree's rows and its
// id-rule span read (RunTreeSpans: its own row first, a derived run's
// too; treeChildRows, treeSpansByIDRule; the id-rule rows cut in Go,
// runsCut.keeps, a cut one kept as obsdb.TreeCutParent so the id rule
// stops at it) and ResolvePublicID. The builders take the cut as a parameter, so a new
// read cannot leave it out unseen. A run's own records and spans
// (Events, RunSpans, …) are read by id, not listed: they are not cut.
//
// The boundary is computed once per obsdb call, in Go (P9-pre-go-fix3):
// the window read once, the cutoff computed once, and every statement
// of the call compares LastSeen with that one instant — never now64(9)
// per statement — so a run crossing the boundary between Sessions'
// count, page, turn facts and summarizer read is in all of them or in
// none, and a window re-read mid-call cannot change it either. The
// cutoff is an integer literal in the statement text (strconv's int64:
// nothing injectable) rather than a bind: a bind would have to be
// threaded beside each builder's own arguments at every place its
// fragment lands, and sessionRuns lands twice in one statement under a
// q filter.
//
// The boundary follows the server's clock, as the TTL merges that drop
// a row do (P9-pre-go-fix4): the window's read takes the server's
// now64(9) beside engine_full in one statement, the offset (server −
// host, at the read's midpoint) is kept with the window, and the cutoff
// is host now + offset − window — one host clock read per call, so
// still one instant per call, on the server's time. A host whose clock
// runs an hour ahead of the server's cuts what the server's merges
// drop, not an hour more.
//
// The window is the table's clause as SHOW CREATE TABLE tells it (TTL
// LastSeen + toIntervalSecond(n)), read from system.tables — never this
// handle's TTL option, which another Open may have overridden — and
// re-read at most once a minute. A clause in any other shape, or none,
// cuts nothing; so does a window longer than maxRunsTTL (about 100
// years: LastSeen plus it is past DateTime's 2106 ceiling, a TTL no
// merge reaches, and its nanoseconds would overflow a Duration). A
// failed read keeps the last window known and is retried only after a
// minute, one Debug line per failure; with no window known (a failed
// read before any succeeded) the reads cut nothing — they show what
// merges have not dropped yet, as before the cut existed — rather than
// fail a list on a metadata read. A read that failed because the
// caller's context ended (cancelled, or past its deadline) is no
// failure of the table's: it answers the last window known and holds
// nothing, so the next caller reads again — one cancelled request never
// stands for a minute of uncut reads.

// runsTTLRe matches the weft_runs TTL clause the migrations and
// applyTTLs write.
var runsTTLRe = regexp.MustCompile(`TTL LastSeen \+ toIntervalSecond\((\d+)\)`)

// retentionEvery is how long a read window, or a failed read, is reused.
const retentionEvery = time.Minute

// maxRunsTTL is the longest window the cut honours, in seconds: 36,525
// days, about 100 years. A longer one cuts nothing.
const maxRunsTTL = 36525 * 24 * 60 * 60

type retention struct {
	mu     sync.Mutex
	at     time.Time
	secs   int64         // 0: no recognised window
	offset time.Duration // the server's clock − the host's, at the last good read
	// hooks are the tests' seams; nil reads system.tables on the host's
	// clock. A pointer, so DB stays comparable.
	hooks *retentionHooks
}

// retentionHooks are retention's test seams.
type retentionHooks struct {
	// read reads the server's now and weft_runs' engine_full; nil
	// reads system.tables. A zero server time leaves the offset 0.
	read func(ctx context.Context) (server time.Time, engine string, err error)
	// now is the cut's host clock; nil is time.Now.
	now func() time.Time
}

// runsTTLSeconds is weft_runs' TTL window in seconds, 0 when the table
// carries none this package recognises (runsTTL's).
func (d *DB) runsTTLSeconds(ctx context.Context) int64 {
	n, _ := d.runsTTL(ctx)
	return n
}

// runsTTL is weft_runs' TTL window in seconds — 0 when the table
// carries none this package recognises — and the server's clock offset
// from the host's. A failed read keeps the last values (0 before any)
// for retentionEvery, logged at Debug: observability never fails a list
// on a metadata read, nor re-reads a failing one on every call under
// the mutex. A read whose caller's context ended answers the last
// values and holds nothing.
func (d *DB) runsTTL(ctx context.Context) (int64, time.Duration) {
	r := &d.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.at.IsZero() && time.Since(r.at) < retentionEvery {
		return r.secs, r.offset
	}
	read := d.readRunsEngine
	if r.hooks != nil && r.hooks.read != nil {
		read = r.hooks.read
	}
	before := time.Now()
	server, engine, err := read(ctx)
	after := time.Now()
	if err != nil {
		if ctx.Err() != nil {
			// The caller's end, not the table's: nothing is held.
			return r.secs, r.offset
		}
		r.at = after
		slog.DebugContext(ctx, "obsdb/clickhouse: weft_runs' TTL could not be read; the run reads keep the last window known for a minute (0: no cut)",
			"window_s", r.secs, "err", err)
		return r.secs, r.offset
	}
	r.at = after
	r.secs, r.offset = 0, 0
	if !server.IsZero() {
		r.offset = server.Sub(before.Add(after.Sub(before) / 2))
	}
	if m := runsTTLRe.FindStringSubmatch(engine); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 && n <= maxRunsTTL {
			r.secs = n
		}
	}
	return r.secs, r.offset
}

// readRunsEngine is the server's now and weft_runs' engine_full from
// system.tables, in one statement.
func (d *DB) readRunsEngine(ctx context.Context) (time.Time, string, error) {
	var now time.Time
	var engine string
	err := d.conn.QueryRow(ctx,
		`SELECT now64(9), engine_full FROM system.tables WHERE database = currentDatabase() AND name = 'weft_runs'`).Scan(&now, &engine)
	return now, engine, err
}

// runsCut is one obsdb call's retention cut: the runs whose newest
// LastSeen is after afterNs, or every run when on is false (no window
// known).
type runsCut struct {
	on      bool
	afterNs int64
}

// runsCut is the cut for one obsdb call (retention.go's rule): the
// window read once, the boundary host now + offset − window computed
// once. Every statement of the call takes this one value.
func (d *DB) runsCut(ctx context.Context) runsCut {
	n, offset := d.runsTTL(ctx)
	if n <= 0 {
		return runsCut{}
	}
	now := time.Now
	if h := d.retention.hooks; h != nil && h.now != nil {
		now = h.now
	}
	return runsCut{on: true, afterNs: now().Add(offset - time.Duration(n)*time.Second).UnixNano()}
}

// keeps is the cut's rule in Go: whether it keeps a run whose newest
// LastSeen is seen (where's comparison, the same instant).
func (c runsCut) keeps(seen time.Time) bool { return !c.on || seen.UnixNano() > c.afterNs }

// where is the cut over a collapsed (GROUP BY RunId) or FINAL run row:
// " AND toUnixTimestamp64Nano(LastSeen) > <cutoff>", "" with no window.
func (c runsCut) where() string { return c.over("LastSeen") }

// over is the cut over the LastSeen expression col (a grouped read's
// max(LastSeen)).
func (c runsCut) over(col string) string {
	if !c.on {
		return ""
	}
	return " AND toUnixTimestamp64Nano(" + col + ") > " + strconv.FormatInt(c.afterNs, 10)
}
