package thread_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
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

// A session carries the conversation: application state survives
// outside the model's context, application messages ride inside it,
// and the title and metadata are editable appends. The IDs option pins
// deterministic ids so the output is stable.
func ExampleCreate() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script()) // no run happens here; Send arrives in step 1.7
	st := thread.Memory()

	next := 0
	ids := []string{"s_demo1", "e_demo2", "e_demo3", "e_demo4", "e_demo5"}
	deterministic := thread.IDs(func() string { id := ids[next]; next++; return id })

	s, err := thread.Create(ctx, st, agent, deterministic)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("session", s.ID())
	if err := s.SetInfo(ctx, "Order support", map[string]string{"team": "ops"}); err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Custom(ctx, "cart", json.RawMessage(`{"items":2}`)); err != nil {
		fmt.Println(err)
		return
	}
	if err := s.CustomMessage(ctx, "note", weft.User("The customer's quote covers two items.")); err != nil {
		fmt.Println(err)
		return
	}

	again, err := thread.Open(ctx, st, s.ID(), agent, thread.IDs(func() string { return "e_demo6" }))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(again.Title(), again.Meta()["team"])
	for _, m := range again.Context() {
		fmt.Println(m.Role, ":", m.Text())
	}
	// Output:
	// session s_demo1
	// Order support ops
	// user : The customer's quote covers two items.
}

// The tree in memory and the session in storage agree, and the leaf is
// where the next entry attaches: Entries walks the whole tree, Path
// one root-to-entry chain, and Usage reads the cost ledger.
func ExampleSession_Usage() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_ledger" }))
	if err != nil {
		fmt.Println(err)
		return
	}
	// A finished turn, recorded the way Send will from step 1.7.
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_q", Created: time.Now().UTC(), Message: weft.User("Summarize the plan.")},
		thread.TurnEntry{
			ID: "e_t", ParentID: "e_q", Created: time.Now().UTC(),
			RunID: "s_ledger-t1", StopReason: weft.StopEndTurn,
			Usage: weft.Usage{InputTokens: 120, OutputTokens: 30},
		},
	); err != nil {
		fmt.Println(err)
		return
	}
	open, err := thread.Open(ctx, st, "s_ledger", agent)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("entries:", len(open.Entries()), "leaf:", open.Leaf())
	fmt.Println("path:", len(mustPath(open, "e_t")))
	u := open.Usage()
	fmt.Println("turn input tokens:", u.Turns.InputTokens, "summary tokens:", u.Summaries.InputTokens)
	// Output:
	// entries: 2 leaf: e_t
	// path: 2
	// turn input tokens: 120 summary tokens: 0
}

func mustPath(s *thread.Session, id string) []thread.Entry {
	path, err := s.Path(id)
	if err != nil {
		panic(err)
	}
	return path
}

// Branch navigates the tree and Fork copies a path into a new session:
// the branch's context drops the abandoned entries, the fork carries
// the whole copied path and names its origin in its header.
func ExampleSession_Branch() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	next := 0
	ids := []string{"s_nav", "e_nav"}
	s, err := thread.Create(ctx, st, agent, thread.IDs(func() string { id := ids[next]; next++; return id }))
	if err != nil {
		fmt.Println(err)
		return
	}
	// Two messages of history, appended the way Send will from step 1.7.
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: weft.User("draft the intro")},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: time.Now().UTC(), Message: weft.Assistant("done")},
	); err != nil {
		fmt.Println(err)
		return
	}
	s, err = thread.Open(ctx, st, s.ID(), agent, thread.IDs(func() string { id := ids[next]; next++; return id }))
	if err != nil {
		fmt.Println(err)
		return
	}

	// Navigate back to the first entry: the abandoned reply drops out
	// of the context, and the file keeps it.
	if err := s.Branch(ctx, "e_1"); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("branch context:", len(s.Context()))

	// Fork the full path into a new session: self-contained, its
	// header naming where it grew from.
	f, err := s.Fork(ctx, "e_2", thread.IDs(func() string { return "s_fork" }))
	if err != nil {
		fmt.Println(err)
		return
	}
	page, _ := thread.List(ctx, st, thread.Query{})
	for _, h := range page.Sessions {
		if h.ID == "s_fork" && h.Parent != nil {
			fmt.Println("fork of", h.Parent.Session, "at", h.Parent.Entry, "carries", len(f.Context()), "messages")
		}
	}
	// Output:
	// branch context: 1
	// fork of s_nav at e_2 carries 2 messages
}

// Send runs a turn: the prompt is durable before the run starts, the
// reply and the turn's ledger land after it, and a reopen sees the
// whole conversation.
func ExampleSession_Send() {
	ctx := context.Background()
	// A scripted model makes the example deterministic and offline.
	agent := weft.New(wefttest.Script(wefttest.Say("It shipped Tuesday."), wefttest.Say("Order 1234, two items.")))
	st := thread.Memory()
	next := 0
	ids := []string{"s_demo", "e_1", "e_2", "e_3", "e_4", "e_5", "e_6"}
	s, _ := thread.Create(ctx, st, agent, thread.IDs(func() string { id := ids[next]; next++; return id }))

	t1, err := s.Send(ctx, weft.User("Where is order 1234?"))
	if err != nil {
		fmt.Println(err)
		return
	}
	res, err := t1.Wait()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("run", t1.RunID(), "replied:", res.Text())

	t2, err := s.Send(ctx, weft.User("And what was in it?"))
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := t2.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	again, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range again.Context() {
		if m.Text() != "" {
			fmt.Println(m.Role, ":", m.Text())
		}
	}
	// Output:
	// run s_demo-t1 replied: It shipped Tuesday.
	// user : Where is order 1234?
	// assistant : It shipped Tuesday.
	// user : And what was in it?
	// assistant : Order 1234, two items.
}

// Compaction summarizes the old part of a session and keeps the recent
// part raw — nothing is deleted, and Uncompact branches right back.
func ExampleSession_Compact() {
	ctx := context.Background()
	// The same model summarizes; a script makes it deterministic.
	rec := &recordingModel{reply: "Goal: ship the order service."}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)

	// A long history, appended the way Send does.
	msgs := []string{strings.Repeat("order ", 12_000), strings.Repeat("invoice ", 12_000), strings.Repeat("refund ", 12_000)}
	parent := ""
	var batch []thread.Entry
	for i, text := range msgs {
		id := fmt.Sprintf("e_%d", i)
		batch = append(batch, thread.MessageEntry{ID: id, ParentID: parent, Created: time.Now().UTC(), Message: weft.User(text)})
		parent = id
	}
	if err := st.Append(ctx, s.ID(), batch...); err != nil {
		fmt.Println(err)
		return
	}
	s, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Compact(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("after Compact:", len(s.Context()), "messages")
	fmt.Println("summary rides first:", strings.HasPrefix(s.Context()[0].Text(), "<weft-summary>"))
	fmt.Println("kept raw:", len(s.Entries()) > 3)
	if err := s.Uncompact(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("after Uncompact:", len(s.Context()), "messages")
	// Output:
	// after Compact: 2 messages
	// summary rides first: true
	// kept raw: true
	// after Uncompact: 3 messages
}

// A session under the Reject busy policy says ErrBusy instead of
// holding a follow-up while a turn runs.
func ExampleBusyPolicy() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	s, _ := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Reject))
	_ = s
	// With a turn in flight (a blocking tool, say):
	//   if _, err := s.Send(ctx, weft.User("one more thing")); errors.Is(err, thread.ErrBusy) { … }
	fmt.Println("Queue is the default; Reject is one option away")
	// Output:
	// Queue is the default; Reject is one option away
}

// Pin keeps an entry in the context through every compaction — the
// requirement, the key decision — recorded as a reserved custom entry
// that survives compaction the way all custom state does.
func ExampleSession_Pin() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	if err := st.Append(ctx, s.ID(), thread.MessageEntry{
		ID: "e_req", Created: time.Now().UTC(),
		Message: weft.User("THE REQUIREMENT: ship by Friday"),
	}); err != nil {
		fmt.Println(err)
		return
	}
	open, _ := thread.Open(ctx, st, s.ID(), agent)
	if err := open.Pin(ctx, "e_req"); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("pinned")
	// Output:
	// pinned
}
