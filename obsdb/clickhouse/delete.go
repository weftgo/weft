package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// Deletion and retention (plan A7, §13.13) on ClickHouse: lightweight
// DELETE FROM … WHERE (GA since ClickHouse 23.3; the tables are all
// MergeTree-family, none carries a projection) on obsdb's tables only —
// otel_traces, otel_logs, weft_records, weft_deltas, weft_runs. Deleted
// rows are masked at once (every read, FINAL included, skips them) and
// leave the parts at the next merge; Vacuum forces that merge. The
// tables' TTLs (TTL, DefaultContentTTL, DefaultMetaTTL) stay as they
// are: Studio's retention is additive.

// deleteChunk bounds one IN list.
const deleteChunk = 400

// runGroup is weft_runs as one row per run: the four columns deletion
// and the sweep read.
const runGroup = `(SELECT RunId, max(ParentRunID) AS P, max(LastSeen) AS L,
	max(FinishedOK) AS F, max(Failed) AS X FROM weft_runs GROUP BY RunId)`

// notRunning is the sweep's status test over runGroup (statusCase's
// rows other than running); one bind: now - InterruptedAfter in ns.
const notRunning = `(X = 1 OR F = 1 OR toUnixTimestamp64Nano(L) < ?)`

// DeleteRun removes a run and its subagent children (obsdb.DB).
func (d *DB) DeleteRun(ctx context.Context, id string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, id); err != nil {
		return err
	}
	ids, err := d.closure(ctx, []string{id}, false)
	if err != nil {
		return err
	}
	return d.deleteRuns(ctx, ids)
}

// DeleteSession removes every run of a session, children included
// (obsdb.DB).
func (d *DB) DeleteSession(ctx context.Context, id string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if id == "" {
		return fmt.Errorf("%w: session %q", obsdb.ErrNotFound, id)
	}
	roots, err := d.queryIDs(ctx, `SELECT RunId FROM (SELECT RunId, max(SessionID) AS S FROM weft_runs GROUP BY RunId) WHERE S = ?`, id)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return fmt.Errorf("%w: session %s", obsdb.ErrNotFound, id)
	}
	ids, err := d.closure(ctx, roots, false)
	if err != nil {
		return err
	}
	return d.deleteRuns(ctx, ids)
}

// Sweep deletes the runs not running whose LastSeen is before the
// cutoff, with their children that are not running (obsdb.DB): a
// candidate whose parent is a candidate too is reached through it.
func (d *DB) Sweep(ctx context.Context, before time.Time) (n int, err error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	defer d.closedErr(&err)
	cut := time.Now().Add(-obsdb.InterruptedAfter).UnixNano()
	rs, err := d.conn.Query(ctx, `SELECT RunId, P FROM `+runGroup+`
		WHERE toUnixTimestamp64Nano(L) < ? AND `+notRunning, before.UnixNano(), cut)
	if err != nil {
		return 0, err
	}
	parent := map[string]string{}
	var order []string
	for rs.Next() {
		var id, p string
		if err := rs.Scan(&id, &p); err != nil {
			_ = rs.Close()
			return 0, err
		}
		parent[id] = p
		order = append(order, id)
	}
	_ = rs.Close()
	if err := rs.Err(); err != nil {
		return 0, err
	}
	var roots []string
	for _, id := range order {
		if p := parent[id]; p == "" || !hasKey(parent, p) {
			roots = append(roots, id)
		}
	}
	ids, err := d.closure(ctx, roots, true)
	if err != nil {
		return 0, err
	}
	if err := d.deleteRuns(ctx, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// vacuumTables are the tables deletions mutate, in the order Vacuum
// merges them.
var vacuumTables = []string{"weft_runs", "weft_records", "weft_deltas", "otel_logs", "otel_traces"}

// Vacuum runs OPTIMIZE TABLE … FINAL on the tables deletions mutate
// (obsdb.DB): every part is merged, which is what drops rows a
// lightweight DELETE masked. Heavy on a large database — the sweep
// never calls it; `weft vacuum` does, on the operator's word.
func (d *DB) Vacuum(ctx context.Context) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	for _, t := range vacuumTables {
		if err := d.conn.Exec(ctx, "OPTIMIZE TABLE "+t+" FINAL"); err != nil {
			return fmt.Errorf("clickhouse: vacuum %s: %w", t, err)
		}
	}
	return nil
}

// closure is roots and every run whose ParentRunID chain reaches one of
// them; sweeping keeps running descendants (and their subtrees) out.
func (d *DB) closure(ctx context.Context, roots []string, sweeping bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, id := range roots {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	cut := time.Now().Add(-obsdb.InterruptedAfter).UnixNano()
	frontier := out
	for len(frontier) > 0 {
		var next []string
		err := inChunks(frontier, func(ph string, args []any) error {
			q := `SELECT RunId FROM ` + runGroup + ` WHERE P IN (` + ph + `)`
			if sweeping {
				q += ` AND ` + notRunning
				args = append(args, cut)
			}
			kids, err := d.queryIDs(ctx, q, args...)
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

// deleteRuns removes the runs' rows: the app log lines under the spans
// obsdb.DeletedSpans names and the runs' own records (both in
// otel_logs), the spans, weft_records, weft_deltas, and weft_runs last,
// so a deletion cut short leaves the run listed and a retry finds it.
func (d *DB) deleteRuns(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	deleted := make(map[string]bool, len(ids))
	for _, id := range ids {
		deleted[id] = true
	}
	var traces []string
	if err := inChunks(ids, func(ph string, args []any) error {
		got, err := d.queryIDs(ctx, `SELECT DISTINCT TraceId FROM otel_traces WHERE RunId IN (`+ph+`)`, args...)
		traces = append(traces, got...)
		return err
	}); err != nil {
		return err
	}
	var links []obsdb.SpanLink
	if err := inChunks(dedup(traces), func(ph string, args []any) error {
		rs, err := d.conn.Query(ctx, `SELECT DISTINCT TraceId, SpanId, ParentSpanId, RunId FROM otel_traces WHERE TraceId IN (`+ph+`)`, args...)
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
	}); err != nil {
		return err
	}
	keys := obsdb.DeletedSpans(links, deleted)
	for start := 0; start < len(keys); start += deleteChunk / 2 {
		chunk := keys[start:min(start+deleteChunk/2, len(keys))]
		args := make([]any, 0, 2*len(chunk))
		for _, k := range chunk {
			args = append(args, k.TraceID, k.SpanID)
		}
		tuples := strings.TrimSuffix(strings.Repeat("(?,?),", len(chunk)), ",")
		if err := d.conn.Exec(ctx, `DELETE FROM otel_logs WHERE LogAttributes['weft.run.id'] = '' AND (TraceId, SpanId) IN (`+tuples+`)`, args...); err != nil {
			return fmt.Errorf("clickhouse: delete app logs: %w", err)
		}
		if err := d.conn.Exec(ctx, `DELETE FROM otel_traces WHERE (TraceId, SpanId) IN (`+tuples+`)`, args...); err != nil {
			return fmt.Errorf("clickhouse: delete spans: %w", err)
		}
	}
	for _, stmt := range []string{
		`DELETE FROM otel_traces WHERE RunId IN (%s)`,
		`DELETE FROM otel_logs WHERE LogAttributes['weft.run.id'] IN (%s)`,
		`DELETE FROM weft_records WHERE RunId IN (%s)`,
		`DELETE FROM weft_deltas WHERE RunId IN (%s)`,
		`DELETE FROM weft_runs WHERE RunId IN (%s)`,
	} {
		if err := inChunks(ids, func(ph string, args []any) error {
			return d.conn.Exec(ctx, fmt.Sprintf(stmt, ph), args...)
		}); err != nil {
			return fmt.Errorf("clickhouse: delete runs: %w", err)
		}
	}
	return nil
}

func (d *DB) queryIDs(ctx context.Context, q string, args ...any) ([]string, error) {
	rs, err := d.conn.Query(ctx, q, args...)
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
