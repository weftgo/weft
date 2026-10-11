package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// dropLog counts the writer's "stopped with writes still held" and
// "queue is full" WARNs.
type dropLog struct {
	mu      sync.Mutex
	stopped []string
	capped  []int64 // each cap WARN's running total
}

func (h *dropLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *dropLog) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelWarn {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case strings.HasPrefix(r.Message, "studio/runtime: the command store writer stopped"):
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "keys" {
				h.stopped = append(h.stopped, a.Value.String())
			}
			return true
		})
	case strings.HasPrefix(r.Message, "studio/runtime: the command store writer's queue is full"):
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "total" {
				h.capped = append(h.capped, a.Value.Int64())
			}
			return true
		})
	}
	return nil
}
func (h *dropLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *dropLog) WithGroup(string) slog.Handler      { return h }

func captureDrops(t *testing.T) *dropLog {
	t.Helper()
	h := &dropLog{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// lifecycle registers rt (its registration's write is the one a blip
// armed before it hits), runs one command to finished (run pg_<rt>)
// and enqueues a second, left queued.
func lifecycle(t *testing.T, rs *RuntimeServer, blip *blipStore, rt string) (done, window Command, stop func()) {
	t.Helper()
	h := mux(rs)
	ts := httptest.NewServer(h)
	reg := regBody(rt)
	reg.Instance = "inst_" + rt
	register(t, h, reg)
	if blip != nil {
		waitTrip(t, blip)
	}
	r, closeStream := subscribe(t, rs, ts.URL, rt, "")
	done = mustEnqueue(t, rs, rt, Command{Agent: "acme-support"})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: done.CommandID, State: "accepted", RunID: "pg_" + rt})
	ack(t, rs, Ack{CommandID: done.CommandID, State: "finished", RunID: "pg_" + rt, Status: "succeeded"})
	window = mustEnqueue(t, rs, rt, Command{Agent: "acme-support"})
	return done, window, func() { closeStream(); ts.Close() }
}

// TestStoreBreakerRequeue: a blip on the last write of a key nothing
// supersedes (the finished ack's) is put back and lands after the
// pause.
func TestStoreBreakerRequeue(t *testing.T) {
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	blip := &blipStore{CommandStore: cs}
	rs := New(WithStore(blip), func(rs *RuntimeServer) { rs.breakerPause = 200 * time.Millisecond })
	defer rs.Close()
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()
	reg := regBody("rt_q")
	reg.Instance = "inst_q"
	register(t, h, reg)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_q", "")
	defer closeStream()
	cmd := mustEnqueue(t, rs, "rt_q", Command{Agent: "acme-support"})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: "pg_q"})
	flush(t, rs)
	blip.mu.Lock()
	blip.failOn = func(what string) bool { return strings.HasPrefix(what, "command:"+cmd.CommandID+":finished") }
	blip.mu.Unlock()
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "finished", RunID: "pg_q", Status: "succeeded"})
	waitTrip(t, blip)
	flush(t, rs)
	if sc := stored(t, cs, cmd.CommandID); sc.State != obsdb.CommandFinished {
		t.Errorf("the blipped finished write after the pause: stored %s, want finished (re-applied)", sc.State)
	}
}

// TestStoreCloseDuringThePause is the reviewer's probe: a blip, then
// Close at once with the default 30 s pause. The store has recovered,
// so Close's drain applies the held queue at once — Close returns in
// well under a second — and after a restart every row is right.
func TestStoreCloseDuringThePause(t *testing.T) {
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	blip := &blipStore{CommandStore: cs, failNext: true}
	rs := New(WithStore(blip))
	if rs.breakerPause != storeBreakerPause {
		t.Fatalf("pause = %v, want the default", rs.breakerPause)
	}
	done, window, stop := lifecycle(t, rs, blip, "rt_p")
	defer stop()
	start := time.Now()
	rs.Close()
	if d := time.Since(start); d > time.Second {
		t.Errorf("Close during the pause took %v over a recovered store, want < 1s", d)
	}

	rs2 := New(WithStore(cs))
	t.Cleanup(rs2.Close)
	if st, err := rs2.Command(done.CommandID); err != nil || st.State != StateFinished {
		t.Errorf("the finished command after the restart = %+v %v, want finished", st, err)
	}
	if _, err := rs2.Command(window.CommandID); err != nil {
		t.Errorf("the window's command after the restart: %v", err)
	}
	if rt, ok := rs2.RuntimeOf("pg_rt_p"); !ok || rt != "rt_p" {
		t.Errorf("RuntimeOf after the restart = %q %v, want rt_p", rt, ok)
	}
	if v := rs2.Snapshot(); len(v) != 1 || v[0].ID != "rt_p" {
		t.Errorf("runtimes after the restart = %+v, want rt_p (its blipped write re-applied)", v)
	}
}

// TestStoreDeadAtClose: a store still failing when the Studio stops —
// a refusing one ends the drain at its first failure, a hanging one at
// Close's bound — loses the writes still held, and one WARN says how
// many keys.
func TestStoreDeadAtClose(t *testing.T) {
	t.Run("refusing", func(t *testing.T) {
		logs := captureDrops(t)
		rs := New(WithStore(failingStore{}))
		_, _, stop := lifecycle(t, rs, nil, "rt_d")
		defer stop()
		start := time.Now()
		rs.Close()
		if d := time.Since(start); d > time.Second {
			t.Errorf("Close over a refusing store took %v, want the first failure to end the drain", d)
		}
		logs.mu.Lock()
		defer logs.mu.Unlock()
		if len(logs.stopped) != 1 || logs.stopped[0] == "0" {
			t.Errorf("drop WARNs = %v, want one naming the held keys", logs.stopped)
		}
	})
	t.Run("hanging", func(t *testing.T) {
		logs := captureDrops(t)
		rs := New(WithStore(hangingStore{}))
		_, _, stop := lifecycle(t, rs, nil, "rt_h")
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if err := rs.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Shutdown over a hanging store = %v, want its bound", err)
		}
		select {
		case <-rs.writer.done:
		default:
			t.Error("the writer is still running after Shutdown returned")
		}
		logs.mu.Lock()
		defer logs.mu.Unlock()
		if len(logs.stopped) != 1 || logs.stopped[0] == "0" {
			t.Errorf("drop WARNs = %v, want one naming the held keys", logs.stopped)
		}
	})
}

// TestStoreQueueCap: against a dead store the held queue never exceeds
// its cap — route writes go first, then the oldest finished commands'
// (documented: such a command may read lost or unknown after a
// restart), with a WARN carrying the running total on the first drop
// and each time it doubles — the live commands', the runtime's and the
// breakpoint set's writes are kept, and the memory prune forgets the
// writes of the rows it removes.
func TestStoreQueueCap(t *testing.T) {
	logs := captureDrops(t)
	const capKeys = 40
	var mu sync.Mutex
	clock := time.Now()
	rs := New(WithStore(failingStore{}), func(rs *RuntimeServer) {
		rs.breakerPause, rs.maxHeld = time.Hour, capKeys
		rs.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	})
	defer shutdownWithin(rs, 100*time.Millisecond)
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()
	reg := regBody("rt_cap")
	reg.Instance = "inst_cap"
	register(t, h, reg)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_cap", "")
	defer closeStream()
	go func() {
		for {
			if _, _, _, err := readEvent(r); err != nil {
				return
			}
		}
	}()
	if err := rs.SetBreakpoints("rt_cap", []string{"refund"}); err != nil {
		t.Fatal(err)
	}
	var live []string
	for i := 0; i < 5; i++ {
		c := mustEnqueue(t, rs, "rt_cap", Command{Agent: "acme-support"})
		postAck(h, Ack{CommandID: c.CommandID, State: "accepted", RunID: "pg_live_" + c.CommandID})
		live = append(live, c.CommandID)
	}
	maxSeen := 0
	for i := 0; i < 300; i++ {
		c := mustEnqueue(t, rs, "rt_cap", Command{Agent: "acme-support"})
		run := "pg_" + c.CommandID
		postAck(h, Ack{CommandID: c.CommandID, State: "accepted", RunID: run})
		postAck(h, Ack{CommandID: c.CommandID, State: "finished", RunID: run, Status: "succeeded"})
		rs.writer.mu.Lock()
		maxSeen = max(maxSeen, len(rs.writer.order))
		rs.writer.mu.Unlock()
	}
	if maxSeen > capKeys {
		t.Errorf("the held queue reached %d keys, cap %d", maxSeen, capKeys)
	}
	w := rs.writer
	w.mu.Lock()
	for _, id := range live {
		if _, ok := w.pending[writeKey{"command", id}]; !ok {
			t.Errorf("a live (accepted) command's write was dropped: %s", id)
		}
	}
	for _, k := range []writeKey{{"runtime", "rt_cap"}, {"breakpoints", "rt_cap"}} {
		if _, ok := w.pending[k]; !ok {
			t.Errorf("%v's write was dropped", k)
		}
	}
	// Routes before any finished command: finished commands were
	// dropped (300 lifecycles under a cap of 40), so no route write may
	// be held, while the newest finished commands' writes still are.
	routes, finished := 0, 0
	for _, k := range w.order {
		switch {
		case k.kind == "route":
			routes++
		case k.kind == "command" && !slices.Contains(live, k.id):
			finished++
		}
	}
	if routes != 0 || finished == 0 {
		t.Errorf("held: %d route writes, %d finished commands' — want routes dropped before any finished command's", routes, finished)
	}
	w.mu.Unlock()
	logs.mu.Lock()
	if n := len(logs.capped); n < 2 || n > 12 {
		t.Errorf("cap WARNs = %v, want one per doubling of the running total", logs.capped)
	}
	for i := 1; i < len(logs.capped); i++ {
		if logs.capped[i] < 2*logs.capped[i-1] {
			t.Errorf("cap WARN totals %v: a WARN before the total doubled", logs.capped)
			break
		}
	}
	logs.mu.Unlock()

	// The memory prune forgets what it removes: past Retention, every
	// finished command's and its route's write goes with the row.
	mu.Lock()
	clock = clock.Add(rs.Retention + time.Hour)
	mu.Unlock()
	rs.Snapshot()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, k := range w.order {
		if k.kind == "route" || (k.kind == "command" && !slices.Contains(live, k.id)) {
			t.Errorf("a pruned row's write is still held: %v", k)
		}
	}
}
