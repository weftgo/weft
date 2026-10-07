package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// fakeStudio is the Studio side of the wire protocol, just enough to
// drive the link: it records registrations and acks, and lets the test
// push SSE frames down the command stream or break the stream to force
// a reconnect.
type fakeStudio struct {
	t  *testing.T
	mu sync.Mutex
	// registrations holds each POST /api/runtime/register body.
	registrations []registration
	// acks holds each POST /api/runtime/acks body, in order.
	acks []ack
	// lastEventIDs holds the Last-Event-ID header of each command
	// stream subscription, in order ("" when none).
	lastEventIDs []string
	// frames is pushed to the live command stream by the test.
	frames chan string
	// drop, when closed, ends every live command stream (a network
	// break) so the link reconnects.
	drop chan struct{}
}

func newFakeStudio(t *testing.T) *fakeStudio {
	return &fakeStudio{
		t:      t,
		frames: make(chan string, 16),
		drop:   make(chan struct{}),
	}
}

func (f *fakeStudio) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/runtime/register", func(w http.ResponseWriter, r *http.Request) {
		var reg registration
		if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
			f.t.Errorf("register body: %v", err)
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.registrations = append(f.registrations, reg)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(registerResponse{
			RuntimeID:   reg.RuntimeID,
			CommandsURL: "/api/runtime/commands?runtime=" + reg.RuntimeID,
		})
	})
	mux.HandleFunc("GET /api/runtime/commands", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastEventIDs = append(f.lastEventIDs, r.Header.Get("Last-Event-ID"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, "event: ping\ndata: {}\n\n")
		flusher.Flush()
		ctx := r.Context()
		for {
			select {
			case fr := <-f.frames:
				_, _ = fmt.Fprint(w, fr)
				flusher.Flush()
			case <-f.drop:
				return // the break: the stream dies, the link reconnects
			case <-ctx.Done():
				return
			}
		}
	})
	mux.HandleFunc("POST /api/runtime/acks", func(w http.ResponseWriter, r *http.Request) {
		var a ack
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			f.t.Errorf("ack body: %v", err)
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.acks = append(f.acks, a)
		n := len(f.acks)
		f.mu.Unlock()
		w.WriteHeader(200)
		_ = n
	})
	return mux
}

// waitAck blocks until n acks arrived or the deadline passes.
func (f *fakeStudio) waitAck(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.acks)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("timed out waiting for %d acks; have %d: %+v", n, len(f.acks), f.acks)
}

// gatedModel is a core.Model whose calls block until the test opens
// the gate — proving the accepted ack is sent before execution — and
// that counts its calls, proving at-most-once.
type gatedModel struct {
	model core.Model
	gate  chan struct{}
	calls chan struct{}
}

func (m *gatedModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	// The gate is the model's first instruction: until the test opens
	// it, no run can pass through this model at all.
	select {
	case <-m.gate:
	case <-ctx.Done():
		return func(yield func(core.ModelEvent, error) bool) {
			yield(nil, ctx.Err())
		}
	}
	select {
	case m.calls <- struct{}{}:
	default:
	}
	return m.model.Stream(ctx, req)
}

// newTestLink wires a link against the fake Studio with instant
// reconnects.
func newTestLink(t *testing.T, tsURL string, model core.Model, budget Budget) *link {
	t.Helper()
	agent := core.New(model, core.Name("acme-support"), core.Instructions("You are Acme's support agent."))
	cfg := &config{
		agents: []*core.Agent{agent},
		models: map[string]core.Model{},
		budget: budget,
	}
	reg := newRegistry(cfg)
	l := newLink(cfg, reg, tsURL, "")
	l.reconnect = func() time.Duration { return time.Millisecond }
	if err := l.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(l.stop)
	return l
}

func runFrame(cmdID string) string {
	return fmt.Sprintf("id: %s\nevent: run\ndata: %s\n\n", cmdID,
		`{"command_id":"`+cmdID+`","agent":"acme-support","source":null,"input":"where is my order?",`+
			`"overrides":{},"engine":"live","side_effects":"substitute","thread":"ephemeral",`+
			`"experiment_id":"exp_1","actor":"user_42","public_id":"pub_7Hk2"}`)
}

// TestLinkRegisterCommandAckRun pins the whole §10.3 flow against a
// speaking Studio: register on connect, one command down the stream,
// the accepted ack (with a pg_ run id) BEFORE the model is called, the
// run, the finished ack.
func TestLinkRegisterCommandAckRun(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	calls := make(chan struct{}, 4)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("It shipped.")), gate: gate, calls: calls}
	newTestLink(t, ts.URL, model, Budget{})

	// Registered on connect, with the payload §10.3 names.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fs.mu.Lock()
		n := len(fs.registrations)
		fs.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	fs.mu.Lock()
	reg := fs.registrations[0]
	fs.mu.Unlock()
	if !strings.HasPrefix(reg.RuntimeID, "rt_") {
		t.Errorf("runtime_id = %q, want an rt_ prefix", reg.RuntimeID)
	}
	// v0.8.0 is the pin: this line follows weftVersion() at every
	// release bump (0.7.0 itself missed it).
	if reg.WeftVersion != "v0.8.0" || reg.Pid == 0 || reg.Host == "" {
		t.Errorf("registration identity incomplete: %+v", reg)
	}
	if len(reg.Agents) != 1 || reg.Agents[0].Name != "acme-support" {
		t.Errorf("agents = %+v", reg.Agents)
	}

	// The command goes down the stream. The model's first instruction
	// is its gate, still closed: if the link executed before acking,
	// the accepted ack could never arrive and this wait would time
	// out. It arriving proves the ack was sent first.
	fs.frames <- runFrame("cmd_01JTEST0000000000000000001")
	fs.waitAck(t, 1)
	fs.mu.Lock()
	accepted := fs.acks[0]
	fs.mu.Unlock()
	if accepted.State != "accepted" || !strings.HasPrefix(accepted.RunID, "pg_") {
		t.Fatalf("first ack = %+v, want accepted with a pg_ run id", accepted)
	}
	select {
	case <-calls:
		t.Fatal("the model ran while its gate was still closed")
	default:
	}
	close(gate) // execution may proceed now
	<-calls
	fs.waitAck(t, 2)
	fs.mu.Lock()
	finished := fs.acks[1]
	fs.mu.Unlock()
	if finished.State != "finished" || finished.Status != "succeeded" || finished.RunID != accepted.RunID {
		t.Errorf("finished ack = %+v", finished)
	}
}

// TestLinkAtMostOnce pins §5.3's at-most-once rule on the runtime
// side: a command id replayed down the stream is ignored — one run,
// one pair of acks.
func TestLinkAtMostOnce(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	calls := make(chan struct{}, 4)
	gate := make(chan struct{})
	close(gate)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("ok")), gate: gate, calls: calls}
	l := newTestLink(t, ts.URL, model, Budget{})
	_ = l

	frame := runFrame("cmd_01JTEST0000000000000000002")
	fs.frames <- frame
	fs.waitAck(t, 2)
	// The replay: the same SSE id and data, twice more.
	fs.frames <- frame
	fs.frames <- frame
	time.Sleep(100 * time.Millisecond)

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.acks) != 2 {
		t.Errorf("acks after a replayed id = %d (%+v), want 2", len(fs.acks), fs.acks)
	}
	if n := len(calls); n != 1 {
		t.Errorf("model calls after a replayed id = %d, want 1", n)
	}
}

// TestLinkReconnectRegisters pins "register on connect and after every
// reconnect": a broken stream makes the link re-register and
// re-subscribe with Last-Event-ID naming the newest command it saw.
func TestLinkReconnectRegisters(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	close(gate)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("ok")), gate: gate, calls: make(chan struct{}, 4)}
	l := newTestLink(t, ts.URL, model, Budget{})
	_ = l

	fs.frames <- runFrame("cmd_01JTEST0000000000000000003")
	fs.waitAck(t, 2)

	// Break the stream; the link re-registers.
	breakOnce := make(chan struct{})
	go func() {
		close(fs.drop) // ends the live stream
		close(breakOnce)
	}()
	<-breakOnce
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fs.mu.Lock()
		regs, subs := len(fs.registrations), len(fs.lastEventIDs)
		fs.mu.Unlock()
		if regs >= 2 && subs >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.registrations) < 2 {
		t.Fatalf("registrations after a break = %d, want 2+", len(fs.registrations))
	}
	if len(fs.lastEventIDs) < 2 || !strings.HasPrefix(fs.lastEventIDs[len(fs.lastEventIDs)-1], "cmd_") {
		t.Fatalf("resume cursors = %q, want the newest command id on reconnect", fs.lastEventIDs)
	}
	if fs.registrations[0].RuntimeID != fs.registrations[1].RuntimeID {
		t.Errorf("runtime id changed across a reconnect: %q vs %q",
			fs.registrations[0].RuntimeID, fs.registrations[1].RuntimeID)
	}
}

// TestLinkBudgetBreach pins §6 rule 6 on the wire: with a one-run cap
// per experiment, the second command of that experiment is answered
// rejected budget_exceeded, and the model never runs for it.
func TestLinkBudgetBreach(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	close(gate)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("ok")), gate: gate, calls: make(chan struct{}, 4)}
	l := newTestLink(t, ts.URL, model, Budget{MaxRunsPerExperiment: 1})
	_ = l

	fs.frames <- runFrame("cmd_01JTEST0000000000000000004") // experiment exp_1
	fs.waitAck(t, 2)
	fs.frames <- runFrame("cmd_01JTEST0000000000000000005") // same experiment
	fs.waitAck(t, 3)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	third := fs.acks[2]
	if third.State != "rejected" || third.Error != "budget_exceeded" {
		t.Errorf("breach ack = %+v, want rejected budget_exceeded", third)
	}
}

// TestLinkPingAndCancel pins the two housekeeping frames: ping is
// ignored silently, and cancel reaches an in-flight run's context.
func TestLinkPingAndCancel(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	calls := make(chan struct{}, 4)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("ok")), gate: gate, calls: calls}
	l := newTestLink(t, ts.URL, model, Budget{})
	_ = l

	fs.frames <- "event: ping\ndata: {}\n\n"
	fs.frames <- "event: unknown\ndata: {}\n\n"
	time.Sleep(50 * time.Millisecond)
	fs.mu.Lock()
	if n := len(fs.acks); n != 0 {
		t.Errorf("acks after ping/unknown = %d, want 0", n)
	}
	fs.mu.Unlock()

	// A run held open at the model (its gate never opens), canceled
	// from the stream: the run's context is the dispatch context, so
	// the model's ctx.Done fires and the run finishes as failed.
	fs.frames <- runFrame("cmd_01JTEST0000000000000000006")
	fs.waitAck(t, 1) // accepted: the run is in flight, parked at the gate
	fs.frames <- "event: cancel\ndata: {\"command_id\":\"cmd_01JTEST0000000000000000006\"}\n\n"
	fs.waitAck(t, 2)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if got := fs.acks[1]; got.State != "finished" || got.Status != "failed" {
		t.Errorf("canceled run's ack = %+v, want finished failed", got)
	}
}

// frame is one `event: run` frame whose command carries extra — JSON
// members spliced into the plain input-only command.
func frame(cmdID, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf("id: %s\nevent: run\ndata: %s\n\n", cmdID,
		`{"command_id":"`+cmdID+`","agent":"acme-support","input":"where is my order?"`+extra+`}`)
}

// ackOf waits for the first ack of a command in the given state.
func (f *fakeStudio) ackOf(t *testing.T, cmdID, state string) ack {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, a := range f.acks {
			if a.CommandID == cmdID && a.State == state {
				f.mu.Unlock()
				return a
			}
		}
		f.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("no %s ack for %s; have %+v", state, cmdID, f.acks)
	return ack{}
}

// TestLinkStopCancelsRuns pins Install's stop: the runs this link
// started hang off the link — stopping it cancels them and waits for
// them. Before the fix every run was started on context.Background():
// a stopped runtime kept running tools and spending tokens, and its
// goroutines outlived the stop.
func TestLinkStopCancelsRuns(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{}) // never opens: only a canceled context ends the model call
	model := &gatedModel{model: wefttest.Script(wefttest.Say("never")), gate: gate, calls: make(chan struct{}, 4)}
	l := newTestLink(t, ts.URL, model, Budget{})

	fs.frames <- runFrame("cmd_01JTEST0000000000000000010")
	fs.ackOf(t, "cmd_01JTEST0000000000000000010", "accepted")

	stopped := make(chan struct{})
	go func() {
		l.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not return: the in-flight run was not canceled")
	}
	if got := fs.ackOf(t, "cmd_01JTEST0000000000000000010", "finished"); got.Status != "failed" {
		t.Errorf("the run a stop canceled acked %+v, want finished failed", got)
	}
	l.stop() // idempotent
}

// TestLinkResumeCursorIsArrivalOrder pins the Last-Event-ID the
// reconnect sends: the newest command frame received. Command ids may
// be the caller's own (§10.4's idempotency key), so the largest id is
// not the newest — before the fix a link that had seen "cmd_zz" resumed
// from it forever.
func TestLinkResumeCursorIsArrivalOrder(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	close(gate)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("one"), wefttest.Say("two")), gate: gate, calls: make(chan struct{}, 4)}
	newTestLink(t, ts.URL, model, Budget{})

	fs.frames <- runFrame("cmd_zz")
	fs.ackOf(t, "cmd_zz", "finished")
	fs.frames <- runFrame("cmd_aa")
	fs.ackOf(t, "cmd_aa", "finished")
	close(fs.drop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fs.mu.Lock()
		subs := len(fs.lastEventIDs)
		fs.mu.Unlock()
		if subs >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.lastEventIDs) < 2 || fs.lastEventIDs[1] != "cmd_aa" {
		t.Errorf("resume cursors = %q, want the reconnect to name cmd_aa (the newest frame received)", fs.lastEventIDs)
	}
}

// TestLinkSeenIsBounded pins the audit's P2-8: the at-most-once set
// keeps the newest maxSeen command ids, not every id since the process
// started — and inside that window a repeated id is still refused.
func TestLinkSeenIsBounded(t *testing.T) {
	cfg := &config{agents: []*core.Agent{testAgent("a")}}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	for i := range maxSeen + 100 {
		id := fmt.Sprintf("cmd_%06d", i)
		if _, ok := l.admit(id); !ok {
			t.Fatalf("a fresh command id %s was refused", id)
		}
		l.release(id)
	}
	if n := len(l.seen); n != maxSeen {
		t.Errorf("seen holds %d ids after %d commands, want the newest %d", n, maxSeen+100, maxSeen)
	}
	if len(l.seenOrder) != maxSeen {
		t.Errorf("seenOrder holds %d ids, want %d", len(l.seenOrder), maxSeen)
	}
	newest := fmt.Sprintf("cmd_%06d", maxSeen+99)
	if _, ok := l.admit(newest); ok {
		t.Error("a repeated command id was admitted twice")
	}
	for _, id := range []string{"", "cmd 1", "cmd\nevent: run", strings.Repeat("x", 129)} {
		if _, ok := l.admit(id); ok {
			t.Errorf("a command with the id %q was admitted (Studio's own rule refuses it; it could not be acked)", id)
		}
	}
	if got := l.lastEventID(); got != newest {
		t.Errorf("resume cursor = %q, want %q", got, newest)
	}
	l.stop()
	if _, ok := l.admit("cmd_after_stop"); ok {
		t.Error("a stopped link admitted a command")
	}
}

// TestLinkScriptedNeverRunsLive pins §5.5's zero-token promise on the
// failure path: a scripted command whose source transcript cannot be
// resolved is rejected. Before the fix the executor logged it and ran
// the command on the agent's own model — a "zero token" experiment
// spending real ones.
func TestLinkScriptedNeverRunsLive(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler()) // serves no /api/runs/…/transcript: the source is unresolvable
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	close(gate)
	calls := make(chan struct{}, 4)
	model := &gatedModel{model: wefttest.Script(wefttest.Say("live answer")), gate: gate, calls: calls}
	newTestLink(t, ts.URL, model, Budget{})

	fs.frames <- frame("cmd_scr", `"source":{"run_id":"s_gone-t1","from_step":0},"engine":"scripted"`)
	if got := fs.ackOf(t, "cmd_scr", "rejected"); !strings.Contains(got.Error, "source transcript unresolved") {
		t.Errorf("rejection = %+v, want the unresolved source named", got)
	}
	if n := len(calls); n != 0 {
		t.Errorf("the live model was called %d times for a scripted command", n)
	}
}

// TestLinkRejectsWhatItDoesNotRecognise pins the trust boundary
// (§10.4: the runtime checks again): a value outside the protocol's
// vocabulary is rejected by the runtime itself, whatever Studio let
// through — never read as the default. Before the fix each of these
// ran: an unknown side_effects mode as "park", an unknown thinking
// level as none, a negative or fractional max_steps as the agent's
// own, an unknown option silently dropped.
func TestLinkRejectsWhatItDoesNotRecognise(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	close(gate)
	calls := make(chan struct{}, 16)
	model := &gatedModel{model: wefttest.Script(), gate: gate, calls: calls}
	newTestLink(t, ts.URL, model, Budget{})

	for i, tc := range []struct{ extra, want string }{
		{`"side_effects":"yolo"`, "unknown side_effects mode"},
		{`"overrides":{"thinking":"max"}`, "unknown thinking level"},
		{`"overrides":{"options":{"max_steps":-1}}`, "not a positive whole number"},
		{`"overrides":{"options":{"max_steps":2.5}}`, "not a positive whole number"},
		{`"overrides":{"options":{"parallelism":0}}`, "not a positive whole number"},
		{`"overrides":{"options":{"temperature":9}}`, "outside 0..2"},
		{`"overrides":{"options":{"top_p":0.5}}`, "unknown option"},
		{`"source":{"run_id":"../../api/meta?x=","from_step":0}`, "not a run id"},
		{`"source":{"run_id":"s_x-t1","from_step":-2}`, "0 or more"},
		{`"source":{"run_id":"s_x-t1","from_step":3}`, "only when from_step is 0"},
	} {
		id := fmt.Sprintf("cmd_bad_%d", i)
		fs.frames <- frame(id, tc.extra)
		if got := fs.ackOf(t, id, "rejected"); !strings.Contains(got.Error, tc.want) {
			t.Errorf("%s: rejection = %q, want it to mention %q", tc.extra, got.Error, tc.want)
		}
	}
	// A frame that is not a command at all is answered too, by its SSE id.
	fs.frames <- "id: cmd_garbage\nevent: run\ndata: {not json\n\n"
	if got := fs.ackOf(t, "cmd_garbage", "rejected"); !strings.Contains(got.Error, "undecodable") {
		t.Errorf("undecodable frame: rejection = %+v", got)
	}
	if n := len(calls); n != 0 {
		t.Errorf("the model was called %d times for rejected commands", n)
	}
}

// slotModel counts the model calls in flight and holds each until the
// gate closes.
type slotModel struct {
	gate     chan struct{}
	mu       sync.Mutex
	inFlight int
	peak     int
}

func (m *slotModel) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: "slot"} }

func (m *slotModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.mu.Lock()
	m.inFlight++
	m.peak = max(m.peak, m.inFlight)
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		defer func() {
			m.mu.Lock()
			m.inFlight--
			m.mu.Unlock()
		}()
		select {
		case <-m.gate:
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		}
		yield(core.ModelTextDelta{Text: "ok"}, nil)
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

// TestLinkRunSlots pins the bound on concurrent runs: a burst of
// commands is acked at once (the at-most-once ack never waits) but
// only maxRunning of them execute together; the rest take a slot as
// one frees. Before the fix every command of a matrix ran at once.
func TestLinkRunSlots(t *testing.T) {
	fs := newFakeStudio(t)
	fs.frames = make(chan string, 64)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	model := &slotModel{gate: make(chan struct{})}
	newTestLink(t, ts.URL, model, Budget{})

	const burst = maxRunning + 8
	for i := range burst {
		fs.frames <- frame(fmt.Sprintf("cmd_slot_%02d", i), "")
	}
	for i := range burst {
		fs.ackOf(t, fmt.Sprintf("cmd_slot_%02d", i), "accepted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		model.mu.Lock()
		n := model.inFlight
		model.mu.Unlock()
		if n >= maxRunning {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // anything past the bound would have started by now
	model.mu.Lock()
	held := model.inFlight
	model.mu.Unlock()
	if held != maxRunning {
		t.Errorf("%d runs in flight under a burst of %d, want %d", held, burst, maxRunning)
	}
	close(model.gate)
	for i := range burst {
		if got := fs.ackOf(t, fmt.Sprintf("cmd_slot_%02d", i), "finished"); got.Status != "succeeded" {
			t.Errorf("command %d finished %+v", i, got)
		}
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.peak > maxRunning {
		t.Errorf("peak concurrent runs = %d, want at most %d", model.peak, maxRunning)
	}
}

// TestLinkContainsACommandPanic pins the backstop around a command's
// goroutine: a panic there is that command's failure — acked — and
// never the program's.
func TestLinkContainsACommandPanic(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)
	cfg := &config{agents: []*core.Agent{testAgent("a")}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")

	func() {
		accepted := false
		defer l.contain("cmd_p1", &accepted)
		panic("before the accepted ack")
	}()
	func() {
		accepted := true
		defer l.contain("cmd_p2", &accepted)
		panic("mid-run")
	}()
	if got := fs.ackOf(t, "cmd_p1", "rejected"); !strings.Contains(got.Error, "internal error") {
		t.Errorf("a panic before the accepted ack acked %+v", got)
	}
	if got := fs.ackOf(t, "cmd_p2", "finished"); got.Status != "failed" || strings.Contains(got.Error, "mid-run") {
		t.Errorf("a panic mid-run acked %+v, want finished failed without the panic's own text", got)
	}
}

// TestScanSSE pins the frame parser on the shapes a stream really
// carries — multi-line data, comments, CRLF, a frame cut by EOF — and
// its bound: a line that never ends is an error, not memory.
func TestScanSSE(t *testing.T) {
	body := ": hello\r\n\r\nid: cmd_1\r\nevent: run\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\nevent: ping\ndata: {}\n\nid: cmd_2\nevent: run\ndata: {}"
	next := scanSSE(bufio.NewReaderSize(strings.NewReader(body), 16))
	ev, err := next()
	if err != nil || ev.id != "cmd_1" || ev.event != "run" || string(ev.data) != "{\"a\":\n 1}" {
		t.Errorf("frame 1 = %+v %q, %v", ev, ev.data, err)
	}
	if ev, err = next(); err != nil || ev.event != "ping" {
		t.Errorf("frame 2 = %+v, %v", ev, err)
	}
	if ev, err = next(); err != nil || ev.id != "cmd_2" || string(ev.data) != "{}" {
		t.Errorf("frame 3 (cut by EOF) = %+v, %v", ev, err)
	}
	if _, err = next(); err != io.EOF {
		t.Errorf("after the last frame err = %v, want EOF", err)
	}

	endless := io.MultiReader(strings.NewReader("event: run\ndata: "), &zeroes{})
	if _, err := scanSSE(bufio.NewReader(endless))(); !errors.Is(err, errFrameTooLarge) {
		t.Errorf("a line that never ends: err = %v, want errFrameTooLarge", err)
	}
	many := io.MultiReader(strings.NewReader("event: run\n"), &lines{})
	if _, err := scanSSE(bufio.NewReader(many))(); !errors.Is(err, errFrameTooLarge) {
		t.Errorf("a frame of endless data lines: err = %v, want errFrameTooLarge", err)
	}
}

// zeroes is an endless line; lines is endless data lines.
type zeroes struct{}

func (*zeroes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = '0'
	}
	return len(p), nil
}

type lines struct{}

func (*lines) Read(p []byte) (int, error) {
	const line = "data: 0123456789abcdef\n"
	n := 0
	for n+len(line) <= len(p) {
		n += copy(p[n:], line)
	}
	if n == 0 {
		n = copy(p, line)
	}
	return n, nil
}

// TestBackoff pins the reconnect schedule: half a second doubling to a
// 30 s cap, each wait jittered over its upper half, and starting over
// after a reset — before the fix the count never reset, so one stream
// break after a day of failures-and-recoveries waited 30 s.
func TestBackoff(t *testing.T) {
	b := newBackoff()
	for i, ceil := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second,
		4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		if d := b.next(); d < ceil/2 || d > ceil {
			t.Errorf("wait %d = %v, want within [%v, %v]", i, d, ceil/2, ceil)
		}
	}
	b.reset()
	if d := b.next(); d > 500*time.Millisecond {
		t.Errorf("first wait after a reset = %v, want at most 500ms", d)
	}
	seen := map[time.Duration]bool{}
	for range 32 {
		b.reset()
		seen[b.next()] = true
	}
	if len(seen) < 2 {
		t.Error("32 first waits were identical: no jitter")
	}
}

// TestLinkReportsBreakpointsAtRegistration pins the debugger's set
// across a reconnect (WEFT-DEVTOOLS §8.3): the set lives in this
// process, so every registration reports it — a Studio that restarted
// learns the rule that is still parking this runtime's runs — and the
// frame Studio re-sends on a reconnect replaces it with itself.
func TestLinkReportsBreakpointsAtRegistration(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	t.Cleanup(ts.Close)

	gate := make(chan struct{})
	close(gate)
	model := &gatedModel{model: wefttest.Script(), gate: gate, calls: make(chan struct{}, 4)}
	l := newTestLink(t, ts.URL, model, Budget{})

	bp := "id: cmd_bp\nevent: breakpoints\ndata: {\"tools\":[\"refund\",\"escalate\"]}\n\n"
	fs.frames <- bp
	fs.frames <- bp // the re-sent copy: idempotent
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(l.breakpointSet()) != 2 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := l.breakpointSet(); len(got) != 2 || got[0] != "escalate" || got[1] != "refund" {
		t.Fatalf("breakpoints = %v, want [escalate refund]", got)
	}
	close(fs.drop) // the break: the link re-registers
	for time.Now().Before(deadline) {
		fs.mu.Lock()
		n := len(fs.registrations)
		fs.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.registrations) < 2 {
		t.Fatal("the link never re-registered")
	}
	if first := fs.registrations[0].Breakpoints; first == nil || len(first) != 0 {
		t.Errorf("first registration's breakpoints = %#v, want [] (empty, not null)", first)
	}
	if got := fs.registrations[1].Breakpoints; len(got) != 2 || got[0] != "escalate" || got[1] != "refund" {
		t.Errorf("re-registration's breakpoints = %v, want the set the runtime still holds", got)
	}
}
