package weft

import (
	"strings"

	"github.com/weftgo/weft/internal/jsonclose"
)

// closedPrefix returns the longest prefix of s that closes into valid
// JSON without inventing value content. The whole string is preferred:
// open strings and brackets close over what is already there, and a
// value still being written simply grows on the next delta. A tail
// that cannot close at all — a bare literal cut mid-token, "tru" or
// "1e" — is dropped back to the previous member boundary, because
// closing it would fabricate a token the model has not sent. Empty
// when nothing closes. The closer itself is internal/jsonclose, the
// state machine mw.RepairJSON shares.
func closedPrefix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != '{' && s[0] != '[' {
		return ""
	}
	if out, ok := jsonclose.Close(s); ok {
		return out
	}
	// A tail that cannot close (a bare literal cut mid-token) is
	// dropped with its member, back to the last delimiter; the
	// members before it closed fine.
	cut := lastMemberEnd(s)
	if cut <= 0 {
		return ""
	}
	p := strings.TrimRight(s[:cut], " \t\r\n")
	out, ok := jsonclose.Close(p)
	if !ok {
		return ""
	}
	return out
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
