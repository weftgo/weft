package ingest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"unicode/utf8"
)

// The second audit pass's pins (2026-10-02).

// TestOTLPJSONAsSendersSpellIt posts OTLP/JSON the way the OTLP spec
// and every stock SDK write it — trace and span ids in hex (protojson
// alone would read them as base64), enums as integers, no resource and
// no scope where the sender has none — and reads the rows back under
// the sender's own ids.
func TestOTLPJSONAsSendersSpellIt(t *testing.T) {
	const traceID, spanID, parentID = "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7", "53995c3f42cd8ad8"
	db := openDB(t)
	traces := `{"resourceSpans":[{"scopeSpans":[{"spans":[{
	  "traceId":"` + traceID + `","spanId":"` + spanID + `","parentSpanId":"` + parentID + `",
	  "name":"chat gpt","kind":3,
	  "startTimeUnixNano":"1790000000000000000","endTimeUnixNano":"1790000001000000000",
	  "attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}}],
	  "status":{"code":1}}]}]}]}`
	resp := postTraces(t, Traces(db, nil, always), []byte(traces), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("traces: %d %s", resp.StatusCode, b)
	}
	spans, err := db.Trace(context.Background(), traceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("Trace(%s) = %d spans, want the one posted under that hex id", traceID, len(spans))
	}
	if s := spans[0]; s.TraceID != traceID || s.SpanID != spanID || s.ParentSpanID != parentID || s.Kind != 3 || s.StatusCode != 1 {
		t.Errorf("span = trace %s span %s parent %s kind %d status %d", s.TraceID, s.SpanID, s.ParentSpanID, s.Kind, s.StatusCode)
	}

	logs := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{
	  "timeUnixNano":"1790000000000000000","severityNumber":9,
	  "eventName":"weft.event","body":{"stringValue":"{\"type\":\"run_start\",\"id\":\"run_hex\"}"},
	  "traceId":"` + traceID + `","spanId":"` + spanID + `",
	  "attributes":[
	    {"key":"weft.record","value":{"stringValue":"event"}},
	    {"key":"weft.run.id","value":{"stringValue":"run_hex"}},
	    {"key":"weft.event.type","value":{"stringValue":"run_start"}},
	    {"key":"weft.event.pos","value":{"intValue":"0"}}]}]}]}]}`
	resp = post(t, Logs(db, nil, always), []byte(logs), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("logs: %d %s", resp.StatusCode, b)
	}
	det, err := db.Run(context.Background(), "run_hex")
	if err != nil {
		t.Fatalf("the run the record names: %v", err)
	}
	if det.TraceID != traceID {
		t.Errorf("run trace id = %q, want the sender's %q", det.TraceID, traceID)
	}
}

// TestErrorBodyIsAlwaysJSON pins the audit's P3: the hand-rolled
// quoter copied bytes above 0x7f through, so an error message echoing
// request bytes (a Content-Encoding value, a decoder's complaint) that
// were not UTF-8 produced a body no JSON reader accepts.
func TestErrorBodyIsAlwaysJSON(t *testing.T) {
	resp := post(t, Logs(openDB(t), nil, always), []byte("x"), map[string]string{
		"Content-Type": "application/json", "Content-Encoding": "br\xff\x00\"zip",
	})
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !utf8.Valid(b) {
		t.Errorf("the error body is not UTF-8: %q", b)
	}
	var doc struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Error.Code != "unsupported" || doc.Error.Message == "" {
		t.Errorf("the error body does not decode: %v %q", err, b)
	}
}
