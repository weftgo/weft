package thread_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// eachBackend runs test against the two v0.1 storages, Memory and
// jsonl on a fresh temp dir — the shared helper every Session test
// goes through, so both backends answer the same table (the threadtest
// rule, carried up to the Session API).
func eachBackend(t *testing.T, test func(t *testing.T, st thread.Storage)) {
	t.Helper()
	for _, b := range []struct {
		name string
		open func(t *testing.T) thread.Storage
	}{
		{"memory", func(t *testing.T) thread.Storage { return thread.Memory() }},
		{"jsonl", func(t *testing.T) thread.Storage {
			st, err := jsonl.Open(t.TempDir())
			if err != nil {
				t.Fatalf("jsonl.Open: %v", err)
			}
			return st
		}},
	} {
		t.Run(b.name, func(t *testing.T) { test(t, b.open(t)) })
	}
}

func TestSessionCreateOpen(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !thread.ValidID(s.ID()) {
			t.Fatalf("session id %q is not valid", s.ID())
		}
		if got := s.Leaf(); got != "" {
			t.Errorf("Leaf of a fresh session = %q, want empty", got)
		}
		if entries := s.Entries(); len(entries) != 0 {
			t.Errorf("Entries of a fresh session = %d, want 0", len(entries))
		}

		again, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if again.ID() != s.ID() {
			t.Errorf("reopened id = %q, want %q", again.ID(), s.ID())
		}
	})
}

func TestSessionCreateOpenNilArgs(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	if _, err := thread.Create(ctx, nil, agent); err == nil {
		t.Error("Create with nil storage: no error")
	}
	if _, err := thread.Create(ctx, thread.Memory(), nil); err == nil {
		t.Error("Create with nil agent: no error")
	}
	if _, err := thread.Open(ctx, nil, "s_x", agent); err == nil {
		t.Error("Open with nil storage: no error")
	}
	if _, err := thread.Open(ctx, thread.Memory(), "s_x", nil); err == nil {
		t.Error("Open with nil agent: no error")
	}
}

func TestSessionOpenUnknown(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		_, err := thread.Open(context.Background(), st, "s_missing", weft.New(wefttest.Script()))
		if !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Open unknown: err = %v, want ErrNotFound", err)
		}
	})
}

func TestSessionListDelete(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		a, _ := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_aaa" }))
		b, _ := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_bbb" }))
		page, err := thread.List(ctx, st, thread.Query{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if page.Total != 2 || len(page.Sessions) != 2 {
			t.Fatalf("List = %d/%d, want 2/2", page.Total, len(page.Sessions))
		}
		if err := thread.Delete(ctx, st, a.ID()); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := thread.Open(ctx, st, a.ID(), agent); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Open deleted: err = %v, want ErrNotFound", err)
		}
		if err := thread.Delete(ctx, st, a.ID()); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Delete twice: err = %v, want ErrNotFound", err)
		}
		page, _ = thread.List(ctx, st, thread.Query{})
		if page.Total != 1 || page.Sessions[0].ID != b.ID() {
			t.Errorf("after Delete: total %d, first %q, want 1/%q", page.Total, page.Sessions[0].ID, b.ID())
		}
		if _, err := thread.List(ctx, nil, thread.Query{}); err == nil {
			t.Error("List with nil storage: no error")
		}
		if err := thread.Delete(ctx, nil, "s_x"); err == nil {
			t.Error("Delete with nil storage: no error")
		}
	})
}

// appendChain writes entries through the storage the way a session
// would (each entry's parent the previous one, ids from fixed), and
// returns the ids in order. Step 1.5 has no message-writing Session
// method yet — Send arrives in 1.7 — so message-path tests go through
// the storage and reopen.
func appendChain(t *testing.T, ctx context.Context, st thread.Storage, s *thread.Session, entries ...thread.Entry) []string {
	t.Helper()
	ids := make([]string, len(entries))
	parent := s.Leaf()
	now := time.Now().UTC()
	setCore := func(e thread.Entry, id string) thread.Entry {
		switch e := e.(type) {
		case thread.MessageEntry:
			e.ID, e.ParentID, e.Created = id, parent, now
			return e
		case thread.TurnEntry:
			e.ID, e.ParentID, e.Created = id, parent, now
			return e
		case thread.CompactionEntry:
			e.ID, e.ParentID, e.Created = id, parent, now
			return e
		case thread.CustomEntry:
			e.ID, e.ParentID, e.Created = id, parent, now
			return e
		case thread.CustomMessageEntry:
			e.ID, e.ParentID, e.Created = id, parent, now
			return e
		}
		t.Fatalf("appendChain: kind %T not wired", e)
		return nil
	}
	for i := range entries {
		id := thread.NewEntryID()
		ids[i] = id
		entries[i] = setCore(entries[i], id)
		parent = id
	}
	if err := st.Append(ctx, s.ID(), entries...); err != nil {
		t.Fatalf("Append chain: %v", err)
	}
	return ids
}

func TestSessionContextIsPathMessages(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		note := weft.User("customer quote covers two items")
		ids := appendChain(t, ctx, st, s,
			thread.MessageEntry{Message: weft.User("hi")},
			thread.MessageEntry{Message: weft.Message{
				Role:    weft.RoleAssistant,
				Content: []weft.Part{weft.ToolCallPart{ID: "c1", Name: "lookup", Args: json.RawMessage(`{}`)}},
			}},
			thread.CustomEntry{Kind: "cart", Data: []byte(`{"items":2}`)},
			thread.CustomMessageEntry{Kind: "note", Message: note},
		)
		if len(ids) != 4 {
			t.Fatalf("appendChain ids = %d, want 4", len(ids))
		}

		open, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		want := []weft.Message{
			weft.User("hi"),
			{Role: weft.RoleAssistant, Content: []weft.Part{weft.ToolCallPart{ID: "c1", Name: "lookup", Args: json.RawMessage(`{}`)}}},
			{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{
				CallID:  "c1",
				Name:    "lookup",
				Content: "no result recorded: the call was interrupted",
				IsError: true,
			}}},
			note,
		}
		if got := open.Context(); !reflect.DeepEqual(got, want) {
			t.Errorf("Context()\n got %#v\nwant %#v", got, want)
		}
	})
}

func TestSessionContextHonorsLeafMove(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		ids := appendChain(t, ctx, st, s,
			thread.MessageEntry{Message: weft.User("first")},
			thread.MessageEntry{Message: weft.Assistant("wrong turn")},
		)
		// Navigate back to the first entry, then continue there: the
		// abandoned assistant message must drop out of the context.
		if err := st.Append(ctx, s.ID(), thread.LeafEntry{ID: thread.NewEntryID(), ParentID: ids[1], Entry: ids[0]}); err != nil {
			t.Fatalf("Append leaf: %v", err)
		}
		if err := st.Append(ctx, s.ID(), thread.MessageEntry{
			ID: thread.NewEntryID(), ParentID: ids[0], Created: time.Now().UTC(),
			Message: weft.User("second"),
		}); err != nil {
			t.Fatalf("Append second: %v", err)
		}

		open, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got := open.Context(); len(got) != 2 || got[0].Text() != "first" || got[1].Text() != "second" {
			t.Errorf("Context after leaf move = %#v, want [first second]", got)
		}
		if got := open.Leaf(); got == "" {
			t.Error("Leaf after reopen = empty")
		}
	})
}

func TestSessionEntriesLeafPath(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		ids := appendChain(t, ctx, st, s,
			thread.MessageEntry{Message: weft.User("one")},
			thread.MessageEntry{Message: weft.Assistant("two")},
		)

		open, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		entries := open.Entries()
		if len(entries) != 2 {
			t.Fatalf("Entries = %d, want 2", len(entries))
		}
		if open.Leaf() != ids[1] {
			t.Errorf("Leaf = %q, want %q", open.Leaf(), ids[1])
		}
		path, err := open.Path(ids[1])
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if len(path) != 2 || idOf(t, path[0]) != ids[0] || idOf(t, path[1]) != ids[1] {
			t.Errorf("Path(ids[1]) = %d entries, wrong ends", len(path))
		}
		if path, err := open.Path(""); err != nil || len(path) != 0 {
			t.Errorf("Path(\"\") = %d, %v; want none, no error", len(path), err)
		}
		if _, err := open.Path("e_nope"); err == nil {
			t.Error("Path(unknown): no error")
		}

		// Mutating what Entries hands back must not reach the session:
		// the slice is fresh and the entries' mutable fields are copies.
		entries[0] = thread.MessageEntry{}
		entries = open.Entries()
		if entries[0].(thread.MessageEntry).Message.Text() != "one" {
			t.Error("writing into the Entries slice reached the session's tree")
		}
	})
}

func idOf(t *testing.T, e thread.Entry) string {
	t.Helper()
	switch e := e.(type) {
	case thread.MessageEntry:
		return e.ID
	case thread.TurnEntry:
		return e.ID
	case thread.CustomEntry:
		return e.ID
	}
	t.Fatalf("idOf: %T", e)
	return ""
}

func TestSessionLabelSetInfoCustom(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		ids := appendChain(t, ctx, st, s, thread.MessageEntry{Message: weft.User("one")})
		open, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := open.Label(ctx, ids[0], "checkpoint"); err != nil {
			t.Fatalf("Label: %v", err)
		}
		if err := open.Label(ctx, "e_nope", "x"); err == nil {
			t.Error("Label unknown entry: no error")
		}
		if err := open.Label(ctx, ids[0], ""); err == nil {
			t.Error("Label empty name: no error")
		}
		if err := open.SetInfo(ctx, "Order support", map[string]string{"team": "ops"}); err != nil {
			t.Fatalf("SetInfo: %v", err)
		}
		if err := open.SetInfo(ctx, "", map[string]string{"lane": "gold"}); err != nil {
			t.Fatalf("SetInfo: %v", err)
		}
		if err := open.Custom(ctx, "cart", []byte(`{"items":2}`)); err != nil {
			t.Fatalf("Custom: %v", err)
		}
		if err := open.Custom(ctx, "", nil); err == nil {
			t.Error("Custom empty kind: no error")
		}
		if err := open.Custom(ctx, "bad", []byte(`{oops`)); err == nil {
			t.Error("Custom non-JSON data: no error")
		}
		if err := open.CustomMessage(ctx, "note", weft.User("n")); err != nil {
			t.Fatalf("CustomMessage: %v", err)
		}
		if err := open.CustomMessage(ctx, "", weft.User("n")); err == nil {
			t.Error("CustomMessage empty kind: no error")
		}

		// Resolve on the live session, and again after a reopen.
		if open.Title() != "Order support" {
			t.Errorf("Title = %q", open.Title())
		}
		meta := open.Meta()
		if meta["team"] != "ops" || meta["lane"] != "gold" {
			t.Errorf("Meta = %v", meta)
		}
		// The caller owns the map: writing into it changes nothing.
		meta["team"] = "hacked"
		if open.Meta()["team"] != "ops" {
			t.Error("mutating Meta reached the session")
		}
		again, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if again.Title() != "Order support" || again.Meta()["lane"] != "gold" {
			t.Errorf("reopen Title/Meta = %q/%v", again.Title(), again.Meta())
		}
		if n := len(again.Entries()); n != 6 { // message + label + 2 info + custom + custom_message
			t.Errorf("Entries = %d, want 6", n)
		}
	})
}

func TestSessionMetaMergesHeader(t *testing.T) {
	st := thread.Memory()
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	if err := st.Create(ctx, thread.Header{
		ID: "s_meta", Created: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		Meta: map[string]string{"app": "orders"},
	}); err != nil {
		t.Fatal(err)
	}
	s, err := thread.Open(ctx, st, "s_meta", agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInfo(ctx, "", map[string]string{"team": "ops"}); err != nil {
		t.Fatal(err)
	}
	meta := s.Meta()
	if len(meta) != 2 || meta["app"] != "orders" || meta["team"] != "ops" {
		t.Errorf("Meta = %v, want header base overlaid by info", meta)
	}
}

func TestSessionUsage(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		t1 := weft.Usage{InputTokens: 100, OutputTokens: 20}
		t2 := weft.Usage{InputTokens: 50, CachedInputTokens: 30}
		sum := weft.Usage{InputTokens: 9, OutputTokens: 90}
		appendChain(t, ctx, st, s,
			thread.MessageEntry{Message: weft.User("one")},
			thread.TurnEntry{RunID: s.ID() + "-t1", Usage: t1},
			thread.MessageEntry{Message: weft.User("two")},
			thread.TurnEntry{RunID: s.ID() + "-t2", Usage: t2},
			thread.CompactionEntry{FirstKept: "e_x", TokensBefore: 150, SummarizerUsage: sum},
		)
		open, _ := thread.Open(ctx, st, s.ID(), agent)
		u := open.Usage()
		if u.Turns != t1.Add(t2) {
			t.Errorf("Usage.Turns = %+v, want %+v", u.Turns, t1.Add(t2))
		}
		if u.Summaries != sum {
			t.Errorf("Usage.Summaries = %+v, want %+v", u.Summaries, sum)
		}
	})
}

func TestSessionIDsOption(t *testing.T) {
	st := thread.Memory()
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	next := 0
	ids := []string{"s_fixed", "e_alpha", "e_beta"}
	opt := thread.IDs(func() string { id := ids[next]; next++; return id })
	s, err := thread.Create(ctx, st, agent, opt)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != "s_fixed" {
		t.Errorf("session id = %q, want s_fixed", s.ID())
	}
	if err := s.Custom(ctx, "k", nil); err != nil {
		t.Fatal(err)
	}
	entries := s.Entries()
	if id := entries[0].(thread.CustomEntry).ID; id != "e_alpha" {
		t.Errorf("entry id = %q, want e_alpha", id)
	}
	again, _ := thread.Open(ctx, st, "s_fixed", agent, thread.IDs(func() string { return "e_gamma" }))
	if err := again.Custom(ctx, "k2", nil); err != nil {
		t.Fatal(err)
	}
	if id := again.Entries()[1].(thread.CustomEntry).ID; id != "e_gamma" {
		t.Errorf("second session entry id = %q, want e_gamma", id)
	}
}

// failOnceStorage fails the next Append once, then behaves.
type failOnceStorage struct {
	thread.Storage
	fail bool
}

func (f *failOnceStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if f.fail {
		f.fail = false
		return errors.New("disk on fire")
	}
	return f.Storage.Append(ctx, session, entries...)
}

func TestSessionFailedAppendNoDivergence(t *testing.T) {
	ctx := context.Background()
	inner := thread.Memory()
	st := &failOnceStorage{Storage: inner, fail: true}
	agent := weft.New(wefttest.Script())
	s, err := thread.Create(ctx, inner, agent) // create through the inner storage: only Append fails
	if err != nil {
		t.Fatal(err)
	}
	s2 := openOn(t, st, s.ID())
	if err := s2.Custom(ctx, "k", nil); err == nil {
		t.Fatal("first Custom: no error")
	}
	if n := len(s2.Entries()); n != 0 {
		t.Errorf("after failed Append, Entries = %d, want 0", n)
	}
	if err := s2.Custom(ctx, "k", nil); err != nil {
		t.Fatalf("second Custom: %v", err)
	}
	if n := len(s2.Entries()); n != 1 {
		t.Errorf("after successful Append, Entries = %d, want 1", n)
	}
	// The storage holds exactly the same tree the session does.
	onDisk, _ := thread.Open(ctx, inner, s.ID(), weft.New(wefttest.Script()))
	if n := len(onDisk.Entries()); n != 1 {
		t.Errorf("storage holds %d entries, want 1 (no divergence)", n)
	}
}

func openOn(t *testing.T, st thread.Storage, id string) *thread.Session {
	t.Helper()
	s, err := thread.Open(context.Background(), st, id, weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestSessionConcurrent(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		const writers, each = 8, 25
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for j := 0; j < each; j++ {
					if err := s.Custom(ctx, fmt.Sprintf("w%d", i), []byte(`{}`)); err != nil {
						t.Errorf("Custom: %v", err)
						return
					}
				}
			}(i)
		}
		for r := 0; r < 2; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < each*writers/2; j++ {
					_ = s.Context()
					_ = s.Entries()
					_ = s.Usage()
					_ = s.Leaf()
					_, _ = s.Path("")
					_ = s.Title()
					_ = s.Meta()
				}
			}()
		}
		wg.Wait()
		if n := len(s.Entries()); n != writers*each {
			t.Errorf("Entries = %d, want %d", n, writers*each)
		}
	})
}

func TestSessionOpenReportsRepair(t *testing.T) {
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelWarn}))
	agent := weft.New(wefttest.Script(), weft.Logger(logger))
	s, err := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_torn" }))
	if err != nil {
		t.Fatal(err)
	}
	inj := st.(interface {
		Inject(ctx context.Context, session string, data []byte) error
	})
	if err := inj.Inject(ctx, s.ID(), []byte(`{"type":"mess`)); err != nil { // torn tail, no newline
		t.Fatal(err)
	}
	if _, err := thread.Open(ctx, st, s.ID(), agent); err != nil {
		t.Fatalf("Open with torn tail: %v", err)
	}
	if !bytes.Contains(log.Bytes(), []byte("repair")) {
		t.Errorf("Open logged nothing about the repair; log = %q", log.String())
	}
}

func TestSessionOpenDanglingLeaf(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_m", Created: time.Now().UTC(), Message: weft.User("one")},
			// A leaf entry pointing at an entry the file does not hold:
			// the session's active position is undefined, and Open must
			// say so instead of answering every read with nothing.
			thread.LeafEntry{ID: "e_l", ParentID: "e_m", Created: time.Now().UTC(), Entry: "e_ghost"},
		); err != nil {
			t.Fatal(err)
		}
		_, err := thread.Open(ctx, st, s.ID(), agent)
		if !errors.Is(err, thread.ErrCorrupt) {
			t.Errorf("Open with a dangling leaf: err = %v, want ErrCorrupt", err)
		}
	})
}
