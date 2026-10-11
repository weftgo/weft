-- 0008 · derived runs (devtools-plan I1.2; docs/record-contract.md §9).
-- A run the reader built from stock OTel GenAI spans — an invoke_agent
-- span with gen_ai.conversation.id and no weft.run.id — is a runs row
-- like any other, keyed otel-<trace id>-<root span id>, with derived = 1:
-- the writer re-derives a trace's rows (obsdb.DeriveStockRuns) whenever
-- a batch brings a stock GenAI span of it, overwriting the row whole.
-- Additive: every row written before it reads derived = 0, which it is.
ALTER TABLE runs ADD COLUMN derived INTEGER NOT NULL DEFAULT 0;
