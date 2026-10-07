-- 0003 — lock identity, session generations, the envelope column and
-- the keyset index, and the title rule.
--
-- session_locks: a pid is not an identity — pids are reused, and a
-- restarted container is PID 1 again on the same hostname. The lock row
-- now also carries the holder's process token (random per process, so
-- "another Storage in this very process" is told apart from "an earlier
-- process that wore this pid") and the holder's start time (so a live
-- pid that is not the process that took the lock reads as dead). Rows
-- written before this migration carry neither and are judged by pid
-- alone, as before.
--
-- sessions.gen: a random token set at Create, so a watcher can tell the
-- session it is tailing from one deleted and created again under the
-- same id. Sessions created before this migration share the empty
-- generation; any successor gets a fresh one.
--
-- sessions.envelope: the header's format integer ("weft"), 0 for a
-- header row that is not a session header at all. List filters on it
-- instead of parsing every header's JSON to learn whether this build
-- can read it: the page and the count are answered from the index.
-- Derived here once for the rows that exist; maintained at Create.
--
-- The List order is (created DESC, id DESC) and its cursor is a keyset
-- over that pair: the index now covers the envelope and both columns,
-- replacing the created-only index no query used.
--
-- The current title is the last info entry carrying a NON-empty title
-- (Session.Title's rule); migration 0002 backfilled the last info
-- entry's title, empty or not. Re-derive once under the rule; lines
-- that are not valid JSON carry no title.

ALTER TABLE session_locks ADD COLUMN process TEXT NOT NULL DEFAULT '';
ALTER TABLE session_locks ADD COLUMN started TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN gen TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN envelope INTEGER NOT NULL DEFAULT 0;
UPDATE sessions SET envelope = CASE
  WHEN json_valid(header) THEN CASE
    WHEN json_extract(header, '$.type') = 'session' AND json_type(header, '$.weft') = 'integer'
    THEN json_extract(header, '$.weft') ELSE 0 END
  ELSE 0 END;
DROP INDEX IF EXISTS sessions_created;
CREATE INDEX sessions_order ON sessions(envelope, created DESC, id DESC);
UPDATE sessions SET title = COALESCE((
  SELECT CASE WHEN json_valid(line) THEN json_extract(line, '$.title') END
  FROM entries
  WHERE entries.session = sessions.id
    AND torn = 0
    AND CASE WHEN json_valid(line)
             THEN json_extract(line, '$.type') = 'info'
              AND COALESCE(json_extract(line, '$.title'), '') <> ''
             ELSE 0 END
  ORDER BY seq DESC
  LIMIT 1
), '');
