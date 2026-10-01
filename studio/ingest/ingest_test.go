package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// The step-4 OTLP fixtures (obsdb/testdata) are the round-trip gate's
// input: the same data as protobuf and as JSON must land in identical
// rows, because obsdb.FromOTLP… is the one mapping both the local sink
// and this receiver go through (S3.3, S4.4).

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "obsdb", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func openDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func post(t *testing.T, h http.HandlerFunc, body []byte, hdr map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:55123"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w.Result()
}

func postTraces(t *testing.T, h http.HandlerFunc, body []byte, hdr map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:55123"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w.Result()
}

var always = func(*http.Request) bool { return true }

// TestRoundTripIdenticalRows pins the S7 step-6 gate: the protobuf
// fixture and the JSON fixture, posted through the receiver, produce
// identical rows — logs (records, run rows) and traces (spans).
func TestRoundTripIdenticalRows(t *testing.T) {
	pbDB, jsDB := openDB(t), openDB(t)

	resp := post(t, Logs(pbDB, nil, always), fixture(t, "logs.pb"),
		map[string]string{"Content-Type": "application/x-protobuf"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pb logs: %d", resp.StatusCode)
	}
	resp = post(t, Logs(jsDB, nil, always), fixture(t, "logs.json"),
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("json logs: %d", resp.StatusCode)
	}
	assertSameRows(t, pbDB, jsDB)

	pbDB2, jsDB2 := openDB(t), openDB(t)
	resp = postTraces(t, Traces(pbDB2, nil, always), fixture(t, "traces.pb"),
		map[string]string{"Content-Type": "application/x-protobuf"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pb traces: %d", resp.StatusCode)
	}
	resp = postTraces(t, Traces(jsDB2, nil, always), fixture(t, "traces.json"),
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("json traces: %d", resp.StatusCode)
	}
	assertSameRows(t, pbDB2, jsDB2)
}

func assertSameRows(t *testing.T, a, b obsdb.DB) {
	t.Helper()
	ctx := context.Background()
	pa, err := a.Runs(ctx, obsdb.RunQuery{ParentRunID: "*", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := b.Runs(ctx, obsdb.RunQuery{ParentRunID: "*", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(pa)
	jb, _ := json.Marshal(pb)
	if string(ja) != string(jb) {
		t.Errorf("run rows differ:\n%s\n%s", ja, jb)
	}
	if len(pa.Runs) == 0 {
		t.Errorf("no runs recorded")
	}
	for _, row := range pa.Runs {
		ea, err := a.Events(ctx, row.ID, -1, 1000)
		if err != nil {
			t.Fatal(err)
		}
		eb, err := b.Events(ctx, row.ID, -1, 1000)
		if err != nil {
			t.Fatal(err)
		}
		ja, _ := json.Marshal(ea)
		jb, _ := json.Marshal(eb)
		if string(ja) != string(jb) {
			t.Errorf("events for %s differ:\n%s\n%s", row.ID, ja, jb)
		}
		sa, err := a.RunSpans(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		sb, err := b.RunSpans(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		ja, _ = json.Marshal(sa)
		jb, _ = json.Marshal(sb)
		if string(ja) != string(jb) {
			t.Errorf("spans for %s differ:\n%s\n%s", row.ID, ja, jb)
		}
	}
}

// TestFormats pins the response contract: 200 with an empty
// Export…ServiceResponse in the request's own format, gzip accepted,
// unsupported content types and encodings refused with 415, and a
// malformed body a 400.
func TestFormats(t *testing.T) {
	db := openDB(t)
	h := Logs(db, nil, always)

	// JSON in: the empty response marshals as {}.
	resp := post(t, h, fixture(t, "logs.json"),
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("json: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("json response content-type %q", ct)
	}
	if string(body) != "{}" {
		t.Errorf("json response body %q, want {}", body)
	}

	// Protobuf in: the empty response is empty bytes.
	resp = post(t, h, fixture(t, "logs.pb"),
		map[string]string{"Content-Type": "application/x-protobuf"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pb: %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("pb response content-type %q", ct)
	}
	if len(body) != 0 {
		t.Errorf("pb response body %q, want empty", body)
	}

	// gzip accepted on either format.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(fixture(t, "logs.pb"))
	_ = zw.Close()
	resp = post(t, h, buf.Bytes(), map[string]string{
		"Content-Type": "application/x-protobuf", "Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gzip: %d", resp.StatusCode)
	}

	// Unsupported content type and encoding: 415 unsupported.
	for _, hdr := range []map[string]string{
		{"Content-Type": "text/plain"},
		{"Content-Type": "application/x-protobuf", "Content-Encoding": "br"},
		{"Content-Encoding": "gzip"}, // no content type at all
	} {
		if resp := post(t, h, []byte("x"), hdr); resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%v: %d, want 415", hdr, resp.StatusCode)
		} else {
			b, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(b), `"unsupported"`) {
				t.Errorf("%v: body %s", hdr, b)
			}
		}
	}

	// Malformed body for the declared format: 400 bad_request.
	if resp := post(t, h, []byte("not json"), map[string]string{"Content-Type": "application/json"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: %d, want 400", resp.StatusCode)
	}
	if resp := post(t, h, []byte("not pb"), map[string]string{"Content-Type": "application/x-protobuf"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad pb: %d, want 400", resp.StatusCode)
	}
}

// TestBodyLimit pins the 16 MiB-after-decompression limit: above it a
// 413, below it a 200 (S4.4).
func TestBodyLimit(t *testing.T) {
	db := openDB(t)
	h := Logs(db, nil, always)

	// A valid OTLP/JSON body whose one attribute value blows past the
	// limit once decompressed: gzip keeps the wire small, so the gate
	// really measures the decompressed size.
	big := strings.Repeat("a", MaxBody+1024)
	body := fmt.Sprintf(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{
		"body":{"stringValue":"x"},
		"attributes":[{"key":"big","value":{"stringValue":%q}}]}]}]}]}`, big)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(body))
	_ = zw.Close()
	resp := post(t, h, buf.Bytes(), map[string]string{
		"Content-Type": "application/json", "Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over limit: %d, want 413", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"bad_request"`) || !strings.Contains(string(b), "16 MiB") {
		t.Errorf("413 body: %s", b)
	}

	// Just under the limit passes the size gate (decoding may still
	// fail; here it succeeds).
	ok := fmt.Sprintf(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{
		"body":{"stringValue":"x"},
		"attributes":[{"key":"big","value":{"stringValue":%q}}]}]}]}]}`,
		strings.Repeat("a", 1024))
	resp = post(t, h, []byte(ok), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("under limit: %d, want 200", resp.StatusCode)
	}
}

// failingDB fails every Write, so the receiver must answer 503
// unavailable (the exporter's retry signal, S4.4).
type failingDB struct {
	obsdb.DB
}

func (failingDB) Write(context.Context, obsdb.Batch) error {
	return fmt.Errorf("disk full")
}

func TestWriteFailure503(t *testing.T) {
	h := Logs(failingDB{}, nil, always)
	resp := post(t, h, fixture(t, "logs.pb"),
		map[string]string{"Content-Type": "application/x-protobuf"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("write failure: %d, want 503", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"unavailable"`) {
		t.Errorf("503 body: %s", b)
	}
}

// slowDB blocks in Write until released, so the test can prove the
// record reached the hub before the insert (S4.4's ordering — the
// live tail never waits on the database).
type slowDB struct {
	obsdb.DB
	release chan struct{}
	once    sync.Once
}

func (s *slowDB) Write(ctx context.Context, b obsdb.Batch) error {
	<-s.release
	return s.DB.Write(ctx, b)
}

func TestFramesBeforeWrite(t *testing.T) {
	inner := openDB(t)
	db := &slowDB{DB: inner, release: make(chan struct{})}
	hub := obsdb.NewHub()
	h := Logs(db, hub, always)

	// An in-process subscriber sees the record while Write is still
	// blocked — publish happened first.
	sawFrame := make(chan obsdb.Frame, 4)
	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, err := hub.Subscribe(subCtx, obsdb.Selector{Agent: "demo"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for f := range frames {
			sawFrame <- f
		}
	}()

	done := make(chan *http.Response, 1)
	go func() {
		done <- post(t, h, fixture(t, "logs.pb"),
			map[string]string{"Content-Type": "application/x-protobuf"})
	}()

	select {
	case f := <-sawFrame:
		if f.Kind != obsdb.FrameRecord || f.Record == nil {
			t.Fatalf("first frame is %q, want a record", f.Kind)
		}
		// The database is still blocked: nothing has been written.
		if n := countRows(t, inner); n != 0 {
			t.Fatalf("%d rows already written before the frame was seen", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame while Write blocked: publish must precede the insert")
	}
	db.once.Do(func() { close(db.release) })
	resp := <-done
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after release: %d", resp.StatusCode)
	}
	if n := countRows(t, inner); n == 0 {
		t.Fatal("nothing written after release")
	}
}

func countRows(t *testing.T, db obsdb.DB) int {
	t.Helper()
	page, err := db.Runs(context.Background(), obsdb.RunQuery{ParentRunID: "*", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	n := len(page.Runs)
	for _, r := range page.Runs {
		ev, err := db.Events(context.Background(), r.ID, -1, 1000)
		if err != nil {
			t.Fatal(err)
		}
		n += len(ev.Events)
	}
	return n
}
