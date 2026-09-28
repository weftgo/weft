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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/wefttest"
)

// The fixtures (plan §3): a success with a tool call, a failure, a
// parent with a subagent (the child records itself), and a
// crash-orphaned "running" row whose stale heartbeat reads
// interrupted. Records run through real wefttest agents and
// store.Record, then their times are pinned so the goldens are
// byte-stable; the only normalization left is weft_version, which
// depends on where the test runs.

var fixtureTags = map[string]string{"cwd": "/tmp/demo"}

// fixtureT0 anchors every fixture time: 2020-01-01T09:00:00Z — far
// enough in the past that DeriveStatus concludes interrupted for the
// stale row at any future "now", so the goldens never flip.
var fixtureT0 = time.Date(2020, 1, 1, 9, 0, 0, 123000000, time.UTC)

func fixtureStore(t *testing.T) store.Store {
	t.Helper()
	s := store.Memory()
	ctx := context.Background()

	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			return "order " + in.OrderID + ": shipped", nil
		})

	// r_ok: one tool call, then the answer.
	ok := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.Say("Order 42 shipped this morning."),
	), weft.Name("orders"), lookup, store.Record(s, store.Tags(fixtureTags)))
	if _, err := ok.Generate(ctx, weft.Prompt("Where is order 42?"), weft.RunID("r_ok")); err != nil {
		t.Fatal(err)
	}

	// r_fail: the model stream fails mid-run; the partial transcript
	// and the error text are part of the record.
	fail := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"43"}`}),
		wefttest.SayThenFail("Let me look that up…", errors.New("wefttest: injected provider 500")),
	), weft.Name("support"), lookup, store.Record(s, store.Tags(fixtureTags)))
	if _, err := fail.Generate(ctx, weft.Prompt("Where is order 43?"), weft.RunID("r_fail")); err == nil {
		t.Fatal("r_fail: want the injected failure")
	}

	// r_sub: a parent that delegates to a researcher subagent; the
	// child's record links back via parent_id and parent_call_id.
	researcher := weft.New(wefttest.Script(
		wefttest.Say("order 42 shipped this morning"),
	), weft.Name("researcher"), store.Record(s, store.Tags(fixtureTags)))
	sub := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"status of order 42"}`}),
		wefttest.Say("Order 42 shipped."),
	), weft.Name("orders"), store.Record(s, store.Tags(fixtureTags)),
		weft.Subagent("research", "Summarize an order's status.", researcher))
	if _, err := sub.Generate(ctx, weft.Prompt("Where is order 42?"), weft.RunID("r_sub")); err != nil {
		t.Fatal(err)
	}

	// r_stale: a hand-built crash orphan — status running, heartbeat
	// minutes stale, events mid-step and no RunFinish.
	staleStart := fixtureT0.Add(3 * time.Minute)
	stale := store.RunRecord{
		ID:        "r_stale",
		Agent:     "orders",
		Model:     weft.ModelInfo{Provider: "wefttest", Name: "script"},
		Started:   staleStart,
		Heartbeat: staleStart,
		Status:    store.Running,
		Tags:      map[string]string{"cwd": "/tmp/demo"},
		Events: []weft.Event{
			weft.RunStart{ID: "r_stale", Agent: "orders", Model: weft.ModelInfo{Provider: "wefttest", Name: "script"}},
			weft.StepStart{RunID: "r_stale", Index: 0},
			weft.TextDelta{RunID: "r_stale", Text: "Let me check that order…"},
		},
	}
	if err := s.Save(ctx, stale); err != nil {
		t.Fatal(err)
	}

	// Pin the recorded times: started/finished/heartbeat per run, in
	// list order (newest first) — r_stale, r_sub (+child), r_fail, r_ok.
	pins := map[string][3]time.Time{
		"r_ok":   {fixtureT0, fixtureT0.Add(2 * time.Second), fixtureT0.Add(2 * time.Second)},
		"r_fail": {fixtureT0.Add(time.Minute), fixtureT0.Add(time.Minute + 2*time.Second), fixtureT0.Add(time.Minute + 2*time.Second)},
		"r_sub":  {fixtureT0.Add(2 * time.Minute), fixtureT0.Add(2*time.Minute + 5*time.Second), fixtureT0.Add(2*time.Minute + 5*time.Second)},
	}
	for _, rec := range allRuns(t, s) {
		p, ok := pins[rec.ID]
		if !ok {
			continue // r_stale is pinned at construction; children below
		}
		rec.Started, rec.Finished, rec.Heartbeat = p[0], p[1], p[2]
		if err := s.Save(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	// The child's window sits inside the parent's.
	for _, rec := range allRuns(t, s) {
		if rec.ParentID != "r_sub" {
			continue
		}
		rec.Started = fixtureT0.Add(2*time.Minute + 3*time.Second)
		rec.Finished = fixtureT0.Add(2*time.Minute + 4*time.Second)
		rec.Heartbeat = rec.Finished
		if err := s.Save(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func allRuns(t *testing.T, s store.Store) []store.RunRecord {
	t.Helper()
	page, err := s.List(context.Background(), store.Query{ParentID: "*"})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.RunRecord
	for _, rec := range page.Runs {
		full, err := s.Get(context.Background(), rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, full)
	}
	return out
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
	h := Handler(fixtureStore(t), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/meta")
	if code != http.StatusOK {
		t.Fatalf("meta: %d", code)
	}
	golden(t, "meta.golden.json", body)

	// The open handler reports no capabilities; a server declares them.
	_, _, plain := get(t, Handler(fixtureStore(t)), "/studio/api/meta")
	if !strings.Contains(plain, `"capabilities":[]`) {
		t.Errorf("default capabilities = %s, want []", plain)
	}
	_, _, caps := get(t, Handler(fixtureStore(t), Capabilities("live", "ingest")), "/studio/api/meta")
	if !strings.Contains(caps, `"capabilities":["live","ingest"]`) {
		t.Errorf("declared capabilities = %s", caps)
	}
}

func TestRunsGolden(t *testing.T) {
	h := Handler(fixtureStore(t), Manifest([]byte(fixtureManifest)))
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
	golden(t, "runs.golden.json", body)
}

func TestRunGolden(t *testing.T) {
	h := Handler(fixtureStore(t), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/runs/r_sub")
	if code != http.StatusOK {
		t.Fatalf("run: %d", code)
	}
	// The document carries children but never events (ADR 0018 §8).
	if strings.Contains(body, `"events"`) {
		t.Error("run document carries inline events")
	}
	if !strings.Contains(body, `"parent_id":"r_sub"`) {
		t.Errorf("run document misses children: %s", body)
	}
	golden(t, "run-sub.golden.json", body)

	// A failed run's document keeps the error text and the partial
	// result (the store's own result document, envelope unwrapped).
	_, _, fail := get(t, h, "/studio/api/runs/r_fail")
	for _, want := range []string{`"status":"failed"`, `"err":"`, `"result"`} {
		if !strings.Contains(fail, want) {
			t.Errorf("r_fail missing %s", want)
		}
	}
	golden(t, "run-fail.golden.json", fail)
}

func TestEventsGolden(t *testing.T) {
	h := Handler(fixtureStore(t), Manifest([]byte(fixtureManifest)))
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
}

func TestEventsPaging(t *testing.T) {
	s := fixtureStore(t)
	rec, err := s.Get(context.Background(), "r_ok")
	if err != nil {
		t.Fatal(err)
	}
	total := len(rec.Events)
	h := Handler(s)

	// Walk the whole stream in pages of 2 and reassemble it: the pages
	// concatenated must equal the store's order (byte-for-byte, each
	// event through its own codec), and the last page reports done.
	want := make([]string, total)
	for i, ev := range rec.Events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = string(b)
	}
	var walked int
	after := 0
	for {
		_, _, body := get(t, h, fmt.Sprintf("/studio/api/runs/r_ok/events?after=%d&limit=2", after))
		var page struct {
			Events    []json.RawMessage `json:"events"`
			NextAfter *int64            `json:"next_after"`
			Done      bool              `json:"done"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, ev := range page.Events {
			if walked >= total || string(ev) != want[walked] {
				t.Fatalf("event %d = %s, want %s", walked, ev, want)
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
		t.Fatalf("walked %d events, store holds %d", walked, total)
	}

	// Past the end: empty page, no cursor, still done.
	_, _, tail := get(t, h, fmt.Sprintf("/studio/api/runs/r_ok/events?after=%d", total))
	if !strings.Contains(tail, `"events":[]`) || !strings.Contains(tail, `"done":true`) {
		t.Errorf("past-the-end page = %s", tail)
	}

	// A live run (fresh heartbeat) is never done and is not cached.
	live := store.RunRecord{
		ID: "r_live", Agent: "orders", Status: store.Running,
		Started: time.Now(), Heartbeat: time.Now(),
		Events: []weft.Event{weft.RunStart{ID: "r_live", Agent: "orders"}},
	}
	if err := s.Save(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	_, _, body := get(t, h, "/studio/api/runs/r_live/events")
	if !strings.Contains(body, `"done":false`) {
		t.Errorf("live run reported done: %s", body)
	}
	if _, _, ok := h.(*app).events.get("r_live"); ok {
		t.Error("running run was cached")
	}
	// The stale orphan is finished-in-effect: done once drained.
	_, _, stale := get(t, h, "/studio/api/runs/r_stale/events")
	if !strings.Contains(stale, `"done":true`) {
		t.Errorf("stale run not done: %s", stale)
	}
}

func TestEventCacheLRU(t *testing.T) {
	c := newEventCache(2)
	ev := []weft.Event{weft.RunStart{ID: "x"}}
	c.put("a", ev)
	c.put("b", ev)
	if _, _, ok := c.get("a"); !ok {
		t.Fatal("a evicted early")
	}
	c.put("c", ev) // a was just touched, so b is the least recent
	if _, _, ok := c.get("a"); !ok {
		t.Error("a evicted while hot")
	}
	if _, _, ok := c.get("b"); ok {
		t.Error("b survived eviction")
	}
	if c.order.Len() != 2 {
		t.Errorf("cache holds %d, want 2", c.order.Len())
	}
}

func TestAPIErrors(t *testing.T) {
	h := Handler(fixtureStore(t))
	code, _, body := get(t, h, "/studio/api/runs/nope")
	if code != http.StatusNotFound || !strings.Contains(body, `"not_found"`) {
		t.Errorf("unknown id: %d %s", code, body)
	}
	if code, _, b := get(t, h, "/studio/api/runs?before=yesterday"); code != http.StatusBadRequest || !strings.Contains(b, "bad_request") {
		t.Errorf("bad before: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/runs/r_ok/events?after=-1"); code != http.StatusBadRequest {
		t.Errorf("negative after: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/runs?limit=lots"); code != http.StatusBadRequest {
		t.Errorf("bad limit: %d %s", code, b)
	}
	if code, _, b := get(t, h, "/studio/api/nope"); code != http.StatusNotFound {
		t.Errorf("unknown api route: %d %s", code, b)
	}

	// POST is refused with Allow, everywhere.
	srv := httptest.NewServer(mounted(h))
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/studio/api/runs", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST api/runs: %d", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q", allow)
	}

	// A recording this weft cannot decode is a 409 with the upgrade
	// message (Memory cannot hold such a doc by construction — it
	// re-marshals — so the mapping is proven against a stub).
	for _, target := range []struct {
		name string
		err  error
	}{{"newer format", fmt.Errorf("store: %w", store.ErrNewerFormat)},
		{"unknown event", fmt.Errorf("store: %w", store.ErrUnknownEvent)}} {
		stub := stubStore{getErr: target.err}
		code, _, b := get(t, Handler(stub), "/studio/api/runs/r_x")
		if code != http.StatusConflict || !strings.Contains(b, "newer_format") ||
			!strings.Contains(b, "upgrade studio") {
			t.Errorf("%s: %d %s", target.name, code, b)
		}
		code, _, b = get(t, Handler(stub), "/studio/api/runs/r_x/events")
		if code != http.StatusConflict {
			t.Errorf("%s (events): %d %s", target.name, code, b)
		}
	}
}

// stubStore fails every Get with the wrapped error; List answers from
// the embedded Memory so the rest of the API still works.
type stubStore struct {
	store.Store
	getErr error
}

func (s stubStore) Get(context.Context, string) (store.RunRecord, error) {
	return store.RunRecord{}, s.getErr
}

func TestManifestEndpoint(t *testing.T) {
	h := Handler(fixtureStore(t), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/manifest")
	if code != http.StatusOK {
		t.Fatalf("manifest: %d", code)
	}
	if body != fixtureManifest {
		t.Errorf("manifest bytes not passed through verbatim")
	}
	code, _, body = get(t, Handler(fixtureStore(t)), "/studio/api/manifest")
	if code != http.StatusNotFound {
		t.Errorf("manifest without option: %d %s", code, body)
	}
}

func TestShellAndFallback(t *testing.T) {
	h := Handler(fixtureStore(t))
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
	if _, _, b := get(t, Handler(fixtureStore(t), Base("/x/")), "/studio/runs/r_ok"); !strings.Contains(b, `<base href="/x/">`) {
		t.Errorf("Base(/x/) not rewritten: %s", b)
	}
	if _, _, b := get(t, Handler(fixtureStore(t), Base("x")), "/studio/"); !strings.Contains(b, `<base href="/x/">`) {
		t.Errorf("Base(x) not normalized: %s", b)
	}
	if _, _, b := get(t, Handler(fixtureStore(t), Title("dev studio")), "/studio/"); !strings.Contains(b, "<title>dev studio</title>") {
		t.Errorf("Title not rewritten: %s", b)
	}
}

func TestAssetHeaders(t *testing.T) {
	h := Handler(fixtureStore(t))
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
			t.Error("Handler(nil) did not panic")
		}
	}()
	_ = Handler(nil)
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
