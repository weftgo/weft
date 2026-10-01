package threadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// RunOneWriter is the Session-level half of the Leaser table: what the
// lease is for. Two Session values on one Storage value are kept to
// one writer — the first to write holds the session until its Close,
// every write of the other fails with ErrLocked and changes nothing —
// Create is a first write, Open and the reads lock nothing, a Session
// whose view fell behind is refused with ErrStale, and Delete through
// the holder's Storage takes the lease with the session. RunLeaser
// runs it as its Sessions subtest; the rows need nothing from the
// backend but thread.Leaser.
func RunOneWriter(t *testing.T, open func(t *testing.T) thread.Storage) {
	t.Helper()
	t.Run("SecondSessionIsLocked", secondSessionLocked(open))
	t.Run("StaleSessionIsRefused", staleSession(open))
	t.Run("CreateTakesTheLease", createTakesLease(open))
	t.Run("ForkTakesTheLease", forkTakesLease(open))
	t.Run("DeleteUnderALiveSession", deleteUnderSession(open))
	t.Run("RecreatedSessionIsStale", recreatedSession(open))
	t.Run("TwoSessionsRaceForTheLease", sessionsRace(open))
}

// sequence returns an IDs function minting prefix1, prefix2, … — the
// first is the session's id when given to Create.
func sequence(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("%s%d", prefix, n)
	}
}

// links is an entry's place in the tree, read off its wire form.
type links struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
}

func link(t *testing.T, e thread.Entry) links {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var l links
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// entryIDs lists a stored session's entry ids, in append order.
func entryIDs(t *testing.T, st thread.Storage, id string) []string {
	t.Helper()
	_, entries, report, err := st.Load(context.Background(), id)
	if err != nil || report != nil {
		t.Fatalf("Load %s: report %+v, err %v", id, report, err)
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = link(t, e).ID
	}
	return ids
}

// oneChain fails the test unless the stored session is a single line:
// one root, every later entry the child of the one before it.
func oneChain(t *testing.T, st thread.Storage, id string) int {
	t.Helper()
	_, entries, report, err := st.Load(context.Background(), id)
	if err != nil || report != nil {
		t.Fatalf("Load %s: report %+v, err %v", id, report, err)
	}
	prev := ""
	for i, e := range entries {
		if p := link(t, e).Parent; p != prev {
			t.Fatalf("entry %d (%s) has parent %q, want %q: the stored tree forked", i, link(t, e).ID, p, prev)
		}
		prev = link(t, e).ID
	}
	return len(entries)
}

// One Session per session id per Storage value writes: the first
// write makes a Session the session's writer, and a second Session on
// the same Storage value is refused with ErrLocked on every write —
// its tree untouched, nothing stored — while its reads keep working.
// Open itself locks nothing. The in-process half of the one-writer
// rule (ADR 0011 §5).
func secondSessionLocked(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		a, err := thread.Create(ctx, st, agent, thread.IDs(sequence("a_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := a.SetInfo(ctx, "first", nil); err != nil {
			t.Fatal(err)
		}
		// Open reads: the writer's lease does not refuse it.
		b, err := thread.Open(ctx, st, a.ID(), agent, thread.IDs(sequence("b_")))
		if err != nil {
			t.Fatalf("Open while another Session holds the lease: %v", err)
		}
		if got := b.Title(); got != "first" {
			t.Errorf("the second Session's Title = %q, want what was stored", got)
		}
		// Its writes are refused, each naming the session, and
		// leave nothing behind — in its tree or in the storage.
		for what, write := range map[string]func() error{
			"SetInfo": func() error { return b.SetInfo(ctx, "second", nil) },
			"Label":   func() error { return b.Label(ctx, "a_2", "mark") },
			"Custom":  func() error { return b.Custom(ctx, "note", nil) },
		} {
			err := write()
			if !errors.Is(err, thread.ErrLocked) {
				t.Fatalf("%s on the second Session: err = %v, want ErrLocked", what, err)
			}
			if !strings.Contains(err.Error(), a.ID()) {
				t.Errorf("%s: ErrLocked says %q, want the session id", what, err)
			}
		}
		if n := len(b.Entries()); n != 1 {
			t.Errorf("the refused writes left %d entries in the second Session's tree, want 1", n)
		}
		if b.Title() != "first" {
			t.Errorf("the refused SetInfo changed the second Session's title to %q", b.Title())
		}
		// The reads of the refused Session still answer.
		if len(b.Context()) != 0 || b.Leaf() != "a_2" {
			t.Errorf("the second Session's reads: context %d, leaf %q", len(b.Context()), b.Leaf())
		}
		// The holder is undisturbed.
		if err := a.SetInfo(ctx, "still first", nil); err != nil {
			t.Fatalf("the holder's write after the refusals: %v", err)
		}
		if n := oneChain(t, st, a.ID()); n != 2 {
			t.Fatalf("storage holds %d entries, want the holder's 2", n)
		}
		// Closing the refused Session must not let go of a lease
		// that is not its own.
		if err := b.Close(ctx); err != nil {
			t.Fatal(err)
		}
		c, err := thread.Open(ctx, st, a.ID(), agent, thread.IDs(sequence("c_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.SetInfo(ctx, "third", nil); !errors.Is(err, thread.ErrLocked) {
			t.Fatalf("a write after a non-holder's Close: err = %v, want ErrLocked", err)
		}
		if err := a.SetInfo(ctx, "last of first", nil); err != nil {
			t.Fatalf("the holder's write after a non-holder's Close: %v", err)
		}
		// Close hands over: a Session opened on what the storage
		// now holds writes.
		if err := a.Close(ctx); err != nil {
			t.Fatal(err)
		}
		d, err := thread.Open(ctx, st, a.ID(), agent, thread.IDs(sequence("d_")))
		if err != nil {
			t.Fatalf("Open after Close: %v", err)
		}
		if err := d.SetInfo(ctx, "handed over", nil); err != nil {
			t.Fatalf("a write after the holder closed: %v", err)
		}
		if n := oneChain(t, st, a.ID()); n != 4 {
			t.Fatalf("storage holds %d entries, want 4", n)
		}
	}
}

// A Session that takes the lease after another Session wrote entries
// it never loaded is refused with ErrStale instead of appending onto
// a leaf the session has moved past; reopening is the fix.
func staleSession(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		seed, err := thread.Create(ctx, st, agent, thread.IDs(sequence("s_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := seed.SetInfo(ctx, "seed", nil); err != nil {
			t.Fatal(err)
		}
		if err := seed.Close(ctx); err != nil {
			t.Fatal(err)
		}
		id := seed.ID()
		a, err := thread.Open(ctx, st, id, agent, thread.IDs(sequence("a_")))
		if err != nil {
			t.Fatal(err)
		}
		b, err := thread.Open(ctx, st, id, agent, thread.IDs(sequence("b_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := a.SetInfo(ctx, "from a", nil); err != nil {
			t.Fatal(err)
		}
		if err := a.Close(ctx); err != nil {
			t.Fatal(err)
		}
		// The lease is free, but b's tree ends one entry short of
		// the session's.
		for i := 0; i < 2; i++ {
			err := b.SetInfo(ctx, "from b", nil)
			if !errors.Is(err, thread.ErrStale) {
				t.Fatalf("stale write #%d: err = %v, want ErrStale", i+1, err)
			}
			if !strings.Contains(err.Error(), id) {
				t.Errorf("ErrStale says %q, want the session id", err)
			}
		}
		if n := len(b.Entries()); n != 1 {
			t.Errorf("the refused write left %d entries in the stale tree, want 1", n)
		}
		if n := oneChain(t, st, id); n != 2 {
			t.Fatalf("storage holds %d entries, want 2: the stale write landed", n)
		}
		// A stale Session's Close frees the lease its attempt took.
		if err := b.Close(ctx); err != nil {
			t.Fatal(err)
		}
		c, err := thread.Open(ctx, st, id, agent, thread.IDs(sequence("c_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.SetInfo(ctx, "from c", nil); err != nil {
			t.Fatalf("a write after reopening: %v", err)
		}
		if c.Title() != "from c" {
			t.Errorf("Title = %q", c.Title())
		}
		if n := oneChain(t, st, id); n != 3 {
			t.Fatalf("storage holds %d entries, want 3", n)
		}
	}
}

// Create is the creating Session's first write: it holds the lease
// from birth, before any entry lands.
func createTakesLease(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		a, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		b, err := thread.Open(ctx, st, a.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.SetInfo(ctx, "b", nil); !errors.Is(err, thread.ErrLocked) {
			t.Fatalf("a write against the creating Session: err = %v, want ErrLocked", err)
		}
		if err := a.SetInfo(ctx, "a", nil); err != nil {
			t.Fatal(err)
		}
	}
}

// Fork creates a session too: the Session it returns is the fork's
// writer from birth, and the origin's lease is not involved — a fork
// is another session.
func forkTakesLease(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		a, err := thread.Create(ctx, st, agent, thread.IDs(sequence("a_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := a.SetInfo(ctx, "origin", nil); err != nil {
			t.Fatal(err)
		}
		f, err := a.Fork(ctx, "a_2", thread.IDs(sequence("f_")))
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		g, err := thread.Open(ctx, st, f.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.SetInfo(ctx, "g", nil); !errors.Is(err, thread.ErrLocked) {
			t.Fatalf("a write against the forking Session: err = %v, want ErrLocked", err)
		}
		if err := f.SetInfo(ctx, "fork", nil); err != nil {
			t.Fatalf("the fork's own write: %v", err)
		}
		if err := a.SetInfo(ctx, "origin still writes", nil); err != nil {
			t.Fatalf("the origin's write after the fork: %v", err)
		}
		// A fork of a session read by a Session that is not its
		// writer works: Fork reads one session and writes another.
		b, err := thread.Open(ctx, st, a.ID(), agent, thread.IDs(sequence("b_")))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Fork(ctx, "a_2"); err != nil {
			t.Fatalf("Fork by a reading Session: %v", err)
		}
		if n := oneChain(t, st, f.ID()); n != 2 {
			t.Errorf("the fork holds %d entries, want the copied one and its own", n)
		}
	}
}

// Delete through the holder's own Storage value is allowed whatever
// Session holds the lease (storage.go's rule): the lease goes with
// the session, and the Session's next write fails with ErrNotFound.
func deleteUnderSession(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		a, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.SetInfo(ctx, "held", nil); err != nil {
			t.Fatal(err)
		}
		if err := thread.Delete(ctx, st, a.ID()); err != nil {
			t.Fatalf("Delete through the holder's storage: %v", err)
		}
		if err := a.SetInfo(ctx, "after", nil); !errors.Is(err, thread.ErrNotFound) {
			t.Fatalf("the Session's write after Delete: err = %v, want ErrNotFound", err)
		}
		if n := len(a.Entries()); n != 1 {
			t.Errorf("the tree holds %d entries after the refused write, want 1", n)
		}
		if err := a.Close(ctx); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Close of a deleted session: err = %v, want ErrNotFound", err)
		}
	}
}

// A session deleted and created again under the same id is another
// session, however much it resembles the first: a Session that loaded
// the old one is refused with ErrStale even when the new one holds
// exactly as many entries — the count alone would wave it through —
// because the lease also reports the stored header's Created.
func recreatedSession(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		same := func() func() string { // every incarnation mints the same ids
			return sequence("r_")
		}
		old, err := thread.Create(ctx, st, agent, thread.IDs(same()), thread.Clock(func() time.Time { return at }))
		if err != nil {
			t.Fatal(err)
		}
		if err := old.SetInfo(ctx, "the first incarnation", nil); err != nil {
			t.Fatal(err)
		}
		if err := thread.Delete(ctx, st, old.ID()); err != nil {
			t.Fatal(err)
		}
		// The same id, the same number of entries, a later header.
		later := at.Add(time.Minute)
		again, err := thread.Create(ctx, st, agent, thread.IDs(same()), thread.Clock(func() time.Time { return later }))
		if err != nil {
			t.Fatalf("Create under the deleted session's id: %v", err)
		}
		if again.ID() != old.ID() {
			t.Fatalf("the second incarnation is %q, want the id %q again", again.ID(), old.ID())
		}
		if err := again.SetInfo(ctx, "the second incarnation", nil); err != nil {
			t.Fatal(err)
		}
		if err := again.Close(ctx); err != nil {
			t.Fatal(err)
		}
		before := entryIDs(t, st, old.ID())
		err = old.SetInfo(ctx, "written into the wrong session", nil)
		if !errors.Is(err, thread.ErrStale) {
			t.Fatalf("a write from the Session of the deleted incarnation: err = %v, want ErrStale", err)
		}
		if after := entryIDs(t, st, old.ID()); len(after) != len(before) {
			t.Fatalf("the refused write stored %d entries", len(after)-len(before))
		}
		if got := old.Title(); got != "the first incarnation" {
			t.Errorf("the stale Session's tree changed: Title = %q", got)
		}
	}
}

// Two Sessions on one Storage value hammering the bookkeeping writes:
// exactly one takes the lease and every write of the other is
// ErrLocked; the storage holds the winner's entries as one chain.
func sessionsRace(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		agent := weft.New(wefttest.Script())
		seed, err := thread.Create(ctx, st, agent, thread.IDs(sequence("s_")))
		if err != nil {
			t.Fatal(err)
		}
		if err := seed.SetInfo(ctx, "seed", nil); err != nil {
			t.Fatal(err)
		}
		if err := seed.Close(ctx); err != nil {
			t.Fatal(err)
		}
		id := seed.ID()
		const writes = 50
		type tally struct{ ok, locked int }
		var tallies [2]tally
		var sessions [2]*thread.Session
		for i := range sessions {
			s, err := thread.Open(ctx, st, id, agent, thread.IDs(sequence(fmt.Sprintf("w%d_", i))))
			if err != nil {
				t.Fatal(err)
			}
			sessions[i] = s
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, s := range sessions {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for n := 0; n < writes; n++ {
					var err error
					if n%2 == 0 {
						err = s.SetInfo(ctx, fmt.Sprintf("w%d %d", i, n), nil)
					} else {
						err = s.Label(ctx, "s_2", fmt.Sprintf("w%d-%d", i, n))
					}
					switch {
					case err == nil:
						tallies[i].ok++
					case errors.Is(err, thread.ErrLocked):
						tallies[i].locked++
					default:
						t.Errorf("writer %d write %d: %v", i, n, err)
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		winner, loser := 0, 1
		if tallies[1].ok > 0 {
			winner, loser = 1, 0
		}
		if tallies[winner].ok != writes || tallies[winner].locked != 0 {
			t.Errorf("the winner: %+v, want every write accepted", tallies[winner])
		}
		if tallies[loser].ok != 0 || tallies[loser].locked != writes {
			t.Errorf("the loser: %+v, want every write ErrLocked", tallies[loser])
		}
		if n := oneChain(t, st, id); n != 1+writes {
			t.Errorf("storage holds %d entries, want %d", n, 1+writes)
		}
		prefix := fmt.Sprintf("w%d_", winner)
		for _, eid := range entryIDs(t, st, id)[1:] {
			if !strings.HasPrefix(eid, prefix) {
				t.Fatalf("entry %s is not the winner's (%s…)", eid, prefix)
			}
		}
		if n := len(sessions[loser].Entries()); n != 1 {
			t.Errorf("the loser's tree holds %d entries, want 1", n)
		}
		if n := len(sessions[winner].Entries()); n != 1+writes {
			t.Errorf("the winner's tree holds %d entries, want %d", n, 1+writes)
		}
	}
}
