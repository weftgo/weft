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

	// ErrLocked is returned when a session is already held by another
	// writer (the one-writer rule, ADR 0011 §5): by a backend, for a
	// writer in another process or another Storage in this one; and by
	// a Session's writes, for another Session value on the same
	// Storage (the Leaser capability). Readers never lock: Load, List
	// and Open always work. Retryable — the same call succeeds once
	// the other writer has let go, which for a Session is its Close.
	ErrLocked = errors.New("thread: session is locked by another writer")

	// ErrStale is returned by a Session's write when the stored
	// session holds entries the Session never loaded: another writer
	// appended to it after this Session was opened, and a write now
	// would attach to a leaf that is no longer the session's — a fork
	// nobody asked for. Nothing is written and the Session's tree is
	// unchanged. Terminal for the Session value: Open the session
	// again to write from what it now holds.
	ErrStale = errors.New("thread: session changed since it was opened")

	// ErrCorrupt wraps the failures a backend reports for stored data
	// it cannot decode: a malformed line that is not a torn tail (a
	// torn final line is a crash, dropped and reported through Load's
	// LoadReport; data from a newer weft is ErrNewerFormat, never
	// skipped). Salvage, the open option every backend accepts,
	// downgrades this to a skip reported in the LoadReport.
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

	// ErrNotPending is returned by Decide and DecideSigned for a
	// decision addressing a call that is not pending — decided already,
	// resumed already, or never parked — or, signed, carrying a
	// signature for another occurrence of the call; by Request for a
	// call that is not pending; by ResolveDelegation on a call that
	// delegates to no child; and by Resume with no open boundary. The
	// core's rule (ADR 0007: a decision names a pending call) made
	// strict and raised before any entry lands or any run starts: a
	// session never records a decision it cannot apply.
	ErrNotPending = errors.New("thread: call is not pending")

	// The approval failures (ADR 0021 §3, §5) follow, each its own
	// error because each is its own operational answer. All are
	// fail-closed: nothing is recorded until every check passes.

	// ErrUnknownKey is returned by Keyring.Sign when the ring does not
	// hold the challenge's key. DecideSigned never returns it: a
	// signature under a key id the session's ring does not hold is
	// ErrBadSignature, so a caller probing key ids learns nothing.
	ErrUnknownKey = errors.New("thread: signing key not in the keyring")

	// ErrBadSignature is returned by DecideSigned when the signed
	// decision does not verify against the session's keyring and the
	// pending request: a MAC mismatch, an unknown key id, an outcome
	// that is none of the four, a session other than this one, an
	// empty nonce or one this session never issued for the request,
	// or an expiry or a tool that is not the request's.
	ErrBadSignature = errors.New("thread: decision signature does not verify")

	// ErrExpired means the request is past its expiry: returned by
	// Decide, DecideSigned and Request. No decision can approve a
	// lapsed request — the session denies it on its own (ADR 0021 §5).
	ErrExpired = errors.New("thread: approval request expired")

	// ErrReplay is returned by DecideSigned when the nonce already
	// answered a recorded decision: a signature decides once, across
	// restarts.
	ErrReplay = errors.New("thread: decision signature replayed")

	// ErrArgsChanged is returned by DecideSigned when the signed
	// arguments hash is not the pending request's: the signer approved
	// a different call.
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

	// ErrNotRun is wrapped by the error Turn.Wait returns for a turn
	// whose run never started: the context its Send carried ended
	// first (the error also wraps context.Canceled or
	// context.DeadlineExceeded), its prompt entry could not be written
	// (the storage's error is wrapped too), or — a resume — the
	// boundary it was armed for was gone (ErrNotPending) or its audit
	// entry could not be written. No model was called.
	ErrNotRun = errors.New("thread: turn did not run")

	// ErrDropped is wrapped by the error Turn.Wait returns for a
	// message ClearQueue removed before it reached a model — a queued
	// steer or a queued send — and for a send an interrupting Send had
	// to take back. The receipt entry records the drop.
	ErrDropped = errors.New("thread: message dropped from the queue")

	// ErrTurnPanicked is wrapped by the error Turn.Wait returns when
	// the session's own turn machinery panicked — which includes the
	// caller's IDs and Clock functions, called under the session's
	// lock. The panic is contained: the turn ends with this error, the
	// lock is released, and the session keeps working. Tool and model
	// panics never surface here; the core reports them as run errors.
	ErrTurnPanicked = errors.New("thread: turn panicked")

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
