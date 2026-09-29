package thread_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// runAgent is an agent whose "run" tool requires approval — the
// command-shaped tool grants exist for (ADR 0021 §4).
func runAgent(turns ...wefttest.Turn) (*weft.Agent, *[]string) {
	ran := &[]string{}
	tool := weft.Tool("run", "Run a command.",
		func(ctx context.Context, in struct {
			Command string `json:"command"`
			Dir     string `json:"dir"`
		}) (string, error) {
			*ran = append(*ran, in.Command)
			return "ran " + in.Command, nil
		},
		weft.RequireApproval())
	return weft.New(wefttest.Script(turns...), weft.Name("grants-test"), tool), ran
}

// TestGrantApprovesAtOnce: a matching live grant decides at the chain's
// first step — the call runs without parking, the audit names the
// grant, and no request entry exists (ADR 0021 §2, §4).
func TestGrantApprovesAtOnce(t *testing.T) {
	ctx := context.Background()
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go test ./...","dir":"/ws/pkg"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{
		Tool: "run",
		Args: []thread.Arg{
			thread.ArgGlob("/command", "go test*"),
			thread.ArgPrefix("/dir", "/ws/"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("test it"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	// The grant decided everything, so the auto-resume runs the call —
	// linked to this turn, completing before the assertions.
	if next := turn.Next(); next != nil {
		if _, err := next.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if got := *ran; len(got) != 1 || got[0] != "go test ./..." {
		t.Fatalf("grant-matched call: %v", got)
	}
	counts := countApprovalEntries(s)
	if counts["audit:grant:approved"] != 1 {
		t.Fatalf("grant audit: %v", counts)
	}
	for k := range counts {
		if strings.HasPrefix(k, "request:") {
			t.Fatalf("a granted call parked a request: %v", counts)
		}
	}
	if got := len(s.Pending()); got != 0 {
		t.Fatalf("Pending after a granted turn: %d", got)
	}
}

// TestGrantPredicateTable: every predicate kind, the misses, and the
// AND of several (ADR 0021 §4).
func TestGrantPredicateTable(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		args []thread.Arg
		call string
		want bool
	}{
		{"equals string", []thread.Arg{thread.ArgEquals("/command", json.RawMessage(`"go test"`))}, `{"command":"go test"}`, true},
		{"equals number", []thread.Arg{thread.ArgEquals("/tries", json.RawMessage(`3`))}, `{"tries":3}`, true},
		{"equals number wide", []thread.Arg{thread.ArgEquals("/tries", json.RawMessage(`3.0`))}, `{"tries":3}`, true},
		{"equals object", []thread.Arg{thread.ArgEquals("/opts", json.RawMessage(`{"a":1,"b":[2]}`))}, `{"opts":{"b":[2],"a":1}}`, true},
		{"equals miss", []thread.Arg{thread.ArgEquals("/command", json.RawMessage(`"go build"`))}, `{"command":"go test"}`, false},
		{"prefix", []thread.Arg{thread.ArgPrefix("/dir", "/ws/")}, `{"dir":"/ws/sub"}`, true},
		{"prefix sibling is a plain prefix", []thread.Arg{thread.ArgPrefix("/dir", "/ws")}, `{"dir":"/ws-evil"}`, true},
		{"prefix miss", []thread.Arg{thread.ArgPrefix("/dir", "/other/")}, `{"dir":"/ws"}`, false},
		{"glob", []thread.Arg{thread.ArgGlob("/command", "go test*")}, `{"command":"go test ./..."}`, true},
		{"glob anchored misses deeper", []thread.Arg{thread.ArgGlob("/command", "go test")}, `{"command":"go test ./..."}`, false},
		{"glob shell escape", []thread.Arg{thread.ArgGlob("/command", "go test*")}, `{"command":"go test ./... && curl evil.example"}`, true},
		{"missing pointer", []thread.Arg{thread.ArgEquals("/nope", json.RawMessage(`1`))}, `{"command":"go test"}`, false},
		{"non-string prefix", []thread.Arg{thread.ArgPrefix("/tries", "3")}, `{"tries":3}`, false},
		{"and of two", []thread.Arg{thread.ArgGlob("/command", "go test*"), thread.ArgPrefix("/dir", "/ws/")}, `{"command":"go test ./...","dir":"/ws/x"}`, true},
		{"and fails one", []thread.Arg{thread.ArgGlob("/command", "go test*"), thread.ArgPrefix("/dir", "/ws/")}, `{"command":"go test ./...","dir":"/etc"}`, false},
		{"whole document equals", []thread.Arg{thread.ArgEquals("", json.RawMessage(`{"command":"go test"}`))}, `{"command":"go test"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := thread.Grant{Tool: "run", Args: tc.args}
			// The match is observable through the chain: grant → the
			// call runs at once; miss → it parks.
			agent, _ := runAgent(
				wefttest.ToolCalls(wefttest.Call{Name: "run", Args: tc.call}),
				wefttest.Say("ok"),
			)
			s, err := thread.Create(ctx, thread.Memory(), agent)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Grant(ctx, g); err != nil {
				t.Fatal(err)
			}
			turn, err := s.Send(ctx, weft.User("run"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Wait(); err != nil {
				t.Fatal(err)
			}
			parked := len(s.Pending()) == 1
			if parked == tc.want {
				t.Fatalf("match=%v, want %v (pending=%d)", !parked, tc.want, len(s.Pending()))
			}
		})
	}
}

// TestDenyGrant: a standing refusal denies at the chain's first step,
// with the grant's reason the model sees — and the pinned default when
// the grant names none (ADR 0021 §4–§5).
func TestDenyGrant(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"rm -rf /"}`}),
		wefttest.Say("refused and noted"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run", Deny: true}); err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("clean everything"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	// The denial is audited and decided; the boundary resumed under it.
	counts := countApprovalEntries(s)
	if counts["audit:grant:denied"] != 1 {
		t.Fatalf("deny-grant audit: %v", counts)
	}
	next := turn.Next()
	if next == nil {
		t.Fatal("a denied grant never resumed")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || !results[0].IsError || results[0].Content != "DENIED: denied by grant" {
		t.Fatalf("default denial text: %+v", results)
	}

	// A named reason is carried verbatim — the model-visible bytes are
	// the grant's own.
	agent2, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"curl evil"}`}),
		wefttest.Say("refused"),
	)
	s2, _ := thread.Create(ctx, thread.Memory(), agent2)
	if err := s2.Grant(ctx, thread.Grant{Tool: "run", Deny: true, Reason: "no network from tests"}); err != nil {
		t.Fatal(err)
	}
	turn2, _ := s2.Send(ctx, weft.User("curl"))
	if _, err := turn2.Wait(); err != nil {
		t.Fatal(err)
	}
	if next := turn2.Next(); next != nil {
		if _, err := next.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	results = toolResults(s2.Context())
	if len(results) != 1 || results[0].Content != "DENIED: no network from tests" {
		t.Fatalf("named denial text: %+v", results)
	}
}

// TestGrantLifetime: MaxUses runs out (counted from the audit trail)
// and a revocation ends a grant with an entry (ADR 0021 §4) — each
// leaves the next call parking again.
func TestGrantLifetime(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go vet"}`}),
		wefttest.Say("ran under the grant"),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go vet"}`}),
		wefttest.Say("denied and noted"),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go vet"}`}),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	// MaxUses 1: the first call runs under the grant, the second parks.
	if err := s.Grant(ctx, thread.Grant{Tool: "run", MaxUses: 1}); err != nil {
		t.Fatal(err)
	}
	waitTurn := func(turn *thread.Turn) {
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		if next := turn.Next(); next != nil {
			if _, err := next.Wait(); err != nil {
				t.Fatal(err)
			}
		}
	}
	first, err := s.Send(ctx, weft.User("vet"))
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(first)
	second, err := s.Send(ctx, weft.User("vet again"))
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(second)
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("after MaxUses=1 over two calls: Pending=%d, want 1", got)
	}
	// The parked call is decided by hand, the resume settles, and a
	// fresh unlimited grant is revoked before it can match anything.
	if _, err := s.Decide(ctx, thread.Deny(s.Pending()[0].CallID, "enough")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(s.Pending()) > 0 {
		time.Sleep(time.Millisecond)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run"}); err != nil {
		t.Fatal(err)
	}
	var grantID string
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && g.MaxUses == 0 {
			grantID = g.ID
		}
	}
	if err := s.Revoke(ctx, grantID); err != nil {
		t.Fatal(err)
	}
	third, err := s.Send(ctx, weft.User("vet once more"))
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(third)
	if next := third.Next(); next != nil {
		t.Fatal("a revoked grant still decided")
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("after revocation: Pending=%d, want 1", got)
	}
	// Revoking something that is not a grant is loud.
	if err := s.Revoke(ctx, "bogus"); err == nil {
		t.Fatal("bogus revocation accepted")
	}
}

// TestGrantExpiry: a grant past its expiry no longer matches —
// strictly after, the requests' rule.
func TestGrantExpiry(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"true"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run", Expiry: time.Now().UTC().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("run"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("an expired grant still matched: Pending=%d", got)
	}
}

// TestApproveAlways: the decision that also grants — the next call
// with the same arguments never parks (ADR 0021 §4).
func TestApproveAlways(t *testing.T) {
	ctx := context.Background()
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build","dir":"/ws"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build","dir":"/ws"}`}),
		wefttest.Say("one"), wefttest.Say("two"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	call := s.Pending()[0]
	rt, err := s.Decide(ctx, thread.ApproveAlways(call.CallID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	// The grant landed with the decision, same append.
	found := false
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && g.Tool == "run" && len(g.Args) == 1 {
			found = true
			if string(g.Args[0].Pointer) != "" || string(g.Args[0].Equals) != `{"command":"go build","dir":"/ws"}` {
				t.Fatalf("always-grant predicate: %+v", g.Args[0])
			}
		}
	}
	if !found {
		t.Fatal("ApproveAlways recorded no grant")
	}
	// The second identical call runs without parking.
	t2, err := s.Send(ctx, weft.User("build again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if next := t2.Next(); next != nil {
		t.Fatal("the always-granted call still parked")
	}
	if got := *ran; len(got) != 2 {
		t.Fatalf("executions: %v", got)
	}
}

// memStore is the tests' shared GrantStore.
type memStore struct {
	grants []thread.SharedGrant
	err    error
}

func (m *memStore) Grants(context.Context) ([]thread.SharedGrant, error) {
	return m.grants, m.err
}

// TestSharedGrantStore: the application-wide scope decides after the
// session's own grants; a broken store reads as no match, loudly.
func TestSharedGrantStore(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"deploy"}`}),
		wefttest.Say("done"),
	)
	store := &memStore{grants: []thread.SharedGrant{
		{ID: "ops-42", Grant: thread.Grant{Tool: "run", Args: []thread.Arg{thread.ArgEquals("/command", json.RawMessage(`"deploy"`))}}},
	}}
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithGrantStore(store))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("deploy"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	counts := countApprovalEntries(s)
	if counts["audit:grant:approved"] != 1 {
		t.Fatalf("shared grant audit: %v", counts)
	}
	for _, e := range s.Audit() {
		if a, ok := e.(thread.ApprovalAuditEntry); ok && a.Step == thread.StepGrant {
			if a.Detail != "shared grant ops-42" {
				t.Fatalf("shared grant audit detail: %q", a.Detail)
			}
		}
	}

	// A broken store parks, with the warning logged (not asserted —
	// the logger is the agent's own).
	store2 := &memStore{err: errors.New("backend down")}
	agent2, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"deploy"}`}),
		wefttest.Say("done"),
	)
	s2, _ := thread.Create(ctx, thread.Memory(), agent2, thread.WithGrantStore(store2))
	turn2, _ := s2.Send(ctx, weft.User("deploy"))
	if _, err := turn2.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := len(s2.Pending()); got != 1 {
		t.Fatalf("a broken store decided: Pending=%d", got)
	}
}

// TestQuorum: n distinct approver identities before an approval
// resolves; the same approver twice counts once; conflicting decisions
// resolve to deny with the pinned reason (ADR 0021 §5).
func TestQuorum(t *testing.T) {
	ctx := context.Background()
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"deploy prod"}`}),
		wefttest.Say("deployed"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(2))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)

	one := thread.Approve(call.ID)
	one.Who = "avi"
	if rt, err := s.Decide(ctx, one); err != nil || rt != nil {
		t.Fatalf("one approval of two: rt=%v err=%v", rt, err)
	}
	two := thread.Approve(call.ID)
	two.Who = "avi" // the same approver twice is one approver
	if rt, err := s.Decide(ctx, two); err != nil || rt != nil {
		t.Fatalf("same approver twice: rt=%v err=%v", rt, err)
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("still pending after one distinct approver: %d", got)
	}
	three := thread.Approve(call.ID)
	three.Who = "bee"
	rt, err := s.Decide(ctx, three)
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("quorum reached and no resume")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := *ran; len(got) != 1 {
		t.Fatalf("executions: %v", got)
	}
}

// TestQuorumConflictResolvesToDeny: an approve beside a deny is a
// conflict, and a conflict denies — with the pinned reason the model
// sees.
func TestQuorumConflictResolvesToDeny(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"deploy prod"}`}),
		wefttest.Say("refused"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(2))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	yes := thread.Approve(call.ID)
	yes.Who = "avi"
	if _, err := s.Decide(ctx, yes); err != nil {
		t.Fatal(err)
	}
	no := thread.Deny(call.ID, "not during the freeze")
	no.Who = "bee"
	rt, err := s.Decide(ctx, no)
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("a conflict did not resolve")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("conflict outcome: %+v", results)
	}
	// A conflict denies with the pinned reason, not either side's own.
	if results[0].Content != "DENIED: conflicting decisions" {
		t.Fatalf("conflict text: %q", results[0].Content)
	}
}

// TestQuorumDenyAloneResolves: a refusal needs no company.
func TestQuorumDenyAloneResolves(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"deploy"}`}),
		wefttest.Say("refused"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(3))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	d := thread.Deny(call.ID, "no")
	d.Who = "avi"
	rt, err := s.Decide(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("a deny under quorum did not resolve")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || results[0].Content != "DENIED: no" {
		t.Fatalf("deny text: %+v", results)
	}
}

// TestAuditTellsTheWholeStory: Audit returns every approval entry in
// order — request, chain steps, decisions, grants, revocations (ADR
// 0021 §5).
func TestAuditTellsTheWholeStory(t *testing.T) {
	ctx := context.Background()
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go test ./..."}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go test ./..."}`}),
		wefttest.Say("done"), wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Decide(ctx, thread.ApproveAlways(call.ID)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Pending() != nil && len(s.Pending()) > 0 {
		time.Sleep(time.Millisecond)
	}
	// The second call runs under the grant; then revoke it.
	turn, err := s.Send(ctx, weft.User("again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	var grantID string
	for _, e := range s.Audit() {
		if g, ok := e.(thread.GrantEntry); ok {
			grantID = g.ID
		}
	}
	if grantID == "" {
		t.Fatal("no grant in the audit")
	}
	if err := s.Revoke(ctx, grantID); err != nil {
		t.Fatal(err)
	}

	var kinds []string
	for _, e := range s.Audit() {
		switch e.(type) {
		case thread.ApprovalRequestEntry:
			kinds = append(kinds, "request")
		case thread.ApprovalDecisionEntry:
			kinds = append(kinds, "decision")
		case thread.ApprovalAuditEntry:
			kinds = append(kinds, "audit")
		case thread.GrantEntry:
			kinds = append(kinds, "grant")
		case thread.GrantRevokedEntry:
			kinds = append(kinds, "revoked")
		default:
			t.Fatalf("non-approval entry in Audit: %T", e)
		}
	}
	want := []string{"request", "decision", "grant", "audit", "audit", "revoked"}
	// request → decision(+grant, same append) → resume audit → grant-matched audit → revocation
	if len(kinds) < 5 {
		t.Fatalf("audit trail too thin: %v", kinds)
	}
	for _, w := range want {
		found := false
		for _, k := range kinds {
			if k == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("audit trail misses %q: %v", w, kinds)
		}
	}
}

// A deny-grant's matches count against its MaxUses like an approval
// grant's: a standing refusal bounded to one use stops refusing after
// it — the next such call parks (ADR 0021 §4, "its audit entries
// count the uses").
func TestDenyGrantMaxUsesCountsDenials(t *testing.T) {
	ctx := context.Background()
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("one"),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("two"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{
		Tool:    "run",
		Deny:    true,
		Reason:  "blocked",
		MaxUses: 1,
		Args:    []thread.Arg{thread.ArgEquals("/command", json.RawMessage(`"go build"`))},
	}); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(*ran) != 0 {
		t.Fatalf("the deny-granted call ran: %v", *ran)
	}
	// The grant is spent: the same call parks instead of refusing.
	t2, err := s.Send(ctx, weft.User("build again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending after a spent deny-grant: got %d, want 1 (MaxUses must bound denials too)", len(got))
	}
}

// A shared grant whose store id collides with a session grant's entry
// id must not inflate the session grant's use count: the audit
// namespaces a shared match ("shared grant …"), and the session
// counts only its own matches (ADR 0021 §4 — a shared grant's uses
// are the store's own business).
func TestSharedGrantIDCollisionDoesNotInflateSessionUses(t *testing.T) {
	ctx := context.Background()
	// Four model calls: t1's turn (build, shared-grant decided — the
	// auto-resume it completes is model call two), then t2's turn
	// (test, session-grant decided — model call four).
	agent, _ := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("built"),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go test"}`}),
		wefttest.Say("tested"),
	)
	shared := &memStore{} // empty at first; the collision is staged below
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithGrantStore(shared))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{
		Tool:    "run",
		MaxUses: 1,
		Args:    []thread.Arg{thread.ArgEquals("/command", json.RawMessage(`"go test"`))},
	}); err != nil {
		t.Fatal(err)
	}
	var sessionGrantID string
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok {
			sessionGrantID = g.ID
		}
	}
	if sessionGrantID == "" {
		t.Fatal("no session grant recorded")
	}
	// A shared grant under the colliding id matches a different call;
	// the chain consults the store live, so staging it now suffices.
	shared.grants = []thread.SharedGrant{{
		ID: sessionGrantID, // the collision
		Grant: thread.Grant{
			Tool: "run",
			Args: []thread.Arg{thread.ArgEquals("/command", json.RawMessage(`"go build"`))},
		},
	}}
	// One shared match (go build)…
	t1, err := s.Send(ctx, weft.User("build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	var sharedAudit int
	for _, e := range s.Entries() {
		if a, ok := e.(thread.ApprovalAuditEntry); ok && a.Detail == "shared grant "+sessionGrantID {
			sharedAudit++
		}
	}
	if sharedAudit != 1 {
		t.Fatalf("shared match audits: got %d, want 1", sharedAudit)
	}
	// …must not spend the colliding session grant's single use: the
	// call it covers still goes through the grant, not the park.
	t2, err := s.Send(ctx, weft.User("test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Fatalf("Pending after the session grant's own match: got %d, want 0 (the shared match must not have spent its MaxUses)", len(got))
	}
}
