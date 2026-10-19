-- 0025 · the session_events index (docs/record-contract.md §6; U12.2,
-- UX-B-01, UX-B-02, D6, A3), one logical schema with obsdb/sqlite's 0022.
--
-- A session's state log — thread's records (weft.record thread, event
-- weft.thread.<kind>) and its compaction markers (weft.record
-- compaction) — sits in otel_logs, ordered by time and service, so a
-- session's timeline would scan every log row of its window.
-- weft_session_events holds each such record that carries a
-- weft.session.id once more, ordered by the session first: the view
-- below copies it at insert, a thread record filed under no run (held at
-- Close: weft.run.id '') included. BodyHash (cityHash64 of the body) is
-- the record's identity in the key — a thread body names its entry,
-- kind and time, a marker its compaction hash — so a retried batch's
-- duplicate collapses (ReplacingMergeTree; obsdb.SessionEventReader
-- reads FINAL). The content TTL (otel_logs', 30 days; clickhouse.TTL
-- rewrites it with the others). No traces or runs view is restated.
--
-- Backfill: every stored row of the two kinds carrying a session id; a
-- row the view copies again while the backfill runs collapses with it.
-- DeleteRun / Sweep delete a run's rows by RunId; Sweep then the runless
-- rows (and their otel_logs records) of a session no run is left of,
-- older than its cutoff; DeleteSession every row of the session. The
-- backfill is linear in otel_logs: a large database's first open at 0025
-- takes that long.
CREATE TABLE IF NOT EXISTS weft_session_events (
    SessionId String,
    Time DateTime64(9),
    RunId String,
    Kind LowCardinality(String),
    BodyHash UInt64,
    EventName LowCardinality(String),
    PublicId String,
    Content LowCardinality(String),
    Body String CODEC(ZSTD(1)),
    InsertTime DateTime DEFAULT now()
) ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY (SessionId, Time, RunId, Kind, BodyHash)
TTL Time + toIntervalSecond(2592000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW IF NOT EXISTS weft_session_events_mv TO weft_session_events AS
SELECT
    LogAttributes['weft.session.id'] AS SessionId,
    Timestamp AS Time,
    LogAttributes['weft.run.id'] AS RunId,
    LogAttributes['weft.record'] AS Kind,
    cityHash64(Body) AS BodyHash,
    EventName AS EventName,
    LogAttributes['weft.public_id'] AS PublicId,
    LogAttributes['weft.content'] AS Content,
    Body AS Body
FROM otel_logs
WHERE LogAttributes['weft.record'] IN ('thread', 'compaction') AND LogAttributes['weft.session.id'] != '';

INSERT INTO weft_session_events (SessionId, Time, RunId, Kind, BodyHash, EventName, PublicId, Content, Body)
SELECT
    LogAttributes['weft.session.id'],
    Timestamp,
    LogAttributes['weft.run.id'],
    LogAttributes['weft.record'],
    cityHash64(Body),
    EventName,
    LogAttributes['weft.public_id'],
    LogAttributes['weft.content'],
    Body
FROM otel_logs
WHERE LogAttributes['weft.record'] IN ('thread', 'compaction') AND LogAttributes['weft.session.id'] != '';
