package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/wefttest"
)

// A steered run records its Steered events with every other event —
// the recorder's run-id fold knows the type, and the event round-trips
// byte-for-byte through the store (ADR 0019 §6: a record shows what
// the model saw and when).
func TestRecordSteeredEvents(t *testing.T) {
	s := store.Memory()
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	steer := wefttest.NewSteers().At(0, weft.User("switch to metric units"))
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.Say("done"),
		),
		weft.Name("steered"),
		store.Record(s),
		echo,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("convert this"), steer.Option())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	var steered []weft.Steered
	for _, ev := range got.Events {
		if e, ok := ev.(weft.Steered); ok {
			steered = append(steered, e)
		}
	}
	if len(steered) != 1 {
		t.Fatalf("recorded %d Steered events, want 1 (the run-id fold must not drop the type)", len(steered))
	}
	if steered[0].Step != 0 || len(steered[0].Messages) != 1 || steered[0].Messages[0].Text() != "switch to metric units" {
		t.Errorf("recorded Steered = %+v, want step 0 with the delivered message", steered[0])
	}
	// The recorded bytes are the wire bytes: a Steered re-marshals to
	// the same JSON the stream carried.
	b, err := json.Marshal(steered[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"steered","run_id":"` + res.ID + `","seq":3,"step":0,"messages":[{"role":"user","content":[{"type":"text","text":"switch to metric units"}]}]}`
	if string(b) != want {
		t.Errorf("recorded Steered bytes:\n got  %s\n want %s", b, want)
	}
}
