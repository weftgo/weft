-- 0017 · the problem tokens re-derived (docs/record-contract.md §6;
-- U4-fix), one logical schema with obsdb/sqlite's 0016.
--
-- U4's rules (0015) counted every failed attempt as retried, keyed
-- fallbacks on a model-name comparison, counted RETRY (core.ModelRetry,
-- the intended repair loop) as a tool error and keyed weft.run.denied
-- by its count. The traces view is restated with obsdb.SpanProblems'
-- new rules:
--
--   attempt:<span>:<chat span>:<index>:<1|0>  every attempt span, so the
--       read fold counts a failed one only below its call's highest
--       index (a later attempt followed it);
--   fallback_answer:<span>  an attempt that succeeded carrying
--       weft.attempt.layer 'fallback' (mw.Fallback's backup report);
--   tool_failed:<span>  an execute_tool error, except DENIED (denied:),
--       RETRY and the context errors;
--   truncated:<span>  unchanged;
--   denied_approval:<span>:<n>  the invoke_agent span's weft.run.denied.
--
-- The tuple gains weft.attempt.layer (a span attribute; the logs view
-- keeps 0015's tuple), pinned by TestMigrationContractTuple.
--
-- Then a backfill from the spans already stored, the new tokens only,
-- one weft_runs row per span with its own times (min/max move nothing).
-- The set is groupUniqArrayArray, so U4's tokens stay in it: their kinds
-- (retried, fallback, tool_error, the one-field denied_approval:<n>)
-- count nowhere (obsdb.CountProblems), the re-derived ones count.

ALTER TABLE weft_runs_traces_mv MODIFY QUERY
WITH if(mapContains(SpanAttributes, 'gen_ai.operation.name'),
       SpanAttributes['gen_ai.operation.name'] = 'invoke_agent',
       startsWith(SpanName, 'invoke_agent')) AS isInvoke,
     StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS isError,
     SpanAttributes['gen_ai.operation.name'] AS op,
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
        'weft.run.events', 'weft.run.messages',
        'weft.records.dropped',
        'weft.run.denied', 'weft.tool.truncated_bytes', 'weft.tool.input_error',
        'weft.panic.layer',
        'weft.attempt.layer'], k), SpanAttributes) AS metaMap
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
    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.messages']), 0) AS MessagesEmitted,
    if(isInvoke, multiIf(SpanAttributes['error.type'] IN ('context.Canceled', 'context.DeadlineExceeded'), 'cancelled', SpanAttributes['error.type']), '') AS ErrorType,
    arrayFilter(x -> x != '', [
        if(SpanName = 'attempt' AND op = '',
           concat('attempt:', SpanId, ':', ParentSpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.attempt.index'])), ':', if(isError, '1', '0')), ''),
        if(SpanName = 'attempt' AND op = '' AND NOT isError AND SpanAttributes['weft.attempt.layer'] = 'fallback', concat('fallback_answer:', SpanId), ''),
        if(op = 'execute_tool' AND isError AND SpanAttributes['error.type'] NOT IN ('RETRY', 'context.Canceled', 'context.DeadlineExceeded'), concat(if(SpanAttributes['error.type'] = 'DENIED', 'denied:', 'tool_failed:'), SpanId), ''),
        if(op = 'execute_tool' AND toInt64OrZero(SpanAttributes['weft.tool.truncated_bytes']) > 0, concat('truncated:', SpanId), ''),
        if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.run.denied']) > 0,
           concat('denied_approval:', SpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.run.denied']))), '')
    ]) AS Problems
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != '';

INSERT INTO weft_runs (RunId, Started, LastSeen, Problems)
SELECT RunId, Started, LastSeen, Problems FROM (
    SELECT
        SpanAttributes['weft.run.id'] AS RunId,
        Timestamp AS Started,
        Timestamp + toIntervalNanosecond(toInt64(Duration)) AS LastSeen,
        SpanAttributes['gen_ai.operation.name'] AS op,
        StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS isError,
        arrayFilter(x -> x != '', [
            if(SpanName = 'attempt' AND op = '',
               concat('attempt:', SpanId, ':', ParentSpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.attempt.index'])), ':', if(isError, '1', '0')), ''),
            if(SpanName = 'attempt' AND op = '' AND NOT isError AND SpanAttributes['weft.attempt.layer'] = 'fallback', concat('fallback_answer:', SpanId), ''),
            if(op = 'execute_tool' AND isError AND SpanAttributes['error.type'] NOT IN ('RETRY', 'context.Canceled', 'context.DeadlineExceeded'), concat(if(SpanAttributes['error.type'] = 'DENIED', 'denied:', 'tool_failed:'), SpanId), ''),
            if(op = 'execute_tool' AND toInt64OrZero(SpanAttributes['weft.tool.truncated_bytes']) > 0, concat('truncated:', SpanId), ''),
            if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.run.denied']) > 0,
               concat('denied_approval:', SpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.run.denied']))), '')
        ]) AS Problems
    FROM otel_traces
    WHERE SpanAttributes['weft.run.id'] != ''
)
WHERE length(Problems) > 0;
