package thread_test

import (
	"context"
	"errors"
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
	again, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return again
}

func contextTexts(s *thread.Session) []string {
	var out []string
	for _, m := range s.Context() {
		out = append(out, m.Text())
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
		s, ids := msgs(t, ctx, st, s, "one")
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
		// SummarizeLeft is loud about not being here yet.
		if err := restarted.Branch(ctx, ids[0], thread.SummarizeLeft()); !errors.Is(err, thread.ErrNotImplemented) {
			t.Errorf("Branch SummarizeLeft: err = %v, want ErrNotImplemented", err)
		}
	})
}

// TestBranchSummarizeLeft lands with step 1.8, which wires the
// summarizer into Branch (ADR 0020 §6). Until then the option is a
// loud ErrNotImplemented, pinned above.
func TestBranchSummarizeLeft(t *testing.T) {
	t.Skip("enabled in step 1.8: SummarizeLeft writes a branch_summary (ADR 0020 §6)")
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
