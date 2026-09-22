package weft

import (
	"encoding/json"
	"strings"
)

// closedPrefix returns the longest prefix of s that closes into valid
// JSON without inventing value content. The whole string is preferred:
// open strings and brackets close over what is already there, and a
// value still being written simply grows on the next delta. A tail
// that cannot close at all — a bare literal cut mid-token, "tru" or
// "1e" — is dropped back to the previous member boundary, because
// closing it would fabricate a token the model has not sent. Empty
// when nothing closes. The sibling of mw.RepairJSON's closer,
// duplicated on purpose: mw imports weft, never the reverse.
func closedPrefix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != '{' && s[0] != '[' {
		return ""
	}
	if json.Valid([]byte(s)) {
		return s
	}
	// Maximal salvage first: when the whole prefix closes into valid
	// JSON, keep every member.
	if out := closeOpen(s); out != "" {
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
	p = strings.TrimSuffix(p, ",")
	return closeOpen(p)
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

// closeOpen closes a truncated JSON document: open strings (with
// escape fixup), then the bracket stack, after trimming a dangling
// comma or colon. No value content is invented — only structure; the
// empty string when the result still is not valid JSON.
func closeOpen(s string) string {
	var stack []byte
	inStr, esc := false, false
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
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	var b strings.Builder
	b.WriteString(s)
	if inStr {
		if esc {
			b.WriteByte('\\') // a dangling escape would eat the quote
		}
		b.WriteByte('"')
	}
	out := strings.TrimRight(b.String(), " \t\r\n")
	out = strings.TrimSuffix(out, ",")
	if strings.HasSuffix(out, ":") {
		out += "null"
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}
	if !json.Valid([]byte(out)) {
		return ""
	}
	return out
}
