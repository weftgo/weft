package thread_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// steerHeldModel answers its first call after the gate opens (held closed
// once it waits), every later call at once; it records the user texts
// of every request — what the turn was fed.
type steerHeldModel struct {
	mu    sync.Mutex
	calls int
	seen  []string
	held  chan struct{}
	gate  chan struct{}
	once  sync.Once
}

func newSteerHeld() *steerHeldModel {
	return &steerHeldModel{held: make(chan struct{}), gate: make(chan struct{})}
}

func (m *steerHeldModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "held"}
}

func (m *steerHeldModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.mu.Lock()
	m.calls++
	n := m.calls
	for _, msg := range req.Messages {
		if msg.Role != core.RoleUser {
			continue
		}
		for _, p := range msg.Content {
			if tp, ok := p.(core.TextPart); ok {
				m.seen = append(m.seen, tp.Text)
			}
		}
	}
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		if n == 1 {
			m.once.Do(func() { close(m.held) })
			select {
			case <-m.gate:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		if !yield(core.ModelTextDelta{Text: "answer"}, nil) {
			return
		}
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

func (m *steerHeldModel) fed() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.seen, "|")
}

// TestSteer pins Session.Steer: into a turn running now it is
// delivered — the model sees it, no second turn starts; on an idle
// session, a parked one, and one reopened from what a crash mid-turn
// leaves (a prompt with no turn end) it is ErrNotRunning and writes
// nothing; a non-user message is refused before anything is written.
func TestSteer(t *testing.T) {
	ctx := context.Background()

	t.Run("running", func(t *testing.T) {
		m := newSteerHeld()
		s, err := thread.Create(ctx, thread.Memory(), core.New(m))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close(ctx) }()
		turn, err := s.Send(ctx, core.User("first"))
		if err != nil {
			t.Fatal(err)
		}
		<-m.held
		if err := s.Steer(ctx, core.Message{Role: core.RoleAssistant}); !errors.Is(err, core.ErrInvalidSteer) {
			t.Errorf("a non-user steer = %v, want core.ErrInvalidSteer", err)
		}
		if err := s.Steer(ctx, core.User("and the refund")); err != nil {
			t.Fatalf("Steer into the running turn = %v", err)
		}
		close(m.gate)
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(m.fed(), "and the refund") {
			t.Errorf("the turn never saw the steer: %s", m.fed())
		}
		if n := countTurns(s); n != 1 {
			t.Errorf("turns = %d, want 1 (the steer joined the running turn)", n)
		}
	})

	t.Run("idle", func(t *testing.T) {
		s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("hi"))))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close(ctx) }()
		turn, _ := s.Send(ctx, core.User("hello"))
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		_ = s.WaitIdle(ctx)
		before := len(s.Entries())
		if err := s.Steer(ctx, core.User("late")); !errors.Is(err, thread.ErrNotRunning) {
			t.Fatalf("Steer on an idle session = %v, want ErrNotRunning", err)
		}
		time.Sleep(10 * time.Millisecond)
		if after := len(s.Entries()); after != before {
			t.Errorf("a refused Steer wrote: %d entries, had %d", after, before)
		}
	})

	t.Run("parked", func(t *testing.T) {
		agent := core.New(
			wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{}`}), wefttest.Say("done")),
			core.Tool("deploy", "Deploy.", func(context.Context, struct{}) (string, error) { return "ok", nil }, core.RequireApproval()),
		)
		s, err := thread.Create(ctx, thread.Memory(), agent)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close(ctx) }()
		turn, _ := s.Send(ctx, core.User("deploy"))
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		before := len(s.Entries())
		if err := s.Steer(ctx, core.User("while parked")); !errors.Is(err, thread.ErrNotRunning) {
			t.Fatalf("Steer on a parked session = %v, want ErrNotRunning", err)
		}
		if after := len(s.Entries()); after != before {
			t.Errorf("a refused Steer wrote: %d entries, had %d", after, before)
		}
	})

	t.Run("crash snapshot", func(t *testing.T) {
		s, before := midTurnSnapshot(t)
		if err := s.Steer(ctx, core.User("anyone?")); !errors.Is(err, thread.ErrNotRunning) {
			t.Fatalf("Steer on a reopened mid-turn snapshot = %v, want ErrNotRunning", err)
		}
		if after := len(s.Entries()); after != before {
			t.Errorf("a refused Steer wrote: %d entries, had %d", after, before)
		}
	})
}

// midTurnSnapshot is what a crash mid-turn leaves: a jsonl session
// whose turn's prompt is durable and whose turn end never landed,
// reopened (by a fresh Storage, as a restarted process would). It
// returns the reopened Session and its entry count.
func midTurnSnapshot(t *testing.T) (*thread.Session, int) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := newSteerHeld()
	agent := core.New(m)
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, core.User("first")); err != nil {
		t.Fatal(err)
	}
	<-m.held
	// The process "dies" here: a second Storage on the same directory
	// reads the file as it stands — the prompt, no turn end.
	st2, err := jsonl.Open(dir, thread.NoLock())
	if err != nil {
		t.Fatal(err)
	}
	re, err := thread.Open(ctx, st2, s.ID(), core.New(wefttest.Script(wefttest.Say("x"))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(m.gate)
		_ = s.Close(ctx)
	})
	return re, len(re.Entries())
}

// countTurns counts the session's turn entries.
func countTurns(s *thread.Session) int {
	n := 0
	for _, e := range s.Entries() {
		if _, ok := e.(thread.TurnEntry); ok {
			n++
		}
	}
	return n
}
