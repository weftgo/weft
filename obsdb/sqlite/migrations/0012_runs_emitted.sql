-- 0012 · what the core emitted, on the run row (docs/record-contract.md
-- §3, §6, §7; U5), one logical schema with obsdb/clickhouse's 0013.
-- events_emitted / messages_emitted are the invoke_agent span's
-- weft.run.events / weft.run.messages: the durable event records and
-- growth messages records the run handed to its LoggerProvider. A reader
-- compares them with event_count / message_count — fewer stored is a gap
-- ("N of M events were lost"), 0 emitted with nothing stored is
-- no_logger. 0 when the root carried neither (not stored yet, or a weft
-- before U5). Additive, with a backfill from the root spans already
-- stored (the larger value, as the writer merges), and the two keys
-- taken out of meta in case obsdb.MetaOf's catch-all ever put them
-- there (they are integers; it keeps strings, so normally none is).
ALTER TABLE runs ADD COLUMN events_emitted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN messages_emitted INTEGER NOT NULL DEFAULT 0;

UPDATE runs SET
  events_emitted = COALESCE((SELECT MAX(json_extract(s.attrs, '$."weft.run.events"')) FROM spans s
    WHERE s.run_id = runs.run_id AND json_type(s.attrs, '$."weft.run.events"') = 'integer'), 0),
  messages_emitted = COALESCE((SELECT MAX(json_extract(s.attrs, '$."weft.run.messages"')) FROM spans s
    WHERE s.run_id = runs.run_id AND json_type(s.attrs, '$."weft.run.messages"') = 'integer'), 0)
WHERE derived = 0;

UPDATE runs SET meta = json_remove(meta, '$."weft.run.events"', '$."weft.run.messages"')
WHERE json_valid(meta) AND (json_type(meta, '$."weft.run.events"') IS NOT NULL
    OR json_type(meta, '$."weft.run.messages"') IS NOT NULL);
