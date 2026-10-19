package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/caps"
)

// Deletion and retention (plan A7, §13.13) on ClickHouse: lightweight
// DELETE FROM … WHERE (GA since ClickHouse 23.3; the tables are all
// MergeTree-family, none carries a projection) on obsdb's tables only —
// otel_traces, otel_logs, weft_records, weft_deltas, weft_session_events,
// weft_runs. Deleted
// rows are masked at once (every read, FINAL included, skips them) and
// leave the parts at the next merge; Vacuum forces that merge. The
// tables' TTLs (TTL, DefaultContentTTL, DefaultMetaTTL) stay as they
// are: Studio's retention is additive.

// deleteChunk bounds one IN list.
const deleteChunk = 400

// runGroup is weft_runs as one row per run: the four columns deletion
// and the sweep read.
const runGroup = `(SELECT RunId, max(ParentRunID) AS P, max(LastSeen) AS L,
	max(greatest(FinishedOK, RootEnded)) AS F, max(Failed) AS X FROM weft_runs GROUP BY RunId)`

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
	_, err = d.deleteRuns(ctx, ids)
	return err
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
	// The session's events go whole, a record filed under no run (held
	// at Close) included: weft_session_events is keyed by session.
	evs, err := d.queryIDs(ctx, `SELECT DISTINCT RunId FROM weft_session_events WHERE SessionId = ?`, id)
	if err != nil {
		return err
	}
	if len(roots) == 0 && len(evs) == 0 {
		return fmt.Errorf("%w: session %s", obsdb.ErrNotFound, id)
	}
	ids, err := d.closure(ctx, roots, false)
	if err != nil {
		return err
	}
	if _, err = d.deleteRuns(ctx, ids); err != nil {
		return err
	}
	sctx := ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	for _, stmt := range []string{
		`DELETE FROM otel_logs WHERE LogAttributes['weft.run.id'] = '' AND LogAttributes['weft.session.id'] = ? AND LogAttributes['weft.record'] IN ('thread', 'compaction')`,
		`DELETE FROM weft_session_events WHERE SessionId = ?`,
	} {
		if err := d.conn.Exec(sctx, stmt, id); err != nil {
			return fmt.Errorf("clickhouse: delete session events: %w", err)
		}
	}
	return nil
}

// Sweep deletes the runs not running whose LastSeen is before the
// cutoff, with their children that are not running (obsdb.DB): a
// candidate whose parent is a candidate too is reached through it. The
// candidates are read once (one row of two strings each); the deletion
// goes in passes of obsdb.SweepBatch roots, so a large backlog is a
// bounded round of mutations at a time rather than one storm.
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
	// Passes of at most obsdb.SweepBatch roots: the roots' subtrees are
	// disjoint (a root's parent is no candidate), so each pass is its
	// own closure and its own round of mutations, ctx checked between.
	for start := 0; start < len(roots); start += caps.SweepBatch {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		ids, err := d.closure(ctx, roots[start:min(start+caps.SweepBatch, len(roots))], true)
		if err != nil {
			return n, err
		}
		extra, err := d.deleteRuns(ctx, ids)
		if err != nil {
			return n, err
		}
		n += len(ids) + extra
	}
	return n, nil
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
// obsdb.DeletedSpans names and the non-weft spans below the runs' own
// (planSpanDeletes' chunks), then the runs' own spans, their records
// (otel_logs), weft_records, weft_deltas, and weft_runs last, so a
// deletion cut short leaves the run listed and a retry finds it — with
// its own spans still there, so the retry's trace lookup (RunId IN …)
// still reaches the non-weft spans it has yet to delete. Each chunk is
// up to seven synchronous lightweight DELETE mutations (clickhouse
// README): a large sweep is many mutations. The rows of the derived
// runs whose roots those spans take (obsdb.StockRunsOf) go too; extra
// is how many there were.
func (d *DB) deleteRuns(ctx context.Context, ids []string) (extra int, err error) {
	if len(ids) == 0 {
		return 0, nil
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
		return 0, err
	}
	traces = append(traces, obsdb.StockTraces(ids)...) // derived runs: their spans carry no run id
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
		return 0, err
	}
	keys := obsdb.DeletedSpans(links, deleted)
	// A derived run whose root span goes with them goes too (the
	// SQLite backend's rule): no row outlives every span it was
	// derived from.
	derivedIDs := obsdb.StockRunsOf(links, keys, deleted)
	if err := inChunks(derivedIDs, func(ph string, args []any) error {
		got, err := d.queryIDs(ctx, `SELECT DISTINCT RunId FROM weft_runs WHERE RunId IN (`+ph+`)`, args...)
		extra += len(got)
		return err
	}); err != nil {
		return 0, err
	}
	ids = append(ids, derivedIDs...)
	for _, c := range planSpanDeletes(links, keys) {
		if len(c.logs) > 0 {
			args, tuples := keyTuples(c.logs)
			if err := d.conn.Exec(ctx, `DELETE FROM otel_logs WHERE LogAttributes['weft.run.id'] = '' AND (TraceId, SpanId) IN (`+tuples+`)`, args...); err != nil {
				return 0, fmt.Errorf("clickhouse: delete app logs: %w", err)
			}
		}
		if len(c.spans) > 0 {
			args, tuples := keyTuples(c.spans)
			if err := d.conn.Exec(ctx, `DELETE FROM otel_traces WHERE (TraceId, SpanId) IN (`+tuples+`)`, args...); err != nil {
				return 0, fmt.Errorf("clickhouse: delete spans: %w", err)
			}
		}
	}
	for _, stmt := range []string{
		`DELETE FROM otel_traces WHERE RunId IN (%s)`,
		`DELETE FROM otel_logs WHERE LogAttributes['weft.run.id'] IN (%s)`,
		`DELETE FROM weft_records WHERE RunId IN (%s)`,
		`DELETE FROM weft_deltas WHERE RunId IN (%s)`,
		`DELETE FROM weft_session_events WHERE RunId IN (%s) AND RunId != ''`,
		`DELETE FROM weft_runs WHERE RunId IN (%s)`,
	} {
		if err := inChunks(ids, func(ph string, args []any) error {
			return d.conn.Exec(ctx, fmt.Sprintf(stmt, ph), args...)
		}); err != nil {
			return 0, fmt.Errorf("clickhouse: delete runs: %w", err)
		}
	}
	return extra, nil
}

// spanChunk is one round of the key loop: the app log lines under
// logs, and the spans in spans.
type spanChunk struct{ logs, spans []obsdb.SpanKey }

// planSpanDeletes chunks DeletedSpans' keys for the key loop. Every key
// has its app log lines deleted there, but a key that is a run's own
// span (a link carrying a RunID) is left out of the chunk's spans: the
// RunId IN … statement after the loop removes those last. A crash
// midway then leaves the run's own spans, so a retry's trace lookup
// still finds the non-weft spans below them.
func planSpanDeletes(links []obsdb.SpanLink, keys []obsdb.SpanKey) []spanChunk {
	own := map[obsdb.SpanKey]bool{}
	for _, l := range links {
		if l.RunID != "" {
			own[obsdb.SpanKey{TraceID: l.TraceID, SpanID: l.SpanID}] = true
		}
	}
	var out []spanChunk
	for start := 0; start < len(keys); start += deleteChunk / 2 {
		c := spanChunk{logs: keys[start:min(start+deleteChunk/2, len(keys))]}
		for _, k := range c.logs {
			if !own[k] {
				c.spans = append(c.spans, k)
			}
		}
		out = append(out, c)
	}
	return out
}

// keyTuples is keys as (TraceId, SpanId) IN args and placeholders.
func keyTuples(keys []obsdb.SpanKey) ([]any, string) {
	args := make([]any, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, k.TraceID, k.SpanID)
	}
	return args, strings.TrimSuffix(strings.Repeat("(?,?),", len(keys)), ",")
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
