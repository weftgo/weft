package obsdb

import (
	"os"
	"reflect"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func loadOTLPLogs(t *testing.T, name string) *collogspb.ExportLogsServiceRequest {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

func loadOTLPTraces(t *testing.T, name string) *coltracepb.ExportTraceServiceRequest {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

func loadOTLPLogsJSON(t *testing.T, name string) *collogspb.ExportLogsServiceRequest {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var req collogspb.ExportLogsServiceRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

func loadOTLPTracesJSON(t *testing.T, name string) *coltracepb.ExportTraceServiceRequest {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

// mustTime is the fixture clock: 2026-10-01T09:12:03.120Z plus d.
func mustTime(d time.Duration) time.Time {
	base := time.Unix(0, 1790845923120000000).UTC()
	return base.Add(d)
}

// The protobuf and JSON fixtures are the same payload (the receiver
// accepts both encodings), so both must decode to identical rows.
func TestOTLPFixturesAgree(t *testing.T) {
	pbLogs := FromOTLPLogs(loadOTLPLogs(t, "logs.pb"))
	jsonLogs := FromOTLPLogs(loadOTLPLogsJSON(t, "logs.json"))
	if len(pbLogs) != len(jsonLogs) {
		t.Fatalf("pb %d records, json %d", len(pbLogs), len(jsonLogs))
	}
	for i := range pbLogs {
		if !reflect.DeepEqual(pbLogs[i], jsonLogs[i]) {
			t.Errorf("record %d: pb %+v != json %+v", i, pbLogs[i], jsonLogs[i])
		}
	}
	pbSpans := FromOTLPTraces(loadOTLPTraces(t, "traces.pb"))
	jsonSpans := FromOTLPTraces(loadOTLPTracesJSON(t, "traces.json"))
	if len(pbSpans) != len(jsonSpans) {
		t.Fatalf("pb %d spans, json %d", len(pbSpans), len(jsonSpans))
	}
	for i := range pbSpans {
		if !reflect.DeepEqual(pbSpans[i], jsonSpans[i]) {
			t.Errorf("span %d: pb %+v != json %+v", i, pbSpans[i], jsonSpans[i])
		}
	}
}

// The golden logs fixture decoded: every record shape and the full
// attribute-value mapping (string, bool, int64, double, arrays → []any,
// kvlists → map, bytes → base64) pinned by name.
func TestFromOTLPLogsGolden(t *testing.T) {
	recs := FromOTLPLogs(loadOTLPLogs(t, "logs.pb"))
	if len(recs) != 6 {
		t.Fatalf("records = %d, want 6", len(recs))
	}

	runStart := recs[0]
	if runStart.EventName != "weft.event" {
		t.Errorf("event name = %q", runStart.EventName)
	}
	if runStart.Time != mustTime(0) || runStart.Observed != mustTime(1) {
		t.Errorf("times = %v/%v", runStart.Time, runStart.Observed)
	}
	if runStart.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || runStart.SpanID != "00f067aa0ba902b7" {
		t.Errorf("ids = %s/%s", runStart.TraceID, runStart.SpanID)
	}
	if runStart.Severity != 9 {
		t.Errorf("severity = %d, want INFO(9)", runStart.Severity)
	}
	if runStart.Body != `{"type":"run_start","id":"r_20261001_1","model":{"provider":"wefttest","name":"script"},"agent":"demo"}` {
		t.Errorf("body = %s", runStart.Body)
	}
	if runStart.Service != "acme-api" {
		t.Errorf("service = %q", runStart.Service)
	}
	for k, want := range map[string]any{
		"weft.record": "event", "weft.run.id": "r_20261001_1",
		"weft.event.type": "run_start", "weft.event.pos": int64(0),
		"weft.content": "full", "weft.manifest.hash": "sha256:0f2c9a",
		"weft.version": "v0.6.0", "weft.session.id": "s_01M3",
		"weft.turn": int64(3), "weft.public_id": "pub_7Hk2", "tenant": "acme",
	} {
		if got := runStart.Attrs[k]; got != want {
			t.Errorf("attr %s = %#v, want %#v", k, got, want)
		}
	}
	// The non-scalar mappings: array, kvlist, bytes.
	if got := runStart.Attrs["weft.roles"]; len(got.([]any)) != 2 || got.([]any)[0] != "support" {
		t.Errorf("array attr = %#v", got)
	}
	if got := runStart.Attrs["weft.limit"]; got.(map[string]any)["rpm"] != int64(60) {
		t.Errorf("kvlist attr = %#v", got)
	}
	if got := runStart.Attrs["weft.token"]; got != "c2VjcmV0MQ==" {
		t.Errorf("bytes attr = %#v, want base64 string", got)
	}
	if runStart.Resource["service.version"] != "1.4.2" {
		t.Errorf("resource = %#v", runStart.Resource)
	}
	w := DeriveRecord(runStart)
	if w.Record != "event" || w.EventType != "run_start" || w.Pos != 0 ||
		w.SessionID != "s_01M3" || w.PublicID != "pub_7Hk2" || w.Turn != 3 || w.Agent != "demo" {
		t.Errorf("run_start derives %+v", w)
	}

	delta := recs[1]
	if w := DeriveRecord(delta); w.Record != "delta" || w.EventType != "text_delta" || w.Pos != 0 {
		t.Errorf("delta derives %+v", w)
	}
	msgs := recs[2]
	if w := DeriveRecord(msgs); w.Record != "messages" || w.Pos != 0 || w.Step != 0 {
		t.Errorf("messages derives %+v", w)
	}
	if msgs.Body != `[{"role":"user","content":[{"type":"text","text":"find order 42"}]}]` {
		t.Errorf("messages body = %s", msgs.Body)
	}
	fin := recs[3]
	if w := DeriveRecord(fin); w.Record != "event" || w.EventType != "run_finish" || w.Pos != 3 {
		t.Errorf("run_finish derives %+v", w)
	}
	hb := recs[4]
	if w := DeriveRecord(hb); w.Record != "heartbeat" || w.RunID != "r_20261001_1" {
		t.Errorf("heartbeat derives %+v", w)
	}
	if hb.Body != "" || hb.EventName != "weft.heartbeat" {
		t.Errorf("heartbeat body/event = %q/%q, want empty body", hb.Body, hb.EventName)
	}
	// The slog line: non-weft, no run identity, a service's own log.
	slogLine := recs[5]
	if w := DeriveRecord(slogLine); w.RunID != "" || w.Record != "" {
		t.Errorf("slog line derives %+v, want non-weft", w)
	}
	if slogLine.Body != "payment worker retrying after 502" || slogLine.Severity != 17 {
		t.Errorf("slog line = %+v", slogLine)
	}
}

// The golden traces fixture decoded: the weft span trio's shapes and a
// polyglot (non-weft) span with an exception event.
func TestFromOTLPTracesGolden(t *testing.T) {
	spans := FromOTLPTraces(loadOTLPTraces(t, "traces.pb"))
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	ia, chat, other := spans[0], spans[1], spans[2]
	if ia.Name != "invoke_agent demo" || ia.Kind != 1 || ia.StatusCode != 1 {
		t.Errorf("invoke_agent = %+v", ia)
	}
	if ia.Start != mustTime(0) || ia.End != mustTime(9410*time.Millisecond) {
		t.Errorf("invoke_agent window = %v..%v", ia.Start, ia.End)
	}
	if w := DeriveSpan(ia); w.RunID != "r_20261001_1" || w.Agent != "demo" || w.SessionID != "s_01M3" || w.Turn != 3 || w.PublicID != "pub_7Hk2" {
		t.Errorf("invoke_agent derives %+v", w)
	}
	if ia.Attrs["gen_ai.usage.input_tokens"] != int64(1012) || ia.Attrs["gen_ai.usage.cache_read.input_tokens"] != int64(120) {
		t.Errorf("usage attrs = %#v", ia.Attrs)
	}
	if chat.ParentSpanID != ia.SpanID || chat.Kind != 3 {
		t.Errorf("chat = %+v", chat)
	}
	if w := DeriveSpan(chat); w.RunID != "r_20261001_1" || w.Step != 0 || w.ToolSeq != -1 {
		t.Errorf("chat derives %+v", w)
	}
	// The polyglot span: no run identity, its exception event intact.
	if w := DeriveSpan(other); w.RunID != "" || w.Agent != "" {
		t.Errorf("non-weft span derives %+v", w)
	}
	if other.Kind != 2 || other.StatusCode != 2 || other.StatusMessage != "upstream timed out" {
		t.Errorf("non-weft status = %d/%q", other.StatusCode, other.StatusMessage)
	}
	if len(other.Events) != 1 || other.Events[0].Name != "exception" ||
		other.Events[0].Attrs["exception.type"] != "timeout" {
		t.Errorf("events = %+v", other.Events)
	}
	// nil requests read as nil, not as a panic.
	if got := FromOTLPLogs(nil); got != nil {
		t.Errorf("nil logs request = %v", got)
	}
	if got := FromOTLPTraces(nil); got != nil {
		t.Errorf("nil traces request = %v", got)
	}
}

// proto round trip: what the receiver decodes is what the model holds —
// marshal a model-decoded request back and compare messages (guards
// against a protojson-only field drifting out of the binary fixture).
func TestOTLPRoundTripStable(t *testing.T) {
	req := loadOTLPLogs(t, "logs.pb")
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(req, &back) {
		t.Error("logs fixture not stable under marshal/unmarshal")
	}
}

// OTLP/JSON is not protojson: the specification encodes traceId, spanId
// and parentSpanId as hex strings (and enums as integers), where the
// standard protobuf JSON mapping reads a bytes field as base64. A hex id
// is valid base64 too, so protojson decodes it without complaint — into
// 24 (or 12) bytes of garbage. Every non-Go OTLP/JSON exporter sends
// this shape; the model's ids must be the ids the sender wrote.
func TestFromOTLPJSONHexIDs(t *testing.T) {
	const traces = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"py-svc"}}]},
	 "scopeSpans":[{"spans":[{"traceId":"5B8EFFF798038103D269B633813FC60C","spanId":"eee19b7ec3c1b174",
	   "parentSpanId":"eee19b7ec3c1b173","name":"chat","kind":3,
	   "startTimeUnixNano":"1790845923120000000","endTimeUnixNano":"1790845923130000000"}]}]}]}`
	var treq coltracepb.ExportTraceServiceRequest
	if err := protojson.Unmarshal([]byte(traces), &treq); err != nil {
		t.Fatal(err)
	}
	spans := FromOTLPTraces(&treq)
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if s := spans[0]; s.TraceID != "5b8efff798038103d269b633813fc60c" ||
		s.SpanID != "eee19b7ec3c1b174" || s.ParentSpanID != "eee19b7ec3c1b173" {
		t.Errorf("hex-encoded OTLP/JSON ids = %q / %q / %q, want the sender's own ids, lowercase",
			s.TraceID, s.SpanID, s.ParentSpanID)
	}
	const logs = `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"1790845923120000000",
	   "traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","body":{"stringValue":"hi"}}]}]}]}`
	var lreq collogspb.ExportLogsServiceRequest
	if err := protojson.Unmarshal([]byte(logs), &lreq); err != nil {
		t.Fatal(err)
	}
	recs := FromOTLPLogs(&lreq)
	if len(recs) != 1 || recs[0].TraceID != "5b8efff798038103d269b633813fc60c" || recs[0].SpanID != "eee19b7ec3c1b174" {
		t.Errorf("hex-encoded OTLP/JSON log ids = %+v", recs)
	}
}

// The requests arrive from the network: a nil element at any layer
// (hand-built requests; decoders that tolerate null) must be skipped,
// never dereferenced.
func TestFromOTLPNilLayers(t *testing.T) {
	spans := FromOTLPTraces(&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{
		nil,
		{ScopeSpans: []*tracepb.ScopeSpans{nil, {Spans: []*tracepb.Span{nil, {Name: "kept", Events: []*tracepb.Span_Event{nil}}}}}},
	}})
	if len(spans) != 1 || spans[0].Name != "kept" {
		t.Errorf("spans = %+v, want the one non-nil span", spans)
	}
	recs := FromOTLPLogs(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{
		nil,
		{ScopeLogs: []*logspb.ScopeLogs{nil, {LogRecords: []*logspb.LogRecord{nil, {EventName: "kept"}}}}},
	}})
	if len(recs) != 1 || recs[0].EventName != "kept" {
		t.Errorf("records = %+v, want the one non-nil record", recs)
	}
}

// A non-string body (a stock bridge's structured log) renders through
// the AnyValue mapping as JSON — never silently as "".
func TestFromOTLPStructuredBody(t *testing.T) {
	body := func(v *commonpb.AnyValue) string {
		recs := FromOTLPLogs(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{
			{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{Body: v}}}}},
		}})
		if len(recs) != 1 {
			t.Fatalf("records = %d", len(recs))
		}
		return recs[0].Body
	}
	kv := &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{
		Values: []*commonpb.KeyValue{
			{Key: "msg", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "paid"}}},
			{Key: "amount", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}},
		},
	}}}
	if got := body(kv); got != `{"amount":42,"msg":"paid"}` {
		t.Errorf("kvlist body = %q, want its JSON", got)
	}
	if got := body(&commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 7}}); got != "7" {
		t.Errorf("int body = %q, want 7", got)
	}
	if got := body(&commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "plain"}}); got != "plain" {
		t.Errorf("string body = %q, want it verbatim", got)
	}
	if got := body(nil); got != "" {
		t.Errorf("absent body = %q, want empty", got)
	}
}
