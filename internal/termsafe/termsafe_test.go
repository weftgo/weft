package termsafe

import "testing"

func TestText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain text, ünïcode", "plain text, ünïcode"},
		{"a\x1b]0;pwned\x07b", "a ]0;pwned b"},
		{"clear\x1b[2Jscreen", "clear [2Jscreen"},
		{"row\roverwritten", "row overwritten"},
		{"tab\there\nline", "tab here line"},
		{"c1\u009bcsi", "c1 csi"},
		{"del\x7f", "del "},
		{"bidi\u202eevil", "bidi evil"},
		{"bad\xffutf8", "bad\ufffdutf8"},
	} {
		if got := Text(c.in); got != c.want {
			t.Errorf("Text(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
