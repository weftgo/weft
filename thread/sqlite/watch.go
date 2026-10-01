package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/weftgo/weft/thread"
)

// pollInterval is how often a watcher looks for new rows: a tail is a
// reader that waits, and waiting costs one indexed query per tick —
// the rows past the last one served, never the session. The
// interval is a constant for the same reason jsonl's is: a watcher's
// contract is arrival order and exactly-once, not latency.
const pollInterval = 200 * time.Millisecond

// Watch returns the session's entries as they arrive — the
// thread.Watcher capability, a tail of a session another process
// writes; WAL lets this read while the writer commits. The sequence
// yields the session's existing entries first (from the one after
// `after`, empty for the whole session), in arrival order, each
// exactly once, then keeps yielding as appends commit, and ends when
// ctx is done. A session the database does not hold fails with
// ErrNotFound before the first yield; an `after` the session does not
// hold fails with a plain error. The load rules follow the backend's
// own: data from a newer weft is ErrNewerFormat, never skipped;
// anything else undecodable is ErrCorrupt naming the row's line,
// unless the storage was opened with Salvage, which skips; a torn
// final row is never yielded — the tail waits at it until a writer
// removes it and appends.
//
// Each poll reads its rows to the end and closes the query before the
// first of them is yielded: the tail holds no connection while the
// consumer runs, so the consumer may Append, Load, List or Delete on
// this same Storage from inside the loop. A session deleted under its
// watcher ends the stream with ErrNotFound, and so does one deleted
// and created again — the tail follows the session it opened (its
// generation token), never whichever session later wears the id.
func (b *backend) Watch(ctx context.Context, session string, after string) (iter.Seq2[thread.Entry, error], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !thread.ValidID(session) {
		return nil, fmtNotFound(session)
	}
	var gen string
	err := b.db.QueryRowContext(ctx, `SELECT gen FROM sessions WHERE id = ?`, session).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmtNotFound(session)
	}
	if err != nil {
		return nil, err
	}
	next := 0
	if after != "" {
		// The resume point: the entry's row number. A line's id names
		// it without a full decode; a line that is not JSON has no id.
		var seq int
		err := b.db.QueryRowContext(ctx,
			`SELECT seq FROM entries WHERE session = ? AND torn = 0
			   AND CASE WHEN json_valid(line) THEN json_extract(line, '$.id') = ? ELSE 0 END
			 ORDER BY seq LIMIT 1`,
			session, after).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("thread: session %s holds no entry %q to watch after", session, after)
		}
		if err != nil {
			return nil, err
		}
		next = seq + 1
	}

	return func(yield func(thread.Entry, error) bool) {
		tick := time.NewTicker(pollInterval)
		defer tick.Stop()
		for {
			// A canceled tail ends cleanly, not with the query error a
			// canceled context would answer: the consumer asked for the
			// end, and one terminal error means one thing went wrong.
			if ctx.Err() != nil {
				return
			}
			rows, err := b.poll(ctx, session, gen, next)
			if err != nil {
				if ctx.Err() != nil {
					return // the cancellation won the race
				}
				yield(nil, err)
				return
			}
			for _, r := range rows {
				if r.torn {
					break // a tail no writer has repaired yet: wait at it
				}
				next = r.seq + 1
				e, err := thread.UnmarshalEntry([]byte(r.line))
				switch {
				case err == nil:
					if !yield(e, nil) {
						return
					}
				case errors.Is(err, thread.ErrNewerFormat):
					yield(nil, err) // loud, salvage or not
					return
				case b.salvage:
					// a skip, as Load would report
				default:
					// The header is line 1; row seq is line seq+2.
					yield(nil, &thread.CorruptError{Session: session, Line: r.seq + 2, Err: err})
					return
				}
			}
			if len(rows) > 0 && !rows[len(rows)-1].torn {
				continue // served something: look again before waiting
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}, nil
}

// watchRow is one entry row a poll read.
type watchRow struct {
	seq  int
	line string
	torn bool
}

// poll reads the session's rows from seq on, whole, and closes the
// query before returning — the caller yields with no connection held.
// The rows are read only from the generation the watcher opened: a
// session that is gone, or was replaced by a new one under the same
// id, is ErrNotFound. One statement is one snapshot, so the rows and
// the generation they were matched against cannot disagree; the
// follow-up existence check only runs when there were no rows to
// vouch for the session.
func (b *backend) poll(ctx context.Context, session, gen string, seq int) ([]watchRow, error) {
	rs, err := b.db.QueryContext(ctx,
		`SELECT e.seq, e.line, e.torn FROM entries e JOIN sessions s ON s.id = e.session
		  WHERE e.session = ? AND s.gen = ? AND e.seq >= ? ORDER BY e.seq`, session, gen, seq)
	if err != nil {
		return nil, err
	}
	var rows []watchRow
	for rs.Next() {
		var r watchRow
		var torn int
		if err := rs.Scan(&r.seq, &r.line, &torn); err != nil {
			_ = rs.Close()
			return nil, err
		}
		r.torn = torn == 1
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		_ = rs.Close()
		return nil, err
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return rows, nil
	}
	var now string
	err = b.db.QueryRowContext(ctx, `SELECT gen FROM sessions WHERE id = ?`, session).Scan(&now)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && now != gen) {
		return nil, fmtNotFound(session)
	}
	return nil, err
}
