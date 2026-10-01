package thread

// CheckBusyInvariant reports a violation of the runner slot's
// invariant (busyInvariantLocked) — the race tests' probe: with a
// runner alive, an item is in flight or the runner is between items,
// never both and never neither.
func (s *Session) CheckBusyInvariant() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busyInvariantLocked()
}
