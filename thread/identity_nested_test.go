package thread_test

// A session's run carries its own identity, never an enclosing run's: a
// turn started inside another run (another session's tool handler, a
// pool child's turn) inherits the caller's other pairs through the
// context, but the keys a session owns — weft.public_id, the fork origin,
// the pool lineage — are its own or absent. A plain Generate in the same
// handler is the enclosing turn's work and still inherits them all.

import (
	"context"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

type nestedIn struct{}

// nestedSendTool runs one turn of inner (a session of its own) from a
// tool handler, on the handler's context, and then one plain Generate
// of plain on the same context.
func nestedSendTool(inner *thread.Session, plain *core.Agent) *core.ToolDef {
	return core.Tool("nested", "Ask the inner session.", func(ctx context.Context, _ nestedIn) (string, error) {
		turn, err := inner.Send(ctx, core.User("inner question"))
		if err != nil {
			return "", err
		}
		if _, err := turn.Wait(); err != nil {
			return "", err
		}
		if _, err := plain.Generate(ctx, core.Prompt("plain work")); err != nil {
			return "", err
		}
		return "ok", nil
	})
}

func TestNestedSessionRunCarriesNoEnclosingPublicID(t *testing.T) {
	ctx := context.Background()
	innerCol, plainCol, outerCol := &mdCollector{}, &mdCollector{}, &mdCollector{}
	inner, err := thread.Create(ctx, thread.Memory(),
		core.New(wefttest.Script(wefttest.Say("inner reply")), innerCol.tap()))
	if err != nil {
		t.Fatalf("Create inner: %v", err)
	}
	plain := core.New(wefttest.Script(wefttest.Say("plain reply")), plainCol.tap())
	outer, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "nested", Args: `{}`}),
		wefttest.Say("outer reply"),
	), outerCol.tap(), nestedSendTool(inner, plain)), thread.PublicID("pub_s"))
	if err != nil {
		t.Fatalf("Create outer: %v", err)
	}
	turn, err := outer.Send(ctx, core.User("outer question"),
		thread.RunOptions(core.Metadata(map[string]string{"tenant": "acme"})))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if mds := outerCol.snapshot(); len(mds) != 1 || mds[0]["weft.public_id"] != "pub_s" {
		t.Fatalf("outer runs = %v, want one carrying weft.public_id pub_s", mds)
	}
	mds := innerCol.snapshot()
	if len(mds) != 1 {
		t.Fatalf("inner runs = %d, want 1", len(mds))
	}
	md := mds[0]
	if v, ok := md["weft.public_id"]; ok {
		t.Errorf("inner session's run carries weft.public_id = %q, want none: the session has no public id", v)
	}
	if md["weft.session.id"] != inner.ID() || md["weft.turn"] != "1" {
		t.Errorf("inner run identity = %v, want its own session %s, turn 1", md, inner.ID())
	}
	if md["tenant"] != "acme" {
		t.Errorf("inner run tenant = %q, want acme: the caller's pairs still ride in", md["tenant"])
	}
	// The plain Generate is the outer turn's work: it inherits everything.
	pm := plainCol.snapshot()
	if len(pm) != 1 || pm[0]["weft.public_id"] != "pub_s" || pm[0]["weft.session.id"] != outer.ID() {
		t.Errorf("plain nested Generate = %v, want the outer turn's identity inherited", pm)
	}
}

func TestNestedSessionUnderPoolChildCarriesNoLineage(t *testing.T) {
	ctx := context.Background()
	innerCol, plainCol, childCol := &mdCollector{}, &mdCollector{}, &mdCollector{}
	inner, err := thread.Create(ctx, thread.Memory(),
		core.New(wefttest.Script(wefttest.Say("inner reply")), innerCol.tap()))
	if err != nil {
		t.Fatalf("Create inner: %v", err)
	}
	plain := core.New(wefttest.Script(wefttest.Say("plain reply")), plainCol.tap())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "nested", Args: `{}`}),
		wefttest.Say("child reply"),
	), childCol.tap(), nestedSendTool(inner, plain))
	p := pool.New(2)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"look"})}),
		wefttest.Say("done"),
	), p.MustWrap("research", "delegates research", child))
	s, err := thread.Create(ctx, thread.Memory(), parent)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	turn, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	cm := childCol.snapshot()
	if len(cm) != 1 || cm[0]["weft.session.parent"] != s.ID() || cm[0]["weft.session.parent_call"] == "" {
		t.Fatalf("pool child runs = %v, want one carrying its lineage", cm)
	}
	mds := innerCol.snapshot()
	if len(mds) != 1 {
		t.Fatalf("inner runs = %d, want 1", len(mds))
	}
	for _, k := range []string{"weft.session.parent", "weft.session.parent_call", "weft.session.forked_from", "weft.public_id"} {
		if v, ok := mds[0][k]; ok {
			t.Errorf("inner session's run carries %s = %q, want none: it is no pool child", k, v)
		}
	}
	if mds[0]["weft.session.id"] != inner.ID() {
		t.Errorf("inner run weft.session.id = %q, want %q", mds[0]["weft.session.id"], inner.ID())
	}
	pm := plainCol.snapshot()
	if len(pm) != 1 || pm[0]["weft.session.parent"] != s.ID() {
		t.Errorf("plain nested Generate = %v, want the pool child's lineage inherited", pm)
	}
}
