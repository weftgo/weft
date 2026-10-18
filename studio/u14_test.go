package studio

import (
	"context"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// TestPoolChildSession is U14's Done line (O11, D3; UX-B-05, UX-E-21,
// UX-B-35), on weft-arena's thread.analysts-pool shape: investment-lead,
// a thread session's turn, delegates through pool.Wrap (call d1) to
// market-analyst. The child session's turn carries weft.caller.* — not
// weft.parent.* — so it is no child run of the lead's turn but its
// caller-linked run; its session exists (the run page's "session …"
// link resolves), is listed by /api/sessions with its lineage, and is
// listed under the lead's session (/api/sessions/{lead}.delegated),
// whose own usage and cost stay its own turn's and whose
// delegated_usage / delegated_sessions_cost_* carry the child's.
func TestPoolChildSession(t *testing.T) {
	_, ts, p := pricedServer(t,
		ModelPrice{Provider: "wefttest", ModelGlob: "lead-m", Input: 1000, Output: 2000},
		ModelPrice{Provider: "wefttest", ModelGlob: "analyst-m", Input: 100, Output: 200})
	ctx := context.Background()
	obs := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	pl := pool.New(1)
	analyst := core.New(namedModel{wefttest.Script(
		wefttest.Say("ACME: P/E 18.2, momentum +12% — strong."),
	), "analyst-m"}, append(obs, core.Name("market-analyst"))...)
	lead := core.New(namedModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "market_analyst", ID: "d1", Args: `{"prompt":"Analyse ACME"}`}),
		wefttest.Say("Recommend ACME."),
	), "lead-m"}, append(obs, core.Name("investment-lead"),
		pl.MustWrap("market_analyst", "Delegate a market analysis.", analyst))...)
	s, err := thread.Create(ctx, thread.Memory(), lead, thread.PublicID("pub_analysts"))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("Get a market analysis of ACME from the market_analyst and give me a one-line recommendation."))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	receipts := pool.Receipts(s)
	if len(receipts) != 1 || receipts[0].Child == "" {
		t.Fatalf("receipts = %+v, want one delegation", receipts)
	}
	childSession := receipts[0].Child
	childRun := childSession + "-t1"
	if err := pl.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// The child's run row: a caller link, no parent.
	var row struct {
		ID           string            `json:"id"`
		ParentRunID  string            `json:"parent_run_id"`
		CallerRunID  string            `json:"caller_run_id"`
		CallerCallID string            `json:"caller_call_id"`
		SessionID    string            `json:"session_id"`
		Agent        string            `json:"agent"`
		Meta         map[string]string `json:"meta"`
		Status       string            `json:"status"`
	}
	getUntil(t, ts, "/api/runs/"+childRun, &row, func() bool { return row.Status == "succeeded" })
	if row.ParentRunID != "" || row.CallerRunID != res.ID || row.CallerCallID != "d1" {
		t.Errorf("child run link: parent %q, caller (%q, %q); want no parent, caller (%s, d1)",
			row.ParentRunID, row.CallerRunID, row.CallerCallID, res.ID)
	}
	if row.SessionID != childSession || row.Agent != "market-analyst" || row.Meta["weft.session.parent"] != s.ID() || row.Meta["weft.session.parent_call"] != "d1" {
		t.Errorf("child run = %+v, want session %s, agent market-analyst, lineage %s / d1", row, childSession, s.ID())
	}
	for _, k := range []string{"weft.caller.run.id", "weft.caller.call.id", "weft.parent.run.id"} {
		if v, ok := row.Meta[k]; ok {
			t.Errorf("child run meta[%s] = %q: a contract key, never metadata", k, v)
		}
	}

	// The lead's run page links it as caller-linked, never as a child.
	var leadRun struct {
		Children     []struct{ ID string } `json:"children"`
		CallerLinked []struct {
			ID           string `json:"id"`
			SessionID    string `json:"session_id"`
			CallerCallID string `json:"caller_call_id"`
		} `json:"caller_linked"`
	}
	getUntil(t, ts, "/api/runs/"+res.ID, &leadRun, func() bool { return len(leadRun.CallerLinked) == 1 })
	if len(leadRun.Children) != 0 || leadRun.CallerLinked[0].ID != childRun ||
		leadRun.CallerLinked[0].SessionID != childSession || leadRun.CallerLinked[0].CallerCallID != "d1" {
		t.Errorf("lead run: children %+v, caller_linked %+v; want no child and %s (session %s, call d1)",
			leadRun.Children, leadRun.CallerLinked, childRun, childSession)
	}

	// The child run page's "session …" link resolves: the child session.
	type sess struct {
		ID            string     `json:"id"`
		Agent         string     `json:"agent"`
		Turns         int        `json:"turns"`
		Usage         core.Usage `json:"usage"`
		ParentSession string     `json:"parent_session"`
		ParentTurn    int        `json:"parent_turn"`
		ParentCall    string     `json:"parent_call"`
		ParentRun     string     `json:"parent_run"`
		CostUSD       *float64   `json:"cost_usd"`
	}
	var child struct {
		sess
		Runs []struct{ ID string } `json:"runs"`
	}
	getUntil(t, ts, "/api/sessions/"+childSession, &child, func() bool { return len(child.Runs) == 1 })
	if child.ID != childSession || child.Agent != "market-analyst" || child.Turns != 1 || child.Runs[0].ID != childRun ||
		child.ParentSession != s.ID() || child.ParentTurn != 1 || child.ParentCall != "d1" || child.ParentRun != res.ID {
		t.Errorf("child session = %+v, want market-analyst, one turn %s, lineage %s t1 call d1 run %s", child, childRun, s.ID(), res.ID)
	}

	// /api/sessions lists both sessions, the child's with its lineage.
	var list struct {
		Total    int    `json:"total"`
		Sessions []sess `json:"sessions"`
	}
	getUntil(t, ts, "/api/sessions", &list, func() bool { return list.Total == 2 })
	var listed *sess
	for i := range list.Sessions {
		if list.Sessions[i].ID == childSession {
			listed = &list.Sessions[i]
		}
	}
	if listed == nil || listed.ParentSession != s.ID() || listed.ParentTurn != 1 || listed.ParentCall != "d1" {
		t.Errorf("/api/sessions = %+v, want the child session %s listed with lineage %s t1 call d1", list.Sessions, childSession, s.ID())
	}

	// The lead's session: its own turn alone, the child under delegated.
	var parent struct {
		sess
		Runs           []struct{ ID string } `json:"runs"`
		Delegated      []sess                `json:"delegated"`
		DelegatedUsage core.Usage            `json:"delegated_usage"`
		DelegatedCost  *float64              `json:"delegated_sessions_cost_usd"`
		DelegatedKnown bool                  `json:"delegated_sessions_cost_known"`
	}
	getUntil(t, ts, "/api/sessions/"+s.ID(), &parent, func() bool { return len(parent.Delegated) == 1 })
	if len(parent.Runs) != 1 || parent.Runs[0].ID != res.ID || parent.Usage.InputTokens != 20 || usd(parent.CostUSD) != "0.04" {
		t.Errorf("lead session: runs %+v, usage %+v, cost %s; want its own turn %s alone, 20 in, $0.04", parent.Runs, parent.Usage, usd(parent.CostUSD), res.ID)
	}
	d := parent.Delegated[0]
	if d.ID != childSession || d.Agent != "market-analyst" || d.ParentTurn != 1 || d.ParentCall != "d1" || usd(d.CostUSD) != "0.002" {
		t.Errorf("lead session delegated = %+v, want %s (market-analyst, t1, d1, $0.002)", d, childSession)
	}
	if parent.DelegatedUsage.InputTokens != 10 || parent.DelegatedUsage.OutputTokens != 5 || !parent.DelegatedKnown || usd(parent.DelegatedCost) != "0.002" {
		t.Errorf("lead session delegated figures: usage %+v, cost %s known %v; want 10/5 and $0.002", parent.DelegatedUsage, usd(parent.DelegatedCost), parent.DelegatedKnown)
	}
}
