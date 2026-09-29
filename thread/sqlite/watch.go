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
// reader that waits, and waiting costs one bounded query per tick. The
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
// hold fails the same way. The load rules follow the backend's own:
// data from a newer weft is ErrNewerFormat, never skipped; anything
// else undecodable is ErrCorrupt naming the row's line, unless the
// storage was opened with Salvage, which skips. Nothing torn can
// exist here — every row is a committed transaction.
func (b *backend) Watch(ctx context.Context, session string, after string) (iter.Seq2[thread.Entry, error], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !thread.ValidID(session) {
		return nil, fmtNotFound(session)
	}
	var one int
	err := b.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ?`, session).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmtNotFound(session)
	}
	if err != nil {
		return nil, err
	}
	next := 0
	if after != "" {
		// The resume point: the entry's row number. A line's id names
		// it without a full decode.
		var seq int
		err := b.db.QueryRowContext(ctx,
			`SELECT seq FROM entries WHERE session = ? AND json_extract(line, '$.id') = ?`,
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
		for {
			rows, err := b.db.QueryContext(ctx,
				`SELECT line FROM entries WHERE session = ? AND seq >= ? ORDER BY seq`, session, next)
			if err != nil {
				yield(nil, err)
				return
			}
			advanced := false
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					_ = rows.Close()
					yield(nil, err)
					return
				}
				e, err := thread.UnmarshalEntry([]byte(line))
				next++
				advanced = true
				if err == nil {
					if !yield(e, nil) {
						_ = rows.Close()
						return
					}
					continue
				}
				if errors.Is(err, thread.ErrNewerFormat) {
					_ = rows.Close()
					yield(nil, err) // loud, salvage or not
					return
				}
				if b.salvage {
					continue // a skip, as Load would report
				}
				_ = rows.Close()
				yield(nil, &thread.CorruptError{Session: session, Line: next + 1, Err: err})
				return
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				yield(nil, err)
				return
			}
			_ = rows.Close()
			// Nothing new: wait for the next tick or the end.
			if !advanced {
				tick := time.NewTicker(pollInterval)
				select {
				case <-ctx.Done():
					tick.Stop()
					return
				case <-tick.C:
				}
				tick.Stop()
			}
			if err := ctx.Err(); err != nil {
				return
			}
		}
	}, nil
}
