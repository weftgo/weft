package wefttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// replayEcho is the deterministic tool the replay tests' agents use.
var replayEcho = weft.Tool("echo", "Echo a message.",
	func(_ context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})

// streamAll drains one model stream, returning its events and terminal
// error.
func streamAll(t *testing.T, m weft.Model, req weft.ModelRequest) ([]weft.ModelEvent, error) {
	t.Helper()
	var (
		evs []weft.ModelEvent
		err error
	)
	for ev, serr := range m.Stream(context.Background(), req) {
		if serr != nil {
			err = serr
			break
		}
		evs = append(evs, ev)
	}
	return evs, err
}

func textOf(evs []weft.ModelEvent) string {
	var sb strings.Builder
	for _, ev := range evs {
		if d, ok := ev.(weft.ModelTextDelta); ok {
			sb.WriteString(d.Text)
		}
	}
	return sb.String()
}

// fixtureFiles lists a test's recorded fixture directory.
func fixtureFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// R1: Record plays inner's stream through unchanged and writes one
// fixture file per request, in conversation order, with the model's
// identity for Replay's Info.
func TestRecordPassesThroughAndWrites(t *testing.T) {
	dir := t.TempDir()
	req := weft.ModelRequest{
		System:   "sys prompt",
		Messages: []weft.Message{weft.User("hi")},
		Tools:    []*weft.ToolDef{replayEcho},
	}
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("hello"), wefttest.Say("again")))

	evs, err := streamAll(t, m, req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(evs) != "hello" || len(evs) != 2 {
		t.Errorf("pass-through = %v, want inner's two events verbatim", evs)
	}
	if _, err := streamAll(t, m, req); err != nil {
		t.Fatal(err)
	}

	files := fixtureFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("%d fixture files, want 2: %v", len(files), files)
	}
	if files[0] >= files[1] || !strings.HasPrefix(files[0], "001-") || !strings.HasPrefix(files[1], "002-") {
		t.Errorf("fixtures %v, want conversation order by sequence number", files)
	}
	b, err := os.ReadFile(filepath.Join(dir, t.Name(), files[0]))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Model struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"model"`
		Request struct {
			System   string `json:"system"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
			Tools []string `json:"tools"`
		} `json:"request"`
		Events []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"events"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	if fx.Model.Provider != "wefttest" || fx.Model.Name != "script" {
		t.Errorf("model = %+v, want the scripted model's identity", fx.Model)
	}
	if fx.Request.System != "sys prompt" {
		t.Errorf("system = %q, want it recorded for the reviewer", fx.Request.System)
	}
	if len(fx.Request.Messages) != 1 || fx.Request.Messages[0].Content[0].Text != "hi" {
		t.Errorf("messages = %+v, want the request's transcript recorded", fx.Request.Messages)
	}
	if !slices.Equal(fx.Request.Tools, []string{"echo"}) {
		t.Errorf("tools = %v, want the sorted names", fx.Request.Tools)
	}
	if len(fx.Events) != 2 || fx.Events[0].Type != "text" || fx.Events[0].Text != "hello" || fx.Events[1].Type != "finish" {
		t.Errorf("events = %+v, want the recorded stream", fx.Events)
	}
	if fx.Error != "" {
		t.Errorf("error = %q, want empty on a clean stream", fx.Error)
	}
}

// R1's error half: a stream that errors is recorded with its error.
func TestRecordCapturesStreamError(t *testing.T) {
	dir := t.TempDir()
	req := weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}}
	boom := errors.New("provider down")
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Fail(boom)))

	if _, err := streamAll(t, m, req); !errors.Is(err, boom) {
		t.Fatalf("pass-through error = %v, want the scripted error", err)
	}
	files := fixtureFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("%d fixture files, want 1", len(files))
	}
	b, err := os.ReadFile(filepath.Join(dir, t.Name(), files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"error": "provider down"`) {
		t.Errorf("fixture does not carry the stream error:\n%s", b)
	}

	// And it replays as that error — the text, with weft's sentinels
	// re-wrapped so errors.Is holds for the realistic case.
	p := wefttest.Replay(t, dir)
	if _, err := streamAll(t, p, req); err == nil || err.Error() != "provider down" {
		t.Errorf("replayed error = %v, want the recorded text", err)
	}

	dir2 := t.TempDir()
	m2 := wefttest.Record(t, dir2, wefttest.Script(
		wefttest.Fail(fmt.Errorf("%w after 60s", weft.ErrStreamIdle))))
	if _, err := streamAll(t, m2, req); err == nil {
		t.Fatal("recorded stream did not fail")
	}
	if _, err := streamAll(t, wefttest.Replay(t, dir2), req); !errors.Is(err, weft.ErrStreamIdle) {
		t.Errorf("replayed sentinel error = %v, want errors.Is(ErrStreamIdle)", err)
	}
}

// A caller that breaks out of the stream still leaves a fixture — the
// events it saw, with the abandonment recorded in "error".
func TestRecordStreamAbandonedByCaller(t *testing.T) {
	dir := t.TempDir()
	req := weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}}
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("hello")))
	for _, err := range m.Stream(context.Background(), req) {
		if err != nil {
			t.Fatal(err)
		}
		break // abandon after the first event
	}
	files := fixtureFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("%d fixture files, want 1", len(files))
	}
	b, err := os.ReadFile(filepath.Join(dir, t.Name(), files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "wefttest: stream abandoned by the caller") {
		t.Errorf("fixture does not record the abandonment:\n%s", b)
	}
}

// A reset that cannot happen (a file sits where the fixture directory
// needs to be) fails the stream — never t.FailNow, which is invalid off
// the test goroutine a subagent's Stream can run on (ADR 0017 C4).
func TestRecordResetFailureFailsStream(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := wefttest.Record(t, blocker, wefttest.Script(wefttest.Say("x")))
	_, err := streamAll(t, m, weft.ModelRequest{Messages: []weft.Message{weft.User("q")}})
	if err == nil || !strings.Contains(err.Error(), "wefttest.Record") {
		t.Errorf("err = %v, want the reset failure as the stream's error", err)
	}
}

// R2: a committed recording replays without recording — the
// fresh-checkout proof. Under WEFT_RECORD the same test re-records from
// the scripted inner model (offline, deterministic), so the committed
// fixtures regenerate with `WEFT_RECORD=1 go test ./wefttest/`.
func TestReplayPlaysRecordedEvents(t *testing.T) {
	// Replay is a wefttest model: it ignores the kill switch like every
	// other double (P1).
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")

	inner := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}),
		wefttest.Say("done"),
	)
	var model weft.Model
	if os.Getenv("WEFT_RECORD") != "" {
		model = wefttest.Record(t, "testdata/replay", inner)
	} else {
		model = wefttest.Replay(t, "testdata/replay")
	}
	res, err := weft.New(model, replayEcho).Generate(context.Background(), weft.Prompt("Echo hi."))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "done" {
		t.Errorf("Text() = %q, want done", res.Text())
	}
	if res.NumSteps() != 2 {
		t.Errorf("steps = %d, want 2 (the tool step and the reply)", res.NumSteps())
	}
	var sawResult bool
	for _, msg := range res.Messages {
		if msg.Role == weft.RoleTool {
			sawResult = true
			for _, p := range msg.Content {
				if tr, ok := p.(weft.ToolResultPart); ok && tr.Content != "echo: hi" {
					t.Errorf("tool result = %q, want the deterministic echo output", tr.Content)
				}
			}
		}
	}
	if !sawResult {
		t.Error("no tool message in the replayed transcript")
	}
}

// R3: the system prompt does not key — a prompt-wording tweak must not
// invalidate fixtures.
func TestReplayKeyIgnoresSystem(t *testing.T) {
	dir := t.TempDir()
	reqA := weft.ModelRequest{System: "system A", Messages: []weft.Message{weft.User("q")}}
	reqB := weft.ModelRequest{System: "system B", Messages: []weft.Message{weft.User("q")}}

	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("one"), wefttest.Say("two")))
	if _, err := streamAll(t, m, reqA); err != nil {
		t.Fatal(err)
	}
	if _, err := streamAll(t, m, reqB); err != nil {
		t.Fatal(err)
	}

	p := wefttest.Replay(t, dir)
	evs, err := streamAll(t, p, reqB)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(evs) != "one" {
		t.Errorf("reqB replayed %q; it must share reqA's key (system prompt not keyed)", textOf(evs))
	}
	if evs, err = streamAll(t, p, reqA); err != nil {
		t.Fatal(err)
	}
	if textOf(evs) != "two" {
		t.Errorf("reqA replayed %q, want the second recorded entry for the shared key", textOf(evs))
	}
}

// R3's complement: everything else in the request keys — a changed
// message, tool catalogue, thinking level, or sequential flag is a miss,
// never a silently wrong answer.
func TestReplayKeyDistinguishes(t *testing.T) {
	dir := t.TempDir()
	base := weft.ModelRequest{
		Messages: []weft.Message{weft.User("q")},
		Tools:    []*weft.ToolDef{replayEcho},
	}
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("recorded")))
	if _, err := streamAll(t, m, base); err != nil {
		t.Fatal(err)
	}

	changedMsg := base
	changedMsg.Messages = []weft.Message{weft.User("different")}

	changedTools := base
	other := weft.Tool("other", "Another tool.", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	changedTools.Tools = []*weft.ToolDef{replayEcho, other}

	changedThinking := base
	changedThinking.Thinking = weft.ThinkingConfig{Level: weft.ThinkOff}

	changedSequential := base
	changedSequential.SequentialTools = true

	p := wefttest.Replay(t, dir)
	for name, req := range map[string]weft.ModelRequest{
		"message":         changedMsg,
		"tool catalogue":  changedTools,
		"thinking level":  changedThinking,
		"sequential flag": changedSequential,
	} {
		if _, err := streamAll(t, p, req); !errors.Is(err, wefttest.ErrNoFixture) {
			t.Errorf("changed %s replayed with err = %v, want ErrNoFixture", name, err)
		}
	}
	// The unmodified request still answers.
	if evs, err := streamAll(t, p, base); err != nil || textOf(evs) != "recorded" {
		t.Errorf("unmodified request = (%q, %v), want the recorded reply", textOf(evs), err)
	}
}

// R4: repeated identical requests (a retry, a loop that asks twice)
// record as separate entries and replay in recorded order; the N+1th
// is a miss.
func TestReplayRepeatedRequestsInOrder(t *testing.T) {
	dir := t.TempDir()
	req := weft.ModelRequest{Messages: []weft.Message{weft.User("again")}}
	m := wefttest.Record(t, dir, wefttest.Script(
		wefttest.Say("first"), wefttest.Say("second"), wefttest.Say("third")))
	for range 3 {
		if _, err := streamAll(t, m, req); err != nil {
			t.Fatal(err)
		}
	}

	p := wefttest.Replay(t, dir)
	for _, want := range []string{"first", "second", "third"} {
		evs, err := streamAll(t, p, req)
		if err != nil {
			t.Fatal(err)
		}
		if textOf(evs) != want {
			t.Errorf("repeated request replayed %q, want %q (recorded order)", textOf(evs), want)
		}
	}
	if _, err := streamAll(t, p, req); !errors.Is(err, wefttest.ErrNoFixture) {
		t.Errorf("fourth identical request = %v, want ErrNoFixture (a miss, not a stale repeat)", err)
	}
}

// R5: a miss is loud and diagnosable — the error names the fixture
// directory, the key, and the first user message's opening words.
func TestReplayMissIsLoud(t *testing.T) {
	p := wefttest.Replay(t, filepath.Join(t.TempDir(), "absent"))
	req := weft.ModelRequest{Messages: []weft.Message{weft.User("Where is order 1234? It arrived broken.")}}
	_, err := streamAll(t, p, req)
	if !errors.Is(err, wefttest.ErrNoFixture) {
		t.Fatalf("err = %v, want ErrNoFixture", err)
	}
	msg := err.Error()
	for _, want := range []string{"no replay fixture", t.Name(), "Where is order 1234? It arrived broken."} {
		if !strings.Contains(msg, want) {
			t.Errorf("miss error %q does not name %q", msg, want)
		}
	}
	if !strings.Contains(msg, "key ") {
		t.Errorf("miss error %q does not name the key", msg)
	}
}

// R6: Replay reports the recorded model's identity, so RunStart.Model
// and the manifest match the live run.
func TestReplayInfoIsRecorded(t *testing.T) {
	dir := t.TempDir()
	inner := infoModel{Model: wefttest.Script(wefttest.Say("x")), info: weft.ModelInfo{Provider: "openai", Name: "gpt-5"}}
	m := wefttest.Record(t, dir, inner)
	if _, err := streamAll(t, m, weft.ModelRequest{Messages: []weft.Message{weft.User("q")}}); err != nil {
		t.Fatal(err)
	}
	p := wefttest.Replay(t, dir)
	got, ok := p.(interface{ Info() weft.ModelInfo })
	if !ok {
		t.Fatal("Replay's model does not implement Info()")
	}
	if info := got.Info(); info != (weft.ModelInfo{Provider: "openai", Name: "gpt-5"}) {
		t.Errorf("Info() = %+v, want the recorded model's identity", info)
	}
}

type infoModel struct {
	weft.Model
	info weft.ModelInfo
}

func (m infoModel) Info() weft.ModelInfo { return m.info }

// R7: recording is deterministic — the same conversation twice writes
// byte-identical fixtures (no timestamps, no paths, stable order).
func TestRecordIsDeterministic(t *testing.T) {
	run := func(dir string) map[string]string {
		t.Helper()
		m := wefttest.Record(t, dir, wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}),
			wefttest.Say("done"),
		))
		if _, err := weft.New(m, replayEcho).Generate(context.Background(), weft.Prompt("Echo hi.")); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, f := range fixtureFiles(t, dir) {
			b, err := os.ReadFile(filepath.Join(dir, t.Name(), f))
			if err != nil {
				t.Fatal(err)
			}
			out[f] = string(b)
		}
		return out
	}
	a, b := run(t.TempDir()), run(t.TempDir())
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("recorded %d and %d fixtures, want 2 each", len(a), len(b))
	}
	for name, want := range a {
		if got, ok := b[name]; !ok || got != want {
			t.Errorf("fixture %s differs between recordings", name)
		}
	}
}

// R8: a subagent's child requests record and replay through the same
// directory; the key, not the sequence number, tells parent and child
// apart. Run under -race in `make test`.
func TestReplaySubagent(t *testing.T) {
	build := func(m weft.Model) *weft.Agent {
		child := weft.New(m, weft.Instructions("You are the researcher."))
		return weft.New(m, replayEcho, weft.Subagent("research", "Research a topic.", child))
	}

	dir := t.TempDir()
	rec := wefttest.Record(t, dir, wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("found"), // the child's stream
		wefttest.Say("done"),  // the parent's second step
	))
	res, err := build(rec).Generate(context.Background(), weft.Prompt("Research replay."))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "done" {
		t.Fatalf("recording run Text() = %q, want done", res.Text())
	}

	p := wefttest.Replay(t, dir)
	res, err = build(p).Generate(context.Background(), weft.Prompt("Research replay."))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "done" {
		t.Errorf("replayed run Text() = %q, want done", res.Text())
	}
	if got := len(res.Messages); got != 4 { // prompt, research call, tool message, reply
		t.Errorf("transcript has %d messages, want 4", got)
	}
	if n := len(p.(interface{ Requests() []weft.ModelRequest }).Requests()); n != 3 {
		t.Errorf("replay served %d requests, want 3 (parent, child, parent)", n)
	}
}

// R9's second half lives in TestReplayMissIsLoud (absent directory);
// here a directory that exists but is empty misses the same way.
func TestReplayMissingDir(t *testing.T) {
	empty := t.TempDir()
	p := wefttest.Replay(t, empty)
	_, err := streamAll(t, p, weft.ModelRequest{Messages: []weft.Message{weft.User("q")}})
	if !errors.Is(err, wefttest.ErrNoFixture) {
		t.Errorf("err = %v, want ErrNoFixture for an empty directory", err)
	}
}

// R10: both models record what the agent asked, so a test asserts on
// requests even when the answers came from disk.
func TestReplayRecordsRequests(t *testing.T) {
	dir := t.TempDir()
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("done")))
	req := weft.ModelRequest{System: "sys", Messages: []weft.Message{weft.User("q")}}
	if _, err := streamAll(t, m, req); err != nil {
		t.Fatal(err)
	}
	if got := m.(interface {
		Requests() []weft.ModelRequest
	}).Requests(); len(got) != 1 || got[0].System != "sys" {
		t.Errorf("Record.Requests() = %+v, want the request verbatim", got)
	}

	p := wefttest.Replay(t, dir)
	if _, err := streamAll(t, p, req); err != nil {
		t.Fatal(err)
	}
	if got := p.(interface {
		Requests() []weft.ModelRequest
	}).Requests(); len(got) != 1 || got[0].System != "sys" {
		t.Errorf("Replay.Requests() = %+v, want the request verbatim", got)
	}
}

// The Model contract, before anything replayed: a done ctx yields
// ctx.Err(), not a miss — the same rule Script enforces.
func TestReplayYieldsContextErrorWhenDone(t *testing.T) {
	dir := t.TempDir()
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("x")))
	req := weft.ModelRequest{Messages: []weft.Message{weft.User("q")}}
	if _, err := streamAll(t, m, req); err != nil {
		t.Fatal(err)
	}
	p := wefttest.Replay(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got error
	for _, err := range p.Stream(ctx, req) {
		got = err
	}
	if !errors.Is(got, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", got)
	}
}

// ToolChoice joins the key (ADR 0017's 2026-09-22 amendment): a forced
// choice changes what the model says, exactly as a thinking level does.
// The file side pins the compatibility half — a request without a
// choice records the same canonical bytes v0.2.0 wrote, no tool_choice
// key at all, so every pre-2a fixture's hash is unchanged.
func TestReplayKeyToolChoice(t *testing.T) {
	dir := t.TempDir()
	base := weft.ModelRequest{
		Messages: []weft.Message{weft.User("q")},
		Tools:    []*weft.ToolDef{replayEcho},
	}
	m := wefttest.Record(t, dir, wefttest.Script(wefttest.Say("recorded")))
	if _, err := streamAll(t, m, base); err != nil {
		t.Fatal(err)
	}

	// The recorded file carries no tool_choice key: nil-when-zero keeps
	// the canonical JSON byte-identical to v0.2.0's keyDoc.
	files, err := filepath.Glob(filepath.Join(dir, t.Name(), "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("fixture files = %v, %v; want exactly one", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "tool_choice") {
		t.Errorf("recorded fixture carries a tool_choice key; nil-when-zero is broken:\n%s", b)
	}

	forced := base
	forced.ToolChoice = weft.ToolChoiceConfig{Mode: weft.ToolChoiceNamed, Name: "echo"}
	p := wefttest.Replay(t, dir)
	if _, err := streamAll(t, p, forced); !errors.Is(err, wefttest.ErrNoFixture) {
		t.Errorf("forced-choice request replayed with err = %v, want ErrNoFixture", err)
	}
	if evs, err := streamAll(t, p, base); err != nil || textOf(evs) != "recorded" {
		t.Errorf("unmodified request = (%q, %v), want the recorded reply", textOf(evs), err)
	}
}
