package thread_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// counter returns an IDs function minting prefix1, prefix2, … — the
// deterministic ids the open and close tests pin entries with.
func counter(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("%s%d", prefix, n)
	}
}

func userEntry(id, parent, text string) thread.MessageEntry {
	return thread.MessageEntry{ID: id, ParentID: parent, Created: time.Unix(1, 0).UTC(), Message: weft.User(text)}
}

// Open validates the tree as it indexes it: every malformed shape a
// file can hold is refused with a CorruptError naming the line and
// the entry, instead of being indexed into a tree whose walks then
// quietly answer something else — a context cut short where a parent
// is missing, the wrong entry where an id repeats, a hang where the
// links form a cycle.
func TestOpenRejectsMalformedTree(t *testing.T) {
	cases := []struct {
		name    string
		entries []thread.Entry
		line    int
		entry   string
		says    string
	}{
		{
			name:    "an entry with no id",
			entries: []thread.Entry{userEntry("e_1", "", "one"), userEntry("", "e_1", "two")},
			line:    3, entry: "", says: "no id",
		},
		{
			name:    "an id that is not a valid id",
			entries: []thread.Entry{userEntry("e/1", "", "one")},
			line:    2, entry: "e/1", says: "not a valid id",
		},
		{
			name: "a duplicate id",
			entries: []thread.Entry{
				userEntry("e_1", "", "one"), userEntry("e_2", "e_1", "two"), userEntry("e_1", "e_2", "again"),
			},
			line: 4, entry: "e_1", says: "already held",
		},
		{
			name:    "a parent the file does not hold",
			entries: []thread.Entry{userEntry("e_1", "", "one"), userEntry("e_3", "e_2", "three")},
			line:    3, entry: "e_3", says: `parent "e_2"`,
		},
		{
			name:    "a parent that comes later (a cycle)",
			entries: []thread.Entry{userEntry("e_1", "e_2", "one"), userEntry("e_2", "e_1", "two")},
			line:    2, entry: "e_1", says: `parent "e_2"`,
		},
		{
			name:    "an entry that is its own parent",
			entries: []thread.Entry{userEntry("e_1", "e_1", "one")},
			line:    2, entry: "e_1", says: `parent "e_1"`,
		},
		{
			name: "a mid-file leaf entry navigating nowhere",
			entries: []thread.Entry{
				userEntry("e_1", "", "one"),
				thread.LeafEntry{ID: "e_l", ParentID: "e_1", Created: time.Unix(1, 0).UTC(), Entry: "e_ghost"},
				userEntry("e_2", "e_1", "two"),
			},
			line: 3, entry: "e_l", says: `navigates to "e_ghost"`,
		},
		{
			name: "a leaf entry navigating to itself",
			entries: []thread.Entry{
				userEntry("e_1", "", "one"),
				thread.LeafEntry{ID: "e_l", ParentID: "e_1", Created: time.Unix(1, 0).UTC(), Entry: "e_l"},
			},
			line: 3, entry: "e_l", says: `navigates to "e_l"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := thread.Memory()
			agent := weft.New(wefttest.Script())
			s, err := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_bad" }))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, s.ID(), tc.entries...); err != nil {
				t.Fatalf("Append: %v", err)
			}
			_, err = thread.Open(ctx, st, s.ID(), agent)
			if !errors.Is(err, thread.ErrCorrupt) {
				t.Fatalf("Open: err = %v, want ErrCorrupt", err)
			}
			var ce *thread.CorruptError
			if !errors.As(err, &ce) {
				t.Fatalf("Open: err = %T, want *CorruptError", err)
			}
			if ce.Session != "s_bad" || ce.Line != tc.line || ce.Entry != tc.entry {
				t.Errorf("CorruptError = session %q line %d entry %q, want s_bad line %d entry %q",
					ce.Session, ce.Line, ce.Entry, tc.line, tc.entry)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error %q does not say %q", err, tc.says)
			}
		})
	}
}

// Several roots are legal — Branch to the root starts a new one — and
// Path answers from whichever root the entry descends from.
func TestOpenAcceptsSeveralRoots(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, _ := thread.Create(ctx, st, agent)
		if err := st.Append(ctx, s.ID(),
			userEntry("e_a1", "", "a one"), userEntry("e_a2", "e_a1", "a two"),
			userEntry("e_b1", "", "b one"), userEntry("e_b2", "e_b1", "b two"),
		); err != nil {
			t.Fatal(err)
		}
		open, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatalf("Open with two roots: %v", err)
		}
		if open.LoadReport() != nil {
			t.Errorf("LoadReport of a clean load = %+v, want nil", open.LoadReport())
		}
		if got := contextTexts(open); !equalStrings(got, []string{"b one", "b two"}) {
			t.Errorf("Context = %v, want the second root's line", got)
		}
		path, err := open.Path("e_a2")
		if err != nil || len(path) != 2 {
			t.Errorf("Path(e_a2) = %d entries, %v; want the first root's line", len(path), err)
		}
	})
}

// salvagedSession writes m1 ← m2 ← m3 ← m4 with m2's line replaced by
// garbage, and returns the directory and the session id: the shape a
// damaged disk leaves, which only Salvage loads.
func salvagedSession(t *testing.T, tail ...thread.Entry) (dir, id string) {
	t.Helper()
	ctx := context.Background()
	dir = t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := thread.Create(ctx, st, weft.New(wefttest.Script()), thread.IDs(func() string { return "s_salvage" }))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, s.ID(), userEntry("e_m1", "", "one")); err != nil {
		t.Fatal(err)
	}
	inj := st.(interface {
		Inject(ctx context.Context, session string, data []byte) error
	})
	if err := inj.Inject(ctx, s.ID(), []byte("{\"type\":\"message\",\"id\":\"e_m2\",\"par\x00\n")); err != nil {
		t.Fatal(err)
	}
	rest := append([]thread.Entry{userEntry("e_m3", "e_m2", "three"), userEntry("e_m4", "e_m3", "four")}, tail...)
	if err := st.Append(ctx, s.ID(), rest...); err != nil {
		t.Fatal(err)
	}
	if c, ok := st.(io.Closer); ok {
		_ = c.Close()
	}
	return dir, s.ID()
}

// Under Salvage a skipped line orphans the entries that hung off it.
// They are kept — never dropped, never re-linked by guessing — and
// reported to the caller, not only to the log: the context is shorter
// than the conversation was, and the session says so.
func TestOpenSalvageReportsOrphans(t *testing.T) {
	ctx := context.Background()
	dir, id := salvagedSession(t)
	agent := weft.New(wefttest.Script())

	strict, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := thread.Open(ctx, strict, id, agent); !errors.Is(err, thread.ErrCorrupt) {
		t.Fatalf("Open without Salvage: err = %v, want ErrCorrupt", err)
	}

	st, err := jsonl.Open(dir, thread.Salvage())
	if err != nil {
		t.Fatal(err)
	}
	s, err := thread.Open(ctx, st, id, agent)
	if err != nil {
		t.Fatalf("Open with Salvage: %v", err)
	}
	r := s.LoadReport()
	if r == nil {
		t.Fatal("LoadReport = nil after a salvaged load")
	}
	if len(r.Skipped) != 1 || r.Skipped[0] != 3 {
		t.Errorf("Skipped = %v, want [3]", r.Skipped)
	}
	if !equalStrings(r.Orphaned, []string{"e_m3"}) {
		t.Errorf("Orphaned = %v, want [e_m3]", r.Orphaned)
	}
	// The report is the caller's copy.
	r.Orphaned[0] = "scribbled"
	if got := s.LoadReport().Orphaned[0]; got != "e_m3" {
		t.Errorf("LoadReport aliased the session's report: %q", got)
	}
	// Nothing was dropped: the survivors are all there, and the
	// orphan is the root of what is left of its line.
	if n := len(s.Entries()); n != 3 {
		t.Errorf("Entries = %d, want the 3 survivors", n)
	}
	if got := contextTexts(s); !equalStrings(got, []string{"three", "four"}) {
		t.Errorf("Context = %v, want [three four] — the line from the orphan on", got)
	}
	path, err := s.Path("e_m4")
	if err != nil || len(path) != 2 {
		t.Errorf("Path(e_m4) = %d entries, %v; want 2, ending at the orphan", len(path), err)
	}
	if s.Leaf() != "e_m4" {
		t.Errorf("Leaf = %q, want e_m4", s.Leaf())
	}

	// A fork of the salvaged line is a whole file: the orphan is its
	// root, and it loads without Salvage.
	f, err := s.Fork(ctx, "e_m4", thread.IDs(func() string { return "s_salvage_fork" }))
	if err != nil {
		t.Fatalf("Fork of a salvaged line: %v", err)
	}
	if c, ok := st.(io.Closer); ok {
		_ = c.Close()
	}
	strict2, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := thread.Open(ctx, strict2, f.ID(), agent)
	if err != nil {
		t.Fatalf("Open the fork without Salvage: %v", err)
	}
	if got := contextTexts(again); !equalStrings(got, []string{"three", "four"}) {
		t.Errorf("fork Context = %v, want [three four]", got)
	}
	if again.LoadReport() != nil {
		t.Errorf("the fork's LoadReport = %+v, want nil", again.LoadReport())
	}
}

// A navigation whose target went with a skipped line is ignored, and
// reported: the leaf stays where it was.
func TestOpenSalvageLostLeafTarget(t *testing.T) {
	ctx := context.Background()
	dir, id := salvagedSession(t,
		thread.LeafEntry{ID: "e_nav", ParentID: "e_m4", Created: time.Unix(2, 0).UTC(), Entry: "e_m2"})
	st, err := jsonl.Open(dir, thread.Salvage())
	if err != nil {
		t.Fatal(err)
	}
	s, err := thread.Open(ctx, st, id, weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Open with Salvage: %v", err)
	}
	if s.Leaf() != "e_m4" {
		t.Errorf("Leaf = %q, want e_m4 — the lost navigation must not move it", s.Leaf())
	}
	if got := s.LoadReport().Orphaned; !equalStrings(got, []string{"e_m3", "e_nav"}) {
		t.Errorf("Orphaned = %v, want [e_m3 e_nav]", got)
	}
}

// A torn tail alone orphans nothing: the report carries the torn line
// and no orphans.
func TestLoadReportTornTail(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script())
	s, _ := thread.Create(ctx, st, agent)
	if err := st.Append(ctx, s.ID(), userEntry("e_1", "", "one")); err != nil {
		t.Fatal(err)
	}
	inj := st.(interface {
		Inject(ctx context.Context, session string, data []byte) error
	})
	if err := inj.Inject(ctx, s.ID(), []byte(`{"type":"mess`)); err != nil {
		t.Fatal(err)
	}
	open, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatalf("Open with a torn tail: %v", err)
	}
	r := open.LoadReport()
	if r == nil || r.Torn != 3 || len(r.Skipped) != 0 || len(r.Orphaned) != 0 {
		t.Errorf("LoadReport = %+v, want Torn 3 and nothing else", r)
	}
}

// The header options are refused by Open — each by name — instead of
// being accepted and dropped.
func TestOpenRejectsCreateOnlyOptions(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script())
	s, _ := thread.Create(ctx, st, agent)
	for name, opt := range map[string]thread.SessionOption{
		"WithMeta":    thread.WithMeta(map[string]string{"team": "support"}),
		"PublicID":    thread.PublicID("share-1"),
		"WithLineage": thread.WithLineage("s_parent", "call_1"),
	} {
		_, err := thread.Open(ctx, st, s.ID(), agent, opt)
		if !errors.Is(err, thread.ErrCreateOnly) {
			t.Errorf("Open with %s: err = %v, want ErrCreateOnly", name, err)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Open with %s: error %q does not name the option", name, err)
		}
	}
	// The options every Open may carry still pass.
	if _, err := thread.Open(ctx, st, s.ID(), agent, thread.BusyPolicy(thread.Reject), thread.IDs(counter("e_"))); err != nil {
		t.Errorf("Open with session options: %v", err)
	}
}

// CorruptError unwraps to both the class and the cause.
func TestCorruptErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("bad bytes")
	err := error(&thread.CorruptError{Session: "s_x", Line: 4, Entry: "e_9", Err: fmt.Errorf("decode: %w", cause)})
	if !errors.Is(err, thread.ErrCorrupt) {
		t.Error("errors.Is(err, ErrCorrupt) = false")
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) = false: Unwrap hides the cause")
	}
	if got, want := err.Error(), "thread: session s_x line 4 (entry e_9) is corrupt: decode: bad bytes"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(&thread.CorruptError{Session: "s_x"}, thread.ErrCorrupt) {
		t.Error("a CorruptError with no cause lost its class")
	}
}

// The Clock option is the session's time source: the header and every
// entry the session appends carry its time, in UTC.
func TestClockPinsTimestamps(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	zone := time.FixedZone("plus2", 2*60*60)
	tick := time.Date(2026, 10, 1, 12, 0, 0, 0, zone)
	clock := thread.Clock(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	agent := weft.New(wefttest.Script(wefttest.Say("hello")))
	s, err := thread.Create(ctx, st, agent, clock, thread.IDs(counter("c_")))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 1, 10, 0, 1, 0, time.UTC)
	if h := headerOf(t, ctx, st, s.ID()); !h.Created.Equal(want) || h.Created.Location() != time.UTC {
		t.Errorf("header Created = %v, want %v in UTC", h.Created, want)
	}
	if err := s.Label(ctx, "", "x"); err == nil {
		t.Fatal("Label of the root: no error")
	}
	if err := s.Custom(ctx, "cart", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetInfo(ctx, "title", nil); err != nil {
		t.Fatal(err)
	}
	entries := s.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries = %d, want 2", len(entries))
	}
	if got := entries[0].(thread.CustomEntry).Created; !got.Equal(want.Add(time.Second)) || got.Location() != time.UTC {
		t.Errorf("custom entry Created = %v, want %v in UTC", got, want.Add(time.Second))
	}
	if got := entries[1].(thread.InfoEntry).Created; !got.Equal(want.Add(2 * time.Second)) {
		t.Errorf("info entry Created = %v, want %v", got, want.Add(2*time.Second))
	}
	// A fork's header is stamped by the fork's own clock.
	forkAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	f, err := s.Fork(ctx, s.Leaf(), thread.IDs(counter("f_")), thread.Clock(func() time.Time { return forkAt }))
	if err != nil {
		t.Fatal(err)
	}
	if h := headerOf(t, ctx, st, f.ID()); !h.Created.Equal(forkAt) {
		t.Errorf("fork header Created = %v, want %v", h.Created, forkAt)
	}
	// A nil clock is an ignored option.
	if _, err := thread.Create(ctx, st, agent, thread.Clock(nil)); err != nil {
		t.Errorf("Create with Clock(nil): %v", err)
	}
}

// SetInfo with nothing to record writes nothing; reserved keys are
// refused; a title is replaced, never cleared.
func TestSetInfoRules(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, err := thread.Create(ctx, st, weft.New(wefttest.Script()),
			thread.WithMeta(map[string]string{"weft.public_id": "pub-1", "team": "support"}))
		if err != nil {
			t.Fatal(err)
		}
		for _, meta := range []map[string]string{nil, {}} {
			if err := s.SetInfo(ctx, "", meta); err != nil {
				t.Fatalf("SetInfo with nothing to record: %v", err)
			}
		}
		if n := len(s.Entries()); n != 0 {
			t.Fatalf("an empty SetInfo wrote %d entries", n)
		}
		if err := s.SetInfo(ctx, "first", map[string]string{"team": "billing"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetInfo(ctx, "", map[string]string{"tier": "gold"}); err != nil {
			t.Fatal(err)
		}
		if got := s.Title(); got != "first" {
			t.Errorf("Title after an empty-title SetInfo = %q, want first — a title is never cleared", got)
		}
		err = s.SetInfo(ctx, "second", map[string]string{"tier": "silver", "weft.session.id": "forged"})
		if !errors.Is(err, thread.ErrReservedKey) {
			t.Fatalf("SetInfo with a weft. key: err = %v, want ErrReservedKey", err)
		}
		if got := s.Title(); got != "first" {
			t.Errorf("the refused SetInfo changed the title to %q", got)
		}
		want := map[string]string{"weft.public_id": "pub-1", "team": "billing", "tier": "gold"}
		got := s.Meta()
		if len(got) != len(want) {
			t.Fatalf("Meta = %v, want %v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("Meta[%q] = %q, want %q", k, got[k], v)
			}
		}
	})
}

// A file whose info entries name a reserved key the header lacks keeps
// the first value: even without a header value, the identity never
// rotates.
func TestMetaReservedKeyFirstWriteWins(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script())
	s, _ := thread.Create(ctx, st, agent)
	at := time.Unix(1, 0).UTC()
	if err := st.Append(ctx, s.ID(),
		thread.InfoEntry{ID: "e_i1", Created: at, Meta: map[string]string{"weft.public_id": "first", "k": "1"}},
		thread.InfoEntry{ID: "e_i2", ParentID: "e_i1", Created: at, Meta: map[string]string{"weft.public_id": "second", "k": "2"}},
	); err != nil {
		t.Fatal(err)
	}
	open, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if got := open.Meta(); got["weft.public_id"] != "first" || got["k"] != "2" {
		t.Errorf("Meta = %v, want the first public id and the last ordinary value", got)
	}
}

// AppendApprovalRequests mints through the same guard as every other
// write: an id the tree holds, an id minted twice for the batch, or an
// invalid id fails the call and writes nothing.
func TestAppendApprovalRequestsGuardsIDs(t *testing.T) {
	ctx := context.Background()
	reqs := []thread.ApprovalRequestEntry{
		{CallID: "s_child/call_1", Tool: "refund", Child: "s_child"},
		{CallID: "s_child/call_2", Tool: "refund", Child: "s_child"},
	}
	for name, ids := range map[string][]string{
		"an id the tree holds":     {"s_p", "e_1", "e_1", "e_2"},
		"an id minted twice":       {"s_p", "e_1", "e_2", "e_2"},
		"an id that is not valid":  {"s_p", "e_1", "e_2", "no/slash"},
		"an empty id from the IDs": {"s_p", "e_1", "", "e_3"},
	} {
		t.Run(name, func(t *testing.T) {
			st := thread.Memory()
			next := 0
			s, err := thread.Create(ctx, st, weft.New(wefttest.Script()),
				thread.IDs(func() string { id := ids[next]; next++; return id }))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Custom(ctx, "seed", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendApprovalRequests(ctx, reqs...); err == nil {
				t.Fatal("AppendApprovalRequests: no error")
			}
			if n := len(s.Entries()); n != 1 {
				t.Errorf("the refused batch left %d entries in memory, want 1", n)
			}
			_, stored, _, err := st.Load(ctx, s.ID())
			if err != nil || len(stored) != 1 {
				t.Errorf("the refused batch left %d entries in storage (%v), want 1", len(stored), err)
			}
		})
	}
	// The good path: ids in order, entries chained, the caller's
	// copies detached from the tree.
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()), thread.IDs(counter("m_")))
	reqs[0].Args = []byte(`{"order":1}`)
	out, err := s.AppendApprovalRequests(ctx, reqs...)
	if err != nil {
		t.Fatalf("AppendApprovalRequests: %v", err)
	}
	if len(out) != 2 || out[0].ID != "m_2" || out[1].ID != "m_3" || out[1].ParentID != "m_2" {
		t.Fatalf("stored requests = %+v", out)
	}
	out[0].Args[2] = 'X'
	reqs[0].Args[2] = 'Y'
	if got := string(s.Entries()[0].(thread.ApprovalRequestEntry).Args); got != `{"order":1}` {
		t.Errorf("the tree's args changed under the caller's hands: %s", got)
	}
}
