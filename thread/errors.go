package thread

import "errors"

var (
	// ErrNotFound is returned by Load, Append, and Delete for a session
	// id the storage does not hold.
	ErrNotFound = errors.New("thread: session not found")

	// ErrLocked is returned by a backend that enforces the one-writer
	// rule (ADR 0011 §5) when a session is already held by another
	// writer — another process, or another Storage in this one.
	// Readers never lock: Load and List always work.
	ErrLocked = errors.New("thread: session is locked by another writer")

	// ErrCorrupt wraps the failures a backend reports for stored data
	// it cannot decode: a malformed line that is not a torn tail (a
	// torn final line is a crash, dropped and reported through Load's
	// LoadReport; data from a newer weft is ErrNewerFormat, never
	// skipped). Salvage, the jsonl open option, downgrades this to a
	// skip reported in the LoadReport.
	ErrCorrupt = errors.New("thread: stored session data is corrupt")

	// ErrNewerFormat wraps the decode failures UnmarshalEntry and
	// Header decoding report for data written by a newer weft than
	// this build: an entry kind this build does not know, an entry
	// carrying a "v" above the version this build reads of its kind,
	// or a session header whose envelope is ahead of FormatVersion
	// (ADR 0011 §5–§6). Loud over silent: a session must decode to
	// exactly what was written, and an older weft says so instead of
	// guessing. List, which reads headers only, still works — an older
	// reader can see the session and name why it cannot open it.
	ErrNewerFormat = errors.New("thread: session format newer than this build")
)
