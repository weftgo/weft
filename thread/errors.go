package thread

import "errors"

// ErrNewerFormat wraps the decode failures UnmarshalEntry and Header
// decoding report for data written by a newer weft than this build: an
// entry kind this build does not know, an entry carrying a "v" above
// the version this build reads of its kind, or a session header whose
// envelope is ahead of FormatVersion (ADR 0011 §5–§6). Loud over
// silent: a session must decode to exactly what was written, and an
// older weft says so instead of guessing. List, which reads headers
// only, still works — an older reader can see the session and name why
// it cannot open it.
var ErrNewerFormat = errors.New("thread: session format newer than this build")
