package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/caps"
)

// Deletion and retention (plan A7, §13.13): every statement below names
// one of obsdb's own tables — runs, records, spans, other_logs,
// run_search, run_tools, runs_fts, session_events — and nothing else. A file that also
// holds thread's sessions (sessions, entries, session_locks,
// thread_migrations) keeps them byte-identical: Studio never writes the
// app's conversation store.

// deleteChunk bounds one IN list: far under SQLite's bind-variable
// limit, so a sweep of many runs is many statements, never one too big.
const deleteChunk = 400

// DeleteRun removes a run and its subagent children (obsdb.DB).
func (d *DB) DeleteRun(ctx context.Context, id string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	return d.deleteTx(ctx, func(tx *sql.Tx) ([]string, error) {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE run_id = ?`, id).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("%w: run %s", obsdb.ErrNotFound, id)
		}
		return closure(ctx, tx, []string{id}, false)
	})
}

// DeleteSession removes every run of a session, children included
// (obsdb.DB).
func (d *DB) DeleteSession(ctx context.Context, id string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	return d.deleteTx(ctx, func(tx *sql.Tx) ([]string, error) {
		roots, err := queryIDs(ctx, tx, `SELECT run_id FROM runs WHERE session_id = ? AND session_id <> ''`, id)
		if err != nil {
			return nil, err
		}
		// The session's events go whole, a record filed under no run
		// (held at Close) included: session_events is keyed by session.
		res, err := tx.ExecContext(ctx, `DELETE FROM session_events WHERE session_id = ? AND session_id <> ''`, id)
		if err != nil {
			return nil, err
		}
		if len(roots) == 0 {
			if n, _ := res.RowsAffected(); n > 0 {
				return nil, nil // a session of runless events alone: they were its whole record
			}
			return nil, fmt.Errorf("%w: session %s", obsdb.ErrNotFound, id)
		}
		return closure(ctx, tx, roots, false)
	})
}

// notRunning is the sweep's status test over a runs row: failed,
// succeeded, or interrupted (last seen before the bound parameter, now -
// InterruptedAfter) — statusExpr's rows other than running.
const notRunning = `(failed = 1 OR finished_ok = 1 OR root_ended = 1 OR last_seen_ns < ?)`

// Sweep deletes the runs not running whose last_seen is before the
// cutoff, with their children that are not running (obsdb.DB). A run's
// subtree is swept from its topmost not-running ancestor: a candidate
// whose parent is a stored run is reached through it, or — when the
// parent is running, or was never stored — is its own root. Each pass
// is one writer transaction over at most obsdb.SweepBatch roots, so a
// write waits on one pass, never on the whole sweep; a pass that found
// fewer roots than the batch was the last. The passes done, the orphan
// spans older than the cutoff go too (sweepOrphanSpans).
func (d *DB) Sweep(ctx context.Context, before time.Time) (n int, err error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	defer d.closedErr(&err)
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		roots, deleted, err := d.sweepPass(ctx, before)
		if err != nil {
			return n, err
		}
		n += deleted
		if err := d.sweepRunlessEvents(ctx, before); err != nil {
			return n, err
		}
		if roots < caps.SweepBatch {
			return n, d.sweepOrphanSpans(ctx, before)
		}
		if sweepPassHook != nil {
			sweepPassHook()
		}
	}
}

// sweepPassHook, when set (tests), runs between two Sweep passes, with
// no transaction open.
var sweepPassHook func()

// sweepPass deletes one batch: up to obsdb.SweepBatch roots and their
// not-running subtrees, in one writer transaction. It returns how many
// roots it found and how many runs it deleted.
func (d *DB) sweepPass(ctx context.Context, before time.Time) (roots, deleted int, err error) {
	extra, err := d.deleteTxCount(ctx, func(tx *sql.Tx) ([]string, error) {
		cut := time.Now().Add(-obsdb.InterruptedAfter).UnixNano()
		ids, err := queryIDs(ctx, tx, `SELECT run_id FROM runs AS r
			WHERE last_seen_ns < ? AND `+notRunning+`
			AND NOT EXISTS (SELECT 1 FROM runs AS p WHERE p.run_id = r.parent_run_id AND r.parent_run_id <> ''
				AND p.last_seen_ns < ? AND (p.failed = 1 OR p.finished_ok = 1 OR p.root_ended = 1 OR p.last_seen_ns < ?))
			LIMIT ?`,
			unixNS(before), cut, unixNS(before), cut, caps.SweepBatch)
		if err != nil {
			return nil, err
		}
		roots = len(ids)
		all, err := closure(ctx, tx, ids, true)
		deleted = len(all)
		return all, err
	})
	if err != nil {
		return 0, 0, err
	}
	return roots, deleted + extra, nil
}

// sweepRunlessEvents deletes, after a sweep pass, the session events
// filed under no run (a thread record held at Close) older than the
// cutoff whose session has no run left: the pass took the last turn
// they sat beside, and no run delete names them (their run_id is "").
// A session that still has a run keeps them, as DeleteSession would
// take them with it. Its own writer statements, the pass committed, in
// batches of obsdb.SweepBatch events (deleteInBatches).
func (d *DB) sweepRunlessEvents(ctx context.Context, before time.Time) error {
	return d.deleteInBatches(ctx, `DELETE FROM session_events WHERE (session_id, run_id, kind, pos) IN (
		SELECT session_id, run_id, kind, pos FROM session_events
		WHERE run_id = '' AND time_ns < ?
		AND NOT EXISTS (SELECT 1 FROM runs WHERE runs.session_id = session_events.session_id AND runs.session_id <> '')
		LIMIT ?)`, unixNS(before))
}

// sweepOrphanSpans deletes the spans filed under a run id no runs row
// holds that started before the cutoff (P9-pre-go-fix3). Only a
// summarizer span is ever one: every other span with a run id makes or
// keeps its run's row (Write), and DeleteRun and the passes take a
// run's spans with it — but a summarizer span feeds no row, so one
// landing after its run was deleted is stored under the gone id and no
// run delete names it. Write keeps it: it cannot tell a deleted run
// from one whose records are still on their way (an OTLP span batch can
// land before the run's run_start), so the orphan waits for the cutoff,
// as the run would have. ClickHouse needs nothing alike: otel_traces'
// TTL drops every span, orphan or not, on its own clock. Narrowed by
// spans_start; in batches of obsdb.SweepBatch spans, each its own
// writer statement (P9-pre-go-fix4), so a write waits on one batch,
// never on every orphan at once.
func (d *DB) sweepOrphanSpans(ctx context.Context, before time.Time) error {
	return d.deleteInBatches(ctx, `DELETE FROM spans WHERE (trace_id, span_id) IN (
		SELECT trace_id, span_id FROM spans
		WHERE start_ns < ? AND run_id <> ''
		AND NOT EXISTS (SELECT 1 FROM runs WHERE runs.run_id = spans.run_id)
		LIMIT ?)`, unixNS(before))
}

// deleteBatchHook, when set (tests), sees each deleteInBatches batch's
// row count, its statement committed.
var deleteBatchHook func(n int64)

// deleteInBatches runs stmt — a DELETE whose last bind is its batch's
// LIMIT — until a batch deletes fewer than obsdb.SweepBatch rows, each
// batch its own writer statement.
func (d *DB) deleteInBatches(ctx context.Context, stmt string, args ...any) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := d.writer.ExecContext(ctx, stmt, append(args[:len(args):len(args)], caps.SweepBatch)...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if deleteBatchHook != nil {
			deleteBatchHook(n)
		}
		if n < int64(caps.SweepBatch) {
			return nil
		}
	}
}

// Vacuum folds the search index's segments and runs VACUUM (obsdb.DB):
// the whole file is rewritten, every table's contents unchanged —
// thread's too, when it shares the file. VACUUM needs the file to
// itself for its duration; a writer in another process waits on the
// busy timeout. In WAL mode VACUUM writes the new file through the
// WAL, so a checkpoint follows (TRUNCATE: the WAL back to zero bytes
// when no reader holds it): the file's size after a vacuum is its size,
// not the main file's plus a WAL the size of the database (UX-S5a-12).
// A checkpoint a reader blocks is not an error — the next one folds it.
func (d *DB) Vacuum(ctx context.Context) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if _, err := d.writer.ExecContext(ctx, `INSERT INTO runs_fts(runs_fts) VALUES ('optimize')`); err != nil {
		return err
	}
	if _, err = d.writer.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	_, err = d.writer.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// deleteTx runs one deletion in a writer transaction: pick chooses the
// run ids (an error aborts, ErrNotFound included), deleteRuns removes
// them.
func (d *DB) deleteTx(ctx context.Context, pick func(*sql.Tx) ([]string, error)) error {
	_, err := d.deleteTxCount(ctx, pick)
	return err
}

// deleteTxCount is deleteTx reporting how many derived rows went beside
// the picked ids (deleteRuns: a stock agent under a deleted run's tool).
func (d *DB) deleteTxCount(ctx context.Context, pick func(*sql.Tx) ([]string, error)) (int, error) {
	tx, err := d.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := pick(tx)
	if err != nil {
		return 0, err
	}
	extra, err := deleteRuns(ctx, tx, ids)
	if err != nil {
		return 0, err
	}
	return extra, tx.Commit()
}

// closure is roots and every run whose parent_run_id chain reaches one
// of them. sweeping keeps a descendant out when it is running (and so
// its subtree with it): the sweep never deletes a running run.
func closure(ctx context.Context, tx *sql.Tx, roots []string, sweeping bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	frontier := roots
	for _, id := range roots {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	cut := time.Now().Add(-obsdb.InterruptedAfter).UnixNano()
	for len(frontier) > 0 {
		var next []string
		err := inChunks(frontier, func(ph string, args []any) error {
			q := `SELECT run_id FROM runs WHERE parent_run_id IN (` + ph + `)`
			if sweeping {
				q += ` AND ` + notRunning
				args = append(args, cut)
			}
			kids, err := queryIDs(ctx, tx, q, args...)
			for _, k := range kids {
				if !seen[k] {
					seen[k] = true
					out = append(out, k)
					next = append(next, k)
				}
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		frontier = next
	}
	return out, nil
}

// deleteRuns removes the runs' rows from every obsdb table: the search
// index first (runs_fts has no run_id: its rows go by run_search's ids),
// then the records, the spans DeletedSpans names with the app log
// records under them, and the run rows — and the rows of the derived
// runs whose roots those spans take (obsdb.StockRunsOf), whose number
// it returns as extra.
func deleteRuns(ctx context.Context, tx *sql.Tx, ids []string) (extra int, err error) {
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := make(map[string]bool, len(ids))
	for _, id := range ids {
		deleted[id] = true
	}
	var traces []string
	err = inChunks(ids, func(ph string, args []any) error {
		got, err := queryIDs(ctx, tx, `SELECT DISTINCT trace_id FROM spans WHERE run_id IN (`+ph+`)`, args...)
		traces = append(traces, got...)
		return err
	})
	if err != nil {
		return 0, err
	}
	traces = append(traces, obsdb.StockTraces(ids)...) // derived runs: their spans carry no run id
	var links []obsdb.SpanLink
	err = inChunks(dedup(traces), func(ph string, args []any) error {
		rs, err := tx.QueryContext(ctx, `SELECT trace_id, span_id, parent_span_id, run_id FROM spans WHERE trace_id IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rs.Close() }()
		for rs.Next() {
			var l obsdb.SpanLink
			if err := rs.Scan(&l.TraceID, &l.SpanID, &l.ParentSpanID, &l.RunID); err != nil {
				return err
			}
			links = append(links, l)
		}
		return rs.Err()
	})
	if err != nil {
		return 0, err
	}
	keys := obsdb.DeletedSpans(links, deleted)
	// A derived run whose root span goes with them — a stock agent
	// under a deleted weft run's tool — goes too: its row would
	// otherwise outlive every span it was derived from.
	derivedIDs := obsdb.StockRunsOf(links, keys, deleted)
	if err := inChunks(derivedIDs, func(ph string, args []any) error {
		got, err := queryIDs(ctx, tx, `SELECT run_id FROM runs WHERE run_id IN (`+ph+`)`, args...)
		extra += len(got)
		return err
	}); err != nil {
		return 0, err
	}
	ids = append(ids, derivedIDs...)
	for _, stmt := range []string{
		`DELETE FROM runs_fts WHERE rowid IN (SELECT id FROM run_search WHERE run_id IN (%s))`,
		`DELETE FROM run_search WHERE run_id IN (%s)`,
		`DELETE FROM run_tools WHERE run_id IN (%s)`,
		`DELETE FROM records WHERE run_id IN (%s)`,
		`DELETE FROM session_events WHERE run_id IN (%s) AND run_id <> ''`,
	} {
		if err := inChunks(ids, func(ph string, args []any) error {
			_, err := tx.ExecContext(ctx, fmt.Sprintf(stmt, ph), args...)
			return err
		}); err != nil {
			return 0, err
		}
	}
	for start := 0; start < len(keys); start += deleteChunk / 2 {
		chunk := keys[start:min(start+deleteChunk/2, len(keys))]
		args := make([]any, 0, 2*len(chunk))
		for _, k := range chunk {
			args = append(args, k.TraceID, k.SpanID)
		}
		values := strings.TrimSuffix(strings.Repeat("(?,?),", len(chunk)), ",")
		if _, err := tx.ExecContext(ctx, `DELETE FROM other_logs WHERE (trace_id, span_id) IN (VALUES `+values+`)`, args...); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM spans WHERE (trace_id, span_id) IN (VALUES `+values+`)`, args...); err != nil {
			return 0, err
		}
	}
	// A run's own spans are all in keys; this catches a span whose
	// trace id the first read did not see (none today) so no span of a
	// deleted run outlives it.
	if err := inChunks(ids, func(ph string, args []any) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM spans WHERE run_id IN (`+ph+`)`, args...)
		return err
	}); err != nil {
		return 0, err
	}
	return extra, inChunks(ids, func(ph string, args []any) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE run_id IN (`+ph+`)`, args...)
		return err
	})
}

// inChunks calls fn for each deleteChunk-sized slice of ids with its
// placeholder list and args.
func inChunks(ids []string, fn func(placeholders string, args []any) error) error {
	for start := 0; start < len(ids); start += deleteChunk {
		chunk := ids[start:min(start+deleteChunk, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		if err := fn(strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ","), args); err != nil {
			return err
		}
	}
	return nil
}

func queryIDs(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rs, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rs.Err()
}

func dedup(ids []string) []string {
	seen := map[string]bool{}
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
