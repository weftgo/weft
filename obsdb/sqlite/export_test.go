package sqlite

// The schema version a fresh or upgraded file reaches: the one place a
// new migration bumps — every test that pins the version (the internal
// upgrade tests and sqlite_test's) reads it.
const (
	WantSchemaVersion = 22
	WantSchemaThrough = "through 0014 the run-list indexes and the manifests, 0015 the all-runs failed index, 0016 the problem tokens re-derived, 0017 the metadata cap backfilled, 0018 the approval link (resumed_from), 0019 the traces window index (spans_start), 0020 a trace in start order (spans_trace_start), 0021 pool-child identity (caller_run_id, the session-parent index), 0022 the session_events index"
)
