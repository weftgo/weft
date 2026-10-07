-- 0004 — the request record (ADR 0028), one logical schema with
-- obsdb/sqlite's 0003. Additive, no backfill: rows written before this
-- migration read the column defaults, and a RequestCount of 0 reads
-- "not recorded" (the not_recorded badge), never "made no model call".
--
-- weft_records gains Step (weft.step.index, -1 = absent — the SQLite
-- records.step convention since its 0001), and weft_records_mv widens
-- to the three new durable kinds, request, prompt and tools, each
-- positioned by its own per-run index (weft.request.index,
-- weft.prompt.index, weft.tools.index) under the same (RunId, Kind,
-- Pos) key. Everything else in the view is 0001's select verbatim.
--
-- weft_runs gains InstructionsHash, CatalogHash and RequestCount as
-- max-aggregates (one non-empty value per run each: the run_start
-- record's hash, the tools record at index 0, the request high-water
-- mark). The run views fill them when the emission ships (ADR 0028's
-- A1), restating their select together with the contract tuple's new
-- keys; until then they hold the defaults.
ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Step Int32 DEFAULT -1 AFTER EventType;

ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS InstructionsHash SimpleAggregateFunction(max, String);

ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS CatalogHash SimpleAggregateFunction(max, String);

ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS RequestCount SimpleAggregateFunction(max, Int64);

ALTER TABLE weft_records_mv MODIFY QUERY
SELECT
    LogAttributes['weft.run.id'] AS RunId,
    LogAttributes['weft.record'] AS Kind,
    multiIf(LogAttributes['weft.event.pos'] != '', toInt64OrZero(LogAttributes['weft.event.pos']),
      LogAttributes['weft.messages.index'] != '', toInt64OrZero(LogAttributes['weft.messages.index']),
      LogAttributes['weft.request.index'] != '', toInt64OrZero(LogAttributes['weft.request.index']),
      LogAttributes['weft.prompt.index'] != '', toInt64OrZero(LogAttributes['weft.prompt.index']),
      LogAttributes['weft.tools.index'] != '', toInt64OrZero(LogAttributes['weft.tools.index']), -1) AS Pos,
    Timestamp AS Time,
    TraceId AS TraceId,
    SpanId AS SpanId,
    LogAttributes['weft.event.type'] AS EventType,
    if(LogAttributes['weft.step.index'] != '', toInt32OrZero(LogAttributes['weft.step.index']), -1) AS Step,
    LogAttributes['weft.session.id'] AS SessionId,
    LogAttributes['weft.public_id'] AS PublicId,
    LogAttributes['gen_ai.agent.name'] AS Agent,
    toInt32OrZero(LogAttributes['weft.turn']) AS Turn,
    Body AS Body
FROM otel_logs
WHERE LogAttributes['weft.record'] IN ('event', 'messages', 'request', 'prompt', 'tools')
  AND LogAttributes['weft.run.id'] != '';
