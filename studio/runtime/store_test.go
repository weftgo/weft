package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// openStore opens the sqlite store at path (obsdbtest's pattern: a
// real file, closed with the test).
func openStore(t *testing.T, path string) (obsdb.DB, obsdb.CommandStore) {
	t.Helper()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cs, ok := db.(obsdb.CommandStore)
	if !ok {
		t.Fatal("obsdb/sqlite does not implement obsdb.CommandStore")
	}
	return db, cs
}

// stored reads one command row back from the store.
func stored(t *testing.T, cs obsdb.CommandStore, id string) obsdb.StoredCommand {
	t.Helper()
	rows, err := cs.Commands(context.Background(), obsdb.CommandQuery{ID: id})
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored command %s: %v %v", id, rows, err)
	}
	return rows[0]
}

// flush waits until the server's writer has applied every queued
// write (the hot path only queues).
func flush(t *testing.T, rs *RuntimeServer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !rs.writer.idle() {
		if time.Now().After(deadline) {
			t.Fatal("the store writer never drained")
		}
		time.Sleep(time.Millisecond)
	}
}

// noRunWithin fails when a non-idle frame arrives within wait.
func noRunWithin(t *testing.T, r *bufio.Reader, wait time.Duration) {
	t.Helper()
	frames := make(chan string, 1)
	go func() {
		for {
			id, event, data, err := readEvent(r)
			if err != nil {
				return
			}
			if idle(event, data) || event == "breakpoints" {
				continue
			}
			frames <- event + " " + id
			return
		}
	}()
	select {
	case f := <-frames:
		t.Fatalf("a frame was re-sent after the restart: %s", f)
	case <-time.After(wait):
	}
}

// TestRestartRestoresState: a store-backed server's commands, routes,
// breakpoints and registrations survive a restart over the same
// database — the accepted command stays accepted with its heartbeat
// watch re-armed from the stored beat, the route answers RuntimeOf and
// PublicOf, the breakpoint set is served and re-sent on the stream, the
// runtime is listed disconnected (restored) until it registers again,
// and a finished command answers as history.
func TestRestartRestoresState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.db")
	db1, cs1 := openStore(t, path)
	rs := New(WithStore(cs1))
	ts := httptest.NewServer(mux(rs))
	reg := regBody("rt_keep")
	reg.Cwd = "/srv/acme"
	reg.Instance = "inst_1"
	reg.HeartbeatMS = 60_000
	register(t, mux(rs), reg)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_keep", "")

	in := "where is my order?"
	held := mustEnqueue(t, rs, "rt_keep", Command{Agent: "acme-support", Input: &in, PublicID: "pub_1", Thread: "fork", ExperimentID: "exp_1"})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: held.CommandID, State: "accepted", RunID: "pg_held"})
	done := mustEnqueue(t, rs, "rt_keep", Command{Agent: "acme-support", Input: &in})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: done.CommandID, State: "accepted", RunID: "pg_done"})
	ack(t, rs, Ack{CommandID: done.CommandID, State: "finished", RunID: "pg_done", Status: "succeeded"})
	if err := rs.SetBreakpoints("rt_keep", []string{"refund"}, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := rs.Command(held.CommandID)

	// The Studio goes away: its timers and writes stop, its database
	// closes, its socket closes.
	rs.Close()
	closeStream()
	ts.Close()
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	db2, cs2 := openStore(t, path)
	t.Cleanup(func() { _ = db2.Close() })
	rs2 := New(WithStore(cs2))
	t.Cleanup(rs2.Close)

	st, err := rs2.Command(held.CommandID)
	if err != nil || st.State != StateAccepted || st.RunID != "pg_held" || !st.Created.Equal(before.Created) {
		t.Fatalf("held command after the restart = %+v %v, want accepted pg_held created %v", st, err, before.Created)
	}
	rs2.mu.Lock()
	row := rs2.commands[held.CommandID]
	armed, cadence, fork := row.lostTimer != nil, row.beat, row.Thread
	rs2.mu.Unlock()
	if !armed || cadence != time.Minute {
		t.Errorf("held command's watch after the restart: armed %v beat %v, want armed at the registered 1m beat", armed, cadence)
	}
	if fork != "fork" {
		t.Errorf("held command's thread mode = %q, want fork (restored from the row)", fork)
	}
	if cmd, ok := rs2.CommandOf(held.CommandID); !ok || cmd.PublicID != "pub_1" || cmd.ExperimentID != "exp_1" || cmd.Input == nil || *cmd.Input != in {
		t.Errorf("CommandOf after the restart = %+v %v", cmd, ok)
	}
	if st, _ := rs2.Command(done.CommandID); st.State != StateFinished || st.Status != "succeeded" {
		t.Errorf("finished command after the restart = %+v, want finished succeeded", st)
	}
	if id, ok := rs2.RuntimeOf("pg_held"); !ok || id != "rt_keep" {
		t.Errorf("RuntimeOf(pg_held) = %q %v, want rt_keep", id, ok)
	}
	if pub, ok := rs2.PublicOf("pg_held"); !ok || pub != "pub_1" {
		t.Errorf("PublicOf(pg_held) = %q %v, want pub_1", pub, ok)
	}
	if bp := rs2.BreakpointsOf("rt_keep"); !slices.Equal(bp, []string{"refund"}) {
		t.Errorf("breakpoints after the restart = %v, want [refund]", bp)
	}
	views := rs2.Snapshot()
	if len(views) != 1 || views[0].ID != "rt_keep" || views[0].Connected || !views[0].Restored ||
		views[0].Cwd != "/srv/acme" || !slices.Equal(views[0].Breakpoints, []string{"refund"}) || len(views[0].Agents) != 1 {
		t.Fatalf("runtimes after the restart = %+v, want rt_keep restored, disconnected, cwd and breakpoints kept", views)
	}

	// The same process reconnects: listed connected again, told the
	// stored set first, and nothing is re-sent (the accepted command is
	// in its hands; the finished one is history).
	ts2 := httptest.NewServer(mux(rs2))
	t.Cleanup(ts2.Close)
	register(t, mux(rs2), reg)
	r2, closeStream2 := subscribe(t, rs2, ts2.URL, "rt_keep", held.CommandID)
	t.Cleanup(closeStream2)
	for {
		_, event, data := nextEvent(t, r2)
		if event == "ping" {
			continue
		}
		if event != "breakpoints" || string(data) != `{"tools":["refund"]}` {
			t.Fatalf("first frame after the reconnect = %s %s, want the stored breakpoint set", event, data)
		}
		break
	}
	noRunWithin(t, r2, 100*time.Millisecond)
	if v := rs2.Snapshot(); !v[0].Connected || v[0].Restored {
		t.Errorf("after the reconnect: connected %v restored %v, want true false", v[0].Connected, v[0].Restored)
	}
	// The runtime's word still lands: a heartbeat, then the finish.
	beat(t, mux(rs2), held.CommandID, "pg_held")
	ack(t, rs2, Ack{CommandID: held.CommandID, State: "finished", RunID: "pg_held", Status: "succeeded"})
	waitState(t, rs2, held.CommandID, StateFinished)
	flush(t, rs2)
	if sc := stored(t, cs2, held.CommandID); sc.State != obsdb.CommandFinished || sc.LastBeat.IsZero() || sc.AcceptedAt.IsZero() {
		t.Errorf("stored row after the finish = %+v, want finished with its accepted and beat times", sc)
	}
}

// TestRestartLostDecidedFromStore: what a restart decides from stored
// state alone — an accepted command whose stored last beat is past its
// watch is lost at load, a queued one past its ack window is lost at
// load, both with the reason and written back; a queued one inside the
// window is re-sent to the same process only, and lost when another
// process registers under the id.
func TestRestartLostDecidedFromStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "weft.db")
	db, cs := openStore(t, path)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now()
	reg := regBody("rt_old")
	reg.Instance = "inst_a"
	reg.HeartbeatMS = 1000
	body, _ := json.Marshal(reg)
	if err := cs.PutRuntime(ctx, obsdb.StoredRuntime{ID: "rt_old", Service: reg.Service, Host: reg.Host, Pid: reg.Pid,
		Agents: []string{"acme-support"}, Registered: now.Add(-2 * time.Hour), LastSeen: now.Add(-50 * time.Minute), Body: body}); err != nil {
		t.Fatal(err)
	}
	in := "hello"
	put := func(id, state string, created, acceptedAt, lastBeat time.Time) {
		t.Helper()
		b, _ := json.Marshal(Command{CommandID: id, Agent: "acme-support", Input: &in})
		if err := cs.PutCommand(ctx, obsdb.StoredCommand{ID: id, RuntimeID: "rt_old", Kind: "run", State: state,
			Agent: "acme-support", Created: created, Updated: created, AcceptedAt: acceptedAt, LastBeat: lastBeat, Body: b}); err != nil {
			t.Fatal(err)
		}
	}
	lastBeat := now.Add(-50 * time.Minute)
	put("cmd_silent", obsdb.CommandAccepted, now.Add(-time.Hour), now.Add(-time.Hour), lastBeat)
	put("cmd_alive", obsdb.CommandAccepted, now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-time.Minute))
	put("cmd_stale", obsdb.CommandQueued, now.Add(-time.Minute), time.Time{}, time.Time{})
	put("cmd_fresh", obsdb.CommandQueued, now, time.Time{}, time.Time{})
	put("cmd_other", obsdb.CommandQueued, now, time.Time{}, time.Time{})

	rs := New(WithStore(cs), func(rs *RuntimeServer) { rs.AckDeadline = 30 * time.Second })
	t.Cleanup(rs.Close)

	st := waitState(t, rs, "cmd_silent", StateLost)
	want := "Studio restarted; no heartbeat since " + lastBeat.UTC().Format(time.RFC3339)
	if st.Error == nil || *st.Error != want {
		t.Errorf("silent accepted command's reason = %v, want %q", st.Error, want)
	}
	flush(t, rs)
	if sc := stored(t, cs, "cmd_silent"); sc.State != obsdb.CommandLost || sc.Error != want {
		t.Errorf("the lost decision was not written back: %+v", sc)
	}
	if st, _ := rs.Command("cmd_alive"); st.State != StateAccepted {
		t.Errorf("an accepted command beating a minute ago (watch 10m) = %s, want accepted", st.State)
	}
	st = waitState(t, rs, "cmd_stale", StateLost)
	if st.Error == nil || !strings.HasPrefix(*st.Error, "Studio restarted; no ack since ") {
		t.Errorf("stale queued command's reason = %v", st.Error)
	}
	if st, _ := rs.Command("cmd_fresh"); st.State != StateQueued {
		t.Fatalf("a queued command inside its ack window = %s, want queued", st.State)
	}

	// The same process reconnects: the queued commands are re-sent (its
	// at-most-once set decides whether they run).
	ts := httptest.NewServer(mux(rs))
	t.Cleanup(ts.Close)
	register(t, mux(rs), reg)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_old", "")
	got := map[string]bool{}
	for len(got) < 2 {
		id, _ := nextRunBounded(t, r, 2*time.Second)
		got[id] = true
	}
	if !got["cmd_fresh"] || !got["cmd_other"] {
		t.Errorf("re-sent to the same process = %v, want cmd_fresh and cmd_other", got)
	}
	ack(t, rs, Ack{CommandID: "cmd_fresh", State: "accepted", RunID: "pg_fresh"})
	closeStream()
	waitDisconnected(t, rs, "rt_old")
	// The disconnect itself lost cmd_other (queued, no ack): the rule
	// without a restart.
	waitState(t, rs, "cmd_other", StateLost)

	// A different process: a restored queued row is never handed to
	// it — the id stays the old process's while the row is live, the
	// newcomer is told rt_old-9999 and its stream re-sends nothing; the
	// row ends lost by its own ack window.
	put("cmd_stranded", obsdb.CommandQueued, now, time.Time{}, time.Time{})
	rs.Close()
	rs2 := New(WithStore(cs), func(rs *RuntimeServer) { rs.AckDeadline = 300 * time.Millisecond })
	t.Cleanup(rs2.Close)
	if st, _ := rs2.Command("cmd_stranded"); st.State != StateQueued {
		t.Fatalf("cmd_stranded at load = %s, want queued", st.State)
	}
	ts2 := httptest.NewServer(mux(rs2))
	t.Cleanup(ts2.Close)
	other := reg
	other.Pid, other.Instance = 9999, "inst_b"
	if out := register(t, mux(rs2), other); out.RuntimeID != "rt_old-9999" {
		t.Errorf("a new process on an id holding another's work got %q, want rt_old-9999", out.RuntimeID)
	}
	r2, closeStream2 := subscribe(t, rs2, ts2.URL, "rt_old-9999", "")
	t.Cleanup(closeStream2)
	noRunWithin(t, r2, 100*time.Millisecond)
	st = waitState(t, rs2, "cmd_stranded", StateLost)
	if st.Error == nil || *st.Error != "no ack in time" {
		t.Errorf("stranded queued command's reason = %v", st.Error)
	}
}

// TestRegisterCollisionSuffixesID: an id another process holds a live
// stream on is answered `<id>-<pid>`; the same process's reconnect
// keeps its id; a disconnected entry is reclaimed under the id.
func TestRegisterCollisionSuffixesID(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	a := regBody("rt_same")
	a.Instance = "inst_a"
	if out := register(t, mux(rs), a); out.RuntimeID != "rt_same" {
		t.Fatalf("first registration = %q", out.RuntimeID)
	}
	_, closeA := subscribe(t, rs, ts.URL, "rt_same", "")
	defer closeA()

	if out := register(t, mux(rs), a); out.RuntimeID != "rt_same" {
		t.Errorf("the same process re-registering = %q, want rt_same", out.RuntimeID)
	}
	b := a
	b.Pid, b.Instance = 5150, "inst_b"
	out := register(t, mux(rs), b)
	if out.RuntimeID != "rt_same-5150" || out.CommandsURL != "/api/runtime/commands?runtime=rt_same-5150" {
		t.Errorf("a second live process = %+v, want rt_same-5150", out)
	}
	_, closeB := subscribe(t, rs, ts.URL, "rt_same-5150", "")
	defer closeB()
	// A third link of that second process (one pid, another instance).
	c := b
	c.RuntimeID, c.Instance = "rt_same", "inst_c"
	if out := register(t, mux(rs), c); out.RuntimeID != "rt_same-5150-2" {
		t.Errorf("a third live link = %q, want rt_same-5150-2", out.RuntimeID)
	}
	if reg, ok := rs.Registration("rt_same"); !ok || reg.Instance != "inst_a" {
		t.Errorf("the live entry was overwritten by a colliding registration: %+v", reg)
	}

	closeA()
	waitDisconnected(t, rs, "rt_same")
	if out := register(t, mux(rs), c); out.RuntimeID != "rt_same" {
		t.Errorf("a disconnected entry holding no work reclaimed = %q, want rt_same", out.RuntimeID)
	}
}

// recordingStore is an obsdb.CommandStore that records its calls and
// fails the test's check when two of them overlap — every write is
// made under the server's mutex, so none may (PruneCommands' ClickHouse
// caveat).
type recordingStore struct {
	inFlight atomic.Int32
	overlaps atomic.Int32
	mu       sync.Mutex
	calls    []string
}

func (s *recordingStore) enter(name string) func() {
	if s.inFlight.Add(1) > 1 {
		s.overlaps.Add(1)
	}
	s.mu.Lock()
	s.calls = append(s.calls, name)
	s.mu.Unlock()
	return func() { s.inFlight.Add(-1) }
}

func (s *recordingStore) PutCommand(context.Context, obsdb.StoredCommand) error {
	defer s.enter("PutCommand")()
	return nil
}

func (s *recordingStore) Commands(context.Context, obsdb.CommandQuery) ([]obsdb.StoredCommand, error) {
	defer s.enter("Commands")()
	return nil, nil
}

func (s *recordingStore) PutRoute(context.Context, obsdb.RunRoute) error {
	defer s.enter("PutRoute")()
	return nil
}

func (s *recordingStore) Route(context.Context, string) (obsdb.RunRoute, bool, error) {
	defer s.enter("Route")()
	return obsdb.RunRoute{}, false, nil
}

func (s *recordingStore) PutBreakpoints(context.Context, string, obsdb.BreakpointSet) error {
	defer s.enter("PutBreakpoints")()
	return nil
}

func (s *recordingStore) Breakpoints(context.Context, string) (obsdb.BreakpointSet, bool, error) {
	defer s.enter("Breakpoints")()
	return obsdb.BreakpointSet{}, false, nil
}

func (s *recordingStore) PutRuntime(context.Context, obsdb.StoredRuntime) error {
	defer s.enter("PutRuntime")()
	return nil
}

func (s *recordingStore) Runtimes(context.Context) ([]obsdb.StoredRuntime, error) {
	defer s.enter("Runtimes")()
	return nil, nil
}

func (s *recordingStore) DeleteRuntime(context.Context, string) error {
	defer s.enter("DeleteRuntime")()
	return nil
}

func (s *recordingStore) PruneCommands(context.Context, time.Time, int) (int, error) {
	defer s.enter("PruneCommands")()
	time.Sleep(time.Millisecond) // widen the window a racing Put would hit
	return 0, nil
}

func (s *recordingStore) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c == name {
			n++
		}
	}
	return n
}

// TestNoStoreUnchanged: without a store (WithStore(nil), or no option)
// nothing is written — a call on the nil interface would panic — and
// the lifecycle is the in-memory one.
func TestNoStoreUnchanged(t *testing.T) {
	var none obsdb.CommandStore
	rs := New(WithStore(none))
	rs.AckDeadline = 40 * time.Millisecond
	rs.FinishDeadline = 60 * time.Millisecond
	rs.pruneStoreEvery = 0
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()
	register(t, h, regBody("rt_mem"))
	r, closeStream := subscribe(t, rs, ts.URL, "rt_mem", "")
	defer closeStream()
	cmd := mustEnqueue(t, rs, "rt_mem", Command{Agent: "acme-support"})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: "pg_mem"})
	if err := rs.SetBreakpoints("rt_mem", []string{"refund"}, ""); err != nil {
		t.Fatal(err)
	}
	rs.Snapshot()
	lost := mustEnqueue(t, rs, "rt_mem", Command{Agent: "acme-support"})
	waitState(t, rs, lost.CommandID, StateLost)
	rs.mu.Lock()
	storing := rs.storing()
	rs.mu.Unlock()
	if storing {
		t.Error("a server without a store writes through")
	}
	if v := rs.Snapshot(); len(v) != 1 || v[0].Restored {
		t.Errorf("snapshot = %+v", v)
	}
}
