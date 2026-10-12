-- 0013 · what the core emitted, on the run row (docs/record-contract.md
-- §3, §6, §7; U5), one logical schema with obsdb/sqlite's 0012.
-- EventsEmitted / MessagesEmitted are the invoke_agent span's
-- weft.run.events / weft.run.messages: the durable event records and
-- growth messages records the run handed to its LoggerProvider. A
-- reader compares them with what it stored — fewer stored is a gap
-- ("N of M events were lost"), 0 emitted with nothing stored is
-- no_logger. 0 when the root carried neither (not stored yet, or a weft
-- before U5).
--
-- weft_runs_traces_mv is 0012's select with the two columns from the
-- invoke_agent span and the two keys in its contract tuple (they are
-- span attributes: without them obsdb.MetaOf's catch-all would put them
-- in meta), pinned by TestMigration0013RestatesTracesView. The logs
-- view is unchanged: no record carries either key. MODIFY QUERY swaps
-- the select in place (0003's reasoning).
--
-- Then a backfill from the root spans already stored: one weft_runs
-- row per root carrying a count, with the span's own times as Started
-- and LastSeen — the values the view inserted for it, so min/max move
-- nothing. Re-running it only adds rows max() folds away.
ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS EventsEmitted SimpleAggregateFunction(max, Int64);

ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS MessagesEmitted SimpleAggregateFunction(max, Int64);

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
        'weft.messages.to_seq', 'weft.compaction.hash', 'weft.compaction.scope',
        'gen_ai.response.model', 'weft.stream', 'weft.ttft_ms', 'weft.latency_ms',
        'weft.attempt.retry_after_ms',
        'weft.override.instructions', 'weft.override.max_steps', 'weft.override.model',
        'weft.override.parallelism', 'weft.override.params', 'weft.override.park_all_except',
        'weft.override.park_on', 'weft.override.thinking', 'weft.override.tool_choice',
        'weft.override.tools',
        'weft.raw.index',
        'weft.run.stopped_by', 'weft.cancel.cause',
        'weft.run.events', 'weft.run.messages'], k), SpanAttributes) AS metaMap
SELECT
    SpanAttributes['weft.run.id'] AS RunId,
    SpanAttributes['weft.parent.run.id'] AS ParentRunID,
    SpanAttributes['weft.parent.call.id'] AS ParentCallID,
    TraceId AS TraceID,
    SpanAttributes['gen_ai.agent.name'] AS Agent,
    SpanAttributes['gen_ai.provider.name'] AS Provider,
    SpanAttributes['gen_ai.request.model'] AS Model,
    SpanAttributes['weft.manifest.hash'] AS ManifestHash,
    if(isInvoke, if(SpanAttributes['weft.version'] != '', SpanAttributes['weft.version'], ResourceAttributes['weft.version']), '') AS WeftVersion,
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
    toInt64(0) AS RequestCount,
    toUInt8(isInvoke) AS RootEnded,
    if(isInvoke, SpanAttributes['weft.run.stopped_by'], '') AS StoppedBy,
    if(isInvoke, SpanAttributes['weft.cancel.cause'], '') AS CancelCause,
    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.events']), 0) AS EventsEmitted,
    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.messages']), 0) AS MessagesEmitted
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != '';

INSERT INTO weft_runs (RunId, Started, LastSeen, EventsEmitted, MessagesEmitted)
SELECT
    SpanAttributes['weft.run.id'],
    Timestamp,
    Timestamp + toIntervalNanosecond(toInt64(Duration)),
    toInt64OrZero(SpanAttributes['weft.run.events']),
    toInt64OrZero(SpanAttributes['weft.run.messages'])
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != ''
  AND (mapContains(SpanAttributes, 'weft.run.events') OR mapContains(SpanAttributes, 'weft.run.messages'))
  AND if(mapContains(SpanAttributes, 'gen_ai.operation.name'),
         SpanAttributes['gen_ai.operation.name'] = 'invoke_agent',
         startsWith(SpanName, 'invoke_agent'));
