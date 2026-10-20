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
// id-rule span read (RunTreeSpans: treeRows, treeSpansByIDRule) and
// ResolvePublicID. The builders take the cut as a parameter, so a new
// read cannot leave it out unseen. A run's own records and spans
// (Events, RunSpans, …) are read by id, not listed: they are not cut.
//
// The boundary is computed once per obsdb call, in Go (P9-pre-go-fix3):
// the window read once, cutoff = now − window, and every statement of
// the call compares LastSeen with that one instant — never now64(9) per
// statement — so a run crossing the boundary between Sessions' count,
// page, turn facts and summarizer read is in all of them or in none,
// and a window re-read mid-call cannot change it either. The cutoff is
// an integer literal in the statement text (strconv's int64: nothing
// injectable) rather than a bind: a bind would have to be threaded
// beside each builder's own arguments at every place its fragment
// lands, and sessionRuns lands twice in one statement under a q filter.
//
// The window is the table's clause as SHOW CREATE TABLE tells it (TTL
// LastSeen + toIntervalSecond(n)), read from system.tables — never this
// handle's TTL option, which another Open may have overridden — and
// re-read at most once a minute. A clause in any other shape, or none,
// cuts nothing. A failed read keeps the last window known and is
// retried only after a minute, one Debug line per failure; with no
// window known (a failed read before any succeeded) the reads cut
// nothing — they show what merges have not dropped yet, as before the
// cut existed — rather than fail a list on a metadata read.

// runsTTLRe matches the weft_runs TTL clause the migrations and
// applyTTLs write.
var runsTTLRe = regexp.MustCompile(`TTL LastSeen \+ toIntervalSecond\((\d+)\)`)

// retentionEvery is how long a read window, or a failed read, is reused.
const retentionEvery = time.Minute

type retention struct {
	mu   sync.Mutex
	at   time.Time
	secs int64 // 0: no recognised window
	// read reads weft_runs' engine_full; nil reads system.tables
	// (tests swap in a failing read).
	read func(ctx context.Context) (string, error)
	// now is the cut's clock; nil is time.Now (tests move it).
	now func() time.Time
}

// runsTTLSeconds is weft_runs' TTL window in seconds, 0 when the table
// carries none this package recognises. A failed read keeps the last
// value (0 before any) for retentionEvery, logged at Debug:
// observability never fails a list on a metadata read, nor re-reads a
// failing one on every call under the mutex.
func (d *DB) runsTTLSeconds(ctx context.Context) int64 {
	r := &d.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.at.IsZero() && time.Since(r.at) < retentionEvery {
		return r.secs
	}
	read := r.read
	if read == nil {
		read = d.readRunsEngine
	}
	engine, err := read(ctx)
	r.at = time.Now()
	if err != nil {
		slog.DebugContext(ctx, "obsdb/clickhouse: weft_runs' TTL could not be read; the run reads keep the last window known for a minute (0: no cut)",
			"window_s", r.secs, "err", err)
		return r.secs
	}
	r.secs = 0
	if m := runsTTLRe.FindStringSubmatch(engine); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 {
			r.secs = n
		}
	}
	return r.secs
}

// readRunsEngine is weft_runs' engine_full from system.tables.
func (d *DB) readRunsEngine(ctx context.Context) (string, error) {
	var engine string
	err := d.conn.QueryRow(ctx,
		`SELECT engine_full FROM system.tables WHERE database = currentDatabase() AND name = 'weft_runs'`).Scan(&engine)
	return engine, err
}

// runsCut is one obsdb call's retention cut: the runs whose newest
// LastSeen is after afterNs, or every run when on is false (no window
// known).
type runsCut struct {
	on      bool
	afterNs int64
}

// runsCut is the cut for one obsdb call (retention.go's rule): the
// window read once, the boundary now − window computed once. Every
// statement of the call takes this one value.
func (d *DB) runsCut(ctx context.Context) runsCut {
	n := d.runsTTLSeconds(ctx)
	if n <= 0 {
		return runsCut{}
	}
	now := time.Now
	if d.retention.now != nil {
		now = d.retention.now
	}
	return runsCut{on: true, afterNs: now().Add(-time.Duration(n) * time.Second).UnixNano()}
}

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
