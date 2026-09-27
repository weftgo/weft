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
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("skip error %q does not name the line", err)
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

// slowSaveStore delays every Save of a running row: the shape of a
// heartbeat touch caught mid-write when the run ends.
type slowSaveStore struct {
	Store
	delay time.Duration
}

func (s *slowSaveStore) Save(ctx context.Context, r RunRecord) error {
	if r.Status == Running {
		time.Sleep(s.delay)
	}
	return s.Store.Save(ctx, r)
}

// A heartbeat touch in flight when the run ends must not land after
// the closing write: the row's final state is the outcome, never a
// stale running row that reads as interrupted half a minute later.
func TestRecordTouchNeverOutlivesEnd(t *testing.T) {
	inner := Memory()
	s := &slowSaveStore{Store: inner, delay: 60 * time.Millisecond}
	rec := &recorder{store: s, heartbeatEvery: 5 * time.Millisecond, now: time.Now, tags: map[string]string{}}
	ctx := context.Background()
	rec.start(ctx, weft.RunStart{ID: "race-1", Agent: "race"})
	time.Sleep(20 * time.Millisecond) // a touch is now blocked inside Save
	rec.end(ctx, &weft.RunResult{ID: "race-1"}, nil)
	time.Sleep(2 * s.delay) // give a touch that escaped the lock time to land
	got, err := inner.Get(ctx, "race-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Succeeded {
		t.Fatalf("after end, status = %q, want succeeded (a heartbeat touch overwrote the closing row)", got.Status)
	}
	if got.Finished.IsZero() {
		t.Error("after end, Finished is zero: the closing row was overwritten")
	}
}

// A run id that restarts in-process (its earlier run never ended —
// a crash, or a hand-reused weft.RunID) must close the old log: an
// orphaned ticker would keep re-saving the stale running row under
// the id and clobber the new run's — including its closing write.
func TestRecordRestartedRunClosesOrphan(t *testing.T) {
	inner := Memory()
	rec := &recorder{store: inner, heartbeatEvery: 5 * time.Millisecond, now: time.Now, tags: map[string]string{}}
	ctx := context.Background()
	rec.start(ctx, weft.RunStart{ID: "again", Agent: "a"})
	time.Sleep(12 * time.Millisecond) // the first ticker has ticked
	rec.start(ctx, weft.RunStart{ID: "again", Agent: "a"})
	rec.end(ctx, &weft.RunResult{ID: "again"}, nil)
	time.Sleep(50 * time.Millisecond) // the orphan's window to overwrite
	got, err := inner.Get(ctx, "again")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Succeeded {
		t.Errorf("after a restarted id ended, status = %q, want succeeded (the orphaned ticker overwrote the closing row)", got.Status)
	}
	if got.Finished.IsZero() {
		t.Error("after a restarted id ended, Finished is zero: the closing row was overwritten")
	}
}

// A nil Store is a construction bug: Record panics at the call site
// (Subagent's nil-child panic is the core's precedent) instead of
// failing on — or silently swallowing — every write that follows.
func TestRecordNilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Record(nil) did not panic")
		}
	}()
	_ = Record(nil)
}
