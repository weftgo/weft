-- 0003 · the request record (ADR 0028). Three run-row columns the
-- request, prompt and tools record kinds feed; additive, no backfill.
-- A run written before this migration keeps the defaults, and a
-- request_count of 0 reads "not recorded" (the not_recorded badge),
-- never "made no model call".
--
-- records needs nothing: kind is free text (the three new kinds land
-- beside event and messages under the same (run_id, kind, pos) key, the
-- position being each kind's own per-run index), and records.step
-- already exists since 0001 (-1 = absent) — weft.step.index on every
-- messages record fills it.
ALTER TABLE runs ADD COLUMN instructions_hash TEXT NOT NULL DEFAULT '';  -- sha256 hex of the run's instructions (run_start)
ALTER TABLE runs ADD COLUMN catalog_hash TEXT NOT NULL DEFAULT '';       -- the run's first tool-catalog hash (tools index 0)
ALTER TABLE runs ADD COLUMN request_count INTEGER NOT NULL DEFAULT 0;    -- request records: max weft.request.index + 1
