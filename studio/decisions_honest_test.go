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

// TestFoldDecisions (U13.2-fix 4b): a call's records fold as thread
// folds them — a quorum's approvers all named, a split verdict a deny
// via quorum with no reason of its own (the transcript's DENIED text is
// the model-visible one), the session's own expiry beside an approval
// its own deny, a resolve resolved.
func TestFoldDecisions(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	a := func(who, key string) rawDecision {
		return rawDecision{outcome: "approve", via: "signed", who: who, keyID: key, at: &at}
	}
	for _, tc := range []struct {
		name string
		ds   []rawDecision
		want string
	}{
		{"quorum", []rawDecision{a("alice", "k_alice"), a("bob", "k_bob")}, "approved|signed|alice + bob|k_alice + k_bob|"},
		{"split", []rawDecision{a("alice", "k_alice"), {outcome: "deny", via: "user", who: "bob", reason: "no"}}, "denied|quorum|alice + bob|k_alice|"},
		{"expiry beside an approval", []rawDecision{a("alice", ""), {outcome: "deny", via: "expiry", reason: "expired: no decision before 09:00"}}, "denied|expiry|||expired: no decision before 09:00"},
		{"resolve", []rawDecision{{outcome: "resolve_error", via: "user", who: "agent-7"}}, "resolved|user|agent-7||"},
		{"undecided", nil, "||||"},
	} {
		v := foldDecisions(tc.ds)
		got := strings.Join([]string{v.outcome, v.via, strings.Join(v.who, " + "), strings.Join(v.keys, " + "), v.reason}, "|")
		if got != tc.want {
			t.Errorf("%s: fold = %q, want %q", tc.name, got, tc.want)
		}
	}
}
