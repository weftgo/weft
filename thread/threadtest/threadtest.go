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
// torn lines — need a backend that can hold undecodable data, so they
// run only when the storage also implements RawInjector and skip
// otherwise: Memory holds the raw bytes (it implements the hook), and
// a backend that cannot hold them at all pins its loudness where its
// format lives.
func Run(t *testing.T, open func(t *testing.T) thread.Storage) {
	t.Run("CreateLoadRoundTrip", roundTrip(open))
	t.Run("AppendAtomic", appendAtomic(open))
	t.Run("AppendArrivalOrder", appendOrder(open))
	t.Run("NotFound", notFound(open))
	t.Run("CreateValidation", createValidation(open))
	t.Run("ListPagesNewestFirst", paging(open))
	t.Run("DeleteRemoves", del(open))
	t.Run("ConcurrentSessions", concurrent(open))
	t.Run("ContextCancellation", canceled(open))
	t.Run("CorruptionIsLoud", corrupt(open))
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
