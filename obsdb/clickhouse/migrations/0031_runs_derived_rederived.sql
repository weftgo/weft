-- 0031 · derived runs re-derived (docs/record-contract.md §9; UX plan
-- I6), one logical schema with obsdb/sqlite's 0026. A top-level
-- invoke_agent span without gen_ai.conversation.id is now a derived run
-- with no session, and a derived row counts its chat spans that carry
-- the conventions' message content: DerivedMessages, read as the row's
-- MessageCount (a derived run has no messages record). The Go step
-- (rederiveStock) re-derives every trace holding a stock candidate span
-- through obsdb.DeriveStockRuns — the writer's rule from this version on.
ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS DerivedMessages SimpleAggregateFunction(max, Int64);
