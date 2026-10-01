package thread_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// turnAdditiveSamples are the entries carrying the optional fields the
// turn machinery added after their formats shipped, keyed by golden
// path: a prompt's run id, a turn's policy and late steps, an overflow
// attempt's ledger, the accepted receipt of a queued send, a delivered
// receipt marked unanswered, and the audit step that ends a resume.
// Every field is omitted when empty and no status or step replaces an
// old one, so an entry without them is byte-for-byte what it always
// was, and a reader that predates them reads the entry and skips the
// key — no format version moved.
func turnAdditiveSamples() map[string]thread.Entry {
	run := sessionID + "-t2"
	queued := weft.User("and after that, the invoices")
	return map[string]thread.Entry{
		"format1/message_prompt.json": thread.MessageEntry{
			ID: entryID0, Created: at(1),
			Message: weft.User("Where is order 1234?"),
			RunID:   sessionID + "-t1",
		},
		"format1/turn_policy.json": thread.TurnEntry{
			ID: entryID1, ParentID: entryID0, Created: at(2),
			RunID: sessionID + "-t1", StopReason: weft.StopEndTurn,
			Usage: weft.Usage{InputTokens: 410, OutputTokens: 62}, Steps: 2,
			Policy: thread.Interrupt.String(), LateSteps: 1,
		},
		"format1/turn_overflow_attempt.json": thread.TurnEntry{
			ID: entryID1, ParentID: entryID0, Created: at(2),
			RunID: run, Usage: weft.Usage{InputTokens: 180_000, OutputTokens: 40}, Steps: 1,
			Err:    "weft: run failed at step 1: model stream: weft: request exceeds the model's context window",
			Policy: thread.Queue.String(), ReRun: sessionID + "-t3",
		},
		"format1/turn_deadline.json": thread.TurnEntry{
			ID: entryID1, ParentID: entryID0, Created: at(2),
			RunID: run, Err: "weft: run failed at step 0: context deadline exceeded",
			Canceled: true, Policy: thread.Queue.String(),
		},
		"format3/receipt_accepted.json": thread.ReceiptEntry{
			ID: entryID10, ParentID: entryID9, Created: at(11),
			Status: thread.ReceiptAccepted, Msg: &queued,
			RunID: run, Turn: entryID11,
		},
		"format3/receipt_delivered_unanswered.json": thread.ReceiptEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			Receipt: entryID10, Status: thread.ReceiptDelivered,
			RunID: sessionID + "-t1", Unanswered: true,
		},
		"format2/approval_audit_resume_completed.json": thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			Step: thread.StepResume, Outcome: "completed", RunID: run,
		},
		"format2/approval_audit_resume_failed.json": thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			Step: thread.StepResume, Outcome: "failed", RunID: run,
			Detail: "weft: run failed at step 0: model stream: provider unavailable",
		},
	}
}

// TestTurnAdditiveFieldGoldens pins the wire bytes of the fields and
// values the turn machinery added — each under its kind's standing
// version — and their round trip. Regenerate with `go test ./thread
// -update` from the module root.
func TestTurnAdditiveFieldGoldens(t *testing.T) {
	versions := map[string]string{"format1": "", "format2": `"v":2`, "format3": `"v":3`}
	for name, e := range turnAdditiveSamples() {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		format, _, _ := strings.Cut(name, "/")
		if v := versions[format]; v == "" && strings.Contains(string(b), `"v":`) {
			t.Errorf("%s: a format-1 kind gained a version: %s", name, b)
		} else if v != "" && !strings.Contains(string(b), v) {
			t.Errorf("%s: no %s on the wire: %s", name, v, b)
		}
		wefttest.Golden(t, filepath.Join("testdata", filepath.FromSlash(name)), append(b, '\n'))
		got, err := thread.UnmarshalEntry(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, e) {
			t.Errorf("%s: round trip differs:\n got %+v\nwant %+v", name, got, e)
		}
	}
}

// The added fields are absent from the wire when empty: an entry that
// does not use them is the bytes it was before they existed.
func TestTurnAdditiveFieldsOmittedWhenEmpty(t *testing.T) {
	steer := weft.User("x")
	for name, tc := range map[string]struct {
		e    thread.Entry
		keys []string
	}{
		"message": {thread.MessageEntry{ID: entryID0, Created: at(1), Message: weft.User("x")}, []string{"run_id"}},
		"turn": {thread.TurnEntry{ID: entryID1, Created: at(2), RunID: sessionID + "-t1"},
			[]string{"policy", "late_steps", "rerun", "canceled"}},
		"receipt": {thread.ReceiptEntry{ID: entryID10, Created: at(11), Status: thread.ReceiptQueued, Msg: &steer},
			[]string{"unanswered", "turn", "run_id"}},
	} {
		b, err := json.Marshal(tc.e)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(b, &keys); err != nil {
			t.Fatal(err)
		}
		for _, k := range tc.keys {
			if _, ok := keys[k]; ok {
				t.Errorf("%s: empty %q is on the wire: %s", name, k, b)
			}
		}
	}
}
