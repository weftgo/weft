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

// nextEvent reads one SSE frame.
func nextEvent(t *testing.T, r *bufio.Reader) (id, event string, data []byte) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream: %v", err)
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
				return id, event, data
			}
		}
	}
}

// nextRun reads the next non-ping frame (the stream opens with one).
func nextRun(t *testing.T, r *bufio.Reader) (id string, data []byte) {
	t.Helper()
	for {
		id, event, data := nextEvent(t, r)
		if event == "ping" {
			continue
		}
		return id, data
	}
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
// Last-Event-ID is re-sent the queued commands after that cursor, and
// nothing the runtime already saw that is no longer queued.
func TestResumeWithLastEventID(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	register(t, mux(rs), regBody("rt_resume"))

	// First stream: two commands, both acked; then it dies.
	r1, close1 := subscribe(t, rs, ts.URL, "rt_resume", "")
	cmd1 := mustEnqueue(t, rs, "rt_resume", Command{})
	cmd2 := mustEnqueue(t, rs, "rt_resume", Command{})
	id1, _ := nextRun(t, r1)
	id2, _ := nextRun(t, r1)
	defer close1()
	if id1 != cmd1.CommandID || id2 != cmd2.CommandID {
		t.Fatalf("delivery order = %s, %s", id1, id2)
	}
	ack(t, rs, Ack{CommandID: cmd1.CommandID, State: "accepted", RunID: "pg_1"})
	close1()

	// A queued command lands while no stream exists: it cannot be
	// delivered (the route would have refused), so enqueue it after
	// the new stream opens.
	r2, close2 := subscribe(t, rs, ts.URL, "rt_resume", cmd1.CommandID)
	defer close2()
	cmd3 := mustEnqueue(t, rs, "rt_resume", Command{})
	id3, _ := nextRun(t, r2)
	if id3 != cmd3.CommandID {
		t.Errorf("after resume, first frame = %s, want the new command %s", id3, cmd3.CommandID)
	}
	_ = cmd2
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
		if event == "ping" {
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
