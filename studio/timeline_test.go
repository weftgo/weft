package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// timelineDB is acme-support's session (the arena's
// thread.support-approvals shape) as stored records, at fixed times: six
// turns, t1 and t3 park issue_refund (denied, then approved), t5 parks
// send_email (approved always, a grant minted), a compaction marker
// under t4, a steer receipt a content-off destination stripped under t2,
// a label under t6, one record of another public id filed under no run, and a
// grant revoke filed under no run (held at Close). s_quiet has a turn
// and no mark (a plain send: thread wrote nothing); s_parked a turn that
// parked a call and no mark (thread's approval.request never arrived);
// s_held has marks and no turn.
func timelineDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := func(d time.Duration) time.Time { return fixtureT0.Add(d) }
	run := func(sid, pub string, turn int, tagged bool, prompt string) []obsdb.Record {
		id := fmt.Sprintf("%s-t%d", sid, turn)
		start := at(time.Duration(turn) * 10 * time.Second)
		attrs := func(kind, ev string, pos int64) map[string]any {
			m := map[string]any{"weft.record": kind, "weft.run.id": id, "weft.session.id": sid, "weft.public_id": pub,
				"weft.turn": int64(turn), "gen_ai.agent.name": "acme-support"}
			if kind == "event" {
				m["weft.event.type"], m["weft.event.pos"] = ev, pos
			} else {
				m["weft.messages.index"] = pos
			}
			if tagged {
				m["arena.batch"] = "b_u122"
			}
			return m
		}
		mk := func(kind, ev string, pos int64, d time.Duration, body string) obsdb.Record {
			return obsdb.Record{Time: start.Add(d), EventName: "weft." + kind, Severity: 9, Body: body, Service: "svc", Attrs: attrs(kind, ev, pos)}
		}
		return []obsdb.Record{
			mk("event", "run_start", 0, 0, `{"type":"run_start","id":"`+id+`","agent":"acme-support"}`),
			mk("messages", "", 0, time.Millisecond, `[{"role":"user","content":[{"type":"text","text":"`+prompt+`"}]}]`),
			mk("event", "run_finish", 1, 2*time.Second, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":10,"output_tokens":3},"steps":1}`),
		}
	}
	thread := func(sid, pub, runID string, d time.Duration, kind, content string, fields map[string]any) obsdb.Record {
		body := map[string]any{"kind": kind, "session": sid, "at": at(d).Format(time.RFC3339Nano)}
		for k, v := range fields {
			body[k] = v
		}
		b, _ := json.Marshal(body)
		return obsdb.Record{Time: at(d), EventName: "weft.thread." + kind, Body: string(b), Severity: 9, Service: "svc",
			Attrs: map[string]any{"weft.record": obsdb.RecordThread, "weft.run.id": runID, "weft.content": content,
				"weft.session.id": sid, "weft.public_id": pub, "gen_ai.agent.name": "acme-support"}}
	}
	const sid, pub = "s_acme", "pub_acme"
	var recs []obsdb.Record
	for turn, p := range []string{"Order A-1002 arrived cracked", "Here is the photo", "resume", "AUTUMN30 coupon", "email me a confirmation", "summarise this case"} {
		recs = append(recs, run(sid, pub, turn+1, turn+1 != 6, p)...)
	}
	recs = append(recs, run("s_quiet", pub, 1, false, "hello")...)
	parked := run("s_parked", pub, 1, false, "refund me")
	parked[2].Body = `{"type":"run_finish","run_id":"s_parked-t1","usage":{},"steps":1,"pending":[{"type":"tool_call","id":"call_1","name":"issue_refund","args":{}}]}`
	recs = append(recs, parked...)
	recs = append(recs,
		thread(sid, pub, sid+"-t1", 11*time.Second, "approval.request", "none", map[string]any{"entry": "e5", "call_id": "r1", "tool": "issue_refund", "args_sha256": "6dfb"}),
		thread(sid, pub, sid+"-t1", 12*time.Second, "approval.decision", "full", map[string]any{"entry": "e7", "call_id": "r1", "outcome": "deny", "via": "user", "request_entry": "e5",
			"content": map[string]any{"who": "agent-7", "reason": "need a photo of the damage"}}),
		thread(sid, pub, sid+"-t2", 21*time.Second, "receipt", "stripped", map[string]any{"entry": "e9", "receipt_id": "e9", "state": "delivered", "input": "steer"}),
		thread(sid, pub, sid+"-t3", 31*time.Second, "approval.request", "none", map[string]any{"entry": "e16", "call_id": "r2", "tool": "issue_refund", "args_sha256": "6dfb"}),
		thread(sid, pub, sid+"-t3", 32*time.Second, "approval.decision", "full", map[string]any{"entry": "e19", "call_id": "r2", "outcome": "approve", "via": "user", "request_entry": "e16",
			"content": map[string]any{"who": "agent-7"}}),
		thread(sid, pub, sid+"-t5", 51*time.Second, "approval.request", "none", map[string]any{"entry": "e29", "call_id": "e1", "tool": "send_email", "args_sha256": "9a1c"}),
		thread(sid, pub, sid+"-t5", 52*time.Second, "approval.decision", "full", map[string]any{"entry": "e31", "call_id": "e1", "outcome": "approve", "via": "user", "always": true, "request_entry": "e29",
			"content": map[string]any{"who": "agent-7"}}),
		thread(sid, pub, sid+"-t5", 53*time.Second, "grant.mint", "none", map[string]any{"entry": "e32", "grant_id": "e32", "tool": "send_email", "scope": "session", "via": "always", "call_id": "e1", "args": 1}),
		thread(sid, pub, sid+"-t6", 62*time.Second, "label", "none", map[string]any{"entry": "e38", "target": "e37", "name": "case-closed"}),
		thread(sid, "pub_else", "", 63*time.Second, "pin", "none", map[string]any{"entry": "e39", "target": "e37"}),
		thread(sid, pub, "", 70*time.Second, "grant.revoke", "none", map[string]any{"entry": "e40", "grant_id": "e32", "tool": "send_email", "uses": 1}),
		thread("s_held", pub, "", 5*time.Second, "grant.mint", "none", map[string]any{"entry": "h1", "grant_id": "h1", "tool": "lookup_order", "scope": "session"}),
		obsdb.Record{Time: at(41 * time.Second), EventName: "weft.compaction", Severity: 9, Service: "svc",
			Body: `{"scope":"session","hash":"sha256:acme","entry":"e24","reason":"threshold","replaced":8,"entries":1,"messages_before":9,"messages_after":2,"tokens_before":900,"tokens_after":300,"summarizer_model":{"provider":"openai","name":"gpt-4o-mini"},"summarizer_usage":{"input_tokens":1800,"output_tokens":60}}`,
			Attrs: map[string]any{"weft.record": obsdb.RecordCompaction, "weft.run.id": sid + "-t4", "weft.compaction.hash": "sha256:acme",
				"weft.compaction.scope": "session", "weft.content": "none", "weft.session.id": sid, "weft.public_id": pub, "gen_ai.agent.name": "acme-support"}},
	)
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	return db
}

type timelineView struct {
	Turns []struct {
		RunID string `json:"run_id"`
		Turn  int    `json:"turn"`
		Marks int    `json:"marks"`
	} `json:"turns"`
	Marks []struct {
		Kind          string         `json:"kind"`
		Lane          string         `json:"lane"`
		RunID         *string        `json:"run_id"`
		Turn          *int           `json:"turn"`
		Entry         string         `json:"entry"`
		Content       map[string]any `json:"content"`
		ContentHidden bool           `json:"content_hidden"`
	} `json:"marks"`
	Lanes []struct {
		Lane  string `json:"lane"`
		Marks int    `json:"marks"`
	} `json:"lanes"`
	MarksTotal int `json:"marks_total"`
	Holes      []struct {
		Hole  string `json:"hole"`
		Count int    `json:"count"`
	} `json:"holes"`
}

func decodeTimeline(t *testing.T, body string) timelineView {
	t.Helper()
	var v timelineView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("timeline: %v: %s", err, body)
	}
	return v
}

func (v timelineView) markLines() []string {
	var out []string
	for _, m := range v.Marks {
		turn := "-"
		if m.Turn != nil {
			turn = fmt.Sprint(*m.Turn)
		}
		out = append(out, fmt.Sprintf("t%s %s %s", turn, m.Kind, m.Lane))
	}
	return out
}

func (v timelineView) hole(h string) (int, bool) {
	for _, x := range v.Holes {
		if x.Hole == h {
			return x.Count, true
		}
	}
	return 0, false
}

// TestSessionTimeline pins GET /api/sessions/{id}/timeline (U12.2, A3):
// the turns in turn order with the marks filed under each, the marks in
// record-time order on their lanes — the runless record last, a
// session-level mark (run_id and turn null) — the lanes' counts, the
// holes; the golden is the shape the web client builds on
// (TestSessionTimelineFromARealRun holds a real thread session to it).
func TestSessionTimeline(t *testing.T) {
	h := Handler(DB(timelineDB(t)))
	code, _, body := get(t, h, "/studio/api/sessions/s_acme/timeline")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d %s", code, body)
	}
	golden(t, "session-timeline.golden.json", body)
	v := decodeTimeline(t, body)
	var turns []string
	for _, tr := range v.Turns {
		turns = append(turns, fmt.Sprintf("%s:%d", tr.RunID, tr.Marks))
	}
	if want := []string{"s_acme-t1:2", "s_acme-t2:1", "s_acme-t3:2", "s_acme-t4:1", "s_acme-t5:3", "s_acme-t6:1"}; !slices.Equal(turns, want) {
		t.Errorf("turns = %v, want %v", turns, want)
	}
	want := []string{
		"t1 approval.request approvals", "t1 approval.decision approvals",
		"t2 receipt steer_queue",
		"t3 approval.request approvals", "t3 approval.decision approvals",
		"t4 compaction compaction",
		"t5 approval.request approvals", "t5 approval.decision approvals", "t5 grant.mint grants",
		"t6 label branch_fork", "t- pin compaction",
		"t- grant.revoke grants",
	}
	if got := v.markLines(); !slices.Equal(got, want) {
		t.Errorf("marks = %q\nwant %q", got, want)
	}
	if last := v.Marks[len(v.Marks)-1]; last.RunID != nil || last.Turn != nil || last.Entry != "e40" {
		t.Errorf("the runless mark = run %v turn %v entry %q, want a session-level mark (null run and turn), entry e40", last.RunID, last.Turn, last.Entry)
	}
	if c := v.Marks[1].Content; c["who"] != "agent-7" || c["reason"] != "need a photo of the damage" {
		t.Errorf("the deny's content = %v, want who and reason (content on, the dev identity)", c)
	}
	var lanes []string
	for _, l := range v.Lanes {
		lanes = append(lanes, fmt.Sprintf("%s:%d", l.Lane, l.Marks))
	}
	if want := []string{"approvals:6", "grants:2", "compaction:2", "steer_queue:1", "branch_fork:1", "pool:0"}; !slices.Equal(lanes, want) {
		t.Errorf("lanes = %v, want %v", lanes, want)
	}
	if n, ok := v.hole("stripped"); !ok || n != 1 || v.MarksTotal != 12 {
		t.Errorf("holes = %+v, marks_total %d; want stripped (1) and 12 marks", v.Holes, v.MarksTotal)
	}

	// entries: the same records, paged.
	code, _, body = get(t, h, "/studio/api/sessions/s_acme/entries?limit=4")
	if code != http.StatusOK {
		t.Fatalf("entries: %d %s", code, body)
	}
	golden(t, "session-entries.golden.json", body)
	var e struct {
		Entries    []json.RawMessage `json:"entries"`
		Total      int               `json:"total"`
		NextOffset *int              `json:"next_offset"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || len(e.Entries) != 4 || e.Total != 12 || e.NextOffset == nil || *e.NextOffset != 4 {
		t.Errorf("entries?limit=4 = %d entries, total %d, next %v (%v); want 4 of 12, next 4", len(e.Entries), e.Total, e.NextOffset, err)
	}
	_, _, body = get(t, h, "/studio/api/sessions/s_acme/entries?offset=10")
	if err := json.Unmarshal([]byte(body), &e); err != nil || len(e.Entries) != 2 || e.NextOffset != nil {
		t.Errorf("entries?offset=10 = %s, want the last 2 and no next", body)
	}
	if code, _, _ := get(t, h, "/studio/api/sessions/s_acme/entries?limit=x"); code != http.StatusBadRequest {
		t.Errorf("entries?limit=x: %d, want 400", code)
	}

	// No mark and no turn that would have written one (a plain send):
	// the timeline is its turns, no hole.
	_, _, body = get(t, h, "/studio/api/sessions/s_quiet/timeline")
	q := decodeTimeline(t, body)
	if len(q.Holes) != 0 || len(q.Turns) != 1 || len(q.Marks) != 0 {
		t.Errorf("s_quiet's timeline = %s, want its turn, no mark and no hole: a plain send writes no thread record", body)
	}
	// No mark although a turn parked a call: not_recorded on that
	// evidence, its fix naming the LoggerProvider.
	_, _, body = get(t, h, "/studio/api/sessions/s_parked/timeline")
	pk := decodeTimeline(t, body)
	if _, ok := pk.hole("not_recorded"); !ok || len(pk.Turns) != 1 || len(pk.Marks) != 0 ||
		!strings.Contains(body, "turn 1 parked a call") || !strings.Contains(body, "LoggerProvider") {
		t.Errorf("s_parked's timeline = %s, want its turn, no mark and the not_recorded hole naming the park and the LoggerProvider", body)
	}
	if _, _, eb := get(t, h, "/studio/api/sessions/s_parked/entries"); !strings.Contains(eb, `"hole":"not_recorded"`) {
		t.Errorf("s_parked's entries = %s, want the not_recorded hole too", eb)
	}
	// Marks and no turn (every record held at Close): the session-level marks.
	code, _, body = get(t, h, "/studio/api/sessions/s_held/timeline")
	if hv := decodeTimeline(t, body); code != http.StatusOK || len(hv.Turns) != 0 || len(hv.Marks) != 1 {
		t.Errorf("s_held's timeline = %d %s, want no turn and its one mark", code, body)
	}
	for _, p := range []string{"/studio/api/sessions/s_none/timeline", "/studio/api/sessions/s_none/entries"} {
		if code, _, _ := get(t, h, p); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}

	// The cap: a truncated hole pointing at entries.
	defer func(n int) { timelineMarkCap = n }(timelineMarkCap)
	timelineMarkCap = 3
	_, _, body = get(t, h, "/studio/api/sessions/s_acme/timeline")
	if tv := decodeTimeline(t, body); len(tv.Marks) != 3 || tv.MarksTotal != 12 || !strings.Contains(body, "entries?offset=3") {
		t.Errorf("capped timeline = %s, want 3 marks of 12 and the truncated hole's offset", body)
	} else if _, ok := tv.hole("truncated"); !ok || !strings.Contains(body, `"hole":"truncated","cause":"response_cap"`) {
		t.Errorf("capped timeline holes = %s, want truncated, cause response_cap", body)
	}
}

// TestSessionTimelineScope: a panel token reads its public id's session
// only; inside it, the marks of another public id are left out under a
// hidden hole, and a read-scoped token reads no content object
// (content_hidden, counted in the same hole); a playground token reads
// the content.
func TestSessionTimelineScope(t *testing.T) {
	h := Handler(DB(timelineDB(t)), Token("srv"))
	tok := func(pub, scope string) string {
		s, err := signPanelToken([]byte("srv"), panelClaims{PublicID: pub, Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	code, _, body := getWith(t, h, "/studio/api/sessions/s_acme/timeline", tok("pub_acme", scopeRead), "")
	if code != http.StatusOK {
		t.Fatalf("read token: %d %s", code, body)
	}
	v := decodeTimeline(t, body)
	if n, ok := v.hole("hidden"); !ok || n != 1+3 || len(v.Marks) != 11 || v.MarksTotal != 11 {
		t.Errorf("read token: %d marks (total %d), holes %+v; want 11, hidden 4 (one foreign mark, three content objects)", len(v.Marks), v.MarksTotal, v.Holes)
	}
	for _, m := range v.Marks {
		if m.Content != nil {
			t.Errorf("read token: a mark's content leaked: %+v", m)
		}
	}
	if !v.Marks[1].ContentHidden || strings.Contains(body, "agent-7") || strings.Contains(body, "need a photo") {
		t.Errorf("read token: the deny is not content_hidden, or who/reason leaked: %s", body)
	}
	_, _, body = getWith(t, h, "/studio/api/sessions/s_acme/timeline", tok("pub_acme", scopePlayground), "")
	if pv := decodeTimeline(t, body); pv.Marks[1].Content["who"] != "agent-7" {
		t.Errorf("playground token: the deny's content = %v, want who", pv.Marks[1].Content)
	} else if n, _ := pv.hole("hidden"); n != 1 {
		t.Errorf("playground token: hidden count %d, want 1 (the foreign mark)", n)
	}
	for _, p := range []string{"/studio/api/sessions/s_acme/timeline", "/studio/api/sessions/s_acme/entries"} {
		if code, _, _ := getWith(t, h, p, tok("pub_other", scopeRead), ""); code != http.StatusForbidden {
			t.Errorf("%s as another public id: %d, want 403", p, code)
		}
	}
	// A session of runless marks alone is, for a token of another public
	// id, the 403 an unknown session gets too: the refusal never says it
	// exists.
	for _, sid := range []string{"s_held", "s_never_stored"} {
		if code, _, _ := getWith(t, h, "/studio/api/sessions/"+sid+"/timeline", tok("pub_other", scopeRead), ""); code != http.StatusForbidden {
			t.Errorf("%s as another public id: %d, want 403", sid, code)
		}
	}
	_, _, body = getWith(t, h, "/studio/api/sessions/s_acme/entries", tok("pub_acme", scopeRead), "")
	if strings.Contains(body, "agent-7") || !strings.Contains(body, `"content_hidden":true`) {
		t.Errorf("read token entries leaked content: %s", body)
	}
	// Both routes count the same way for a panel token: its own marks'
	// total (11 of 12), the foreign one in the hidden hole — /entries'
	// total and offsets over its marks alone, every page.
	var e struct {
		Entries    []json.RawMessage `json:"entries"`
		Total      int               `json:"total"`
		NextOffset *int              `json:"next_offset"`
		Holes      []struct {
			Hole   string `json:"hole"`
			Reason string `json:"reason"`
			Count  int    `json:"count"`
		} `json:"holes"`
	}
	hidden := func() (int, string) {
		for _, x := range e.Holes {
			if x.Hole == "hidden" {
				return x.Count, x.Reason
			}
		}
		return 0, ""
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || len(e.Entries) != 11 || e.Total != 11 || e.NextOffset != nil {
		t.Errorf("read token entries = %d entries, total %d, next %v (%v); want 11 of 11", len(e.Entries), e.Total, e.NextOffset, err)
	} else if n, _ := hidden(); n != 1+3 {
		t.Errorf("read token entries: hidden %d, holes %+v; want 4 like the timeline (one foreign mark, three content objects)", n, e.Holes)
	}
	_, _, body = getWith(t, h, "/studio/api/sessions/s_acme/entries?offset=8&limit=4", tok("pub_acme", scopeRead), "")
	e.NextOffset, e.Holes = nil, nil
	if err := json.Unmarshal([]byte(body), &e); err != nil || len(e.Entries) != 3 || e.Total != 11 || e.NextOffset != nil {
		t.Errorf("read token entries?offset=8 = %s, want the last 3 of 11", body)
	} else if n, why := hidden(); n != 1 || !strings.Contains(why, "1 of this session's marks carry another public id") {
		t.Errorf("read token entries?offset=8 = %s, want the last 3 of 11, the foreign mark still counted", body)
	}
	if strings.Contains(body, `"entry":"e39"`) {
		t.Errorf("read token entries: the foreign mark leaked: %s", body)
	}
	// Past the cap the content count is the marks read: a floor, said so.
	defer func(n int) { timelineMarkCap = n }(timelineMarkCap)
	timelineMarkCap = 3
	_, _, body = getWith(t, h, "/studio/api/sessions/s_acme/timeline", tok("pub_acme", scopeRead), "")
	if cv := decodeTimeline(t, body); cv.MarksTotal != 11 || len(cv.Marks) != 3 {
		t.Errorf("capped read-token timeline = %s, want 3 of 11", body)
	} else if n, _ := cv.hole("hidden"); n != 1+1 || !strings.Contains(body, "a floor: counted over the first 3 marks") {
		t.Errorf("capped read-token timeline's hidden hole = %d in %s, want 2 (the foreign mark, one content object read) said to be a floor", n, body)
	}
}

// TestSessionsTagFilter: /api/sessions?tag.<k>=<v> (D6) lists the
// sessions one of whose own turns carries the pair, every turn of each.
func TestSessionsTagFilter(t *testing.T) {
	h := Handler(DB(timelineDB(t)))
	_, _, body := get(t, h, "/studio/api/sessions?tag.arena.batch=b_u122")
	if !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"id":"s_acme"`) || !strings.Contains(body, `"turns":6`) {
		t.Errorf("sessions?tag.arena.batch=b_u122 = %s, want s_acme alone with its 6 turns", body)
	}
	_, _, body = get(t, h, "/studio/api/sessions?tag.arena.batch=nope")
	if !strings.Contains(body, `"total":0`) {
		t.Errorf("sessions?tag.arena.batch=nope = %s, want none", body)
	}
}

// noEventsDB is a database without obsdb.SessionEventReader.
type noEventsDB struct{ obsdb.DB }

// TestSessionTimelineOff: a database that reads no session events
// leaves the timeline capability off, says why, and the routes are 501.
func TestSessionTimelineOff(t *testing.T) {
	h := Handler(DB(noEventsDB{timelineDB(t)}))
	_, _, meta := get(t, h, "/studio/api/meta")
	if strings.Contains(meta, `"timeline",`) || !strings.Contains(meta, `"timeline":"the database cannot read a session's state log`) {
		t.Errorf("meta without a reader: %s", meta)
	}
	for _, p := range []string{"/studio/api/sessions/s_acme/timeline", "/studio/api/sessions/s_acme/entries"} {
		if code, _, body := get(t, h, p); code != http.StatusNotImplemented || !strings.Contains(body, "SessionEventReader") {
			t.Errorf("%s without a reader: %d %s, want 501 naming the interface", p, code, body)
		}
	}
}
