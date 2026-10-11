package clickhouse

// SetPruneHook installs fn as the hook PruneCommands runs between each
// rule's select and its delete, and returns what removes it (tests).
func SetPruneHook(fn func(rule string)) (remove func()) {
	pruneHook = fn
	return func() { pruneHook = nil }
}
