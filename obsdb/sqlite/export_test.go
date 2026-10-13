package sqlite

// The schema version a fresh or upgraded file reaches: the one place a
// new migration bumps — every test that pins the version (the internal
// upgrade tests and sqlite_test's) reads it.
const (
	WantSchemaVersion = 17
	WantSchemaThrough = "through 0014 the run-list indexes and the manifests, 0015 the all-runs failed index, 0016 the problem tokens re-derived, 0017 the metadata cap backfilled"
)
