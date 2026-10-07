package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// A write whose context ends while its transaction is open is a
// canceled write, and says so: database/sql rolls the transaction back
// on its own when the context ends, and the commit then fails with
// sql.ErrTxDone — which the backend used to return raw, so a caller
// that matched context.Canceled (thread/pool's canceled settlement)
// read a canceled write as a storage failure. The crash matrix's
// pool_canceled point failed that way in CI once ("the delegation did
// not rest at canceled: … sql: transaction has already been committed
// or rolled back"). The hook cancels between the inserts and the
// commit and waits for the rollback, so the race is decided the way
// CI lost it, every run.
func TestCanceledWriteReportsCancellation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	bg := context.Background()
	if err := st.Create(bg, thread.Header{ID: "s_cancel", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	beforeCommit = func(tx *sql.Tx) {
		cancel()
		deadline := time.Now().Add(5 * time.Second)
		for { // until database/sql's watcher has rolled the transaction back
			if _, err := tx.ExecContext(bg, `SELECT 1`); errors.Is(err, sql.ErrTxDone) {
				return
			}
			if time.Now().After(deadline) {
				t.Error("the transaction was never rolled back")
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	defer func() { beforeCommit = nil }()
	err = st.Append(ctx, "s_cancel", thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: weft.User("hi")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Append on a context canceled mid-transaction = %v, want an error matching context.Canceled", err)
	}
	beforeCommit = nil
	_, entries, _, err := st.Load(bg, "s_cancel")
	if err != nil || len(entries) != 0 {
		t.Errorf("after the canceled append: %d entries, %v; want none — the write did not land", len(entries), err)
	}
}
