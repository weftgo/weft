-- 0002 — the denormalised session title (plan §7, the List filters).
-- The current title is the last info entry's Title — entry state, not
-- header state — so the title filter would otherwise read a session's
-- every entry per query. The column is maintained on Append (the last
-- info entry of a batch wins) and backfilled here from the entries
-- that exist, once, in the migration (the store's denormalisation
-- pattern: derived values are never stored twice going forward, and
-- are derived exactly once at the migration).

ALTER TABLE sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';
UPDATE sessions SET title = COALESCE((
  SELECT json_extract(line, '$.title')
  FROM entries
  WHERE entries.session = sessions.id
    AND json_extract(line, '$.type') = 'info'
  ORDER BY seq DESC
  LIMIT 1
), '');
