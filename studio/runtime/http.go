package runtime

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// The package's own small plumbing: the error shape every Studio
// route answers with ({"error":{code,message}}, S4.2), and the
// command-id mint. The weft/runtime module mirrors both; the JSON
// between the two sides is the contract, pinned by tests.

// writeJSON writes v as the route's JSON body.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr answers in the Studio error shape (S4.2).
func writeErr(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	writeJSON(w, r, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, msg}})
}

// Command ids (§10.4: "cmd_01J…"): a ULID — 48-bit millisecond
// timestamp + 80 random bits, Crockford base32, 26 chars — so the ids
// sort by creation time as strings and double as SSE frame ids (the
// Last-Event-ID resume cursor).

const ulidLen = 26

var (
	idMu    sync.Mutex
	lastRaw [16]byte
	haveRaw bool
)

// newCommandID mints one "cmd_"-prefixed id, monotonic in-process.
func newCommandID() string { return "cmd_" + newULID() }

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
		panic("studio/runtime: crypto/rand unavailable: " + err.Error())
	}
	if haveRaw && bytesLE(b, lastRaw) {
		copy(b[:], lastRaw[:])
		incBytes(b[6:])
	}
	copy(lastRaw[:], b[:])
	haveRaw = true
	id := encodeULID(b)
	return string(id[:])
}

// encodeULID renders 128 bits as 26 Crockford base32 chars.
func encodeULID(b [16]byte) [ulidLen]byte {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var out [ulidLen]byte
	for i := ulidLen - 1; i >= 0; i-- {
		out[i] = crockford[b[15]&31]
		shrBytes(b[:], 5)
	}
	return out
}

func shrBytes(b []byte, n uint) {
	var carry byte
	for i := 0; i < len(b); i++ {
		v := b[i]
		b[i] = v>>n | carry
		carry = v << (8 - n)
	}
}

func incBytes(b []byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

func bytesLE(a, b [16]byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
