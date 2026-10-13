// Package termsafe makes untrusted text safe to print to an operator's
// terminal: the CLI's tables (weft runs, weft doctor) print prompts,
// agent names and error text that came from an app or an OTLP sender,
// and a control character in them — an escape sequence, a carriage
// return — would reach the terminal raw.
package termsafe

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text returns s with every control rune (unicode.IsControl: C0, DEL,
// C1 — ESC, CR, LF, TAB among them) and every bidirectional formatting
// rune (unicode.Bidi_Control) replaced by a space, so a printed cell
// can neither drive the terminal nor reorder or overwrite its row.
// Invalid UTF-8 becomes U+FFFD. Nothing else changes.
func Text(s string) string {
	if strings.IndexFunc(s, unsafe) < 0 && utf8.ValidString(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafe(r) {
			return ' '
		}
		return r
	}, s)
}

func unsafe(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}
