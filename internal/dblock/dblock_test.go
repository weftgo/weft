package dblock_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/internal/dblock"
)

// TestExclusive: one holder at a time — a second Acquire (another
// handle, as another process would hold it) is ErrHeld and reads the
// holder; after Release the lock is free again and names nobody.
func TestExclusive(t *testing.T) {
	db := filepath.Join(t.TempDir(), "weft.db")
	l, err := dblock.Acquire(db)
	if err != nil {
		t.Fatal(err)
	}
	l.SetHolder(dblock.Holder{URL: "http://127.0.0.1:7347", PID: 42})
	if _, err := dblock.Acquire(db); !errors.Is(err, dblock.ErrHeld) {
		t.Fatalf("second Acquire = %v, want ErrHeld", err)
	}
	if h, ok := dblock.ReadHolder(db); !ok || h.URL != "http://127.0.0.1:7347" || h.PID != 42 {
		t.Errorf("holder = %+v %v", h, ok)
	}
	l.Release()
	l.Release() // idempotent
	if _, ok := dblock.ReadHolder(db); ok {
		t.Error("a released lock still names a holder")
	}
	again, err := dblock.Acquire(db)
	if err != nil {
		t.Fatalf("Acquire after Release = %v", err)
	}
	again.Release()
	var nilLock *dblock.Lock
	nilLock.Release()
	nilLock.SetHolder(dblock.Holder{})
}
