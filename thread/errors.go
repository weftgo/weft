package thread

import (
	"errors"
	"fmt"
)

var (
	// ErrBusy is returned by Send under the Reject busy policy when the
	// session is already running a turn, and by Branch while a turn is
	// in flight: the call was not accepted and nothing was written
	// (ADR 0011 §4). Retryable — the same call succeeds once the turn
	// has ended.
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
	// Readers never lock: Load and List always work. Retryable — the
	// same call succeeds once the other writer has let go.
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

	// ErrNotPending is returned by Decide for a decision addressing a
	// call that is not pending — decided already, resumed already, or
	// never parked — and by Resume with no open boundary. The core's
	// rule (ADR 0007: a decision names a pending call) made strict and
	// raised before any entry lands or any run starts: a session never
	// records a decision it cannot apply.
	ErrNotPending = errors.New("thread: call is not pending")

	// The signed-decision failures (ADR 0021 §3) follow, each its own
	// error because each is its own operational answer. All are
	// fail-closed: nothing is recorded until every check passes.

	// ErrUnknownKey is returned by DecideSigned for a decision signed
	// under a key id the session's Keyring does not hold: the signer
	// is not one this session trusts.
	ErrUnknownKey = errors.New("thread: signing key not in the keyring")

	// ErrBadSignature is returned by DecideSigned when the signature
	// does not verify under the named key: the decision was altered
	// after signing, or signed with another key.
	ErrBadSignature = errors.New("thread: decision signature does not verify")

	// ErrExpired is returned by DecideSigned when the challenge the
	// signature answers has lapsed: ask for a fresh Request and sign
	// that one.
	ErrExpired = errors.New("thread: signing challenge expired")

	// ErrReplay is returned by DecideSigned when the challenge's nonce
	// was already answered by a recorded decision: a signature decides
	// once, across restarts.
	ErrReplay = errors.New("thread: decision signature replayed")

	// ErrArgsChanged is returned by DecideSigned when the parked
	// call's arguments no longer hash to what the signature covered:
	// the signer approved a different call.
	ErrArgsChanged = errors.New("thread: request arguments changed under the signature")

	// ErrSignatureRequired is returned by Decide on a session opened
	// with RequireSigned: the unsigned door is closed, and only
	// DecideSigned records caller-held decisions (ADR 0021 §3).
	ErrSignatureRequired = errors.New("thread: this session requires signed decisions")

	// ErrClosed is returned by Send, Continue and every write on a
	// Session whose Close has run: a closed session accepts no new
	// work and writes nothing. Reads keep answering from the tree the
	// session held when it closed. Terminal for the Session value —
	// Open the session again to continue it.
	ErrClosed = errors.New("thread: session is closed")

	// ErrNotPersisted is wrapped by the error Turn.Wait returns when
	// the turn ran but its end could not be written: the storage
	// refused the batch that carries the turn entry (and whatever
	// messages the steps had not already written), so the session's
	// tree holds no record of how the turn ended and its usage is in
	// no ledger. The run's result still rides beside the error — the
	// model answered; the storage did not keep it. Messages the steps
	// wrote as they joined are in the tree.
	ErrNotPersisted = errors.New("thread: turn end not persisted")

	// ErrCreateOnly is returned by Open when it is handed an option
	// that only means something while a session's header is being
	// written — WithMeta, PublicID, WithLineage. The header is
	// immutable once stored, so Open refuses the option instead of
	// ignoring it; Create and Fork honour it.
	ErrCreateOnly = errors.New("thread: option applies only when a session is created")

	// ErrReservedKey is returned by SetInfo for a metadata key under
	// the reserved "weft." prefix: those keys are the session's
	// identity (weft.public_id), set once in the header at Create —
	// WithMeta, PublicID — and never edited afterwards.
	ErrReservedKey = errors.New("thread: metadata key is reserved")
)

// CorruptError is the typed shape ErrCorrupt takes when the failure
// belongs to one place in a stored session (ADR 0011 §5: "ErrCorrupt
// naming the line"): it carries the session, the 1-based line number
// — the header is line 1 — and, when the failure is an entry the tree
// cannot hold (Open's validation: an empty, invalid or duplicate id, a
// parent the session does not hold before it), that entry's id, so a
// caller, a log, or a UI can point at the place. Match the class with
// errors.Is(err, ErrCorrupt), take the place with errors.As, and reach
// the cause through Err or errors.Is — Unwrap exposes both. Backends
// construct it directly through their own decode-failure paths.
type CorruptError struct {
	Session string
	Line    int    // 1-based; 0 when the failure is not one line's
	Entry   string // the entry id the failure names; empty when it has none
	Err     error
}

// Error names the session and the place — the line, the entry, or
// neither — and then the cause.
func (e *CorruptError) Error() string {
	switch {
	case e.Line > 0 && e.Entry != "":
		return fmt.Sprintf("thread: session %s line %d (entry %s) is corrupt: %v", e.Session, e.Line, e.Entry, e.Err)
	case e.Line > 0:
		return fmt.Sprintf("thread: session %s line %d is corrupt: %v", e.Session, e.Line, e.Err)
	case e.Entry != "":
		return fmt.Sprintf("thread: session %s entry %s is corrupt: %v", e.Session, e.Entry, e.Err)
	}
	return fmt.Sprintf("thread: session %s holds corrupt data: %v", e.Session, e.Err)
}

// Unwrap returns the class and the cause: errors.Is(err, ErrCorrupt)
// is true for every CorruptError, and errors.Is and errors.As also
// reach whatever Err wraps — a JSON syntax error, a sentinel of the
// backend's own.
func (e *CorruptError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrCorrupt}
	}
	return []error{ErrCorrupt, e.Err}
}
