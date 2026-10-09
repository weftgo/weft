package obsdbtest

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// messagesAsOf: ADR 0029's reader (obsdb.MessagesAsOf) answers alike on
// every backend. A three-step run whose step 2 request carried a
// PrepareStep view (index 5, replacing transcript seqs [1, 3) with a
// summary): step 0 is the input, step 1 the plain transcript through
// its ref, step 2 the view applied; a step past the run is ErrNotFound;
// a content-off chain's run (stripped requests, no messages) is
// ErrStepMessages with the stripped hole.
func messagesAsOf(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		f := &fxRun{id: "asof"}
		f.start(true)
		msg := func(index int64, step int, role, text string, extra map[string]any) {
			a := map[string]any{"weft.messages.index": index, "weft.step.index": int64(step), "weft.content": "full"}
			for k, v := range extra {
				a[k] = v
			}
			f.add("messages", "weft.messages", `[{"role":"`+role+`","content":[{"type":"text","text":"`+text+`"}]}]`, a)
		}
		msg(0, 0, "user", "u0", map[string]any{"weft.messages.input": true})
		f.request(0, 0, 1, fxSystem1, fxCatalog1, "full", fxRequest(0, 1, fxSystem1, fxCatalog1, "lookup", "script", 0, 1))
		msg(1, 0, "assistant", "a0", nil)
		msg(2, 0, "user", "t0", nil)
		f.request(1, 1, 1, fxSystem1, fxCatalog1, "full", fxRequest(1, 1, fxSystem1, fxCatalog1, "lookup", "script", 2, 3))
		msg(3, 1, "assistant", "a1", nil)
		msg(4, 1, "user", "t1", nil)
		msg(5, 2, "user", "summary", map[string]any{"weft.messages.reason": "compacted",
			"weft.messages.from_seq": int64(1), "weft.messages.to_seq": int64(3), "weft.messages.count": int64(1),
			"weft.compaction.scope": "run", "weft.compaction.hash": "h_asof"})
		f.request(2, 2, 1, fxSystem1, fxCatalog1, "full", fxRequest(2, 1, fxSystem1, fxCatalog1, "lookup", "script", 5, 4))
		msg(6, 2, "assistant", "a2", nil)
		f.finish(1, 3)
		if err := db.Write(ctx(), obsdb.Batch{Records: f.recs}); err != nil {
			t.Fatal(err)
		}
		texts := func(ms []core.Message) string {
			b, _ := json.Marshal(func() []string {
				var out []string
				for _, m := range ms {
					out = append(out, m.Text())
				}
				return out
			}())
			return string(b)
		}
		for step, want := range map[int]string{
			0: `["u0"]`,
			1: `["u0","a0","t0"]`,
			2: `["u0","summary","a1","t1"]`,
		} {
			sm, err := obsdb.MessagesAsOf(ctx(), db, "asof", step)
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			if got := texts(sm.Messages); got != want || (sm.View != nil) != (step == 2) || sm.Derived {
				t.Errorf("step %d = %s (view %v, derived %v), want %s", step, got, sm.View != nil, sm.Derived, want)
			}
			if sm.View != nil && (sm.View.Index != 5 || sm.View.FromSeq != 1 || sm.View.ToSeq != 3 || sm.View.Hash != "h_asof") {
				t.Errorf("step 2's view = %+v", sm.View)
			}
		}
		if _, err := obsdb.MessagesAsOf(ctx(), db, "asof", 7); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("step 7 = %v, want ErrNotFound", err)
		}
		if _, err := obsdb.MessagesAsOf(ctx(), db, "asof_nope", 0); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("unknown run = %v, want ErrNotFound", err)
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: strippedFixture("asof_off")}); err != nil {
			t.Fatal(err)
		}
		var se *obsdb.StepMessagesError
		if _, err := obsdb.MessagesAsOf(ctx(), db, "asof_off", 1); !errors.As(err, &se) || se.Hole != obsdb.HoleStripped {
			t.Errorf("content-off run = %v, want ErrStepMessages (stripped)", err)
		}
	}
}
