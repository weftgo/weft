//go:build unix && !linux && !darwin

package sqlite

// procStart has no portable answer on this platform: it reports ""
// — unknown — and the lock falls back to what it can prove. This
// process's own earlier incarnations are still recognised (by the
// process token), and a dead pid is still dead; the one case left
// conservative is an unrelated live process wearing a dead holder's
// pid, which keeps the session ErrLocked until that process exits.
func procStart(int) string { return "" }
