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
// storage was opened with Salvage, which skips. A torn row (only
// Inject writes one) is a writer mid-append, as in jsonl: the tail
// waits at it and never yields it. A session deleted under its
// watcher ends the stream with ErrNotFound — also when its id was
// created again before the next poll: the new row is another session.
//
// Each poll reads its batch in one read transaction and releases the
// connection before the first yield: the handle holds one connection,
// and a consumer that writes to the same Storage from inside its loop
// (or is merely slow) must never starve the handle's other callers.
func (b *backend) Watch(ctx context.Context, session string, after string) (iter.Seq2[thread.Entry, error], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !thread.ValidID(session) {
		return nil, fmtNotFound(session)
	}
	// The session's incarnation: its rowid and header line. A Delete
	// and a Create of the same id between two polls changes one or
	// both (a header carries its Created to the nanosecond).
	var born incarnation
	err := b.db.QueryRowContext(ctx, `SELECT rowid, header FROM sessions WHERE id = ?`, session).Scan(&born.rowid, &born.header)
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
			// A canceled tail ends cleanly, not with the query error a
			// canceled context would answer: the consumer asked for the
			// end, and one terminal error means one thing went wrong.
			if ctx.Err() != nil {
				return
			}
			batch, gone, err := b.poll(ctx, session, born, next)
			if err != nil {
				if ctx.Err() == nil {
					yield(nil, err)
				}
				return
			}
			if gone {
				yield(nil, fmtNotFound(session))
				return
			}
			for _, r := range batch {
				next = r.seq + 1
				e, err := thread.UnmarshalEntry([]byte(r.line))
				if err == nil {
					if !yield(e, nil) {
						return
					}
					continue
				}
				if errors.Is(err, thread.ErrNewerFormat) {
					yield(nil, err) // loud, salvage or not
					return
				}
				if b.salvage {
					continue // a skip, as Load would report
				}
				yield(nil, &thread.CorruptError{Session: session, Line: r.seq + 2, Err: err})
				return
			}
			if ctx.Err() != nil {
				return
			}
			if len(batch) == 0 {
				// Nothing new: wait for the next tick or the end.
				tick := time.NewTimer(pollInterval)
				select {
				case <-ctx.Done():
					tick.Stop()
					return
				case <-tick.C:
				}
			}
		}
	}, nil
}

// incarnation names one life of a session id: its sessions rowid and
// header line.
type incarnation struct {
	rowid  int64
	header string
}

// watchRow is one entry row a poll read.
type watchRow struct {
	seq  int
	line string
}

// poll reads the rows from seq `from` on in one read transaction, the
// session's incarnation checked in the same snapshot: gone is true
// when the session was deleted — or deleted and created again — since
// the watch began. The batch stops before a torn row, which waits for
// the append that replaces it. The connection is released before
// poll returns.
func (b *backend) poll(ctx context.Context, session string, born incarnation, from int) (batch []watchRow, gone bool, err error) {
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer rollback(tx)
	var now incarnation
	err = tx.QueryRowContext(ctx, `SELECT rowid, header FROM sessions WHERE id = ?`, session).Scan(&now.rowid, &now.header)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && now != born) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT seq, line, torn FROM entries WHERE session = ? AND seq >= ? ORDER BY seq`, session, from)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r watchRow
		var torn int
		if err := rows.Scan(&r.seq, &r.line, &torn); err != nil {
			return nil, false, err
		}
		if torn == 1 {
			break // a writer mid-append: wait for it
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return batch, false, nil
}
