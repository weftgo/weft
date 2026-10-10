-- 0004 · search (plan A7). Three tables the writer keeps beside runs;
-- additive, nothing existing changes shape, so no read of a run, its
-- records or its spans answers differently.
--
-- run_search holds each run's two content search fields as the writer
-- learned them from its messages records (transcript growth only, never
-- a compaction view): prompt, the turn's own (obsdb.PromptText and
-- TurnPrompt: the input record's last user message with text — or, on a
-- resume whose input record ends at an assistant message with tool
-- calls, the next record's user text before its first assistant
-- message — else another record's first user text), with prompt_idx its
-- rank, the lowest winning: an input record's candidate ranks at its
-- index − 2^40, below every other record's (ranked at its index), and is
-- re-resolved from the stored pair whenever it or its next record
-- arrives, so the order the two arrive in does not matter; and answer, the text of the last
-- assistant message with text (answer_idx likewise, the highest index
-- wins). A run indexed for its error or tool names alone (content-off:
-- no messages are stored) has a row with both empty. Its id is the
-- run's runs_fts rowid, so a reindex replaces one row by key. The run
-- row's prompt_preview is substr(prompt, 1, 200).
--
-- run_tools is every distinct tool name a run's tool_start events
-- named (content-off chains keep the name): the tool= filter's exact
-- match, and the tools search field.
--
-- runs_fts is the search index: one row per run that has anything to
-- index — its prompt, answer, error (runs.err) and tool names (one per
-- line, so no phrase spans two names) — under FTS5's trigram tokenizer:
-- case-insensitive substring matching, the same semantics the
-- ClickHouse backend's positionCaseInsensitiveUTF8 scans give. The
-- writer replaces a run's row whenever one of the four changes.
--
-- Runs written before this migration are indexed by the Go step that
-- runs with it in the same transaction (backfillSearch, sqlite/search.go):
-- the same derivation over the records already stored.
CREATE TABLE run_search (
  id INTEGER PRIMARY KEY,                  -- the run's runs_fts rowid
  run_id TEXT NOT NULL UNIQUE,
  prompt TEXT NOT NULL DEFAULT '', prompt_idx INTEGER NOT NULL DEFAULT -1,
  answer TEXT NOT NULL DEFAULT '', answer_idx INTEGER NOT NULL DEFAULT -1
);

CREATE TABLE run_tools (
  run_id TEXT NOT NULL, name TEXT NOT NULL,
  PRIMARY KEY (run_id, name)
) WITHOUT ROWID;
CREATE INDEX run_tools_name ON run_tools(name);

CREATE VIRTUAL TABLE runs_fts USING fts5(  -- rowid = run_search.id
  prompt, answer, err, tools,
  tokenize = 'trigram'
);
