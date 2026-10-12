-- 0016 · the problem tokens re-derived (docs/record-contract.md §6;
-- U4-fix), one logical schema with obsdb/clickhouse's 0017. (0015 is
-- U27-fix's; the loader applies the versions in order, gaps allowed.)
--
-- U4's rules (0013) counted every failed attempt as retried, keyed
-- fallbacks on a model-name comparison, counted RETRY (core.ModelRetry,
-- the intended repair loop) as a tool error and keyed weft.run.denied
-- by its count. runs.problems is re-derived whole from the stored spans
-- with obsdb.SpanProblems' new rules — the writer's from this version
-- on — pinned against the Go ones by TestMigration0016RederivesProblems:
--
--   attempt:<span>:<chat span>:<index>:<1|0>  every attempt span, so the
--       read fold counts a failed one only below its call's highest
--       index (a later attempt followed it);
--   fallback_answer:<span>  an attempt that succeeded carrying
--       weft.attempt.layer 'fallback' (mw.Fallback's backup report);
--   tool_failed:<span>  an execute_tool error, except DENIED (denied:),
--       RETRY and the context errors;
--   truncated:<span>  unchanged;
--   denied_approval:<span>:<n>  the invoke_agent span's weft.run.denied.
--
-- weft.attempt.layer is contract (obsdb's nonMetaAttr) from its first
-- write, so no meta cleanup is due. Derived runs keep '[]' (0013's rule).
UPDATE runs SET problems = COALESCE((SELECT json_group_array(tok) FROM (SELECT DISTINCT tok FROM (
    SELECT CASE
        WHEN s.name = 'attempt' AND json_type(s.attrs, '$."gen_ai.operation.name"') IS NULL
          THEN 'attempt:' || s.span_id || ':' || COALESCE(s.parent_span_id, '') || ':'
               || COALESCE(CAST(json_extract(s.attrs, '$."weft.attempt.index"') AS INTEGER), 0) || ':'
               || CASE WHEN s.status_code = 2 THEN '1' ELSE '0' END
        WHEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'execute_tool' AND s.status_code = 2
             AND COALESCE(json_extract(s.attrs, '$."error.type"'), '') NOT IN ('RETRY', 'context.Canceled', 'context.DeadlineExceeded')
          THEN CASE WHEN json_extract(s.attrs, '$."error.type"') = 'DENIED'
                    THEN 'denied:' ELSE 'tool_failed:' END || s.span_id
        WHEN json_extract(s.attrs, '$."gen_ai.operation.name"') = 'invoke_agent'
             AND CAST(json_extract(s.attrs, '$."weft.run.denied"') AS INTEGER) > 0
          THEN 'denied_approval:' || s.span_id || ':' || CAST(json_extract(s.attrs, '$."weft.run.denied"') AS INTEGER)
      END AS tok
    FROM spans s WHERE s.run_id = runs.run_id
    UNION ALL
    SELECT 'fallback_answer:' || s.span_id AS tok
    FROM spans s WHERE s.run_id = runs.run_id
      AND s.name = 'attempt' AND json_type(s.attrs, '$."gen_ai.operation.name"') IS NULL
      AND s.status_code <> 2
      AND json_extract(s.attrs, '$."weft.attempt.layer"') = 'fallback'
    UNION ALL
    SELECT 'truncated:' || s.span_id AS tok
    FROM spans s WHERE s.run_id = runs.run_id
      AND json_extract(s.attrs, '$."gen_ai.operation.name"') = 'execute_tool'
      AND CAST(json_extract(s.attrs, '$."weft.tool.truncated_bytes"') AS INTEGER) > 0
  ) WHERE tok IS NOT NULL ORDER BY tok)), '[]')
WHERE derived = 0;
