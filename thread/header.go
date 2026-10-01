package thread

import (
	"encoding/json"
	"fmt"
	"time"
)

// FormatVersion is the wire version of every document the thread
// module writes (ADR 0011 §6) — the integer every weft wire document
// carries ("weft": 1, ADR 0005). It moves only for a layout change a
// reader cannot handle additively: a new entry kind, a new optional
// key, a new entry version never move it. A header carrying a higher
// number fails with ErrNewerFormat; nothing is ever rewritten on
// open.
//
// The format is not frozen. This build reads every file an earlier
// thread release wrote — the committed goldens of every format pin
// that, and a release that could not read one would fail its own
// tests — but no compatibility promise beyond what the tests hold is
// made before the module's API and format are declared stable; there
// is no migration tool, and none is needed while every change so far
// has been additive.
const FormatVersion = 1

// kindSession is the header line's wire discriminator: a session
// file's first line is the only line that carries it.
const kindSession = "session"

// Header is a session's first line: its identity, its creation time,
// its fork origin and caller metadata. A Fork names the session and
// entry it grew from in Parent (ADR 0011 §3), so the new session is
// self-contained but traceable; beyond that the header is immutable —
// title and metadata edits are info entries appended to the file,
// never rewrites of it.
type Header struct {
	// Weft is the envelope integer. The zero value writes and means
	// FormatVersion; Create rejects anything else, so a caller never
	// has to know the number to construct a valid header.
	Weft    int               `json:"weft"`
	ID      string            `json:"id"`
	Created time.Time         `json:"created"`
	Parent  *ParentRef        `json:"parent,omitempty"`
	Lineage *Lineage          `json:"lineage,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// Lineage names a pool child's origin (ADR 0022 §3): the parent
// session and, for a wrapped delegation, the call that delegated. It
// is a reference, not a copy — unlike Parent, a fork's self-contained
// path, the child's file holds only its own entries, and the link is
// how a nested approval finds the session that must resume the child.
type Lineage struct {
	Session string `json:"parent_session"`
	Call    string `json:"parent_call_id,omitempty"`
}

// ParentRef names a fork's origin: the session the new session was
// forked from, and the entry its copied path ends at (ADR 0011 §3).
type ParentRef struct {
	Session string `json:"session"`
	Entry   string `json:"entry"`
}

// headerWire is Header without its methods, so the plain struct
// encoding is used — the same wire-alias trick as the entries and the
// core's events.
type headerWire Header

// MarshalJSON encodes the header with its "type":"session"
// discriminator. A zero Weft writes FormatVersion — the zero Header is
// a valid, current-format header.
func (h Header) MarshalJSON() ([]byte, error) {
	if h.Weft == 0 {
		h.Weft = FormatVersion
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		headerWire
	}{kindSession, headerWire(h)})
}

// UnmarshalJSON decodes a session header and enforces the envelope
// rule (ADR 0011 §6): a header from a newer format fails with
// ErrNewerFormat instead of being read with this build's tags, and a
// line without the session shape was never a thread session's first
// line. Unknown keys are additive and ignored.
func (h *Header) UnmarshalJSON(b []byte) error {
	var head struct {
		Type string `json:"type"`
		Weft int    `json:"weft"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	if head.Type != kindSession {
		return fmt.Errorf("thread: %q is not a session header", head.Type)
	}
	switch {
	case head.Weft > FormatVersion:
		return fmt.Errorf("%w: session header has weft=%d, this build reads %d", ErrNewerFormat, head.Weft, FormatVersion)
	case head.Weft != FormatVersion:
		return fmt.Errorf("thread: session header has weft=%d, this build reads %d — not a header this format wrote", head.Weft, FormatVersion)
	}
	var w headerWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*h = Header(w)
	return nil
}
