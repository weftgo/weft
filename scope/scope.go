// Package scope is the Go side of the devtools' Scope (plan §13.3):
// the unit every discovery rung, deep link and API call carries — one
// conversation's public id and, optionally, the session, flow and run
// inside it — and the net/http middleware that hands it to the page.
//
// A Scope has one string form, shared byte for byte with the web
// client's lib/scope.ts (the golden studio/testdata/scope.golden.json
// pins both):
//
//	pub_…;session=s_…;flow=f_…;run=r_…
//
// the public id first and bare, the rest optional, in that fixed
// order, empty fields omitted, each value percent-encoded with
// encodeURIComponent's rules. Parse reads it back and never fails: a
// newer writer's field is not this reader's to reject.
//
// The response header is the development rung: thread has no HTTP
// layer, so the app's own handler sets it, in one line —
//
//	mux.Handle("POST /chat", scope.Header(chat, func(r *http.Request) scope.Scope {
//		return scope.Scope{PublicID: publicIDOf(r)}
//	}))
//
// and a handler that learns the run id only once its turn starts
// calls scope.Set(w, …) before it writes. The header never carries a
// token; a Scope has nowhere to put one.
//
// The package lives beside the root rather than in it: the root
// package is a generated facade over core, which never imports
// net/http (ADR 0016, ADR 0027).
package scope

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// HeaderName is the response header the app's handler sets and the
// panel reads.
const HeaderName = "Weft-Scope"

// Scope names what the devtools follow. PublicID is the join key the
// app hands out ("" is the dev list: no conversation); the other
// fields narrow it. The JSON names are the web client's.
type Scope struct {
	PublicID  string `json:"publicId"`
	SessionID string `json:"session,omitempty"`
	FlowID    string `json:"flow,omitempty"`
	RunID     string `json:"run,omitempty"`
}

// IsZero reports whether every field is empty.
func (s Scope) IsZero() bool { return s == Scope{} }

// String is the scope's one string form: the public id first and bare,
// then session=, flow=, run= for each field set, joined by ";", every
// value percent-encoded. The zero Scope is "".
func (s Scope) String() string {
	var b strings.Builder
	b.WriteString(encode(s.PublicID))
	for _, f := range [...]struct{ k, v string }{{"session", s.SessionID}, {"flow", s.FlowID}, {"run", s.RunID}} {
		if f.v != "" {
			b.WriteString(";" + f.k + "=" + encode(f.v))
		}
	}
	return b.String()
}

// Parse reads a scope's string form back. The first ";"-segment is the
// public id; each later one is key=value. A key it does not know, a
// segment without "=", an empty value and a repeat of a key already
// read are ignored; a value that does not percent-decode is kept raw.
// Parse never fails.
func Parse(str string) Scope {
	segs := strings.Split(str, ";")
	out := Scope{PublicID: decode(trim(segs[0]))}
	for _, seg := range segs[1:] {
		k, v, ok := strings.Cut(seg, "=")
		if !ok {
			continue
		}
		var dst *string
		switch trim(k) {
		case "session":
			dst = &out.SessionID
		case "flow":
			dst = &out.FlowID
		case "run":
			dst = &out.RunID
		default:
			continue
		}
		if v = decode(trim(v)); v != "" && *dst == "" {
			*dst = v
		}
	}
	return out
}

// Header sets the Weft-Scope response header to scopeOf(r) on every
// response next writes, unless that scope is zero, and exposes the
// header to cross-origin pages (Access-Control-Expose-Headers gains
// Weft-Scope; the app's CORS policy must still allow the page's
// origin). The header is set before next runs, so a streaming
// handler's first flush carries it; next may replace it with Set. A
// CORS layer inside Header that sets its own expose list replaces the
// entry, so the header is sent but a cross-origin page cannot read it:
// put scope.Header inside your CORS middleware, or list Weft-Scope in
// its exposed headers. The devtools panel reads a cross-origin
// response's header only when the page and the response are both on
// loopback (a dev server on :5173 calling the app on :8080); a
// cross-origin production app names its scope with data-scope or the
// DOM marker instead.
func Header(next http.Handler, scopeOf func(*http.Request) Scope) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := scopeOf(r); !s.IsZero() {
			Set(w, s)
		}
		next.ServeHTTP(w, r)
	})
}

// Set sets the Weft-Scope header on w to s, replacing any earlier
// value, and exposes it to cross-origin pages (Weft-Scope is appended
// to Access-Control-Expose-Headers unless already listed; a "*" there
// does not cover it for a credentialed request). A zero s removes the
// Weft-Scope header but leaves the expose entry an earlier Set added.
// Like any header it must be set before the handler's first Write or
// WriteHeader — the dynamic case: a handler that knows the run id once
// its turn starts; a Set after that changes nothing sent.
func Set(w http.ResponseWriter, s Scope) {
	h := w.Header()
	if s.IsZero() {
		h.Del(HeaderName)
		return
	}
	h.Set(HeaderName, s.String())
	for _, v := range h.Values("Access-Control-Expose-Headers") {
		for _, name := range strings.Split(v, ",") {
			if n := strings.TrimSpace(name); strings.EqualFold(n, HeaderName) {
				return
			}
		}
	}
	h.Add("Access-Control-Expose-Headers", HeaderName)
}

// encode is encodeURIComponent: every byte outside A–Z a–z 0–9 and
// -_.!~*'() becomes %XX. A string that is not valid UTF-8 — what
// encodeURIComponent refuses — has only the form's own delimiters
// (%, ;, =) encoded, as the web side's fallback does.
func encode(v string) string {
	valid := utf8.ValidString(v)
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		keep := !valid && c != '%' && c != ';' && c != '=' ||
			valid && ('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0)
		if keep {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// decode is decodeURIComponent, keeping v raw where that throws: a
// "%" not followed by two hex digits, or a run of escapes that does
// not spell valid UTF-8 (the raw characters between escapes are kept
// as they are, valid or not). "+" stays "+".
func decode(v string) string {
	if !strings.Contains(v, "%") {
		return v
	}
	out := make([]byte, 0, len(v))
	run := -1 // where the current run of escapes starts in out
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			if run >= 0 && !utf8.Valid(out[run:]) {
				return v
			}
			run = -1
			out = append(out, v[i])
			continue
		}
		if i+2 >= len(v) {
			return v
		}
		hi, ok1 := unhex(v[i+1])
		lo, ok2 := unhex(v[i+2])
		if !ok1 || !ok2 {
			return v
		}
		if run < 0 {
			run = len(out)
		}
		out = append(out, hi<<4|lo)
		i += 2
	}
	if run >= 0 && !utf8.Valid(out[run:]) {
		return v
	}
	return string(out)
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// trim is String.prototype.trim: JavaScript's white space and line
// terminators, which differ from unicode.IsSpace at U+0085 and U+FEFF.
func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return 0x2000 <= r && r <= 0x200A
	})
}
