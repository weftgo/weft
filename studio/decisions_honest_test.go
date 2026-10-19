package studio

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// ingestEdited ingests one contract fixture into a Studio of its own
// with only the attributes edit keeps (edit may change a value in
// place) — the records as another writer, an older core or a stock
// collector would have sent them. logs false sends the spans alone.
func ingestEdited(t *testing.T, fixture string, logs bool, edit func(kv map[string]any) bool) http.Handler {
	t.Helper()
	srv := New(Open(t.TempDir()+"/dec.db"), Token("srv"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	kinds := []string{"traces"}
	if logs {
		kinds = append(kinds, "logs")
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				x[k] = walk(c)
			}
		case []any:
			out := x[:0]
			for _, c := range x {
				if m, ok := c.(map[string]any); ok {
					if _, kv := m["key"].(string); kv && m["value"] != nil && !edit(m) {
						continue
					}
				}
				out = append(out, walk(c))
			}
			return out
		}
		return v
	}
	for _, k := range kinds {
		b, err := os.ReadFile("../obsdb/testdata/contract/" + fixture + "/" + k + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(walk(doc))
		if err != nil {
			t.Fatal(err)
		}
		if code, _, body := post(t, h, "/studio/v1/"+k, "application/json", string(out)); code != http.StatusOK {
			t.Fatalf("ingest %s/%s: %d %s", fixture, k, code, body)
		}
	}
	return h
}

// dropKeys is an ingestEdited edit leaving out the named attributes.
func dropKeys(keys ...string) func(map[string]any) bool {
	return func(kv map[string]any) bool { return !slices.Contains(keys, kv["key"].(string)) }
}

// readDoc decodes a decisions block.
func readDoc(t *testing.T, raw json.RawMessage) decisionDoc {
	t.Helper()
	var d decisionDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decisions block %s: %v", raw, err)
	}
	return d
}

// callLines is a block's calls as "<id>=<outcome>[ derived]".
func callLines(d decisionDoc) string {
	var out []string
	for _, c := range d.Calls {
		s := c.CallID + "=" + c.Outcome
		if c.Derived {
			s += " derived"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func firstOf(a, _ json.RawMessage) json.RawMessage { return a }

// TestDecisionsSupportDesk: rw.support-desk's own shape — a plain
// Generate the arena resumes with Approve and ResumeOf, no
// weft.approval.actor — reads approved with no decider: the `resumed`
// fixture without its actor (decisions_support_desk.golden.json, read
// by parity.test.ts: "✓ approved → <run>").
func TestDecisionsSupportDesk(t *testing.T) {
	h := ingestEdited(t, "resumed", true, dropKeys("weft.approval.actor"))
	decided, _ := runBlocks(t, h, "a1100000000000000000000000000001", "srv")
	_, resumes := runBlocks(t, h, "a1100000000000000000000000000002", "srv")
	d := readDoc(t, decided)
	if d.Actor != "" || callLines(d) != "call_1=approved call_2=resolved" || d.Calls[0].Who != "" {
		t.Errorf("support-desk decided = %s, want approved + resolved, no actor, no who", decided)
	}
	b, err := json.Marshal(map[string]json.RawMessage{"support_desk_decided": decided, "support_desk_resumes": resumes})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "decisions_support_desk.golden.json", string(b))
}

// TestDecisionsWithoutResumeCalls (U13.2-fix 1, 4a, 5): a resume
// recorded before weft.resume.calls lists its calls from the pre-step
// results, and thread's record still names a resolve resolved; with no
// transcript either (spans alone) the step-0 execute_tool spans list
// them — a denied span's flag read as a bool or as the string "true" a
// stock collector into ClickHouse leaves —; with nothing that names a
// call the block is a not_recorded hole, never an empty list alone.
func TestDecisionsWithoutResumeCalls(t *testing.T) {
	const sess = "01K00000000000000000000001"
	h := ingestEdited(t, "thread_approvals", true, dropKeys("weft.resume.calls"))
	d := readDoc(t, firstOf(runBlocks(t, h, sess+"-t7", "srv")))
	if d.Source != "transcript" || callLines(d) != "p1=resolved" {
		t.Errorf("t7 without weft.resume.calls = %+v, want the transcript's call, resolved by thread's record (not derived)", d)
	}
	stringly := func(kv map[string]any) bool {
		switch kv["key"] {
		case "weft.resume.calls":
			return false
		case "weft.tool.denied":
			kv["value"] = map[string]any{"stringValue": "true"}
		}
		return true
	}
	for name, edit := range map[string]func(map[string]any) bool{"bool": dropKeys("weft.resume.calls"), "string": stringly} {
		h := ingestEdited(t, "approval_denied", false, edit)
		d := readDoc(t, firstOf(runBlocks(t, h, "a1100000000000000000000000000001", "srv")))
		if d.Source != "spans" || callLines(d) != "call_1=denied derived call_2=no_decision derived" || d.Hole != "" {
			t.Errorf("%s: spans alone = %+v, want both calls from their denied spans", name, d)
			continue
		}
		for _, c := range d.Calls {
			if c.At == nil {
				t.Errorf("%s: %s has no time: the denied span's flag was not read", name, c.CallID)
			}
		}
	}
	none := ingestEdited(t, "resumed", false, dropKeys("weft.resume.calls", "gen_ai.tool.call.id"))
	d = readDoc(t, firstOf(runBlocks(t, none, "a1100000000000000000000000000001", "srv")))
	if d.Source != "none" || d.Hole != "not_recorded" || len(d.Calls) != 0 {
		t.Errorf("nothing names a call = %+v, want source none, hole not_recorded", d)
	}
}

// TestFoldDecisions (U13.2-fix 4b, U13.2-fix2 4): a call's records fold
// as thread folds them — a met quorum's approvers all named, a call
// short of its quorum undecided and unnamed, a split verdict a deny via
// quorum with no reason of its own (the transcript's DENIED text is the
// model-visible one) naming its denier as the decider and the approvals
// as approvers, the session's own expiry beside an approval its own
// deny, a resolve resolved.
func TestFoldDecisions(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	a := func(who, key string) rawDecision {
		return rawDecision{outcome: "approve", via: "signed", who: who, keyID: key, at: &at}
	}
	for _, tc := range []struct {
		name   string
		ds     []rawDecision
		quorum int
		want   string
	}{
		{"two approvals, no quorum", []rawDecision{a("alice", "k_alice"), a("bob", "k_bob")}, 0, "approved|signed|alice + bob|k_alice + k_bob||"},
		// U13.2-fix2 4a: a quorum met names every approver; one short of
		// it names nobody (the call is core's no_decision).
		{"quorum met", []rawDecision{a("alice", "k_alice"), a("bob", "k_bob")}, 2, "approved|quorum|alice + bob|k_alice + k_bob||"},
		{"short of its quorum", []rawDecision{a("alice", "k_alice")}, 2, "|||||"},
		{"one key twice is one approver", []rawDecision{a("alice", "k_alice"), a("alice2", "k_alice")}, 2, "|||||"},
		// U13.2-fix2 4b: a split verdict's decider is its denier; the
		// approvals it overruled are approvers, never deniers.
		{"split", []rawDecision{a("alice", "k_alice"), {outcome: "deny", via: "user", who: "bob", reason: "no"}}, 2, "denied|quorum|bob||alice|"},
		{"resolve beside an approval", []rawDecision{a("alice", ""), {outcome: "resolve", via: "user", who: "carol"}}, 0, "denied|quorum|||alice|"},
		{"expiry beside an approval", []rawDecision{a("alice", ""), {outcome: "deny", via: "expiry", reason: "expired: no decision before 09:00"}}, 0, "denied|expiry||||expired: no decision before 09:00"},
		{"resolve", []rawDecision{{outcome: "resolve_error", via: "user", who: "agent-7"}}, 0, "resolved|user|agent-7|||"},
		{"undecided", nil, 0, "|||||"},
	} {
		v := foldDecisions(tc.ds, tc.quorum)
		got := strings.Join([]string{v.outcome, v.via, strings.Join(v.who, " + "), strings.Join(v.keys, " + "), strings.Join(v.approvers, " + "), v.reason}, "|")
		if got != tc.want {
			t.Errorf("%s: fold = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ingestWithoutChat ingests a fixture's spans alone with its chat spans
// and weft.resume.calls left out: a resume no model call followed (it
// parked again, or failed, before one), recorded with no transcript.
func ingestWithoutChat(t *testing.T, fixture string) http.Handler {
	t.Helper()
	srv := New(Open(t.TempDir()+"/dec.db"), Token("srv"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	b, err := os.ReadFile("../obsdb/testdata/contract/" + fixture + "/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ResourceSpans []struct {
			Resource   any `json:"resource"`
			ScopeSpans []struct {
				Scope any              `json:"scope"`
				Spans []map[string]any `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for i := range doc.ResourceSpans {
		for j := range doc.ResourceSpans[i].ScopeSpans {
			ss := &doc.ResourceSpans[i].ScopeSpans[j]
			var keep []map[string]any
			for _, sp := range ss.Spans {
				if name, _ := sp["name"].(string); strings.HasPrefix(name, "chat") {
					continue
				}
				attrs, _ := sp["attributes"].([]any)
				var kept []any
				for _, a := range attrs {
					if m, _ := a.(map[string]any); m["key"] != "weft.resume.calls" {
						kept = append(kept, a)
					}
				}
				sp["attributes"] = kept
				keep = append(keep, sp)
			}
			ss.Spans = keep
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, body := post(t, h, "/studio/v1/traces", "application/json", string(out)); code != http.StatusOK {
		t.Fatalf("ingest %s: %d %s", fixture, code, body)
	}
	return h
}

// TestDecisionsNoModelCall (U13.2-fix2 3): a resume with no step-0 chat
// span ran no model call, so every step-0 execute_tool span is a
// decided call — listed approved (derived), never skipped into a
// not_recorded hole.
func TestDecisionsNoModelCall(t *testing.T) {
	h := ingestWithoutChat(t, "resumed")
	d := readDoc(t, firstOf(runBlocks(t, h, "a1100000000000000000000000000001", "srv")))
	if d.Source != "spans" || callLines(d) != "call_1=approved derived" || d.Hole != "" {
		t.Errorf("no model call = %+v, want call_1 approved from its span", d)
	}
}

// TestDecisionsResolvedResult (U13.2-fix2 2): core emits no tool event
// or span for a resolve, so a resolved call's result is served on the
// decided call from the resume's pre-step result — under the content
// policy: hidden to a read token, not_recorded with no transcript.
func TestDecisionsResolvedResult(t *testing.T) {
	res := decisionsServer(t, "resumed")
	d := readDoc(t, firstOf(runBlocks(t, res, "a1100000000000000000000000000001", "srv")))
	if c := d.Calls[1]; c.Outcome != outcomeResolved || c.Result != "noted by hand" || c.ResultBadge != "" {
		t.Errorf("resumed call_2 = %+v, want resolved with the result noted by hand", c)
	}
	if c := d.Calls[0]; c.Result != "" || c.ResultBadge != "" {
		t.Errorf("an approved call carries its result in the fold, not here: %+v", c)
	}
	const sess = "01K00000000000000000000001"
	h := decisionsServer(t, "thread_approvals")
	read, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_thread_approvals", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	full := readDoc(t, firstOf(runBlocks(t, h, sess+"-t7", "srv")))
	hidden := readDoc(t, firstOf(runBlocks(t, h, sess+"-t7", read)))
	if full.Calls[0].Result == "" || hidden.Calls[0].Result != "" || hidden.Calls[0].ResultBadge != "hidden" {
		t.Errorf("t7's resolve: full %+v, read token %+v — want the result, then hidden", full.Calls[0], hidden.Calls[0])
	}
	spans := ingestEdited(t, "resumed", false, func(map[string]any) bool { return true })
	d = readDoc(t, firstOf(runBlocks(t, spans, "a1100000000000000000000000000001", "srv")))
	if c := d.Calls[1]; c.Outcome != outcomeResolved || c.Result != "" || c.ResultBadge != "not_recorded" {
		t.Errorf("spans alone: call_2 = %+v, want resolved, result not_recorded", c)
	}
}

// TestDecisionsUndecidedNamesNoOne (U13.2-fix2 4a): a call core left
// undecided (weft.resume.calls no_decision: short of its quorum) takes
// no via, who or key from thread's records beside it.
func TestDecisionsUndecidedNamesNoOne(t *testing.T) {
	const sess = "01K00000000000000000000001"
	h := ingestEdited(t, "thread_approvals", true, func(kv map[string]any) bool {
		if kv["key"] == "weft.resume.calls" {
			if v, _ := kv["value"].(map[string]any); v["stringValue"] == "p1=resolved" {
				kv["value"] = map[string]any{"stringValue": "p1=no_decision"}
			}
		}
		return true
	})
	d := readDoc(t, firstOf(runBlocks(t, h, sess+"-t7", "srv")))
	if c := d.Calls[0]; c.Outcome != outcomeNoDecision || c.Via != "" || c.Who != "" || c.KeyID != "" || c.WhoHidden {
		t.Errorf("t7 undecided = %+v, want no_decision with no attribution", c)
	}
}
