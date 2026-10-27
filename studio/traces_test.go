package studio

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The trace-list fixture (plan A7's Done line): four traces on one
// database, one fixture second apart.
//
//   - tlParent (t0): run tl_parent's invoke_agent root, its
//     execute_tool span and the subagent child tl_parent/0/call_1's
//     invoke_agent span under it (ended in error) — two runs, one
//     trace, every span pub_a's;
//   - tlWeb (t0+10s), through OTLP ingest: a non-weft root "POST /chat"
//     (service edge-svc, no weft attribute) with run tl_web's
//     invoke_agent span (pub_a) under it;
//   - tlOrphan (t0+20s): two spans whose parent never arrived — the
//     root is missing — service orphan-svc;
//   - tlOther (t0+30s): run tl_other, public id pub_b.
const (
	tlParent = "1a000000000000000000000000000001"
	tlWeb    = "2b000000000000000000000000000002"
	tlOrphan = "3c000000000000000000000000000003"
	tlOther  = "4d000000000000000000000000000004"
)

func tlSpan(trace, span, parent, name, service string, start, end time.Duration, status int, attrs map[string]any) obsdb.Span {
	if attrs == nil {
		attrs = map[string]any{}
	}
	return obsdb.Span{
		TraceID: trace, SpanID: span, ParentSpanID: parent, Name: name, Kind: 1,
		Start: fixtureT0.Add(start), End: fixtureT0.Add(end), StatusCode: status, StatusMessage: "",
		Service: service, Attrs: attrs, Resource: map[string]any{"service.name": service},
	}
}

func tlWeft(run, public, op string, extra map[string]any) map[string]any {
	a := map[string]any{"gen_ai.operation.name": op, "weft.run.id": run, "weft.public_id": public, "gen_ai.agent.name": "orders",
		// A read-scoped token never reads these on spans (spansFor);
		// the list carries no attribute at all.
		"weft.override.tools": "secret_only_tool"}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// b64 is an OTLP/JSON id: the hex id's bytes, base64 (protojson).
func b64(t *testing.T, id string) string {
	t.Helper()
	b, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// seedTraces writes the fixture through db (and the OTLP trace through
// the handler's ingest).
func seedTraces(t *testing.T, db obsdb.DB, h http.Handler, prefix string) {
	t.Helper()
	const s = time.Second
	if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{
		tlSpan(tlParent, "1a00000000000001", "", "invoke_agent orders", "studio-test", 0, 5*s, 1, tlWeft("tl_parent", "pub_a", "invoke_agent", nil)),
		tlSpan(tlParent, "1a00000000000002", "1a00000000000001", "execute_tool research", "studio-test", s, 4*s, 0, tlWeft("tl_parent", "pub_a", "execute_tool", nil)),
		tlSpan(tlParent, "1a00000000000003", "1a00000000000002", "invoke_agent researcher", "studio-test", 2*s, 3*s, 2,
			tlWeft("tl_parent/0/call_1", "pub_a", "invoke_agent", map[string]any{"weft.parent.run.id": "tl_parent", "weft.parent.call.id": "call_1"})),
		tlSpan(tlOrphan, "3c00000000000001", "3c000000000000ff", "work", "orphan-svc", 20*s, 23*s, 0, nil),
		tlSpan(tlOrphan, "3c00000000000002", "3c000000000000ff", "work", "orphan-svc", 21*s, 22*s, 0, nil),
		tlSpan(tlOther, "4d00000000000001", "", "invoke_agent orders", "studio-test", 30*s, 31*s, 1, tlWeft("tl_other", "pub_b", "invoke_agent", nil)),
	}}); err != nil {
		t.Fatal(err)
	}
	ns := func(d time.Duration) string { return fmt.Sprint(fixtureT0.Add(d).UnixNano()) }
	otlp := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"edge-svc"}}]},
		"scopeSpans":[{"scope":{"name":"net/http"},"spans":[{"traceId":"` + b64(t, tlWeb) + `","spanId":"` + b64(t, "2b00000000000001") + `",
		"name":"POST /chat","kind":"SPAN_KIND_SERVER","startTimeUnixNano":"` + ns(10*s) + `","endTimeUnixNano":"` + ns(13*s) + `",
		"attributes":[{"key":"http.request.method","value":{"stringValue":"POST"}}],"status":{}}]}]},
		{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"studio-test"}}]},
		"scopeSpans":[{"scope":{"name":"github.com/weftgo/weft"},"spans":[{"traceId":"` + b64(t, tlWeb) + `","spanId":"` + b64(t, "2b00000000000002") + `",
		"parentSpanId":"` + b64(t, "2b00000000000001") + `","name":"invoke_agent orders","kind":"SPAN_KIND_INTERNAL",
		"startTimeUnixNano":"` + ns(10*s+s/2) + `","endTimeUnixNano":"` + ns(12*s) + `",
		"attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"invoke_agent"}},{"key":"weft.run.id","value":{"stringValue":"tl_web"}},
		{"key":"weft.public_id","value":{"stringValue":"pub_a"}},{"key":"gen_ai.agent.name","value":{"stringValue":"orders"}}],
		"status":{"code":"STATUS_CODE_OK"}}]}]}]}`
	if code, _, body := post(t, h, prefix+"/v1/traces", "application/json", otlp); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, body)
	}
}

// tracesFixture is a setup-A server over a fresh file database holding
// the fixture.
func tracesFixture(t *testing.T) http.Handler {
	t.Helper()
	srv := New(Open(t.TempDir() + "/traces.db"))
	t.Cleanup(func() { _ = srv.Close() })
	seedTraces(t, srv.db, srv.Handler(), "/studio")
	return srv.Handler()
}

type traceListDoc struct {
	Total  int `json:"total"`
	Traces []struct {
		TraceID string `json:"trace_id"`
		Root    *struct {
			SpanID string `json:"span_id"`
			Name   string `json:"name"`
		} `json:"root"`
		Service    string   `json:"service"`
		DurationMs int64    `json:"duration_ms"`
		SpanCount  int      `json:"span_count"`
		Error      bool     `json:"error"`
		RunIDs     []string `json:"run_ids"`
		Badge      string   `json:"badge"`
	} `json:"traces"`
	NextBefore   *time.Time `json:"next_before"`
	NextBeforeID *string    `json:"next_before_id"`
}

func traceList(t *testing.T, h http.Handler, path string) traceListDoc {
	t.Helper()
	code, _, body := get(t, h, path)
	if code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, code, body)
	}
	var doc traceListDoc
	decode(t, body, &doc)
	return doc
}

func (d traceListDoc) ids() []string {
	out := []string{}
	for _, tr := range d.Traces {
		out = append(out, tr.TraceID)
	}
	return out
}

// TestTracesGolden pins GET /api/traces's shape (plan A7) over the
// fixture: newest first, the span counts and run ids each trace holds
// (the subagent child beside its parent, the OTLP-ingested trace's
// run under its non-weft root), the missing root as null with the gap
// badge, error from any span.
func TestTracesGolden(t *testing.T) {
	h := tracesFixture(t)
	code, _, body := get(t, h, "/studio/api/traces")
	if code != http.StatusOK {
		t.Fatalf("traces: %d %s", code, body)
	}
	golden(t, "traces.golden.json", body)
	doc := traceList(t, h, "/studio/api/traces")
	if got, want := doc.ids(), []string{tlOther, tlOrphan, tlWeb, tlParent}; !reflect.DeepEqual(got, want) || doc.Total != 4 || doc.NextBefore != nil || doc.NextBeforeID != nil {
		t.Fatalf("traces = %v total %d next %v/%v, want %v, one page", got, doc.Total, doc.NextBefore, doc.NextBeforeID, want)
	}
	type row struct {
		root, service string
		ms            int64
		spans         int
		err           bool
		runs          []string
		badge         string
	}
	want := map[string]row{
		tlParent: {"invoke_agent orders", "studio-test", 5000, 3, true, []string{"tl_parent", "tl_parent/0/call_1"}, ""},
		tlWeb:    {"POST /chat", "edge-svc", 3000, 2, false, []string{"tl_web"}, ""},
		tlOrphan: {"", "orphan-svc", 3000, 2, false, []string{}, "gap"},
		tlOther:  {"invoke_agent orders", "studio-test", 1000, 1, false, []string{"tl_other"}, ""},
	}
	for _, tr := range doc.Traces {
		got := row{"", tr.Service, tr.DurationMs, tr.SpanCount, tr.Error, tr.RunIDs, tr.Badge}
		if tr.Root != nil {
			got.root = tr.Root.Name
		}
		if w := want[tr.TraceID]; !reflect.DeepEqual(got, w) {
			t.Errorf("%s = %+v, want %+v", tr.TraceID, got, w)
		}
	}
	if strings.Contains(body, "secret_only_tool") || strings.Contains(body, "attrs") {
		t.Errorf("the list carries span attributes: %s", body)
	}
}

// TestTracesRoute pins the parameters: since/until on the trace's
// start (its earliest span), service on any span, the cursor, and the
// 400s.
func TestTracesRoute(t *testing.T) {
	h := tracesFixture(t)
	at := func(d time.Duration) string { return fixtureT0.Add(d).Format(time.RFC3339Nano) }
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"since=" + at(15*time.Second), []string{tlOther, tlOrphan}},
		{"since=" + at(time.Second), []string{tlOther, tlOrphan, tlWeb}}, // tlParent's later spans do not move its start
		{"until=" + at(15*time.Second), []string{tlWeb, tlParent}},
		{"since=" + at(5*time.Second) + "&until=" + at(25*time.Second), []string{tlOrphan, tlWeb}},
		{"service=edge-svc", []string{tlWeb}},
		{"service=studio-test", []string{tlOther, tlWeb, tlParent}}, // any span: tlWeb's run under its edge root
		{"service=orphan-svc&since=" + at(15*time.Second), []string{tlOrphan}},
		{"service=studio", []string{}},          // exact
		{"public_id=pub_a", []string{tlParent}}, // every span pub_a's: tlWeb's non-weft root is not
		{"public_id=pub_b", []string{tlOther}},
		// id_prefix (UX-A-13: ⌘K finds a trace by a pasted short id).
		{"id_prefix=" + tlWeb[:8], []string{tlWeb}},
		{"id_prefix=" + tlWeb, []string{tlWeb}},
		{"id_prefix=" + tlWeb[:8] + "&service=orphan-svc", []string{}},
	} {
		doc := traceList(t, h, "/studio/api/traces?"+strings.ReplaceAll(c.query, "+", "%2B"))
		if got := doc.ids(); !reflect.DeepEqual(got, c.want) || doc.Total != len(c.want) {
			t.Errorf("?%s = %v total %d, want %v", c.query, got, doc.Total, c.want)
		}
	}
	// Two at a time: the (before, before_id) pair walks every trace once.
	var walked []string
	path := "/studio/api/traces?limit=2"
	for range 4 {
		doc := traceList(t, h, path)
		walked = append(walked, doc.ids()...)
		if doc.NextBefore == nil {
			break
		}
		path = "/studio/api/traces?limit=2&before=" + doc.NextBefore.Format(time.RFC3339Nano) + "&before_id=" + *doc.NextBeforeID
		path = strings.ReplaceAll(path, "+", "%2B")
	}
	if want := []string{tlOther, tlOrphan, tlWeb, tlParent}; !reflect.DeepEqual(walked, want) {
		t.Errorf("paged = %v, want %v", walked, want)
	}
	// before_id without before is ignored: the first page, not a cursor.
	if doc := traceList(t, h, "/studio/api/traces?limit=2&before_id="+tlOrphan); !reflect.DeepEqual(doc.ids(), []string{tlOther, tlOrphan}) {
		t.Errorf("before_id alone = %v, want the first page [%s %s]", doc.ids(), tlOther, tlOrphan)
	}
	for _, q := range []string{"since=yesterday", "until=1", "before=x", "limit=-1", "limit=x"} {
		if code, _, body := get(t, h, "/studio/api/traces?"+q); code != http.StatusBadRequest {
			t.Errorf("?%s = %d %s, want 400", q, code, body)
		}
	}
	// The bare path, never a redirect to the /api/traces/ subtree.
	if code, _, _ := get(t, h, "/studio/api/traces/"); code != http.StatusNotFound {
		t.Errorf("/api/traces/ = %d, want 404 (no trace id)", code)
	}
}

// TestTracesPanelScope: a panel token lists only the traces of its
// public id — every span its, the rule /api/traces/{id} checks, so
// each listed trace opens and no other public id's run id is listed —
// and its read scope has nothing to redact: a row carries no span
// attribute (the override tool names spansFor drops are not there to
// begin with).
func TestTracesPanelScope(t *testing.T) {
	const tok = "srv-traces"
	srv := New(Open(t.TempDir()+"/traces.db"), Token(tok), IngestToken("ing"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ingest := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer ing")
		srv.Handler().ServeHTTP(w, r)
	})
	seedTraces(t, srv.db, ingest, "/studio")
	read := func(path, bearer string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode, readAll(t, resp)
	}
	for _, scope := range []string{scopeRead, scopePlayground} {
		pt, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/traces", "/api/traces?service=studio-test", "/api/traces?public_id=pub_a"} {
			code, body := read(path, pt)
			var doc traceListDoc
			decode(t, body, &doc)
			if code != http.StatusOK || !reflect.DeepEqual(doc.ids(), []string{tlParent}) {
				t.Errorf("%s as %s = %d %v, want [%s]", path, scope, code, doc.ids(), tlParent)
			}
			for _, leak := range []string{"tl_other", tlOther, "tl_web", "pub_b", "secret_only_tool", "attrs"} {
				if strings.Contains(body, leak) {
					t.Errorf("%s as %s carries %q: %s", path, scope, leak, body)
				}
			}
			// Each listed trace opens for the same token.
			for _, id := range doc.ids() {
				if code, body := read("/api/traces/"+id, pt); code != http.StatusOK {
					t.Errorf("listed trace %s as %s: detail %d %s", id, scope, code, body)
				}
			}
		}
		if code, _ := read("/api/traces?public_id=pub_b", pt); code != http.StatusForbidden {
			t.Errorf("foreign public_id as %s = %d, want 403", scope, code)
		}
		// The scope's 403 wins over a bad parameter.
		if code, _ := read("/api/traces?public_id=pub_b&since=x", pt); code != http.StatusForbidden {
			t.Errorf("foreign public_id with a bad since as %s = %d, want 403", scope, code)
		}
	}
	// The server token lists them all.
	if code, body := read("/api/traces", tok); code != http.StatusOK || !strings.Contains(body, `"total":4`) {
		t.Errorf("server token: %d %s", code, body)
	}
}

// TestTracesRootPastPage: a trace of more runs than one runs page (500)
// still names its root (read by id, not as the oldest of a newest-first
// page) and counts every failed descendant.
func TestTracesRootPastPage(t *testing.T) {
	srv := New(Open(t.TempDir() + "/big.db"))
	t.Cleanup(func() { _ = srv.Close() })
	const trace = "5e000000000000000000000000000005"
	spans := []obsdb.Span{tlSpan(trace, "5e00000000000001", "", "invoke_agent orders", "studio-test", 0, time.Hour, 1, tlWeft("big", "pub_a", "invoke_agent", nil))}
	for i := range 600 {
		status := 1
		if i%200 == 7 {
			status = 2 // three failed children
		}
		run := fmt.Sprintf("big/0/c%03d", i)
		spans = append(spans, tlSpan(trace, fmt.Sprintf("5e1%013x", i), "5e00000000000001", "invoke_agent researcher", "studio-test",
			time.Duration(i+1)*time.Second, time.Duration(i+2)*time.Second, status,
			tlWeft(run, "pub_a", "invoke_agent", map[string]any{"weft.parent.run.id": "big", "weft.parent.call.id": fmt.Sprintf("c%03d", i)})))
	}
	if err := srv.db.Write(context.Background(), obsdb.Batch{Spans: spans}); err != nil {
		t.Fatal(err)
	}
	code, _, body := get(t, srv.Handler(), "/studio/api/traces")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var doc struct {
		Traces []struct {
			RootRun *struct {
				ID string `json:"id"`
			} `json:"root_run"`
			FailedRuns int  `json:"failed_runs"`
			Capped     bool `json:"failed_runs_capped"`
		} `json:"traces"`
	}
	decode(t, body, &doc)
	if len(doc.Traces) != 1 || doc.Traces[0].RootRun == nil || doc.Traces[0].RootRun.ID != "big" ||
		doc.Traces[0].FailedRuns != 3 || doc.Traces[0].Capped {
		t.Errorf("trace row = %.600s", body)
	}
}
