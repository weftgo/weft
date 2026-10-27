package clickhouse

// SetPruneHook installs fn as the hook PruneCommands runs between each
// rule's select and its delete, and returns what removes it (tests).
func SetPruneHook(fn func(rule string)) (remove func()) {
	pruneHook = fn
	return func() { pruneHook = nil }
}

// The schema version a fresh or upgraded database reaches, and the
// obsdb_migrations rows it holds (one per embedded migration): the one
// place a new migration bumps — every test that pins them reads these.
const (
	WantSchemaVersion = 32
	WantSchemaThrough = "0001 init … 0013 the emitted counts, 0014 records.dropped out of meta, 0015 the problem counters, 0016 the RunId index and the manifests, 0017 the problem tokens re-derived, 0018 retry counts, tap panics and the metadata cap, 0019 the approval link (ResumedFrom), 0020 the ParentRunID index, 0021 weft.logs.bridged out of meta, 0022 the run cards' facts out of meta, 0023 pool-child identity (CallerRunID), 0024 weft.chat.role out of meta and a summarizer span out of the run row, 0025 the session_events index (weft_session_events), 0026 the Meta ngram index (idx_meta), then 0027 0024's traces view restated for a database that applied its first text, 0028 the experiment pass predicate, 0029 the onion (repaired, layer keys out of meta), 0030 weft.tool.origin out of meta, 0031 derived runs re-derived (DerivedMessages), 0032 a remote caller link (CallerRunID empty when weft.caller.remote, the key out of meta)"
)

// WantMigrationRows is one obsdb_migrations row per embedded migration.
var WantMigrationRows = len(migrations)
