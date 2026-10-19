package studio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// decisionsServer ingests one contract fixture into a Studio of its own
// (with a server token, so a panel token can be minted against it).
func decisionsServer(t *testing.T, fixture string) http.Handler {
	t.Helper()
	srv := New(Open(t.TempDir()+"/dec.db"), Token("srv"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	for _, k := range []string{"traces", "logs"} {
		ingestFile(t, h, "/studio/v1/"+k, "../obsdb/testdata/contract/"+fixture+"/"+k+".json")
	}
	return h
}

// runBlocks reads one run document's decided and resumes blocks.
func runBlocks(t *testing.T, h http.Handler, id, bearer string) (decided, resumes json.RawMessage) {
	t.Helper()
	code, _, body := getWith(t, h, "/studio/api/runs/"+id, bearer, "")
	if code != http.StatusOK {
		t.Fatalf("run %s: %d %s", id, code, body)
	}
	var d struct {
		Decided json.RawMessage `json:"decided"`
		Resumes json.RawMessage `json:"resumes"`
	}
	decode(t, body, &d)
	return d.Decided, d.Resumes
}

// TestRunDecisions pins the run document's decided / resumes blocks
// (U13.2; UX-B-03, UX-H-17, UX-S3-04) over the record contract's
// fixtures, one golden (testdata/api/decisions.golden.json) the web
// tests on both surfaces read: thread_approvals' t1 — agent-7's deny,
// "need a photo of the damage" — decided by t2, and t2 resuming it;
// alice's signed approval (via signed, key k_alice); the resolve; a
// read-scoped panel token's t1 (no who, no reason: hidden); the
// content-off session's t1 (the reason not_recorded); a plain Generate's
// deny and its undecided call (approval_denied); rw.support-desk's
// approve + resolve with the actor in metadata (resumed); the grant
// chain's grant / approver provenance (thread_grants).
func TestRunDecisions(t *testing.T) {
	const sess = "01K00000000000000000000001"
	out := map[string]json.RawMessage{}
	h := decisionsServer(t, "thread_approvals")
	out["thread_t1_decided"], _ = runBlocks(t, h, sess+"-t1", "srv")
	_, out["thread_t2_resumes"] = runBlocks(t, h, sess+"-t2", "srv")
	out["thread_t3_decided"], _ = runBlocks(t, h, sess+"-t3", "srv")
	out["thread_t7_decided"], _ = runBlocks(t, h, sess+"-t7", "srv")
	read, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_thread_approvals", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	out["read_token_t1_decided"], _ = runBlocks(t, h, sess+"-t1", read)
	if s := string(out["read_token_t1_decided"]); strings.Contains(s, "need a photo") ||
		strings.Contains(s, `"who"`) || !strings.Contains(s, `"reason_badge":"hidden"`) {
		t.Errorf("read token: who or the reason leaked, or no hidden badge: %s", s)
	}
	if s := string(out["thread_t1_decided"]); !strings.Contains(s, `"outcome":"denied"`) ||
		!strings.Contains(s, `"who":"agent-7"`) || !strings.Contains(s, `"reason":"need a photo of the damage"`) ||
		!strings.Contains(s, `"run":"`+sess+`-t2"`) {
		t.Errorf("t1 decided = %s, want denied by agent-7, the reason, run t2", s)
	}
	if s := string(out["thread_t2_resumes"]); !strings.Contains(s, `"run":"`+sess+`-t1"`) || !strings.Contains(s, `"outcome":"denied"`) {
		t.Errorf("t2 resumes = %s, want t1's deny", s)
	}

	// U13.2-fix2 1: a resume decided by two actors (thread comma-joins
	// them) names no one call's decider: a read token gets the hidden
	// badge, not a bare "denied".
	two := ingestEdited(t, "thread_approvals", true, func(kv map[string]any) bool {
		if kv["key"] == "weft.approval.actor" {
			kv["value"] = map[string]any{"stringValue": "agent-7,agent-8"}
		}
		return true
	})
	out["read_token_two_actors_t1_decided"], _ = runBlocks(t, two, sess+"-t1", read)
	if s := string(out["read_token_two_actors_t1_decided"]); !strings.Contains(s, `"who_hidden":true`) || strings.Contains(s, `"who"`) {
		t.Errorf("two actors, read token: %s, want who_hidden and no who", s)
	}
	off := decisionsServer(t, "thread_content_off")
	out["content_off_t1_decided"], _ = runBlocks(t, off, sess+"-t1", "srv")
	if s := string(out["content_off_t1_decided"]); !strings.Contains(s, `"reason_badge":"not_recorded"`) || strings.Contains(s, `"who"`) {
		t.Errorf("content off t1 = %s, want the reason not_recorded and no who", s)
	}
	den := decisionsServer(t, "approval_denied")
	out["approval_denied_decided"], _ = runBlocks(t, den, "a1100000000000000000000000000001", "srv")
	if s := string(out["approval_denied_decided"]); !strings.Contains(s, `"outcome":"no_decision"`) || !strings.Contains(s, `"reason":"over the limit"`) {
		t.Errorf("approval_denied = %s, want the deny's reason and the undecided call", s)
	}
	res := decisionsServer(t, "resumed")
	out["resumed_decided"], _ = runBlocks(t, res, "a1100000000000000000000000000001", "srv")
	_, out["resumed_resumes"] = runBlocks(t, res, "a1100000000000000000000000000002", "srv")
	if s := string(out["resumed_decided"]); !strings.Contains(s, `"actor":"op_42"`) || !strings.Contains(s, `"outcome":"resolved"`) {
		t.Errorf("resumed = %s, want the actor and the resolve", s)
	}
	gr := decisionsServer(t, "thread_grants")
	out["grants_t1_decided"], _ = runBlocks(t, gr, sess+"-t1", "srv")
	out["grants_t2_decided"], _ = runBlocks(t, gr, sess+"-t2", "srv")
	if s := string(out["grants_t1_decided"]) + string(out["grants_t2_decided"]); !strings.Contains(s, `"via":"grant"`) ||
		!strings.Contains(s, `"grant_id":"e_thread_grants_0002"`) || !strings.Contains(s, `"via":"approver"`) {
		t.Errorf("grants = %s, want the grant and the approver", s)
	}
	// A run neither parked-and-resumed nor a resume carries neither block.
	if d, r := runBlocks(t, h, sess+"-t8", "srv"); d != nil || r == nil {
		t.Errorf("t8 (a resume, not parked): decided %s, resumes %s", d, r)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "decisions.golden.json", string(b))
}

// TestDecisionsFromTranscript: a resume recorded before
// weft.resume.calls (U13.1) still states its calls — derived from its
// pre-step results (the step-0 output batch before the model's first
// message): core's "DENIED: no decision", thread's "DENIED: expired: …",
// any other DENIED a deny, else approved; a later tool batch is the
// model's own, never a decision.
func TestDecisionsFromTranscript(t *testing.T) {
	batches := []obsdb.TranscriptBatch{
		{Step: 0, Input: true, Messages: json.RawMessage(`[{"role":"assistant","content":[` +
			`{"type":"tool_call","id":"a","name":"refund"},{"type":"tool_call","id":"b","name":"refund"},` +
			`{"type":"tool_call","id":"c","name":"email"},{"type":"tool_call","id":"d","name":"email"}]}]`)},
		{Step: 0, Messages: json.RawMessage(`[{"role":"tool","content":[` +
			`{"type":"tool_result","call_id":"a","content":"refunded","is_error":false},` +
			`{"type":"tool_result","call_id":"b","content":"DENIED: over the limit","is_error":true},` +
			`{"type":"tool_result","call_id":"c","content":"DENIED: no decision","is_error":true},` +
			`{"type":"tool_result","call_id":"d","content":"DENIED: expired: no decision before 09:00","is_error":true}]}]`)},
		{Step: 0, Messages: json.RawMessage(`[{"role":"assistant","content":[{"type":"tool_call","id":"m","name":"lookup"}]},` +
			`{"role":"tool","content":[{"type":"tool_result","call_id":"m","content":"DENIED: by policy","is_error":true}]}]`)},
	}
	res, names := preStepResults(batches)
	var got []string
	for _, id := range res.order {
		got = append(got, id+"="+derivedOutcome(res.by[id])+":"+names[id])
	}
	want := "a=approved:refund b=denied:refund c=no_decision:email d=expired:email"
	if strings.Join(got, " ") != want {
		t.Errorf("derived = %v, want %s", got, want)
	}
}

// TestTimelineDecisionActor (P9-pre-web-fix): a decision mark carries
// the deciding resume's weft.approval.actor — metadata, as the run
// document's decided block's actor — for every identity, a read-scoped
// panel token included, while the free-text who and reason stay
// content (hidden to it).
func TestTimelineDecisionActor(t *testing.T) {
	const sess = "01K00000000000000000000001"
	h := decisionsServer(t, "thread_approvals")
	decided, _ := runBlocks(t, h, sess+"-t1", "srv")
	var d struct {
		Actor string `json:"actor"`
	}
	decode(t, string(decided), &d)
	if d.Actor == "" {
		t.Fatalf("t1 decided names no actor: %s", decided)
	}
	read, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_thread_approvals", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"srv", read} {
		code, _, body := getWith(t, h, "/studio/api/sessions/"+sess+"/timeline", tok, "")
		if code != http.StatusOK {
			t.Fatalf("timeline: %d %s", code, body)
		}
		var v struct {
			Marks []struct {
				Kind          string `json:"kind"`
				RunID         string `json:"run_id"`
				Actor         string `json:"actor"`
				ContentHidden bool   `json:"content_hidden"`
			} `json:"marks"`
		}
		decode(t, body, &v)
		found := false
		for _, m := range v.Marks {
			if m.Kind != "approval.decision" || (m.RunID != sess+"-t1" && m.RunID != sess+"-t2") {
				continue
			}
			found = true
			if m.Actor != d.Actor {
				t.Errorf("token %q: t1's decision mark actor %q, want the decided block's %q", tok[:3], m.Actor, d.Actor)
			}
			if tok == read && !m.ContentHidden {
				t.Errorf("read token: the decision's content is not hidden")
			}
		}
		if !found {
			t.Errorf("token %q: no decision mark for t1: %s", tok[:3], body)
		}
		if tok == read && strings.Contains(body, "need a photo") {
			t.Errorf("read token: the deny reason leaked: %s", body)
		}
	}
}

// TestTimelineDecisionActorChained (P9-pre-web-fix2 1): thread files a
// decision under the parked run, so its actor is the resume's — never
// the parked run's own, which names who decided the run before it. t3
// parks; t4 resumes it as alice and parks again; t5 resumes t4 as bob:
// t3's marks read alice, t4's bob. An app stamping an actor on every
// turn (t3's own "carol") changes nothing. A run no stored turn resumed
// keeps its own. One resume deciding several calls (t6 resumes t5 as
// "bob,k_dave") names each call's own — the key its record names, else,
// to a content reader, its who — and otherwise the list, as the
// resume's (actor_of).
func TestTimelineDecisionActorChained(t *testing.T) {
	const sess = "01K00000000000000000000009"
	run := func(n int, actor, from string) obsdb.RunRow {
		rr := obsdb.RunRow{ID: fmt.Sprintf("%s-t%d", sess, n), Turn: n, ResumedFrom: from, Meta: map[string]string{}}
		if actor != "" {
			rr.Meta[approvalActorKey] = actor
		}
		return rr
	}
	t3, t4, t5, t6 := run(3, "carol", ""), run(4, "alice", sess+"-t3"), run(5, "bob", sess+"-t4"), run(6, "bob,k_dave", sess+"-t5")
	lone := run(8, "erin", "")
	det := obsdb.SessionDetail{SessionRow: obsdb.SessionRow{ID: sess}, Runs: []obsdb.RunRow{t3, t4, t5, t6, lone}}
	srv := &Server{}
	mark := func(reads bool, runID, body string) sessionEntry {
		mb := srv.newMarkBuilder(httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess+"/timeline", nil), det)
		mb.readsContent = reads
		m, ok := mb.mark(obsdb.SessionEvent{SessionID: sess, RunID: runID, Kind: obsdb.RecordThread, Name: "weft.thread.approval.decision", Body: json.RawMessage(body)})
		if !ok {
			t.Fatalf("mark %s left out", runID)
		}
		return m
	}
	plain := `{"kind":"approval.decision","call_id":"c1","outcome":"approve"}`
	for _, c := range []struct {
		run, body  string
		reads      bool
		actor, off string
	}{
		{t3.ID, plain, true, "alice", ""},
		{t4.ID, plain, true, "bob", ""},
		{t4.ID, plain, false, "bob", ""},
		{t5.ID, `{"kind":"approval.decision","call_id":"c1","outcome":"approve","key_id":"k_dave","content":{"who":"dave"}}`, false, "k_dave", ""},
		{t5.ID, `{"kind":"approval.decision","call_id":"c2","outcome":"deny","content":{"who":"bob"}}`, true, "bob", ""},
		{t5.ID, `{"kind":"approval.decision","call_id":"c2","outcome":"deny","content":{"who":"bob"}}`, false, "bob,k_dave", "resume"},
		{t5.ID, `not json`, true, "bob,k_dave", "resume"},
		{lone.ID, plain, true, "erin", ""},
	} {
		m := mark(c.reads, c.run, c.body)
		if m.Actor != c.actor || m.ActorOf != c.off {
			t.Errorf("%s %s (reads %v): actor %q of %q, want %q of %q", c.run[len(sess):], c.body, c.reads, m.Actor, m.ActorOf, c.actor, c.off)
		}
	}
	// Any other kind carries no actor.
	mb := srv.newMarkBuilder(httptest.NewRequest(http.MethodGet, "/", nil), det)
	if m, _ := mb.mark(obsdb.SessionEvent{SessionID: sess, RunID: t3.ID, Kind: obsdb.RecordThread, Name: "weft.thread.approval.request", Body: json.RawMessage(plain)}); m.Actor != "" {
		t.Errorf("a request mark carries actor %q", m.Actor)
	}
}
