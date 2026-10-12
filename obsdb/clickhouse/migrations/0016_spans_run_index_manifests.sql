-- 0016 · a run's spans by RunId, and the manifests a run's
-- ManifestHash resolves to (U27; one logical schema with obsdb/sqlite's
-- 0014).
--
-- otel_traces is ORDER BY (ServiceName, SpanName, Timestamp) and
-- RunSpans asked `RunId = ?`, which no index served: every granule of
-- the table was read. RunSpans now asks `TraceId = ? AND RunId = ?`
-- when the run row names its trace (idx_trace_id, 0001), and this
-- bloom filter serves RunId for the rest (a run row with no trace id,
-- and the trace-less fallback). MATERIALIZE INDEX builds it for the
-- parts written before it — a mutation ClickHouse runs in the
-- background (mutations_sync 0), so Open does not wait for it; until
-- it finishes those parts are read in full, as before.
ALTER TABLE otel_traces ADD INDEX IF NOT EXISTS idx_weft_run_id RunId TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE otel_traces MATERIALIZE INDEX idx_weft_run_id;

-- manifests (obsdb.ManifestStore): each agent's one-agent core.Manifest
-- document under its sha256, written once per hash and never deleted
-- (DeleteRun, DeleteSession, Sweep and Vacuum never name the table).
-- The first write wins: Ver is 2^63 - FirstNs, so ReplacingMergeTree's
-- highest version is the earliest write, and PutManifest skips a hash
-- already stored; a read takes the earliest row of a hash (argMin), so
-- an unmerged duplicate reads as the first.
CREATE TABLE IF NOT EXISTS manifests
(
    Hash       String,
    Body       String CODEC(ZSTD(1)),
    Source     LowCardinality(String),
    FirstNs    Int64,
    Ver        UInt64
)
ENGINE = ReplacingMergeTree(Ver)
ORDER BY Hash;
