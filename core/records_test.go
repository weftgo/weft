package core_test

// The record tests, on the S1.3 contract (ADR 0024): every durable event
// and every delta reported from deliver as an OTel log record, two
// position counters, Nested not reported, attributes pinned by name.
// The Logs API provider here is implemented in this file on the API's
// embedded types — the same stance otel_test.go takes for the tracer:
// the SDK never enters the root module's go.mod, and the real SDK is
// proven in examples/otel.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// recLogProvider records every record emitted through it and every
// logger requested from it (name and instrumentation version pinned).
type recLogProvider struct {
	embedded.LoggerProvider

	mu      sync.Mutex
	records []recLogRecord
	loggers []recLoggerInfo
	enabled func(eventName string) bool // nil: everything enabled
}

type recLoggerInfo struct {
	name    string
	version string
}

type recLogRecord struct {
	eventName string
	severity  log.Severity
	body      string
	attrs     []attribute.KeyValue
	hasTime   bool
}

func newRecLogProvider() *recLogProvider { return &recLogProvider{} }

func (p *recLogProvider) Logger(name string, opts ...log.LoggerOption) log.Logger {
	cfg := log.NewLoggerConfig(opts...)
	p.mu.Lock()
	p.loggers = append(p.loggers, recLoggerInfo{name: name, version: cfg.InstrumentationVersion()})
	p.mu.Unlock()
	return &recLogLogger{p: p}
}

type recLogLogger struct {
	embedded.Logger
	p *recLogProvider
}

func (l *recLogLogger) Emit(_ context.Context, r log.Record) {
	rec := recLogRecord{
		eventName: r.EventName(),
		severity:  r.Severity(),
		body:      r.Body().AsString(),
		hasTime:   !r.Timestamp().IsZero(),
	}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		rec.attrs = append(rec.attrs, kv)
		return true
	})
	l.p.mu.Lock()
	l.p.records = append(l.p.records, rec)
	l.p.mu.Unlock()
}

func (l *recLogLogger) Enabled(_ context.Context, param log.EnabledParameters) bool {
	if l.p.enabled != nil {
		return l.p.enabled(param.EventName)
	}
	return true
}

// ofKind returns the records whose weft.record attribute is kind, in
// emission order.
func (p *recLogProvider) ofKind(t *testing.T, kind string) []recLogRecord {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []recLogRecord
	for _, r := range p.records {
		if r.attr("weft.record") == kind {
			out = append(out, r)
		}
	}
	return out
}

// attr returns the record's string attribute value, "" when absent.
func (r recLogRecord) attr(key string) string {
	for _, kv := range r.attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

// intAttr returns the record's integer attribute value, ok false when
// absent or not an integer.
func (r recLogRecord) intAttr(key string) (int64, bool) {
	for _, kv := range r.attrs {
		if string(kv.Key) == key && kv.Value.Type() == attribute.INT64 {
			return kv.Value.AsInt64(), true
		}
	}
	return 0, false
}

// hasAttr reports whether the key is present at all.
func (r recLogRecord) hasAttr(key string) bool {
	for _, kv := range r.attrs {
		if string(kv.Key) == key {
			return true
		}
	}
	return false
}

// lookupTool echoes; boomTool fails with a ToolError, so its tool_finish
// record carries WARN.
func boomTool() *core.ToolDef {
	return core.Tool("boom", "Always fails.", func(ctx context.Context, in struct{}) (string, error) {
		return "", &core.ToolError{Code: "BOOM", Message: "it blew up"}
	})
}

// S1.3: every durable event produces one event record, every delta one
// delta record, positions contiguous from 0, run_start first and
// run_finish last; bodies are the events' wire JSON; the common and
// per-type attributes are exactly the contract's.
func TestEventAndDeltaRecords(t *testing.T) {
	lp := newRecLogProvider()
	echo := core.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`, ID: "c_e"},
			wefttest.Call{Name: "boom", ID: "c_b"},
		),
		wefttest.Say("done"),
	), core.Name("recorder-test"), echo, boomTool(),
		core.LoggerProvider(lp))

	res, err := agt.Generate(context.Background(), core.Prompt("hello"),
		core.Metadata(map[string]string{"tenant": "acme", "weft.session.id": "s_1", "enduser.id": "u_9"}))
	if err != nil {
		t.Fatal(err)
	}

	// Logger identity: weft's instrumentation name and the core version.
	if len(lp.loggers) == 0 {
		t.Fatal("no logger requested from the provider")
	}
	if lp.loggers[0].name != "github.com/weftgo/weft/core" {
		t.Errorf("logger name = %q, want github.com/weftgo/weft", lp.loggers[0].name)
	}
	if lp.loggers[0].version == "" {
		t.Error("logger instrumentation version is empty, want the core version")
	}

	// The durable sequence, in emission order with contiguous positions.
	events_rec := lp.ofKind(t, "event")
	// The two calls run in parallel: both starts precede either finish
	// (the ordering rule), the finishes in completion order.
	wantTypes := []string{"run_start", "step_start", "tool_start", "tool_start",
		"tool_finish", "tool_finish", "step_finish", "step_start", "step_finish", "run_finish"}
	if len(events_rec) != len(wantTypes) {
		t.Fatalf("got %d event records (%v), want %d", len(events_rec), typesOf(events_rec), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got := events_rec[i].attr("weft.event.type"); got != want {
			t.Errorf("event record %d type = %q, want %q", i, got, want)
		}
		if pos, ok := events_rec[i].intAttr("weft.event.pos"); !ok || pos != int64(i) {
			t.Errorf("event record %d pos = %d (ok=%v), want %d", i, pos, ok, i)
		}
		if events_rec[i].eventName != "weft.event" {
			t.Errorf("event record %d EventName = %q, want weft.event", i, events_rec[i].eventName)
		}
		wantSev := log.SeverityInfo
		if want == "tool_finish" && events_rec[i].attr("gen_ai.tool.name") == "boom" {
			wantSev = log.SeverityWarn // a failing tool's finish carries WARN
		}
		if events_rec[i].severity != wantSev {
			t.Errorf("event record %d severity = %v, want %v", i, events_rec[i].severity, wantSev)
		}
	}

	// Deltas: one text delta from Say, its own counter, contiguous.
	deltas := lp.ofKind(t, "delta")
	if len(deltas) != 1 {
		t.Fatalf("got %d delta records, want 1", len(deltas))
	}
	if deltas[0].attr("weft.event.type") != "text_delta" {
		t.Errorf("delta type = %q, want text_delta", deltas[0].attr("weft.event.type"))
	}
	if pos, _ := deltas[0].intAttr("weft.delta.pos"); pos != 0 {
		t.Errorf("delta pos = %d, want 0", pos)
	}
	if deltas[0].hasAttr("weft.event.pos") {
		t.Error("delta record carries weft.event.pos; the counters are separate [D3]")
	}
	if deltas[0].eventName != "weft.delta" {
		t.Errorf("delta record EventName = %q, want weft.delta", deltas[0].eventName)
	}

	// Bodies are the events' wire JSON, content full (capture is on: the
	// provider answers Enabled true). Every record decodes back to the
	// event its weft.event.type names.
	for _, r := range events_rec {
		ev, err := core.UnmarshalEvent([]byte(r.body))
		if err != nil {
			t.Fatalf("event record body does not decode: %v (%s)", err, r.body)
		}
		if got := wireType(ev); got != r.attr("weft.event.type") {
			t.Errorf("body type %q != weft.event.type %q", got, r.attr("weft.event.type"))
		}
	}
	if s := events_rec[0].attr("weft.content"); s != "full" {
		t.Errorf("weft.content = %q, want full", s)
	}

	// The common attributes on every record.
	for _, r := range append(append([]recLogRecord{}, events_rec...), deltas...) {
		if r.attr("weft.run.id") != res.ID {
			t.Errorf("record %s: weft.run.id = %q, want %q", r.attr("weft.event.type"), r.attr("weft.run.id"), res.ID)
		}
		if r.attr("gen_ai.agent.name") != "recorder-test" {
			t.Errorf("record %s: gen_ai.agent.name missing", r.attr("weft.event.type"))
		}
		for k, want := range map[string]string{"tenant": "acme", "weft.session.id": "s_1", "enduser.id": "u_9"} {
			if r.attr(k) != want {
				t.Errorf("record %s: metadata %s = %q, want %q", r.attr("weft.event.type"), k, r.attr(k), want)
			}
		}
		if !r.hasTime {
			t.Errorf("record %s: no timestamp; the record gives events the time they never carried", r.attr("weft.event.type"))
		}
	}

	// Per-type extras: step indexes, tool identity.
	var stepStarts, stepFinishes, toolStarts, toolFinishes []recLogRecord
	for _, r := range events_rec {
		switch r.attr("weft.event.type") {
		case "step_start":
			stepStarts = append(stepStarts, r)
		case "step_finish":
			stepFinishes = append(stepFinishes, r)
		case "tool_start":
			toolStarts = append(toolStarts, r)
		case "tool_finish":
			toolFinishes = append(toolFinishes, r)
		}
	}
	for i, r := range stepStarts {
		if pos, _ := r.intAttr("weft.step.index"); pos != int64(i) {
			t.Errorf("step_start %d weft.step.index = %d, want %d", i, pos, i)
		}
	}
	for i, r := range stepFinishes {
		if pos, _ := r.intAttr("weft.step.index"); pos != int64(i) {
			t.Errorf("step_finish %d weft.step.index = %d, want %d", i, pos, i)
		}
	}
	toolCases := []struct {
		recs []recLogRecord
		name string
		id   string
	}{{toolStarts, "echo", "c_e"}, {toolStarts, "boom", "c_b"}, {toolFinishes, "echo", "c_e"}, {toolFinishes, "boom", "c_b"}}
	for _, tc := range toolCases {
		found := false
		for _, r := range tc.recs {
			if r.attr("gen_ai.tool.name") == tc.name && r.attr("gen_ai.tool.call.id") == tc.id {
				found = true
				if _, ok := r.intAttr("weft.tool.seq"); !ok {
					t.Errorf("tool record %s/%s: no weft.tool.seq", tc.name, tc.id)
				}
			}
		}
		if !found {
			t.Errorf("no tool record for %s (%s)", tc.name, tc.id)
		}
	}
	// A failing tool_finish carries WARN.
	for _, r := range toolFinishes {
		want := log.SeverityInfo
		if r.attr("gen_ai.tool.name") == "boom" {
			want = log.SeverityWarn
		}
		if r.severity != want {
			t.Errorf("tool_finish %s severity = %v, want %v", r.attr("gen_ai.tool.name"), r.severity, want)
		}
	}

	// run_start extras: the run's identity. A top-level run has no
	// parent ids; the manifest hash is present for a named agent; the
	// version is pinned to the literal.
	rs := events_rec[0]
	if rs.hasAttr("weft.parent.run.id") || rs.hasAttr("weft.parent.call.id") {
		t.Error("top-level run_start carries parent ids")
	}
	if !rs.hasAttr("weft.manifest.hash") {
		t.Error("run_start carries no weft.manifest.hash for a named agent")
	}
	if rs.attr("weft.version") == "" {
		t.Error("run_start carries no weft.version")
	}
	// Only run_start carries them.
	if events_rec[1].hasAttr("weft.manifest.hash") {
		t.Error("a non-run_start record carries weft.manifest.hash")
	}
}

func typesOf(recs []recLogRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.attr("weft.event.type"))
	}
	return out
}

func wireType(ev core.Event) string {
	b, err := json.Marshal(ev)
	if err != nil {
		return ""
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return ""
	}
	return head.Type
}

// Content(false): bodies stripped, weft.content none — and no messages
// records at all (their absence is pinned once they exist).
func TestRecordsContentOff(t *testing.T) {
	lp := newRecLogProvider()
	echo := core.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`, ID: "c1"}),
		wefttest.Say("done"),
	), echo, core.LoggerProvider(lp), core.Content(false))
	if _, err := agt.Generate(context.Background(), core.Prompt("hello")); err != nil {
		t.Fatal(err)
	}
	for _, r := range lp.ofKind(t, "event") {
		if r.attr("weft.content") != "none" {
			t.Errorf("record %s: weft.content = %q, want none", r.attr("weft.event.type"), r.attr("weft.content"))
		}
		switch r.attr("weft.event.type") {
		case "tool_start":
			if want := `{"type":"tool_start","run_id":"` + r.attr("weft.run.id") + `","seq":1,"call_id":"c1","name":"echo","args":null}`; r.body != want {
				t.Errorf("stripped tool_start body = %s\n               want %s", r.body, want)
			}
		}
	}
	for _, r := range lp.ofKind(t, "delta") {
		if r.body != `{"type":"text_delta","run_id":"`+r.attr("weft.run.id")+`","text":""}` {
			t.Errorf("stripped delta body = %s", r.body)
		}
	}
	if msgs := lp.ofKind(t, "messages"); len(msgs) != 0 {
		t.Errorf("%d messages records with Content(false), want none", len(msgs))
	}
}

// Nested is not reported: a subagent's child run emits its own records,
// numbered from its own counters, with the parent linkage on its
// run_start. The parent's records never carry a nested event type.
func TestRecordsNestedNotReportedChildNumbersOwn(t *testing.T) {
	lp := newRecLogProvider()
	child := core.New(wefttest.Script(wefttest.Say("child done")),
		core.Name("child"), core.LoggerProvider(lp))
	delegate := core.Subagent("research", "Do the research.", child)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"x"}`, ID: "c_r"}),
		wefttest.Say("final"),
	), core.Name("parent"), delegate, core.LoggerProvider(lp))
	res, err := parent.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range lp.ofKind(t, "event") {
		if r.attr("weft.event.type") == "nested" {
			t.Error("a nested event was reported as a record; the child numbers its own [D3]")
		}
		if r.attr("gen_ai.agent.name") == "parent" {
			// The parent's durable sequence must stay contiguous: no
			// positions were consumed by the child's events.
			if r.attr("weft.event.type") == "run_finish" {
				var last int64
				for _, p := range lp.ofKind(t, "event") {
					if p.attr("gen_ai.agent.name") == "parent" {
						if pos, ok := p.intAttr("weft.event.pos"); ok {
							last = pos
						}
					}
				}
				if last == 0 {
					t.Error("parent's last event position is 0; positions were consumed by nested events")
				}
			}
		}
	}

	// The child's run_start carries the parent linkage and the child's
	// own contiguous sequence.
	lp.mu.Lock()
	all := append([]recLogRecord(nil), lp.records...)
	lp.mu.Unlock()
	var childRunStart *recLogRecord
	for i := range all {
		r := &all[i]
		if r.attr("gen_ai.agent.name") == "child" && r.attr("weft.event.type") == "run_start" {
			childRunStart = r
			break
		}
	}
	if childRunStart == nil {
		t.Fatal("child run emitted no run_start record")
	}
	if childRunStart.attr("weft.parent.run.id") != res.ID {
		t.Errorf("child run_start weft.parent.run.id = %q, want %q", childRunStart.attr("weft.parent.run.id"), res.ID)
	}
	if childRunStart.attr("weft.parent.call.id") != "c_r" {
		t.Errorf("child run_start weft.parent.call.id = %q, want c_r", childRunStart.attr("weft.parent.call.id"))
	}
	var childPos []int64
	for _, r := range lp.ofKind(t, "event") {
		if r.attr("gen_ai.agent.name") == "child" {
			if pos, ok := r.intAttr("weft.event.pos"); ok {
				childPos = append(childPos, pos)
			}
		}
	}
	for i, pos := range childPos {
		if pos != int64(i) {
			t.Errorf("child event position %d = %d, want contiguous from 0", i, pos)
			break
		}
	}
}

// A run whose logger is never enabled emits nothing: Enabled gates the
// whole path, so no marshalling happens a destination did not ask for.
func TestRecordsDisabledLoggerEmitsNothing(t *testing.T) {
	lp := newRecLogProvider()
	lp.enabled = func(string) bool { return false }
	echo := core.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", ID: "c1"}),
		wefttest.Say("done"),
	), echo, core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("hello")); err != nil {
		t.Fatal(err)
	}
	lp.mu.Lock()
	n := len(lp.records)
	lp.mu.Unlock()
	if n != 0 {
		t.Errorf("%d records emitted with Enabled answering false, want 0", n)
	}
}

// ExampleLoggerProvider prints the record sequence of a one-tool run,
// as a Logs API destination would receive it: every durable event and
// every delta, positions contiguous.
func ExampleLoggerProvider() {
	lp := newRecLogProvider()
	echo := core.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", ID: "c1"}),
		wefttest.Say("done"),
	), echo, core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		return
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	for _, r := range lp.records {
		switch r.attr("weft.record") {
		case "messages":
			idx, _ := r.intAttr("weft.messages.index")
			fmt.Printf("messages index=%d\n", idx)
			continue
		case "request":
			idx, _ := r.intAttr("weft.request.index")
			step, _ := r.intAttr("weft.step.index")
			fmt.Printf("request index=%d step=%d\n", idx, step)
			continue
		case "tools":
			idx, _ := r.intAttr("weft.tools.index")
			fmt.Printf("tools index=%d\n", idx)
			continue
		}
		pos, ok := r.intAttr("weft.event.pos")
		if !ok {
			pos, _ = r.intAttr("weft.delta.pos")
		}
		fmt.Printf("%s %s pos=%d\n", r.attr("weft.record"), r.attr("weft.event.type"), pos)
	}
	// Output:
	// event run_start pos=0
	// messages index=0
	// event step_start pos=1
	// tools index=0
	// request index=0 step=0
	// messages index=1
	// event tool_start pos=2
	// event tool_finish pos=3
	// messages index=2
	// event step_finish pos=4
	// event step_start pos=5
	// request index=1 step=1
	// delta text_delta pos=0
	// messages index=3
	// event step_finish pos=6
	// event run_finish pos=7
}

// ExampleContent pins the agent-level veto: with Content(false) the
// records keep their shape and positions but their bodies carry no
// content, whatever a destination asks.
func ExampleContent() {
	lp := newRecLogProvider()
	echo := core.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "secret value", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", ID: "c1"}),
		wefttest.Say("done"),
	), echo, core.LoggerProvider(lp), core.Content(false))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi"), core.RunID("r")); err != nil {
		return
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	for _, r := range lp.records {
		if r.attr("weft.event.type") == "tool_start" {
			fmt.Println(r.body)
		}
	}
	// Output:
	// {"type":"tool_start","run_id":"r","seq":1,"call_id":"c1","name":"echo","args":null}
}

// collectMessages concatenates one run's messages-record bodies, in
// weft.messages.index order, and checks the indexes are contiguous from
// 0 — the replay rule (S1.3). The provider may hold several runs'
// records; the run id picks this run's.
func collectMessages(t *testing.T, lp *recLogProvider, runID string) []core.Message {
	t.Helper()
	lp.mu.Lock()
	defer lp.mu.Unlock()
	var byIndex = map[int64]string{}
	for _, r := range lp.records {
		if r.attr("weft.record") != "messages" || r.attr("weft.run.id") != runID {
			continue
		}
		idx, ok := r.intAttr("weft.messages.index")
		if !ok {
			t.Fatal("messages record without weft.messages.index")
		}
		if _, dup := byIndex[idx]; dup {
			t.Fatalf("messages index %d recorded twice", idx)
		}
		byIndex[idx] = r.body
	}
	var msgs []core.Message
	for i := int64(0); i < int64(len(byIndex)); i++ {
		body, ok := byIndex[i]
		if !ok {
			t.Fatalf("messages indexes not contiguous: %d missing of %d", i, len(byIndex))
		}
		var batch []core.Message
		if err := json.Unmarshal([]byte(body), &batch); err != nil {
			t.Fatalf("messages body does not decode: %v", err)
		}
		msgs = append(msgs, batch...)
	}
	return msgs
}

// assertTranscriptEqual proves byte-for-byte equality between the
// concatenated messages records and the run's transcript: same length,
// same marshalled bytes per message (signatures included).
func assertTranscriptEqual(t *testing.T, lp *recLogProvider, res *core.RunResult) {
	t.Helper()
	got := collectMessages(t, lp, res.ID)
	if len(got) != len(res.Messages) {
		t.Fatalf("records rebuild %d messages, transcript has %d", len(got), len(res.Messages))
	}
	for i := range got {
		gb, err := json.Marshal(got[i])
		if err != nil {
			t.Fatal(err)
		}
		wb, err := json.Marshal(res.Messages[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(gb) != string(wb) {
			t.Errorf("message %d: records have %s, transcript has %s", i, gb, wb)
		}
	}
}

// msgEcho echoes its argument, so its result text is assertable.
func msgEcho() *core.ToolDef {
	return core.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
}

// S1.3 acceptance: for a fresh run the concatenation of the messages
// records equals RunResult.Messages byte-for-byte — the input record
// included, which today's OnMessages never sees.
func TestMessagesRecordsFreshRun(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`, ID: "c1"}),
		wefttest.Say("done"),
	), msgEcho(), core.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), core.Prompt("hello"), core.RunID("r"))
	if err != nil {
		t.Fatal(err)
	}
	assertTranscriptEqual(t, lp, res)

	// The input record's own attributes.
	lp.mu.Lock()
	defer lp.mu.Unlock()
	var input *recLogRecord
	for i := range lp.records {
		r := &lp.records[i]
		if r.attr("weft.record") == "messages" && r.hasAttr("weft.messages.input") {
			input = r
			break
		}
	}
	if input == nil {
		t.Fatal("no input messages record (weft.messages.input)")
	}
	if input.eventName != "weft.messages" {
		t.Errorf("messages record EventName = %q, want weft.messages", input.eventName)
	}
	if idx, _ := input.intAttr("weft.messages.index"); idx != 0 {
		t.Errorf("input record index = %d, want 0", idx)
	}
	if step, _ := input.intAttr("weft.step.index"); step != 0 {
		t.Errorf("input record step = %d, want 0", step)
	}
	if n, _ := input.intAttr("weft.messages.count"); n != 1 {
		t.Errorf("input record count = %d, want 1 (the prompt)", n)
	}
	if input.attr("weft.content") != "full" {
		t.Errorf("input record content = %q, want full", input.attr("weft.content"))
	}
}

// A Messages(...) continuation: the input record carries the whole fed
// transcript (repaired), and the concatenation still equals the final
// transcript byte-for-byte.
func TestMessagesRecordsContinuation(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(
		wefttest.Say("first"),
		wefttest.Say("second"),
	), core.LoggerProvider(lp))
	res1, err := agt.Generate(context.Background(), core.Prompt("one"))
	if err != nil {
		t.Fatal(err)
	}
	res2, err := agt.Generate(context.Background(),
		core.Messages(res1.Messages...), core.Prompt("two"))
	if err != nil {
		t.Fatal(err)
	}
	assertTranscriptEqual(t, lp, res2)
}

// A resume over an approval: the run parks, the transcript carries no
// tool message for the pending call, and the resuming run's records —
// input (with the dangling call) then the created tool message —
// concatenate to the resumed transcript byte-for-byte.
func TestMessagesRecordsResumeCreated(t *testing.T) {
	lp := newRecLogProvider()
	gated := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.Say("all set"),
	), gated, core.LoggerProvider(lp))
	res1, err := agt.Generate(context.Background(), core.Prompt("refund please"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Pending) != 1 {
		t.Fatalf("first run pending = %v, want one parked call", res1.Pending)
	}
	res2, err := agt.Generate(context.Background(),
		core.Messages(res1.Messages...), core.Approve("c1"))
	if err != nil {
		t.Fatal(err)
	}
	assertTranscriptEqual(t, lp, res2)

	// The second messages record is the tool message the resume created,
	// at step 0.
	lp.mu.Lock()
	defer lp.mu.Unlock()
	var toolBatch *recLogRecord
	for i := range lp.records {
		r := &lp.records[i]
		if r.attr("weft.record") == "messages" && r.attr("weft.run.id") == res2.ID && !r.hasAttr("weft.messages.input") {
			toolBatch = r
			break
		}
	}
	if toolBatch == nil {
		t.Fatal("no record for the tool message the resume created")
	}
	if step, _ := toolBatch.intAttr("weft.step.index"); step != 0 {
		t.Errorf("created tool message record step = %d, want 0", step)
	}
	var batch []core.Message
	if err := json.Unmarshal([]byte(toolBatch.body), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 || batch[0].Role != core.RoleTool {
		t.Fatalf("created-tool record body = %s, want one tool message", toolBatch.body)
	}
}

// A resume that rebuilds a partial tool message: the earlier run's
// transcript already carried the executed sibling's result, and the
// resume completes the message in place. attachResults now reports the
// rebuilt message (the D1 widening), so OnMessages sees it and a record
// carries it. The input record keeps the partial message as the run was
// fed it — the concatenation then holds both versions, which is the
// recorded cost of reporting the input before the resume resolves it;
// the rebuilt record is the authoritative shape.
func TestMessagesRecordsResumeRebuilt(t *testing.T) {
	lp := newRecLogProvider()
	gated := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "echo", Args: `{"msg":"x"}`, ID: "c_e"},
			wefttest.Call{Name: "refund", ID: "c_r"},
		),
		wefttest.Say("all set"),
	), msgEcho(), gated, core.LoggerProvider(lp))
	res1, err := agt.Generate(context.Background(), core.Prompt("refund please"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Pending) != 1 {
		t.Fatalf("first run pending = %v, want the refund parked", res1.Pending)
	}

	// OnMessages must see the rebuilt message (the widened attachResults).
	var joinedSteps []int
	res2, err := agt.Generate(context.Background(),
		core.Messages(res1.Messages...), core.Approve("c_r"),
		core.OnMessages(func(_ context.Context, step int, msgs []core.Message) {
			for _, m := range msgs {
				if m.Role == core.RoleTool {
					joinedSteps = append(joinedSteps, step)
				}
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one: the rebuilt message (the resume's own steps make no
	// tool message — Say requests no calls). Pre-widening this was zero:
	// a rebuilt message reported nothing.
	if len(joinedSteps) != 1 || joinedSteps[0] != 0 {
		t.Errorf("OnMessages tool batches = %v, want exactly the rebuilt message at step 0", joinedSteps)
	}

	// The rebuilt record carries both results in call order.
	lp.mu.Lock()
	defer lp.mu.Unlock()
	var rebuilt *recLogRecord
	for i := range lp.records {
		r := &lp.records[i]
		if r.attr("weft.record") == "messages" && r.attr("weft.run.id") == res2.ID && !r.hasAttr("weft.messages.input") {
			rebuilt = r // the first non-input batch of the resume
			break
		}
	}
	if rebuilt == nil {
		t.Fatal("no record for the rebuilt tool message")
	}
	var batch []core.Message
	if err := json.Unmarshal([]byte(rebuilt.body), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 || len(batch[0].Content) != 2 {
		t.Fatalf("rebuilt record = %s, want one tool message with both results", rebuilt.body)
	}
	if res2.Messages == nil {
		t.Error("no transcript")
	}
}

// A steered batch gets its messages record, at the step whose drain
// delivered it.
func TestMessagesRecordsSteered(t *testing.T) {
	lp := newRecLogProvider()
	steer := func(_ context.Context, at core.SteerPoint) []core.Message {
		if at.Step == 0 && at.Final {
			return []core.Message{core.User("and one more thing")}
		}
		return nil
	}
	agt := core.New(wefttest.Script(
		wefttest.Say("first"),
		wefttest.Say("second"),
	), core.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"), core.Steering(steer))
	if err != nil {
		t.Fatal(err)
	}
	assertTranscriptEqual(t, lp, res)

	lp.mu.Lock()
	defer lp.mu.Unlock()
	var steeredRec *recLogRecord
	for i := range lp.records {
		r := &lp.records[i]
		if r.attr("weft.record") == "messages" {
			var batch []core.Message
			if err := json.Unmarshal([]byte(r.body), &batch); err != nil {
				t.Fatal(err)
			}
			for _, m := range batch {
				if m.Role == core.RoleUser && m.Text() == "and one more thing" {
					steeredRec = r
				}
			}
		}
	}
	if steeredRec == nil {
		t.Fatal("no messages record for the steered batch")
	}
	if step, _ := steeredRec.intAttr("weft.step.index"); step != 0 {
		t.Errorf("steered record step = %d, want 0 (the drain step)", step)
	}
}

// A model can emit tool-call arguments that are not JSON (a cut-off
// object, stray text): the loop keeps the raw bytes in the transcript
// and answers the call with INVALID_INPUT. encoding/json refuses to
// embed an invalid RawMessage, so the records carrying those bytes — the
// tool_start event, the assistant's messages batch, every later run's
// input, a parked call's run_finish — used to be dropped whole, leaving
// a tool_finish with no start and a transcript that could not rebuild.
// They are recorded with the raw bytes as a JSON string instead:
// positions and indexes stay contiguous and every body decodes.
func TestRecordsSurviveToolArgsThatAreNotJSON(t *testing.T) {
	const raw = `{"msg": "hi` // cut off mid-string
	wantArgs, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: raw, ID: "c1"}),
		wefttest.Say("done"),
	), msgEcho(), core.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), core.Prompt("hello"), core.RunID("r"))
	if err != nil {
		t.Fatal(err)
	}

	events := lp.ofKind(t, "event")
	wantTypes := []string{"run_start", "step_start", "tool_start", "tool_finish",
		"step_finish", "step_start", "step_finish", "run_finish"}
	if len(events) != len(wantTypes) {
		t.Fatalf("got %d event records (%v), want %d (%v)", len(events), typesOf(events), len(wantTypes), wantTypes)
	}
	for i, want := range wantTypes {
		if got := events[i].attr("weft.event.type"); got != want {
			t.Errorf("event record %d type = %q, want %q", i, got, want)
		}
		if pos, ok := events[i].intAttr("weft.event.pos"); !ok || pos != int64(i) {
			t.Errorf("event record %d pos = %d (ok=%v), want %d", i, pos, ok, i)
		}
	}
	ev, err := core.UnmarshalEvent([]byte(events[2].body))
	if err != nil {
		t.Fatalf("tool_start body does not decode: %v", err)
	}
	if start, ok := ev.(core.ToolStart); !ok || string(start.Args) != string(wantArgs) {
		t.Errorf("tool_start body = %s, want args %s (the raw bytes as a JSON string)", events[2].body, wantArgs)
	}

	// The transcript rebuilds: every message is there, and the call
	// carries the raw bytes as a JSON string.
	got := collectMessages(t, lp, "r")
	if len(got) != len(res.Messages) {
		t.Fatalf("records rebuild %d messages, transcript has %d", len(got), len(res.Messages))
	}
	call, ok := got[1].Content[0].(core.ToolCallPart)
	if !ok || string(call.Args) != string(wantArgs) {
		t.Errorf("recorded assistant message = %+v, want the call's args %s", got[1], wantArgs)
	}

	// A continuation feeds that transcript back: its input record — the
	// whole fed transcript — is still index 0.
	lp2 := newRecLogProvider()
	agt2 := core.New(wefttest.Script(wefttest.Say("again")), core.LoggerProvider(lp2))
	res2, err := agt2.Generate(context.Background(),
		core.Messages(res.Messages...), core.Prompt("more"), core.RunID("r2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := collectMessages(t, lp2, "r2"); len(got) != len(res2.Messages) {
		t.Fatalf("continuation records rebuild %d messages, transcript has %d", len(got), len(res2.Messages))
	}
	msgs := lp2.ofKind(t, "messages")
	if len(msgs) == 0 || !msgs[0].hasAttr("weft.messages.input") {
		t.Fatalf("continuation has no input messages record first: %d messages records", len(msgs))
	}

	// A parked call's arguments ride run_finish's pending list: the
	// terminal record must not be lost either.
	lp3 := newRecLogProvider()
	guarded := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agt3 := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: raw, ID: "c9"}),
	), guarded, core.LoggerProvider(lp3))
	res3, err := agt3.Generate(context.Background(), core.Prompt("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Pending) != 1 {
		t.Fatalf("pending = %d, want the parked call", len(res3.Pending))
	}
	parked := lp3.ofKind(t, "event")
	if last := parked[len(parked)-1]; last.attr("weft.event.type") != "run_finish" {
		t.Fatalf("last event record = %q, want run_finish (%v)", last.attr("weft.event.type"), typesOf(parked))
	} else if _, err := core.UnmarshalEvent([]byte(last.body)); err != nil {
		t.Errorf("run_finish body does not decode: %v", err)
	}
}

// S1.3's cancellation rule covers the messages records too: nothing is
// recorded after the run's context ends. A run started on a dead context
// used to leave one orphan input record — no run_start beside it — and
// a run cancelled mid-step recorded the tool message of the cancelled
// batch after the event stream had stopped.
func TestNoRecordsAfterCancellation(t *testing.T) {
	t.Run("dead context", func(t *testing.T) {
		lp := newRecLogProvider()
		agt := core.New(wefttest.Script(wefttest.Say("never")), core.LoggerProvider(lp))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := agt.Generate(ctx, core.Prompt("hello")); err == nil {
			t.Fatal("a run on a dead context succeeded")
		}
		lp.mu.Lock()
		defer lp.mu.Unlock()
		if n := len(lp.records); n != 0 {
			t.Errorf("%d records emitted on a dead context, want 0 (first: %s %s)",
				n, lp.records[0].eventName, lp.records[0].body)
		}
	})
	t.Run("cancelled mid-step", func(t *testing.T) {
		lp := newRecLogProvider()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stop := core.Tool("stop", "Cancels the run.", func(context.Context, struct{}) (string, error) {
			cancel()
			return "stopped", nil
		})
		agt := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "stop", ID: "c1"}),
			wefttest.Say("never"),
		), stop, core.LoggerProvider(lp))
		if _, err := agt.Generate(ctx, core.Prompt("hello")); err == nil {
			t.Fatal("a cancelled run succeeded")
		}
		// Before the cancellation: the input and the assistant's call.
		// The tool message joined the transcript after it — not recorded.
		if msgs := lp.ofKind(t, "messages"); len(msgs) != 2 {
			t.Errorf("%d messages records, want 2 (input, assistant) — none after the cancellation", len(msgs))
		}
	})
}
