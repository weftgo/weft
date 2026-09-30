package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/weftgo/weft"
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
			e := thread.MessageEntry{Message: weft.User("tick " + strconv.Itoa(j))}
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

// The sqlite half of the budget suite (plan §10, step 7.2): the same
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
	st, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	buildBigSession(t, st, "s_big", 100_000)

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

// budgetList10k prices one List call over a 10k fleet — a page, the
// count, the title search, and a meta filter — not a full cursor walk:
// this backend's List is a fleet scan per call (every header decoded
// and sorted in Go, then paged — Meta filtering needs the decoded
// header, so the scan is the documented cost, and SQL-side paging
// wants the v0.8 freeze's index work). A full walk pays the scan per
// page, which is the caller's choice to make; Total answers "how
// many" in one call. The timed part measured 2026-09-30 at well under
// a second per call.
const budgetList10k = 15 * time.Second

func TestBudgetList10k(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "fleet.db")
	st, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10_000; i++ {
		h := thread.Header{ID: "s_l" + strconv.Itoa(i), Created: time.Now().UTC()}
		if err := st.Create(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	page, err := thread.List(ctx, st, thread.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 100 || page.Total != 10_000 {
		t.Fatalf("page = %d sessions, total %d; want 100 of 10000", len(page.Sessions), page.Total)
	}
	if _, err := thread.List(ctx, st, thread.Query{Limit: 100, TitleSearch: "s_l1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := thread.List(ctx, st, thread.Query{Limit: 100, Meta: map[string]string{"tier": "gold"}}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > budgetList10k {
		t.Errorf("list over 10k (page + count + title + meta) = %s, budget %s", d, budgetList10k)
	}
}
