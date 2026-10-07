-- 0004 — the request record (ADR 0028), one logical schema with
-- obsdb/sqlite's 0003. Additive, no backfill: rows written before this
-- migration read the column defaults. A run row with InstructionsHash
-- '' was written before this contract (not_recorded); one with a hash
-- and RequestCount 0 made no model call.
--
-- weft_records gains Step (weft.step.index, -1 = absent — the SQLite
-- records.step convention since its 0001; rows from before 0004 read
-- -1 and readers infer, badged derived) and Reason
-- (weft.messages.reason, '' = transcript growth; ClickHouse keeps no
-- attribute column, so a compaction view record is told apart here),
-- and three more attribute reads the request record's readers need for
-- the same reason: Input (weft.messages.input as 0/1; rows written
-- before this migration read -1, "not stored", and the transcript
-- reader infers the flag for them, badged derived), Content
-- (weft.content: 'full', 'stripped', '' when absent) and
-- TruncatedBytes (weft.content.truncated_bytes, 0 when absent).
-- weft_records_mv widens to the three new durable kinds, request,
-- prompt and tools, each positioned by its own per-run index
-- (weft.request.index, weft.prompt.index, weft.tools.index) under the
-- same (RunId, Kind, Pos) key; a new-kind record without its index
-- reads Pos -1, like obsdb.DeriveRecord. Everything else in the view
-- is 0001's select verbatim. (Input, Content and TruncatedBytes were
-- added to this file before it was released, with A1.2; a database
-- that applied the earlier text of 0004 lacks them and must be
-- recreated — no released binary wrote one.)
--
-- weft_runs gains InstructionsHash, CatalogHash and RequestCount as
-- max-aggregates, and both run views are restated (0001's logs view,
-- 0003's traces view) with two changes each: the contract tuple gains
-- ADR 0028's twelve keys (obsdb.MetaOf's set; TestMigrationContractTuple
-- pins it), and the three columns are filled. InstructionsHash comes
-- from run_start's (and the invoke_agent span's) weft.instructions.hash;
-- CatalogHash from request index 0's weft.catalog.hash (the request
-- record survives content-off chains, the tools record does not);
-- RequestCount is the high-water mark max(weft.request.index) + 1 —
-- the count of request records, since the index is contiguous from 0,
-- and retry-proof the way DeltaCount is (a sum over rows would count a
-- retried batch twice). TestMigration0004RestatesViews pins every
-- restated select against the one it replaces.
ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Step Int32 DEFAULT -1 AFTER EventType;

ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Reason LowCardinality(String) DEFAULT '' AFTER Step;

ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Input Int8 DEFAULT -1 AFTER Reason;

ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Content LowCardinality(String) DEFAULT '' AFTER Input;

ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS TruncatedBytes Int64 DEFAULT 0 AFTER Content;

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
    LogAttributes['weft.messages.reason'] AS Reason,
    toInt8(LogAttributes['weft.messages.input'] = 'true') AS Input,
    LogAttributes['weft.content'] AS Content,
    toInt64OrZero(LogAttributes['weft.content.truncated_bytes']) AS TruncatedBytes,
    LogAttributes['weft.session.id'] AS SessionId,
    LogAttributes['weft.public_id'] AS PublicId,
    LogAttributes['gen_ai.agent.name'] AS Agent,
    toInt32OrZero(LogAttributes['weft.turn']) AS Turn,
    Body AS Body
FROM otel_logs
WHERE LogAttributes['weft.record'] IN ('event', 'messages', 'request', 'prompt', 'tools')
  AND LogAttributes['weft.run.id'] != '';

ALTER TABLE weft_runs_logs_mv MODIFY QUERY
WITH mapFilter((k, v) -> NOT has([
        'weft.run.id', 'weft.parent.run.id', 'weft.parent.call.id',
        'weft.session.id', 'weft.public_id', 'weft.turn', 'gen_ai.agent.name',
        'weft.record', 'weft.event.type', 'weft.event.pos', 'weft.delta.pos',
        'weft.messages.index', 'weft.step.index', 'weft.tool.seq',
        'weft.playground', 'weft.experiment.id', 'weft.forked_from',
        'weft.messages.count', 'weft.messages.input', 'weft.content',
        'weft.content.truncated_bytes', 'weft.version', 'weft.manifest.hash',
        'weft.run.steps', 'weft.run.pending', 'weft.run.stop_reason',
        'weft.stop.raw', 'weft.model.tool_calls', 'weft.tool.approved',
        'weft.tool.pending', 'weft.tool.result_bytes', 'weft.metadata.dropped',
        'weft.override.hash',
        'gen_ai.operation.name', 'gen_ai.provider.name', 'gen_ai.request.model',
        'gen_ai.response.finish_reasons', 'gen_ai.usage.input_tokens',
        'gen_ai.usage.output_tokens', 'gen_ai.usage.cache_read.input_tokens',
        'gen_ai.usage.cache_creation.input_tokens', 'gen_ai.usage.reasoning.output_tokens',
        'gen_ai.tool.name', 'gen_ai.tool.call.id', 'gen_ai.conversation.id',
        'session.id', 'user.id', 'error.type',
        'weft.request.index', 'weft.prompt.index', 'weft.tools.index',
        'weft.system.hash', 'weft.catalog.hash', 'weft.attempt.index',
        'weft.instructions.hash', 'weft.messages.reason', 'weft.messages.from_seq',
        'weft.messages.to_seq', 'weft.compaction.hash', 'weft.compaction.scope'], k), LogAttributes) AS metaMap
SELECT
    LogAttributes['weft.run.id'] AS RunId,
    LogAttributes['weft.parent.run.id'] AS ParentRunID,
    LogAttributes['weft.parent.call.id'] AS ParentCallID,
    TraceId AS TraceID,
    LogAttributes['gen_ai.agent.name'] AS Agent,
    if(LogAttributes['weft.event.type'] = 'run_start', JSONExtractString(Body, 'model', 'provider'), '') AS Provider,
    if(LogAttributes['weft.event.type'] = 'run_start', JSONExtractString(Body, 'model', 'name'), '') AS Model,
    LogAttributes['weft.manifest.hash'] AS ManifestHash,
    LogAttributes['weft.version'] AS WeftVersion,
    ServiceName AS Service,
    LogAttributes['weft.session.id'] AS SessionID,
    LogAttributes['weft.public_id'] AS PublicID,
    toInt32OrZero(LogAttributes['weft.turn']) AS Turn,
    toUInt8(LogAttributes['weft.playground'] = 'true') AS Playground,
    LogAttributes['weft.experiment.id'] AS ExperimentID,
    LogAttributes['weft.forked_from'] AS ForkedFrom,
    if(length(metaMap) = 0, '', toJSONString(metaMap)) AS Meta,
    Timestamp AS Started,
    if(LogAttributes['weft.event.type'] = 'run_finish', Timestamp, NULL) AS Finished,
    Timestamp AS LastSeen,
    toUInt8(LogAttributes['weft.event.type'] = 'run_finish') AS FinishedOK,
    toUInt8(0) AS Failed,
    '' AS Err,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt32(JSONExtractInt(Body, 'steps')), 0) AS Steps,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt32(length(JSONExtractArrayRaw(Body, 'pending'))), 0) AS Pending,
    '' AS StopReason,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt64(JSONExtractInt(Body, 'usage', 'input_tokens')), 0) AS InputTokens,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt64(JSONExtractInt(Body, 'usage', 'output_tokens')), 0) AS OutputTokens,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt64(JSONExtractInt(Body, 'usage', 'cached_input_tokens')), 0) AS CachedInputTokens,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt64(JSONExtractInt(Body, 'usage', 'cache_write_tokens')), 0) AS CacheWriteTokens,
    if(LogAttributes['weft.event.type'] = 'run_finish', toInt64(JSONExtractInt(Body, 'usage', 'reasoning_tokens')), 0) AS ReasoningTokens,
    if(LogAttributes['weft.record'] = 'delta', toInt64OrZero(LogAttributes['weft.delta.pos']) + 1, 0) AS DeltaCount,
    if(LogAttributes['weft.event.type'] = 'run_start', LogAttributes['weft.instructions.hash'], '') AS InstructionsHash,
    if(LogAttributes['weft.record'] = 'request' AND LogAttributes['weft.request.index'] = '0', LogAttributes['weft.catalog.hash'], '') AS CatalogHash,
    if(LogAttributes['weft.record'] = 'request' AND LogAttributes['weft.request.index'] != '', toInt64OrZero(LogAttributes['weft.request.index']) + 1, 0) AS RequestCount
FROM otel_logs
WHERE LogAttributes['weft.run.id'] != '';

ALTER TABLE weft_runs_traces_mv MODIFY QUERY
WITH if(mapContains(SpanAttributes, 'gen_ai.operation.name'),
       SpanAttributes['gen_ai.operation.name'] = 'invoke_agent',
       startsWith(SpanName, 'invoke_agent')) AS isInvoke,
     StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS isError,
     mapFilter((k, v) -> NOT has([
        'weft.run.id', 'weft.parent.run.id', 'weft.parent.call.id',
        'weft.session.id', 'weft.public_id', 'weft.turn', 'gen_ai.agent.name',
        'weft.record', 'weft.event.type', 'weft.event.pos', 'weft.delta.pos',
        'weft.messages.index', 'weft.step.index', 'weft.tool.seq',
        'weft.playground', 'weft.experiment.id', 'weft.forked_from',
        'weft.messages.count', 'weft.messages.input', 'weft.content',
        'weft.content.truncated_bytes', 'weft.version', 'weft.manifest.hash',
        'weft.run.steps', 'weft.run.pending', 'weft.run.stop_reason',
        'weft.stop.raw', 'weft.model.tool_calls', 'weft.tool.approved',
        'weft.tool.pending', 'weft.tool.result_bytes', 'weft.metadata.dropped',
        'weft.override.hash',
        'gen_ai.operation.name', 'gen_ai.provider.name', 'gen_ai.request.model',
        'gen_ai.response.finish_reasons', 'gen_ai.usage.input_tokens',
        'gen_ai.usage.output_tokens', 'gen_ai.usage.cache_read.input_tokens',
        'gen_ai.usage.cache_creation.input_tokens', 'gen_ai.usage.reasoning.output_tokens',
        'gen_ai.tool.name', 'gen_ai.tool.call.id', 'gen_ai.conversation.id',
        'session.id', 'user.id', 'error.type',
        'weft.request.index', 'weft.prompt.index', 'weft.tools.index',
        'weft.system.hash', 'weft.catalog.hash', 'weft.attempt.index',
        'weft.instructions.hash', 'weft.messages.reason', 'weft.messages.from_seq',
        'weft.messages.to_seq', 'weft.compaction.hash', 'weft.compaction.scope'], k), SpanAttributes) AS metaMap
SELECT
    SpanAttributes['weft.run.id'] AS RunId,
    SpanAttributes['weft.parent.run.id'] AS ParentRunID,
    SpanAttributes['weft.parent.call.id'] AS ParentCallID,
    TraceId AS TraceID,
    SpanAttributes['gen_ai.agent.name'] AS Agent,
    SpanAttributes['gen_ai.provider.name'] AS Provider,
    SpanAttributes['gen_ai.request.model'] AS Model,
    SpanAttributes['weft.manifest.hash'] AS ManifestHash,
    SpanAttributes['weft.version'] AS WeftVersion,
    ServiceName AS Service,
    SpanAttributes['weft.session.id'] AS SessionID,
    SpanAttributes['weft.public_id'] AS PublicID,
    toInt32OrZero(SpanAttributes['weft.turn']) AS Turn,
    toUInt8(SpanAttributes['weft.playground'] = 'true') AS Playground,
    SpanAttributes['weft.experiment.id'] AS ExperimentID,
    SpanAttributes['weft.forked_from'] AS ForkedFrom,
    if(length(metaMap) = 0, '', toJSONString(metaMap)) AS Meta,
    Timestamp AS Started,
    if(isInvoke, Timestamp + toIntervalNanosecond(toInt64(Duration)), NULL) AS Finished,
    Timestamp + toIntervalNanosecond(toInt64(Duration)) AS LastSeen,
    toUInt8(0) AS FinishedOK,
    toUInt8(isInvoke AND isError) AS Failed,
    if(isInvoke AND isError, StatusMessage, '') AS Err,
    if(isInvoke, toInt32OrZero(SpanAttributes['weft.run.steps']), 0) AS Steps,
    if(isInvoke, toInt32OrZero(SpanAttributes['weft.run.pending']), 0) AS Pending,
    if(isInvoke, SpanAttributes['weft.run.stop_reason'], '') AS StopReason,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.input_tokens']), 0) AS InputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.output_tokens']), 0) AS OutputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.cache_read.input_tokens']), 0) AS CachedInputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.cache_creation.input_tokens']), 0) AS CacheWriteTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.reasoning.output_tokens']), 0) AS ReasoningTokens,
    toInt64(0) AS DeltaCount,
    if(isInvoke, SpanAttributes['weft.instructions.hash'], '') AS InstructionsHash,
    '' AS CatalogHash,
    toInt64(0) AS RequestCount
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != '';
