package core

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// A generic Output type whose type arguments name non-ASCII identifiers
// long enough to pass maxOutputTypeName.
type Rechnungsübersichtsgegenstandsbeschreibung struct{}

type ÜbersichtBx[A, B any] struct {
	A A `json:"a"`
	B B `json:"b"`
}

// TestOutputTypeNameCutsAtRune (UX-S1-06): weft.output.type is at most
// maxOutputTypeName bytes and valid UTF-8 — a cut lands on a rune
// boundary, never inside a multi-byte identifier.
func TestOutputTypeNameCutsAtRune(t *testing.T) {
	typ := reflect.TypeFor[*ÜbersichtBx[Rechnungsübersichtsgegenstandsbeschreibung, Rechnungsübersichtsgegenstandsbeschreibung]]()
	full := typ.Elem().String()
	if len(full) <= maxOutputTypeName {
		t.Fatalf("the type's name is %d bytes, want more than %d to cut", len(full), maxOutputTypeName)
	}
	// The cap falls inside the second argument's "ü": a byte cut would
	// leave half a rune.
	if utf8.RuneStart(full[maxOutputTypeName]) {
		t.Fatalf("byte %d of %q starts a rune; the fixture should straddle it", maxOutputTypeName, full)
	}
	got := outputTypeName(typ)
	if len(got) > maxOutputTypeName || !utf8.ValidString(got) || !strings.HasPrefix(full, got) {
		t.Errorf("outputTypeName = %q (%d bytes, valid %v), want a valid prefix of %q of at most %d bytes",
			got, len(got), utf8.ValidString(got), full, maxOutputTypeName)
	}
	if len(got) < maxOutputTypeName-utf8.UTFMax {
		t.Errorf("outputTypeName cut %d bytes short of the cap", maxOutputTypeName-len(got))
	}
}
