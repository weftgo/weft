package wefttest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failRecorder captures a testing.TB failure so TestGolden can assert
// that Golden fails when it must, without failing the test itself.
// Golden only ever calls Helper and Fatalf.
type failRecorder struct {
	testing.TB // nil embed: any other method would panic, which the test would catch
	failed     bool
	msg        string
}

func (f *failRecorder) Helper() {}
func (f *failRecorder) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = fmt.Sprintf(format, args...)
}

// The golden helper's own contract: a missing file fails with a hint,
// -update writes (creating parent directories), a match passes, and a
// stale file fails with a diff. Comparison is forced on regardless of
// how the binary was invoked, so this test passes under plain `go
// test` and under `-update` alike; the write path is toggled explicitly.
func TestGolden(t *testing.T) {
	prev := *update
	*update = false
	defer func() { *update = prev }()

	path := filepath.Join(t.TempDir(), "nested", "golden.txt")

	missing := &failRecorder{}
	Golden(missing, path, []byte("v1\n"))
	if !missing.failed || !strings.Contains(missing.msg, "-update") {
		t.Errorf("missing file: failed=%v, msg=%q; want a failure hinting -update", missing.failed, missing.msg)
	}

	*update = true
	Golden(t, path, []byte("v1\n"))
	*update = false

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "v1\n" {
		t.Fatalf("-update wrote %q, %v; want v1\\n (and parent dirs created)", got, err)
	}

	Golden(t, path, []byte("v1\n")) // an exact match passes

	stale := &failRecorder{}
	Golden(stale, path, []byte("v2\n"))
	if !stale.failed || !strings.Contains(stale.msg, "- v1") || !strings.Contains(stale.msg, "+ v2") {
		t.Errorf("stale file: failed=%v, msg=%q; want a diff naming both sides", stale.failed, stale.msg)
	}
}
