package store_test

import (
	"context"
	"fmt"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/wefttest"
)

// Record captures every run into the store — install it on every
// agent of a fleet; a subagent's child records itself and links back
// to the parent's call.
func ExampleRecord() {
	s := store.Memory()
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "roll_dice"}),
			wefttest.Say("rolled a 4"),
		),
		weft.Name("dice"),
		store.Record(s, store.Tags(map[string]string{"room": "table-1"})),
		weft.Tool("roll_dice", "Roll a die.",
			func(_ context.Context, _ struct{}) (int, error) { return 4, nil }),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("Roll.")); err != nil {
		fmt.Println(err)
		return
	}
	page, err := s.List(context.Background(), store.Query{})
	if err != nil {
		fmt.Println(err)
		return
	}
	rec := page.Runs[0]
	fmt.Printf("agent=%s tags=%v steps=%d tokens=%d status=%s\n",
		rec.Agent, rec.Tags, rec.Steps, rec.Usage.Total(), rec.Status)
	full, _ := s.Get(context.Background(), rec.ID)
	for _, ev := range full.Events {
		switch e := ev.(type) {
		case weft.RunStart:
			fmt.Printf("start agent=%s\n", e.Agent)
		case weft.ToolStart:
			fmt.Printf("tool %s\n", e.Name)
		case weft.RunFinish:
			fmt.Printf("finish steps=%d\n", e.Steps)
		}
	}
	// Output:
	// agent=dice tags=map[room:table-1] steps=2 tokens=30 status=succeeded
	// start agent=dice
	// tool roll_dice
	// finish steps=2
}
