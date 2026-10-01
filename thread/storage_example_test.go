package thread_test

import (
	"context"
	"fmt"
	"time"

	"github.com/weftgo/weft/thread"
)

// Page through every session with the keyset cursor: hand List the
// last session of the previous page — its Created as Before, its ID as
// BeforeID — and the next page starts right after it, even when
// sessions share a creation time. Before alone would skip the rest of
// such a group.
func ExampleQuery_keysetCursor() {
	ctx := context.Background()
	st := thread.Memory()
	imported := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"s_a", "s_b", "s_c", "s_d", "s_e"} {
		// A bulk import: five sessions, one creation time.
		if err := st.Create(ctx, thread.Header{ID: id, Created: imported}); err != nil {
			fmt.Println(err)
			return
		}
	}
	q := thread.Query{Limit: 2}
	for {
		page, err := thread.List(ctx, st, q)
		if err != nil {
			fmt.Println(err)
			return
		}
		if len(page.Sessions) == 0 {
			break
		}
		for _, h := range page.Sessions {
			fmt.Print(h.ID, " ")
		}
		fmt.Println("of", page.Total)
		last := page.Sessions[len(page.Sessions)-1]
		q.Before, q.BeforeID = last.Created, last.ID
	}
	// Output:
	// s_e s_d of 5
	// s_c s_b of 5
	// s_a of 5
}

// Release a session when its writer is done with it: a backend that
// locks (jsonl, sqlite) lets go of the session so another Storage or
// process may write it; one that does not (Memory) only checks the
// session exists. The capability is discovered by type assertion.
func ExampleReleaser() {
	ctx := context.Background()
	st := thread.Memory()
	if err := st.Create(ctx, thread.Header{ID: "s_done", Created: time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)}); err != nil {
		fmt.Println(err)
		return
	}
	if r, ok := st.(thread.Releaser); ok {
		fmt.Println("released:", r.Release(ctx, "s_done"))
		fmt.Println("unknown session:", r.Release(ctx, "s_never"))
	}
	// Output:
	// released: <nil>
	// unknown session: thread: session not found: s_never
}
