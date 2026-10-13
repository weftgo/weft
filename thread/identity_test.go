package thread_test

// The session-identity tests, S5 of WEFT-OTEL-DATA-ARCHITECTURE (ADR
// 0024): every run a session starts — a send, a resume, an overflow
// re-run — carries weft.session.id and weft.turn, plus weft.public_id
// when the session has one and the fork or pool linkage the header
// names, on its spans and its records; the session's keys win over a
// caller's colliding Metadata; List finds a session by its create-time
// public id. The tracer and the Logs API provider here are implemented
// in this file on the OTel API's embedded types — the same stance the
// root module's otel_test.go and records_test.go take — so the
// attributes are asserted on the real wire shapes the core stamps
// while the SDK never enters this module's go.mod.

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// idTracerProvider records every span started through it, with the
// attributes the loop stamps.
type idTracerProvider struct {
	trace.TracerProvider // embedded marker

	mu    sync.Mutex
	spans []*idSpan
	// panicSpanContext makes every span's SpanContext panic: a broken
	// tracer the session's reporting reads (seeRun), never the loop.
	panicSpanContext bool
}

func (p *idTracerProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	return &idTracer{p: p}
}

// named returns the spans whose name starts with prefix, in start
// order.
func (p *idTracerProvider) named(prefix string) []*idSpan {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*idSpan
	for _, s := range p.spans {
		if len(s.name) >= len(prefix) && s.name[:len(prefix)] == prefix {
			out = append(out, s)
		}
	}
	return out
}

type idTracer struct {
	trace.Tracer // embedded marker

	p *idTracerProvider
}

func (t *idTracer) Start(ctx context.Context, name string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	s := &idSpan{name: name, recording: true, panicSC: t.p.panicSpanContext}
	t.p.mu.Lock()
	t.p.spans = append(t.p.spans, s)
	t.p.mu.Unlock()
	return trace.ContextWithSpan(ctx, s), s
}

// idSpan records the attributes set on it. The methods the loop calls
// on its spans are overridden; the rest stay the interface's no-ops.
type idSpan struct {
	trace.Span // embedded: the methods the loop and tests use are overridden

	mu        sync.Mutex
	name      string
	attrs     []attribute.KeyValue
	recording bool
	panicSC   bool
}

func (s *idSpan) End(...trace.SpanEndOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = false
}

func (s *idSpan) IsRecording() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recording
}

func (s *idSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attrs = append(s.attrs, attrs...)
}

func (s *idSpan) SetStatus(codes.Code, string) {}

// SpanContext answers the zero context: the embedded interface is nil,
// and a run whose tools execute reads it (a span-less observer would
// otherwise panic, contained, and drop the step's messages).
func (s *idSpan) SpanContext() trace.SpanContext {
	if s.panicSC {
		panic("broken tracer: SpanContext")
	}
	return trace.SpanContext{}
}

func (s *idSpan) RecordError(error, ...trace.EventOption) {}

func (s *idSpan) attr(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kv := range s.attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

// idLogProvider records every log record emitted through it, with its
// attributes.
type idLogProvider struct {
	embedded.LoggerProvider

	mu      sync.Mutex
	records []idLogRecord
}

func (p *idLogProvider) Logger(string, ...log.LoggerOption) log.Logger {
	return &idLogger{p: p}
}

type idLogger struct {
	embedded.Logger

	p *idLogProvider
}

func (l *idLogger) Emit(_ context.Context, r log.Record) {
	rec := idLogRecord{}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		rec.attrs = append(rec.attrs, kv)
		return true
	})
	l.p.mu.Lock()
	defer l.p.mu.Unlock()
	l.p.records = append(l.p.records, rec)
}

func (l *idLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }

type idLogRecord struct {
	attrs []attribute.KeyValue
}

func (r idLogRecord) attr(key string) string {
	for _, kv := range r.attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

// ofRecord returns the records whose weft.record attribute is kind, in
// emission order.
func (p *idLogProvider) ofRecord(kind string) []idLogRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []idLogRecord
	for _, r := range p.records {
		if r.attr("weft.record") == kind {
			out = append(out, r)
		}
	}
	return out
}

// all returns every captured record.
func (p *idLogProvider) all() []idLogRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]idLogRecord(nil), p.records...)
}

// mdCollector captures the metadata in force on each run's RunStart —
// the merged view the session and the caller both fed into the run.
type mdCollector struct {
	mu  sync.Mutex
	mds []map[string]string
}

func (c *mdCollector) tap() core.Option {
	return core.Tap(func(ctx context.Context, ev core.Event) {
		if _, ok := ev.(core.RunStart); ok {
			c.mu.Lock()
			c.mds = append(c.mds, core.MetadataFromContext(ctx))
			c.mu.Unlock()
		}
	})
}

func (c *mdCollector) snapshot() []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]string(nil), c.mds...)
}

// A turn's run carries the session identity on its spans and its
// records: weft.session.id, weft.turn — the counter the run id was
// minted from — and weft.public_id when PublicID set one (S5).
func TestRunIdentityOnSpansAndRecords(t *testing.T) {
	tp, lp := &idTracerProvider{}, &idLogProvider{}
	ctx := context.Background()
	agent := core.New(wefttest.Script(wefttest.Say("first"), wefttest.Say("second")),
		core.Name("identity"),
		core.TracerProvider(tp), core.LoggerProvider(lp))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.PublicID("support-42"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t1, err := s.Send(ctx, core.User("question one"))
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("Wait 1: %v", err)
	}
	t2, err := s.Send(ctx, core.User("question two"))
	if err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatalf("Wait 2: %v", err)
	}

	// Spans: the invoke_agent span of each run, and every other span
	// below it, carries the pairs.
	invokes := tp.named("invoke_agent")
	if len(invokes) != 2 {
		t.Fatalf("invoke_agent spans = %d, want one per turn", len(invokes))
	}
	for i, sp := range invokes {
		want := map[string]string{
			"weft.session.id": s.ID(),
			"weft.turn":       strconv.Itoa(i + 1),
			"weft.public_id":  "support-42",
		}
		for k, v := range want {
			if got := sp.attr(k); got != v {
				t.Errorf("turn %d, span %q: %s = %q, want %q", i+1, sp.name, k, got, v)
			}
		}
	}
	for i, sp := range tp.named("chat") {
		if got := sp.attr("weft.session.id"); got != s.ID() {
			t.Errorf("chat span %d: weft.session.id = %q, want %q", i, got, s.ID())
		}
	}

	// Records: every record of the runs carries the pairs — the event
	// records and the messages records alike.
	events := lp.ofRecord("event")
	if len(events) == 0 {
		t.Fatal("no event records captured")
	}
	for _, r := range lp.all() {
		if got := r.attr("weft.session.id"); got != s.ID() {
			t.Errorf("record %v: weft.session.id = %q, want %q", r.attrs, got, s.ID())
		}
		if got := r.attr("weft.public_id"); got != "support-42" {
			t.Errorf("record %v: weft.public_id = %q, want support-42", r.attrs, got)
		}
	}
	// The two runs' run_start event records name their own turn.
	starts := 0
	for _, r := range events {
		if r.attr("weft.event.type") == "run_start" {
			starts++
			want := strconv.Itoa(starts)
			if got := r.attr("weft.turn"); got != want {
				t.Errorf("run_start record %d: weft.turn = %q, want %q", starts, got, want)
			}
		}
	}
	if starts != 2 {
		t.Errorf("run_start event records = %d, want 2", starts)
	}
}

// A resumed turn carries the same session and its own turn: the resume
// is a run of the session like any other, minted from the same counter
// (S5, ADR 0021's resume through runTurn).
func TestRunIdentityResumedTurn(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		col := &mdCollector{}
		agent := core.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "dangerous", ID: "call_9"}),
				wefttest.Say("the call ran"),
			),
			core.Tool("dangerous", "needs a human", func(ctx context.Context, in struct{}) (string, error) {
				return "approved result", nil
			}, core.RequireApproval()),
			col.tap(),
		)
		s, err := thread.Create(ctx, st, agent, thread.PublicID("share-9"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		t1, err := s.Send(ctx, core.User("do the dangerous thing"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, err := t1.Wait(); err != nil {
			t.Fatalf("Wait 1: %v", err) // a pending turn is a success
		}
		rt, err := s.Decide(ctx, thread.Approve("call_9"))
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if rt == nil {
			t.Fatal("Decide did not resume the boundary")
		}
		if _, err := rt.Wait(); err != nil {
			t.Fatalf("resume Wait: %v", err)
		}

		mds := col.snapshot()
		if len(mds) != 2 {
			t.Fatalf("runs = %d, want 2 (the parked turn and its resume)", len(mds))
		}
		for i, md := range mds {
			if md["weft.session.id"] != s.ID() {
				t.Errorf("run %d: weft.session.id = %q, want %q", i, md["weft.session.id"], s.ID())
			}
			if md["weft.public_id"] != "share-9" {
				t.Errorf("run %d: weft.public_id = %q, want share-9", i, md["weft.public_id"])
			}
		}
		if mds[0]["weft.turn"] != "1" || mds[1]["weft.turn"] != "2" {
			t.Errorf("turns = %q, %q; want 1, 2 — the resume mints its own", mds[0]["weft.turn"], mds[1]["weft.turn"])
		}
		if rt.RunID() != s.ID()+"-t2" {
			t.Errorf("resume RunID = %q, want %s-t2", rt.RunID(), s.ID())
		}
	})
}

// The overflow re-run carries the new turn number: the re-run is
// minted from the same counter, and its metadata names the turn it now
// is, not the failed attempt's (S5).
func TestRunIdentityOverflowReRunCarriesNewTurn(t *testing.T) {
	ctx := context.Background()
	col := &mdCollector{}
	model := wefttest.Script(
		wefttest.Say("the first answer"),
		wefttest.Fail(core.ErrContextOverflow),
		wefttest.Say("the summary of what came before"),
		wefttest.Say("recovered after compaction"),
	)
	agent := core.New(model, col.tap())
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t0, err := s.Send(ctx, core.User("a first question"))
	if err != nil {
		t.Fatalf("Send 0: %v", err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatalf("Wait 0: %v", err)
	}
	t1, err := s.Send(ctx, core.User("a prompt that overflows"))
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("Wait 1: %v", err)
	}

	mds := col.snapshot()
	if len(mds) != 3 {
		t.Fatalf("runs = %d, want 3 (first turn, failed attempt, re-run)", len(mds))
	}
	for i, md := range mds {
		if md["weft.session.id"] != s.ID() {
			t.Errorf("run %d: weft.session.id = %q, want %q", i, md["weft.session.id"], s.ID())
		}
	}
	if mds[1]["weft.turn"] != "2" || mds[2]["weft.turn"] != "3" {
		t.Errorf("turns = ..., %q, %q; want the failed attempt 2, the re-run 3",
			mds[1]["weft.turn"], mds[2]["weft.turn"])
	}
	if t1.RunID() != s.ID()+"-t3" {
		t.Errorf("re-run RunID = %q, want %s-t3", t1.RunID(), s.ID())
	}
}

// A fork's runs carry the new session and weft.session.forked_from —
// the parent session and entry the fork grew from (S5, ADR 0011 §3).
func TestRunIdentityFork(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		col := &mdCollector{}
		agent := core.New(wefttest.Script(wefttest.Say("parent reply"), wefttest.Say("fork reply")), col.tap())
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		t0, err := s.Send(ctx, core.User("the original question"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, err := t0.Wait(); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		f, err := s.Fork(ctx, t0.ID())
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		ft, err := f.Send(ctx, core.User("the fork's question"))
		if err != nil {
			t.Fatalf("fork Send: %v", err)
		}
		if _, err := ft.Wait(); err != nil {
			t.Fatalf("fork Wait: %v", err)
		}

		mds := col.snapshot()
		if len(mds) != 2 {
			t.Fatalf("runs = %d, want 2 (the original and the fork's)", len(mds))
		}
		orig, forked := mds[0], mds[1]
		if orig["weft.session.id"] != s.ID() {
			t.Errorf("original run: weft.session.id = %q, want %q", orig["weft.session.id"], s.ID())
		}
		if _, ok := orig["weft.session.forked_from"]; ok {
			t.Errorf("original run carries forked_from: %v", orig)
		}
		if forked["weft.session.id"] != f.ID() {
			t.Errorf("fork run: weft.session.id = %q, want the fork's %q", forked["weft.session.id"], f.ID())
		}
		if want := s.ID() + "#" + t0.ID(); forked["weft.session.forked_from"] != want {
			t.Errorf("fork run: weft.session.forked_from = %q, want %q", forked["weft.session.forked_from"], want)
		}
		// The fork point is the prompt entry, so the copied path holds
		// no turn entries: the fork numbers its first run from zero
		// (fork at an entry past a turn entry and the count carries —
		// TestForkMintsRunIDsPastTheCopiedTurns).
		if forked["weft.turn"] != "1" {
			t.Errorf("fork run: weft.turn = %q, want 1 — no turns were copied", forked["weft.turn"])
		}
	})
}

// A caller's Metadata key that collides with a session key loses: the
// session's identity is appended after the caller's options, and a
// later core.Metadata wins (S5, S1.1). Keys the session does not claim
// pass through untouched.
func TestRunIdentityCallerCollisionLoses(t *testing.T) {
	ctx := context.Background()
	col := &mdCollector{}
	agent := core.New(wefttest.Script(wefttest.Say("a reply")), col.tap())
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.PublicID("share-1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	turn, err := s.Send(ctx, core.User("hello"),
		thread.RunOptions(core.Metadata(map[string]string{
			"weft.session.id": "forged-session",
			"weft.turn":       "99",
			"tenant":          "acme",
		})))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	mds := col.snapshot()
	if len(mds) != 1 {
		t.Fatalf("runs = %d, want 1", len(mds))
	}
	md := mds[0]
	if md["weft.session.id"] != s.ID() {
		t.Errorf("weft.session.id = %q, want the session's %q — the caller's forged key must lose", md["weft.session.id"], s.ID())
	}
	if md["weft.turn"] != "1" {
		t.Errorf("weft.turn = %q, want 1 — the caller's forged key must lose", md["weft.turn"])
	}
	if md["tenant"] != "acme" {
		t.Errorf("tenant = %q, want acme — keys the session does not claim pass through", md["tenant"])
	}
}

// List finds a session by its create-time public id — the header's
// Meta is what every backend matches — and the id is fixed for the
// session's life: SetInfo refuses the reserved key (ErrReservedKey),
// and an info entry that names it anyway never overrides the header
// in Session.Meta or on the runs (ADR 0024 S5).
func TestListByPublicID(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := core.New(wefttest.Script(wefttest.Say("r"), wefttest.Say("r"), wefttest.Say("r")))
		a, err := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_pub_a" }), thread.PublicID("share-a"))
		if err != nil {
			t.Fatalf("Create a: %v", err)
		}
		if _, err := thread.Create(ctx, st, agent, thread.IDs(func() string { return "s_pub_b" })); err != nil {
			t.Fatalf("Create b: %v", err)
		}
		c, err := thread.Create(ctx, st, agent,
			thread.IDs(func() string { return "s_pub_c" }), thread.PublicID("share-c"))
		if err != nil {
			t.Fatalf("Create c: %v", err)
		}

		p, err := thread.List(ctx, st, thread.Query{Meta: map[string]string{"weft.public_id": "share-a"}})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if p.Total != 1 || len(p.Sessions) != 1 || p.Sessions[0].ID != a.ID() {
			t.Fatalf("List by share-a = %d sessions, want exactly %s", p.Total, a.ID())
		}

		// The public id cannot be rotated: SetInfo refuses the reserved
		// key, writes nothing, and the header keeps matching.
		before := len(c.Entries())
		err = c.SetInfo(ctx, "rotated", map[string]string{"weft.public_id": "share-rot"})
		if !errors.Is(err, thread.ErrReservedKey) {
			t.Fatalf("SetInfo with a weft. key: err = %v, want ErrReservedKey", err)
		}
		if got := len(c.Entries()); got != before {
			t.Errorf("the refused SetInfo wrote %d entries", got-before)
		}
		if p, err = thread.List(ctx, st, thread.Query{Meta: map[string]string{"weft.public_id": "share-rot"}}); err != nil || p.Total != 0 {
			t.Errorf("List by the rotated id: total %d, err %v, want 0", p.Total, err)
		}
		if p, err = thread.List(ctx, st, thread.Query{Meta: map[string]string{"weft.public_id": "share-c"}}); err != nil || p.Total != 1 {
			t.Errorf("List by the create-time id: total %d, err %v, want 1 — the header keeps it", p.Total, err)
		}

		// Defence in depth: a file that already holds an info entry
		// naming the reserved key — written before the rule, or by
		// other hands — still cannot rotate the id. The header's value
		// stands in the merged view, and the runs carry it.
		if err := st.Append(ctx, c.ID(), thread.InfoEntry{
			ID: "e_rot", Created: time.Now().UTC(),
			Meta: map[string]string{"weft.public_id": "share-rot", "team": "support"},
		}); err != nil {
			t.Fatalf("Append the old-shape info entry: %v", err)
		}
		col := &mdCollector{}
		rotated := core.New(wefttest.Script(wefttest.Say("r")), col.tap())
		abandon(t, st, c.ID())
		cAgent, err := thread.Open(ctx, st, c.ID(), rotated)
		if err != nil {
			t.Fatalf("Open c: %v", err)
		}
		if got := cAgent.Meta(); got["weft.public_id"] != "share-c" || got["team"] != "support" {
			t.Errorf("Meta = %v, want the header's share-c beside the ordinary key", got)
		}
		turn, err := cAgent.Send(ctx, core.User("after the rotation attempt"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		mds := col.snapshot()
		if len(mds) != 1 || mds[0]["weft.public_id"] != "share-c" {
			t.Errorf("run carries %v, want the header's share-c — the public id never rotates", mds)
		}
	})
}

// The session's title rides its turns as weft.session.title (metadata,
// so it survives a content-off destination): absent before SetInfo
// names one, the current title on every turn after — a title set after
// the first turn reaches the later ones, a replaced one the turns that
// follow it.
func TestRunIdentitySessionTitle(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		col := &mdCollector{}
		agent := core.New(wefttest.Script(wefttest.Say("one"), wefttest.Say("two"), wefttest.Say("three")), col.tap())
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer func() { _ = s.Close(ctx) }()
		send := func(text string) {
			t.Helper()
			turn, err := s.Send(ctx, core.User(text))
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if _, err := turn.Wait(); err != nil {
				t.Fatalf("Wait: %v", err)
			}
		}
		send("first")
		if err := s.SetInfo(ctx, "Acme support desk", nil); err != nil {
			t.Fatalf("SetInfo: %v", err)
		}
		send("second")
		if err := s.SetInfo(ctx, "Trip planner", nil); err != nil {
			t.Fatalf("SetInfo: %v", err)
		}
		send("third")
		mds := col.snapshot()
		if len(mds) != 3 {
			t.Fatalf("runs = %d, want 3", len(mds))
		}
		if v, ok := mds[0]["weft.session.title"]; ok {
			t.Errorf("turn 1 carries weft.session.title %q before any SetInfo named one", v)
		}
		if mds[1]["weft.session.title"] != "Acme support desk" || mds[2]["weft.session.title"] != "Trip planner" {
			t.Errorf("titles = %q, %q; want the title current at each turn's start",
				mds[1]["weft.session.title"], mds[2]["weft.session.title"])
		}
	})
}

// A resume turn links the turn that parked (UX-E-17): its run carries
// weft.resumed_from — the parked run's id, from the request entries —
// weft.resume.calls, and the deciding actor as weft.approval.actor; the
// parked turn's run carries none of them.
func TestResumeTurnLinksTheParkedRun(t *testing.T) {
	tp := &idTracerProvider{}
	ctx := context.Background()
	refund := core.Tool("refund", "Refund an order.", func(context.Context, struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "rf1", Args: `{}`}),
		wefttest.Say("done"),
	), core.Name("linked"), core.TracerProvider(tp), refund)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	res1, err := t1.Wait()
	if err != nil || len(res1.Pending) != 1 {
		t.Fatalf("parked turn: %v, %v", res1, err)
	}
	d := thread.Approve(res1.Pending[0].ID)
	d.Who = "op_42"
	t2, err := s.Decide(ctx, d)
	if err != nil || t2 == nil {
		t.Fatalf("Decide: turn %v, err %v", t2, err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	invokes := tp.named("invoke_agent")
	if len(invokes) != 2 {
		t.Fatalf("invoke_agent spans = %d, want 2", len(invokes))
	}
	parked, resumed := invokes[0], invokes[1]
	for _, k := range []string{"weft.resumed_from", "weft.resume.calls", "weft.approval.actor"} {
		if v := parked.attr(k); v != "" {
			t.Errorf("parked turn's run carries %s = %q", k, v)
		}
	}
	if got := resumed.attr("weft.resumed_from"); got != t1.RunID() {
		t.Errorf("weft.resumed_from = %q, want the parked run %q", got, t1.RunID())
	}
	if got, want := resumed.attr("weft.resume.calls"), res1.Pending[0].ID+"=approved"; got != want {
		t.Errorf("weft.resume.calls = %q, want %q", got, want)
	}
	if got := resumed.attr("weft.approval.actor"); got != "op_42" {
		t.Errorf("weft.approval.actor = %q, want op_42", got)
	}
}

// linkedRefundAgent is a RequireApproval refund agent over script whose
// spans land in tp.
func linkedRefundAgent(tp *idTracerProvider, steps ...wefttest.Turn) *core.Agent {
	refund := core.Tool("refund", "Refund an order.", func(context.Context, struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	return core.New(wefttest.Script(steps...), core.Name("linked"), core.TracerProvider(tp), refund)
}

// everyDecidingRunLinks fails when an invoke_agent span reports
// decisions (weft.resume.calls) without naming the run it resumes:
// whatever turn shape applied them, the link comes with them.
func everyDecidingRunLinks(t *testing.T, tp *idTracerProvider) {
	t.Helper()
	for i, sp := range tp.named("invoke_agent") {
		if sp.attr("weft.resume.calls") != "" && sp.attr("weft.resumed_from") == "" {
			t.Errorf("invoke_agent #%d carries weft.resume.calls %q but no weft.resumed_from", i, sp.attr("weft.resume.calls"))
		}
	}
}

// A session reopened behind a parked turn resumes it linked: Open
// restores the boundary from the request entries, so the resume run
// Decide starts on the reopened Session names the parked run and the
// actor exactly as a resume on the writer that parked (U13.1-fix).
func TestResumeAfterOpenLinksTheParkedRun(t *testing.T) {
	tp := &idTracerProvider{}
	ctx := context.Background()
	agent := linkedRefundAgent(tp,
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "rf1", Args: `{}`}),
		wefttest.Say("done"),
	)
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("parked turn: %v, %v", res, err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s2, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close(ctx) }()
	d := thread.Approve("rf1")
	d.Who = "op_42"
	t2, err := s2.Decide(ctx, d)
	if err != nil || t2 == nil {
		t.Fatalf("Decide after Open: turn %v, err %v", t2, err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	invokes := tp.named("invoke_agent")
	if len(invokes) != 2 {
		t.Fatalf("invoke_agent spans = %d, want 2", len(invokes))
	}
	r := invokes[1]
	if got := r.attr("weft.resumed_from"); got != t1.RunID() {
		t.Errorf("weft.resumed_from after Open = %q, want the parked run %q", got, t1.RunID())
	}
	if got := r.attr("weft.resume.calls"); got != "rf1=approved" {
		t.Errorf("weft.resume.calls after Open = %q, want rf1=approved", got)
	}
	if got := r.attr("weft.approval.actor"); got != "op_42" {
		t.Errorf("weft.approval.actor after Open = %q, want op_42", got)
	}
}

// A chain links each resume to the run that parked its own calls: t1
// parks, t2 resumes it and parks again, t3 resumes t2 — t3's
// weft.resumed_from is t2's run, never t1's (U13.1-fix).
func TestResumeChainLinksTheLatestPark(t *testing.T) {
	tp := &idTracerProvider{}
	ctx := context.Background()
	agent := linkedRefundAgent(tp,
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "rf1", Args: `{}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "rf2", Args: `{}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	t1, err := s.Send(ctx, core.User("refund both"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("t1: %v, %v", res, err)
	}
	t2, err := s.Decide(ctx, thread.Approve("rf1"))
	if err != nil || t2 == nil {
		t.Fatalf("Decide rf1: turn %v, err %v", t2, err)
	}
	if res, err := t2.Wait(); err != nil || len(res.Pending) != 1 || res.Pending[0].ID != "rf2" {
		t.Fatalf("t2 (resumes t1, parks rf2): %v, %v", res, err)
	}
	t3, err := s.Decide(ctx, thread.Deny("rf2", "not today"))
	if err != nil || t3 == nil {
		t.Fatalf("Decide rf2: turn %v, err %v", t3, err)
	}
	if _, err := t3.Wait(); err != nil {
		t.Fatal(err)
	}
	invokes := tp.named("invoke_agent")
	if len(invokes) != 3 {
		t.Fatalf("invoke_agent spans = %d, want 3", len(invokes))
	}
	for i, want := range []struct{ from, calls string }{
		{"", ""},
		{t1.RunID(), "rf1=approved"},
		{t2.RunID(), "rf2=denied"},
	} {
		if got := invokes[i].attr("weft.resumed_from"); got != want.from {
			t.Errorf("run %d: weft.resumed_from = %q, want %q", i+1, got, want.from)
		}
		if got := invokes[i].attr("weft.resume.calls"); got != want.calls {
			t.Errorf("run %d: weft.resume.calls = %q, want %q", i+1, got, want.calls)
		}
	}
	everyDecidingRunLinks(t, tp)
}

// Decisions link whichever turn applies them (U13.1-fix: the link is
// computed whenever the recorded decisions ride a run, not only on a
// resume turn). With AutoResume off, a Decide records and a plain Send
// queues behind the open boundary (ADR 0021 §1) — it never carries the
// decisions itself; the Resume that follows does, linked, and the
// queued Send then runs with no link and no actor.
func TestDecisionsLinkWhicheverTurnAppliesThem(t *testing.T) {
	tp := &idTracerProvider{}
	ctx := context.Background()
	agent := linkedRefundAgent(tp,
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "rf1", Args: `{}`}),
		wefttest.Say("refunded"),
		wefttest.Say("and done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	t1, err := s.Send(ctx, core.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("parked turn: %v, %v", res, err)
	}
	d := thread.Approve("rf1")
	d.Who = "op_42"
	if t2, err := s.Decide(ctx, d); err != nil || t2 != nil {
		t.Fatalf("Decide under AutoResume(false): turn %v, err %v; want no turn", t2, err)
	}
	t3, err := s.Send(ctx, core.User("anything else?"))
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tn := range []*thread.Turn{t2, t3} {
		if _, err := tn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	invokes := tp.named("invoke_agent")
	if len(invokes) != 3 {
		t.Fatalf("invoke_agent spans = %d, want 3 (park, resume, the queued send)", len(invokes))
	}
	r, after := invokes[1], invokes[2]
	if got := r.attr("weft.resumed_from"); got != t1.RunID() {
		t.Errorf("weft.resumed_from = %q, want the parked run %q", got, t1.RunID())
	}
	if got := r.attr("weft.resume.calls"); got != "rf1=approved" {
		t.Errorf("weft.resume.calls = %q, want rf1=approved", got)
	}
	if got := r.attr("weft.approval.actor"); got != "op_42" {
		t.Errorf("weft.approval.actor = %q, want op_42", got)
	}
	for _, k := range []string{"weft.resumed_from", "weft.resume.calls", "weft.approval.actor"} {
		if v := after.attr(k); v != "" {
			t.Errorf("the queued send's run carries %s = %q", k, v)
		}
	}
	everyDecidingRunLinks(t, tp)
}

// A reporting panic never costs per-step persistence (U13.1-fix;
// observability never changes behaviour, T4): the transcript observer
// appends each step's messages before the session's own reporting —
// the run sight seeRun takes for compaction markers — reads the run's
// span, so a tracer whose SpanContext panics still leaves step 0's
// messages durable while step 1 runs, not only at the turn's end.
func TestReportingPanicKeepsPerStepPersistence(t *testing.T) {
	tp := &idTracerProvider{panicSpanContext: true}
	ctx := context.Background()
	var s *thread.Session
	var seen []string
	lookup := core.Tool("lookup", "Look up.", func(context.Context, struct{}) (string, error) {
		return "found it", nil
	})
	check := core.Tool("check", "Check.", func(context.Context, struct{}) (string, error) {
		for _, e := range s.Entries() {
			if me, ok := e.(thread.MessageEntry); ok {
				for _, p := range me.Message.Content {
					if r, ok := p.(core.ToolResultPart); ok {
						seen = append(seen, r.Content)
					}
				}
			}
		}
		return "checked", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", ID: "l1", Args: `{}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "check", ID: "c1", Args: `{}`}),
		wefttest.Say("done"),
	), core.TracerProvider(tp), lookup, check)
	var err error
	s, err = thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	turn, err := s.Send(ctx, core.User("look it up"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(seen, "found it") {
		t.Errorf("step 0's tool result was not durable while step 1 ran (results on the tree: %q): a reporting panic skipped the per-step append", seen)
	}
}
