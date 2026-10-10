package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// seedAt writes one run at t: run_start, a prompt, and — when finished —
// run_finish and its invoke_agent span with an app log line under it.
// A run with no finish whose t is recent reads running.
func seedAt(t *testing.T, db obsdb.DB, id, session, parent string, at time.Time, finished bool) {
	t.Helper()
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	span := fmt.Sprintf("%016x", h.Sum64())
	trace := strings.Repeat("de", 16)
	rec := func(kind, typ string, pos int64, body string) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": id, "gen_ai.agent.name": "acme-support"}
		if session != "" {
			a["weft.session.id"] = session
		}
		if parent != "" {
			a["weft.parent.run.id"] = parent
		}
		if kind == "event" {
			a["weft.event.type"], a["weft.event.pos"] = typ, pos
		} else {
			a["weft.messages.index"] = pos
		}
		return obsdb.Record{Time: at.Add(time.Duration(pos) * time.Millisecond), TraceID: trace, SpanID: span, Body: body, Attrs: a}
	}
	b := obsdb.Batch{Records: []obsdb.Record{
		rec("event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`),
		rec("messages", "", 0, `[{"role":"user","content":[{"type":"text","text":"hello"}]}]`),
	}}
	if finished {
		b.Records = append(b.Records, rec("event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","steps":1}`),
			obsdb.Record{Time: at, TraceID: trace, SpanID: span, Body: "app line"})
		b.Spans = []obsdb.Span{{TraceID: trace, SpanID: span, Name: "invoke_agent acme-support", Start: at, End: at.Add(time.Second),
			StatusCode: 1, Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": id}}}
	}
	if err := db.Write(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func call(t *testing.T, ts *httptest.Server, method, path, host, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// DELETE /api/runs/{id} takes the run and its subagent child (setup B,
// the dev token): 200 with what was deleted, then every read of either
// is 404, a second delete is 404, and the other run stays.
func TestDeleteRunRoute(t *testing.T) {
	const tok = "tok_delete"
	srv := New(Open(t.TempDir()+"/del.db"), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	now := time.Now().UTC()
	seedAt(t, srv.db, "run_p", "s_1", "", now, true)
	seedAt(t, srv.db, "run_p/0/call_1", "", "run_p", now, true)
	seedAt(t, srv.db, "run_keep", "s_2", "", now, true)

	code, body := call(t, ts, http.MethodDelete, "/api/runs/run_p", "", tok)
	if code != http.StatusOK {
		t.Fatalf("DELETE run = %d %s", code, body)
	}
	var doc map[string]string
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["deleted"] != "run" || doc["run_id"] != "run_p" || doc["note"] != deleteRunNote {
		t.Errorf("DELETE run body = %s", body)
	}
	for _, p := range []string{"/api/runs/run_p", "/api/runs/run_p/0/call_1", "/api/runs/run_p/events", "/api/runs/run_p/0/call_1/spans"} {
		if code, _ := call(t, ts, http.MethodGet, p, "", tok); code != http.StatusNotFound {
			t.Errorf("GET %s after delete = %d, want 404", p, code)
		}
	}
	if code, _ := call(t, ts, http.MethodGet, "/api/runs/run_keep", "", tok); code != http.StatusOK {
		t.Errorf("the other run = %d", code)
	}
	if code, _ := call(t, ts, http.MethodDelete, "/api/runs/run_p", "", tok); code != http.StatusNotFound {
		t.Errorf("a second DELETE = %d, want 404", code)
	}
	// A child alone: its parent stays.
	seedAt(t, srv.db, "run_keep/0/c", "", "run_keep", now, true)
	if code, body := call(t, ts, http.MethodDelete, "/api/runs/run_keep/0/c", "", tok); code != http.StatusOK || !strings.Contains(body, `"run_id":"run_keep/0/c"`) {
		t.Errorf("DELETE child = %d %s", code, body)
	}
	if code, _ := call(t, ts, http.MethodGet, "/api/runs/run_keep", "", tok); code != http.StatusOK {
		t.Errorf("the parent after its child's delete = %d", code)
	}
}

// DELETE /api/sessions/{id} takes the session's observability rows and
// says the app's conversation store is untouched.
func TestDeleteSessionRoute(t *testing.T) {
	const tok = "tok_delete"
	srv := New(Open(t.TempDir()+"/del.db"), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	now := time.Now().UTC()
	seedAt(t, srv.db, "t1", "s_gone", "", now, true)
	seedAt(t, srv.db, "t2", "s_gone", "", now.Add(time.Millisecond), true)
	seedAt(t, srv.db, "t3", "s_stays", "", now, true)
	code, body := call(t, ts, http.MethodDelete, "/api/sessions/s_gone", "", tok)
	if code != http.StatusOK || !strings.Contains(body, "the app's conversation store is untouched") || !strings.Contains(body, `"session_id":"s_gone"`) {
		t.Fatalf("DELETE session = %d %s", code, body)
	}
	for _, p := range []string{"/api/sessions/s_gone", "/api/runs/t1", "/api/runs/t2"} {
		if code, _ := call(t, ts, http.MethodGet, p, "", tok); code != http.StatusNotFound {
			t.Errorf("GET %s after delete = %d, want 404", p, code)
		}
	}
	if code, _ := call(t, ts, http.MethodGet, "/api/sessions/s_stays", "", tok); code != http.StatusOK {
		t.Errorf("the other session = %d", code)
	}
	for _, p := range []string{"/api/sessions/s_gone", "/api/sessions/nope", "/api/sessions/a/b"} {
		if code, _ := call(t, ts, http.MethodDelete, p, "", tok); code != http.StatusNotFound {
			t.Errorf("DELETE %s = %d, want 404", p, code)
		}
	}
}

// Setup A (no Token): deletion answers a loopback Host only — an
// AllowOrigins host may read but not delete, a rebound Host is walled
// before the route (403 either way).
func TestDeleteSetupALoopbackOnly(t *testing.T) {
	srv := New(Open(t.TempDir()+"/del.db"), AllowOrigins("http://myapp.internal:8080"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	now := time.Now().UTC()
	for _, id := range []string{"a1", "a2", "a3"} {
		seedAt(t, srv.db, id, "s_"+id, "", now, true)
	}
	cases := []struct {
		host, path string
		want       int
	}{
		{"myapp.internal:8080", "/api/runs/a1", http.StatusForbidden},
		{"myapp.internal:8080", "/api/sessions/s_a1", http.StatusForbidden},
		{"evil.example:7331", "/api/runs/a1", http.StatusForbidden},
		{"evil.example:7331", "/api/sessions/s_a1", http.StatusForbidden},
		{"127.0.0.1:7331", "/api/runs/a1", http.StatusOK},
		{"localhost:3000", "/api/sessions/s_a2", http.StatusOK},
		{"[::1]:7331", "/api/runs/a3", http.StatusOK},
		{"127.0.0.1:7331", "/api/runs/a1", http.StatusNotFound},
	}
	for _, c := range cases {
		if code, body := call(t, ts, http.MethodDelete, c.path, c.host, ""); code != c.want {
			t.Errorf("DELETE %s Host %s = %d, want %d: %s", c.path, c.host, code, c.want, body)
		}
	}
	// The AllowOrigins host still reads.
	if code, _ := call(t, ts, http.MethodGet, "/api/runs", "myapp.internal:8080", ""); code != http.StatusOK {
		t.Errorf("GET /api/runs from the AllowOrigins host = %d", code)
	}
}

func TestSweepInterval(t *testing.T) {
	for _, c := range []struct{ d, want time.Duration }{
		{720 * time.Hour, time.Hour},
		{10 * time.Hour, time.Hour},
		{time.Hour, 6 * time.Minute},
		{5 * time.Minute, time.Minute},
		{30 * time.Second, time.Minute},
	} {
		if got := sweepInterval(c.d); got != c.want {
			t.Errorf("sweepInterval(%v) = %v, want %v", c.d, got, c.want)
		}
	}
}

// lockedBuf is a log sink the sweeper's goroutine writes while the test
// reads.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Retain's sweeper deletes the runs not running whose last_seen is past
// the retention — on start and on its timer — keeps a running run and a
// recent one, logs one Debug line per sweep with its count, and stops
// at Close.
func TestRetainSweeper(t *testing.T) {
	logs := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	db := openTestDB(t)
	now := time.Now().UTC()
	seedAt(t, db, "old_done", "s_old", "", now.Add(-3*time.Hour), true)
	seedAt(t, db, "old_done/0/c", "", "old_done", now.Add(-3*time.Hour), true)
	seedAt(t, db, "old_interrupted", "", "", now.Add(-3*time.Hour), false)
	seedAt(t, db, "recent", "", "", now, true)
	seedAt(t, db, "running", "", "", now, false)

	srv := New(DB(db), Retain(time.Hour), func(c *config) { c.sweepEvery = 20 * time.Millisecond })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	waitGone := func(id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := db.Run(context.Background(), id); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("run %s was not swept", id)
	}
	waitGone("old_done")
	waitGone("old_done/0/c")
	waitGone("old_interrupted")
	// The timer sweeps again: a run that turns old after start goes too.
	seedAt(t, db, "later_old", "", "", now.Add(-2*time.Hour), true)
	waitGone("later_old")
	for _, id := range []string{"recent", "running"} {
		if _, err := db.Run(context.Background(), id); err != nil {
			t.Errorf("%s was swept: %v", id, err)
		}
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !strings.Contains(out, "studio: retention sweep") || !strings.Contains(out, "deleted_runs=3") ||
		!strings.Contains(out, "deleted_runs=1") || !strings.Contains(out, "retention=1h0m0s") {
		t.Errorf("sweep log lines = %s", out)
	}
	// Close stopped it: nothing more is swept.
	seedAt(t, db, "after_close", "", "", now.Add(-2*time.Hour), true)
	time.Sleep(100 * time.Millisecond)
	if _, err := db.Run(context.Background(), "after_close"); err != nil {
		t.Errorf("a run written after Close was swept: %v", err)
	}
}

// A failing sweep goes to the logger, never further: the server keeps
// serving.
func TestRetainSweepFailureIsLogged(t *testing.T) {
	logs := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	db := openTestDB(t)
	srv := New(DB(failingSweep{db}), Retain(time.Hour), func(c *config) { c.sweepEvery = 20 * time.Millisecond })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "studio: retention sweep failed") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "level=WARN msg=\"studio: retention sweep failed\"") {
		t.Errorf("no Warn line for the failed sweep: %s", logs.String())
	}
	if code, _ := call(t, ts, http.MethodGet, "/api/runs", "", ""); code != http.StatusOK {
		t.Errorf("the server after a failed sweep = %d", code)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
}

// A sweeper whose DB the pipeline closed under it (Handler, nothing to
// Close) stops on its own: at most one Warn, a Debug line saying why,
// and no further sweep.
func TestRetainSweeperStopsOnClosedDB(t *testing.T) {
	logs := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	db, err := sqlite.Open(t.TempDir() + "/closed.db")
	if err != nil {
		t.Fatal(err)
	}
	counted := &countingSweep{DB: db}
	srv := New(DB(counted), Retain(time.Hour), func(c *config) { c.sweepEvery = 10 * time.Millisecond })
	deadline := time.Now().Add(5 * time.Second)
	for counted.n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(logs.String(), "sweeper stopped: database closed") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "level=DEBUG msg=\"studio: sweeper stopped: database closed\"") {
		t.Fatalf("no stop line: %s", logs.String())
	}
	n := counted.n.Load()
	time.Sleep(100 * time.Millisecond) // ten ticks
	if got := counted.n.Load(); got != n {
		t.Errorf("the sweeper kept sweeping after the DB closed: %d sweeps, then %d", n, got)
	}
	if w := strings.Count(logs.String(), "level=WARN"); w > 1 {
		t.Errorf("%d Warn lines after the DB closed, want at most one: %s", w, logs.String())
	}
	// The goroutine is gone: Close returns at once.
	done := make(chan struct{})
	go func() { _ = srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on a stopped sweeper")
	}
}

type countingSweep struct {
	obsdb.DB
	n atomic.Int64
}

func (c *countingSweep) Sweep(ctx context.Context, before time.Time) (int, error) {
	c.n.Add(1)
	return c.DB.Sweep(ctx, before)
}

// Retain's floor: 0 < d < 1h panics at New with the reason; 0 (off) and
// an hour pass. Close is safe to call twice, concurrently.
func TestRetainFloor(t *testing.T) {
	for _, d := range []time.Duration{time.Second, 30 * time.Minute, time.Hour - time.Nanosecond} {
		func() {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(fmt.Sprint(r), retainFloorMsg) {
					t.Errorf("Retain(%v): panic = %v, want the floor's message", d, r)
				}
			}()
			_ = New(DB(openTestDB(t)), Retain(d))
		}()
	}
	if !strings.HasPrefix(retainFloorMsg, "retention must be at least 1h: ") {
		t.Errorf("floor message = %q", retainFloorMsg)
	}
	for _, d := range []time.Duration{0, time.Hour} {
		srv := New(DB(openTestDB(t)), Retain(d))
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); _ = srv.Close() }()
		}
		wg.Wait()
	}
}

type failingSweep struct{ obsdb.DB }

func (failingSweep) Sweep(context.Context, time.Time) (int, error) {
	return 0, fmt.Errorf("disk on fire")
}

// api/meta's retention: null by default (meta.golden.json), the
// configured duration as a Go duration string with Retain.
func TestMetaRetention(t *testing.T) {
	srv := New(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)), Retain(720*time.Hour))
	t.Cleanup(func() { _ = srv.Close() })
	code, _, body := get(t, srv.Handler(), "/studio/api/meta")
	if code != http.StatusOK {
		t.Fatalf("meta: %d", code)
	}
	if !strings.Contains(body, `"retention":"720h0m0s"`) {
		t.Errorf("meta retention: %s", body)
	}
	wefttest.Golden(t, "testdata/api/meta-retention.golden.json",
		[]byte(metaPIDRe.ReplaceAllString(indent(t, body), `"pid": 1234`)))
	_, _, plain := get(t, Handler(DB(fixtureDB(t))), "/studio/api/meta")
	if !strings.Contains(plain, `"retention":null`) {
		t.Errorf("default meta retention: %s", plain)
	}
}

func openTestDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/retain.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
