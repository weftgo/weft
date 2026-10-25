-- 0024 · the experiment's pass predicate (UX plan D8, UX-C-38), one
-- logical schema with obsdb/clickhouse's 0028: the playground's
-- predicate text, stored and returned verbatim (obsdb.Experiment's
-- PassPredicate; the language is the playground's). Additive, no
-- backfill: a row saved before it reads ''.
ALTER TABLE experiments ADD COLUMN pass_predicate TEXT NOT NULL DEFAULT '';
