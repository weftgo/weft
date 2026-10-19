package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// The sessions list's states (U16; UX-B-15, UX-B-16, UX-B-28): a row
// carries its newest turn's open park (pending), its outcome strip and
// its failed / ended counts (obsdb.SessionTurnFacts), and
// /api/sessions filters by awaiting, failed, compacted, lineage and
// since — a
// malformed value a 400 naming the parameter.
func TestSessionStatesAndFilters(t *testing.T) {
	const serverTok = "srv-token"
	db, err := sqlite.Open(t.TempDir() + "/states.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_park-t1", "pub_p", "s_park", `{"type":"run_finish","run_id":"s_park-t1","usage":{"input_tokens":1,"output_tokens":1},"steps":1,"pending":[{"id":"c1"}]}`, nil)
	seedRun(t, db, "s_plain-t1", "pub_q", "s_plain", "", nil)
	srv := New(DB(db), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	type row struct {
		ID       string   `json:"id"`
		Status   string   `json:"status"`
		Pending  int      `json:"pending"`
		Outcomes []string `json:"outcomes"`
	}
	list := func(path string) []row {
		t.Helper()
		code, out := b4Do(t, http.MethodGet, ts.URL+path, serverTok, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, out)
		}
		var page struct {
			Sessions []row `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatal(err)
		}
		return page.Sessions
	}
	byID := map[string]row{}
	for _, r := range list("/api/sessions") {
		byID[r.ID] = r
	}
	// The row's status is the newest turn's outcome — ?status='s rule,
	// so a parked newest turn reads parked, never succeeded, and
	// ?status=succeeded (below) leaves it out (P9-pre-web-fix2 11).
	if p := byID["s_park"]; p.Status != "parked" || p.Pending != 1 || !slices.Equal(p.Outcomes, []string{"parked"}) {
		t.Errorf("s_park = %+v; want parked, pending 1, outcomes [parked]", p)
	}
	if p := byID["s_plain"]; p.Status != "succeeded" {
		t.Errorf("s_plain status = %q; want succeeded", p.Status)
	}
	if p := byID["s_plain"]; p.Pending != 0 || !slices.Equal(p.Outcomes, []string{"succeeded"}) {
		t.Errorf("s_plain = %+v; want no pending, outcomes [succeeded]", p)
	}
	if got := list("/api/sessions?awaiting=1"); len(got) != 1 || got[0].ID != "s_park" {
		t.Errorf("awaiting=1 = %+v; want s_park alone", got)
	}
	if got := list("/api/sessions?failed=true"); len(got) != 0 {
		t.Errorf("failed=true = %+v; want none", got)
	}
	// status= is the newest turn's outcome, the session page's word: a
	// parked newest turn is not succeeded (P9-pre-web-fix).
	if got := list("/api/sessions?status=succeeded"); len(got) != 1 || got[0].ID != "s_plain" {
		t.Errorf("status=succeeded = %+v; want s_plain alone", got)
	}
	if got := list("/api/sessions?status=failed"); len(got) != 0 {
		t.Errorf("status=failed = %+v; want none", got)
	}
	if got := list("/api/sessions?compacted=1"); len(got) != 0 {
		t.Errorf("compacted=1 = %+v; want none", got)
	}
	if got := list("/api/sessions?lineage=fork"); len(got) != 0 {
		t.Errorf("lineage=fork = %+v; want none", got)
	}
	for _, q := range []string{"awaiting=maybe", "failed=2", "compacted=x", "status=parked", "status=nope", "lineage=cousin", "since=yesterday"} {
		if code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions?"+q, serverTok, ""); code != http.StatusBadRequest {
			t.Errorf("?%s = %d %s; want 400", q, code, out)
		}
	}
	// The session document carries the same facts.
	code, out := b4Do(t, http.MethodGet, ts.URL+"/api/sessions/s_park", serverTok, "")
	var doc row
	if code != http.StatusOK || json.Unmarshal([]byte(out), &doc) != nil || doc.Pending != 1 {
		t.Errorf("GET /api/sessions/s_park = %d %s; want pending 1", code, out)
	}
}

// TestSessionRowStatus (P9-pre-web-fix2 11): a session row's status is
// its newest turn's outcome — the word SessionQuery.Status filters by —
// so a cancelled or busy-ended newest turn never reads failed and a
// parked or decided one never succeeded; a row with no turn facts keeps
// its stored status.
func TestSessionRowStatus(t *testing.T) {
	for _, c := range []struct {
		stored   obsdb.Status
		outcomes []obsdb.TurnOutcome
		want     string
	}{
		{obsdb.StatusFailed, []obsdb.TurnOutcome{obsdb.OutcomeSucceeded, obsdb.OutcomeCancelled}, "cancelled"},
		{obsdb.StatusFailed, []obsdb.TurnOutcome{obsdb.OutcomeEnded}, "ended"},
		{obsdb.StatusFailed, []obsdb.TurnOutcome{obsdb.OutcomeCancelled, obsdb.OutcomeFailed}, "failed"},
		{obsdb.StatusSucceeded, []obsdb.TurnOutcome{obsdb.OutcomeParked}, "parked"},
		{obsdb.StatusSucceeded, []obsdb.TurnOutcome{obsdb.OutcomeResumed}, "resumed"},
		{obsdb.StatusRunning, []obsdb.TurnOutcome{obsdb.OutcomeRunning}, "running"},
		{obsdb.StatusSucceeded, nil, "succeeded"},
	} {
		if got := sessRow(obsdb.SessionRow{Status: c.stored, Outcomes: c.outcomes}).Status; got != c.want {
			t.Errorf("stored %s, outcomes %v: status %q, want %q", c.stored, c.outcomes, got, c.want)
		}
	}
}
