-- 0009 · status from the root span (docs/record-contract.md §6; ADR
-- 0024's 2026-10-12 amendment). root_ended is 1 once the run's
-- invoke_agent span is stored: a span is exported only when it ended, so
-- the run is over even when its run_finish record never arrived (a
-- tracer-only app's run, or one whose record tail a destination
-- dropped). The status rule reads it beside finished_ok: failed on an
-- Error root, succeeded on a run_finish or any other root, running or
-- interrupted by last-seen otherwise. Additive, with a backfill from the
-- spans already stored — the operation by gen_ai.operation.name when it
-- is a string, else by the span's name (the writer's isInvokeAgent) —
-- and the same backfill for the two identity columns the writer now
-- fills from spans too: weft_version (the span's weft.version, else its
-- resource's) and manifest_hash (the span's weft.manifest.hash), where
-- the records left them empty.
ALTER TABLE runs ADD COLUMN root_ended INTEGER NOT NULL DEFAULT 0;

UPDATE runs SET root_ended = 1 WHERE derived = 0 AND EXISTS (
  SELECT 1 FROM spans s WHERE s.run_id = runs.run_id AND
    CASE WHEN json_type(s.attrs, '$."gen_ai.operation.name"') = 'text'
      THEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'invoke_agent'
      ELSE substr(s.name, 1, 12) = 'invoke_agent' END);

UPDATE runs SET weft_version = COALESCE((
  SELECT COALESCE(
      CASE WHEN json_type(s.attrs, '$."weft.version"') = 'text' THEN NULLIF(json_extract(s.attrs, '$."weft.version"'), '') END,
      CASE WHEN json_type(s.resource, '$."weft.version"') = 'text' THEN NULLIF(json_extract(s.resource, '$."weft.version"'), '') END)
  FROM spans s WHERE s.run_id = runs.run_id AND COALESCE(
      CASE WHEN json_type(s.attrs, '$."weft.version"') = 'text' THEN NULLIF(json_extract(s.attrs, '$."weft.version"'), '') END,
      CASE WHEN json_type(s.resource, '$."weft.version"') = 'text' THEN NULLIF(json_extract(s.resource, '$."weft.version"'), '') END) IS NOT NULL
  LIMIT 1), '')
WHERE weft_version = '' AND derived = 0;

UPDATE runs SET manifest_hash = COALESCE((
  SELECT json_extract(s.attrs, '$."weft.manifest.hash"') FROM spans s
  WHERE s.run_id = runs.run_id AND json_type(s.attrs, '$."weft.manifest.hash"') = 'text'
    AND json_extract(s.attrs, '$."weft.manifest.hash"') <> ''
  LIMIT 1), '')
WHERE manifest_hash = '' AND derived = 0;
