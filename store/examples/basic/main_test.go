package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/weftgo/weft/wefttest"
)

// The example's output is golden-gated: timestamps and the working
// directory are normalized away, everything a reader cares about —
// the table, the stream, the child — is pinned byte-for-byte.
func TestGoldenOutput(t *testing.T) {
	dir := t.TempDir()
	capture := func() string {
		t.Helper()
		tmp := t.TempDir()
		outPath := filepath.Join(tmp, "out")
		real := os.Stdout
		f, err := os.Create(outPath)
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = f
		runErr := run(dir)
		os.Stdout = real
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if runErr != nil {
			t.Fatal(runErr)
		}
		b, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	got := normalize(capture())
	wefttest.Golden(t, "testdata/golden.txt", []byte(got))

	// The acceptance shape: a second run against the same database
	// lists two top-level runs, Total: 2 — children never flood the
	// list.
	second := capture()
	if want := "Total: 2"; !contains(second, want) {
		t.Errorf("second run's list = %q, want %q", second, want)
	}
	topLevel := regexp.MustCompile(`(?m)^demo-[0-9a-z]+\s`).FindAllString(second, -1)
	if len(topLevel) != 2 {
		t.Errorf("second run lists %d top-level rows (%v), want 2", len(topLevel), topLevel)
	}
}

var (
	tsRe = regexp.MustCompile(`\d{2}:\d{2}:\d{2}`)
	idRe = regexp.MustCompile(`demo-[0-9a-z]+`)
)

func normalize(s string) string {
	s = tsRe.ReplaceAllString(s, "HH:MM:SS")
	return idRe.ReplaceAllString(s, "demo-RUN")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
