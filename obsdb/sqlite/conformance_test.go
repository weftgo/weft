package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/obsdbtest"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// The conformance table runs against both handle shapes: the shared
// in-memory database and a real file with its writer + read pool.
func TestConformanceMemory(t *testing.T) {
	obsdbtest.Run(t, func(t *testing.T) obsdb.DB {
		db, err := sqlite.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	})
}

func TestConformanceFile(t *testing.T) {
	obsdbtest.Run(t, func(t *testing.T) obsdb.DB {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "conf.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	})
}
