-- 0001 — the initial runs schema (ADR 0010, plan §3.5).
-- Timestamps are RFC 3339 with fixed nine-digit nanoseconds, UTC, so
-- lexicographic order is chronological order; events are rows keyed
-- by (run_id, seq), DeerFlow's run_events shape; tags are a JSON
-- object filtered with json_extract (Mastra's metadata rule); status
-- stores running | succeeded | failed only — interrupted is derived
-- on read from heartbeat_at and never written (ADR 0010 §2.4).

CREATE TABLE runs (
  id             TEXT PRIMARY KEY,
  parent_id      TEXT REFERENCES runs(id) ON DELETE SET NULL,
  parent_call_id TEXT,
  agent          TEXT NOT NULL DEFAULT '',
  model          TEXT NOT NULL,              -- weft.ModelInfo JSON
  manifest_hash  TEXT NOT NULL DEFAULT '',
  weft_version   TEXT NOT NULL DEFAULT '',
  started        TEXT NOT NULL,              -- RFC 3339 nano, UTC
  finished       TEXT,
  heartbeat_at   TEXT NOT NULL,
  status         TEXT NOT NULL,              -- running | succeeded | failed (never interrupted)
  steps          INTEGER NOT NULL DEFAULT 0, -- denormalised from the result for the list view
  usage          TEXT NOT NULL DEFAULT '{}', -- weft.Usage JSON, list view
  tags           TEXT NOT NULL DEFAULT '{}', -- JSON object; json_extract filters
  result         TEXT,                       -- resultDoc JSON (ADR 0010 §2.2)
  err            TEXT,
  format         INTEGER NOT NULL            -- store.FormatVersion
);
CREATE INDEX runs_started       ON runs(started DESC);
CREATE INDEX runs_agent_started ON runs(agent, started DESC);
CREATE INDEX runs_parent        ON runs(parent_id);
CREATE INDEX runs_manifest      ON runs(manifest_hash);
CREATE TABLE run_events (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq    INTEGER NOT NULL,
  event  TEXT NOT NULL,                     -- weft.Event wire JSON
  PRIMARY KEY (run_id, seq)
);
