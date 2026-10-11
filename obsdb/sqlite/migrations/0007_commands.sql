-- 0007 · the playground link's own records (plan I3, obsdb.CommandStore):
-- what Studio's runtime server asked the runtimes it links to do, so a
-- restarted Studio still knows. Four small mutable tables, upserted by
-- key; Studio's bookkeeping, not run data — DeleteRun, DeleteSession,
-- Sweep and Vacuum never touch them, PruneCommands is their retention.
--
-- commands: one row per command, the wire body and last ack verbatim
-- (JSON text), the columns the queries select by, and the times a
-- restart decides "lost" from (accepted_ns, beat_ns; 0 = never).
CREATE TABLE commands (
  id            TEXT PRIMARY KEY,
  runtime_id    TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL DEFAULT '',
  state         TEXT NOT NULL,
  run_id        TEXT NOT NULL DEFAULT '',
  public_id     TEXT NOT NULL DEFAULT '',
  fork          INTEGER NOT NULL DEFAULT 0,
  agent         TEXT NOT NULL DEFAULT '',
  source_run_id TEXT NOT NULL DEFAULT '',
  experiment_id TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT '',
  created_ns    INTEGER NOT NULL,
  updated_ns    INTEGER NOT NULL,
  accepted_ns   INTEGER NOT NULL DEFAULT 0,
  beat_ns       INTEGER NOT NULL DEFAULT 0,
  body          TEXT NOT NULL DEFAULT '',
  ack           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX commands_created ON commands(created_ns DESC, id DESC);
CREATE INDEX commands_runtime ON commands(runtime_id, created_ns DESC);
CREATE INDEX commands_run ON commands(run_id, created_ns DESC) WHERE run_id <> '';
CREATE INDEX commands_state ON commands(state, updated_ns);
-- run_routes: run id → the runtime an ack said started it (its own
-- table, not a view over commands.run_id: an ack may name a run its
-- command row does not hold, and a route outlives the command by its
-- own clock, seen_ns).
CREATE TABLE run_routes (
  run_id     TEXT PRIMARY KEY,
  runtime_id TEXT NOT NULL,
  command_id TEXT NOT NULL DEFAULT '',
  public_id  TEXT NOT NULL DEFAULT '',
  fork       INTEGER NOT NULL DEFAULT 0,
  seen_ns    INTEGER NOT NULL
);
CREATE INDEX run_routes_command ON run_routes(command_id);
-- breakpoints: each runtime's debugger tool set, replaced whole (JSON
-- array; '[]' is a stored clear, distinct from no row).
CREATE TABLE breakpoints (
  runtime_id TEXT PRIMARY KEY,
  tools      TEXT NOT NULL DEFAULT '[]',
  updated_ns INTEGER NOT NULL
);
-- runtimes: each runtime's latest registration (body verbatim).
CREATE TABLE runtimes (
  id            TEXT PRIMARY KEY,
  service       TEXT NOT NULL DEFAULT '',
  host          TEXT NOT NULL DEFAULT '',
  pid           INTEGER NOT NULL DEFAULT 0,
  cwd           TEXT NOT NULL DEFAULT '',
  agents        TEXT NOT NULL DEFAULT '[]',
  registered_ns INTEGER NOT NULL,
  last_seen_ns  INTEGER NOT NULL,
  body          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runtimes_last_seen ON runtimes(last_seen_ns DESC);
