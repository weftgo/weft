package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
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
		fmt.Fprint(w, "event: ping\ndata: {}\n\n")
		flusher.Flush()
		ctx := r.Context()
		for {
			select {
			case fr := <-f.frames:
				fmt.Fprint(w, fr)
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

// gatedModel is a weft.Model whose calls block until the test opens
// the gate — proving the accepted ack is sent before execution — and
// that counts its calls, proving at-most-once.
type gatedModel struct {
	model weft.Model
	gate  chan struct{}
	calls chan struct{}
}

func (m *gatedModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	// The gate is the model's first instruction: until the test opens
	// it, no run can pass through this model at all.
	select {
	case <-m.gate:
	case <-ctx.Done():
		return func(yield func(weft.ModelEvent, error) bool) {
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
func newTestLink(t *testing.T, tsURL string, model weft.Model, budget Budget) *link {
	t.Helper()
	agent := weft.New(model, weft.Name("acme-support"), weft.Instructions("You are Acme's support agent."))
	cfg := &config{
		agents: []*weft.Agent{agent},
		models: map[string]weft.Model{},
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
	if reg.WeftVersion != "v0.6.0" || reg.Pid == 0 || reg.Host == "" {
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
