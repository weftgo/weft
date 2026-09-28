package thread_test

import (
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
