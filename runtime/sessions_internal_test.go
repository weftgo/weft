package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/studio"
	linkruntime "github.com/weftgo/weft/studio/runtime"
	"github.com/weftgo/weft/thread"
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

// identityRun is everything one app turn produces that observability
// must leave alone: its events (as the Tap saw them), the session's
// entries and context, the run's usage and its metadata.
type identityRun struct {
	Events   []string
	Entries  []string
	Context  string
	Usage    core.Usage
	Metadata map[string]string
}

// runAppTurn runs one turn of a fresh thread session — deterministic
// ids and clock — a lookup and an answer — and records it.
func runAppTurn(t *testing.T, install func(agent *core.Agent) func()) identityRun {
	t.Helper()
	ctx := context.Background()
	var (
		mu  sync.Mutex
		out identityRun
	)
	lookup := core.Tool("lookup_order", "Look up an order by ID.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return `{"status":"shipped"}`, nil
	})
	agent := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"4411"}`}),
			wefttest.Say("It shipped."),
		),
		core.Name("acme-support"),
		core.Tap(func(ctx context.Context, ev core.Event) {
			b, err := json.Marshal(ev)
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
		delete(m, "runtime_id") // per link, random
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
