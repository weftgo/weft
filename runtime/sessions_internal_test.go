package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/studio"
	linkruntime "github.com/weftgo/weft/studio/runtime"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// sessionFrame renders one `event: session` frame as Studio writes it.
func sessionFrame(sc sessionCommand) string {
	b, _ := json.Marshal(sc)
	return fmt.Sprintf("id: %s\nevent: session\ndata: %s\n\n", sc.CommandID, b)
}

// newSessionsLink is newTestLink with a SessionResolver (nil: none).
func newSessionsLink(t *testing.T, tsURL string, resolve SessionResolver) *link {
	t.Helper()
	agent := core.New(wefttest.Script(), core.Name("acme-support"))
	cfg := &config{agents: []*core.Agent{agent}, models: map[string]core.Model{}, sessions: resolve}
	l := newLink(cfg, newRegistry(cfg), tsURL, "")
	l.reconnect = func() time.Duration { return time.Millisecond }
	if err := l.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(l.stop)
	return l
}

// TestSessionCommandRefusals pins every refusal a session command can
// carry before its ack — each one rejected, never accepted, so nothing
// was written — and the resolver's contract around them: nil is "no
// session for this public id", thread.ErrLocked the locked sentence,
// ErrStale one more resolve, a session other than the run's refused,
// a slow resolver bounded (its late answer released), a panicking one
// contained. The registration says sessions: true only with the option.
func TestSessionCommandRefusals(t *testing.T) {
	ctx := context.Background()
	other, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	var (
		calls       atomic.Int32
		lateRelease = make(chan struct{})
		lateOnce    sync.Once
	)
	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		calls.Add(1)
		switch publicID {
		case "pub_locked":
			return nil, nil, fmt.Errorf("open: %w", thread.ErrLocked)
		case "pub_stale":
			return nil, nil, fmt.Errorf("open: %w", thread.ErrStale)
		case "pub_other":
			return other, nil, nil
		case "pub_slow":
			<-ctx.Done()
			time.Sleep(10 * time.Millisecond) // answers after the bound
			return other, func() { lateOnce.Do(func() { close(lateRelease) }) }, nil
		case "pub_panic":
			panic("boom")
		case "pub_err":
			return nil, nil, errors.New("the app's store is down")
		}
		return nil, nil, nil
	}
	defer func(d time.Duration) { sessionResolveTimeout = d }(sessionResolveTimeout)
	sessionResolveTimeout = 50 * time.Millisecond

	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, resolve)

	base := sessionCommand{Verb: verbDecide, SessionID: "s_1", RunID: "s_1-t1", CallID: "c_1", Decision: "approve"}
	cases := []struct {
		name string
		edit func(sc *sessionCommand)
		want string
	}{
		{"unknown verb", func(sc *sessionCommand) { sc.Verb = "kill" }, `unknown session verb "kill"`},
		{"no addressing", func(sc *sessionCommand) { sc.SessionID = "" }, "a session command needs public_id, session_id and run_id"},
		{"unknown decision", func(sc *sessionCommand) { sc.Decision = "maybe" }, `unknown decision "maybe"`},
		{"steer without a message", func(sc *sessionCommand) { sc.Verb = verbSteer }, "a steer needs a message"},
		{"cancel", func(sc *sessionCommand) { sc.Verb = verbCancel; sc.PublicID = "pub_none" }, refusedCancel},
		{"not this app's", func(sc *sessionCommand) { sc.PublicID = "pub_none" }, refusedNoSession},
		{"locked", func(sc *sessionCommand) { sc.PublicID = "pub_locked" }, refusedLocked},
		{"stale twice", func(sc *sessionCommand) { sc.PublicID = "pub_stale" }, "open: " + thread.ErrStale.Error()},
		{"another session", func(sc *sessionCommand) { sc.PublicID = "pub_other" },
			"the session resolver answered session " + other.ID() + " for public id pub_other, not the run's session s_1"},
		{"slow", func(sc *sessionCommand) { sc.PublicID = "pub_slow" }, "session resolver timed out"},
		{"panic", func(sc *sessionCommand) { sc.PublicID = "pub_panic" }, "session resolver panicked: boom"},
		{"error", func(sc *sessionCommand) { sc.PublicID = "pub_err" }, "the app's store is down"},
	}
	for i, c := range cases {
		sc := base
		sc.PublicID = "pub_x"
		sc.CommandID = fmt.Sprintf("cmd_s%d", i)
		c.edit(&sc)
		before := calls.Load()
		f.frames <- sessionFrame(sc)
		f.waitAck(t, i+1)
		f.mu.Lock()
		a := f.acks[i]
		f.mu.Unlock()
		if a.CommandID != sc.CommandID || a.State != "rejected" || a.Error != c.want {
			t.Errorf("%s: ack = %+v, want rejected %q", c.name, a, c.want)
		}
		if c.name == "stale twice" && calls.Load()-before != 2 {
			t.Errorf("stale: the resolver was asked %d times, want 2 (once more after ErrStale)", calls.Load()-before)
		}
		if c.name == "cancel" && calls.Load() != before {
			t.Error("cancel asked the resolver: it is refused before anything is resolved")
		}
	}
	select {
	case <-lateRelease:
	case <-time.After(5 * time.Second):
		t.Error("the slow resolver's late answer was never released")
	}
	f.mu.Lock()
	if len(f.registrations) == 0 || !f.registrations[0].Sessions {
		t.Errorf("registration with runtime.Sessions = %+v, want sessions: true", f.registrations)
	}
	f.mu.Unlock()
}

// TestSessionCommandWithoutOption: a runtime installed without
// runtime.Sessions refuses a session frame (Studio never routes one to
// it; an old or confused Studio gets the reason) and registers
// sessions: false.
func TestSessionCommandWithoutOption(t *testing.T) {
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, nil)
	f.frames <- sessionFrame(sessionCommand{CommandID: "cmd_no", Verb: verbDecide, PublicID: "p", SessionID: "s",
		RunID: "s-t1", CallID: "c", Decision: "deny"})
	f.waitAck(t, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.acks[0]; a.State != "rejected" || a.Error != refusedNoOption {
		t.Errorf("ack = %+v, want rejected %q", a, refusedNoOption)
	}
	if f.registrations[0].Sessions {
		t.Error("registration without runtime.Sessions says sessions: true")
	}
}

// TestSessionWireShape pins the session frame and the registration's
// sessions flag across both sides (the shapes are duplicated by
// design, wire.go): Studio's SessionCommand, as studio/runtime writes
// it, decodes into the client's sessionCommand with no unknown field
// and re-encodes byte for byte; the frame is the committed golden
// (testdata/session-frame.golden.json, hand-kept like studio's API goldens); and a real
// RuntimeServer delivers an enqueued one to a real link, whose
// rejection lands on the command's row.
func TestSessionWireShape(t *testing.T) {
	full := linkruntime.SessionCommand{
		CommandID: "cmd_01K7Q2W3E4R5T6Y7U8I9O0PABC", Verb: linkruntime.VerbDecide,
		PublicID: "pub_7Hk2", SessionID: "s_01K7", RunID: "s_01K7-t1", CallID: "call_1",
		Decision: "resolve", Reason: "r", Content: "c", Message: "m", Actor: "devtools",
	}
	server, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var client sessionCommand
	dec := json.NewDecoder(bytes.NewReader(server))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&client); err != nil {
		t.Fatalf("the client does not read Studio's frame: %v", err)
	}
	again, _ := json.Marshal(client)
	if !bytes.Equal(again, server) {
		t.Errorf("the frame does not round-trip:\nstudio %s\nclient %s", server, again)
	}
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, server, "", "  ")
	pretty.WriteByte('\n')
	want, err := os.ReadFile(filepath.Join("testdata", "session-frame.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, pretty.Bytes()) {
		t.Errorf("the session frame changed shape:\ngolden %s\nnow    %s", want, pretty.Bytes())
	}

	// The registration's flag, both ways.
	reg, _ := json.Marshal(registration{Sessions: true})
	var sreg linkruntime.Registration
	if err := json.Unmarshal(reg, &sreg); err != nil || !sreg.Sessions {
		t.Errorf("Studio does not read sessions: true from %s (%v)", reg, err)
	}

	// Both sides, end to end: a real RuntimeServer, a real link.
	rs := linkruntime.New()
	mux := http.NewServeMux()
	rs.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return nil, nil, nil })
	// The runtime's id as Studio holds it (the link's own field is its
	// goroutine's).
	rtID := ""
	deadline := time.Now().Add(5 * time.Second)
	for rtID == "" && time.Now().Before(deadline) {
		for _, v := range rs.Snapshot() {
			if v.Connected {
				rtID = v.ID
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got, ok := rs.Registration(rtID); !ok || !got.Sessions {
		t.Fatalf("Studio's copy of the registration: %+v (ok %v), want sessions: true", got, ok)
	}
	if id, ok := rs.SessionsRuntime(serviceName()); !ok || id != rtID {
		t.Fatalf("SessionsRuntime(%q) = %q %v, want %q", serviceName(), id, ok, rtID)
	}
	sent := full
	sent.CommandID = ""
	sent.Verb = linkruntime.VerbSteer
	sent, err = rs.EnqueueSession(rtID, sent)
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		if st, _ := rs.Command(sent.CommandID); st.State == linkruntime.StateRejected {
			if st.Error == nil || *st.Error != refusedNoSession {
				t.Errorf("row = %+v, want rejected %q", st, refusedNoSession)
			}
			if _, started := rs.RuntimeOf(sent.RunID); started {
				t.Error("a session command's ack made the app's run a runtime-started one")
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	st, _ := rs.Command(sent.CommandID)
	t.Fatalf("the session command never came back rejected: %+v", st)
}

// identityEventJSON is an event as the identity goldens compare it:
// its wire JSON with the wall-clock fields (step_finish's latency_ms
// and ttft_ms) dropped — they measure the machine, not the loop, and
// differed by a millisecond once under `make stress`.
func identityEventJSON(ev core.Event) ([]byte, error) {
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	delete(m, "latency_ms")
	delete(m, "ttft_ms")
	return json.Marshal(m)
}

// identityRun is everything one app turn produces that observability
// must leave alone: its events (as the Tap saw them), the session's
// entries and context, the run's usage and its metadata.
type identityRun struct {
	Events   []string
	Entries  []string
	Context  string
	Usage    core.Usage
	Metadata map[string]string
	// Spans is every ended span's name and weft.* attributes, in end
	// order.
	Spans []string
	// ToolMetadata is the metadata the lookup handler's context
	// carried — what a run passes on past its start (the handler, a
	// child run), not only what RunStart saw.
	ToolMetadata map[string]string
}

// runAppTurn runs one turn of a fresh thread session — deterministic
// ids and clock — a lookup and an answer — and records it.
// opts are added to the agent's (runtime.Breakpoints' identity golden).
func runAppTurn(t *testing.T, install func(agent *core.Agent) func(), opts ...core.Option) identityRun {
	t.Helper()
	ctx := context.Background()
	var (
		mu  sync.Mutex
		out identityRun
	)
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })
	lookup := core.Tool("lookup_order", "Look up an order by ID.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		mu.Lock()
		out.ToolMetadata = core.MetadataFromContext(ctx)
		mu.Unlock()
		return `{"status":"shipped"}`, nil
	})
	agent := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"4411"}`}),
			wefttest.Say("It shipped."),
		),
		core.Name("acme-support"),
		core.TracerProvider(tp),
		core.Options(opts...),
		core.Tap(func(ctx context.Context, ev core.Event) {
			b, err := identityEventJSON(ev)
			if err != nil {
				t.Errorf("event: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			out.Events = append(out.Events, string(b))
			if _, ok := ev.(core.RunStart); ok {
				out.Metadata = core.MetadataFromContext(ctx)
			}
		}),
		lookup,
	)
	stop := install(agent)
	defer stop()
	n := 0
	ids := func() string { n++; return fmt.Sprintf("e%03d", n) }
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agent, thread.IDs(ids), thread.Clock(func() time.Time { return at }),
		thread.PublicID("pub_identity"))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("Where is order 4411?"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	_, entries, _, err := st.Load(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, _ := json.Marshal(e)
		out.Entries = append(out.Entries, string(b))
	}
	b, _ := json.Marshal(s.Context())
	out.Context = string(b)
	out.Usage = res.Usage
	for _, sp := range rec.Ended() {
		var kv []string
		for _, a := range sp.Attributes() {
			if k := string(a.Key); strings.HasPrefix(k, "weft.") && k != "weft.ttft_ms" && k != "weft.latency_ms" { // the wall-clock ones measure the machine
				kv = append(kv, string(a.Key)+"="+fmt.Sprint(a.Value.AsInterface()))
			}
		}
		sort.Strings(kv)
		out.Spans = append(out.Spans, sp.Name()+" "+strings.Join(kv, " "))
	}
	mu.Lock()
	defer mu.Unlock()
	return out
}

// TestSessionsOptionAbsentChangesNothing is the behaviour-identity
// golden (§1.1: observability never changes behaviour): the same app
// turn run under runtime.Install(runtime.Local(srv)) — no Sessions —
// and with no Install at all is byte-identical in events, entries,
// transcript, usage and run metadata; and the registration a runtime
// sends differs with the option only in sessions (false without it).
func TestSessionsOptionAbsentChangesNothing(t *testing.T) {
	plain := runAppTurn(t, func(*core.Agent) func() { return func() {} })
	linked := runAppTurn(t, func(agent *core.Agent) func() {
		srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "studio.db")), studio.Playground(true))
		shutdown := Install(Local(srv), Agents(agent), Enabled(true))
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			connected := false
			for _, v := range srv.Runtime().Snapshot() {
				connected = connected || v.Connected
			}
			if connected {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		return func() { shutdown(); _ = srv.Close() }
	})
	if len(plain.Events) == 0 || len(plain.Entries) == 0 {
		t.Fatalf("nothing recorded: %+v", plain)
	}
	if !reflect.DeepEqual(plain, linked) {
		t.Errorf("an installed runtime changed the app's turn:\nplain  %+v\nlinked %+v", plain, linked)
	}

	// The registration, with and without the option.
	register := func(opts ...Option) map[string]any {
		t.Helper()
		f := newFakeStudio(t)
		ts := httptest.NewServer(f.handler())
		t.Cleanup(ts.Close)
		agent := core.New(wefttest.Script(), core.Name("acme-support"))
		shutdown := Install(append([]Option{Studio(ts.URL, ""), Agents(agent), Enabled(true)}, opts...)...)
		t.Cleanup(shutdown)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			n := len(f.registrations)
			f.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.registrations) == 0 {
			t.Fatal("no registration")
		}
		b, _ := json.Marshal(f.registrations[0])
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "instance")   // per link, random
		delete(m, "studio_url") // each call dials its own fake Studio
		return m
	}
	without := register()
	with := register(Sessions(func(context.Context, string) (*thread.Session, func(), error) { return nil, nil, nil }))
	if without["sessions"] != false || with["sessions"] != true {
		t.Fatalf("sessions: without %v, with %v; want false, true", without["sessions"], with["sessions"])
	}
	delete(without, "sessions")
	delete(with, "sessions")
	if !reflect.DeepEqual(without, with) {
		t.Errorf("the option changed more of the registration than sessions:\nwithout %v\nwith    %v", without, with)
	}
}

// holdFirstModel holds its first call until gate closes (held closes
// once it waits); every call answers "answer".
type holdFirstModel struct {
	mu    sync.Mutex
	calls int
	held  chan struct{}
	gate  chan struct{}
	once  sync.Once
}

func (m *holdFirstModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "hold"}
}

func (m *holdFirstModel) Stream(ctx context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		if n == 1 {
			m.once.Do(func() { close(m.held) })
			select {
			case <-m.gate:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		if !yield(core.ModelTextDelta{Text: "answer"}, nil) {
			return
		}
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

// steerTrace counts a session's turn entries and the prompts carrying
// text — what a steer that started a turn would leave.
func steerTrace(s *thread.Session, text string) (turns, prompts int) {
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case thread.TurnEntry:
			turns++
		case thread.MessageEntry:
			for _, p := range e.Message.Content {
				if tp, ok := p.(core.TextPart); ok && tp.Text == text {
					prompts++
				}
			}
		}
	}
	return turns, prompts
}

// TestSessionSteerRaceStartsNoTurn is the review's first probe as a
// regression: the app's turn is running when the steer command arrives
// and lands while the runtime waits on its accepted ack — the window
// between any "is it running?" check and the delivery. The steer must
// not become a turn of its own: the command finishes failed "no running
// turn to steer", the session holds one turn and no prompt with the
// steer's text. (Under the entries check that preceded Session.Steer,
// the delivery was Send(As(Steer)) on an idle session: a second turn
// whose prompt was the debugger's text.)
func TestSessionSteerRaceStartsNoTurn(t *testing.T) {
	ctx := context.Background()
	m := &holdFirstModel{held: make(chan struct{}), gate: make(chan struct{})}
	s, err := thread.Create(ctx, thread.Memory(), core.New(m), thread.PublicID("pub_race"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	turn, err := s.Send(ctx, core.User("first"))
	if err != nil {
		t.Fatal(err)
	}
	<-m.held

	f := newFakeStudio(t)
	f.ackStatus = func(a ack) int {
		if a.CommandID == "cmd_race" && a.State == "accepted" {
			// The turn lands while the accepted ack is in flight.
			close(m.gate)
			if _, err := turn.Wait(); err != nil {
				t.Errorf("turn: %v", err)
			}
		}
		return http.StatusOK
	}
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return s, nil, nil })

	f.frames <- sessionFrame(sessionCommand{CommandID: "cmd_race", Verb: verbSteer, PublicID: "pub_race",
		SessionID: s.ID(), RunID: turn.RunID(), Message: "too late"})
	f.waitAck(t, 2)
	f.mu.Lock()
	fin := f.acks[1]
	f.mu.Unlock()
	if fin.State != "finished" || fin.Status != "failed" || !strings.HasPrefix(fin.Error, refusedNoTurn) {
		t.Errorf("finished ack = %+v, want failed %q", fin, refusedNoTurn)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if turns, prompts := steerTrace(s, "too late"); turns != 1 || prompts != 0 {
		t.Errorf("turns = %d, prompts with the steer = %d; want 1, 0 (the steer started a turn)", turns, prompts)
	}
}

// TestSessionSteerMidTurnSnapshotStartsNoTurn is the review's second
// probe: the resolver opens the session from what a crash mid-turn
// leaves — a prompt whose turn end never landed — so the entries read
// "in flight" forever. Nothing runs in that Session: the steer finishes
// failed "no running turn to steer" and the reopened session gains no
// turn and no prompt.
func TestSessionSteerMidTurnSnapshotStartsNoTurn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &holdFirstModel{held: make(chan struct{}), gate: make(chan struct{})}
	orig, err := thread.Create(ctx, st, core.New(m), thread.PublicID("pub_snap"))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := orig.Send(ctx, core.User("first"))
	if err != nil {
		t.Fatal(err)
	}
	<-m.held
	t.Cleanup(func() { close(m.gate); _ = orig.Close(ctx) })
	// What a restarted process finds: a fresh Storage on the directory.
	st2, err := jsonl.Open(dir, thread.NoLock())
	if err != nil {
		t.Fatal(err)
	}
	re, err := thread.Open(ctx, st2, orig.ID(), core.New(wefttest.Script(wefttest.Say("a turn the debugger started"))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = re.Close(ctx) }()
	before := len(re.Entries())

	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return re, nil, nil })
	f.frames <- sessionFrame(sessionCommand{CommandID: "cmd_snap", Verb: verbSteer, PublicID: "pub_snap",
		SessionID: re.ID(), RunID: turn.RunID(), Message: "anyone?"})
	f.waitAck(t, 2)
	f.mu.Lock()
	fin := f.acks[1]
	f.mu.Unlock()
	if fin.State != "finished" || fin.Status != "failed" || !strings.HasPrefix(fin.Error, refusedNoTurn) {
		t.Errorf("finished ack = %+v, want failed %q", fin, refusedNoTurn)
	}
	time.Sleep(20 * time.Millisecond)
	if turns, prompts := steerTrace(re, "anyone?"); turns != 0 || prompts != 0 || len(re.Entries()) != before {
		t.Errorf("the reopened session gained turns %d, steer prompts %d, entries %d → %d; want nothing written",
			turns, prompts, before, len(re.Entries()))
	}
}

// parkedSession creates a session on st whose first turn parks a gated
// deploy call; it returns the Session, the parked turn and the call id.
func parkedSession(t *testing.T, st thread.Storage, opts ...thread.SessionOption) (*thread.Session, *thread.Turn, string) {
	t.Helper()
	ctx := context.Background()
	agent := core.New(
		wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}), wefttest.Say("done")),
		core.Tool("deploy", "Deploy.", func(context.Context, struct {
			Env string `json:"env"`
		}) (string, error) {
			return "deployed", nil
		}, core.RequireApproval()),
	)
	s, err := thread.Create(ctx, st, agent, append([]thread.SessionOption{thread.PublicID("pub_park")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("deploy"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("no park: %+v", res.Pending)
	}
	return s, turn, res.Pending[0].ID
}

// decideFrame is a decide session frame for s's parked call.
func decideFrame(id string, s *thread.Session, turn *thread.Turn, callID, decision string) string {
	return sessionFrame(sessionCommand{CommandID: id, Verb: verbDecide, PublicID: "pub_park", SessionID: s.ID(),
		RunID: turn.RunID(), CallID: callID, Decision: decision})
}

// storedEntries is the stored session's entry count.
func storedEntries(t *testing.T, st thread.Storage, id string) int {
	t.Helper()
	_, entries, _, err := st.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// TestSessionDecideLeaseLostAfterClaim: the runtime claimed the lease
// before its ack, and lost it before Decide wrote — the storage's hold
// released and another writer claimed it in between. Decide fails
// ErrLocked: the command finishes failed with exactly the locked
// sentence, and nothing was written (T1).
func TestSessionDecideLeaseLostAfterClaim(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	app, turn, callID := parkedSession(t, st)
	agent := core.New(wefttest.Script(wefttest.Say("done")))
	if err := app.Close(ctx); err != nil { // the app's writer is gone: the lease is free
		t.Fatal(err)
	}
	re, err := thread.Open(ctx, st, app.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = re.Close(ctx) }()
	var other *thread.Session
	f := newFakeStudio(t)
	f.ackStatus = func(a ack) int {
		if a.CommandID == "cmd_lost" && a.State == "accepted" {
			// Claimed by re; now the hold ends and another writer takes it.
			if err := st.(thread.Releaser).Release(ctx, app.ID()); err != nil {
				t.Errorf("release: %v", err)
			}
			o, err := thread.Open(ctx, st, app.ID(), agent)
			if err != nil {
				t.Errorf("open: %v", err)
				return http.StatusOK
			}
			if err := o.Claim(ctx); err != nil {
				t.Errorf("the other writer's claim: %v", err)
			}
			other = o
		}
		return http.StatusOK
	}
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return re, nil, nil })
	before := storedEntries(t, st, app.ID())
	f.frames <- decideFrame("cmd_lost", re, turn, callID, "approve")
	f.waitAck(t, 2)
	f.mu.Lock()
	acc, fin := f.acks[0], f.acks[1]
	f.mu.Unlock()
	if acc.State != "accepted" || fin.State != "finished" || fin.Status != "failed" || fin.Error != refusedLocked {
		t.Errorf("acks = %+v, %+v; want accepted, then finished failed %q", acc, fin, refusedLocked)
	}
	if after := storedEntries(t, st, app.ID()); after != before {
		t.Errorf("a decision refused ErrLocked wrote: %d entries, had %d", after, before)
	}
	if other != nil {
		_ = other.Close(ctx)
	}
}

// TestSessionDecideRequiresSigned: a session under thread.RequireSigned
// refuses an unsigned Decide at its door, so the runtime refuses the
// decision before its ack — rejected, the reason naming signed
// decisions — and writes nothing.
func TestSessionDecideRequiresSigned(t *testing.T) {
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: []byte("a-test-keyring-secret-32-bytes!!"), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	st := thread.Memory()
	s, turn, callID := parkedSession(t, st, thread.WithKeyring(ring), thread.RequireSigned())
	defer func() { _ = s.Close(context.Background()) }()
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return s, nil, nil })
	before := storedEntries(t, st, s.ID())
	f.frames <- decideFrame("cmd_signed", s, turn, callID, "approve")
	f.waitAck(t, 1)
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	acks := append([]ack(nil), f.acks...)
	f.mu.Unlock()
	if len(acks) != 1 || acks[0].State != "rejected" || acks[0].Error != refusedSigned {
		t.Errorf("acks = %+v, want one rejected %q", acks, refusedSigned)
	}
	if after := storedEntries(t, st, s.ID()); after != before {
		t.Errorf("a refused decision wrote: %d entries, had %d", after, before)
	}
	if len(s.Pending()) != 1 {
		t.Error("the call is no longer pending")
	}
}

// TestSessionDenyDefaultReason pins the one deny default: a deny sent
// with no reason reaches the model as "DENIED: skipped from the devtools
// panel" — the runtime-started path's words (executor.go), not a second
// model-visible text.
func TestSessionDenyDefaultReason(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	s, turn, callID := parkedSession(t, st)
	defer func() { _ = s.Close(ctx) }()
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return s, nil, nil })
	f.frames <- decideFrame("cmd_deny", s, turn, callID, "deny")
	f.waitAck(t, 2)
	next := turn.Next()
	if next == nil {
		t.Fatal("the deny did not resume the turn")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	if skippedReason != "skipped from the devtools panel" {
		t.Errorf("skippedReason = %q", skippedReason)
	}
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok && tr.CallID == callID {
				if !strings.Contains(tr.Content, "skipped from the devtools panel") {
					t.Errorf("the denied call reads %q", tr.Content)
				}
				return
			}
		}
	}
	t.Error("no result for the denied call")
}

// decisionRun is what a resumed turn leaves that observers read: the
// events of the session's runs (as the Tap saw them), the stored entries
// with the audit's Who/Via removed (who decided is the one honest
// difference), every run's metadata and the weft.* attributes of every
// span, by span name, in end order.
type decisionRun struct {
	Events   []string
	Entries  []string
	Metadata []map[string]string
	Spans    []string
}

// parkAndDecide parks a gated deploy in a fresh, deterministic session
// (ids, clock) traced into its own recorder, lets decide resolve it, and
// records the resumed turn's run.
func parkAndDecide(t *testing.T, decide func(s *thread.Session, parked *thread.Turn, callID string) *thread.Turn) decisionRun {
	t.Helper()
	return parkAndDecideBy(t, false, decide)
}

// parkAndDecideBy is parkAndDecide with the park's cause chosen: the
// gate (RequireApproval) or, with breakpoint set, an ungated deploy on
// an agent built with Breakpoints() while a live link holds the
// breakpoint set [deploy] (plan F4.2).
func parkAndDecideBy(t *testing.T, breakpoint bool, decide func(s *thread.Session, parked *thread.Turn, callID string) *thread.Turn) decisionRun {
	t.Helper()
	gate := []core.ToolOption{core.RequireApproval()}
	var extra []core.Option
	if breakpoint {
		gate, extra = nil, []core.Option{Breakpoints()}
		// A thread turn parks on a breakpoint only when a live link
		// has Sessions (this one has, never called here) and the
		// session a public id (pub_park, below).
		cfg := &config{sessions: func(context.Context, string) (*thread.Session, func(), error) { return nil, nil, nil }}
		l := newLink(cfg, newRegistry(cfg), "http://unused.invalid", "")
		l.setBreakpoints([]string{"deploy"})
		addLiveLink(l)
		t.Cleanup(func() { removeLiveLink(l) })
	}
	ctx := context.Background()
	var (
		mu  sync.Mutex
		out decisionRun
	)
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })
	agent := core.New(
		wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}), wefttest.Say("Deployed.")),
		core.Name("approvals-example"),
		core.TracerProvider(tp),
		core.Tap(func(ctx context.Context, ev core.Event) {
			b, _ := identityEventJSON(ev)
			mu.Lock()
			defer mu.Unlock()
			out.Events = append(out.Events, string(b))
			if _, ok := ev.(core.RunStart); ok {
				out.Metadata = append(out.Metadata, core.MetadataFromContext(ctx))
			}
		}),
		core.Tool("deploy", "Deploy the service.", func(ctx context.Context, in struct {
			Env string `json:"env"`
		}) (string, error) {
			call, _ := core.CallFromContext(ctx)
			return fmt.Sprintf("deployed to %s (approved: %v)", in.Env, call.Approved), nil
		}, gate...),
		core.Options(extra...),
	)
	n := 0
	ids := func() string { n++; return fmt.Sprintf("e%03d", n) }
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agent, thread.IDs(ids), thread.Clock(func() time.Time { return at }),
		thread.PublicID("pub_park"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	parked, err := s.Send(ctx, core.User("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	res, err := parked.Wait()
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("park: %v %+v", err, res)
	}
	resumed := decide(s, parked, res.Pending[0].ID)
	if resumed == nil {
		t.Fatal("no resumed turn")
	}
	if _, err := resumed.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	_, entries, _, err := st.Load(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, _ := json.Marshal(e)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "who")
		delete(m, "via")
		b, _ = json.Marshal(m)
		out.Entries = append(out.Entries, string(b))
	}
	for _, sp := range rec.Ended() {
		var kv []string
		for _, a := range sp.Attributes() {
			if k := string(a.Key); strings.HasPrefix(k, "weft.") && k != "weft.ttft_ms" && k != "weft.latency_ms" { // the wall-clock ones measure the machine
				kv = append(kv, string(a.Key)+"="+fmt.Sprint(a.Value.AsInterface()))
			}
		}
		sort.Strings(kv)
		out.Spans = append(out.Spans, sp.Name()+" "+strings.Join(kv, " "))
	}
	mu.Lock()
	defer mu.Unlock()
	return out
}

// TestSessionsDecisionIsTheAppsDecision is the acting path's identity
// golden: a parked turn approved through runtime.Sessions resumes
// exactly as one the app approved itself with s.Decide — the same
// events, the same entries (who decided aside), the same run metadata
// and the same weft.* span attributes. Whatever the runtime adds on the
// way (a context value, a metadata key) shows up here.
func TestSessionsDecisionIsTheAppsDecision(t *testing.T) {
	ctx := context.Background()
	own := parkAndDecide(t, func(s *thread.Session, _ *thread.Turn, callID string) *thread.Turn {
		// The same declared decider as the runtime's command (its
		// Actor): the resume's weft.approval.actor (UX-E-17) is the
		// decider's, so the two paths stamp the same metadata. "devtools"
		// here only aligns the equality check below; an app's own Decide
		// names its own decider (an opaque id: the actor survives
		// content-off).
		d := thread.Approve(callID)
		d.Who = "devtools"
		turn, err := s.Decide(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		return turn
	})
	viaRuntime := parkAndDecide(t, func(s *thread.Session, parked *thread.Turn, callID string) *thread.Turn {
		f := newFakeStudio(t)
		ts := httptest.NewServer(f.handler())
		t.Cleanup(ts.Close)
		newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return s, nil, nil })
		f.frames <- sessionFrame(sessionCommand{CommandID: "cmd_same", Verb: verbDecide, PublicID: "pub_park",
			SessionID: s.ID(), RunID: parked.RunID(), CallID: callID, Decision: "approve", Actor: "devtools"})
		f.waitAck(t, 2)
		f.mu.Lock()
		fin := f.acks[1]
		f.mu.Unlock()
		if fin.Status != "succeeded" {
			t.Fatalf("decision ack = %+v", fin)
		}
		return parked.Next()
	})
	if len(own.Spans) == 0 || len(own.Metadata) != 2 {
		t.Fatalf("nothing recorded: %+v", own)
	}
	for name, pair := range map[string][2]any{
		"events": {own.Events, viaRuntime.Events}, "entries": {own.Entries, viaRuntime.Entries},
		"metadata": {own.Metadata, viaRuntime.Metadata}, "spans": {own.Spans, viaRuntime.Spans},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("the runtime's decision differs from the app's in its %s:\napp     %v\nruntime %v", name, pair[0], pair[1])
		}
	}
}

// holdSecondModel answers its first call at once and holds its second
// until gate closes (held closes once it waits); it records the user
// texts of every call.
type holdSecondModel struct {
	mu    sync.Mutex
	calls int
	seen  []string
	held  chan struct{}
	gate  chan struct{}
}

func (m *holdSecondModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "hold2"}
}

func (m *holdSecondModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.mu.Lock()
	m.calls++
	n := m.calls
	for _, msg := range req.Messages {
		if msg.Role != core.RoleUser {
			continue
		}
		for _, p := range msg.Content {
			if tp, ok := p.(core.TextPart); ok {
				m.seen = append(m.seen, tp.Text)
			}
		}
	}
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		if n == 2 {
			close(m.held)
			select {
			case <-m.gate:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		if !yield(core.ModelTextDelta{Text: "answer"}, nil) {
			return
		}
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

// TestSessionSteerNamedRunNotTheRunningOne is the re-review's probe as
// a regression: the user steers t1 (finished) while t2 runs. The steer
// names t1, so it is not delivered into t2: the command finishes failed
// naming both runs, and no model call sees the text. (With Steer
// accepting any running turn, it landed in t2's next model call and the
// command finished succeeded.)
func TestSessionSteerNamedRunNotTheRunningOne(t *testing.T) {
	ctx := context.Background()
	m := &holdSecondModel{held: make(chan struct{}), gate: make(chan struct{})}
	s, err := thread.Create(ctx, thread.Memory(), core.New(m), thread.PublicID("pub_named"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	t1, _ := s.Send(ctx, core.User("first"))
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	t2, _ := s.Send(ctx, core.User("second"))
	<-m.held

	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return s, nil, nil })
	f.frames <- sessionFrame(sessionCommand{CommandID: "cmd_named", Verb: verbSteer, PublicID: "pub_named",
		SessionID: s.ID(), RunID: t1.RunID(), Message: "meant for t1"})
	f.waitAck(t, 2)
	close(m.gate)
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = s.WaitIdle(ctx)
	f.mu.Lock()
	fin := f.acks[1]
	f.mu.Unlock()
	want := refusedNoTurn + " for run " + t1.RunID() + " (session " + s.ID() + " is running " + t2.RunID() + ", not " + t1.RunID() + ")"
	if fin.State != "finished" || fin.Status != "failed" || fin.Error != want {
		t.Errorf("finished ack = %+v\nwant failed %q", fin, want)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, text := range m.seen {
		if text == "meant for t1" {
			t.Errorf("a model call saw the steer meant for t1: %v", m.seen)
		}
	}
}

// blockingLease is a Storage whose lease never answers: Acquire waits
// for its context — a backend's lock stuck behind a dead holder.
type blockingLease struct{ thread.Storage }

func (blockingLease) Acquire(ctx context.Context, _ string, _ any) (int, time.Time, error) {
	<-ctx.Done()
	return 0, time.Time{}, ctx.Err()
}

func (blockingLease) Yield(context.Context, string, any) error { return nil }

// TestSessionClaimTimeout: a Claim bounded by the resolve's deadline
// that hits it is refused in its own words, "session claim timed out" —
// not the context's "context deadline exceeded" — before any ack.
func TestSessionClaimTimeout(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	app, turn, callID := parkedSession(t, st)
	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stuck, err := thread.Open(ctx, blockingLease{st}, app.ID(), core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { sessionResolveTimeout = d }(sessionResolveTimeout)
	sessionResolveTimeout = 50 * time.Millisecond
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	newSessionsLink(t, ts.URL, func(context.Context, string) (*thread.Session, func(), error) { return stuck, nil, nil })
	f.frames <- decideFrame("cmd_stuck", stuck, turn, callID, "approve")
	f.waitAck(t, 1)
	f.mu.Lock()
	a := f.acks[0]
	f.mu.Unlock()
	if a.State != "rejected" || a.Error != "session claim timed out" {
		t.Errorf("ack = %+v, want rejected %q", a, "session claim timed out")
	}
}
