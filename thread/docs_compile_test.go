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

	// README: the v0.4 block — the watcher tail and the list filters.
	w := st.(thread.Watcher)
	wctx, wcancel := context.WithCancel(ctx)
	wseq, err := w.Watch(wctx, s.ID(), turn.ID())
	if err != nil {
		t.Fatal(err)
	}
	for range wseq {
		wcancel()
		break
	}
	wcancel()
	page, err := st.List(ctx, thread.Query{
		Meta:        map[string]string{"env": "prod"},
		TitleSearch: "checkout",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Sessions
	_ = page.Total

	// README: the approvals block. A gated tool parks; Pending,
	// Decide, Turn.Next, the grant, and the signed exchange all exist
	// exactly as written.
	apAgent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "deploy"}), wefttest.Say("done")),
		weft.Tool("deploy", "Deploy.", func(context.Context, struct{}) (string, error) {
			return "deployed", nil
		}, weft.RequireApproval()))
	s4, err := thread.Create(ctx, thread.Memory(), apAgent)
	if err != nil {
		t.Fatal(err)
	}
	apTurn, err := s4.Send(ctx, weft.User("Deploy to prod."))
	if err != nil {
		t.Fatal(err)
	}
	apRes, err := apTurn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	_ = apRes
	for _, r := range s4.Pending() {
		notify := r.CallID
		_ = notify
	}
	if pend := s4.Pending(); len(pend) > 0 {
		rt, err := s4.Decide(ctx, thread.Approve(pend[0].CallID))
		if err != nil {
			t.Fatal(err)
		}
		if rt != nil {
			if _, err := rt.Wait(); err != nil {
				t.Fatal(err)
			}
		}
		follow := apTurn.Next()
		_ = follow
	}
	if err := s4.Grant(ctx, thread.Grant{
		Tool: "run",
		Args: []thread.Arg{thread.ArgGlob("/command", "go test*")},
	}); err != nil {
		t.Fatal(err)
	}
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: []byte("docs secret, sixteen bytes"), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	apAgent2 := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "deploy"}), wefttest.Say("done")),
		weft.Tool("deploy", "Deploy.", func(context.Context, struct{}) (string, error) {
			return "deployed", nil
		}, weft.RequireApproval()))
	s5, err := thread.Create(ctx, thread.Memory(), apAgent2, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	sdTurn, err := s5.Send(ctx, weft.User("Deploy again."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdTurn.Wait(); err != nil {
		t.Fatal(err)
	}
	if pend := s5.Pending(); len(pend) > 0 {
		r, err := s5.Request(pend[0].CallID)
		if err != nil {
			t.Fatal(err)
		}
		sd := thread.SignDecision([]byte("docs secret, sixteen bytes"), r, thread.Approve(pend[0].CallID))
		rt, err := s5.DecideSigned(ctx, sd)
		if err != nil {
			t.Fatal(err)
		}
		_ = rt
	}
}
