package thread

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// NewSessionID returns a new session id: "s_" plus 26 characters — a
// millisecond-precision timestamp then random bits, Crockford base32,
// the ULID shape — so ids sort with the sessions they name. Sorting is
// to the millisecond only: ids born in the same millisecond order
// randomly among themselves, and a session's order of record is its
// entry order, never id order.
func NewSessionID() string { return newID("s_") }

// NewEntryID returns a new entry id: "e_" plus 26 time-sortable random
// characters (see NewSessionID).
func NewEntryID() string { return newID("e_") }

// newID builds a prefixed 26-character id: 48 bits of millisecond
// timestamp, 80 bits of randomness. It panics only if the system
// entropy source is broken — there is no degraded id to fall back to.
func newID(prefix string) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16)
	if _, err := rand.Read(b[6:16]); err != nil {
		panic("thread: crypto/rand failed: " + err.Error())
	}
	return prefix + encodeID(b)
}

// idAlphabet is Crockford base32 — 0-9 and twenty letters, no I L O U
// — so an id contains no character a human or a filename misreads.
const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// encodeID renders 128 bits as 26 Crockford base32 characters (the
// final two bits are zero padding), most significant first, so the
// string sorts exactly as the bytes do.
func encodeID(b [16]byte) string {
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = idAlphabet[b[15]&31]
		shr5(b[:])
	}
	return string(out[:])
}

// shr5 shifts 16 big-endian bytes right by 5 bits.
func shr5(b []byte) {
	var carry byte
	for i := 0; i < len(b); i++ {
		next := b[i] << 3 // this byte's high 5 bits, bound for byte i+1
		b[i] = b[i]>>5 | carry
		carry = next
	}
}

// ValidID reports whether id is safe as a session or entry id: 1 to
// 128 characters of letters, digits, '_' or '-', so an id is always
// exactly one path component — never a separator, a traversal, or a
// dotfile (ADR 0011 §5: backends vet the ids they are given). weft's
// own ids satisfy it by construction; the ids a caller supplies must
// stay inside it too.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
