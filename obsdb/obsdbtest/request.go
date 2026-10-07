package obsdbtest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The request record fixture (ADR 0028), synthetic records in the
// shape core emits them (core/request.go; the bodies in its struct
// field order, request 0's and the prompt's and tools' values those of
// core/testdata/records/*.golden.json).
const (
	fxInstructions = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	fxSystem1      = "068dac11e8f131c8f87e8c902dda33d8bcd5ce2bdba165e5ef58fce6a8137623"
	fxCatalog1     = "3adb2763c108ccb41856390296a2a74e41f44f9ccd7df7ea21e24e3281ee6d59"
	fxSystem2      = "2222222222222222222222222222222222222222222222222222222222222222"
	fxCatalog2     = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	fxPrompt1      = `{"hash":"` + fxSystem1 + `","text":"You are a support agent.\n\nUse lookup for orders."}`
	fxPrompt2      = `{"hash":"` + fxSystem2 + `","text":"Trimmed for step 2."}`
	fxTools1       = `{"hash":"` + fxCatalog1 + `","tools":[{"name":"lookup","description":"Echo lookup.","schema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]},"timeout_ms":1000,"approval":false,"replay":"never","max_result_bytes":65536,"sequential":false,"source":"local"}]}`
	fxTools2       = `{"hash":"` + fxCatalog2 + `","tools":[{"name":"refund","description":"Refund an order.","schema":{"type":"object"},"timeout_ms":0,"approval":true,"replay":"never","max_result_bytes":65536,"sequential":true,"source":"mcp"}]}`
	// fxRequest0 is the golden request body, in core's field order.
	fxRequest0 = `{"step":0,"attempt":1,"system_hash":"` + fxSystem1 + `","messages_ref":{"index":0,"count":1},"tools":{"catalog_hash":"` + fxCatalog1 + `","names":["lookup"]},"tool_choice":{"mode":"tool","name":"lookup"},"thinking":{"level":"high","budget":1024},"sequential_tools":true,"params":{"temperature":0.5,"max_tokens":256,"stop":["STOP"]},"model":{"provider":"wefttest","name":"script"},"stream":true}`
)

// fxRequest is a request body in core's field order; msgIndex < 0
// omits messages_ref.index (a content-off chain's shape).
func fxRequest(step, attempt int, system, catalog, tool, model string, msgIndex, count int) string {
	ref := fmt.Sprintf(`{"count":%d}`, count)
	if msgIndex >= 0 {
		ref = fmt.Sprintf(`{"index":%d,"count":%d}`, msgIndex, count)
	}
	return fmt.Sprintf(`{"step":%d,"attempt":%d,"system_hash":%q,"messages_ref":%s,"tools":{"catalog_hash":%q,"names":[%q]},"sequential_tools":false,"params":{},"model":{"provider":"wefttest","name":%q},"stream":true}`,
		step, attempt, system, ref, catalog, tool, model)
}

// fxRun builds one run's records in emission order, each its own time.
type fxRun struct {
	id   string
	n    int
	recs []obsdb.Record
}

func (f *fxRun) add(kind, eventName, body string, attrs map[string]any) {
	f.n++
	a := map[string]any{"weft.record": kind, "weft.run.id": f.id, "gen_ai.agent.name": "support"}
	for k, v := range attrs {
		a[k] = v
	}
	f.recs = append(f.recs, obsdb.Record{
		Time: at(time.Duration(f.n) * time.Millisecond), EventName: eventName,
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708",
		Severity: 9, Body: body, Service: "conf-svc", Attrs: a,
		Resource: map[string]any{"service.name": "conf-svc"},
	})
}

func (f *fxRun) event(pos int64, typ, body string, attrs map[string]any) {
	a := map[string]any{"weft.event.type": typ, "weft.event.pos": pos}
	for k, v := range attrs {
		a[k] = v
	}
	f.add("event", "weft.event", body, a)
}

func (f *fxRun) start(instructions bool) {
	var a map[string]any
	if instructions {
		a = map[string]any{"weft.instructions.hash": fxInstructions}
	}
	f.event(0, "run_start", `{"type":"run_start","id":"`+f.id+`","model":{"provider":"wefttest","name":"script"},"agent":"support"}`, a)
}

func (f *fxRun) finish(pos int64, steps int) {
	f.event(pos, "run_finish", fmt.Sprintf(`{"type":"run_finish","run_id":%q,"usage":{"input_tokens":10,"output_tokens":2},"steps":%d}`, f.id, steps), nil)
}

func (f *fxRun) prompt(index int64, system, body string, extra map[string]any) {
	a := map[string]any{"weft.content": "full", "weft.prompt.index": index, "weft.system.hash": system}
	for k, v := range extra {
		a[k] = v
	}
	f.add("prompt", "weft.prompt", body, a)
}

func (f *fxRun) tools(index int64, catalog, body string) {
	f.add("tools", "weft.tools", body, map[string]any{"weft.content": "full", "weft.tools.index": index, "weft.catalog.hash": catalog})
}

func (f *fxRun) request(index int64, step, attempt int, system, catalog, content, body string) {
	a := map[string]any{
		"weft.content": content, "weft.request.index": index,
		"weft.step.index": int64(step), "weft.attempt.index": int64(attempt),
	}
	if system != "" {
		a["weft.system.hash"] = system
	}
	if catalog != "" {
		a["weft.catalog.hash"] = catalog
	}
	f.add("request", "weft.request", body, a)
}

// requestFixture is the three-step run: a PrepareStep rewrite at step 2
// (a second prompt, capped by its destination, and a second catalog)
// and a retried model call at step 1 (two request records, attempts 1
// and 2, the second naming its fallback model).
func requestFixture(id string) []obsdb.Record {
	f := &fxRun{id: id}
	f.start(true)
	f.add("messages", "weft.messages", `[{"role":"user","content":[{"type":"text","text":"where is 42"}]}]`,
		map[string]any{"weft.messages.index": int64(0), "weft.step.index": int64(0), "weft.messages.input": true, "weft.content": "full"})
	f.prompt(0, fxSystem1, fxPrompt1, nil)
	f.tools(0, fxCatalog1, fxTools1)
	f.request(0, 0, 1, fxSystem1, fxCatalog1, "full", fxRequest0)
	f.request(1, 1, 1, fxSystem1, fxCatalog1, "full", fxRequest(1, 1, fxSystem1, fxCatalog1, "lookup", "script", 2, 3))
	f.request(2, 1, 2, fxSystem1, fxCatalog1, "full", fxRequest(1, 2, fxSystem1, fxCatalog1, "lookup", "backup", 2, 3))
	f.prompt(1, fxSystem2, fxPrompt2, map[string]any{"weft.content.truncated_bytes": int64(12)})
	f.tools(1, fxCatalog2, fxTools2)
	f.request(3, 2, 1, fxSystem2, fxCatalog2, "full", fxRequest(2, 1, fxSystem2, fxCatalog2, "refund", "script", 4, 5))
	f.finish(1, 3)
	return f.recs
}

// strippedFixture is what a content-off chain receives of a two-step
// run: the request records stripped (no params.stop, no
// messages_ref.index), no prompt, tools or messages records.
func strippedFixture(id string) []obsdb.Record {
	f := &fxRun{id: id}
	f.start(true)
	f.request(0, 0, 1, fxSystem1, fxCatalog1, "stripped", fxRequest(0, 1, fxSystem1, fxCatalog1, "lookup", "script", -1, 1))
	f.request(1, 1, 1, fxSystem1, fxCatalog1, "stripped", fxRequest(1, 1, fxSystem1, fxCatalog1, "lookup", "script", -1, 3))
	f.finish(1, 2)
	return f.recs
}

// requestRecords: ADR 0028's three kinds read back alike on every
// backend — the run row's three columns, Requests' paging and step
// filter, Prompt, Tools and Catalogs, and the holes: stripped (a
// content-off chain), gap (a dropped prompt), not_recorded (a run
// written before the contract) and the no-model-call reading.
func requestRecords(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		gap := &fxRun{id: "rq_gap"}
		gap.start(true)
		gap.request(0, 0, 1, fxSystem1, "", "full", fxRequest(0, 1, fxSystem1, "", "lookup", "script", 0, 1))
		gap.finish(1, 1)
		old := &fxRun{id: "rq_old"}
		old.start(false)
		old.finish(1, 1)
		nocall := &fxRun{id: "rq_nocall"}
		nocall.start(true)
		nocall.finish(1, 0)
		var recs []obsdb.Record
		recs = append(recs, requestFixture("rq")...)
		recs = append(recs, strippedFixture("rq_off")...)
		recs = append(append(append(recs, gap.recs...), old.recs...), nocall.recs...)
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		// A retried transport: nothing moves, the high-water mark included.
		if err := db.Write(ctx(), obsdb.Batch{Records: requestFixture("rq")}); err != nil {
			t.Fatal(err)
		}

		// The run rows and ADR 0028 §10's reading table.
		for _, c := range []struct {
			id, instructions, catalog string
			count                     int64
			hole                      obsdb.Hole
		}{
			{"rq", fxInstructions, fxCatalog1, 4, ""},
			{"rq_off", fxInstructions, fxCatalog1, 2, ""},
			{"rq_gap", fxInstructions, "", 1, ""},
			{"rq_old", "", "", 0, obsdb.HoleNotRecorded},
			{"rq_nocall", fxInstructions, "", 0, ""},
		} {
			run, err := db.Run(ctx(), c.id)
			if err != nil {
				t.Fatal(err)
			}
			if run.InstructionsHash != c.instructions || run.CatalogHash != c.catalog ||
				run.RequestCount != c.count || run.RequestsHole() != c.hole {
				t.Errorf("run %s = instructions %q catalog %q requests %d hole %q; want %q %q %d %q", c.id,
					run.InstructionsHash, run.CatalogHash, run.RequestCount, run.RequestsHole(),
					c.instructions, c.catalog, c.count, c.hole)
			}
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Runs {
			if r.ID == "rq" && (r.InstructionsHash != fxInstructions || r.CatalogHash != fxCatalog1 || r.RequestCount != 4) {
				t.Errorf("Runs' row for rq = %q %q %d; want Run's", r.InstructionsHash, r.CatalogHash, r.RequestCount)
			}
		}

		// Requests: every attempt, in index order, parsed.
		all, err := db.Requests(ctx(), "rq", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1})
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			index         int64
			step          int
			attempt       int64
			system, model string
		}
		want := []row{{0, 0, 1, fxSystem1, "script"}, {1, 1, 1, fxSystem1, "script"}, {2, 1, 2, fxSystem1, "backup"}, {3, 2, 1, fxSystem2, "script"}}
		if len(all) != len(want) {
			t.Fatalf("Requests = %d rows, want %d", len(all), len(want))
		}
		for i, w := range want {
			r := all[i]
			if r.Index != w.index || r.Step != w.step || r.Attempt != w.attempt || r.SystemHash != w.system ||
				r.Body.Model.Name != w.model || r.Content != "" || r.TruncatedBytes != 0 {
				t.Errorf("request %d = %+v; want %+v, content as emitted", i, r, w)
			}
		}
		if all[3].CatalogHash != fxCatalog2 || all[0].CatalogHash != fxCatalog1 {
			t.Errorf("catalog hashes = %q, %q", all[0].CatalogHash, all[3].CatalogHash)
		}
		r0 := all[0]
		if string(r0.Raw) != fxRequest0 {
			t.Errorf("request 0 raw = %s, want the body verbatim", r0.Raw)
		}
		b := r0.Body
		if b.MessagesRef.Index == nil || *b.MessagesRef.Index != 0 || b.MessagesRef.Count != 1 ||
			b.ToolChoice == nil || *b.ToolChoice != (obsdb.RequestToolChoice{Mode: "tool", Name: "lookup"}) ||
			b.Thinking == nil || *b.Thinking != (obsdb.RequestThinking{Level: "high", Budget: 1024}) ||
			!b.SequentialTools || !b.Stream || b.Model != (obsdb.RequestModel{Provider: "wefttest", Name: "script"}) ||
			b.Params.Temperature == nil || *b.Params.Temperature != 0.5 || b.Params.MaxTokens == nil || *b.Params.MaxTokens != 256 ||
			strings.Join(b.Params.Stop, ",") != "STOP" || strings.Join(b.Tools.Names, ",") != "lookup" {
			t.Errorf("request 0 body = %+v", b)
		}
		// Paging and the step filter.
		first, err := db.Requests(ctx(), "rq", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1, Limit: 3})
		if err != nil || len(first) != 3 || first[2].Index != 2 {
			t.Fatalf("first page = %d rows, %v; want indexes 0..2", len(first), err)
		}
		rest, err := db.Requests(ctx(), "rq", obsdb.RequestQuery{Step: obsdb.AllSteps, After: first[2].Index, Limit: 3})
		if err != nil || len(rest) != 1 || rest[0].Index != 3 {
			t.Fatalf("second page = %+v, %v; want index 3 alone", rest, err)
		}
		for step, wantIdx := range map[int][]int64{0: {0}, 1: {1, 2}, 2: {3}, 9: nil} {
			got, err := db.Requests(ctx(), "rq", obsdb.RequestQuery{Step: step, After: -1})
			if err != nil {
				t.Fatal(err)
			}
			var idx []int64
			for _, r := range got {
				idx = append(idx, r.Index)
			}
			if fmt.Sprint(idx) != fmt.Sprint(wantIdx) {
				t.Errorf("step %d requests = %v, want %v", step, idx, wantIdx)
			}
		}

		// Prompt, Tools, Catalogs.
		p1, err := db.Prompt(ctx(), "rq", fxSystem1)
		if err != nil || p1.Index != 0 || p1.Hash != fxSystem1 || p1.Text != "You are a support agent.\n\nUse lookup for orders." || p1.Content != "" {
			t.Errorf("Prompt(system 1) = %+v, %v", p1, err)
		}
		p2, err := db.Prompt(ctx(), "rq", fxSystem2)
		if err != nil || p2.Index != 1 || p2.Text != "Trimmed for step 2." || p2.Content != obsdb.HoleTruncated || p2.TruncatedBytes != 12 {
			t.Errorf("Prompt(system 2) = %+v, %v; want the capped prompt, truncated 12", p2, err)
		}
		t1, err := db.Tools(ctx(), "rq", fxCatalog1)
		if err != nil || len(t1.Tools) != 1 {
			t.Fatalf("Tools(catalog 1) = %+v, %v", t1, err)
		}
		if e := t1.Tools[0]; e.Name != "lookup" || e.Description != "Echo lookup." || e.TimeoutMS != 1000 ||
			e.Replay != "never" || e.MaxResultBytes != 65536 || e.Source != "local" ||
			string(e.Schema) != `{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}` {
			t.Errorf("Tools(catalog 1) entry = %+v", e)
		}
		cats, err := db.Catalogs(ctx(), "rq")
		if err != nil || len(cats) != 2 || cats[0].Hash != fxCatalog1 || cats[1].Hash != fxCatalog2 ||
			cats[1].Tools[0].Source != "mcp" || !cats[1].Tools[0].Approval {
			t.Errorf("Catalogs = %+v, %v; want catalog 1 then catalog 2", cats, err)
		}
		var hole *obsdb.HoleError
		if _, err := db.Prompt(ctx(), "rq", "nope"); !errors.Is(err, obsdb.ErrNotFound) || errors.As(err, &hole) {
			t.Errorf("Prompt(unnamed hash) = %v; want a plain ErrNotFound", err)
		}

		// The holes.
		offReqs, err := db.Requests(ctx(), "rq_off", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1})
		if err != nil || len(offReqs) != 2 {
			t.Fatalf("content-off Requests = %d, %v; want 2", len(offReqs), err)
		}
		for _, r := range offReqs {
			if r.Content != obsdb.HoleStripped || r.SystemHash != fxSystem1 || r.CatalogHash != fxCatalog1 || r.Body.MessagesRef.Index != nil {
				t.Errorf("content-off request %d = %+v; want stripped with its hashes", r.Index, r)
			}
		}
		if cats, err := db.Catalogs(ctx(), "rq_off"); err != nil || len(cats) != 0 {
			t.Errorf("content-off Catalogs = %+v, %v; want none", cats, err)
		}
		for _, c := range []struct {
			run, kind, hash string
			hole            obsdb.Hole
		}{
			{"rq_off", "prompt", fxSystem1, obsdb.HoleStripped},
			{"rq_off", "tools", fxCatalog1, obsdb.HoleStripped},
			{"rq_gap", "prompt", fxSystem1, obsdb.HoleGap},
			{"rq_old", "prompt", fxSystem1, obsdb.HoleNotRecorded},
			{"rq_old", "tools", fxCatalog1, obsdb.HoleNotRecorded},
		} {
			var err error
			if c.kind == "prompt" {
				_, err = db.Prompt(ctx(), c.run, c.hash)
			} else {
				_, err = db.Tools(ctx(), c.run, c.hash)
			}
			hole = nil
			if !errors.Is(err, obsdb.ErrNotFound) || !errors.As(err, &hole) || hole.Hole != c.hole || hole.Kind != c.kind || hole.Hash != c.hash {
				t.Errorf("%s %s of %s = %v; want ErrNotFound with hole %s", c.kind, c.hash[:8], c.run, err, c.hole)
			}
		}
		if reqs, err := db.Requests(ctx(), "rq_old", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1}); err != nil || len(reqs) != 0 {
			t.Errorf("pre-0028 run's Requests = %+v, %v; want none", reqs, err)
		}
		for name, read := range map[string]func() error{
			"Requests": func() error {
				_, err := db.Requests(ctx(), "nope", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1})
				return err
			},
			"Prompt":   func() error { _, err := db.Prompt(ctx(), "nope", fxSystem1); return err },
			"Tools":    func() error { _, err := db.Tools(ctx(), "nope", fxCatalog1); return err },
			"Catalogs": func() error { _, err := db.Catalogs(ctx(), "nope"); return err },
		} {
			if err := read(); !errors.Is(err, obsdb.ErrNotFound) || errors.As(err, &hole) {
				t.Errorf("%s of an unknown run = %v; want a plain ErrNotFound", name, err)
			}
		}
	}
}

// requestsManySteps: ADR 0028's dedupe on every backend — a 50-step run
// whose prompt and catalog never change holds one prompt, one tools
// and 50 request records, readable with the right hashes, the run row
// counting 50.
func requestsManySteps(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const steps = 50
		f := &fxRun{id: "rq50"}
		f.start(true)
		f.prompt(0, fxSystem1, fxPrompt1, nil)
		f.tools(0, fxCatalog1, fxTools1)
		for i := 0; i < steps; i++ {
			f.request(int64(i), i, 1, fxSystem1, fxCatalog1, "full", fxRequest(i, 1, fxSystem1, fxCatalog1, "lookup", "script", 2*i, 2*i+1))
		}
		f.finish(1, steps)
		if err := db.Write(ctx(), obsdb.Batch{Records: f.recs}); err != nil {
			t.Fatal(err)
		}
		run, err := db.Run(ctx(), "rq50")
		if err != nil || run.RequestCount != steps || run.CatalogHash != fxCatalog1 || run.InstructionsHash != fxInstructions {
			t.Fatalf("run = %+v, %v; want %d requests", run, err, steps)
		}
		reqs, err := db.Requests(ctx(), "rq50", obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1})
		if err != nil || len(reqs) != steps {
			t.Fatalf("Requests = %d, %v; want %d", len(reqs), err, steps)
		}
		for i, r := range reqs {
			if r.Index != int64(i) || r.Step != i || r.SystemHash != fxSystem1 || r.CatalogHash != fxCatalog1 {
				t.Errorf("request %d = %+v", i, r)
			}
		}
		if p, err := db.Prompt(ctx(), "rq50", fxSystem1); err != nil || p.Index != 0 {
			t.Errorf("Prompt = %+v, %v", p, err)
		}
		if cats, err := db.Catalogs(ctx(), "rq50"); err != nil || len(cats) != 1 {
			t.Errorf("Catalogs = %d, %v; want exactly one", len(cats), err)
		}
	}
}
