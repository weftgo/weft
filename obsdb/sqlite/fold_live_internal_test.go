package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The writer announces a derived row it drops (I6-fix2): an inner agent
// without a conversation id, a run while its parent was not stored, is
// published Removed once the parent's export folds it — after the
// enclosing run's own frame.
func TestFoldPublishesRemoved(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, err := db.(*DB).Hub().Subscribe(ctx, obsdb.Selector{All: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	const tr = "6d0c0ffee0000000000000000000d001"
	at := time.Now().Add(-time.Minute)
	sp := func(id, parent, name string, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: name, Start: at, End: at.Add(time.Second), Attrs: attrs}
	}
	inner := sp("6d00000000000002", "6d00000000000001", "invoke_agent helper", map[string]any{"gen_ai.agent.name": "helper"})
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{inner}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{sp("6d00000000000001", "", "invoke_agent planner", nil)}}); err != nil {
		t.Fatal(err)
	}
	innerID := obsdb.StockRunID(tr, inner.SpanID)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-frames:
			if f.Kind == obsdb.FrameRun && f.Run.ID == innerID && f.Run.Removed {
				if f.Run.Agent != "helper" || !f.Run.Derived {
					t.Errorf("removed frame = %+v, want the helper's, derived", f.Run)
				}
				return
			}
		case <-deadline:
			t.Fatal("no Removed frame for the folded inner agent")
		}
	}
}
