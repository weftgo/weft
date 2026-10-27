package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
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
	derivedFix    = "stock OTel GenAI spans carry no weft events or request records: set OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true for the messages and tool arguments (Studio reads them from the span attributes) and gen_ai.conversation.id on the invoke_agent span for a session (docs/record-contract.md §9); weft's own records need a weft SDK (Go today)"
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
				CallID string          `json:"call_id"`
				Name   string          `json:"name"`
				Seq    int64           `json:"seq"`
				Badge  string          `json:"badge"`
				Args   json.RawMessage `json:"args"`
				Result *struct {
					Content string `json:"content"`
					IsError bool   `json:"is_error"`
				} `json:"result"`
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
		// I6 (UX-H-01): the producer captured content — the call's
		// arguments and result are its span's gen_ai.tool.call.*.
		if c.calls == 1 {
			if tc := st.ToolCalls[0]; string(tc.Args) != `{"id":"4411"}` || tc.Result == nil ||
				tc.Result.Content != `{"status":"shipped"}` || tc.Result.IsError {
				t.Errorf("steps/%s call content = %s %+v, want the span's arguments and result", c.n, tc.Args, tc.Result)
			}
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

// stockVariant is the stock_genai export with every span attribute for
// which drop answers true removed (or, under empty, set to ""): the
// producers §9 still reads — no gen_ai.operation.name (the span name
// decides), no gen_ai.tool.call.id (only recommended), no usage.
func stockVariant(t *testing.T, drop func(key string) bool, empty bool) string {
	return stockVariantWith(t, drop, func(kv map[string]any) map[string]any {
		if empty {
			return map[string]any{"key": kv["key"], "value": map[string]any{"stringValue": ""}}
		}
		return nil
	})
}

// stockVariantWith is stockVariant with the replacement of each
// matched attribute spelled out: nil drops it.
func stockVariantWith(t *testing.T, match func(key string) bool, replace func(kv map[string]any) map[string]any) string {
	t.Helper()
	b, err := os.ReadFile("../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	decode(t, string(b), &doc)
	for _, rs := range doc["resourceSpans"].([]any) {
		for _, ss := range rs.(map[string]any)["scopeSpans"].([]any) {
			for _, sp := range ss.(map[string]any)["spans"].([]any) {
				span := sp.(map[string]any)
				var kept []any
				for _, a := range span["attributes"].([]any) {
					kv := a.(map[string]any)
					if !match(kv["key"].(string)) {
						kept = append(kept, kv)
					} else if r := replace(kv); r != nil {
						kept = append(kept, r)
					}
				}
				span["attributes"] = kept
			}
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// variantFixture ingests a stock_genai variant into a fresh server.
func variantFixture(t *testing.T, body string) http.Handler {
	t.Helper()
	srv := New(Open(t.TempDir() + "/variant.db"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	if code, _, out := post(t, h, "/studio/v1/traces", "application/json", body); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, out)
	}
	return h
}

// §9's name fallback and its optional call id reach the step route: a
// producer that sets no gen_ai.operation.name (or sets it empty) and no
// gen_ai.tool.call.id still gets each step's usage, times and its call
// (call_id "", keyed by its span, badged derived).
func TestDerivedStepsNameOnly(t *testing.T) {
	for name, body := range map[string]string{
		"no operation attribute": stockVariant(t, func(k string) bool { return k == "gen_ai.operation.name" || k == "gen_ai.tool.call.id" }, false),
		"empty operation":        stockVariant(t, func(k string) bool { return k == "gen_ai.operation.name" || k == "gen_ai.tool.call.id" }, true),
	} {
		h := variantFixture(t, body)
		_, _, row := get(t, h, "/studio/api/runs/"+derivedOK)
		if !strings.Contains(row, `"steps":2`) || !strings.Contains(row, `"input_tokens":1717`) {
			t.Errorf("%s: run row = %s, want 2 steps, 1717 input", name, row)
		}
		for _, c := range []struct {
			n       string
			in      int64
			calls   int
			started bool
		}{{"0", 812, 1, true}, {"1", 905, 0, true}} {
			_, _, body := get(t, h, "/studio/api/runs/"+derivedOK+"/steps/"+c.n)
			var st struct {
				Started  *string `json:"started"`
				Finished *string `json:"finished"`
				Usage    struct {
					In int64 `json:"input_tokens"`
				} `json:"usage"`
				CostKnown bool `json:"cost_known"`
				ToolCalls []struct {
					CallID string `json:"call_id"`
					Name   string `json:"name"`
					Seq    int64  `json:"seq"`
					Badge  string `json:"badge"`
					Span   *struct {
						ID string `json:"id"`
					} `json:"span"`
				} `json:"tool_calls"`
			}
			decode(t, body, &st)
			if st.Usage.In != c.in || !st.CostKnown || st.Started == nil || st.Finished == nil {
				t.Errorf("%s: steps/%s = usage %d known %v times %v/%v, want %d priced, timed", name, c.n, st.Usage.In, st.CostKnown, st.Started, st.Finished, c.in)
			}
			if len(st.ToolCalls) != c.calls {
				t.Errorf("%s: steps/%s calls = %+v, want %d", name, c.n, st.ToolCalls, c.calls)
				continue
			}
			if c.calls == 1 {
				tc := st.ToolCalls[0]
				if tc.CallID != "" || tc.Name != "lookup_order" || tc.Seq != 1 || tc.Badge != "derived" || tc.Span == nil || tc.Span.ID != "a1b2c3d4e5f60002" {
					t.Errorf("%s: steps/0 call = %+v, want call_id \"\", lookup_order, seq 1, derived, its span", name, tc)
				}
			}
		}
	}
}

// A stock producer that reports no usage anywhere, or only zeros: the
// run row's and every step's cost is unknown, with the reason — never a
// known $0.
func TestDerivedNoUsageCostUnknown(t *testing.T) {
	usage := func(k string) bool { return strings.HasPrefix(k, "gen_ai.usage.") }
	zero := func(kv map[string]any) map[string]any {
		return map[string]any{"key": kv["key"], "value": map[string]any{"intValue": "0"}}
	}
	for name, body := range map[string]string{
		"absent": stockVariant(t, usage, false),
		"zero":   stockVariantWith(t, usage, zero),
	} {
		t.Run(name, func(t *testing.T) { noUsageCostUnknown(t, variantFixture(t, body)) })
	}
}

func noUsageCostUnknown(t *testing.T, h http.Handler) {
	t.Helper()
	type costed struct {
		CostUSD    *float64 `json:"cost_usd"`
		CostKnown  bool     `json:"cost_known"`
		CostBadge  string   `json:"cost_badge"`
		CostReason string   `json:"cost_reason"`
	}
	unknown := func(c costed) bool {
		return c.CostUSD == nil && !c.CostKnown && c.CostBadge == "unknown" && strings.Contains(c.CostReason, "no (or zero) usage")
	}
	for _, p := range []string{"/runs/" + derivedOK, "/runs/" + derivedFail, "/runs/" + derivedOK + "/steps/0", "/runs/" + derivedOK + "/steps/1"} {
		_, _, body := get(t, h, "/studio/api"+p)
		var c costed
		decode(t, body, &c)
		if !unknown(c) {
			t.Errorf("%s cost = %+v, want unknown with the no-usage reason", p, c)
		}
	}
}

// The preview and the run route answer one body with a derived source
// alike — the derived refusal, whatever else the body gets wrong (an
// unknown runtime, a fork, malformed command and experiment ids) — and a command refusal's body is still the
// bare error shape, no badge.
func TestDerivedRefusalParity(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	b, err := os.ReadFile("../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	if code, out := previewPost(t, pt, "/v1/traces", "", string(b)); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, out)
	}
	body := `{"runtime":"rt_nope","agent":"nobody","thread":"fork","command_id":"bad id!","experiment_id":"bad id!",` +
		`"source":{"run_id":"` + derivedOK + `","from_step":0}}`
	var answers []string
	for _, path := range []string{"/api/playground/preview", "/api/playground/runs"} {
		code, out := previewPost(t, pt, path, "", body)
		var refused badged
		decode(t, out, &refused)
		if code != http.StatusBadRequest || !refused.derived() {
			t.Errorf("%s = %d %s, want the derived refusal", path, code, out)
		}
		answers = append(answers, out)
	}
	if len(answers) == 2 && answers[0] != answers[1] {
		t.Errorf("the two routes answer one body differently:\n%s\n%s", answers[0], answers[1])
	}
	code, out := previewPost(t, pt, "/api/playground/runs", "",
		`{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_unknown","from_step":-1},"engine":"live","side_effects":"substitute","thread":"ephemeral"}`)
	var shape map[string]any
	decode(t, out, &shape)
	errObj, _ := shape["error"].(map[string]any)
	if code != http.StatusBadRequest || len(shape) != 1 || len(errObj) != 2 || errObj["code"] != "bad_request" || errObj["message"] == nil {
		t.Errorf("a command refusal = %d %s, want the bare {error: {code, message}} shape", code, out)
	}
}

// A producer that captured no content (no gen_ai.tool.call.* on the
// tool span): the derived call's arguments and result stay unknown —
// args null, no result — never an empty value (§9, I6).
func TestDerivedStepsNoContent(t *testing.T) {
	h := variantFixture(t, stockVariant(t, func(k string) bool {
		return strings.HasPrefix(k, "gen_ai.tool.call.a") || strings.HasPrefix(k, "gen_ai.tool.call.r")
	}, false))
	_, _, body := get(t, h, "/studio/api/runs/"+derivedOK+"/steps/0")
	var st struct {
		ToolCalls []struct {
			Args   json.RawMessage  `json:"args"`
			Result *json.RawMessage `json:"result"`
			Badge  string           `json:"badge"`
		} `json:"tool_calls"`
	}
	decode(t, body, &st)
	if len(st.ToolCalls) != 1 || string(st.ToolCalls[0].Args) != "null" || st.ToolCalls[0].Result != nil || st.ToolCalls[0].Badge != "derived" {
		t.Errorf("steps/0 calls = %s, want one derived call with args null and no result", body)
	}
}

// The trace routes name the runs a trace makes, derived ones included
// (UX-H-03), and a trace that makes none says why (UX-H-02): the
// stock_genai_noconv fixture's TypeScript agent with no conversation
// id is a run with no session; its Flask app's lone chat span is not a
// run.
func TestDerivedTraceRuns(t *testing.T) {
	h := derivedFixture(t)
	ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/stock_genai_noconv/traces.json")
	const (
		agentTrace = "7c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f"
		chatTrace  = "8d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60"
		noconv     = "otel-" + agentTrace + "-7c00000000000002"
	)
	type traceDoc struct {
		RunIDs []string `json:"run_ids"`
		NotRun *struct {
			Reason string `json:"reason"`
			Fix    string `json:"fix"`
		} `json:"not_run"`
	}
	for _, c := range []struct {
		trace  string
		runs   []string
		notRun string
	}{
		{"4bf92f3577b34da6a3ce929d0e0e4736", []string{derivedOK}, ""},
		{agentTrace, []string{noconv}, ""},
		{chatTrace, []string{}, "model or tool calls only: no invoke_agent span opens a run"},
	} {
		code, _, body := get(t, h, "/studio/api/traces/"+c.trace)
		if code != http.StatusOK {
			t.Fatalf("traces/%s: %d %s", c.trace, code, body)
		}
		var d traceDoc
		decode(t, body, &d)
		if !slices.Equal(d.RunIDs, c.runs) {
			t.Errorf("traces/%s run_ids = %v, want %v", c.trace, d.RunIDs, c.runs)
		}
		switch {
		case c.notRun == "" && d.NotRun != nil:
			t.Errorf("traces/%s not_run = %+v, want none: it makes a run", c.trace, d.NotRun)
		case c.notRun != "" && (d.NotRun == nil || !strings.HasPrefix(d.NotRun.Reason, c.notRun) || !strings.Contains(d.NotRun.Fix, "invoke_agent")):
			t.Errorf("traces/%s not_run = %+v, want %q with a fix naming invoke_agent", c.trace, d.NotRun, c.notRun)
		}
	}
	// The list: the derived runs in run_ids, the chat-only trace none.
	_, _, body := get(t, h, "/studio/api/traces")
	var page struct {
		Traces []struct {
			TraceID string   `json:"trace_id"`
			RunIDs  []string `json:"run_ids"`
			RootRun *struct {
				ID string `json:"id"`
			} `json:"root_run"`
		} `json:"traces"`
	}
	decode(t, body, &page)
	got := map[string][]string{}
	for _, tr := range page.Traces {
		got[tr.TraceID] = tr.RunIDs
		if len(tr.RunIDs) > 0 && (tr.RootRun == nil || tr.RootRun.ID != tr.RunIDs[0]) {
			t.Errorf("trace %s root_run = %+v, want its derived run", tr.TraceID, tr.RootRun)
		}
	}
	if !slices.Equal(got[agentTrace], []string{noconv}) || !slices.Equal(got["4bf92f3577b34da6a3ce929d0e0e4736"], []string{derivedOK}) ||
		len(got[chatTrace]) != 0 {
		t.Errorf("traces list run_ids = %v", got)
	}
	// The conversation-id-less agent has a run page: derived, no session.
	code, _, body := get(t, h, "/studio/api/runs/"+noconv)
	var run struct {
		Derived   bool   `json:"derived"`
		SessionID string `json:"session_id"`
		Agent     string `json:"agent"`
		Steps     int    `json:"steps"`
	}
	decode(t, body, &run)
	if code != http.StatusOK || !run.Derived || run.SessionID != "" || run.Agent != "planner" || run.Steps != 1 {
		t.Errorf("runs/%s = %d %s, want derived, no session, planner, 1 step", noconv, code, body)
	}
}

// A token that may not read system prompts (a read-scoped panel token,
// here a tag token whose tag every stock span carries) reads a stock
// trace's spans without the system prompt its content attributes carry
// (docs/record-contract.md §9, the I6 review): no
// gen_ai.system_instructions, no system-role input message, no legacy
// system slot, no plain gen_ai.prompt / gen_ai.content.prompt string
// (it cannot be split), no gen_ai.system.message event — the user's
// messages stay. The server token reads them
// all.
func TestDerivedSpansHideSystemPromptFromReadTokens(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const tr = "7f000000000000000000000000000001"
	at := time.Now().Add(-time.Minute)
	tag := func(a map[string]any) map[string]any { a["arena.batch"] = "b1"; return a }
	spans := []obsdb.Span{
		{TraceID: tr, SpanID: "7f00000000000001", Name: "invoke_agent planner", Start: at, End: at.Add(time.Second),
			Attrs: tag(map[string]any{"gen_ai.operation.name": "invoke_agent"})},
		{TraceID: tr, SpanID: "7f00000000000002", ParentSpanID: "7f00000000000001", Name: "chat m", Start: at, End: at.Add(time.Second),
			Attrs: tag(map[string]any{"gen_ai.operation.name": "chat",
				"gen_ai.system_instructions": `[{"type":"text","content":"SECRET SYSTEM"}]`,
				"gen_ai.input.messages":      `[{"role":"system","parts":[{"type":"text","content":"SECRET INLINE"}]},{"role":"user","parts":[{"type":"text","content":"hello there"}]}]`,
				"gen_ai.prompt.0.role":       "system", "gen_ai.prompt.0.content": "SECRET LEGACY",
				"gen_ai.prompt.1.role": "user", "gen_ai.prompt.1.content": "legacy user",
				"gen_ai.content.prompt": "SECRET PLAIN CONTENT"}),
			Events: []obsdb.SpanEvent{
				{Time: at, Name: "gen_ai.system.message", Attrs: map[string]any{"content": "SECRET EVENT"}},
				{Time: at, Name: "gen_ai.user.message", Attrs: map[string]any{"content": "user event"}},
			}},
		{TraceID: tr, SpanID: "7f00000000000003", ParentSpanID: "7f00000000000001", Name: "chat m", Start: at, End: at.Add(time.Second),
			Attrs: tag(map[string]any{"gen_ai.operation.name": "chat", "gen_ai.prompt": "SECRET PLAIN"})},
	}
	if err := db.Write(context.Background(), obsdb.Batch{Spans: spans}); err != nil {
		t.Fatal(err)
	}
	srv := New(DB(db), Token("srv"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)

	code, body := tcall(t, ts, http.MethodGet, "/api/traces/"+tr, tok, "")
	if code != http.StatusOK {
		t.Fatalf("tag token's trace read: %d %s", code, body)
	}
	for _, secret := range []string{"SECRET SYSTEM", "SECRET INLINE", "SECRET LEGACY", "SECRET PLAIN", "SECRET EVENT", "gen_ai.system_instructions"} {
		if strings.Contains(body, secret) {
			t.Errorf("a read token's trace carries %q: %s", secret, body)
		}
	}
	for _, kept := range []string{"hello there", "legacy user", "user event"} {
		if !strings.Contains(body, kept) {
			t.Errorf("a read token's trace lost %q: %s", kept, body)
		}
	}
	if _, full := tcall(t, ts, http.MethodGet, "/api/traces/"+tr, "srv", ""); !strings.Contains(full, "SECRET SYSTEM") ||
		!strings.Contains(full, "SECRET INLINE") || !strings.Contains(full, "SECRET LEGACY") ||
		!strings.Contains(full, "SECRET PLAIN") || !strings.Contains(full, "SECRET EVENT") {
		t.Errorf("the server token's trace lost the system prompt: %s", full)
	}
}

// The trace list names a derived trace's root run by its row (the I6
// review): a nested derived run whose span id sorts before its parent's
// is never the root; the failures count every derived run of the trace.
func TestDerivedTraceRootRun(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const tr = "7f000000000000000000000000000002"
	at := time.Now().Add(-time.Minute)
	sp := func(id, parent, name string, attrs map[string]any, status int) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: name, Start: at, End: at.Add(time.Second),
			StatusCode: status, Attrs: attrs}
	}
	// The root's span id sorts last; its child (a conversation of its
	// own, under the root's tool) first, and failed.
	spans := []obsdb.Span{
		sp("ff00000000000001", "", "invoke_agent planner", map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "planner"}, 1),
		sp("ff00000000000002", "ff00000000000001", "execute_tool delegate", map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.call.id": "c1"}, 1),
		sp("0000000000000003", "ff00000000000002", "invoke_agent worker", map[string]any{"gen_ai.operation.name": "invoke_agent",
			"gen_ai.agent.name": "worker", "gen_ai.conversation.id": "conv_w"}, 2),
	}
	if err := db.Write(context.Background(), obsdb.Batch{Spans: spans}); err != nil {
		t.Fatal(err)
	}
	srv := New(DB(db))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	_, _, body := get(t, h, "/studio/api/traces")
	var page struct {
		Traces []struct {
			RunIDs  []string `json:"run_ids"`
			RootRun *struct {
				ID    string `json:"id"`
				Agent string `json:"agent"`
			} `json:"root_run"`
			FailedRuns int `json:"failed_runs"`
		} `json:"traces"`
	}
	decode(t, body, &page)
	if len(page.Traces) != 1 || len(page.Traces[0].RunIDs) != 2 {
		t.Fatalf("traces = %s, want one trace with two derived runs", body)
	}
	got := page.Traces[0]
	if got.RootRun == nil || got.RootRun.ID != "otel-"+tr+"-ff00000000000001" || got.RootRun.Agent != "planner" || got.FailedRuns != 1 {
		t.Errorf("root run %+v failed %d, want the planner (no parent) and the worker's failure counted", got.RootRun, got.FailedRuns)
	}
}
