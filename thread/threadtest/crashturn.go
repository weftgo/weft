package threadtest

import (
	"bufio"
	"context"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// The mid-turn crash harness (plan §7, ADR 0011 §7): a child process
// runs a two-step turn against a real backend — the first step calls a
// tool, the second blocks in the model — and the parent SIGKILLs it
// once the first step is fully emitted. The assertions pin the v0.4
// promise: everything emitted is durable (prompt, the signed assistant
// message, the tool message), nothing is torn, the turn entry never
// landed (the turn never ended), and a reopened session continues from
// the crash point. Each backend's suite runs it through its own
// re-executed helper (the pattern jsonl's storage crash tests set).

// CrashSession is the session id the child creates.
const CrashSession = "s_crashturn"

// crashEnv gates the child side of the harness.
const crashEnv = "WEFT_THREADTEST_CRASH_TURN"

// CrashTurn runs the parent side: it re-executes the test binary at
// helperTest (the backend's child test, gated on the crash env), waits
// for the child's "step0done" marker, SIGKILLs it mid-second-step, and
// asserts the crash's durability through reopen — a fresh Storage over
// the same location. storagePath is passed to the child through
// WEFT_THREADTEST_CRASH_STORAGE.
func CrashTurn(t *testing.T, helperTest, storagePath string, reopen func() (thread.Storage, error)) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+helperTest+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), crashEnv+"=1", "WEFT_THREADTEST_CRASH_STORAGE="+storagePath)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sawStep := make(chan struct{})
	go func() {
		defer close(sawStep)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == "step0done" {
				return
			}
		}
	}()
	select {
	case <-sawStep:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the child never finished its first step")
	}
	_ = cmd.Process.Kill() // SIGKILL mid-second-step: no cleanup, no flush
	_, _ = cmd.Process.Wait()

	st, err := reopen()
	if err != nil {
		t.Fatalf("reopen after the crash: %v", err)
	}
	ctx := context.Background()
	h, entries, report, err := st.Load(ctx, CrashSession)
	if err != nil {
		t.Fatalf("the crashed session must load: %v", err)
	}
	if report != nil {
		t.Errorf("a crash between steps left repairs: %+v", report)
	}
	if h.ID != CrashSession {
		t.Fatalf("header = %+v", h)
	}
	// Prompt, the signed assistant with its call, the tool's answer —
	// and no turn entry: the turn never ended.
	want := []weft.Message{
		weft.User("crash the turn"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "one step", Signature: "sig-crash"},
			weft.ToolCallPart{ID: "call_crash", Name: "note", Args: []byte(`{"text":"crash"}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "call_crash", Name: "note", Content: "noted: crash"},
		}},
	}
	var got []weft.Message
	for _, e := range entries {
		if _, ok := e.(thread.TurnEntry); ok {
			t.Fatal("a turn entry landed for a turn that never ended")
		}
		if me, ok := e.(thread.MessageEntry); ok {
			got = append(got, me.Message)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the crash lost or changed emitted work:\n got %+v\nwant %+v", got, want)
	}

	// Recovery: a reopened session continues from the crash point — the
	// model's request carries everything that survived.
	m := &replayModel{}
	s, err := thread.Open(ctx, st, CrashSession, weft.New(m))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("continue"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("the follow-up turn after the crash: %v", err)
	}
	if reqs := m.Requests(); len(reqs) == 0 {
		t.Fatal("the follow-up made no model call")
	} else {
		fed := reqs[0].Messages
		if len(fed) != 4 || fed[0].Text() != "crash the turn" || fed[3].Text() != "continue" {
			t.Errorf("the model was fed %d messages — the crash point must be the context", len(fed))
		}
	}
}

// RunCrashTurnChild is the child side, called from the backend's
// re-executed helper test: it opens its storage through open (the
// WEFT_THREADTEST_CRASH_STORAGE location), runs the turn, prints the
// step0done marker once the first step is fully emitted, and blocks in
// the second model call until the parent kills it. It returns only
// when the env gate is unset (the parent's own run).
func RunCrashTurnChild(t *testing.T, open func() (thread.Storage, error)) {
	t.Helper()
	if os.Getenv(crashEnv) == "" {
		return // the parent's own run, or a plain `go test`
	}
	st, err := open()
	if err != nil {
		fmt.Println("helper: open failed:", err)
		os.Exit(2)
	}
	block := make(chan struct{}) // never closed in the child
	agent := weft.New(&crashTurnModel{block: block}, crashNoteTool())
	ctx := context.Background()
	// The fixed session id the parent asserts over; every entry id
	// after it stays unique.
	first := true
	s, err := thread.Create(ctx, st, agent, thread.IDs(func() string {
		if first {
			first = false
			return CrashSession
		}
		return thread.NewEntryID()
	}))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("crash the turn"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	sawStep := false
	for ev, err := range turn.Events() {
		if err != nil {
			fmt.Println("helper: stream error:", err)
			os.Exit(2)
		}
		if f, ok := ev.(weft.StepFinish); ok && f.Index == 0 && !sawStep {
			sawStep = true
			// The step's messages were persisted before this event was
			// emitted (the fire sites precede it); say so plainly.
			fmt.Println("step0done")
		}
	}
	fmt.Println("helper: the turn ended before the kill")
	os.Exit(2)
}

// crashTurnModel is the child's two-step model: the first call emits
// signed reasoning and a tool call; every later call blocks on block
// (never closed in the child) until ctx dies, honouring the contract.
type crashTurnModel struct {
	block chan struct{}
	calls int
}

func (m *crashTurnModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "crashturn"}
}

func (m *crashTurnModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	first := m.calls == 1
	return func(yield func(weft.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if !first {
			select {
			case <-m.block:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		events := []weft.ModelEvent{
			weft.ModelTextDelta{Text: "never reached"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		}
		if first {
			events = []weft.ModelEvent{
				weft.ModelReasoningDelta{Text: "one step", Signature: "sig-crash"},
				weft.ModelToolCall{ID: "call_crash", Name: "note", Args: []byte(`{"text":"crash"}`)},
				weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
			}
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// replaySay is the parent's follow-up model: one plain answer per
// call, recording its requests for the context assertion.
type replayModel struct {
	requests []weft.ModelRequest
}

func (m *replayModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "say"}
}

func (m *replayModel) Requests() []weft.ModelRequest {
	return append([]weft.ModelRequest(nil), m.requests...)
}

func (m *replayModel) Stream(_ context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.requests = append(m.requests, req)
	return func(yield func(weft.ModelEvent, error) bool) {
		for _, ev := range []weft.ModelEvent{
			weft.ModelTextDelta{Text: "recovered"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// crashNoteTool is the child's tool: one plain answer.
func crashNoteTool() *weft.ToolDef {
	return weft.Tool("note", "Record a note.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "noted: " + in.Text, nil
	})
}
