package clickhouse

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/weftgo/weft/obsdb"
)

// The aggregates (obsdb.Aggregator; UX plan D7, A4, A2; decision 3):
// native GROUP BY over otel_traces and the collapsed weft_runs rows, no
// rollup table — the columnar scan is the rollup. Every number is the
// record contract's rule restated over the collector's string-valued
// SpanAttributes map (statsSpans), the rules SQLite's span_stats
// derivation (obsdb/sqlite stats.go) computes at ingest; the
// conformance suite holds the two backends to equal answers,
// percentiles included (both nearest-rank, exact).

var _ obsdb.Aggregator = (*DB)(nil)

// statsRuns is the runs every Stats statement reads: runsInner's list
// (every RunQuery filter, the retention cut) not derived, started in
// the window, the status filter, each run with its bucket rb; for
// error_type the failed runs alone. Its columns are r-prefixed:
// ClickHouse aliases are query-global, and the list's own aliases
// (max(Agent) AS Agent, …) must not meet the spans' columns.
func statsRuns(q obsdb.StatsQuery, buckets int, width time.Duration, now time.Time, cut runsCut) (string, []any) {
	rq := q.RunQuery
	rq.Before, rq.BeforeID = time.Time{}, ""
	inner, innerArgs := runsInner(rq, cut)
	conds := []string{"toUnixTimestamp64Nano(Started) >= ?", "toUnixTimestamp64Nano(Started) < ?"}
	condArgs := []any{q.Since.UnixNano(), q.Until.UnixNano()}
	if sc, sa := statusConds(rq.Status, now, cut); len(sc) > 0 {
		conds = append(conds, sc...)
		condArgs = append(condArgs, sa...)
	}
	if q.By == obsdb.StatsByErrorType {
		conds = append(conds, "Failed = 1")
	}
	args := []any{int64(buckets - 1), q.Since.UnixNano(), int64(width)}
	args = append(args, innerArgs...)
	args = append(args, condArgs...)
	return `SELECT RunId AS rid, Agent AS ragent, ErrorType AS rerr, toInt64(Failed) AS rfailed,
		toUnixTimestamp64Nano(Started) AS rstart, Finished AS rfin,
		least(?, intDiv(toUnixTimestamp64Nano(Started) - ?, ?)) AS rb
		FROM (` + inner + `) WHERE ` + strings.Join(conds, " AND "), args
}

// statsOp is a span's operation: gen_ai.operation.name, else its
// name's prefix — SQLite's spanStatsOp.
const statsOp = `multiIf(SpanAttributes['gen_ai.operation.name'] != '', SpanAttributes['gen_ai.operation.name'],
	startsWith(SpanName, 'invoke_agent'), 'invoke_agent', startsWith(SpanName, 'chat'), 'chat',
	startsWith(SpanName, 'execute_tool'), 'execute_tool', '')`

// statsChatInt is an integer attribute of a chat span, 0 on any other.
func statsChatInt(key string) string {
	return `if(sOp = 'chat', toInt64OrZero(SpanAttributes['` + key + `']), 0)`
}

// statsSpans is SQLite's span_stats derivation over otel_traces, for
// the spans of the runs runs selects: one row per (trace, span) — a
// retried batch's duplicate read once — of each invoke_agent, chat and
// execute_tool span with a run id, never a summarizer's call
// (weft.chat.role) nor a call a resume denied (weft.tool.denied); a
// chat span's attempts its attempt children's highest
// weft.attempt.index (1 without; obsdb.AttemptIndex's rule: decimal
// digits, anything else no index). Binds: runs' twice, then the window's
// start.
func statsSpans(runs string) string {
	return `SELECT sTrace, sSpan, sRun, sOp, sName, sProvider, sModel, sStep, sDur, sTTFT,
		if(sOp = 'chat', greatest(toInt64(1), a.aMax), toInt64(1)) AS sAttempts, sFailed, sDenied,
		sIn, sOut, sCached, sCacheWrite, sReasoning
	FROM (SELECT TraceId AS sTrace, SpanId AS sSpan, RunId AS sRun, ` + statsOp + ` AS sOp,
		multiIf(sOp = 'invoke_agent', SpanAttributes['gen_ai.agent.name'], sOp = 'execute_tool', SpanAttributes['gen_ai.tool.name'],
			SpanAttributes['gen_ai.request.model']) AS sName,
		SpanAttributes['gen_ai.provider.name'] AS sProvider, SpanAttributes['gen_ai.request.model'] AS sModel,
		ifNull(toInt64OrNull(SpanAttributes['weft.step.index']), toInt64(-1)) AS sStep,
		Duration / 1e6 AS sDur,
		if(sOp = 'chat', toFloat64OrNull(SpanAttributes['weft.ttft_ms']), NULL) AS sTTFT,
		StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS sIsErr, SpanAttributes['error.type'] AS sEt,
		toInt64(sIsErr AND NOT (sOp = 'execute_tool' AND sEt IN ('DENIED', 'RETRY', 'context.Canceled', 'context.DeadlineExceeded'))) AS sFailed,
		multiIf(sOp = 'invoke_agent', greatest(toInt64OrZero(SpanAttributes['weft.run.denied']), toInt64(0)),
			sOp = 'execute_tool', toInt64(sIsErr AND sEt = 'DENIED'), toInt64(0)) AS sDenied,
		` + statsChatInt("gen_ai.usage.input_tokens") + ` AS sIn,
		` + statsChatInt("gen_ai.usage.output_tokens") + ` AS sOut,
		` + statsChatInt("gen_ai.usage.cache_read.input_tokens") + ` AS sCached,
		` + statsChatInt("gen_ai.usage.cache_creation.input_tokens") + ` AS sCacheWrite,
		` + statsChatInt("gen_ai.usage.reasoning.output_tokens") + ` AS sReasoning
		FROM otel_traces
		WHERE RunId != '' AND RunId IN (SELECT rid FROM (` + runs + `))
			AND sOp IN ('invoke_agent', 'chat', 'execute_tool')
			AND SpanAttributes['weft.chat.role'] = ''
			AND NOT (sOp = 'execute_tool' AND SpanAttributes['weft.tool.denied'] = 'true')
			AND Timestamp >= fromUnixTimestamp64Nano(?)
		LIMIT 1 BY TraceId, SpanId) AS s
	LEFT JOIN (SELECT TraceId AS aTrace, ParentSpanId AS aParent, max(if(match(SpanAttributes['weft.attempt.index'], '^[0-9]+$'), toInt64OrZero(SpanAttributes['weft.attempt.index']), toInt64(0))) AS aMax
		FROM otel_traces WHERE SpanName = 'attempt' AND RunId IN (SELECT rid FROM (` + runs + `))
		GROUP BY TraceId, ParentSpanId) AS a ON a.aTrace = s.sTrace AND a.aParent = s.sSpan`
}

// statsBelow is the delegated calls of by=agent (obsdb.StatsGroup's
// Delegated): every chat span of a Subagent descendant — a run id with
// a '/', its ancestors the ids cut at every <step>/<call> pair — joined
// to the group's runs among its ancestors, kept when the descendant's
// own agent is another, each (agent, span) once. SQLite reads the same
// set as an id range below each run. The descendant is held to the
// query's scope: its spans to the public id (pub), its own run row to
// every meta pair (RunQuery.Meta's rule over the collapsed row: a
// tag-scoped panel token never sums an untagged child, I4-fix), and
// under a scope a descendant with an out-of-scope run between it and
// the group's run is left out with that run's subtree — the tree's
// rule, the run document's hidden children (I4-fix2): the out-of-scope
// descendant rows started in the window, whose ids none of dAnc's
// intermediates (longer than the group's run) may be. Binds: the
// window's start, the public id when pub is set, runs', the meta pairs
// (key, key, value each, in key order), then under a scope the
// window's start, the public id and the meta pairs again (statsBelowArgs).
func statsBelow(runs, pub string, meta map[string]string) string {
	scope := ""
	if pub != "" {
		scope = " AND PublicId = ?"
	}
	cmeta, ccond := "", ""
	if len(meta) > 0 {
		cmeta = ", max(Meta) AS cmeta"
		for range meta {
			ccond += " AND JSONHas(c.cmeta, ?) AND JSONExtractString(c.cmeta, ?) = ?"
		}
	}
	between := ""
	if pub != "" || len(meta) > 0 {
		in := "1 = 1"
		if pub != "" {
			in += " AND max(PublicID) = ?"
		}
		for range meta {
			in += " AND JSONHas(max(Meta), ?) AND JSONExtractString(max(Meta), ?) = ?"
		}
		between = ` AND NOT hasAny(arrayFilter(a -> length(a) > length(d.dParent), d.dAnc),
		(SELECT groupArray(RunId) FROM (SELECT RunId FROM weft_runs WHERE position(RunId, '/') > 0 GROUP BY RunId
			HAVING min(Started) >= fromUnixTimestamp64Nano(?) AND NOT (` + in + `))))`
	}
	return `SELECT DISTINCT ragent AS k, dTrace, dSpan, dRun, dProvider, dModel, dIn, dOut, dCached, dCacheWrite, dReasoning
	FROM (SELECT dTrace, dSpan, dRun, dProvider, dModel, dIn, dOut, dCached, dCacheWrite, dReasoning, dParent, dAnc
		FROM (SELECT TraceId AS dTrace, SpanId AS dSpan, RunId AS dRun,
			SpanAttributes['gen_ai.provider.name'] AS dProvider, SpanAttributes['gen_ai.request.model'] AS dModel,
			toInt64OrZero(SpanAttributes['gen_ai.usage.input_tokens']) AS dIn,
			toInt64OrZero(SpanAttributes['gen_ai.usage.output_tokens']) AS dOut,
			toInt64OrZero(SpanAttributes['gen_ai.usage.cache_read.input_tokens']) AS dCached,
			toInt64OrZero(SpanAttributes['gen_ai.usage.cache_creation.input_tokens']) AS dCacheWrite,
			toInt64OrZero(SpanAttributes['gen_ai.usage.reasoning.output_tokens']) AS dReasoning,
			arrayMap(i -> arrayStringConcat(arraySlice(splitByChar('/', dRun), 1, i), '/'),
				range(1, length(splitByChar('/', dRun)), 2)) AS dAnc
		FROM otel_traces
		WHERE position(RunId, '/') > 0 AND (` + statsOp + `) = 'chat' AND SpanAttributes['weft.chat.role'] = ''
			AND Timestamp >= fromUnixTimestamp64Nano(?)` + scope + `
		LIMIT 1 BY TraceId, SpanId) ARRAY JOIN dAnc AS dParent) AS d
	INNER JOIN (` + runs + `) AS r ON d.dParent = r.rid
	INNER JOIN (SELECT RunId AS cid, max(Agent) AS cagent` + cmeta + ` FROM weft_runs WHERE position(RunId, '/') > 0 GROUP BY RunId) AS c
		ON c.cid = d.dRun
	WHERE c.cagent != r.ragent` + ccond + between
}

// statsBelowArgs is statsBelow's binds after runs': the meta pairs,
// then under a scope its intermediates' window start, public id and
// meta pairs.
func statsBelowArgs(since int64, pub string, meta map[string]string) []any {
	var out []any
	pairs := func() {
		for _, k := range slices.Sorted(maps.Keys(meta)) {
			out = append(out, k, k, meta[k])
		}
	}
	pairs()
	if pub != "" || len(meta) > 0 {
		out = append(out, since)
		if pub != "" {
			out = append(out, pub)
		}
		pairs()
	}
	return out
}

// statsKeyCH is the group key's column and the span op a span-level
// query reads ("" for the run-level ones).
func statsKeyCH(by obsdb.StatsBy) (key, op string) {
	switch by {
	case obsdb.StatsByAgent:
		return "ragent", ""
	case obsdb.StatsByErrorType:
		return "rerr", ""
	case obsdb.StatsByModel:
		return "sModel", "chat"
	case obsdb.StatsByTool:
		return "sName", "execute_tool"
	default: // step
		return "toString(sStep)", "chat"
	}
}

// statsPercentiles wraps a (k, v) value query in obsdb.PercentileRank's
// nearest-rank percentiles per k: k, n, p50, p95 — the sorted values'
// elements at ⌈p·n/100⌉, SQLite's ROW_NUMBER rank exactly.
func statsPercentiles(values string) string {
	return `SELECT k, toInt64(count()),
		toFloat64(arrayElement(arraySort(groupArray(v)), intDiv(count() * 50 + 99, 100))),
		toFloat64(arrayElement(arraySort(groupArray(v)), intDiv(count() * 95 + 99, 100)))
	FROM (` + values + `) GROUP BY k`
}

// Stats answers obsdb.Aggregator.
func (d *DB) Stats(ctx context.Context, q obsdb.StatsQuery) (_ obsdb.StatsPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.StatsPage{}, err
	}
	defer d.closedErr(&err)
	buckets, width, err := obsdb.CheckStats(q)
	if err != nil {
		return obsdb.StatsPage{}, err
	}
	cut := d.runsCut(ctx) // one instant for every statement below
	runs, rargs := statsRuns(q, buckets, width, time.Now(), cut)
	spans := statsSpans(runs)
	sargs := append(append(append([]any{}, rargs...), q.Since.UnixNano()), rargs...)
	joined := `SELECT * FROM (` + spans + `) AS s INNER JOIN (` + runs + `) AS r ON s.sRun = r.rid`
	jargs := append(append([]any{}, sargs...), rargs...)
	key, op := statsKeyCH(q.By)

	var cells []obsdb.StatsCell
	var models []obsdb.StatsModelPart
	pcts := map[string]*obsdb.StatsPercentiles{}
	pct := func(k string) *obsdb.StatsPercentiles {
		if pcts[k] == nil {
			pcts[k] = &obsdb.StatsPercentiles{Key: k}
		}
		return pcts[k]
	}
	query := func(stmt string, args []any, scan func(driver.Rows) error) error {
		rows, err := d.conn.Query(ctx, stmt, args...)
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
	cell := func(rows driver.Rows, c *obsdb.StatsCell, rest ...any) error {
		var b int64
		if err := rows.Scan(append([]any{&c.Key, &b}, rest...)...); err != nil {
			return err
		}
		c.Bucket = int(b)
		return nil
	}
	tokens := `toInt64(sum(sIn)), toInt64(sum(sOut)), toInt64(sum(sCached)), toInt64(sum(sCacheWrite)), toInt64(sum(sReasoning))`
	if op == "" {
		if err := query(`SELECT `+key+`, rb, toInt64(count()), toInt64(sum(rfailed)) FROM (`+runs+`) GROUP BY `+key+`, rb`, rargs,
			func(rows driver.Rows) error {
				var c obsdb.StatsCell
				if err := cell(rows, &c, &c.Runs, &c.Failed); err != nil {
					return err
				}
				cells = append(cells, c)
				return nil
			}); err != nil {
			return obsdb.StatsPage{}, err
		}
		if err := query(`SELECT `+key+`, rb, toInt64(countIf(sOp = 'chat')), toInt64(countIf(sOp = 'chat' AND sFailed = 1)),
			toInt64(countIf(sOp = 'chat' AND sAttempts > 1)), toInt64(sum(sDenied)), `+tokens+`
			FROM (`+joined+`) GROUP BY `+key+`, rb`, jargs, func(rows driver.Rows) error {
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
		if err := query(`SELECT `+key+`, rb, toInt64(uniqExact(sRun)), toInt64(uniqExactIf(sRun, rfailed = 1)),
			toInt64(count()), toInt64(sum(sFailed)), toInt64(countIf(sAttempts > 1)), toInt64(sum(sDenied)), `+tokens+`
			FROM (`+joined+`) WHERE sOp = ? GROUP BY `+key+`, rb`, append(append([]any{}, jargs...), op),
			func(rows driver.Rows) error {
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
	if q.By != obsdb.StatsByTool {
		if err := query(`SELECT `+key+`, sProvider, sModel, `+tokens+` FROM (`+joined+`) WHERE sOp = 'chat'
			GROUP BY `+key+`, sProvider, sModel`, jargs, func(rows driver.Rows) error {
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
	if q.By == obsdb.StatsByAgent {
		below, bargs := statsBelow(runs, q.PublicID, q.Meta), []any{q.Since.UnixNano()}
		if q.PublicID != "" {
			bargs = append(bargs, q.PublicID)
		}
		bargs = append(bargs, rargs...)
		bargs = append(bargs, statsBelowArgs(q.Since.UnixNano(), q.PublicID, q.Meta)...)
		if err := query(`SELECT k, dProvider, dModel, toInt64(sum(dIn)), toInt64(sum(dOut)), toInt64(sum(dCached)),
			toInt64(sum(dCacheWrite)), toInt64(sum(dReasoning)) FROM (`+below+`) GROUP BY k, dProvider, dModel`, bargs,
			func(rows driver.Rows) error {
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
		if err := query(`SELECT k, toInt64(uniqExact(dRun)) FROM (`+below+`) GROUP BY k`, bargs, func(rows driver.Rows) error {
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
	scanPct := func(set func(*obsdb.StatsPercentiles, obsdb.Percentiles)) func(driver.Rows) error {
		return func(rows driver.Rows) error {
			var k string
			var p obsdb.Percentiles
			if err := rows.Scan(&k, &p.N, &p.P50, &p.P95); err != nil {
				return err
			}
			set(pct(k), p)
			return nil
		}
	}
	durations, dargs := `SELECT `+key+` AS k, (toUnixTimestamp64Nano(assumeNotNull(rfin)) - rstart) / 1e6 AS v
		FROM (`+runs+`) WHERE rfin IS NOT NULL`, rargs
	if op != "" {
		durations = `SELECT ` + key + ` AS k, sDur AS v FROM (` + joined + `) WHERE sOp = ?`
		dargs = append(append([]any{}, jargs...), op)
	}
	if err := query(statsPercentiles(durations), dargs, scanPct(func(s *obsdb.StatsPercentiles, p obsdb.Percentiles) { s.Duration = p })); err != nil {
		return obsdb.StatsPage{}, err
	}
	if q.By != obsdb.StatsByTool {
		ttft := `SELECT ` + key + ` AS k, assumeNotNull(sTTFT) AS v FROM (` + joined + `) WHERE sOp = 'chat' AND sTTFT IS NOT NULL`
		if err := query(statsPercentiles(ttft), jargs, scanPct(func(s *obsdb.StatsPercentiles, p obsdb.Percentiles) { s.TTFT = p })); err != nil {
			return obsdb.StatsPage{}, err
		}
	}
	list := make([]obsdb.StatsPercentiles, 0, len(pcts))
	for _, p := range pcts {
		list = append(list, *p)
	}
	return obsdb.BuildStats(q, cells, models, list), nil
}

// ErrorCounts answers obsdb.Aggregator over Search's match.
func (d *DB) ErrorCounts(ctx context.Context, q obsdb.SearchQuery) (_ []obsdb.ErrorCount, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, false, err
	}
	defer d.closedErr(&err)
	from, args, where, condArgs, err := searchFrom(q, time.Now(), d.runsCut(ctx))
	if err != nil {
		return nil, false, err
	}
	all := append(append(append([]any{}, args...), condArgs...), obsdb.MaxErrorTexts+1)
	rows, err := d.conn.Query(ctx, `SELECT Err, toInt64(count()) AS n FROM `+from+` WHERE `+where+` AND Err != ''
		GROUP BY Err ORDER BY n DESC, Err LIMIT ?`, all...)
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
