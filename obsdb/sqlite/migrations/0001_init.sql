-- 0001 — the initial obsdb schema (ADR 0024, S3.4). One logical schema
-- for two dialects: this one and obsdb/clickhouse's. The dedup key is
-- the transport-level idempotency (I4): (run, kind, pos) for records,
-- (trace, span) for spans — a retried batch is a no-op. Deltas and
-- heartbeats are never inserted here (their rules live in Write);
-- KeepDeltas turns delta rows on for debugging, keyed by their own
-- counter so they can never collide with the durable sequence.
-- (obsdb_migrations itself is created by the runner before any
-- migration body executes; its shape is
--   CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)
-- as in S3.4.)

CREATE TABLE spans (
  trace_id TEXT NOT NULL, span_id TEXT NOT NULL, parent_span_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL, kind INTEGER NOT NULL,
  start_ns INTEGER NOT NULL, end_ns INTEGER NOT NULL,
  status_code INTEGER NOT NULL, status_message TEXT NOT NULL DEFAULT '',
  service TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL DEFAULT '', step INTEGER NOT NULL DEFAULT -1, tool_seq INTEGER NOT NULL DEFAULT -1,
  session_id TEXT NOT NULL DEFAULT '',
  attrs TEXT NOT NULL,      -- JSON object
  resource TEXT NOT NULL,   -- JSON object
  events TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (trace_id, span_id)
) WITHOUT ROWID;
CREATE INDEX spans_run ON spans(run_id, start_ns) WHERE run_id <> '';

CREATE TABLE records (
  run_id TEXT NOT NULL, kind TEXT NOT NULL, pos INTEGER NOT NULL,
  time_ns INTEGER NOT NULL,
  trace_id TEXT NOT NULL DEFAULT '', span_id TEXT NOT NULL DEFAULT '',
  event_type TEXT NOT NULL DEFAULT '', step INTEGER NOT NULL DEFAULT -1,
  body TEXT NOT NULL,
  attrs TEXT NOT NULL,
  PRIMARY KEY (run_id, kind, pos)          -- the dedup key: INSERT OR IGNORE
) WITHOUT ROWID;

CREATE TABLE other_logs (                  -- non-weft records (slog lines, other services)
  id INTEGER PRIMARY KEY, time_ns INTEGER NOT NULL, trace_id TEXT, span_id TEXT,
  severity INTEGER, event_name TEXT, body TEXT, service TEXT, attrs TEXT, resource TEXT
);
CREATE INDEX other_logs_trace ON other_logs(trace_id);

CREATE TABLE runs (
  run_id TEXT PRIMARY KEY,
  parent_run_id TEXT NOT NULL DEFAULT '', parent_call_id TEXT NOT NULL DEFAULT '',
  trace_id TEXT NOT NULL DEFAULT '',
  agent TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
  manifest_hash TEXT NOT NULL DEFAULT '', weft_version TEXT NOT NULL DEFAULT '', service TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '', public_id TEXT NOT NULL DEFAULT '', turn INTEGER NOT NULL DEFAULT 0,
  playground INTEGER NOT NULL DEFAULT 0, experiment_id TEXT NOT NULL DEFAULT '', forked_from TEXT NOT NULL DEFAULT '',
  meta TEXT NOT NULL DEFAULT '{}',
  started_ns INTEGER NOT NULL, finished_ns INTEGER, last_seen_ns INTEGER NOT NULL,
  finished_ok INTEGER NOT NULL DEFAULT 0,   -- a run_finish arrived
  failed INTEGER NOT NULL DEFAULT 0,        -- the invoke_agent span ended in error
  err TEXT NOT NULL DEFAULT '',
  steps INTEGER NOT NULL DEFAULT 0, pending INTEGER NOT NULL DEFAULT 0, stop_reason TEXT NOT NULL DEFAULT '',
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens INTEGER NOT NULL DEFAULT 0, cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens INTEGER NOT NULL DEFAULT 0,
  event_count INTEGER NOT NULL DEFAULT 0, delta_count INTEGER NOT NULL DEFAULT 0, message_count INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX runs_started ON runs(parent_run_id, started_ns DESC);
CREATE INDEX runs_session ON runs(session_id, turn) WHERE session_id <> '';
CREATE INDEX runs_public ON runs(public_id) WHERE public_id <> '';
CREATE INDEX runs_agent ON runs(agent, started_ns DESC);
