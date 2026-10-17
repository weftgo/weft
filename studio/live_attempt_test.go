package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// attemptSpan is one `attempt` span of run, step 0 (docs/record-contract.md
// §3): failed with error.type and a retry-after ask, or ok.
func attemptSpan(run, id string, index int64, failed bool, at time.Time) obsdb.Span {
	sp := obsdb.Span{
		TraceID: "0123456789abcdef0123456789abcdef", SpanID: id, ParentSpanID: "c0c0c0c0c0c0c0c0",
		Name: "attempt", Kind: 3, Start: at, End: at.Add(40 * time.Millisecond), StatusCode: 1,
		Attrs: map[string]any{
			"weft.run.id": run, "weft.step.index": int64(0), "weft.attempt.index": index,
			"gen_ai.provider.name": "wefttest", "gen_ai.request.model": "script",
			"weft.public_id": "pub_a",
		},
	}
	if failed {
		sp.StatusCode = 2
		sp.Attrs["error.type"] = "rate_limited"
		sp.Attrs["weft.attempt.retry_after_ms"] = int64(4000)
		sp.Attrs["weft.attempt.wait_ms"] = int64(4100)
	}
	return sp
}

// TestLiveAttemptFrames pins O17 (UX-E-30): an attempt span is an
// `event: attempt` frame the moment it lands — run, step, attempt
// index, status, error type, the provider's wait — only when the
// stream asks for kind attempt; a re-written batch (a retried
// transport) is one frame; the chat span beside it is none.
func TestLiveAttemptFrames(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := Handler(DB(db)) // the database's own hub: setup A
	resp := subscribeLive(t, h, "?run=r_att&kinds=attempt", "")
	other := subscribeLive(t, h, "?run=r_att&kinds=event,run", "")

	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	chat := obsdb.Span{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "c0c0c0c0c0c0c0c0", Name: "chat",
		Start: at, End: at.Add(time.Second), Attrs: map[string]any{"weft.run.id": "r_att", "weft.step.index": int64(0)}}
	first := obsdb.Batch{Spans: []obsdb.Span{attemptSpan("r_att", "a1a1a1a1a1a1a1a1", 1, true, at), chat}}
	ctx := context.Background()
	for range 2 { // the same batch twice: one frame
		if err := db.Write(ctx, first); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{attemptSpan("r_att", "a2a2a2a2a2a2a2a2", 2, false, at.Add(time.Second))}}); err != nil {
		t.Fatal(err)
	}

	frames := framesOf(readSSE(t, resp, 3, 2*time.Second), "attempt")
	if len(frames) != 2 {
		t.Fatalf("got %d attempt frames, want 2 (the re-written batch deduped): %+v", len(frames), frames)
	}
	var a1, a2 map[string]any
	if err := json.Unmarshal([]byte(frames[0].data), &a1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(frames[1].data), &a2); err != nil {
		t.Fatal(err)
	}
	want1 := map[string]any{"run_id": "r_att", "step": 0.0, "attempt": 1.0, "status": "error",
		"error_type": "rate_limited", "wait_ms": 4100.0, "retry_after_ms": 4000.0, "public_id": "pub_a", "model": "script", "provider": "wefttest"}
	for k, v := range want1 {
		if a1[k] != v {
			t.Errorf("first attempt frame %s = %v, want %v (%s)", k, a1[k], v, frames[0].data)
		}
	}
	if a2["attempt"] != 2.0 || a2["status"] != "ok" || a2["wait_ms"] != nil || a2["retry_after_ms"] != nil || a2["error_type"] != nil {
		t.Errorf("second attempt frame: %s", frames[1].data)
	}
	if frames[0].id == "" {
		t.Error("an attempt frame carries the hub's seq as its id")
	}
	// The default kinds never carry it.
	for _, f := range readSSE(t, other, 4, 300*time.Millisecond) {
		if f.event == "attempt" {
			t.Errorf("an event,run stream got an attempt frame: %s", f.data)
		}
	}
}

// TestLiveAttemptFramesStayInScope (the auth matrix's kinds=attempt
// rows, P8-gate-fix): a panel token's attempt stream, opened by a
// grant on a run nothing has stored yet, receives no attempt frame of
// another public id's run — or of one with none — and its own public
// id's frames flow; the server token's stream reads them all.
func TestLiveAttemptFramesStayInScope(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := Handler(DB(db), Token("srv"))
	for _, sc := range []string{scopeRead, scopePlayground} {
		tok, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_mine", Scope: sc, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		run := "r_future_" + sc
		q := "run=" + run + "&kinds=attempt"
		resp := subscribeLive(t, h, "?"+q+"&sig="+mustGrant(t, h, tok, q), "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", sc, resp.StatusCode)
		}
		at := time.Now().UTC()
		foreign := attemptSpan(run, "f1f1f1f1f1f1f1f1", 1, true, at)
		foreign.Attrs["weft.public_id"] = "pub_other"
		bare := attemptSpan(run, "f2f2f2f2f2f2f2f2", 2, true, at)
		delete(bare.Attrs, "weft.public_id")
		if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{foreign, bare}}); err != nil {
			t.Fatal(err)
		}
		if frames := readSSE(t, resp, 1, 400*time.Millisecond); len(frames) != 0 {
			t.Errorf("%s: a pub_mine token received another public id's attempt frame: %+v", sc, frames[0])
		}
		_ = resp.Body.Close()

		own := "r_own_" + sc
		q = "run=" + own + "&kinds=attempt"
		resp = subscribeLive(t, h, "?"+q+"&sig="+mustGrant(t, h, tok, q), "")
		mine := attemptSpan(own, "f3f3f3f3f3f3f3f3", 1, true, at)
		mine.Attrs["weft.public_id"] = "pub_mine"
		if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{mine}}); err != nil {
			t.Fatal(err)
		}
		if frames := framesOf(readSSE(t, resp, 1, 2*time.Second), "attempt"); len(frames) != 1 || !strings.Contains(frames[0].data, `"public_id":"pub_mine"`) {
			t.Errorf("%s: own attempt frames = %+v", sc, frames)
		}
		_ = resp.Body.Close()
	}
}

// TestLiveAttemptFramesThroughIngest: OTLP ingest publishes the attempt
// spans of a /v1/traces batch to a hub the database does not own (a
// ClickHouse-shaped setup B).
func TestLiveAttemptFramesThroughIngest(t *testing.T) {
	inner, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Close() })
	hub := obsdb.NewHub()
	srv := New(DB(noHubDB{inner}), Live(hub))
	t.Cleanup(func() { _ = srv.Close() })
	resp := subscribeLive(t, srv.Handler(), "?run=r_att&kinds=attempt", "")

	body := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"app"}}]},"scopeSpans":[{"spans":[{
	  "traceId":"0123456789abcdef0123456789abcdef","spanId":"a1a1a1a1a1a1a1a1","parentSpanId":"c0c0c0c0c0c0c0c0",
	  "name":"attempt","kind":3,"startTimeUnixNano":"1792227600000000000","endTimeUnixNano":"1792227600040000000",
	  "status":{"code":2},
	  "attributes":[{"key":"weft.run.id","value":{"stringValue":"r_att"}},{"key":"weft.step.index","value":{"intValue":"1"}},
	    {"key":"weft.attempt.index","value":{"intValue":"1"}},{"key":"weft.attempt.layer","value":{"stringValue":"fallback"}},
	    {"key":"error.type","value":{"stringValue":"overloaded"}}]}]}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte(body)))
	req.RemoteAddr = "127.0.0.1:55123"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}
	frames := framesOf(readSSE(t, resp, 1, 2*time.Second), "attempt")
	if len(frames) != 1 {
		t.Fatalf("no attempt frame from ingest: %+v", frames)
	}
	for _, want := range []string{`"step":1`, `"attempt":1`, `"layer":"fallback"`, `"status":"error"`, `"error_type":"overloaded"`} {
		if !strings.Contains(frames[0].data, want) {
			t.Errorf("attempt frame misses %s: %s", want, frames[0].data)
		}
	}
}

// noHubDB hides the sqlite database's Hub(): the ingest path publishes.
type noHubDB struct{ obsdb.DB }
