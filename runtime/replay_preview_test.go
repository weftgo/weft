package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

// postStudio posts body to Studio's path and returns the status and
// the error message (or the whole body on success).
func postStudio(t *testing.T, url, path string, body any) (int, string) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error.Message != "" {
			return resp.StatusCode, e.Error.Message
		}
	}
	return resp.StatusCode, string(out)
}

// previewBody is the §5.1 command as Studio's routes read it.
type previewBody struct {
	Runtime         string           `json:"runtime,omitempty"`
	Agent           string           `json:"agent"`
	Source          *sourceSpec      `json:"source"`
	Input           *string          `json:"input,omitempty"`
	Overrides       overrides        `json:"overrides"`
	TranscriptEdits []transcriptEdit `json:"transcript_edits,omitempty"`
	Engine          string           `json:"engine,omitempty"`
	Thread          string           `json:"thread,omitempty"`
	SideEffects     string           `json:"side_effects,omitempty"`
}

// previewWill is the part of the preview this file compares.
type previewWill struct {
	WillSend struct {
		System   *string         `json:"system"`
		Messages json.RawMessage `json:"messages"`
		Tools    []struct {
			Name string `json:"name"`
		} `json:"tools"`
		ToolsSource string `json:"tools_source"`
	} `json:"will_send"`
}

func preview(t *testing.T, url string, body previewBody) previewWill {
	t.Helper()
	code, out := postStudio(t, url, "/api/playground/preview", body)
	if code != http.StatusOK {
		t.Fatalf("preview = %d %s", code, out)
	}
	var p previewWill
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPreviewMatchesTheReplayInput: Studio's preview is pure assembly,
// the runtime's replay is the run itself — the two must agree on the
// first request byte for byte. One command with every new edit kind
// (user, tool_args, insert) beside a result patch is previewed over
// POST /api/playground/preview and run through validate + execute: the
// preview's will_send.messages is the JSON of what the replay's model
// received, over a plain prefix and over a compacted one (ADR 0029
// decision 1's view). With the runtime registered, the system (an
// instructions override) and the tool names agree too.
func TestPreviewMatchesTheReplayInput(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	source := editOrdersAgent(p, wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"order_id":"43"}`, ID: "c2"}),
		wefttest.Say("both shipped"),
	))
	if _, err := source.Generate(ctx, core.RunID("r_pm"), core.Prompt("where are orders 42 and 43?")); err != nil {
		t.Fatal(err)
	}
	tail := &tailModel{}
	compactor := compactingAgent(p, tail)
	if _, err := compactor.Generate(ctx, core.RunID("r_pc"), core.Prompt("look everything up")); err != nil {
		t.Fatal(err)
	}
	tail.take()
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	srv := studio.New(studio.DB(db), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("plain", func(t *testing.T) {
		model := wefttest.Script(wefttest.Say("done"))
		agent := editOrdersAgent(p, model)
		cfg := &config{agents: []*core.Agent{agent}}
		l := newLink(cfg, newRegistry(cfg), ts.URL, "")
		l.localDB = func() obsdb.DB { return db }
		defer l.stop()
		if err := l.register(ctx); err != nil {
			t.Fatal(err)
		}
		body := previewBody{Runtime: l.id, Agent: "orders", Source: &sourceSpec{RunID: "r_pm", FromStep: 2},
			Overrides: overrides{Instructions: "Be brief."},
			TranscriptEdits: []transcriptEdit{
				{Kind: "user", Step: 0, Content: "where are orders 7 and 43?"},
				{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`{ "order_id": "7" }`)},
				{Step: 1, CallID: "c2", ToolResult: "order 43 lost"},
				{Kind: "insert", Step: 1, Content: "also check 43"},
				{Kind: "insert", Step: 2, Content: "and refund the lost one"},
			}}
		pv := preview(t, ts.URL, body)
		cmd := command{CommandID: "cmd_pm", Agent: "orders", Engine: "live", SideEffects: "substitute",
			Source: body.Source, Overrides: body.Overrides, TranscriptEdits: body.TranscriptEdits}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Fatalf("validate: %s", reason)
		}
		if status, _, errText := l.execute(ctx, cmd, "pg_pm"); status != "succeeded" {
			t.Fatalf("execute = %s %s", status, errText)
		}
		req := model.Requests()[0]
		fed, _ := json.Marshal(req.Messages)
		if string(pv.WillSend.Messages) != string(fed) {
			t.Errorf("preview will_send.messages\n%s\nthe replay's model received\n%s", pv.WillSend.Messages, fed)
		}
		if pv.WillSend.System == nil || *pv.WillSend.System != req.System {
			t.Errorf("preview system %v, the replay's %q", pv.WillSend.System, req.System)
		}
		var names []string
		for _, tl := range pv.WillSend.Tools {
			names = append(names, tl.Name)
		}
		if got, want := strings.Join(names, ","), strings.Join(toolNames(req.Tools), ","); got != want || pv.WillSend.ToolsSource != "agent" {
			t.Errorf("preview tools %s (%s), the replay's %s", got, pv.WillSend.ToolsSource, want)
		}
	})

	t.Run("compacted", func(t *testing.T) {
		cfg := &config{agents: []*core.Agent{compactor}}
		l := newLink(cfg, newRegistry(cfg), ts.URL, "")
		l.localDB = func() obsdb.DB { return db }
		defer l.stop()
		body := previewBody{Agent: "compactor", Source: &sourceSpec{RunID: "r_pc", FromStep: 3},
			TranscriptEdits: []transcriptEdit{
				{Kind: "user", Step: 0, Content: "look it all up"},
				{Kind: "tool_args", Step: 1, CallID: "c2", Args: json.RawMessage(`{"id":"c2x"}`)},
				{Step: 2, CallID: "c3", ToolResult: "record c3, patched"},
				{Kind: "insert", Step: 1, Content: "keep going"},
				{Kind: "insert", Step: 3, Content: "last one"},
			}}
		pv := preview(t, ts.URL, body)
		cmd := command{CommandID: "cmd_pc", Agent: "compactor", Engine: "live", SideEffects: "substitute",
			Source: body.Source, TranscriptEdits: body.TranscriptEdits}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Fatalf("validate: %s", reason)
		}
		if status, _, errText := l.execute(ctx, cmd, "pg_pc"); status != "succeeded" {
			t.Fatalf("execute = %s %s", status, errText)
		}
		fed := tail.take()
		if len(fed) == 0 || string(pv.WillSend.Messages) != fed[0] {
			t.Errorf("preview will_send.messages\n%s\nthe replay's model received\n%v", pv.WillSend.Messages, fed)
		}
		if !strings.Contains(fed[0], "summary: record c1") || !strings.Contains(fed[0], `"keep going"`) {
			t.Errorf("the replay's input is not the edited compacted prefix: %s", fed[0])
		}
	})
}

// TestOneSentenceOnBothSides: a body that breaks two rules gets the
// same refusal from Studio (the run route, 400) and from the runtime
// (validate, before the ack) — the checks run in one order on both
// sides: the edits first (before the engine, the thread, the side
// effects and the overrides), then the scripted engine's refusals
// (worded alike, §5.5 suffix included), then the input.
func TestOneSentenceOnBothSides(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	source := editOrdersAgent(p, wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"order_id":"43"}`, ID: "c2"}),
		wefttest.Say("both shipped"),
	))
	if _, err := source.Generate(ctx, core.RunID("r_os"), core.Prompt("where are orders 42 and 43?")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	ts := httptest.NewServer(studio.New(studio.DB(db), studio.Playground(true)).Handler())
	defer ts.Close()
	cfg := &config{agents: []*core.Agent{source}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	l.localDB = func() obsdb.DB { return db }
	defer l.stop()
	in := "where is order 9?"
	badEdit := []transcriptEdit{{Kind: "insert", Step: 5, Content: "x"}}
	const badInsert = "insert step 5 is past from_step 2: an insert lands at a step boundary 0..2"
	for _, c := range []struct {
		name string
		body previewBody
		want string
	}{
		{"scripted, from_step 0, an edit", previewBody{Engine: "scripted", Source: &sourceSpec{RunID: "r_os", FromStep: 0},
			TranscriptEdits: []transcriptEdit{{Kind: "user", Step: 0, Content: "x"}}},
			"transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)"},
		{"input, from_step 2, a bad edit", previewBody{Input: &in, Source: &sourceSpec{RunID: "r_os", FromStep: 2},
			TranscriptEdits: []transcriptEdit{{Kind: "insert", Step: 5, Content: "x"}}},
			"insert step 5 is past from_step 2: an insert lands at a step boundary 0..2"},
		{"scripted, from_step 2, an edit", previewBody{Engine: "scripted", Source: &sourceSpec{RunID: "r_os", FromStep: 2},
			TranscriptEdits: []transcriptEdit{{Kind: "insert", Step: 2, Content: "x"}}},
			"the scripted engine would replay the recorded turn 2, which answered a different prompt: transcript edits need engine live"},
		{"fork, a bad edit", previewBody{Thread: "fork", Input: &in,
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"from_step -1, an edit", previewBody{Source: &sourceSpec{RunID: "r_os", FromStep: -1},
			TranscriptEdits: []transcriptEdit{{Kind: "user", Step: 0, Content: "x"}}},
			"transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)"},
		{"scripted, an instructions override, a bad edit", previewBody{Engine: "scripted", Overrides: overrides{Instructions: "x"},
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"scripted, a model override, a bad edit", previewBody{Engine: "scripted", Overrides: overrides{Model: "m"},
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"an unknown thinking level, a bad edit", previewBody{Overrides: overrides{Thinking: "max"},
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"an unknown thread mode, a bad edit", previewBody{Thread: "branch",
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"an unknown side_effects mode, a bad edit", previewBody{SideEffects: "maybe",
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"an unknown tool, a bad edit", previewBody{Overrides: overrides{ToolsEnabled: []string{"nope"}},
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: badEdit}, badInsert},
		{"scripted, a valid edit, an unknown thread mode", previewBody{Engine: "scripted", Thread: "branch",
			Source: &sourceSpec{RunID: "r_os", FromStep: 2}, TranscriptEdits: []transcriptEdit{{Kind: "insert", Step: 2, Content: "x"}}},
			"the scripted engine would replay the recorded turn 2, which answered a different prompt: transcript edits need engine live"},
		{"scripted, an instructions override, no edit", previewBody{Engine: "scripted", Overrides: overrides{Instructions: "x"},
			Source: &sourceSpec{RunID: "r_os", FromStep: 1}},
			"scripted engine with an instructions override would silently replay the old answer (WEFT-PLAYGROUND §5.5)"},
		{"scripted, a model override, no edit", previewBody{Engine: "scripted", Overrides: overrides{Model: "m"},
			Source: &sourceSpec{RunID: "r_os", FromStep: 1}},
			"scripted engine with a model override would silently replay the old answer (WEFT-PLAYGROUND §5.5)"},
	} {
		c.body.Agent, c.body.Runtime = "orders", l.id
		if code, msg := postStudio(t, ts.URL, "/api/playground/runs", c.body); code != http.StatusBadRequest || msg != c.want {
			t.Errorf("%s: Studio = %d %q, want 400 %q", c.name, code, msg, c.want)
		}
		cmd := command{CommandID: "cmd_os", Agent: "orders", Engine: c.body.Engine, Source: c.body.Source,
			Input: c.body.Input, TranscriptEdits: c.body.TranscriptEdits, Overrides: c.body.Overrides,
			Thread: c.body.Thread, SideEffects: c.body.SideEffects}
		if reason, ok := l.validate(ctx, &cmd); ok || reason != c.want {
			t.Errorf("%s: runtime = %v %q, want %q", c.name, ok, reason, c.want)
		}
	}

	// The option lab's park rule, one sentence on both sides: a named
	// tool_choice park_on parks — the command's own, or the agent's
	// default under a command that sends none — makes every forced call
	// park. Studio's side is the preview's registration check (the run
	// route's checkRegistered), the runtime's validate.
	lookup := core.Tool("lookup", "Look up an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "order " + in.OrderID, nil
	})
	forced := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("forced"), lookup,
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "lookup"}))
	fcfg := &config{agents: []*core.Agent{forced}}
	fl := newLink(fcfg, newRegistry(fcfg), ts.URL, "")
	fl.localDB = func() obsdb.DB { return db }
	defer fl.stop()
	if err := fl.register(ctx); err != nil {
		t.Fatal(err)
	}
	const parked = "tool_choice names lookup, which park_on parks: every forced call would park"
	for name, o := range map[string]overrides{
		"the agent's default": {ParkOn: []string{"lookup"}},
		"the command's own":   {ParkOn: []string{"lookup"}, ToolChoice: &toolChoiceWire{Mode: "named", Name: "lookup"}},
	} {
		body := previewBody{Runtime: fl.id, Agent: "forced", Source: &sourceSpec{RunID: "r_os", FromStep: 1}, Overrides: o}
		if code, msg := postStudio(t, ts.URL, "/api/playground/preview", body); code != http.StatusBadRequest || msg != parked {
			t.Errorf("%s parked: Studio = %d %q, want 400 %q", name, code, msg, parked)
		}
		cmd := command{CommandID: "cmd_park", Agent: "forced", Engine: "live", Source: body.Source, Overrides: o}
		if reason, ok := fl.validate(ctx, &cmd); ok || reason != parked {
			t.Errorf("%s parked: runtime = %v %q, want %q", name, ok, reason, parked)
		}
	}
}

// TestSubstituteKeysOnTheEditedPair (ADR 0029 §8): the substitute
// lookup answers a kept call from what the model saw answered — the
// edited pair. A never-class call re-issued with its edited arguments
// is answered with the kept result (the handler never runs); one
// re-issued with the original arguments no longer matches and parks.
func TestSubstituteKeysOnTheEditedPair(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	var ran atomic.Int32
	never := func(model core.Model) *core.Agent {
		lookup := core.Tool("lookup", "Look up an order.", func(_ context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			ran.Add(1)
			return "order " + in.OrderID + " shipped", nil
		}) // unannotated: never
		return core.New(model, core.Name("orders"),
			core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup)
	}
	if _, err := never(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.Say("shipped"),
	)).Generate(ctx, core.RunID("r_sub"), core.Prompt("where is order 42?")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	ran.Store(0)
	argsEdit := transcriptEdit{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`{"order_id":"7"}`)}
	for _, c := range []struct {
		name, args string
		edit       transcriptEdit
		parked     bool
		answer     string
	}{
		{"edited args re-issued", `{"order_id":"7"}`, argsEdit, false, "order 42 shipped"},
		{"original args re-issued", `{"order_id":"42"}`, argsEdit, true, ""},
		// A patched result answers its call's re-issue (the fresh steps
		// never recorded that key: their records would queue first).
		{"patched result re-issued", `{"order_id":"42"}`, transcriptEdit{Step: 0, CallID: "c1", ToolResult: "lost"}, false, "lost"},
	} {
		t.Run(c.name, func(t *testing.T) {
			model := wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: c.args, ID: "r1"}),
				wefttest.Say("ok"),
			)
			cfg := &config{agents: []*core.Agent{never(model)}}
			l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
			l.localDB = func() obsdb.DB { return db }
			defer l.stop()
			cmd := command{CommandID: "cmd_sub", Agent: "orders", Engine: "live", SideEffects: "substitute",
				Source:          &sourceSpec{RunID: "r_sub", FromStep: 1},
				TranscriptEdits: []transcriptEdit{c.edit}}
			if reason, ok := l.validate(ctx, &cmd); !ok {
				t.Fatalf("validate: %s", reason)
			}
			runID := "pg_sub_" + strings.ReplaceAll(c.name, " ", "_")
			if status, final, errText := l.execute(ctx, cmd, runID); status != "succeeded" {
				t.Fatalf("execute = %s %s", status, errText)
			} else {
				l.mu.Lock()
				_, parked := l.parked[final]
				l.mu.Unlock()
				if parked != c.parked {
					t.Errorf("parked = %v, want %v", parked, c.parked)
				}
			}
			if n := ran.Load(); n != 0 {
				t.Errorf("the never-class handler ran %d times", n)
			}
			if !c.parked {
				reqs := model.Requests()
				last, _ := json.Marshal(reqs[len(reqs)-1].Messages)
				if !strings.Contains(string(last), `"call_id":"r1","name":"lookup","content":"`+c.answer+`"`) {
					t.Errorf("the re-issued call was not answered with the kept result: %s", last)
				}
			}
		})
	}
}

// wideModel calls lookup c<k+1> while the request holds k < 3 tool
// messages, then answers "done"; it keeps every request's messages.
type wideModel struct {
	mu   sync.Mutex
	seen []string
}

func (*wideModel) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: "wide"} }

func (m *wideModel) Stream(_ context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	b, _ := json.Marshal(req.Messages)
	m.mu.Lock()
	m.seen = append(m.seen, string(b))
	m.mu.Unlock()
	k := 0
	for _, msg := range req.Messages {
		if msg.Role == core.RoleTool {
			k++
		}
	}
	return func(yield func(core.ModelEvent, error) bool) {
		if k < 3 {
			id := fmt.Sprintf("c%d", k+1)
			if yield(core.ModelToolCall{ID: id, Name: "lookup", Args: []byte(`{"id":"` + id + `"}`)}, nil) {
				yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
			}
			return
		}
		if yield(core.ModelTextDelta{Text: "done"}, nil) {
			yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
		}
	}
}

// TestEditsInsideAWideView: a source whose step 0 was steered and
// whose step-3 request compacted [1, 6) — step 0's call and result, the
// steer, step 1's call and result. From step 3, an insert at boundary
// 1 (seq 4, strictly inside) and a user edit of the steer (step 0's
// user message 1, seq 3) are refused on both sides in one wording; the
// turn's prompt (seq 0) is editable. From step 2 (no view) the steer is
// step 0's user message 1 and a user edit of it applies, on both sides.
func TestEditsInsideAWideView(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	lookup := core.Tool("lookup", "Look up a record.", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return "record " + in.ID, nil
	}, core.Replay(core.ReplaySafe))
	agent := core.New(&wideModel{}, core.Name("wide"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup,
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step < 3 || len(req.Messages) < 7 {
				return req, nil
			}
			m := req.Messages
			req.Messages = append([]core.Message{m[0], core.User("summary: c1 and c2 were looked up")}, m[6:]...)
			return req, nil
		}))
	if _, err := agent.Generate(ctx, core.RunID("r_wide"), core.Prompt("look up c1 to c3"),
		wefttest.NewSteers().At(0, core.User("and c2 too")).Option()); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	if _, view := stepMessagesJSON(t, db, "r_wide", 3); view == nil || view.FromSeq != 1 || view.ToSeq != 6 {
		t.Fatalf("step 3's view = %+v, want [1, 6)", view)
	}
	ts := httptest.NewServer(studio.New(studio.DB(db), studio.Playground(true)).Handler())
	defer ts.Close()
	cfg := &config{agents: []*core.Agent{agent}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	l.localDB = func() obsdb.DB { return db }
	defer l.stop()

	for _, c := range []struct {
		name  string
		from  int
		edits []transcriptEdit
		want  string // "" accepted
	}{
		{"insert strictly inside the view", 3, []transcriptEdit{{Kind: "insert", Step: 1, Content: "x"}},
			"the boundary before step 1 was compacted away before step 3's request (messages [1, 6) replaced by 1): the model never saw it there; insert outside the range"},
		{"user edit of the steer inside the view", 3, []transcriptEdit{{Kind: "user", Step: 0, Index: 1, Content: "x"}},
			"the user message of step 0 was compacted away before step 3's request (messages [1, 6) replaced by 1): the model never saw it there; edit from an earlier from_step"},
		{"user edit of the prompt outside the view", 3, []transcriptEdit{{Kind: "user", Step: 0, Content: "look up c1 to c3, fast"}}, ""},
		{"user edit of the steer, no view", 2, []transcriptEdit{{Kind: "user", Step: 0, Index: 1, Content: "and c2, please"}}, ""},
	} {
		body := previewBody{Runtime: l.id, Agent: "wide", Source: &sourceSpec{RunID: "r_wide", FromStep: c.from}, TranscriptEdits: c.edits}
		code, msg := postStudio(t, ts.URL, "/api/playground/preview", body)
		cmd := command{CommandID: "cmd_w", Agent: "wide", Engine: "live", Source: body.Source, TranscriptEdits: c.edits}
		reason, ok := l.validate(ctx, &cmd)
		if c.want != "" {
			if code != http.StatusBadRequest || msg != c.want {
				t.Errorf("%s: Studio = %d %q, want 400 %q", c.name, code, msg, c.want)
			}
			if ok || reason != c.want {
				t.Errorf("%s: runtime = %v %q, want %q", c.name, ok, reason, c.want)
			}
			continue
		}
		if code != http.StatusOK || !ok {
			t.Fatalf("%s: Studio %d %s, runtime %v %s", c.name, code, msg, ok, reason)
		}
		var pv previewWill
		if err := json.Unmarshal([]byte(msg), &pv); err != nil {
			t.Fatal(err)
		}
		prefix, _ := json.Marshal(cmd.prefix)
		if string(pv.WillSend.Messages) != string(prefix) || !strings.Contains(string(prefix), c.edits[0].Content) {
			t.Errorf("%s: preview\n%s\nruntime prefix\n%s", c.name, pv.WillSend.Messages, prefix)
		}
		if c.from == 2 {
			var msgs []core.Message
			_ = json.Unmarshal(prefix, &msgs)
			if len(msgs) < 4 || msgs[3].Role != core.RoleUser || !slices.ContainsFunc(msgs[3].Content, func(p core.Part) bool {
				tp, ok := p.(core.TextPart)
				return ok && tp.Text == "and c2, please"
			}) {
				t.Errorf("the steer was not rewritten in place: %s", prefix)
			}
		}
	}
}
