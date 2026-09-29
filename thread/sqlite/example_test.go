package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/sqlite"
)

// Open a sessions database, write one turn's worth of entries, read the
// session back. ":memory:" keeps the example self-contained; a path
// gives the same answers on disk.
func ExampleOpen() {
	st, err := sqlite.Open(":memory:")
	if err != nil {
		fmt.Println(err)
		return
	}
	ctx := context.Background()
	created := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	if err := st.Create(ctx, thread.Header{ID: "s_demo", Created: created}); err != nil {
		fmt.Println(err)
		return
	}
	if err := st.Append(ctx, "s_demo",
		thread.MessageEntry{
			ID: "e_1", Created: created.Add(time.Second), Message: weft.User("Where is order 1234?"),
		},
		thread.TurnEntry{
			ID: "e_2", Created: created.Add(2 * time.Second),
			RunID: "s_demo-t1", StopReason: weft.StopEndTurn, Steps: 1,
		},
	); err != nil {
		fmt.Println(err)
		return
	}
	h, entries, report, err := st.Load(ctx, "s_demo")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(h.ID, len(entries), entries[0].(thread.MessageEntry).Message.Text(), report == nil)
	// Output:
	// s_demo 2 Where is order 1234? true
}

// The one-writer rule is per session and crosses processes: a second
// Storage over the same file — here in the same process, the cheapest
// way to show it — is refused with thread.ErrLocked until the holder
// lets go, while Load keeps answering: readers never lock.
func ExampleOpen_secondWriter() {
	dir, err := os.MkdirTemp("", "weft-sqlite-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx := context.Background()
	path := filepath.Join(dir, "sessions.db")

	holder, err := sqlite.Open(path)
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := holder.Create(ctx, thread.Header{ID: "s_demo", Created: time.Now().UTC()}); err != nil {
		fmt.Println(err)
		return
	}
	second, err := sqlite.Open(path)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = second.Append(ctx, "s_demo", thread.MessageEntry{
		ID: "e_1", Created: time.Now().UTC(), Message: weft.User("second writer"),
	})
	fmt.Println(errors.Is(err, thread.ErrLocked))
	// Output:
	// true
}
