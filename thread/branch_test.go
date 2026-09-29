package thread_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// msgs appends one message entry per text through the storage (Send
// arrives in step 1.7), each entry's parent the session's current
// leaf, then reopens the session — a write behind a Session's back is
// invisible to it until the next Open — returning the refreshed
// session and the ids.
func msgs(t *testing.T, ctx context.Context, st thread.Storage, s *thread.Session, texts ...string) (*thread.Session, []string) {
	t.Helper()
	parent := s.Leaf()
	ids := make([]string, len(texts))
	entries := make([]thread.Entry, len(texts))
	now := time.Now().UTC()
	for i, text := range texts {
		id := thread.NewEntryID()
		ids[i] = id
		entries[i] = thread.MessageEntry{ID: id, ParentID: parent, Created: now, Message: weft.User(text)}
		parent = id
	}
	if err := st.Append(ctx, s.ID(), entries...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return reopen(t, ctx, st, s), ids
}

func reopen(t *testing.T, ctx context.Context, st thread.Storage, s *thread.Session) *thread.Session {
	t.Helper()
	return reopenWith(t, ctx, st, s, weft.New(wefttest.Script()))
}

// reopenWith reopens on the caller's agent — the tests whose model
// records what it saw (the compaction suite) must not swap it for a
// fresh scripted one on the way back in.
func reopenWith(t *testing.T, ctx context.Context, st thread.Storage, s *thread.Session, agent *weft.Agent, opts ...thread.SessionOption) *thread.Session {
	t.Helper()
	again, err := thread.Open(ctx, st, s.ID(), agent, opts...)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return again
}

// contextTexts lists the non-empty message texts, top to bottom — the
// call and result messages of a tool step carry their content in parts
// Text() does not join, so they read as blanks here.
func contextTexts(s *thread.Session) []string {
	var out []string
	for _, m := range s.Context() {
		if m.Text() != "" {
			out = append(out, m.Text())
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// headerOf reads a session's header back through List — the read-only
// route a second process would take.
func headerOf(t *testing.T, ctx context.Context, st thread.Storage, id string) thread.Header {
	t.Helper()
	page, err := thread.List(ctx, st, thread.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, h := range page.Sessions {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("session %s not found in List", id)
	return thread.Header{}
}

func TestBranchTree(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))

		// A deep tree: a five-message main line, a branch off the
		// third entry, back to the main line's end, then onto the
		// branch again — each navigation is a leaf entry, each write
		// attaches where the last navigation left the leaf.
		s, main := msgs(t, ctx, st, s, "m1", "m2", "m3", "m4", "m5")
		if err := s.Branch(ctx, main[2]); err != nil {
			t.Fatalf("Branch to m3: %v", err)
		}
		s, branch := msgs(t, ctx, st, s, "b1", "b2")
		if err := s.Branch(ctx, main[4]); err != nil { // back to the main line
			t.Fatalf("Branch to m5: %v", err)
		}
		s, _ = msgs(t, ctx, st, s, "m6")
		if err := s.Branch(ctx, branch[1]); err != nil { // onto the abandoned branch
			t.Fatalf("Branch to b2: %v", err)
		}
		s, _ = msgs(t, ctx, st, s, "b3")

		open := reopen(t, ctx, st, s)
		want := []string{"m1", "m2", "m3", "b1", "b2", "b3"}
		if got := contextTexts(open); !equalStrings(got, want) {
			t.Errorf("Context after branch walk = %v, want %v", got, want)
		}
		// The pi invariant: nothing from the abandoned main line
		// (m4, m5, m6) reaches the context.
		for _, m := range open.Context() {
			if m.Text() == "m4" || m.Text() == "m5" || m.Text() == "m6" {
				t.Errorf("abandoned-branch message %q reached the context", m.Text())
			}
		}
		// The whole tree is still there: 9 messages plus 3 leaf
		// entries.
		if n := len(open.Entries()); n != 12 {
			t.Errorf("Entries = %d, want 12", n)
		}
		// Leaf resolution after reopen with several leaf entries in
		// the file: the last write (b3) is the leaf.
		if open.Leaf() == "" {
			t.Error("Leaf after reopen empty")
		}
	})
}

func TestBranchLeafEntryIsLeafAfterReopen(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		s, ids := msgs(t, ctx, st, s, "one", "two")
		// A branch with no write after it: the file's last entry is a
		// leaf entry, and the leaf is its target.
		if err := s.Branch(ctx, ids[0]); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		open := reopen(t, ctx, st, s)
		if open.Leaf() != ids[0] {
			t.Errorf("Leaf = %q, want %q", open.Leaf(), ids[0])
		}
		if got := contextTexts(open); !equalStrings(got, []string{"one"}) {
			t.Errorf("Context = %v, want [one]", got)
		}
	})
}

func TestBranchValidation(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		s, _ = msgs(t, ctx, st, s, "one")
		if err := s.Branch(ctx, "e_nope"); err == nil {
			t.Error("Branch to unknown id: no error")
		}
		if err := s.Branch(ctx, s.Leaf()); err != nil { // the current leaf: a recorded no-op
			t.Errorf("Branch to current leaf: %v", err)
		}
		// The navigation still lands: a write goes to the same leaf.
		s, _ = msgs(t, ctx, st, s, "two")
		open := s
		if got := contextTexts(open); !equalStrings(got, []string{"one", "two"}) {
			t.Errorf("Context = %v, want [one two]", got)
		}
		// Branch to the root: the conversation restarts from nothing,
		// the file keeps everything.
		if err := open.Branch(ctx, ""); err != nil {
			t.Fatalf("Branch to root: %v", err)
		}
		restarted := reopen(t, ctx, st, s)
		if got := contextTexts(restarted); len(got) != 0 {
			t.Errorf("Context after branch to root = %v, want none", got)
		}
		if n := len(restarted.Entries()); n != 4 { // one, two, and the two leaf entries
			t.Errorf("Entries after branch to root = %d, want 4 (nothing deleted)", n)
		}
		// SummarizeLeft now summarizes (step 1.8); its shape is
		// pinned by TestBranchSummarizeLeft.
		_ = restarted
	})
}

// The SummarizeLeft option, wired by step 1.8: the branch being left
// is summarized with the compaction summarizer, and the new line's
// context carries the summary in the abandoned branch's place
// (ADR 0020 §6).
func TestBranchSummarizeLeft(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "what the abandoned branch did"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent)
		now := time.Now().UTC()
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_bs1", Created: now, Message: weft.User("the setup")},
			thread.MessageEntry{ID: "e_bs2", ParentID: "e_bs1", Created: now, Message: weft.Assistant("the wrong turn")},
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Branch(ctx, "e_bs1", thread.SummarizeLeft()); err != nil {
			t.Fatalf("Branch SummarizeLeft: %v", err)
		}

		// The abandoned branch is gone from the context; its summary
		// rides in its place, behind the same marker compaction uses.
		got := s.Context()
		if len(got) != 2 {
			t.Fatalf("Context = %d messages, want 2 (the setup, the summary)", len(got))
		}
		if want := "<weft-summary>\nwhat the abandoned branch did\n</weft-summary>"; got[1].Text() != want {
			t.Errorf("branch summary message = %q, want %q", got[1].Text(), want)
		}
		// Durable, and the abandoned entries stay in the file.
		open := reopenWith(t, ctx, st, s, agent)
		if fmt.Sprint(open.Context()) != fmt.Sprint(s.Context()) {
			t.Error("reopen changed the branched context")
		}
		if n := len(open.Entries()); n != 4 { // setup, wrong turn, leaf, branch_summary
			t.Errorf("Entries = %d, want 4", n)
		}
		var bs *thread.BranchSummaryEntry
		for _, e := range open.Entries() {
			if b, ok := e.(thread.BranchSummaryEntry); ok {
				bs = &b
			}
		}
		if bs == nil || bs.FromEntry != "e_bs1" {
			t.Errorf("branch_summary = %+v, want FromEntry e_bs1", bs)
		}
	})
}

func TestFork(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		s, ids := msgs(t, ctx, st, s, "one", "two", "three")
		s, _ = msgs(t, ctx, st, s, "four") // beyond the fork point

		f, err := s.Fork(ctx, ids[1])
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		if f.ID() == s.ID() {
			t.Fatal("fork shares the session id")
		}
		if got := contextTexts(f); !equalStrings(got, []string{"one", "two"}) {
			t.Errorf("fork Context = %v, want [one two]", got)
		}
		if f.Leaf() != ids[1] {
			t.Errorf("fork Leaf = %q, want %q", f.Leaf(), ids[1])
		}
		// The copied entries keep their ids and parent links: the path
		// inside the new file is whole, and self-contained.
		path, err := f.Path(ids[1])
		if err != nil || len(path) != 2 {
			t.Fatalf("fork Path = %d entries, %v; want 2", len(path), err)
		}
		// The header names the origin (ADR 0011 §3).
		h := headerOf(t, ctx, st, f.ID())
		if h.Parent == nil || h.Parent.Session != s.ID() || h.Parent.Entry != ids[1] {
			t.Errorf("fork header parent = %+v, want session %q entry %q", h.Parent, s.ID(), ids[1])
		}
		// The fork grows on its own: its writes never reach the
		// original, and the original's never reach it.
		f, _ = msgs(t, ctx, st, f, "fork-only")
		s, _ = msgs(t, ctx, st, s, "origin-only")
		if got := contextTexts(reopen(t, ctx, st, f)); !equalStrings(got, []string{"one", "two", "fork-only"}) {
			t.Errorf("fork after its own write = %v", got)
		}
		if got := contextTexts(reopen(t, ctx, st, s)); !equalStrings(got, []string{"one", "two", "three", "four", "origin-only"}) {
			t.Errorf("origin after fork's write = %v", got)
		}

		// A fork of a fork: same rules, one level deeper.
		f2, err := f.Fork(ctx, f.Leaf())
		if err != nil {
			t.Fatalf("fork of fork: %v", err)
		}
		h2 := headerOf(t, ctx, st, f2.ID())
		if h2.Parent == nil || h2.Parent.Session != f.ID() {
			t.Errorf("fork-of-fork parent session = %+v, want %q", h2.Parent, f.ID())
		}
		if got := contextTexts(f2); !equalStrings(got, []string{"one", "two", "fork-only"}) {
			t.Errorf("fork of fork Context = %v", got)
		}
	})
}

func TestForkValidation(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		if _, err := s.Fork(ctx, "e_nope"); err == nil {
			t.Error("Fork to unknown id: no error")
		}
		// A fork of an empty session is an empty session with a
		// parent: self-contained, nothing copied.
		f, err := s.Fork(ctx, "")
		if err != nil {
			t.Fatalf("Fork of empty session: %v", err)
		}
		if n := len(f.Entries()); n != 0 {
			t.Errorf("fork of empty session Entries = %d, want 0", n)
		}
		if f.Leaf() != "" {
			t.Errorf("fork of empty session Leaf = %q, want empty", f.Leaf())
		}
		h := headerOf(t, ctx, st, f.ID())
		if h.Parent == nil || h.Parent.Session != s.ID() || h.Parent.Entry != "" {
			t.Errorf("fork of empty session parent = %+v", h.Parent)
		}
		// The fork is usable: a write lands and survives a reopen.
		f, _ = msgs(t, ctx, st, f, "first in the fork")
		if got := contextTexts(reopen(t, ctx, st, f)); !equalStrings(got, []string{"first in the fork"}) {
			t.Errorf("empty fork after write = %v", got)
		}
	})
}

func TestSessionDuplicateEntryID(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	next := 0
	ids := []string{"s_dup", "e_dup", "e_dup"}
	mint := thread.IDs(func() string { id := ids[next]; next++; return id })
	s, err := thread.Create(ctx, st, agent, mint)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Custom(ctx, "k", nil); err != nil {
		t.Fatalf("first Custom: %v", err)
	}
	// An ids function that mints an id the session already holds would
	// make the tree ambiguous (which entry does the id name?); the
	// write must fail loudly instead.
	if err := s.Custom(ctx, "k2", nil); err == nil {
		t.Error("duplicate entry id accepted")
	}
	if n := len(s.Entries()); n != 1 {
		t.Errorf("Entries after rejected duplicate = %d, want 1", n)
	}
}

// Branch is a between-turns operation: while a turn runs, the line
// its transcript must land on is held, and a navigation underneath it
// would strand the run's messages on a branch whose context the model
// never saw — so it fails with ErrBusy, loudly, instead.
func TestBranchDuringRunningTurnFailsBusy(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	type blockInput struct{}
	tool := weft.Tool("block", "Block until released.",
		func(ctx context.Context, in blockInput) (string, error) {
			close(started)
			<-release
			return "ok", nil
		})
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "block"}),
			wefttest.Say("done"),
		),
		weft.Name("branch-busy-test"), tool)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never started its tool")
	}
	if err := s.Branch(ctx, ""); !errors.Is(err, thread.ErrBusy) {
		t.Fatalf("Branch during a running turn: got %v, want ErrBusy", err)
	}
	close(release)
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.Branch(ctx, ""); err != nil {
		t.Fatalf("Branch between turns: %v", err)
	}
}
