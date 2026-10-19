package clickhouse

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// The session reads' retention cut (U31.1). weft_runs' TTL drops a row
// past its window only when ClickHouse merges the part that holds it —
// seconds or hours after it expired, at a moment no reader chooses — so
// the session list's two queries (the count, then the page) could
// straddle a merge: Total counted a session the page no longer held
// ("1 session" over an empty table). The session reads therefore cut
// at the table's own window themselves: a collapsed run whose LastSeen
// is past the TTL is neither counted nor paged, merged away or not, so
// Total and the rows agree. The window is the table's clause as
// SHOW CREATE TABLE tells it (TTL LastSeen + toIntervalSecond(n)), read
// from system.tables — never this handle's TTL option, which another
// Open may have overridden — and re-read at most once a minute. A
// clause in any other shape, or none, cuts nothing.

// runsTTLRe matches the weft_runs TTL clause the migrations and
// applyTTLs write.
var runsTTLRe = regexp.MustCompile(`TTL LastSeen \+ toIntervalSecond\((\d+)\)`)

// retentionEvery is how long a read window is reused.
const retentionEvery = time.Minute

type retention struct {
	mu   sync.Mutex
	at   time.Time
	secs int64 // 0: no recognised window
}

// runsTTLSeconds is weft_runs' TTL window in seconds, 0 when the table
// carries none this package recognises. A failed read keeps the last
// value (0 before any): observability never fails a list on a
// metadata read.
func (d *DB) runsTTLSeconds(ctx context.Context) int64 {
	r := &d.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.at.IsZero() && time.Since(r.at) < retentionEvery {
		return r.secs
	}
	var engine string
	if err := d.conn.QueryRow(ctx,
		`SELECT engine_full FROM system.tables WHERE database = currentDatabase() AND name = 'weft_runs'`).Scan(&engine); err != nil {
		return r.secs
	}
	r.secs = 0
	if m := runsTTLRe.FindStringSubmatch(engine); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 {
			r.secs = n
		}
	}
	r.at = time.Now()
	return r.secs
}

// retentionCond is the session reads' cut over a collapsed run row
// (sessionRuns' cond): " AND LastSeen + toIntervalSecond(n) > now64(9)",
// the TTL's own expression, or "" with no recognised window.
func (d *DB) retentionCond(ctx context.Context) string {
	n := d.runsTTLSeconds(ctx)
	if n <= 0 {
		return ""
	}
	return " AND LastSeen + toIntervalSecond(" + strconv.FormatInt(n, 10) + ") > now64(9)"
}
