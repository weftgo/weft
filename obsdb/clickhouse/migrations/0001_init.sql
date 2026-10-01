-- 0001 — the ClickHouse dialect of the obsdb schema (ADR 0024, S3.6),
-- one logical schema with obsdb/sqlite's 0001. The dedup key is the
-- transport-level idempotency (I4): (run, kind, pos) for records,
-- (trace, span) for spans — ReplacingMergeTree collapses a retried
-- batch's duplicates at merge time, exact reads use FINAL.
--
-- otel_traces and otel_logs are column-compatible with the OTel
-- Collector ClickHouse exporter pinned at v0.162.0 (2026-09-29): we
-- keep every column name and type its INSERT names — traces_insert.sql
-- and logs_insert.sql at that tag, including the optional EventName
-- column it detects via DESC TABLE — and add the weft columns beside
-- them. Not DDL-verbatim: the exporter's README tells production users
-- to manage their own schema (create_schema: false); we own the
-- engines, the TTLs and the materialized weft columns, and a stock
-- collector pinned to v0.162.0 can write into these tables (the
-- collector-shape test inserts with its exact column lists). The
-- exporter's optional *AttributesKeys columns are not created
-- because its default INSERT never names them — they exist only in
-- its json-mode table variants (logs_json_table.sql and friends) —
-- so their absence here is safe.
--
-- TTLs are the S3.6 defaults: content (otel_logs, weft_records,
-- weft_deltas) 30 days, spans and runs 90 days; clickhouse.TTL(...)
-- overrides them per Open with ALTER TABLE ... MODIFY TTL.

CREATE TABLE IF NOT EXISTS otel_traces (
    Timestamp DateTime64(9) CODEC(Delta, ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    SpanId String CODEC(ZSTD(1)),
    ParentSpanId String CODEC(ZSTD(1)),
    TraceState String CODEC(ZSTD(1)),
    SpanName LowCardinality(String) CODEC(ZSTD(1)),
    SpanKind LowCardinality(String) CODEC(ZSTD(1)),
    ServiceName LowCardinality(String) CODEC(ZSTD(1)),
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    ScopeName String CODEC(ZSTD(1)),
    ScopeVersion String CODEC(ZSTD(1)),
    SpanAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    Duration UInt64 CODEC(ZSTD(1)),
    StatusCode LowCardinality(String) CODEC(ZSTD(1)),
    StatusMessage String CODEC(ZSTD(1)),
    Events Nested (
        Timestamp DateTime64(9),
        Name LowCardinality(String),
        Attributes Map(LowCardinality(String), String)
    ) CODEC(ZSTD(1)),
    Links Nested (
        TraceId String,
        SpanId String,
        TraceState String,
        Attributes Map(LowCardinality(String), String)
    ) CODEC(ZSTD(1)),
    -- weft additions. WeftAttrs/WeftEvents/WeftResource carry the typed
    -- (JSON) attribute maps for exact round-trips: the collector's map
    -- columns stringify every value, which loses the int64 the record
    -- contract pins. They default to '' so a stock collector's INSERT
    -- (which never names them) still lands; reads fall back to the map
    -- columns for such rows. The MATERIALIZED columns are the §4 sketch:
    -- identity extracted from SpanAttributes; the collector's own tables
    -- materialize k8s fields the same way.
    WeftAttrs String DEFAULT '' CODEC(ZSTD(1)),
    WeftEvents String DEFAULT '' CODEC(ZSTD(1)),
    WeftResource String DEFAULT '' CODEC(ZSTD(1)),
    RunId LowCardinality(String) MATERIALIZED SpanAttributes['weft.run.id'],
    SessionId String MATERIALIZED SpanAttributes['weft.session.id'],
    PublicId String MATERIALIZED SpanAttributes['weft.public_id'],
    Agent String MATERIALIZED SpanAttributes['gen_ai.agent.name'],
    ManifestHash String MATERIALIZED SpanAttributes['weft.manifest.hash'],
    PromptId String MATERIALIZED SpanAttributes['weft.prompt.id'],
    PromptVersion String MATERIALIZED SpanAttributes['weft.prompt.version'],
    -- S3.6 skip indexes: bloom_filter on SessionId, PublicId, Agent,
    -- TraceId (the collector default's own index, kept verbatim).
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_weft_session_id SessionId TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_weft_public_id PublicId TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_weft_agent Agent TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_span_attr_key mapKeys(SpanAttributes) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, SpanName, toDateTime(Timestamp))
TTL Timestamp + toIntervalSecond(7776000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel_logs (
    Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    SpanId String CODEC(ZSTD(1)),
    TraceFlags UInt8,
    SeverityText LowCardinality(String) CODEC(ZSTD(1)),
    SeverityNumber UInt8,
    ServiceName LowCardinality(String) CODEC(ZSTD(1)),
    Body String CODEC(ZSTD(1)),
    ResourceSchemaUrl LowCardinality(String) CODEC(ZSTD(1)),
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    ScopeSchemaUrl LowCardinality(String) CODEC(ZSTD(1)),
    ScopeName String CODEC(ZSTD(1)),
    ScopeVersion LowCardinality(String) CODEC(ZSTD(1)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    LogAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    EventName String CODEC(ZSTD(1)),
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)
TTL Timestamp + toIntervalSecond(2592000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS weft_records (
    RunId String,
    Kind LowCardinality(String),
    Pos Int64,
    Time DateTime64(9),
    InsertTime DateTime DEFAULT now(),
    TraceId String,
    SpanId String,
    EventType LowCardinality(String),
    SessionId String,
    PublicId String,
    Agent String,
    Turn Int32,
    Body String CODEC(ZSTD(1))
) ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY (RunId, Kind, Pos)
TTL Time + toIntervalSecond(2592000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS weft_deltas (
    RunId String,
    Pos Int64,
    Time DateTime64(9),
    InsertTime DateTime DEFAULT now(),
    TraceId String,
    SpanId String,
    EventType LowCardinality(String),
    Body String CODEC(ZSTD(1))
) ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY (RunId, Pos)
TTL Time + toIntervalSecond(2592000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS weft_runs (
    RunId String,
    ParentRunID SimpleAggregateFunction(max, String),
    ParentCallID SimpleAggregateFunction(max, String),
    TraceID SimpleAggregateFunction(max, String),
    Agent SimpleAggregateFunction(max, String),
    Provider SimpleAggregateFunction(max, String),
    Model SimpleAggregateFunction(max, String),
    ManifestHash SimpleAggregateFunction(max, String),
    WeftVersion SimpleAggregateFunction(max, String),
    Service SimpleAggregateFunction(max, String),
    SessionID SimpleAggregateFunction(max, String),
    PublicID SimpleAggregateFunction(max, String),
    Turn SimpleAggregateFunction(max, Int32),
    Playground SimpleAggregateFunction(max, UInt8),
    ExperimentID SimpleAggregateFunction(max, String),
    ForkedFrom SimpleAggregateFunction(max, String),
    Meta SimpleAggregateFunction(max, String),
    Started SimpleAggregateFunction(min, DateTime64(9)),
    Finished SimpleAggregateFunction(max, Nullable(DateTime64(9))),
    LastSeen SimpleAggregateFunction(max, DateTime64(9)),
    FinishedOK SimpleAggregateFunction(max, UInt8),
    Failed SimpleAggregateFunction(max, UInt8),
    Err SimpleAggregateFunction(max, String),
    Steps SimpleAggregateFunction(max, Int32),
    Pending SimpleAggregateFunction(max, Int32),
    StopReason SimpleAggregateFunction(max, String),
    InputTokens SimpleAggregateFunction(max, Int64),
    OutputTokens SimpleAggregateFunction(max, Int64),
    CachedInputTokens SimpleAggregateFunction(max, Int64),
    CacheWriteTokens SimpleAggregateFunction(max, Int64),
    ReasoningTokens SimpleAggregateFunction(max, Int64),
    DeltaCount SimpleAggregateFunction(max, Int64)
) ENGINE = AggregatingMergeTree
ORDER BY RunId
TTL LastSeen + toIntervalSecond(7776000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- weft_records from otel_logs: only the durable kinds — never 'delta'
-- (D3: counted, never stored; the weft_runs view counts them) and never
-- 'heartbeat' (no position; it only moves weft_runs' last-seen). The
-- identity columns ride along for §4's sketch; sessions group weft_runs.
CREATE MATERIALIZED VIEW IF NOT EXISTS weft_records_mv TO weft_records AS
SELECT
    LogAttributes['weft.run.id'] AS RunId,
    LogAttributes['weft.record'] AS Kind,
    if(LogAttributes['weft.event.pos'] != '', toInt64OrZero(LogAttributes['weft.event.pos']),
      if(LogAttributes['weft.messages.index'] != '', toInt64OrZero(LogAttributes['weft.messages.index']), -1)) AS Pos,
    Timestamp AS Time,
    TraceId AS TraceId,
    SpanId AS SpanId,
    LogAttributes['weft.event.type'] AS EventType,
    LogAttributes['weft.session.id'] AS SessionId,
    LogAttributes['weft.public_id'] AS PublicId,
    LogAttributes['gen_ai.agent.name'] AS Agent,
    toInt32OrZero(LogAttributes['weft.turn']) AS Turn,
    Body AS Body
FROM otel_logs
WHERE LogAttributes['weft.record'] IN ('event', 'messages')
  AND LogAttributes['weft.run.id'] != '';

-- weft_runs from otel_logs: every weft record moves started/last-seen
-- (min/max), heartbeats and deltas included; run_start contributes
-- provider/model from the body, run_finish the terminal fields. Usage
-- numbers are max: only one non-zero value exists per run and a retried
-- batch carries identical bytes. Meta is the caller metadata as JSON —
-- the attribute map filtered of the record contract's keys (obsdb's
-- MetaOf exclusion set; TestMigrationContractTuple pins this tuple to
-- it), so max over a run's rows keeps the metadata instead of picking
-- whichever record's full attribute JSON sorts last. The read side
-- applies obsdb.MetaOf again — the values are all strings, so it is an
-- identity there, and MetaOf stays the one implementation. DeltaCount
-- is the high-water mark (max delta pos + 1), retry-proof the same way.
CREATE MATERIALIZED VIEW IF NOT EXISTS weft_runs_logs_mv TO weft_runs AS
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
        'session.id', 'user.id', 'error.type'], k), LogAttributes) AS metaMap
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
    if(LogAttributes['weft.record'] = 'delta', toInt64OrZero(LogAttributes['weft.delta.pos']) + 1, 0) AS DeltaCount
FROM otel_logs
WHERE LogAttributes['weft.run.id'] != '';

-- weft_runs from otel_traces: spans move last-seen to their end
-- (Timestamp + Duration); the invoke_agent span — detected exactly the
-- way obsdb/sqlite's isInvokeAgent does, by gen_ai.operation.name when
-- present and by name prefix otherwise — additionally sets trace,
-- finish, usage, steps, stop reason and, on error, failed and err.
CREATE MATERIALIZED VIEW IF NOT EXISTS weft_runs_traces_mv TO weft_runs AS
WITH if(mapContains(SpanAttributes, 'gen_ai.operation.name'),
       SpanAttributes['gen_ai.operation.name'] = 'invoke_agent',
       startsWith(SpanName, 'invoke_agent')) AS isInvoke,
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
        'session.id', 'user.id', 'error.type'], k), SpanAttributes) AS metaMap
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
    toUInt8(isInvoke AND StatusCode = 'STATUS_CODE_ERROR') AS Failed,
    if(isInvoke AND StatusCode = 'STATUS_CODE_ERROR', StatusMessage, '') AS Err,
    if(isInvoke, toInt32OrZero(SpanAttributes['weft.run.steps']), 0) AS Steps,
    if(isInvoke, toInt32OrZero(SpanAttributes['weft.run.pending']), 0) AS Pending,
    if(isInvoke, SpanAttributes['weft.run.stop_reason'], '') AS StopReason,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.input_tokens']), 0) AS InputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.output_tokens']), 0) AS OutputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.cache_read.input_tokens']), 0) AS CachedInputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.cache_creation.input_tokens']), 0) AS CacheWriteTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.reasoning.output_tokens']), 0) AS ReasoningTokens,
    toInt64(0) AS DeltaCount
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != '';
