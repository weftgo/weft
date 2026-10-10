package studio

// The requests row's raw (ADR 0028 §12, plan A6.1): the attempt's wire
// exchange inline, null with not_recorded where the run holds none, and
// never to a read-scoped panel token (the route refuses it, hidden).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

func seedRawRun(t *testing.T, db obsdb.DB) {
	t.Helper()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	rec := func(n int, kind, event, body string, attrs map[string]any) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": "run_raw", "weft.public_id": "pub_a"}
		for k, v := range attrs {
			a[k] = v
		}
		return obsdb.Record{Time: at.Add(time.Duration(n) * time.Millisecond), EventName: event, Severity: 9,
			Body: body, Service: "svc", Attrs: a, Resource: map[string]any{"service.name": "svc"}}
	}
	recs := []obsdb.Record{
		rec(0, "event", "weft.event", `{"type":"run_start","id":"run_raw","model":{"provider":"anthropic","name":"m"}}`,
			map[string]any{"weft.event.type": "run_start", "weft.event.pos": int64(0), "weft.instructions.hash": "abc"}),
		rec(1, "request", "weft.request", `{"step":0,"attempt":1,"system_hash":"","messages_ref":{"count":1},"tools":{"catalog_hash":"","names":[]},"sequential_tools":false,"params":{},"model":{"provider":"anthropic","name":"m"},"stream":true}`,
			map[string]any{"weft.content": "full", "weft.request.index": int64(0), "weft.step.index": int64(0), "weft.attempt.index": int64(1)}),
		rec(2, "raw", "weft.raw", `{"step":0,"attempt":1,"request":{"method":"POST","url":"https://api.example/v1/messages","headers":{"Content-Type":["application/json"]},"media_type":"application/json","body":"{\"wire\":\"WIRE-BODY\"}","bytes":19},"response":{"status":200,"headers":{},"media_type":"text/event-stream","body":"data: ok\n\n","bytes":10},"redacted_headers":["x-api-key"]}`,
			map[string]any{"weft.content": "full", "weft.raw.index": int64(0), "weft.step.index": int64(0), "weft.attempt.index": int64(1)}),
		rec(3, "event", "weft.event", `{"type":"run_finish","run_id":"run_raw","usage":{},"steps":1}`,
			map[string]any{"weft.event.type": "run_finish", "weft.event.pos": int64(1)}),
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestsRowCarriesRaw(t *testing.T) {
	const tok = "server-token"
	srv := New(Open(t.TempDir()+"/raw.db"), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	seedRawRun(t, srv.db)
	get := func(bearer string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/run_raw/requests", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode, readAll(t, resp)
	}
	check := func(who string, code int, body string) {
		t.Helper()
		var page struct {
			Requests []struct {
				Raw      *obsdb.RawRecord `json:"raw"`
				RawBadge string           `json:"raw_badge"`
			} `json:"requests"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil || code != http.StatusOK || len(page.Requests) != 1 {
			t.Fatalf("%s: %d %v %s", who, code, err, body)
		}
		r := page.Requests[0]
		if r.Raw == nil || r.RawBadge != "" || r.Raw.Request.Body != `{"wire":"WIRE-BODY"}` ||
			r.Raw.Response == nil || r.Raw.Response.Body != "data: ok\n\n" || r.Raw.RedactedHeaders[0] != "x-api-key" {
			t.Errorf("%s: row raw = %+v badge %q", who, r.Raw, r.RawBadge)
		}
	}
	code, body := get(tok)
	check("server token", code, body)
	play, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, body = get(play)
	check("playground token", code, body)
	read, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, body = get(read)
	if code != http.StatusForbidden || !strings.Contains(body, `"hidden"`) || strings.Contains(body, "WIRE-BODY") {
		t.Errorf("read token = %d %s; want 403 hidden without the wire body", code, body)
	}
}

// seedRawGoldenRun is requests-raw.golden.json's run: step 0 retried
// (attempt 1's response not read to its end, its unread rest counted;
// attempt 2's request cut by the cap and its response binary, base64),
// step 1's exchange without a response (a transport error), and step
// 2's raw record malformed — obsdb reads it as derived, AttachRaw
// attaches no derived record, so the row is raw null, not_recorded.
func seedRawGoldenRun(t *testing.T, db obsdb.DB) {
	t.Helper()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	n := 0
	rec := func(kind, event, body string, attrs map[string]any) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": "run_rawg", "weft.public_id": "pub_a"}
		for k, v := range attrs {
			a[k] = v
		}
		n++
		return obsdb.Record{Time: at.Add(time.Duration(n) * time.Millisecond), EventName: event, Severity: 9,
			Body: body, Service: "svc", Attrs: a, Resource: map[string]any{"service.name": "svc"}}
	}
	request := func(idx int64, step int, attempt int64) obsdb.Record {
		return rec("request", "weft.request",
			fmt.Sprintf(`{"step":%d,"attempt":%d,"system_hash":"","messages_ref":{"count":1},"tools":{"catalog_hash":"","names":[]},"sequential_tools":false,"params":{},"model":{"provider":"anthropic","name":"m"},"stream":true}`, step, attempt),
			map[string]any{"weft.content": "full", "weft.request.index": idx, "weft.step.index": int64(step), "weft.attempt.index": attempt})
	}
	raw := func(idx int64, step int, attempt int64, body string, cut int64) obsdb.Record {
		a := map[string]any{"weft.content": "full", "weft.raw.index": idx, "weft.step.index": int64(step), "weft.attempt.index": attempt}
		if cut > 0 {
			a["weft.content.truncated_bytes"] = cut
		}
		return rec("raw", "weft.raw", body, a)
	}
	recs := []obsdb.Record{
		rec("event", "weft.event", `{"type":"run_start","id":"run_rawg","model":{"provider":"anthropic","name":"m"}}`,
			map[string]any{"weft.event.type": "run_start", "weft.event.pos": int64(0), "weft.instructions.hash": "abc"}),
		request(0, 0, 1),
		raw(0, 0, 1, `{"step":0,"attempt":1,"request":{"method":"POST","url":"https://api.example/v1/messages?key=REDACTED","headers":{"Anthropic-Version":["2023-06-01"],"Content-Type":["application/json"]},"media_type":"application/json","body":"{\"model\":\"m\",\"seed\":9007199254740993,\"temperature\":1.0}","bytes":56},"response":{"status":200,"headers":{"Content-Type":["text/event-stream"]},"media_type":"text/event-stream","body":"event: message_start\ndata: {}\n\n","bytes":2079,"truncated_bytes":2048,"complete":false},"redacted_headers":["authorization","x-api-key"],"redacted_query":["key"]}`, 2048),
		request(1, 0, 2),
		raw(1, 0, 2, `{"step":0,"attempt":2,"request":{"method":"POST","url":"https://api.example/v1/messages","headers":{"Content-Type":["application/json"]},"media_type":"application/json","body":"{\"model\":\"m\",\"messages\":[","bytes":266240,"truncated_bytes":4096},"response":{"status":200,"headers":{"Content-Type":["application/octet-stream"]},"media_type":"application/octet-stream","body":"AAEC/w==","encoding":"base64","bytes":4,"complete":true},"redacted_headers":["x-api-key"]}`, 4096),
		request(2, 1, 1),
		raw(2, 1, 1, `{"step":1,"attempt":1,"request":{"method":"POST","url":"https://api.example/v1/messages","headers":{},"media_type":"application/json","body":"{}","bytes":2},"response":null,"redacted_headers":[]}`, 0),
		request(3, 2, 1),
		raw(3, 2, 1, `{"step":2,"attempt":1,"request":`, 0),
		rec("event", "weft.event", `{"type":"run_finish","run_id":"run_rawg","usage":{},"steps":3}`,
			map[string]any{"weft.event.type": "run_finish", "weft.event.pos": int64(1)}),
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
}

// TestRequestsRawGolden pins the requests row's raw (plan A6) as the web
// client reads it (src/lib/api.ts's RawRecord/RawSide, the Wire tab's
// tests read this golden): every field of both sides, a response not
// read to its end, a cut request, a base64 body, a missing response,
// and a malformed record's row (raw null, not_recorded, raw_off).
func TestRequestsRawGolden(t *testing.T) {
	srv := New(Open(t.TempDir() + "/rawg.db"))
	t.Cleanup(func() { _ = srv.Close() })
	seedRawGoldenRun(t, srv.db)
	code, _, body := get(t, srv.Handler(), "/studio/api/runs/run_rawg/requests?refs=1")
	if code != http.StatusOK {
		t.Fatalf("requests: %d %s", code, body)
	}
	requestsGolden(t, "requests-raw.golden.json", body)
	var page struct {
		Requests []struct {
			Raw      *obsdb.RawRecord `json:"raw"`
			RawBadge string           `json:"raw_badge"`
		} `json:"requests"`
	}
	decode(t, body, &page)
	if len(page.Requests) != 4 {
		t.Fatalf("rows = %d, want 4: %s", len(page.Requests), body)
	}
	r := page.Requests
	if r[0].Raw == nil || r[0].Raw.Response == nil || r[0].Raw.Response.Complete == nil || *r[0].Raw.Response.Complete ||
		r[0].Raw.Content != obsdb.HoleTruncated || len(r[0].Raw.RedactedQuery) != 1 {
		t.Errorf("row 0 raw = %+v", r[0].Raw)
	}
	if r[1].Raw == nil || r[1].Raw.Response == nil || r[1].Raw.Response.Encoding != "base64" || r[1].Raw.Request.TruncatedBytes != 4096 {
		t.Errorf("row 1 raw = %+v", r[1].Raw)
	}
	if r[2].Raw == nil || r[2].Raw.Response != nil {
		t.Errorf("row 2 raw = %+v, want a raw without a response", r[2].Raw)
	}
	if r[3].Raw != nil || r[3].RawBadge != string(obsdb.HoleNotRecorded) {
		t.Errorf("row 3 = %+v %q, want the malformed record's row raw null, not_recorded", r[3].Raw, r[3].RawBadge)
	}
}
