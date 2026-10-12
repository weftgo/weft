-- 0015 · a failed run's kind and its problem counters, on the run row
-- (docs/record-contract.md §3, §6; U4), one logical schema with
-- obsdb/sqlite's 0013.
--
-- ErrorType is the invoke_agent span's error.type as the row reads it
-- (obsdb.RunErrorType): the core's value, with context.Canceled and
-- context.DeadlineExceeded read as 'cancelled'; max over the run's rows,
-- as SQLite keeps the larger. Problems is the run's problem tokens
-- (obsdb.SpanProblems) — "<kind>:<span id>" for a failed attempt
-- (retried), a chat span answered by another model than the one asked
-- (fallback), an execute_tool span that failed (tool_error; denied for
-- error.type DENIED; none for a context error) or whose result the
-- cap cut (truncated), and
-- "denied_approval:<n>" for an invoke_agent span's weft.run.denied —
-- kept as a set by groupUniqArrayArray, so a retried batch that stores a
-- span twice never counts it twice; readers fold it into the counters
-- (obsdb.CountProblems).
--
-- Two restatements, pinned by TestMigration0015RestatesRunViews:
-- weft_runs_logs_mv is 0014's select (0011's with the tuple through
-- weft.records.dropped), weft_runs_traces_mv 0013's with an `op` alias
-- and the two columns. Both contract tuples become contractKeys whole —
-- the traces tuple gains 0014's weft.records.dropped too, which no span
-- carries — with the four span keys U4 adds (weft.run.denied,
-- weft.tool.truncated_bytes, weft.tool.input_error, weft.panic.layer):
-- every attribute is a string here, so a key left out would land in
-- Meta. MODIFY QUERY swaps each select in place (0003's reasoning).
--
-- Then a backfill from the spans already stored: one weft_runs row per
-- span carrying an error type or a problem, with the span's own times as
-- Started and LastSeen — the values the view inserted for it, so
-- min/max move nothing. Re-running it only adds rows max() and the set
-- fold away.
ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS ErrorType SimpleAggregateFunction(max, String);

ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS Problems SimpleAggregateFunction(groupUniqArrayArray, Array(String));

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
        'weft.raw.index',
        'weft.run.stopped_by', 'weft.cancel.cause',
        'weft.run.events', 'weft.run.messages',
        'weft.records.dropped',
        'weft.run.denied', 'weft.tool.truncated_bytes', 'weft.tool.input_error',
        'weft.panic.layer'], k), LogAttributes) AS metaMap
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
    if(LogAttributes['weft.record'] = 'request' AND LogAttributes['weft.request.index'] != '', toInt64OrZero(LogAttributes['weft.request.index']) + 1, 0) AS RequestCount,
    if(LogAttributes['weft.event.type'] = 'run_finish', JSONExtractString(Body, 'stopped_by'), '') AS StoppedBy
FROM otel_logs
WHERE LogAttributes['weft.run.id'] != '';

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
        'weft.panic.layer'], k), SpanAttributes) AS metaMap
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
        if(SpanName = 'attempt' AND op = '' AND isError, concat('retried:', SpanId), ''),
        if(op = 'chat' AND SpanAttributes['gen_ai.request.model'] != '' AND SpanAttributes['gen_ai.response.model'] != ''
           AND SpanAttributes['gen_ai.request.model'] != SpanAttributes['gen_ai.response.model'], concat('fallback:', SpanId), ''),
        if(op = 'execute_tool' AND isError AND SpanAttributes['error.type'] NOT IN ('context.Canceled', 'context.DeadlineExceeded'), concat(if(SpanAttributes['error.type'] = 'DENIED', 'denied:', 'tool_error:'), SpanId), ''),
        if(op = 'execute_tool' AND toInt64OrZero(SpanAttributes['weft.tool.truncated_bytes']) > 0, concat('truncated:', SpanId), ''),
        if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.run.denied']) > 0,
           concat('denied_approval:', toString(toInt64OrZero(SpanAttributes['weft.run.denied']))), '')
    ]) AS Problems
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != '';

INSERT INTO weft_runs (RunId, Started, LastSeen, ErrorType, Problems)
SELECT RunId, Started, LastSeen, ErrorType, Problems FROM (
    SELECT
        SpanAttributes['weft.run.id'] AS RunId,
        Timestamp AS Started,
        Timestamp + toIntervalNanosecond(toInt64(Duration)) AS LastSeen,
        SpanAttributes['gen_ai.operation.name'] AS op,
        StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS isError,
        if(if(mapContains(SpanAttributes, 'gen_ai.operation.name'),
              op = 'invoke_agent',
              startsWith(SpanName, 'invoke_agent')),
           multiIf(SpanAttributes['error.type'] IN ('context.Canceled', 'context.DeadlineExceeded'), 'cancelled', SpanAttributes['error.type']),
           '') AS ErrorType,
        arrayFilter(x -> x != '', [
            if(SpanName = 'attempt' AND op = '' AND isError, concat('retried:', SpanId), ''),
            if(op = 'chat' AND SpanAttributes['gen_ai.request.model'] != '' AND SpanAttributes['gen_ai.response.model'] != ''
               AND SpanAttributes['gen_ai.request.model'] != SpanAttributes['gen_ai.response.model'], concat('fallback:', SpanId), ''),
            if(op = 'execute_tool' AND isError AND SpanAttributes['error.type'] NOT IN ('context.Canceled', 'context.DeadlineExceeded'), concat(if(SpanAttributes['error.type'] = 'DENIED', 'denied:', 'tool_error:'), SpanId), ''),
            if(op = 'execute_tool' AND toInt64OrZero(SpanAttributes['weft.tool.truncated_bytes']) > 0, concat('truncated:', SpanId), ''),
            if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.run.denied']) > 0,
               concat('denied_approval:', toString(toInt64OrZero(SpanAttributes['weft.run.denied']))), '')
        ]) AS Problems
    FROM otel_traces
    WHERE SpanAttributes['weft.run.id'] != ''
)
WHERE ErrorType != '' OR length(Problems) > 0;
