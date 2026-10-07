package thread_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// heldBetweenItems builds a session whose runner, after its first
// turn is decided, stays between items until release closes: the
// post-turn compaction trigger fires (the first reply reports 95k of
// a 100k window) and its BeforeCompact hook blocks, then cancels. ids
// records every id the session draws. entered closes once the runner
// is held.
func heldBetweenItems(t *testing.T) (s *thread.Session, ids func() []string, entered, release chan struct{}) {
	t.Helper()
	ctx := context.Background()
	var mu sync.Mutex
	var drawn []string
	n := 0
	mint := thread.IDs(func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		id := fmt.Sprintf("e_%02d", n)
		if n == 1 {
			id = "s_held"
		}
		drawn = append(drawn, id)
		return id
	})
	entered, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	hook := thread.BeforeCompact(func(ctx context.Context, _ *thread.Preparation) (thread.Verdict, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return thread.Cancel(), nil
	})
	agent := core.New(wefttest.Script(
		wefttest.Say(strings.Repeat("first ", 1000)).WithUsage(core.Usage{InputTokens: 95_000, OutputTokens: 5}),
		wefttest.Say("second"),
	))
	s, err := thread.Create(ctx, thread.Memory(), agent, mint, hook,
		thread.ContextWindow(100_000), thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	return s, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), drawn...)
	}, entered, release
}

func accepted(s *thread.Session) []thread.ReceiptEntry {
	var out []thread.ReceiptEntry
	for _, e := range s.Entries() {
		if r, ok := e.(thread.ReceiptEntry); ok && r.Status == thread.ReceiptAccepted {
			out = append(out, r)
		}
	}
	return out
}

// A turn's Wait returns when the turn is decided, before the runner's
// between-turn work (ADR 0020 §2). A Send in that window is accepted —
// one more id, an accepted receipt entry — and runs next; after
// WaitIdle the same Send takes the idle path and writes its prompt
// alone. This is the window that made ExampleSession_Path and
// ExampleSession_Send flaky under load: their fixed id lists had no id
// for the receipt, and the turn's end drew past the list ("index out
// of range [8] with length 8"). The examples now WaitIdle before
// their second Send.
func TestSendBetweenItems(t *testing.T) {
	ctx := context.Background()
	t.Run("accepted with a receipt", func(t *testing.T) {
		s, ids, entered, release := heldBetweenItems(t)
		t1, _ := s.Send(ctx, core.User("one"))
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the post-turn trigger never ran: the runner is not held between items")
		}
		before := len(ids())
		t2, err := s.Send(ctx, core.User("two"))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(ids()) - before; got != 2 {
			t.Errorf("the Send drew %d ids, want 2 (its turn's id and the accepted receipt's)", got)
		}
		if rs := accepted(s); len(rs) != 1 || rs[0].Turn != t2.ID() {
			t.Errorf("accepted receipts = %+v, want one naming the second turn", rs)
		}
		close(release)
		if res, err := t2.Wait(); err != nil || res.Text() != "second" {
			t.Fatalf("second turn: %v, %v", res, err)
		}
	})
	t.Run("WaitIdle closes the window", func(t *testing.T) {
		s, ids, entered, release := heldBetweenItems(t)
		t1, _ := s.Send(ctx, core.User("one"))
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		<-entered
		go func() {
			time.Sleep(10 * time.Millisecond)
			close(release)
		}()
		if err := s.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		before := len(ids())
		t2, err := s.Send(ctx, core.User("two"))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(ids()) - before; got != 1 {
			t.Errorf("the Send drew %d ids, want 1 (its prompt entry's)", got)
		}
		if rs := accepted(s); len(rs) != 0 {
			t.Errorf("accepted receipts = %+v, want none on the idle path", rs)
		}
		if _, err := t2.Wait(); err != nil {
			t.Fatal(err)
		}
	})
}
