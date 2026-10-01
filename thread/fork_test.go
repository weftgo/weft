package thread_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// Fork honours the header options exactly as Create does — metadata,
// the public id, the lineage — and inherits nothing from the
// original's header.
func TestForkHonoursHeaderOptions(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, err := thread.Create(ctx, st, agent, thread.PublicID("pub-origin"),
			thread.WithMeta(map[string]string{"tenant": "acme"}))
		if err != nil {
			t.Fatal(err)
		}
		s, ids := msgs(t, ctx, st, s, "one", "two")
		f, err := s.Fork(ctx, ids[1],
			thread.PublicID("pub-fork"),
			thread.WithMeta(map[string]string{"purpose": "what-if"}),
			thread.WithLineage("s_pool_parent", "call_7"))
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		want := map[string]string{"weft.public_id": "pub-fork", "purpose": "what-if"}
		for _, sess := range []*thread.Session{f, reopen(t, ctx, st, f)} {
			got := sess.Meta()
			if len(got) != len(want) || got["weft.public_id"] != "pub-fork" || got["purpose"] != "what-if" {
				t.Errorf("fork Meta = %v, want %v — the options' and nothing of the original's header", got, want)
			}
			if lin := sess.Lineage(); lin.Session != "s_pool_parent" || lin.Call != "call_7" {
				t.Errorf("fork Lineage = %+v, want the option's", lin)
			}
		}
		h := headerOf(t, ctx, st, f.ID())
		if h.Parent == nil || h.Parent.Session != s.ID() || h.Parent.Entry != ids[1] {
			t.Errorf("fork header parent = %+v", h.Parent)
		}
		// List finds the fork by its own public id, and only it.
		p, err := thread.List(ctx, st, thread.Query{Meta: map[string]string{"weft.public_id": "pub-fork"}})
		if err != nil || p.Total != 1 || p.Sessions[0].ID != f.ID() {
			t.Errorf("List by the fork's public id = %+v, %v", p, err)
		}
		// A plain fork carries no header metadata at all.
		plain, err := s.Fork(ctx, ids[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := plain.Meta(); len(got) != 0 {
			t.Errorf("plain fork Meta = %v, want none — the original's public id is not inherited", got)
		}
	})
}

// A fork inherits what its copied path records: the title and the
// SetInfo metadata, labels, the turn ledger.
func TestForkInheritsThePath(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("first answer"), wefttest.Say("second answer"), wefttest.Say("in the fork")))
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		turn, _ := s.Send(ctx, weft.User("hello"))
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.SetInfo(ctx, "Order 1234", map[string]string{"team": "support"}); err != nil {
			t.Fatal(err)
		}
		at := s.Leaf()
		turn2, _ := s.Send(ctx, weft.User("after the fork point"))
		if _, err := turn2.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.SetInfo(ctx, "Renamed later", nil); err != nil {
			t.Fatal(err)
		}

		f, err := s.Fork(ctx, at)
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		if f.Leaf() != at {
			t.Errorf("fork Leaf = %q, want %q", f.Leaf(), at)
		}
		if got := f.Title(); got != "Order 1234" {
			t.Errorf("fork Title = %q, want the title as of the fork point", got)
		}
		if got := f.Meta()["team"]; got != "support" {
			t.Errorf("fork Meta[team] = %q, want support", got)
		}
		if got, want := f.Usage().Turns, s.Usage().Turns; got == (weft.Usage{}) || got == want {
			t.Errorf("fork Usage.Turns = %+v, want the one copied turn's (origin has %+v)", got, want)
		}
		// The run ids continue after the copied turn, live and reopened.
		ft, err := f.Send(ctx, weft.User("go on"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ft.Wait(); err != nil {
			t.Fatal(err)
		}
		if want := f.ID() + "-t2"; ft.RunID() != want {
			t.Errorf("fork's first run id = %q, want %q", ft.RunID(), want)
		}
	})
}

// A fork is a navigation target's copy, not a navigation: forking at a
// leaf entry is refused, because the copy would reopen somewhere other
// than where Fork returned it.
func TestForkRejectsLeafEntry(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()), thread.IDs(counter("e_")))
	if err := s.Custom(ctx, "a", nil); err != nil { // e_2
		t.Fatal(err)
	}
	if err := s.Custom(ctx, "b", nil); err != nil { // e_3
		t.Fatal(err)
	}
	if err := s.Branch(ctx, "e_2"); err != nil { // e_4, the leaf entry
		t.Fatal(err)
	}
	_, err := s.Fork(ctx, "e_4")
	if err == nil || !strings.Contains(err.Error(), "leaf entry") {
		t.Fatalf("Fork at a leaf entry: err = %v, want a refusal naming it", err)
	}
	if p, _ := thread.List(ctx, st, thread.Query{}); p.Total != 1 {
		t.Errorf("the refused Fork left %d sessions, want 1", p.Total)
	}
}

// A steer still queued on the copied path belongs to the original:
// the fork records it dropped, so neither the fork nor a reopen of it
// ever restores or runs it, while the original still does.
func TestForkDoesNotInheritQueuedSteers(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		model := wefttest.Script(wefttest.Say("ran the steer"))
		agent := weft.New(model)
		s, _ := thread.Create(ctx, st, agent)
		msg := weft.User("steer me")
		if err := st.Append(ctx, s.ID(),
			userEntry("e_m1", "", "one"),
			thread.ReceiptEntry{ID: "e_steer", ParentID: "e_m1", Created: time.Unix(1, 0).UTC(),
				Status: thread.ReceiptQueued, Msg: &msg},
			userEntry("e_m2", "e_steer", "two"),
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent)
		if q := s.Queue(); len(q) != 1 {
			t.Fatalf("origin Queue = %+v, want the restored steer", q)
		}

		f, err := s.Fork(ctx, "e_m2", thread.IDs(counter("f_")))
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		for name, sess := range map[string]*thread.Session{"fork": f, "reopened fork": reopenWith(t, ctx, st, f, agent)} {
			if q := sess.Queue(); len(q) != 0 {
				t.Errorf("%s Queue = %+v, want empty", name, q)
			}
			if got := receiptStatus(receipts(sess))["e_steer"]; got != thread.ReceiptDropped {
				t.Errorf("%s: the copied steer's receipt = %q, want dropped", name, got)
			}
			if got := contextTexts(sess); !equalStrings(got, []string{"one", "two"}) {
				t.Errorf("%s Context = %v, want the copied path's", name, got)
			}
			if turn, err := sess.Continue(ctx); err != nil || turn != nil {
				t.Errorf("%s Continue = %v, %v; want nothing to run", name, turn, err)
			}
		}
		if n := len(model.Requests()); n != 0 {
			t.Fatalf("forking ran the model %d times", n)
		}
		// The original still owns its steer.
		turn, err := s.Continue(ctx)
		if err != nil || turn == nil {
			t.Fatalf("origin Continue = %v, %v", turn, err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		if !steerInContext(s, "steer me") {
			t.Error("the original lost its steer to the fork")
		}
	})
}

// Pool state on the copied path is neutralised: mirrored child
// requests are left out — the fork cannot decide the original's
// children — and an unsettled delegation is settled as canceled.
func TestForkNeutralisesPoolState(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent, thread.IDs(counter("o_")))
		if err := s.CustomMessage(ctx, "note", weft.User("before")); err != nil { // o_2
			t.Fatal(err)
		}
		settled, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{Status: thread.PoolAccepted, Child: "s_done"}) // o_3
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{ // o_4
			Receipt: settled.ID, Status: thread.PoolDone, Child: "s_done", Stop: "answer",
			Usage: weft.Usage{InputTokens: 5},
		}); err != nil {
			t.Fatal(err)
		}
		open, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{Status: thread.PoolAccepted, Child: "s_child"}) // o_5
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendApprovalRequests(ctx, // o_6, o_7
			thread.ApprovalRequestEntry{CallID: "s_child/call_1", Tool: "refund", Child: "s_child"},
			thread.ApprovalRequestEntry{CallID: "s_child/call_2", Tool: "refund", Child: "s_child"},
		); err != nil {
			t.Fatal(err)
		}
		if err := s.CustomMessage(ctx, "note", weft.User("after")); err != nil { // o_8
			t.Fatal(err)
		}
		if n := len(s.Pending()); n != 2 {
			t.Fatalf("origin Pending = %d, want the 2 mirrors", n)
		}

		f, err := s.Fork(ctx, s.Leaf(), thread.IDs(counter("f_")))
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		for name, sess := range map[string]*thread.Session{"fork": f, "reopened fork": reopen(t, ctx, st, f)} {
			if p := sess.Pending(); len(p) != 0 {
				t.Errorf("%s Pending = %+v, want none — the mirrors are the original's", name, p)
			}
			var canceled, done int
			for _, e := range sess.Entries() {
				switch e := e.(type) {
				case thread.ApprovalRequestEntry:
					t.Errorf("%s holds mirror %s", name, e.CallID)
				case thread.PoolReceiptEntry:
					if e.Receipt == open.ID && e.Status == thread.PoolCanceled && strings.HasPrefix(e.Stop, "forked") {
						canceled++
					}
					if e.Receipt == settled.ID && e.Status == thread.PoolDone {
						done++
					}
				}
			}
			if canceled != 1 || done != 1 {
				t.Errorf("%s: canceled settlements = %d, copied settlements = %d; want 1 and 1", name, canceled, done)
			}
			if got := contextTexts(sess); !equalStrings(got, []string{"before", "after"}) {
				t.Errorf("%s Context = %v, want [before after] — the chain re-linked across the mirrors", name, got)
			}
			// The settled delegation's cost is the copied path's; the
			// canceled one adds nothing.
			if got := sess.Usage().Delegated.InputTokens; got != 5 {
				t.Errorf("%s Usage.Delegated = %d input tokens, want 5", name, got)
			}
		}
		// The original is untouched.
		if n := len(s.Pending()); n != 2 {
			t.Errorf("origin Pending after the fork = %d, want 2", n)
		}
	})
}

// failAppendTo fails every Append to one session id.
type failAppendTo struct {
	thread.Storage
	prefix string
}

func (f *failAppendTo) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if strings.HasPrefix(session, f.prefix) {
		return errors.New("disk full")
	}
	return f.Storage.Append(ctx, session, entries...)
}

// A fork whose entries cannot be written does not leave a half-made
// session behind.
func TestForkFailureLeavesNoSession(t *testing.T) {
	ctx := context.Background()
	st := &failAppendTo{Storage: thread.Memory(), prefix: "s_fork"}
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	if err := s.Custom(ctx, "a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fork(ctx, s.Leaf(), thread.IDs(func() string { return "s_fork" })); err == nil {
		t.Fatal("Fork over a failing Append: no error")
	}
	if _, _, _, err := st.Load(ctx, "s_fork"); !errors.Is(err, thread.ErrNotFound) {
		t.Errorf("the failed fork is still stored: Load err = %v, want ErrNotFound", err)
	}
}

// Fork is a snapshot and is allowed while a turn runs, where Branch is
// not: the fork holds what had been appended, the run is undisturbed.
func TestForkDuringRunningTurn(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	tool := weft.Tool("block", "Block until released.",
		func(ctx context.Context, _ struct{}) (string, error) {
			close(started)
			<-release
			return "ok", nil
		})
	agent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}),
		wefttest.Say("done"),
	), tool)
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never started its tool")
	}
	if err := s.Branch(ctx, ""); !errors.Is(err, thread.ErrBusy) {
		t.Fatalf("Branch mid-turn: err = %v, want ErrBusy", err)
	}
	f, err := s.Fork(ctx, turn.ID())
	if err != nil {
		t.Fatalf("Fork mid-turn: %v", err)
	}
	if got := contextTexts(f); !equalStrings(got, []string{"go"}) {
		t.Errorf("mid-turn fork Context = %v, want the prompt alone", got)
	}
	close(release)
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("the forked-from turn: %v", err)
	}
	if got := contextTexts(s); !equalStrings(got, []string{"go", "done"}) {
		t.Errorf("origin Context = %v", got)
	}
	if got := contextTexts(f); !equalStrings(got, []string{"go"}) {
		t.Errorf("the fork moved with the original: %v", got)
	}
}
