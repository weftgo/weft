package reqlog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"": false, "abc-1.2_3:4": true, "has space": false, "a\nb": false,
		string(make([]byte, maxID+1)): false,
	} {
		if validID(id) != want {
			t.Errorf("validID(%q) = %v", id, !want)
		}
	}
}

// The ingest refusal limit: one line a window per key, the next line
// counting what it suppressed.
func TestAllowWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := New()
	l.now = func() time.Time { return now }
	if ok, n, _, _ := l.allow("k"); !ok || n != 0 {
		t.Fatalf("first: %v %d", ok, n)
	}
	for range 3 {
		if ok, _, _, _ := l.allow("k"); ok {
			t.Fatal("a repeat inside the window was allowed")
		}
	}
	if ok, _, _, _ := l.allow("other"); !ok {
		t.Error("another key was limited")
	}
	now = now.Add(limitWindow)
	if ok, n, _, _ := l.allow("k"); !ok || n != 3 {
		t.Errorf("after the window: %v suppressed=%d, want true 3", ok, n)
	}
}

// P8-fix-a 3: the key's route is its class, so distinct /v1/<x> 404s
// share one limit instead of each being a key of its own.
func TestRouteClass(t *testing.T) {
	for path, want := range map[string]string{
		"/v1/traces": "/v1/traces", "/v1/logs": "/v1/logs",
		"/v1/metrics": "/v1/*", "/v1/a/b": "/v1/*", "/v1/x1": "/v1/*",
	} {
		if got := routeClass(path); got != want {
			t.Errorf("routeClass(%q) = %q, want %q", path, got, want)
		}
	}
	logged := capture(t)
	l := New()
	h := l.Wrap(http.NotFoundHandler())
	for i := range 5 {
		req := httptest.NewRequest(http.MethodPost, "/v1/scan"+strconv.Itoa(i), nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if n := strings.Count(logged.String(), "ingest batch refused"); n != 1 {
		t.Errorf("five distinct /v1/<x> 404s logged %d lines, want 1:\n%s", n, logged)
	}
}

// P8-fix-a 3: a full limiter drops the expired windows first, and with
// none expired it stops logging new keys — never resets every limit —
// and the next line carries the count.
func TestAllowOverflow(t *testing.T) {
	now := time.Unix(0, 0)
	l := New()
	l.now = func() time.Time { return now }
	for i := range maxKeys {
		if ok, _, _, _ := l.allow("k" + strconv.Itoa(i)); !ok {
			t.Fatalf("key %d refused while filling", i)
		}
	}
	if ok, _, _, _ := l.allow("new"); ok {
		t.Fatal("a new key was logged while every window is live")
	}
	if ok, _, _, _ := l.allow("k0"); ok {
		t.Fatal("the full map was reset: a limited key logged again inside its window")
	}
	now = now.Add(limitWindow)
	ok, _, over, exp := l.allow("new")
	if !ok || over != 1 {
		t.Fatalf("after the windows turned: ok=%v overflow=%d, want true 1", ok, over)
	}
	// k0's refusal inside its window was suppressed; the sweep that
	// dropped k0 carries it onto this line (P8-fix-b #16).
	if exp != 1 {
		t.Errorf("after the sweep: suppressed_expired=%d, want 1 (k0's suppressed refusal)", exp)
	}
	if len(l.seen) != 1 {
		t.Errorf("expired windows kept: %d keys", len(l.seen))
	}
	// Reported once.
	now = now.Add(limitWindow)
	if _, _, _, exp := l.allow("again"); exp != 0 {
		t.Errorf("the expired count was reported twice: %d", exp)
	}
}

// P8-fix-a 8: a handler panic is one ERROR line with the request id,
// and the panic goes on to net/http; ErrAbortHandler is not logged.
func TestWrapLogsAPanic(t *testing.T) {
	logged := capture(t)
	h := New().Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	req.Header.Set(Header, "rid-1")
	func() {
		defer func() {
			if v := recover(); v != "boom" {
				t.Errorf("recovered %v, want the handler's panic re-raised", v)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	l := logged.String()
	for _, want := range []string{"level=ERROR", `msg="studio: handler panicked"`, "request_id=rid-1", "panic=boom"} {
		if !strings.Contains(l, want) {
			t.Errorf("the line %q lacks %q", l, want)
		}
	}
	logged.Reset()
	abort := New().Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	func() {
		defer func() { _ = recover() }()
		abort.ServeHTTP(httptest.NewRecorder(), req)
	}()
	if logged.Len() != 0 {
		t.Errorf("ErrAbortHandler logged: %s", logged)
	}
}

// plainWriter is a ResponseWriter that cannot flush.
type plainWriter struct{ h http.Header }

func (w *plainWriter) Header() http.Header         { return w.h }
func (w *plainWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *plainWriter) WriteHeader(int)             {}

// P8-fix-a 10: the wrapped writer is an http.Flusher exactly when the
// connection's writer is, so the live stream's check answers truly.
func TestWrapFlusherFollowsTheWriter(t *testing.T) {
	var got bool
	h := New().Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, got = w.(http.Flusher)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/live", nil)
	h.ServeHTTP(&plainWriter{h: http.Header{}}, req)
	if got {
		t.Error("over a writer that cannot flush, the wrapper claims http.Flusher")
	}
	rec := httptest.NewRecorder()
	h = New().Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		got = ok
		if ok {
			f.Flush()
		}
	}))
	h.ServeHTTP(rec, req)
	if !got || !rec.Flushed {
		t.Errorf("over a flushing writer: Flusher %v, flushed %v", got, rec.Flushed)
	}
}

// P8-fix-a 2 and 13: Cause is what the line names while the body keeps
// Refuse's text, and the ingest counters count POSTs under /v1/.
func TestCauseAndIngestCounts(t *testing.T) {
	logged := capture(t)
	l := New()
	h := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/runs/r1":
			Refuse(r.Context(), "internal", "read run r1 failed")
			Cause(r.Context(), "read run r1: disk on fire")
			w.WriteHeader(http.StatusInternalServerError)
		case "/v1/traces":
			if r.Header.Get("X-Bad") != "" {
				Refuse(r.Context(), "bad_request", "not otlp")
				w.WriteHeader(http.StatusBadRequest)
			}
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/runs/r1", nil))
	if l := logged.String(); !strings.Contains(l, `reason="read run r1: disk on fire"`) {
		t.Errorf("the 5xx line lost the cause: %s", l)
	}
	if c := l.Ingest(); c != (IngestCounts{}) {
		t.Errorf("an API request counted as ingest: %+v", c)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/traces", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodOptions, "/v1/traces", nil))
	bad := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	bad.Header.Set("X-Bad", "1")
	h.ServeHTTP(httptest.NewRecorder(), bad)
	c := l.Ingest()
	if c.Accepted != 1 || c.Refused != 1 || c.LastError != "400 bad_request: not otlp" || c.LastAt.IsZero() {
		t.Errorf("ingest counts %+v, want 1 accepted, 1 refused, the refusal named", c)
	}
}

// capture points slog.Default at a buffer for the test.
func capture(t *testing.T) *strings.Builder {
	t.Helper()
	var b strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&b, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &b
}
