-- 0028 · the experiment's pass predicate (UX plan D8, UX-C-38), one
-- logical schema with obsdb/sqlite's 0024: the playground's predicate
-- text, stored and returned verbatim (obsdb.Experiment's PassPredicate).
-- Additive, no backfill: a row written before it reads ''.
ALTER TABLE experiments ADD COLUMN IF NOT EXISTS PassPredicate String DEFAULT '';
