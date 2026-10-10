package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// recordPreviewRun records, through the real pipeline into the
// playground server, the edit fixtures' source run: the turn's prompt,
// step 0 looks order 42 up (c1), step 1 order 43 (c2), step 2 answers.
func recordPreviewRun(t *testing.T, pt *playgroundTestServer, runID string) {
	t.Helper()
	withPipeline(t, pt.ts.URL, true, func(prov []core.Option) {
		lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in diffOrderIn) (string, error) {
			return "order " + in.OrderID + " shipped", nil
		})
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"43"}`, ID: "c2"}),
			wefttest.Say("both shipped"),
		), append([]core.Option{core.Name("acme-support"), core.Instructions("You look orders up."), lookup}, prov...)...)
		if _, err := agent.Generate(context.Background(), core.RunID(runID), core.Prompt("where are orders 42 and 43?")); err != nil {
			t.Fatal(err)
		}
	})
	waitRun(t, pt.ts, runID, 3, pt.token)
}

// previewPost posts a body to path (the preview or the run route).
func previewPost(t *testing.T, pt *playgroundTestServer, path, bearer, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, pt.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// previewEdits are the Done line's edits: the turn's prompt, c1's
// arguments, c2's result (the pre-F2 shape, no kind), two inserts.
const previewEdits = `[` +
	`{"kind":"user","step":0,"content":"where are orders 7 and 43?"},` +
	`{"kind":"tool_args","step":0,"call_id":"c1","args":{"order_id":"7"}},` +
	`{"step":1,"call_id":"c2","tool_result":"order 43 lost"},` +
	`{"kind":"insert","step":2,"content":"and refund the lost one"},` +
	`{"kind":"insert","step":1,"content":"also check 43"}]`

// previewDocT is the preview as a client decodes it.
type previewDocT struct {
	Runtime  *string `json:"runtime"`
	WillSend struct {
		System       *string           `json:"system"`
		SystemSource string            `json:"system_source"`
		SystemBadge  string            `json:"system_badge"`
		Messages     []json.RawMessage `json:"messages"`
		Tools        []struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
		} `json:"tools"`
		ToolsSource  string `json:"tools_source"`
		ParamsSource string `json:"params_source"`
		Params       struct {
			Temperature *float64 `json:"temperature"`
		} `json:"params"`
		Badge string `json:"badge"`
	} `json:"will_send"`
	WasSent struct {
		System   *string           `json:"system"`
		Messages []json.RawMessage `json:"messages"`
	} `json:"was_sent"`
	Diff struct {
		System   string `json:"system"`
		Messages []struct {
			Op   string `json:"op"`
			Was  *int   `json:"was"`
			Will *int   `json:"will"`
		} `json:"messages"`
		Tools *struct {
			Added   []string `json:"added"`
			Removed []string `json:"removed"`
		} `json:"tools"`
		Params string `json:"params"`
	} `json:"diff"`
	Warnings []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"warnings"`
	Unchecked []string `json:"unchecked"`
}

func (d previewDocT) warning(kind string) string {
	for _, w := range d.Warnings {
		if w.Kind == kind {
			return w.Message
		}
	}
	return ""
}

// TestPreviewRoute is plan F2's Done line on the API: one command that
// edits a user message, a tool's args and a tool's result (and inserts
// two messages) previews as the exact first request — the edited
// prefix, the override's system, the sampling laid over the recorded
// request's — diffed against what step 2 recorded, row by row, without
// a runtime. Pinned as a golden (the "will be sent" pane both UIs
// render). The command's refusals are the run route's, status and
// sentence — a schema-invalid args edit names the field — except the
// scripted engine's, which the preview turns into warnings. With a
// runtime registered the overrides are checked against it; without,
// they are listed unchecked.
func TestPreviewRoute(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	recordPreviewRun(t, pt, "r_pv")

	body := `{"agent":"acme-support","source":{"run_id":"r_pv","from_step":2},` +
		`"overrides":{"instructions":"Be brief.","options":{"temperature":0.5}},` +
		`"transcript_edits":` + previewEdits + `,"engine":"live"}`
	code, out := previewPost(t, pt, "/api/playground/preview", "", body)
	if code != http.StatusOK {
		t.Fatalf("preview = %d %s", code, out)
	}
	stepGolden(t, "playground-preview.golden.json", out)
	var d previewDocT
	decode(t, out, &d)

	// The exact first request: the recorded step-2 prefix, edited.
	was := d.WasSent.Messages
	if len(was) != 5 {
		t.Fatalf("was_sent = %d messages, want step 2's five", len(was))
	}
	user := func(text string) json.RawMessage {
		b, _ := json.Marshal(core.User(text))
		return b
	}
	var a1 core.Message
	if err := json.Unmarshal(was[1], &a1); err != nil {
		t.Fatal(err)
	}
	call := a1.Content[0].(core.ToolCallPart)
	call.Args = json.RawMessage(`{"order_id":"7"}`)
	a1.Content = []core.Part{call}
	a1JSON, _ := json.Marshal(a1)
	var t2 core.Message
	if err := json.Unmarshal(was[4], &t2); err != nil {
		t.Fatal(err)
	}
	r := t2.Content[0].(core.ToolResultPart)
	r.Content, r.IsError = "order 43 lost", false
	t2.Content = []core.Part{r}
	t2JSON, _ := json.Marshal(t2)
	want := []json.RawMessage{user("where are orders 7 and 43?"), a1JSON, was[2], user("also check 43"), was[3], t2JSON, user("and refund the lost one")}
	gotJSON, _ := json.Marshal(d.WillSend.Messages)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("will_send.messages\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	var ops []string
	for _, row := range d.Diff.Messages {
		op := row.Op
		if row.Was != nil {
			op += ":" + strconv.Itoa(*row.Was)
		}
		op += "/"
		if row.Will != nil {
			op += strconv.Itoa(*row.Will)
		}
		ops = append(ops, op)
	}
	if got := strings.Join(ops, " "); got != "changed:0/0 changed:1/1 same:2/2 added/3 same:3/4 changed:4/5 added/6" {
		t.Errorf("diff.messages = %s", got)
	}
	if d.WillSend.System == nil || *d.WillSend.System != "Be brief." || d.WillSend.SystemSource != "override" ||
		d.WasSent.System == nil || *d.WasSent.System != "You look orders up." || d.Diff.System != "changed" {
		t.Errorf("system: will %v (%s), was %v, diff %s", d.WillSend.System, d.WillSend.SystemSource, d.WasSent.System, d.Diff.System)
	}
	if p := d.WillSend.Params.Temperature; p == nil || *p != 0.5 || d.Diff.Params != "changed" || d.WillSend.ParamsSource != "recorded" {
		t.Errorf("params: temperature %v (%s), diff %s", p, d.WillSend.ParamsSource, d.Diff.Params)
	}
	if len(d.WillSend.Tools) != 1 || d.WillSend.Tools[0].Name != "lookup_order" || len(d.WillSend.Tools[0].Schema) == 0 ||
		d.Diff.Tools == nil || len(d.Diff.Tools.Added)+len(d.Diff.Tools.Removed) != 0 {
		t.Errorf("tools = %+v, diff %+v; want the recorded catalog, unchanged", d.WillSend.Tools, d.Diff.Tools)
	}
	if d.warning("prepare_step") == "" || d.warning("instructions") == "" || d.Runtime != nil || len(d.Unchecked) != 0 {
		t.Errorf("warnings %+v runtime %v unchecked %v", d.Warnings, d.Runtime, d.Unchecked)
	}

	// The refusals: the run route's status and sentence, on both routes.
	edit := func(e string) string {
		return `{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_pv","from_step":2},"transcript_edits":[` + e + `],"engine":"live"}`
	}
	for _, c := range []struct{ name, body, want string }{
		{"schema-invalid args", edit(`{"kind":"tool_args","step":0,"call_id":"c1","args":{"order_id":7}}`),
			`INVALID_INPUT: tool "lookup_order": field "order_id": expected string, got number`},
		{"undeclared field", edit(`{"kind":"tool_args","step":0,"call_id":"c1","args":{"order_id":"7","x":1}}`), ""},
		{"user index out of range", edit(`{"kind":"user","step":0,"index":1,"content":"x"}`),
			"step 0 has 1 user message(s) in the kept prefix: index 1 is out of range"},
		{"insert past from_step", edit(`{"kind":"insert","step":3,"content":"x"}`),
			"insert step 3 is past from_step 2: an insert lands at a step boundary 0..2"},
		{"unknown kind", edit(`{"kind":"system","step":0,"content":"x"}`),
			`unknown edit kind "system" (tool_result, reply, user, tool_args or insert)`},
		{"a field the kind does not take", edit(`{"kind":"insert","step":1,"content":"x","call_id":"c1"}`),
			"an insert edit does not take call_id"},
		{"args without kind", edit(`{"step":0,"call_id":"c1","args":{}}`), `an edit with args needs kind "tool_args"`},
		{"args null on a tool_args edit", edit(`{"kind":"tool_args","step":0,"call_id":"c1","args":null}`), "a tool_args edit needs args"},
		{"args null beside a user edit", edit(`{"kind":"user","step":0,"content":"x","args":null}`), ""},
		{"args null beside an insert", edit(`{"kind":"insert","step":1,"content":"x","args":null}`), ""},
		{"input beside from_step", `{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_pv","from_step":2},"input":"x"}`,
			`input replaces the turn's user message only when from_step is 0: with from_step > 0, edit step 0's user message instead (a transcript edit of kind "user")`},
	} {
		for _, path := range []string{"/api/playground/preview", "/api/playground/runs"} {
			code, out := previewPost(t, pt, path, "", c.body)
			if c.want == "" {
				// Not a refusal: the lookup_order schema the reflector
				// wrote does not close the object; null args are absent.
				if path == "/api/playground/preview" && code != http.StatusOK {
					t.Errorf("%s on %s = %d %s, want accepted", c.name, path, code, out)
				}
				continue
			}
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(out), &e)
			if code != http.StatusBadRequest || !strings.Contains(e.Error.Message, c.want) {
				t.Errorf("%s on %s = %d %s\nwant 400 with %s", c.name, path, code, out, c.want)
			}
		}
	}

	// The scripted engine: a 400 on the run route, a warning here.
	scripted := `{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_pv","from_step":2},` +
		`"transcript_edits":[{"kind":"insert","step":2,"content":"x"}],"engine":"scripted"}`
	const trap = "the scripted engine would replay the recorded turn 2, which answered a different prompt: transcript edits need engine live"
	if code, out := previewPost(t, pt, "/api/playground/runs", "", scripted); code != http.StatusBadRequest || !strings.Contains(out, trap) {
		t.Errorf("scripted run with edits = %d %s, want 400 %q", code, out, trap)
	}
	code, out = previewPost(t, pt, "/api/playground/preview", "", scripted)
	var sd previewDocT
	decode(t, out, &sd)
	if code != http.StatusOK || sd.warning("scripted") != trap {
		t.Errorf("scripted preview = %d %s, want 200 with the scripted warning", code, out)
	}

	// Registered: the overrides are checked against the runtime's copy
	// and the agent's defaults stand in for the record's.
	code, out = previewPost(t, pt, "/api/playground/preview", "",
		`{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_pv","from_step":2},"overrides":{"tools_enabled":["nope"]}}`)
	if code != http.StatusBadRequest || !strings.Contains(out, "tool nope is not in agent acme-support's manifest") {
		t.Errorf("unknown tool against the registration = %d %s", code, out)
	}
	code, out = previewPost(t, pt, "/api/playground/preview", "",
		`{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_pv","from_step":2},"overrides":{"only_tools":["lookup_order","refund"]}}`)
	var rd previewDocT
	decode(t, out, &rd)
	if code != http.StatusOK || rd.Runtime == nil || rd.WillSend.ToolsSource != "agent" || len(rd.WillSend.Tools) != 2 ||
		rd.Diff.Tools == nil || strings.Join(rd.Diff.Tools.Added, ",") != "refund" || rd.WillSend.ParamsSource != "agent" {
		t.Errorf("registered preview = %d %s", code, out)
	}
	// Unregistered: listed, not checked.
	code, out = previewPost(t, pt, "/api/playground/preview", "",
		`{"agent":"acme-support","source":{"run_id":"r_pv","from_step":2},"overrides":{"tools_enabled":["nope"],"model":"x"}}`)
	var ud previewDocT
	decode(t, out, &ud)
	if code != http.StatusOK || strings.Join(ud.Unchecked, ",") != "tools_enabled,model" || len(ud.WillSend.Tools) != 0 {
		t.Errorf("unregistered preview = %d %s, want tools_enabled and model unchecked, no tool kept", code, out)
	}

	// from_step 0 with an input: the turn's prompt replaced.
	code, out = previewPost(t, pt, "/api/playground/preview", "",
		`{"agent":"acme-support","source":{"run_id":"r_pv","from_step":0},"input":"where is order 9?"}`)
	var zd previewDocT
	decode(t, out, &zd)
	if code != http.StatusOK || len(zd.WillSend.Messages) != 1 || string(zd.WillSend.Messages[0]) != string(user("where is order 9?")) ||
		len(zd.Diff.Messages) != 1 || zd.Diff.Messages[0].Op != "changed" {
		t.Errorf("from_step 0 preview = %d %s", code, out)
	}
	// No source; past the last step.
	if code, out := previewPost(t, pt, "/api/playground/preview", "", `{"agent":"acme-support"}`); code != http.StatusBadRequest {
		t.Errorf("no source = %d %s", code, out)
	}
	if code, out := previewPost(t, pt, "/api/playground/preview", "", `{"agent":"acme-support","source":{"run_id":"r_pv","from_step":4}}`); code != http.StatusBadRequest ||
		!strings.Contains(out, "from_step 4 is beyond the source run's last step") {
		t.Errorf("past the end = %d %s", code, out)
	}
}

// TestPreviewHidesSystemFromAReadToken: a read-scoped panel token
// previews a run of its public id with the system prompt and the
// catalog hidden (badge hidden, the diff's columns with them), and the
// prompt-adjacent knobs with them — a named tool choice's tool and the
// stop sequences, on both requests; a playground-scoped one reads them.
func TestPreviewHidesSystemFromAReadToken(t *testing.T) {
	pt := newPlaygroundServer(t, "srv-token")
	withPipeline(t, pt.ts.URL, true, func(prov []core.Option) {
		secret := core.Tool("SECRET_TOOL", "A tool.", func(context.Context, struct{}) (string, error) { return "", nil })
		agent := core.New(wefttest.Script(wefttest.Say("hi")),
			append([]core.Option{core.Name("acme-support"), core.Instructions("SECRET SYSTEM"), secret,
				core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "SECRET_TOOL"}),
				core.Params(core.RequestParams{Stop: []string{"SECRET STOP"}})}, prov...)...)
		if _, err := agent.Generate(context.Background(), core.RunID("r_tok"), core.Prompt("hello"),
			core.Metadata(map[string]string{"weft.public_id": "pub_a"})); err != nil {
			t.Fatal(err)
		}
	})
	waitRun(t, pt.ts, "r_tok", 1, "srv-token")
	body := `{"agent":"acme-support","source":{"run_id":"r_tok","from_step":0}}`
	for _, c := range []struct {
		scope  string
		hidden bool
	}{{scopeRead, true}, {scopePlayground, false}} {
		tok, err := signPanelToken([]byte("srv-token"), panelClaims{PublicID: "pub_a", Scope: c.scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		code, out := previewPost(t, pt, "/api/playground/preview", tok, body)
		var d previewDocT
		decode(t, out, &d)
		hidden := d.WillSend.System == nil && d.WillSend.SystemBadge == "hidden" && d.Diff.System == "hidden" && !strings.Contains(out, "SECRET")
		shown := strings.Contains(out, `"name":"SECRET_TOOL"`) && strings.Contains(out, "SECRET STOP") && strings.Contains(out, "SECRET SYSTEM")
		if code != http.StatusOK || hidden != c.hidden || shown == c.hidden {
			t.Errorf("%s token: preview = %d %s, want system hidden %v", c.scope, code, out, c.hidden)
		}
	}
}

// TestPreviewOverACompactedStep: from a step whose request carried a
// compaction view and with no edit, the preview's first request is
// exactly what that step recorded — every message same — with the view
// named (compacted_at) and the compacted warning; an insert at the
// view's end lands after the summary, the rest unchanged.
func TestPreviewOverACompactedStep(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	recordStepsRun(t, pt.ts.URL, "r_cv", nil)
	fetchJSON(t, pt.ts, "/api/runs/r_cv", func(b string) bool { return strings.Contains(b, `"request_count":6`) })

	code, out := previewPost(t, pt, "/api/playground/preview", "", `{"agent":"orders","source":{"run_id":"r_cv","from_step":2}}`)
	var d previewDocT
	decode(t, out, &d)
	var doc struct {
		CompactedAt *struct {
			FromSeq int `json:"from_seq"`
			ToSeq   int `json:"to_seq"`
		} `json:"compacted_at"`
	}
	decode(t, out, &doc)
	same := len(d.Diff.Messages) == len(d.WasSent.Messages) && len(d.WasSent.Messages) > 0
	for _, row := range d.Diff.Messages {
		same = same && row.Op == "same"
	}
	if code != http.StatusOK || !same || doc.CompactedAt == nil || d.warning("compacted") == "" || d.Diff.System != "same" {
		t.Errorf("compacted preview = %d %s, want every message same, the view named", code, out)
	}

	code, out = previewPost(t, pt, "/api/playground/preview", "",
		`{"agent":"orders","source":{"run_id":"r_cv","from_step":2},"transcript_edits":[{"kind":"insert","step":1,"content":"go on"}]}`)
	var ins previewDocT
	decode(t, out, &ins)
	var ops []string
	for _, row := range ins.Diff.Messages {
		ops = append(ops, row.Op)
	}
	// step 2's request: [prompt, summary, a(c_sub), t(c_sub)]; boundary 1
	// is seq 3, the view's end: after the summary, before step 1's call.
	if code != http.StatusOK || strings.Join(ops, ",") != "same,same,added,same,same" ||
		string(ins.WillSend.Messages[2]) != `{"role":"user","content":[{"type":"text","text":"go on"}]}` {
		t.Errorf("insert over the view = %d %v %s", code, ops, out)
	}
}

// TestPreviewReadTokenLearnsNoCatalog: the preview answers a read-scoped
// token, which may not read the tool catalog — so no refusal may carry
// it. Under a read token a tool_args edit is checked for its object
// shape alone (a schema-invalid one previews; the run route would
// refuse it to a token that may act), a registration refusal is
// generic, and over a compaction view the messages and their diff are
// hidden too. A playground-scoped token gets the detailed sentences.
func TestPreviewReadTokenLearnsNoCatalog(t *testing.T) {
	pt := newPlaygroundServer(t, "srv-token")
	withPipeline(t, pt.ts.URL, true, func(prov []core.Option) {
		lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in diffOrderIn) (string, error) {
			return "order " + in.OrderID + " shipped", nil
		})
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
			wefttest.Say("shipped"),
		), append([]core.Option{core.Name("acme-support"), lookup}, prov...)...)
		if _, err := agent.Generate(context.Background(), core.RunID("r_rt"), core.Prompt("where is 42?"),
			core.Metadata(map[string]string{"weft.public_id": "pub_a"})); err != nil {
			t.Fatal(err)
		}
	})
	waitRun(t, pt.ts, "r_rt", 2, "srv-token")
	recordStepsRun(t, pt.ts.URL, "r_rv", map[string]string{"weft.public_id": "pub_a"})
	fetchJSONAs(t, pt.ts, "/api/runs/r_rv", "srv-token", func(b string) bool { return strings.Contains(b, `"request_count":6`) })
	tok := func(scope string) string {
		s, err := signPanelToken([]byte("srv-token"), panelClaims{PublicID: "pub_a", Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	read, pg := tok(scopeRead), tok(scopePlayground)
	bad := `{"agent":"acme-support","source":{"run_id":"r_rt","from_step":1},` +
		`"transcript_edits":[{"kind":"tool_args","step":0,"call_id":"c1","args":{"order_id":7}}]}`
	if code, out := previewPost(t, pt, "/api/playground/preview", read, bad); code != http.StatusOK || strings.Contains(out, "expected string") {
		t.Errorf("read token, schema-invalid args = %d %s, want previewed, no schema detail", code, out)
	}
	if code, out := previewPost(t, pt, "/api/playground/preview", pg, bad); code != http.StatusBadRequest ||
		!strings.Contains(out, `field \"order_id\": expected string, got number`) {
		t.Errorf("playground token, schema-invalid args = %d %s, want the field named", code, out)
	}
	if code, out := previewPost(t, pt, "/api/playground/preview", read, `{"agent":"acme-support","source":{"run_id":"r_rt","from_step":1},"transcript_edits":[{"kind":"tool_args","step":0,"call_id":"c1","args":[7]}]}`); code != http.StatusBadRequest ||
		!strings.Contains(out, "expected object at the top level, got array") {
		t.Errorf("read token, args not an object = %d %s, want the shape refused", code, out)
	}
	unknown := `{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_rt","from_step":1},"overrides":{"tools_enabled":["secret_tool"]}}`
	if code, out := previewPost(t, pt, "/api/playground/preview", read, unknown); code != http.StatusBadRequest ||
		strings.Contains(out, "secret_tool") || !strings.Contains(out, "hidden to this token") {
		t.Errorf("read token, unknown tool = %d %s, want a generic 400", code, out)
	}
	if code, out := previewPost(t, pt, "/api/playground/preview", pg, unknown); code != http.StatusBadRequest || !strings.Contains(out, "secret_tool is not in agent") {
		t.Errorf("playground token, unknown tool = %d %s, want the tool named", code, out)
	}
	// Over a view: the messages and their diff hidden from a read token.
	view := `{"agent":"orders","source":{"run_id":"r_rv","from_step":2}}`
	code, out := previewPost(t, pt, "/api/playground/preview", read, view)
	var d struct {
		WillSend struct {
			Messages      []json.RawMessage `json:"messages"`
			MessagesBadge string            `json:"messages_badge"`
		} `json:"will_send"`
		WasSent struct {
			Messages      []json.RawMessage `json:"messages"`
			MessagesBadge string            `json:"messages_badge"`
		} `json:"was_sent"`
		Diff struct {
			Messages []json.RawMessage `json:"messages"`
		} `json:"diff"`
		CompactedAt any `json:"compacted_at"`
	}
	decode(t, out, &d)
	if code != http.StatusOK || d.WillSend.Messages != nil || d.WasSent.Messages != nil || d.Diff.Messages != nil ||
		d.WillSend.MessagesBadge != "hidden" || d.WasSent.MessagesBadge != "hidden" || d.CompactedAt == nil || strings.Contains(out, "summary:") {
		t.Errorf("read token over a view = %d %s, want messages and their diff hidden", code, out)
	}
	if code, out := previewPost(t, pt, "/api/playground/preview", pg, view); code != http.StatusOK || !strings.Contains(out, "summary:") {
		t.Errorf("playground token over a view = %d %s, want the messages", code, out)
	}
}

// TestEditedPrefixKinds pins Studio's mirror on the kinds the API
// tests do not reach: the explicit reply kind (the pre-F2 content
// edit, named), and null args read as absent.
func TestEditedPrefixKinds(t *testing.T) {
	input := []core.Message{core.User("refund 4411")}
	steps := []stepMessage{
		{0, core.Message{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "lookup", Args: []byte(`{}`)}}}},
		{0, core.Message{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "lookup", Content: "shipped"}}}},
		{1, core.Assistant("Shipped.")},
		{1, core.User("thanks")},
		{2, core.Assistant("Anytime.")},
	}
	got, _, err := editedPrefix(input, steps, 2, []linkruntime.TranscriptEdit{
		{Kind: "reply", Step: 1, Content: "rewritten", Args: json.RawMessage(`null`)},
		{Kind: "user", Step: 1, Content: "thank you"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"text":"rewritten"`) || !strings.Contains(string(b), `"text":"thank you"`) || strings.Contains(string(b), "Shipped.") {
		t.Errorf("edited prefix = %s", b)
	}
	if _, _, err := editedPrefix(input, steps, 2, []linkruntime.TranscriptEdit{{Kind: "reply", Step: 0, Content: "x"}}, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "carried tool calls") {
		t.Errorf("explicit reply of a step with calls = %v", err)
	}
}

// TestDiffMessagesFallsBackPastTheCap: past maxDiffCells the alignment
// is position by position — a long transcript still answers, every
// message aligned, the one changed message changed.
func TestDiffMessagesFallsBackPastTheCap(t *testing.T) {
	n := 1100 // 1101² cells > maxDiffCells
	was := make([]core.Message, n)
	for i := range was {
		was[i] = core.User(strconv.Itoa(i))
	}
	will := slices.Clone(was)
	will[500] = core.User("edited")
	rows := diffMessages(was, will)
	changed := 0
	for i, r := range rows {
		if r.Was == nil || r.Will == nil || *r.Was != i || *r.Will != i {
			t.Fatalf("row %d = %+v, want positional", i, r)
		}
		if r.Op == "changed" {
			changed++
		}
	}
	if len(rows) != n || changed != 1 || rows[500].Op != "changed" {
		t.Errorf("%d rows, %d changed, row 500 %s", len(rows), changed, rows[500].Op)
	}
}

// pagedRequestsDB answers Requests from a synthetic list, in index
// order, a page at a time as obsdb does (Step filters, From is the
// first index, PageLimit caps the page).
type pagedRequestsDB struct {
	obsdb.DB
	recs []obsdb.RequestRecord
}

func (p *pagedRequestsDB) Requests(_ context.Context, _ string, q obsdb.RequestQuery) ([]obsdb.RequestRecord, error) {
	var out []obsdb.RequestRecord
	for _, r := range p.recs {
		if r.Index < q.From || q.Step != nil && r.Step != *q.Step {
			continue
		}
		if len(out) == q.PageLimit() {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

// TestRecordedRequestIsTheLatestEarlier: from_step at the step count
// has no request of its own, and was_sent is the latest earlier one —
// for a run with more attempts than one page holds, the last of step
// from − 1, never the 1000th by index. A step's own answering attempt
// is its highest index even past a page.
func TestRecordedRequestIsTheLatestEarlier(t *testing.T) {
	var recs []obsdb.RequestRecord
	for i := 0; i < 1500; i++ {
		recs = append(recs, obsdb.RequestRecord{Index: int64(i), Step: i / 500}) // steps 0..2, 500 attempts each
	}
	for i := 1500; i < 2700; i++ {
		recs = append(recs, obsdb.RequestRecord{Index: int64(i), Step: 3}) // step 3: 1200 attempts
	}
	s := &Server{config: config{db: &pagedRequestsDB{recs: recs}}}
	for _, c := range []struct {
		from      int
		step      int
		index     int64
		situation string
	}{
		{4, 3, 2699, "from_step at the count: step 3's last attempt"},
		{3, 3, 2699, "step 3's own answering attempt, past one page"},
		{1, 1, 999, "step 1's own"},
	} {
		got, err := s.recordedRequest(context.Background(), "r", c.from)
		if err != nil || got == nil || got.Step != c.step || got.Index != c.index {
			t.Errorf("%s: recordedRequest(%d) = %+v, %v; want step %d index %d", c.situation, c.from, got, err, c.step, c.index)
		}
	}
	empty := &Server{config: config{db: &pagedRequestsDB{}}}
	if got, err := empty.recordedRequest(context.Background(), "r", 2); got != nil || err != nil {
		t.Errorf("no records: %+v, %v; want nil", got, err)
	}
}

// TestArgsEditChecksTheEditedStepsSchema: a tool_args edit is checked
// against the schema of the step whose call it rewrites — the catalog
// that step's request recorded, not the run's last — and, with the
// agent registered, against its manifest's (what the runtime itself
// checks). Here lookup_order takes a string order_id at step 0 and an
// integer one from step 1 on (a ToolSource swaps it).
func TestArgsEditChecksTheEditedStepsSchema(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	type intIn struct {
		OrderID int `json:"order_id"`
	}
	var swapped atomic.Bool
	withPipeline(t, pt.ts.URL, true, func(prov []core.Option) {
		byString := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in diffOrderIn) (string, error) {
			swapped.Store(true)
			return "order " + in.OrderID + " shipped", nil
		})
		byInt := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in intIn) (string, error) {
			return "order " + strconv.Itoa(in.OrderID) + " shipped", nil
		})
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":43}`, ID: "c2"}),
			wefttest.Say("both shipped"),
		), append([]core.Option{core.Name("acme-support"), core.ToolSource(func() []*core.ToolDef {
			if swapped.Load() {
				return []*core.ToolDef{byInt}
			}
			return []*core.ToolDef{byString}
		})}, prov...)...)
		if _, err := agent.Generate(context.Background(), core.RunID("r_sw"), core.Prompt("42 and 43?")); err != nil {
			t.Fatal(err)
		}
	})
	waitRun(t, pt.ts, "r_sw", 3, pt.token)
	fetchJSON(t, pt.ts, "/api/runs/r_sw", func(b string) bool { return strings.Contains(b, `"request_count":3`) })

	edit := func(runtime, step, call, args string) string {
		rt := ""
		if runtime != "" {
			rt = `"runtime":"` + runtime + `",`
		}
		return `{` + rt + `"agent":"acme-support","source":{"run_id":"r_sw","from_step":2},` +
			`"transcript_edits":[{"kind":"tool_args","step":` + step + `,"call_id":"` + call + `","args":` + args + `}]}`
	}
	for _, c := range []struct {
		name, body string
		code       int
		want       string
	}{
		// No registration: each step's own recorded catalog.
		{"step 0, a string, step 0's schema", edit("", "0", "c1", `{"order_id":"7"}`), http.StatusOK, ""},
		{"step 0, a number, step 0's schema", edit("", "0", "c1", `{"order_id":7}`), http.StatusBadRequest, `field \"order_id\": expected string, got number`},
		{"step 1, a number, step 1's schema", edit("", "1", "c2", `{"order_id":7}`), http.StatusOK, ""},
		{"step 1, a string, step 1's schema", edit("", "1", "c2", `{"order_id":"7"}`), http.StatusBadRequest, `field \"order_id\": expected integer, got string`},
		// Registered: the manifest's (a string order_id), whatever the step.
		{"registered, step 1, a string", edit("rt_test", "1", "c2", `{"order_id":"7"}`), http.StatusOK, ""},
		{"registered, step 1, a number", edit("rt_test", "1", "c2", `{"order_id":7}`), http.StatusBadRequest, `field \"order_id\": expected string, got number`},
	} {
		code, out := previewPost(t, pt, "/api/playground/preview", "", c.body)
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("%s: preview = %d %s, want %d %q", c.name, code, out, c.code, c.want)
		}
	}
	// The run route shares the rule: registered, the manifest decides.
	run := strings.Replace(edit("rt_test", "1", "c2", `{"order_id":7}`), `{"runtime"`, `{"engine":"live","runtime"`, 1)
	if code, out := pt.post(t, run); code != http.StatusBadRequest || !strings.Contains(out, `expected string, got number`) {
		t.Errorf("run route, registered, a number = %d %s, want the manifest's refusal", code, out)
	}
	if code, out := pt.post(t, edit("rt_test", "1", "c2", `{"order_id":"7"}`)); code != http.StatusAccepted {
		t.Errorf("run route, registered, a string = %d %s, want 202", code, out)
	} else {
		pt.waitCommand(t, "registered string")
	}
}
