-- 0007 · a skip index on weft_runs.ExperimentID (the phase 5
-- pre-release review, M2). weft_runs is ORDER BY RunId, so an
-- experiment's list (runsInner's narrowing subquery, `RunId IN (SELECT
-- r.RunId FROM weft_runs AS r WHERE r.ExperimentID = ?)`, the filter
-- already inside the GROUP BY's scan) read every granule; the bloom
-- filter lets it skip the granules holding no row of that experiment —
-- most of them: most runs carry ''. Parts written before it carry no
-- index and are read in full until a merge rewrites them (MATERIALIZE
-- INDEX is left to the operator: a mutation over the whole table).
ALTER TABLE weft_runs ADD INDEX IF NOT EXISTS idx_experiment ExperimentID TYPE bloom_filter GRANULARITY 4;
