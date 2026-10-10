-- 0005 — the raw record (ADR 0028 §12, plan A6). No new column: the
-- raw kind's Step, Content and TruncatedBytes are the columns 0004
-- added, its attempt is read from its body. Two restatements of 0004:
--
-- weft_records_mv widens to the raw kind, positioned by its own per-run
-- index (weft.raw.index) under the same (RunId, Kind, Pos) key; a raw
-- record without its index reads Pos -1, like obsdb.DeriveRecord.
-- Everything else in the view is 0004's select verbatim.
--
-- weft_runs_logs_mv's contract tuple gains weft.raw.index (obsdb.MetaOf's
-- set; TestMigrationContractTuple pins it), so the raw record's position
-- never lands in a run row's metadata. Everything else is 0004's select
-- verbatim. The traces view is untouched: no span carries the key.
-- TestMigration0005RestatesViews pins both selects against 0004's.
ALTER TABLE weft_records_mv MODIFY QUERY
SELECT
    LogAttributes['weft.run.id'] AS RunId,
    LogAttributes['weft.record'] AS Kind,
    multiIf(LogAttributes['weft.event.pos'] != '', toInt64OrZero(LogAttributes['weft.event.pos']),
      LogAttributes['weft.messages.index'] != '', toInt64OrZero(LogAttributes['weft.messages.index']),
      LogAttributes['weft.request.index'] != '', toInt64OrZero(LogAttributes['weft.request.index']),
      LogAttributes['weft.prompt.index'] != '', toInt64OrZero(LogAttributes['weft.prompt.index']),
      LogAttributes['weft.tools.index'] != '', toInt64OrZero(LogAttributes['weft.tools.index']),
      LogAttributes['weft.raw.index'] != '', toInt64OrZero(LogAttributes['weft.raw.index']), -1) AS Pos,
    Timestamp AS Time,
    TraceId AS TraceId,
    SpanId AS SpanId,
    LogAttributes['weft.event.type'] AS EventType,
    if(LogAttributes['weft.step.index'] != '', toInt32OrZero(LogAttributes['weft.step.index']), -1) AS Step,
    LogAttributes['weft.messages.reason'] AS Reason,
    toInt8(LogAttributes['weft.messages.input'] = 'true') AS Input,
    LogAttributes['weft.content'] AS Content,
    toInt64OrZero(LogAttributes['weft.content.truncated_bytes']) AS TruncatedBytes,
    LogAttributes['weft.system.hash'] AS SystemHash,
    LogAttributes['weft.catalog.hash'] AS CatalogHash,
    LogAttributes['weft.session.id'] AS SessionId,
    LogAttributes['weft.public_id'] AS PublicId,
    LogAttributes['gen_ai.agent.name'] AS Agent,
    toInt32OrZero(LogAttributes['weft.turn']) AS Turn,
    Body AS Body
FROM otel_logs
WHERE LogAttributes['weft.record'] IN ('event', 'messages', 'request', 'prompt', 'tools', 'raw')
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
        'weft.messages.to_seq', 'weft.compaction.hash', 'weft.compaction.scope',
        'gen_ai.response.model', 'weft.stream', 'weft.ttft_ms', 'weft.latency_ms',
        'weft.attempt.retry_after_ms',
        'weft.override.instructions', 'weft.override.max_steps', 'weft.override.model',
        'weft.override.parallelism', 'weft.override.params', 'weft.override.park_all_except',
        'weft.override.park_on', 'weft.override.thinking', 'weft.override.tool_choice',
        'weft.override.tools',
        'weft.raw.index'], k), LogAttributes) AS metaMap
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
