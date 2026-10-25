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

// A status filter as the read API takes it (I3): the four derived
// statuses and the read-time parked and cancelled are filters; any other
// word is an error naming them, never an empty list. A read-time word
// narrows its derived status (Stored) with a condition of its own.
// A weft flag read the same in both spellings: the bool a fixture or
// the OTLP wire carries, and the string "true" a store that stringifies
// attributes (ClickHouse behind a stock collector) reads back.
func ExampleAttrTrue() {
	fmt.Println(
		obsdb.AttrTrue(map[string]any{"weft.tool.denied": true}, "weft.tool.denied"),
		obsdb.AttrTrue(map[string]any{"weft.tool.denied": "true"}, "weft.tool.denied"),
		obsdb.AttrTrue(map[string]any{}, "weft.tool.denied"),
	)
	// Output: true true false
}

// A session's state over time (U16): a busy-policy interrupt is ended,
// and another cancel cancelled (U16-fix) — neither a failure; a decided
// park resumed; the newest turn's open park is the session's Pending —
// it awaits a decision.
func ExampleSessionTurnFacts() {
	turns := []obsdb.RunRow{
		{ID: "s-t1", Turn: 1, Status: obsdb.StatusSucceeded, Pending: 1, ResumedBy: "s-t2"},
		{ID: "s-t2", Turn: 2, Status: obsdb.StatusSucceeded, ResumedFrom: "s-t1"},
		{ID: "s-t3", Turn: 3, Status: obsdb.StatusFailed, ErrorType: obsdb.ErrorTypeCancelled, CancelCause: obsdb.BusyCancelCause},
		{ID: "s-t4", Turn: 4, Status: obsdb.StatusFailed, ErrorType: "model_error"},
		{ID: "s-t5", Turn: 5, Status: obsdb.StatusFailed, ErrorType: obsdb.ErrorTypeCancelled, CancelCause: "thread.closed"},
		{ID: "s-t6", Turn: 6, Status: obsdb.StatusSucceeded, Pending: 2},
	}
	var row obsdb.SessionRow
	obsdb.SessionTurnFacts(&row, turns)
	fmt.Println(row.Outcomes)
	fmt.Println("pending", row.Pending, "failed", row.FailedTurns, "ended", row.EndedTurns,
		"cancelled", row.CancelledTurns, "resumes", row.Resumes)
	// Output:
	// [resumed succeeded ended failed cancelled parked]
	// pending 2 failed 1 ended 1 cancelled 1 resumes 1
}

// The sessions list's state filters (U16, U16-fix) and a session's
// events of a few kinds: a session thread compacted, its newest turn
// succeeded — SessionQuery's Compacted and Status keep it — and its
// compaction marker read alone among its events (SessionEventQuery.Kinds,
// what Studio's turn marks read).
func ExampleSessionQuery_states() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	ident := map[string]any{"weft.run.id": "s_ex-t1", "weft.session.id": "s_ex", "weft.public_id": "pub_ex", "weft.turn": int64(1)}
	span := obsdb.Span{TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0a0b0c0d0e0f0102",
		Name: "invoke_agent ex", Kind: 1, Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "ex",
		Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent"}}
	label := obsdb.Record{Time: at, EventName: "weft.thread.label", Body: `{"kind":"label","entry":"e1","name":"start"}`,
		Attrs: map[string]any{"weft.record": obsdb.RecordThread}, Service: "ex", Severity: 9}
	marker := obsdb.Record{Time: at.Add(time.Second), EventName: "weft.compaction", Body: `{"scope":"session","hash":"sha256:ex","replaced":4}`,
		Attrs:   map[string]any{"weft.record": obsdb.RecordCompaction, "weft.compaction.scope": "session", "weft.compaction.hash": "sha256:ex"},
		Service: "ex", Severity: 9}
	for k, v := range ident {
		span.Attrs[k], label.Attrs[k], marker.Attrs[k] = v, v, v
	}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{span}, Records: []obsdb.Record{label, marker}}); err != nil {
		log.Fatal(err)
	}
	page, err := db.Sessions(ctx, obsdb.SessionQuery{Compacted: true, Status: obsdb.StatusSucceeded})
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range page.Sessions {
		fmt.Println(r.ID, r.Status)
	}
	evs, err := db.(obsdb.SessionEventReader).SessionEvents(ctx, obsdb.SessionEventQuery{SessionID: "s_ex", Kinds: []string{obsdb.RecordCompaction}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(evs.Events), evs.Total, obsdb.SessionEventKind(evs.Events[0]))
	// Output:
	// s_ex succeeded
	// 1 1 compaction
}

func ExampleParseStatus() {
	st, err := obsdb.ParseStatus("parked")
	fmt.Println(st, st.Stored(), err)
	_, err = obsdb.ParseStatus("completed")
	fmt.Println(errors.Is(err, obsdb.ErrInvalidStatus))
	fmt.Println(err)
	// Output:
	// parked succeeded <nil>
	// true
	// status must be running, succeeded, failed, interrupted, parked or cancelled ("completed" given)
}

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

// A Searcher finds runs by what they said and did: every term a
// case-insensitive substring of the run's prompt, answer, error or a
// tool name, each term in any of them. The run row carries its prompt
// preview.
func ExampleSearcher() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	t0 := time.Unix(0, 1790845923120000000).UTC()
	write := func(run, prompt, answer string, at time.Duration) {
		rec := func(idx int64, body string) obsdb.Record {
			return obsdb.Record{Time: t0.Add(at), Body: body, Attrs: map[string]any{
				"weft.record": "messages", "weft.run.id": run, "weft.messages.index": idx}}
		}
		if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
			rec(0, `[{"role":"user","content":[{"type":"text","text":"`+prompt+`"}]}]`),
			rec(1, `[{"role":"assistant","content":[{"type":"text","text":"`+answer+`"}]}]`),
		}}); err != nil {
			log.Fatal(err)
		}
	}
	write("run_1", "Refund order 7", "The refund failed: card declined.", 0)
	write("run_2", "Refund order 9", "Refunded.", time.Second)

	page, err := db.(obsdb.Searcher).Search(context.Background(), obsdb.SearchQuery{Text: "refund failed"})
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range page.Runs {
		fmt.Println(r.ID, "-", r.PromptPreview)
	}
	// Output:
	// run_1 - Refund order 7
}

// A CommandStore keeps what Studio asked a runtime to do across a
// restart: the command with its state and the times a restarted Studio
// decides "lost" from, the route from the run an ack named back to the
// runtime, and the runtime's breakpoint set.
func ExampleCommandStore() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	cs, ok := db.(obsdb.CommandStore)
	if !ok {
		log.Fatal("this backend keeps commands in memory only")
	}
	ctx := context.Background()
	t0 := time.Unix(0, 1790845923120000000).UTC()

	cmd := obsdb.StoredCommand{ID: "cmd_1", RuntimeID: "rt_1", State: obsdb.CommandQueued,
		Created: t0, Body: []byte(`{"command_id":"cmd_1","agent":"support"}`)}
	if err := cs.PutCommand(ctx, cmd); err != nil {
		log.Fatal(err)
	}
	// The accepted ack names the run: the row moves on, the route is learned.
	cmd.State, cmd.RunID, cmd.Updated, cmd.AcceptedAt = obsdb.CommandAccepted, "run_1", t0.Add(time.Second), t0.Add(time.Second)
	if err := cs.PutCommand(ctx, cmd); err != nil {
		log.Fatal(err)
	}
	if err := cs.PutRoute(ctx, obsdb.RunRoute{RunID: "run_1", RuntimeID: "rt_1", CommandID: "cmd_1", Seen: cmd.Updated}); err != nil {
		log.Fatal(err)
	}
	if err := cs.PutBreakpoints(ctx, "rt_1", []string{"issue_refund"}); err != nil {
		log.Fatal(err)
	}

	live, err := cs.Commands(ctx, obsdb.CommandQuery{States: []string{obsdb.CommandQueued, obsdb.CommandAccepted}})
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range live {
		fmt.Println(c.ID, c.State, c.RunID, "accepted at", c.AcceptedAt.Sub(c.Created))
	}
	route, _, _ := cs.Route(ctx, "run_1")
	tools, _, _ := cs.Breakpoints(ctx, "rt_1")
	fmt.Println("run_1 →", route.RuntimeID, "parks on", tools)
	// Output:
	// cmd_1 accepted run_1 accepted at 1s
	// run_1 → rt_1 parks on [issue_refund]
}

// A SessionEventReader reads a session's state log — thread's records
// and its compaction markers — in record-time order from the
// session_events index, a record filed under no run (held at Close)
// among them: the session timeline's one read.
func ExampleSessionEventReader() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	t0 := time.Unix(0, 1790845923120000000).UTC()
	fact := func(run, kind, body string, at time.Duration) obsdb.Record {
		return obsdb.Record{Time: t0.Add(at), EventName: "weft.thread." + kind, Body: body, Attrs: map[string]any{
			"weft.record": obsdb.RecordThread, "weft.run.id": run, "weft.session.id": "s_1", "weft.content": "none"}}
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		fact("s_1-t1", "approval.request", `{"kind":"approval.request","session":"s_1","entry":"e3","call_id":"c1","tool":"refund"}`, 0),
		fact("s_1-t1", "approval.decision", `{"kind":"approval.decision","session":"s_1","entry":"e4","call_id":"c1","outcome":"deny","via":"user"}`, time.Second),
		fact("", "grant.revoke", `{"kind":"grant.revoke","session":"s_1","entry":"e9","grant_id":"g1"}`, 2*time.Second),
	}}); err != nil {
		log.Fatal(err)
	}
	page, err := db.(obsdb.SessionEventReader).SessionEvents(context.Background(), obsdb.SessionEventQuery{SessionID: "s_1"})
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range page.Events {
		fmt.Printf("%q %s\n", e.RunID, obsdb.SessionEventKind(e))
	}
	fmt.Println(page.Total)
	// RunIDs narrows the read to the named runs' events (one turn's
	// decisions); the runless record is under none of them.
	turn, err := db.(obsdb.SessionEventReader).SessionEvents(context.Background(),
		obsdb.SessionEventQuery{SessionID: "s_1", RunIDs: []string{"s_1-t1"}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(turn.Total)
	// Output:
	// "s_1-t1" approval.request
	// "s_1-t1" approval.decision
	// "" grant.revoke
	// 3
	// 2
}

// An Aggregator answers numbers across runs: here, per agent over a
// day, from the spans the writer stored — SQLite's span_stats rollup
// (ClickHouse groups otel_traces natively). The error class folds the
// failures that differ only in their numbers.
func ExampleAggregator() {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	run := func(id string, at time.Duration, in int64, failure string) {
		root := map[string]any{"weft.run.id": id, "gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "researcher"}
		chat := map[string]any{"weft.run.id": id, "gen_ai.operation.name": "chat", "weft.step.index": int64(0),
			"gen_ai.provider.name": "zai", "gen_ai.request.model": "glm-5.3-flash",
			"gen_ai.usage.input_tokens": in, "gen_ai.usage.output_tokens": int64(5), "weft.ttft_ms": int64(400)}
		status := 1
		if failure != "" {
			status, root["error.type"] = 2, "usage_limit"
		}
		trace := fmt.Sprintf("%032x", at)
		if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{
			{TraceID: trace, SpanID: "0000000000000002", ParentSpanID: "0000000000000001", Name: "chat glm-5.3-flash",
				Start: t0.Add(at), End: t0.Add(at + time.Second), StatusCode: 1, Attrs: chat},
			{TraceID: trace, SpanID: "0000000000000001", Name: "invoke_agent researcher",
				Start: t0.Add(at), End: t0.Add(at + 2*time.Second), StatusCode: status, StatusMessage: failure, Attrs: root},
		}}); err != nil {
			log.Fatal(err)
		}
	}
	run("run_1", 0, 20, "")
	run("run_2", time.Hour, 30, "weft: run exceeded its usage limit (input 30/25, output 5/0)")
	run("run_3", 2*time.Hour, 41, "weft: run exceeded its usage limit (input 41/25, output 5/0)")

	agg := db.(obsdb.Aggregator)
	page, err := agg.Stats(context.Background(), obsdb.StatsQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"},
		By: obsdb.StatsByAgent, Since: t0, Until: t0.Add(24 * time.Hour)})
	if err != nil {
		log.Fatal(err)
	}
	for _, g := range page.Groups {
		fmt.Printf("%s: %d runs, %d failed, %d input tokens, p50 %gms, TTFT p50 %gms\n",
			g.Key, g.Runs, g.Failed, g.Usage.InputTokens, g.Duration.P50, g.TTFT.P50)
	}
	counts, _, err := agg.ErrorCounts(context.Background(), obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"}})
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range obsdb.ErrorClasses(counts, 0) {
		fmt.Println(c.Count, c.Class)
	}
	// Output:
	// researcher: 3 runs, 2 failed, 91 input tokens, p50 2000ms, TTFT p50 400ms
	// 2 weft: run exceeded its usage limit (input N/N, output N/N)
}

// ErrorClass reads an error text as its class: numbers N, ids <id>,
// quoted strings and names kept.
func ExampleErrorClass() {
	fmt.Println(obsdb.ErrorClass("weft: run failed at step 2: weft: run exceeded its usage limit (input 30/25, output 15/0)"))
	fmt.Println(obsdb.ErrorClass(`tool "lookup_42" retried 3 times, past MaxModelRetries(2)`))
	// Output:
	// weft: run failed at step N: weft: run exceeded its usage limit (input N/N, output N/N)
	// tool "lookup_42" retried N times, past MaxModelRetries(N)
}
