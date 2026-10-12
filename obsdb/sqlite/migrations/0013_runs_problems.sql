-- 0013 · a failed run's kind and its problem counters, on the run row
-- (docs/record-contract.md §3, §6; U4), one logical schema with
-- obsdb/clickhouse's 0015.
--
-- error_type is the invoke_agent span's error.type as the row reads it
-- (obsdb.RunErrorType): the core's value, with context.Canceled and
-- context.DeadlineExceeded read as 'cancelled'. '' for a run that did
-- not fail and for a failure whose root carried none.
--
-- problems is the run's problem tokens (obsdb.SpanProblems), a JSON
-- array kept as a sorted set — "<kind>:<span id>" for a failed attempt
-- (retried), a chat span answered by another model than the one asked
-- (fallback), an execute_tool span that failed (tool_error; denied for
-- error.type DENIED; none for a context error) or whose result the
-- cap cut (truncated), and
-- "denied_approval:<n>" for an invoke_agent span's weft.run.denied.
-- Readers fold it into the counters (obsdb.CountProblems); a set, so a
-- span stored twice never counts twice.
--
-- Additive, with a backfill from the spans already stored — the same
-- rules, restated here, pinned against the Go ones by
-- TestMigration0013BackfillsProblems. The new span keys
-- (weft.tool.input_error, weft.panic.layer, weft.tool.truncated_bytes,
-- weft.run.denied) are contract (obsdb's nonMetaAttr) from their first
-- write, so no meta cleanup is due.
ALTER TABLE runs ADD COLUMN error_type TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN problems TEXT NOT NULL DEFAULT '[]';

UPDATE runs SET error_type = COALESCE((SELECT MAX(CASE json_extract(s.attrs, '$."error.type"')
      WHEN 'context.Canceled' THEN 'cancelled'
      WHEN 'context.DeadlineExceeded' THEN 'cancelled'
      ELSE json_extract(s.attrs, '$."error.type"') END)
    FROM spans s
    WHERE s.run_id = runs.run_id
      AND json_type(s.attrs, '$."error.type"') = 'text'
      AND CASE WHEN json_type(s.attrs, '$."gen_ai.operation.name"') = 'text'
               THEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'invoke_agent'
               ELSE substr(s.name, 1, 12) = 'invoke_agent' END), '')
WHERE derived = 0;

UPDATE runs SET problems = COALESCE((SELECT json_group_array(tok) FROM (SELECT DISTINCT tok FROM (
    SELECT CASE
        WHEN s.name = 'attempt' AND json_type(s.attrs, '$."gen_ai.operation.name"') IS NULL
             AND s.status_code = 2
          THEN 'retried:' || s.span_id
        WHEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'chat'
             AND json_type(s.attrs, '$."gen_ai.request.model"') = 'text'
             AND json_type(s.attrs, '$."gen_ai.response.model"') = 'text'
             AND json_extract(s.attrs, '$."gen_ai.request.model"') <> ''
             AND json_extract(s.attrs, '$."gen_ai.response.model"') <> ''
             AND json_extract(s.attrs, '$."gen_ai.request.model"') <> json_extract(s.attrs, '$."gen_ai.response.model"')
          THEN 'fallback:' || s.span_id
        WHEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'execute_tool' AND s.status_code = 2
             AND COALESCE(json_extract(s.attrs, '$."error.type"'), '') NOT IN ('context.Canceled', 'context.DeadlineExceeded')
          THEN CASE WHEN json_extract(s.attrs, '$."error.type"') = 'DENIED'
                    THEN 'denied:' ELSE 'tool_error:' END || s.span_id
        WHEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'invoke_agent'
             AND CAST(json_extract(s.attrs, '$."weft.run.denied"') AS INTEGER) > 0
          THEN 'denied_approval:' || CAST(json_extract(s.attrs, '$."weft.run.denied"') AS INTEGER)
      END AS tok
    FROM spans s WHERE s.run_id = runs.run_id
    UNION ALL
    SELECT 'truncated:' || s.span_id AS tok
    FROM spans s WHERE s.run_id = runs.run_id
      AND json_extract(s.attrs, '$."gen_ai.operation.name"') = 'execute_tool'
      AND CAST(json_extract(s.attrs, '$."weft.tool.truncated_bytes"') AS INTEGER) > 0
  ) WHERE tok IS NOT NULL ORDER BY tok)), '[]')
WHERE derived = 0;
