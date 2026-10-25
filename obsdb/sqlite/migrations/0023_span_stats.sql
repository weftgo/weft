-- 0023 · the span_stats rollup (UX plan D7, decision 3; UX-E-25): one
-- row per stored weft span that aggregates read — the run's root
-- (invoke_agent), each model call (chat) and each tool call
-- (execute_tool) — with what obsdb.Aggregator's Stats groups and sums,
-- so GET /api/stats never parses an attribute JSON at read time.
-- ClickHouse has no such table: it aggregates otel_traces natively.
--
-- A derived index, never a record: every column is computed from the
-- spans row it is keyed by (spanStatsSelect in stats.go, the one
-- derivation both the writer and this migration's backfill run), so
-- the table is rebuildable from spans at any time. The writer inserts
-- the row in the span's own transaction (INSERT OR IGNORE: a retried
-- batch is a no-op); an attempt span raises its chat span's attempts
-- in either arrival order. Not a row: a span with no weft.run.id, a
-- summarizer's call (weft.chat.role — never the run's own, §3), a call
-- a resume denied (weft.tool.denied — zero-length, never run; the
-- root's weft.run.denied counts it), an attempt span, any other span.
--
-- Deletion follows the spans table by trigger: whatever removes a span
-- — DeleteRun, DeleteSession, Sweep and its orphan pass — removes its
-- row, so a deleted or expired run leaves no aggregate behind.
--
-- span_stats_op is decision 3's (op, name, start_ns): a by=tool or
-- by=model read over a window; span_stats_run the join from the runs a
-- query keeps. The backfill (postMigrations[23], backfillSpanStats) runs
-- the derivation over the spans already stored in keyset batches of
-- spanStatsBatchSize spans, inside this migration's transaction.
CREATE TABLE IF NOT EXISTS span_stats (
  trace_id TEXT NOT NULL, span_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  op TEXT NOT NULL,                       -- invoke_agent | chat | execute_tool
  name TEXT NOT NULL DEFAULT '',          -- the agent, the model asked for, the tool
  provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
  step INTEGER NOT NULL DEFAULT -1,
  start_ns INTEGER NOT NULL,
  dur_ms REAL NOT NULL,                   -- (end − start) / 1e6, floored at 0
  ttft_ms REAL,                           -- chat: weft.ttft_ms
  attempts INTEGER NOT NULL DEFAULT 1,    -- chat: the highest weft.attempt.index of its attempt spans, else 1
  failed INTEGER NOT NULL DEFAULT 0,      -- status Error (execute_tool: the tool_errors rule)
  error_type TEXT NOT NULL DEFAULT '',
  denied INTEGER NOT NULL DEFAULT 0,      -- problems.denied's share: weft.run.denied, or 1 for a DENIED tool call
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens INTEGER NOT NULL DEFAULT 0, cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (trace_id, span_id)
) WITHOUT ROWID;
-- spans_attempt serves the derivation's attempts subquery — a chat
-- span's attempt children, by trace and parent — which the statement
-- names (INDEXED BY): without it, or without ANALYZE to make the
-- planner pick it, every chat span scans its whole trace (a 30,000-span
-- trace ingested in 41.7 s, the backfill blocked Open for 46.8 s).
CREATE INDEX IF NOT EXISTS spans_attempt ON spans(trace_id, parent_span_id) WHERE name = 'attempt';
CREATE INDEX IF NOT EXISTS span_stats_op ON span_stats(op, name, start_ns);
CREATE INDEX IF NOT EXISTS span_stats_run ON span_stats(run_id);
CREATE TRIGGER IF NOT EXISTS span_stats_gone AFTER DELETE ON spans BEGIN
  DELETE FROM span_stats WHERE trace_id = old.trace_id AND span_id = old.span_id;
END;
