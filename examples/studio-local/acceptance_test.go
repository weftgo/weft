package main

// The §10.1 acceptance test: a fresh Go app with exactly the five
// lines of setup A runs a thread session, and its turns appear live —
// an httptest-driven subscription to /api/live sees the run's
// run_start record frame while the database still holds no run_finish
// for it, the turn then lands grouped under one session with content
// and timing, and nothing else is configured. The manual half of the
// gate (the browser) is described in this directory's README run.
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/scope"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// TestOutOfTheBoxLive is the S7 step-6 gate for §10.1.
func TestOutOfTheBoxLive(t *testing.T) {
	path := t.TempDir() + "/weft.db"

	// The five lines of setup A (§10.1), verbatim in shape: Install
	// the defaults with an explicit Local path so the test owns the
	// file, and mount Studio over the pipeline's own handle.
	shutdown := otel.Install(
		otel.NoEnv(),
		otel.Local(path),
	)
	defer shutdown()
	handler := studio.Handler(studio.DB(otel.LocalDB()))

	// Subscribe first: the panel's position.
	frames := make(chan string, 64)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/live?public_id=pub_demo&kinds=event,delta,run", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("live content type %q", ct)
	}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "event: ") {
				frames <- strings.TrimPrefix(line, "event: ")
			}
		}
		close(frames)
	}()

	// The app's own turn, on its own goroutine: the demo's handler
	// with its pacing model, so the run stays open for a moment.
	started := make(chan struct{})
	go func() {
		defer close(started)
		d := newDemo(thread.Memory(), demoAgent())
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/run",
			strings.NewReader("where is order 42?"))
		d.run(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("/run: %d %s", w.Code, w.Body.String())
		}
	}()

	// The run_start record frame arrives while the run is still open:
	// the durable lane holds the run row and its run_start, but no
	// run_finish yet — the live lane is ahead, which is the point of
	// setup A.
	deadline := time.After(15 * time.Second)
	sawRecord := false
	for !sawRecord {
		select {
		case ev, ok := <-frames:
			if !ok {
				t.Fatal("live stream closed before any record frame")
			}
			sawRecord = ev == "record"
		case <-deadline:
			t.Fatal("no record frame within 15s: the live lane is not wired")
		}
	}
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.Runs(ctx, obsdb.RunQuery{ParentRunID: "*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) == 0 {
		t.Fatal("no run row: the local sink did not write")
	}
	runID := page.Runs[0].ID
	events, err := db.Events(ctx, runID, -1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	hasFinish := false
	for _, pe := range events.Events {
		if strings.Contains(string(pe.Event), `"run_finish"`) {
			hasFinish = true
		}
	}
	if hasFinish {
		t.Fatal("run_finish was already written when the live frame arrived: " +
			"the pacing model finished before the frame was seen")
	}

	// Drain: the turn lands grouped under one session, with content
	// and timing, and nothing else was configured.
	<-started
	deadline = time.After(30 * time.Second)
	for {
		det, err := db.Run(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if det.Status == obsdb.StatusSucceeded {
			if det.SessionID == "" {
				t.Errorf("run carries no session id: %s", det.ID)
			}
			if det.PublicID != "pub_demo" {
				t.Errorf("run public id = %q, want pub_demo", det.PublicID)
			}
			if det.Finished == nil || det.Finished.Sub(det.Started) <= 0 {
				t.Errorf("run has no timing: %v..%v", det.Started, det.Finished)
			}
			bodies, err := db.Transcript(ctx, det.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(bodies) < 2 {
				t.Fatalf("no content: %d messages records", len(bodies))
			}
			fmt.Printf("acceptance: run %s (session %s, turn %d) %s in %s, %d messages records\n",
				det.ID, det.SessionID, det.Turn, det.Status,
				det.Finished.Sub(det.Started).Round(time.Millisecond), len(bodies))
			return
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatalf("run never succeeded: %s", det.Status)
		}
	}
}

// TestRunTakesTheQuestionFromTheBody pins the demo's documented call —
// curl -XPOST …/run -d 'where is order 42?' — the way curl really
// sends it: a form content type whose body is the bare question. The
// turn must run on those words (a text=… field works too).
func TestRunTakesTheQuestionFromTheBody(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body, want string }{
		{"curl -d", "application/x-www-form-urlencoded", "where is order 77?", `About "where is order 77?": order 77`},
		{"text field", "application/x-www-form-urlencoded", "text=where+is+order+78%3F", `About "where is order 78?": order 78`},
		{"raw body", "", "where is order 79?", `About "where is order 79?": order 79`},
		{"empty", "", "", `About "hello": order 42`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := weft.New(echoModel{}, weft.Name("studio-local"), lookupOrder) // the demo's agent, unpaced
			d := newDemo(thread.Memory(), agent)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(tc.body))
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			d.run(w, r)
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("/run answered %q, want it to contain %q", w.Body.String(), tc.want)
			}
		})
	}
}

// TestPlaygroundOverTheInProcessLink is the demo's playground half: the
// runtime link dials the embedded Studio in-process (runtime.Local, no
// socket), registers the demo agent, and a playground command re-running
// the app's own turn on a new input is acked and finished succeeded —
// its run answering the new words, recorded in the same local sink.
func TestPlaygroundOverTheInProcessLink(t *testing.T) {
	path := t.TempDir() + "/weft.db"
	defer otel.Install(otel.NoEnv(), otel.Local(path))()
	agent := weft.New(echoModel{}, weft.Name("studio-local"), lookupOrder)
	srv := studio.New(studio.DB(otel.LocalDB()), studio.Playground(true))
	defer func() { _ = srv.Close() }()
	defer runtime.Install(runtime.Local(srv), runtime.Agents(agent), runtime.Enabled(true))()
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	poll := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	d := newDemo(thread.Memory(), agent)
	w := httptest.NewRecorder()
	d.run(w, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("where is order 42?")))
	source := strings.Fields(w.Body.String())[1]
	poll("the source transcript", func() bool { return strings.Contains(get("/api/runs/"+source+"/transcript"), "order 42") })

	var rts struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	poll("the runtime to connect", func() bool {
		return json.Unmarshal([]byte(get("/api/runtimes")), &rts) == nil && len(rts.Runtimes) == 1 &&
			srv.Runtime().Connected(rts.Runtimes[0].ID) // registered AND streaming: else the command is a 503
	})
	body := fmt.Sprintf(`{"command_id":"cmd_demo","runtime":%q,"agent":"studio-local",
	  "source":{"run_id":%q,"from_step":0},"input":"where is order 9?","engine":"live","thread":"ephemeral"}`,
		rts.Runtimes[0].ID, source)
	resp, err := http.Post(api.URL+"/api/playground/runs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("playground command = %d", resp.StatusCode)
	}
	var row struct {
		State  string  `json:"state"`
		Status string  `json:"status"`
		RunID  string  `json:"run_id"`
		Error  *string `json:"error"`
	}
	poll("the command to finish", func() bool {
		_ = json.Unmarshal([]byte(get("/api/playground/commands/cmd_demo")), &row)
		return row.State == "finished" || row.State == "rejected" || row.State == "lost"
	})
	if row.State != "finished" || row.Status != "succeeded" || !strings.HasPrefix(row.RunID, "pg_") {
		t.Fatalf("command row = %+v", row)
	}
	poll("the experiment's run in the sink", func() bool { return strings.Contains(get("/api/runs/"+row.RunID+"/transcript"), "order 9") })
}

// Every /run is a turn of the demo's one session: the second request
// lands on the Session the first created — it used to open the
// session again without closing the first Session, and the writer
// lease refused its Send with ErrLocked. Durable storage, as serve
// wires it.
func TestRunTwiceIsOneSession(t *testing.T) {
	st, err := jsonl.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := newDemo(st, weft.New(echoModel{}, lookupOrder))
	var bodies []string
	for _, q := range []string{"where is order 42?", "and order 43?", "thanks"} {
		w := httptest.NewRecorder()
		d.run(w, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(q)))
		if w.Code != http.StatusOK {
			t.Fatalf("/run %q: %d %s", q, w.Code, w.Body.String())
		}
		bodies = append(bodies, w.Body.String())
	}
	id := d.s.ID()
	for i, b := range bodies {
		if !strings.Contains(b, "("+id+")") || strings.Contains(b, "locked") {
			t.Errorf("run %d answered %q, want a turn of session %s", i+1, b, id)
		}
	}
	turns := 0
	for _, e := range d.s.Entries() {
		if _, ok := e.(thread.TurnEntry); ok {
			turns++
		}
	}
	if turns != len(bodies) {
		t.Errorf("the session holds %d turns, want %d", turns, len(bodies))
	}
	page, err := thread.List(context.Background(), st, thread.Query{})
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("the storage holds %d sessions (%v), want the one", len(page.Sessions), err)
	}
}

// Every /run response carries the Weft-Scope header (the development
// rung, C3.1): the demo's public id, narrowed to the turn's run — the
// run id the body names — and exposed to cross-origin pages. No
// session field (the Done line's shape: pub_demo;run=<id>) and never a
// token.
func TestRunCarriesTheScopeHeader(t *testing.T) {
	d := newDemo(thread.Memory(), weft.New(echoModel{}, lookupOrder))
	srv := httptest.NewServer(d.handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/run", "text/plain", strings.NewReader("where is order 42?"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	fields := strings.Fields(string(body))
	if resp.StatusCode != http.StatusOK || len(fields) < 2 || fields[0] != "run" {
		t.Fatalf("/run: %d %q", resp.StatusCode, body)
	}
	want := "pub_demo;run=" + fields[1]
	if got := resp.Header.Values("Weft-Scope"); len(got) != 1 || got[0] != want {
		t.Errorf("Weft-Scope = %q, want [%s]", got, want)
	}
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != "Weft-Scope" {
		t.Errorf("Access-Control-Expose-Headers = %q, want Weft-Scope", got)
	}
	if got := scope.Parse(resp.Header.Get("Weft-Scope")); got.SessionID != "" || got.PublicID != "pub_demo" {
		t.Errorf("the header's scope is %#v, want pub_demo and no session", got)
	}
}

// The landing page's panel tag names no scope (plan C3.2): no
// data-public-id, no data-scope — the panel scopes itself from the
// Weft-Scope header of the page's own fetch to /run (detection rung
// 2), which the page's ask form makes. The panel's own suite proves
// the scoping (studio/web/src/panel/detect.test.ts, the Done line);
// this pins the page that suite models.
func TestPageScopesFromTheRunHeader(t *testing.T) {
	var tag string
	for _, line := range strings.Split(page, "\n") {
		if strings.Contains(line, `src="/studio/panel.js"`) {
			tag = line
		}
	}
	if tag == "" {
		t.Fatal("the page carries no panel script tag")
	}
	if !strings.Contains(tag, "data-weft") {
		t.Errorf("the panel tag %q lacks data-weft", tag)
	}
	for _, attr := range []string{"data-public-id", "data-scope", "data-detect"} {
		if strings.Contains(tag, attr) {
			t.Errorf("the panel tag %q carries %s: the scope must come from the /run header", tag, attr)
		}
	}
	if !strings.Contains(page, `fetch("/run"`) {
		t.Error("the page does not fetch /run: nothing would carry the scope header to the panel")
	}
}

// The deterministic parking path (plan C4.2): a refund question makes
// the echo model call refund_order, a weft.RequireApproval tool, so the
// turn's run ends successfully with that one call pending — the run the
// panel reports once through on("parked"), with the call id the
// approval takes. The demo has no approver: the next question denies
// the pending call first, so the session is not held behind it.
func TestRefundParksOnApproval(t *testing.T) {
	path := t.TempDir() + "/weft.db"
	defer otel.Install(otel.NoEnv(), otel.Local(path))()
	agent := weft.New(echoModel{}, weft.Name("studio-local"), lookupOrder, refundOrder) // the demo's tools, unpaced
	d := newDemo(thread.Memory(), agent)
	ask := func(q string) string {
		t.Helper()
		w := httptest.NewRecorder()
		d.run(w, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(q)))
		if w.Code != http.StatusOK {
			t.Fatalf("/run %q: %d %s", q, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	body := ask("refund order 42")
	if !strings.Contains(body, "awaiting approval: refund_order (call "+refundCallID+")") {
		t.Fatalf("the refund turn answered %q, want it parked on refund_order", body)
	}
	runID := strings.Fields(body)[1]
	pending := d.s.Pending()
	if len(pending) != 1 || pending[0].CallID != refundCallID || pending[0].Tool != "refund_order" {
		t.Fatalf("pending = %+v, want the one refund_order call %s", pending, refundCallID)
	}
	// The recorded run: succeeded, one call pending (the row the panel
	// reads as parked), its run_finish naming the call.
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for {
		det, err := otel.LocalDB().Run(ctx, runID)
		if err == nil && det.Status == obsdb.StatusSucceeded && det.Pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never read succeeded with 1 pending: %+v (%v)", runID, det, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	events, err := otel.LocalDB().Events(ctx, runID, -1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	finish := ""
	for _, pe := range events.Events {
		if strings.Contains(string(pe.Event), `"run_finish"`) {
			finish = string(pe.Event)
		}
	}
	if !strings.Contains(finish, `"pending"`) || !strings.Contains(finish, refundCallID) {
		t.Errorf("run_finish = %s, want its pending list naming %s", finish, refundCallID)
	}
	// The next question is not held behind the parked call.
	if next := ask("where is order 43?"); !strings.Contains(next, `About "where is order 43?": order 43`) {
		t.Errorf("the next turn answered %q", next)
	}
	if p := d.s.Pending(); len(p) != 0 {
		t.Errorf("still pending after the next question: %+v", p)
	}
}

// The page drives the panel from its own UI through window.weft.devtools
// (plan C4): a "debug this" button per reply that scopes the panel to the
// turn and selects its step, a "report this run" link built from
// on("run") by the panel's studioLink, and on("parked") counted into
// <body data-parked-count>. The panel's own suite proves each call
// (studio/web/src/panel/api.test.ts); this pins the page that uses them.
func TestPageDrivesThePanel(t *testing.T) {
	for _, want := range []string{
		`<body data-parked-count="0">`,
		`debug.className = "debug-this"`,
		`debug.textContent = "debug this"`,
		`window.weft && window.weft.devtools`,
		`devtools.scope("pub_demo;run=" + b.dataset.run)`,
		`devtools.select(b.dataset.run, 0)`,
		`devtools.on("run", (d) => {`,
		`report.href = devtools.studioLink(d.runId, d.step)`,
		`devtools.on("parked", () => {`,
		`document.body.dataset.parkedCount`,
		`<a id="report"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	// The page builds no Studio URL of its own: the link is the panel's.
	if strings.Contains(page, "/studio/runs") {
		t.Error("the page hand-builds a Studio run URL; it must ask studioLink")
	}
}
