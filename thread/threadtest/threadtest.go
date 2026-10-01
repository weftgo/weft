// Package threadtest is the shared conformance table for
// thread.Storage backends — the executable form of ADR 0011 §5's
// promises. Every backend runs it before it may call itself a backend
// (the store's storetest precedent: LangGraph ships one table for its
// savers and got five backends out of it); Run takes a factory so each
// subtest gets fresh storage.
package threadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// Run executes the conformance table against a backend. open returns
// fresh storage for each subtest; the table never shares state between
// them. The corruption rows — unknown kind, newer "v", malformed and
// torn lines, a header from a newer weft — need a backend that can
// hold undecodable data, so they run only when the storage also
// implements RawInjector (RawHeaderInjector for the header rows) and
// skip otherwise: Memory holds the raw bytes (it implements the
// hooks), and a backend that cannot hold them at all pins its loudness
// where its format lives. The capability rows run when the backend has
// the capability: Flusher and Releaser each get their row, and a
// backend that implements thread.Watcher runs the whole RunWatch table
// as the Watch subtest. The one thing Run cannot reach is a second
// Storage over the same sessions — RunTwoWriters takes that factory.
func Run(t *testing.T, open func(t *testing.T) thread.Storage) {
	t.Run("CreateLoadRoundTrip", roundTrip(open))
	t.Run("AppendAtomic", appendAtomic(open))
	t.Run("AppendArrivalOrder", appendOrder(open))
	t.Run("NotFound", notFound(open))
	t.Run("InvalidIDs", invalidIDs(open))
	t.Run("CreateValidation", createValidation(open))
	t.Run("ListPagesNewestFirst", paging(open))
	t.Run("ListPagesThroughTies", pagingTies(open))
	t.Run("ListLimitNormalised", limits(open))
	t.Run("DeleteRemoves", del(open))
	t.Run("ConcurrentSessions", concurrent(open))
	t.Run("ConcurrentAppendOneSession", concurrentAppend(open))
	t.Run("LoadUnderConcurrentDelete", loadUnderDelete(open))
	t.Run("ContextCancellation", canceled(open))
	t.Run("CorruptionIsLoud", corrupt(open))
	t.Run("AppendAfterTornTail", appendAfterTorn(open))
	t.Run("HeaderCorruptionIsLoud", corruptHeader(open))
	t.Run("ListFilters", filters(open))
	t.Run("ListPagingUnderWrites", pagingUnderWrites(open))
	t.Run("Flusher", flusher(open))
	t.Run("Releaser", releaser(open))
	t.Run("Watch", func(t *testing.T) {
		if _, ok := open(t).(thread.Watcher); !ok {
			t.Skip("the backend does not implement thread.Watcher")
		}
		RunWatch(t, open)
	})
}

// pagingUnderWrites pins the paging contract under concurrent inserts:
// pages never duplicate a session, never go backwards, and terminate —
// whatever else is being created while the caller pages. New sessions
// land at the newest edge (a cursor walks away from it), so a walk
// that started before them simply does not see them; what it must
// never see is a session twice or out of order.
func pagingUnderWrites(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
		const stable = 80
		for i := 0; i < stable; i++ {
			h := header(fmt.Sprintf("s_pg%03d", i))
			h.Created = base.Add(time.Duration(i) * time.Second)
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
		}
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { // the churn: new sessions, newest-first edge —
			// bounded and paced, a writer's cadence, not a fork bomb
			// (a List reads every header, and an unbounded churn makes
			// each page quadratically dear).
			defer wg.Done()
			for i := 0; i < 30; i++ {
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
				}
				h := header(fmt.Sprintf("s_new%03d", i))
				h.Created = base.Add(time.Duration(stable+i) * time.Second)
				if err := st.Create(ctx(), h); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		var seen []string
		var before time.Time
		pages := 0
		for {
			p, err := st.List(ctx(), thread.Query{Before: before, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for i, h := range p.Sessions {
				if i > 0 && h.Created.After(p.Sessions[i-1].Created) {
					t.Fatalf("a page out of order: %v", p.Sessions)
				}
				seen = append(seen, h.ID)
			}
			if len(p.Sessions) == 0 {
				break
			}
			before = p.Sessions[len(p.Sessions)-1].Created
			pages++
			if pages > 30 {
				t.Fatal("paging did not terminate")
			}
		}
		close(stop)
		wg.Wait()
		dup := map[string]bool{}
		for _, id := range seen {
			if dup[id] {
				t.Fatalf("session %s appeared on two pages under concurrent writes", id)
			}
			dup[id] = true
		}
		if len(seen) < stable {
			t.Fatalf("paged over %d sessions, want at least the %d that predate the walk", len(seen), stable)
		}
	}
}

// filters pins the Query filter contract: Meta matches every pair
// exactly against the header's metadata, and a wanted key must be
// present; TitleSearch matches case-insensitively as a substring
// against the session's current title — the last info entry carrying a
// non-empty one, Session.Title's rule; both narrow Total; and the Before
// cursor and Limit page the filtered set, not the whole directory.
func filters(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
		// Six sessions: metas split across shapes, titles set by info
		// entries (one retitled late — the last info entry wins).
		seed := []struct {
			id      string
			created time.Time
			meta    map[string]string
			titles  []string
		}{
			{"s_f0", base, map[string]string{"env": "prod", "team": "a"}, []string{"Checkout bug"}},
			{"s_f1", base.Add(time.Second), map[string]string{"env": "dev"}, []string{"login FLOW"}},
			{"s_f2", base.Add(2 * time.Second), map[string]string{"env": "prod"}, nil},
			{"s_f3", base.Add(3 * time.Second), nil, []string{"checkout again"}},
			{"s_f4", base.Add(4 * time.Second), map[string]string{"env": "prod", "team": "b"}, []string{"draft", "Checkout final"}},
			{"s_f5", base.Add(5 * time.Second), map[string]string{"env": "prod"}, []string{}},
			// An info entry with an empty Title (a metadata-only edit)
			// does not clear the title before it.
			{"s_f6", base.Add(6 * time.Second), map[string]string{"env": "stage"}, []string{"Renamed once", ""}},
		}
		for _, f := range seed {
			h := header(f.id)
			h.Created = f.created
			h.Meta = f.meta
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
			for _, title := range f.titles {
				if err := st.Append(ctx(), f.id, thread.InfoEntry{
					ID: thread.NewEntryID(), Created: time.Now().UTC(), Title: title,
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		ids := func(p thread.Page) []string {
			out := make([]string, len(p.Sessions))
			for i, h := range p.Sessions {
				out[i] = h.ID
			}
			return out
		}
		eq := func(got []string, want ...string) {
			t.Helper()
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ids = %v, want %v", got, want)
			}
		}
		p, err := st.List(ctx(), thread.Query{Meta: map[string]string{"env": "prod"}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 4 {
			t.Fatalf("Meta env=prod Total = %d, want 4", p.Total)
		}
		eq(ids(p), "s_f5", "s_f4", "s_f2", "s_f0")
		p, err = st.List(ctx(), thread.Query{Meta: map[string]string{"env": "prod", "team": "a"}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 1 {
			t.Errorf("two pairs Total = %d, want 1", p.Total)
		}
		eq(ids(p), "s_f0")
		// The title is the LAST info entry's: s_f4 retitled to
		// "Checkout final", so "draft" no longer matches.
		p, err = st.List(ctx(), thread.Query{TitleSearch: "checkout"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 3 {
			t.Fatalf("TitleSearch checkout Total = %d, want 3", p.Total)
		}
		eq(ids(p), "s_f4", "s_f3", "s_f0")
		p, err = st.List(ctx(), thread.Query{TitleSearch: "FLOW"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 1 || ids(p)[0] != "s_f1" {
			t.Errorf("case-insensitive title: %v (total %d), want [s_f1]", ids(p), p.Total)
		}
		// The current title is the last NON-empty one: s_f6's trailing
		// empty info entry leaves "Renamed once" standing.
		p, err = st.List(ctx(), thread.Query{TitleSearch: "renamed"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 1 || len(p.Sessions) != 1 || p.Sessions[0].ID != "s_f6" {
			t.Errorf("title after an empty info entry: %v (total %d), want [s_f6]", ids(p), p.Total)
		}
		// A wanted key must be present: an empty wanted value does not
		// match a header that lacks the key.
		p, err = st.List(ctx(), thread.Query{Meta: map[string]string{"absent": ""}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 0 || len(p.Sessions) != 0 {
			t.Errorf("Meta on an absent key: %v (total %d), want none", ids(p), p.Total)
		}
		// Combined, and paged: the cursor and limit apply to the
		// filtered set.
		p, err = st.List(ctx(), thread.Query{
			Meta:        map[string]string{"env": "prod"},
			TitleSearch: "checkout",
			Limit:       2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 2 || len(p.Sessions) != 2 {
			t.Fatalf("combined: %d of %d, want a full page of 2", len(p.Sessions), p.Total)
		}
		next, err := st.List(ctx(), thread.Query{
			Meta:        map[string]string{"env": "prod"},
			TitleSearch: "checkout",
			Before:      p.Sessions[1].Created,
		})
		if err != nil {
			t.Fatal(err)
		}
		if next.Total != 2 || len(next.Sessions) != 0 {
			t.Errorf("after the last filtered session: %d of %d, want 0 of 2", len(next.Sessions), next.Total)
		}
	}
}

// RunWatch runs the Watch capability's conformance table against a
// backend that implements thread.Watcher (the optional interface,
// ADR 0011 §5); Run calls it for such a backend. The whole session
// yields in arrival order, after names the resume point, entries
// appended while watching arrive exactly once, a canceled context ends
// the stream, and the loud failures (unknown session, an invalid id,
// an after the tree does not hold) are errors before the first yield.
// The consumer may call the storage from inside the loop; a malformed
// line ends the stream with ErrCorrupt naming it; a torn tail waits
// for the writer; and a session deleted — or deleted and created again
// — under its watcher ends the stream with ErrNotFound.
func RunWatch(t *testing.T, open func(t *testing.T) thread.Storage) {
	t.Helper()
	t.Run("TailResumeAndDelete", func(t *testing.T) {
		watch, ok := open(t).(thread.Watcher)
		if !ok {
			t.Fatalf("%T does not implement thread.Watcher", watch)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := header("s_watch")
		if err := watch.(thread.Storage).Create(ctx, h); err != nil {
			t.Fatal(err)
		}
		mk := func(text string) thread.Entry {
			return thread.MessageEntry{ID: thread.NewEntryID(), Created: time.Now().UTC(), Message: weft.User(text)}
		}
		first := mk("one")
		second := mk("two")
		if err := watch.(thread.Storage).Append(ctx, h.ID, first, second); err != nil {
			t.Fatal(err)
		}

		// The existing entries arrive in order, then the live ones — each
		// exactly once. got counts (the backlog gate); texts records.
		got := make(chan string, 8)
		var mu sync.Mutex
		var texts []string
		wctx, wcancel := context.WithCancel(ctx)
		seq, err := watch.Watch(wctx, h.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			count := 0
			for e, err := range seq {
				if err != nil {
					t.Errorf("watch stream error: %v", err)
					return
				}
				count++
				txt := e.(thread.MessageEntry).Message.Text()
				mu.Lock()
				texts = append(texts, txt)
				mu.Unlock()
				got <- txt
				if count == 3 {
					wcancel() // the stream ends when ctx is done
				}
			}
			if count != 3 {
				t.Errorf("the watch yielded %d entries, want 3 (two existing, one live)", count)
			}
		}()
		// Wait for the backlog, then append live.
		awaitCount(t, got, 2)
		if err := watch.(thread.Storage).Append(ctx, h.ID, mk("three")); err != nil {
			t.Fatal(err)
		}
		<-done
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(texts, []string{"one", "two", "three"}) {
			t.Errorf("watched texts = %v, want [one two three]", texts)
		}

		// after names the resume point.
		seq, err = watch.Watch(ctx, h.ID, first.(thread.MessageEntry).ID)
		if err != nil {
			t.Fatal(err)
		}
		var resumed []string
		for e, err := range seq {
			if err != nil {
				t.Fatal(err)
			}
			resumed = append(resumed, e.(thread.MessageEntry).Message.Text())
			if len(resumed) == 2 {
				break
			}
		}
		if !reflect.DeepEqual(resumed, []string{"two", "three"}) {
			t.Errorf("resumed texts = %v, want [two three]", resumed)
		}

		// The loud failures, before the first yield.
		if _, err := watch.Watch(context.Background(), "s_missing", ""); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Watch on a missing session: err = %v, want ErrNotFound", err)
		}
		if _, err := watch.Watch(context.Background(), "../escape", ""); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Watch on an invalid id: err = %v, want ErrNotFound", err)
		}
		if _, err := watch.Watch(context.Background(), h.ID, "e_never_seen"); err == nil {
			t.Error("Watch after an entry the tree does not hold: err = nil, want an error")
		}

		// A session deleted under its watcher ends the tail with
		// ErrNotFound — the tail does not hang on a session that is gone.
		h2 := header("s_watch_del")
		if err := watch.(thread.Storage).Create(ctx, h2); err != nil {
			t.Fatal(err)
		}
		dctx, dcancel := context.WithCancel(context.Background())
		defer dcancel()
		dseq, err := watch.Watch(dctx, h2.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		ended := make(chan error, 1)
		go func() {
			var last error
			for _, err := range dseq {
				if err != nil {
					last = err
					break
				}
			}
			ended <- last
		}()
		time.Sleep(2 * pollWait) // let the tail settle on the empty session
		if err := watch.(thread.Storage).Delete(ctx, h2.ID); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-ended:
			if !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("a deleted session's tail ended with %v, want ErrNotFound", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the tail never ended after the session was deleted")
		}
	})
	t.Run("ConsumerWritesInsideLoop", watchConsumerWrites(open))
	t.Run("CorruptLineIsErrCorrupt", watchCorrupt(open))
	t.Run("TornTailWaitsForTheWriter", watchTorn(open))
	t.Run("ReplacedSessionEndsTheTail", watchReplaced(open))
}

// watcher opens fresh storage and asserts the capability.
func watcher(t *testing.T, open func(t *testing.T) thread.Storage) (thread.Storage, thread.Watcher) {
	t.Helper()
	st := open(t)
	w, ok := st.(thread.Watcher)
	if !ok {
		t.Fatalf("%T does not implement thread.Watcher", st)
	}
	return st, w
}

// msg builds a message entry with a fresh id.
func msg(text string) thread.MessageEntry {
	return thread.MessageEntry{ID: thread.NewEntryID(), Created: time.Now().UTC(), Message: weft.User(text)}
}

// watchConsumerWrites pins that a watcher holds nothing of the storage
// while it yields: the consumer appends to another session, loads and
// lists from inside the loop — the shape of a tail that mirrors one
// session into another — and nothing deadlocks.
func watchConsumerWrites(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st, w := watcher(t, open)
		src, dst := header("s_w_src"), header("s_w_dst")
		for _, h := range []thread.Header{src, dst} {
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Append(ctx(), src.ID, msg("a"), msg("b"), msg("c")); err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		seq, err := w.Watch(wctx, src.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			n := 0
			for e, err := range seq {
				if err != nil {
					done <- err
					return
				}
				if err := st.Append(wctx, dst.ID, msg("mirror "+e.(thread.MessageEntry).Message.Text())); err != nil {
					done <- fmt.Errorf("append inside the watch loop: %w", err)
					return
				}
				if _, _, _, err := st.Load(wctx, dst.ID); err != nil {
					done <- fmt.Errorf("load inside the watch loop: %w", err)
					return
				}
				if _, err := st.List(wctx, thread.Query{}); err != nil {
					done <- fmt.Errorf("list inside the watch loop: %w", err)
					return
				}
				if n++; n == 3 {
					break
				}
			}
			done <- nil
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a consumer that writes inside the watch loop deadlocked")
		}
		if _, mirrored, _, err := st.Load(ctx(), dst.ID); err != nil || len(mirrored) != 3 {
			t.Errorf("the mirror session holds %d entries (err %v), want 3", len(mirrored), err)
		}
	}
}

// collect drains a watch stream in the background: entries' texts on
// got, the terminal error (nil when the stream just ends) on ended.
func collect(seq iter.Seq2[thread.Entry, error]) (got chan string, ended chan error) {
	got, ended = make(chan string, 64), make(chan error, 1)
	go func() {
		for e, err := range seq {
			if err != nil {
				ended <- err
				return
			}
			if m, ok := e.(thread.MessageEntry); ok {
				got <- m.Message.Text()
			} else {
				got <- fmt.Sprintf("%T", e)
			}
		}
		ended <- nil
	}()
	return got, ended
}

// awaitEnd waits for a watch stream's terminal error.
func awaitEnd(t *testing.T, ended <-chan error) error {
	t.Helper()
	select {
	case err := <-ended:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the watch stream never ended")
		return nil
	}
}

// watchCorrupt pins the tail's loud row: a malformed line ends the
// stream with ErrCorrupt as a CorruptError naming the line — the same
// class and place Load answers with — after the entries before it.
func watchCorrupt(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st, w := watcher(t, open)
		inj, ok := st.(RawInjector)
		if !ok {
			t.Skipf("%T cannot hold undecodable data", st)
		}
		h := header("s_w_corrupt")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx(), h.ID, msg("one")); err != nil {
			t.Fatal(err)
		}
		if err := inj.Inject(ctx(), h.ID, []byte("this is not json\n")); err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		seq, err := w.Watch(wctx, h.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		got, ended := collect(seq)
		awaitCount(t, got, 1)
		err = awaitEnd(t, ended)
		var ce *thread.CorruptError
		if !errors.Is(err, thread.ErrCorrupt) || !errors.As(err, &ce) || ce.Line != 3 {
			t.Errorf("the tail over a malformed line ended with %v, want a CorruptError on line 3", err)
		}
	}
}

// watchTorn pins the tail over a torn final line: it is never yielded
// half, never an error — and when the next writer repairs the tail and
// appends, the new entry arrives.
func watchTorn(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st, w := watcher(t, open)
		inj, ok := st.(RawInjector)
		if !ok {
			t.Skipf("%T cannot hold undecodable data", st)
		}
		h := header("s_w_torn")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx(), h.ID, msg("one")); err != nil {
			t.Fatal(err)
		}
		if err := inj.Inject(ctx(), h.ID, []byte(`{"type":"message","id":"e_t`)); err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		seq, err := w.Watch(wctx, h.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		got, ended := collect(seq)
		awaitCount(t, got, 1)
		time.Sleep(pollWait) // a poll over the torn tail: nothing to yield, nothing to fail
		select {
		case txt := <-got:
			t.Fatalf("the tail yielded %q from a torn line", txt)
		case err := <-ended:
			t.Fatalf("the tail ended over a torn line: %v", err)
		default:
		}
		if err := st.Append(ctx(), h.ID, msg("two")); err != nil {
			t.Fatal(err)
		}
		select {
		case txt := <-got:
			if txt != "two" {
				t.Errorf("after the repair the tail yielded %q, want %q", txt, "two")
			}
		case err := <-ended:
			t.Fatalf("the tail ended after the repair: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("the tail never saw the entry appended after the repair")
		}
	}
}

// watchReplaced pins that a tail follows one session, not a name: a
// session deleted and created again between two polls ends the stream
// with ErrNotFound — whether the successor holds fewer entries than
// the tail had served (it would otherwise wait forever) or more (it
// would otherwise yield another session's entries as this one's).
func watchReplaced(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			successor int // entries the re-created session holds
		}{
			{"FewerEntries", 1},
			{"MoreEntries", 4},
		} {
			t.Run(tc.name, func(t *testing.T) {
				st, w := watcher(t, open)
				h := header("s_w_replaced")
				if err := st.Create(ctx(), h); err != nil {
					t.Fatal(err)
				}
				if err := st.Append(ctx(), h.ID, msg("old 1"), msg("old 2")); err != nil {
					t.Fatal(err)
				}
				wctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				seq, err := w.Watch(wctx, h.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				got, ended := collect(seq)
				awaitCount(t, got, 2)
				// Delete and re-create back to back — inside one poll
				// interval, under the very same header.
				if err := st.Delete(ctx(), h.ID); err != nil {
					t.Fatal(err)
				}
				if err := st.Create(ctx(), h); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < tc.successor; i++ {
					if err := st.Append(ctx(), h.ID, msg(fmt.Sprintf("new %d", i+1))); err != nil {
						t.Fatal(err)
					}
				}
				if err := awaitEnd(t, ended); !errors.Is(err, thread.ErrNotFound) {
					t.Errorf("the tail of a replaced session ended with %v, want ErrNotFound", err)
				}
				select {
				case txt := <-got:
					t.Errorf("the tail yielded %q from the session that replaced its own", txt)
				default:
				}
			})
		}
	}
}

// pollWait is how long the table waits for a watcher's poll cycle —
// generous against the backends' 200ms poll, still test-quick.
const pollWait = 400 * time.Millisecond

// awaitCount blocks until ch holds n values or the deadline passes.
func awaitCount(t *testing.T, ch <-chan string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for seen := 0; seen < n; {
		select {
		case <-ch:
			seen++
		case <-time.After(time.Until(deadline)):
			t.Fatalf("the watch never yielded its backlog (%d of %d)", seen, n)
			return
		}
	}
}

// RawInjector is the optional hook a durable backend implements so the
// table can exercise the loud rows the Storage interface itself cannot
// express: it appends raw bytes to a session's stored data, verbatim —
// the way a crashed or newer writer would have left them. The test
// controls the bytes exactly, torn tails included (no trailing
// newline).
type RawInjector interface {
	Inject(ctx context.Context, session string, data []byte) error
}

// RawHeaderInjector is the optional hook for the header's loud rows:
// it creates a session whose first line is the given bytes, verbatim
// (the hook adds the newline) — the header a newer weft, or a broken
// writer, would have left. Creating over an existing session fails
// with ErrExists.
type RawHeaderInjector interface {
	InjectHeader(ctx context.Context, session string, line []byte) error
}

func ctx() context.Context { return context.Background() }

// header builds the header the table creates under id.
func header(id string) thread.Header {
	return thread.Header{
		ID:      id,
		Created: time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC),
		Meta:    map[string]string{"table": "threadtest"},
	}
}

// batch is a small realistic entry set: a message, the turn that
// answered it, and application state.
func batch(session string) []thread.Entry {
	now := time.Now().UTC()
	return []thread.Entry{
		thread.MessageEntry{
			ID:      thread.NewEntryID(),
			Created: now,
			Message: weft.User("Where is order 1234?"),
		},
		thread.TurnEntry{
			ID:         thread.NewEntryID(),
			Created:    now,
			RunID:      session + "-t1",
			StopReason: weft.StopEndTurn,
			Usage:      weft.Usage{InputTokens: 410, OutputTokens: 62},
			Steps:      1,
		},
		thread.CustomEntry{
			ID:      thread.NewEntryID(),
			Created: now,
			Kind:    "state",
			Data:    json.RawMessage(`{"open":true}`),
		},
	}
}

// roundTrip pins the first promise: a session created, appended and
// loaded comes back as itself — same header, same entries, same order
// — and nothing aliases: editing what came back, or what was passed
// in, changes nothing on the next load.
func roundTrip(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		h := header("s_roundtrip")
		h.Weft = 0 // the zero envelope means the current format
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		entries := batch(h.ID)
		if err := st.Append(ctx(), h.ID, entries...); err != nil {
			t.Fatal(err)
		}
		got, loaded, report, err := st.Load(ctx(), h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if report != nil {
			t.Errorf("clean load reported repairs: %+v", report)
		}
		want := h
		want.Weft = thread.FormatVersion // stored normalized
		if !reflect.DeepEqual(got, want) {
			t.Errorf("header: got %+v, want %+v", got, want)
		}
		if len(loaded) != len(entries) {
			t.Fatalf("%d entries, want %d", len(loaded), len(entries))
		}
		for i := range entries {
			if !reflect.DeepEqual(entries[i], loaded[i]) {
				t.Errorf("entry %d: got %#v, want %#v", i, loaded[i], entries[i])
			}
		}
		// No aliasing, in either direction: the loaded values are the
		// caller's, and the passed-in values are already the storage's.
		got.Meta["table"] = "edited"
		loaded[0].(thread.MessageEntry).Message.Content[0] = weft.TextPart{Text: "edited"}
		loaded[2].(thread.CustomEntry).Data[0] = 'X'
		entries[2].(thread.CustomEntry).Data[0] = 'Y'
		again, reloaded, _, err := st.Load(ctx(), h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if again.Meta["table"] != "threadtest" {
			t.Errorf("the storage aliased a returned header's meta: %v", again.Meta)
		}
		if txt := reloaded[0].(thread.MessageEntry).Message.Text(); txt != "Where is order 1234?" {
			t.Errorf("the storage aliased a returned entry: %q", txt)
		}
		if string(reloaded[2].(thread.CustomEntry).Data) != `{"open":true}` {
			t.Errorf("the storage aliased an entry: %s", reloaded[2].(thread.CustomEntry).Data)
		}
	}
}

// appendAtomic pins the all-or-none rule: a batch with an entry that
// cannot encode is rejected whole, and nothing of it becomes visible —
// whichever position the bad entry sits in.
func appendAtomic(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		h := header("s_atomic")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		good := batch(h.ID)[0]
		bad := thread.CustomEntry{
			ID:      thread.NewEntryID(),
			Created: time.Now().UTC(),
			Kind:    "broken",
			Data:    json.RawMessage(`{not json`),
		}
		for _, entries := range [][]thread.Entry{{good, bad}, {bad, good}} {
			if err := st.Append(ctx(), h.ID, entries...); err == nil {
				t.Fatal("an append with a broken entry succeeded")
			}
			_, loaded, _, err := st.Load(ctx(), h.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 0 {
				t.Fatalf("a rejected batch left %d entries behind", len(loaded))
			}
		}
		// An empty append is a no-op, not an error.
		if err := st.Append(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx(), h.ID, good); err != nil {
			t.Fatal(err)
		}
		_, loaded, _, err := st.Load(ctx(), h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded) != 1 {
			t.Fatalf("%d entries after the good append, want 1", len(loaded))
		}
	}
}

// appendOrder pins arrival order across separate Append calls: two
// entries per call, in call order, entry order within the batch.
func appendOrder(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		h := header("s_order")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		var want []string
		for round := 0; round < 3; round++ {
			first := thread.MessageEntry{
				ID:      thread.NewEntryID(),
				Created: time.Now().UTC(),
				Message: weft.User(fmt.Sprintf("round %d a", round)),
			}
			second := thread.MessageEntry{
				ID:      thread.NewEntryID(),
				Created: time.Now().UTC(),
				Message: weft.User(fmt.Sprintf("round %d b", round)),
			}
			want = append(want, first.ID, second.ID)
			if err := st.Append(ctx(), h.ID, first, second); err != nil {
				t.Fatal(err)
			}
		}
		_, loaded, _, err := st.Load(ctx(), h.ID)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range loaded {
			got = append(got, e.(thread.MessageEntry).ID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("entry order: got %v, want %v", got, want)
		}
	}
}

// notFound pins the ErrNotFound shape of Load, Append, and Delete —
// including an empty Append, which is a lookup like any other.
func notFound(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		if _, _, _, err := st.Load(ctx(), "s_missing"); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Load: err = %v, want ErrNotFound", err)
		}
		if err := st.Append(ctx(), "s_missing"); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("empty Append: err = %v, want ErrNotFound", err)
		}
		if err := st.Append(ctx(), "s_missing", batch("s_missing")[0]); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Append: err = %v, want ErrNotFound", err)
		}
		if err := st.Delete(ctx(), "s_missing"); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Delete: err = %v, want ErrNotFound", err)
		}
	}
}

// createValidation pins Create's contract: one path component for an
// id, the current envelope (zero means it), and never a silent
// replacement of an existing session.
func createValidation(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		for _, id := range []string{"", "../escape", "a/b", "a.b", ".hidden", strings.Repeat("x", 129)} {
			if err := st.Create(ctx(), header(id)); err == nil {
				t.Errorf("Create(%q) succeeded, want rejected", id)
			}
		}
		tooNew := header("s_envelope")
		tooNew.Weft = thread.FormatVersion + 1
		if err := st.Create(ctx(), tooNew); err == nil {
			t.Error("Create with a newer envelope succeeded, want rejected")
		}
		h := header("s_create")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := st.Create(ctx(), h); !errors.Is(err, thread.ErrExists) {
			t.Errorf("Create over an existing session: err = %v, want ErrExists", err)
		}
		got, _, _, err := st.Load(ctx(), h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Weft != thread.FormatVersion {
			t.Errorf("stored envelope = %d, want %d (a zero Weft stores the current format)", got.Weft, thread.FormatVersion)
		}
	}
}

// paging pins the list contract: headers only, newest first, the
// Before cursor, the Total count, no duplicates, and the limit.
func paging(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
		const total = 120
		for i := 0; i < total; i++ {
			h := header(fmt.Sprintf("s_page%03d", i))
			h.Created = base.Add(time.Duration(i) * time.Second)
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
		}
		var seen []string
		var before time.Time
		pages := 0
		for {
			p, err := st.List(ctx(), thread.Query{Before: before})
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != total {
				t.Fatalf("page %d Total = %d, want %d", pages, p.Total, total)
			}
			if len(p.Sessions) == 0 {
				break
			}
			for _, h := range p.Sessions {
				seen = append(seen, h.ID)
			}
			before = p.Sessions[len(p.Sessions)-1].Created
			pages++
			if pages > 10 {
				t.Fatal("paging did not terminate")
			}
		}
		if len(seen) != total {
			t.Fatalf("paged over %d sessions, want %d", len(seen), total)
		}
		dup := map[string]bool{}
		for _, id := range seen {
			if dup[id] {
				t.Fatalf("duplicate session %s across pages", id)
			}
			dup[id] = true
		}
		for i := 1; i < len(seen); i++ {
			if seen[i] > seen[i-1] {
				t.Fatalf("pages not newest-first: %v ...", seen[:i+1])
			}
		}
		p, err := st.List(ctx(), thread.Query{Limit: 7})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Sessions) != 7 || p.Total != total {
			t.Errorf("Limit=7: got %d of %d, want 7 of %d", len(p.Sessions), p.Total, total)
		}
	}
}

// del pins Delete: the session and its entries go, the others stay,
// and the List total drops.
func del(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		keep, drop := header("s_keep"), header("s_drop")
		for _, h := range []thread.Header{keep, drop} {
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx(), h.ID, batch(h.ID)...); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Delete(ctx(), drop.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := st.Load(ctx(), drop.ID); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Load after Delete: %v, want ErrNotFound", err)
		}
		if err := st.Delete(ctx(), drop.ID); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("second Delete: %v, want ErrNotFound", err)
		}
		if _, loaded, _, err := st.Load(ctx(), keep.ID); err != nil || len(loaded) != len(batch(keep.ID)) {
			t.Errorf("the surviving session changed: %d entries, err %v", len(loaded), err)
		}
		if p, _ := st.List(ctx(), thread.Query{}); p.Total != 1 {
			t.Errorf("List Total after Delete = %d, want 1", p.Total)
		}
	}
}

// concurrent pins the concurrency contract: many sessions at once, one
// goroutine each — the shape a fleet of agents produces.
func concurrent(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		const workers = 8
		const perWorker = 20
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				h := header(fmt.Sprintf("s_conc%02d", w))
				if err := st.Create(ctx(), h); err != nil {
					errs <- err
					return
				}
				for i := 0; i < perWorker; i++ {
					if err := st.Append(ctx(), h.ID, thread.MessageEntry{
						ID:      thread.NewEntryID(),
						Created: time.Now().UTC(),
						Message: weft.User(fmt.Sprintf("w%d e%d", w, i)),
					}); err != nil {
						errs <- err
						return
					}
				}
				_, loaded, _, err := st.Load(ctx(), h.ID)
				if err != nil {
					errs <- err
					return
				}
				if len(loaded) != perWorker {
					errs <- fmt.Errorf("worker %d: %d entries, want %d", w, len(loaded), perWorker)
				}
			}(w)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if p, err := st.List(ctx(), thread.Query{}); err != nil || p.Total != workers {
			t.Errorf("List after the fleet: %d sessions (err %v), want %d", p.Total, err, workers)
		}
	}
}

// canceled pins the context rule: every method fails with
// context.Canceled before writing anything.
func canceled(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		h := header("s_canceled")
		if err := st.Create(cctx, h); !errors.Is(err, context.Canceled) {
			t.Errorf("Create: err = %v, want context.Canceled", err)
		}
		if err := st.Append(cctx, h.ID, batch(h.ID)...); !errors.Is(err, context.Canceled) {
			t.Errorf("Append: err = %v, want context.Canceled", err)
		}
		if _, _, _, err := st.Load(cctx, h.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("Load: err = %v, want context.Canceled", err)
		}
		if _, err := st.List(cctx, thread.Query{}); !errors.Is(err, context.Canceled) {
			t.Errorf("List: err = %v, want context.Canceled", err)
		}
		if err := st.Delete(cctx, h.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("Delete: err = %v, want context.Canceled", err)
		}
		// Nothing was written under the canceled context.
		if p, _ := st.List(ctx(), thread.Query{}); p.Total != 0 {
			t.Errorf("%d sessions exist after canceled calls, want 0", p.Total)
		}
	}
}

// corrupt pins the loud rows (ADR 0011 §5), through RawInjector: an
// unknown kind and a newer "v" are ErrNewerFormat, never a skip; a
// malformed line mid-file is ErrCorrupt naming the line; a torn final
// line is dropped and reported; and a corrupt file never hides the
// session from List.
func corrupt(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		inj, ok := st.(RawInjector)
		if !ok {
			t.Skipf("%T cannot hold undecodable data; the loud rows run against backends that implement threadtest.RawInjector", st)
		}
		mk := func(t *testing.T, id string) {
			t.Helper()
			if err := st.Create(ctx(), header(id)); err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx(), id, thread.MessageEntry{
				ID:      thread.NewEntryID(),
				Created: time.Now().UTC(),
				Message: weft.User("one"),
			}); err != nil {
				t.Fatal(err)
			}
		}

		t.Run("UnknownKindIsNewerFormat", func(t *testing.T) {
			mk(t, "s_c_unknown")
			if err := inj.Inject(ctx(), "s_c_unknown", []byte(`{"type":"approval","id":"e_x"}`+"\n")); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := st.Load(ctx(), "s_c_unknown"); !errors.Is(err, thread.ErrNewerFormat) {
				t.Errorf("err = %v, want ErrNewerFormat", err)
			}
		})

		t.Run("NewerVIsNewerFormat", func(t *testing.T) {
			mk(t, "s_c_newer")
			if err := inj.Inject(ctx(), "s_c_newer", []byte(`{"type":"message","v":2,"id":"e_x"}`+"\n")); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := st.Load(ctx(), "s_c_newer"); !errors.Is(err, thread.ErrNewerFormat) {
				t.Errorf("err = %v, want ErrNewerFormat", err)
			}
		})

		t.Run("MalformedMidFileIsCorruptNamingLine", func(t *testing.T) {
			mk(t, "s_c_mid")
			if err := inj.Inject(ctx(), "s_c_mid", []byte("this is not json\n"+"{}\n")); err != nil {
				t.Fatal(err)
			}
			_, _, _, err := st.Load(ctx(), "s_c_mid")
			if !errors.Is(err, thread.ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
			var ce *thread.CorruptError
			if !errors.As(err, &ce) || ce.Line != 3 {
				t.Errorf("err = %v, want a CorruptError carrying line 3 (header is line 1)", err)
			}
		})

		t.Run("TornTailDroppedAndReported", func(t *testing.T) {
			mk(t, "s_c_torn")
			if err := inj.Inject(ctx(), "s_c_torn", []byte(`{"type":"message","id":"e_t`)); err != nil {
				t.Fatal(err)
			}
			_, loaded, report, err := st.Load(ctx(), "s_c_torn")
			if err != nil {
				t.Fatalf("a torn tail must not fail the load: %v", err)
			}
			if len(loaded) != 1 {
				t.Errorf("%d entries after a torn tail, want the 1 complete one", len(loaded))
			}
			if report == nil || report.Torn != 3 {
				t.Errorf("report = %+v, want Torn=3 (header, entry, torn tail)", report)
			}
		})

		t.Run("CorruptFileDoesNotHideFromList", func(t *testing.T) {
			mk(t, "s_c_list")
			if err := inj.Inject(ctx(), "s_c_list", []byte("garbage\n")); err != nil {
				t.Fatal(err)
			}
			p, err := st.List(ctx(), thread.Query{})
			if err != nil {
				t.Fatalf("List over a corrupt file: %v", err)
			}
			found := false
			for _, h := range p.Sessions {
				if h.ID == "s_c_list" {
					found = true
				}
			}
			if !found {
				t.Errorf("the corrupt session is missing from List: %d sessions", len(p.Sessions))
			}
		})
	}
}

// invalidIDs pins that every method vets the id it is given (ADR 0011
// §5: an id is a path component in some backend, so every backend
// checks): an id that is not one valid component names no session —
// ErrNotFound — and reaches nothing outside the storage.
func invalidIDs(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		if err := st.Create(ctx(), header("s_valid")); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"", "../s_valid", "a/b", "s_valid.jsonl", ".", strings.Repeat("x", 129)} {
			if _, _, _, err := st.Load(ctx(), id); !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("Load(%q): err = %v, want ErrNotFound", id, err)
			}
			if err := st.Append(ctx(), id); !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("empty Append(%q): err = %v, want ErrNotFound", id, err)
			}
			if err := st.Append(ctx(), id, msg("x")); !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("Append(%q): err = %v, want ErrNotFound", id, err)
			}
			if err := st.Delete(ctx(), id); !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("Delete(%q): err = %v, want ErrNotFound", id, err)
			}
			if f, ok := st.(thread.Flusher); ok {
				if err := f.Flush(ctx(), id); !errors.Is(err, thread.ErrNotFound) {
					t.Errorf("Flush(%q): err = %v, want ErrNotFound", id, err)
				}
			}
			if r, ok := st.(thread.Releaser); ok {
				if err := r.Release(ctx(), id); !errors.Is(err, thread.ErrNotFound) {
					t.Errorf("Release(%q): err = %v, want ErrNotFound", id, err)
				}
			}
			if w, ok := st.(thread.Watcher); ok {
				if _, err := w.Watch(ctx(), id, ""); !errors.Is(err, thread.ErrNotFound) {
					t.Errorf("Watch(%q): err = %v, want ErrNotFound", id, err)
				}
			}
		}
		// The valid session beside them is untouched.
		if _, entries, _, err := st.Load(ctx(), "s_valid"); err != nil || len(entries) != 0 {
			t.Errorf("the valid session changed: %d entries, err %v", len(entries), err)
		}
	}
}

// pagingTies pins the keyset cursor: sessions sharing one Created time
// are ordered by id descending, and the (Before, BeforeID) cursor
// walks through the tie — every session exactly once, in order —
// where Before alone, strict on time, skips the rest of the group.
func pagingTies(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		tied := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
		const ties, older = 60, 5
		for i := 0; i < ties; i++ {
			h := header(fmt.Sprintf("s_tie%03d", i))
			h.Created = tied
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < older; i++ {
			h := header(fmt.Sprintf("s_old%03d", i))
			h.Created = tied.Add(-time.Duration(i+1) * time.Second)
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
		}
		var seen []thread.Header
		var q thread.Query
		q.Limit = 25
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatal("paging did not terminate")
			}
			p, err := st.List(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != ties+older {
				t.Fatalf("page %d Total = %d, want %d", pages, p.Total, ties+older)
			}
			if len(p.Sessions) == 0 {
				break
			}
			seen = append(seen, p.Sessions...)
			last := p.Sessions[len(p.Sessions)-1]
			q.Before, q.BeforeID = last.Created, last.ID
		}
		if len(seen) != ties+older {
			t.Fatalf("the keyset walk saw %d sessions, want %d — a cursor inside a tie must not skip", len(seen), ties+older)
		}
		for i := 1; i < len(seen); i++ {
			prev, cur := seen[i-1], seen[i]
			inOrder := cur.Created.Before(prev.Created) || (cur.Created.Equal(prev.Created) && cur.ID < prev.ID)
			if !inOrder {
				t.Fatalf("out of order or duplicated at %d: %s (%v) after %s (%v)", i, cur.ID, cur.Created, prev.ID, prev.Created)
			}
		}
		// Before alone is strict on time: from inside the tie it skips
		// the rest of the group and lands on the older sessions.
		p, err := st.List(ctx(), thread.Query{Before: tied})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Sessions) != older {
			t.Errorf("Before alone at the tied instant returned %d sessions, want the %d strictly older", len(p.Sessions), older)
		}
		// BeforeID without Before is no cursor at all.
		p, err = st.List(ctx(), thread.Query{BeforeID: "s_tie030", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Sessions) != 1 || p.Sessions[0].ID != "s_tie059" {
			t.Errorf("BeforeID with a zero Before: %v, want the newest session", p.Sessions)
		}
	}
}

// limits pins the page-size rule every backend shares: 0 and negatives
// mean 50, values above 500 clamp to 500, anything between is itself.
func limits(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
		const total = 505
		for i := 0; i < total; i++ {
			h := header(fmt.Sprintf("s_lim%03d", i))
			h.Created = base.Add(time.Duration(i) * time.Second)
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ limit, want int }{
			{0, 50}, {-1, 50}, {-1000, 50}, {1, 1}, {50, 50}, {500, 500}, {501, 500}, {1 << 30, 500},
		} {
			p, err := st.List(ctx(), thread.Query{Limit: tc.limit})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Sessions) != tc.want || p.Total != total {
				t.Errorf("Limit=%d: %d sessions of %d, want %d of %d", tc.limit, len(p.Sessions), p.Total, tc.want, total)
			}
		}
	}
}

// concurrentAppend pins one session under many goroutines of one
// Storage — the session layer serializes its own writes, but a backend
// must not depend on that: every batch lands whole, its entries
// adjacent and in order, no line torn into another's, and each
// goroutine's batches in the order it appended them.
func concurrentAppend(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		h := header("s_conc_one")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		const workers, rounds = 8, 25
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for r := 0; r < rounds; r++ {
					if err := st.Append(ctx(), h.ID,
						msg(fmt.Sprintf("w%d r%d a", w, r)), msg(fmt.Sprintf("w%d r%d b", w, r))); err != nil {
						errs <- err
						return
					}
				}
			}(w)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		_, loaded, report, err := st.Load(ctx(), h.ID)
		if err != nil || report != nil {
			t.Fatalf("Load after concurrent appends: err %v, report %+v", err, report)
		}
		if len(loaded) != workers*rounds*2 {
			t.Fatalf("%d entries, want %d", len(loaded), workers*rounds*2)
		}
		nextRound := make([]int, workers)
		for i := 0; i < len(loaded); i += 2 {
			var w, r int
			var half string
			a := loaded[i].(thread.MessageEntry).Message.Text()
			if _, err := fmt.Sscanf(a, "w%d r%d %s", &w, &r, &half); err != nil || half != "a" {
				t.Fatalf("entry %d = %q: a batch was split or reordered", i, a)
			}
			if b := loaded[i+1].(thread.MessageEntry).Message.Text(); b != fmt.Sprintf("w%d r%d b", w, r) {
				t.Fatalf("entry %d = %q after %q: a batch's entries are not adjacent", i+1, b, a)
			}
			if r != nextRound[w] {
				t.Fatalf("worker %d round %d arrived when round %d was due", w, r, nextRound[w])
			}
			nextRound[w]++
		}
	}
}

// loadUnderDelete pins that a Load racing a Delete answers one of two
// whole truths — the session as it was, every entry, or ErrNotFound —
// and never a header whose entries have vanished, which a caller could
// not tell from data loss.
func loadUnderDelete(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		const rounds, entries = 20, 40
		for round := 0; round < rounds; round++ {
			h := header(fmt.Sprintf("s_lud%02d", round))
			if err := st.Create(ctx(), h); err != nil {
				t.Fatal(err)
			}
			batch := make([]thread.Entry, entries)
			for i := range batch {
				batch[i] = msg(fmt.Sprintf("e%d", i))
			}
			if err := st.Append(ctx(), h.ID, batch...); err != nil {
				t.Fatal(err)
			}
			deleted := make(chan error, 1)
			go func() { deleted <- st.Delete(ctx(), h.ID) }()
			for gone := false; !gone; {
				_, loaded, report, err := st.Load(ctx(), h.ID)
				switch {
				case errors.Is(err, thread.ErrNotFound):
					gone = true
				case err != nil:
					t.Fatalf("round %d: Load under Delete: %v", round, err)
				case len(loaded) != entries || report != nil:
					t.Fatalf("round %d: Load under Delete returned %d of %d entries (report %+v) — a partial session",
						round, len(loaded), entries, report)
				}
			}
			if err := <-deleted; err != nil {
				t.Fatalf("round %d: Delete: %v", round, err)
			}
		}
	}
}

// appendAfterTorn pins the writer's repair rule: a torn tail — the
// half-line a crashed writer left — is reported by Load while it
// stands, and removed by the next Append before it writes, so the new
// entry is its own line and the session loads clean with every
// complete entry. The alternative, appending onto the torn bytes,
// glues two lines into one malformed one and poisons every later load.
func appendAfterTorn(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		inj, ok := st.(RawInjector)
		if !ok {
			t.Skipf("%T cannot hold a torn tail", st)
		}
		h := header("s_torn_append")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx(), h.ID, msg("one")); err != nil {
			t.Fatal(err)
		}
		if err := inj.Inject(ctx(), h.ID, []byte(`{"type":"message","id":"e_t`)); err != nil {
			t.Fatal(err)
		}
		if _, _, report, err := st.Load(ctx(), h.ID); err != nil || report == nil || report.Torn != 3 {
			t.Fatalf("before the repair: report %+v, err %v; want Torn=3", report, err)
		}
		if err := st.Append(ctx(), h.ID, msg("two"), msg("three")); err != nil {
			t.Fatalf("Append over a torn tail: %v", err)
		}
		_, loaded, report, err := st.Load(ctx(), h.ID)
		if err != nil {
			t.Fatalf("Load after appending over a torn tail: %v", err)
		}
		if report != nil {
			t.Errorf("after the repair the load still reports %+v", report)
		}
		var texts []string
		for _, e := range loaded {
			texts = append(texts, e.(thread.MessageEntry).Message.Text())
		}
		if !reflect.DeepEqual(texts, []string{"one", "two", "three"}) {
			t.Errorf("entries after the repair = %v, want [one two three]", texts)
		}
	}
}

// corruptHeader pins the header's loud rows, through
// RawHeaderInjector: a first line from a newer weft fails Load with
// ErrNewerFormat — its own class, not line-1 corruption — and a first
// line that is not a header fails with ErrCorrupt naming line 1;
// neither appears in List or its Total, neither breaks listing the
// sessions beside it, neither can be created over, and both can be
// deleted.
func corruptHeader(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		inj, ok := st.(RawHeaderInjector)
		if !ok {
			t.Skipf("%T cannot hold an undecodable header; the header rows run against backends that implement threadtest.RawHeaderInjector", st)
		}
		if err := st.Create(ctx(), header("s_h_good")); err != nil {
			t.Fatal(err)
		}
		newer := fmt.Sprintf(`{"type":"session","weft":%d,"id":"s_h_newer","created":"2026-09-28T12:00:00Z"}`, thread.FormatVersion+1)
		if err := inj.InjectHeader(ctx(), "s_h_newer", []byte(newer)); err != nil {
			t.Fatal(err)
		}
		if err := inj.InjectHeader(ctx(), "s_h_garbage", []byte(`not a header`)); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := st.Load(ctx(), "s_h_newer"); !errors.Is(err, thread.ErrNewerFormat) {
			t.Errorf("Load on a newer header: err = %v, want ErrNewerFormat", err)
		}
		_, _, _, err := st.Load(ctx(), "s_h_garbage")
		var ce *thread.CorruptError
		if !errors.Is(err, thread.ErrCorrupt) || !errors.As(err, &ce) || ce.Line != 1 {
			t.Errorf("Load on a garbage header: err = %v, want a CorruptError on line 1", err)
		}
		p, err := st.List(ctx(), thread.Query{})
		if err != nil {
			t.Fatalf("List beside undecodable headers: %v", err)
		}
		if p.Total != 1 || len(p.Sessions) != 1 || p.Sessions[0].ID != "s_h_good" {
			t.Errorf("List = %d sessions (total %d), want only s_h_good", len(p.Sessions), p.Total)
		}
		for _, id := range []string{"s_h_newer", "s_h_garbage"} {
			if err := st.Create(ctx(), header(id)); !errors.Is(err, thread.ErrExists) {
				t.Errorf("Create over %s: err = %v, want ErrExists", id, err)
			}
			if err := inj.InjectHeader(ctx(), id, []byte(newer)); !errors.Is(err, thread.ErrExists) {
				t.Errorf("InjectHeader over %s: err = %v, want ErrExists", id, err)
			}
			if err := st.Delete(ctx(), id); err != nil {
				t.Errorf("Delete of %s: %v", id, err)
			}
			if _, _, _, err := st.Load(ctx(), id); !errors.Is(err, thread.ErrNotFound) {
				t.Errorf("Load of %s after Delete: err = %v, want ErrNotFound", id, err)
			}
		}
	}
}

// flusher pins the Flusher capability for a backend that has it: an
// unknown session is ErrNotFound, a canceled context fails first, a
// held session flushes clean, and what was appended before the flush
// is what loads after it.
func flusher(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		f, ok := st.(thread.Flusher)
		if !ok {
			t.Skipf("%T does not implement thread.Flusher", st)
		}
		if err := f.Flush(ctx(), "s_missing"); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Flush on a missing session: err = %v, want ErrNotFound", err)
		}
		h := header("s_flush")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := f.Flush(ctx(), h.ID); err != nil {
			t.Errorf("Flush on a fresh session: %v", err)
		}
		if err := st.Append(ctx(), h.ID, batch(h.ID)...); err != nil {
			t.Fatal(err)
		}
		if err := f.Flush(ctx(), h.ID); err != nil {
			t.Errorf("Flush after an append: %v", err)
		}
		if err := f.Flush(ctx(), h.ID); err != nil {
			t.Errorf("a second Flush with nothing new: %v", err)
		}
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := f.Flush(cctx, h.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("Flush under a canceled context: err = %v, want context.Canceled", err)
		}
		if _, loaded, report, err := st.Load(ctx(), h.ID); err != nil || report != nil || len(loaded) != len(batch(h.ID)) {
			t.Errorf("after Flush: %d entries, report %+v, err %v", len(loaded), report, err)
		}
		if err := st.Delete(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.Flush(ctx(), h.ID); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Flush after Delete: err = %v, want ErrNotFound", err)
		}
	}
}

// releaser pins the Releaser capability's single-writer half for a
// backend that has it (RunTwoWriters pins the hand-over): an unknown
// session is ErrNotFound, a canceled context fails first, releasing is
// idempotent, nothing written before a release is lost, and the
// releasing storage writes again afterwards by re-acquiring.
func releaser(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		st := open(t)
		r, ok := st.(thread.Releaser)
		if !ok {
			t.Skipf("%T does not implement thread.Releaser", st)
		}
		if err := r.Release(ctx(), "s_missing"); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Release on a missing session: err = %v, want ErrNotFound", err)
		}
		h := header("s_release")
		if err := st.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx(), h.ID, msg("before")); err != nil {
			t.Fatal(err)
		}
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := r.Release(cctx, h.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("Release under a canceled context: err = %v, want context.Canceled", err)
		}
		for i := 0; i < 2; i++ { // the second is a release of nothing held
			if err := r.Release(ctx(), h.ID); err != nil {
				t.Errorf("Release #%d: %v", i+1, err)
			}
		}
		if err := st.Append(ctx(), h.ID, msg("after")); err != nil {
			t.Fatalf("Append after Release must re-acquire: %v", err)
		}
		_, loaded, report, err := st.Load(ctx(), h.ID)
		if err != nil || report != nil || len(loaded) != 2 {
			t.Fatalf("after release and re-acquire: %d entries, report %+v, err %v", len(loaded), report, err)
		}
		if err := r.Release(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.Delete(ctx(), h.ID); err != nil {
			t.Errorf("Delete of a released session: %v", err)
		}
		if err := r.Release(ctx(), h.ID); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Release after Delete: err = %v, want ErrNotFound", err)
		}
	}
}

// RunTwoWriters is the conformance sub-table for the one-writer rule
// (ADR 0011 §5), for backends that enforce it across Storage values: a
// backend opts in by providing open, which returns two independent
// Storages over the same sessions — two opens of one directory or one
// database, the in-process shape of two processes. The second writer
// is refused with ErrLocked naming the session (never a filesystem
// path), readers never lock, a held session cannot be created over or
// deleted by the other, and Delete frees the name. When the backend
// implements thread.Releaser the hold is a lease: Release hands the
// session to the other writer, and the first one is refused in turn
// until it is handed back.
func RunTwoWriters(t *testing.T, open func(t *testing.T) (first, second thread.Storage)) {
	t.Helper()
	t.Run("SecondWriterIsLocked", func(t *testing.T) {
		first, second := open(t)
		h := header("s_two_writers")
		if err := first.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := first.Append(ctx(), h.ID, msg("held")); err != nil {
			t.Fatal(err)
		}
		locked := func(what string, err error) {
			t.Helper()
			if !errors.Is(err, thread.ErrLocked) {
				t.Errorf("second writer %s: err = %v, want ErrLocked", what, err)
				return
			}
			if !strings.Contains(err.Error(), h.ID) || strings.ContainsAny(err.Error(), `/\`) {
				t.Errorf("second writer %s: ErrLocked says %q, want the session id and no path", what, err)
			}
		}
		locked("Append", second.Append(ctx(), h.ID, msg("second")))
		locked("empty Append", second.Append(ctx(), h.ID))
		locked("Delete", second.Delete(ctx(), h.ID))
		if err := second.Create(ctx(), h); !errors.Is(err, thread.ErrExists) {
			t.Errorf("second Create over a held session: err = %v, want ErrExists", err)
		}
		// Readers never lock.
		if _, loaded, report, err := second.Load(ctx(), h.ID); err != nil || report != nil || len(loaded) != 1 {
			t.Errorf("Load under a foreign hold: %d entries, report %+v, err %v", len(loaded), report, err)
		}
		if p, err := second.List(ctx(), thread.Query{}); err != nil || p.Total != 1 {
			t.Errorf("List under a foreign hold: total %d, err %v", p.Total, err)
		}
		// The refused writes wrote nothing; the holder still writes.
		if err := first.Append(ctx(), h.ID, msg("still held")); err != nil {
			t.Errorf("the holder's Append after the refusals: %v", err)
		}
		// Delete releases; the next creator gets a clean slate.
		if err := first.Delete(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if err := second.Create(ctx(), h); err != nil {
			t.Fatalf("Create after the holder deleted: %v", err)
		}
		if err := first.Append(ctx(), h.ID, msg("late")); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("the old holder against the new session: err = %v, want ErrLocked", err)
		}
	})
	t.Run("ReleaseHandsOver", func(t *testing.T) {
		first, second := open(t)
		fr, ok1 := first.(thread.Releaser)
		sr, ok2 := second.(thread.Releaser)
		if !ok1 || !ok2 {
			t.Skipf("%T does not implement thread.Releaser", first)
		}
		h := header("s_handover")
		if err := first.Create(ctx(), h); err != nil {
			t.Fatal(err)
		}
		if err := first.Append(ctx(), h.ID, msg("first 1")); err != nil {
			t.Fatal(err)
		}
		// Releasing a session another writer holds releases nothing.
		if err := sr.Release(ctx(), h.ID); err != nil {
			t.Errorf("Release by a non-holder: %v", err)
		}
		if err := second.Append(ctx(), h.ID, msg("early")); !errors.Is(err, thread.ErrLocked) {
			t.Fatalf("a non-holder's Release must not free the session: err = %v, want ErrLocked", err)
		}
		if err := fr.Release(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if err := second.Append(ctx(), h.ID, msg("second 1")); err != nil {
			t.Fatalf("Append after the holder released: %v", err)
		}
		if err := first.Append(ctx(), h.ID, msg("first 2")); !errors.Is(err, thread.ErrLocked) {
			t.Fatalf("the releaser against the new holder: err = %v, want ErrLocked", err)
		}
		if err := sr.Release(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if err := first.Append(ctx(), h.ID, msg("first 2")); err != nil {
			t.Fatalf("the releaser re-acquiring after the hand-back: %v", err)
		}
		_, loaded, report, err := first.Load(ctx(), h.ID)
		if err != nil || report != nil {
			t.Fatalf("Load after the hand-overs: err %v, report %+v", err, report)
		}
		var texts []string
		for _, e := range loaded {
			texts = append(texts, e.(thread.MessageEntry).Message.Text())
		}
		if !reflect.DeepEqual(texts, []string{"first 1", "second 1", "first 2"}) {
			t.Errorf("entries across the hand-overs = %v", texts)
		}
	})
}
