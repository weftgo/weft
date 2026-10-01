package jsonl_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// Open a session directory, write one turn's worth of entries, read
// the session back — and look at the file: one line per record, the
// header first.
func ExampleOpen() {
	dir, err := os.MkdirTemp("", "weft-jsonl-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	st, err := jsonl.Open(filepath.Join(dir, "sessions"))
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

	f, err := os.Open(filepath.Join(dir, "sessions", "s_demo.jsonl"))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = f.Close() }()
	lines := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		lines++
	}
	fmt.Println("lines in s_demo.jsonl:", lines)
	// Output:
	// s_demo 2 Where is order 1234? true
	// lines in s_demo.jsonl: 3
}

// The writer's hold on a session is a lease: a second Storage over the
// same directory — another process, in real life — is refused with
// thread.ErrLocked until the holder releases the session, and then
// writes it.
func ExampleOpen_release() {
	dir, err := os.MkdirTemp("", "weft-jsonl-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	first, err := jsonl.Open(dir)
	if err != nil {
		fmt.Println(err)
		return
	}
	second, err := jsonl.Open(dir)
	if err != nil {
		fmt.Println(err)
		return
	}
	ctx := context.Background()
	created := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	if err := first.Create(ctx, thread.Header{ID: "s_shared", Created: created}); err != nil {
		fmt.Println(err)
		return
	}
	note := thread.MessageEntry{ID: "e_1", Created: created, Message: weft.User("from the second writer")}

	err = second.Append(ctx, "s_shared", note)
	fmt.Println("while held:", errors.Is(err, thread.ErrLocked))

	if err := first.(thread.Releaser).Release(ctx, "s_shared"); err != nil {
		fmt.Println(err)
		return
	}
	err = second.Append(ctx, "s_shared", note)
	fmt.Println("after release:", err)
	// Output:
	// while held: true
	// after release: <nil>
}
