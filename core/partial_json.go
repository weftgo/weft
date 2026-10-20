package core

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/weftgo/weft/core/internal/jsonclose"
)

// closedPrefix returns the longest prefix of s that closes into valid
// JSON without inventing value content. The whole string is preferred:
// open strings and brackets close over what is already there, and a
// value still being written simply grows on the next delta. A tail
// that cannot close yet is cut back to where it began, never further,
// so the closed prefix of a longer stream keeps every field and array
// element the closed prefix of a shorter one had — the OutputDecoder's
// monotonic partials (0.18.1: an open array whose tail could not close
// was dropped whole, a streamed partial going 2 items → 0 → 2):
//
//   - a bare literal cut mid-token: a number keeps its longest valid
//     prefix ("1e" → 1), anything else ("tru", "-") is dropped back to
//     the delimiter before it, where a dangling key closes as null —
//     closing the token would fabricate one the model has not sent;
//   - a string cut inside a \u escape: the escape is dropped;
//   - a string whose tail the next bytes would take back — a lone
//     backslash, a rune cut mid-sequence, a high surrogate before its
//     pair: the tail is held back first (holdStringTail), so a string
//     only grows by appending;
//   - anything else (a key cut mid-string, a key with no colon yet):
//     the innermost member is dropped, back to the last comma or open
//     bracket at any depth — the top-level opener excepted: a first
//     member still being written closes to nothing, and the decoder
//     keeps its last good partial.
//
// Only JSON whitespace (space, tab, CR, LF) is trimmed around the
// document; any other rune, Unicode whitespace included, is text.
// Garbage after the top-level value closed is dropped with it. Empty
// when nothing closes. The closer itself is internal/jsonclose, the
// state machine mw.RepairJSON shares.
func closedPrefix(s string) string {
	// JSON whitespace only: a raw NBSP, U+2028 or U+3000 at the end
	// of an open string is text (0.18.1-fix4: strings.TrimSpace took
	// it, and the partial shrank by it until the next byte arrived).
	s = strings.Trim(s, " \t\r\n")
	if s == "" || s[0] != '{' && s[0] != '[' {
		return ""
	}
	s = holdStringTail(s)
	if out, ok := jsonclose.Close(s); ok {
		return out
	}
	sc := scanTail(s)
	if sc.end < len(s) {
		// Garbage after the top-level value: keep the value.
		s = s[:sc.end]
		if out, ok := jsonclose.Close(s); ok {
			return out
		}
	}
	switch {
	case sc.inStr && sc.esc >= 0:
		// A string cut inside an escape: drop the escape.
		if out, ok := closeTrimmed(s[:sc.esc]); ok {
			return out
		}
	case !sc.inStr && sc.tok >= 0:
		// A bare literal cut mid-token: a number keeps its longest
		// valid prefix, else the token goes.
		tok := s[sc.tok:]
		for k := len(tok) - 1; k > 0; k-- {
			if json.Valid([]byte(tok[:k])) {
				if out, ok := closeTrimmed(s[:sc.tok] + tok[:k]); ok {
					return out
				}
				break
			}
		}
		if out, ok := closeTrimmed(s[:sc.tok]); ok {
			return out
		}
	}
	// The innermost member goes: back to the last delimiter at any
	// depth, the latest first. On a prefix of a valid document the
	// first cut closes; the bound keeps garbage linear.
	// The top-level opener is no cut: a first member still being
	// written closes to nothing (the decoder reports no partial yet)
	// rather than to an empty document.
	for i, n := len(sc.delims)-1, 0; i >= 0 && n < maxDelimCuts; i, n = i-1, n+1 {
		d := sc.delims[i]
		if d == 0 {
			break
		}
		p := s[:d]
		if s[d] != ',' {
			p = s[:d+1] // an open bracket stays, emptied
		}
		if out, ok := closeTrimmed(p); ok {
			return out
		}
	}
	// Garbage: the last top-level member boundary.
	cut := lastMemberEnd(s)
	if cut <= 0 {
		return ""
	}
	out, ok := closeTrimmed(s[:cut])
	if !ok {
		return ""
	}
	return out
}

// holdStringTail drops, from the end of an open string, what the next
// bytes would take back: a dangling escape (a lone backslash closes as
// an escaped one, "x\\", then the escape arrives, "x\n"), an
// incomplete UTF-8 sequence (decoded as U+FFFD until its rune
// completes) and a \uD800–\uDBFF escape still waiting for its low
// surrogate (U+FFFD until the pair completes). Held back, a streamed
// string only grows by appending. Cheap on the common path: the
// structure is scanned only when the tail could be one of those.
// jsonclose itself keeps closing such tails (mw.RepairJSON wants a
// whole document, not a partial one).
func holdStringTail(s string) string {
	for i := 0; i < 3; i++ { // an escape, its surrogate, a cut rune: at most three cuts
		n := len(s)
		cut := n - incompleteRune(s)
		if cut == n && strings.LastIndexByte(s[max(0, n-6):], '\\') < 0 {
			return s
		}
		sc := scanTail(s)
		if !sc.inStr || sc.end < n {
			return s
		}
		switch {
		case sc.esc >= 0:
			cut = sc.esc
		case cut < n: // the cut rune goes
		case sc.last >= 0 && sc.last == n-6 && s[sc.last+1] == 'u' && isHighSurrogate(s[n-4:]):
			// Only a \u escape: \\DBFF, \nDAD0 are an escape then text.
			cut = sc.last
		default:
			return s
		}
		s = s[:cut]
	}
	return s
}

// incompleteRune returns the length of the incomplete UTF-8 sequence
// that ends s, 0 when s ends on a rune boundary.
func incompleteRune(s string) int {
	for k := 1; k <= utf8.UTFMax-1 && k <= len(s); k++ {
		c := s[len(s)-k]
		if c < utf8.RuneSelf {
			return 0
		}
		if utf8.RuneStart(c) {
			if utf8.FullRuneInString(s[len(s)-k:]) {
				return 0
			}
			return k
		}
	}
	return 0
}

// isHighSurrogate reports whether hex, four hex digits, names a high
// surrogate (U+D800–U+DBFF).
func isHighSurrogate(hex string) bool {
	v, err := strconv.ParseUint(hex, 16, 16)
	return err == nil && v >= 0xD800 && v <= 0xDBFF
}

// closeTrimmed closes p with its trailing whitespace dropped.
func closeTrimmed(p string) (string, bool) {
	return jsonclose.Close(strings.TrimRight(p, " \t\r\n"))
}

// maxDelimCuts bounds closedPrefix's member cuts: a prefix of a valid
// document closes at the first, garbage gives up after a few.
const maxDelimCuts = 8

// tailScan is scanTail's reading of a string that does not close.
type tailScan struct {
	end    int   // just past the top-level value when it closed, else len(s)
	inStr  bool  // s[:end] ends inside a string
	esc    int   // inside a string: where a trailing escape began, else -1
	last   int   // inside a string: where its latest escape began, else -1
	tok    int   // outside a string: where a trailing bare token began, else -1
	delims []int // commas and open brackets outside strings, in order
}

// scanTail reads s's structure once: where its top-level value closed
// (when it did), what its tail is cut in — a string's escape, a bare
// token — and where every member delimiter sits.
func scanTail(s string) tailScan {
	sc := tailScan{end: len(s), esc: -1, last: -1, tok: -1}
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if sc.inStr {
			switch {
			case sc.esc >= 0:
				// \uXXXX takes four hex digits; any other escape one byte.
				if s[sc.esc+1] != 'u' || i-sc.esc == 5 {
					sc.esc = -1
				}
			case c == '\\':
				sc.esc, sc.last = i, i
			case c == '"':
				sc.inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			sc.inStr, sc.tok, sc.last = true, -1, -1
		case c == '{' || c == '[':
			depth++
			sc.delims = append(sc.delims, i)
			sc.tok = -1
		case c == '}' || c == ']':
			depth--
			sc.tok = -1
			if depth <= 0 {
				sc.end = i + 1
				return sc
			}
		case c == ',':
			sc.delims = append(sc.delims, i)
			sc.tok = -1
		case c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '+' || c == '-':
			if sc.tok < 0 {
				sc.tok = i
			}
		default:
			sc.tok = -1
		}
	}
	return sc
}

// lastMemberEnd scans s and returns the offset just past the last
// top-level member delimiter: a comma at depth one, outside any
// string, or the bracket that returns to depth zero. Escapes are
// honoured inside strings; garbage outside the structure ends the
// scan at the point the structure closed.
func lastMemberEnd(s string) int {
	var stack []byte
	inStr, esc := false, false
	best := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{' || c == '[':
			stack = append(stack, c)
		case c == '}' || c == ']':
			if len(stack) == 0 {
				return best + 1 // garbage: unbalanced close
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1 // the top-level value closed here
			}
		case c == ',' && len(stack) == 1:
			best = i // a complete top-level member ends at this comma
		}
	}
	return best + 1
}
