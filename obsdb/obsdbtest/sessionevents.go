package obsdbtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// sessionEvents (U12.2, D6; record contract §4, §6): the session_events
// index. acme-support's session (the arena's thread.support-approvals
// shape) is six turns, each with its thread records filed under the run
// it concerns — t1 parks issue_refund and is denied, t3 parks it again
// and is approved, t5 parks send_email and is approved always, minting
// a grant — a compaction marker under t4, a label under t6, and one
// record filed under no run (held at Close: weft.run.id "", the session
// id its key). SessionEvents reads them in record-time order with the
// runless one among them, pages by offset, dedups a retried batch and
// answers an unknown session empty; the tag filter (SessionQuery.Meta)
// selects the session by a tag one of its turns carries and lists every
// turn of it; DeleteRun removes a run's events, DeleteSession every
// one, the runless one included.
func sessionEvents(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		er, ok := db.(obsdb.SessionEventReader)
		if !ok {
			t.Skip("the backend reads no session events (obsdb.SessionEventReader)")
		}
		const sid = "s_acme"
		ident := func(turn int, tagged bool) map[string]any {
			m := map[string]any{"weft.session.id": sid, "weft.public_id": "pub_acme", "weft.turn": int64(turn), "gen_ai.agent.name": "acme-support"}
			if tagged {
				m["arena.batch"] = "b_u122"
			}
			return m
		}
		var recs []obsdb.Record
		for turn := 1; turn <= 6; turn++ {
			id := fmt.Sprintf("%s-t%d", sid, turn)
			shift := time.Duration(turn) * 10 * time.Second
			for _, r := range []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, ident(turn, turn != 6)),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","steps":1}`, ident(turn, turn != 6)),
			} {
				r.Time = r.Time.Add(shift)
				recs = append(recs, r)
			}
		}
		thread := func(runID string, d time.Duration, kind, content string, fields map[string]any) obsdb.Record {
			body := map[string]any{"kind": kind, "session": sid, "at": at(d).Format(time.RFC3339Nano)}
			for k, v := range fields {
				body[k] = v
			}
			b, _ := json.Marshal(body)
			attrs := map[string]any{"weft.record": obsdb.RecordThread, "weft.run.id": runID, "weft.content": content,
				"weft.session.id": sid, "weft.public_id": "pub_acme", "gen_ai.agent.name": "acme-support"}
			return obsdb.Record{Time: at(d), EventName: "weft.thread." + kind, Body: string(b), Attrs: attrs, Service: "conf-svc", Severity: 9}
		}
		marks := []obsdb.Record{
			thread(sid+"-t1", 11*time.Second, "approval.request", "none", map[string]any{"entry": "e5", "call_id": "r1", "tool": "issue_refund"}),
			thread(sid+"-t1", 12*time.Second, "approval.decision", "full", map[string]any{"entry": "e7", "call_id": "r1", "outcome": "deny", "via": "user",
				"content": map[string]any{"who": "agent-7", "reason": "need a photo of the damage"}}),
			thread(sid+"-t3", 31*time.Second, "approval.request", "none", map[string]any{"entry": "e16", "call_id": "r2", "tool": "issue_refund"}),
			thread(sid+"-t3", 32*time.Second, "approval.decision", "full", map[string]any{"entry": "e19", "call_id": "r2", "outcome": "approve", "via": "user",
				"content": map[string]any{"who": "agent-7"}}),
			thread(sid+"-t5", 51*time.Second, "approval.request", "none", map[string]any{"entry": "e29", "call_id": "e1", "tool": "send_email"}),
			thread(sid+"-t5", 52*time.Second, "approval.decision", "full", map[string]any{"entry": "e31", "call_id": "e1", "outcome": "approve", "via": "user", "always": true,
				"content": map[string]any{"who": "agent-7"}}),
			thread(sid+"-t5", 53*time.Second, "grant.mint", "none", map[string]any{"entry": "e32", "grant_id": "e32", "tool": "send_email", "scope": "session", "via": "always"}),
			thread(sid+"-t6", 62*time.Second, "label", "none", map[string]any{"entry": "e38", "target": "e37", "name": "case-closed"}),
			thread("", 70*time.Second, "grant.revoke", "none", map[string]any{"entry": "e40", "grant_id": "e32", "tool": "send_email"}),
		}
		marker := obsdb.Record{Time: at(41 * time.Second), EventName: "weft.compaction",
			Body: `{"scope":"session","hash":"sha256:acme","entry":"e24","reason":"threshold","replaced":8,"entries":1,"messages_before":9,"messages_after":2,"tokens_before":900,"tokens_after":300}`,
			Attrs: map[string]any{"weft.record": obsdb.RecordCompaction, "weft.run.id": sid + "-t4", "weft.compaction.hash": "sha256:acme",
				"weft.compaction.scope": "session", "weft.content": "none", "weft.session.id": sid, "weft.public_id": "pub_acme"},
			Service: "conf-svc", Severity: 9}
		other := thread("s_other-t1", 15*time.Second, "label", "none", map[string]any{"entry": "x1", "target": "x0", "name": "elsewhere"})
		other.Attrs["weft.session.id"] = "s_other"
		all := append(append(append(recs, marks...), marker), other)
		if err := db.Write(ctx(), obsdb.Batch{Records: all}); err != nil {
			t.Fatal(err)
		}
		// A retried batch is a no-op (the index's key is the record's).
		if err := db.Write(ctx(), obsdb.Batch{Records: append(append([]obsdb.Record{}, marks...), marker)}); err != nil {
			t.Fatal(err)
		}

		line := func(e obsdb.SessionEvent) string {
			return e.RunID + " " + obsdb.SessionEventKind(e)
		}
		want := []string{
			"s_acme-t1 approval.request", "s_acme-t1 approval.decision",
			"s_acme-t3 approval.request", "s_acme-t3 approval.decision",
			"s_acme-t4 compaction",
			"s_acme-t5 approval.request", "s_acme-t5 approval.decision", "s_acme-t5 grant.mint",
			"s_acme-t6 label",
			" grant.revoke",
		}
		page, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: sid})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range page.Events {
			got = append(got, line(e))
		}
		if !slices.Equal(got, want) || page.Total != len(want) {
			t.Fatalf("SessionEvents(%s) = %q (total %d), want %q in record-time order, the runless record among them", sid, got, page.Total, want)
		}
		deny := page.Events[1]
		if deny.Kind != obsdb.RecordThread || deny.Name != "weft.thread.approval.decision" || deny.Content != "full" ||
			deny.PublicID != "pub_acme" || deny.SessionID != sid || !deny.Time.Equal(at(12*time.Second)) {
			t.Errorf("the deny's event = %+v, want kind thread, its event name, content full, pub_acme at +12s", deny)
		}
		var body map[string]any
		if err := json.Unmarshal(deny.Body, &body); err != nil || body["outcome"] != "deny" {
			t.Errorf("the deny's body = %s (%v), want the record's body verbatim", deny.Body, err)
		}
		if m := page.Events[4]; m.Kind != obsdb.RecordCompaction || m.Name != "weft.compaction" {
			t.Errorf("the marker's event = %+v, want kind compaction, weft.compaction", m)
		}
		// Paging by offset; an unknown session is empty.
		p2, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: sid, Offset: 8, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(p2.Events) != 2 || line(p2.Events[0]) != want[8] || p2.Total != len(want) {
			t.Errorf("SessionEvents offset 8 = %d events (total %d), want the last two", len(p2.Events), p2.Total)
		}
		if none, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: "s_none"}); err != nil || len(none.Events) != 0 || none.Total != 0 {
			t.Errorf("SessionEvents(s_none) = %+v, %v; want empty, no error", none, err)
		}
		// RunIDs: the named runs' events alone — Total and offsets count
		// them alone, the runless record is under none, an unknown id
		// selects nothing (Studio's decisions read t1 + t2, U13.2).
		turns := func(q obsdb.SessionEventQuery) ([]string, int) {
			t.Helper()
			p, err := er.SessionEvents(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, e := range p.Events {
				out = append(out, line(e))
			}
			return out, p.Total
		}
		if got, n := turns(obsdb.SessionEventQuery{SessionID: sid, RunIDs: []string{sid + "-t1", sid + "-t2", sid + "-t5"}}); !slices.Equal(got, []string{
			"s_acme-t1 approval.request", "s_acme-t1 approval.decision",
			"s_acme-t5 approval.request", "s_acme-t5 approval.decision", "s_acme-t5 grant.mint",
		}) || n != 5 {
			t.Errorf("SessionEvents(RunIDs t1, t2, t5) = %q (total %d), want t1's and t5's five", got, n)
		}
		if got, n := turns(obsdb.SessionEventQuery{SessionID: sid, RunIDs: []string{sid + "-t5"}, Offset: 1, Limit: 1}); !slices.Equal(got, []string{"s_acme-t5 approval.decision"}) || n != 3 {
			t.Errorf("SessionEvents(RunIDs t5, offset 1, limit 1) = %q (total %d), want its decision of 3", got, n)
		}
		if got, n := turns(obsdb.SessionEventQuery{SessionID: sid, RunIDs: []string{"s_nope-t1"}}); len(got) != 0 || n != 0 {
			t.Errorf("SessionEvents(RunIDs unknown) = %q (total %d), want none", got, n)
		}

		// The tag filter selects sessions, never turns: t6 carries no tag,
		// the session still lists its six turns.
		tagged, err := db.Sessions(ctx(), obsdb.SessionQuery{Meta: map[string]string{"arena.batch": "b_u122"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(tagged.Sessions) != 1 || tagged.Sessions[0].ID != sid || tagged.Sessions[0].Turns != 6 || tagged.Total != 1 {
			t.Errorf("Sessions(tag arena.batch=b_u122) = %+v (total %d), want s_acme with its 6 turns", tagged.Sessions, tagged.Total)
		}
		for _, q := range []obsdb.SessionQuery{
			{Meta: map[string]string{"arena.batch": "b_other"}},
			{Meta: map[string]string{"arena.batch": "b_u122", "tenant": "nobody"}},
			{Meta: map[string]string{"arena.batch": "b_u122"}, PublicID: "pub_else"},
		} {
			p, err := db.Sessions(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Sessions) != 0 || p.Total != 0 {
				t.Errorf("Sessions(%+v) = %+v, want none", q, p.Sessions)
			}
		}
		if p, err := db.Sessions(ctx(), obsdb.SessionQuery{Meta: map[string]string{"arena.batch": "b_u122"}, PublicID: "pub_acme"}); err != nil || len(p.Sessions) != 1 {
			t.Errorf("Sessions(tag, pub_acme) = %+v, %v; want s_acme", p.Sessions, err)
		}

		// DeleteRun takes a run's events; DeleteSession the rest, the
		// runless one included.
		if err := db.DeleteRun(ctx(), sid+"-t1"); err != nil {
			t.Fatal(err)
		}
		after, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: sid})
		if err != nil {
			t.Fatal(err)
		}
		if after.Total != len(want)-2 || line(after.Events[0]) != want[2] {
			t.Errorf("after DeleteRun(t1): %d events, first %v; want %d, t3's request first", after.Total, after.Events, len(want)-2)
		}
		if err := db.DeleteSession(ctx(), sid); err != nil {
			t.Fatal(err)
		}
		gone, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: sid})
		if err != nil {
			t.Fatal(err)
		}
		if gone.Total != 0 || len(gone.Events) != 0 {
			t.Errorf("after DeleteSession: %d events (%v), want none — the runless record included", gone.Total, gone.Events)
		}
		if err := db.DeleteSession(ctx(), sid); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("DeleteSession again = %v, want ErrNotFound", err)
		}
		if o, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: "s_other"}); err != nil || o.Total != 1 {
			t.Errorf("s_other's events after deleting s_acme = %+v, %v; want its one label", o, err)
		}

		// Ties: records of one time, run and kind read in body order on
		// every backend, whatever order they were written in; PublicID
		// keeps one public id's events, Total and offsets counting them
		// alone.
		const tie = "s_tie"
		tied := func(runID, pub, entry string) obsdb.Record {
			r := thread(runID, 90*time.Second, "label", "none", map[string]any{"entry": entry, "target": "e0", "name": "n"})
			r.Body = `{"kind":"label","session":"s_tie","at":"` + at(90*time.Second).Format(time.RFC3339Nano) + `","entry":"` + entry + `"}`
			r.Attrs["weft.session.id"], r.Attrs["weft.public_id"] = tie, pub
			return r
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			tied("s_tie-t1", "pub_b", "e3"), tied("s_tie-t1", "pub_a", "e1"), tied("", "pub_a", "e9"),
			tied("s_tie-t1", "pub_a", "e2"), tied("s_tie-t0", "pub_b", "e8"),
		}}); err != nil {
			t.Fatal(err)
		}
		entries := func(q obsdb.SessionEventQuery) ([]string, int) {
			t.Helper()
			p, err := er.SessionEvents(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, e := range p.Events {
				var b struct {
					Entry string `json:"entry"`
				}
				_ = json.Unmarshal(e.Body, &b)
				out = append(out, e.RunID+":"+b.Entry)
			}
			return out, p.Total
		}
		if got, n := entries(obsdb.SessionEventQuery{SessionID: tie}); !slices.Equal(got, []string{":e9", "s_tie-t0:e8", "s_tie-t1:e1", "s_tie-t1:e2", "s_tie-t1:e3"}) || n != 5 {
			t.Errorf("tied events = %q (total %d), want (time, run, kind, body) order: the runless one, t0, then t1's by body", got, n)
		}
		if got, n := entries(obsdb.SessionEventQuery{SessionID: tie, PublicID: "pub_a", Offset: 1}); !slices.Equal(got, []string{"s_tie-t1:e1", "s_tie-t1:e2"}) || n != 3 {
			t.Errorf("pub_a's tied events from offset 1 = %q (total %d), want e1, e2 of its 3", got, n)
		}

		// Sweep: a runless event goes once its session has no run left
		// and it is older than the cutoff; a session with a running turn,
		// or an event past the cutoff, keeps it.
		now := time.Now().UTC()
		sw := func(sid, runID string, tm time.Time, entry string) obsdb.Record {
			r := thread(runID, 0, "grant.revoke", "none", map[string]any{"entry": entry})
			r.Time = tm
			r.Body = `{"kind":"grant.revoke","session":"` + sid + `","entry":"` + entry + `"}`
			r.Attrs["weft.session.id"] = sid
			return r
		}
		swIdent := func(sid string) map[string]any {
			return map[string]any{"weft.session.id": sid, "weft.turn": int64(1), "gen_ai.agent.name": "acme-support"}
		}
		done := record("s_sw-t1", "event", "run_start", 0, `{"type":"run_start","id":"s_sw-t1"}`, swIdent("s_sw"))
		fin := record("s_sw-t1", "event", "run_finish", 1, `{"type":"run_finish","run_id":"s_sw-t1","steps":1}`, swIdent("s_sw"))
		live := record("s_keep-t1", "event", "run_start", 0, `{"type":"run_start","id":"s_keep-t1"}`, swIdent("s_keep"))
		done.Time, fin.Time, live.Time = now.Add(-time.Minute), now.Add(-time.Minute), now
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			done, fin, live,
			sw("s_sw", "s_sw-t1", now.Add(-time.Minute), "w1"),
			sw("s_sw", "", now.Add(-time.Minute), "w2"),
			sw("s_keep", "", now.Add(-time.Minute), "k1"),
			sw("s_young", "", now.Add(3*time.Hour), "y1"),
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Sweep(ctx(), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		for sid, want := range map[string]int{"s_sw": 0, "s_keep": 1, "s_young": 1} {
			if _, n := entries(obsdb.SessionEventQuery{SessionID: sid}); n != want {
				t.Errorf("after Sweep: %s has %d events, want %d (s_sw's runless one goes with its last run; a session with a running turn, or an event past the cutoff, keeps it)", sid, n, want)
			}
		}
	}
}
