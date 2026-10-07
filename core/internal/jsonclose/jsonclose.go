// Package jsonclose closes truncated JSON documents: the state machine
// both the OutputDecoder's prefix salvage (partial_json.go) and
// mw.RepairJSON need. It lived as two hand-maintained copies until the
// 2026-09-22 0.3.0 review flagged that they had already drifted (the
// trailing-comma rule differed), and moves here rather than into either
// public package — THE-END-GOAL principle 3 names internal/ as the tool
// that keeps the guaranteed surface small, and Go's path-based internal
// rule lets both the root package and mw import it while third parties
// cannot (the adapterkit precedent).
package jsonclose

import (
	"encoding/json"
	"strings"
)

// Close returns s closed over its open strings and brackets — structure
// only, never value content: a dangling escape is fixed so it cannot
// eat the closing quote, trailing commas are dropped, and a dangling
// colon gets a null. The attempt is handed back even when it still is
// not valid JSON (mw's payload-preservation rule: the bytes the caller
// sent survive byte-for-byte); valid reports whether it is.
func Close(s string) (closed string, valid bool) {
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
	// A trailing comma run, or a key with no value, cannot close as is.
	out = strings.TrimRight(out, ",")
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
	return out, json.Valid([]byte(out))
}
