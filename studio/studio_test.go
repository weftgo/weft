package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/wefttest"
)

// The fixtures (plan §3, rebuilt on obsdb for the step-5 interim read
// path): a success with a tool call, a failure, a parent with a
// subagent (the child is its own run, linked by parent_run_id and
// parent_call_id), and a crash-orphaned "running" row whose stale
// last-seen reads interrupted. Each run is one obsdb Batch — the
// events, messages and invoke span a real pipeline would write — with
// every time pinned, so the goldens are byte-stable. The only
// normalization left is weft_version, which depends on where the test
// runs.

var fixtureTags = map[string]string{"cwd": "/tmp/demo"}

// fixtureT0 anchors every fixture time: 2020-01-01T09:00:00Z — far
// enough in the past that the derived status of the stale row reads
// interrupted at any future "now", so the goldens never flip.
var fixtureT0 = time.Date(2020, 1, 1, 9, 0, 0, 123000000, time.UTC)

const (
	fxTrace = "0102030405060708090a0b0c0d0e0f10"
	fxSpan  = "0102030405060708"
	// fxHash mirrors what weft.New stamps at construction (the
	// manifest hash rides every record and span).
	fxHash = "sha256:demo-fleet"
)

// fxRecord builds one weft log record for run at time at: kind is
// event | messages, pos its durable position, body its wire JSON.
// The identity chain, the record contract attributes and the caller
// metadata a real run carries ride Attrs.
func fxRecord(run, kind, eventType string, pos int64, at time.Time, body string) obsdb.Record {
	attrs := map[string]any{
		"weft.run.id":        run,
		"weft.record":        kind,
		"weft.version":       "v0.6.0",
		"weft.manifest.hash": fxHash,
		"gen_ai.agent.name":  "orders",
	}
	switch kind {
	case "event":
		attrs["weft.event.type"] = eventType
		attrs["weft.event.pos"] = pos
	case "messages":
		attrs["weft.messages.index"] = pos
	}
	for k, v := range fixtureTags {
		attrs[k] = v
	}
	return obsdb.Record{
		Time: at, TraceID: fxTrace, SpanID: fxSpan, Severity: 9,
		EventName: "weft." + kind, Body: body, Service: "studio-test",
		Attrs: attrs, Resource: map[string]any{"service.name": "studio-test"},
	}
}

// fxSpanRec builds a run's invoke_agent span: status 1 ok or 2 error,
// ending at end with the usage and step count the core reports.
func fxSpanRec(run string, start, end time.Time, status int, statusMsg string, inTok, outTok int64) obsdb.Span {
	attrs := map[string]any{
		"gen_ai.operation.name":      "invoke_agent",
		"weft.run.id":                run,
		"gen_ai.agent.name":          "orders",
		"gen_ai.provider.name":       "wefttest",
		"gen_ai.request.model":       "script",
		"gen_ai.usage.input_tokens":  inTok,
		"gen_ai.usage.output_tokens": outTok,
		"weft.run.steps":             int64(1),
		"weft.version":               "v0.6.0",
		"weft.manifest.hash":         fxHash,
	}
	for k, v := range fixtureTags {
		attrs[k] = v
	}
	return obsdb.Span{
		TraceID: fxTrace, SpanID: fxSpan, Name: "invoke_agent", Kind: 1,
		Start: start, End: end, StatusCode: status, StatusMessage: statusMsg,
		Service: "studio-test", Attrs: attrs, Resource: map[string]any{"service.name": "studio-test"},
	}
}

// fixtureDB writes the four fixture runs into one in-memory obsdb.
//
//   - r_ok: one tool call, then the answer (t0 .. t0+2s).
//   - r_fail: the model stream fails mid-run; the partial transcript
//     and the error text are part of the row (t0+1m .. +2s).
//   - r_sub: a parent that delegates to a researcher subagent; the
//     child run records itself and links back (t0+2m .. +5s, child
//     +3s .. +4s). The child's id carries slashes (childRunID).
//   - r_stale: a hand-built crash orphan — no run_finish, no span, a
//     last-seen minutes stale, events mid-step (t0+3m).
func fixtureDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	okAt := func(d time.Duration) time.Time { return fixtureT0.Add(d) }

	// r_ok
	okEvents := []obsdb.Record{
		fxRecord("r_ok", "event", "run_start", 0, okAt(0),
			`{"type":"run_start","id":"r_ok","model":{"provider":"wefttest","name":"script"},"agent":"orders"}`),
		fxRecord("r_ok", "event", "step_start", 1, okAt(300*time.Millisecond),
			`{"type":"step_start","run_id":"r_ok","index":0}`),
		fxRecord("r_ok", "event", "tool_start", 2, okAt(500*time.Millisecond),
			`{"type":"tool_start","run_id":"r_ok","seq":1,"call_id":"call_1","name":"lookup_order","args":{"order_id":"42"}}`),
		fxRecord("r_ok", "event", "tool_finish", 3, okAt(time.Second),
			`{"type":"tool_finish","run_id":"r_ok","seq":1,"call_id":"call_1","name":"lookup_order","content":"order 42: shipped","is_error":false}`),
		fxRecord("r_ok", "event", "step_finish", 4, okAt(1500*time.Millisecond),
			`{"type":"step_finish","run_id":"r_ok","index":0,"reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4}}`),
		fxRecord("r_ok", "event", "run_finish", 5, okAt(2*time.Second),
			`{"type":"run_finish","run_id":"r_ok","usage":{"input_tokens":10,"output_tokens":4},"steps":1}`),
		fxRecord("r_ok", "messages", "", 0, okAt(100*time.Millisecond),
			`[{"role":"user","content":[{"type":"text","text":"Where is order 42?"}]}]`),
		fxRecord("r_ok", "messages", "", 1, okAt(2*time.Second),
			`[{"role":"assistant","content":[{"type":"text","text":"Order 42 shipped this morning."}]}]`),
	}
	if err := db.Write(ctx, obsdb.Batch{Records: okEvents, Spans: []obsdb.Span{
		fxSpanRec("r_ok", okAt(0), okAt(2*time.Second), 1, "", 10, 4),
	}}); err != nil {
		t.Fatal(err)
	}

	// r_fail
	failAt := func(d time.Duration) time.Time { return fixtureT0.Add(time.Minute + d) }
	failEvents := []obsdb.Record{
		fxRecord("r_fail", "event", "run_start", 0, failAt(0),
			`{"type":"run_start","id":"r_fail","model":{"provider":"wefttest","name":"script"},"agent":"orders"}`),
		fxRecord("r_fail", "event", "step_start", 1, failAt(300*time.Millisecond),
			`{"type":"step_start","run_id":"r_fail","index":0}`),
		fxRecord("r_fail", "event", "tool_start", 2, failAt(500*time.Millisecond),
			`{"type":"tool_start","run_id":"r_fail","seq":1,"call_id":"call_2","name":"lookup_order","args":{"order_id":"43"}}`),
		fxRecord("r_fail", "event", "tool_finish", 3, failAt(time.Second),
			`{"type":"tool_finish","run_id":"r_fail","seq":1,"call_id":"call_2","name":"lookup_order","content":"order 43: backordered","is_error":false}`),
		fxRecord("r_fail", "messages", "", 0, failAt(100*time.Millisecond),
			`[{"role":"user","content":[{"type":"text","text":"Where is order 43?"}]}]`),
	}
	if err := db.Write(ctx, obsdb.Batch{Records: failEvents, Spans: []obsdb.Span{
		fxSpanRec("r_fail", failAt(0), failAt(2*time.Second), 2, "wefttest: injected provider 500", 8, 2),
	}}); err != nil {
		t.Fatal(err)
	}

	// r_sub and its child. The parent's durable stream carries no
	// Nested wrappers — the child is its own run, joined by
	// parent_run_id / parent_call_id (S4.3's fold input, step 6).
	subAt := func(d time.Duration) time.Time { return fixtureT0.Add(2*time.Minute + d) }
	const childID = "r_sub/0/call_3"
	subEvents := []obsdb.Record{
		fxRecord("r_sub", "event", "run_start", 0, subAt(0),
			`{"type":"run_start","id":"r_sub","model":{"provider":"wefttest","name":"script"},"agent":"orders"}`),
		fxRecord("r_sub", "event", "step_start", 1, subAt(300*time.Millisecond),
			`{"type":"step_start","run_id":"r_sub","index":0}`),
		fxRecord("r_sub", "event", "tool_start", 2, subAt(500*time.Millisecond),
			`{"type":"tool_start","run_id":"r_sub","seq":1,"call_id":"call_3","name":"research","args":{"prompt":"status of order 42"}}`),
		fxRecord("r_sub", "event", "tool_finish", 3, subAt(4*time.Second),
			`{"type":"tool_finish","run_id":"r_sub","seq":1,"call_id":"call_3","name":"research","content":"order 42 shipped this morning","is_error":false}`),
		fxRecord("r_sub", "event", "step_finish", 4, subAt(4500*time.Millisecond),
			`{"type":"step_finish","run_id":"r_sub","index":0,"reason":"end_turn","usage":{"input_tokens":12,"output_tokens":6}}`),
		fxRecord("r_sub", "event", "run_finish", 5, subAt(5*time.Second),
			`{"type":"run_finish","run_id":"r_sub","usage":{"input_tokens":12,"output_tokens":6},"steps":1}`),
		fxRecord("r_sub", "messages", "", 0, subAt(100*time.Millisecond),
			`[{"role":"user","content":[{"type":"text","text":"Where is order 42?"}]}]`),
		fxRecord("r_sub", "messages", "", 1, subAt(5*time.Second),
			`[{"role":"assistant","content":[{"type":"text","text":"Order 42 shipped."}]}]`),
	}
	if err := db.Write(ctx, obsdb.Batch{Records: subEvents, Spans: []obsdb.Span{
		fxSpanRec("r_sub", subAt(0), subAt(5*time.Second), 1, "", 12, 6),
	}}); err != nil {
		t.Fatal(err)
	}
	childEvents := []obsdb.Record{
		fxRecord(childID, "event", "run_start", 0, subAt(3*time.Second),
			`{"type":"run_start","id":"`+childID+`","model":{"provider":"wefttest","name":"script"},"agent":"researcher"}`),
		fxRecord(childID, "event", "step_start", 1, subAt(3200*time.Millisecond),
			`{"type":"step_start","run_id":"`+childID+`","index":0}`),
		fxRecord(childID, "event", "step_finish", 2, subAt(3700*time.Millisecond),
			`{"type":"step_finish","run_id":"`+childID+`","index":0,"reason":"end_turn","usage":{"input_tokens":9,"output_tokens":5}}`),
		fxRecord(childID, "event", "run_finish", 3, subAt(4*time.Second),
			`{"type":"run_finish","run_id":"`+childID+`","usage":{"input_tokens":9,"output_tokens":5},"steps":1}`),
		fxRecord(childID, "messages", "", 0, subAt(3*time.Second),
			`[{"role":"user","content":[{"type":"text","text":"status of order 42"}]}]`),
		fxRecord(childID, "messages", "", 1, subAt(4*time.Second),
			`[{"role":"assistant","content":[{"type":"text","text":"order 42 shipped this morning"}]}]`),
	}
	// The child's records carry the parent link the subagent stamps.
	for i := range childEvents {
		childEvents[i].Attrs["weft.parent.run.id"] = "r_sub"
		childEvents[i].Attrs["weft.parent.call.id"] = "call_3"
		childEvents[i].Attrs["gen_ai.agent.name"] = "researcher"
	}
	if err := db.Write(ctx, obsdb.Batch{Records: childEvents, Spans: []obsdb.Span{
		{TraceID: fxTrace, SpanID: "0a0b0c0d0e0f0102", ParentSpanID: fxSpan,
			Name: "invoke_agent", Kind: 1,
			Start: subAt(3 * time.Second), End: subAt(4 * time.Second), StatusCode: 1,
			Service: "studio-test",
			Attrs: map[string]any{
				"gen_ai.operation.name":      "invoke_agent",
				"weft.run.id":                childID,
				"weft.parent.run.id":         "r_sub",
				"weft.parent.call.id":        "call_3",
				"gen_ai.agent.name":          "researcher",
				"gen_ai.provider.name":       "wefttest",
				"gen_ai.request.model":       "script",
				"gen_ai.usage.input_tokens":  int64(9),
				"gen_ai.usage.output_tokens": int64(5),
				"weft.run.steps":             int64(1),
				"weft.version":               "v0.6.0",
				"weft.manifest.hash":         fxHash,
				"cwd":                        "/tmp/demo",
			},
			Resource: map[string]any{"service.name": "studio-test"}},
	}}); err != nil {
		t.Fatal(err)
	}

	// r_stale: the crash orphan. No run_finish, no span; its two events
	// are all the table ever learns.
	staleStart := fixtureT0.Add(3 * time.Minute)
	stale := []obsdb.Record{
		fxRecord("r_stale", "event", "run_start", 0, staleStart,
			`{"type":"run_start","id":"r_stale","model":{"provider":"wefttest","name":"script"},"agent":"orders"}`),
		fxRecord("r_stale", "event", "step_start", 1, staleStart.Add(200*time.Millisecond),
			`{"type":"step_start","run_id":"r_stale","index":0}`),
	}
	if err := db.Write(ctx, obsdb.Batch{Records: stale}); err != nil {
		t.Fatal(err)
	}
	return db
}

// fixtureManifest is a minimal manifest document for the manifest
// endpoint's golden — pass-through bytes, so a literal is the honest
// fixture.
const fixtureManifest = `{
  "weft": 1,
  "agents": [
    {
      "name": "orders",
      "model": {"provider": "wefttest", "name": "script"},
      "instructions": "You handle orders.",
      "policy": {"parallelism": 4, "max_steps": 10, "max_result_bytes": 65536, "max_model_retries": 3},
      "tools": [
        {
          "name": "lookup_order",
          "description": "Look up an order by ID.",
          "input_schema": {"type": "object", "properties": {"order_id": {"type": "string", "description": "the order to look up"}}, "required": ["order_id"]}
        }
      ]
    }
  ]
}
`

var weftVersionRe = regexp.MustCompile(`"weft_version": ?"[^"]*"`)

// get issues a GET against a handler mounted as the docs show and
// returns status, headers, and the body.
func get(t *testing.T, h http.Handler, path string) (int, http.Header, string) {
	t.Helper()
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

// mounted wraps a Handler in the mux the doc comment shows, so tests
// exercise the same path shape users mount (StripPrefix "/studio").
func mounted(h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", h))
	return mux
}

// post issues a POST with a body against a mounted handler.
func post(t *testing.T, h http.Handler, path, ctype, body string) (int, http.Header, string) {
	t.Helper()
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+path, ctype, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

// pretty re-indents a compact JSON body so goldens read like the API.
func pretty(t *testing.T, body string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(body), "", "  "); err != nil {
		t.Fatalf("indent: %v", err)
	}
	out := weftVersionRe.ReplaceAllString(buf.String(), `"weft_version": "(test)"`)
	return out + "\n"
}

func golden(t *testing.T, name, body string) {
	t.Helper()
	wefttest.Golden(t, "testdata/api/"+name, []byte(pretty(t, body)))
}

func TestMetaGolden(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/meta")
	if code != http.StatusOK {
		t.Fatalf("meta: %d", code)
	}
	if !strings.Contains(body, `"store":"sqlite"`) {
		t.Errorf("meta store kind = %s", body)
	}
	golden(t, "meta.golden.json", body)

	// Capabilities are computed from the registered route groups
	// (S4.2): the read API names none and ingest is registered, so the
	// open handler reports exactly [ingest] — live joins with its
	// group (S4.5), the panel and playground with theirs (step 7/8).
	_, _, plain := get(t, Handler(DB(fixtureDB(t))), "/studio/api/meta")
	if !strings.Contains(plain, `"capabilities":["ingest"]`) {
		t.Errorf("default capabilities = %s, want [ingest]", plain)
	}
	// Ingest is open on loopback without a token, and meta says so
	// (S4.4); a configured token closes it.
	if !strings.Contains(body, `"ingest_open":true`) {
		t.Errorf("meta ingest_open: %s", body)
	}
	_, _, tok := get(t, Handler(DB(fixtureDB(t)), IngestToken("s3cr3t")), "/studio/api/meta")
	if !strings.Contains(tok, `"ingest_open":false`) {
		t.Errorf("meta ingest_open with token: %s", tok)
	}
	// NoIngest drops the ingest group with its routes and capability.
	_, _, ro := get(t, Handler(DB(fixtureDB(t)), NoIngest()), "/studio/api/meta")
	if !strings.Contains(ro, `"capabilities":[]`) || !strings.Contains(ro, `"ingest_open":false`) {
		t.Errorf("NoIngest meta: %s", ro)
	}
	if code, _, _ := post(t, Handler(DB(fixtureDB(t)), NoIngest()), "/studio/v1/logs", "application/json", "{}"); code != http.StatusNotFound {
		t.Errorf("NoIngest /v1/logs: %d, want 404", code)
	}
	// Playground(true) is accepted but adds nothing until step 8's
	// playground.go registers its group.
	_, _, pg := get(t, Handler(DB(fixtureDB(t)), Playground(true)), "/studio/api/meta")
	if !strings.Contains(pg, `"capabilities":["ingest"]`) {
		t.Errorf("Playground capabilities = %s", pg)
	}
	// A hosting wrapper declares its own verbs beside the groups'.
	_, _, caps := get(t, Handler(DB(fixtureDB(t)), Capabilities("fleet")), "/studio/api/meta")
	if !strings.Contains(caps, `"capabilities":["ingest","fleet"]`) {
		t.Errorf("declared capabilities = %s", caps)
	}
}

// TestIngestAuth pins S4.4's ingest auth through the server: the
// configured token as a bearer, or — with none — loopback peers only.
func TestIngestAuth(t *testing.T) {
	pb, err := os.ReadFile(filepath.Join("..", "obsdb", "testdata", "logs.pb"))
	if err != nil {
		t.Fatal(err)
	}
	postTo := func(h http.Handler, remote, bearer string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(pb))
		req.RemoteAddr = remote
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	// No token: loopback open (setup B), a remote peer refused.
	open := New(DB(fixtureDB(t))).Handler()
	if c := postTo(open, "127.0.0.1:51234", ""); c != http.StatusOK {
		t.Errorf("loopback without token: %d", c)
	}
	if c := postTo(open, "10.0.0.5:51234", ""); c != http.StatusUnauthorized {
		t.Errorf("remote without token: %d, want 401", c)
	}
	// With a token: only the bearer works, from anywhere.
	tok := New(DB(fixtureDB(t)), IngestToken("s3cr3t")).Handler()
	if c := postTo(tok, "127.0.0.1:51234", ""); c != http.StatusUnauthorized {
		t.Errorf("loopback without bearer: %d, want 401", c)
	}
	if c := postTo(tok, "127.0.0.1:51234", "wrong"); c != http.StatusUnauthorized {
		t.Errorf("wrong bearer: %d, want 401", c)
	}
	if c := postTo(tok, "10.0.0.5:51234", "s3cr3t"); c != http.StatusOK {
		t.Errorf("remote with bearer: %d", c)
	}
}

func TestRunsGolden(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/runs")
	if code != http.StatusOK {
		t.Fatalf("runs: %d", code)
	}
	// Top-level only, newest first: the stale crash-orphan reads
	// interrupted and is shown (A1), the child never floods the list.
	for _, want := range []string{
		`"id":"r_stale"`, `"status":"interrupted"`,
		`"id":"r_sub"`, `"id":"r_fail"`, `"id":"r_ok"`,
		`"total":4`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runs list missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, `"r_sub/`) {
		t.Error("runs list includes a child run")
	}
	// The store-era tags key reads the row's metadata.
	if !strings.Contains(body, `"tags":{"cwd":"/tmp/demo"}`) {
		t.Errorf("runs list missing metadata under tags: %s", body)
	}
	golden(t, "runs.golden.json", body)
}

func TestRunGolden(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/runs/r_sub")
	if code != http.StatusOK {
		t.Fatalf("run: %d", code)
	}
	// The document carries children but never events (ADR 0018 §8),
	// and event_count sizes the replay scrubber (plan §3).
	if strings.Contains(body, `"events"`) {
		t.Error("run document carries inline events")
	}
	if !strings.Contains(body, `"parent_id":"r_sub"`) {
		t.Errorf("run document misses children: %s", body)
	}
	if !strings.Contains(body, `"event_count":6`) {
		t.Errorf("run document misses event_count: %s", body)
	}
	golden(t, "run-sub.golden.json", body)

	// A failed run's document keeps the error text; the result
	// document itself is null on obsdb (the store's MarshalResult is
	// gone; the transcript replaces it in step 6).
	_, _, fail := get(t, h, "/studio/api/runs/r_fail")
	for _, want := range []string{`"status":"failed"`, `"err":"`, `"result":null`} {
		if !strings.Contains(fail, want) {
			t.Errorf("r_fail missing %s", want)
		}
	}
	golden(t, "run-fail.golden.json", fail)
}

func TestEventsGolden(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/runs/r_ok/events?limit=1000")
	if code != http.StatusOK {
		t.Fatalf("events: %d", code)
	}
	if !strings.Contains(body, `"done":true`) {
		t.Errorf("finished run's first page not done: %s", body)
	}
	golden(t, "events-ok.golden.json", body)

	_, _, paged := get(t, h, "/studio/api/runs/r_ok/events?after=2&limit=3")
	golden(t, "events-ok-paged.golden.json", paged)

	// The subagent parent's stream: no Nested wrappers inline — the
	// child run is a separate row joined by parent_call_id (S4.3).
	_, _, sub := get(t, h, "/studio/api/runs/r_sub/events?limit=1000")
	if strings.Contains(sub, `"nested"`) {
		t.Errorf("parent stream carries nested wrappers: %s", sub)
	}
	golden(t, "events-sub.golden.json", sub)

	// A child run's id carries slashes (childRunID: parent/step/callID)
	// and its page is a full run page (B7): the document and the events
	// endpoint must both answer for it.
	kid := childID(t, h, "r_sub")
	if code, _, b := get(t, h, "/studio/api/runs/"+kid); code != http.StatusOK ||
		!strings.Contains(b, `"id":"`+kid+`"`) || !strings.Contains(b, `"parent_id":"r_sub"`) {
		t.Errorf("child run document: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/runs/"+kid+"/events"); code != http.StatusOK ||
		!strings.Contains(b, `"done":true`) {
		t.Errorf("child run events: %d %s", code, b)
	}
}

// childID returns the parent's first child run id from the run document.
func childID(t *testing.T, h http.Handler, parent string) string {
	t.Helper()
	_, _, body := get(t, h, "/studio/api/runs/"+parent)
	var doc struct {
		Children []struct {
			ID string `json:"id"`
		} `json:"children"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil || len(doc.Children) == 0 {
		t.Fatalf("no children under %s: %v %s", parent, err, body)
	}
	return doc.Children[0].ID
}

func TestEventsPaging(t *testing.T) {
	db := fixtureDB(t)
	h := Handler(DB(db))
	ctx := context.Background()

	// The store of record for the walk: the database's own event order.
	full, err := db.Events(ctx, "r_ok", -1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	total := len(full.Events)
	want := make([]string, total)
	for i, pe := range full.Events {
		want[i] = string(pe.Event)
	}

	// Walk the whole stream in pages of 2 and reassemble it: the pages
	// concatenated must equal the database's order byte-for-byte, and
	// the last page reports done.
	var walked int
	after := 0
	for {
		_, _, body := get(t, h, fmt.Sprintf("/studio/api/runs/r_ok/events?after=%d&limit=2", after))
		var page struct {
			Events []struct {
				Pos   int64           `json:"pos"`
				Event json.RawMessage `json:"event"`
			} `json:"events"`
			NextAfter *int64 `json:"next_after"`
			Done      bool   `json:"done"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for i, pe := range page.Events {
			// Positions are dense and continuous across pages: each
			// page starts exactly at the cursor it was given.
			if pe.Pos != int64(after+i) {
				t.Fatalf("event %d has pos %d, want %d", walked, pe.Pos, after+i)
			}
			if walked >= total || string(pe.Event) != want[walked] {
				t.Fatalf("event %d = %s, want %s", walked, pe.Event, want)
			}
			walked++
		}
		if page.NextAfter == nil {
			if !page.Done {
				t.Fatalf("last page not done: %s", body)
			}
			break
		}
		after = int(*page.NextAfter)
	}
	if walked != total {
		t.Fatalf("walked %d events, database holds %d", walked, total)
	}

	// -1 is the documented "from the start" default and reads like 0.
	if code, _, b := get(t, h, "/studio/api/runs/r_ok/events?after=-1&limit=2"); code != http.StatusOK ||
		!strings.Contains(b, `"pos":0`) {
		t.Errorf("after=-1: %d %s", code, b)
	}

	// Past the end: empty page, no cursor, still done.
	for _, after := range []int64{int64(total), int64(total) + 5, 1<<63 - 1} {
		code, _, tail := get(t, h, fmt.Sprintf("/studio/api/runs/r_ok/events?after=%d", after))
		if code != http.StatusOK || !strings.Contains(tail, `"events":[]`) ||
			!strings.Contains(tail, `"next_after":null`) || !strings.Contains(tail, `"done":true`) {
			t.Errorf("after=%d past the end: %d %s", after, code, tail)
		}
	}

	// A run that is live right now (fresh last-seen, no run_finish) is
	// never done.
	liveAt := time.Now()
	live := []obsdb.Record{
		fxRecord("r_live", "event", "run_start", 0, liveAt,
			`{"type":"run_start","id":"r_live","model":{"provider":"wefttest","name":"script"},"agent":"orders"}`),
		fxRecord("r_live", "event", "step_start", 1, liveAt.Add(50*time.Millisecond),
			`{"type":"step_start","run_id":"r_live","index":0}`),
	}
	if err := db.Write(ctx, obsdb.Batch{Records: live}); err != nil {
		t.Fatal(err)
	}
	_, _, body := get(t, h, "/studio/api/runs/r_live/events")
	if !strings.Contains(body, `"done":false`) {
		t.Errorf("live run reported done: %s", body)
	}
	// The stale orphan is finished-in-effect: done once drained.
	_, _, stale := get(t, h, "/studio/api/runs/r_stale/events")
	if !strings.Contains(stale, `"done":true`) {
		t.Errorf("stale run not done: %s", stale)
	}
}

func TestAPIErrors(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	code, _, body := get(t, h, "/studio/api/runs/nope")
	if code != http.StatusNotFound || !strings.Contains(body, `"not_found"`) {
		t.Errorf("unknown id: %d %s", code, body)
	}
	if code, _, b := get(t, h, "/studio/api/runs?before=yesterday"); code != http.StatusBadRequest || !strings.Contains(b, "bad_request") {
		t.Errorf("bad before: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/runs/r_ok/events?after=-2"); code != http.StatusBadRequest {
		t.Errorf("after=-2: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/runs?limit=lots"); code != http.StatusBadRequest {
		t.Errorf("bad limit: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/nope"); code != http.StatusNotFound {
		t.Errorf("unknown api route: %d %s", code, b)
	}

	// POST is refused everywhere on the API: read routes answer the
	// error shape (the registered patterns are GET-only), the UI says
	// GET only with Allow.
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/studio/api/runs", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST api/runs: %d", resp.StatusCode)
	}
	resp, err = http.Post(srv.URL+"/studio/runs", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST ui: %d", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q", allow)
	}

	// A database that fails reads answers 500 internal naming the op —
	// loud over silent.
	stub := stubDB{err: errors.New("disk on fire")}
	for _, path := range []string{"/studio/api/runs", "/studio/api/runs/r_x", "/studio/api/runs/r_x/events"} {
		code, _, b := get(t, Handler(DB(stub)), path)
		if code != http.StatusInternalServerError || !strings.Contains(b, `"internal"`) {
			t.Errorf("%s: %d %s", path, code, b)
		}
	}
}

// stubDB fails every read with err.
type stubDB struct {
	obsdb.DB
	err error
}

func (s stubDB) Runs(context.Context, obsdb.RunQuery) (obsdb.RunPage, error) {
	return obsdb.RunPage{}, s.err
}
func (s stubDB) Run(context.Context, string) (obsdb.RunDetail, error) {
	return obsdb.RunDetail{}, s.err
}
func (s stubDB) Events(context.Context, string, int64, int) (obsdb.EventPage, error) {
	return obsdb.EventPage{}, s.err
}

// TestOpenOption pins S4.1's Open and the $WEFT_DB default: Open(path)
// opens (creating) the sqlite file, and Handler without DB or Open
// opens $WEFT_DB.
func TestOpenOption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dev.db") // Open creates the parent
	h := Handler(Open(path))
	code, _, body := get(t, h, "/studio/api/meta")
	if code != http.StatusOK || !strings.Contains(body, `"store":"sqlite"`) {
		t.Errorf("Open: %d %s", code, body)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Open did not create %s: %v", path, err)
	}

	envDB := filepath.Join(dir, "env.db")
	t.Setenv("WEFT_DB", envDB)
	_, _, body = get(t, Handler(), "/studio/api/meta")
	if !strings.Contains(body, `"store":"sqlite"`) {
		t.Errorf("WEFT_DB default: %s", body)
	}
	if _, err := os.Stat(envDB); err != nil {
		t.Errorf("default did not open %s: %v", envDB, err)
	}
}

func TestManifestEndpoint(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/manifest")
	if code != http.StatusOK {
		t.Fatalf("manifest: %d", code)
	}
	if body != fixtureManifest {
		t.Errorf("manifest bytes not passed through verbatim")
	}
	code, _, body = get(t, Handler(DB(fixtureDB(t))), "/studio/api/manifest")
	if code != http.StatusNotFound {
		t.Errorf("manifest without option: %d %s", code, body)
	}
}

func TestShellAndFallback(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	code, hdr, body := get(t, h, "/studio/")
	if code != http.StatusOK {
		t.Fatalf("shell: %d", code)
	}
	if !strings.Contains(body, `<base href="/studio/">`) {
		t.Errorf("shell missing base rewrite: %s", body)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("shell content-type %q", ct)
	}
	if cc := hdr.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("shell cache-control %q", cc)
	}
	if csp := hdr.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "default-src 'self'") {
		t.Errorf("CSP %q", csp)
	}
	if hdr.Get("X-Content-Type-Options") != "nosniff" || hdr.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("security headers missing: %v", hdr)
	}

	// Deep links survive reload: every non-file GET is the shell.
	for _, p := range []string{"/studio/runs", "/studio/runs/r_ok", "/studio/agents", "/studio/"} {
		if _, _, b := get(t, h, p); !strings.Contains(b, `<base href="/studio/">`) {
			t.Errorf("%s is not the shell", p)
		}
	}

	// Base and Title rewrite. The handler cannot see the mount prefix
	// (StripPrefix removed it), so Base only decides what the shell
	// says — the caller mounts consistently.
	if _, _, b := get(t, Handler(DB(fixtureDB(t)), Base("/x/")), "/studio/runs/r_ok"); !strings.Contains(b, `<base href="/x/">`) {
		t.Errorf("Base(/x/) not rewritten: %s", b)
	}
	if _, _, b := get(t, Handler(DB(fixtureDB(t)), Base("x")), "/studio/"); !strings.Contains(b, `<base href="/x/">`) {
		t.Errorf("Base(x) not normalized: %s", b)
	}
	if _, _, b := get(t, Handler(DB(fixtureDB(t)), Title("dev studio")), "/studio/"); !strings.Contains(b, "<title>dev studio</title>") {
		t.Errorf("Title not rewritten: %s", b)
	}
	if _, _, b := get(t, Handler(DB(fixtureDB(t)), Title("a <b> & c")), "/studio/"); !strings.Contains(b, "<title>a &lt;b&gt; &amp; c</title>") {
		t.Errorf("Title not escaped: %s", b)
	}
	if code, hdr, _ := get(t, h, "/studio/assets/missing-0000.js"); code != http.StatusNotFound ||
		strings.Contains(hdr.Get("Content-Type"), "text/html") {
		t.Errorf("missing asset: %d %q, want a non-HTML 404", code, hdr.Get("Content-Type"))
	}
}

func TestAssetHeaders(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	// Meaningful once the web build is committed (step 2): hashed
	// assets are immutable, other files revalidate.
	for _, name := range files() {
		_, hdr, _ := get(t, h, "/studio/"+name)
		cc := hdr.Get("Cache-Control")
		if strings.HasPrefix(name, "assets/") {
			if cc != "public, max-age=31536000, immutable" {
				t.Errorf("%s cache-control %q", name, cc)
			}
		} else if cc != "no-cache" {
			t.Errorf("%s cache-control %q", name, cc)
		}
	}
}

func TestHandlerNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Handler(DB(nil)) did not panic")
		}
	}()
	_ = Handler(DB(nil))
}

// TestServerLifecycle pins S4.1's New/Server surface: Handler() is the
// server's handler, Close closes only what New opened, and Runtime is
// nil until step 8.
func TestServerLifecycle(t *testing.T) {
	srv := New(DB(fixtureDB(t)))
	if srv.Runtime() != nil {
		t.Error("Runtime() is not nil before step 8")
	}
	code, _, body := get(t, srv.Handler(), "/studio/api/meta")
	if code != http.StatusOK || !strings.Contains(body, `"studio_version"`) {
		t.Errorf("Server.Handler(): %d %s", code, body)
	}
	// A passed-in DB stays the caller's: Close must not close it (the
	// fixture's cleanup would then fail on a second close, and later
	// reads would hit ErrClosed).
	if err := srv.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := srv.db.Run(context.Background(), "r_ok"); err != nil {
		t.Errorf("Close closed a caller-owned DB: %v", err)
	}

	// What New opened itself, Close closes.
	dir := t.TempDir()
	owned := New(Open(filepath.Join(dir, "owned.db")))
	if err := owned.Close(); err != nil {
		t.Errorf("Close(owned): %v", err)
	}
	if _, err := owned.db.Run(context.Background(), "nope"); !errors.Is(err, obsdb.ErrClosed) {
		t.Errorf("owned DB not closed: %v", err)
	}
}

// TestLiveDefault pins S4.1's Live default: the DB's own hub when it
// implements Hub() (setup A's lane), else a fresh hub.
func TestLiveDefault(t *testing.T) {
	db := fixtureDB(t)
	srv := New(DB(db))
	if srv.live == nil {
		t.Fatal("no live hub")
	}
	if h, ok := db.(interface{ Hub() obsdb.Hub }); !ok || srv.live != h.Hub() {
		t.Error("Live default is not the DB's own hub")
	}
	custom := obsdb.NewHub()
	if s2 := New(DB(db), Live(custom)); s2.live != custom {
		t.Error("Live(h) not honoured")
	}
}

// The CSP hashes must match what the browser computes over the PARSED
// script text: the HTML parser replaces NUL bytes with U+FFFD (the
// router's streamed match ids contain one), so a NUL in an inline
// script must hash identically to its already-replaced form —
// otherwise the script is blocked and the app never boots.
func TestCSPHashMatchesParsedText(t *testing.T) {
	withNUL := cspFor([]byte(`<script>x("a` + "\x00" + `");</script>`))
	withReplacement := cspFor([]byte(`<script>x("a` + "�" + `");</script>`))
	if withNUL != withReplacement {
		t.Errorf("NUL script hashed differently than its parsed form:\n%s\n%s", withNUL, withReplacement)
	}
	if !strings.Contains(withNUL, "'sha256-") {
		t.Errorf("no hash emitted: %s", withNUL)
	}
}
