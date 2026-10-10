package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

// TestSearchRefundFailedAcrossRealRuns is the phase-5 gate's clause
// "search 'refund failed' across runs" (plan A7) end to end: scripted
// runs driven through the real pipeline (otel → OTLP ingest → obsdb),
// then GET /api/search. A run matches when every term is in its prompt,
// answer, error or a tool name — each term in any of them — and only
// those: a system prompt or a tool result holding the words is no hit.
func TestSearchRefundFailedAcrossRealRuns(t *testing.T) {
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type in struct {
		OrderID string `json:"order_id"`
	}
	issueRefund := core.Tool("issue_refund", "Refund an order.", func(context.Context, in) (string, error) {
		return "ok", nil
	})
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, in) (string, error) {
		return "the refund failed upstream", nil // a tool result: never searched
	})
	run := func(name, prompt string, turns ...wefttest.Turn) string {
		t.Helper()
		agent := core.New(wefttest.Script(turns...), core.Name(name),
			core.Instructions("When a refund failed, apologise."), // the system prompt: never searched
			core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), issueRefund, lookup)
		res, err := agent.Generate(ctx, core.Prompt(prompt))
		var re *core.RunError
		if errors.As(err, &re) {
			return re.Result.ID
		}
		if err != nil {
			t.Fatal(err)
		}
		return res.ID
	}
	answer := run("support", "Where is my parcel?", wefttest.Say("Your refund failed to process; retrying."))
	time.Sleep(5 * time.Millisecond)
	errored := run("support", "Refund order 7 please",
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", Args: `{"order_id":"7"}`}),
		wefttest.Fail(errors.New("payment gateway failed")))
	time.Sleep(5 * time.Millisecond)
	_ = run("support", "Refund order 9", wefttest.Say("Done, refunded.")) // no "failed"
	time.Sleep(5 * time.Millisecond)
	_ = run("billing", "Check order 3",
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"3"}`}),
		wefttest.Say("All good.")) // the words only in a tool result and the system prompt
	// A continued conversation: turn 2 is fed turn 1's transcript and
	// its own question — found by that question, never by turn 1's.
	conv := core.New(wefttest.Script(wefttest.Say("Order 5 shipped."), wefttest.Say("Your parcel insurance is active.")),
		core.Name("support"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	turn1, err := conv.Generate(ctx, core.Prompt("Where is order 5?"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	turn2, err := conv.Generate(ctx, core.Messages(turn1.Messages...), core.Prompt("Is my parcel insured?"))
	if err != nil {
		t.Fatal(err)
	}
	// A resume through the real path: a RequireApproval tool parks the
	// run; the next run approves it and carries a new prompt, which the
	// core records after the rebuilt tool message (the record after the
	// input record) — found by that prompt, not the parked question.
	cancel := core.Tool("cancel_booking", "Cancel a booking.", func(context.Context, in) (string, error) {
		return "cancelled", nil
	}, core.RequireApproval())
	booker := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "cancel_booking", Args: `{"order_id":"11"}`}),
		wefttest.Say("Done."),
	), core.Name("bookings"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), cancel)
	parked, err := booker.Generate(ctx, core.Prompt("Cancel booking eleven"))
	if err != nil || len(parked.Pending) != 1 {
		t.Fatalf("parked run: %v, pending %v", err, parked.Pending)
	}
	time.Sleep(5 * time.Millisecond)
	resumed, err := booker.Generate(ctx, core.Messages(parked.Messages...), core.Approve(parked.Pending[0].ID),
		core.Prompt("Make it booking twenty-two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// The first search waits for ingest to land (the shutdown flushed
	// it); the rest read what is stored.
	waited := false
	search := func(query string) []string {
		t.Helper()
		var ids []string
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			resp, err := http.Get(ts.URL + "/api/search?" + query)
			if err != nil {
				t.Fatal(err)
			}
			var page struct {
				Runs []struct {
					ID string `json:"id"`
				} `json:"runs"`
			}
			err = json.NewDecoder(resp.Body).Decode(&page)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("search?%s = %d %v", query, resp.StatusCode, err)
			}
			ids = ids[:0]
			for _, r := range page.Runs {
				ids = append(ids, r.ID)
			}
			if waited || len(ids) > 0 || time.Now().After(deadline) {
				waited = true
				return ids
			}
		}
	}
	q := url.Values{"q": {"refund failed"}}
	if got, want := search(q.Encode()), []string{errored, answer}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("search 'refund failed' (newest first) = %v, want %v (the error run, then the answer run)", got, want)
	}
	q.Set("sort", "duration")
	if got := search(q.Encode()); len(got) != 2 {
		t.Errorf("search 'refund failed' by duration = %v, want the same two runs", got)
	}
	if got := search("tool=issue_refund&q=failed"); strings.Join(got, ",") != errored {
		t.Errorf("tool=issue_refund&q=failed = %v, want %s", got, errored)
	}
	if got := search("error=gateway"); strings.Join(got, ",") != errored {
		t.Errorf("error=gateway = %v, want %s", got, errored)
	}
	if got := search(url.Values{"q": {`"parcel insured"`}}.Encode()); strings.Join(got, ",") != turn2.ID {
		t.Errorf("search turn 2's question = %v, want turn 2 %s", got, turn2.ID)
	}
	if got := search(url.Values{"q": {`"order 5?"`}}.Encode()); strings.Join(got, ",") != turn1.ID {
		t.Errorf("search turn 1's question = %v, want turn 1 %s alone", got, turn1.ID)
	}
	if got := search(url.Values{"q": {`"booking twenty-two"`}}.Encode()); strings.Join(got, ",") != resumed.ID {
		t.Errorf("search the resume's new prompt = %v, want the resumed run %s", got, resumed.ID)
	}
	if got := search(url.Values{"q": {`"booking eleven"`}}.Encode()); strings.Join(got, ",") != parked.ID {
		t.Errorf("search the parked question = %v, want the parked run %s alone", got, parked.ID)
	}
	if got := search("q=apologise"); len(got) != 0 {
		t.Errorf("q=apologise (the system prompt's word) = %v, want nothing", got)
	}
}
