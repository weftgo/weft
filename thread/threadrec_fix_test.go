package thread_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log"
	logembedded "go.opentelemetry.io/otel/log/embedded"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/trace"
	traceembedded "go.opentelemetry.io/otel/trace/embedded"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// The record family's emitter (U12.1-fix): no provider call under the
// session lock, held records out at Close, the pre-run summarizer under
// its run, and observability that never changes the session.

const threadScope = "github.com/weftgo/weft/thread"

// gateLP is a LoggerProvider whose thread records block in Emit until
// the gate opens — a slow exporter, an obsdb insert, a Blocking()
// destination; the core's own records pass.
type gateLP struct {
	logembedded.LoggerProvider
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (p *gateLP) Logger(name string, _ ...log.LoggerOption) log.Logger {
	if name != threadScope {
		return lognoop.Logger{}
	}
	return gateLogger{p: p}
}

type gateLogger struct {
	logembedded.Logger
	p *gateLP
}

func (l gateLogger) Emit(_ context.Context, r log.Record) {
	if strings.HasPrefix(r.EventName(), "weft.thread.") {
		l.p.once.Do(func() { close(l.p.entered) })
		<-l.p.gate
	}
}

func (gateLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }

// A slow LoggerProvider never holds the session: while the emitter is
// stuck in Emit, Pending, Entries and a Decide all return; Close waits
// for the emitter once the provider lets go.
func TestThreadRecordsSlowProviderDoesNotBlockTheSession(t *testing.T) {
	lp := &gateLP{gate: make(chan struct{}), entered: make(chan struct{})}
	refund := core.Tool("issue_refund", "Refund.", func(context.Context, struct{}) (string, error) { return "ok", nil }, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r1", Args: `{}`}),
		wefttest.Say("refunded."),
	), refund, core.LoggerProvider(lp))
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt)
	if err != nil {
		t.Fatal(err)
	}
	res := recSendWait(t, s, "refund")
	if len(res.Pending) != 1 {
		t.Fatalf("pending %v, want r1", res.Pending)
	}
	select {
	case <-lp.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no thread record reached the provider")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if p := s.Pending(); len(p) != 1 {
			t.Errorf("Pending = %v", p)
		}
		_ = s.Entries()
		if _, err := s.Decide(ctx, thread.Approve("r1")); err != nil {
			t.Errorf("Decide: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Pending / Entries / Decide waited on a blocked LoggerProvider")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close(ctx) }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) while the emitter was still in Emit", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(lp.gate)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close never returned")
	}
}

// Create → Grant → Close: no run ever reported, and the grant.mint is
// still exported — filed under no run (no weft.run.id), held true,
// keyed by the session's identity — and stored by the Local sink.
func TestThreadRecordsHeldAtCloseAreEmitted(t *testing.T) {
	rp := newRecPipe(t)
	agt := rp.agent("sre", wefttest.Script())
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.PublicID("pub_held"),
		thread.WithRunMetadata(map[string]string{"arena.batch": "b1"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run_command"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	mints := ofKind(rp.on.records(t), "grant.mint")
	if len(mints) != 1 {
		t.Fatalf("grant.mint records = %d, want 1", len(mints))
	}
	// Emitted on a context carrying no span: no trace or span id.
	rp.on.mu.Lock()
	for _, r := range rp.on.logs {
		if r.EventName() == "weft.thread.grant.mint" && (r.TraceID().IsValid() || r.SpanID().IsValid()) {
			t.Errorf("held grant.mint carries trace %s span %s, want none", r.TraceID(), r.SpanID())
		}
	}
	rp.on.mu.Unlock()
	m := mints[0]
	if _, ok := m.attrs["weft.run.id"]; ok || m.body["held"] != true || m.attrs["weft.session.id"] != s.ID() ||
		m.attrs["weft.public_id"] != "pub_held" || m.attrs["arena.batch"] != "b1" || m.attrs["weft.turn"] != "" {
		t.Errorf("held grant.mint = %v %v; want held, no run, the session's identity and run metadata", m.body, m.attrs)
	}
	if stored := rp.shutdown(t); stored["grant.mint"] != 1 {
		t.Errorf("the Local sink stored %v", stored)
	}
}

// The pre-run trigger's compaction runs before the turn's run reports:
// its summarizer span waits for that run and opens under its
// invoke_agent span, in its trace — never as a trace of its own.
func TestPreRunCompactionSummarizerUnderTheRun(t *testing.T) {
	rp := newRecPipe(t)
	fetch := core.Tool("fetch_doc", "Fetch a document.", func(_ context.Context, in struct {
		Topic string `json:"topic"`
	}) (string, error) {
		return strings.Repeat(in.Topic+" notes. ", 400), nil
	})
	agt := rp.agent("research", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "fetch_doc", Args: `{"topic":"raft"}`}),
		wefttest.Say("Read raft."),
		wefttest.Say("SUMMARY: raft read.").WithUsage(core.Usage{InputTokens: 900, OutputTokens: 30}),
		wefttest.Say("Paxos next."),
	), fetch)
	var fired atomic.Bool
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.ContextWindow(64_000), thread.KeepRecent(10),
		thread.TriggerFunc(func(in thread.TriggerInput) bool { return in.Estimated > 0 && fired.CompareAndSwap(false, true) }))
	if err != nil {
		t.Fatal(err)
	}
	recSendWait(t, s, "Fetch the raft document.")
	// The summary is the script's third answer: the compaction runs
	// before the second turn's own model call (its fourth).
	second, err := s.Send(ctx, core.User("What next?"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Wait(); err != nil {
		t.Fatal(err)
	}
	if !fired.Load() {
		t.Fatal("the pre-run trigger never fired")
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	spans := rp.on.summarizerSpans()
	if len(spans) != 1 {
		t.Fatalf("summarizer spans = %d, want 1", len(spans))
	}
	sp := spans[0]
	var root trace.SpanContext
	rp.on.mu.Lock()
	for _, x := range rp.on.spans {
		if strings.HasPrefix(x.Name(), "invoke_agent") && spanAttr(x, "weft.run.id").AsString() == second.RunID() {
			root = x.SpanContext()
		}
	}
	rp.on.mu.Unlock()
	if !root.IsValid() {
		t.Fatalf("no invoke_agent span for %s", second.RunID())
	}
	if !sp.Parent().IsValid() || sp.Parent().SpanID() != root.SpanID() || sp.SpanContext().TraceID() != root.TraceID() {
		t.Errorf("summarizer span parent %v trace %v; want %s's invoke_agent %v", sp.Parent(), sp.SpanContext().TraceID(), second.RunID(), root)
	}
	if spanAttr(sp, "weft.run.id").AsString() != second.RunID() || spanAttr(sp, "gen_ai.usage.input_tokens").AsInt64() != 900 {
		t.Errorf("summarizer span = %v", sp.Attributes())
	}
	rp.shutdown(t)
}

// lockedBuf is a log sink safe for the Session's goroutines.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A pre-run summarizer call held for a run that ends without reporting
// (it fails before any message joins) is dropped with a Debug line —
// when a later run reports (stale) or at Close — never opened bare: it
// would be a root span in a trace of its own, read as a run (record
// contract §3).
func TestHeldSummarizerOfAnUnreportedRunIsDropped(t *testing.T) {
	for _, c := range []struct {
		name, line string
		later      bool
	}{
		{"stale", "summarizer spans dropped: their run ended without reporting", true},
		{"close", "summarizer spans dropped at close: their run never reported", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rp := newRecPipe(t)
			logs := &lockedBuf{}
			fetch := core.Tool("fetch_doc", "Fetch a document.", func(_ context.Context, in struct {
				Topic string `json:"topic"`
			}) (string, error) {
				return strings.Repeat(in.Topic+" notes. ", 400), nil
			})
			agt := rp.agent("research", wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "fetch_doc", Args: `{"topic":"raft"}`}),
				wefttest.Say("Read raft."),
				wefttest.Say("SUMMARY: raft read.").WithUsage(core.Usage{InputTokens: 900, OutputTokens: 30}),
				wefttest.Say("Paxos next."),
			), fetch, core.MaxSteps(5),
				core.Logger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			var fired atomic.Bool
			ctx := context.Background()
			s, err := thread.Create(ctx, thread.Memory(), agt, thread.ContextWindow(64_000), thread.KeepRecent(10),
				thread.TriggerFunc(func(in thread.TriggerInput) bool { return in.Estimated > 0 && fired.CompareAndSwap(false, true) }))
			if err != nil {
				t.Fatal(err)
			}
			recSendWait(t, s, "Fetch the raft document.")
			// The second turn's run fails before it reports (a raised
			// MaxSteps is refused before any model call), after the pre-run
			// trigger's compaction held its summarizer call for it.
			second, err := s.Send(ctx, core.User("What next?"), thread.RunOptions(core.MaxSteps(50)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := second.Wait(); !errors.Is(err, core.ErrInvalidRunOption) {
				t.Fatalf("second turn: err = %v, want ErrInvalidRunOption", err)
			}
			if !fired.Load() {
				t.Fatal("the pre-run trigger never fired")
			}
			if c.later {
				recSendWait(t, s, "And then?")
			}
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if err := rp.p.ForceFlush(ctx); err != nil {
				t.Fatal(err)
			}
			for _, sp := range rp.on.summarizerSpans() {
				t.Errorf("summarizer span opened: run %s parent %v", spanAttr(sp, "weft.run.id").AsString(), sp.Parent())
			}
			if !strings.Contains(logs.String(), c.line) {
				t.Errorf("no Debug line %q; logs:\n%s", c.line, logs.String())
			}
			rp.shutdown(t)
		})
	}
}

// The sre-grants chain resumes every approved call with its provenance:
// the execute_tool span names approved_by grant (and the grant) or
// approver; a call a person approved reads user.
func TestResumedCallsNameTheirApproval(t *testing.T) {
	rp := newRecPipe(t)
	run := core.Tool("run_command", "Run a shell command.", func(_ context.Context, in struct {
		Command string `json:"command"`
	}) (string, error) {
		return "ran " + in.Command, nil
	}, core.RequireApproval())
	agt := rp.agent("sre", wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "run_command", ID: "c1", Args: `{"command":"kubectl get pods"}`},
			wefttest.Call{Name: "run_command", ID: "c2", Args: `{"command":"ls"}`},
			wefttest.Call{Name: "run_command", ID: "c3", Args: `{"command":"df -h"}`},
		),
		wefttest.Say("done."),
	), run)
	approver := func(_ context.Context, r thread.Request) (thread.Decision, bool) {
		if strings.Contains(string(r.Args), `"ls"`) {
			return recWho(thread.Approve(r.CallID), "policy-bot"), true
		}
		return thread.Decision{}, false
	}
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.WithApprover(approver, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "kubectl get *")}}); err != nil {
		t.Fatal(err)
	}
	grant := s.Leaf()
	recSendWait(t, s, "check the cluster")
	recWaitIdle(t, s)
	recDecide(t, s, recWho(thread.Approve("c3"), "agent-7"))
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	rp.on.mu.Lock()
	for _, x := range rp.on.spans {
		if strings.HasPrefix(x.Name(), "execute_tool") && spanAttr(x, "weft.tool.approved").AsBool() {
			got[spanAttr(x, "gen_ai.tool.call.id").AsString()] = [2]string{spanAttr(x, "weft.tool.approved_by").AsString(), spanAttr(x, "weft.grant.id").AsString()}
		}
	}
	rp.on.mu.Unlock()
	want := map[string][2]string{"c1": {"grant", grant}, "c2": {"approver", ""}, "c3": {"user", ""}}
	for call, w := range want {
		if got[call] != w {
			t.Errorf("%s's execute_tool span: approved_by/grant %v, want %v", call, got[call], w)
		}
	}
	rp.shutdown(t)
}

// A turn after a Branch names the turn whose line it continues
// (weft.turn.parent) and the branch (weft.session.branch: the label on
// the entry it went to, else the navigation's entry id); a linear turn
// names neither, and later turns on the branch keep the branch alone.
func TestTurnsNameTheirBranch(t *testing.T) {
	rp := newRecPipe(t)
	agt := rp.agent("trip", wefttest.Script(wefttest.Say("Lisbon."), wefttest.Say("Porto."), wefttest.Say("Seville."), wefttest.Say("Cádiz.")))
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt)
	if err != nil {
		t.Fatal(err)
	}
	r1 := recSendWait(t, s, "Plan Lisbon.")
	after := s.Leaf()
	r2 := recSendWait(t, s, "Add Porto.")
	if err := s.Branch(ctx, after); err != nil {
		t.Fatal(err)
	}
	if err := s.Label(ctx, after, "via-spain"); err != nil {
		t.Fatal(err)
	}
	r3 := recSendWait(t, s, "Seville instead.")
	r4 := recSendWait(t, s, "Add Cádiz.")
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, parent, branch string
	}{{r1.ID, "", ""}, {r2.ID, "", ""}, {r3.ID, "1", "via-spain"}, {r4.ID, "", "via-spain"}} {
		run, err := rp.p.LocalDB().Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Meta["weft.turn.parent"] != c.parent || run.Meta["weft.session.branch"] != c.branch {
			t.Errorf("run %s meta %v; want weft.turn.parent %q weft.session.branch %q", c.id, run.Meta, c.parent, c.branch)
		}
	}
	rp.shutdown(t)
}

// A failed send's leaf move (abandonLocked: the prompt written, its
// flush refused, the leaf moved back) is no navigation: the turns after
// it on an un-navigated line carry no weft.session.branch, and on a
// branched line they keep the branch the real navigation named
// (record contract §2). The move is marked on its entry, so a reopened
// Session reads it the same.
func TestAbandonedSendNamesNoBranch(t *testing.T) {
	rp := newRecPipe(t)
	agt := rp.agent("trip", wefttest.Script(wefttest.Say("Lisbon."), wefttest.Say("Porto."), wefttest.Say("Seville."), wefttest.Say("Cádiz.")))
	ctx := context.Background()
	st := &faultStorage{Storage: thread.Memory()}
	failNext := func(text string) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.flushFailAfter = func(es []thread.Entry) bool {
			return hasEntry(es, func(e thread.Entry) bool {
				m, ok := e.(thread.MessageEntry)
				return ok && m.Message.Text() == text
			})
		}
	}
	s, err := thread.Create(ctx, st, agt)
	if err != nil {
		t.Fatal(err)
	}
	r1 := recSendWait(t, s, "Plan Lisbon.")
	after := s.Leaf()
	failNext("Fails.")
	if _, err := s.Send(ctx, core.User("Fails.")); err == nil {
		t.Fatal("Send with a refused prompt flush succeeded")
	}
	r2 := recSendWait(t, s, "Add Porto.")
	if err := s.Branch(ctx, after); err != nil {
		t.Fatal(err)
	}
	if err := s.Label(ctx, after, "via-spain"); err != nil {
		t.Fatal(err)
	}
	r3 := recSendWait(t, s, "Seville instead.")
	failNext("Fails again.")
	if _, err := s.Send(ctx, core.User("Fails again.")); err == nil {
		t.Fatal("Send with a refused prompt flush succeeded")
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	abandoned := 0
	for _, e := range s.Entries() {
		if le, ok := e.(thread.LeafEntry); ok && le.Abandoned {
			abandoned++
		}
	}
	if abandoned != 2 {
		t.Errorf("abandoned leaf entries = %d, want 2 (one per failed send)", abandoned)
	}
	// Reopened: the marks are on the entries, not in memory.
	s2, err := thread.Open(ctx, st, s.ID(), agt)
	if err != nil {
		t.Fatal(err)
	}
	r4 := recSendWait(t, s2, "Add Cádiz.")
	if err := s2.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, parent, branch string
	}{{r1.ID, "", ""}, {r2.ID, "", ""}, {r3.ID, "1", "via-spain"}, {r4.ID, "", "via-spain"}} {
		run, err := rp.p.LocalDB().Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Meta["weft.turn.parent"] != c.parent || run.Meta["weft.session.branch"] != c.branch {
			t.Errorf("run %s meta %v; want weft.turn.parent %q weft.session.branch %q", c.id, run.Meta, c.parent, c.branch)
		}
	}
	rp.shutdown(t)
}

// scopedPanicLP / scopedPanicTP panic in every call weft/thread makes
// on them (its instrumentation scope) and are no-ops for the core's
// own scope: the core does not contain a panicking provider of its
// own (out of this item), so the test isolates thread's containment.
type scopedPanicLP struct{ logembedded.LoggerProvider }

func (scopedPanicLP) Logger(name string, _ ...log.LoggerOption) log.Logger {
	if name != threadScope {
		return lognoop.Logger{}
	}
	return panicLogger{}
}

type panicLogger struct{ logembedded.Logger }

func (panicLogger) Emit(context.Context, log.Record) { panic("thread logger: emit") }
func (panicLogger) Enabled(context.Context, log.EnabledParameters) bool {
	panic("thread logger: enabled")
}

type scopedPanicTP struct{ traceembedded.TracerProvider }

func (scopedPanicTP) Tracer(name string, _ ...trace.TracerOption) trace.Tracer {
	if name != threadScope {
		return tracenoop.NewTracerProvider().Tracer(name)
	}
	return panicTracer{}
}

type panicTracer struct{ traceembedded.Tracer }

func (panicTracer) Start(context.Context, string, ...trace.SpanStartOption) (context.Context, trace.Span) {
	panic("thread tracer: start")
}

// The observability on/off golden: the warroom-busy session — a steer
// delivered, a steer dropped, a queued send, an interrupt, a rollback —
// and a compaction run with no pipeline, with no-op providers, with
// providers that panic on every thread call, and with a real pipeline:
// the session's entries and every turn's result are identical, and the
// panics never escape.
func TestObservabilityNeverChangesTheSession(t *testing.T) {
	modes := []struct {
		name string
		opts func(t *testing.T) []core.Option
	}{
		{"none", func(*testing.T) []core.Option { return nil }},
		{"noop", func(*testing.T) []core.Option {
			return []core.Option{core.TracerProvider(tracenoop.NewTracerProvider()), core.LoggerProvider(lognoop.NewLoggerProvider())}
		}},
		{"panicking", func(*testing.T) []core.Option {
			return []core.Option{core.TracerProvider(scopedPanicTP{}), core.LoggerProvider(scopedPanicLP{})}
		}},
		{"pipeline", func(t *testing.T) []core.Option {
			rp := newRecPipe(t)
			return []core.Option{core.TracerProvider(rp.p.TracerProvider()), core.LoggerProvider(rp.p.LoggerProvider())}
		}},
	}
	var golden string
	for _, m := range modes {
		got := runBusyGolden(t, m.opts(t)...)
		if golden == "" {
			golden = got
			continue
		}
		if got != golden {
			t.Errorf("mode %s changed the session:\n got: %s\nwant: %s", m.name, got, golden)
		}
	}
}

// runBusyGolden runs the busy scenario with pinned ids and clock and
// returns the session's entries and every turn's outcome, as JSON.
func runBusyGolden(t *testing.T, opts ...core.Option) string {
	t.Helper()
	entered := make(chan struct{}, 8)
	release := make(chan struct{}, 8)
	metric := core.Tool("check_metric", "Read a metric.", func(ctx context.Context, in struct {
		Name string `json:"name"`
	}) (string, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return in.Name + " ok", nil
	})
	analysis := core.Tool("run_analysis", "A slow analysis.", func(ctx context.Context, _ struct {
		Dataset string `json:"dataset"`
	}) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "check_metric", ID: "m1", Args: `{"name":"rps"}`}),
		wefttest.Say("EU only: rps 4120."),
		wefttest.ToolCalls(wefttest.Call{Name: "check_metric", ID: "m2", Args: `{"name":"error_rate"}`}),
		wefttest.Say("error rate 2.3%."),
		wefttest.Say("queued follow-up answered."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_analysis", ID: "a1", Args: `{"dataset":"checkout-latency"}`}),
		wefttest.Say("p99 is 1880 ms."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_analysis", ID: "a2", Args: `{"dataset":"checkout-latency"}`}),
		wefttest.Say("error rate 2.3%."),
		wefttest.Say("SUMMARY: the war room so far.").WithUsage(core.Usage{InputTokens: 500, OutputTokens: 20}),
	), append([]core.Option{core.Name("warroom"), metric, analysis}, opts...)...)
	var n atomic.Int64
	ids := thread.IDs(func() string {
		if k := n.Add(1); k > 1 {
			return fmt.Sprintf("e_golden_%04d", k)
		}
		return "s_golden"
	})
	epoch := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt, ids, thread.Clock(func() time.Time { return epoch }),
		thread.ContextWindow(64_000), thread.KeepRecent(10), thread.NoAutoCompact())
	if err != nil {
		t.Fatal(err)
	}
	var turns []*thread.Turn
	send := func(text string, o ...thread.SendOption) {
		t.Helper()
		turn, err := s.Send(ctx, core.User(text), o...)
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, turn)
	}
	wait := func() {
		t.Helper()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the tool never started")
		}
	}
	send("Check the current rps.")
	wait()
	send("Focus only on the EU region.", thread.As(thread.Steer))
	release <- struct{}{}
	recWaitIdle(t, s)
	send("Check the error rate.")
	wait()
	send("this steer will be dropped", thread.As(thread.Steer))
	if n, err := s.ClearQueue(ctx); err != nil || n != 1 {
		t.Fatalf("ClearQueue = %d, %v", n, err)
	}
	send("After that, summarise in one line.", thread.As(thread.Queue))
	release <- struct{}{}
	recWaitIdle(t, s)
	send("Run a deep analysis.")
	wait()
	send("Stop — just tell me the p99.", thread.As(thread.Interrupt))
	recWaitIdle(t, s)
	send("Run another deep analysis.")
	wait()
	send("Forget that. What's the error rate?", thread.As(thread.Rollback))
	recWaitIdle(t, s)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	entries := s.Entries()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, turn := range turns {
		res, err := turn.Wait()
		o := fmt.Sprintf("%s err=%v", turn.RunID(), err)
		if res != nil {
			b, _ := json.Marshal(res.Messages)
			o += fmt.Sprintf(" text=%q usage=%+v messages=%s", res.Text(), res.Usage, b)
		}
		outcomes = append(outcomes, o)
	}
	b, err := json.Marshal(struct {
		Entries  []thread.Entry
		Outcomes []string
	}{entries, outcomes})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
