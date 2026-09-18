package wefttest

import (
	"fmt"
	"testing"

	"github.com/weftgo/weft"
)

// ConformInfo reports nil when middleware m forwards the inner model's
// identity unchanged — the convention ModelMiddleware documents
// ("implementations should forward Info") — and an error naming the
// offender otherwise. Use it in a middleware's own test suite to turn
// the documented convention into a checked fact.
func ConformInfo(m weft.ModelMiddleware) error {
	inner := Script()
	got, want := weft.InfoOf(m(inner)), weft.InfoOf(inner)
	if got != want {
		return fmt.Errorf("middleware %T dropped Info: got %+v, want %+v", m, got, want)
	}
	return nil
}

// ConformInfoT is ConformInfo as a test helper: it fails t instead of
// returning an error.
func ConformInfoT(t testing.TB, m weft.ModelMiddleware) {
	t.Helper()
	if err := ConformInfo(m); err != nil {
		t.Error(err)
	}
}
