package thread_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	_ "modernc.org/sqlite"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// The thread record family (record contract §4, kind thread; U12.1):
// these tests mirror the arena's thread scenarios over a real pipeline
// — a Local sink (replay-grade, content on), a content-on exporter and
// a content-off one — and read what each destination received.

type recCapture struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
	logs  []sdklog.Record
}

func (c *recCapture) ExportSpans(_ context.Context, s []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, s...)
	return nil
}

func (c *recCapture) Export(_ context.Context, recs []sdklog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range recs {
		c.logs = append(c.logs, r.Clone())
	}
	return nil
}

func (c *recCapture) Shutdown(context.Context) error   { return nil }
func (c *recCapture) ForceFlush(context.Context) error { return nil }

// threadRecord is one exported thread record, decoded.
type threadRecord struct {
	kind, run, content string
	severity           log.Severity
	body               map[string]any
	attrs              map[string]string
}

func (r threadRecord) str(k string) string { s, _ := r.body[k].(string); return s }
func (r threadRecord) num(k string) int    { f, _ := r.body[k].(float64); return int(f) }
func (r threadRecord) contentOf(k string) string {
	c, _ := r.body["content"].(map[string]any)
	s, _ := c[k].(string)
	return s
}

func (c *recCapture) records(t *testing.T) []threadRecord {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []threadRecord
	for _, r := range c.logs {
		attrs := map[string]string{}
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
			attrs[string(kv.Key)] = kv.Value.String()
			return true
		})
		if attrs["weft.record"] != "thread" {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(r.Body().AsString()), &body); err != nil {
			t.Fatalf("thread record body is not JSON: %v (%q)", err, r.Body().AsString())
		}
		kind, _ := body["kind"].(string)
		if r.EventName() != "weft.thread."+kind {
			t.Errorf("record of kind %q has event name %q", kind, r.EventName())
		}
		out = append(out, threadRecord{kind: kind, run: attrs["weft.run.id"], content: attrs["weft.content"],
			severity: r.Severity(), body: body, attrs: attrs})
	}
	return out
}

func ofKind(rs []threadRecord, kind string) []threadRecord {
	var out []threadRecord
	for _, r := range rs {
		if r.kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func (c *recCapture) summarizerSpans() []sdktrace.ReadOnlySpan {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []sdktrace.ReadOnlySpan
	for _, s := range c.spans {
		for _, kv := range s.Attributes() {
			if kv.Key == "weft.chat.role" && kv.Value.AsString() == "summarizer" {
				out = append(out, s)
			}
		}
	}
	return out
}

func spanAttr(s sdktrace.ReadOnlySpan, k string) attribute.Value {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == k {
			return kv.Value
		}
	}
	return attribute.Value{}
}

// recPipe is one pipeline: Local at path, a content-on and a
// content-off exporter.
type recPipe struct {
	p       *otel.Pipeline
	on, off *recCapture
	path    string
}

func newRecPipe(t *testing.T) *recPipe {
	t.Helper()
	rp := &recPipe{on: &recCapture{}, off: &recCapture{}, path: filepath.Join(t.TempDir(), "weft.db")}
	p, err := otel.Start(context.Background(), otel.NoGlobal(), otel.NoEnv(), otel.Heartbeat(0),
		otel.Local(rp.path), otel.Exporters(rp.on, rp.on, otel.WithContent()), otel.Exporters(rp.off, rp.off, otel.NoContent()))
	if err != nil {
		t.Fatal(err)
	}
	rp.p = p
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return rp
}

func (rp *recPipe) agent(name string, m core.Model, opts ...core.Option) *core.Agent {
	all := []core.Option{core.Name(name), core.TracerProvider(rp.p.TracerProvider()), core.LoggerProvider(rp.p.LoggerProvider())}
	return core.New(m, append(all, opts...)...)
}

// shutdown flushes everything and returns the thread records the Local
// sink stored, by kind (read straight from its session_events index,
// where every thread record lands — a runless one only there; the
// reader is U12.2's).
func (rp *recPipe) shutdown(t *testing.T) map[string]int {
	t.Helper()
	if err := rp.p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+rp.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT json_extract(body, '$.kind'), COUNT(*) FROM session_events WHERE kind = 'thread' GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

func countKinds(rs []threadRecord) map[string]int {
	out := map[string]int{}
	for _, r := range rs {
		out[r.kind]++
	}
	return out
}

func recWaitIdle(t *testing.T, s *thread.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func recSendWait(t *testing.T, s *thread.Session, text string) *core.RunResult {
	t.Helper()
	turn, err := s.Send(context.Background(), core.User(text))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func recDecide(t *testing.T, s *thread.Session, d thread.Decision) {
	t.Helper()
	if _, err := s.Decide(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	recWaitIdle(t, s)
}

func recWho(d thread.Decision, w string) thread.Decision { d.Who = w; return d }

// thread.support-approvals: a refund through every decision kind —
// deny with a reason, approve, resolve, approve-always minting a grant —
// then a label. Every decision is recorded with who, via and when, under
// the run that parked its call; the grant mint once, under the parking
// run of the call it came from.
func TestThreadRecordsSupportApprovals(t *testing.T) {
	rp := newRecPipe(t)
	refund := core.Tool("issue_refund", "Refund an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "refunded " + in.OrderID, nil
	}, core.RequireApproval())
	email := core.Tool("send_email", "Send an email.", func(_ context.Context, in struct {
		To string `json:"to"`
	}) (string, error) {
		return "sent to " + in.To, nil
	}, core.RequireApproval())
	agt := rp.agent("support", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r1", Args: `{"order_id":"A-1002"}`}),
		wefttest.Say("I need a photo of the damage first."),
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r2", Args: `{"order_id":"A-1002"}`}),
		wefttest.Say("Refund issued."),
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r3", Args: `{"order_id":"A-1003"}`}),
		wefttest.Say("Queued for finance review."),
		wefttest.ToolCalls(wefttest.Call{Name: "send_email", ID: "e1", Args: `{"to":"maria@example.com"}`}),
		wefttest.Say("Email sent."),
	), refund, email)
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.PublicID("pub_support"))
	if err != nil {
		t.Fatal(err)
	}
	parked := map[string]string{} // call id → the run that parked it
	park := func(text, call string) {
		res := recSendWait(t, s, text)
		if len(res.Pending) != 1 || res.Pending[0].ID != call {
			t.Fatalf("%q: pending %v, want %s", text, res.Pending, call)
		}
		parked[call] = res.ID
	}
	park("Refund A-1002 in full, please.", "r1")
	recDecide(t, s, recWho(thread.Deny("r1", "need a photo of the damage"), "agent-7"))
	park("Here is the photo.", "r2")
	recDecide(t, s, recWho(thread.Approve("r2"), "agent-7"))
	park("Refund the coupon difference on A-1003.", "r3")
	recDecide(t, s, recWho(thread.Resolve("r3", `{"status":"queued for finance review"}`), "finance-bot"))
	if err := s.SetInfo(ctx, "Refund desk", nil); err != nil {
		t.Fatal(err)
	}
	park("Email me a confirmation.", "e1")
	recDecide(t, s, recWho(thread.ApproveAlways("e1"), "agent-7"))
	leaf := s.Leaf()
	if err := s.Label(ctx, leaf, "case-closed"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}

	on := rp.on.records(t)
	if got := countKinds(on); got["approval.request"] != 4 || got["approval.decision"] != 4 || got["grant.mint"] != 1 || got["label"] != 1 {
		t.Fatalf("content-on kinds = %v; want 4 requests, 4 decisions, 1 grant.mint, 1 label", got)
	}
	want := map[string]struct{ outcome, who, reason, content string }{
		"r1": {"deny", "agent-7", "need a photo of the damage", ""},
		"r2": {"approve", "agent-7", "", ""},
		"r3": {"resolve", "finance-bot", "", `{"status":"queued for finance review"}`},
		"e1": {"approve", "agent-7", "", ""},
	}
	for _, d := range ofKind(on, "approval.decision") {
		call := d.str("call_id")
		w, ok := want[call]
		if !ok {
			t.Fatalf("decision for unexpected call %q", call)
		}
		if d.str("outcome") != w.outcome || d.str("via") != "user" || d.contentOf("who") != w.who ||
			d.contentOf("reason") != w.reason || d.contentOf("content") != w.content {
			t.Errorf("decision %s = %v, want outcome %s via user who %s", call, d.body, w.outcome, w.who)
		}
		if at, err := time.Parse(time.RFC3339Nano, d.str("at")); err != nil || at.IsZero() {
			t.Errorf("decision %s has no time: %q", call, d.str("at"))
		}
		if d.str("request_entry") == "" || d.str("entry") == "" {
			t.Errorf("decision %s names no entries: %v", call, d.body)
		}
		if d.run != parked[call] || d.content != "full" {
			t.Errorf("decision %s filed under %q (content %s), want the parking run %q", call, d.run, d.content, parked[call])
		}
		if d.attrs["weft.session.id"] != s.ID() || d.attrs["weft.public_id"] != "pub_support" {
			t.Errorf("decision %s carries the session identity %v", call, d.attrs)
		}
		if call == "e1" && d.body["always"] != true {
			t.Errorf("approve-always decision does not say so: %v", d.body)
		}
	}
	g := ofKind(on, "grant.mint")[0]
	if g.str("tool") != "send_email" || g.str("via") != "always" || g.str("call_id") != "e1" || g.run != parked["e1"] || g.str("grant_id") == "" {
		t.Errorf("grant.mint = %v under %q", g.body, g.run)
	}
	for _, r := range ofKind(on, "approval.request") {
		if r.str("args_sha256") == "" || r.run != parked[r.str("call_id")] {
			t.Errorf("request = %v under %q", r.body, r.run)
		}
	}

	// Content off: who and reasons are gone, the record and its ids,
	// enums and names stay — the label's name and the session title.
	off := rp.off.records(t)
	if got := countKinds(off); got["approval.decision"] != 4 || got["label"] != 1 {
		t.Fatalf("content-off kinds = %v", got)
	}
	for _, r := range off {
		if _, ok := r.body["content"]; ok {
			t.Errorf("content-off record %s carries content: %v", r.kind, r.body)
		}
		if r.content != "stripped" && r.content != "none" {
			t.Errorf("content-off record %s has weft.content %q", r.kind, r.content)
		}
	}
	for _, d := range ofKind(off, "approval.decision") {
		if d.str("via") != "user" || d.str("outcome") == "" || d.str("at") == "" {
			t.Errorf("content-off decision lost its ids: %v", d.body)
		}
	}
	l := ofKind(off, "label")[0]
	if l.str("name") != "case-closed" || l.str("target") != leaf || l.attrs["weft.session.title"] != "Refund desk" {
		t.Errorf("content-off label = %v %v; want its name and the session title", l.body, l.attrs)
	}

	stored := rp.shutdown(t)
	if stored["approval.decision"] != 4 || stored["grant.mint"] != 1 || stored["approval.request"] != 4 || stored["label"] != 1 {
		t.Errorf("the Local sink stored %v", stored)
	}
}

// thread.sre-grants: one turn's calls decided before parking — a glob
// grant, the approver, a deny-grant — then a MaxUses grant used up and
// a revoked grant. Every use names its grant and counts; the last use
// and a passed expiry each report the lapse.
func TestThreadRecordsSREGrants(t *testing.T) {
	rp := newRecPipe(t)
	run := core.Tool("run_command", "Run a shell command.", func(_ context.Context, in struct {
		Command string `json:"command"`
	}) (string, error) {
		return "ran " + in.Command, nil
	}, core.RequireApproval())
	agt := rp.agent("sre", wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "run_command", ID: "c1", Args: `{"command":"kubectl get pods"}`},
			wefttest.Call{Name: "run_command", ID: "c2", Args: `{"command":"ls"}`},
			wefttest.Call{Name: "run_command", ID: "c3", Args: `{"command":"rm -rf /srv/old"}`},
		),
		wefttest.Say("Pods listed; the rm was refused by policy."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "x1", Args: `{"command":"systemctl restart checkout"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "x2", Args: `{"command":"systemctl restart checkout"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run_command", ID: "x3", Args: `{"command":"systemctl restart checkout"}`}),
		wefttest.Say("Third restart needs you."),
	), run, core.MaxSteps(10))
	ctx := context.Background()
	approver := func(_ context.Context, r thread.Request) (thread.Decision, bool) {
		if strings.Contains(string(r.Args), `"ls"`) {
			return recWho(thread.Approve(r.CallID), "policy-bot"), true
		}
		return thread.Decision{}, false
	}
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.WithApprover(approver, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []thread.Grant{
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "kubectl get *")}},
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "rm -rf *")}, Deny: true, Reason: "destructive commands are never run from chat"},
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "systemctl restart *")}, MaxUses: 2},
		{Tool: "run_command", Args: []thread.Arg{thread.ArgGlob("/command", "journalctl *")}, Expiry: time.Now().Add(-time.Second)},
	} {
		if err := s.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	// The chain decides at the boundary: the turn's run parks, the
	// chain's verdicts land with it, and the resume runs them.
	first := recSendWait(t, s, "List the pods, list the dir, free space.")
	recWaitIdle(t, s)
	if p := s.Pending(); len(p) != 0 {
		t.Fatalf("the chain left %v pending", p)
	}
	recSendWait(t, s, "Restart checkout three times.")
	recWaitIdle(t, s)
	if p := s.Pending(); len(p) != 1 || p[0].CallID != "x3" {
		t.Fatalf("pending %v, want x3 past MaxUses", p)
	}
	recDecide(t, s, thread.Deny("x3", "enough restarts"))
	var kubectl string
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && len(g.Args) == 1 && g.Args[0].Glob == "kubectl get *" {
			kubectl = g.ID
		}
	}
	if err := s.Revoke(ctx, kubectl); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	on := rp.on.records(t)
	got := countKinds(on)
	if got["grant.mint"] != 4 || got["grant.use"] != 4 || got["grant.revoke"] != 1 {
		t.Fatalf("kinds = %v; want 4 mints, 4 uses (c1, c3, x1, x2), 1 revoke", got)
	}
	// The grants were minted before any run: held, then filed under the
	// first turn's run when it reported.
	for _, g := range ofKind(on, "grant.mint") {
		if g.run != first.ID {
			t.Errorf("grant.mint %v filed under %q, want the first run %q", g.body, g.run, first.ID)
		}
	}
	uses := map[string]threadRecord{}
	for _, u := range ofKind(on, "grant.use") {
		uses[u.str("call_id")] = u
	}
	if u := uses["c1"]; u.str("outcome") != "approved" || u.str("grant_id") != kubectl || u.num("uses") != 1 || u.run != first.ID {
		t.Errorf("c1's use = %v under %q", u.body, u.run)
	}
	if u := uses["c3"]; u.str("outcome") != "denied" {
		t.Errorf("c3's use = %v", u.body)
	}
	if u := uses["x2"]; u.num("uses") != 2 || u.num("max_uses") != 2 || u.run == "" {
		t.Errorf("x2's use = %v under %q", u.body, u.run)
	}
	var reasons []string
	for _, e := range ofKind(on, "grant.expire") {
		reasons = append(reasons, e.str("reason"))
	}
	slices.Sort(reasons)
	if !slices.Equal(reasons, []string{"expiry", "max_uses"}) {
		t.Errorf("grant.expire reasons = %v, want expiry and max_uses", reasons)
	}
	vias := map[string]threadRecord{}
	for _, d := range ofKind(on, "approval.decision") {
		vias[d.str("call_id")] = d
	}
	if d := vias["c1"]; d.str("via") != "grant" || d.str("grant_id") != kubectl {
		t.Errorf("c1's decision = %v", d.body)
	}
	if d := vias["c2"]; d.str("via") != "approver" || d.contentOf("who") != "policy-bot" {
		t.Errorf("c2's decision = %v", d.body)
	}
	if d := vias["c3"]; d.str("outcome") != "deny" || d.contentOf("reason") != "destructive commands are never run from chat" {
		t.Errorf("c3's decision = %v", d.body)
	}
	if r := ofKind(on, "grant.revoke")[0]; r.str("grant_id") != kubectl || r.num("uses") != 1 {
		t.Errorf("grant.revoke = %v", r.body)
	}
	if stored := rp.shutdown(t); stored["grant.use"] != 4 || stored["grant.mint"] != 4 {
		t.Errorf("the Local sink stored %v", stored)
	}
}

// thread.warroom-busy: a steer delivered, a steer dropped by
// ClearQueue, a queued follow-up, an interrupt and a rollback. Each
// receipt and the interrupt are filed under the busy turn; the
// rollback's rewind under the run it took off the path.
func TestThreadRecordsWarroomBusy(t *testing.T) {
	rp := newRecPipe(t)
	entered := make(chan string, 8)
	release := make(chan struct{}, 8)
	metric := core.Tool("check_metric", "Read a metric.", func(ctx context.Context, in struct {
		Name string `json:"name"`
	}) (string, error) {
		entered <- in.Name
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return in.Name + " ok", nil
	})
	analysis := core.Tool("run_analysis", "A slow analysis.", func(ctx context.Context, _ struct {
		Dataset string `json:"dataset"`
	}) (string, error) {
		entered <- "analysis"
		<-ctx.Done()
		return "", ctx.Err()
	})
	agt := rp.agent("warroom", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "check_metric", Args: `{"name":"rps"}`}),
		wefttest.Say("EU only: rps 4120."),
		wefttest.ToolCalls(wefttest.Call{Name: "check_metric", Args: `{"name":"error_rate"}`}),
		wefttest.Say("error rate 2.3%."),
		wefttest.Say("queued follow-up answered."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_analysis", Args: `{"dataset":"checkout-latency"}`}),
		wefttest.Say("p99 is 1880 ms."),
		wefttest.ToolCalls(wefttest.Call{Name: "run_analysis", Args: `{"dataset":"checkout-latency"}`}),
		wefttest.Say("error rate 2.3%."),
	), metric, analysis)
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt)
	if err != nil {
		t.Fatal(err)
	}
	send := func(text string, opts ...thread.SendOption) *thread.Turn {
		t.Helper()
		turn, err := s.Send(ctx, core.User(text), opts...)
		if err != nil {
			t.Fatal(err)
		}
		return turn
	}
	wait := func() {
		t.Helper()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the tool never started")
		}
	}
	// 1. A steer, delivered at the drain.
	t1 := send("Check the current rps.")
	wait()
	send("Focus only on the EU region.", thread.As(thread.Steer))
	release <- struct{}{}
	recWaitIdle(t, s)
	// 2. A steer dropped by ClearQueue, a queued follow-up.
	t2 := send("Check the error rate.")
	wait()
	send("this steer will be dropped", thread.As(thread.Steer))
	if n, err := s.ClearQueue(ctx); err != nil || n != 1 {
		t.Fatalf("ClearQueue = %d, %v; want 1", n, err)
	}
	send("After that, summarise in one line.", thread.As(thread.Queue))
	release <- struct{}{}
	recWaitIdle(t, s)
	// 3. An interrupt.
	t3 := send("Run a deep analysis.")
	wait()
	send("Stop — just tell me the p99.", thread.As(thread.Interrupt))
	recWaitIdle(t, s)
	// 4. A rollback.
	t4 := send("Run another deep analysis.")
	wait()
	send("Forget that. What's the error rate?", thread.As(thread.Rollback))
	recWaitIdle(t, s)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}

	on := rp.on.records(t)
	var states []string
	for _, r := range ofKind(on, "receipt") {
		states = append(states, r.str("input")+":"+r.str("state"))
	}
	for _, want := range []string{"steer:queued", "steer:delivered", "steer:dropped", "send:accepted"} {
		if !slices.Contains(states, want) {
			t.Errorf("receipts %v lack %s", states, want)
		}
	}
	for _, r := range ofKind(on, "receipt") {
		switch {
		case r.str("state") == "delivered" && r.run != t1.RunID():
			t.Errorf("delivered steer filed under %q, want the run that drained it %q", r.run, t1.RunID())
		case r.str("state") == "dropped":
			if r.run != t2.RunID() {
				t.Errorf("dropped steer filed under %q, want the busy turn %q", r.run, t2.RunID())
			}
		case r.str("state") == "queued" && r.contentOf("text") == "this steer will be dropped":
			if r.run != t2.RunID() {
				t.Errorf("queued steer filed under %q", r.run)
			}
		}
	}
	// A steer takes a turn number as it is accepted: its queued receipt
	// names it, so the run ids' gaps (t2, t4) are explained by a record.
	var steerTurns []int
	for _, r := range ofKind(on, "receipt") {
		if r.str("state") == "queued" {
			if r.str("minted_run") != fmt.Sprintf("%s-t%d", s.ID(), r.num("turn")) {
				t.Errorf("queued steer receipt %v names no minted turn", r.body)
			}
			steerTurns = append(steerTurns, r.num("turn"))
		}
	}
	if !slices.Equal(steerTurns, []int{2, 4}) {
		t.Errorf("the steers' turns = %v, want [2 4] (t1 and t3 were running)", steerTurns)
	}
	dropped := false
	for _, r := range ofKind(on, "receipt") {
		dropped = dropped || (r.str("state") == "queued" && r.contentOf("text") == "this steer will be dropped")
	}
	if !dropped {
		t.Error("the dropped steer's text is on no receipt (content on)")
	}
	ints := ofKind(on, "interrupt")
	if len(ints) != 2 {
		t.Fatalf("interrupt records = %d, want 2", len(ints))
	}
	byRun := map[string]threadRecord{}
	for _, r := range ints {
		byRun[r.run] = r
	}
	if r, ok := byRun[t3.RunID()]; !ok || r.str("policy") != "interrupt" || r.str("target") != "run" || r.num("by_turn") == 0 {
		t.Errorf("the interrupt of %s = %v", t3.RunID(), r.body)
	}
	if r, ok := byRun[t4.RunID()]; !ok || r.str("policy") != "rollback" {
		t.Errorf("the rollback's interrupt of %s = %v", t4.RunID(), r.body)
	}
	rw := ofKind(on, "rewind")
	if len(rw) != 1 || rw[0].run != t4.RunID() || rw[0].str("reason") != "rollback" || rw[0].str("to_entry") == "" {
		t.Fatalf("rewind records = %+v, want one under %s", rw, t4.RunID())
	}
	// Content off: the steer texts are gone, the receipts' states stay.
	for _, r := range ofKind(rp.off.records(t), "receipt") {
		if r.contentOf("text") != "" || r.str("state") == "" {
			t.Errorf("content-off receipt = %v", r.body)
		}
	}
	if stored := rp.shutdown(t); stored["interrupt"] != 2 || stored["rewind"] != 1 || stored["receipt"] < 5 {
		t.Errorf("the Local sink stored %v", stored)
	}
}

// thread.research-compaction: a pin, a manual compaction whose summary
// is a model call, an uncompact. The summarizer's call is a chat span
// (weft.chat.role summarizer) with its usage, under the run the
// compaction concerns; the marker body names the same usage.
func TestThreadRecordsResearchCompaction(t *testing.T) {
	rp := newRecPipe(t)
	fetch := core.Tool("fetch_doc", "Fetch a document.", func(_ context.Context, in struct {
		Topic string `json:"topic"`
	}) (string, error) {
		return strings.Repeat(in.Topic+" notes. ", 400), nil
	})
	agt := rp.agent("research", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "fetch_doc", Args: `{"topic":"raft"}`}),
		wefttest.Say("Read raft; elections 150–300 ms."),
		wefttest.ToolCalls(wefttest.Call{Name: "fetch_doc", Args: `{"topic":"crdt"}`}),
		wefttest.Say("Read crdt; they converge."),
		wefttest.Say("SUMMARY: raft elections 150–300 ms; CRDTs converge.").WithUsage(core.Usage{InputTokens: 1800, OutputTokens: 60}),
	), fetch)
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.ContextWindow(64_000), thread.KeepRecent(200), thread.NoAutoCompact())
	if err != nil {
		t.Fatal(err)
	}
	recSendWait(t, s, "Fetch the raft document.")
	last := recSendWait(t, s, "Fetch the crdt document.")
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
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	spans := rp.on.summarizerSpans()
	if len(spans) != 1 {
		t.Fatalf("summarizer spans = %d, want 1", len(spans))
	}
	sp := spans[0]
	if spanAttr(sp, "gen_ai.operation.name").AsString() != "chat" || spanAttr(sp, "gen_ai.usage.input_tokens").AsInt64() != 1800 ||
		spanAttr(sp, "gen_ai.usage.output_tokens").AsInt64() != 60 || spanAttr(sp, "weft.run.id").AsString() != last.ID ||
		!strings.HasPrefix(sp.Name(), "chat") || spanAttr(sp, "weft.session.id").AsString() != s.ID() {
		t.Errorf("summarizer span %q = %v", sp.Name(), sp.Attributes())
	}
	if !sp.Parent().IsValid() {
		t.Errorf("summarizer span has no parent: it is filed under %s's invoke_agent span", last.ID)
	}
	// The marker names the same usage (UX-B-13).
	var marker map[string]any
	rp.on.mu.Lock()
	for _, r := range rp.on.logs {
		if r.EventName() == "weft.compaction" {
			_ = json.Unmarshal([]byte(r.Body().AsString()), &marker)
		}
	}
	rp.on.mu.Unlock()
	u, _ := marker["summarizer_usage"].(map[string]any)
	if u["input_tokens"] != float64(1800) || u["output_tokens"] != float64(60) || marker["summarizer_model"] == nil {
		t.Errorf("marker = %v, want the summarizer's usage and model", marker)
	}
	on := rp.on.records(t)
	if p := ofKind(on, "pin"); len(p) != 1 || p[0].str("target") != firstUser {
		t.Errorf("pin records = %+v", p)
	}
	if uc := ofKind(on, "uncompact"); len(uc) != 1 || uc[0].str("compaction") == "" || uc[0].run != last.ID {
		t.Errorf("uncompact records = %+v", uc)
	}
	// Content off: the span is the same (spans carry no content).
	if n := len(rp.off.summarizerSpans()); n != 1 {
		t.Errorf("content-off summarizer spans = %d", n)
	}
	rp.shutdown(t)
}

// WithRunMetadata: a session's create-time metadata stamps every turn,
// under the identity keys (which win) and a Send's own pairs (which win
// per key); an Open's own pairs join the stored ones.
func TestWithRunMetadataStampsEveryTurn(t *testing.T) {
	rp := newRecPipe(t)
	agt := rp.agent("support", wefttest.Script(wefttest.Say("one"), wefttest.Say("two"), wefttest.Say("three")))
	ctx := context.Background()
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agt, thread.WithRunMetadata(map[string]string{"arena.batch": "b1", "weft.turn": "999"}))
	if err != nil {
		t.Fatal(err)
	}
	r1 := recSendWait(t, s, "hi")
	turn, err := s.Send(ctx, core.User("again"), thread.RunOptions(core.Metadata(map[string]string{"arena.batch": "b2"})))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	o, err := thread.Open(ctx, st, s.ID(), agt, thread.WithRunMetadata(map[string]string{"tenant": "acme"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := o.RunMeta(); got["arena.batch"] != "b1" || got["tenant"] != "acme" {
		t.Fatalf("RunMeta after Open = %v", got)
	}
	r3 := recSendWait(t, o, "third")
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	local := rp.p.LocalDB()
	for _, c := range []struct {
		id, batch, tenant, turn string
	}{{r1.ID, "b1", "", "1"}, {r2.ID, "b2", "", "2"}, {r3.ID, "b1", "acme", "3"}} {
		run, err := local.Run(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Meta["arena.batch"] != c.batch || run.Meta["tenant"] != c.tenant || run.Turn != recAtoi(c.turn) {
			t.Errorf("run %s: meta %v turn %d; want arena.batch %s tenant %q turn %s", c.id, run.Meta, run.Turn, c.batch, c.tenant, c.turn)
		}
	}
	rp.shutdown(t)
}

func recAtoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// The thread records never reach a run row's metadata, and a record
// whose agent says Content(false) carries none even to a content-on
// destination.
func TestThreadRecordsRespectContentOption(t *testing.T) {
	rp := newRecPipe(t)
	refund := core.Tool("issue_refund", "Refund.", func(context.Context, struct{}) (string, error) { return "ok", nil }, core.RequireApproval())
	agt := rp.agent("support", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r1", Args: `{}`}),
		wefttest.Say("denied."),
	), refund, core.Content(false))
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), agt)
	if err != nil {
		t.Fatal(err)
	}
	res := recSendWait(t, s, "refund")
	recDecide(t, s, recWho(thread.Deny("r1", "no"), "agent-7"))
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range rp.on.records(t) {
		if _, ok := r.body["content"]; ok || r.content != "none" {
			t.Errorf("Content(false): record %s = %v (weft.content %s)", r.kind, r.body, r.content)
		}
	}
	run, err := rp.p.LocalDB().Run(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	for k := range run.Meta {
		if strings.HasPrefix(k, "weft.thread") || k == "kind" {
			t.Errorf("run meta holds %q", k)
		}
	}
	rp.shutdown(t)
}
