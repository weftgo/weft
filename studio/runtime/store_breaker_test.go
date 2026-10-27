package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// blipStore wraps a real store: armed, its next write fails once (a
// blip); every write that lands is logged with its time.
type blipStore struct {
	obsdb.CommandStore
	mu       sync.Mutex
	failNext bool
	// failOn, when set, blips the first write it matches instead (once);
	// block, when set, holds that failing call until it is closed.
	failOn   func(what string) bool
	block    chan struct{}
	failing  chan struct{} // closed when the matched call is in flight
	failedAt time.Time
	landed   []time.Time
	order    []string // "command:<id>:<state>:<run>" | "route:<run>" | …
}

func (b *blipStore) gate(what string) error {
	b.mu.Lock()
	if b.failNext || (b.failOn != nil && b.failOn(what)) {
		b.failNext, b.failOn = false, nil
		b.failedAt = time.Now()
		block, failing := b.block, b.failing
		b.mu.Unlock()
		if failing != nil {
			close(failing)
		}
		if block != nil {
			<-block
		}
		return errors.New("blip")
	}
	defer b.mu.Unlock()
	b.landed = append(b.landed, time.Now())
	b.order = append(b.order, what)
	return nil
}

// tripped reports whether the blip happened.
func (b *blipStore) tripped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.failedAt.IsZero()
}

func (b *blipStore) PutCommand(ctx context.Context, c obsdb.StoredCommand) error {
	if err := b.gate(fmt.Sprintf("command:%s:%s:%s", c.ID, c.State, c.RunID)); err != nil {
		return err
	}
	return b.CommandStore.PutCommand(ctx, c)
}

func (b *blipStore) PutRoute(ctx context.Context, r obsdb.RunRoute) error {
	if err := b.gate("route:" + r.RunID); err != nil {
		return err
	}
	return b.CommandStore.PutRoute(ctx, r)
}

func (b *blipStore) PutBreakpoints(ctx context.Context, id string, tools obsdb.BreakpointSet) error {
	if err := b.gate("breakpoints:" + id); err != nil {
		return err
	}
	return b.CommandStore.PutBreakpoints(ctx, id, tools)
}

func (b *blipStore) PutRuntime(ctx context.Context, r obsdb.StoredRuntime) error {
	if err := b.gate("runtime:" + r.ID); err != nil {
		return err
	}
	return b.CommandStore.PutRuntime(ctx, r)
}

func (b *blipStore) PruneCommands(ctx context.Context, before time.Time, keep int) (int, error) {
	if err := b.gate("prune"); err != nil {
		return 0, err
	}
	return b.CommandStore.PruneCommands(ctx, before, keep)
}

// waitTrip waits for b's blip.
func waitTrip(t *testing.T, b *blipStore) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !b.tripped() {
		if time.Now().After(deadline) {
			t.Fatal("the blip never happened")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStoreBreaker: one failed store call holds the writer's queue for
// the pause — nothing reaches the store meanwhile, the queue still
// coalescing — and then every key's latest write lands, the failed one
// included. The reviewer's probe: a blip before the accepted ack used
// to drop the accepted and finished writes (the command read lost after
// a restart, the window's commands 404, the route gone); after a
// restart now the finished command reads finished, the window's
// command is answered and the route is there.
func TestStoreBreaker(t *testing.T) {
	const pause = 300 * time.Millisecond
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	blip := &blipStore{CommandStore: cs}
	rs := New(WithStore(blip), func(rs *RuntimeServer) { rs.breakerPause = pause })
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()

	blip.mu.Lock()
	blip.failNext = true // the registration's own write blips
	blip.mu.Unlock()
	reg := regBody("rt_b")
	reg.Instance = "inst_b"
	register(t, h, reg)
	waitTrip(t, blip)
	// Inside the pause from here on: the whole lifecycle.
	r, closeStream := subscribe(t, rs, ts.URL, "rt_b", "")
	defer closeStream()
	done := mustEnqueue(t, rs, "rt_b", Command{Agent: "acme-support"})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: done.CommandID, State: "accepted", RunID: "pg_b"})
	ack(t, rs, Ack{CommandID: done.CommandID, State: "finished", RunID: "pg_b", Status: "succeeded"})
	if err := rs.SetBreakpoints("rt_b", []string{"refund"}, ""); err != nil {
		t.Fatal(err)
	}
	window := mustEnqueue(t, rs, "rt_b", Command{Agent: "acme-support"})

	blip.mu.Lock()
	failedAt, landed := blip.failedAt, 0
	for _, at := range blip.landed {
		if at.After(failedAt) {
			landed++
		}
	}
	blip.mu.Unlock()
	if failedAt.IsZero() {
		t.Fatal("the blip never happened")
	}
	if time.Since(failedAt) < pause && landed != 0 {
		t.Fatalf("%d writes reached the store during the breaker's pause, want none (held)", landed)
	}
	flush(t, rs)
	blip.mu.Lock()
	for _, at := range blip.landed {
		if at.After(failedAt) && at.Before(failedAt.Add(pause)) {
			t.Errorf("a write landed %v after the blip, inside the %v pause", at.Sub(failedAt), pause)
		}
	}
	blip.mu.Unlock()

	// Every key's latest state landed.
	if sc := stored(t, cs, done.CommandID); sc.State != obsdb.CommandFinished || sc.RunID != "pg_b" {
		t.Errorf("stored finished command = %s %q, want finished pg_b", sc.State, sc.RunID)
	}
	if sc := stored(t, cs, window.CommandID); sc.State != obsdb.CommandQueued {
		t.Errorf("stored window command = %s, want queued", sc.State)
	}
	if rt, ok, err := cs.Route(context.Background(), "pg_b"); err != nil || !ok || rt.RuntimeID != "rt_b" {
		t.Errorf("stored route = %+v %v %v", rt, ok, err)
	}
	if bp, ok, _ := cs.Breakpoints(context.Background(), "rt_b"); !ok || !slices.Equal(bp.Tools, []string{"refund"}) {
		t.Errorf("stored breakpoints = %v %v", bp, ok)
	}
	if rows, _ := cs.Runtimes(context.Background()); len(rows) != 1 || rows[0].ID != "rt_b" {
		t.Errorf("stored runtimes = %+v, want rt_b (the blipped write re-applied)", rows)
	}

	// The probe: a restart over the store reads what happened.
	rs.Close()
	rs2 := New(WithStore(cs))
	t.Cleanup(rs2.Close)
	if st, err := rs2.Command(done.CommandID); err != nil || st.State != StateFinished {
		t.Errorf("the finished command after the restart = %+v %v, want finished", st, err)
	}
	if _, err := rs2.Command(window.CommandID); err != nil {
		t.Errorf("the window's command after the restart: %v", err)
	}
	if rt, ok := rs2.RuntimeOf("pg_b"); !ok || rt != "rt_b" {
		t.Errorf("RuntimeOf(pg_b) after the restart = %q %v", rt, ok)
	}
}

// TestHeldByOther pins heldByOtherLocked clause by clause: a
// disconnected id is another process's while it holds a queued or
// accepted command (whatever its route's age); a finished command's
// run route, however recent, does not hold it — the id is reclaimable.
func TestHeldByOther(t *testing.T) {
	now := time.Now()
	old := regBody("rt_x")
	old.Pid, old.Instance = 100, "inst_old"
	newcomer := old
	newcomer.Pid, newcomer.Instance = 200, "inst_new"
	setup := func(state string, routeAge time.Duration) *RuntimeServer {
		rs := New()
		rs.now = func() time.Time { return now }
		rs.runtimes["rt_x"] = &connected{reg: old}
		rs.commands["cmd_1"] = &commandRow{Command: Command{CommandID: "cmd_1", Runtime: "rt_x"}, state: state, runID: "pg_1"}
		rs.runs["pg_1"] = "rt_x"
		rs.runSeen["pg_1"] = now.Add(-routeAge)
		return rs
	}
	for _, c := range []struct {
		name     string
		state    string
		routeAge time.Duration
		held     bool
	}{
		{"accepted with an old route", StateAccepted, 20 * time.Minute, true},
		{"finished with a recent route", StateFinished, 5 * time.Minute, false},
		{"finished with an old route", StateFinished, 20 * time.Minute, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rs := setup(c.state, c.routeAge)
			rs.mu.Lock()
			got := rs.heldByOtherLocked("rt_x", newcomer)
			same := rs.heldByOtherLocked("rt_x", old)
			rs.mu.Unlock()
			if got != c.held {
				t.Errorf("held by the old process = %v, want %v", got, c.held)
			}
			if same {
				t.Error("the old process itself reads the id as another's")
			}
		})
	}
}

// TestRestartKeepsStableID: an app restarted as a new process (new pid,
// new instance) minutes after a playground run finished re-registers
// under the SAME stable id — weft dev restarts on every save — so it
// gets Studio's breakpoint set and the /runtimes/<id> link. Only a live
// stream or unfinished work holds an id for the old process.
func TestRestartKeepsStableID(t *testing.T) {
	rs := New()
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()
	reg := regBody("rt_stable")
	reg.Pid, reg.Instance = 1111, "inst_first"
	if got := register(t, h, reg).RuntimeID; got != "rt_stable" {
		t.Fatalf("first registration = %q", got)
	}
	r, closeStream := subscribe(t, rs, ts.URL, "rt_stable", "")
	cmd := mustEnqueue(t, rs, "rt_stable", Command{Agent: "acme-support", PublicID: "pub_s"})
	nextRun(t, r)
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: "pg_s"})
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "finished", RunID: "pg_s", Status: "succeeded"})
	closeStream()
	waitDisconnected(t, rs, "rt_stable")

	restarted := reg
	restarted.Pid, restarted.Instance = 9999, "inst_second"
	if got := register(t, h, restarted).RuntimeID; got != "rt_stable" {
		t.Errorf("a restarted process after a finished run registered as %q, want the stable rt_stable", got)
	}
}

// TestRouteAfterCommand: an ack naming a run queues the command's write
// before the route's, so a stop between them leaves a command naming
// the run and no route — and load rebuilds that route from the
// command. (The other order would leave a route whose command names no
// run, which load cannot find.)
func TestRouteAfterCommand(t *testing.T) {
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	blip := &blipStore{CommandStore: cs}
	rs := New(WithStore(blip))
	h := mux(rs)
	ts := httptest.NewServer(h)
	defer ts.Close()
	reg := regBody("rt_o")
	reg.Instance = "inst_o"
	register(t, h, reg)
	r, closeStream := subscribe(t, rs, ts.URL, "rt_o", "")
	defer closeStream()
	cmd := mustEnqueue(t, rs, "rt_o", Command{Agent: "acme-support", PublicID: "pub_o"})
	nextRun(t, r)
	flush(t, rs)
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: "pg_o"})
	flush(t, rs)
	blip.mu.Lock()
	order := append([]string{}, blip.order...)
	blip.mu.Unlock()
	ci, ri := slices.Index(order, "command:"+cmd.CommandID+":accepted:pg_o"), slices.Index(order, "route:pg_o")
	if ci < 0 || ri < 0 || ci > ri {
		t.Fatalf("write order = %v, want the accepted command before its route", order)
	}

	// Under a blip: the command's write fails while a newer one (a
	// heartbeat) is queued behind its route — the newer write moves to
	// the head, so the command still lands before the route.
	cmd2 := mustEnqueue(t, rs, "rt_o", Command{Agent: "acme-support", PublicID: "pub_o"})
	nextRun(t, r)
	flush(t, rs)
	rs.writer.pause = 100 * time.Millisecond
	blip.mu.Lock()
	blip.failOn = func(what string) bool { return strings.HasPrefix(what, "command:"+cmd2.CommandID+":accepted") }
	blip.block, blip.failing = make(chan struct{}), make(chan struct{})
	failing, block := blip.failing, blip.block
	blip.order = nil
	blip.mu.Unlock()
	ack(t, rs, Ack{CommandID: cmd2.CommandID, State: "accepted", RunID: "pg_o2"})
	<-failing                                                                     // the command's write is in flight, failing; its route waits behind it
	ack(t, rs, Ack{CommandID: cmd2.CommandID, State: "accepted", RunID: "pg_o2"}) // a heartbeat: newer write, queued after the route
	close(block)
	waitTrip(t, blip)
	flush(t, rs)
	blip.mu.Lock()
	order = append([]string{}, blip.order...)
	blip.mu.Unlock()
	ci, ri = slices.Index(order, "command:"+cmd2.CommandID+":accepted:pg_o2"), slices.Index(order, "route:pg_o2")
	if ci < 0 || ri < 0 || ci > ri {
		t.Fatalf("write order under a blip = %v, want the command before its route", order)
	}
	rs.Close()

	// The crash shape the order leaves: the command, no route — copied
	// into a fresh store without its route.
	sc := stored(t, cs, cmd.CommandID)
	ctx := context.Background()
	db2, cs2 := openStore(t, filepath.Join(t.TempDir(), "w2.db"))
	t.Cleanup(func() { _ = db2.Close() })
	rows, _ := cs.Runtimes(ctx)
	for _, rr := range rows {
		_ = cs2.PutRuntime(ctx, rr)
	}
	if err := cs2.PutCommand(ctx, sc); err != nil {
		t.Fatal(err)
	}
	rs2 := New(WithStore(cs2))
	t.Cleanup(rs2.Close)
	if rt, ok := rs2.RuntimeOf("pg_o"); !ok || rt != "rt_o" {
		t.Errorf("RuntimeOf(pg_o) with no stored route = %q %v, want rt_o rebuilt from the command", rt, ok)
	}
	if pub, ok := rs2.PublicOf("pg_o"); !ok || pub != "pub_o" {
		t.Errorf("PublicOf(pg_o) rebuilt = %q %v, want pub_o", pub, ok)
	}
}

// TestWriterStopsWithClose: the writer goroutine exits when the server
// closes.
func TestWriterStopsWithClose(t *testing.T) {
	db, cs := openStore(t, filepath.Join(t.TempDir(), "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	rs := New(WithStore(cs))
	register(t, mux(rs), regBody("rt_c"))
	w := rs.writer
	select {
	case <-w.done:
		t.Fatal("the writer exited before Close")
	default:
	}
	rs.Close()
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer goroutine is still running after Close")
	}
}
