-- 0011 · root_ended for derived runs (docs/record-contract.md §6; the U2
-- review), one logical schema with obsdb/clickhouse's 0012. 0009's
-- backfill skipped derived rows while every derived write since sets
-- root_ended = 1, so a derived run's flag depended on when it was
-- written — harmless (its status comes from finished_ok/failed, and
-- every hole rule reads derived first), but one rule for every derived
-- run. (0012's other half, the version from the root span alone, is
-- the writer's: SQLite's view of it is write.go, not a stored view.)
UPDATE runs SET root_ended = 1 WHERE derived = 1 AND root_ended = 0;
