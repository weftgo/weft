-- 0001 — the initial thread sessions schema (ADR 0011 §5, plan §7).
-- A session is its header row plus entry rows carrying the same wire
-- lines jsonl writes — one line per row, in arrival order by seq — so
-- the two durable backends store identical bytes and differ only in
-- where they keep them. Timestamps are RFC 3339 with fixed nine-digit
-- nanoseconds, UTC, so lexicographic order is chronological order
-- (the store's schema rule, kept). session_locks is the one-writer
-- rule's row: taken on a session's first write, released on Delete,
-- and taken over from a holder whose process has died — flock's
-- death-release, rebuilt on the database the backend already needs.

CREATE TABLE sessions (
  id      TEXT PRIMARY KEY,
  created TEXT NOT NULL,              -- RFC 3339 nano, UTC; the headers-only list order
  header  TEXT NOT NULL               -- the header line JSON, verbatim (ADR 0011 §6)
);
CREATE INDEX sessions_created ON sessions(created DESC);
CREATE TABLE entries (
  session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  seq     INTEGER NOT NULL,           -- arrival order within the session
  line    TEXT NOT NULL,              -- one entry line, the wire JSON (ADR 0011 §2)
  torn    INTEGER NOT NULL DEFAULT 0, -- 1: bytes a crashed writer left without their newline
  PRIMARY KEY (session, seq)
);
CREATE TABLE session_locks (
  session TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  host    TEXT NOT NULL,              -- the holder's machine; liveness is per-host
  owner   TEXT NOT NULL,              -- host/instance: one Storage's writer identity
  pid     INTEGER NOT NULL,           -- the holder's process, for death detection
  taken   TEXT NOT NULL               -- RFC 3339, diagnostics
);
