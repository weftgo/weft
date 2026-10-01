package jsonl_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// A writer that died mid-append leaves a torn final line. Load reports
// it; the next writer removes it before appending — and says so
// through the logger named at open — so the session loads clean
// afterwards.
func ExampleOpen_tornTail() {
	dir, err := os.MkdirTemp("", "weft-jsonl-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// The storage reports its repairs here; the timestamp is dropped
	// so the example's output is stable.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	st, err := jsonl.Open(dir, thread.OpenLogger(logger))
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
	// The crash: the writer lets go, half a line in the file.
	if err := st.(thread.Releaser).Release(ctx, "s_demo"); err != nil {
		fmt.Println(err)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "s_demo.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		fmt.Println(err)
		return
	}
	_, _ = f.WriteString(`{"type":"mess`)
	_ = f.Close()

	_, _, report, _ := st.Load(ctx, "s_demo")
	fmt.Println("torn line:", report.Torn)

	if err := st.Append(ctx, "s_demo", thread.MessageEntry{
		ID: "e_1", Created: created.Add(time.Second), Message: weft.User("after the crash"),
	}); err != nil {
		fmt.Println(err)
		return
	}
	_, entries, report, err := st.Load(ctx, "s_demo")
	fmt.Println(len(entries), report == nil, err)
	// Output:
	// torn line: 2
	// level=WARN msg="thread/jsonl: removed a torn tail before appending" session=s_demo dropped_bytes=13
	// 1 true <nil>
}

// NoLock opens the directory without the cross-process writer lock:
// the caller promises one writer per session, and a second Storage is
// no longer refused. It is what a platform without file locks needs —
// there Open fails without it — and a choice everywhere else.
func ExampleOpen_noLock() {
	dir, err := os.MkdirTemp("", "weft-jsonl-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	first, err := jsonl.Open(dir, thread.NoLock())
	if err != nil {
		fmt.Println(err)
		return
	}
	second, err := jsonl.Open(dir, thread.NoLock())
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
	err = second.Append(ctx, "s_shared", thread.MessageEntry{ID: "e_1", Created: created, Message: weft.User("unrefused")})
	fmt.Println("second writer:", err)
	// Output:
	// second writer: <nil>
}
