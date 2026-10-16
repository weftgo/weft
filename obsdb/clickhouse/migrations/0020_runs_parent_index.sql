-- 0020 · a skip index on weft_runs.ParentRunID (U9.1, UX-E-24; one
-- logical change with obsdb/sqlite's 0020, whose trace-order index has
-- no counterpart here: a trace's spans are idx_trace_id's, 0001).
--
-- weft_runs is ORDER BY RunId, so a parent's children — Run's
-- Children, and ChildRuns (obsdb.TreeReader: the runs list prices a
-- page's subagent trees in one statement per level) — narrowed with
-- `RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.ParentRunID IN
-- (…))` read every granule. The bloom filter skips the granules holding
-- no row of those parents — most of them: most runs are top-level and
-- carry ''. MATERIALIZE INDEX builds it for the parts written before
-- it, in the background (0016's rule): until it finishes those parts
-- are read in full, as before.
ALTER TABLE weft_runs ADD INDEX IF NOT EXISTS idx_parent_run ParentRunID TYPE bloom_filter GRANULARITY 4;

ALTER TABLE weft_runs MATERIALIZE INDEX idx_parent_run;
