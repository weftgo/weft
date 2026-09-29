package thread_test

import (
	"context"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// The Delegated bucket sums settled pool receipts only (ADR 0022 D3):
// acceptance and running entries carry no usage, every settlement
// adds exactly once.
func TestUsageDelegatedBucket(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		s, err := thread.Create(ctx, st, weft.New(wefttest.Script()))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		accept, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
			Status: thread.PoolAccepted, Child: "s_child", Prompt: "go",
		})
		if err != nil {
			t.Fatalf("acceptance: %v", err)
		}
		if _, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
			Receipt: accept.ID, Status: thread.PoolRunning, Child: "s_child",
		}); err != nil {
			t.Fatalf("running: %v", err)
		}
		if s.Usage().Delegated.InputTokens != 0 {
			t.Errorf("unsettled receipt billed: %+v", s.Usage().Delegated)
		}
		if _, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
			Receipt: accept.ID, Status: thread.PoolDone, Child: "s_child",
			Stop:  "the answer",
			Usage: weft.Usage{InputTokens: 10, OutputTokens: 5},
		}); err != nil {
			t.Fatalf("settlement: %v", err)
		}
		u := s.Usage()
		if u.Delegated.InputTokens != 10 || u.Delegated.OutputTokens != 5 {
			t.Errorf("Delegated = %+v", u.Delegated)
		}
		if u.Turns.InputTokens != 0 || u.Summaries.InputTokens != 0 {
			t.Errorf("Delegated leaked into another bucket: %+v", u)
		}
		// The minted acceptance id is the handle, and the stored entry
		// round-trips through the tree.
		var found int
		for _, e := range s.Entries() {
			if _, ok := e.(thread.PoolReceiptEntry); ok {
				found++
			}
		}
		if found != 3 {
			t.Errorf("pool receipt entries = %d, want 3", found)
		}
	})
}

// A run's context carries its session; nothing else does (ADR 0022
// §2 — the pool's parent lookup).
func TestSessionFromContext(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		if thread.SessionFromContext(ctx) != nil {
			t.Errorf("a bare context has a session")
		}
		var inside, outside *thread.Session
		tool := weft.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
			inside = thread.SessionFromContext(ctx)
			return "probed", nil
		})
		agent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{}`}),
			wefttest.Say("done"),
		), tool)
		if _, err := tool.Invoke(ctx, []byte(`{}`)); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		outside = thread.SessionFromContext(ctx)
		if outside != nil {
			t.Errorf("Invoke outside a run saw a session")
		}
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		turn, err := s.Send(ctx, weft.User("probe"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		if inside == nil {
			t.Fatalf("the tool's run context carried no session")
		}
		if inside.ID() != s.ID() {
			t.Errorf("session on context = %s, want %s", inside.ID(), s.ID())
		}
	})
}

// The lineage: set at Create, read back from the file, ignored by
// Open's options (the header is the truth).
func TestWithLineage(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script())
		s, err := thread.Create(ctx, st, agent, thread.WithLineage("s_parent", "call_1"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		lin := s.Lineage()
		if lin.Session != "s_parent" || lin.Call != "call_1" {
			t.Errorf("Lineage = %+v", lin)
		}
		open, err := thread.Open(ctx, st, s.ID(), agent, thread.WithLineage("s_other", ""))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got := open.Lineage(); got.Session != "s_parent" || got.Call != "call_1" {
			t.Errorf("Lineage after reopen = %+v; Open must not rewrite it", got)
		}
		plain, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		if (plain.Lineage() != thread.Lineage{}) {
			t.Errorf("a session with no lineage = %+v", plain.Lineage())
		}
	})
}
