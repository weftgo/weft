package thread_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/pool"
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
	// README: Close before reopening — one Session writes a session.
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	_ = again

	// README and AGENTS block 8: the three ways to wait on a turn, the
	// outcome, and WaitIdle.
	if _, err := turn.WaitContext(ctx); err != nil {
		t.Fatal(err)
	}
	<-turn.Done()
	if got := turn.Outcome(); got != thread.TurnAnswered {
		t.Fatalf("outcome %v, want answered", got)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, weft.User("after close")); !errors.Is(err, thread.ErrClosed) {
		t.Fatalf("Send on a closed session: %v, want ErrClosed", err)
	}

	// README: the compaction layers.
	s2, err := thread.Create(ctx, st, agent,
		thread.ContextWindow(200_000),
		thread.SummaryModel(wefttest.Script()),
		thread.SummaryFocus("keep file paths"),
		thread.ClearOldToolResults(4),
		thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			return thread.Proceed(), nil
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
	if err := s2.Compact(ctx, thread.SummaryInstructions("focus on the API design")); err != nil {
		t.Log("a fresh session has nothing to compact — fine:", err)
	}
	if err := s2.Uncompact(ctx); err == nil {
		t.Log("nothing to undo in a fresh session — fine")
	}
	// README and AGENTS block 8: the other verdicts and the off switch
	// exist under these names.
	_ = thread.Cancel()
	_ = thread.Replace(plan)
	_ = thread.NoAutoCompact()

	// AGENTS block 8: the busy policy and the run options carry.
	s3, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Reject))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s3.Send(ctx, weft.User("x"), thread.RunOptions(weft.Metadata(map[string]string{"tenant": "acme"})))
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
	if err := again.Compact(ctx, thread.SummaryInstructions("focus on the API design")); err != nil {
		t.Log("a small session has nothing to compact — fine:", err)
	}

	// README: the watcher tail and the list filters.
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
	secret := []byte("docs secret, sixteen bytes")
	key := thread.Key{ID: "k1", Secret: secret, Active: true}
	ring, err := thread.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	ask := func(context.Context, thread.Request) (thread.Decision, bool) {
		return thread.Decision{}, false // no terminal here: decline, and the call parks
	}
	apAgent2 := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "deploy"}), wefttest.Say("done")),
		weft.Tool("deploy", "Deploy.", func(context.Context, struct{}) (string, error) {
			return "deployed", nil
		}, weft.RequireApproval()))
	s5, err := thread.Create(ctx, thread.Memory(), apAgent2,
		thread.WithKeyring(ring),
		thread.RequireSigned(),
		thread.WithApprover(ask, 30*time.Second))
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
		sd, err := ring.Sign(r, thread.Approve(pend[0].CallID))
		if err != nil {
			t.Fatal(err)
		}
		// AGENTS block 8: key.Sign is the same exchange for an approver
		// holding one key.
		if _, err := key.Sign(r, thread.Approve(pend[0].CallID)); err != nil {
			t.Fatal(err)
		}
		rt, err := s5.DecideSigned(ctx, sd)
		if err != nil {
			t.Fatal(err)
		}
		_ = rt
		// README: RequireSigned closes the unsigned door.
		if _, err := s5.Decide(ctx, thread.Approve(pend[0].CallID)); !errors.Is(err, thread.ErrSignatureRequired) {
			t.Fatalf("Decide under RequireSigned: %v, want ErrSignatureRequired", err)
		}
	}

	// README: the steering block. On an idle session every policy runs
	// a plain turn; the calls and the names are what this pins.
	stAgent := weft.New(wefttest.Script(
		wefttest.Say("r"), wefttest.Say("r"), wefttest.Say("r")))
	s6, err := thread.Create(ctx, thread.Memory(), stAgent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steer, err := s6.Send(ctx, weft.User("wait — metric units"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := steer.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = steer.Outcome()
	_ = steer.Next()
	_ = s6.Queue()
	if _, err := s6.ClearQueue(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []thread.Policy{thread.Interrupt, thread.Rollback} {
		tn, err := s6.Send(ctx, weft.User("stop, do this instead"), thread.As(p))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if next, err := s6.Continue(ctx); err != nil || next != nil {
		t.Fatalf("Continue with nothing waiting: %v, %v", next, err)
	}
	_ = thread.ReRunOnOverflow(false)
	_ = thread.ErrDropped

	// README: the pool block.
	researcher := weft.New(wefttest.Script(wefttest.Say("found")))
	pl := pool.New(4)
	research := pl.MustWrap("research", "Research a topic.", researcher)
	_ = research
	ps, err := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := pl.Submit(ctx, ps, researcher, "survey the options")
	if err != nil {
		t.Fatal(err)
	}
	rc2, err := pl.Wait(ctx, ps, rc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rc2.Settled() {
		t.Fatalf("receipt at rest and not parked should be settled: %v", rc2.State)
	}
	if err := pl.Decide(ctx, ps, thread.Approve("call_nobody_parked")); err == nil {
		t.Fatal("Decide for a call that is not pending returned nil")
	}
	if err := pl.Recover(ctx, ps); err != nil {
		t.Fatal(err)
	}
	// AGENTS block 8: the rest of the pool's names, as written there.
	if _, err := pl.Wrap("research2", "Research a topic.", researcher, pool.Async()); err != nil {
		t.Fatal(err)
	}
	_ = pool.New(1, pool.MaxDepth(2))
	_ = pl.Cancel(ctx, ps, rc.ID)                     // settled already: a *StateError
	_, _ = pl.Forward(ctx, ps, rc.ID, weft.User("x")) // the same
	_ = pool.Receipts(ps)
	if _, err := pool.Children(ctx, ps); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Descendants(ctx, ps); err != nil {
		t.Fatal(err)
	}
	if err := pl.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// AGENTS block 8: the package-level calls, the keyset cursor, and
	// the open options every backend takes.
	lp, err := thread.List(ctx, st, thread.Query{Before: time.Now(), BeforeID: "s_zzz", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	_ = lp
	st2, err := jsonl.Open(t.TempDir(), thread.Salvage(), thread.FsyncOnFlush(), thread.NoLock(), thread.OpenLogger(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	gone, err := thread.Create(ctx, st2, agent, thread.PublicID("pub_docs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := gone.SetInfo(ctx, "", map[string]string{"weft.public_id": "other"}); !errors.Is(err, thread.ErrReservedKey) {
		t.Fatalf("SetInfo of a weft. key: %v, want ErrReservedKey", err)
	}
	_ = gone.Context()
	_ = gone.Entries()
	if _, err := gone.Path(""); err != nil {
		t.Fatal(err)
	}
	_ = gone.Audit()
	if err := gone.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := thread.Delete(ctx, st2, gone.ID()); err != nil {
		t.Fatal(err)
	}
	_ = []error{thread.ErrBusy, thread.ErrLocked, thread.ErrStale, thread.ErrClosed, thread.ErrCorrupt, thread.ErrNewerFormat}
	_ = []any{thread.Quorum(2), thread.RequestExpiry(time.Minute), thread.OnRequest(func(thread.Request) {}),
		thread.Deny, thread.Resolve, thread.ResolveError, thread.ApproveAlways, thread.SummarizeLeft}
	_ = s4.Resume
	_ = s4.Revoke
}
