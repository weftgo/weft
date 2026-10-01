package obsdb

import (
	"context"
	"testing"
	"time"
)

func recFrame(runID, sessionID, kind string) Frame {
	r := Record{EventName: "weft.event", Body: `{}`,
		Attrs: map[string]any{"weft.run.id": runID, "weft.record": kind, "weft.session.id": sessionID}}
	return RecordFrame(r)
}

// Frames are numbered by one hub-wide monotonic counter — the SSE id and
// the resume cursor — whatever selector each subscriber uses.
func TestHubSeqMonotonicAcrossSelectors(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	byRun, err := h.Subscribe(ctx, Selector{RunID: "r1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	bySess, err := h.Subscribe(ctx, Selector{SessionID: "s1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var last uint64
	for i := 0; i < 5; i++ {
		h.Publish(ctx, recFrame("r1", "s1", "event"))
		select {
		case f := <-byRun:
			if f.Seq <= last {
				t.Fatalf("seq %d after %d", f.Seq, last)
			}
			last = f.Seq
		case <-time.After(time.Second):
			t.Fatal("no frame on the run subscription")
		}
		select {
		case f := <-bySess:
			if f.Seq != last {
				t.Fatalf("session frame seq %d, run frame %d", f.Seq, last)
			}
		case <-time.After(time.Second):
			t.Fatal("no frame on the session subscription")
		}
	}
}

// A selector matches exactly its scope: a session subscription sees both
// its runs, a run subscription only its own frames, and an unset
// selector sees nothing (a live tail must be scoped, never a firehose).
func TestHubSelectorScoping(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	byRun, _ := h.Subscribe(ctx, Selector{RunID: "r1"}, 0)
	bySess, _ := h.Subscribe(ctx, Selector{SessionID: "s1"}, 0)
	byNone, _ := h.Subscribe(ctx, Selector{}, 0)
	h.Publish(ctx, recFrame("r1", "s1", "event"))
	h.Publish(ctx, recFrame("r2", "s1", "event")) // same session, other run
	h.Publish(ctx, recFrame("r9", "s9", "event")) // neither
	if n := drain(byRun); n != 1 {
		t.Errorf("run selector saw %d frames, want 1", n)
	}
	if n := drain(bySess); n != 2 {
		t.Errorf("session selector saw %d frames, want 2", n)
	}
	if n := drain(byNone); n != 0 {
		t.Errorf("empty selector saw %d frames, want 0", n)
	}
}

// drain empties ch without blocking, counting buffered frames; a closed
// channel drains its buffer and stops (a closed-and-empty receive
// succeeds with ok=false — the naive loop never terminates).
func drain(ch <-chan Frame) int {
	n := 0
	for {
		select {
		case _, open := <-ch:
			if !open {
				return n
			}
			n++
		default:
			return n
		}
	}
}

// A slow subscriber is dropped, not the writer blocked: with a queue of
// three and a subscriber that never reads, the fourth publish drops it
// (closing its channel once its buffered frames are drained), and later
// publishes reach the healthy subscriber.
func TestHubOverflowDropsSubscriber(t *testing.T) {
	h := NewHub(QueueSize(3))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow, err := h.Subscribe(ctx, Selector{SessionID: "s1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := h.Subscribe(ctx, Selector{SessionID: "s1"}, 0)
	for i := 0; i < 3; i++ { // fill slow's queue; keep fresh drained
		h.Publish(ctx, recFrame("r1", "s1", "event"))
		if drain(fresh) != 1 {
			t.Fatal("healthy subscriber missed a frame")
		}
	}
	h.Publish(ctx, recFrame("r1", "s1", "event")) // overflow: slow is dropped
	if n := drain(fresh); n != 1 {
		t.Fatalf("healthy subscriber missed the overflow-triggering frame")
	}
	if n := drain(slow); n != 3 {
		t.Fatalf("slow subscriber had %d buffered frames, want 3", n)
	}
	if _, open := <-slow; open {
		t.Error("slow subscriber's channel still open after overflow")
	}
	h.Publish(ctx, recFrame("r1", "s1", "event")) // the hub still works
	if drain(fresh) != 1 {
		t.Error("healthy subscriber starved by the dropped one")
	}
}

// The after cursor: a subscription resumes from a Seq, so frames at or
// below it are not delivered twice.
func TestHubAfterCursor(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, _ := h.Subscribe(ctx, Selector{SessionID: "s1"}, 0)
	h.Publish(ctx, recFrame("r1", "s1", "event"))
	f := <-first
	late, _ := h.Subscribe(ctx, Selector{SessionID: "s1"}, f.Seq)
	h.Publish(ctx, recFrame("r1", "s1", "event"))
	if n := drain(late); n != 1 {
		t.Fatalf("resumed subscription saw %d frames, want 1 (the pre-cursor frame not repeated)", n)
	}
	if n := drain(first); n != 1 {
		t.Fatalf("first subscription saw %d more frames, want 1", n)
	}
}

// Context end drops the subscription without closing it twice.
func TestHubContextEndDrops(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := h.Subscribe(ctx, Selector{RunID: "r1"}, 0)
	cancel()
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				goto closed
			}
		case <-deadline:
			t.Fatal("channel not closed after context end")
		}
	}
closed:
	// Publishing after the drop must not panic on the dead subscription.
	h.Publish(context.Background(), recFrame("r1", "s1", "event"))
}

// RecordFrame and RunFrame build the two frame kinds with identity
// derived.
func TestFrameBuilders(t *testing.T) {
	rf := RecordFrame(Record{Attrs: map[string]any{"weft.run.id": "r1", "weft.record": "delta", "weft.delta.pos": int64(3)}})
	if rf.Kind != FrameRecord || rf.Record == nil || rf.Run != nil {
		t.Fatalf("record frame = %+v", rf)
	}
	if rf.Weft.RunID != "r1" || rf.Weft.Record != "delta" || rf.Weft.Pos != 3 {
		t.Errorf("record frame identity = %+v", rf.Weft)
	}
	run := RunRow{ID: "r1", SessionID: "s1", Agent: "a", Turn: 2}
	uf := RunFrame(run)
	if uf.Kind != FrameRun || uf.Run == nil || uf.Record != nil {
		t.Fatalf("run frame = %+v", uf)
	}
	if uf.Weft.RunID != "r1" || uf.Weft.SessionID != "s1" || uf.Weft.Agent != "a" || uf.Weft.Turn != 2 {
		t.Errorf("run frame identity = %+v", uf.Weft)
	}
}
