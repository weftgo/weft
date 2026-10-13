-- 0018 · the approval link (docs/record-contract.md §6; U13.1,
-- UX-E-17), one logical schema with obsdb/clickhouse's 0019.
--
-- A resuming run names the parked run it resumes (core.ResumeOf →
-- weft.resumed_from on its invoke_agent span and run_start record):
-- runs.resumed_from keeps it, fill-if-empty like every identity
-- column. The reverse link — a parked run's resumed_by — is not
-- stored: the reader derives it (the latest by start time (a tie: the
-- larger id) whose resumed_from names the row), so a resumer ingested
-- before or after the parked run links it alike, and a deleted
-- resumer unlinks it. runs_resumed_from serves that lookup; the
-- reader states the partial index's own condition (resumed_from <> '')
-- beside the equality.
--
-- No backfill: weft.resumed_from is new with this version, so no
-- stored span or record carries it.
ALTER TABLE runs ADD COLUMN resumed_from TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS runs_resumed_from ON runs(resumed_from, run_id) WHERE resumed_from <> '';
