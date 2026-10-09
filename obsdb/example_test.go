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
	reqs, err := db.Requests(ctx, "run_1", obsdb.RequestQuery{})
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

// ADR 0028 §8's worked example, read back as the model saw it: growth
// records 0 (the input, u1), 1 (a1) and 2 (t1), then step 1's
// PrepareStep sent [u1, s] — the core recorded view 3 replacing seqs
// [1, 3) with s, and step 1's request names it. MessagesAsOf answers
// each step's messages: step 0 the plain transcript, step 1 the view
// applied (the replay prefix for from_step 1, ADR 0029).
func ExampleMessagesAsOf() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	at := time.Unix(0, 1790845923120000000).UTC()
	text := func(role, t string) string {
		return `{"role":"` + role + `","content":[{"type":"text","text":"` + t + `"}]}`
	}
	msgs := func(index, step int64, body string, extra map[string]any) obsdb.Record {
		attrs := map[string]any{"weft.record": "messages", "weft.run.id": "run_1",
			"weft.messages.index": index, "weft.step.index": step}
		for k, v := range extra {
			attrs[k] = v
		}
		return obsdb.Record{Time: at, EventName: "weft.messages", Body: body, Attrs: attrs}
	}
	request := func(index, step int64, ref int64, count int) obsdb.Record {
		return obsdb.Record{Time: at, EventName: "weft.request",
			Body: fmt.Sprintf(`{"step":%d,"attempt":1,"messages_ref":{"index":%d,"count":%d},"tools":{"catalog_hash":"","names":[]},"params":{},"model":{"name":"m"}}`, step, ref, count),
			Attrs: map[string]any{"weft.record": "request", "weft.run.id": "run_1",
				"weft.request.index": index, "weft.step.index": step, "weft.attempt.index": int64(1)}}
	}
	err = db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		{Time: at, EventName: "weft.event", Body: `{"type":"run_start","id":"run_1"}`,
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": "run_1", "weft.event.type": "run_start", "weft.event.pos": int64(0)}},
		msgs(0, 0, "["+text("user", "u1")+"]", map[string]any{"weft.messages.input": true}),
		request(0, 0, 0, 1),
		msgs(1, 0, `[{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"lookup","args":{}}]}]`, nil),
		msgs(2, 0, `[{"role":"tool","content":[{"type":"tool_result","call_id":"c1","name":"lookup","content":"t1"}]}]`, nil),
		msgs(3, 1, "["+text("user", "s")+"]", map[string]any{"weft.messages.reason": "compacted",
			"weft.messages.from_seq": int64(1), "weft.messages.to_seq": int64(3),
			"weft.compaction.scope": "run", "weft.compaction.hash": "h1"}),
		request(1, 1, 3, 2),
	}})
	if err != nil {
		log.Fatal(err)
	}
	for _, step := range []int{0, 1} {
		sm, err := obsdb.MessagesAsOf(ctx, db, "run_1", step)
		if err != nil {
			log.Fatal(err)
		}
		var roles []string
		for _, m := range sm.Messages {
			roles = append(roles, string(m.Role))
		}
		fmt.Println(step, roles, sm.View != nil)
	}
	_, err = obsdb.MessagesAsOf(ctx, db, "run_1", 5)
	fmt.Println(errors.Is(err, obsdb.ErrNotFound))
	// Output:
	// 0 [user] false
	// 1 [user user] true
	// true
}
