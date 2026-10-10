package studio

// The requests row's raw (ADR 0028 §12, plan A6.1): the attempt's wire
// exchange inline, null with not_recorded where the run holds none, and
// never to a read-scoped panel token (the route refuses it, hidden).

import (
	"context"
	"encoding/json"
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
