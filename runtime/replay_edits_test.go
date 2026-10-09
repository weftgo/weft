package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

// editOrdersAgent is the source and replay agent of the edit fixtures:
// one lookup tool (vouched safe, so a substitute never parks it) over
// the given model.
func editOrdersAgent(p *otel.Pipeline, model core.Model) *core.Agent {
	lookup := core.Tool("lookup", "Look up an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "order " + in.OrderID + " shipped", nil
	}, core.Replay(core.ReplaySafe))
	return core.New(model, core.Name("orders"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup)
}

// TestReplayWithEditedRequest is plan F2's Done line on the runtime
// (ADR 0029 §8): one command that rewrites the turn's user message,
// a call's arguments and another call's result, and inserts user
// messages at two step boundaries, feeds the replay's first model call
// exactly the edited prefix — the rewritten prompt, the call with its
// new arguments beside its kept result, the inserts where a steer
// delivered at those boundaries would land — over both of the
// runtime's record paths (the local obsdb and Studio's transcript
// route). The replayed run carries weft.edits, the "args edited" mark
// on the pair. A schema-invalid args edit is refused before the ack
// with the field named; the scripted engine refuses edits in one
// sentence; input beside from_step > 0 points at the user edit.
func TestReplayWithEditedRequest(t *testing.T) {
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
	res, err := source.Generate(ctx, core.RunID("r_edit"), core.Prompt("where are orders 42 and 43?"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	ts := httptest.NewServer(studio.New(studio.DB(db)).Handler())
	defer ts.Close()

	// What the replay's first model call must see: the source's prefix
	// through step 1, edited by hand.
	m := res.Messages // user, a(c1), t(c1), a(c2), t(c2), a(done)
	edited := func() []core.Message {
		out := []core.Message{core.User("where are orders 7 and 43?")}
		call := m[1].Content[0].(core.ToolCallPart)
		call.Args = json.RawMessage(`{"order_id":"7"}`)
		out = append(out, core.Message{Role: core.RoleAssistant, Content: []core.Part{call}})
		out = append(out, m[2]) // c1's result stays: only its arguments were edited
		out = append(out, core.User("also check 43"))
		out = append(out, m[3])
		r := m[4].Content[0].(core.ToolResultPart)
		r.Content, r.IsError = "order 43 lost", false
		out = append(out, core.Message{Role: core.RoleTool, Content: []core.Part{r}})
		return append(out, core.User("and refund the lost one"))
	}()
	want, _ := json.Marshal(edited)
	edits := []transcriptEdit{
		{Kind: "user", Step: 0, Content: "where are orders 7 and 43?"},
		{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`{ "order_id": "7" }`)},
		{Step: 1, CallID: "c2", ToolResult: "order 43 lost"}, // no kind: the pre-F2 shape
		{Kind: "insert", Step: 2, Content: "and refund the lost one"},
		{Kind: "insert", Step: 1, Content: "also check 43"},
	}

	for name, local := range map[string]func() obsdb.DB{
		"local":  func() obsdb.DB { return db },
		"studio": func() obsdb.DB { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			replayModel := wefttest.Script(wefttest.Say("order 43 refunded"))
			agent := editOrdersAgent(p, replayModel)
			cfg := &config{agents: []*core.Agent{agent}}
			l := newLink(cfg, newRegistry(cfg), ts.URL, "")
			l.localDB = local
			defer l.stop()

			cmd := command{CommandID: "cmd_" + name, Agent: "orders", Engine: "live", SideEffects: "substitute",
				Source: &sourceSpec{RunID: "r_edit", FromStep: 2}, TranscriptEdits: edits}
			if reason, ok := l.validate(ctx, &cmd); !ok {
				t.Fatalf("validate: %s", reason)
			}
			runID := "pg_edit_" + name
			if status, _, errText := l.execute(ctx, cmd, runID); status != "succeeded" {
				t.Fatalf("execute = %s %s", status, errText)
			}
			reqs := replayModel.Requests()
			if len(reqs) != 1 {
				t.Fatalf("the replay made %d model calls, want 1", len(reqs))
			}
			if got, _ := json.Marshal(reqs[0].Messages); string(got) != string(want) {
				t.Errorf("the replay's model saw\n%s\nwant the edited prefix\n%s", got, want)
			}
			if err := p.ForceFlush(ctx); err != nil {
				t.Fatal(err)
			}
			row, err := db.Run(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			if got := row.Meta["weft.edits"]; got != "0:user,0:c1:args,1:c2:result,2:insert,1:insert" {
				t.Errorf("weft.edits = %q, want every edit, c1 marked args", got)
			}

			// A client that serialises "args": null on every edit sends
			// no args: a user edit with it is accepted.
			nullArgs := command{CommandID: "cmd_null", Agent: "orders", Engine: "live",
				Source: &sourceSpec{RunID: "r_edit", FromStep: 2},
				TranscriptEdits: []transcriptEdit{{Kind: "user", Step: 0, Content: "x", Args: json.RawMessage(`null`)},
					{Kind: "reply", Step: 1, Content: "y", Args: json.RawMessage(` `)}}}
			if reason, ok := l.validate(ctx, &nullArgs); ok || reason != "step 1 has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)" {
				t.Errorf("null args = %v %q, want them read as absent (and the explicit reply kind checked as a reply)", ok, reason)
			}
			for _, c := range []struct {
				name   string
				mutate func(*command)
				want   string
			}{
				{"schema-invalid args", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`{"order_id":7}`)}}
				}, `INVALID_INPUT: tool "lookup": field "order_id": expected string, got number`},
				{"args not an object", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`["7"]`)}}
				}, `INVALID_INPUT: tool "lookup": expected object at the top level, got array`},
				{"args of an unknown call", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "tool_args", Step: 1, CallID: "c1", Args: json.RawMessage(`{}`)}}
				}, `no tool call "c1" in the kept prefix's step 1`},
				{"user edit of a step without one", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "user", Step: 1, Content: "x"}}
				}, "step 1 has no user message in the kept prefix"},
				{"user index out of range", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "user", Step: 0, Index: 1, Content: "x"}}
				}, "step 0 has 1 user message(s) in the kept prefix: index 1 is out of range"},
				{"insert past from_step", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "insert", Step: 3, Content: "x"}}
				}, "insert step 3 is past from_step 2: an insert lands at a step boundary 0..2"},
				{"user edit of a re-run step", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "user", Step: 2, Content: "x"}}
				}, "edit step 2 is not in the kept prefix (from_step 2 keeps steps 0..1)"},
				{"unknown kind", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "system", Step: 0, Content: "x"}}
				}, `unknown edit kind "system" (tool_result, reply, user, tool_args or insert)`},
				{"a field the kind does not take", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "user", Step: 0, Content: "x", CallID: "c1"}}
				}, "a user edit does not take call_id"},
				{"args null on a tool_args edit", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`null`)}}
				}, "a tool_args edit needs args"},
				{"args without kind", func(c *command) {
					c.TranscriptEdits = []transcriptEdit{{Step: 0, CallID: "c1", Args: json.RawMessage(`{}`)}}
				}, `an edit with args needs kind "tool_args"`},
				{"scripted with edits", func(c *command) { c.Engine = "scripted" },
					"the scripted engine would replay the recorded turn 2, which answered a different prompt: transcript edits need engine live"},
				{"input beside from_step", func(c *command) {
					in := "where is order 7?"
					c.Input, c.TranscriptEdits = &in, nil
				}, `input replaces the turn's user message only when from_step is 0: with from_step > 0, edit step 0's user message instead (a transcript edit of kind "user")`},
			} {
				bad := command{CommandID: "cmd_bad", Agent: "orders", Engine: "live",
					Source: &sourceSpec{RunID: "r_edit", FromStep: 2}, TranscriptEdits: edits}
				c.mutate(&bad)
				if reason, ok := l.validate(ctx, &bad); ok || reason != c.want {
					t.Errorf("%s: validate = %v %q\nwant refused: %q", c.name, ok, reason, c.want)
				}
			}
		})
	}
}

// TestReplayEditsOverACompactedPrefix: the F2 kinds over ADR 0029's
// compacted prefix. From step 3 the view replaced step 0's call and
// result ([1, 3)): an args edit of c1 is refused in F1.1's words; the
// turn's prompt (seq 0) lies before the range and a user edit of it
// applies; an insert at boundary 1 (seq 3, the range's end) lands
// after the view's summary. Over the local and the Studio path.
func TestReplayEditsOverACompactedPrefix(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	model := &tailModel{}
	agent := compactingAgent(p, model)
	if _, err := agent.Generate(ctx, core.RunID("r_cmp"), core.Prompt("look everything up")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	source := model.take()
	ts := httptest.NewServer(studio.New(studio.DB(db)).Handler())
	defer ts.Close()

	for name, local := range map[string]func() obsdb.DB{
		"local":  func() obsdb.DB { return db },
		"studio": func() obsdb.DB { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &config{agents: []*core.Agent{agent}}
			l := newLink(cfg, newRegistry(cfg), ts.URL, "")
			l.localDB = local
			defer l.stop()
			cmd := command{CommandID: "cmd_c", Agent: "compactor", Engine: "live",
				Source:          &sourceSpec{RunID: "r_cmp", FromStep: 3},
				TranscriptEdits: []transcriptEdit{{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`{"id":"c9"}`)}}}
			const want = `call "c1" of step 0 was compacted away before step 3's request (messages [1, 3) replaced by 1): the model never saw it there; edit from an earlier from_step`
			if reason, ok := l.validate(ctx, &cmd); ok || reason != want {
				t.Errorf("args edit inside the view = %v %q\nwant refused: %q", ok, reason, want)
			}
			cmd = command{CommandID: "cmd_c2", Agent: "compactor", Engine: "live",
				Source: &sourceSpec{RunID: "r_cmp", FromStep: 3},
				TranscriptEdits: []transcriptEdit{
					{Kind: "user", Step: 0, Content: "look it all up"},
					{Kind: "insert", Step: 1, Content: "keep going"},
				}}
			if reason, ok := l.validate(ctx, &cmd); !ok {
				t.Fatalf("edits outside the view refused: %s", reason)
			}
			var saw []core.Message
			if err := json.Unmarshal([]byte(source[3]), &saw); err != nil {
				t.Fatal(err)
			}
			// step 3's request: [prompt, summary, a(c2), t(c2), a(c3), t(c3)]
			wantMsgs := append([]core.Message{core.User("look it all up"), saw[1], core.User("keep going")}, saw[2:]...)
			got, _ := json.Marshal(cmd.prefix)
			if w, _ := json.Marshal(wantMsgs); string(got) != string(w) {
				t.Errorf("the edited compacted prefix\n%s\nwant\n%s", got, w)
			}
		})
	}
}

// TestEditsMark pins weft.edits' spelling and its cap: past core's
// 1024-byte metadata value the list ends in "+<n> more", never cut
// silently mid-token.
func TestEditsMark(t *testing.T) {
	got := editsMark([]transcriptEdit{
		{Kind: "user", Step: 0, Content: "x"}, {Kind: "user", Step: 1, Index: 2, Content: "y"},
		{Kind: "tool_args", Step: 0, CallID: "c1", Args: json.RawMessage(`{}`)},
		{Step: 1, CallID: "c2", ToolResult: "r"}, {Step: 2, Content: "reply"}, {Kind: "insert", Step: 3, Content: "z"},
	})
	if got != "0:user,1:user:2,0:c1:args,1:c2:result,2:reply,3:insert" {
		t.Errorf("editsMark = %q", got)
	}
	var many []transcriptEdit
	for i := range 200 {
		many = append(many, transcriptEdit{Kind: "tool_args", Step: i, CallID: fmt.Sprintf("call_%03d", i), Args: json.RawMessage(`{}`)})
	}
	long := editsMark(many)
	if len(long) > 1024 || !strings.HasSuffix(long, " more") || strings.Count(long, ",")+0 < 10 {
		t.Errorf("capped mark = %d bytes %q", len(long), long)
	}
	kept := strings.Count(long, ":args")
	if !strings.HasSuffix(long, fmt.Sprintf(",+%d more", 200-kept)) {
		t.Errorf("capped mark %q does not count the %d dropped edits", long[len(long)-20:], 200-kept)
	}
}
