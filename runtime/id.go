package runtime

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// Time-ordered ids (a ULID shape: 48-bit millisecond timestamp + 80
// random bits, Crockford base32, 26 chars). Command ids sort by
// creation time as strings, which is what the SSE resume cursor
// (Last-Event-ID, a command id) compares against. No dependency: the
// studio/runtime package mirrors these 30 lines.

const ulidLen = 26

var (
	idMu    sync.Mutex
	lastRaw [16]byte
	haveRaw bool
)

// newULID returns a time-ordered id, monotonic within the process: the
// millisecond field never moves backwards (a wall-clock step back is
// clamped to the last value), and inside one millisecond the random
// suffix increments — so in-process ordering never regresses, which is
// what the SSE resume cursor relies on.
func newULID() string {
	idMu.Lock()
	defer idMu.Unlock()
	ms := time.Now().UnixMilli()
	if haveRaw {
		if last := int64(binary.BigEndian.Uint64(lastRaw[:8]) >> 16); ms <= last {
			ms = last
		}
	}
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(ms)<<16) // top 48 bits
	if _, err := rand.Read(b[6:]); err != nil {
		panic("weft/runtime: crypto/rand unavailable: " + err.Error())
	}
	if haveRaw && bytesLE(b, lastRaw) {
		// The fresh draw sorted below the last id (same millisecond):
		// take the last value and bump it, so the new id is exactly
		// last+1, never last-epsilon.
		copy(b[:], lastRaw[:])
		incBytes(b[6:])
	}
	copy(lastRaw[:], b[:])
	haveRaw = true
	id := encodeULID(b)
	return string(id[:])
}

// newID prefixes a ULID: "rt_" a runtime, "cmd_" a command, "pg_" a
// playground run.
func newID(prefix string) string { return prefix + newULID() }

// encodeULID renders 128 bits as 26 Crockford base32 chars (130 bits
// with two zero bits of front padding).
func encodeULID(b [16]byte) [ulidLen]byte {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var out [ulidLen]byte
	for i := ulidLen - 1; i >= 0; i-- {
		out[i] = crockford[b[15]&31]
		shrBytes(b[:], 5)
	}
	return out
}

// shrBytes shifts a big-endian byte slice right by n (< 8) bits,
// in place.
func shrBytes(b []byte, n uint) {
	var carry byte
	for i := 0; i < len(b); i++ {
		v := b[i]
		b[i] = v>>n | carry
		carry = v << (8 - n)
	}
}

// incBytes adds one to a big-endian byte slice, in place.
func incBytes(b []byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

// bytesLE reports a < b.
func bytesLE(a, b [16]byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
