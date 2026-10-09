package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
)

// tailModel answers by the request's last message, so the source run
// and every replay of it are answered alike (a wefttest.Script queue is
// shared across runs): a user message calls lookup c1, a result of call
// c<n> calls c<n+1> up to c3, then the model says "done". It keeps the
// exact messages of every request it saw, as JSON — what the model saw.
type tailModel struct {
	mu   sync.Mutex
	seen []string
}

func (*tailModel) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: "tail"} }

func (m *tailModel) Stream(_ context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	b, _ := json.Marshal(req.Messages)
	m.mu.Lock()
	m.seen = append(m.seen, string(b))
	m.mu.Unlock()
	last := req.Messages[len(req.Messages)-1]
	next := ""
	switch last.Role {
	case core.RoleUser:
		next = "c1"
	case core.RoleTool:
		for _, p := range last.Content {
			if tr, ok := p.(core.ToolResultPart); ok && tr.CallID != "c3" {
				next = fmt.Sprintf("c%d", int(tr.CallID[1]-'0')+1)
			}
		}
	}
	return func(yield func(core.ModelEvent, error) bool) {
		if next != "" {
			if !yield(core.ModelToolCall{ID: next, Name: "lookup", Args: []byte(`{"id":"` + next + `"}`)}, nil) {
				return
			}
			yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
			return
		}
		if yield(core.ModelTextDelta{Text: "done"}, nil) {
			yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
		}
	}
}

// requests returns the requests seen since the last call, resetting.
func (m *tailModel) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.seen
	m.seen = nil
	return out
}

// compactingAgent is the source agent: four steps (lookup c1, c2, c3,
// then "done"), and a PrepareStep that — from step 2 on — sends the
// model a summary in place of step 0's call and result. Each such
// request is a run-scope compaction view (ADR 0028 §8) replacing
// transcript seqs [1, 3); step 1's request carries the plain
// transcript.
func compactingAgent(p *otel.Pipeline, model core.Model) *core.Agent {
	lookup := core.Tool("lookup", "Look up a record.", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return "record " + in.ID, nil
	}, core.Replay(core.ReplaySafe))
	return core.New(model, core.Name("compactor"),
		core.Instructions("You look things up."),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step < 2 || len(req.Messages) < 3 {
				return req, nil
			}
			m := req.Messages
			out := append([]core.Message{m[0], core.User("summary: record c1 was looked up")}, m[3:]...)
			req.Messages = out
			return req, nil
		}),
		lookup)
}

// stepMessagesJSON is obsdb's assembly of one step's request, as JSON.
func stepMessagesJSON(t *testing.T, db obsdb.DB, runID string, step int) (string, *obsdb.Compaction) {
	t.Helper()
	sm, err := obsdb.MessagesAsOf(context.Background(), db, runID, step)
	if err != nil {
		t.Fatalf("MessagesAsOf(%s, %d): %v", runID, step, err)
	}
	b, err := json.Marshal(sm.Messages)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), sm.View
}

// TestReplayAcrossCompactionReproducesTheModelsInput is plan A9's
// replay clause and the phase 4 gate's "a replay across a compaction
// boundary reproduces the model's exact input" (ADR 0029): a run whose
// PrepareStep compacts steps 2 and 3 is recorded through the real otel
// pipeline into a local obsdb; replaying it from step 3 (and 2) feeds
// the replay's step 0 exactly the messages the source's step-3 (step-2)
// request carried — the compacted prefix — and from step 1 the
// original prefix (that request carried no view). Checked three ways,
// each against the others: the messages the model itself received, the
// request records' assembly (obsdb.MessagesAsOf — the golden against
// the request record), and over both of the runtime's record paths —
// the local obsdb and Studio's transcript route with ?step=.
func TestReplayAcrossCompactionReproducesTheModelsInput(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	model := &tailModel{}
	agent := compactingAgent(p, model)
	if _, err := agent.Generate(ctx, core.RunID("r_src"), core.Prompt("look everything up")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	source := model.take() // the source's four requests, as the model saw them
	if len(source) != 4 {
		t.Fatalf("the source made %d model calls, want 4", len(source))
	}
	for step, want := range map[int]bool{1: false, 2: true, 3: true} {
		got, view := stepMessagesJSON(t, db, "r_src", step)
		if got != source[step] {
			t.Fatalf("step %d: the request record's assembly differs from what the model saw:\nrecord: %s\nmodel:  %s", step, got, source[step])
		}
		if (view != nil) != want || (view != nil && (view.FromSeq != 1 || view.ToSeq != 3 || view.Entries != 1)) {
			t.Fatalf("step %d: view = %+v, want compacted %v over [1, 3)", step, view, want)
		}
	}
	if !strings.Contains(source[3], "summary: record c1") || strings.Contains(source[3], `"call_id":"c1"`) {
		t.Fatalf("step 3's request is not the compacted one: %s", source[3])
	}

	srv := studio.New(studio.DB(db))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	paths := map[string]func() obsdb.DB{
		"local":  func() obsdb.DB { return db },
		"studio": func() obsdb.DB { return nil },
	}
	for name, local := range paths {
		for _, from := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/from_step_%d", name, from), func(t *testing.T) {
				cfg := &config{agents: []*core.Agent{agent}}
				l := newLink(cfg, newRegistry(cfg), ts.URL, "")
				l.localDB = local
				defer l.stop()
				cmd := command{CommandID: "cmd_" + name, Agent: "compactor", Engine: "live", SideEffects: "substitute",
					Source: &sourceSpec{RunID: "r_src", FromStep: from}}
				if reason, ok := l.validate(ctx, &cmd); !ok {
					t.Fatalf("validate: %s", reason)
				}
				runID := fmt.Sprintf("pg_%s_%d", name, from)
				if status, _, errText := l.execute(ctx, cmd, runID); status != "succeeded" {
					t.Fatalf("execute = %s %s", status, errText)
				}
				if err := p.ForceFlush(ctx); err != nil {
					t.Fatal(err)
				}
				fed := model.take()
				if len(fed) == 0 || fed[0] != source[from] {
					t.Fatalf("the replay's step 0 saw\n%v\nwant the source's step-%d request\n%s", fed, from, source[from])
				}
				got, view := stepMessagesJSON(t, db, runID, 0)
				want, _ := stepMessagesJSON(t, db, "r_src", from)
				if got != want {
					t.Errorf("replay step-0 request record\n%s\nwant source step-%d request record\n%s", got, from, want)
				}
				if view != nil {
					t.Errorf("the replay's step 0 carried a view %+v: it was fed the prefix, not compacted again", view)
				}
				row, err := db.Run(ctx, runID)
				if err != nil {
					t.Fatal(err)
				}
				if row.ForkedFrom != fmt.Sprintf("r_src#%d", from) || !row.Playground {
					t.Errorf("lineage = forked_from %q playground %v, want r_src#%d", row.ForkedFrom, row.Playground, from)
				}
			})
		}
	}

	// A thread-stored source: the thread path reads the turn's entries
	// (no request records there), and the view of from_step comes from
	// the records — the local sink when it holds the run, else Studio.
	for name, local := range paths {
		t.Run("thread-"+name+"/from_step_3", func(t *testing.T) {
			store := thread.Memory()
			s, err := thread.Create(ctx, store, agent)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close(ctx) }()
			turn, err := s.Send(ctx, core.User("look everything up"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := p.ForceFlush(ctx); err != nil {
				t.Fatal(err)
			}
			saw := model.take()
			cfg := &config{agents: []*core.Agent{agent}, threads: store}
			l := newLink(cfg, newRegistry(cfg), ts.URL, "")
			l.localDB = local
			defer l.stop()
			cmd := command{CommandID: "cmd_t", Agent: "compactor", Engine: "live",
				Source: &sourceSpec{RunID: turn.RunID(), FromStep: 3}}
			if reason, ok := l.validate(ctx, &cmd); !ok {
				t.Fatalf("validate: %s", reason)
			}
			if b, _ := json.Marshal(cmd.prefix); len(saw) != 4 || string(b) != saw[3] {
				t.Errorf("thread-sourced prefix\n%s\nwant step 3's request\n%v", b, saw)
			}
		})
	}

	// The scripted engine replays the recorded turns over the compacted
	// prefix: from step 3 the recorded "done" answers it, zero tokens.
	t.Run("scripted/from_step_3", func(t *testing.T) {
		cfg := &config{agents: []*core.Agent{agent}}
		l := newLink(cfg, newRegistry(cfg), ts.URL, "")
		l.localDB = func() obsdb.DB { return db }
		defer l.stop()
		cmd := command{CommandID: "cmd_scripted", Agent: "compactor", Engine: "scripted",
			Source: &sourceSpec{RunID: "r_src", FromStep: 3}}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Fatalf("validate: %s", reason)
		}
		if status, _, errText := l.execute(ctx, cmd, "pg_scripted"); status != "succeeded" {
			t.Fatalf("scripted replay across the compaction = %s %s", status, errText)
		}
		if n := len(model.take()); n != 0 {
			t.Errorf("the scripted engine reached the live model %d times", n)
		}
	})

	// Edits over the compacted prefix (ADR 0029): from step 3, step 0's
	// call c1 is inside the replaced range — the model never saw its
	// result there — so patching it is refused on both paths in one
	// wording (studio's TestPlaygroundEditInsideCompactionRefused pins
	// Studio's copy); step 1's c2 is outside the range and patches.
	for name, local := range paths {
		t.Run(name+"/edits", func(t *testing.T) {
			cfg := &config{agents: []*core.Agent{agent}}
			l := newLink(cfg, newRegistry(cfg), ts.URL, "")
			l.localDB = local
			defer l.stop()
			cmd := command{CommandID: "cmd_e", Agent: "compactor", Engine: "live",
				Source:          &sourceSpec{RunID: "r_src", FromStep: 3},
				TranscriptEdits: []transcriptEdit{{Step: 0, CallID: "c1", ToolResult: "x"}}}
			const want = `call "c1" of step 0 was compacted away before step 3's request (messages [1, 3) replaced by 1): the model never saw it there; edit from an earlier from_step`
			if reason, ok := l.validate(ctx, &cmd); ok || reason != want {
				t.Errorf("edit inside the view = %v %q\nwant refused: %q", ok, reason, want)
			}
			cmd = command{CommandID: "cmd_e2", Agent: "compactor", Engine: "live",
				Source:          &sourceSpec{RunID: "r_src", FromStep: 3},
				TranscriptEdits: []transcriptEdit{{Step: 1, CallID: "c2", ToolResult: "patched c2"}}}
			if reason, ok := l.validate(ctx, &cmd); !ok {
				t.Fatalf("edit outside the view refused: %s", reason)
			}
			b, _ := json.Marshal(cmd.prefix)
			if !strings.Contains(string(b), "patched c2") || !strings.Contains(string(b), "summary: record c1") || len(cmd.prefix) != 6 {
				t.Errorf("the edited compacted prefix = %s", b)
			}
		})
	}
}

// TestReplayChildRunAsItsOwnRun is plan A10's replay clause (ADR 0029):
// a source run that is a subagent's child ("<parent>/<step>/<call>")
// replays as its own run of the child's agent — registered on the
// runtime by that name — with weft.playground and
// weft.forked_from="<child id>#<from_step>", and no parent linkage
// (weft.parent.run.id / weft.parent.call.id): the parent is untouched.
// Replaying the parent from the step that called the child re-runs the
// Subagent call the way the loop would, under its replay class: an
// unannotated Subagent tool is never, so the call parks and substitute
// answers it with the recorded result — the child is not run again
// (side_effects park leaves it pending for a human instead).
func TestReplayChildRunAsItsOwnRun(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	prov := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	var childCalls atomic.Int64
	childModel := modelFunc(func(req core.ModelRequest) []core.ModelEvent {
		childCalls.Add(1)
		if last := req.Messages[len(req.Messages)-1]; last.Role == core.RoleUser {
			return []core.ModelEvent{core.ModelToolCall{ID: "n1", Name: "note", Args: []byte(`{}`)}, core.ModelFinish{Reason: core.StopToolCalls}}
		}
		return []core.ModelEvent{core.ModelTextDelta{Text: "the carrier lost it"}, core.ModelFinish{Reason: core.StopEndTurn}}
	})
	note := core.Tool("note", "Read the notes.", func(context.Context, struct{}) (string, error) {
		return "late at the depot", nil
	}, core.Replay(core.ReplaySafe))
	child := core.New(childModel, append([]core.Option{core.Name("researcher"), note}, prov...)...)
	parentModel := modelFunc(func(req core.ModelRequest) []core.ModelEvent {
		if last := req.Messages[len(req.Messages)-1]; last.Role == core.RoleUser {
			return []core.ModelEvent{core.ModelToolCall{ID: "c_sub", Name: "research", Args: []byte(`{"prompt":"why late?"}`)}, core.ModelFinish{Reason: core.StopToolCalls}}
		}
		return []core.ModelEvent{core.ModelTextDelta{Text: "it was lost"}, core.ModelFinish{Reason: core.StopEndTurn}}
	})
	parent := core.New(parentModel, append([]core.Option{core.Name("orders"),
		core.Subagent("research", "Research an order.", child)}, prov...)...)
	if _, err := parent.Generate(ctx, core.RunID("r_parent"), core.Prompt("where is order 42?")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	det, err := db.Run(ctx, "r_parent")
	if err != nil || len(det.Children) != 1 {
		t.Fatalf("parent = %+v %v, want one child", det, err)
	}
	childID := det.Children[0].ID
	if !validRunID(childID) || det.Children[0].ParentRunID != "r_parent" {
		t.Fatalf("child row = %+v", det.Children[0])
	}

	cfg := &config{agents: []*core.Agent{parent, child}}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	l.localDB = func() obsdb.DB { return db }
	defer l.stop()

	// The child as its own run, from its step 1.
	childCalls.Store(0)
	cmd := command{CommandID: "cmd_child", Agent: "researcher", Engine: "live",
		Source: &sourceSpec{RunID: childID, FromStep: 1}}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatalf("validate: %s", reason)
	}
	if status, _, errText := l.execute(ctx, cmd, "pg_child_1"); status != "succeeded" {
		t.Fatalf("execute = %s %s", status, errText)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	row, err := db.Run(ctx, "pg_child_1")
	if err != nil {
		t.Fatal(err)
	}
	if row.ParentRunID != "" || row.ParentCallID != "" || row.Meta["weft.parent.run.id"] != "" || row.Meta["weft.parent.call.id"] != "" {
		t.Errorf("the child's replay carries parent linkage: parent %q call %q meta %v", row.ParentRunID, row.ParentCallID, row.Meta)
	}
	if !row.Playground || row.ForkedFrom != childID+"#1" || row.Agent != "researcher" {
		t.Errorf("child replay = playground %v forked_from %q agent %q, want %s#1 of researcher", row.Playground, row.ForkedFrom, childID, row.Agent)
	}
	if n := childCalls.Load(); n != 1 {
		t.Errorf("the child's model ran %d times, want 1 (step 1 fresh)", n)
	}
	if parentAfter, _ := db.Run(ctx, "r_parent"); len(parentAfter.Children) != 1 {
		t.Errorf("the parent gained children: %+v", parentAfter.Children)
	}

	// The parent from the step that called the child: the Subagent call
	// is never-class (unannotated), so it parks and substitute answers it
	// from the record — the child does not run again.
	childCalls.Store(0)
	cmd = command{CommandID: "cmd_parent", Agent: "orders", Engine: "live", SideEffects: "substitute",
		Source: &sourceSpec{RunID: "r_parent", FromStep: 0}}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatalf("validate: %s", reason)
	}
	status, final, errText := l.execute(ctx, cmd, "pg_parent_0")
	if status != "succeeded" {
		t.Fatalf("parent replay = %s %s", status, errText)
	}
	if n := childCalls.Load(); n != 0 {
		t.Errorf("substitute re-ran the child %d times: a never-class Subagent call must be answered from the record", n)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	fin, err := db.Run(ctx, final)
	if err != nil {
		t.Fatal(err)
	}
	if fin.Pending != 0 || len(fin.Children) != 0 {
		t.Errorf("the substituted leg = pending %d children %d, want the recorded result and no child run", fin.Pending, len(fin.Children))
	}

	// park: the same call is left pending for a human; still no child.
	cmd = command{CommandID: "cmd_park", Agent: "orders", Engine: "live", SideEffects: "park",
		Source: &sourceSpec{RunID: "r_parent", FromStep: 0}}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatalf("validate: %s", reason)
	}
	if status, _, errText := l.execute(ctx, cmd, "pg_parent_park"); status != "succeeded" {
		t.Fatalf("park replay = %s %s", status, errText)
	}
	if n := childCalls.Load(); n != 0 {
		t.Errorf("park mode ran the child %d times", n)
	}
	if !l.parked["pg_parent_park"].isPending("c_sub") {
		t.Errorf("park mode: the research call is not pending")
	}
}

// modelFunc is a stateless core.Model: each request answered by f.
type modelFunc func(req core.ModelRequest) []core.ModelEvent

func (modelFunc) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: "func"} }

func (f modelFunc) Stream(_ context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	evs := f(req)
	return func(yield func(core.ModelEvent, error) bool) {
		for _, ev := range evs {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// TestThreadSourceWithContentOffRecords (ADR 0029, the thread path): a
// thread-stored turn whose records were captured content-off holds no
// messages to rebuild a step's request from — the local sink's
// MessagesAsOf and Studio's ?step= (409) refuse it — so the view of
// from_step is unknown, logged, and the replay proceeds on the thread's
// own messages, as it did before ADR 0029; it is never refused.
func TestThreadSourceWithContentOffRecords(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db"), otel.NoContent()), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	model := &tailModel{}
	agent := compactingAgent(p, model)
	store := thread.Memory()
	s, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	turn, err := s.Send(ctx, core.User("look everything up"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	saw := model.take()
	db := p.LocalDB()
	if _, err := obsdb.MessagesAsOf(ctx, db, turn.RunID(), 1); !errors.Is(err, obsdb.ErrStepMessages) {
		t.Fatalf("the content-off records answer step 1 with %v, want ErrStepMessages", err)
	}
	ts := httptest.NewServer(studio.New(studio.DB(db)).Handler())
	defer ts.Close()
	for name, local := range map[string]func() obsdb.DB{
		"local":  func() obsdb.DB { return db },
		"studio": func() obsdb.DB { return nil },
	} {
		cfg := &config{agents: []*core.Agent{agent}, threads: store}
		l := newLink(cfg, newRegistry(cfg), ts.URL, "")
		l.localDB = local
		cmd := command{CommandID: "cmd_off", Agent: "compactor", Engine: "live",
			Source: &sourceSpec{RunID: turn.RunID(), FromStep: 1}}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Errorf("%s: a thread source with content-off records was refused: %s", name, reason)
		} else if b, _ := json.Marshal(cmd.prefix); len(saw) != 4 || string(b) != saw[1] {
			t.Errorf("%s: prefix %s, want step 1's request %v", name, b, saw)
		}
		l.stop()
	}
}

// TestReplayRerunsTheAgentsPrepareStep (ADR 0029's limit): the runtime
// runs the agent, PrepareStep included, so the replay is fed step N's
// exact request — but the replay's own PrepareStep then runs over it.
// A PrepareStep idempotent over its own output leaves it as is (the
// main test's); one keyed on a count re-shapes it. Here PrepareStep
// summarizes everything between the prompt and the last two messages
// as "summary of K messages" once a request holds four or more: the
// source's step 3 saw "summary of 4 messages"; the replay from step 3
// is fed exactly that, and its PrepareStep — over 4 messages — rewrites
// it to "summary of 1 messages". The replay's input record is the exact
// prefix; what its model saw is not.
func TestReplayRerunsTheAgentsPrepareStep(t *testing.T) {
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(t.TempDir(), "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	model := &tailModel{}
	lookup := core.Tool("lookup", "Look up a record.", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return "record " + in.ID, nil
	}, core.Replay(core.ReplaySafe))
	agent := core.New(model, core.Name("counter"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()),
		core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
			m := req.Messages
			if len(m) < 4 {
				return req, nil
			}
			req.Messages = []core.Message{m[0], core.User(fmt.Sprintf("summary of %d messages", len(m)-3)), m[len(m)-2], m[len(m)-1]}
			return req, nil
		}), lookup)
	if _, err := agent.Generate(ctx, core.RunID("r_count"), core.Prompt("look everything up")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	source := model.take()
	if len(source) != 4 || !strings.Contains(source[3], "summary of 4 messages") {
		t.Fatalf("source requests = %v", source)
	}
	cfg := &config{agents: []*core.Agent{agent}}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	db := p.LocalDB()
	l.localDB = func() obsdb.DB { return db }
	defer l.stop()
	cmd := command{CommandID: "cmd_count", Agent: "counter", Engine: "live",
		Source: &sourceSpec{RunID: "r_count", FromStep: 3}}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatalf("validate: %s", reason)
	}
	if b, _ := json.Marshal(cmd.prefix); string(b) != source[3] {
		t.Fatalf("the replay's prefix\n%s\nwant step 3's request\n%s", b, source[3])
	}
	if status, _, errText := l.execute(ctx, cmd, "pg_count"); status != "succeeded" {
		t.Fatalf("execute = %s %s", status, errText)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	fed := model.take()
	if len(fed) == 0 || fed[0] == source[3] || !strings.Contains(fed[0], "summary of 1 messages") {
		t.Fatalf("the replay's step 0 saw %v, want its PrepareStep's re-shaping (summary of 1 messages)", fed)
	}
	got, view := stepMessagesJSON(t, db, "pg_count", 0)
	if got != fed[0] || view == nil {
		t.Errorf("the replay's step-0 request record = %s (view %v), want what its model saw, as a view", got, view)
	}
	batches, err := db.TranscriptBatches(ctx, "pg_count")
	if err != nil || len(batches) == 0 || !batches[0].Input {
		t.Fatalf("batches = %+v %v", batches, err)
	}
	var input []core.Message
	if err := json.Unmarshal(batches[0].Messages, &input); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(input); string(b) != source[3] {
		t.Errorf("the replay's input record = %s, want the source's step-3 request exactly", b)
	}
}
