package thread_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// approvalAdditiveSamples are the format-2 kinds carrying the optional
// fields added after the format shipped — every one omitted when
// empty, so an entry without them is byte-for-byte what it always was
// and a reader that predates them reads the entry and ignores the
// field.
func approvalAdditiveSamples() map[string]thread.Entry {
	run := sessionID + "-t1"
	return map[string]thread.Entry{
		// A signed "approve and always allow", bound to its request entry.
		"approval_decision_signed.json": thread.ApprovalDecisionEntry{
			ID: entryID10, ParentID: entryID9, Created: at(11),
			CallID: "call_1", Outcome: thread.OutcomeApprove,
			Who: "avi", Via: "signed", RunID: run,
			Nonce:     "5f1d3c0a9b8e7d6c5b4a39281706f5e4.0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			KeyID:     "k1",
			RequestID: entryID9,
			Always:    true,
		},
		// A grant match: the grant named in a field, never only in prose.
		"approval_audit_grant.json": thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			CallID: "call_1", Step: thread.StepGrant, Outcome: "approved",
			Detail: "grant " + grantID0, RunID: run,
			GrantID: grantID0,
		},
		"approval_audit_grant_shared.json": thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			CallID: "call_1", Step: thread.StepGrant, Outcome: "denied",
			Detail: "shared grant org-policy-7", RunID: run,
			GrantID: "org-policy-7", GrantShared: true,
		},
		// A resume's start, listing the decisions it spends.
		"approval_audit_resume.json": thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			Step: thread.StepResume, Outcome: "started",
			Detail: "1 call(s) to resolve", RunID: sessionID + "-t2",
			Decisions: []string{entryID10},
		},
		// A refused signed decision: a reason and a key, no decision.
		"approval_audit_signed.json": thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			CallID: "call_1", Step: thread.StepSigned, Outcome: "refused",
			Detail: "replayed", RunID: run,
			KeyID: "k1",
		},
	}
}

// TestApprovalAdditiveFieldGoldens pins the wire bytes of the optional
// fields the approval kinds gained — still "v":2: each is additive, a
// key an older reader skips — and their round trip. Regenerate with
// `go test ./thread -update` from the module root.
func TestApprovalAdditiveFieldGoldens(t *testing.T) {
	for name, e := range approvalAdditiveSamples() {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(b), `"v":2`) {
			t.Errorf("%s: no v:2 on the wire: %s", name, b)
		}
		wefttest.Golden(t, filepath.Join("testdata", "format2", name), append(b, '\n'))
		got, err := thread.UnmarshalEntry(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", e) {
			t.Errorf("%s round trip: got %+v, want %+v", name, got, e)
		}
	}
	// Additive means the entries without the new fields did not move:
	// the samples the format shipped with carry none of the new keys.
	for _, e := range approvalSampleEntries() {
		if _, ok := e.(thread.GrantRevokedEntry); ok {
			continue // its own grant_id is the kind's original field
		}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{`"request_id"`, `"always"`, `"grant_id"`, `"grant_shared"`, `"decisions"`} {
			if bytes.Contains(b, []byte(key)) {
				t.Errorf("%T: an entry without the field writes %s: %s", e, key, b)
			}
		}
	}
}

// TestRequireSignedHeaderGolden pins the header a session created
// under RequireSigned writes: the format-1 envelope with the rule as
// one more metadata key — what makes the rule the session's own, read
// back by every Open.
func TestRequireSignedHeaderGolden(t *testing.T) {
	h := thread.Header{
		ID: sessionID, Created: at(0),
		Meta: map[string]string{"weft.require_signed": "true"},
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "format1", "require_signed_header.json")
	wefttest.Golden(t, path, append(b, '\n'))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back thread.Header
	if err := json.Unmarshal(bytes.TrimRight(raw, "\n"), &back); err != nil {
		t.Fatal(err)
	}
	if back.Meta["weft.require_signed"] != "true" {
		t.Fatalf("the golden header's meta: %v", back.Meta)
	}

	// And Create writes exactly that key, beside the caller's own.
	ctx := context.Background()
	ring, _ := signerRing(t)
	st := thread.Memory()
	agent, _ := refundAgent(wefttest.Say("hi"))
	s, err := thread.Create(ctx, st, agent,
		thread.WithKeyring(ring), thread.RequireSigned(), thread.WithMeta(map[string]string{"team": "payments"}))
	if err != nil {
		t.Fatal(err)
	}
	stored, _, _, err := st.Load(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Meta["weft.require_signed"] != "true" || stored.Meta["team"] != "payments" {
		t.Fatalf("the created header's meta: %v", stored.Meta)
	}
	plain, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, _, err = st.Load(ctx, plain.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, has := stored.Meta["weft.require_signed"]; has {
		t.Fatalf("a session created without RequireSigned carries the key: %v", stored.Meta)
	}
}
