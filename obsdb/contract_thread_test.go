package obsdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// The thread record family's conformance cases (record contract §4,
// kind thread; U12.1): one generated case per group of kinds, each a
// real thread session under the pipeline. A case's expect.json lists
// its thread records (thread_records): "<run id> <kind> <weft.content>
// <body>" in arrival order — the body as emitted, its "at" removed (the
// session clock is pinned, but the line says what the record is, not
// when). Sessions mint their ids through thread.IDs and read time from
// a pinned thread.Clock, so a regeneration is byte-identical.

// fixtureSession pins a case's session ids and clock: the session is
// "s_<name>", its entries "e_<name>_<n>"; a Fork through the same
// option gets "s_<name>_fork".
func fixtureSession(name string) []thread.SessionOption {
	var n atomic.Int64
	ids := thread.IDs(func() string {
		switch k := n.Add(1); k {
		case 1:
			return "s_" + name
		default:
			return fmt.Sprintf("e_%s_%04d", name, k)
		}
	})
	return []thread.SessionOption{ids, thread.Clock(func() time.Time { return fixtureEpoch })}
}

func idleOrFail(t *testing.T, s *thread.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func sendTurn(t *testing.T, s *thread.Session, text string, opts ...thread.SendOption) *thread.Turn {
	t.Helper()
	turn, err := s.Send(context.Background(), core.User(text), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func waitTurn(t *testing.T, turn *thread.Turn) *core.RunResult {
	t.Helper()
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// sessionRunIDs is every run id the session's turn entries name, the
// way the store reads them back.
func sessionRunIDs(s *thread.Session) []string {
	var ids []string
	for _, e := range s.Entries() {
		if te, ok := e.(thread.TurnEntry); ok {
			ids = append(ids, te.RunID)
		}
	}
	return ids
}

func approvalsCase(name string) func(*testing.T, context.Context, *otel.Pipeline, *otlpCapture) []string {
	return func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
		refund := core.Tool("issue_refund", "Refund an order.", func(_ context.Context, in lookupIn) (string, error) {
			return "refunded " + in.OrderID, nil
		}, core.RequireApproval())
		email := core.Tool("send_email", "Email the customer.", func(_ context.Context, in lookupIn) (string, error) {
			return "emailed about " + in.OrderID, nil
		}, core.RequireApproval())
		agt := pipelineAgent(p, "support", wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r1", Args: `{"order_id":"A-1002"}`}),
			wefttest.Say("I need a photo of the damage first."),
			wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r2", Args: `{"order_id":"A-1002"}`}),
			wefttest.Say("Refund issued."),
			wefttest.ToolCalls(wefttest.Call{Name: "send_email", ID: "e1", Args: `{"order_id":"A-1002"}`}),
			wefttest.Say("Email sent."),
		), core.Instructions("You are a support agent."), refund, email)
		alice := thread.Key{ID: "k_alice", Secret: []byte("alice-secret"), Active: true}
		ring, err := thread.NewKeyring(alice)
		if err != nil {
			t.Fatal(err)
		}
		opts := append(fixtureSession(name), thread.PublicID("pub_"+name), thread.WithKeyring(ring),
			thread.WithRunMetadata(map[string]string{"arena.batch": "b_contract"}))
		s, err := thread.Create(ctx, thread.Memory(), agt, opts...)
		if err != nil {
			t.Fatal(err)
		}
		decide := func(d thread.Decision, who string) {
			d.Who = who
			if _, err := s.Decide(ctx, d); err != nil {
				t.Fatal(err)
			}
			idleOrFail(t, s)
		}
		waitTurn(t, sendTurn(t, s, "Refund A-1002 in full, please."))
		decide(thread.Deny("r1", "need a photo of the damage"), "agent-7")
		waitTurn(t, sendTurn(t, s, "Here is the photo."))
		// A signature under a key the ring does not hold is refused and
		// audited; alice's own is recorded, via signed.
		req, err := s.Request("r2")
		if err != nil {
			t.Fatal(err)
		}
		forged, err := thread.Key{ID: "k_mallory", Secret: []byte("guess")}.Sign(req, thread.Approve("r2"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.DecideSigned(ctx, forged); !errors.Is(err, thread.ErrBadSignature) {
			t.Fatalf("the forged signature: %v, want ErrBadSignature", err)
		}
		d := thread.Approve("r2")
		d.Who = "alice"
		signed, err := alice.Sign(req, d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.DecideSigned(ctx, signed); err != nil {
			t.Fatal(err)
		}
		idleOrFail(t, s)
		if err := s.SetInfo(ctx, "Refund desk", nil); err != nil {
			t.Fatal(err)
		}
		waitTurn(t, sendTurn(t, s, "Email me a confirmation."))
		decide(thread.ApproveAlways("e1"), "agent-7")
		if err := s.Label(ctx, s.Leaf(), "case-closed"); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		return sessionRunIDs(s)
	}
}

func runThreadGrants(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
	run := core.Tool("run_command", "Run a shell command.", func(_ context.Context, in struct {
		Command string `json:"command" jsonschema:"the command"`
	}) (string, error) {
		return "ran " + in.Command, nil
	}, core.RequireApproval())
	agt := pipelineAgent(p, "sre", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "c1", Args: `{"command":"kubectl get pods"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "c2", Args: `{"command":"ls"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "c3", Args: `{"command":"rm -rf /srv/old"}`}),
		wefttest.Say("Pods and the directory listed; the rm was refused by policy."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "x1", Args: `{"command":"systemctl restart checkout"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "x2", Args: `{"command":"systemctl restart checkout"}`}),
		wefttest.Say("Restarted twice."),
	), core.Instructions("You are an SRE shell."), run)
	approver := func(_ context.Context, r thread.Request) (thread.Decision, bool) {
		if strings.Contains(string(r.Args), `"ls"`) {
			d := thread.Approve(r.CallID)
			d.Who = "policy-bot"
			return d, true
		}
		return thread.Decision{}, false
	}
	opts := append(fixtureSession("thread_grants"), thread.WithApprover(approver, 5*time.Second))
	s, err := thread.Create(ctx, thread.Memory(), agt, opts...)
	if err != nil {
		t.Fatal(err)
	}
	var kubectl string
	for _, g := range []thread.Grant{
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "kubectl get *")}},
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "rm -rf *")}, Deny: true, Reason: "destructive commands are never run from chat"},
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "systemctl restart *")}, MaxUses: 2},
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "journalctl *")}, Expiry: fixtureEpoch.Add(-time.Minute)},
	} {
		if err := s.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
		if kubectl == "" {
			kubectl = s.Leaf()
		}
	}
	waitTurn(t, sendTurn(t, s, "List the pods, list the dir, free some space."))
	idleOrFail(t, s)
	waitTurn(t, sendTurn(t, s, "Restart checkout twice."))
	idleOrFail(t, s)
	if err := s.Revoke(ctx, kubectl); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return sessionRunIDs(s)
}

func runThreadBusy(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
	entered := make(chan struct{}, 8)
	release := make(chan struct{}, 8)
	metric := core.Tool("check_metric", "Read a metric.", func(ctx context.Context, in struct {
		Name string `json:"name" jsonschema:"the metric"`
	}) (string, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return in.Name + " ok", nil
	})
	analysis := core.Tool("run_analysis", "A slow analysis.", func(ctx context.Context, _ struct {
		Dataset string `json:"dataset" jsonschema:"the dataset"`
	}) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	agt := pipelineAgent(p, "warroom", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "check_metric", Args: `{"name":"rps"}`}),
		wefttest.Say("EU only: rps 4120."),
		wefttest.ToolCalls(wefttest.Call{Name: "check_metric", Args: `{"name":"error_rate"}`}),
		wefttest.Say("error rate 2.3%."),
		wefttest.Say("queued follow-up answered."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_analysis", Args: `{"dataset":"checkout-latency"}`}),
		wefttest.Say("p99 is 1880 ms."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_analysis", Args: `{"dataset":"checkout-latency"}`}),
		wefttest.Say("error rate 2.3%."),
	), core.Instructions("You are the incident analyst."), metric, analysis)
	s, err := thread.Create(ctx, thread.Memory(), agt, fixtureSession("thread_busy")...)
	if err != nil {
		t.Fatal(err)
	}
	wait := func() {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the tool never started")
		}
	}
	sendTurn(t, s, "Check the current rps.")
	wait()
	sendTurn(t, s, "Focus only on the EU region.", thread.As(thread.Steer))
	release <- struct{}{}
	idleOrFail(t, s)
	sendTurn(t, s, "Check the error rate.")
	wait()
	sendTurn(t, s, "this steer will be dropped", thread.As(thread.Steer))
	if n, err := s.ClearQueue(ctx); err != nil || n != 1 {
		t.Fatalf("ClearQueue = %d, %v", n, err)
	}
	sendTurn(t, s, "After that, summarise in one line.", thread.As(thread.Queue))
	release <- struct{}{}
	idleOrFail(t, s)
	sendTurn(t, s, "Run a deep analysis.")
	wait()
	sendTurn(t, s, "Stop — just tell me the p99.", thread.As(thread.Interrupt))
	idleOrFail(t, s)
	sendTurn(t, s, "Run another deep analysis.")
	wait()
	sendTurn(t, s, "Forget that. What's the error rate?", thread.As(thread.Rollback))
	idleOrFail(t, s)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return sessionRunIDs(s)
}

func runThreadCompaction(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
	fetch := core.Tool("fetch_doc", "Fetch a document.", func(_ context.Context, in struct {
		Topic string `json:"topic" jsonschema:"the topic"`
	}) (string, error) {
		return strings.Repeat(in.Topic+" notes. ", 400), nil
	})
	agt := pipelineAgent(p, "research", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "fetch_doc", Args: `{"topic":"raft"}`}),
		wefttest.Say("Read raft; elections 150–300 ms."),
		wefttest.ToolCalls(wefttest.Call{Name: "fetch_doc", Args: `{"topic":"crdt"}`}),
		wefttest.Say("Read crdt; they converge."),
		wefttest.Say("SUMMARY: raft elections 150–300 ms; CRDTs converge.").WithUsage(core.Usage{InputTokens: 1800, OutputTokens: 60}),
		wefttest.Say("SUMMARY of the abandoned branch: crdt notes.").WithUsage(core.Usage{InputTokens: 900, OutputTokens: 30}),
	), core.Instructions("You are a research assistant."), fetch)
	opts := append(fixtureSession("thread_compaction"), thread.ContextWindow(64_000), thread.KeepRecent(200), thread.NoAutoCompact())
	s, err := thread.Create(ctx, thread.Memory(), agt, opts...)
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, sendTurn(t, s, "Fetch the raft document."))
	afterRaft := s.Leaf()
	waitTurn(t, sendTurn(t, s, "Fetch the crdt document."))
	var firstUser string
	for _, e := range s.Entries() {
		if m, ok := e.(thread.MessageEntry); ok && m.Message.Role == core.RoleUser {
			firstUser = m.ID
			break
		}
	}
	if err := s.Pin(ctx, firstUser); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx, thread.SummaryInstructions("keep every number")); err != nil {
		t.Fatal(err)
	}
	if err := s.Uncompact(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Branch(ctx, afterRaft, thread.SummarizeLeft()); err != nil {
		t.Fatal(err)
	}
	if err := s.Label(ctx, afterRaft, "after-raft"); err != nil {
		t.Fatal(err)
	}
	f, err := s.Fork(ctx, afterRaft, thread.IDs(func() string { return "s_thread_compaction_fork" }))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return sessionRunIDs(s)
}

func runThreadPool(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
	child := pipelineAgent(p, "researcher", wefttest.Script(wefttest.Say("The answer is 42.")),
		core.Instructions("You research."))
	pl := pool.New(1)
	parent := pipelineAgent(p, "lead", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"find the answer"}`}),
		wefttest.Say("It is 42."),
	), core.Instructions("You lead."), pl.MustWrap("research", "Delegate research to a child session.", child))
	s, err := thread.Create(ctx, thread.Memory(), parent, fixtureSession("thread_pool")...)
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, sendTurn(t, s, "What is the answer?"))
	if err := pl.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	ids := sessionRunIDs(s)
	for _, r := range pool.Receipts(s) {
		ids = append(ids, r.Child+"-t1")
	}
	return ids
}

// threadGenCases are the thread record family's generated cases,
// appended after genCases' own (their fixture minutes follow).
func threadGenCases() []genCase {
	content := []otel.DestOption{otel.WithContent()}
	return []genCase{
		{
			name: "thread_approvals",
			desc: "a thread session (thread.WithRunMetadata arena.batch, a public id) through every decision path, content on: a deny with a reason, a signature under a key the ring does not hold (approval.refused, reason bad_signature), alice's signed approval (via signed, key_id), an approve-always minting a grant (grant.mint via always under the parking run), a label; each approval.request and approval.decision filed under the run that parked its call, who and the reason under content; the session's run metadata in every run row's meta",
			dest: content,
			run:  approvalsCase("thread_approvals"),
		},
		{
			name: "thread_grants",
			desc: "the decision chain at the boundary: four grants minted before any run (held, then filed under the first run), a glob grant's use, the approver's decision (via approver, who policy-bot), a deny-grant's refusal, a MaxUses grant's two uses and its lapse (grant.expire max_uses), a past-expiry grant's lapse noticed by the chain (grant.expire expiry), a revocation (grant.revoke with the grant's use count)",
			dest: content,
			run:  runThreadGrants,
		},
		{
			name: "thread_busy",
			desc: "the busy policies: a steer accepted (receipt queued) and drained (delivered, under the run that drained it), a steer dropped by ClearQueue (dropped, under the busy turn), a queued send (accepted, its minted turn), an interrupt (interrupt under the felled run, by_turn the follow-up) and a rollback (interrupt, then rewind reason rollback under the run taken off the path); the steer texts under content",
			dest: content,
			run:  runThreadBusy,
		},
		{
			name: "thread_compaction",
			desc: "a pin, a manual compaction whose summary is a model call — a chat span with weft.chat.role summarizer and its usage (1800 in, 60 out) under the run the compaction concerns, the marker body's summarizer_usage and summarizer_model — an uncompact, a SummarizeLeft branch (branch summarized, its summarizer usage, a second summarizer span), a label and a fork (fork, new_session) of the origin",
			dest: content,
			run:  runThreadCompaction,
		},
		{
			name: "thread_pool",
			desc: "a sync thread/pool delegation from a session's turn: pool.delegate (child session, under the delegating turn's run) and pool.retire (state done, the child's usage); the child session's own run",
			dest: content,
			run:  runThreadPool,
		},
		{
			name: "thread_content_off",
			desc: "the thread_approvals session through a content-off destination: every thread record's content object stripped (weft.content stripped where it carried one, none where it had none) — who, reasons, the grant's reason — while the ids, enums, the label's name (an app-chosen name is metadata) and the session title (weft.session.title on the label record and the later turns) stay",
			dest: nil,
			run:  approvalsCase("thread_content_off"),
		},
	}
}

// threadRecordsOf reads a case's thread records from its logs.json:
// "<run id> <kind> <weft.content> <body without at>", in arrival order.
func threadRecordsOf(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "logs.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var req collogspb.ExportLogsServiceRequest
	if err := protojson.Unmarshal(b, &req); err != nil {
		t.Fatalf("%s/logs.json: %v", dir, err)
	}
	var out []string
	type line struct {
		time uint64
		s    string
	}
	var lines []line
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, r := range sl.GetLogRecords() {
				if kvString(r.Attributes, "weft.record") != "thread" {
					continue
				}
				var body map[string]any
				if err := json.Unmarshal([]byte(r.GetBody().GetStringValue()), &body); err != nil {
					t.Fatalf("%s: a thread record's body is not JSON: %v", dir, err)
				}
				if want := "weft.thread." + fmt.Sprint(body["kind"]); r.GetEventName() != want {
					t.Errorf("%s: a thread record of kind %v has event name %q", dir, body["kind"], r.GetEventName())
				}
				delete(body, "at")
				kind := body["kind"]
				delete(body, "kind")
				bb, _ := json.Marshal(body)
				lines = append(lines, line{r.GetTimeUnixNano(), fmt.Sprintf("%s %v %s %s",
					kvString(r.Attributes, "weft.run.id"), kind, kvString(r.Attributes, "weft.content"), bb)})
			}
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].time < lines[j].time })
	for _, l := range lines {
		out = append(out, l.s)
	}
	return out
}

// TestThreadRecordKindsHaveFixtures: every kind of the family appears in
// some case's thread_records, and every kind there is one the record
// contract's §4 table names.
func TestThreadRecordKindsHaveFixtures(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "record-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"approval.request", "approval.decision", "approval.refused", "grant.mint", "grant.use",
		"grant.revoke", "grant.expire", "receipt", "interrupt", "rewind", "branch", "label", "fork",
		"pool.delegate", "pool.retire", "uncompact", "pin"}
	seen := map[string]bool{}
	dirs, err := os.ReadDir(contractDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		for _, l := range threadRecordsOf(t, filepath.Join(contractDir, d.Name())) {
			f := strings.Fields(l)
			seen[f[1]] = true
		}
	}
	for _, k := range kinds {
		if !seen[k] {
			t.Errorf("no contract fixture holds a thread record of kind %s", k)
		}
		if !strings.Contains(string(doc), "`weft.thread."+k+"`") {
			t.Errorf("docs/record-contract.md does not name `weft.thread.%s`", k)
		}
	}
	for k := range seen {
		if !strings.Contains(strings.Join(kinds, " "), k) {
			t.Errorf("a fixture holds thread record kind %q this test does not know", k)
		}
	}
}
