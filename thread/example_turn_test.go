package thread_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// slowTool returns a tool that blocks until release is closed or its
// context ends, and a channel closed when its first call has started —
// the examples' stand-in for a long piece of work.
func slowTool(name string) (tool *weft.ToolDef, started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	tool = weft.Tool(name, "Takes a while.", func(ctx context.Context, _ struct{}) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return name + " finished", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	return tool, started, release
}

// Interrupt cancels the running turn and runs the new message next.
// The interrupted turn stays in the transcript, its unanswered call
// carrying the interruption text, so the model sees what was cut off.
func ExampleInterrupt() {
	ctx := context.Background()
	research, started, _ := slowTool("research")
	agent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "call_1"}),
		wefttest.Say("The short version: two tides a day."),
	), research)
	s, _ := thread.Create(ctx, thread.Memory(), agent)

	long, _ := s.Send(ctx, weft.User("Research everything about tides."))
	<-started
	short, err := s.Send(ctx, weft.User("Stop. Just the short version."), thread.As(thread.Interrupt))
	if err != nil {
		fmt.Println(err)
		return
	}

	_, err = long.Wait()
	fmt.Println("first turn:", long.Outcome(), "-", errors.Is(err, context.Canceled))
	res, err := short.Wait()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("second turn:", res.Text())
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				fmt.Println("the model reads:", r.Content)
			}
		}
	}
	// Output:
	// first turn: canceled - true
	// second turn: The short version: two tides a day.
	// the model reads: tool call research was interrupted: the run was canceled for a newer message
}

// Rollback is an Interrupt that also returns the leaf to where it was
// before the interrupted turn: the new message runs as though the
// interrupted one had never been sent. Nothing is deleted — the
// interrupted turn keeps its own line of the tree.
func ExampleRollback() {
	ctx := context.Background()
	research, started, _ := slowTool("research")
	agent := weft.New(wefttest.Script(
		wefttest.Say("Hello."),
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "call_1"}),
		wefttest.Say("Tides, briefly: two a day."),
	), research)
	s, _ := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Rollback))

	hello, _ := s.Send(ctx, weft.User("Hi."))
	if _, err := hello.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	wrong, _ := s.Send(ctx, weft.User("Research everything about tides."))
	<-started
	right, _ := s.Send(ctx, weft.User("Sorry — tides, briefly.")) // the session's policy: Rollback
	_, _ = wrong.Wait()
	if _, err := right.Wait(); err != nil {
		fmt.Println(err)
		return
	}

	for _, m := range s.Context() {
		fmt.Printf("%s: %s\n", m.Role, m.Text())
	}
	kept := 0
	for _, e := range s.Entries() {
		if m, ok := e.(thread.MessageEntry); ok && m.Message.Text() == "Research everything about tides." {
			kept++
		}
	}
	fmt.Println("interrupted prompt still in the tree:", kept == 1)
	// Output:
	// user: Hi.
	// assistant: Hello.
	// user: Sorry — tides, briefly.
	// assistant: Tides, briefly: two a day.
	// interrupted prompt still in the tree: true
}

// Steer delivers a message into the turn that is running: the model
// sees it after the current step's tool results, in the same run. The
// Send's Turn is the message's receipt — it ends when the message is
// delivered, and the answer is the running turn's.
func ExampleSteer() {
	ctx := context.Background()
	var s *thread.Session
	var steer *thread.Turn
	// The user types while the tool is running.
	convert := weft.Tool("convert", "Convert a length.", func(ctx context.Context, _ struct{}) (string, error) {
		steer, _ = s.Send(ctx, weft.User("Use metric units."), thread.As(thread.Steer))
		return "5 miles", nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "convert", ID: "call_1"}),
		wefttest.Say("That is about 8 kilometres."),
	)
	s, _ = thread.Create(ctx, thread.Memory(), weft.New(model, convert))

	turn, _ := s.Send(ctx, weft.User("How far is the lighthouse?"))
	res, err := turn.Wait()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("steer:", steer.Outcome())
	fmt.Println("the model's second request ends with:", model.LastRequest().LastText())
	fmt.Println("answer:", res.Text())
	// Output:
	// steer: delivered
	// the model's second request ends with: Use metric units.
	// answer: That is about 8 kilometres.
}

// A steer that cannot be delivered — here the session is waiting on an
// approval, and a steer never decides a parked call — becomes a
// follow-up turn. Turn.Next is that turn: it runs once the boundary
// resolves.
func ExampleTurn_Next() {
	ctx := context.Background()
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "deploy", ID: "call_1"}),
			wefttest.Say("Not deployed."),
			wefttest.Say("Rolled back to v41."),
		),
		weft.Tool("deploy", "Deploy the service.", func(context.Context, struct{}) (string, error) {
			return "deployed", nil
		}, weft.RequireApproval()),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent)

	parked, _ := s.Send(ctx, weft.User("Deploy v42."))
	if _, err := parked.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("first turn:", parked.Outcome())

	steer, _ := s.Send(ctx, weft.User("Actually, roll back to v41."), thread.As(thread.Steer))
	_, _ = steer.Wait()
	fmt.Println("steer:", steer.Outcome())
	followUp := steer.Next()

	resume, _ := s.Decide(ctx, thread.Deny("call_1", "superseded"))
	if _, err := resume.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	res, err := followUp.Wait()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("follow-up:", res.Text())
	// Output:
	// first turn: parked
	// steer: deferred
	// follow-up: Rolled back to v41.
}

// Events yields the turn's run events as the session observes them.
// The session drives the run, not the reader: breaking out of the
// range cancels nothing, and Wait still returns the result.
func ExampleTurn_Events() {
	ctx := context.Background()
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup", ID: "call_1"}),
			wefttest.Say("Order 1234 shipped."),
		),
		weft.Tool("lookup", "Look up an order.", func(context.Context, struct{}) (string, error) {
			return "shipped", nil
		}),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	turn, _ := s.Send(ctx, weft.User("Where is order 1234?"))

	var text strings.Builder
	for ev, err := range turn.Events() {
		if err != nil {
			fmt.Println("run failed:", err) // at most once, as the last element
			break
		}
		switch ev := ev.(type) {
		case weft.ToolStart:
			fmt.Println("tool:", ev.Name)
		case weft.ToolFinish:
			fmt.Println("result:", ev.Content)
		case weft.TextDelta:
			text.WriteString(ev.Text)
		case weft.RunFinish:
			fmt.Println("steps:", ev.Steps)
		}
	}
	fmt.Println("text:", text.String())
	res, _ := turn.Wait()
	fmt.Println("same answer from Wait:", res.Text() == text.String())
	// Output:
	// tool: lookup
	// result: shipped
	// steps: 2
	// text: Order 1234 shipped.
	// same answer from Wait: true
}

// Queue lists what the session has accepted and not yet given to the
// model — steers waiting for the running turn, sends waiting for a
// turn of their own. Both are durable from the moment Send returns;
// ClearQueue drops them, and their Turns say so.
func ExampleSession_Queue() {
	ctx := context.Background()
	work, started, release := slowTool("work")
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "work", ID: "call_1"}),
		wefttest.Say("Done."),
	)
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(model, work))

	running, _ := s.Send(ctx, weft.User("Do the long job."))
	<-started
	later, _ := s.Send(ctx, weft.User("Then email me the result."))                   // Queue, the default
	nudge, _ := s.Send(ctx, weft.User("Skip the appendix."), thread.As(thread.Steer)) // into the running turn

	for _, q := range s.Queue() {
		fmt.Printf("%s: %s\n", q.Policy, q.Msg.Text())
	}
	n, err := s.ClearQueue(ctx)
	fmt.Println("dropped:", n, err)

	close(release)
	res, _ := running.Wait()
	fmt.Println("running turn:", res.Text())
	_, err = later.Wait()
	fmt.Println("queued send:", later.Outcome(), "-", errors.Is(err, thread.ErrDropped))
	fmt.Println("steer:", nudge.Outcome())
	fmt.Println("model calls:", len(model.Requests()))
	// Output:
	// steer: Skip the appendix.
	// queue: Then email me the result.
	// dropped: 2 <nil>
	// running turn: Done.
	// queued send: dropped - true
	// steer: dropped
	// model calls: 2
}

// RunOptions carries per-run configuration into one turn's run —
// metadata here. What the session owns cannot be overridden: the
// transcript, the run id, the steering source and approval decisions
// are refused before anything is written.
func ExampleRunOptions() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(wefttest.Say("Hello, Acme.")),
		weft.Tap(func(ctx context.Context, ev weft.Event) {
			if _, ok := ev.(weft.RunStart); ok {
				fmt.Println("run for tenant:", weft.MetadataFromContext(ctx)["tenant"])
			}
		}))
	s, _ := thread.Create(ctx, thread.Memory(), agent)

	_, err := s.Send(ctx, weft.User("Hi."), thread.RunOptions(weft.Prompt("something else")))
	fmt.Println("refused:", errors.Is(err, weft.ErrInvalidRunOption))

	turn, err := s.Send(ctx, weft.User("Hi."),
		thread.RunOptions(weft.Metadata(map[string]string{"tenant": "acme"})))
	if err != nil {
		fmt.Println(err)
		return
	}
	res, _ := turn.Wait()
	fmt.Println(res.Text())
	// Output:
	// refused: true
	// run for tenant: acme
	// Hello, Acme.
}

// Done is the channel form of Wait, for a select: wait for the turn or
// for something else, whichever comes first. Giving up the wait does
// not stop the turn.
func ExampleTurn_Done() {
	ctx := context.Background()
	work, started, release := slowTool("work")
	agent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "work", ID: "call_1"}),
		wefttest.Say("Finished."),
	), work)
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	turn, _ := s.Send(ctx, weft.User("Do the long job."))
	<-started

	select {
	case <-turn.Done():
		fmt.Println("done already")
	case <-time.After(10 * time.Millisecond):
		fmt.Println("still running:", turn.Outcome())
	}

	close(release)
	<-turn.Done()
	res, err := turn.Wait() // returns at once
	fmt.Println(res.Text(), err)
	// Output:
	// still running: running
	// Finished. <nil>
}

// WaitContext bounds the wait, not the turn: when the context ends
// first the caller gets its error back and the turn keeps running.
func ExampleTurn_WaitContext() {
	ctx := context.Background()
	work, started, release := slowTool("work")
	agent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "work", ID: "call_1"}),
		wefttest.Say("Finished."),
	), work)
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	turn, _ := s.Send(ctx, weft.User("Do the long job."))
	<-started

	short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, err := turn.WaitContext(short)
	fmt.Println("gave up waiting:", errors.Is(err, context.DeadlineExceeded))

	close(release)
	res, err := turn.WaitContext(ctx)
	fmt.Println(res.Text(), err)
	// Output:
	// gave up waiting: true
	// Finished. <nil>
}
