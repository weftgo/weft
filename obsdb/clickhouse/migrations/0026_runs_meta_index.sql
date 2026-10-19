-- 0026 · a skip index on weft_runs.Meta for the tag filters (U12.2's
-- GET /api/sessions?tag.<k>=<v>, obsdb.SessionQuery.Meta, and
-- obsdb.RunQuery.Meta; P9-pre-go-fix2). No SQLite counterpart: SQLite
-- reads the tag through its runs_meta rows.
--
-- Meta is a run row's caller metadata as one JSON object string
-- (toJSONString of the attribute map, 0001's views: compact, no space
-- after the colon). The tag predicate — JSONHas / JSONExtractString —
-- is no expression a skip index answers, so the narrowing beside it
-- (clickhouse.metaNarrow) also asks `Meta LIKE '%"<k>":"<v>"%'` for
-- each pair whose key and value hold only characters toJSONString
-- writes verbatim: a superset of the exact match (the pair's JSON text
-- is a substring of every row that carries it), which this ngram bloom
-- filter answers — the granules whose Meta holds none of the pair's
-- 4-grams are skipped instead of every Meta row being parsed. A pair
-- with any other character narrows nothing more than before (the exact
-- predicate still decides). MATERIALIZE INDEX builds it for the parts
-- written before it, in the background (0016's rule): until it
-- finishes those parts are read in full, as before.
ALTER TABLE weft_runs ADD INDEX IF NOT EXISTS idx_meta Meta TYPE ngrambf_v1(4, 16384, 3, 0) GRANULARITY 1;

ALTER TABLE weft_runs MATERIALIZE INDEX idx_meta;
