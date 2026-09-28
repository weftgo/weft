package thread

import (
	"errors"
	"fmt"
)

var (
	// ErrNotImplemented is returned by the pieces of the v0.1 design
	// whose step has not landed yet — SummarizeLeft until step 1.8
	// writes branch summaries (ADR 0020 §6). It says "not in this
	// build", never "not in the design": the option exists so callers
	// code against the final shape and get a loud, matchable error
	// instead of a silent no-op.
	ErrNotImplemented = errors.New("thread: feature not implemented in this build")

	// ErrBusy is returned by Send under the Reject busy policy when the
	// session is already running a turn: the follow-up was not
	// accepted and nothing was written (ADR 0011 §4).
	ErrBusy = errors.New("thread: session is busy with another turn")

	// ErrNotFound is returned by Load, Append, and Delete for a session
	// id the storage does not hold.
	ErrNotFound = errors.New("thread: session not found")

	// ErrExists is returned by Create for a session id the storage
	// already holds — in this process, or in any other sharing the
	// backend. A session is never silently replaced (ADR 0011 §5).
	ErrExists = errors.New("thread: session already exists")

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
	// reader sees a session whose entries are newer and Load names why
	// it cannot open it. The one thing List cannot show is a session
	// whose header itself is newer: the envelope gates the whole file,
	// so such a session is invisible to an older List — visible again
	// as soon as its directory is read by a weft that knows the format.
	ErrNewerFormat = errors.New("thread: session format newer than this build")
)

// CorruptError is the typed shape ErrCorrupt takes when the failure
// belongs to one line of a stored session (ADR 0011 §5: "ErrCorrupt
// naming the line"): it carries the session and the 1-based line
// number — the header is line 1 — so a caller, a log, or a UI can
// point at the place. Match the class with errors.Is(err, ErrCorrupt)
// and take the place with errors.As; backends construct it directly
// through their own decode-failure paths.
type CorruptError struct {
	Session string
	Line    int // 1-based; 0 when the failure is not one line's
	Err     error
}

func (e *CorruptError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("thread: session %s line %d is corrupt: %v", e.Session, e.Line, e.Err)
	}
	return fmt.Sprintf("thread: session %s holds corrupt data: %v", e.Session, e.Err)
}

// Unwrap makes errors.Is(err, ErrCorrupt) true — the class — while
// Err keeps the cause for a caller who wants it.
func (e *CorruptError) Unwrap() error { return ErrCorrupt }
