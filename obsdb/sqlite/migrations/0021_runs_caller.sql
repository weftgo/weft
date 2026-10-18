-- 0021 · pool-child identity (docs/record-contract.md §2, §6; U14,
-- UX-E-21, UX-B-05; O11, D3), one logical schema with obsdb/clickhouse's
-- 0023.
--
-- The core stamps weft.parent.run.id / weft.parent.call.id on a core
-- Subagent's child alone; every other run started on a tool call's
-- context — a thread/pool child session's turn, another agent's Generate
-- inside a handler — carries weft.caller.run.id / weft.caller.call.id (a
-- link, never parenthood) on its invoke_agent span and run_start record.
-- runs.caller_run_id / caller_call_id keep them, fill-if-empty like
-- every identity column; runs_caller serves a run's caller-linked runs
-- (obsdb.RunQuery.CallerRunID: a delegating turn's pool child turns).
--
-- A session is grouped over its own turns (D3), and a session's
-- delegated sessions are found by their turns' weft.session.parent
-- (obsdb.SessionQuery.Parent): runs_session_parent indexes that meta
-- key over the runs carrying a session, the reader stating the same
-- expression and the partial condition (session_id <> '').
--
-- No backfill: weft.caller.* is new with this version. A run recorded
-- before it that a tool call started keeps its parent_run_id; the own-
-- turn rule reads a pool child's turn of that age by its id.
ALTER TABLE runs ADD COLUMN caller_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN caller_call_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS runs_caller ON runs(caller_run_id, started_ns) WHERE caller_run_id <> '';
CREATE INDEX IF NOT EXISTS runs_session_parent ON runs(json_extract(meta, '$."weft.session.parent"'))
  WHERE session_id <> '';
