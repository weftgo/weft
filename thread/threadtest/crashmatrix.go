//go:build unix

// The crash matrix: every write point the session
// layer has, each proved under SIGKILL on a real backend. The write
// points, enumerated from the code (every storage.Append site the
// session layer owns):
//
//	prompt       Send's input entry, durable before the run starts
//	turn_end     the turn-end batch: the turn's messages and its turn entry
//	approval     a run parking: its approval request entries
//	decision     Decide's decision entries over the parked boundary
//	compaction   the compaction entry, summarizer usage and all
//	steer        a steer's acceptance receipt on a busy session
//	pool_receipt thread/pool's acceptance, mirror batch and settlement
//
// and the writes the machinery makes on its own, each with a point of
// its own:
//
//	expiry_sweep  the expiry sweep's audit and denial over a lapsed request
//	auto_trim     the trim record the between-turn trigger writes
//	queued_send   a queued Send's accepted receipt on a busy session
//	resume_join   a resume's completed tool message, replacing the partial one
//	fork_settle   a fork's settling entries for what it copied unsettled
//	pool_parked   a live pool's parked receipt and mirror batch
//	pool_canceled a live pool's canceled settlement
//	pool_capped   a live pool's capped settlement
//
// The child performs the write, proves it is durable (the API returned
// means the backend synced), prints its marker and dies — SIGKILL, no
// cleanup, exactly a writer dropping dead at that write point. The
// prompt and steer points die mid-run instead (the parent kills them
// once the write is provably durable and the child is parked inside
// its model): their write happened on the way into the run, and the
// crash lands in the harder window, between write points. The parent
// then reopens and asserts each point's invariant: the entries the
// point promises, nothing torn the backend does not report, and a
// session that still continues.

package threadtest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
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
	CrashMatrixForkID  = "s_mx_fork"  // fork and fork_settle (the forked session's own id)
	CrashMatrixKidID   = "s_mx_kid"   // the pool points' child session
)

// crashPoints is the full matrix, in walk order. The second block
// holds the five Append sites found by enumerating every
// storage.Append call site in the session layer (branch, fork, the
// steer queue's drop receipts, Resume's arm entry, and DecideSigned's
// decision+grant batch).
var crashPoints = []string{
	"prompt", "turn_end", "approval", "decision", "compaction", "steer", "pool_receipt",
	"branch", "fork", "clear_queue", "resume_arm", "decide_signed",
	// The writes the machinery makes on its own (the 2026-10-01
	// review): the expiry sweep, the automatic trim record, a queued
	// send's accepted receipt, the resume join, a fork's settling
	// entries, and a live pool's parked, canceled and capped receipts.
	"expiry_sweep", "auto_trim", "queued_send", "resume_join", "fork_settle",
	"pool_parked", "pool_canceled", "pool_capped",
}

// parentKilled are the points where the parent lands the kill (the
// child is mid-run, blocked in its model); the rest self-kill right
// after their write returned.
func parentKilled(point string) bool {
	return point == "prompt" || point == "steer" || point == "resume_join"
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
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("the child never reached %q:\n%s", marker, strings.Join(lines, "\n"))
			}
			if parentKilled(point) {
				_ = cmd.Process.Kill() // SIGKILL mid-run: no cleanup, no flush
			}
			_, _ = cmd.Process.Wait()
			mu.Lock()
			got := strings.Join(lines, "\n")
			mu.Unlock()
			if !strings.Contains(got, marker) {
				t.Fatalf("marker %q never printed:\n%s", marker, got)
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
		// Resume's arm entry is durable; the boundary it arms reads
		// decided, and the reopen continues.
		_, entries, _, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil {
			t.Fatal(err)
		}
		if k := kindsOf(entries); !strings.Contains(k, "approval_decision") {
			t.Fatalf("resume_arm kinds = %q", k)
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
	case "expiry_sweep":
		// The sweep's batch — the expiry audit step and the denial it
		// records — is durable whole: the lapsed request reads decided
		// after the reopen, by the expiry and nothing else, and the
		// boundary resumes with that denial.
		_, entries, report, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); !strings.HasSuffix(k, ",approval_audit,approval_decision") {
			t.Fatalf("expiry_sweep kinds = %q", k)
		}
		audit := entries[len(entries)-2].(thread.ApprovalAuditEntry)
		denial := entries[len(entries)-1].(thread.ApprovalDecisionEntry)
		if audit.Step != thread.StepExpiry || denial.Via != "expiry" || denial.Outcome != thread.OutcomeDeny {
			t.Fatalf("the sweep's batch = %+v, %+v", audit, denial)
		}
		s := openMatrixSession(t, st, CrashMatrixParkID)
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("the lapsed request reopened pending: %+v", p)
		}
		closeMatrixSession(t, s)
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "auto_trim":
		// The trim record is the last entry and carries its stub; a
		// reopen with no trimmer configured replays it from the entry.
		_, entries, report, err := st.Load(ctx, CrashMatrixTurnID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		trim, ok := entries[len(entries)-1].(thread.CompactionEntry)
		if !ok || trim.Reason != thread.ReasonTrim || trim.Trim == nil || len(trim.Trim.Stubs) != 1 {
			t.Fatalf("auto_trim: the last entry is %+v (kinds %q), want a trim record with one stub", entries[len(entries)-1], kindsOf(entries))
		}
		s := openMatrixSession(t, st, CrashMatrixTurnID)
		stubbed := false
		for _, m := range s.Context() {
			for _, part := range m.Content {
				if r, ok := part.(weft.ToolResultPart); ok {
					stubbed = r.Content == trim.Trim.Stubs[0].Content
					if !stubbed {
						t.Fatalf("the trimmed result reads %q after the reopen, want the recorded stub %q", r.Content, trim.Trim.Stubs[0].Content)
					}
				}
			}
		}
		if !stubbed {
			t.Fatal("the reopened context holds no tool result to read the stub from")
		}
		closeMatrixSession(t, s)
		continueTurn(t, st, CrashMatrixTurnID, -1)
	case "queued_send":
		// The accepted receipt is durable: the reopened session holds
		// the send in its queue, runs nothing on its own, and Continue
		// runs it — once.
		_, entries, report, err := st.Load(ctx, CrashMatrixTurnID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); k != "message,receipt" {
			t.Fatalf("queued_send kinds = %q", k)
		}
		accepted := entries[1].(thread.ReceiptEntry)
		if accepted.Status != thread.ReceiptAccepted || accepted.Msg == nil || accepted.Msg.Text() != "mx queued" {
			t.Fatalf("the accepted receipt = %+v", accepted)
		}
		m := &countingModel{}
		s, err := thread.Open(ctx, st, CrashMatrixTurnID, weft.New(m, plainSpend(), matrixNote()))
		if err != nil {
			t.Fatal(err)
		}
		if q := s.Queue(); len(q) != 1 || q[0].Receipt != accepted.Turn || q[0].Policy != thread.Queue {
			t.Fatalf("the reopened Queue = %+v, want the accepted send under its id %q", q, accepted.Turn)
		}
		if n := len(m.Requests()); n != 0 {
			t.Fatalf("Open ran %d model calls", n)
		}
		turn, err := s.Continue(ctx)
		if err != nil || turn == nil {
			t.Fatalf("Continue = %v, %v", turn, err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatalf("the restored send's turn: %v", err)
		}
		if turn.ID() != accepted.Turn || turn.RunID() != accepted.RunID {
			t.Fatalf("the restored turn is %q / %q, want the accepted %q / %q", turn.ID(), turn.RunID(), accepted.Turn, accepted.RunID)
		}
		queued := 0
		for _, msg := range s.Context() {
			if msg.Text() == "mx queued" {
				queued++
			}
		}
		if queued != 1 || len(s.Queue()) != 0 {
			t.Fatalf("after Continue the queued message sits %d times in the context and the queue holds %d", queued, len(s.Queue()))
		}
		closeMatrixSession(t, s)
		continueTurn(t, st, CrashMatrixTurnID, -1)
	case "resume_join":
		// The writer died inside the resume, after its join: the
		// completed tool message took the partial one's place on the
		// path, so the boundary reads closed — both calls answered,
		// once each — no turn entry landed for the resume, and the
		// session continues.
		_, entries, report, err := st.Load(ctx, CrashMatrixParkID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		turns := 0
		for _, e := range entries {
			if _, ok := e.(thread.TurnEntry); ok {
				turns++
			}
		}
		if turns != 1 {
			t.Fatalf("resume_join: %d turn entries, want the parked turn's alone (kinds %q)", turns, kindsOf(entries))
		}
		s := openMatrixSession(t, st, CrashMatrixParkID)
		if p := s.Pending(); len(p) != 0 {
			t.Fatalf("the joined boundary reopened pending: %+v", p)
		}
		results := map[string][]string{}
		for _, m := range s.Context() {
			for _, part := range m.Content {
				if r, ok := part.(weft.ToolResultPart); ok {
					results[r.CallID] = append(results[r.CallID], r.Content)
				}
			}
		}
		if got := results["call_note"]; len(got) != 1 || got[0] != "noted: mx" {
			t.Fatalf("the call that ran before the park reads %q, want its one result", got)
		}
		if got := results["call_mx"]; len(got) != 1 || got[0] != "spent" {
			t.Fatalf("the approved call reads %q, want the result the resume's join recorded", got)
		}
		closeMatrixSession(t, s)
		continueTurn(t, st, CrashMatrixParkID, -1)
	case "fork_settle":
		// The fork's file holds the copied path and, behind it, one
		// settling entry per thing it copied unsettled: the origin's
		// delegation canceled, the origin's queued send dropped. The
		// fork restores nothing and continues; the origin keeps its
		// queued send.
		_, entries, report, err := st.Load(ctx, CrashMatrixForkID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); k != "pool_receipt,message,receipt,pool_receipt,receipt" {
			t.Fatalf("fork_settle kinds = %q", k)
		}
		settledPool := entries[3].(thread.PoolReceiptEntry)
		settledSend := entries[4].(thread.ReceiptEntry)
		if settledPool.Status != thread.PoolCanceled || settledPool.Receipt != entries[0].(thread.PoolReceiptEntry).ID {
			t.Fatalf("the fork's delegation settlement = %+v", settledPool)
		}
		if settledSend.Status != thread.ReceiptDropped || settledSend.Receipt != entries[2].(thread.ReceiptEntry).ID {
			t.Fatalf("the fork's queued-send settlement = %+v", settledSend)
		}
		f := openMatrixSession(t, st, CrashMatrixForkID)
		if q := f.Queue(); len(q) != 0 {
			t.Fatalf("the reopened fork's Queue = %+v, want empty", q)
		}
		if rs := pool.Receipts(f); len(rs) != 1 || rs[0].State != pool.Canceled {
			t.Fatalf("the fork's receipts = %+v, want its copy canceled", rs)
		}
		closeMatrixSession(t, f)
		continueTurn(t, st, CrashMatrixForkID, -1)
		origin := openMatrixSession(t, st, "s_mx_forksrc")
		if q := origin.Queue(); len(q) != 1 || q[0].Msg.Text() != "mx queued" {
			t.Fatalf("the origin's Queue = %+v, want its queued send restored", q)
		}
		closeMatrixSession(t, origin)
	case "pool_parked":
		// A pool died with a child parked: the ledger says parked, the
		// mirror is there, and a new pool recovers the delegation,
		// carries the decision down and settles it.
		_, entries, report, err := st.Load(ctx, CrashMatrixPoolID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); k != "pool_receipt,pool_receipt,approval_request,pool_receipt" {
			t.Fatalf("pool_parked kinds = %q", k)
		}
		s := openMatrixSession(t, st, CrashMatrixPoolID)
		rs := pool.Receipts(s)
		if len(rs) != 1 || rs[0].State != pool.Parked || rs[0].Child != CrashMatrixKidID {
			t.Fatalf("the ledger after the crash = %+v", rs)
		}
		p := pool.New(1)
		if err := p.Register(CrashMatrixKidID, weft.New(&countingModel{}, plainSpend())); err != nil {
			t.Fatal(err)
		}
		if err := p.Recover(ctx, s); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		pend := s.Pending()
		if len(pend) != 1 || pend[0].Child != CrashMatrixKidID {
			t.Fatalf("the mirrored request after the crash = %+v", pend)
		}
		if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
			t.Fatalf("Decide: %v", err)
		}
		wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		rc, err := p.Wait(wctx, s, rs[0].ID)
		if err != nil || rc.State != pool.Done || rc.Stop != "recovered" {
			t.Fatalf("the recovered delegation rests at %+v, %v; want done with the child's answer", rc, err)
		}
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if got := len(s.Pending()); got != 0 {
			t.Fatalf("%d requests still pending after the delegation settled", got)
		}
		closeMatrixSession(t, s)
		continueTurn(t, st, CrashMatrixPoolID, -1)
	case "pool_canceled", "pool_capped":
		// The settlement is durable: the ledger reads settled with its
		// cause, a new pool's Recover finds nothing to do, the child's
		// own session loads, and the parent continues.
		want := pool.Canceled
		if point == "pool_capped" {
			want = pool.Capped
		}
		_, entries, report, err := st.Load(ctx, CrashMatrixPoolID)
		if err != nil || report != nil {
			t.Fatalf("Load: err %v, report %+v", err, report)
		}
		if k := kindsOf(entries); k != "pool_receipt,pool_receipt,pool_receipt" {
			t.Fatalf("%s kinds = %q", point, k)
		}
		s := openMatrixSession(t, st, CrashMatrixPoolID)
		rs := pool.Receipts(s)
		if len(rs) != 1 || rs[0].State != want || rs[0].Stop == "" {
			t.Fatalf("the ledger after the crash = %+v, want one receipt settled %s with its cause", rs, want)
		}
		if point == "pool_capped" {
			if u := s.Usage().Delegated; u.InputTokens == 0 {
				t.Fatalf("the capped child's usage is not on the parent's ledger: %+v", s.Usage())
			}
		}
		p := pool.New(1)
		if err := p.Recover(ctx, s); err != nil {
			t.Fatalf("Recover over a settled ledger: %v", err)
		}
		if got := len(s.Entries()); got != len(entries) {
			t.Fatalf("Recover wrote %d entries over a settled ledger", got-len(entries))
		}
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, kidReport, err := st.Load(ctx, CrashMatrixKidID); err != nil || kidReport != nil {
			t.Fatalf("the child session after the crash: report %+v, err %v", kidReport, err)
		}
		closeMatrixSession(t, s)
		continueTurn(t, st, CrashMatrixPoolID, -1)
	default:
		t.Fatalf("unknown point %q", point)
	}
}

// closeMatrixSession closes a session an assertion opened, so the
// follow-up's own Session can be the writer.
func closeMatrixSession(t *testing.T, s *thread.Session) {
	t.Helper()
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
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
	case "expiry_sweep":
		crashMatrixExpiryChild(st)
	case "auto_trim":
		crashMatrixTrimChild(st)
	case "queued_send", "fork_settle":
		crashMatrixQueuedChild(point, st)
	case "resume_join":
		crashMatrixJoinChild(st)
	case "pool_parked", "pool_canceled", "pool_capped":
		crashMatrixLivePoolChild(point, st)
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

// crashMatrixPoolChild covers the pool_receipt point: the three
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
// points over one parked boundary: Resume's arming write (dying before
// its turn is waited on), or the signed decision and its Always grant
// recorded in one batch.
func crashMatrixSignedChild(point string, st thread.Storage) {
	ctx := context.Background()
	key := []byte("mx-key-material")
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: key, Active: true})
	if err != nil {
		fmt.Println("helper: keyring failed:", err)
		os.Exit(2)
	}
	spend := matrixSpend()
	s, err := thread.Create(ctx, st, weft.New(&mxParkModel{}, spend), fixedIDs(CrashMatrixParkID),
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
		if _, err := s.Resume(ctx); err != nil { // the arm entry lands in here
			fmt.Println("helper: resume failed:", err)
			os.Exit(2)
		}
		dieAt(point) // armed, the resumed turn dying with us
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

// helperFail reports a child-side setup failure and exits: the parent
// reads the line when the marker never comes.
func helperFail(what string, args ...any) {
	fmt.Println(append([]any{"helper: " + what + ":"}, args...)...)
	os.Exit(2)
}

// crashMatrixExpiryChild covers the expiry_sweep point: a request
// parked under a one-hour expiry, the clock moved past it, and the
// sweep run by the next path that looks at the boundary — a Decide,
// refused with ErrExpired after the sweep's batch is durable.
func crashMatrixExpiryChild(st thread.Storage) {
	ctx := context.Background()
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	s, err := thread.Create(ctx, st, weft.New(&mxParkModel{}, matrixSpend()), fixedIDs(CrashMatrixParkID),
		thread.AutoResume(false), thread.RequestExpiry(time.Hour), thread.Clock(clock))
	if err != nil {
		helperFail("create failed", err)
	}
	turn, err := s.Send(ctx, weft.User("mx spend"))
	if err != nil {
		helperFail("send failed", err)
	}
	if _, err := turn.Wait(); err != nil {
		helperFail("turn failed", err)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		helperFail("the turn did not park", pend)
	}
	mu.Lock()
	now = now.Add(2 * time.Hour)
	mu.Unlock()
	if _, err := s.Decide(ctx, thread.Approve(pend[0].CallID)); !errors.Is(err, thread.ErrExpired) {
		helperFail("the decision over a lapsed request was not refused as expired", err)
	}
	dieAt("expiry_sweep")
}

// crashMatrixTrimChild covers the auto_trim point: one turn with a
// tool result, a trigger that always fires and the built-in trimmer
// keeping none — the between-turn pass writes a trim record instead
// of a summary, and the child dies once the session is idle.
func crashMatrixTrimChild(st thread.Storage) {
	ctx := context.Background()
	s, err := thread.Create(ctx, st, weft.New(&mxStepModel{}, matrixNote()), fixedIDs(CrashMatrixTurnID),
		thread.ContextWindow(100_000),
		thread.TriggerFunc(func(thread.TriggerInput) bool { return true }),
		thread.ClearOldToolResults(0))
	if err != nil {
		helperFail("create failed", err)
	}
	turn, err := s.Send(ctx, weft.User("mx trim"))
	if err != nil {
		helperFail("send failed", err)
	}
	if _, err := turn.Wait(); err != nil {
		helperFail("turn failed", err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		helperFail("wait idle failed", err)
	}
	entries := s.Entries()
	if c, ok := entries[len(entries)-1].(thread.CompactionEntry); !ok || c.Trim == nil {
		helperFail("the between-turn pass wrote no trim record", kindsOf(entries))
	}
	dieAt("auto_trim")
}

// crashMatrixQueuedChild covers the queued_send and fork_settle
// points: a turn blocked in its model and a second Send queued behind
// it, its accepted receipt durable when Send returns. queued_send dies
// there. fork_settle first records an unsettled delegation, then forks
// at the leaf — the fork's file takes the copied path and its settling
// entries in one append — and dies when Fork returns.
func crashMatrixQueuedChild(point string, st thread.Storage) {
	ctx := context.Background()
	id := CrashMatrixTurnID
	if point == "fork_settle" {
		id = "s_mx_forksrc"
	}
	block := make(chan struct{}) // never closed in the child
	s, err := thread.Create(ctx, st, weft.New(&mxTurnModel{block: block, point: "prompt"}), fixedIDs(id))
	if err != nil {
		helperFail("create failed", err)
	}
	if point == "fork_settle" {
		if _, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
			Status: thread.PoolAccepted, Child: CrashMatrixKidID, Prompt: "mx",
		}); err != nil {
			helperFail("acceptance failed", err)
		}
	}
	if _, err := s.Send(ctx, weft.User("mx one")); err != nil {
		helperFail("send failed", err)
	}
	if _, err := s.Send(ctx, weft.User("mx queued")); err != nil {
		helperFail("queued send failed", err)
	}
	if q := s.Queue(); len(q) != 1 {
		helperFail("the second send was not queued", q)
	}
	if point == "fork_settle" {
		if _, err := s.Fork(ctx, s.Leaf(), fixedIDs(CrashMatrixForkID)); err != nil {
			helperFail("fork failed", err)
		}
	}
	dieAt(point)
}

// crashMatrixJoinChild covers the resume_join point: a step of two
// calls, one that runs and one that parks, the approval, and the
// resume — whose first write is the join, the completed tool message.
// The resume's model call then blocks; the child announces itself from
// inside it, the join durable by then, and the parent kills it.
func crashMatrixJoinChild(st thread.Storage) {
	ctx := context.Background()
	inResume := make(chan struct{})
	m := &mxJoinModel{block: make(chan struct{}), resumed: inResume}
	s, err := thread.Create(ctx, st, weft.New(m, matrixNote(), matrixSpend()), fixedIDs(CrashMatrixParkID))
	if err != nil {
		helperFail("create failed", err)
	}
	turn, err := s.Send(ctx, weft.User("mx mixed"))
	if err != nil {
		helperFail("send failed", err)
	}
	if _, err := turn.Wait(); err != nil {
		helperFail("turn failed", err)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		helperFail("the turn did not park", pend)
	}
	if _, err := s.Decide(ctx, thread.Approve(pend[0].CallID)); err != nil {
		helperFail("decide failed", err)
	}
	<-inResume // the resume's model call: the join landed before it
	fmt.Println("crashmx:resume_join:waiting")
	time.Sleep(time.Hour)
}

// crashMatrixLivePoolChild covers the pool points a real pool writes:
// a submitted child that parks (the mirror batch and the parked
// receipt), one the pool cancels mid-run (the canceled settlement),
// and one that dies on its step budget (the capped settlement). Each
// waits for the delegation to come to rest — its receipt durable —
// and dies with the pool still open.
func crashMatrixLivePoolChild(point string, st thread.Storage) {
	ctx := context.Background()
	s, err := thread.Create(ctx, st, weft.New(&replayModel{}), fixedIDs(CrashMatrixPoolID))
	if err != nil {
		helperFail("create failed", err)
	}
	var first atomic.Bool
	p := pool.New(1, pool.IDs(func() string {
		if first.CompareAndSwap(false, true) {
			return CrashMatrixKidID
		}
		return thread.NewEntryID()
	}))
	var child *weft.Agent
	want := pool.Parked
	switch point {
	case "pool_parked":
		child = weft.New(&mxParkModel{}, matrixSpend())
	case "pool_canceled":
		child, want = weft.New(&mxTurnModel{block: make(chan struct{}), point: "prompt"}), pool.Canceled
	case "pool_capped":
		child, want = weft.New(&mxLoopModel{}, matrixNote(), weft.MaxSteps(1)), pool.Capped
	}
	r, err := p.Submit(ctx, s, child, "mx delegated")
	if err != nil {
		helperFail("submit failed", err)
	}
	if point == "pool_canceled" {
		// Cancel a running child, not a queued one: the receipt must
		// record the start first.
		deadline := time.Now().Add(10 * time.Second)
		for pool.Receipts(s)[0].State != pool.Running {
			if time.Now().After(deadline) {
				helperFail("the child never started", pool.Receipts(s))
			}
			time.Sleep(time.Millisecond)
		}
		if err := p.Cancel(ctx, s, r.ID); err != nil {
			helperFail("cancel failed", err)
		}
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rc, err := p.Wait(wctx, s, r.ID)
	if err != nil || rc.State != want {
		helperFail("the delegation did not rest at "+string(want), rc, err)
	}
	dieAt(point)
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
	calls int
}

func (m *mxParkModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxpark"}
}

func (m *mxParkModel) Stream(_ context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	first := m.calls == 1
	return func(yield func(weft.ModelEvent, error) bool) {
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

// mxStepModel is the auto_trim point's model: one tool call, then the
// answer — a turn that leaves a tool result for the trimmer.
type mxStepModel struct{ calls int }

func (m *mxStepModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxstep"}
}

func (m *mxStepModel) Stream(_ context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	first := m.calls == 1
	return func(yield func(weft.ModelEvent, error) bool) {
		events := []weft.ModelEvent{
			weft.ModelTextDelta{Text: "mx noted"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		}
		if first {
			events = []weft.ModelEvent{
				weft.ModelToolCall{ID: "call_mx", Name: "note", Args: []byte(`{"text":"mx"}`)},
				weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
			}
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// mxLoopModel is the pool_capped point's child model: a tool call on
// every step, so the run dies on its step budget.
type mxLoopModel struct{}

func (m *mxLoopModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxloop"}
}

func (m *mxLoopModel) Stream(_ context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		for _, ev := range []weft.ModelEvent{
			weft.ModelToolCall{ID: "call_mx", Name: "note", Args: []byte(`{"text":"mx"}`)},
			weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// mxJoinModel is the resume_join point's model: the first call issues
// a plain call and a gated one in one step; the second — the resume's
// — signals that it was reached and blocks until its context dies.
type mxJoinModel struct {
	block   chan struct{}
	resumed chan struct{}
	calls   int
}

func (m *mxJoinModel) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: "threadtest", Name: "mxjoin"}
}

func (m *mxJoinModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	call := m.calls
	return func(yield func(weft.ModelEvent, error) bool) {
		if call > 1 {
			if call == 2 {
				close(m.resumed)
			}
			select {
			case <-m.block:
			case <-ctx.Done():
				yield(nil, ctx.Err())
			}
			return
		}
		for _, ev := range []weft.ModelEvent{
			weft.ModelToolCall{ID: "call_note", Name: "note", Args: []byte(`{"text":"mx"}`)},
			weft.ModelToolCall{ID: "call_mx", Name: "spend", Args: []byte(`{}`)},
			weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
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
