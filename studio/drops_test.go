package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
)

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestEventStormGapNamesTheQueue is U5's Done line, end to end: an event
// storm (thousands of deltas, hundreds of events) through a Studio
// destination whose queue is smaller than the storm. The app logs one
// WARN for the destination, naming it and the knob; the run reaches
// Studio with its run_start and run_finish (shed last) but without most
// of its events, and the run document's gap says how many — "N of M
// events were lost", M the root span's weft.run.events — with a fix
// naming otel.QueueSize; the heartbeats that follow carry the
// destination's drop count, which api/meta's ingest health serves for
// the app's service.
func TestEventStormGapNamesTheQueue(t *testing.T) {
	logs := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := New(DB(db))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	const queue = 64
	p, err := otel.Start(context.Background(), otel.NoGlobal(), otel.NoEnv(), otel.Service("storm-app"),
		otel.Heartbeat(5*time.Millisecond),
		// Nothing drains before the flush: the storm meets a full queue
		// deterministically.
		otel.Studio(ts.URL, "", otel.QueueSize(queue), otel.BatchDelay(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	opts := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}

	// The storm: 20 steps of 100 deltas and 3 tool calls each.
	echo := core.Tool("echo", "Echoes.", func(context.Context, struct{}) (string, error) { return "ok", nil })
	var turns []wefttest.Turn
	for s := range 20 {
		var evs []core.ModelEvent
		for range 100 {
			evs = append(evs, core.ModelTextDelta{Text: "x"})
		}
		for c := range 3 {
			evs = append(evs, core.ModelToolCall{ID: fmt.Sprintf("c%d_%d", s, c), Name: "echo", Args: []byte(`{}`)})
		}
		evs = append(evs, core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}})
		turns = append(turns, wefttest.Raw(evs...))
	}
	turns = append(turns, wefttest.Say("done"))
	storm := core.New(wefttest.Script(turns...), append([]core.Option{core.Name("storm"), echo, core.MaxSteps(25)}, opts...)...)
	res, err := storm.Generate(context.Background(), core.Prompt("storm"))
	if err != nil {
		t.Fatal(err)
	}

	// A second run that stays open long enough to heartbeat: its
	// heartbeats carry the destination's drop count so far.
	wait := core.Tool("wait", "Waits.", func(ctx context.Context, _ struct{}) (string, error) {
		time.Sleep(60 * time.Millisecond)
		return "ok", nil
	})
	quiet := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "wait"}), wefttest.Say("ok")),
		append([]core.Option{core.Name("quiet"), wait}, opts...)...)
	if _, err := quiet.Generate(context.Background(), core.Prompt("wait")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}

	getJSON := func(path string, v any) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("GET %s: %v: %s", path, err, body)
		}
	}
	var doc struct {
		holesDoc
		EventCount    int64 `json:"event_count"`
		EventsEmitted int64 `json:"events_emitted"`
		Settling      bool  `json:"settling"`
	}
	getJSON("/api/runs/"+res.ID, &doc)
	if doc.Status != "succeeded" || doc.Settling {
		t.Errorf("storm run = %s (settling %v), want succeeded and settled: run_finish is shed last", doc.Status, doc.Settling)
	}
	if doc.EventsEmitted == 0 || doc.EventCount >= doc.EventsEmitted {
		t.Fatalf("stored %d of %d emitted events: the storm lost none (test bug)", doc.EventCount, doc.EventsEmitted)
	}
	want := regexp.MustCompile(fmt.Sprintf(`^%d of %d events were lost`, doc.EventsEmitted-doc.EventCount, doc.EventsEmitted))
	var gap bool
	for _, h := range doc.Holes {
		if h.Hole != "gap" {
			continue
		}
		gap = true
		if !want.MatchString(h.Reason) {
			t.Errorf("gap reason = %q, want %q", h.Reason, want)
		}
		if !strings.Contains(h.Fix, "otel.QueueSize") || !strings.Contains(h.Fix, "destination queue full") {
			t.Errorf("gap fix = %q, want the knob (otel.QueueSize) and the WARN named", h.Fix)
		}
	}
	if !gap {
		t.Errorf("storm run holes = %+v, want a gap", doc.Holes)
	}

	if n := strings.Count(logs.String(), "destination queue full"); n != 1 {
		t.Errorf("queue-full WARN lines = %d, want one for the destination:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "dest=studio") {
		t.Errorf("the WARN does not name the destination:\n%s", logs.String())
	}

	var meta struct {
		Ingest struct {
			Dropped []struct {
				Service string `json:"service"`
				Records int64  `json:"records"`
			} `json:"dropped"`
		} `json:"ingest"`
	}
	getJSON("/api/meta", &meta)
	if len(meta.Ingest.Dropped) != 1 || meta.Ingest.Dropped[0].Service != "storm-app" || meta.Ingest.Dropped[0].Records <= 0 {
		t.Errorf("meta ingest.dropped = %+v, want storm-app's count", meta.Ingest.Dropped)
	}
}
