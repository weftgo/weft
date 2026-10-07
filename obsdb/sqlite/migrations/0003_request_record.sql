-- 0003 · the request record (ADR 0028). Three run-row columns the
-- request record feeds; additive, no backfill. A run written before
-- this migration keeps the defaults: instructions_hash '' reads "not
-- recorded" (the not_recorded badge), while a run written under ADR
-- 0028 always carries a hash (sha256 of "" without instructions), so a
-- hash with request_count 0 reads "made no model call".
--
-- records needs nothing: kind is free text (the three new kinds land
-- beside event and messages under the same (run_id, kind, pos) key, the
-- position being each kind's own per-run index, -1 without one), and
-- records.step already exists since 0001 (-1 = absent) — weft.step.index
-- on every messages record fills it. A compaction view record's reason
-- stays in records.attrs (weft.messages.reason).
ALTER TABLE runs ADD COLUMN instructions_hash TEXT NOT NULL DEFAULT '';  -- run_start's weft.instructions.hash
ALTER TABLE runs ADD COLUMN catalog_hash TEXT NOT NULL DEFAULT '';       -- weft.catalog.hash of request index 0
ALTER TABLE runs ADD COLUMN request_count INTEGER NOT NULL DEFAULT 0;    -- request records: max weft.request.index + 1
