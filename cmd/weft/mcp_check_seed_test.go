//go:build livecheck

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// The investigation shapes `make mcp-check` asks explain_failure and
// session about (I5): built here, in-repo, from the weft-arena
// scenarios they are named after, run on wefttest scripted models (no
// provider, WEFT_MODEL_REQUESTS=deny) through the real otel pipeline
// into the scratch Studio's OTLP ingest.
const (
	chkSubFailed = "c5b0000000000000000000000000f001" // sub.failed: a child's failure is data the parent reads
	chkChaos     = "c5b0000000000000000000000000c405" // agentic.chaos: six children, four failure shapes
	chkOpsSess   = "s_chk_ops"                        // a thread turn parked on deploy, denied by alice
	chkChatSess  = "s_chk_chat"                       // a nine-turn session
)

// hangModel never answers: it waits for its context (the Subagent
// Timeout reaps it).
type hangModel struct{}

func (hangModel) Stream(ctx context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}

// seedInvestigations runs the four shapes into the Studio at base and
// waits until each run reads settled there.
func seedInvestigations(t *testing.T, ctx context.Context, base, token string) {
	t.Helper()
	p, err := otel.Start(ctx, otel.Studio(base, token), otel.NoGlobal(), otel.NoEnv())
	if err != nil {
		t.Fatal(err)
	}
	prov := func(opts ...core.Option) []core.Option {
		return append(opts, core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	}

	// sub.failed: the shipping-quote child hits a carrier outage; the
	// parent reads SUBAGENT_FAILED and offers a fallback — it succeeds.
	quoter := core.New(wefttest.Script(wefttest.Fail(errors.New("carrier API outage: DHL rates unavailable"))),
		prov(core.Name("shipping-quoter"))...)
	checkout := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "q1", Name: "shipping_quote", Args: `{"prompt":"Quote 2 kg Lisbon to Berlin"}`}),
		wefttest.Say("Express quotes are down; standard shipping is €7.90, 4–6 days."),
	), prov(core.Name("checkout-assistant"), core.Subagent("shipping_quote", "Quote shipping.", quoter))...)
	if _, err := checkout.Generate(ctx, core.RunID(chkSubFailed), core.Prompt("How much to ship my order to Berlin?")); err != nil {
		t.Fatalf("sub.failed: %v", err)
	}

	// agentic.chaos: six reviewers in one step — two clean, the IP
	// reviewer's provider down (SUBAGENT_FAILED), the tax reviewer
	// timed out, the NDA signer parked (SUBAGENT_PENDING), the
	// litigation reviewer's OCR tool panicking inside a child that
	// recovers.
	type nda struct {
		Counterparty string `json:"counterparty"`
	}
	type doc struct {
		Doc string `json:"doc"`
	}
	ok := func(name, verdict string) *core.Agent {
		return core.New(wefttest.Script(wefttest.Say(verdict)), prov(core.Name(name))...)
	}
	signer := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "n1", Name: "sign_nda", Args: `{"counterparty":"Northwind GmbH"}`})),
		prov(core.Name("nda-signer"), core.Tool("sign_nda", "Sign an NDA.", func(context.Context, nda) (string, error) { return "signed", nil },
			core.RequireApproval()))...)
	litigation := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "o1", Name: "ocr_exhibit", Args: `{"doc":"exhibit-12.pdf"}`}),
		wefttest.Say("OCR failed on exhibit 12; reviewed the text layer: no pending litigation"),
	), prov(core.Name("litigation-reviewer"), core.Tool("ocr_exhibit", "OCR a scanned exhibit.", func(context.Context, doc) (string, error) {
		panic("ocr engine: nil page")
	}))...)
	lead := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "f1", Name: "review_financials", Args: `{"prompt":"Review the financial statements folder"}`},
			wefttest.Call{ID: "h1", Name: "review_hr", Args: `{"prompt":"Review the HR folder"}`},
			wefttest.Call{ID: "i1", Name: "review_ip", Args: `{"prompt":"Review the patents folder"}`},
			wefttest.Call{ID: "t1", Name: "review_tax", Args: `{"prompt":"Review the tax filings"}`},
			wefttest.Call{ID: "s1", Name: "sign_nda", Args: `{"prompt":"Countersign the Northwind NDA"}`},
			wefttest.Call{ID: "l1", Name: "review_litigation", Args: `{"prompt":"Review the litigation exhibits"}`},
		),
		wefttest.Say("Data room: 2 folders clean, IP and tax pending re-review, NDA awaiting sign-off, litigation clear."),
	), prov(core.Name("deal-lead"),
		core.Subagent("review_financials", "Review financials.", ok("financials-reviewer", "financials clean")),
		core.Subagent("review_hr", "Review HR.", ok("hr-reviewer", "HR clean")),
		core.Subagent("review_ip", "Review IP.", core.New(wefttest.Script(wefttest.Fail(errors.New("provider maintenance window"))), prov(core.Name("ip-reviewer"))...)),
		core.Subagent("review_tax", "Review tax.", core.New(hangModel{}, prov(core.Name("tax-reviewer"))...), core.Timeout(150*time.Millisecond)),
		core.Subagent("sign_nda", "Countersign an NDA.", signer),
		core.Subagent("review_litigation", "Review litigation.", litigation),
		core.Parallelism(6), core.MaxSteps(4))...)
	if _, err := lead.Generate(ctx, core.RunID(chkChaos), core.Prompt("Review the Northwind data room.")); err != nil {
		t.Fatalf("agentic.chaos: %v", err)
	}

	// A thread turn parked on deploy; alice denies it; the session
	// resumes as turn 2, which answers.
	type env struct {
		Env string `json:"env"`
	}
	ops := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "d1", Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.Say("Deploy to prod cancelled: alice denied it (change freeze)."),
	), prov(core.Name("ops-bot"), core.Tool("deploy", "Deploy a build.", func(_ context.Context, in env) (string, error) {
		return "deployed to " + in.Env, nil
	}, core.RequireApproval()))...)
	s, err := thread.Create(ctx, thread.Memory(), ops, pinnedIDs(chkOpsSess), thread.PublicID("pub_chk_ops"))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("Deploy build 1.4.2 to prod."))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("ops t1: %v %v, want one parked call", res, err)
	}
	d := thread.Deny("d1", "change freeze until Monday")
	d.Who = "alice"
	t2, err := s.Decide(ctx, d)
	if err != nil || t2 == nil {
		t.Fatalf("ops decide: %v %v, want the resumed turn", t2, err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// A nine-turn session: a support conversation on scripted answers.
	var turns []wefttest.Turn
	for i := 1; i <= 9; i++ {
		turns = append(turns, wefttest.Say(fmt.Sprintf("Turn %d: order A-%d is packed and leaves the Lisbon warehouse tomorrow morning by DHL Express.", i, 1000+i)))
	}
	chat := core.New(wefttest.Script(turns...), prov(core.Name("support-desk"))...)
	cs, err := thread.Create(ctx, thread.Memory(), chat, pinnedIDs(chkChatSess), thread.PublicID("pub_chk_chat"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 9; i++ {
		tr, err := cs.Send(ctx, core.User(fmt.Sprintf("Where is my order A-%d? It was due yesterday.", 1000+i)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if err := cs.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	c := newClient(base, token)
	settled := map[string]string{
		chkSubFailed: `"status":"succeeded"`, chkChaos: `"status":"succeeded"`,
		chkOpsSess + "-t1": `"resumed_by":"` + chkOpsSess + `-t2"`, chkOpsSess + "-t2": `"status":"succeeded"`,
		chkChatSess + "-t9": `"status":"succeeded"`,
	}
	for id, want := range settled {
		for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			b, err := c.get(ctx, runPath(id), nil)
			if err == nil && bytes.Contains(b, []byte(want)) && !bytes.Contains(b, []byte(`"settling":true`)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s never read %s: %.400s %v", id, want, b, err)
			}
		}
	}
	say("seeded sub.failed, agentic.chaos, a denied thread turn and a nine-turn session (wefttest scripts through the otel pipeline)")
}

// pinnedIDs names the session id and numbers its entries after it.
func pinnedIDs(session string) thread.SessionOption {
	n := 0
	return thread.IDs(func() string {
		n++
		if n == 1 {
			return session
		}
		return fmt.Sprintf("%s_e%04d", session, n)
	})
}
