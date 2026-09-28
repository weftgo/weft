package thread_test

import (
	"context"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// The README's Sessions section and AGENTS.md block 8, copied line for
// line (placeholders filled, names unchanged) — the docs' code blocks
// compile against the API they describe, and this test breaks when
// either side drifts.
func TestDocsSessionsBlocksCompile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// README: the session loop.
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	agent := weft.New(wefttest.Script(
		wefttest.Say("r"), wefttest.Say("r"), wefttest.Say("r"), wefttest.Say("r")))
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("Where is order 1234?"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	for ev, err := range turn.Events() {
		if err != nil {
			t.Fatal(err)
		}
		_ = ev
	}
	if err := s.Branch(ctx, turn.ID()); err != nil {
		t.Fatal(err)
	}
	fork, err := s.Fork(ctx, turn.ID())
	if err != nil {
		t.Fatal(err)
	}
	_ = fork
	again, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	_ = again

	// README: the compaction layers.
	s2, err := thread.Create(ctx, st, agent,
		thread.ContextWindow(200_000),
		thread.SummaryModel(wefttest.Script()),
		thread.SummaryFocus("keep file paths"),
		thread.ClearOldToolResults(4),
		thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			return thread.Proceed, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s2.PreviewCompaction(ctx)
	if err == nil {
		if err := s2.ApplyCompaction(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if err := s2.Uncompact(ctx); err == nil {
		t.Log("nothing to undo in a fresh session — fine")
	}

	// AGENTS block 8: the busy policy and the run options carry.
	s3, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Reject))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s3.Send(ctx, weft.User("x"), thread.RunOptions(weft.Deny("call_1", "not now")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queued.Wait(); err != nil {
		t.Fatal(err)
	}

	// AGENTS block 8: the bookkeeping appends, on the session that
	// holds the entry.
	if err := again.Label(ctx, turn.ID(), "bookmark"); err != nil {
		t.Fatal(err)
	}
	if err := again.SetInfo(ctx, "title", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if err := again.Custom(ctx, "kind", nil); err != nil {
		t.Fatal(err)
	}
	if err := again.CustomMessage(ctx, "kind", weft.User("note")); err != nil {
		t.Fatal(err)
	}
	if err := again.Pin(ctx, turn.ID()); err != nil {
		t.Fatal(err)
	}
	if err := again.Compact(ctx, thread.Instructions("focus on the API design")); err != nil {
		t.Log("a small session has nothing to compact — fine:", err)
	}
}
