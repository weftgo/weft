package studio_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
)

// TestDecidedUndecidedCallFromARealResume: a thread session parks two
// calls, one is denied, and Session.Resume runs the boundary with the
// other undecided. The run document's decided block reads the deny as
// denied and the undecided call as no_decision (AGENTS block 7, the
// record contract) — the resume's weft.resume.calls and the call's
// execute_tool span say no_decision, while the model still reads
// "DENIED: no decision" (rule 12). Before 0.18.1 thread synthesized a
// plain Deny for the undecided call, and every read said denied.
func TestDecidedUndecidedCallFromARealResume(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Local(filepath.Join(dir, "weft.db")), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	srv := studio.New(studio.DB(p.LocalDB()))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	type in struct {
		OrderID string `json:"order_id"`
	}
	refund := core.Tool("issue_refund", "Refund an order.", func(_ context.Context, a in) (string, error) {
		return "refunded " + a.OrderID, nil
	}, core.RequireApproval())
	email := core.Tool("send_email", "Email the customer.", func(_ context.Context, a in) (string, error) {
		return "emailed about " + a.OrderID, nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "issue_refund", ID: "r1", Args: `{"order_id":"A-1"}`},
			wefttest.Call{Name: "send_email", ID: "e1", Args: `{"order_id":"A-1"}`},
		),
		wefttest.Say("Nothing was done."),
	), core.Name("undecided-desk"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()),
		refund, email)
	s, err := thread.Create(ctx, thread.Memory(), agt)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("Refund A-1 and email me."))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 2 {
		t.Fatalf("parked turn: %v, %v", res, err)
	}
	d := thread.Deny("r1", "out of policy")
	d.Who = "agent-7"
	if next, err := s.Decide(ctx, d); err != nil || next != nil {
		t.Fatalf("Decide one of two: turn %v, err %v (want no resume yet)", next, err)
	}
	t2, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := t2.Wait()
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, m := range res.Messages {
		for _, part := range m.Content {
			if tr, ok := part.(core.ToolResultPart); ok {
				results[tr.CallID] = tr.Content
			}
		}
	}
	if results["r1"] != "DENIED: out of policy" || results["e1"] != "DENIED: no decision" {
		t.Errorf("model-visible results = %v, want the deny and rule 12's text", results)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/api/runs/" + t1.RunID())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET run = %d %s", resp.StatusCode, b)
	}
	var doc struct {
		Decided struct {
			Source string `json:"source"`
			Calls  []struct {
				CallID  string `json:"call_id"`
				Outcome string `json:"outcome"`
				Via     string `json:"via"`
				Who     string `json:"who"`
			} `json:"calls"`
		} `json:"decided"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range doc.Decided.Calls {
		got[c.CallID] = c.Outcome
		if c.CallID == "e1" && (c.Via != "" || c.Who != "") {
			t.Errorf("the undecided call names a decider: %+v", c)
		}
	}
	if got["r1"] != "denied" || got["e1"] != "no_decision" {
		t.Errorf("decided outcomes = %v (source %s), want r1 denied, e1 no_decision", got, doc.Decided.Source)
	}
}
