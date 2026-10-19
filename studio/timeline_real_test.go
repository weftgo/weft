package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
)

// TestSessionTimelineFromARealRun (U12.2's done line): acme-support's
// session — the arena's thread.support-approvals shape — driven through
// a real thread session and a Local pipeline into SQLite, read back
// through GET /api/sessions/{id}/timeline: the six turns in order, the
// marks in order under the runs they concern (each request and decision
// under the run that parked its call, the approve-always grant under
// it too, the label under the last turn), on their lanes; then every
// field the session-timeline and session-entries goldens pin exists in
// the real answers with the same JSON type (TestGoldensMatchARealRun's
// rule), and the tag filter finds the session by its
// thread.WithRunMetadata tag.
func TestSessionTimelineFromARealRun(t *testing.T) {
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
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r1", Args: `{"order_id":"A-1002"}`}),
		wefttest.Say("I need a photo of the damage first."),
		wefttest.ToolCalls(wefttest.Call{Name: "issue_refund", ID: "r2", Args: `{"order_id":"A-1002"}`}),
		wefttest.Say("Refund issued."),
		wefttest.ToolCalls(wefttest.Call{Name: "send_email", ID: "e1", Args: `{"order_id":"A-1002"}`}),
		wefttest.Say("Email sent."),
	), core.Name("acme-support"), core.Instructions("You are a support agent."),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), refund, email)
	s, err := thread.Create(ctx, thread.Memory(), agt, thread.PublicID("pub_acme"),
		thread.WithRunMetadata(map[string]string{"arena.batch": "b_u122"}))
	if err != nil {
		t.Fatal(err)
	}
	idle := func() {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := s.WaitIdle(wctx); err != nil {
			t.Fatal(err)
		}
	}
	send := func(text string) {
		t.Helper()
		turn, err := s.Send(ctx, core.User(text))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	decide := func(d thread.Decision) {
		t.Helper()
		d.Who = "agent-7"
		if _, err := s.Decide(ctx, d); err != nil {
			t.Fatal(err)
		}
		idle()
	}
	send("Order A-1002 arrived with the dripper cracked — refund it in full, please.")
	decide(thread.Deny("r1", "need a photo of the damage"))
	send("Here is the photo of the cracked dripper.")
	decide(thread.Approve("r2"))
	send("Please email me a confirmation.")
	decide(thread.ApproveAlways("e1"))
	if err := s.Label(ctx, s.Leaf(), "case-closed"); err != nil {
		t.Fatal(err)
	}
	sid := s.ID()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	fetch := func(path string) (any, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		return doc, string(b)
	}
	doc, body := fetch("/api/sessions/" + sid + "/timeline")
	var tl struct {
		Turns []struct {
			RunID string `json:"run_id"`
			Turn  int    `json:"turn"`
		} `json:"turns"`
		Marks []struct {
			Kind    string            `json:"kind"`
			Lane    string            `json:"lane"`
			Turn    *int              `json:"turn"`
			Content map[string]string `json:"content"`
		} `json:"marks"`
	}
	if err := json.Unmarshal([]byte(body), &tl); err != nil {
		t.Fatal(err)
	}
	var turns []string
	for _, tr := range tl.Turns {
		turns = append(turns, fmt.Sprintf("%d:%s", tr.Turn, strings.TrimPrefix(tr.RunID, sid)))
	}
	if want := []string{"1:-t1", "2:-t2", "3:-t3", "4:-t4", "5:-t5", "6:-t6"}; !slices.Equal(turns, want) {
		t.Errorf("acme-support's turns = %v, want %v", turns, want)
	}
	var marks []string
	for _, m := range tl.Marks {
		turn := "-"
		if m.Turn != nil {
			turn = fmt.Sprint(*m.Turn)
		}
		marks = append(marks, fmt.Sprintf("t%s %s %s", turn, m.Kind, m.Lane))
	}
	want := []string{
		"t1 approval.request approvals", "t1 approval.decision approvals",
		"t3 approval.request approvals", "t3 approval.decision approvals",
		"t5 approval.request approvals", "t5 approval.decision approvals", "t5 grant.mint grants",
		"t6 label branch_fork",
	}
	if !slices.Equal(marks, want) {
		t.Errorf("acme-support's marks = %q\nwant %q", marks, want)
	}
	if len(tl.Marks) > 1 && (tl.Marks[1].Content["who"] != "agent-7" || tl.Marks[1].Content["reason"] != "need a photo of the damage") {
		t.Errorf("the deny's content = %v, want who and reason (Local is content on)", tl.Marks[1].Content)
	}
	entries, _ := fetch("/api/sessions/" + sid + "/entries?limit=3")
	for _, c := range []struct {
		golden string
		doc    any
		absent []string
	}{
		// The fixture holds what this session does not: a stripped
		// receipt (the stripped hole), a compaction marker (no fact_at),
		// a session-level mark (run_id/turn null), a parked turn, a priced summarizer
		// call (a turn's and the session's summarizer, U15.1); a mark's
		// fields are its kind's (data, not shape: the field set is §4's table).
		{"session-timeline.golden.json", doc, []string{".marks[].run_id=<nil>", ".marks[].turn=<nil>", ".holes[]", ".marks[].fields.", ".summarizer", ".turns[].summarizer"}},
		{"session-entries.golden.json", entries, []string{".holes[]", ".entries[].fields."}},
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "api", c.golden))
		if err != nil {
			t.Fatal(err)
		}
		var g any
		if err := json.Unmarshal(b, &g); err != nil {
			t.Fatal(err)
		}
		wantP, gotP := map[string]bool{}, map[string]bool{}
		shapePaths("", g, wantP)
		shapePaths("", c.doc, gotP)
		var missing []string
	next:
		for k := range wantP {
			if gotP[k] {
				continue
			}
			for _, a := range c.absent {
				if strings.HasPrefix(k, a) {
					continue next
				}
			}
			missing = append(missing, k)
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s pins fields a real thread session's answer does not carry: %v", c.golden, missing)
		}
	}
	// The tag filter (D6) finds the session by its WithRunMetadata tag.
	_, list := fetch("/api/sessions?tag.arena.batch=b_u122")
	if !strings.Contains(list, `"id":"`+sid+`"`) || !strings.Contains(list, `"total":1`) {
		t.Errorf("sessions?tag.arena.batch=b_u122 = %s, want the session", list)
	}
}
