-- 0006 · the experiment's command (plan F5.1): the rest of the §5.1
-- command a saved experiment reopens with — engine, side_effects,
-- thread, from_step, source_run_id, transcript_edits — as one JSON
-- document (obsdb.ExperimentCommand). Additive, no backfill: a row
-- written before it reads '' and comes back with the fields absent.
-- The per-variant modes need no column: they ride in Variants.
ALTER TABLE experiments ADD COLUMN IF NOT EXISTS Command String DEFAULT '';
