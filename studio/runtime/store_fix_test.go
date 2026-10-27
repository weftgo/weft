package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// postAck posts one ack through the route and answers its status.
func postAck(h http.Handler, a Ack) int {
	b, _ := json.Marshal(a)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runtime/acks", strings.NewReader(string(b))))
	return rec.Code
}

// shutdownWithin closes rs with its drain bounded by d, then waits for
// the writer goroutine to be gone — so nothing it logs lands in a later
// test's capture.
func shutdownWithin(rs *RuntimeServer, d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = rs.Shutdown(ctx)
	if rs.writer != nil {
		select {
		case <-rs.writer.done:
		case <-time.After(5 * time.Second):
		}
	}
}

// orderStore records each command's stored states in write order, and
// counts calls that overlapped another.
type orderStore struct {
	recordingStore
	mu2    sync.Mutex
	states map[string][]string
}

func (s *orderStore) PutCommand(_ context.Context, c obsdb.StoredCommand) error {
	defer s.enter("PutCommand")()
	s.mu2.Lock()
	s.states[c.ID] = append(s.states[c.ID], c.State)
	s.mu2.Unlock()
	return nil
}

// hangingStore answers reads at once and holds every write until its
// context ends — a store that stopped answering.
type hangingStore struct{}

func (hangingStore) PutCommand(ctx context.Context, _ obsdb.StoredCommand) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingStore) Commands(context.Context, obsdb.CommandQuery) ([]obsdb.StoredCommand, error) {
	return nil, nil
}
func (hangingStore) PutRoute(ctx context.Context, _ obsdb.RunRoute) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingStore) Route(context.Context, string) (obsdb.RunRoute, bool, error) {
	return obsdb.RunRoute{}, false, nil
}
func (hangingStore) PutBreakpoints(ctx context.Context, _ string, _ []string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingStore) Breakpoints(context.Context, string) ([]string, bool, error) {
	return nil, false, nil
}
func (hangingStore) PutRuntime(ctx context.Context, _ obsdb.StoredRuntime) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingStore) Runtimes(context.Context) ([]obsdb.StoredRuntime, error) { return nil, nil }
func (hangingStore) DeleteRuntime(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingStore) PruneCommands(ctx context.Context, _ time.Time, _ int) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

// TestWriteThroughOrdered: the write-through is ordered per key, the
// prune never overlaps a Put, and the hot path never waits for the
// store — against a store that holds every write until its deadline,
// register, enqueue and two concurrent accepted acks each answer in
// well under a second.
func TestWriteThroughOrdered(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		store := &orderStore{states: map[string][]string{}}
		rs := New(WithStore(store), func(rs *RuntimeServer) { rs.pruneStoreEvery = 0 })
		h := mux(rs)
		ts := httptest.NewServer(h)
		defer ts.Close()
		register(t, h, regBody("rt_race"))
		r, closeStream := subscribe(t, rs, ts.URL, "rt_race", "")
		defer closeStream()
		go func() {
			for {
				if _, _, _, err := readEvent(r); err != nil {
					return
				}
			}
		}()
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					cmd, err := rs.Enqueue("rt_race", Command{Agent: "acme-support"})
					if err != nil {
						continue
					}
					run := "pg_" + cmd.CommandID
					postAck(h, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: run})
					postAck(h, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: run})
					postAck(h, Ack{CommandID: cmd.CommandID, State: "finished", RunID: run, Status: "succeeded"})
					rs.Snapshot() // a prune pass (throttle lifted: each queues one)
				}
			}()
		}
		wg.Wait()
		rs.Close() // drains
		if n := store.overlaps.Load(); n != 0 {
			t.Errorf("%d store calls overlapped another", n)
		}
		if store.count("PruneCommands") == 0 || store.count("PutRoute") == 0 {
			t.Errorf("calls: prune %d, route %d — each expected", store.count("PruneCommands"), store.count("PutRoute"))
		}
		rank := map[string]int{StateQueued: 0, StateAccepted: 1, StateFinished: 2}
		store.mu2.Lock()
		defer store.mu2.Unlock()
		if len(store.states) != 80 {
			t.Errorf("%d commands stored, want 80", len(store.states))
		}
		for id, seq := range store.states {
			for k := 1; k < len(seq); k++ {
				if rank[seq[k]] < rank[seq[k-1]] {
					t.Errorf("command %s stored out of order: %v", id, seq)
					break
				}
			}
			if seq[len(seq)-1] != StateFinished {
				t.Errorf("command %s's last stored state = %s (%v), want finished", id, seq[len(seq)-1], seq)
			}
		}
	})

	t.Run("latency", func(t *testing.T) {
		rs := New(WithStore(hangingStore{}))
		defer rs.Close()
		h := mux(rs)
		ts := httptest.NewServer(h)
		defer ts.Close()
		const bound = time.Second
		start := time.Now()
		register(t, h, regBody("rt_h"))
		if d := time.Since(start); d > bound {
			t.Errorf("register took %v with a hanging store, want < %v", d, bound)
		}
		r, closeStream := subscribe(t, rs, ts.URL, "rt_h", "")
		defer closeStream()
		go func() {
			for {
				if _, _, _, err := readEvent(r); err != nil {
					return
				}
			}
		}()
		start = time.Now()
		c1 := mustEnqueue(t, rs, "rt_h", Command{Agent: "acme-support"})
		c2 := mustEnqueue(t, rs, "rt_h", Command{Agent: "acme-support"})
		if d := time.Since(start); d > bound {
			t.Errorf("two enqueues took %v with a hanging store, want < %v", d, bound)
		}
		var wg sync.WaitGroup
		for _, c := range []Command{c1, c2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				code := postAck(h, Ack{CommandID: c.CommandID, State: "accepted", RunID: "pg_" + c.CommandID})
				if d := time.Since(start); code != http.StatusOK || d > bound {
					t.Errorf("a concurrent accepted ack answered %d in %v with a hanging store, want 200 in < %v", code, d, bound)
				}
			}()
		}
		wg.Wait()
		waitState(t, rs, c1.CommandID, StateAccepted)
		waitState(t, rs, c2.CommandID, StateAccepted)
	})
}

// failingStore errors on every call.
type failingStore struct{}

var errStoreDown = errors.New("store down")

func (failingStore) PutCommand(context.Context, obsdb.StoredCommand) error { return errStoreDown }
func (failingStore) Commands(context.Context, obsdb.CommandQuery) ([]obsdb.StoredCommand, error) {
	return nil, errStoreDown
}
func (failingStore) PutRoute(context.Context, obsdb.RunRoute) error { return errStoreDown }
func (failingStore) Route(context.Context, string) (obsdb.RunRoute, bool, error) {
	return obsdb.RunRoute{}, false, errStoreDown
}
func (failingStore) PutBreakpoints(context.Context, string, []string) error { return errStoreDown }
func (failingStore) Breakpoints(context.Context, string) ([]string, bool, error) {
	return nil, false, errStoreDown
}
func (failingStore) PutRuntime(context.Context, obsdb.StoredRuntime) error { return errStoreDown }
func (failingStore) Runtimes(context.Context) ([]obsdb.StoredRuntime, error) {
	return nil, errStoreDown
}
func (failingStore) DeleteRuntime(context.Context, string) error { return errStoreDown }
func (failingStore) PruneCommands(context.Context, time.Time, int) (int, error) {
	return 0, errStoreDown
}

// levelCount counts this package's log lines about the store's writes.
type levelCount struct {
	mu    sync.Mutex
	warns int
	debug int
}

func (h *levelCount) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelCount) Handle(_ context.Context, r slog.Record) error {
	if !strings.HasPrefix(r.Message, "studio/runtime: the command store refused a write") {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Level >= slog.LevelWarn {
		h.warns++
	} else {
		h.debug++
	}
	return nil
}
func (h *levelCount) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelCount) WithGroup(string) slog.Handler      { return h }

// TestFailingStoreChangesNothing: a store refusing every call changes
// no command's fate — the whole lifecycle runs as in memory — and says
// so once at WARN, then at Debug.
func TestFailingStoreChangesNothing(t *testing.T) {
	lc := &levelCount{}
	prev := slog.Default()
	slog.SetDefault(slog.New(lc))
	t.Cleanup(func() { slog.SetDefault(prev) })
	rs := New(WithStore(failingStore{}), func(rs *RuntimeServer) { rs.pruneStoreEvery, rs.breakerPause = 0, 20*time.Millisecond })
	rs.AckDeadline = 40 * time.Millisecond
	rs.FinishDeadline = 80 * time.Millisecond
	defer shutdownWithin(rs, 50*time.Millisecond)
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()
	reg := regBody("rt_f")
	reg.HeartbeatMS, reg.Instance = 20, "inst_f"
	register(t, h, reg)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_f", "")
	defer closeStream()
	c := mustEnqueue(t, rs, "rt_f", Command{Agent: "acme-support"})
	nextRun(t, r)
	for _, a := range []Ack{
		{CommandID: c.CommandID, State: "accepted", RunID: "pg_f"},
		{CommandID: c.CommandID, State: "accepted", RunID: "pg_f"},
		{CommandID: c.CommandID, State: "finished", RunID: "pg_f", Status: "succeeded"},
	} {
		if code := postAck(h, a); code != http.StatusOK {
			t.Fatalf("%s ack = %d", a.State, code)
		}
	}
	waitState(t, rs, c.CommandID, StateFinished)
	if rt, ok := rs.RuntimeOf("pg_f"); !ok || rt != "rt_f" {
		t.Errorf("RuntimeOf(pg_f) = %q %v", rt, ok)
	}
	if err := rs.SetBreakpoints("rt_f", []string{"refund"}); err != nil {
		t.Fatal(err)
	}
	lost := mustEnqueue(t, rs, "rt_f", Command{Agent: "acme-support"})
	waitState(t, rs, lost.CommandID, StateLost)
	acc := mustEnqueue(t, rs, "rt_f", Command{Agent: "acme-support"})
	postAck(h, Ack{CommandID: acc.CommandID, State: "accepted", RunID: "pg_acc"})
	waitState(t, rs, acc.CommandID, StateLost)
	rs.Snapshot()
	time.Sleep(100 * time.Millisecond) // the breaker re-applies, fails, holds again
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.warns != 1 || lc.debug == 0 {
		t.Errorf("store lines: %d WARN, %d Debug — want one WARN, then Debug", lc.warns, lc.debug)
	}
}

// TestRestartLoadBoundaries: each rule at its edge, with the clock
// fixed at the load — the ack window's 30 s, the 10 min watch of a
// runtime without a heartbeat (its row without a body), Updated when no
// accepted time was stored, and a queued row whose runtime has no
// stored row (lost, and never handed to a process that registers under
// its id).
func TestRestartLoadBoundaries(t *testing.T) {
	ctx := context.Background()
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().Truncate(time.Second)
	if err := cs.PutRuntime(ctx, obsdb.StoredRuntime{ID: "rt_nb", Service: "s", Host: "h", Pid: 1,
		Agents: []string{"acme-support"}, Registered: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	put := func(id, rt, state string, created, updated, acc time.Time) {
		t.Helper()
		b, _ := json.Marshal(Command{CommandID: id, Agent: "acme-support"})
		if err := cs.PutCommand(ctx, obsdb.StoredCommand{ID: id, RuntimeID: rt, Kind: "run", State: state, Agent: "acme-support",
			Created: created, Updated: updated, AcceptedAt: acc, Body: b}); err != nil {
			t.Fatal(err)
		}
	}
	z := time.Time{}
	put("cmd_q_at", "rt_nb", StateQueued, now.Add(-30*time.Second), now.Add(-30*time.Second), z)
	put("cmd_q_in", "rt_nb", StateQueued, now.Add(-29*time.Second), now.Add(-29*time.Second), z)
	put("cmd_a_at", "rt_nb", StateAccepted, now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-10*time.Minute))
	put("cmd_a_in", "rt_nb", StateAccepted, now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-10*time.Minute+time.Second))
	put("cmd_a_upd", "rt_nb", StateAccepted, now.Add(-time.Hour), now.Add(-11*time.Minute), z)
	put("cmd_orphan", "rt_orphan", StateQueued, now.Add(-time.Second), now.Add(-time.Second), z)
	rs := New(WithStore(cs), func(rs *RuntimeServer) { rs.now = func() time.Time { return now } })
	t.Cleanup(rs.Close)

	for _, c := range []struct{ id, state, why string }{
		{"cmd_q_at", StateLost, "Studio restarted; no ack since " + now.Add(-30*time.Second).UTC().Format(time.RFC3339)},
		{"cmd_q_in", StateQueued, ""},
		{"cmd_a_at", StateLost, "Studio restarted; no heartbeat since " + now.Add(-10*time.Minute).UTC().Format(time.RFC3339)},
		{"cmd_a_in", StateAccepted, ""},
		{"cmd_a_upd", StateLost, "Studio restarted; no heartbeat since " + now.Add(-11*time.Minute).UTC().Format(time.RFC3339)},
		{"cmd_orphan", StateLost, "Studio restarted; the runtime's identity is not stored"},
	} {
		st, err := rs.Command(c.id)
		why := ""
		if st.Error != nil {
			why = *st.Error
		}
		if err != nil || st.State != c.state || why != c.why {
			t.Errorf("%s at load = %s %q (%v), want %s %q", c.id, st.State, why, err, c.state, c.why)
		}
	}
	if v := rs.Snapshot(); len(v) != 1 || v[0].ID != "rt_nb" || !v[0].Restored || v[0].Connected {
		t.Errorf("runtimes at load = %+v, want rt_nb restored and disconnected", v)
	}

	// The orphan's id registered by a fresh process: nothing is sent.
	rs.mu.Lock()
	rs.now = time.Now
	rs.mu.Unlock()
	h := mux(rs)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	o := regBody("rt_orphan")
	o.Pid, o.Instance = 777, "inst_new"
	register(t, h, o)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_orphan", "")
	t.Cleanup(closeStream)
	noRunWithin(t, r, 150*time.Millisecond)
}

// TestRestartRuntimesGrace: a runtime restored from the store is listed
// (disconnected, restored) for FinishDeadline after the load however
// long Studio was down — then by the memory prune's rule.
func TestRestartRuntimesGrace(t *testing.T) {
	ctx := context.Background()
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now()
	if err := cs.PutRuntime(ctx, obsdb.StoredRuntime{ID: "rt_quiet", Service: "s", Host: "h", Pid: 1,
		Agents: []string{"acme-support"}, Registered: now.Add(-3 * time.Hour), LastSeen: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	clock := now
	var mu sync.Mutex
	rs := New(WithStore(cs), func(rs *RuntimeServer) {
		rs.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	})
	t.Cleanup(rs.Close)
	if v := rs.Snapshot(); len(v) != 1 || v[0].ID != "rt_quiet" || !v[0].Restored {
		t.Fatalf("a runtime last seen 2 h before the load = %+v, want listed restored", v)
	}
	mu.Lock()
	clock = now.Add(rs.FinishDeadline - time.Second)
	mu.Unlock()
	if v := rs.Snapshot(); len(v) != 1 {
		t.Fatalf("inside the grace = %+v, want still listed", v)
	}
	mu.Lock()
	clock = now.Add(rs.FinishDeadline + time.Second)
	mu.Unlock()
	if v := rs.Snapshot(); len(v) != 0 {
		t.Errorf("past the grace = %+v, want pruned (nothing pending, no breakpoints)", v)
	}
}

// TestRestartKeepsAnotherProcessesWork: a disconnected id whose process
// still holds work (an accepted run) is not reclaimed by another
// process after a restart — the newcomer is told <id>-<pid>, the run's
// route stays, and the old process reconnecting keeps its id.
func TestRestartKeepsAnotherProcessesWork(t *testing.T) {
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	rs := New(WithStore(cs))
	ts := httptest.NewServer(mux(rs))
	a := regBody("rt_x")
	a.Instance, a.Pid, a.HeartbeatMS = "inst_a", 100, 60_000
	register(t, mux(rs), a)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_x", "")
	in := "hi"
	x := mustEnqueue(t, rs, "rt_x", Command{Agent: "acme-support", Input: &in})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: x.CommandID, State: "accepted", RunID: "pg_a"})
	rs.Close()
	closeStream()
	ts.Close()

	rs2 := New(WithStore(cs))
	t.Cleanup(rs2.Close)
	h := mux(rs2)
	b := a
	b.Instance, b.Pid = "inst_b", 200
	if out := register(t, h, b); out.RuntimeID != "rt_x-200" {
		t.Errorf("another process on an id holding an accepted run got %q, want rt_x-200", out.RuntimeID)
	}
	if rt, ok := rs2.RuntimeOf("pg_a"); !ok || rt != "rt_x" {
		t.Errorf("RuntimeOf(pg_a) = %q %v, want rt_x", rt, ok)
	}
	if out := register(t, h, a); out.RuntimeID != "rt_x" {
		t.Errorf("the old process coming back got %q, want rt_x", out.RuntimeID)
	}
	if reg, _ := rs2.Registration("rt_x"); reg.Instance != "inst_a" {
		t.Errorf("rt_x's registration = %+v, want the old process's", reg)
	}
	if reg, _ := rs2.Registration("rt_x-200"); reg.Instance != "inst_b" {
		t.Errorf("rt_x-200's registration = %+v, want the newcomer's", reg)
	}
}

// TestRestartWithTheRuntime: Studio and the app restart together (a new
// process, the same stable id). The old process's queued command is
// never delivered to the new one, the accepted one is never re-sent,
// the new process works under <id>-<pid>, and the old one's late words
// still land on its rows.
func TestRestartWithTheRuntime(t *testing.T) {
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	rs := New(WithStore(cs))
	ts := httptest.NewServer(mux(rs))
	a := regBody("rt_x")
	a.Instance, a.Pid, a.HeartbeatMS = "inst_a", 100, 60_000
	register(t, mux(rs), a)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_x", "")
	in := "hi"
	q := mustEnqueue(t, rs, "rt_x", Command{Agent: "acme-support", Input: &in})
	nextRun(t, r)
	x := mustEnqueue(t, rs, "rt_x", Command{Agent: "acme-support", Input: &in})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: x.CommandID, State: "accepted", RunID: "pg_x"})
	rs.Close()
	closeStream()
	ts.Close()

	rs2 := New(WithStore(cs), func(rs *RuntimeServer) { rs.AckDeadline = 300 * time.Millisecond })
	t.Cleanup(rs2.Close)
	h := mux(rs2)
	ts2 := httptest.NewServer(h)
	t.Cleanup(ts2.Close)
	b := a
	b.Instance, b.Pid = "inst_b", 200
	if out := register(t, h, b); out.RuntimeID != "rt_x-200" {
		t.Fatalf("the new process = %s, want rt_x-200", out.RuntimeID)
	}
	r2, closeStream2 := subscribe(t, rs2, ts2.URL, "rt_x-200", "")
	t.Cleanup(closeStream2)
	noRunWithin(t, r2, 150*time.Millisecond)
	if st := waitState(t, rs2, q.CommandID, StateLost); st.Error == nil || *st.Error != "no ack in time" {
		t.Errorf("the old process's queued command = %+v, want lost by its ack window", st)
	}
	if st, _ := rs2.Command(x.CommandID); st.State != StateAccepted {
		t.Errorf("the accepted command = %s, want accepted (never re-sent)", st.State)
	}
	if code := postAck(h, Ack{CommandID: x.CommandID, State: "finished", RunID: "pg_x", Status: "succeeded"}); code != http.StatusOK {
		t.Errorf("the old process's late finish = %d", code)
	}
	waitState(t, rs2, x.CommandID, StateFinished)
	if rt, _ := rs2.RuntimeOf("pg_x"); rt != "rt_x" {
		t.Errorf("pg_x routes to %q, want rt_x", rt)
	}
}
