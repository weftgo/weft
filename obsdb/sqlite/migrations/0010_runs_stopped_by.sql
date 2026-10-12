-- 0010 · what ended a run, on its row (docs/record-contract.md §6; U3.2).
-- stopped_by is the invoke_agent span's weft.run.stopped_by or
-- run_finish's stopped_by — one core value on both carriers (the larger
-- when they differ, ClickHouse's max); cancel_cause is the span's
-- weft.cancel.cause. Both '' when nothing carried them. Additive, with a
-- backfill from the spans and run_finish records already stored, and the
-- two keys taken out of meta, where obsdb.MetaOf's catch-all had put them
-- before they were contract (U3.1 to U3.2).
ALTER TABLE runs ADD COLUMN stopped_by TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN cancel_cause TEXT NOT NULL DEFAULT '';

UPDATE runs SET stopped_by = MAX(
    COALESCE((SELECT MAX(json_extract(s.attrs, '$."weft.run.stopped_by"')) FROM spans s
      WHERE s.run_id = runs.run_id AND json_type(s.attrs, '$."weft.run.stopped_by"') = 'text'), ''),
    COALESCE((SELECT MAX(json_extract(r.body, '$.stopped_by')) FROM records r
      WHERE r.run_id = runs.run_id AND r.kind = 'event' AND r.event_type = 'run_finish'
        AND json_valid(r.body) AND json_type(r.body, '$.stopped_by') = 'text'), ''))
WHERE derived = 0;

UPDATE runs SET cancel_cause = COALESCE((SELECT MAX(json_extract(s.attrs, '$."weft.cancel.cause"')) FROM spans s
    WHERE s.run_id = runs.run_id AND json_type(s.attrs, '$."weft.cancel.cause"') = 'text'), '')
WHERE derived = 0;

UPDATE runs SET meta = json_remove(meta, '$."weft.run.stopped_by"', '$."weft.cancel.cause"')
WHERE json_valid(meta) AND (json_type(meta, '$."weft.run.stopped_by"') IS NOT NULL
    OR json_type(meta, '$."weft.cancel.cause"') IS NOT NULL);
