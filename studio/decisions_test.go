package studio

import (
	"encoding/json"
	"net/http"
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
