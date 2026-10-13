-- 0017 · the metadata cap backfilled (docs/record-contract.md §6;
-- U32.1), one logical schema with obsdb/clickhouse's 0018.
--
-- obsdb.SpanProblems derives two more tokens from a run's invoke_agent
-- span — tap_panics:<span>:<n> (weft.tap.panics, new with this version:
-- nothing stored carries it) and metadata_dropped:<span>:<n>
-- (weft.metadata.dropped, written since ADR 0024 S1.1: the run
-- document's truncated hole, cause metadata_cap). The stored spans that
-- carry the second add their token to runs.problems — the set as the
-- writer keeps it, sorted, each token once — pinned against the Go rule
-- by TestMigration0017BackfillsMetadataDropped. The three new span
-- attributes are integers, which obsdb.MetaOf never keeps on SQLite, so
-- no meta cleanup is due.
UPDATE runs SET problems = (SELECT json_group_array(tok) FROM (
    SELECT value AS tok FROM json_each(CASE WHEN json_valid(runs.problems) THEN runs.problems ELSE '[]' END)
    UNION
    SELECT 'metadata_dropped:' || s.span_id || ':' || CAST(json_extract(s.attrs, '$."weft.metadata.dropped"') AS INTEGER) AS tok
    FROM spans s WHERE s.run_id = runs.run_id
      AND json_extract(s.attrs, '$."gen_ai.operation.name"') = 'invoke_agent'
      AND CAST(json_extract(s.attrs, '$."weft.metadata.dropped"') AS INTEGER) > 0
    ORDER BY tok))
WHERE derived = 0 AND EXISTS (SELECT 1 FROM spans s WHERE s.run_id = runs.run_id
      AND json_extract(s.attrs, '$."gen_ai.operation.name"') = 'invoke_agent'
      AND CAST(json_extract(s.attrs, '$."weft.metadata.dropped"') AS INTEGER) > 0);
