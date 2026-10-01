package thread_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// gatedDeploy is the agent of the approval examples: its one tool,
// deploy, needs a decision before it runs, and the scripted model
// plays turns.
func gatedDeploy(turns ...wefttest.Turn) *weft.Agent {
	return weft.New(wefttest.Script(turns...),
		weft.Tool("deploy", "Deploy the service.",
			func(ctx context.Context, in struct {
				Env string `json:"env"`
			}) (string, error) {
				return "deployed to " + in.Env, nil
			},
			weft.RequireApproval()))
}

// deployTo is a model turn asking to deploy to env.
func deployTo(env string) wefttest.Turn {
	return wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"` + env + `"}`})
}

// settle waits for a turn and, when its boundary resumed on its own,
// for the resume too; it returns the last result.
func settle(t *thread.Turn) *weft.RunResult {
	res, err := t.Wait()
	if err != nil {
		fmt.Println("turn failed:", err)
		return nil
	}
	if next := t.Next(); next != nil {
		return settle(next)
	}
	return res
}

// A grant approves future calls without asking: here every deploy
// whose env matches a glob. The grant is an entry, so is its
// revocation — after it the same call parks again.
func ExampleSession_Grant() {
	ctx := context.Background()
	agent := gatedDeploy(
		deployTo("staging-eu"), wefttest.Say("Staging is live."),
		deployTo("staging-us"),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent)

	err := s.Grant(ctx, thread.Grant{
		Tool: "deploy",
		Args: []thread.Arg{thread.ArgGlob("/env", "staging-*")},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	t1, _ := s.Send(ctx, weft.User("Deploy to staging-eu."))
	fmt.Println("granted:", settle(t1).Text())
	fmt.Println("pending:", len(s.Pending()))

	// The audit entry of the match names the grant; revoke it.
	for _, e := range s.Audit() {
		if a, ok := e.(thread.ApprovalAuditEntry); ok && a.Step == thread.StepGrant {
			if err := s.Revoke(ctx, a.GrantID); err != nil {
				fmt.Println(err)
				return
			}
		}
	}
	t2, _ := s.Send(ctx, weft.User("Deploy to staging-us."))
	settle(t2)
	fmt.Println("pending after the revocation:", len(s.Pending()))
	// Output:
	// granted: Staging is live.
	// pending: 0
	// pending after the revocation: 1
}

// The signed flow, for decisions that cross a process boundary: the
// session mints a challenge over the pending request, the signing side
// signs a decision over it, and DecideSigned verifies before anything
// is recorded. RequireSigned closes the unsigned door, durably — the
// session's header keeps the rule.
func ExampleSession_DecideSigned() {
	ctx := context.Background()
	ring, err := thread.NewKeyring(thread.Key{ID: "ops-2026", Secret: []byte("a secret the session file never sees"), Active: true})
	if err != nil {
		fmt.Println(err)
		return
	}
	agent := gatedDeploy(deployTo("prod"), wefttest.Say("Deployed."))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		fmt.Println(err)
		return
	}
	t1, _ := s.Send(ctx, weft.User("Deploy to prod."))
	settle(t1)
	call := s.Pending()[0].CallID

	_, err = s.Decide(ctx, thread.Approve(call))
	fmt.Println("unsigned:", errors.Is(err, thread.ErrSignatureRequired))

	challenge, err := s.Request(call) // travels to the signing side
	if err != nil {
		fmt.Println(err)
		return
	}
	signed, err := ring.Sign(challenge, thread.Approve(call)) // travels back
	if err != nil {
		fmt.Println(err)
		return
	}
	resume, err := s.DecideSigned(ctx, signed)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("signed:", settle(resume).Text())

	_, err = s.DecideSigned(ctx, signed)
	fmt.Println("replayed:", errors.Is(err, thread.ErrReplay))
	// Output:
	// unsigned: true
	// signed: Deployed.
	// replayed: true
}

// Under Quorum a call needs approvals from distinct approvers. Signed,
// an approver is a key: each approver signs with their own, and one
// key signing twice — whatever names it gives — is still one approver.
func ExampleQuorum() {
	ctx := context.Background()
	alice := thread.Key{ID: "alice", Secret: []byte("alice's secret"), Active: true}
	bob := thread.Key{ID: "bob", Secret: []byte("bob's secret")}
	ring, _ := thread.NewKeyring(alice, bob)

	agent := gatedDeploy(deployTo("prod"), wefttest.Say("Deployed."))
	s, _ := thread.Create(ctx, thread.Memory(), agent,
		thread.WithKeyring(ring), thread.RequireSigned(), thread.Quorum(2))
	t1, _ := s.Send(ctx, weft.User("Deploy to prod."))
	settle(t1)
	call := s.Pending()[0].CallID

	approve := func(k thread.Key) *thread.Turn {
		challenge, err := s.Request(call) // one challenge per signature
		if err != nil {
			fmt.Println(err)
			return nil
		}
		signed, err := k.Sign(challenge, thread.Approve(call))
		if err != nil {
			fmt.Println(err)
			return nil
		}
		turn, err := s.DecideSigned(ctx, signed)
		if err != nil {
			fmt.Println(err)
		}
		return turn
	}
	approve(alice)
	approve(alice) // the same key again adds nothing
	fmt.Println("after alice, twice:", len(s.Pending()), "pending")

	resume := approve(bob)
	fmt.Println("after bob:", settle(resume).Text())
	// Output:
	// after alice, twice: 1 pending
	// after bob: Deployed.
}

// RequestExpiry gives every parked request a lifetime, and OnRequest
// says when one parks. A request past its expiry takes no decision:
// Decide refuses it with ErrExpired and the request is denied with a
// reason the model sees.
func ExampleRequestExpiry() {
	ctx := context.Background()
	agent := gatedDeploy(deployTo("prod"), wefttest.Say("Noted: the request lapsed."))
	s, _ := thread.Create(ctx, thread.Memory(), agent,
		thread.RequestExpiry(10*time.Millisecond),
		thread.OnRequest(func(r thread.Request) {
			// Runs on the session's runner: hand the request to a queue
			// or a goroutine and return.
			fmt.Println("parked:", r.Tool, "expires:", !r.Expiry.IsZero())
		}))
	t1, _ := s.Send(ctx, weft.User("Deploy to prod."))
	settle(t1)
	req := s.Pending()[0]

	time.Sleep(time.Until(req.Expiry) + 5*time.Millisecond) // nobody decided in time

	_, err := s.Decide(ctx, thread.Approve(req.CallID))
	fmt.Println("too late:", errors.Is(err, thread.ErrExpired))

	// The expiry's denial completed the boundary; the session resumed.
	settle(t1)
	for _, m := range s.Context() {
		if r, ok := lastResult(m); ok {
			fmt.Println("the model saw:", strings.SplitN(r, " before ", 2)[0])
		}
	}
	// Output:
	// parked: deploy expires: true
	// too late: true
	// the model saw: DENIED: expired: no decision
}

// An Approver is the "ask now" step for a UI that is already
// connected: consulted before a call parks, under the timeout it is
// given. It declines what it will not decide, and that call parks
// like any other.
func ExampleWithApprover() {
	ctx := context.Background()
	approver := func(ctx context.Context, r thread.Request) (thread.Decision, bool) {
		if strings.Contains(string(r.Args), `"staging"`) {
			d := thread.Approve(r.CallID)
			d.Who = "terminal"
			return d, true
		}
		return thread.Decision{}, false // not mine to decide: park it
	}
	agent := gatedDeploy(
		deployTo("staging"), wefttest.Say("Staging is live."),
		deployTo("prod"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithApprover(approver, 30*time.Second))
	if err != nil {
		fmt.Println(err)
		return
	}
	t1, _ := s.Send(ctx, weft.User("Deploy to staging."))
	fmt.Println("staging:", settle(t1).Text())

	t2, _ := s.Send(ctx, weft.User("Now prod."))
	settle(t2)
	for _, r := range s.Pending() {
		fmt.Println("parked:", r.Tool, string(r.Args))
	}
	// Output:
	// staging: Staging is live.
	// parked: deploy {"env":"prod"}
}

// Audit is the session's approval trail: every request, chain step,
// decision, grant and revocation, in order — and for a resume, the
// entry that says it started and the turn entry that says how it
// ended. It indexes the session's log; it is not tamper-evident on
// its own.
func ExampleSession_Audit() {
	ctx := context.Background()
	agent := gatedDeploy(deployTo("prod"), wefttest.Say("Understood."))
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	t1, _ := s.Send(ctx, weft.User("Deploy to prod."))
	settle(t1)

	no := thread.Deny(s.Pending()[0].CallID, "change freeze")
	no.Who = "avi"
	resume, _ := s.Decide(ctx, no)
	settle(resume)

	for _, e := range s.Audit() {
		switch e := e.(type) {
		case thread.ApprovalRequestEntry:
			fmt.Println("request:", e.Tool, "-", e.Reason)
		case thread.ApprovalAuditEntry:
			fmt.Println("step:", e.Step, e.Outcome)
		case thread.ApprovalDecisionEntry:
			fmt.Println("decision:", e.Outcome, "by", e.Who, "via", e.Via, "-", e.Reason)
		}
	}
	// Output:
	// request: deploy - tool requires approval
	// step: park parked
	// decision: deny by avi via user - change freeze
	// step: resume started
	// step: resume completed
}

// With AutoResume off the caller drives the boundary: decisions are
// recorded as they arrive, and Resume runs it — the decided calls
// under their decisions, every undecided one denied as "no decision".
func ExampleSession_Resume() {
	ctx := context.Background()
	agent := gatedDeploy(
		wefttest.ToolCalls(
			wefttest.Call{Name: "deploy", ID: "call_eu", Args: `{"env":"eu"}`},
			wefttest.Call{Name: "deploy", ID: "call_us", Args: `{"env":"us"}`},
		),
		wefttest.Say("One region is live."),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	t1, _ := s.Send(ctx, weft.User("Deploy everywhere."))
	settle(t1)

	turn, err := s.Decide(ctx, thread.Approve("call_eu"))
	fmt.Println("decided one of two; resumed:", turn != nil, err)

	resume, err := s.Resume(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	res := settle(resume)
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				fmt.Println(r.CallID+":", r.Content)
			}
		}
	}
	fmt.Println(res.Text())
	// Output:
	// decided one of two; resumed: false <nil>
	// call_eu: deployed to eu
	// call_us: DENIED: no decision
	// One region is live.
}
