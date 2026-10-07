// Command basic records demo runs — a tool call, a subagent, a
// failure — into an obsdb sqlite database and serves Studio on
// 127.0.0.1:7331:
//
//	go run ./studio/examples/basic [-serve] [-addr 127.0.0.1:7331] [dir]
//
// The database lives at <dir>/dev.db (default .weft) and persists
// across invocations; -serve skips recording and only serves; -addr
// picks the listen address (loopback by default — bind loopback until
// token auth exists, features doc L4). The manifest option feeds the
// agent and tool cards (H1).
//
// Recording here writes obsdb batches directly — the events, messages
// records and invoke span a run produces — so the example stays
// dependency-light. A real app records the same rows through weft/otel:
//
//	p, _ := otel.Start(ctx, otel.Local(path), otel.NoGlobal())
//	agent := weft.New(model, weft.LoggerProvider(p.LoggerProvider()),
//		weft.TracerProvider(p.TracerProvider()))
//
// (weft-arena's Studio mirror, internal/arena/studio.go, is the living
// example of that wiring.)
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/wefttest"
)

// demoVersion mirrors weft's version const (unexported there): the
// weft.version attribute every record and span carries.
const demoVersion = "v0.6.0"

func main() {
	serve := flag.Bool("serve", false, "serve only; record no new run")
	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	flag.Parse()
	dir := ".weft"
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	if err := run(dir, *serve, *addr); err != nil {
		fmt.Fprintln(os.Stderr, "basic:", err)
		os.Exit(1)
	}
}

func run(dir string, serveOnly bool, addr string) error {
	db, err := sqlite.Open(filepath.Join(dir, "dev.db"))
	if err != nil {
		return err
	}
	if !serveOnly {
		if err := record(context.Background(), db); err != nil {
			return err
		}
	}
	fmt.Printf("studio: http://%s/studio/\n", addr)
	return http.ListenAndServe(addr, handler(db))
}

// handler wires Studio under /studio/ with the demo manifest.
func handler(db obsdb.DB) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", studio.Handler(studio.DB(db),
		studio.Manifest(manifest()))))
	return mux
}

// stamp is a fixed-width base-36 nanosecond suffix, so a rerun lists
// another row under a fresh id.
func stamp() string {
	s := strconv.FormatInt(time.Now().UnixNano(), 36)
	for len(s) < 8 {
		s = "0" + s
	}
	return s[len(s)-8:]
}

// record writes three fresh demo runs into db — a plain tool call, a
// subagent delegation (the child run records itself and links back),
// and one failure — so the list shows every status. The batches are
// the ones weft/otel's Local destination writes for the same runs.
func record(ctx context.Context, db obsdb.DB) error {
	wd, _ := os.Getwd()
	meta := map[string]any{"cwd": wd}
	base := time.Now()
	toolRun := "demo-" + stamp() + "-0"
	delegRun := "demo-" + stamp() + "-1"
	failRun := "demo-" + stamp() + "-2"
	delegChild := delegRun + "/0/call_1"

	ok := demoRun(toolRun, "orders", base, 2*time.Second, 1, "", 10, 4)
	ok.Records = append(ok.Records,
		demoRecord(toolRun, "event", "tool_start", 2, base.Add(500*time.Millisecond), meta,
			`{"type":"tool_start","run_id":"`+toolRun+`","seq":1,"call_id":"call_1","name":"lookup_order","args":{"order_id":"42"}}`),
		demoRecord(toolRun, "event", "tool_finish", 3, base.Add(time.Second), meta,
			`{"type":"tool_finish","run_id":"`+toolRun+`","seq":1,"call_id":"call_1","name":"lookup_order","content":"order 42: shipped","is_error":false}`),
		demoRecord(toolRun, "messages", "", 1, base.Add(2*time.Second), meta,
			`[{"role":"assistant","content":[{"type":"text","text":"Order 42 shipped this morning."}]}]`),
	)

	child := demoRun(delegChild, "researcher", base.Add(3*time.Minute), 4*time.Second, 1, "", 9, 5)
	for i := range child.Records {
		child.Records[i].Attrs["weft.parent.run.id"] = delegRun
		child.Records[i].Attrs["weft.parent.call.id"] = "call_1"
	}
	child.Spans[0].Attrs["weft.parent.run.id"] = delegRun
	child.Spans[0].Attrs["weft.parent.call.id"] = "call_1"
	child.Spans[0].Attrs["gen_ai.agent.name"] = "researcher"

	deleg := demoRun(delegRun, "orders", base.Add(3*time.Minute), 5*time.Second, 1, "", 12, 6)
	deleg.Records = append(deleg.Records,
		demoRecord(delegRun, "event", "tool_start", 2, base.Add(3*time.Minute+500*time.Millisecond), meta,
			`{"type":"tool_start","run_id":"`+delegRun+`","seq":1,"call_id":"call_1","name":"research","args":{"prompt":"status of order 42"}}`),
		demoRecord(delegRun, "event", "tool_finish", 3, base.Add(3*time.Minute+4*time.Second), meta,
			`{"type":"tool_finish","run_id":"`+delegRun+`","seq":1,"call_id":"call_1","name":"research","content":"order 42 shipped this morning","is_error":false}`),
		demoRecord(delegRun, "messages", "", 1, base.Add(3*time.Minute+5*time.Second), meta,
			`[{"role":"assistant","content":[{"type":"text","text":"Order 42 shipped."}]}]`),
	)

	// The failing run is the fixture: its error is the demo, not a bug —
	// the invoke span's error status is what fails the row. Its stream
	// stops mid-step: no step_finish, no run_finish.
	fail := demoRun(failRun, "orders", base.Add(6*time.Minute), 2*time.Second, 2,
		"wefttest: injected provider 500", 8, 2)
	kept := make([]obsdb.Record, 0, len(fail.Records))
	for _, r := range fail.Records {
		if t, _ := r.Attrs["weft.event.type"].(string); t == "step_finish" || t == "run_finish" {
			continue
		}
		kept = append(kept, r)
	}
	fail.Records = append(kept,
		demoRecord(failRun, "event", "tool_start", 2, base.Add(6*time.Minute+500*time.Millisecond), meta,
			`{"type":"tool_start","run_id":"`+failRun+`","seq":1,"call_id":"call_2","name":"refund_order","args":{"order_id":"99"}}`),
		demoRecord(failRun, "event", "tool_finish", 3, base.Add(6*time.Minute+time.Second), meta,
			`{"type":"tool_finish","run_id":"`+failRun+`","seq":1,"call_id":"call_2","name":"refund_order","content":"order 99 does not exist","is_error":true}`),
	)

	for _, b := range []obsdb.Batch{ok, child, deleg, fail} {
		if err := db.Write(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

// demoRun builds one batch for a run: the durable events (run_start,
// step_start, step_finish, run_finish), the input messages record, and
// the invoke_agent span whose usage and step count the run row reads.
// status 1 finishes ok; status 2 fails the run with statusMsg.
func demoRun(run, agent string, start time.Time, took time.Duration, status int, statusMsg string, inTok, outTok int64) obsdb.Batch {
	meta := map[string]any{"cwd": "/tmp/demo"}
	usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d}`, inTok, outTok)
	events := []obsdb.Record{
		demoRecord(run, "event", "run_start", 0, start, meta,
			`{"type":"run_start","id":"`+run+`","model":{"provider":"wefttest","name":"script"},"agent":"`+agent+`"}`),
		demoRecord(run, "event", "step_start", 1, start.Add(300*time.Millisecond), meta,
			`{"type":"step_start","run_id":"`+run+`","index":0}`),
		demoRecord(run, "event", "step_finish", 4, start.Add(took-500*time.Millisecond), meta,
			`{"type":"step_finish","run_id":"`+run+`","index":0,"reason":"stop","usage":`+usage+`}`),
		demoRecord(run, "event", "run_finish", 5, start.Add(took), meta,
			`{"type":"run_finish","run_id":"`+run+`","usage":`+usage+`,"steps":1}`),
		demoRecord(run, "messages", "", 0, start.Add(100*time.Millisecond), meta,
			`[{"role":"user","content":[{"type":"text","text":"Where is order 42?"}]}]`),
	}
	spanAttrs := map[string]any{
		"gen_ai.operation.name":      "invoke_agent",
		"weft.run.id":                run,
		"gen_ai.agent.name":          agent,
		"gen_ai.provider.name":       "wefttest",
		"gen_ai.request.model":       "script",
		"gen_ai.usage.input_tokens":  inTok,
		"gen_ai.usage.output_tokens": outTok,
		"weft.run.steps":             int64(1),
		"weft.version":               demoVersion,
		"weft.manifest.hash":         "sha256:demo",
		"cwd":                        "/tmp/demo",
	}
	span := obsdb.Span{
		Name: "invoke_agent " + agent, Kind: 1,
		Start: start, End: start.Add(took), StatusCode: status, StatusMessage: statusMsg,
		Service: "studio-demo", Attrs: spanAttrs, Resource: map[string]any{"service.name": "studio-demo"},
	}
	return obsdb.Batch{Records: events, Spans: []obsdb.Span{span}}
}

// demoRecord builds one weft log record: kind event | messages, pos
// its durable position, body its wire JSON. The identity chain, the
// record contract attributes and caller metadata ride Attrs.
func demoRecord(run, kind, eventType string, pos int64, at time.Time, meta map[string]any, body string) obsdb.Record {
	a := map[string]any{
		"weft.run.id":        run,
		"weft.record":        kind,
		"weft.version":       demoVersion,
		"weft.manifest.hash": "sha256:demo",
		"gen_ai.agent.name":  "orders",
	}
	switch kind {
	case "event":
		a["weft.event.type"] = eventType
		a["weft.event.pos"] = pos
	case "messages":
		a["weft.messages.index"] = pos
	}
	for k, v := range meta {
		a[k] = v
	}
	return obsdb.Record{
		Time: at, Severity: 9, EventName: "weft." + kind, Body: body,
		Service: "studio-demo", Attrs: a, Resource: map[string]any{"service.name": "studio-demo"},
	}
}

// manifest is weft.Manifest over the same agents, feeding the agent
// and tool cards. Built by hand here so -serve (no run recorded)
// still serves the cards.
func manifest() []byte {
	researcher := weft.New(wefttest.Script(), weft.Name("researcher"))
	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
		}) (string, error) {
			return "", nil
		})
	orders := weft.New(wefttest.Script(), weft.Name("orders"), lookup,
		weft.Subagent("research", "Summarize an order's status.", researcher))
	b, err := weft.Manifest(orders, researcher)
	if err != nil {
		return nil
	}
	return b
}
