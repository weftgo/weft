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

// The command store's restart property (plan I3): a second handle on
// the same file, opened after the first closed, reads what it wrote.
func TestCommandsRestartFile(t *testing.T) {
	obsdbtest.CommandsRestart(t, func(t *testing.T) (obsdb.DB, func() obsdb.DB) {
		path := filepath.Join(t.TempDir(), "restart.db")
		open := func() obsdb.DB {
			db, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}
		return open(), open
	})
}
