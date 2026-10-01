package obsdb_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// One small batch — a run_start event and one messages record — is the
// durable shape every weft run writes. Events returns the positioned
// event bodies verbatim; Transcript returns the messages bodies in
// weft.messages.index order. Deltas would be counted, never stored.
func ExampleDB_Write() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	at := func(pos int64) time.Time {
		return time.Unix(0, 1790845923120000000).UTC().Add(time.Duration(pos) * time.Second)
	}
	rec := func(kind, eventType string, pos int64, body string) obsdb.Record {
		attrs := map[string]any{"weft.record": kind, "weft.run.id": "run_1"}
		if eventType != "" {
			attrs["weft.event.type"] = eventType
		}
		switch kind {
		case "event":
			attrs["weft.event.pos"] = pos
		case "messages":
			attrs["weft.messages.index"] = pos
		}
		return obsdb.Record{
			Time: at(pos), EventName: "weft." + kind, Body: body, Attrs: attrs,
		}
	}

	err = db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		rec("event", "run_start", 0,
			`{"type":"run_start","id":"run_1","agent":"greeter"}`),
		rec("messages", "", 0,
			`[{"role":"user","content":[{"type":"text","text":"hi"}]},`+
				`{"role":"assistant","content":[{"type":"text","text":"hello"}]}]`),
	}})
	if err != nil {
		log.Fatal(err)
	}

	events, err := db.Events(context.Background(), "run_1", -1, 100)
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range events.Events {
		fmt.Printf("%d %s\n", e.Pos, e.Event)
	}
	transcript, err := db.Transcript(context.Background(), "run_1")
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range transcript {
		fmt.Println(string(m))
	}
	// Output:
	// 0 {"type":"run_start","id":"run_1","agent":"greeter"}
	// [{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"text","text":"hello"}]}]
}
