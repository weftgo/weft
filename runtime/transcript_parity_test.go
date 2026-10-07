package runtime

import (
	"context"
	"encoding/json"
	"iter"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// parityModel answers by the transcript's tail: "refund" calls the
// refund tool, "steer me" is held until gate closes (a steer lands
// mid-turn), a tool result gets words, anything else gets words.
type parityModel struct{ gate chan struct{} }

func (*parityModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "wefttest", Name: "parity"}
}

func (m *parityModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	last := req.Messages[len(req.Messages)-1]
	return func(yield func(weft.ModelEvent, error) bool) {
		switch {
		case last.Role == weft.RoleUser && last.Text() == "refund":
			yield(weft.ModelToolCall{ID: "c_r", Name: "refund", Args: []byte(`{"order_id":"1"}`)}, nil)
			yield(weft.ModelFinish{Reason: weft.StopToolCalls}, nil)
			return
		case last.Role == weft.RoleUser && last.Text() == "steer me":
			select {
			case <-m.gate:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		yield(weft.ModelTextDelta{Text: "reply to " + string(last.Role)}, nil)
		yield(weft.ModelFinish{Reason: weft.StopEndTurn}, nil)
	}
}

// TestTranscriptPathsAgree records one real multi-turn session through
// a local otel pipeline and a thread store, then resolves each turn
// through the three paths (§10.3: thread, local obsdb, Studio HTTP):
// the {input, steps} split must be the same — a re-run of a turn must
// not depend on which store answered.
func TestTranscriptPathsAgree(t *testing.T) {
	for _, backend := range []string{"jsonl", "memory"} {
		t.Run(backend, func(t *testing.T) { transcriptParity(t, backend) })
	}
}

func transcriptParity(t *testing.T, backend string) {
	ctx := context.Background()
	dir := t.TempDir()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(dir, "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	refund := weft.Tool("refund", "Refund.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "refunded", nil
	}, weft.RequireApproval())
	model := &parityModel{gate: make(chan struct{})}
	agent := weft.New(model, weft.Name("acme-support"),
		weft.TracerProvider(p.TracerProvider()), weft.LoggerProvider(p.LoggerProvider()), refund)
	store := thread.Memory()
	if backend == "jsonl" {
		if store, err = jsonl.Open(filepath.Join(dir, "threads")); err != nil {
			t.Fatal(err)
		}
	}
	s, err := thread.Create(ctx, store, agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	send := func(msg weft.Message) *thread.Turn {
		t.Helper()
		turn, err := s.Send(ctx, msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		return turn
	}
	runs := map[string]string{}
	runs["turn 1"] = send(weft.User("hello")).RunID()
	runs["turn 2"] = send(weft.User("again")).RunID()
	runs["parked"] = send(weft.User("refund")).RunID()
	resumed, err := s.Decide(ctx, thread.Approve("c_r"))
	if err != nil || resumed == nil {
		t.Fatalf("decide: %v %v", resumed, err)
	}
	if _, err := resumed.Wait(); err != nil {
		t.Fatal(err)
	}
	runs["resumed"] = resumed.RunID()
	runs["multimodal"] = send(weft.UserParts(weft.TextPart{Text: "look"},
		weft.FilePart{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}})).RunID()
	steered, err := s.Send(ctx, weft.User("steer me"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := s.Send(ctx, weft.User("and also this"), thread.As(thread.Steer)); err != nil {
		t.Fatal(err)
	}
	close(model.gate)
	if _, err := steered.Wait(); err != nil {
		t.Fatal(err)
	}
	runs["steered"] = steered.RunID()
	// A turn after a compaction: the model saw the summary, not the
	// entries. The thread path cannot rebuild that context (it reads the
	// path raw) and must refuse rather than feed a re-run a context the
	// run never had; the obsdb/Studio record is the truth.
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	compacted := send(weft.User("after compaction")).RunID()
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}

	srv := studio.New(studio.DB(p.LocalDB()))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cfg := &config{agents: []*weft.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	defer l.stop()

	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	if got, err := transcriptFromThread(ctx, store, agent, compacted); err == nil {
		fromDB, _ := transcriptFromObsdb(ctx, p.LocalDB(), compacted)
		if fromDB == nil || js(got.input) != js(fromDB.input) {
			t.Errorf("after compaction the thread path resolved a context the run was not fed:\nthread: %s\nrecord: %s", js(got.input), js(fromDB))
		}
	}
	for name, runID := range runs {
		fromThread, err := transcriptFromThread(ctx, store, agent, runID)
		if err != nil {
			t.Errorf("%s: thread: %v", name, err)
			continue
		}
		fromDB, err := transcriptFromObsdb(ctx, p.LocalDB(), runID)
		if err != nil {
			t.Errorf("%s: obsdb: %v", name, err)
			continue
		}
		fromStudio, err := l.transcriptFromStudio(ctx, runID)
		if err != nil {
			t.Errorf("%s: studio: %v", name, err)
			continue
		}
		for _, c := range []struct {
			path string
			got  *sourceRun
		}{{"obsdb", fromDB}, {"studio", fromStudio}} {
			if js(c.got.input) != js(fromThread.input) {
				t.Errorf("%s (%s): %s input differs from thread's\n%s: %s\nthread: %s", name, runID, c.path, c.path, js(c.got.input), js(fromThread.input))
			}
			if js(c.got.steps) != js(fromThread.steps) {
				t.Errorf("%s (%s): %s steps differ from thread's\n%s: %s\nthread: %s", name, runID, c.path, c.path, js(c.got.steps), js(fromThread.steps))
			}
		}
	}
}

// TestNewInputOnAResumedSourceKeepsTheResolution pins §5.1's "input
// replaces the turn's user message" on a resumed source: the resumed
// run has no prompt — it was fed the transcript ending at the parked
// call and recorded the call's result before its first model call. A
// new input must follow that result; dropping it leaves the call
// orphaned, and Repair would invent a result the run never had.
func TestNewInputOnAResumedSourceKeepsTheResolution(t *testing.T) {
	src := &sourceRun{
		input: []weft.Message{weft.User("refund"),
			{Role: weft.RoleAssistant, Content: []weft.Part{weft.ToolCallPart{ID: "c_r", Name: "refund", Args: []byte(`{}`)}}}},
		steps: []weft.Message{
			{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{CallID: "c_r", Name: "refund", Content: "refunded"}}},
			{Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: "done"}}}},
	}
	in := "something else"
	got, err := runPrefix(src, command{Input: &in, Source: &sourceSpec{RunID: "s_x-t4"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := prefixComplete(got); err != nil {
		t.Fatalf("the prefix a new input follows: %v", err)
	}
	if n := len(got); n != 3 || got[n-1].Role != weft.RoleTool {
		t.Errorf("prefix = %+v, want the conversation through the parked call's result", got)
	}
	// A plain turn still drops its prompt.
	plain := &sourceRun{input: []weft.Message{weft.User("hi")}, steps: []weft.Message{{Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: "yo"}}}}}
	if got, _ := runPrefix(plain, command{Input: &in, Source: &sourceSpec{RunID: "s_x-t1"}}, true); len(got) != 0 {
		t.Errorf("plain turn prefix = %+v, want the prompt dropped", got)
	}
}

// TestParseThreadRunIDWholeSuffix pins the thread run id shape: a
// subagent child of a thread turn ("<session>-t1/2/c_1") is not a turn
// of the session — fork mode must refuse it up front, not fork and fail.
func TestParseThreadRunIDWholeSuffix(t *testing.T) {
	for _, id := range []string{"s_x-t1/2/c_1", "s_x-t1x", "s_x-t+1", "s_x-t0", "s_x-t"} {
		if _, _, err := parseThreadRunID(id); err == nil {
			t.Errorf("parseThreadRunID(%q) accepted a non-turn id", id)
		}
	}
	if s, n, err := parseThreadRunID("s_a-tb-t12"); err != nil || s != "s_a-tb" || n != 12 {
		t.Errorf("parseThreadRunID = %q %d %v", s, n, err)
	}
}

// TestScriptedReplaysSignedTurns pins the scripted engine over a run of
// a reasoning model (Anthropic thinking, Gemini thought signatures):
// the record carries each block's and call's signature, and the
// re-run's request for step 1 holds step 0's message as the engine
// rebuilt it — a rebuilt message without the signatures keys
// differently, and every multi-step signed run missed at step 1.
func TestScriptedReplaysSignedTurns(t *testing.T) {
	lookup := weft.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		return "shipped", nil
	})
	src := &sourceRun{
		input: []weft.Message{weft.User("where is #4411?")},
		steps: []weft.Message{
			{Role: weft.RoleAssistant, Content: []weft.Part{
				weft.ReasoningPart{Text: "first think", Signature: "sig-a"},
				weft.ReasoningPart{Text: "then think", Signature: "sig-b"},
				weft.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{}`), Signature: "sig-call"},
			}},
			{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "shipped"}}},
			weft.Assistant("It shipped."),
		},
	}
	agt := weft.New(newScriptedModel(src, []string{"lookup_order"}), weft.Name("a"), lookup)
	res, err := agt.Generate(context.Background(), weft.Messages(src.input...))
	if err != nil {
		t.Fatalf("the scripted re-run of a signed run: %v", err)
	}
	if res.Text() != "It shipped." {
		t.Errorf("reply = %q", res.Text())
	}
}
