package runtime

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fastServer is a RuntimeServer with §10.5's timers tightened to test
// scale.
func fastServer() *RuntimeServer {
	rs := New()
	rs.AckDeadline = 40 * time.Millisecond
	rs.FinishDeadline = 60 * time.Millisecond
	rs.PingEvery = 10 * time.Millisecond
	return rs
}

// regBody is one well-formed registration.
func regBody(id string) Registration {
	return Registration{
		RuntimeID:   id,
		Host:        "wajih-laptop",
		Pid:         4121,
		Service:     "acme-api",
		Env:         "dev",
		WeftVersion: "v0.6.0",
		Budget:      Budget{MaxTokensPerExperiment: 200000, MaxRunsPerExperiment: 60},
		Threads:     true,
		Agents: []AgentRegistration{{
			Name:        "acme-support",
			Manifest:    `{"weft":1,"agents":[{"name":"acme-support","model":{"provider":"wefttest","name":"script"},"policy":{"parallelism":4,"max_steps":10,"max_model_retries":3},"tools":[{"name":"lookup_order"},{"name":"refund"}]}]}`,
			Models:      []string{"glm-5.3-flash"},
			Limits:      AgentLimits{MaxSteps: 10, Parallelism: 4},
			SideEffects: map[string]string{"lookup_order": "never", "refund": "never"},
			Allow:       []string{"lookup_order"},
		}},
	}
}

// register registers a runtime over HTTP.
func register(t *testing.T, h http.Handler, reg Registration) RegisterResponse {
	t.Helper()
	b, _ := json.Marshal(reg)
	resp, err := http.Post(httptest.NewServer(h).URL+"/api/runtime/register", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	var out RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mux(rs *RuntimeServer) *http.ServeMux {
	m := http.NewServeMux()
	rs.Mount(m)
	return m
}

// subscribe opens the SSE command stream and returns a reader plus a
// done channel for the handler's end.
func subscribe(t *testing.T, rs *RuntimeServer, tsURL, runtimeID, lastEventID string) (*bufio.Reader, func()) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, tsURL+"/api/runtime/commands?runtime="+runtimeID, nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commands: %d", resp.StatusCode)
	}
	var once sync.Once
	return bufio.NewReader(resp.Body), func() {
		once.Do(func() { _ = resp.Body.Close() })
	}
}

// readEvent reads one SSE frame; nextEvent's error-returning core, for
// callers that must not touch t (the bounded reader's goroutine).
func readEvent(r *bufio.Reader) (id, event string, data []byte, err error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", "", nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			if event != "" || id != "" || data != nil {
				return id, event, data, nil
			}
		}
	}
}

// nextEvent reads one SSE frame.
func nextEvent(t *testing.T, r *bufio.Reader) (id, event string, data []byte) {
	t.Helper()
	id, event, data, err := readEvent(r)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return id, event, data
}

// idle reports a frame that carries no command: a ping, or the empty
// breakpoint set every stream open is told (a runtime with no stored
// set — the handshake's own tests read these frames unfiltered).
func idle(event string, data []byte) bool {
	return event == "ping" || (event == "breakpoints" && string(data) == `{"tools":[]}`)
}

// nextRun reads the next frame that is not idle (the stream opens with
// a ping and the breakpoint set).
func nextRun(t *testing.T, r *bufio.Reader) (id string, data []byte) {
	t.Helper()
	for {
		id, event, data := nextEvent(t, r)
		if idle(event, data) {
			continue
		}
		return id, data
	}
}

// nextRunBounded is nextRun behind a deadline: a frame that never
// comes fails the test within wait instead of blocking forever. The
// ping stream makes an unbounded read especially dangerous — pings
// keep it alive every PingEvery, so a read whose supply depends on
// the expectation under test wedges the whole binary for go test's
// default ten minutes. Every read like that goes through here.
func nextRunBounded(t *testing.T, r *bufio.Reader, wait time.Duration) (id string, data []byte) {
	t.Helper()
	type frame struct {
		id   string
		data []byte
	}
	frames := make(chan frame, 1)
	errs := make(chan error, 1)
	go func() {
		for {
			id, event, data, err := readEvent(r)
			if err != nil {
				errs <- err
				return
			}
			if idle(event, data) {
				continue
			}
			frames <- frame{id: id, data: data}
			return
		}
	}()
	select {
	case f := <-frames:
		return f.id, f.data
	case err := <-errs:
		t.Fatalf("stream: %v", err)
	case <-time.After(wait):
		t.Fatalf("no run frame within %v", wait)
	}
	return "", nil // unreachable; Fatalf does not return
}

func mustEnqueue(t *testing.T, rs *RuntimeServer, runtimeID string, cmd Command) Command {
	t.Helper()
	out, err := rs.Enqueue(runtimeID, cmd)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return out
}

func waitState(t *testing.T, rs *RuntimeServer, id, want string) CommandStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := rs.Command(id)
		if err == nil && st.State == want {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	st, err := rs.Command(id)
	t.Fatalf("command %s never reached %q (now %+v, err %v)", id, want, st, err)
	return CommandStatus{}
}

func ack(t *testing.T, rs *RuntimeServer, a Ack) {
	t.Helper()
	body, _ := json.Marshal(a)
	// Acks go through the server's route: build a throwaway server per
	// call is wasteful but honest for unit scale.
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/runtime/acks", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack %s: %d", a.State, resp.StatusCode)
	}
}

// TestRegisterResponse pins §10.3's register round trip: the response
// names the runtime id and its commands url.
func TestRegisterResponse(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	out := register(t, mux(rs), regBody("rt_test1"))
	if out.RuntimeID != "rt_test1" {
		t.Errorf("runtime_id = %q", out.RuntimeID)
	}
	if want := "/api/runtime/commands?runtime=rt_test1"; out.CommandsURL != want {
		t.Errorf("commands_url = %q, want %q", out.CommandsURL, want)
	}

	// A registration without an id or without agents is a 400.
	for _, bad := range []string{`{}`, `{"runtime_id":"rt_x"}`} {
		resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("register(%s) = %d, want 400", bad, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// TestEnqueueDeliversAndAcks pins the §10.5 happy path: enqueue while
// connected, the frame arrives with the command id as its SSE id, the
// accepted ack moves the row, the finished ack closes it.
func TestEnqueueDeliversAndAcks(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_ok"))

	r, closeStream := subscribe(t, rs, ts.URL, "rt_ok", "")
	defer closeStream()

	input := "where is my order #4411?"
	cmd := mustEnqueue(t, rs, "rt_ok", Command{
		Agent: "acme-support", Input: &input, Engine: "live", Thread: "ephemeral",
	})
	if !strings.HasPrefix(cmd.CommandID, "cmd_") {
		t.Errorf("command id = %q, want a cmd_ prefix", cmd.CommandID)
	}
	id, data := nextRun(t, r)
	if id != cmd.CommandID {
		t.Fatalf("frame = %s, want %s run", id, cmd.CommandID)
	}
	var got Command
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Agent != "acme-support" || *got.Input != input {
		t.Errorf("command on the wire = %+v", got)
	}
	if strings.Contains(string(data), `"Runtime"`) || strings.Contains(string(data), `"runtime"`) {
		t.Errorf("the wire carries the server's bookkeeping: %s", data)
	}

	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "accepted", RunID: "pg_1"})
	st := waitState(t, rs, cmd.CommandID, StateAccepted)
	if st.RunID != "pg_1" {
		t.Errorf("run id = %q", st.RunID)
	}
	ack(t, rs, Ack{CommandID: cmd.CommandID, State: "finished", RunID: "pg_1", Status: "succeeded"})
	if st = waitState(t, rs, cmd.CommandID, StateFinished); st.Error != nil {
		t.Errorf("finished row carries an error: %v", st.Error)
	}
}

// TestEnqueueErrors pins the enqueue refusals: unknown runtime,
// not connected, and a reused command id (the 409 side of
// at-most-once).
func TestEnqueueErrors(t *testing.T) {
	rs := fastServer()
	if _, err := rs.Enqueue("rt_nope", Command{}); err != ErrUnknownRuntime {
		t.Errorf("unknown runtime: %v", err)
	}
	register(t, mux(rs), regBody("rt_registered"))
	if _, err := rs.Enqueue("rt_registered", Command{}); err != ErrNotConnected {
		t.Errorf("never-connected runtime: %v", err)
	}
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	_, closeStream := subscribe(t, rs, ts.URL, "rt_registered", "")
	mustEnqueue(t, rs, "rt_registered", Command{CommandID: "cmd_same"})
	if _, err := rs.Enqueue("rt_registered", Command{CommandID: "cmd_same"}); err != ErrDuplicateCommand {
		t.Errorf("reused id: %v", err)
	}
	closeStream()
}

// TestLostUnacked pins §10.5's first timer: a runtime that never acks
// loses the command after AckDeadline.
func TestLostUnacked(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_slow"))
	r, closeStream := subscribe(t, rs, ts.URL, "rt_slow", "")
	defer closeStream()
	cmd := mustEnqueue(t, rs, "rt_slow", Command{})
	nextRun(t, r) // delivered; never acked
	waitState(t, rs, cmd.CommandID, StateLost)
}

// TestLostOnDisconnect pins the sweep: queued commands of a runtime
// whose stream dies are lost at once; an accepted one is lost only
// after FinishDeadline without a finish — and a late finish wins.
func TestLostOnDisconnect(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_gone"))

	// A first stream to accept one command, then it dies.
	r1, close1 := subscribe(t, rs, ts.URL, "rt_gone", "")
	mustEnqueue(t, rs, "rt_gone", Command{CommandID: "cmd_a"})
	nextRun(t, r1)
	ack(t, rs, Ack{CommandID: "cmd_a", State: "accepted", RunID: "pg_a"})
	waitState(t, rs, "cmd_a", StateAccepted)
	close1()
	waitState(t, rs, "cmd_a", StateLost) // accepted + no finish in time

	// A queued command when the stream dies is lost immediately.
	r2, close2 := subscribe(t, rs, ts.URL, "rt_gone", "")
	mustEnqueue(t, rs, "rt_gone", Command{CommandID: "cmd_b"})
	nextRun(t, r2)
	close2()
	waitState(t, rs, "cmd_b", StateLost)

	// A late finish corrects the row: the truth wins over the timer.
	ack(t, rs, Ack{CommandID: "cmd_a", State: "finished", RunID: "pg_a", Status: "succeeded"})
	waitState(t, rs, "cmd_a", StateFinished)
}

// TestResumeWithLastEventID pins the SSE resume: a reconnect carrying
// Last-Event-ID is re-sent the still-queued commands after that
// cursor, and nothing the runtime already saw that is no longer
// queued. An unacked queued row has two real removers — the ack timer
// and the disconnect sweep (§10.5) — and which one governs after an
// unacked delivery plus a disconnect is an ordering race, so the test
// controls the ordering instead of asserting blind:
//
//   - the resume stream is subscribed BEFORE stream 1 dies, so
//     serveCommands has already replaced the runtime's feed when the
//     sweep runs and the sweep deterministically no-ops on the stale
//     feed guard (runtime.go:496) — cmd2 provably reaches the resume
//     queued and the backlog rule governs. The other ordering (the
//     sweep winning) is already pinned by TestLostOnDisconnect.
//   - the ack deadline stays a real timer at New's production scale,
//     not fastServer's 40 ms: this test pins §10.3's backlog rule,
//     not the timer (TestLostUnacked owns that one), and no
//     test-scale step can reach 30 s.
//
// Every read whose supply would depend on the expectation above it is
// bounded, so a wrong expectation fails in seconds instead of wedging
// on the ping stream.
func TestResumeWithLastEventID(t *testing.T) {
	rs := fastServer()
	rs.AckDeadline = 30 * time.Second
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_resume"))

	// Stream 1: two commands, both delivered; cmd1 acked (the cursor
	// row), cmd2 left queued — delivery changes nothing, only an ack
	// does.
	r1, close1 := subscribe(t, rs, ts.URL, "rt_resume", "")
	defer close1()
	cmd1 := mustEnqueue(t, rs, "rt_resume", Command{})
	cmd2 := mustEnqueue(t, rs, "rt_resume", Command{})
	id1, _ := nextRun(t, r1)
	id2, _ := nextRun(t, r1)
	if id1 != cmd1.CommandID || id2 != cmd2.CommandID {
		t.Fatalf("delivery order = %s, %s", id1, id2)
	}
	ack(t, rs, Ack{CommandID: cmd1.CommandID, State: "accepted", RunID: "pg_1"})
	waitState(t, rs, cmd1.CommandID, StateAccepted)

	// The resume — subscribed before stream 1 dies. serveCommands
	// replaces c.feed and snapshots the backlog under one lock hold
	// (runtime.go:404-411) before its opening ping reaches the wire,
	// so once that ping is read the replacement is settled: the later
	// close1 sweep no-ops on the stale-feed guard instead of racing
	// the resume for cmd2.
	r2, close2 := subscribe(t, rs, ts.URL, "rt_resume", cmd1.CommandID)
	defer close2()
	if _, event, _ := nextEvent(t, r2); event != "ping" {
		t.Fatalf("the resume opened with %q, want the initial ping", event)
	}
	close1()

	// The accepted cursor cmd1 is not re-sent (a wrongly re-sent
	// cursor would sort before cmd2 and be the first frame); the
	// still-queued cmd2 is re-delivered after it (backlogLocked,
	// runtime.go:465). Bounded read: a wrong backlog fails within 2 s
	// instead of wedging on the ping stream.
	if id, _ := nextRunBounded(t, r2, 2*time.Second); id != cmd2.CommandID {
		t.Errorf("after resume, first frame = %s, want the still-queued %s", id, cmd2.CommandID)
	}

	// A command enqueued under the new stream arrives after the
	// backlog — enqueued before the read so this read's supply, too,
	// never depends on the expectation above it.
	cmd3 := mustEnqueue(t, rs, "rt_resume", Command{})
	if id3, _ := nextRunBounded(t, r2, 2*time.Second); id3 != cmd3.CommandID {
		t.Errorf("after resume, next frame = %s, want the new command %s", id3, cmd3.CommandID)
	}
}

// TestPingKeepalive pins the SSE housekeeping: ping frames flow on the
// cadence and update last_seen.
func TestPingKeepalive(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_ping"))
	before := rs.Snapshot()[0].LastSeen
	r, closeStream := subscribe(t, rs, ts.URL, "rt_ping", "")
	defer closeStream()
	for {
		_, event, _ := nextEvent(t, r)
		if event == "ping" {
			break
		}
	}
	if after := rs.Snapshot()[0].LastSeen; !after.After(before) {
		t.Error("a ping did not move last_seen")
	}
}

// TestSnapshotShape pins §10.4's GET /api/runtimes row: identity,
// liveness, and per agent the models and every tool with its
// side-effect class and allow flag.
func TestSnapshotShape(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_view"))
	_, closeStream := subscribe(t, rs, ts.URL, "rt_view", "")
	defer closeStream()

	views := rs.Snapshot()
	if len(views) != 1 {
		t.Fatalf("views = %d", len(views))
	}
	v := views[0]
	if v.ID != "rt_view" || v.Host != "wajih-laptop" || v.Pid != 4121 ||
		v.Service != "acme-api" || v.Env != "dev" {
		t.Errorf("identity = %+v", v)
	}
	if v.ConnectedSince.IsZero() || v.LastSeen.IsZero() {
		t.Errorf("liveness = %+v", v)
	}
	if len(v.Agents) != 1 || v.Agents[0].Name != "acme-support" {
		t.Fatalf("agents = %+v", v.Agents)
	}
	a := v.Agents[0]
	if len(a.Models) != 1 || a.Models[0] != "glm-5.3-flash" {
		t.Errorf("models = %v", a.Models)
	}
	wantTools := []ToolView{
		{Name: "lookup_order", SideEffects: "never", Allow: true},
		{Name: "refund", SideEffects: "never", Allow: false},
	}
	if len(a.Tools) != 2 || a.Tools[0] != wantTools[0] || a.Tools[1] != wantTools[1] {
		t.Errorf("tools = %+v, want %+v", a.Tools, wantTools)
	}

	// The JSON shape itself: field names and nesting exactly as §10.4.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"id":"rt_view"`, `"host":"wajih-laptop"`, `"pid":4121`,
		`"service":"acme-api"`, `"env":"dev"`, `"connected_since"`, `"last_seen"`,
		`"agents":[{`, `"name":"acme-support"`, `"models":["glm-5.3-flash"]`,
		`"name":"lookup_order"`, `"side_effects":"never"`, `"allow":true`, `"allow":false`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("snapshot JSON lacks %s: %s", key, b)
		}
	}
}

// TestCancelFrame pins the cancel wire shape.
func TestCancelFrame(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_cancel"))
	r, closeStream := subscribe(t, rs, ts.URL, "rt_cancel", "")
	defer closeStream()
	rs.Cancel("rt_cancel", "cmd_x")
	for {
		_, event, data := nextEvent(t, r)
		if idle(event, data) {
			continue
		}
		if event != "cancel" || !strings.Contains(string(data), `"command_id":"cmd_x"`) {
			t.Errorf("cancel frame = %s %s", event, data)
		}
		break
	}
}

// TestCommandStatusUnknown pins the 404 for an unknown command id.
func TestCommandStatusUnknown(t *testing.T) {
	rs := fastServer()
	if _, err := rs.Command("cmd_nope"); err != ErrUnknownCommand {
		t.Errorf("unknown command: %v", err)
	}
	resp, err := http.Post(httptest.NewServer(mux(rs)).URL+"/api/runtime/acks",
		"application/json", strings.NewReader(`{"command_id":"cmd_nope","state":"accepted"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ack for an unknown command = %d, want 404", resp.StatusCode)
	}
}

// TestCommandIDShape pins the mint: cmd_ + 26 time-ordered chars,
// monotonic in-process (the SSE resume cursor compares them).
func TestCommandIDShape(t *testing.T) {
	prev := ""
	for i := 0; i < 200; i++ {
		id := newCommandID()
		if !strings.HasPrefix(id, "cmd_") || len(id) != len("cmd_")+ulidLen {
			t.Fatalf("id = %q", id)
		}
		if prev != "" && id < prev {
			t.Fatalf("ids regressed: %q after %q", id, prev)
		}
		prev = id
	}
}

// TestRegisterReplacesCopy pins "register after every reconnect": the
// second registration's agents replace the first's (the runtime's
// copy is authoritative, §10.4).
func TestRegisterReplacesCopy(t *testing.T) {
	rs := fastServer()
	reg := regBody("rt_again")
	reg.Agents[0].Models = []string{"old-model"}
	register(t, mux(rs), reg)
	reg.Agents[0].Models = []string{"new-model"}
	register(t, mux(rs), reg)
	got, ok := rs.Registration("rt_again")
	if !ok || len(got.Agents) != 1 || got.Agents[0].Models[0] != "new-model" {
		t.Errorf("registration after re-register = %+v ok=%v", got, ok)
	}
}

// TestFullFeedEndsStalledStream pins the audit's P2-9: when Enqueue
// drops a full feed, the stalled stream must be terminated — the old
// code nilled the feed and left the SSE open, so its pings kept the
// runtime looking connected while every POST 503'd on the nil feed
// until the stream happened to break. The drop is driven at the unit
// level (a live stream drains its feed into the socket buffer as fast
// as it fills, so a full feed needs a reader that is not reading).
func TestFullFeedEndsStalledStream(t *testing.T) {
	rs := fastServer()
	rs.mu.Lock()
	stalled := make(chan Command, 1)
	stalled <- Command{CommandID: "cmd_stuck"} // full: cap 1, no reader
	rs.runtimes["rt_stall"] = &connected{reg: regBody("rt_stall"), feed: stalled}
	rs.mu.Unlock()

	// The next enqueue finds the feed full: the stream is dropped, the
	// channel closed, the runtime disconnected.
	dropped, err := rs.Enqueue("rt_stall", Command{})
	if err != nil {
		t.Fatalf("enqueue onto a full feed: %v (the drop, not an error)", err)
	}
	if rs.Connected("rt_stall") {
		t.Error("runtime still connected after the feed drop")
	}
	if _, err := rs.Enqueue("rt_stall", Command{}); err != ErrNotConnected {
		t.Errorf("enqueue after the drop = %v, want ErrNotConnected", err)
	}
	// The terminated stream sees its feed closed — a reader parked on
	// it wakes with ok=false instead of pinging forever (the buffered
	// frame delivers first, then the close).
	<-stalled
	if _, ok := <-stalled; ok {
		t.Error("the dropped feed was not closed")
	}

	// The queued command survived (the backlog re-sends it), and a
	// reconnect on a real stream serves it.
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	r, closeStream := subscribe(t, rs, ts.URL, "rt_stall", "")
	defer closeStream()
	if _, event, _ := nextEvent(t, r); event != "ping" {
		t.Fatalf("the reconnect opened with %q, want the initial ping", event)
	}
	if !rs.Connected("rt_stall") {
		t.Error("runtime not connected after the reconnect")
	}
	if id, _ := nextRunBounded(t, r, 2*time.Second); id != dropped.CommandID {
		t.Errorf("after the reconnect, first frame = %s, want the queued %s", id, dropped.CommandID)
	}
}

// TestLateAcceptedAckIsRefused pins the at-most-once rule across the
// ack window: a command the lost sweep took (no accepted ack in time)
// is never revived by a late accepted ack — the user may already have
// re-issued it, and both would run. The late ack is 409 and the row
// stays lost (weft/runtime runs nothing on a non-200 accepted ack); a
// finished ack still records what did run.
func TestLateAcceptedAckIsRefused(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_late"))
	r, closeStream := subscribe(t, rs, ts.URL, "rt_late", "")
	defer closeStream()

	cmd := mustEnqueue(t, rs, "rt_late", Command{CommandID: "cmd_late"})
	nextRun(t, r) // delivered; no ack
	waitState(t, rs, cmd.CommandID, StateLost)

	post := func(a Ack) int {
		t.Helper()
		body, _ := json.Marshal(a)
		resp, err := http.Post(ts.URL+"/api/runtime/acks", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(Ack{CommandID: "cmd_late", State: "accepted", RunID: "pg_late"}); code != http.StatusConflict {
		t.Errorf("late accepted ack = %d, want 409", code)
	}
	if st, err := rs.Command("cmd_late"); err != nil || st.State != StateLost {
		t.Errorf("after the late accepted ack the row is %+v (%v), want lost", st, err)
	}
	// A finished ack still lands: what ran is recorded.
	ack(t, rs, Ack{CommandID: "cmd_late", State: "finished", RunID: "pg_late", Status: "succeeded"})
	waitState(t, rs, "cmd_late", StateFinished)
}

// TestAcceptedRowWatchedByHeartbeat pins the finish watch on a
// connected runtime's accepted command (Registration.HeartbeatMS): a
// runtime that heartbeats and then falls silent — its accepted ack's
// response lost, it ran nothing and could not say so — has the row
// marked lost after the watch although its stream stays open; one
// whose heartbeats keep coming stays accepted however long it runs
// (here well past FinishDeadline) and finishes; a runtime older than
// the heartbeat (0) is not watched while connected, as before.
func TestAcceptedRowWatchedByHeartbeat(t *testing.T) {
	rs := fastServer()
	rs.FinishDeadline = 40 * time.Millisecond // the watch: max(40 ms, 3 × 10 ms)
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	beating := regBody("rt_beat")
	beating.HeartbeatMS = 10
	register(t, mux(rs), beating)
	register(t, mux(rs), regBody("rt_old"))
	rb, closeB := subscribe(t, rs, ts.URL, "rt_beat", "")
	defer closeB()
	ro, closeO := subscribe(t, rs, ts.URL, "rt_old", "")
	defer closeO()

	// Silent after the accepted ack: lost, the stream still open.
	mustEnqueue(t, rs, "rt_beat", Command{CommandID: "cmd_silent"})
	nextRun(t, rb)
	ack(t, rs, Ack{CommandID: "cmd_silent", State: "accepted", RunID: "pg_silent"})
	st := waitState(t, rs, "cmd_silent", StateLost)
	if want := "accepted, but no finish and no heartbeat from the runtime in 40ms"; st.Error == nil || *st.Error != want {
		t.Errorf("lost reason = %v, want %q", st.Error, want)
	}
	if !rs.Connected("rt_beat") {
		t.Error("the runtime's stream closed: the watch fired on a disconnect, not on silence")
	}

	// Beating: accepted for 3× FinishDeadline, then its finish lands.
	mustEnqueue(t, rs, "rt_beat", Command{CommandID: "cmd_long"})
	nextRun(t, rb)
	ack(t, rs, Ack{CommandID: "cmd_long", State: "accepted", RunID: "pg_long"})
	for end := time.Now().Add(3 * rs.FinishDeadline); time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
		ack(t, rs, Ack{CommandID: "cmd_long", State: "accepted", RunID: "pg_long"})
	}
	if st, _ := rs.Command("cmd_long"); st.State != StateAccepted {
		t.Fatalf("a heartbeating command is %+v, want accepted", st)
	}
	ack(t, rs, Ack{CommandID: "cmd_long", State: "finished", RunID: "pg_long", Status: "succeeded"})
	waitState(t, rs, "cmd_long", StateFinished)

	// An older runtime, connected: no watch.
	mustEnqueue(t, rs, "rt_old", Command{CommandID: "cmd_old"})
	nextRun(t, ro)
	ack(t, rs, Ack{CommandID: "cmd_old", State: "accepted", RunID: "pg_old"})
	time.Sleep(2 * rs.FinishDeadline)
	if st, _ := rs.Command("cmd_old"); st.State != StateAccepted {
		t.Errorf("an older runtime's connected command is %+v, want accepted (no heartbeat, no watch)", st)
	}
}
