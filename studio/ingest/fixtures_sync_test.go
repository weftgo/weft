package ingest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestOTLPFixturesMatchObsdb: testdata/otlp holds copies of
// obsdb/testdata's OTLP fixtures — copies, so the module's tests run from
// the module cache where the sibling module is absent. Inside the repo
// each copy must stay byte-identical to obsdb's original.
func TestOTLPFixturesMatchObsdb(t *testing.T) {
	src := filepath.Join("..", "..", "obsdb", "testdata")
	if _, err := os.Stat(src); err != nil {
		t.Skip("obsdb/testdata not beside this module (module cache)")
	}
	copies, err := filepath.Glob(filepath.Join("testdata", "otlp", "*"))
	if err != nil || len(copies) == 0 {
		t.Fatalf("no fixtures in testdata/otlp: %v", err)
	}
	for _, c := range copies {
		got, err := os.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(src, filepath.Base(c)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from obsdb/testdata/%s: copy the fixture again", c, filepath.Base(c))
		}
	}
}
