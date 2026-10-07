package thread_test

// The session compaction marker (ADR 0028 §8, session scope): the first
// run to start after a compaction carries one record of kind
// compaction — scope session, the compaction's hash, the counts — and
// no messages; runs before it and after it carry none.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// markerLogs is a Logs API provider that keeps every record.
type markerLogs struct {
	embedded.LoggerProvider
	mu   sync.Mutex
	recs []markerRec
}

type markerRec struct {
	span      trace.SpanContext
	scope     string
	eventName string
	body      string
	attrs     map[string]string
}

func (p *markerLogs) Logger(name string, _ ...log.LoggerOption) log.Logger {
	return &markerLogger{p: p, scope: name}
}

type markerLogger struct {
	embedded.Logger
	p     *markerLogs
	scope string
}

func (l *markerLogger) Emit(ctx context.Context, r log.Record) {
	rec := markerRec{span: trace.SpanContextFromContext(ctx), scope: l.scope, eventName: r.EventName(), body: r.Body().AsString(), attrs: map[string]string{}}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		rec.attrs[string(kv.Key)] = kv.Value.String()
		return true
	})
	l.p.mu.Lock()
	l.p.recs = append(l.p.recs, rec)
	l.p.mu.Unlock()
}

func (l *markerLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }

func (p *markerLogs) kind(k string) []markerRec {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []markerRec
	for _, r := range p.recs {
		if r.attrs["weft.record"] == k {
			out = append(out, r)
		}
	}
	return out
}

type markerBody struct {
	Scope          string `json:"scope"`
	Hash           string `json:"hash"`
	Entry          string `json:"entry"`
	Reason         string `json:"reason"`
	Replaced       int    `json:"replaced"`
	Entries        int    `json:"entries"`
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
	TokensBefore   int64  `json:"tokens_before"`
	TokensAfter    int64  `json:"tokens_after"`
}

func sendWait(t *testing.T, ctx context.Context, s *thread.Session, text string) *thread.Turn {
	t.Helper()
	turn, err := s.Send(ctx, core.User(text))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return turn
}

// A compaction of a context no run produced (entries appended by
// hand): the marker waits for the next run this Session drives and is
// emitted under it, after its run_start; the turn after it carries
// none.
func TestCompactionMarkerManual(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		lp := &markerLogs{}
		rec := &summaryRecorder{reply: "the summary text"}
		s, _ := thread.Create(ctx, st, core.New(rec))
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		s = reopenWith(t, ctx, st, s, core.New(rec, core.Name("support"), core.LoggerProvider(lp)))
		before := s.Context()

		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		after := s.Context()
		if n := len(lp.kind("compaction")); n != 0 {
			t.Fatalf("markers before any run = %d, want 0 (no run produced the context; the next run reports it)", n)
		}
		var entry thread.CompactionEntry
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok {
				entry = c
			}
		}

		first := sendWait(t, ctx, s, "next question")
		sendWait(t, ctx, s, "and another")

		ms := lp.kind("compaction")
		if len(ms) != 1 {
			t.Fatalf("markers = %d, want 1", len(ms))
		}
		m := ms[0]
		if m.eventName != "weft.compaction" || m.scope != "github.com/weftgo/weft/thread" {
			t.Errorf("marker event %q scope %q", m.eventName, m.scope)
		}
		if m.attrs["weft.run.id"] != first.RunID() {
			t.Errorf("marker run = %q, want the first run after the compaction, %q", m.attrs["weft.run.id"], first.RunID())
		}
		b, _ := json.Marshal(entry)
		sum := sha256.Sum256(b)
		wantHash := hex.EncodeToString(sum[:])
		for k, want := range map[string]string{
			"weft.compaction.scope": "session",
			"weft.compaction.hash":  wantHash,
			"weft.content":          "none",
			"weft.session.id":       s.ID(),
			"gen_ai.agent.name":     "support",
		} {
			if m.attrs[k] != want {
				t.Errorf("marker %s = %q, want %q", k, m.attrs[k], want)
			}
		}
		for _, k := range []string{"weft.messages.index", "weft.messages.reason", "weft.messages.from_seq"} {
			if _, ok := m.attrs[k]; ok {
				t.Errorf("the session marker carries %s", k)
			}
		}
		var body markerBody
		if err := json.Unmarshal([]byte(m.body), &body); err != nil {
			t.Fatal(err)
		}
		// The context went from a, b, c to summary, b, c: one message
		// replaced by one summary entry.
		if body.Scope != "session" || body.Hash != wantHash || body.Entry != entry.ID || body.Reason != "manual" ||
			body.MessagesBefore != len(before) || body.MessagesAfter != len(after) ||
			body.Replaced != 1 || body.Entries != 1 ||
			body.TokensBefore != entry.TokensBefore || body.TokensAfter <= 0 || body.TokensAfter >= body.TokensBefore {
			t.Errorf("marker body = %+v (before %d, after %d, entry %+v)", body, len(before), len(after), entry.TokensBefore)
		}
		if strings.Contains(m.body, "the summary text") || strings.Contains(m.body, "bbbb") {
			t.Error("the session marker carries messages")
		}

		// The run's own run_start (core's) went out before the marker:
		// the marker never names a run that did not start.
		lp.mu.Lock()
		defer lp.mu.Unlock()
		sawStart := false
		for _, r := range lp.recs {
			if r.attrs["weft.run.id"] == first.RunID() && r.attrs["weft.event.type"] == "run_start" {
				sawStart = true
			}
			if r.attrs["weft.record"] == "compaction" && !sawStart {
				t.Error("the marker preceded its run's run_start")
			}
		}
	})
}

// The overflow re-run (ADR 0020 §5) compacts and runs again under a
// fresh id: one marker, reason overflow.
func TestCompactionMarkerOverflowReRun(t *testing.T) {
	ctx := context.Background()
	lp := &markerLogs{}
	model := wefttest.Script(
		wefttest.Say("the first answer"),
		wefttest.Fail(core.ErrContextOverflow),
		wefttest.Say("the summary of what came before"),
		wefttest.Say("recovered after compaction"),
	)
	s, err := thread.Create(ctx, thread.Memory(), core.New(model, core.LoggerProvider(lp)), thread.KeepRecent(1))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sendWait(t, ctx, s, "a first question")
	t1 := sendWait(t, ctx, s, "a prompt that overflows")
	ms := lp.kind("compaction")
	if len(ms) != 1 {
		t.Fatalf("markers = %d, want 1", len(ms))
	}
	// Emitted when the compaction landed, under the run that produced
	// the compacted context: the first turn's (-t1). The overflowed
	// attempt (-t2) produced nothing of it — only its prompt is on the
	// path, with no turn entry yet; the re-run is -t3.
	if ms[0].attrs["weft.run.id"] != s.ID()+"-t1" || t1.RunID() != s.ID()+"-t3" {
		t.Errorf("marker run = %q, want the first turn %s-t1 (re-run %q)", ms[0].attrs["weft.run.id"], s.ID(), t1.RunID())
	}
	var body markerBody
	if err := json.Unmarshal([]byte(ms[0].body), &body); err != nil || body.Reason != "overflow" || body.Scope != "session" {
		t.Errorf("marker body = %s (%v), want reason overflow", ms[0].body, err)
	}
}

// Compactions after real turns: each emits its marker when it lands —
// before any further run, and before Close — under the last run that
// produced the compacted context, on that run's invoke_agent span and
// with its merged metadata (the caller's thread.RunOptions metadata
// included). Two compactions, two markers.
func TestCompactionMarkerAtCompactionTime(t *testing.T) {
	ctx := context.Background()
	lp := &markerLogs{}
	model := wefttest.Script(
		wefttest.Say(strings.Repeat("a", 4000)), wefttest.Say(strings.Repeat("b", 4000)),
		wefttest.Say("summary one"), wefttest.Say(strings.Repeat("c", 4000)), wefttest.Say("summary two"))
	agent := core.New(model, core.LoggerProvider(lp), core.TracerProvider(sdktrace.NewTracerProvider()))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	caller := thread.RunOptions(core.Metadata(map[string]string{"tenant": "acme"}))
	send := func(text string) *thread.Turn {
		turn, err := s.Send(ctx, core.User(text), caller)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		return turn
	}
	send("q1")
	t2 := send("q2")
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	ms := lp.kind("compaction")
	if len(ms) != 1 {
		t.Fatalf("markers right after the compaction = %d, want 1", len(ms))
	}
	m := ms[0]
	if m.attrs["weft.run.id"] != t2.RunID() || m.attrs["tenant"] != "acme" || m.attrs["weft.session.id"] != s.ID() || m.attrs["weft.turn"] != "2" {
		t.Errorf("marker attrs = %v, want under %s with the run's metadata", m.attrs, t2.RunID())
	}
	var runSpan trace.SpanContext
	for _, r := range lp.kind("event") {
		if r.attrs["weft.run.id"] == t2.RunID() && r.attrs["weft.event.type"] == "run_start" {
			runSpan = r.span
		}
	}
	if !runSpan.IsValid() || m.span.SpanID() != runSpan.SpanID() || m.span.TraceID() != runSpan.TraceID() {
		t.Errorf("marker span = %v, want the run's invoke_agent span %v", m.span, runSpan)
	}
	send("q3")
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	ms = lp.kind("compaction")
	if len(ms) != 2 || ms[0].attrs["weft.compaction.hash"] == ms[1].attrs["weft.compaction.hash"] {
		t.Fatalf("markers = %d, want 2 with distinct hashes", len(ms))
	}
	if ms[1].attrs["weft.run.id"] != s.ID()+"-t3" {
		t.Errorf("second marker run = %q, want %s-t3", ms[1].attrs["weft.run.id"], s.ID())
	}
}

// A compaction of a context no run produced, then Close with no run:
// no run id to file the marker under — dropped, with a Debug line.
func TestCompactionMarkerDroppedAtCloseWithoutARun(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	lp := &markerLogs{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rec := &summaryRecorder{reply: "the summary text"}
	s, _ := thread.Create(ctx, st, core.New(rec))
	msgs(t, ctx, st, s, strings.Repeat("a", 30_000), strings.Repeat("b", 30_000), strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, core.New(rec, core.LoggerProvider(lp), core.Logger(logger)))
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(lp.kind("compaction")); n != 0 {
		t.Errorf("markers = %d, want 0", n)
	}
	if !strings.Contains(logs.String(), "compaction markers dropped at close") {
		t.Errorf("no Debug line for the dropped marker:\n%s", logs.String())
	}
}

// The pre-run threshold trigger (ADR 0020 §2) fires after turn 2's
// prompt is appended: its marker is filed under turn 1, the run that
// produced the compacted context — on turn 1's span, with its
// metadata (weft.turn 1, the caller's pairs) — and emitted before turn
// 2's run_start. Never under turn 2, which had produced nothing yet.
func TestCompactionMarkerThresholdNamesTheProducingRun(t *testing.T) {
	ctx := context.Background()
	lp := &markerLogs{}
	st := thread.Memory()
	// Turn 1 reports 80k input: under the line (100k − 16,384 reserve)
	// after it, over it once turn 2's ~7.5k-token prompt joins.
	model := wefttest.Script(
		wefttest.Say("turn one").WithUsage(core.Usage{InputTokens: 80_000, OutputTokens: 5}),
		wefttest.Say("the summary"),
		wefttest.Say("turn two"),
	)
	agent := core.New(model, core.LoggerProvider(lp), core.TracerProvider(sdktrace.NewTracerProvider()))
	s, err := thread.Create(ctx, st, core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	msgs(t, ctx, st, s, strings.Repeat("a", 30_000), strings.Repeat("b", 30_000), strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, thread.ContextWindow(100_000))
	caller := thread.RunOptions(core.Metadata(map[string]string{"tenant": "acme"}))
	t1, err := s.Send(ctx, core.User("first"), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = s.WaitIdle(ctx)
	if n := hasCompaction(s); n != 0 {
		t.Fatalf("compactions after turn 1 = %d, want 0 (under the line)", n)
	}
	t2, err := s.Send(ctx, core.User(strings.Repeat("d", 30_000)), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	var reason thread.Reason
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			reason = c.Reason
		}
	}
	if reason != thread.ReasonThreshold {
		t.Fatalf("compaction reason = %q, want threshold before turn 2", reason)
	}
	ms := lp.kind("compaction")
	if len(ms) != 1 {
		t.Fatalf("markers = %d, want 1", len(ms))
	}
	m := ms[0]
	if m.attrs["weft.run.id"] != t1.RunID() || m.attrs["weft.turn"] != "1" || m.attrs["tenant"] != "acme" {
		t.Errorf("marker attrs = %v, want under turn 1 (%s) with its metadata", m.attrs, t1.RunID())
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	var t1Span trace.SpanContext
	markerAt, t2StartAt := -1, -1
	for i, r := range lp.recs {
		switch {
		case r.attrs["weft.run.id"] == t1.RunID() && r.attrs["weft.event.type"] == "run_start":
			t1Span = r.span
		case r.attrs["weft.run.id"] == t2.RunID() && r.attrs["weft.event.type"] == "run_start":
			t2StartAt = i
		case r.attrs["weft.record"] == "compaction":
			markerAt = i
		}
	}
	if !t1Span.IsValid() || m.span.SpanID() != t1Span.SpanID() {
		t.Errorf("marker span = %v, want turn 1's invoke_agent span %v", m.span, t1Span)
	}
	if markerAt < 0 || t2StartAt < 0 || markerAt > t2StartAt {
		t.Errorf("marker at %d, turn 2's run_start at %d: want the marker first", markerAt, t2StartAt)
	}
}
