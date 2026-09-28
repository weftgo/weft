package thread_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// A session entry marshals with its "type" discriminator — one JSON
// line per entry is the on-disk format (ADR 0011 §2) — and
// UnmarshalEntry restores the sealed type.
func ExampleMessageEntry() {
	e := thread.MessageEntry{
		ID:      "e_01J8X9M2K7QW4R5N8T6V2B3C4E",
		Created: time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC),
		Message: weft.User("Where is order 1234?"),
	}
	b, err := json.Marshal(e)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(b))
	restored, err := thread.UnmarshalEntry(b)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(restored.(thread.MessageEntry).Message.Role)
	// Output:
	// {"type":"message","id":"e_01J8X9M2K7QW4R5N8T6V2B3C4E","created":"2026-09-28T12:00:00.123456789Z","message":{"role":"user","content":[{"type":"text","text":"Where is order 1234?"}]}}
	// user
}

// A session's header is its first line: the weft envelope integer, the
// session's id and creation time, and — for a fork — where it came
// from (ADR 0011 §3, §6).
func ExampleHeader() {
	h := thread.Header{
		ID:      "s_01J8X9M2K7QW4R5N8T6V2B3C4Q",
		Created: time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC),
		Parent:  &thread.ParentRef{Session: "s_01J8X9M2K7QW4R5N8T6V2B3C4D", Entry: "e_01J8X9M2K7QW4R5N8T6V2B3C4G"},
	}
	b, err := json.Marshal(h)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(b))
	// Output:
	// {"type":"session","weft":1,"id":"s_01J8X9M2K7QW4R5N8T6V2B3C4Q","created":"2026-09-28T12:00:00.123456789Z","parent":{"session":"s_01J8X9M2K7QW4R5N8T6V2B3C4D","entry":"e_01J8X9M2K7QW4R5N8T6V2B3C4G"}}
}

// NewSessionID and NewEntryID mint time-sortable ids: "s_" and "e_"
// plus 26 characters, so a directory of sessions lists in creation
// order and a session's entries debug readably.
func ExampleNewSessionID() {
	fmt.Println(len(thread.NewSessionID()), len(thread.NewEntryID()))
	// Output: 28 28
}

// Memory is the in-process Storage: create a session, append entries,
// load them back — the shape every backend shares (threadtest pins the
// rest).
func ExampleMemory() {
	ctx := context.Background()
	st := thread.Memory()
	h := thread.Header{
		ID:      "s_demo",
		Created: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		Meta:    map[string]string{"room": "table-1"},
	}
	if err := st.Create(ctx, h); err != nil {
		fmt.Println(err)
		return
	}
	if err := st.Append(ctx, h.ID, thread.MessageEntry{
		ID:      "e_demo1",
		Created: h.Created.Add(time.Second),
		Message: weft.User("Where is order 1234?"),
	}); err != nil {
		fmt.Println(err)
		return
	}
	header, entries, report, err := st.Load(ctx, h.ID)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(header.ID, header.Meta["room"], len(entries), report)
	for _, e := range entries {
		switch e := e.(type) {
		case thread.MessageEntry:
			fmt.Println(e.Message.Role, "asks:", e.Message.Text())
		}
	}
	// Output:
	// s_demo table-1 1 <nil>
	// user asks: Where is order 1234?
}
