-- 0008 · the playground link's own records (plan I3, obsdb.CommandStore):
-- what Studio's runtime server asked the runtimes it links to do, so a
-- restarted Studio still knows. Four small mutable tables, the house
-- shape for a mutable row (experiments, 0002): ReplacingMergeTree
-- versioned by InsertTime, ordered by the row's key, an upsert an
-- insert and every read FINAL — the newest write wins. Times are
-- integer nanoseconds (0 = never), so nothing floors them. Studio's
-- bookkeeping, not run data: no TTL, DeleteRun, DeleteSession, Sweep
-- and Vacuum never touch them; PruneCommands is their retention.
CREATE TABLE IF NOT EXISTS commands
(
    Id           String,
    RuntimeId    String,
    Kind         String,
    State        LowCardinality(String),
    RunId        String,
    PublicId     String,
    Fork         UInt8,
    Agent        String,
    SourceRunId  String,
    ExperimentId String,
    Status       String,
    Error        String,
    CreatedNs    Int64,
    UpdatedNs    Int64,
    AcceptedNs   Int64,
    BeatNs       Int64,
    Body         String,
    Ack          String,
    InsertTime   DateTime64(9, 'UTC') DEFAULT now64(9),
    INDEX idx_runtime RuntimeId TYPE bloom_filter GRANULARITY 4,
    INDEX idx_run RunId TYPE bloom_filter GRANULARITY 4
)
ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY Id;
CREATE TABLE IF NOT EXISTS run_routes
(
    RunId      String,
    RuntimeId  String,
    CommandId  String,
    PublicId   String,
    Fork       UInt8,
    SeenNs     Int64,
    InsertTime DateTime64(9, 'UTC') DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY RunId;
CREATE TABLE IF NOT EXISTS breakpoints
(
    RuntimeId  String,
    Tools      String,
    UpdatedNs  Int64,
    InsertTime DateTime64(9, 'UTC') DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY RuntimeId;
CREATE TABLE IF NOT EXISTS runtimes
(
    Id           String,
    Service      String,
    Host         String,
    Pid          Int64,
    Cwd          String,
    Agents       String,
    RegisteredNs Int64,
    LastSeenNs   Int64,
    Body         String,
    InsertTime   DateTime64(9, 'UTC') DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(InsertTime)
ORDER BY Id;
