package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/store/storetest"
)

// The conformance table runs against a file-backed database (fresh per
// subtest) and against ":memory:".
func TestFileConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := Open(filepath.Join(t.TempDir(), "runs.db"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestMemoryConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// Open applies migration 0001 to a fresh file; a second Open on the
// same file is a no-op; a schema ahead of the binary fails loudly with
// ErrNewerSchema, never a silent misread.
func TestMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrations.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db := s.(*Store).db
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("fresh file has %d applied migrations, want 1", n)
	}

	if _, err := Open(path); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("second Open applied more migrations (%d), want idempotent 1", n)
	}

	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (999, ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	if !errors.Is(err, ErrNewerSchema) {
		t.Errorf("Open on a newer schema: err = %v, want ErrNewerSchema", err)
	}
}

// An event whose type this weft does not know fails Get with
// ErrUnknownEvent naming it — and List still returns the run, so an
// older Inspector shows the run and says why it cannot open it
// (ADR 0010 §2.5).
func TestUnknownEventLoudOnGet(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Save(ctx, store.RunRecord{
		ID: "run-future", Agent: "a", Started: time.Now().UTC(), Status: store.Succeeded,
	}); err != nil {
		t.Fatal(err)
	}
	db := s.(*Store).db
	if _, err := db.Exec(`INSERT INTO run_events (run_id, seq, event) VALUES ('run-future', 0, ?)`,
		`{"type":"future","run_id":"run-future"}`); err != nil {
		t.Fatal(err)
	}
	_, err = s.Get(ctx, "run-future")
	if !errors.Is(err, store.ErrUnknownEvent) {
		t.Fatalf("Get: err = %v, want ErrUnknownEvent", err)
	}
	if got := err.Error(); !strings.Contains(got, "future") {
		t.Errorf("error %q does not name the unknown type", got)
	}
	p, err := s.List(ctx, store.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 {
		t.Errorf("List Total = %d, want 1 — List never reads events", p.Total)
	}
}

// A second handle on the same file reads what the first wrote while it
// is still open — WAL concurrency, the Inspector's read path.
func TestTwoHandlesOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := w.Save(ctx, store.RunRecord{
		ID: "run-shared", Agent: "a", Started: time.Now().UTC(), Status: store.Succeeded,
		Events: []weft.Event{weft.RunStart{ID: "run-shared"}, weft.TextDelta{RunID: "run-shared", Text: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "run-shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 {
		t.Fatalf("second handle saw %d events, want 2", len(got.Events))
	}
	// A stale running row reads interrupted through the other handle.
	if err := w.Save(ctx, store.RunRecord{
		ID: "run-crash", Agent: "a", Started: time.Now().UTC().Add(-time.Hour),
		Heartbeat: time.Now().UTC().Add(-2 * store.HeartbeatTimeout), Status: store.Running,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = r.Get(ctx, "run-crash")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.Interrupted {
		t.Errorf("stale row through second handle = %q, want interrupted", got.Status)
	}
}
