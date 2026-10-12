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
	WantSchemaVersion = 16
	WantSchemaThrough = "0001 init … 0013 the emitted counts, 0014 records.dropped out of meta, 0015 the problem counters, then 0016 the RunId index and the manifests"
)

// WantMigrationRows is one obsdb_migrations row per embedded migration.
var WantMigrationRows = len(migrations)
