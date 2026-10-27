package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The aggregates (obsdb.Aggregator; UX plan D7, A4, A2) over the
// span_stats rollup (migration 0023): Stats joins the runs a query
// keeps — runWhere, the /api/runs filters, the window on started_ns —
// to their rollup rows and lets SQLite group them; ErrorCounts groups
// the search match's error texts. The record contract's numbers are
// computed in SQL once, at ingest (spanStatsInsert), never parsed out
// of an attribute JSON at read.

var _ obsdb.Aggregator = (*DB)(nil)

// spanStatsOp is a span's operation in SQL: its gen_ai.operation.name
// when set, else its name's prefix (a weft span always sets the
// attribute; the prefix is ClickHouse's fallback too).
const spanStatsOp = `COALESCE(NULLIF(json_extract(attrs, '$."gen_ai.operation.name"'), ''),
	CASE WHEN substr(name, 1, 12) = 'invoke_agent' THEN 'invoke_agent'
	WHEN substr(name, 1, 4) = 'chat' THEN 'chat'
	WHEN substr(name, 1, 12) = 'execute_tool' THEN 'execute_tool' ELSE '' END)`

// chatInt is an integer attribute of a chat span, 0 on any other.
func chatInt(key string) string {
	return `CASE WHEN op = 'chat' THEN COALESCE(CAST(json_extract(attrs, '$."` + key + `"') AS INTEGER), 0) ELSE 0 END`
}

// attemptIndexSQL is an attempt span's weft.attempt.index the one way
// every reader reads it (obsdb.AttemptIndex): a JSON integer, or a
// string of decimal digits — the spelling a collector's stringified
// attributes carry, which ClickHouse reads — else NULL (no index). The
// subquery reads it through spans_attempt (0023), named: without
// ANALYZE the planner scans the whole trace for every chat span.
const attemptIndexSQL = `CASE json_type(a.attrs, '$."weft.attempt.index"')
	WHEN 'integer' THEN json_extract(a.attrs, '$."weft.attempt.index"')
	WHEN 'text' THEN CASE WHEN json_extract(a.attrs, '$."weft.attempt.index"') GLOB '[0-9]*'
		AND json_extract(a.attrs, '$."weft.attempt.index"') NOT GLOB '*[^0-9]*'
		THEN CAST(json_extract(a.attrs, '$."weft.attempt.index"') AS INTEGER) END END`

// spanStatsInsert derives span_stats rows from the spans the filter
// keeps (a condition over spans' own columns, binds after it): the one
// derivation the writer (one span) and migration 0023's backfill (a
// batch) run. The rules are migration 0023's comment; ClickHouse's
// statsSpans restates them over otel_traces, and the conformance suite
// pins both agree.
func spanStatsInsert(filter string) string {
	return `INSERT OR IGNORE INTO span_stats (trace_id, span_id, run_id, op, name, provider, model, step,
		start_ns, dur_ms, ttft_ms, attempts, failed, error_type, denied,
		input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens)
	SELECT trace_id, span_id, run_id, op,
		COALESCE(CASE op WHEN 'invoke_agent' THEN json_extract(attrs, '$."gen_ai.agent.name"')
			WHEN 'execute_tool' THEN json_extract(attrs, '$."gen_ai.tool.name"')
			ELSE json_extract(attrs, '$."gen_ai.request.model"') END, ''),
		COALESCE(json_extract(attrs, '$."gen_ai.provider.name"'), ''),
		COALESCE(json_extract(attrs, '$."gen_ai.request.model"'), ''),
		step, start_ns, MAX(end_ns - start_ns, 0) / 1e6,
		CASE WHEN op = 'chat' THEN CAST(json_extract(attrs, '$."weft.ttft_ms"') AS REAL) END,
		CASE WHEN op = 'chat' THEN MAX(1, COALESCE((SELECT MAX(` + attemptIndexSQL + `)
			FROM spans AS a INDEXED BY spans_attempt
			WHERE a.trace_id = x.trace_id AND a.parent_span_id = x.span_id AND a.name = 'attempt'), 1)) ELSE 1 END,
		CASE WHEN status_code = 2 AND NOT (op = 'execute_tool' AND et IN ('DENIED', 'RETRY', 'context.Canceled', 'context.DeadlineExceeded'))
			THEN 1 ELSE 0 END,
		et,
		CASE op WHEN 'invoke_agent' THEN MAX(COALESCE(CAST(json_extract(attrs, '$."weft.run.denied"') AS INTEGER), 0), 0)
			WHEN 'execute_tool' THEN (status_code = 2 AND et = 'DENIED') ELSE 0 END,
		` + chatInt("gen_ai.usage.input_tokens") + `,
		` + chatInt("gen_ai.usage.output_tokens") + `,
		` + chatInt("gen_ai.usage.cache_read.input_tokens") + `,
		` + chatInt("gen_ai.usage.cache_creation.input_tokens") + `,
		` + chatInt("gen_ai.usage.reasoning.output_tokens") + `
	FROM (SELECT trace_id, span_id, run_id, name, step, start_ns, end_ns, status_code, attrs,
		` + spanStatsOp + ` AS op, COALESCE(json_extract(attrs, '$."error.type"'), '') AS et
		FROM spans WHERE ` + filter + `) AS x
	WHERE run_id <> '' AND op IN ('invoke_agent', 'chat', 'execute_tool')
		AND COALESCE(json_extract(attrs, '$."weft.chat.role"'), '') = ''
		AND NOT (op = 'execute_tool' AND COALESCE(json_extract(attrs, '$."weft.tool.denied"'), 0) IN (1, 'true'))`
}

// spanStatWriter is the writer's half, one per Write: the rollup row
// of each span just stored (a no-op for a span the rollup skips) and —
// for an attempt span — its chat span's attempts raised to the
// attempt's index, when the chat span's row is already there (stored
// first, its subquery could not see this attempt). Both statements are
// prepared once per Write, on first use: the INSERT … SELECT is long,
// and parsing it per span was most of a span's cost
// (BenchmarkSpanStatsIngest pins it).
type spanStatWriter struct {
	tx              *sql.Tx
	insert, attempt *sql.Stmt
}

// spanStatsWarn is the once-per-process WARN of a span_stats write
// that failed (writeSaved); a var so a test can reset it.
var spanStatsWarn sync.Once

// writeSaved is write inside a SAVEPOINT: on its failure the rollup's
// half is rolled back and the span — stored before it in the same
// transaction — is kept. The rollup is a derived index (rebuildable
// from spans), so a failure there must not lose the OTLP batch; it
// logs one WARN per process through slog's default logger. Only a
// failure of the savepoint itself is returned.
func (w *spanStatWriter) writeSaved(ctx context.Context, s obsdb.Span) error {
	if _, err := w.tx.ExecContext(ctx, `SAVEPOINT span_stats`); err != nil {
		return err
	}
	if err := w.write(ctx, s); err != nil {
		if _, rerr := w.tx.ExecContext(ctx, `ROLLBACK TO span_stats`); rerr != nil {
			return rerr
		}
		spanStatsWarn.Do(func() {
			slog.Default().WarnContext(ctx, "weft/obsdb/sqlite: span_stats rollup write failed; the span is stored, the stats skip it (further failures are not logged)",
				"trace_id", s.TraceID, "span_id", s.SpanID, "error", err)
		})
	}
	_, err := w.tx.ExecContext(ctx, `RELEASE span_stats`)
	return err
}

func (w *spanStatWriter) write(ctx context.Context, s obsdb.Span) error {
	if w.insert == nil {
		st, err := w.tx.PrepareContext(ctx, spanStatsInsert(`trace_id = ? AND span_id = ?`))
		if err != nil {
			return err
		}
		w.insert = st
	}
	if _, err := w.insert.ExecContext(ctx, s.TraceID, s.SpanID); err != nil {
		return err
	}
	if s.Name != "attempt" || s.ParentSpanID == "" {
		return nil
	}
	if op, _ := s.Attrs["gen_ai.operation.name"].(string); op != "" {
		return nil
	}
	idx, ok := obsdb.AttemptIndex(s.Attrs)
	if !ok || idx <= 1 {
		return nil
	}
	if w.attempt == nil {
		st, err := w.tx.PrepareContext(ctx, `UPDATE span_stats SET attempts = MAX(attempts, ?)
			WHERE trace_id = ? AND span_id = ? AND op = 'chat'`)
		if err != nil {
			return err
		}
		w.attempt = st
	}
	_, err := w.attempt.ExecContext(ctx, idx, s.TraceID, s.ParentSpanID)
	return err
}

// close releases the prepared statements (before the commit).
func (w *spanStatWriter) close() {
	for _, st := range []*sql.Stmt{w.insert, w.attempt} {
		if st != nil {
			_ = st.Close()
		}
	}
}

// spanStatsBatchSize is the backfill's keyset batch: spans per
// statement. A var only so the backfill test can lower it.
var spanStatsBatchSize = 5000

// backfillSpanStats is migration 0023's Go step: the rollup derived for
// every span already stored, spanStatsBatchSize spans at a time in primary
// key order — each statement bounded, the whole inside the migration's
// transaction (a large file's first open at 0023 takes as long as its
// spans take to derive: TestSpanStatsBackfillTime measures it).
func backfillSpanStats(tx *sql.Tx) (err error) {
	ctx := context.Background()
	// Open blocks for the backfill's length: one INFO line before (the
	// span count) and one after (its duration), never a silent wait. A
	// file with no spans (a new one) has nothing to wait for and logs
	// nothing.
	var spans int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM spans`).Scan(&spans); err != nil {
		return err
	}
	if spans == 0 {
		return nil
	}
	slog.Default().InfoContext(ctx, "weft/obsdb/sqlite: migration 0023 backfills the span_stats rollup; Open waits for it", "spans", spans)
	began := time.Now()
	defer func() {
		if err != nil {
			return
		}
		slog.Default().InfoContext(ctx, "weft/obsdb/sqlite: migration 0023 backfill done", "spans", spans, "duration", time.Since(began).Round(time.Millisecond).String())
	}()
	// The lower bound is a separate condition, never "first OR (…) >
	// (…)": an OR with a bind defeats the primary key range, and every
	// batch would scan the table from its start.
	lower, largs := "1 = 1", []any(nil)
	for {
		var hiTrace, hiSpan string
		err := tx.QueryRowContext(ctx, `SELECT trace_id, span_id FROM spans WHERE `+lower+`
			ORDER BY trace_id, span_id LIMIT 1 OFFSET ?`, append(largs[:len(largs):len(largs)], spanStatsBatchSize-1)...).Scan(&hiTrace, &hiSpan)
		last := errors.Is(err, sql.ErrNoRows)
		if err != nil && !last {
			return err
		}
		cond, args := lower, largs[:len(largs):len(largs)]
		if !last {
			cond += ` AND (trace_id, span_id) <= (?, ?)`
			args = append(args, hiTrace, hiSpan)
		}
		if _, err := tx.ExecContext(ctx, spanStatsInsert(cond), args...); err != nil {
			return err
		}
		if last {
			return nil
		}
		lower, largs = `(trace_id, span_id) > (?, ?)`, []any{hiTrace, hiSpan}
	}
}

// statsRuns is the runs CTE every Stats statement starts from: the
// runs q keeps (runWhere), started in the window, each
// with its bucket b; error_type keeps the failed runs alone.
func statsRuns(q obsdb.StatsQuery, buckets int, width time.Duration, now time.Time) (string, []any) {
	rq := q.RunQuery
	rq.Before, rq.BeforeID = time.Time{}, ""
	where, wargs := runWhere(rq, now)
	extra := ""
	if q.By == obsdb.StatsByErrorType {
		extra = " AND failed = 1"
	}
	args := []any{buckets - 1, q.Since.UnixNano(), int64(width)}
	args = append(args, wargs...)
	args = append(args, q.Since.UnixNano(), q.Until.UnixNano())
	return `WITH r AS (SELECT run_id, agent, error_type, failed, started_ns, finished_ns,
		MIN(?, (started_ns - ?) / ?) AS b FROM runs
		WHERE ` + where + ` AND started_ns >= ? AND started_ns < ?` + extra + `) `, args
}

// statsKey is the group key's SQL and the span op a span-level query
// reads ("" for the run-level ones).
func statsKey(by obsdb.StatsBy) (key, op string) {
	switch by {
	case obsdb.StatsByAgent:
		return "r.agent", ""
	case obsdb.StatsByErrorType:
		return "r.error_type", ""
	case obsdb.StatsByModel:
		return "s.model", "chat"
	case obsdb.StatsByTool:
		return "s.name", "execute_tool"
	default: // step
		return "CAST(s.step AS TEXT)", "chat"
	}
}

// statsBelowSQL is by=agent's delegated calls over the r CTE (Stats'
// comment above its use); scope is the panel token's condition, if any.
func statsBelowSQL(scope string) string {
	return `SELECT DISTINCT r.agent AS k, s.trace_id, s.span_id, s.run_id, s.provider, s.model,
			s.input_tokens, s.output_tokens, s.cached_input_tokens, s.cache_write_tokens, s.reasoning_tokens
			FROM r CROSS JOIN span_stats AS s INDEXED BY span_stats_run ON s.run_id > r.run_id || '/' AND s.run_id < r.run_id || '0'
			JOIN runs AS c ON c.run_id = s.run_id
			WHERE s.op = 'chat' AND s.start_ns >= ? AND c.agent <> r.agent` + scope
}

// percentileSQL wraps a (k, v) value query in obsdb.PercentileRank's
// nearest-rank percentiles per k: k, n, p50, p95.
func percentileSQL(values string) string {
	return `SELECT k, n, MAX(CASE WHEN rn = (n * 50 + 99) / 100 THEN v END), MAX(CASE WHEN rn = (n * 95 + 99) / 100 THEN v END)
	FROM (SELECT k, v, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v) AS rn, COUNT(*) OVER (PARTITION BY k) AS n
		FROM (` + values + `)) GROUP BY k, n`
}

// Stats answers obsdb.Aggregator over span_stats.
func (d *DB) Stats(ctx context.Context, q obsdb.StatsQuery) (_ obsdb.StatsPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.StatsPage{}, err
	}
	defer d.closedErr(&err)
	buckets, width, err := obsdb.CheckStats(q)
	if err != nil {
		return obsdb.StatsPage{}, err
	}
	with, wargs := statsRuns(q, buckets, width, time.Now())
	key, op := statsKey(q.By)
	since := q.Since.UnixNano()
	var cells []obsdb.StatsCell
	var models []obsdb.StatsModelPart
	pcts := map[string]*obsdb.StatsPercentiles{}
	pct := func(k string) *obsdb.StatsPercentiles {
		if pcts[k] == nil {
			pcts[k] = &obsdb.StatsPercentiles{Key: k}
		}
		return pcts[k]
	}
	query := func(stmt string, args []any, scan func(*sql.Rows) error) error {
		rows, err := d.reads.QueryContext(ctx, with+stmt, append(append([]any{}, wargs...), args...)...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	cell := func(rows *sql.Rows, c *obsdb.StatsCell, rest ...any) error {
		return rows.Scan(append([]any{&c.Key, &c.Bucket}, rest...)...)
	}
	if op == "" {
		// The runs side: the runs and the failed among them.
		if err := query(`SELECT `+key+`, r.b, COUNT(*), SUM(r.failed) FROM r GROUP BY 1, 2`, nil, func(rows *sql.Rows) error {
			var c obsdb.StatsCell
			if err := cell(rows, &c, &c.Runs, &c.Failed); err != nil {
				return err
			}
			cells = append(cells, c)
			return nil
		}); err != nil {
			return obsdb.StatsPage{}, err
		}
		// The spans side: the runs' model calls, retries, denials, usage.
		if err := query(`SELECT `+key+`, r.b, SUM(s.op = 'chat'), SUM(s.op = 'chat' AND s.failed = 1),
			SUM(s.op = 'chat' AND s.attempts > 1), SUM(s.denied),
			SUM(s.input_tokens), SUM(s.output_tokens), SUM(s.cached_input_tokens), SUM(s.cache_write_tokens), SUM(s.reasoning_tokens)
			FROM r JOIN span_stats AS s ON s.run_id = r.run_id WHERE s.start_ns >= ? GROUP BY 1, 2`, []any{since}, func(rows *sql.Rows) error {
			var c obsdb.StatsCell
			if err := cell(rows, &c, &c.Calls, &c.FailedCalls, &c.Retried, &c.Denials,
				&c.InputTokens, &c.OutputTokens, &c.CachedInputTokens, &c.CacheWriteTokens, &c.ReasoningTokens); err != nil {
				return err
			}
			cells = append(cells, c)
			return nil
		}); err != nil {
			return obsdb.StatsPage{}, err
		}
	} else {
		if err := query(`SELECT `+key+`, r.b, COUNT(DISTINCT s.run_id), COUNT(DISTINCT CASE WHEN r.failed = 1 THEN s.run_id END),
			COUNT(*), SUM(s.failed), SUM(s.attempts > 1), SUM(s.denied),
			SUM(s.input_tokens), SUM(s.output_tokens), SUM(s.cached_input_tokens), SUM(s.cache_write_tokens), SUM(s.reasoning_tokens)
			FROM r JOIN span_stats AS s ON s.run_id = r.run_id WHERE s.op = ? AND s.start_ns >= ? GROUP BY 1, 2`,
			[]any{op, since}, func(rows *sql.Rows) error {
				var c obsdb.StatsCell
				if err := cell(rows, &c, &c.Runs, &c.Failed, &c.Calls, &c.FailedCalls, &c.Retried, &c.Denials,
					&c.InputTokens, &c.OutputTokens, &c.CachedInputTokens, &c.CacheWriteTokens, &c.ReasoningTokens); err != nil {
					return err
				}
				cells = append(cells, c)
				return nil
			}); err != nil {
			return obsdb.StatsPage{}, err
		}
	}
	// The usage by model, for pricing.
	if q.By != obsdb.StatsByTool {
		if err := query(`SELECT `+key+`, s.provider, s.model, SUM(s.input_tokens), SUM(s.output_tokens),
			SUM(s.cached_input_tokens), SUM(s.cache_write_tokens), SUM(s.reasoning_tokens)
			FROM r JOIN span_stats AS s ON s.run_id = r.run_id WHERE s.op = 'chat' AND s.start_ns >= ? GROUP BY 1, 2, 3`, []any{since}, func(rows *sql.Rows) error {
			var m obsdb.StatsModelPart
			u := &m.Usage
			if err := rows.Scan(&m.Key, &m.Provider, &m.Model, &u.InputTokens, &u.OutputTokens,
				&u.CachedInputTokens, &u.CacheWriteTokens, &u.ReasoningTokens); err != nil {
				return err
			}
			models = append(models, m)
			return nil
		}); err != nil {
			return obsdb.StatsPage{}, err
		}
	}
	// Delegated (by=agent): the model calls below the group's runs in
	// their Subagent trees — ids <run>/<step>/<call>, at any depth: the
	// range (run || '/', run || '0') — whose run is another agent's,
	// each call once per agent (DISTINCT). CROSS JOIN keeps r driving
	// and INDEXED BY span_stats_run makes each run's range one index
	// seek: left to itself the planner drove from span_stats_op and
	// re-scanned r per chat span (15.5 s at 4,000 runs;
	// TestStatsDelegatedScale guards it).
	if q.By == obsdb.StatsByAgent {
		// The descendant c is held to the query's scope as a listed run
		// is: its public id, and every meta pair on its own row
		// (RunQuery.Meta's json_each rule) — a tag-scoped panel token
		// never sums an untagged child (I4-fix).
		scope, sargs := "", []any{since}
		if q.PublicID != "" {
			scope, sargs = " AND c.public_id = ?", append(sargs, q.PublicID)
		}
		for _, k := range slices.Sorted(maps.Keys(q.Meta)) {
			scope += " AND EXISTS (SELECT 1 FROM json_each(c.meta) AS cj WHERE cj.key = ? AND cj.value = ?)"
			sargs = append(sargs, k, q.Meta[k])
		}
		if q.PublicID != "" || len(q.Meta) > 0 {
			// The tree's rule (obsdb's scopeTree, the run document's
			// hidden children, I4-fix2): a descendant under an
			// out-of-scope run between it and the group's run is hidden
			// with that run's whole subtree, so it is no part of the
			// delegated bill either. a is such an intermediate: in r's
			// id range, s.run_id in a's.
			in := "1 = 1"
			var iargs []any
			if q.PublicID != "" {
				in += " AND a.public_id = ?"
				iargs = append(iargs, q.PublicID)
			}
			for _, k := range slices.Sorted(maps.Keys(q.Meta)) {
				in += " AND EXISTS (SELECT 1 FROM json_each(a.meta) AS aj WHERE aj.key = ? AND aj.value = ?)"
				iargs = append(iargs, k, q.Meta[k])
			}
			scope += ` AND NOT EXISTS (SELECT 1 FROM runs AS a WHERE a.run_id > r.run_id || '/' AND a.run_id < r.run_id || '0'
				AND s.run_id > a.run_id || '/' AND s.run_id < a.run_id || '0' AND NOT (` + in + `))`
			sargs = append(sargs, iargs...)
		}
		below := statsBelowSQL(scope)
		if err := query(`SELECT k, provider, model, SUM(input_tokens), SUM(output_tokens),
			SUM(cached_input_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens)
			FROM (`+below+`) GROUP BY 1, 2, 3`, sargs, func(rows *sql.Rows) error {
			m := obsdb.StatsModelPart{Delegated: true}
			u := &m.Usage
			if err := rows.Scan(&m.Key, &m.Provider, &m.Model, &u.InputTokens, &u.OutputTokens,
				&u.CachedInputTokens, &u.CacheWriteTokens, &u.ReasoningTokens); err != nil {
				return err
			}
			models = append(models, m)
			return nil
		}); err != nil {
			return obsdb.StatsPage{}, err
		}
		if err := query(`SELECT k, COUNT(DISTINCT run_id) FROM (`+below+`) GROUP BY 1`, sargs, func(rows *sql.Rows) error {
			var c obsdb.StatsCell
			if err := rows.Scan(&c.Key, &c.DelegatedRuns); err != nil {
				return err
			}
			cells = append(cells, c)
			return nil
		}); err != nil {
			return obsdb.StatsPage{}, err
		}
	}
	// Durations: the runs' for a run-level group, the calls' otherwise.
	durations, dargs := `SELECT `+key+` AS k, (r.finished_ns - r.started_ns) / 1e6 AS v FROM r WHERE r.finished_ns IS NOT NULL`, []any(nil)
	if op != "" {
		durations = `SELECT ` + key + ` AS k, s.dur_ms AS v FROM r JOIN span_stats AS s ON s.run_id = r.run_id WHERE s.op = ? AND s.start_ns >= ?`
		dargs = []any{op, since}
	}
	scanPct := func(set func(*obsdb.StatsPercentiles, obsdb.Percentiles)) func(*sql.Rows) error {
		return func(rows *sql.Rows) error {
			var k string
			var p obsdb.Percentiles
			if err := rows.Scan(&k, &p.N, &p.P50, &p.P95); err != nil {
				return err
			}
			set(pct(k), p)
			return nil
		}
	}
	if err := query(percentileSQL(durations), dargs, scanPct(func(s *obsdb.StatsPercentiles, p obsdb.Percentiles) { s.Duration = p })); err != nil {
		return obsdb.StatsPage{}, err
	}
	if q.By != obsdb.StatsByTool {
		ttft := `SELECT ` + key + ` AS k, s.ttft_ms AS v FROM r JOIN span_stats AS s ON s.run_id = r.run_id
			WHERE s.op = 'chat' AND s.ttft_ms IS NOT NULL AND s.start_ns >= ?`
		if err := query(percentileSQL(ttft), []any{since}, scanPct(func(s *obsdb.StatsPercentiles, p obsdb.Percentiles) { s.TTFT = p })); err != nil {
			return obsdb.StatsPage{}, err
		}
	}
	list := make([]obsdb.StatsPercentiles, 0, len(pcts))
	for _, p := range pcts {
		list = append(list, *p)
	}
	return obsdb.BuildStats(q, cells, models, list), nil
}

// ErrorCounts answers obsdb.Aggregator over the search match.
func (d *DB) ErrorCounts(ctx context.Context, q obsdb.SearchQuery) (_ []obsdb.ErrorCount, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, false, err
	}
	defer d.closedErr(&err)
	full, args, err := searchWhere(q, time.Now())
	if err != nil {
		return nil, false, err
	}
	rows, err := d.reads.QueryContext(ctx, `SELECT err, COUNT(*) FROM runs WHERE `+full+` AND err <> ''
		GROUP BY err ORDER BY COUNT(*) DESC, err LIMIT ?`, append(args, obsdb.MaxErrorTexts+1)...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.ErrorCount
	for rows.Next() {
		var c obsdb.ErrorCount
		if err := rows.Scan(&c.Err, &c.Count); err != nil {
			return nil, false, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	capped := len(out) > obsdb.MaxErrorTexts
	if capped {
		out = out[:obsdb.MaxErrorTexts]
	}
	return out, capped, nil
}

// errsCond is RunQuery.Errs' condition: err IN the set, or nothing for
// an empty one.
func errsCond(errs []string) (string, []any) {
	if len(errs) == 0 {
		return "0 = 1", nil
	}
	args := make([]any, len(errs))
	for i, e := range errs {
		args[i] = e
	}
	return "err IN (" + strings.TrimSuffix(strings.Repeat("?,", len(errs)), ",") + ")", args
}
