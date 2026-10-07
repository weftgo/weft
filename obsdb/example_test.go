package obsdb_test

import (
	"context"
	"errors"
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

// A content-off destination's copy of a run keeps its request records
// stripped — hashes, names and numbers — and drops the prompt and tools
// records they name. Requests reads the attempts; Prompt answers the
// missing text with ErrNotFound carrying the stripped badge (a
// HoleError), which a reader shows instead of a blank.
func ExampleDB_Requests() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	at := time.Unix(0, 1790845923120000000).UTC()
	err = db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{{
		Time: at, EventName: "weft.event", Body: `{"type":"run_start","id":"run_1"}`,
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": "run_1", "weft.event.type": "run_start",
			"weft.event.pos": int64(0), "weft.instructions.hash": "ih"},
	}, {
		Time: at, EventName: "weft.request",
		Body: `{"step":0,"attempt":1,"system_hash":"sh","messages_ref":{"count":1},"tools":{"catalog_hash":"","names":[]},"sequential_tools":false,"params":{},"model":{"name":"m"},"stream":true}`,
		Attrs: map[string]any{"weft.record": "request", "weft.run.id": "run_1", "weft.content": "stripped",
			"weft.request.index": int64(0), "weft.step.index": int64(0), "weft.attempt.index": int64(1),
			"weft.system.hash": "sh"},
	}}})
	if err != nil {
		log.Fatal(err)
	}
	reqs, err := db.Requests(ctx, "run_1", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1})
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range reqs {
		fmt.Println(r.Index, r.Step, r.Attempt, r.SystemHash, r.Body.Model.Name, r.Content)
	}
	var hole *obsdb.HoleError
	_, err = db.Prompt(ctx, "run_1", "sh")
	fmt.Println(errors.Is(err, obsdb.ErrNotFound), errors.As(err, &hole) && hole.Hole == obsdb.HoleStripped)
	// Output:
	// 0 0 1 sh m stripped
	// true true
}
