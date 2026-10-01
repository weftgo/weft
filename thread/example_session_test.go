package thread_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// exampleIDs mints the given ids in order — the IDs option's usual
// shape in an example: the session id first, then one per entry.
func exampleIDs(ids ...string) thread.SessionOption {
	next := 0
	return thread.IDs(func() string {
		id := ids[next]
		next++
		return id
	})
}

// exampleClock ticks one second per reading from a fixed instant, so
// every stored timestamp in an example is known.
func exampleClock() thread.SessionOption {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return thread.Clock(func() time.Time {
		now = now.Add(time.Second)
		return now
	})
}

// Open loads a stored session at its leaf. It reads and nothing else:
// no entry is written and no run starts, whatever the file holds.
func ExampleOpen() {
	ctx := context.Background()
	st := thread.Memory()
	model := wefttest.Script(wefttest.Say("Order 1234 shipped Tuesday."))
	agent := weft.New(model)

	s, err := thread.Create(ctx, st, agent, exampleIDs("s_orders", "e_prompt", "e_reply", "e_turn"))
	if err != nil {
		fmt.Println(err)
		return
	}
	turn, err := s.Send(ctx, weft.User("Where is order 1234?"))
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Close(ctx); err != nil { // one Session per session id
		fmt.Println(err)
		return
	}

	again, err := thread.Open(ctx, st, "s_orders", agent)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("leaf:", again.Leaf())
	for _, m := range again.Context() {
		fmt.Printf("%s: %s\n", m.Role, m.Text())
	}
	fmt.Println("model calls:", len(model.Requests()))

	_, err = thread.Open(ctx, st, "s_missing", agent)
	fmt.Println(errors.Is(err, thread.ErrNotFound))
	// Output:
	// leaf: e_turn
	// user: Where is order 1234?
	// assistant: Order 1234 shipped Tuesday.
	// model calls: 1
	// true
}

// List pages session headers, newest first — headers only, never
// entries.
func ExampleList() {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script())
	clock := exampleClock()
	for _, id := range []string{"s_monday", "s_tuesday", "s_wednesday"} {
		if _, err := thread.Create(ctx, st, agent, exampleIDs(id), clock); err != nil {
			fmt.Println(err)
			return
		}
	}

	page, err := thread.List(ctx, st, thread.Query{Limit: 2})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("total:", page.Total)
	for _, h := range page.Sessions {
		fmt.Println(h.ID, h.Created.Format(time.TimeOnly))
	}
	// The next page starts before the last header seen.
	next, err := thread.List(ctx, st, thread.Query{Limit: 2, Before: page.Sessions[1].Created})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, h := range next.Sessions {
		fmt.Println(h.ID, h.Created.Format(time.TimeOnly))
	}
	// Output:
	// total: 3
	// s_wednesday 09:00:03
	// s_tuesday 09:00:02
	// s_monday 09:00:01
}

// Delete removes a session and its entries. Close the Session first:
// Delete does not look for open ones.
func ExampleDelete() {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script())
	s, err := thread.Create(ctx, st, agent, exampleIDs("s_scratch"))
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Close(ctx); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(thread.Delete(ctx, st, "s_scratch"))
	_, err = thread.Open(ctx, st, "s_scratch", agent)
	fmt.Println(errors.Is(err, thread.ErrNotFound))
	fmt.Println(errors.Is(thread.Delete(ctx, st, "s_scratch"), thread.ErrNotFound))
	// Output:
	// <nil>
	// true
	// true
}

// Fork copies a path into a new, self-contained session. The fork
// takes its own header options and inherits what the copied path
// records — here the conversation and the title.
func ExampleSession_Fork() {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script(
		wefttest.Say("Draft one."),
		wefttest.Say("Draft two, in the fork."),
	))
	s, err := thread.Create(ctx, st, agent,
		exampleIDs("s_draft", "e_prompt", "e_reply", "e_turn", "e_title"), thread.PublicID("pub-original"))
	if err != nil {
		fmt.Println(err)
		return
	}
	turn, _ := s.Send(ctx, weft.User("Draft the intro."))
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	if err := s.SetInfo(ctx, "Intro drafts", nil); err != nil {
		fmt.Println(err)
		return
	}

	fork, err := s.Fork(ctx, s.Leaf(),
		exampleIDs("s_whatif", "e_fprompt", "e_freply", "e_fturn"), thread.PublicID("pub-whatif"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("fork:", fork.ID(), "title:", fork.Title(), "public id:", fork.Meta()["weft.public_id"])

	turn, _ = fork.Send(ctx, weft.User("Try another angle."))
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("run:", turn.RunID())
	fmt.Println("fork context:", len(fork.Context()), "messages; original:", len(s.Context()))

	page, _ := thread.List(ctx, st, thread.Query{Meta: map[string]string{"weft.public_id": "pub-whatif"}})
	h := page.Sessions[0]
	fmt.Println("forked from:", h.Parent.Session, "at", h.Parent.Entry)
	// Output:
	// fork: s_whatif title: Intro drafts public id: pub-whatif
	// run: s_whatif-t2
	// fork context: 4 messages; original: 2
	// forked from: s_draft at e_title
}

// WithMeta writes header metadata at Create — what List's Query.Meta
// matches. SetInfo overlays later edits in Session.Meta, except under
// the reserved "weft." prefix, which only the header may set.
func ExampleWithMeta() {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script())
	s, err := thread.Create(ctx, st, agent, exampleIDs("s_acme", "e_info"),
		thread.WithMeta(map[string]string{"tenant": "acme", "tier": "trial"}),
		thread.PublicID("pub-7f3a"))
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := s.SetInfo(ctx, "", map[string]string{"tier": "paid"}); err != nil {
		fmt.Println(err)
		return
	}
	meta := s.Meta()
	fmt.Println(meta["tenant"], meta["tier"], meta["weft.public_id"])

	err = s.SetInfo(ctx, "", map[string]string{"weft.public_id": "pub-other"})
	fmt.Println(errors.Is(err, thread.ErrReservedKey))

	// List matches the header: the create-time values.
	trial, _ := thread.List(ctx, st, thread.Query{Meta: map[string]string{"tier": "trial"}})
	paid, _ := thread.List(ctx, st, thread.Query{Meta: map[string]string{"tier": "paid"}})
	fmt.Println(trial.Total, paid.Total)

	// A header option handed to Open is refused, not dropped.
	_, err = thread.Open(ctx, st, "s_acme", agent, thread.WithMeta(map[string]string{"tenant": "other"}))
	fmt.Println(errors.Is(err, thread.ErrCreateOnly))
	// Output:
	// acme paid pub-7f3a
	// true
	// 1 0
	// true
}

// Entries is the whole tree in append order, abandoned branches
// included; Path is one line of it, from a root to an entry. Both
// return copies.
func ExampleSession_Path() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(wefttest.Say("First draft."), wefttest.Say("Second draft.")))
	s, err := thread.Create(ctx, thread.Memory(), agent, exampleIDs("s_tree",
		"e_p1", "e_r1", "e_t1", // turn 1
		"e_nav",                // the branch back to the prompt
		"e_p2", "e_r2", "e_t2", // turn 2, on the new branch
	))
	if err != nil {
		fmt.Println(err)
		return
	}
	turn, _ := s.Send(ctx, weft.User("Draft it."))
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Branch(ctx, "e_p1"); err != nil {
		fmt.Println(err)
		return
	}
	turn, _ = s.Send(ctx, weft.User("Shorter."))
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("entries:", len(s.Entries()))
	path, err := s.Path(s.Leaf())
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range path {
		switch e := e.(type) {
		case thread.MessageEntry:
			fmt.Printf("%s message %s: %s\n", e.ID, e.Message.Role, e.Message.Text())
		case thread.TurnEntry:
			fmt.Printf("%s turn %s\n", e.ID, e.RunID)
		}
	}
	// Output:
	// entries: 7
	// e_p1 message user: Draft it.
	// e_p2 message user: Shorter.
	// e_r2 message assistant: Second draft.
	// e_t2 turn s_tree-t2
}

// Label names an entry: a bookmark a UI lists and a later Branch or
// Fork can return to. Labels are entries; they never reach the model.
func ExampleSession_Label() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(wefttest.Say("Shipped Tuesday.")))
	s, err := thread.Create(ctx, thread.Memory(), agent,
		exampleIDs("s_labels", "e_prompt", "e_reply", "e_turn", "e_label"))
	if err != nil {
		fmt.Println(err)
		return
	}
	turn, _ := s.Send(ctx, weft.User("Where is order 1234?"))
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Label(ctx, "e_reply", "the shipping answer"); err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range s.Entries() {
		if l, ok := e.(thread.LabelEntry); ok {
			fmt.Printf("%s: %q names %s\n", l.ID, l.Name, l.Entry)
		}
	}
	fmt.Println("context:", len(s.Context()), "messages")
	fmt.Println(s.Label(ctx, "e_nowhere", "x"))
	// Output:
	// e_label: "the shipping answer" names e_reply
	// context: 2 messages
	// thread: session s_labels holds no entry "e_nowhere"
}

// Close quiesces a session: new Sends are refused at once, the running
// turn and the queue behind it finish, and then every write fails with
// ErrClosed while reads keep answering.
func ExampleSession_Close() {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script(wefttest.Say("One."), wefttest.Say("Two.")))
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		fmt.Println(err)
		return
	}
	first, _ := s.Send(ctx, weft.User("first"))
	second, _ := s.Send(ctx, weft.User("second")) // queued behind the first

	// Close waits for both turns; a deadline on ctx bounds the wait.
	fmt.Println("close:", s.Close(ctx))
	for _, turn := range []*thread.Turn{first, second} {
		res, err := turn.Wait()
		fmt.Println(res.Text(), err)
	}

	_, err = s.Send(ctx, weft.User("third"))
	fmt.Println(errors.Is(err, thread.ErrClosed))
	fmt.Println(errors.Is(s.Label(ctx, first.ID(), "late"), thread.ErrClosed))
	fmt.Println("still readable:", len(s.Context()), "messages")
	fmt.Println("close again:", s.Close(ctx))
	// Output:
	// close: <nil>
	// One. <nil>
	// Two. <nil>
	// true
	// true
	// still readable: 4 messages
	// close again: <nil>
}

// Continue runs what a session holds but is not running — here a
// steer a crashed writer had accepted and never settled. Open restores
// it to the queue and runs nothing; Continue is the caller saying go.
func ExampleSession_Continue() {
	ctx := context.Background()
	st := thread.Memory()
	model := wefttest.Script(wefttest.Say("Switched to metric."))
	agent := weft.New(model)
	s, err := thread.Create(ctx, st, agent, exampleIDs("s_crashed"))
	if err != nil {
		fmt.Println(err)
		return
	}
	// What the crash left: a queued receipt, and no entry settling it.
	steer := weft.User("Use metric units.")
	if err := st.Append(ctx, s.ID(), thread.ReceiptEntry{
		ID: "e_steer", Created: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		Status: thread.ReceiptQueued, Msg: &steer,
	}); err != nil {
		fmt.Println(err)
		return
	}

	// The crashed process took its hold on the session with it.
	_ = s.Close(ctx)
	open, err := thread.Open(ctx, st, "s_crashed", agent, exampleIDs("e_followup", "e_deferred", "e_reply", "e_turn"))
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, q := range open.Queue() {
		fmt.Println("restored:", q.Receipt, q.Msg.Text())
	}
	fmt.Println("model calls after Open:", len(model.Requests()))

	turn, err := open.Continue(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	res, err := turn.Wait()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(res.Text())
	fmt.Println("queue:", len(open.Queue()))
	// Output:
	// restored: e_steer Use metric units.
	// model calls after Open: 0
	// Switched to metric.
	// queue: 0
}

// Salvage loads a session whose file has a damaged line instead of
// refusing it, and LoadReport says exactly what that cost: the line
// skipped, and the entries left without the parent it held. They are
// kept — the context starts at the orphan.
func ExampleSalvage() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "weft-salvage-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	agent := weft.New(wefttest.Script())
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	// A session of three messages, the middle line destroyed on disk.
	st, err := jsonl.Open(dir)
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := thread.Create(ctx, st, agent, exampleIDs("s_damaged")); err != nil {
		fmt.Println(err)
		return
	}
	damage := st.(interface {
		Inject(ctx context.Context, session string, data []byte) error
	})
	_ = st.Append(ctx, "s_damaged", thread.MessageEntry{ID: "e_1", Created: at, Message: weft.User("one")})
	_ = damage.Inject(ctx, "s_damaged", []byte("{\"type\":\"message\",\"id\":\"e_2\",#!\n"))
	_ = st.Append(ctx, "s_damaged", thread.MessageEntry{ID: "e_3", ParentID: "e_2", Created: at, Message: weft.User("three")})

	_, err = thread.Open(ctx, st, "s_damaged", agent)
	fmt.Println("without Salvage:", errors.Is(err, thread.ErrCorrupt))

	salvaging, err := jsonl.Open(dir, thread.Salvage())
	if err != nil {
		fmt.Println(err)
		return
	}
	s, err := thread.Open(ctx, salvaging, "s_damaged", agent)
	if err != nil {
		fmt.Println(err)
		return
	}
	report := s.LoadReport()
	fmt.Println("skipped lines:", report.Skipped)
	fmt.Println("orphaned entries:", report.Orphaned)
	fmt.Println("entries kept:", len(s.Entries()))
	for _, m := range s.Context() {
		fmt.Println("context:", m.Text())
	}
	// Output:
	// without Salvage: true
	// skipped lines: [3]
	// orphaned entries: [e_3]
	// entries kept: 2
	// context: three
}

// Clock pins the session's time source, the way IDs pins its ids:
// the header and every entry the session appends read it.
func ExampleClock() {
	ctx := context.Background()
	st := thread.Memory()
	s, err := thread.Create(ctx, st, weft.New(wefttest.Script()),
		exampleIDs("s_clock", "e_note"), exampleClock())
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := s.Custom(ctx, "note", []byte(`{"pinned":true}`)); err != nil {
		fmt.Println(err)
		return
	}
	page, _ := thread.List(ctx, st, thread.Query{})
	fmt.Println("header:", page.Sessions[0].Created.Format(time.RFC3339))
	fmt.Println("entry: ", s.Entries()[0].(thread.CustomEntry).Created.Format(time.RFC3339))
	// Output:
	// header: 2026-10-01T09:00:01Z
	// entry:  2026-10-01T09:00:02Z
}
