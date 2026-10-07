// Command session walks one weft/thread session through its whole
// life: two turns, a label, a branch off the first answer, a fork of
// the branch, a manual compaction with preview, a close, and a reopen
// from disk — everything on a JSONL backend, everything offline
// through a scripted model, every id deterministic.
//
// By default the session files go to a fresh temporary directory that
// is removed when the command exits, so it can be run any number of
// times; -dir keeps them somewhere you can read them.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

func main() {
	dir := flag.String("dir", "", "keep the session files in this directory, which must not already hold\nthe example's sessions (default: a temporary directory, removed at exit)")
	flag.Parse()
	if err := runIn(os.Stdout, *dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runIn runs the example in dir, or — when dir is empty — in a fresh
// temporary directory it removes afterwards: the ids are fixed, so a
// directory that already holds s_demo would fail the Create.
func runIn(w io.Writer, dir string) (err error) {
	if dir == "" {
		dir, err = os.MkdirTemp("", "weft-session-example-")
		if err != nil {
			return err
		}
		defer func() {
			if rerr := os.RemoveAll(dir); rerr != nil && err == nil {
				err = rerr
			}
		}()
	}
	return run(w, dir)
}

// p prints one line to the example's writer; a failed write ends the
// example with the error, the way a closed pipe ends a CLI.
func p(w io.Writer, a ...any) error {
	_, err := fmt.Fprintln(w, a...)
	return err
}

// run is the example; opts are extra agent options (its test passes a
// LoggerProvider to watch the compaction marker the session emits).
func run(w io.Writer, dir string, opts ...weft.Option) error {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(
		wefttest.Say("Order 1234 shipped Tuesday, tracking 1Z89."),
		wefttest.Say("Draft two: same facts, tighter opening."),
		wefttest.Say("Goal: answer order-status questions.\nProgress: order 1234 shipped Tuesday."),
	), opts...)

	st, err := jsonl.Open(dir)
	if err != nil {
		return err
	}

	// Deterministic ids: one per mint, in order — the session id
	// first, then each entry as it is appended.
	next := 0
	ids := []string{
		"s_demo",     // the session
		"e_t1prompt", // turn 1: prompt, reply, ledger
		"e_t1reply",
		"e_t1ledger",
		"e_label",    // the label
		"e_cart",     // the custom state
		"e_branch",   // the branch navigation
		"e_t2prompt", // turn 2 on the branch
		"e_t2reply",
		"e_t2ledger",
		"e_compaction",
	}
	mint := thread.IDs(func() string { id := ids[next]; next++; return id })

	// A small keep window, so a three-message session has something to
	// compact and the example shows a real preview.
	s, err := thread.Create(ctx, st, agent, mint, thread.KeepRecent(50))
	if err != nil {
		return err
	}

	turn1, err := s.Send(ctx, weft.User("Where is order 1234?"))
	if err != nil {
		return err
	}
	res1, err := turn1.Wait()
	if err != nil {
		return err
	}
	if err := p(w, "turn 1:", res1.Text()); err != nil {
		return err
	}
	if err := p(w, "receipt:", turn1.ID(), "run:", turn1.RunID()); err != nil {
		return err
	}
	// Wait returns when the turn is decided; the runner may still be
	// between items, and a Send in that window is accepted with a
	// receipt entry first — one more id. The ids above are fixed, so
	// wait for the session to be idle.
	if err := s.WaitIdle(ctx); err != nil {
		return err
	}

	if err := s.Label(ctx, turn1.ID(), "the shipping answer"); err != nil {
		return err
	}
	if err := s.Custom(ctx, "cart", []byte(`{"items":2}`)); err != nil {
		return err
	}

	// Branch back to the first answer's prompt and redraft from there.
	if err := s.Branch(ctx, turn1.ID()); err != nil {
		return err
	}
	turn2, err := s.Send(ctx, weft.User("Redraft that, tighter."))
	if err != nil {
		return err
	}
	if _, err := turn2.Wait(); err != nil {
		return err
	}
	if err := p(w, "branched; context holds", len(s.Context()), "messages"); err != nil {
		return err
	}

	// Fork the whole branch: a new session, self-contained, its
	// header naming the origin.
	fork, err := s.Fork(ctx, s.Leaf(), thread.IDs(func() string { return "s_fork" }))
	if err != nil {
		return err
	}
	if err := p(w, "fork:", fork.ID(), "carries", len(fork.Context()), "messages"); err != nil {
		return err
	}

	// Compaction with preview: the plan names the cut and the summary;
	// applying writes it, nothing is deleted.
	plan, err := s.PreviewCompaction(ctx)
	if err != nil {
		return err
	}
	if err := p(w, "preview: first kept", plan.FirstKept, "tokens before", plan.TokensBefore); err != nil {
		return err
	}
	if err := s.ApplyCompaction(ctx, plan); err != nil {
		return err
	}
	if err := p(w, "compacted; context holds", len(s.Context()), "messages; file holds", len(s.Entries()), "entries"); err != nil {
		return err
	}

	// One Session per session id: close this one — it drains, seals
	// and lets go of the file — before the session is opened again.
	if err := s.Close(ctx); err != nil {
		return err
	}

	// Reopen from disk: the same session, the same context.
	again, err := thread.Open(ctx, st, s.ID(), agent, thread.KeepRecent(50))
	if err != nil {
		return err
	}
	return p(w, "reopened:", len(again.Context()), "messages, leaf", again.Leaf() != "")
}
