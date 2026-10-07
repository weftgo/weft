package studio

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// runCompactionT is a run document's compactions[] row as a client
// decodes it (api.go's runCompaction).
type runCompactionT struct {
	Scope        string `json:"scope"`
	Index        *int64 `json:"index"`
	Step         *int   `json:"step"`
	FromSeq      *int64 `json:"from_seq"`
	ToSeq        *int64 `json:"to_seq"`
	Hash         string `json:"hash"`
	Replaced     int    `json:"replaced"`
	Entries      int    `json:"entries"`
	TokensBefore int64  `json:"tokens_before"`
	TokensAfter  int64  `json:"tokens_after"`
	Reason       string `json:"reason"`
}

// runNorm normalizes a real run's document beside stepNorm: the trace
// id and last_seen a run stamps differently each time.
var runNorm = regexp.MustCompile(`"(last_seen|trace_id)": ?"[^"]*"`)

// TestRunCompactionsView (plan A9.2): the run document of A7's step
// run — whose step 2 has a PrepareStep trim — names the run-scope view
// with its step, index, the replaced range [1, 3) and the counts (2
// messages rewritten into 1), the hash the step route's compaction
// block carries, and no message body. The run's events and transcript
// are pinned beside it: the page's tests draw the marker from these
// three goldens, and "show original" expands transcript seqs 1 and 2.
func TestRunCompactionsView(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_steps", nil)
	body := fetchJSON(t, ts, "/api/runs/r_steps", func(b string) bool {
		return strings.Contains(b, `"request_count":6`) && strings.Contains(b, `"id":"r_steps/1/c_sub"`)
	})
	stepGolden(t, "run-compacted.golden.json", runNorm.ReplaceAllString(body, `"$1":"(norm)"`))
	var doc struct {
		Compactions []runCompactionT `json:"compactions"`
	}
	decode(t, body, &doc)
	var step struct {
		Compaction *struct {
			Index int64  `json:"index"`
			Hash  string `json:"hash"`
		} `json:"compaction"`
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_steps/steps/2", nil), &step)
	if len(doc.Compactions) != 1 || step.Compaction == nil {
		t.Fatalf("compactions = %+v (step 2's block %+v), want the one view", doc.Compactions, step.Compaction)
	}
	c := doc.Compactions[0]
	if c.Scope != "run" || c.Step == nil || *c.Step != 2 || c.Index == nil || *c.Index != step.Compaction.Index ||
		c.FromSeq == nil || *c.FromSeq != 1 || c.ToSeq == nil || *c.ToSeq != 3 || c.Replaced != 2 || c.Entries != 1 ||
		c.Hash != step.Compaction.Hash || c.Reason != "" || c.TokensBefore != 0 {
		t.Errorf("view = %+v, want step 2, index %d, [1,3), 2 → 1, hash %s", c, step.Compaction.Index, step.Compaction.Hash)
	}
	if strings.Contains(body, "summary: the order was looked up") {
		t.Errorf("the run document inlines the view's body: %s", body)
	}
	stepGolden(t, "events-compacted.golden.json", fetchJSON(t, ts, "/api/runs/r_steps/events?limit=1000", nil))
	stepGolden(t, "transcript-compacted.golden.json", fetchJSON(t, ts, "/api/runs/r_steps/transcript", nil))
}

// TestRunCompactionsReadToken: a read-scoped panel token reads the run
// document's compactions — counts and hash — and no message body (the
// view's replacement text appears nowhere in the answer).
func TestRunCompactionsReadToken(t *testing.T) {
	const tok = "srv-token"
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	recordStepsRun(t, ts.URL, "r_tok", map[string]string{"weft.public_id": "pub_a"})
	read, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	body := fetchJSON(t, ts, "/api/runs/r_tok?token="+read, func(b string) bool { return strings.Contains(b, `"request_count":6`) })
	var doc struct {
		Compactions []runCompactionT `json:"compactions"`
	}
	decode(t, body, &doc)
	if len(doc.Compactions) != 1 || doc.Compactions[0].Replaced != 2 || doc.Compactions[0].Entries != 1 || doc.Compactions[0].Hash == "" {
		t.Errorf("read token compactions = %+v, want the view's counts and hash", doc.Compactions)
	}
	if strings.Contains(body, "summary: the order was looked up") || strings.Contains(body, `"messages"`) {
		t.Errorf("read token run document carries message bodies: %s", body)
	}
}

// TestRunCompactionsSession (plan A9.2): thread's session marker
// through the real pipeline (thread.Memory → otel → OTLP ingest →
// obsdb): two turns, then a manual Compact keeping one recent message.
// The marker is filed under turn 2's run — the run that produced the
// compacted context — and its run document names it with scope
// session, reason manual, the counts and the token estimates, no
// index, step or range; turn 1's document names none.
func TestRunCompactionsSession(t *testing.T) {
	ts, _ := requestsServer(t)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	model := wefttest.Script(
		wefttest.Say(strings.Repeat("a", 4000)), wefttest.Say(strings.Repeat("b", 4000)), wefttest.Say("summary one"))
	agent := core.New(model, core.Name("support"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	n := 0
	ids := thread.IDs(func() string { n++; return fmt.Sprintf("s_compact_%03d", n) })
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1), ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"q1", "q2"} {
		turn, err := s.Send(ctx, core.User(q))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	run := s.ID() + "-t2"
	body := fetchJSON(t, ts, "/api/runs/"+run, func(b string) bool { return strings.Contains(b, `"scope":"session"`) })
	// The marker's hash is over the compaction entry as thread wrote it,
	// its timestamp included: normalized (its length is checked below).
	norm := regexp.MustCompile(`"hash":"[0-9a-f]{64}"`).ReplaceAllString(runNorm.ReplaceAllString(body, `"$1":"(norm)"`), `"hash":"(hash)"`)
	stepGolden(t, "run-session-compacted.golden.json", norm)
	var doc struct {
		Compactions []runCompactionT `json:"compactions"`
	}
	decode(t, body, &doc)
	if len(doc.Compactions) != 1 {
		t.Fatalf("compactions of %s = %+v, want the one session marker", run, doc.Compactions)
	}
	c := doc.Compactions[0]
	if c.Scope != "session" || c.Reason != "manual" || c.Index != nil || c.Step != nil || c.FromSeq != nil || c.ToSeq != nil ||
		c.Replaced < 1 || c.Entries < 1 || c.TokensBefore <= c.TokensAfter || c.TokensAfter <= 0 || len(c.Hash) != 64 {
		t.Errorf("session marker = %+v, want scope session, reason manual, counts and tokens before > after, no range", c)
	}
	var first struct {
		Compactions []runCompactionT `json:"compactions"`
	}
	decode(t, fetchJSON(t, ts, "/api/runs/"+s.ID()+"-t1", nil), &first)
	if first.Compactions == nil || len(first.Compactions) != 0 {
		t.Errorf("turn 1's compactions = %+v, want [] (none filed under it)", first.Compactions)
	}
}
