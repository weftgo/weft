package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
)

// The file rules (ADR 0010 §2.5): a torn final line is dropped, a bad
// middle line is skipped and reported, and the rest survive.
func TestReadJSONLEvents(t *testing.T) {
	var lines []string
	for _, ev := range []weft.Event{
		weft.RunStart{ID: "r"},
		weft.StepStart{RunID: "r", Index: 0},
		weft.TextDelta{RunID: "r", Text: "a"},
	} {
		b, _ := json.Marshal(ev)
		lines = append(lines, string(b))
	}
	good := strings.Join(lines, "\n") + "\n"

	torn := good + `{"type":"text_delta","run_id":"r","text":"cu` // no newline: cut mid-write
	bad := good + "{not json}\n" + `{"type":"text_delta","run_id":"r","text":"after"}` + "\n"

	evs, err := readJSONLEvents(torn)
	if err != nil || len(evs) != 3 {
		t.Errorf("torn tail: %d events, err %v; want 3, nil (tail dropped)", len(evs), err)
	}
	evs, err = readJSONLEvents(bad)
	if err == nil {
		t.Error("bad middle line: err = nil, want the skip reported")
	}
	if len(evs) != 4 {
		t.Errorf("bad middle line: %d events, want 4 (one skipped, the rest kept)", len(evs))
	}
	if d, ok := evs[3].(weft.TextDelta); !ok || d.Text != "after" {
		t.Errorf("events after the bad line = %#v, want kept", evs[3])
	}
}

// The ticker keeps a quiet-but-alive run's heartbeat fresh through the
// same touch path the live ticker drives (the real cadence is
// HeartbeatTimeout/3 — too slow for a test clock, so this drives a
// fast one directly).
func TestRecordHeartbeatTicker(t *testing.T) {
	s := Memory()
	rec := &recorder{store: s, heartbeatEvery: 20 * time.Millisecond, now: time.Now, tags: map[string]string{}}
	l := &runLog{stop: make(chan struct{}), logger: slog.Default()}
	l.rec = RunRecord{ID: "tick-check", Status: Running, Started: time.Now(), Heartbeat: time.Now()}
	rec.logs = map[string]*runLog{"tick-check": l}
	if err := s.Save(context.Background(), l.rec); err != nil {
		t.Fatal(err)
	}
	go rec.startTicker(context.Background(), l)
	time.Sleep(80 * time.Millisecond)
	close(l.stop)
	got, err := s.Get(context.Background(), "tick-check")
	if err != nil {
		t.Fatal(err)
	}
	if got.Heartbeat.Sub(l.rec.Started) < 40*time.Millisecond {
		t.Errorf("heartbeat advanced by %v; the ticker did not touch the row", got.Heartbeat.Sub(l.rec.Started))
	}
}
