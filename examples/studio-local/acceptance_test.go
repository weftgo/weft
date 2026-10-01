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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
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
