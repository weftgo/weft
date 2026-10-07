package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/sqlite"
)

// buildBigSession writes n message entries in batches through the
// backend's public Append — jsonl's budget file carries the same
// builder; each backend's module is its own package.
func buildBigSession(tb testing.TB, st thread.Storage, id string, n int) {
	tb.Helper()
	ctx := context.Background()
	if err := st.Create(ctx, thread.Header{ID: id, Created: time.Now().UTC()}); err != nil {
		tb.Fatal(err)
	}
	const batch = 500
	parent := ""
	for i := 0; i < n; i += batch {
		end := i + batch
		if end > n {
			end = n
		}
		entries := make([]thread.Entry, 0, end-i)
		for j := i; j < end; j++ {
			e := thread.MessageEntry{Message: core.User("tick " + strconv.Itoa(j))}
			e.ID = fmt.Sprintf("e_%06d", j)
			e.ParentID = parent
			e.Created = time.Now().UTC()
			entries = append(entries, e)
			parent = e.ID
		}
		if err := st.Append(ctx, id, entries...); err != nil {
			tb.Fatal(err)
		}
	}
}

// The sqlite half of the budget suite: the same
// 100k-entry open as jsonl's (the second backend's decode path is the
// database's, and its own cost to bound), and List over 10k sessions —
// the paged walk and the title filter that migration 0002 denormalised
// for exactly this.

// budgetOpen100k mirrors jsonl's: measured 2026-09-30 in the same
// class (~3s, the decode path is shared); the bound leaves CI
// hardware its margin.
const budgetOpen100k = 30 * time.Second

func TestBudgetOpen100k(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "big.db")
	st, err := sqlite.Open(db, thread.FsyncOnFlush()) // a fixture: bulk-loaded
	if err != nil {
		t.Fatal(err)
	}
	buildBigSession(t, st, "s_big", 100_000)
	if err := st.(thread.Flusher).Flush(ctx, "s_big"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	fresh, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	_, entries, _, err := fresh.Load(ctx, "s_big")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > budgetOpen100k {
		t.Errorf("opening a 100k-entry session = %s, budget %s", d, budgetOpen100k)
	}
	if len(entries) != 100_000 {
		t.Fatalf("loaded %d entries, want 100000", len(entries))
	}
}

// budgetList10k prices List over a 10k fleet: the full keyset walk —
// a hundred pages of a hundred, each an index range read plus the
// count — then the title search and a meta filter. The page no longer
// costs a scan of the fleet (it used to decode and sort every header
// per call, which made a full walk quadratic); what still reads every
// row is the count, over an index, and a title search's pass over the
// title column. Measured 2026-10-01 at ~0.17s for the whole walk, ~7s
// under -race (the driver is transpiled C, and the detector prices
// it); the bound covers a loaded box running the latter.
const budgetList10k = 30 * time.Second

func TestBudgetList10k(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "fleet.db")
	// The fleet is a fixture: seeded under FsyncOnFlush, the policy
	// for bulk loads — the default fsyncs each of the ten thousand
	// Creates, which is the durability a live session wants and
	// seconds of disk time the list budget is not about.
	st, err := sqlite.Open(db, thread.FsyncOnFlush())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10_000; i++ {
		h := thread.Header{ID: "s_l" + strconv.Itoa(i), Created: time.Now().UTC()}
		if i == 0 {
			h.Meta = map[string]string{"tier": "gold"}
		}
		if err := st.Create(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	seen := 0
	q := thread.Query{Limit: 100}
	for {
		page, err := thread.List(ctx, st, q)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 10_000 {
			t.Fatalf("Total = %d, want 10000", page.Total)
		}
		if len(page.Sessions) == 0 {
			break
		}
		seen += len(page.Sessions)
		last := page.Sessions[len(page.Sessions)-1]
		q.Before, q.BeforeID = last.Created, last.ID
	}
	if seen != 10_000 {
		t.Fatalf("the keyset walk saw %d sessions, want 10000", seen)
	}
	if _, err := thread.List(ctx, st, thread.Query{Limit: 100, TitleSearch: "s_l1"}); err != nil {
		t.Fatal(err)
	}
	gold, err := thread.List(ctx, st, thread.Query{Limit: 100, Meta: map[string]string{"tier": "gold"}})
	if err != nil || gold.Total != 1 || len(gold.Sessions) != 1 {
		t.Fatalf("meta filter over the fleet: %d of %d, err %v; want the one gold session", len(gold.Sessions), gold.Total, err)
	}
	d := time.Since(start)
	t.Logf("list over 10k (full walk + title + meta) = %s", d)
	if d > budgetList10k {
		t.Errorf("list over 10k (full walk + title + meta) = %s, budget %s", d, budgetList10k)
	}
}
