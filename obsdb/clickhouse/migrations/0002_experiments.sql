-- 0002 · the experiments table (WEFT-PLAYGROUND §10.4, PQ4): the
-- playground's saved experiment definitions — name, variants, inputs —
-- keyed by the id its runs carry as weft.experiment.id. Replacing by
-- InsertTime (the newest write wins under FINAL), the same idempotent
-- shape weft_runs uses; the runs themselves stay in weft_runs and
-- join by ExperimentId.
CREATE TABLE IF NOT EXISTS experiments
(
    Id        String,
    Name      String,
    Agent     String,
    Created   DateTime64(9, 'UTC'),
    Updated   DateTime64(9, 'UTC'),
    Variants  String,
    Inputs    String,
    InsertTime DateTime64(9, 'UTC') DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY Id;
