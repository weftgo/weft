package studio

// Studio's server log (UX-E-37, audit row D10): a refused ingest batch
// is one WARN line on slog.Default naming its reason and the request
// id; a 5xx is one ERROR line with the request id, which the response
// carries too (X-Request-Id, and request_id in the 5xx body).
import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// captureLog points slog.Default at a buffer for the test.
func captureLog(t *testing.T) *strings.Builder {
	t.Helper()
	var b strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&b, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &b
}

func linesOf(b *strings.Builder) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestServerLogNamesARefusedIngestBatch(t *testing.T) {
	logged := captureLog(t)
	h := New(DB(fixtureDB(t))).Handler()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/traces", strings.NewReader("{not otlp"))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed batch: %d %s", w.Code, w.Body.String())
	}
	id := w.Header().Get("X-Request-Id")
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("X-Request-Id = %q, want a generated id", id)
	}
	lines := linesOf(logged)
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want one: %q", len(lines), lines)
	}
	l := lines[0]
	for _, want := range []string{"level=WARN", `msg="studio: ingest batch refused"`, "request_id=" + id,
		"status=400", "path=/v1/traces", "code=bad_request", `reason="decode application/json:`} {
		if !strings.Contains(l, want) {
			t.Errorf("the line %q lacks %q", l, want)
		}
	}

	// The same refusal again inside the minute is counted, not logged;
	// a client's own well-formed request id is echoed.
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/traces", strings.NewReader("{not otlp"))
	req.RemoteAddr = "127.0.0.1:5556"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "exporter-42")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := w.Header().Get("X-Request-Id"); got != "exporter-42" {
		t.Errorf("echoed X-Request-Id = %q", got)
	}
	if n := len(linesOf(logged)); n != 1 {
		t.Errorf("a repeat refusal inside the window logged: %d lines", n)
	}
}

func TestServerLogNamesA5xxWithItsRequestID(t *testing.T) {
	logged := captureLog(t)
	h := New(DB(failingDB{DB: fixtureDB(t), runs: true})).Handler()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/runs", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GET api/runs over a failing DB: %d %s", w.Code, w.Body.String())
	}
	id := w.Header().Get("X-Request-Id")
	var body struct {
		Error struct {
			Code, Message string
			RequestID     string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if id == "" || body.Error.RequestID != id {
		t.Errorf("5xx body request_id %q, header %q: want the same id", body.Error.RequestID, id)
	}
	var errLines []string
	for _, l := range linesOf(logged) {
		if strings.Contains(l, "level=ERROR") {
			errLines = append(errLines, l)
		}
	}
	if len(errLines) != 1 {
		t.Fatalf("ERROR lines %q, want one", errLines)
	}
	for _, want := range []string{`msg="studio: request failed"`, "request_id=" + id, "status=500", "path=/api/runs", "code=internal"} {
		if !strings.Contains(errLines[0], want) {
			t.Errorf("the line %q lacks %q", errLines[0], want)
		}
	}

	// A 4xx on the API is the client's: no line.
	logged.Reset()
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/runs/nope", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w = httptest.NewRecorder()
	New(DB(fixtureDB(t))).Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound || logged.Len() != 0 {
		t.Errorf("a 404: %d, logged %q", w.Code, logged.String())
	}
}

// P8-fix-a 2: a 5xx answered to a panel token masks the database's error
// text in its body, but Studio's server log line for that request id
// still names the real error — the operator's only way to it.
func TestServerLogNamesAPanelTokens5xxCause(t *testing.T) {
	logged := captureLog(t)
	stub := stubDB{err: errors.New("open /var/lib/studio/secret.db: disk on fire")}
	h := Handler(DB(stub), Token("srv"))
	tok, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_orders", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, hdr, body := getWith(t, h, "/studio/api/runs/r_x", tok, "")
	if code != http.StatusInternalServerError || strings.Contains(body, "disk on fire") {
		t.Fatalf("panel token's 500: %d %s", code, body)
	}
	id := hdr.Get("X-Request-Id")
	var line string
	for _, l := range linesOf(logged) {
		if strings.Contains(l, "request_id="+id) {
			line = l
		}
	}
	if !strings.Contains(line, `reason="read run r_x: open /var/lib/studio/secret.db: disk on fire"`) {
		t.Errorf("the 5xx line for %s does not name the real error: %q", id, line)
	}
}

// P8-fix-a 13: /api/meta's ingest section counts the batches this
// Studio accepted and refused, with the last refusal and its time.
func TestMetaIngestCounts(t *testing.T) {
	captureLog(t)
	ts := httptest.NewServer(New(DB(fixtureDB(t))).Handler())
	t.Cleanup(ts.Close)
	post := func(body string) {
		resp, err := http.Post(ts.URL+"/v1/traces", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	post(`{"resourceSpans":[]}`)
	post("{not otlp")
	in := metaOf(t, ts, "").Ingest
	if in == nil {
		t.Fatal("meta carries no ingest section")
	}
	if in.Accepted != 1 || in.Refused != 1 || in.LastAt == nil ||
		!strings.HasPrefix(in.LastError, "400 bad_request: decode application/json") {
		t.Errorf("ingest counts %+v", *in)
	}
}
