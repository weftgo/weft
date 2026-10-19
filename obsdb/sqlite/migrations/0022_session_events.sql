-- 0022 · the session_events index (docs/record-contract.md §6; U12.2,
-- UX-B-01, UX-B-02, D6, A3), one logical schema with obsdb/clickhouse's
-- 0025.
--
-- A session's state log — thread's records (weft.record thread, event
-- weft.thread.<kind>) and its compaction markers (weft.record
-- compaction) — is filed under runs, so a session's timeline would read
-- every turn's records to find them. session_events holds each such
-- record that carries a weft.session.id once more, keyed by the session:
-- the writer inserts it beside the record (the same transaction, INSERT
-- OR IGNORE on (session_id, run_id, kind, pos) — pos the record's
-- body-derived position, so a retried batch is a no-op), and a thread
-- record filed under no run (held at Close: weft.run.id "") lands here
-- alone, run_id ''. session_events_time serves obsdb.SessionEventReader:
-- one session in (time, run, kind, pos) order.
--
-- Backfill: every stored thread and compaction record carrying a session
-- id, its event name derived as obsdb.SessionEventName derives it (the
-- records table keeps none). DeleteRun / Sweep delete a run's rows by
-- run_id, DeleteSession every row of the session.
CREATE TABLE IF NOT EXISTS session_events (
  session_id TEXT NOT NULL,
  run_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  pos INTEGER NOT NULL,
  time_ns INTEGER NOT NULL,
  event_name TEXT NOT NULL DEFAULT '',
  public_id TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL,
  PRIMARY KEY (session_id, run_id, kind, pos)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS session_events_time ON session_events(session_id, time_ns, run_id, kind, pos);
CREATE INDEX IF NOT EXISTS session_events_run ON session_events(run_id) WHERE run_id <> '';
INSERT OR IGNORE INTO session_events (session_id, run_id, kind, pos, time_ns, event_name, public_id, content, body)
SELECT json_extract(attrs, '$."weft.session.id"'), run_id, kind, pos, time_ns,
       CASE WHEN kind = 'compaction' THEN 'weft.compaction'
            WHEN json_valid(body) AND COALESCE(json_extract(body, '$.kind'), '') <> '' THEN 'weft.thread.' || json_extract(body, '$.kind')
            ELSE '' END,
       COALESCE(json_extract(attrs, '$."weft.public_id"'), ''),
       COALESCE(json_extract(attrs, '$."weft.content"'), ''),
       body
FROM records
WHERE kind IN ('thread', 'compaction') AND json_valid(attrs)
  AND COALESCE(json_extract(attrs, '$."weft.session.id"'), '') <> '';
