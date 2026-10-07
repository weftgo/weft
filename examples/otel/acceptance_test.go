package main

// The S1.3 acceptance through the real SDK (ADR 0024 step 2): an
// in-memory sdk/log exporter beside the in-memory span exporter — the
// SDK is a dependency of this example module only, never of the root —
// proves the core's record emission against the SDK a user actually
// registers: the global-delegation path, the trace/span correlation the
// ctx carries, and the two counters.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// memLogExporter is the logs-side tracetest: every exported record,
// cloned (the SDK hands the same slice it may reuse).
type memLogExporter struct {
	records []sdklog.Record
}

func (e *memLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, r := range records {
		e.records = append(e.records, r.Clone())
	}
	return nil
}

func (e *memLogExporter) Shutdown(context.Context) error   { return nil }
func (e *memLogExporter) ForceFlush(context.Context) error { return nil }

func (e *memLogExporter) attr(r sdklog.Record, key string) (attribute.Value, bool) {
	var out attribute.Value
	found := false
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == key {
			out, found = kv.Value, true
			return false
		}
		return true
	})
	return out, found
}

func acceptanceEcho() *weft.ToolDef {
	return weft.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
}

func TestRecordsThroughRealSDK(t *testing.T) {
	spanExp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	logExp := &memLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExp)))
	defer func() { _ = lp.Shutdown(context.Background()) }()

	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`, ID: "c1"}),
		wefttest.Say("done"),
	), weft.Name("sdk-demo"), acceptanceEcho(),
		weft.TracerProvider(tp), weft.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), weft.Prompt("hello"),
		weft.Metadata(map[string]string{"tenant": "acme", "weft.session.id": "s_sdk"}))
	if err != nil {
		t.Fatal(err)
	}

	// The span side: the run span exists and names its trace.
	spans := spanExp.GetSpans()
	var runTrace string
	for _, s := range spans {
		if s.Name == "invoke_agent sdk-demo" {
			runTrace = s.SpanContext.TraceID().String()
		}
	}
	if runTrace == "" {
		t.Fatalf("no invoke_agent span; have %v", spanNames(spans))
	}

	// Every record correlates to that trace — the ctx the core emitted
	// on carried the run's span, which is the whole join.
	if len(logExp.records) == 0 {
		t.Fatal("no records reached the log exporter")
	}
	var events, deltas, messages, requests, tools int
	chatSpans := map[string]bool{}
	for _, s := range spans {
		if strings.HasPrefix(s.Name, "chat") {
			chatSpans[s.SpanContext.SpanID().String()] = true
		}
	}
	var lastEventPos = -1
	var transcript []weft.Message
	for _, r := range logExp.records {
		if got := r.TraceID().String(); got != runTrace {
			t.Errorf("record %s trace = %s, want the run's %s", r.EventName(), got, runTrace)
		}
		kind, _ := logExp.attr(r, "weft.record")
		switch kind.AsString() {
		case "event":
			events++
			pos, _ := logExp.attr(r, "weft.event.pos")
			if int(pos.AsInt64()) != lastEventPos+1 {
				t.Errorf("event pos = %d, want %d (contiguous)", pos.AsInt64(), lastEventPos+1)
			}
			lastEventPos = int(pos.AsInt64())
		case "delta":
			deltas++
		case "messages":
			messages++
			var batch []weft.Message
			if err := json.Unmarshal([]byte(r.Body().AsString()), &batch); err != nil {
				t.Fatalf("messages record body does not decode: %v", err)
			}
			transcript = append(transcript, batch...)
		case "request", "tools":
			// ADR 0028: emitted on the chat span's context, so they join
			// the model call by span id.
			if kind.AsString() == "request" {
				requests++
			} else {
				tools++
			}
			if !chatSpans[r.SpanID().String()] {
				t.Errorf("%s record span %s is not a chat span", kind.AsString(), r.SpanID())
			}
		default:
			t.Errorf("record kind = %q", kind.AsString())
		}
		if v, ok := logExp.attr(r, "weft.run.id"); !ok || v.AsString() != res.ID {
			t.Errorf("record %s weft.run.id missing or wrong", r.EventName())
		}
		if v, _ := logExp.attr(r, "tenant"); v.AsString() != "acme" {
			t.Errorf("record %s: metadata tenant = %q, want acme", r.EventName(), v.AsString())
		}
	}
	if events < 8 { // run_start..run_finish of a two-step run, tools included
		t.Errorf("%d event records, want at least 8", events)
	}
	if deltas != 1 {
		t.Errorf("%d delta records, want 1 (Say's text)", deltas)
	}
	if requests != 2 || tools != 1 {
		t.Errorf("%d request and %d tools records, want 2 and 1 (no instructions: no prompt record)", requests, tools)
	}
	// The messages records rebuild the transcript byte-for-byte.
	if len(transcript) != len(res.Messages) {
		t.Fatalf("records rebuild %d messages, transcript has %d", len(transcript), len(res.Messages))
	}
	for i := range transcript {
		gb, _ := json.Marshal(transcript[i])
		wb, _ := json.Marshal(res.Messages[i])
		if !slices.Equal(gb, wb) {
			t.Errorf("message %d: records have %s, transcript has %s", i, gb, wb)
		}
	}

	// The run_start record names the provenance.
	for _, r := range logExp.records {
		if r.EventName() != "weft.event" {
			continue
		}
		typ, _ := logExp.attr(r, "weft.event.type")
		if typ.AsString() != "run_start" {
			continue
		}
		if v, ok := logExp.attr(r, "weft.manifest.hash"); !ok || len(v.AsString()) != 64 {
			t.Error("run_start record: no manifest hash")
		}
		if v, ok := logExp.attr(r, "weft.version"); !ok || v.AsString() == "" {
			t.Error("run_start record: no weft.version")
		}
		break
	}

	// The span side carries the metadata and mirrors too — the identity
	// chain is on both signals.
	found := map[string]bool{}
	for _, s := range spans {
		if s.Name != "invoke_agent sdk-demo" {
			continue
		}
		for _, kv := range s.Attributes {
			found[string(kv.Key)] = true
		}
	}
	for _, key := range []string{"tenant", "weft.session.id", "gen_ai.conversation.id", "session.id", "weft.manifest.hash", "weft.version"} {
		if !found[key] {
			t.Errorf("run span missing %s", key)
		}
	}
}

// spanNames is main_test.go's helper, reused.
var _ = spanNames
