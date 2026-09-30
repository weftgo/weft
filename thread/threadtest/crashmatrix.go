//go:build unix

// The crash matrix (plan §10, step 7.1): every write point the session
// layer has, each proved under SIGKILL on a real backend. The write
// points, enumerated from the code (every storage.Append site the
// session layer owns):
//
//	prompt       Send's input entry, durable before the run starts
//	turn_end     the turn-end batch: the turn's messages and its turn entry
//	approval     a run parking: its approval request entries
//	decision     Decide's decision entries over the parked boundary
//	compaction   the compaction entry, summarizer usage and all
//	steer        a steer's acceptance receipt on a busy session (v0.3)
//	pool_receipt thread/pool's acceptance, mirror batch and settlement (v0.5)
//	branch       Branch's navigation and summary batch
//	fork         Fork's copy of the path into a session of its own
//	clear_queue  ClearQueue's dropped-receipt settlement
//	resume_arm   the resumed run's step persistence over a decided boundary
//	decide_signed DecideSigned's decision and Always-grant batch
//	expiry       Resume's expiry sweep: the lapsed request's audit and denial
//	trim         the auto-compaction trim record (ADR 0020 §4's pre-pass)
//
// The child performs the write, proves it is durable (the API returned
// means the backend synced), prints its marker and dies — SIGKILL, no
// cleanup, exactly a writer dropping dead at that write point. The
// prompt, steer, resume_arm and expiry points die mid-run instead (the parent kills them
// once the write is provably durable and the child is parked inside
// its model): their write happened on the way into the run, and the
// crash lands in the harder window, between write points. The parent
// then reopens and asserts each point's invariant: the entries the
// point promises, nothing torn the backend does not report, and a
// session that still continues.

package threadtest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// crashMatrixEnv names the point the child dies at; its value is one
// of the points above. The storage location rides the same env the
// other crash helpers read (WEFT_THREADTEST_CRASH_STORAGE).
const crashMatrixEnv = "WEFT_THREADTEST_CRASH_MATRIX"

// The four sessions the child creates, one per family of write
// points; the parent asserts over each by id.
const (
	CrashMatrixTurnID  = "s_mx_turn"  // prompt and turn_end
	CrashMatrixParkID  = "s_mx_park"  // approval, decision, compaction, resume_arm, decide_signed
	CrashMatrixSteerID = "s_mx_steer" // steer and clear_queue
	CrashMatrixPoolID  = "s_mx_pool"  // pool_receipt
	CrashMatrixForkID  = "s_mx_fork"  // fork (the forked session's own id)
	CrashMatrixTrimID  = "s_mx_trim"  // trim
)

// crashPoints is the full matrix, in walk order. The second block is
// the 7.1 review's additions: the five Append sites the first walk
// missed, found by enumerating every storage.Append call site in the
// session layer and comparing (branch, fork, the steer queue's drop
// receipts, the resumed run's step persistence, and DecideSigned's decision+grant
// batch).
var crashPoints = []string{
	"prompt", "turn_end", "approval", "decision", "compaction", "steer", "pool_receipt",
	"branch", "fork", "clear_queue", "resume_arm", "decide_signed",
	// The post-0.7 review's: the last two Append sites, until then
	// covered by shape only (the expiry sweep, the auto trim).
	"expiry", "trim",
}

// parentKilled are the points where the parent lands the kill (the
// child is mid-run, blocked in its model); the rest self-kill right
// after their write returned.
func parentKilled(point string) bool {
	return point == "prompt" || point == "steer" || point == "resume_arm" || point == "expiry"
}

// CrashMatrix runs the parent side over every write point: for each,
// it re-executes the test binary at helperTest (the backend's child,
// gated on the crash env) with the point set and its own fresh storage
// location (pathFor — the points must not share sessions), waits out
// the child, and asserts the point's invariant through reopen — a
// fresh Storage over the same location, the shape of the next process
// after the crash. open is the backend's constructor over a location.
func CrashMatrix(t *testing.T, helperTest string, pathFor func(point string) string, open func(path string) (thread.Storage, error)) {
	t.Helper()
	for _, point := range crashPoints {
		point := point
		path := pathFor(point)
		t.Run(point, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^"+helperTest+"$", "-test.count=1")
			cmd.Env = append(os.Environ(), crashMatrixEnv+"="+point, "WEFT_THREADTEST_CRASH_STORAGE="+path)
			var stderr bytes.Buffer // a child's panic trace lands here, not on stdout
			cmd.Stderr = &stderr
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			marker := "crashmx:" + point
			sawMarker := make(chan struct{})
			var lines []string
			var mu sync.Mutex
			go func() {
				defer close(sawMarker)
				sc := bufio.NewScanner(out)
				for sc.Scan() {
					mu.Lock()
					lines = append(lines, sc.Text())
					mu.Unlock()
					if sc.Text() == marker || sc.Text() == marker+":waiting" {
						return
					}
				}
			}()
			select {
			case <-sawMarker:
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait() // reap the child; closes the pipe and settles stderr
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("the child never reached %q:\n%s\nstderr:\n%s", marker, strings.Join(lines, "\n"), stderr.String())
			}
			if parentKilled(point) {
				_ = cmd.Process.Kill() // SIGKILL mid-run: no cleanup, no flush
			}
			_ = cmd.Wait() // the child died by SIGKILL: the error is expected
			mu.Lock()
			got := strings.Join(lines, "\n")
			mu.Unlock()
			if !strings.Contains(got, marker) {
				t.Fatalf("marker %q never printed:\n%s\nstderr:\n%s", marker, got, stderr.String())
			}

			st, err := open(path)
			if err != nil {
				t.Fatalf("reopen after the crash at %s: %v", point, err)
			}
			assertCrashPoint(t, point, st)
		})
	}
}

// assertCrashPoint holds each write point's invariant over the
// reopened storage.
func assertCrashPoint(t *testing.T, point string, st thread.Storage) {
	t.Helper()
	ctx := context.Background()
	switch point {
	case "prompt":
		h, entries, report, err := st.Load(ctx, CrashMatrixTurnID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if h.ID != CrashMatrixTurnID || kindsOf(entries) != "message" {
			t.Fatalf("prompt point: header %q, kinds %q", h.ID, kindsOf(entries))
		}
		continueTurn(t, st, CrashMatrixTurnID, 2) // the prompt and the follow-up
	case "turn_end":
		_, entries, report, err := st.Load(ctx, CrashMatrixTurnID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); k != "message,message,turn" {
			t.Fatalf("turn_end kinds = %q", k)
		}
		continueTurn(t, st, CrashMatrixTurnID, 3) // + the follow-up's answer
	case "approval":
		_, entries, _, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil {
			t.Fatal(err)
		}
		// The parked turn is a whole turn — its entry lands at the
		// park, with the request and its audit behind it.
		if k := kindsOf(entries); !strings.HasSuffix(k, ",turn,approval_request,approval_audit") ||
			strings.Count(k, "approval_request") != 1 {
			t.Fatalf("approval kinds = %q", k)
		}
		// The parked boundary decides and the run completes: the
		// continuation is the whole approval cycle, not just a Send.
		s := openMatrixSession(t, st, CrashMatrixParkID)
		pend := s.Pending()
		if len(pend) != 1 {
			t.Fatalf("pending after the crash = %+v", pend)
		}
		if _, err := s.Decide(ctx, thread.Approve(pend[0].CallID)); err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("the boundary stayed open: %+v", p)
		}
	case "decision":
		_, entries, _, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); !strings.Contains(k, "approval_decision") {
			t.Fatalf("decision kinds = %q", k)
		}
		s := openMatrixSession(t, st, CrashMatrixParkID)
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("a decided boundary reopened pending: %+v", p)
		}
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "compaction":
		_, entries, _, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); !strings.HasSuffix(k, ",compaction") {
			t.Fatalf("compaction kinds = %q", k)
		}
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "steer":
		_, entries, report, err := st.Load(ctx, CrashMatrixSteerID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		// Two durable shapes, both legitimate, decided by where the
		// steer met the run: accepted while the second model call was
		// already in flight, it stays queued — receipt only; accepted
		// in the between-steps window, steerSource hands it to the
		// next call and the per-step observer persists its message
		// after the receipt (ADR 0019's drain point, ADR 0011 §7's
		// persist). Load's loaded box widens that window; both trees
		// are exactly the crash state the point proves.
		if k := kindsOf(entries); k != "message,message,message,receipt" &&
			k != "message,message,message,receipt,message" {
			t.Fatalf("steer kinds = %q", k)
		}
		for _, e := range entries {
			if _, ok := e.(thread.TurnEntry); ok {
				t.Fatal("a turn entry landed for a turn that never ended")
			}
		}
		continueTurn(t, st, CrashMatrixSteerID, -1)
	case "pool_receipt":
		_, entries, _, err := st.Load(ctx, CrashMatrixPoolID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); k != "pool_receipt,approval_request,pool_receipt" {
			t.Fatalf("pool_receipt kinds = %q", k)
		}
		s := openMatrixSession(t, st, CrashMatrixPoolID)
		if p := s.Pending(); len(p) != 1 || p[0].Child == "" {
			t.Fatalf("the mirrored request after the crash = %+v", p)
		}
		if err := s.SetInfo(ctx, "still alive", nil); err != nil {
			t.Fatalf("the session is wedged: %v", err)
		}
	case "branch":
		// Branch writes two entries atomically: the navigation off the
		// old leaf and the summary on the new line. Both are there, and
		// the session still branches again.
		_, entries, _, err := st.Load(ctx, CrashMatrixTurnID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); k != "message,message,turn,leaf,branch_summary" {
			t.Fatalf("branch kinds = %q", k)
		}
		s := openMatrixSession(t, st, CrashMatrixTurnID)
		first := s.Entries()[0].(thread.MessageEntry).ID
		if err := s.Branch(ctx, first, thread.SummarizeLeft()); err != nil {
			t.Fatalf("the session no longer branches: %v", err)
		}
	case "fork":
		// Fork copies the path into a session of its own: the child
		// loads with the copied entries — the same kinds, their own
		// file — and continues.
		_, entries, _, err := st.Load(ctx, CrashMatrixForkID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); k != "message,message,turn" {
			t.Fatalf("fork kinds = %q", k)
		}
		continueTurn(t, st, CrashMatrixForkID, 3) // the copied prompt and answer + the follow-up
	case "clear_queue":
		// ClearQueue drops the queued steer's receipt in one batch: the
		// drop is durable (a second receipt entry) and the queue reads
		// empty after the reopen. The drained shape is the steer
		// point's second timing: steerSource handed the steer to the
		// blocked call between steps, so the queue ClearQueue meets is
		// already empty — nothing to drop, the steer's message
		// persisted instead. Both trees are the point's crash state.
		_, entries, _, err := st.Load(ctx, CrashMatrixSteerID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); k != "message,message,message,receipt,receipt" &&
			k != "message,message,message,receipt,message" {
			t.Fatalf("clear_queue kinds = %q", k)
		}
		s := openMatrixSession(t, st, CrashMatrixSteerID)
		if n, err := s.ClearQueue(ctx); err != nil || n != 0 {
			t.Fatalf("ClearQueue after the crash: %d, %v", n, err)
		}
		continueTurn(t, st, CrashMatrixSteerID, -1)
	case "resume_arm":
		// The resumed run's step is durable: the approved call's result
		// follows the decision, and no turn entry — the resumed turn
		// never ended. The boundary reads resolved, and the reopen
		// continues.
		_, entries, report, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); !strings.HasSuffix(k, ",turn,approval_request,approval_audit,approval_decision,approval_audit,message") {
			t.Fatalf("resume_arm kinds = %q", k)
		}
		step := entries[len(entries)-1].(thread.MessageEntry).Message
		if len(step.Content) != 1 {
			t.Fatalf("the persisted step = %+v, want the approved call's result", step)
		}
		if r, ok := step.Content[0].(weft.ToolResultPart); !ok ||
			r.CallID != "call_mx" || r.Content != "spent" || r.IsError {
			t.Fatalf("the persisted step = %+v, want the approved call's result", step)
		}
		s := openMatrixSession(t, st, CrashMatrixParkID)
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("the armed boundary reopened pending: %+v", p)
		}
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "decide_signed":
		// DecideSigned records the decision and the Always grant in
		// one batch: both durable, the boundary closed, the grant
		// readable.
		_, entries, _, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); !strings.Contains(k, "approval_decision") || !strings.Contains(k, "grant") {
			t.Fatalf("decide_signed kinds = %q", k)
		}
		s := openMatrixSession(t, st, CrashMatrixParkID)
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("the decided boundary reopened pending: %+v", p)
		}
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "expiry":
		// Resume's sweep denied the lapsed request — its audit and the
		// denial, via expiry — and the resumed run persisted the denied
		// call's error result before the kill. The boundary reads
		// resolved, and the reopen continues.
		_, entries, report, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); !strings.HasSuffix(k, ",turn,approval_request,approval_audit,approval_audit,approval_decision,approval_audit,message") {
			t.Fatalf("expiry kinds = %q", k)
		}
		d, ok := entries[len(entries)-3].(thread.ApprovalDecisionEntry)
		if !ok || d.Via != "expiry" || d.Outcome != thread.OutcomeDeny {
			t.Fatalf("the sweep's decision = %+v", entries[len(entries)-3])
		}
		step := entries[len(entries)-1].(thread.MessageEntry).Message
		if len(step.Content) != 1 {
			t.Fatalf("the persisted step = %+v, want the denied call's result", step)
		}
		if r, ok := step.Content[0].(weft.ToolResultPart); !ok || r.CallID != "call_mx" || !r.IsError {
			t.Fatalf("the persisted step = %+v, want the denied call's error result", step)
		}
		s := openMatrixSession(t, st, CrashMatrixParkID)
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("the swept boundary reopened pending: %+v", p)
		}
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "trim":
		// The trim record is durable — a compaction entry with the trim
		// reason and no summary — and the session continues over it.
		_, entries, report, err := st.Load(ctx, CrashMatrixTrimID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); !strings.HasSuffix(k, ",turn,compaction") {
			t.Fatalf("trim kinds = %q", k)
		}
		if c := entries[len(entries)-1].(thread.CompactionEntry); c.Reason != thread.ReasonTrim || c.Summary != "" {
			t.Fatalf("the trim record = %+v", c)
		}
		continueTurn(t, st, CrashMatrixTrimID, -1)
	default:
		t.Fatalf("unknown point %q", point)
	}
}

// kindsOf renders an entry list's kinds in order, comma-joined — the
// shape each write point is asserted against.
func kindsOf(entries []thread.Entry) string {
	var ks []string
	for _, e := range entries {
		switch e.(type) {
		case thread.MessageEntry:
			ks = append(ks, "message")
		case thread.TurnEntry:
			ks = append(ks, "turn")
		case thread.ApprovalRequestEntry:
			ks = append(ks, "approval_request")
		case thread.ApprovalDecisionEntry:
			ks = append(ks, "approval_decision")
		case thread.ApprovalAuditEntry:
			ks = append(ks, "approval_audit")
		case thread.GrantEntry:
			ks = append(ks, "grant")
		case thread.GrantRevokedEntry:
			ks = append(ks, "grant_revoked")
		case thread.CompactionEntry:
			ks = append(ks, "compaction")
		case thread.ReceiptEntry:
			ks = append(ks, "receipt")
		case thread.PoolReceiptEntry:
			ks = append(ks, "pool_receipt")
		case thread.LabelEntry:
			ks = append(ks, "label")
		case thread.LeafEntry:
			ks = append(ks, "leaf")
		case thread.BranchSummaryEntry:
			ks = append(ks, "branch_summary")
		case thread.InfoEntry:
			ks = append(ks, "info")
		case thread.CustomEntry:
			ks = append(ks, "custom")
		default:
			ks = append(ks, "other")
		}
	}
	return strings.Join(ks, ",")
}

// matrixSpend is the park family's gated tool.
func matrixSpend() *weft.ToolDef {
	return weft.Tool("spend", "Spend money.", func(_ context.Context, _ struct{}) (string, error) {
		return "spent", nil
	}, weft.RequireApproval())
}

// plainSpend is the follow-up's spend — plain, no gate: what a crash
// point parked is decided by then, and the resumed run must execute
// it, not re-ask.
func plainSpend() *weft.ToolDef {
	return weft.Tool("spend", "Spend money.", func(_ context.Context, _ struct{}) (string, error) {
		return "spent", nil
	})
}

// matrixNote is the steer family's step tool, plain for the follow-up.
func matrixNote() *weft.ToolDef {
	return weft.Tool("note", "Record a note.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "noted: " + in.Text, nil
	})
}

// openMatrixSession reopens id under the matrix's follow-up agent and
// returns the session ready to continue.
func openMatrixSession(t *testing.T, st thread.Storage, id string) *thread.Session {
	t.Helper()
	s, err := thread.Open(context.Background(), st, id, weft.New(&countingModel{}, plainSpend(), matrixNote()))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// continueTurn proves the crashed session still runs: a follow-up Send
// completes and the model saw at least minFed messages of context (the
// crash point's durable prefix), the last being the follow-up prompt.
// minFed below zero skips the count — points whose follow-up rides a
// resume or a queued steer, where the exact count is the machinery's
// business, not this assertion's.
func continueTurn(t *testing.T, st thread.Storage, id string, minFed int) {
	t.Helper()
	ctx := context.Background()
	m := &countingModel{}
	s, err := thread.Open(ctx, st, id, weft.New(m, plainSpend(), matrixNote()))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("continue"))
	if err != nil {
		t.Fatalf("the follow-up Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("the follow-up turn: %v", err)
	}
	if minFed >= 0 {
		reqs := m.Requests()
		if len(reqs) == 0 {
			t.Fatal("the follow-up made no model call")
		}
		last := reqs[len(reqs)-1]
		if len(last.Messages) == 0 {
			t.Fatal("the follow-up fed the model no messages")
		}
		if len(last.Messages) < minFed || last.Messages[len(last.Messages)-1].Text() != "continue" {
			t.Fatalf("fed %d messages, last %q", len(last.Messages), last.Messages[len(last.Messages)-1].Text())
		}
	}
}

// RunCrashMatrixChild is the child side, called from the backend's
// re-executed helper test: it opens its storage through open and dies
// at the point the env names. It returns only when the env gate is
// unset (the parent's own run).
func RunCrashMatrixChild(t *testing.T, open func() (thread.Storage, error)) {
	t.Helper()
	point := os.Getenv(crashMatrixEnv)
	if point == "" {
		return // the parent's own run, or a plain `go test`
	}
	st, err := open()
	if err != nil {
		fmt.Println("helper: open failed:", err)
		os.Exit(2)
	}
	switch point {
	case "prompt", "turn_end":
		crashMatrixTurnChild(point, st)
	case "approval", "decision", "compaction":
		crashMatrixParkChild(point, st)
	case "steer":
		crashMatrixSteerChild(st)
	case "pool_receipt":
		crashMatrixPoolChild(st)
	case "branch", "fork":
		crashMatrixBranchChild(point, st)
	case "clear_queue":
		crashMatrixQueueChild(st)
	case "resume_arm", "decide_signed":
		crashMatrixSignedChild(point, st)
	case "expiry":
		crashMatrixExpiryChild(st)
	case "trim":
		crashMatrixTrimChild(st)
	default:
		fmt.Println("helper: unknown point", point)
		os.Exit(2)
	}
}

// dieAt prints the point's marker and SIGKILLs this process — no
// cleanup, no flush, a writer dropping dead right after its write.
func dieAt(point string) {
	fmt.Println("crashmx:" + point)
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		fmt.Println("helper: kill failed:", err)
		os.Exit(2)
	}
	time.Sleep(time.Hour) // unreachable; the kill is immediate
}

// fixedIDs returns the id source minting id first, then ordinary ids.
func fixedIDs(id string) thread.SessionOption {
	first := true
	return thread.IDs(func() string {
		if first {
			first = false
			return id
		}
		return thread.NewEntryID()
	})
}

// crashMatrixTurnChild covers the prompt and turn_end points: one
// turn. At prompt the child announces itself once Send has returned —
// the prompt entry is durable by then — and sleeps; the parent kills
// it while its model is blocked mid-run. At turn_end the turn
// completes and the child dies right after the batch.
func crashMatrixTurnChild(point string, st thread.Storage) {
	ctx := context.Background()
	block := make(chan struct{}) // never closed in the child
	s, err := thread.Create(ctx, st, weft.New(&mxTurnModel{block: block, point: point}), fixedIDs(CrashMatrixTurnID))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("mx one"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	if point == "prompt" {
		// Send returned: the prompt is durable, the run is flying and
		// about to block in its model. The parent lands the kill.
		fmt.Println("crashmx:prompt:waiting")
		time.Sleep(time.Hour)
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println("helper: turn failed:", err)
		os.Exit(2)
	}
	dieAt(point)
}

// crashMatrixParkChild covers the approval, decision and compaction
// points: a turn that parks at a gated call, the decision over it
// (AutoResume off — the decision write alone), then a manual
// compaction whose summarizer call is the model's second turn.
func crashMatrixParkChild(point string, st thread.Storage) {
	ctx := context.Background()
	spend := matrixSpend()
	// KeepRecent tiny so the manual compaction at the last point has a
	// cut to make: the walk's one turn would otherwise fit inside the
	// default 20k-token tail and Compact refuse ("nothing to
	// compact") — the point is the write, not the policy.
	s, err := thread.Create(ctx, st, weft.New(&mxParkModel{}, spend), fixedIDs(CrashMatrixParkID),
		thread.AutoResume(false), thread.KeepRecent(1))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("mx spend"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println("helper: turn failed:", err)
		os.Exit(2)
	}
	if len(s.Pending()) != 1 {
		fmt.Println("helper: the turn did not park:", s.Pending())
		os.Exit(2)
	}
	if point == "approval" {
		dieAt(point)
	}
	if _, err := s.Decide(ctx, thread.Approve(s.Pending()[0].CallID)); err != nil {
		fmt.Println("helper: decide failed:", err)
		os.Exit(2)
	}
	if point == "decision" {
		dieAt(point)
	}
	// Compact refuses a boundary that only looks resolved: the decision
	// is recorded but the call dangles until the resume runs it. Resume
	// (the manual half of AutoResume) completes the boundary first.
	rt, err := s.Resume(ctx)
	if err != nil {
		fmt.Println("helper: resume failed:", err)
		os.Exit(2)
	}
	if _, err := rt.Wait(); err != nil {
		fmt.Println("helper: resumed turn failed:", err)
		os.Exit(2)
	}
	if err := s.Compact(ctx); err != nil {
		fmt.Println("helper: compact failed:", err)
		os.Exit(2)
	}
	dieAt(point)
}

// crashMatrixSteerSession runs the steer walk's setup: a session whose
// two-step turn is blocked in its second model call with the first
// step fully emitted, and a steer accepted after it — the steer and
// clear_queue points share it.
func crashMatrixSteerSession(st thread.Storage) *thread.Session {
	ctx := context.Background()
	block := make(chan struct{}) // never closed in the child
	s, err := thread.Create(ctx, st, weft.New(&mxSteerModel{block: block}, matrixNote()), fixedIDs(CrashMatrixSteerID))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("mx steer"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	sawStep := false
	for ev, serr := range turn.Events() {
		if serr != nil {
			fmt.Println("helper: stream error:", serr)
			os.Exit(2)
		}
		if f, ok := ev.(weft.StepFinish); ok && f.Index == 0 && !sawStep {
			sawStep = true
			break // the run stays flying, blocked in its second model call
		}
	}
	if !sawStep {
		fmt.Println("helper: the first step never finished")
		os.Exit(2)
	}
	// The receipt lands before the caller announces itself; the steer
	// never resolves the run, which stays blocked in its model.
	if _, err := s.Send(ctx, weft.User("steer it"), thread.As(thread.Steer)); err != nil {
		fmt.Println("helper: steer failed:", err)
		os.Exit(2)
	}
	return s
}

// crashMatrixSteerChild covers the steer point: the walk above, the
// child announcing itself, and the parent killing it mid-second-step.
func crashMatrixSteerChild(st thread.Storage) {
	crashMatrixSteerSession(st)
	fmt.Println("crashmx:steer:waiting")
	time.Sleep(time.Hour) // the kill arrives mid-second-step
}

// crashMatrixPoolChild covers the pool_receipt point: the three v0.5
// writes a pool performs on a parent session — acceptance, the mirror
// batch, the settlement — each durable, then death.
func crashMatrixPoolChild(st thread.Storage) {
	ctx := context.Background()
	s, err := thread.Create(ctx, st, weft.New(&replayModel{}), fixedIDs(CrashMatrixPoolID))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	accept, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Status: thread.PoolAccepted, Child: "s_mx_kid", Prompt: "mx",
	})
	if err != nil {
		fmt.Println("helper: acceptance failed:", err)
		os.Exit(2)
	}
	if _, err := s.AppendApprovalRequests(ctx, thread.ApprovalRequestEntry{
		CallID: "s_mx_kid/call_mx", Tool: "spend", Args: []byte(`{"order":"1"}`),
		ArgsSHA256: "mx", Child: "s_mx_kid", Wrapper: "call_wrap",
	}); err != nil {
		fmt.Println("helper: mirror failed:", err)
		os.Exit(2)
	}
	if _, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: accept.ID, Status: thread.PoolDone, Child: "s_mx_kid", Stop: "mx done",
	}); err != nil {
		fmt.Println("helper: settlement failed:", err)
		os.Exit(2)
	}
	dieAt("pool_receipt")
}

// crashMatrixBranchChild covers the branch and fork points: one
// completed turn, then either Branch — the navigation and summary
// batch — on the same session, or Fork, which copies the path into a
// session of its own (its id minted by the fork's own ids option) and
// dies right after the copy returned.
func crashMatrixBranchChild(point string, st thread.Storage) {
	ctx := context.Background()
	src := CrashMatrixTurnID
	if point == "fork" {
		src = "s_mx_forksrc"
	}
	s, err := thread.Create(ctx, st, weft.New(&mxTurnModel{block: make(chan struct{}), point: "turn_end"}), fixedIDs(src))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("mx one"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println("helper: turn failed:", err)
		os.Exit(2)
	}
	if point == "branch" {
		// SummarizeLeft needs a left side: branch at the prompt's entry,
		// the completed turn's answer is the branch that gets summarized.
		at := s.Entries()[0].(thread.MessageEntry).ID
		if err := s.Branch(ctx, at, thread.SummarizeLeft()); err != nil {
			fmt.Println("helper: branch failed:", err)
			os.Exit(2)
		}
		dieAt(point)
	}
	if _, err := s.Fork(ctx, s.Leaf(), fixedIDs(CrashMatrixForkID)); err != nil {
		fmt.Println("helper: fork failed:", err)
		os.Exit(2)
	}
	dieAt(point)
}

// crashMatrixQueueChild covers the clear_queue point: the steer walk —
// a turn mid-second-step with a steer accepted — and then the queue
// dropped, its receipts' settlement durable, and death. The walk's
// steer may have been drained between steps (the steer point's second
// timing): then the queue is already empty and there is nothing to
// drop — either landing is the point's crash state.
func crashMatrixQueueChild(st thread.Storage) {
	s := crashMatrixSteerSession(st)
	if n, err := s.ClearQueue(context.Background()); err != nil || n > 1 {
		fmt.Println("helper: clear failed:", n, err)
		os.Exit(2)
	}
	dieAt("clear_queue")
}

// crashMatrixSignedChild covers the resume_arm and decide_signed
// points over one parked boundary: the resumed run's first step
// persisted (the parent killing it inside the resume's model call), or
// the signed decision and its Always grant recorded in one batch.
func crashMatrixSignedChild(point string, st thread.Storage) {
	ctx := context.Background()
	key := []byte("mx-key-material")
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: key, Active: true})
	if err != nil {
		fmt.Println("helper: keyring failed:", err)
		os.Exit(2)
	}
	spend := matrixSpend()
	// At resume_arm the resume's model call blocks: the approved call
	// runs and its result is persisted by the step observer, then the
	// run parks inside its model and the parent lands the kill.
	model := &mxParkModel{blockResume: point == "resume_arm"}
	s, err := thread.Create(ctx, st, weft.New(model, spend), fixedIDs(CrashMatrixParkID),
		thread.AutoResume(false), thread.WithKeyring(ring))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("mx spend"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println("helper: turn failed:", err)
		os.Exit(2)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		fmt.Println("helper: the turn did not park:", pend)
		os.Exit(2)
	}
	if point == "resume_arm" {
		if _, err := s.Decide(ctx, thread.Approve(pend[0].CallID)); err != nil {
			fmt.Println("helper: decide failed:", err)
			os.Exit(2)
		}
		if _, err := s.Resume(ctx); err != nil {
			fmt.Println("helper: resume failed:", err)
			os.Exit(2)
		}
		// Resume writes nothing itself (no expiry here): the write this
		// point proves is the resumed run's step persistence — the
		// approved call's result, appended by the observer before the
		// run's next model call. Session.Entries adopts an entry only
		// after its Append returned, so seeing it means it is durable.
		waitPersisted(s)
		fmt.Println("crashmx:resume_arm:waiting")
		time.Sleep(time.Hour) // the kill arrives inside the resume's model call
	}
	req, err := s.Request(pend[0].CallID)
	if err != nil {
		fmt.Println("helper: request failed:", err)
		os.Exit(2)
	}
	sd := thread.SignDecision(key, req, thread.ApproveAlways(req.CallID))
	if _, err := s.DecideSigned(ctx, sd); err != nil {
		fmt.Println("helper: decide signed failed:", err)
		os.Exit(2)
	}
	dieAt(point)
}

// crashMatrixExpiryChild covers the expiry point: a request parked
// with a lifetime that lapses, then Resume — whose sweep writes the
// audit and the denial in one batch before the resume runs. The
// resume's model call blocks: the denied call's result persists, the
// child announces itself, and the parent lands the kill.
func crashMatrixExpiryChild(st thread.Storage) {
	ctx := context.Background()
	s, err := thread.Create(ctx, st, weft.New(&mxParkModel{blockResume: true}, matrixSpend()),
		fixedIDs(CrashMatrixParkID), thread.AutoResume(false), thread.RequestExpiry(time.Millisecond))
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("mx spend"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println("helper: turn failed:", err)
		os.Exit(2)
	}
	if len(s.Pending()) != 1 {
		fmt.Println("helper: the turn did not park:", s.Pending())
		os.Exit(2)
	}
	time.Sleep(10 * time.Millisecond) // strictly past the lifetime
	if _, err := s.Resume(ctx); err != nil {
		fmt.Println("helper: resume failed:", err)
		os.Exit(2)
	}
	waitPersisted(s)
	fmt.Println("crashmx:expiry:waiting")
	time.Sleep(time.Hour) // the kill arrives inside the resume's model call
}

// crashMatrixTrimChild covers the trim point: a session whose history
// carries two bulky tool results, over a window the next turn's usage
// crosses — the trimmer's stub of the older result brings the context
// back under the line, so the automatic path writes the trim record,
// not a summary. The child dies right after the record is durable.
func crashMatrixTrimChild(st thread.Storage) {
	ctx := context.Background()
	opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.ClearOldToolResults(1)}
	model := &mxTrimModel{}
	s, err := thread.Create(ctx, st, weft.New(model), append(opts, fixedIDs(CrashMatrixTrimID))...)
	if err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_t1", Created: now, Message: weft.User("run the tools")},
		thread.MessageEntry{ID: "e_t2", ParentID: "e_t1", Created: now, Message: weft.Message{
			Role: weft.RoleAssistant,
			Content: []weft.Part{
				weft.ToolCallPart{ID: "c1", Name: "read", Args: []byte("{}")},
				weft.ToolCallPart{ID: "c2", Name: "read", Args: []byte("{}")},
			},
		}},
		thread.MessageEntry{ID: "e_t3", ParentID: "e_t2", Created: now, Message: weft.Message{
			Role: weft.RoleTool,
			Content: []weft.Part{
				weft.ToolResultPart{CallID: "c1", Name: "read", Content: strings.Repeat("r", 40_000)},
				weft.ToolResultPart{CallID: "c2", Name: "read", Content: strings.Repeat("r", 40_000)},
			},
		}},
		thread.MessageEntry{ID: "e_t4", ParentID: "e_t3", Created: now, Message: weft.Assistant("done")},
	); err != nil {
		fmt.Println("helper: seed failed:", err)
		os.Exit(2)
	}
	// Reopen so the session adopts the seeded history.
	s, err = thread.Open(ctx, st, CrashMatrixTrimID, weft.New(model), opts...)
	if err != nil {
		fmt.Println("helper: reopen failed:", err)
		os.Exit(2)
	}
	turn, err := s.Send(ctx, weft.User("again"))
	if err != nil {
		fmt.Println("helper: send failed:", err)
		os.Exit(2)
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println("helper: turn failed:", err)
		os.Exit(2)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		es := s.Entries()
		if c, ok := es[len(es)-1].(thread.CompactionEntry); ok && c.Reason == thread.ReasonTrim {
			break // adopted after its Append returned: durable
		}
		if time.Now().After(deadline) {
			fmt.Println("helper: the trim record never landed")
			os.Exit(2)
		}
		time.Sleep(time.Millisecond)
	}
	dieAt("trim")
}

// waitPersisted waits until the resumed run's first step is durable.
func waitPersisted(s *thread.Session) {
	deadline := time.Now().Add(10 * time.Second)
	for !resumeStepPersisted(s.Entries()) {
		if time.Now().After(deadline) {
			fmt.Println("helper: the resumed step never persisted")
			os.Exit(2)
		}
		time.Sleep(time.Millisecond)
	}
}

// resumeStepPersisted reports whether a message entry follows the
// approval decision — the resumed run's first persisted step.
func resumeStepPersisted(entries []thread.Entry) bool {
	decided := false
	for _, e := range entries {
		switch e.(type) {
		case thread.ApprovalDecisionEntry:
			decided = true
		case thread.MessageEntry:
			if decided {
				return true
			}
		}
	}
	return false
}

// mxTurnModel is the turn family's model, shaped by the point it
// serves: at prompt every call blocks on the never-closed channel
// until its context dies — the run never gets past the model, so the
// only durable write is the prompt; at turn_end it answers plainly.
type mxTurnModel struct {
	block chan struct{}
	point string
}

func (m *mxTurnModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxturn"}
}

func (m *mxTurnModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if m.point == "prompt" {
			select {
			case <-m.block:
			case <-ctx.Done():
				yield(nil, ctx.Err())
			}
			return
		}
		for _, ev := range []weft.ModelEvent{
			weft.ModelTextDelta{Text: "turn one done"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// mxParkModel is the park family's model: the first call parks at the
// gated tool; the second is the resume's continuation; the third is
// the compaction's summarizer.
type mxParkModel struct {
	calls       int
	blockResume bool // every call after the first blocks until its context dies
}

func (m *mxParkModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxpark"}
}

func (m *mxParkModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	first := m.calls == 1
	return func(yield func(weft.ModelEvent, error) bool) {
		if !first && m.blockResume {
			<-ctx.Done()
			yield(nil, ctx.Err())
			return
		}
		events := []weft.ModelEvent{
			weft.ModelTextDelta{Text: "mx summary"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		}
		if first {
			events = []weft.ModelEvent{
				weft.ModelToolCall{ID: "call_mx", Name: "spend", Args: []byte(`{}`)},
				weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
			}
		} else if m.calls == 2 {
			events = []weft.ModelEvent{
				weft.ModelTextDelta{Text: "mx spent"},
				weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
			}
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// mxSteerModel is the steer point's model: the first call emits
// reasoning and a tool call; every later call blocks until its context
// dies — the parent kills the child inside the second.
type mxSteerModel struct {
	block chan struct{}
	calls int
}

func (m *mxSteerModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxsteer"}
}

func (m *mxSteerModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	first := m.calls == 1
	return func(yield func(weft.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if !first {
			select {
			case <-m.block:
			case <-ctx.Done():
				yield(nil, ctx.Err())
			}
			return
		}
		events := []weft.ModelEvent{
			weft.ModelReasoningDelta{Text: "one step", Signature: "sig-mx"},
			weft.ModelToolCall{ID: "call_mx", Name: "note", Args: []byte(`{"text":"mx"}`)},
			weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// mxTrimModel is the trim point's model: every answer reports a
// context near the window, the usage the trigger measures.
type mxTrimModel struct{}

func (mxTrimModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxtrim"}
}

func (mxTrimModel) Stream(context.Context, weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		for _, ev := range []weft.ModelEvent{
			weft.ModelTextDelta{Text: "reply"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 90_000, OutputTokens: 5}},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// countingModel is the parent's follow-up model: one plain answer per
// call, recording its requests for the context assertion. It is
// replayModel with a public name — the matrix's asserts read it.
type countingModel struct{ replayModel }

func (m *countingModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxcount"}
}
