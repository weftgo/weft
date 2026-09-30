package weft_test

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

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
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
func boomTool() *weft.ToolDef {
	return weft.Tool("boom", "Always fails.", func(ctx context.Context, in struct{}) (string, error) {
		return "", &weft.ToolError{Code: "BOOM", Message: "it blew up"}
	})
}

// S1.3: every durable event produces one event record, every delta one
// delta record, positions contiguous from 0, run_start first and
// run_finish last; bodies are the events' wire JSON; the common and
// per-type attributes are exactly the contract's.
func TestEventAndDeltaRecords(t *testing.T) {
	lp := newRecLogProvider()
	echo := weft.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`, ID: "c_e"},
			wefttest.Call{Name: "boom", ID: "c_b"},
		),
		wefttest.Say("done"),
	), weft.Name("recorder-test"), echo, boomTool(),
		weft.LoggerProvider(lp))

	res, err := agt.Generate(context.Background(), weft.Prompt("hello"),
		weft.Metadata(map[string]string{"tenant": "acme", "weft.session.id": "s_1", "enduser.id": "u_9"}))
	if err != nil {
		t.Fatal(err)
	}

	// Logger identity: weft's instrumentation name and the core version.
	if len(lp.loggers) == 0 {
		t.Fatal("no logger requested from the provider")
	}
	if lp.loggers[0].name != "github.com/weftgo/weft" {
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

	// Bodies are the events' wire JSON, content full (capture is on: the
	// provider answers Enabled true). Every record decodes back to the
	// event its weft.event.type names.
	for _, r := range events_rec {
		ev, err := weft.UnmarshalEvent([]byte(r.body))
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

func wireType(ev weft.Event) string {
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
	echo := weft.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`, ID: "c1"}),
		wefttest.Say("done"),
	), echo, weft.LoggerProvider(lp), weft.Content(false))
	if _, err := agt.Generate(context.Background(), weft.Prompt("hello")); err != nil {
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
	child := weft.New(wefttest.Script(wefttest.Say("child done")),
		weft.Name("child"), weft.LoggerProvider(lp))
	delegate := weft.Subagent("research", "Do the research.", child)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"x"}`, ID: "c_r"}),
		wefttest.Say("final"),
	), weft.Name("parent"), delegate, weft.LoggerProvider(lp))
	res, err := parent.Generate(context.Background(), weft.Prompt("go"))
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
	echo := weft.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", ID: "c1"}),
		wefttest.Say("done"),
	), echo, weft.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), weft.Prompt("hello")); err != nil {
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
	echo := weft.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", ID: "c1"}),
		wefttest.Say("done"),
	), echo, weft.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), weft.Prompt("hi")); err != nil {
		return
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	for _, r := range lp.records {
		pos, ok := r.intAttr("weft.event.pos")
		if !ok {
			pos, _ = r.intAttr("weft.delta.pos")
		}
		fmt.Printf("%s %s pos=%d\n", r.attr("weft.record"), r.attr("weft.event.type"), pos)
	}
	// Output:
	// event run_start pos=0
	// event step_start pos=1
	// event tool_start pos=2
	// event tool_finish pos=3
	// event step_finish pos=4
	// event step_start pos=5
	// delta text_delta pos=0
	// event step_finish pos=6
	// event run_finish pos=7
}

// ExampleContent pins the agent-level veto: with Content(false) the
// records keep their shape and positions but their bodies carry no
// content, whatever a destination asks.
func ExampleContent() {
	lp := newRecLogProvider()
	echo := weft.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "secret value", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", ID: "c1"}),
		wefttest.Say("done"),
	), echo, weft.LoggerProvider(lp), weft.Content(false))
	if _, err := agt.Generate(context.Background(), weft.Prompt("hi"), weft.RunID("r")); err != nil {
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
