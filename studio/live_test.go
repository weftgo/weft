package studio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// SSE reading for tests: one buffered scanner over the response body,
// frames collected as they flush.

type sseFrame struct {
	id    string
	event string
	data  string
}

// readSSE reads frames until n arrive, the body closes, or the
// deadline passes.
func readSSE(t *testing.T, resp *http.Response, n int, timeout time.Duration) []sseFrame {
	t.Helper()
	frames := make(chan sseFrame, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur.event != "" || cur.data != "" || cur.id != "" {
					frames <- cur
					cur = sseFrame{}
				}
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
		close(frames)
	}()
	var out []sseFrame
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case f, ok := <-frames:
			if !ok {
				return out
			}
			out = append(out, f)
		case <-deadline:
			return out
		}
	}
	return out
}

// liveSSE opens one /api/live request and returns status and headers
// only — the validation cases read nothing off the stream. Reading
// frames needs a real server (subscribeLive): httptest.NewRecorder
// never flushes.
func liveSSE(t *testing.T, h http.Handler, query, lastEventID string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/studio/api/live"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The handler may have refused before streaming: retry via a
		// plain recorder for the status.
		w := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodGet, "/api/live"+query, nil)
		if lastEventID != "" {
			r2.Header.Set("Last-Event-ID", lastEventID)
		}
		h.ServeHTTP(w, r2)
		return &http.Response{StatusCode: w.Code, Header: w.Header(), Body: io.NopCloser(w.Body)}
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// subscribeLive drives the handler on a real server so the stream
// actually flushes.
func subscribeLive(t *testing.T, h http.Handler, query, lastEventID string) *http.Response {
	t.Helper()
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/studio/api/live"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestLiveSelectorValidation(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	for _, q := range []string{
		"",                       // none
		"?run=a&session=b",       // two
		"?run=a&public_id=p&x=1", // two among noise
		"?run=",                  // empty value: none
	} {
		resp := liveSSE(t, h, q, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", q, resp.StatusCode)
		}
	}
	// Exactly one is fine.
	if resp := liveSSE(t, h, "?run=r_ok", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("one selector: %d", resp.StatusCode)
	}
	// Bad kinds and a bad Last-Event-ID are 400s.
	// kinds= (empty) reads as the default, like an absent parameter.
	for _, q := range []string{"?run=r_ok&kinds=nope", "?run=r_ok&kinds=event,nope"} {
		if resp := liveSSE(t, h, q, ""); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", q, resp.StatusCode)
		}
	}
	if resp := liveSSE(t, h, "?run=r_ok", "not-a-number"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad Last-Event-ID: %d, want 400", resp.StatusCode)
	}
}

// TestLiveRecordAndRunFrames pins the wire shape (S4.5): record
// frames with the hub's Seq as the id and the record's derived
// identity beside its body, run frames with the row, and the kinds
// filter (deltas only when asked; heartbeats never).
func TestLiveRecordAndRunFrames(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))

	resp := subscribeLive(t, h, "?run=r_1&kinds=event,delta,messages,run", "")
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}

	base := time.Date(2026, 10, 1, 9, 12, 5, 410000000, time.UTC)
	publish := func(kind string, pos int64, body string) {
		rec := fxRecord("r_1", kind, "text_delta", pos, base, body)
		if kind == "delta" {
			delete(rec.Attrs, "weft.event.type")
			rec.Attrs["weft.delta.pos"] = pos
		}
		hub.Publish(context.Background(), obsdb.RecordFrame(rec))
	}
	publish("event", 0, `{"type":"run_start","id":"r_1"}`)
	publish("delta", 4, `{"type":"text_delta","run_id":"r_1","text":"Your order"}`)
	publish("messages", 0, `[{"role":"user","content":[]}]`)
	// A heartbeat: never a record frame (S4.5).
	hb := fxRecord("r_1", "heartbeat", "", 0, base, `{}`)
	delete(hb.Attrs, "weft.event.type")
	delete(hb.Attrs, "weft.event.pos")
	hb.Attrs["weft.record"] = "heartbeat"
	hub.Publish(context.Background(), obsdb.RecordFrame(hb))
	// A run frame.
	row := obsdb.RunRow{ID: "r_1", Agent: "orders", SessionID: "s_1", Status: obsdb.StatusRunning}
	hub.Publish(context.Background(), obsdb.RunFrame(row))

	frames := readSSE(t, resp, 4, 5*time.Second)
	var kinds []string
	for i, f := range frames {
		switch i {
		case 0:
			if f.event != "record" || f.id != "1" {
				t.Errorf("frame 0: event %q id %q", f.event, f.id)
			}
			for _, want := range []string{`"run_id":"r_1"`, `"kind":"event"`, `"pos":0`, `"time":"2026-10-01T09:12:05.41Z"`, `"event":{"type":"run_start"`} {
				if !strings.Contains(f.data, want) {
					t.Errorf("frame 0 data misses %s: %s", want, f.data)
				}
			}
		case 1:
			if !strings.Contains(f.data, `"kind":"delta"`) || !strings.Contains(f.data, `"pos":4`) {
				t.Errorf("delta frame: %s", f.data)
			}
		case 2:
			if !strings.Contains(f.data, `"kind":"messages"`) {
				t.Errorf("messages frame: %s", f.data)
			}
		case 3:
			if f.event != "run" || !strings.Contains(f.data, `"run":{`) || !strings.Contains(f.data, `"id":"r_1"`) {
				t.Errorf("run frame: %q %s", f.event, f.data)
			}
		}
		kinds = append(kinds, f.event)
	}
	if len(frames) != 4 {
		t.Fatalf("got %d frames (%v), want 4 (heartbeat excluded)", len(frames), kinds)
	}
}

// TestLiveDefaultKindsExcludesDeltas pins the default (event,run):
// deltas are opt-in (S4.5).
func TestLiveDefaultKindsExcludesDeltas(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))
	resp := subscribeLive(t, h, "?run=r_1", "")

	base := time.Date(2026, 10, 1, 9, 12, 5, 410000000, time.UTC)
	ev := fxRecord("r_1", "event", "run_start", 0, base, `{"type":"run_start","id":"r_1"}`)
	hub.Publish(context.Background(), obsdb.RecordFrame(ev))
	dl := fxRecord("r_1", "delta", "text_delta", 0, base, `{"type":"text_delta","run_id":"r_1","text":"x"}`)
	delete(dl.Attrs, "weft.event.type")
	dl.Attrs["weft.delta.pos"] = 0
	hub.Publish(context.Background(), obsdb.RecordFrame(dl))

	frames := readSSE(t, resp, 2, 2*time.Second)
	if len(frames) != 1 || !strings.Contains(frames[0].data, `"kind":"event"`) {
		t.Fatalf("default kinds forwarded %v, want the event only", frames)
	}
}

// TestLivePing pins the 15 s cadence (tightened for the test).
func TestLivePing(t *testing.T) {
	old := pingEvery
	pingEvery = 30 * time.Millisecond
	t.Cleanup(func() { pingEvery = old })

	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := Handler(DB(db), Live(obsdb.NewHub()))
	resp := subscribeLive(t, h, "?run=r_1", "")
	frames := readSSE(t, resp, 2, 3*time.Second)
	if len(frames) < 2 || frames[0].event != "ping" || frames[0].data != "{}" {
		t.Fatalf("pings: %+v", frames)
	}
}

// TestLiveOverflow pins the drop contract (S4.5): a subscriber whose
// queue overflows is told (event: overflow) and the stream closes;
// the client refetches pages and reconnects.
func TestLiveOverflow(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := obsdb.NewHub(obsdb.QueueSize(1))
	h := Handler(DB(db), Live(hub))
	resp := subscribeLive(t, h, "?run=r_1", "")

	base := time.Date(2026, 10, 1, 9, 12, 5, 410000000, time.UTC)
	for i := 0; i < 8; i++ {
		hub.Publish(context.Background(), obsdb.RecordFrame(
			fxRecord("r_1", "event", "step_start", int64(i), base, `{"type":"step_start"}`)))
	}
	frames := readSSE(t, resp, 99, 3*time.Second)
	if len(frames) == 0 || frames[len(frames)-1].event != "overflow" {
		t.Fatalf("no overflow frame: %+v", frames)
	}
	for _, f := range frames[:len(frames)-1] {
		if f.event == "overflow" {
			t.Fatalf("overflow before the last frame: %+v", frames)
		}
	}
}

// TestLiveResumeExactlyOnce is the S7 step-6 gate: Last-Event-ID
// resume across a two-run session returns every durable record
// exactly once. The session's two runs are stored; the client
// reconnects with the seq it last saw; the backfill delivers every
// durable record of both runs (deltas excluded), no record twice —
// and a record published while the backfill runs is not duplicated
// either.
func TestLiveResumeExactlyOnce(t *testing.T) {
	db := twoRunSessionDB(t)
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))

	// While the resume is being served, live traffic continues: the
	// same record re-published (a transport retry) must not duplicate.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		retry := fxRecord("run_2", "event", "run_finish", 5,
			time.Date(2026, 10, 1, 9, 1, 10, 0, time.UTC),
			`{"type":"run_finish","run_id":"run_2"}`)
		hub.Publish(context.Background(), obsdb.RecordFrame(retry))
	}()

	resp := subscribeLive(t, h, "?session=s_two&kinds=event,messages", "3")
	frames := readSSE(t, resp, 99, 3*time.Second)
	wg.Wait()

	seen := map[liveKey]int{}
	for _, f := range frames {
		if f.event != "record" {
			t.Fatalf("unexpected frame %q", f.event)
		}
		var dto struct {
			RunID string `json:"run_id"`
			Kind  string `json:"kind"`
			Pos   int64  `json:"pos"`
		}
		if err := json.Unmarshal([]byte(f.data), &dto); err != nil {
			t.Fatalf("frame data: %v", err)
		}
		if dto.Kind == "delta" {
			t.Error("a delta was backfilled")
		}
		seen[liveKey{run: dto.RunID, kind: dto.Kind, pos: dto.Pos}]++
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("record %+v delivered %d times", key, n)
		}
	}
	// Every durable record of both runs: the events and the messages.
	for _, run := range []string{"run_1", "run_2"} {
		for pos := int64(0); pos < 6; pos++ {
			if n := seen[liveKey{run: run, kind: "event", pos: pos}]; n != 1 {
				t.Errorf("%s event %d seen %d times", run, pos, n)
			}
		}
		for pos := int64(0); pos < 2; pos++ {
			if n := seen[liveKey{run: run, kind: "messages", pos: pos}]; n != 1 {
				t.Errorf("%s messages %d seen %d times", run, pos, n)
			}
		}
	}
}

// twoRunSessionDB stores two finished runs of one session, public id
// pub_two, agent "orders", plus one delta (never stored, only
// counted) whose live frame the resume must not synthesize.
func twoRunSessionDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for run, minute := range map[string]int{"run_1": 0, "run_2": 1} {
		start := time.Date(2026, 10, 1, 9, minute, 0, 0, time.UTC)
		var recs []obsdb.Record
		for pos := int64(0); pos < 6; pos++ {
			recs = append(recs, fxRecord(run, "event", eventTypes[pos], pos,
				start.Add(time.Duration(pos)*100*time.Millisecond),
				fmt.Sprintf(`{"type":%q,"run_id":%q,"pos":%d}`, eventTypes[pos], run, pos)))
		}
		for pos, body := range []string{
			`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`,
			`[{"role":"assistant","content":[{"type":"text","text":"hello"}]}]`,
		} {
			recs = append(recs, fxRecord(run, "messages", "", int64(pos),
				start.Add(time.Duration(pos)*10*time.Millisecond), body))
		}
		// A delta record: counted, never stored; the resume must not
		// produce a frame for it.
		dl := fxRecord(run, "delta", "text_delta", 0, start.Add(5*time.Millisecond),
			`{"type":"text_delta","run_id":"`+run+`","text":"x"}`)
		delete(dl.Attrs, "weft.event.type")
		dl.Attrs["weft.delta.pos"] = 0
		recs = append(recs, dl)
		for i := range recs {
			recs[i].Attrs["weft.session.id"] = "s_two"
			recs[i].Attrs["weft.public_id"] = "pub_two"
		}
		if err := db.Write(ctx, obsdb.Batch{Records: recs, Spans: []obsdb.Span{
			fxSpanRec(run, start, start.Add(time.Second), 1, "", 10, 4),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

var eventTypes = []string{
	"run_start", "step_start", "tool_start", "tool_finish", "step_finish", "run_finish",
}

// TestLiveIngestToEndToEnd pins the whole lane through the server:
// POST /v1/logs while the database write is blocked, and the record
// frame is already on the subscriber's stream (S4.4's ordering,
// served over S4.5's transport).
func TestLiveIngestToEndToEnd(t *testing.T) {
	inner, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Close() })
	release := make(chan struct{})
	var once sync.Once
	srv := New(DB(&slowWriteDB{DB: inner, release: release}))

	resp := subscribeLive(t, srv.Handler(), "?agent=demo", "")
	pb, err := os.ReadFile("testdata/otlp/logs.pb")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(pb))
		req.RemoteAddr = "127.0.0.1:55123"
		req.Header.Set("Content-Type", "application/x-protobuf")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		once.Do(func() { close(release) })
	}()

	frames := readSSE(t, resp, 1, 5*time.Second)
	if len(frames) == 0 || frames[0].event != "record" {
		t.Fatalf("no record frame while the write was blocked: %+v", frames)
	}
	if !strings.Contains(frames[0].data, `"run_id":"r_20261001_1"`) {
		t.Fatalf("unexpected first frame: %s", frames[0].data)
	}
	once.Do(func() { close(release) })
}

// slowWriteDB blocks Write until its channel closes.
type slowWriteDB struct {
	obsdb.DB
	release chan struct{}
}

func (d *slowWriteDB) Write(ctx context.Context, b obsdb.Batch) error {
	<-d.release
	return d.DB.Write(ctx, b)
}

// A run holding a compaction view (ADR 0028 §8): the catch-up sends
// the growth records under their stored index (never a slot number,
// which would shift past the view and collide with the live frames of
// the records after it), and the view itself is never forwarded — not
// by the catch-up, not by the live lane. Every growth record arrives
// exactly once even when the live lane re-publishes the record after
// the view.
func TestLiveSkipsCompactionViews(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	view := fxRecord("run_v", "messages", "", 1, start.Add(20*time.Millisecond),
		`[{"role":"user","content":[{"type":"text","text":"summary"}]}]`)
	view.Attrs["weft.messages.reason"] = "compacted"
	view.Attrs["weft.messages.from_seq"] = int64(0)
	view.Attrs["weft.messages.to_seq"] = int64(1)
	view.Attrs["weft.compaction.scope"] = "run"
	after := fxRecord("run_v", "messages", "", 2, start.Add(30*time.Millisecond),
		`[{"role":"assistant","content":[{"type":"text","text":"after"}]}]`)
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		fxRecord("run_v", "event", "run_start", 0, start, `{"type":"run_start","run_id":"run_v"}`),
		fxRecord("run_v", "messages", "", 0, start.Add(10*time.Millisecond),
			`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`),
		view, after,
	}}); err != nil {
		t.Fatal(err)
	}
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))
	go func() {
		time.Sleep(50 * time.Millisecond)
		hub.Publish(context.Background(), obsdb.RecordFrame(view))
		hub.Publish(context.Background(), obsdb.RecordFrame(after)) // a transport retry
	}()
	resp := subscribeLive(t, h, "?run=run_v&kinds=messages", "1")
	frames := readSSE(t, resp, 99, 500*time.Millisecond)
	seen := map[int64]int{}
	for _, f := range frames {
		var dto struct {
			Kind string `json:"kind"`
			Pos  int64  `json:"pos"`
		}
		if err := json.Unmarshal([]byte(f.data), &dto); err != nil {
			t.Fatalf("frame data: %v", err)
		}
		if dto.Kind == "messages" {
			seen[dto.Pos]++
		}
	}
	if seen[0] != 1 || seen[2] != 1 || seen[1] != 0 || len(seen) != 2 {
		t.Errorf("messages frames by position = %v, want 0 and 2 once each, never the view (1)", seen)
	}
}
