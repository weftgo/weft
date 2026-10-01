package otel

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// The golden test (S3.3): the SDK path and the OTLP path produce the
// same rows for the same data, against obsdb's checked-in fixtures.
// The fixtures live in obsdb/testdata so step 6's ingest (the receiver)
// and this module pin the identical bytes.

func loadLogsFixture(t *testing.T) []obsdb.Record {
	t.Helper()
	b, err := os.ReadFile("../obsdb/testdata/logs.pb")
	if err != nil {
		t.Fatal(err)
	}
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return obsdb.FromOTLPLogs(&req)
}

func loadTracesFixture(t *testing.T) []obsdb.Span {
	t.Helper()
	b, err := os.ReadFile("../obsdb/testdata/traces.pb")
	if err != nil {
		t.Fatal(err)
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return obsdb.FromOTLPTraces(&req)
}

// fixtureTime is the fixtures' clock: 2026-10-01T09:12:03.120Z + d.
func fixtureTime(d time.Duration) time.Time {
	return time.Unix(0, 1790845923120000000).UTC().Add(d)
}

// The fixture's resource block, as the SDK would hold it.
func fixtureResource(t *testing.T) *sdkresource.Resource {
	t.Helper()
	r, err := sdkresource.New(context.Background(),
		sdkresource.WithAttributes(
			semconv.ServiceName("acme-api"),
			attribute.String("service.version", "1.4.2"),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFromSDKRecordsMatchesOTLPFixture(t *testing.T) {
	want := loadLogsFixture(t)
	res := fixtureResource(t)

	// Emit the six fixture records through a real provider, so the SDK
	// itself stamps the resource and the ctx's trace/span ids — exactly
	// the records an exporter receives. Attribute order does not matter
	// (maps); scalar types must match the OTLP mapping exactly.
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: mustTraceID(t, "4bf92f3577b34da6a3ce929d0e0e4736"),
		SpanID:  mustSpanID(t, "00f067aa0ba902b7"),
	})
	emitCtx := trace.ContextWithSpanContext(context.Background(), sc)
	capture := &recordCapture{}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)),
	)
	defer func() { _ = provider.Shutdown(context.Background()) }()
	lg := provider.Logger("github.com/weftgo/weft")

	emit := func(ts, observed time.Time, severity int, eventName, body string, attrs []attribute.KeyValue, withSpan bool) {
		var r log.Record
		r.SetTimestamp(ts)
		r.SetObservedTimestamp(observed)
		r.SetSeverity(log.Severity(severity))
		r.SetEventName(eventName)
		r.SetBody(attribute.StringValue(body))
		r.AddAttributes(attrs...)
		ctx := context.Background()
		if withSpan {
			ctx = emitCtx
		}
		lg.Emit(ctx, r)
	}

	emit(fixtureTime(0), fixtureTime(1), 9, "weft.event",
		`{"type":"run_start","id":"r_20261001_1","model":{"provider":"wefttest","name":"script"},"agent":"demo"}`,
		[]attribute.KeyValue{
			attribute.String("weft.record", "event"),
			attribute.String("weft.run.id", "r_20261001_1"),
			attribute.String("weft.event.type", "run_start"),
			attribute.Int("weft.event.pos", 0),
			attribute.String("weft.content", "full"),
			attribute.String("gen_ai.agent.name", "demo"),
			attribute.String("weft.manifest.hash", "sha256:0f2c9a"),
			attribute.String("weft.version", "v0.6.0"),
			attribute.String("weft.session.id", "s_01M3"),
			attribute.String("gen_ai.conversation.id", "s_01M3"),
			attribute.String("session.id", "s_01M3"),
			attribute.Int("weft.turn", 3),
			attribute.String("weft.public_id", "pub_7Hk2"),
			attribute.String("tenant", "acme"),
			// The OTLP fixture also carries kvlist/bytes values
			// (weft.limit, weft.token), which the Go attribute API
			// cannot express — only another language's SDK sends those,
			// and obsdb's own test pins their mapping. The array value
			// is expressible and pinned here.
			attribute.StringSlice("weft.roles", []string{"support", "billing"}),
		}, true)
	emit(fixtureTime(1250*time.Millisecond), fixtureTime(1250*time.Millisecond+1), 9, "weft.delta",
		`{"type":"text_delta","run_id":"r_20261001_1","text":"Your order"}`,
		[]attribute.KeyValue{
			attribute.String("weft.record", "delta"),
			attribute.String("weft.run.id", "r_20261001_1"),
			attribute.String("weft.event.type", "text_delta"),
			attribute.Int("weft.delta.pos", 0),
			attribute.String("weft.content", "full"),
		}, true)
	emit(fixtureTime(4*time.Second), fixtureTime(4*time.Second+1), 9, "weft.messages",
		`[{"role":"user","content":[{"type":"text","text":"find order 42"}]}]`,
		[]attribute.KeyValue{
			attribute.String("weft.record", "messages"),
			attribute.String("weft.run.id", "r_20261001_1"),
			attribute.Int("weft.step.index", 0),
			attribute.Int("weft.messages.index", 0),
			attribute.Int("weft.messages.count", 1),
			attribute.Bool("weft.messages.input", true),
			attribute.String("weft.content", "full"),
		}, true)
	emit(fixtureTime(9410*time.Millisecond), fixtureTime(9410*time.Millisecond+1), 9, "weft.event",
		`{"type":"run_finish","run_id":"r_20261001_1","usage":{"input_tokens":1012,"output_tokens":209},"steps":2}`,
		[]attribute.KeyValue{
			attribute.String("weft.record", "event"),
			attribute.String("weft.run.id", "r_20261001_1"),
			attribute.String("weft.event.type", "run_finish"),
			attribute.Int("weft.event.pos", 3),
			attribute.String("weft.content", "full"),
		}, true)
	emit(fixtureTime(10*time.Second), fixtureTime(10*time.Second+1), 9, "weft.heartbeat",
		"",
		[]attribute.KeyValue{
			attribute.String("weft.record", "heartbeat"),
			attribute.String("weft.run.id", "r_20261001_1"),
			attribute.String("tenant", "acme"),
		}, false)
	emit(fixtureTime(12140*time.Millisecond), fixtureTime(12140*time.Millisecond+1), 17, "",
		"payment worker retrying after 502",
		[]attribute.KeyValue{attribute.String("worker", "payments")}, false)

	got := FromSDKRecords(capture.records)
	if len(got) != len(want) {
		t.Fatalf("SDK path produced %d records, OTLP fixture holds %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		// The fixture's kvlist/bytes attrs cannot be expressed through
		// the Go API; compare everything else strictly.
		w.Attrs = cloneWithout(w.Attrs, "weft.limit", "weft.token")
		if !reflect.DeepEqual(g, w) {
			t.Errorf("record %d:\n got %+v\nwant %+v", i, g, w)
		}
	}
}

// recordCapture is a log exporter that keeps what it was handed.
type recordCapture struct {
	records []sdklog.Record
}

func (c *recordCapture) Export(_ context.Context, recs []sdklog.Record) error {
	for _, r := range recs {
		c.records = append(c.records, r.Clone())
	}
	return nil
}

func (c *recordCapture) Shutdown(context.Context) error   { return nil }
func (c *recordCapture) ForceFlush(context.Context) error { return nil }

func cloneWithout(m map[string]any, keys ...string) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		skip := false
		for _, k2 := range keys {
			if k == k2 {
				skip = true
				break
			}
		}
		if !skip {
			out[k] = v
		}
	}
	return out
}

func TestFromSDKSpansMatchesOTLPFixture(t *testing.T) {
	want := loadTracesFixture(t)
	res := fixtureResource(t)
	traceID := mustTraceID(t, "4bf92f3577b34da6a3ce929d0e0e4736")
	span1 := mustSpanID(t, "00f067aa0ba902b7")
	span2 := mustSpanID(t, "b3ad0b6d9c2f4a08")
	span3 := mustSpanID(t, "e08a5f2b53bd1e3d")
	spans := []sdktrace.ReadOnlySpan{
		stubSpan(res, traceID, span1, trace.SpanID{}, "invoke_agent demo", 1,
			fixtureTime(0), fixtureTime(9410*time.Millisecond), 1, "",
			[]attribute.KeyValue{
				attribute.String("gen_ai.operation.name", "invoke_agent"),
				attribute.String("weft.run.id", "r_20261001_1"),
				attribute.String("gen_ai.agent.name", "demo"),
				attribute.String("gen_ai.provider.name", "wefttest"),
				attribute.String("gen_ai.request.model", "script"),
				attribute.String("weft.manifest.hash", "sha256:0f2c9a"),
				attribute.String("weft.version", "v0.6.0"),
				attribute.Int("gen_ai.usage.input_tokens", 1012),
				attribute.Int("gen_ai.usage.output_tokens", 209),
				attribute.Int("gen_ai.usage.cache_read.input_tokens", 120),
				attribute.Int("weft.run.steps", 2),
				attribute.String("weft.run.stop_reason", "end_turn"),
				attribute.String("weft.session.id", "s_01M3"),
				attribute.String("gen_ai.conversation.id", "s_01M3"),
				attribute.String("session.id", "s_01M3"),
				attribute.Int("weft.turn", 3),
				attribute.String("weft.public_id", "pub_7Hk2"),
				attribute.String("tenant", "acme"),
				attribute.String("enduser.id", "user_77"),
				attribute.String("user.id", "user_77"),
			}, nil),
		stubSpan(res, traceID, span2, span1, "chat script", 3,
			fixtureTime(10*time.Millisecond), fixtureTime(1250*time.Millisecond), 1, "",
			[]attribute.KeyValue{
				attribute.String("gen_ai.operation.name", "chat"),
				attribute.String("weft.run.id", "r_20261001_1"),
				attribute.Int("weft.step.index", 0),
				attribute.String("gen_ai.provider.name", "wefttest"),
				attribute.String("gen_ai.request.model", "script"),
			}, nil),
		stubSpan(res, traceID, span3, trace.SpanID{}, "GET /orders", 2,
			fixtureTime(0), fixtureTime(6*time.Second), 2, "upstream timed out",
			[]attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.path", "/orders"),
			},
			[]sdktrace.Event{{
				Name: "exception",
				Time: fixtureTime(5990 * time.Millisecond),
				Attributes: []attribute.KeyValue{
					attribute.String("exception.type", "timeout"),
					attribute.String("exception.message", "upstream timed out"),
				},
			}}),
	}
	got := FromSDKSpans(spans)
	if len(got) != len(want) {
		t.Fatalf("SDK path produced %d spans, OTLP fixture holds %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("span %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func stubSpan(res *sdkresource.Resource, traceID trace.TraceID, spanID, parent trace.SpanID, name string, kind int, start, end time.Time, statusCode int, statusMsg string, attrs []attribute.KeyValue, events []sdktrace.Event) sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{
		Name:        name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}),
		Parent:      trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: parent}),
		SpanKind:    trace.SpanKind(kind),
		StartTime:   start,
		EndTime:     end,
		Attributes:  attrs,
		Events:      events,
		Status:      sdktrace.Status{Code: otelcodes.Code(statusCode), Description: statusMsg},
		Resource:    res,
	}.Snapshot()
}

func mustTraceID(t *testing.T, s string) trace.TraceID {
	t.Helper()
	id, err := trace.TraceIDFromHex(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustSpanID(t *testing.T, s string) trace.SpanID {
	t.Helper()
	id, err := trace.SpanIDFromHex(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The fixtures' JSON encodings decode to the same rows as the .pb ones
// (the receiver accepts both); this rides the same fixtures so the JSON
// form stays pinned from this module too.
func TestFixturesJSONMatchesPB(t *testing.T) {
	var lj collogspb.ExportLogsServiceRequest
	lb, err := os.ReadFile("../obsdb/testdata/logs.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(lb, &lj); err != nil {
		t.Fatal(err)
	}
	if got, want := obsdb.FromOTLPLogs(&lj), loadLogsFixture(t); !reflect.DeepEqual(got, want) {
		t.Errorf("logs JSON != pb:\n got %+v\nwant %+v", got, want)
	}
	var tj coltracepb.ExportTraceServiceRequest
	tb, err := os.ReadFile("../obsdb/testdata/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(tb, &tj); err != nil {
		t.Fatal(err)
	}
	if got, want := obsdb.FromOTLPTraces(&tj), loadTracesFixture(t); !reflect.DeepEqual(got, want) {
		t.Errorf("traces JSON != pb:\n got %+v\nwant %+v", got, want)
	}
}
