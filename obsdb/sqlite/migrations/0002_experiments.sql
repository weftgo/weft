-- 0002 · the experiments table (WEFT-PLAYGROUND §10.4, PQ4): an
-- experiment is a saved group of playground runs — name, variants,
-- inputs — keyed by its id, which is the weft.experiment.id the runs
-- carry. One small table beside runs; the runs themselves stay in
-- runs, joined by experiment_id.
CREATE TABLE experiments (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL DEFAULT '',
  agent       TEXT NOT NULL DEFAULT '',
  created_ns  INTEGER NOT NULL,
  updated_ns  INTEGER NOT NULL,
  variants    TEXT NOT NULL DEFAULT '[]',   -- [{key, overrides}]
  inputs      TEXT NOT NULL DEFAULT '[]'    -- [{key, source_run_id, text}]
);
CREATE INDEX experiments_updated ON experiments(updated_ns DESC);
