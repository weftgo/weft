-- 0006 · an index for an experiment's runs (the phase 5 pre-release
-- review, M2): GET /api/experiments reads each experiment's runs by
-- experiment_id (RunQuery.ExperimentID), a full runs scan per
-- experiment without it. Its columns are the list's own read —
-- experiment_id and parent_run_id equal, newest first — so the planner
-- prefers it over runs_started. Partial: most runs belong to no
-- experiment and carry '', so the index holds the playground's runs
-- alone (runWhere repeats the experiment_id <> '' the planner needs to
-- use it).
CREATE INDEX IF NOT EXISTS runs_experiment ON runs(experiment_id, parent_run_id, started_ns DESC) WHERE experiment_id <> '';
