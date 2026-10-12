-- 0014 · indexes for the run lists, and the manifests a run's
-- manifest_hash resolves to (U27; one logical schema with
-- obsdb/clickhouse's 0016).
--
-- runs_public and runs_session (0001) took the equality alone, so a
-- page ordered by start (RunQuery.PublicID / SessionID, the panel's
-- and the session's lists) was planned on runs_started — every
-- top-level run walked. runs_public is rebuilt to carry the list's
-- whole key (public id, then the parent equality, then the order),
-- runs_session_started is the same for a session (runs_session stays:
-- the session detail orders by turn), and runWhere states each partial
-- index's own condition (public_id <> '', session_id <> '') beside the
-- equality, which sqlite needs before it uses a partial index.
-- runs_started gains run_id, the order's tie-break: the top-level page
-- sorted it in a temp B-tree. runs_experiment (0006) gains it too, or
-- the planner would now prefer runs_started's whole order over the
-- experiment's equality.
DROP INDEX IF EXISTS runs_public;
CREATE INDEX IF NOT EXISTS runs_public ON runs(public_id, parent_run_id, started_ns DESC, run_id DESC) WHERE public_id <> '';
CREATE INDEX IF NOT EXISTS runs_session_started ON runs(session_id, parent_run_id, started_ns DESC, run_id DESC) WHERE session_id <> '';
DROP INDEX IF EXISTS runs_started;
CREATE INDEX IF NOT EXISTS runs_started ON runs(parent_run_id, started_ns DESC, run_id DESC);
DROP INDEX IF EXISTS runs_experiment;
CREATE INDEX IF NOT EXISTS runs_experiment ON runs(experiment_id, parent_run_id, started_ns DESC, run_id DESC) WHERE experiment_id <> '';

-- runs_all serves the all-runs list (RunQuery.ParentRunID "*", the
-- API's all=1): ORDER BY started_ns DESC, run_id DESC with no
-- parent_run_id equality, which runs_started cannot order.
-- runs_last_seen serves RunQuery.LastSeenAfter (the live stream's
-- backfill window) and the running status filter, which runWhere
-- states as last_seen_ns >= cutoff beside statusExpr's CASE (the CASE
-- itself, with its bind parameter, no index can serve). runs_failed is
-- the failed filter's: a partial index runWhere's `failed = 1` beside
-- the CASE matches. succeeded and interrupted are most rows: no index
-- narrows them, the list walks its order index and tests the CASE.
CREATE INDEX IF NOT EXISTS runs_all ON runs(started_ns DESC, run_id DESC);
CREATE INDEX IF NOT EXISTS runs_last_seen ON runs(last_seen_ns);
CREATE INDEX IF NOT EXISTS runs_failed ON runs(parent_run_id, started_ns DESC, run_id DESC) WHERE failed = 1;

-- manifests: each agent's one-agent core.Manifest document by its
-- sha256 (the weft.manifest.hash its runs carry, docs/record-contract.md
-- §2), so a run's manifest_hash resolves to the configuration it ran
-- with (obsdb.ManifestStore). Written by Studio from a weft.json it
-- serves (source 'file') and from runtime registrations ('runtime').
-- Content-addressed and immutable: a second write of a hash keeps the
-- first row (first_ns, source). Studio's bookkeeping, not run data:
-- DeleteRun, DeleteSession, Sweep and Vacuum never touch it — kept
-- forever, one small row per distinct agent configuration.
CREATE TABLE IF NOT EXISTS manifests (
  hash     TEXT PRIMARY KEY,
  body     TEXT NOT NULL,
  source   TEXT NOT NULL DEFAULT '',
  first_ns INTEGER NOT NULL
);
