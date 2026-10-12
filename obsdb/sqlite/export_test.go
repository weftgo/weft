package sqlite

// The schema version a fresh or upgraded file reaches: the one place a
// new migration bumps — every test that pins the version (the internal
// upgrade tests and sqlite_test's) reads it.
const (
	WantSchemaVersion = 14
	WantSchemaThrough = "through 0014 the run-list indexes and the manifests"
)
