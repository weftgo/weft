// Command refund-plan demonstrates the orchestration ledger on a
// weft/thread session: a refund-support chat whose policy state lives in
// the session file as custom entries, so a process that dies mid-flow is
// replaced by one that resumes exactly where the file says — the last
// refund_plan entry. Never re-ask the customer facts the transcript
// already holds; never infer the step from the model's phrasing; never
// re-decide policy with newer rules than the customer was promised.
//
// The conversation is the scripted model's job; the policy is this
// program's. Every transition appends the COMPLETE current plan state —
// pi's rule: the complete, total current state, never a delta — so any
// single surviving entry is enough to resume, and the plan and the
// transcript share one atomic-append file, so they can never disagree.
//
// Run 1 — drive the chat and die after the manager hand-off:
//
//	go run ./thread/examples/refund-plan -crash
//
// Run 2 — a brand-new process resumes from the ledger and finishes:
//
//	go run ./thread/examples/refund-plan
//
// Run 3 — a plan version above what this build knows is refused loudly:
//
//	go run ./thread/examples/refund-plan -future
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// planKind is this application's namespace inside the session's custom
// entries. thread stays opaque to the kind and its data; the versioning
// discipline below belongs to this program.
const planKind = "refund_plan"

// planV is the plan schema this build reads. A ledger entry carrying a
// higher v is from a newer build of this program and must be refused,
// never guessed at — the entry format's "v" rule, re-expressed at the
// application layer.
const planV = 1

// The policy steps, in flow order. The empty step is a fresh
// conversation; done is a finished one.
const (
	stepAskedProblem = "asked_problem"
	stepReceipt      = "receipt_requested"
	stepManager      = "awaiting_manager"
	stepDone         = "done"
)

// Plan is the complete policy state, journaled after every transition.
// The whole struct is written each time: a torn or lost append costs one
// stale note, never a mis-folded position.
type Plan struct {
	V         int    `json:"v"`
	Step      string `json:"step"`
	OrderID   string `json:"order_id,omitempty"`
	Amount    int    `json:"amount,omitempty"`
	VIP       bool   `json:"vip,omitempty"`
	ReceiptOK bool   `json:"receipt_ok,omitempty"`
}

// stage is one move of the flow: the prompt this stage sends, the
// scripted model's answer, and the policy decision that advances the
// plan once the turn lands.
type stage struct {
	stepAfter string
	user      string
	reply     string
	advance   func(*Plan)
}

// stages is the whole policy, in order. A run builds its scripted model
// from the stage its resumed plan names — resume is literally "slice the
// remaining stages".
var stages = []stage{
	{stepAskedProblem,
		"I want a refund for order 1234.",
		"I'm sorry about that — what happened with the order?",
		func(p *Plan) { p.OrderID = "1234"; p.Step = stepAskedProblem }},
	{stepReceipt,
		"It arrived broken.",
		"Found order 1234 — $240.00. Could you upload the receipt?",
		func(p *Plan) {
			// The policy's own lookup (here fixed): $240, not VIP —
			// so the receipt is required and a manager must approve.
			p.Amount, p.VIP = 240, false
			p.Step = stepReceipt
		}},
	{stepManager,
		"Here is the receipt.",
		"Got it — checking with our team now.",
		func(p *Plan) { p.ReceiptOK = true; p.Step = stepManager }},
	{stepDone,
		// The manager queue is out of band; in this example it approves
		// between turns, and the approval arrives as an ordinary user
		// turn. (A real app would log it as s.CustomMessage with its own
		// kind, so the transcript can prove who said it.)
		"The manager approved the refund.",
		"Good news — your $240.00 refund is on its way back to your card.",
		func(p *Plan) { p.Step = stepDone }},
}

func main() {
	dir := flag.String("dir", filepath.Join(os.TempDir(), "weft-refund-plan"), "directory for the session files")
	crash := flag.Bool("crash", false, "die (exit 9) after the manager hand-off, before the flow finishes")
	future := flag.Bool("future", false, "inject a plan from a newer build and show the loud refusal")
	flag.Parse()
	if err := run(os.Stdout, *dir, *crash, *future); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer, dir string, crash, future bool) error {
	ctx := context.Background()
	st, err := jsonl.Open(dir)
	if err != nil {
		return err
	}

	// Find the demo's session, or create it on the first run. The file
	// is the only state that crosses process death.
	id, err := findSession(ctx, st)
	if err != nil {
		return err
	}
	var s *thread.Session
	if id == "" {
		s, err = thread.Create(ctx, st, agentFrom(0))
		if err != nil {
			return err
		}
		if err := p(w, "session", s.ID(), "created"); err != nil {
			return err
		}
	} else {
		s, err = thread.Open(ctx, st, id, agentFrom(0))
		if err != nil {
			return err
		}
	}

	// The resume point: the last refund_plan entry in the file.
	plan, at, err := readPlan(s)
	if err != nil {
		return err
	}
	if plan.Step == "" {
		if err := p(w, "fresh conversation: no plan entry yet"); err != nil {
			return err
		}
	} else {
		if err := p(w, "resumed at step", fmt.Sprintf("%q", plan.Step), "from entry", at,
			"— context holds", len(s.Context()), "messages"); err != nil {
			return err
		}
	}

	// A plan from a newer build: refused loudly, never guessed at.
	if future {
		if err := s.Custom(ctx, planKind, []byte(`{"v":9,"step":"awaiting_manager"}`)); err != nil {
			return err
		}
		if _, _, err := readPlan(s); err != nil {
			if perr := p(w, "future plan refused as designed:"); perr != nil {
				return perr
			}
			return err // the refusal is the result: a nonzero exit
		}
		return fmt.Errorf("-future: the injected plan read back clean; the refusal is missing")
	}

	// The flow: continue from the stage the plan names. The scripted
	// model answers only the turns this process actually drives, so the
	// session is reopened with the script this run needs — the script
	// is per-process state; the conversation comes from the file.
	from := stageIndex(plan.Step)
	s, err = thread.Open(ctx, st, s.ID(), agentFrom(from))
	if err != nil {
		return err
	}
	for i := from; i < len(stages); i++ {
		st := stages[i]
		turn, err := s.Send(ctx, weft.User(st.user))
		if err != nil {
			return err
		}
		res, err := turn.Wait()
		if err != nil {
			return err
		}
		if err := p(w, "turn:", st.user, "->", res.Text()); err != nil {
			return err
		}
		st.advance(&plan)
		plan.V = planV
		if err := writePlan(ctx, s, plan); err != nil {
			return err
		}
		if err := p(w, "plan ->", planJSON(plan)); err != nil {
			return err
		}
		if crash && plan.Step == stepManager {
			return simulatedDeath(w, dir, s.ID())
		}
	}
	return p(w, "flow complete: refund issued, session durable at", s.Leaf())
}

// agentFrom builds the scripted model with the answers of the stages
// from index i onward: a fresh run scripts all four turns, a resumed
// run only the ones it will drive.
func agentFrom(i int) *weft.Agent {
	turns := make([]wefttest.Turn, 0, len(stages)-i)
	for _, st := range stages[i:] {
		turns = append(turns, wefttest.Say(st.reply))
	}
	return weft.New(wefttest.Script(turns...))
}

// findSession returns the newest session id in the storage, or "" when
// the directory holds none (the first run).
func findSession(ctx context.Context, st thread.Storage) (string, error) {
	page, err := st.List(ctx, thread.Query{Limit: 1})
	if err != nil {
		return "", err
	}
	if len(page.Sessions) == 0 {
		return "", nil
	}
	return page.Sessions[0].ID, nil
}

// readPlan walks the session and returns the last refund_plan entry —
// the complete-current-state rule makes the last note sufficient. A
// ledger entry from a newer build (v above planV) fails loudly instead
// of being read with this build's tags.
func readPlan(s *thread.Session) (Plan, string, error) {
	var plan Plan
	var at string
	for _, e := range s.Entries() {
		c, ok := e.(thread.CustomEntry)
		if !ok || c.Kind != planKind {
			continue
		}
		var got Plan
		if err := json.Unmarshal(c.Data, &got); err != nil {
			return plan, at, fmt.Errorf("refund_plan entry %s does not decode: %w", c.ID, err)
		}
		if got.V > planV {
			return plan, at, fmt.Errorf("refund_plan v%d in entry %s is newer than this build (v%d); the session predates a migration — refusing instead of guessing", got.V, c.ID, planV)
		}
		plan, at = got, c.ID
	}
	return plan, at, nil
}

// writePlan appends the complete plan state as one custom entry: the
// sticky note this program leaves for the process that replaces it.
// custom never enters the model's context and survives every compaction
// (ADR 0020 §4) — exactly the two guarantees an orchestration ledger
// needs.
func writePlan(ctx context.Context, s *thread.Session, plan Plan) error {
	b, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return s.Custom(ctx, planKind, b)
}

// planJSON renders the plan compactly for the example's output.
func planJSON(plan Plan) string {
	b, _ := json.Marshal(plan)
	return string(b)
}

// stageIndex maps a plan step to the stage a run resumes at: the empty
// step starts the flow, any named step resumes after it, done is past
// the end.
func stageIndex(step string) int {
	for i, st := range stages {
		if st.stepAfter == step {
			return i + 1
		}
	}
	return 0
}

// simulatedDeath is Friday 17:03: the plan note for awaiting_manager is
// durable, the flow is one stage from done, and the process exits. The
// tail dump shows the sticky note sitting in the same file as the
// conversation it belongs to.
func simulatedDeath(w io.Writer, dir, sid string) error {
	if err := p(w, "manager queue notified; dying before the flow can finish"); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, sid+".jsonl"))
	if err == nil {
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if err := p(w, "file tail:"); err != nil {
			return err
		}
		for _, l := range lines[max(0, len(lines)-4):] {
			if err := p(w, "  |", l); err != nil {
				return err
			}
		}
	}
	if err := p(w, "simulated process death (exit 9) — run again without -crash to resume"); err != nil {
		return err
	}
	os.Exit(9)
	return nil
}

// p prints one line to the example's writer.
func p(w io.Writer, a ...any) error {
	_, err := fmt.Fprintln(w, a...)
	return err
}
