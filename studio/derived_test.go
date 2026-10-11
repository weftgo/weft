package studio

import (
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

// The stock_genai fixture's two derived runs (docs/record-contract.md
// §9): a succeeded one with two steps and a tool call, a failed one.
const (
	derivedOK   = "otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"
	derivedFail = "otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081"
)

// The badge's words every derived pane carries (obsdb.HoleNoteFor(
// HoleDerived, CauseStockSpans)) — pinned here so a change is seen.
const (
	derivedReason = "derived from spans · no events"
	derivedFix    = "the producer exports stock OTel GenAI spans only: emit weft's records (docs/record-contract.md) for events, the transcript and requests"
)

// derivedFixture is a setup-A server over a fresh file database that
// ingested the record contract's stock_genai fixture through Studio's
// own /v1/traces (the real OTLP path): a Python app's stock GenAI
// spans, no weft.* attribute anywhere.
func derivedFixture(t *testing.T) http.Handler {
	t.Helper()
	srv := New(Open(t.TempDir() + "/derived.db"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/stock_genai/traces.json")
	return h
}

// ingestFile posts an OTLP/JSON file to an ingest route of h.
func ingestFile(t *testing.T, h http.Handler, route, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, body := post(t, h, route, "application/json", string(b)); code != http.StatusOK {
		t.Fatalf("ingest %s: %d %s", path, code, body)
	}
}

// badged is an answer's envelope badge.
type badged struct {
	Badge  string `json:"badge"`
	Reason string `json:"reason"`
	Fix    string `json:"fix"`
}

func (b badged) derived() bool {
	return b.Badge == "derived" && b.Reason == derivedReason && b.Fix == derivedFix
}

// A stock app's spans get the runs list, the run header, the
// waterfall and the session view (plan I1): every route a derived run
// reaches answers what spans can say, and every pane spans cannot fill
// carries the derived badge with its reason and its fix.
func TestDerivedRunRoutes(t *testing.T) {
	h := derivedFixture(t)

	// The list: both runs, marked, requests_badge derived.
	code, _, body := get(t, h, "/studio/api/runs")
	if code != http.StatusOK {
		t.Fatalf("runs: %d %s", code, body)
	}
	var list struct {
		Total int `json:"total"`
		Runs  []struct {
			ID            string `json:"id"`
			Derived       bool   `json:"derived"`
			RequestsBadge string `json:"requests_badge"`
			SessionID     string `json:"session_id"`
			Status        string `json:"status"`
		} `json:"runs"`
	}
	decode(t, body, &list)
	if list.Total != 2 || len(list.Runs) != 2 {
		t.Fatalf("runs = %s, want the two derived runs", body)
	}
	for _, r := range list.Runs {
		if !r.Derived || r.RequestsBadge != "derived" || r.SessionID != "conv_7f3a" {
			t.Errorf("run row %+v, want derived, requests_badge derived, session conv_7f3a", r)
		}
	}

	// The run page: the row, its one hole, no children.
	code, _, body = get(t, h, "/studio/api/runs/"+derivedOK)
	if code != http.StatusOK {
		t.Fatalf("run: %d %s", code, body)
	}
	var doc struct {
		Derived bool    `json:"derived"`
		Steps   int     `json:"steps"`
		Status  string  `json:"status"`
		Holes   []badge `json:"holes"`
	}
	decode(t, body, &doc)
	if !doc.Derived || doc.Steps != 2 || doc.Status != "succeeded" ||
		len(doc.Holes) != 1 || doc.Holes[0] != (badge{"derived", derivedReason, derivedFix}) {
		t.Errorf("run page = %s, want derived, 2 steps, succeeded, the one derived hole", body)
	}
	golden(t, "run-derived.golden.json", body)

	// Events and transcript: empty, badged — never an empty pane.
	_, _, body = get(t, h, "/studio/api/runs/"+derivedOK+"/events")
	var ev struct {
		Events []any `json:"events"`
		Done   bool  `json:"done"`
		badged
	}
	decode(t, body, &ev)
	if len(ev.Events) != 0 || !ev.Done || !ev.derived() {
		t.Errorf("events = %s, want none, done, badged derived", body)
	}
	golden(t, "events-derived.golden.json", body)
	_, _, body = get(t, h, "/studio/api/runs/"+derivedOK+"/transcript")
	var tr struct {
		Batches []any `json:"batches"`
		badged
	}
	decode(t, body, &tr)
	if len(tr.Batches) != 0 || !tr.derived() {
		t.Errorf("transcript = %s, want no batch, badged derived", body)
	}
	golden(t, "transcript-derived.golden.json", body)
	code, _, body = get(t, h, "/studio/api/runs/"+derivedOK+"/transcript?step=1")
	var refused badged
	decode(t, body, &refused)
	if code != http.StatusConflict || !refused.derived() {
		t.Errorf("transcript?step=1 = %d %s, want 409 badged derived", code, body)
	}

	// The waterfall: the real spans, the reader's step and seq on them.
	_, _, body = get(t, h, "/studio/api/runs/"+derivedOK+"/spans")
	var sp struct {
		Spans []struct {
			Name  string         `json:"name"`
			Attrs map[string]any `json:"attrs"`
		} `json:"spans"`
	}
	decode(t, body, &sp)
	var got []string
	for _, s := range sp.Spans {
		got = append(got, s.Name)
	}
	if want := []string{"invoke_agent support", "chat gpt-5", "execute_tool lookup_order", "chat gpt-5"}; !slices.Equal(got, want) {
		t.Errorf("spans = %v, want %v", got, want)
	}
	if len(sp.Spans) == 4 && (sp.Spans[2].Attrs["weft.tool.seq"] != float64(1) || sp.Spans[3].Attrs["weft.step.index"] != float64(1)) {
		t.Errorf("spans' positions = %v / %v, want seq 1 and step 1", sp.Spans[2].Attrs, sp.Spans[3].Attrs)
	}

	// Requests and tools: empty, badged derived.
	for _, route := range []string{"requests", "tools"} {
		code, _, body = get(t, h, "/studio/api/runs/"+derivedOK+"/"+route)
		var b badged
		decode(t, body, &b)
		if code != http.StatusOK || !b.derived() {
			t.Errorf("%s = %d %s, want 200 badged derived in the stock_spans words", route, code, body)
		}
	}

	// Search: by error text, by model; sessions: the conversation.
	_, _, body = get(t, h, "/studio/api/search?q=cancelled")
	decode(t, body, &list)
	if list.Total != 1 || list.Runs[0].ID != derivedFail || !list.Runs[0].Derived {
		t.Errorf("search q=cancelled = %s, want the failed derived run", body)
	}
	_, _, body = get(t, h, "/studio/api/search?model=gpt-5&agent=support")
	decode(t, body, &list)
	if list.Total != 2 {
		t.Errorf("search model+agent = %s, want both", body)
	}
	_, _, body = get(t, h, "/studio/api/sessions")
	var sessions struct {
		Sessions []struct {
			ID    string `json:"id"`
			Turns int    `json:"turns"`
		} `json:"sessions"`
	}
	decode(t, body, &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != "conv_7f3a" || sessions.Sessions[0].Turns != 2 {
		t.Errorf("sessions = %s, want conv_7f3a with its two turns", body)
	}
	code, _, body = get(t, h, "/studio/api/sessions/conv_7f3a")
	if code != http.StatusOK || !strings.Contains(body, derivedOK) || !strings.Contains(body, derivedFail) {
		t.Errorf("session conv_7f3a = %d %s, want both runs", code, body)
	}
}

// The export carries what exists — the row and the spans — and badges
// every block spans cannot fill; the wefttest fixtures are refused 400:
// there is no transcript to replay.
func TestDerivedExport(t *testing.T) {
	h := derivedFixture(t)
	code, _, body := get(t, h, "/studio/api/runs/"+derivedOK+"/export?format=json")
	if code != http.StatusOK {
		t.Fatalf("export json: %d %s", code, body)
	}
	var x struct {
		Run struct {
			Derived bool    `json:"derived"`
			Holes   []badge `json:"holes"`
		} `json:"run"`
		Events     badged  `json:"events"`
		Transcript badged  `json:"transcript"`
		Requests   badged  `json:"requests"`
		Spans      []any   `json:"spans"`
		Holes      []badge `json:"holes"`
	}
	decode(t, body, &x)
	if !x.Run.Derived || !x.Events.derived() || !x.Transcript.derived() || !x.Requests.derived() || len(x.Spans) != 4 {
		t.Errorf("export = run %+v, events %+v, transcript %+v, requests %+v, %d spans; want every block badged derived and the four spans",
			x.Run, x.Events, x.Transcript, x.Requests, len(x.Spans))
	}
	if len(x.Holes) != 1 || x.Holes[0] != (badge{"derived", derivedReason, derivedFix}) {
		t.Errorf("export holes = %+v, want the one derived hole", x.Holes)
	}
	for _, f := range []string{"jsonl", "otlp"} {
		if code, _, body := get(t, h, "/studio/api/runs/"+derivedOK+"/export?format="+f); code != http.StatusOK {
			t.Errorf("export %s = %d %s", f, code, body)
		}
	}
	code, _, body = get(t, h, "/studio/api/runs/"+derivedOK+"/export?format=wefttest")
	var refused struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		badged
	}
	decode(t, body, &refused)
	if code != http.StatusBadRequest || refused.Error.Code != "bad_request" || !refused.derived() {
		t.Errorf("export wefttest = %d %s, want 400 badged derived", code, body)
	}
}

// A diff with a derived side compares nothing: every column of every
// step both sides hold is unknown, never a change; the step only one
// side has is still missing; each derived side's one hole is derived.
func TestDerivedDiff(t *testing.T) {
	h := derivedFixture(t)
	code, _, body := get(t, h, "/studio/api/diff?a="+derivedOK+"&b="+derivedFail)
	if code != http.StatusOK {
		t.Fatalf("diff: %d %s", code, body)
	}
	var d struct {
		Steps []struct {
			Step    int      `json:"step"`
			Changes []string `json:"changes"`
			Unknown []string `json:"unknown"`
			A       *struct {
				Holes []badge `json:"holes"`
			} `json:"a"`
		} `json:"steps"`
	}
	decode(t, body, &d)
	if len(d.Steps) != 2 {
		t.Fatalf("diff steps = %s, want 2", body)
	}
	all := []string{"system", "tool_calls", "tool_results", "text", "usage"}
	if s := d.Steps[0]; len(s.Changes) != 0 || !slices.Equal(s.Unknown, all) {
		t.Errorf("step 0 = changes %v unknown %v, want none and every column", s.Changes, s.Unknown)
	}
	if s := d.Steps[1]; !slices.Equal(s.Changes, []string{"missing"}) {
		t.Errorf("step 1 = changes %v, want missing (b has one step)", s.Changes)
	}
	if a := d.Steps[0].A; a == nil || len(a.Holes) != 1 || a.Holes[0] != (badge{"derived", derivedReason, derivedFix}) {
		t.Errorf("step 0's derived side holes = %+v, want the one derived hole", a)
	}
	// Against a weft run too: unknown, never changed.
	ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/plain/traces.json")
	ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/plain/logs.json")
	_, _, runs := get(t, h, "/studio/api/runs")
	var list struct {
		Runs []struct {
			ID      string `json:"id"`
			Derived bool   `json:"derived"`
		} `json:"runs"`
	}
	decode(t, runs, &list)
	weft := ""
	for _, r := range list.Runs {
		if !r.Derived {
			weft = r.ID
		}
	}
	if weft == "" {
		t.Fatalf("no weft run after ingesting plain: %s", runs)
	}
	_, _, body = get(t, h, "/studio/api/diff?a="+weft+"&b="+derivedFail)
	decode(t, body, &d)
	if len(d.Steps) == 0 || len(d.Steps[0].Changes) != 0 || !slices.Equal(d.Steps[0].Unknown, all) {
		t.Errorf("weft vs derived = %s, want step 0 all unknown, unchanged", body)
	}
}

// A weft run carries the GenAI mirrors (gen_ai.conversation.id among
// them, for a session's run) on its spans and is never derived a twin:
// ingesting a generated case through Studio leaves exactly one row.
func TestDerivedNoTwinForWeftRuns(t *testing.T) {
	for _, c := range []string{"tool_calls", "session_turns"} {
		srv := New(Open(t.TempDir() + "/twin.db"))
		t.Cleanup(func() { _ = srv.Close() })
		h := srv.Handler()
		ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/"+c+"/traces.json")
		ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/"+c+"/logs.json")
		_, _, body := get(t, h, "/studio/api/runs?all=1")
		var list struct {
			Total int `json:"total"`
		}
		decode(t, body, &list)
		var want struct {
			Runs []any `json:"runs"`
		}
		b, err := os.ReadFile("../obsdb/testdata/contract/" + c + "/expect.json")
		if err != nil {
			t.Fatal(err)
		}
		decode(t, string(b), &want)
		if list.Total != len(want.Runs) || strings.Contains(body, `"derived"`) || strings.Contains(body, `"otel-`) {
			t.Errorf("%s: %d runs (%s), want exactly the %d weft runs, none derived", c, list.Total, body, len(want.Runs))
		}
	}
}

// The playground refuses a replay from a derived run with its words:
// there is no transcript to replay (the run route's checkSource, the
// preview route, and the fixtures builder the playground's fixtures
// route shares). No command reaches the runtime.
func TestDerivedReplayRefused(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	b, err := os.ReadFile("../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	if code, out := previewPost(t, pt, "/v1/traces", "", string(b)); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, out)
	}
	run := func(engine string, from int, input string) string {
		in := ""
		if input != "" {
			in = `"input":"` + input + `",`
		}
		return `{"runtime":"rt_test","agent":"acme-support",` + in + `"source":{"run_id":"` + derivedOK + `","from_step":` +
			string(rune('0'+from)) + `},"engine":"` + engine + `","side_effects":"substitute","thread":"ephemeral"}`
	}
	for _, c := range []struct{ path, body string }{
		{"/api/playground/preview", `{"agent":"support","source":{"run_id":"` + derivedOK + `","from_step":0}}`},
		{"/api/playground/fixtures", `{"run_id":"` + derivedOK + `"}`},
		// The run route: live and scripted, from step 0 and 1, and the
		// input-only case that would otherwise spend a live model call.
		{"/api/playground/runs", run("live", 0, "")},
		{"/api/playground/runs", run("live", 1, "")},
		{"/api/playground/runs", run("live", 0, "where is my order?")},
		{"/api/playground/runs", run("scripted", 0, "")},
		{"/api/playground/runs", run("scripted", 1, "")},
	} {
		code, out := previewPost(t, pt, c.path, "", c.body)
		var refused badged
		decode(t, out, &refused)
		if code != http.StatusBadRequest || !refused.derived() || !strings.Contains(out, "derived from stock spans") ||
			!strings.Contains(out, `"code":"bad_request"`) {
			t.Errorf("%s %s = %d %s, want 400 bad_request badged derived, naming why", c.path, c.body, code, out)
		}
	}
	select {
	case cmd := <-pt.cmd:
		t.Errorf("a command reached the runtime: %+v", cmd)
	default:
	}
}

// The step route for a derived run (docs/record-contract.md §9): the
// step is its chat span — its usage, priced like any other, and its
// tool calls from the execute_tool spans — and its one hole is derived,
// in the stock_spans words: never a gap ("a destination dropped") or
// not_recorded ("upgrade weft"), which would send the author to the
// wrong fix.
func TestDerivedSteps(t *testing.T) {
	h := derivedFixture(t)
	for _, c := range []struct {
		n       string
		in, out int64
		calls   int
	}{{"0", 812, 41, 1}, {"1", 905, 37, 0}} {
		code, _, body := get(t, h, "/studio/api/runs/"+derivedOK+"/steps/"+c.n)
		if code != http.StatusOK {
			t.Fatalf("steps/%s: %d %s", c.n, code, body)
		}
		var st struct {
			Status string `json:"status"`
			Usage  struct {
				In  int64 `json:"input_tokens"`
				Out int64 `json:"output_tokens"`
			} `json:"usage"`
			CostUSD       *float64 `json:"cost_usd"`
			CostKnown     bool     `json:"cost_known"`
			Request       badged   `json:"request"`
			MessagesIn    badged   `json:"messages_in"`
			AttemptsBadge *badged  `json:"attempts_badge"`
			ToolCalls     []struct {
				CallID string `json:"call_id"`
				Name   string `json:"name"`
				Seq    int64  `json:"seq"`
				Badge  string `json:"badge"`
			} `json:"tool_calls"`
			Holes []badge `json:"holes"`
		}
		decode(t, body, &st)
		if st.Status != "ok" || st.Usage.In != c.in || st.Usage.Out != c.out {
			t.Errorf("steps/%s = status %s usage %+v, want ok and the chat span's %d/%d", c.n, st.Status, st.Usage, c.in, c.out)
		}
		if st.CostUSD == nil || !st.CostKnown || *st.CostUSD <= 0 {
			t.Errorf("steps/%s cost = %v known %v, want the chat span's usage priced (never a known $0)", c.n, st.CostUSD, st.CostKnown)
		}
		if len(st.Holes) != 1 || st.Holes[0] != (badge{"derived", derivedReason, derivedFix}) {
			t.Errorf("steps/%s holes = %+v, want exactly the derived hole", c.n, st.Holes)
		}
		if !st.Request.derived() || !st.MessagesIn.derived() || st.AttemptsBadge != nil {
			t.Errorf("steps/%s request %+v messages_in %+v attempts_badge %+v, want derived, derived, none", c.n, st.Request, st.MessagesIn, st.AttemptsBadge)
		}
		if len(st.ToolCalls) != c.calls || (c.calls == 1 && (st.ToolCalls[0].CallID != "call_abc" || st.ToolCalls[0].Name != "lookup_order" ||
			st.ToolCalls[0].Seq != 1 || st.ToolCalls[0].Badge != "derived")) {
			t.Errorf("steps/%s tool calls = %+v, want %d (call_abc, lookup_order, seq 1, derived)", c.n, st.ToolCalls, c.calls)
		}
	}
	// The failed run's one step: its chat span ended ok, the run failed —
	// the last step reads error, as a weft run's would.
	_, _, body := get(t, h, "/studio/api/runs/"+derivedFail+"/steps/0")
	if !strings.Contains(body, `"status":"error"`) {
		t.Errorf("failed run's steps/0 = %s, want status error", body)
	}
}

// badge is a hole as the run page and the export list it.
type badge struct {
	Hole   string `json:"hole"`
	Reason string `json:"reason"`
	Fix    string `json:"fix"`
}
