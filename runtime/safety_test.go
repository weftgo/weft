package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/thread"
)

// forkSteerModel answers by the transcript's tail: the app's own
// greeting gets words; any other user message calls refund (the first
// such call held until gate closes, so a steer can land mid-turn); a
// tool result gets words.
type forkSteerModel struct {
	gate chan struct{}
	once sync.Once
}

func (*forkSteerModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "fork-steer"}
}

func (m *forkSteerModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	last := req.Messages[len(req.Messages)-1]
	return func(yield func(core.ModelEvent, error) bool) {
		switch {
		case last.Role == core.RoleUser && last.Text() == "hello app":
			yield(core.ModelTextDelta{Text: "hi"}, nil)
			yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
		case last.Role == core.RoleUser:
			held := false
			m.once.Do(func() { held = true })
			if held {
				select {
				case <-m.gate:
				case <-ctx.Done():
					yield(nil, ctx.Err())
					return
				}
			}
			yield(core.ModelToolCall{ID: "c_r", Name: "refund", Args: []byte(`{}`)}, nil)
			yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
		default:
			yield(core.ModelTextDelta{Text: "ok"}, nil)
			yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
		}
	}
}

// TestForkSteerNeverRunsUnparked pins §6 rule 3 on a fork's steer
// (§8.4): a steer into a fork's turn that thread defers (it met the
// approval boundary) runs later as a follow-up turn of the fork — and
// that follow-up runs under the fork turn's run options, so the
// playground's park rule binds it: the never tool parks, its handler
// never runs. (Before thread's steer follow-up fix the follow-up ran
// without the turn's options and the handler fired; the runtime refused
// fork steering until it landed.)
func TestForkSteerNeverRunsUnparked(t *testing.T) {
	var refunds atomic.Int64
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		refunds.Add(1)
		return "refunded for real", nil
	})
	model := &forkSteerModel{gate: make(chan struct{})}
	agent := core.New(model, core.Name("acme-support"), refund)
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}

	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	defer l.stop()
	input := "refund please"
	cmd := command{CommandID: "cmd_fs", Agent: "acme-support", Thread: "fork", Engine: "live",
		Source: &sourceSpec{RunID: turn.RunID()}, Input: &input}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatal(reason)
	}
	done := make(chan string, 1)
	go func() {
		_, runID, _ := l.execute(ctx, cmd, "pg_unused")
		done <- runID
	}()
	// The fork's turn is in flight (held in the model): steer it.
	var forkRun string
	deadline := time.Now().Add(5 * time.Second)
	for forkRun == "" && time.Now().Before(deadline) {
		l.mu.Lock()
		for id := range l.steerSess {
			forkRun = id
		}
		l.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	if forkRun == "" {
		t.Fatal("the fork's turn never registered for steering")
	}
	l.steer(steerFrame{RunID: forkRun, Message: "and refund the other one too"})
	close(model.gate)
	parkedRun := <-done
	if n := refunds.Load(); n != 0 {
		t.Fatalf("refund ran %d times in the fork's own turn, want it parked", n)
	}
	l.mu.Lock()
	pr := l.parked[parkedRun]
	if pr != nil {
		pr.decisions["c_r"] = approvalDecision{CallID: "c_r", Decision: "deny"}
		l.forgetParkLocked(parkedRun)
	}
	l.mu.Unlock()
	if pr == nil {
		t.Fatalf("the fork's turn %s did not park", parkedRun)
	}
	if status, _, errText := l.resume(ctx, pr, "pg_resume", ""); status != "succeeded" {
		t.Fatalf("resume = %s %s", status, errText)
	}
	// The steer's follow-up turn runs now: it calls refund, which must
	// park in the fork like the turn's own call did.
	sessID := parkedRun[:strings.LastIndex(parkedRun, "-t")]
	l.mu.Lock()
	fork := l.forks[sessID]
	l.mu.Unlock()
	if fork == nil {
		t.Fatalf("the runtime does not hold fork %s", sessID)
	}
	var followUp []thread.Request
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(followUp) == 0 && refunds.Load() == 0 {
		for _, r := range fork.Pending() {
			if r.RunID != parkedRun && r.Tool == "refund" {
				followUp = append(followUp, r)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := refunds.Load(); n != 0 {
		t.Fatalf("refund's handler ran %d time(s) for real: the steer's follow-up turn ran without the playground's park rule", n)
	}
	if len(followUp) == 0 {
		t.Fatalf("the steer's follow-up never parked its refund call (pending %+v)", fork.Pending())
	}
	// The steer was delivered — as the fork's next user message.
	var steered bool
	for _, m := range fork.Context() {
		if m.Role == core.RoleUser && strings.Contains(m.Text(), "refund the other one") {
			steered = true
		}
	}
	if !steered {
		t.Error("the steer never reached the fork's conversation")
	}
}

// TestForkSteerIdleSessionKeepsTheParkRule pins the one path thread
// does not cover by inheritance: a steer that reaches the fork's
// session after its turn ended — nothing in flight, no boundary open —
// is a plain turn under the Send's own options. The runtime passes the
// fork turn's options on that Send, so the never tool still parks.
func TestForkSteerIdleSessionKeepsTheParkRule(t *testing.T) {
	var refunds atomic.Int64
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		refunds.Add(1)
		return "refunded for real", nil
	})
	model := &forkSteerModel{gate: make(chan struct{})}
	close(model.gate)
	agent := core.New(model, core.Name("acme-support"), refund)
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	defer l.stop()
	// A fork whose turn answers without a tool call: "hello app" again.
	input := "hello app"
	cmd := command{CommandID: "cmd_fi", Agent: "acme-support", Thread: "fork", Engine: "live",
		Source: &sourceSpec{RunID: turn.RunID()}, Input: &input}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatal(reason)
	}
	status, runID, errText := l.execute(ctx, cmd, "pg_unused")
	if status != "succeeded" {
		t.Fatalf("fork = %s %s", status, errText)
	}
	sessID := runID[:strings.LastIndex(runID, "-t")]
	l.mu.Lock()
	fork := l.forks[sessID]
	forkCmd := l.forkCmd[sessID]
	l.mu.Unlock()
	opts := l.overrideOptions(forkCmd)
	l.mu.Lock()
	// The registry entry as awaitTurn holds it while a turn is in
	// flight — re-armed here so the steer finds an idle session.
	l.steerSess[runID] = forkSteer{sess: fork, opts: opts}
	l.mu.Unlock()
	l.steer(steerFrame{RunID: runID, Message: "refund it"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(fork.Pending()) == 0 && refunds.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := refunds.Load(); n != 0 {
		t.Fatalf("refund ran %d time(s) for real: the idle-session steer ran without the park rule", n)
	}
	if p := fork.Pending(); len(p) != 1 || p[0].Tool != "refund" {
		t.Fatalf("pending = %+v, want the steer turn's refund parked", p)
	}
	l.mu.Lock()
	delete(l.steerSess, runID)
	l.mu.Unlock()
}

// TestForkDoesNotInheritTheAppsGrants pins §6 rule 3 against ADR
// 0021's grants: a session-scoped grant is an entry of the app's
// session, Fork copies the conversation through the source turn — the
// grant with it — and the fork's boundary chain would auto-approve the
// parked call: the never tool's handler would run for real in a
// playground experiment nobody approved.
func TestForkDoesNotInheritTheAppsGrants(t *testing.T) {
	var refunds atomic.Int64
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		refunds.Add(1)
		return "refunded for real", nil
	})
	model := &forkSteerModel{gate: make(chan struct{})}
	close(model.gate)
	agent := core.New(model, core.Name("acme-support"), refund)
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Grant(ctx, thread.Grant{Tool: "refund"}); err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}

	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	defer l.stop()
	input := "refund please"
	cmd := command{CommandID: "cmd_fg", Agent: "acme-support", Thread: "fork", Engine: "live",
		Source: &sourceSpec{RunID: turn.RunID()}, Input: &input}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatal(reason)
	}
	status, runID, errText := l.execute(ctx, cmd, "pg_unused")
	// The chain's auto-approval resumes in the background: give it room
	// before reading the count, and require the park the rule promises.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) && refunds.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := refunds.Load(); n != 0 {
		t.Fatalf("refund's handler ran %d time(s) for real in the fork (status %s %s, run %s): the app's grant approved it", n, status, errText, runID)
	}
	l.mu.Lock()
	pr := l.parked[runID]
	l.mu.Unlock()
	if pr == nil || pr.sess == nil || len(pr.sess.Pending()) != 1 {
		t.Errorf("the fork's turn %s is not parked on refund (status %s %s)", runID, status, errText)
	}
}

// decisionLink is a link over a fake Studio holding one parked
// ephemeral run with the given pending calls.
func decisionLink(t *testing.T, calls ...string) (*link, *fakeStudio, *parkedRun) {
	t.Helper()
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	agent := core.New(&forkSteerModel{gate: make(chan struct{})}, core.Name("acme-support"))
	cfg := &config{agents: []*core.Agent{agent}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	t.Cleanup(l.stop)
	pr := &parkedRun{cmd: command{CommandID: "cmd_src", Agent: "acme-support"}, decisions: map[string]approvalDecision{}}
	for _, c := range calls {
		pr.pending = append(pr.pending, core.ToolCallPart{ID: c, Name: "refund", Args: []byte(`{}`)})
	}
	l.rememberPark("pg_parked", pr)
	return l, f, pr
}

// decide admits and dispatches one decision frame, synchronously.
func decide(t *testing.T, l *link, ctx context.Context, d approvalDecision) {
	t.Helper()
	cctx, ok := l.admit(d.CommandID)
	if !ok {
		t.Fatalf("decision %s not admitted", d.CommandID)
	}
	if ctx != nil {
		var cancel context.CancelFunc
		cctx, cancel = context.WithCancel(cctx)
		go func() { <-ctx.Done(); cancel() }()
	}
	l.dispatchDecision(cctx, d)
}

// TestSecondDecisionOnACallIsRejected pins the held-decision rule: a
// parked run with two calls holds the first call's decision until the
// second arrives. A second, conflicting decision on the same call must
// not silently replace the first (both were acked succeeded, and the
// run resumed under whichever came last).
func TestSecondDecisionOnACallIsRejected(t *testing.T) {
	l, f, pr := decisionLink(t, "c1", "c2")
	decide(t, l, nil, approvalDecision{CommandID: "cmd_d1", RunID: "pg_parked", CallID: "c1", Decision: "approve"})
	decide(t, l, nil, approvalDecision{CommandID: "cmd_d2", RunID: "pg_parked", CallID: "c1", Decision: "deny"})
	l.mu.Lock()
	got := pr.decisions["c1"].Decision
	l.mu.Unlock()
	if got != "approve" {
		t.Errorf("c1's held decision = %q after a second decision, want the first (approve) kept", got)
	}
	if a := f.ackOf(t, "cmd_d2", "rejected"); !strings.Contains(a.Error, "already") {
		t.Errorf("the second decision's rejection = %q, want it to say the call is already decided", a.Error)
	}
}

// TestRestoreParkIsOneLockHold pins restorePark's atomicity: the
// completer's decision leaves the park and the park goes back under one
// l.mu hold. In a gap between the two, a held decision whose accepted
// ack failed (dispatchDecision) would find its park missing and the set
// short, settle "not held; decide again", and the park would come back
// still holding it — a re-decision then "already decided". The hook
// runs at that point: the lock must be held there, and a held
// decision's check, queued on the lock, must see the park back with
// itself still in it and the completer's decision gone.
func TestRestoreParkIsOneLockHold(t *testing.T) {
	l, _, pr := decisionLink(t, "c1", "c2")
	held := approvalDecision{CommandID: "cmd_d1", RunID: "pg_parked", CallID: "c1", Decision: "approve"}
	l.mu.Lock()
	pr.decisions["c1"] = held
	pr.decisions["c2"] = approvalDecision{CommandID: "cmd_d2", RunID: "pg_parked", CallID: "c2", Decision: "approve"}
	l.forgetParkLocked("pg_parked") // the completer took the park
	l.mu.Unlock()

	type seen struct{ mine, dropped, taken bool }
	got := make(chan seen, 1)
	defer func(f func()) { restoreParkGap = f }(restoreParkGap)
	restoreParkGap = func() {
		restoreParkGap = nil
		if l.mu.TryLock() {
			l.mu.Unlock()
			t.Error("restorePark's gap ran without l.mu: the decision's removal and the park's return are two holds")
		}
		go func() { // the held decision's check after its accepted ack failed
			l.mu.Lock()
			defer l.mu.Unlock()
			mine := pr.decisions["c1"] == held
			dropped := mine && l.parked["pg_parked"] == pr
			got <- seen{mine, dropped, mine && !dropped && len(pr.decisions) == len(pr.pending)}
		}()
	}
	l.restorePark("pg_parked", pr, "c2")
	if s := <-got; !s.mine || !s.dropped || s.taken {
		t.Errorf("the held decision's check saw mine=%v dropped=%v taken=%v, want the park back with it held (mine, dropped)", s.mine, s.dropped, s.taken)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.parked["pg_parked"] != pr {
		t.Error("the park did not go back")
	}
	if _, ok := pr.decisions["c2"]; ok {
		t.Error("the completer's decision is still in the restored park")
	}
}

// TestRefusedDecisionAckRestoresThePark pins dispatchDecision's
// at-most-once rule on the decision that completes the set: its
// accepted ack refused (409), the resume does not run and the park
// goes back without the decision — no finished ack — so deciding again
// resumes it. Unconfirmed (the response lost), the same, plus a
// finished ack, failed "not run", settling the row Studio may hold.
func TestRefusedDecisionAckRestoresThePark(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		settle string // the refused decision's finished ack error, "" none
	}{
		{"refused", http.StatusConflict, ""},
		{"unconfirmed", dropResponse, notConfirmed + "; decide again to resume"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, f, _ := decisionLink(t, "c1")
			f.mu.Lock()
			f.ackStatus = func(a ack) int {
				if a.CommandID == "cmd_d1" && a.State == "accepted" {
					return c.status
				}
				return http.StatusOK
			}
			f.mu.Unlock()
			decide(t, l, nil, approvalDecision{CommandID: "cmd_d1", RunID: "pg_parked", CallID: "c1", Decision: "approve"})
			l.mu.Lock()
			pr := l.parked["pg_parked"]
			held := 0
			if pr != nil {
				held = len(pr.decisions)
			}
			l.mu.Unlock()
			if pr == nil || held != 0 {
				t.Fatalf("after a refused accepted ack: park %v holding %d decision(s), want it back with none", pr != nil, held)
			}
			f.mu.Lock()
			var fin []ack
			for _, a := range f.acks {
				if a.CommandID == "cmd_d1" && a.State == "finished" {
					fin = append(fin, a)
				}
			}
			f.mu.Unlock()
			switch {
			case c.settle == "" && len(fin) != 0:
				t.Errorf("finished acks %+v after a 409, want none", fin)
			case c.settle != "" && (len(fin) != 1 || fin[0].Status != "failed" || fin[0].Error != c.settle):
				t.Errorf("finished acks %+v, want one failed %q", fin, c.settle)
			}

			// Deciding again resumes it: accepted, the park taken, finished.
			decide(t, l, nil, approvalDecision{CommandID: "cmd_d2", RunID: "pg_parked", CallID: "c1", Decision: "approve"})
			f.ackOf(t, "cmd_d2", "accepted")
			f.ackOf(t, "cmd_d2", "finished")
			l.mu.Lock()
			_, still := l.parked["pg_parked"]
			l.mu.Unlock()
			if still {
				t.Error("the re-decision did not resume the run: still parked")
			}
		})
	}
}

// TestRefusedHeldDecisionIsNotHeld pins the decision that does not
// complete the set (two calls parked, one decided): its accepted ack
// refused, the decision is not held either — no finished ack — so a
// re-decision on the same call is taken, not refused "already
// decided". Unconfirmed (the response lost), the same, plus a finished
// ack, failed "not run …; decide again", settling the row Studio may
// hold accepted.
func TestRefusedHeldDecisionIsNotHeld(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		settle string // the decision's finished ack error, "" none
	}{
		{"refused", http.StatusConflict, ""},
		{"unconfirmed", dropResponse, notConfirmed + "; decide again"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, f, pr := decisionLink(t, "c1", "c2")
			f.mu.Lock()
			f.ackStatus = func(a ack) int {
				if a.CommandID == "cmd_d1" && a.State == "accepted" {
					return c.status
				}
				return http.StatusOK
			}
			f.mu.Unlock()
			decide(t, l, nil, approvalDecision{CommandID: "cmd_d1", RunID: "pg_parked", CallID: "c1", Decision: "approve"})
			l.mu.Lock()
			held := len(pr.decisions)
			l.mu.Unlock()
			if held != 0 {
				t.Errorf("the park holds %d decision(s) after a refused accepted ack, want none", held)
			}
			f.mu.Lock()
			var fin []ack
			for _, a := range f.acks {
				if a.CommandID == "cmd_d1" && a.State == "finished" {
					fin = append(fin, a)
				}
			}
			f.mu.Unlock()
			switch {
			case c.settle == "" && len(fin) != 0:
				t.Errorf("finished acks %+v after a 409, want none", fin)
			case c.settle != "" && (len(fin) != 1 || fin[0].Status != "failed" || fin[0].Error != c.settle):
				t.Errorf("finished acks %+v, want one failed %q", fin, c.settle)
			}
			decide(t, l, nil, approvalDecision{CommandID: "cmd_d2", RunID: "pg_parked", CallID: "c1", Decision: "deny"})
			if a := f.ackOf(t, "cmd_d2", "finished"); a.Status != "succeeded" {
				t.Errorf("the re-decision's finished ack = %+v, want succeeded (held)", a)
			}
			l.mu.Lock()
			got := pr.decisions["c1"].Decision
			l.mu.Unlock()
			if got != "deny" {
				t.Errorf("c1's held decision = %q, want the re-decision (deny)", got)
			}
		})
	}
}

// TestHeldDecisionTakenByTheResumeSucceeds pins the race the held
// decision's refused accepted ack can lose: while that ack is in
// flight, the other call's decision completes the set and takes the
// park with this decision in it — the resume applies it. The decision
// is then not told "decide again" (it was applied): its finished ack
// says succeeded, whether the ack was refused (the finished ack
// overrides the lost row) or unconfirmed.
func TestHeldDecisionTakenByTheResumeSucceeds(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
	}{
		{"refused", http.StatusConflict},
		{"unconfirmed", dropResponse},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, f, pr := decisionLink(t, "c1", "c2")
			arrived, release := make(chan struct{}), make(chan struct{})
			f.mu.Lock()
			f.ackStatus = func(a ack) int {
				if a.CommandID == "cmd_d1" && a.State == "accepted" {
					close(arrived)
					<-release
					return c.status
				}
				return http.StatusOK
			}
			f.mu.Unlock()
			d1 := approvalDecision{CommandID: "cmd_d1", RunID: "pg_parked", CallID: "c1", Decision: "approve"}
			// decide's t.Fatalf belongs on the test goroutine: admit here,
			// dispatch on others.
			c1ctx, ok1 := l.admit(d1.CommandID)
			c2ctx, ok2 := l.admit("cmd_d2")
			if !ok1 || !ok2 {
				t.Fatal("decisions not admitted")
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				l.dispatchDecision(c1ctx, d1)
			}()
			<-arrived
			// c2's decision completes the set while d1's ack hangs: it
			// takes the park, c1's approve in it.
			go l.dispatchDecision(c2ctx, approvalDecision{CommandID: "cmd_d2", RunID: "pg_parked", CallID: "c2", Decision: "deny"})
			f.ackOf(t, "cmd_d2", "accepted")
			l.mu.Lock()
			_, still := l.parked["pg_parked"]
			got := pr.decisions["c1"]
			l.mu.Unlock()
			if still || got != d1 {
				t.Fatalf("after the completing decision: parked %v, c1's decision %+v; want the park taken with d1 in it", still, got)
			}
			close(release)
			<-done
			f.mu.Lock()
			var fin []ack
			for _, a := range f.acks {
				if a.CommandID == "cmd_d1" && a.State == "finished" {
					fin = append(fin, a)
				}
			}
			f.mu.Unlock()
			if len(fin) != 1 || fin[0].Status != "succeeded" || fin[0].RunID != "pg_parked" {
				t.Errorf("d1's finished acks = %+v, want one succeeded naming pg_parked: the resume applied it", fin)
			}
		})
	}
}

// TestDecisionCanceledBeforeItsSlotKeepsThePark pins the resume that
// never started: the decision completing the set takes the park, and
// when it is canceled waiting for a run slot (sixteen runs busy) the
// run never resumed — the park must still be there for the next
// decision, or the run is stranded ("no parked run") for good.
func TestDecisionCanceledBeforeItsSlotKeepsThePark(t *testing.T) {
	l, _, _ := decisionLink(t, "c1")
	for range maxRunning {
		l.slots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		decide(t, l, ctx, approvalDecision{CommandID: "cmd_d1", RunID: "pg_parked", CallID: "c1", Decision: "approve"})
	}()
	time.Sleep(20 * time.Millisecond) // the decision is waiting for a slot
	cancel()
	<-done
	l.mu.Lock()
	pr := l.parked["pg_parked"]
	l.mu.Unlock()
	if pr == nil {
		t.Fatal("the park is gone although the resume never started")
	}
	if len(pr.decisions) != 0 {
		t.Errorf("the restored park holds decisions %v, want the canceled one dropped", pr.decisions)
	}
}

// recordingModel answers every request in words and keeps the user
// messages each request carried.
type recordingModel struct {
	mu   sync.Mutex
	seen [][]string
}

func (*recordingModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "rec"}
}

func (m *recordingModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	var users []string
	for _, msg := range req.Messages {
		if msg.Role == core.RoleUser {
			users = append(users, msg.Text())
		}
	}
	m.mu.Lock()
	m.seen = append(m.seen, users)
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		yield(core.ModelTextDelta{Text: "ok"}, nil)
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

func (m *recordingModel) last() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen[len(m.seen)-1]
}

// TestForkFromAnOlderTurnOfAFork pins fork mode's source: a command
// naming a turn of a fork this runtime holds continues that fork in
// place only when the turn is the fork's latest; naming an earlier
// turn forks from it — the conversation through that turn, not
// everything the fork said since.
func TestForkFromAnOlderTurnOfAFork(t *testing.T) {
	model := &recordingModel{}
	agent := core.New(model, core.Name("acme-support"))
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	defer l.stop()
	fork := func(id, source, input string) string {
		t.Helper()
		cmd := command{CommandID: id, Agent: "acme-support", Thread: "fork", Engine: "live",
			Source: &sourceSpec{RunID: source}, Input: &input}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Fatal(reason)
		}
		status, runID, errText := l.execute(ctx, cmd, "pg_unused")
		if status != "succeeded" {
			t.Fatalf("%s: %s %s", id, status, errText)
		}
		return runID
	}
	first := fork("cmd_f1", turn.RunID(), "first")
	second := fork("cmd_f2", first, "second")
	if got := model.last(); !slices.Equal(got, []string{"hello app", "first", "second"}) {
		t.Fatalf("continuing the fork's latest turn fed %q", got)
	}
	_ = second
	fork("cmd_f3", first, "instead")
	if got := model.last(); !slices.Equal(got, []string{"hello app", "first", "instead"}) {
		t.Errorf("forking from the fork's earlier turn %s fed %q, want the conversation through that turn", first, got)
	}
}

// TestCanceledBeforeItsSlotReleasesTheBudget pins the run cap's
// reservation (§6 rule 6): a command counted at admission and canceled
// while it waited for a run slot never ran — it must give its run back,
// or a cap of N is spent by commands that did nothing and the
// experiment reads budget_exceeded for good.
func TestCanceledBeforeItsSlotReleasesTheBudget(t *testing.T) {
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	agent := core.New(&recordingModel{}, core.Name("acme-support"))
	cfg := &config{agents: []*core.Agent{agent}, budget: Budget{MaxRunsPerExperiment: 1}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	defer l.stop()
	for range maxRunning {
		l.slots <- struct{}{}
	}
	in := "hi"
	cmd := command{CommandID: "cmd_b1", Agent: "acme-support", Input: &in, ExperimentID: "exp_1"}
	cctx, ok := l.admit(cmd.CommandID)
	if !ok {
		t.Fatal("not admitted")
	}
	done := make(chan struct{})
	go func() { defer close(done); l.dispatch(cctx, cmd) }()
	f.waitAck(t, 1) // accepted: reserved, waiting for a slot
	l.cancelCommand(cmd.CommandID)
	<-done
	for range maxRunning {
		<-l.slots
	}
	cmd.CommandID = "cmd_b2"
	if reason, ok := l.validate(context.Background(), &cmd); !ok || !l.reserve(cmd) {
		t.Errorf("the experiment's next command was refused (%q): the canceled one kept its run", reason)
	}
}

// TestOversizedFrameIsRejectedNotRedelivered pins the 8 MiB frame cap's
// failure path: the stream ends on the frame, and its command is acked
// rejected — before the fix nothing answered it, Studio's backlog
// re-sent it on every reconnect (it was still queued) and the stream
// died on it again, holding every command behind it until the ack
// timer gave up on all of them.
func TestOversizedFrameIsRejectedNotRedelivered(t *testing.T) {
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	agent := core.New(&recordingModel{}, core.Name("acme-support"))
	cfg := &config{agents: []*core.Agent{agent}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	defer l.stop()
	big := "id: cmd_big\nevent: run\ndata: {\"command_id\":\"cmd_big\",\"input\":\"" +
		strings.Repeat("<", maxFrameBytes) + "\"}\n\n"
	err := l.readStream(context.Background(), strings.NewReader(big))
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("readStream = %v, want errFrameTooLarge", err)
	}
	if a := f.ackOf(t, "cmd_big", "rejected"); !strings.Contains(a.Error, "larger") {
		t.Errorf("rejection = %q", a.Error)
	}
	if got := l.lastEventID(); got != "cmd_big" {
		t.Errorf("resume cursor = %q, want past the oversized command", got)
	}
}

// TestInProcessTransportHonoursTheCallersContext pins setup A's parity
// with a socket: a Studio handler that has not answered yet does not
// hold the caller past its context (the register timeout, shutdown).
func TestInProcessTransportHonoursTheCallersContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	tr := &handlerTransport{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // ignores its context, answers late
		_, _ = w.Write([]byte("late"))
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://weft.studio.local/slow", nil)
	errc := make(chan error, 1)
	go func() {
		_, err := tr.RoundTrip(req)
		errc <- err
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("RoundTrip = %v, want the caller's deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RoundTrip outlived its caller's context by seconds")
	}
}

// TestLinkStopLeaksNoGoroutines pins stop's promise against the
// goroutine count: after a burst of commands — run, rejected busy,
// undecodable, canceled waiting for a slot — and a stop, the link's
// goroutines (the connect loop, dispatches, ack posts, the stream's
// HTTP connection) are all gone.
func TestLinkStopLeaksNoGoroutines(t *testing.T) {
	fs := newFakeStudio(t)
	ts := httptest.NewServer(fs.handler())
	defer ts.Close()
	gate := make(chan struct{})
	close(gate)
	base := goruntime.NumGoroutine()
	model := &gatedModel{model: wefttest.Script(wefttest.Say("a"), wefttest.Say("b"), wefttest.Say("c")), gate: gate, calls: make(chan struct{}, 8)}
	agent := core.New(model, core.Name("acme-support"))
	cfg := &config{agents: []*core.Agent{agent}}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	l.reconnect = func() time.Duration { return time.Millisecond }
	if err := l.start(); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		fs.frames <- runFrame(fmt.Sprintf("cmd_g%d", i))
	}
	fs.frames <- "id: cmd_bad\nevent: run\ndata: {nope\n\n"
	for i := range 3 {
		fs.ackOf(t, fmt.Sprintf("cmd_g%d", i), "finished")
	}
	fs.ackOf(t, "cmd_bad", "rejected")
	l.stop()
	ts.CloseClientConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && goruntime.NumGoroutine() > base {
		time.Sleep(10 * time.Millisecond)
	}
	if n := goruntime.NumGoroutine(); n > base {
		buf := make([]byte, 1<<20)
		t.Errorf("goroutines after stop = %d, before the link %d:\n%s", n, base, buf[:goruntime.Stack(buf, true)])
	}
}

// TestBreakpointIsNotSubstituted pins the debugger's breakpoint (§8.3)
// against substitute mode: the breakpoint parks its tool "whatever the
// command asked for" — a substitute-mode re-run whose source recorded
// that call must stop there, not answer the call from the record and
// run straight past the breakpoint.
func TestBreakpointIsNotSubstituted(t *testing.T) {
	var lookups atomic.Int64
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		lookups.Add(1)
		return "shipped", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1"}`, ID: "c1"}),
		wefttest.Say("done")), core.Name("acme-support"), lookup)
	l := newExecLink(nil, agent)
	defer l.stop()
	l.setBreakpoints([]string{"lookup_order"})
	src := &sourceRun{
		input: []core.Message{core.User("where is 1?")},
		steps: []core.Message{
			{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{"order_id":"1"}`)}}},
			{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "recorded"}}},
			core.Assistant("done"),
		},
	}
	cmd := command{CommandID: "cmd_bp", Agent: "acme-support", Source: &sourceSpec{RunID: "run_src"}, src: src, prefix: src.input}
	status, runID, errText := l.execute(context.Background(), cmd, "pg_bp")
	if status != "succeeded" {
		t.Fatalf("run = %s %s", status, errText)
	}
	l.mu.Lock()
	pr := l.parked[runID]
	l.mu.Unlock()
	if pr == nil || len(pr.pending) != 1 || pr.pending[0].Name != "lookup_order" {
		t.Errorf("the run did not stop at the breakpoint (parked: %+v): the record answered the call", pr)
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("lookup ran %d times", n)
	}
}

// TestForkAcceptedAckNamesNoInventedRun pins the fork's accepted ack:
// the turn's run id is the session's to mint (<session>-tN), known
// only once the turn starts; an accepted ack naming a fresh pg_ id
// sent Studio a run that never exists — its row linked to nothing, and
// a steer or decision on that id was answered 202 and went nowhere.
func TestForkAcceptedAckNamesNoInventedRun(t *testing.T) {
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	agent := core.New(&recordingModel{}, core.Name("acme-support"))
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	defer l.stop()
	in := "continue"
	cmd := command{CommandID: "cmd_fa", Agent: "acme-support", Thread: "fork", Engine: "live",
		Source: &sourceSpec{RunID: turn.RunID()}, Input: &in}
	cctx, ok := l.admit(cmd.CommandID)
	if !ok {
		t.Fatal("not admitted")
	}
	l.dispatch(cctx, cmd)
	if a := f.ackOf(t, "cmd_fa", "accepted"); a.RunID != "" {
		t.Errorf("fork accepted ack names run %q, a run that never exists", a.RunID)
	}
	if a := f.ackOf(t, "cmd_fa", "finished"); !strings.Contains(a.RunID, "-t") || a.Status != "succeeded" {
		t.Errorf("fork finished ack = %+v, want the fork's turn", a)
	}
}

// TestForkOfAParkedTurnDoesNotHang pins fork mode over a source turn
// that parked: Fork copies the open approval boundary, and a Send on it
// queues behind the boundary — the command would wait for a decision
// nobody can send (the runtime holds no park for the app's own turn),
// holding a run slot for good. It must fail fast and say why.
func TestForkOfAParkedTurnDoesNotHang(t *testing.T) {
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	model := &forkSteerModel{gate: make(chan struct{})}
	close(model.gate)
	agent := core.New(model, core.Name("acme-support"), refund)
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("refund please"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := turn.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("the app's turn did not park: %v %v", res, err)
	}
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	defer l.stop()
	in := "and then?"
	cmd := command{CommandID: "cmd_fp", Agent: "acme-support", Thread: "fork", Engine: "live",
		Source: &sourceSpec{RunID: turn.RunID()}, Input: &in}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatal(reason)
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		status, _, errText := l.execute(cctx, cmd, "pg_unused")
		done <- status + " " + errText
	}()
	select {
	case got := <-done:
		if !strings.HasPrefix(got, "failed") || !strings.Contains(got, "parked") {
			t.Errorf("fork of a parked turn = %q, want a failure naming the parked call(s)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the fork command hangs behind the source's open approval boundary")
	}
}

// TestEvictedForkParkStaysDecidable pins a fork's parked turn past the
// bounded park set: its record evicted (maxParked), the fork session
// this runtime still holds waits on the call. A new message on the fork
// must name the call to decide, and the decision must resume the fork
// — before the fix it answered "no parked run" while every new message
// answered "decide first": the fork was wedged for good.
func TestEvictedForkParkStaysDecidable(t *testing.T) {
	defer func(n int) { maxParked = n }(maxParked)
	maxParked = 1
	var refunds atomic.Int64
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		refunds.Add(1)
		return "refunded", nil
	})
	model := &forkSteerModel{gate: make(chan struct{})}
	close(model.gate)
	agent := core.New(model, core.Name("acme-support"), refund)
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	defer l.stop()
	forkCmd := func(id, source string) (string, string, string) {
		in := "refund please"
		cmd := command{CommandID: id, Agent: "acme-support", Thread: "fork", Engine: "live",
			Source: &sourceSpec{RunID: source}, Input: &in}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Fatal(reason)
		}
		return l.execute(ctx, cmd, "pg_unused")
	}
	_, parked, _ := forkCmd("cmd_e1", turn.RunID())
	if refunds.Load() != 0 {
		t.Fatal("refund ran in the fork's turn")
	}
	// Another park evicts the fork's record.
	l.rememberPark("pg_other", &parkedRun{decisions: map[string]approvalDecision{}})
	l.mu.Lock()
	_, held := l.parked[parked]
	l.mu.Unlock()
	if held {
		t.Fatal("the fork's park was not evicted")
	}
	if status, _, errText := forkCmd("cmd_e2", parked); status != "failed" || !strings.Contains(errText, "c_r") {
		t.Errorf("a new message on the parked fork = %s %q, want a failure naming call c_r", status, errText)
	}
	// Evicted again: the decision itself must find the call.
	l.rememberPark("pg_other2", &parkedRun{decisions: map[string]approvalDecision{}})
	decide(t, l, nil, approvalDecision{CommandID: "cmd_d", RunID: parked, CallID: "c_r", Decision: "approve"})
	if a := f.ackOf(t, "cmd_d", "finished"); a.Status != "succeeded" {
		t.Errorf("the decision on the evicted park = %+v, want the fork resumed", a)
	}
	if n := refunds.Load(); n != 1 {
		t.Errorf("approved refund ran %d times, want 1", n)
	}
}

// TestForgottenForksGiveUpTheirSessions pins the fork mode under
// thread's writer lease: a Session holds its session from its first
// write until Close, so a fork the runtime forgets (evicted past
// maxForks) or still holds at stop must be closed — or no other writer
// could ever continue that session (ErrLocked), and on jsonl its file
// lock would live as long as the process.
func TestForgottenForksGiveUpTheirSessions(t *testing.T) {
	defer func(n int) { maxForks = n }(maxForks)
	maxForks = 1
	agent := core.New(&recordingModel{}, core.Name("acme-support"))
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "")
	stopped := false
	defer func() {
		if !stopped {
			l.stop()
		}
	}()
	fork := func(id, input string) string {
		t.Helper()
		cmd := command{CommandID: id, Agent: "acme-support", Thread: "fork", Engine: "live",
			Source: &sourceSpec{RunID: turn.RunID()}, Input: &input}
		if reason, ok := l.validate(ctx, &cmd); !ok {
			t.Fatal(reason)
		}
		status, runID, errText := l.execute(ctx, cmd, "pg_unused")
		if status != "succeeded" {
			t.Fatalf("%s: %s %s", id, status, errText)
		}
		session, _, err := parseThreadRunID(runID)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	// writable reports whether another Session can write the session —
	// retried briefly, since an evicted fork is closed in the background.
	writable := func(session string) error {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			s, err := thread.Open(ctx, store, session, agent)
			if err != nil {
				return err
			}
			err = s.Label(ctx, s.Leaf(), "continued elsewhere")
			_ = s.Close(ctx)
			if err == nil || !errors.Is(err, thread.ErrLocked) || time.Now().After(deadline) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	first := fork("cmd_g1", "first")
	second := fork("cmd_g2", "second") // evicts the first fork
	if err := writable(first); err != nil {
		t.Errorf("the evicted fork %s is still held: %v", first, err)
	}
	l.stop()
	stopped = true
	if err := writable(second); err != nil {
		t.Errorf("the fork %s the stopped link held is still held: %v", second, err)
	}
}

// TestForkTurnAckedInFlight pins how Studio learns a fork turn's run
// id while it runs (so the panel can follow and steer it): the
// dispatch's accepted ack names no run (the session mints the id at
// Send), and awaitTurn acks accepted again naming the turn, before it
// finishes.
func TestForkTurnAckedInFlight(t *testing.T) {
	var mu sync.Mutex
	var acks []ack
	studio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a ack
		if r.URL.Path == "/api/runtime/acks" && json.NewDecoder(r.Body).Decode(&a) == nil {
			mu.Lock()
			acks = append(acks, a)
			mu.Unlock()
		}
	}))
	defer studio.Close()

	model := &forkSteerModel{gate: make(chan struct{})}
	close(model.gate)
	agent := core.New(model, core.Name("acme-support"))
	store := thread.Memory()
	ctx := context.Background()
	app, err := thread.Create(ctx, store, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := app.Send(ctx, core.User("hello app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	cfg := &config{agents: []*core.Agent{agent}, threads: store}
	l := newLink(cfg, newRegistry(cfg), studio.URL, "")
	defer l.stop()
	input := "hello app"
	cmd := command{CommandID: "cmd_fa", Agent: "acme-support", Thread: "fork", Engine: "live",
		Source: &sourceSpec{RunID: turn.RunID()}, Input: &input}
	if reason, ok := l.validate(ctx, &cmd); !ok {
		t.Fatal(reason)
	}
	status, runID, errText := l.execute(ctx, cmd, "pg_unused")
	if status != "succeeded" {
		t.Fatalf("fork = %s %s", status, errText)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(acks) != 1 || acks[0] != (ack{CommandID: "cmd_fa", State: "accepted", RunID: runID}) {
		t.Errorf("acks during the fork turn = %+v, want one accepted naming %s", acks, runID)
	}
}

// TestParkOnIsNotSubstituted pins the command's park_on against
// substitute mode, like a breakpoint: a re-run whose source recorded
// the parked call stops there — the record does not answer it — and a
// ReplaySafe tool in park_on parks instead of running or being
// substituted.
func TestParkOnIsNotSubstituted(t *testing.T) {
	var lookups atomic.Int64
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		lookups.Add(1)
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1"}`, ID: "c1"}),
		wefttest.Say("done")), core.Name("acme-support"), lookup)
	l := newExecLink(nil, agent)
	defer l.stop()
	src := &sourceRun{
		input: []core.Message{core.User("where is 1?")},
		steps: []core.Message{
			{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{"order_id":"1"}`)}}},
			{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "recorded"}}},
			core.Assistant("done"),
		},
	}
	cmd := command{CommandID: "cmd_po", Agent: "acme-support", Source: &sourceSpec{RunID: "run_src"}, src: src, prefix: src.input,
		Overrides: overrides{ParkOn: []string{"lookup_order"}}}
	status, runID, errText := l.execute(context.Background(), cmd, "pg_po")
	if status != "succeeded" {
		t.Fatalf("run = %s %s", status, errText)
	}
	l.mu.Lock()
	pr := l.parked[runID]
	l.mu.Unlock()
	if pr == nil || len(pr.pending) != 1 || pr.pending[0].Name != "lookup_order" {
		t.Errorf("the run did not stop at park_on (parked: %+v): the record answered the call", pr)
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("lookup ran %d times", n)
	}
}

// TestModelResolverIsBounded pins the resolver's deadline: one that
// never returns is cut off before the ack and the command rejected
// "resolver timed out" — never left unacked for Studio to mark lost.
func TestModelResolverIsBounded(t *testing.T) {
	defer func(d time.Duration) { resolveTimeout = d }(resolveTimeout)
	resolveTimeout = 20 * time.Millisecond
	agent := core.New(wefttest.Script(wefttest.Say("own")), core.Name("a"))
	cfg := &config{agents: []*core.Agent{agent}}
	ModelResolver(func(ctx context.Context, name string) (core.Model, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})(cfg)
	l := newLink(cfg, newRegistry(cfg), "", "")
	in := "hi"
	cmd := &command{CommandID: "cmd_slow", Agent: "a", Engine: "live", Thread: "ephemeral", Input: &in,
		Overrides: overrides{Model: "anthropic/claude-haiku-4-5"}}
	reason, ok := l.validate(context.Background(), cmd)
	if ok || !strings.Contains(reason, "model anthropic/claude-haiku-4-5: resolver timed out") {
		t.Errorf("slow resolver: reason = %q ok = %v, want rejected as timed out", reason, ok)
	}
}

// TestModelResolverIgnoringCtxIsAbandoned pins the bound for a resolver
// that never looks at ctx: validate returns at the timeout, rejected
// "resolver timed out", and the abandoned call's late result is dropped
// (the command carries no model).
func TestModelResolverIgnoringCtxIsAbandoned(t *testing.T) {
	defer func(d time.Duration) { resolveTimeout = d }(resolveTimeout)
	resolveTimeout = 20 * time.Millisecond
	block := make(chan struct{}) // never closed by the test
	late := make(chan struct{})
	agent := core.New(wefttest.Script(wefttest.Say("own")), core.Name("a"))
	cfg := &config{agents: []*core.Agent{agent}}
	ModelResolver(func(context.Context, string) (core.Model, error) {
		<-block
		close(late)
		return wefttest.Script(wefttest.Say("late")), nil
	})(cfg)
	l := newLink(cfg, newRegistry(cfg), "", "")
	in := "hi"
	cmd := &command{CommandID: "cmd_stuck", Agent: "a", Engine: "live", Thread: "ephemeral", Input: &in,
		Overrides: overrides{Model: "anthropic/claude-haiku-4-5"}}
	start := time.Now()
	reason, ok := l.validate(context.Background(), cmd)
	if ok || reason != "model anthropic/claude-haiku-4-5: resolver timed out after 20ms" {
		t.Errorf("stuck resolver: reason = %q ok = %v", reason, ok)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("validate took %v: the stuck resolver held it", d)
	}
	block <- struct{}{} // let the abandoned call finish: its result is dropped
	<-late
	if cmd.model != nil {
		t.Error("the abandoned call's late model reached the command")
	}
}

// TestValidateIsBoundedByTheAckWindow pins one deadline over the whole
// validation (validateTimeout), inside Studio's 30 s ack window: a slow
// source fetch and a slow resolver share it — the resolver gets what is
// left, never its own full bound on top — so the accepted ack can never
// land after Studio marked the command lost.
func TestValidateIsBoundedByTheAckWindow(t *testing.T) {
	defer func(v, r time.Duration) { validateTimeout, resolveTimeout = v, r }(validateTimeout, resolveTimeout)
	validateTimeout, resolveTimeout = 80*time.Millisecond, 10*time.Second

	// A resolver that waits on ctx: cut off at validate's deadline, not
	// at its own 10 s.
	agent := core.New(wefttest.Script(wefttest.Say("own")), core.Name("a"))
	cfg := &config{agents: []*core.Agent{agent}}
	ModelResolver(func(ctx context.Context, name string) (core.Model, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})(cfg)
	l := newLink(cfg, newRegistry(cfg), "", "")
	in := "hi"
	start := time.Now()
	reason, ok := l.validate(context.Background(), &command{CommandID: "cmd_slow", Agent: "a", Engine: "live",
		Thread: "ephemeral", Input: &in, Overrides: overrides{Model: "anthropic/claude-haiku-4-5"}})
	if ok || !strings.Contains(reason, "resolver timed out after") {
		t.Errorf("slow resolver: reason = %q ok = %v, want timed out", reason, ok)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("validate took %v: the resolver ran past validate's deadline", d)
	}

	// A source fetch that never answers: refused at the same deadline.
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stall.Close()
	cfg2 := &config{agents: []*core.Agent{agent}}
	l2 := newLink(cfg2, newRegistry(cfg2), stall.URL, "")
	l2.localDB = func() obsdb.DB { return nil }
	start = time.Now()
	reason, ok = l2.validate(context.Background(), &command{CommandID: "cmd_stall", Agent: "a", Engine: "live",
		Thread: "ephemeral", Source: &sourceSpec{RunID: "r_src", FromStep: 1}})
	if ok || !strings.Contains(reason, "source transcript unresolved") {
		t.Errorf("stalled source: reason = %q ok = %v, want unresolved", reason, ok)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("validate took %v on a stalled source: past validate's deadline", d)
	}
}

// TestValidatePastTheDeadlineIsRefused pins validate's own branch: a
// validation that succeeds — every check passed — but only after its
// deadline (a step that ignored ctx) is refused in validate's sentence,
// never acked accepted after Studio's ack window closed.
func TestValidatePastTheDeadlineIsRefused(t *testing.T) {
	defer func(v time.Duration) { validateTimeout = v }(validateTimeout)
	validateTimeout = 20 * time.Millisecond

	agent := core.New(wefttest.Script(wefttest.Say("own")), core.Name("a"))
	cfg := &config{agents: []*core.Agent{agent}}
	l := newLink(cfg, newRegistry(cfg), "http://127.0.0.1:1", "") // no Studio there
	defer l.stop()
	in := "and order 9?"
	cmd := func() *command {
		// A fresh input on a source nobody holds: validated, and run on
		// the input alone (prepareSource's fallback).
		return &command{CommandID: "cmd_late", Agent: "a", Thread: "ephemeral", Engine: "live",
			Source: &sourceSpec{RunID: "r_gone"}, Input: &in}
	}
	ctx := context.Background()
	l.localDB = func() obsdb.DB { return nil }
	if reason, ok := l.validate(ctx, cmd()); !ok {
		t.Fatalf("in time: %s", reason)
	}
	// A local-sink lookup that ignores the deadline: every check still
	// passes, just past validateTimeout.
	l.localDB = func() obsdb.DB { time.Sleep(3 * validateTimeout); return nil }
	want := fmt.Sprintf("validation took longer than %s, past Studio's ack window: the command is not run", validateTimeout)
	if reason, ok := l.validate(ctx, cmd()); ok || reason != want {
		t.Errorf("late: %v %q, want %q", ok, reason, want)
	}
}
