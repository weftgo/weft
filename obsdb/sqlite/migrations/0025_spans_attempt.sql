-- 0025 · spans_attempt for a file that applied 0023 before it carried
-- the index (ux-plan between U24.1 and U24.1-fix): span_stats'
-- derivation names it (INDEXED BY), so a file without it could not
-- store a span. A no-op everywhere else — 0023 creates it.
CREATE INDEX IF NOT EXISTS spans_attempt ON spans(trace_id, parent_span_id) WHERE name = 'attempt';
