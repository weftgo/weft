package obsdbtest

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The deletion fixture (plan A7): delRun is one run with everything a
// deletion must take — its records (event, messages with a searchable
// prompt, a tool_start naming a tool), its invoke_agent span (once it
// ended), a non-weft span below it and two app log lines, one under
// each span. A subagent child shares its parent's trace, its invoke
// span under the parent's.
type delRun struct {
	id, session, parent string
	t                   time.Time
	end                 string // "ok", "fail", "" (no end: running or interrupted, by t)
}

func hex16(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

// trace is the run's trace: the root's, so children share it.
func (f delRun) trace(root string) string { return hex16(root+"/a") + hex16(root+"/b") }

func (f delRun) invokeID() string { return hex16(f.id + "/invoke") }
func (f delRun) appID() string    { return hex16(f.id + "/app") }

func (f delRun) batch(root string) obsdb.Batch {
	trace := f.trace(root)
	rec := func(kind, typ string, pos int64, body string, extra map[string]any) obsdb.Record {
		r := record(f.id, kind, typ, pos, body, extra)
		r.Time = f.t.Add(time.Duration(pos) * time.Millisecond)
		r.TraceID, r.SpanID = trace, f.invokeID()
		return r
	}
	start := map[string]any{"weft.session.id": f.session}
	if f.parent != "" {
		start["weft.parent.run.id"] = f.parent
		start["weft.parent.call.id"] = "call_1"
	}
	recs := []obsdb.Record{
		rec("event", "run_start", 0, `{"type":"run_start","id":"`+f.id+`","agent":"conf"}`, start),
		rec("messages", "", 0, `[{"role":"user","content":[{"type":"text","text":"zebracrossing `+f.id+`"}]}]`,
			map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
		rec("event", "tool_start", 1, `{"type":"tool_start","run_id":"`+f.id+`","seq":1,"call_id":"c1","name":"del_tool","args":{}}`, nil),
	}
	if f.end == "ok" {
		recs = append(recs, rec("event", "run_finish", 2, `{"type":"run_finish","run_id":"`+f.id+`","steps":1}`, nil))
	}
	b := obsdb.Batch{Records: recs}
	if f.end == "" {
		return b
	}
	status := 1
	if f.end == "fail" {
		status = 2
	}
	parentSpan := ""
	if f.parent != "" {
		parentSpan = delRun{id: f.parent}.invokeID()
	}
	attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": f.id, "gen_ai.agent.name": "conf",
		"weft.session.id": f.session}
	if f.parent != "" {
		attrs["weft.parent.run.id"] = f.parent
	}
	b.Spans = []obsdb.Span{
		{TraceID: trace, SpanID: f.invokeID(), ParentSpanID: parentSpan, Name: "invoke_agent conf", Kind: 1,
			Start: f.t, End: f.t.Add(time.Second), StatusCode: status, Service: "conf-svc", Attrs: attrs,
			Resource: map[string]any{"service.name": "conf-svc"}},
		{TraceID: trace, SpanID: f.appID(), ParentSpanID: f.invokeID(), Name: "db query", Kind: 1,
			Start: f.t.Add(100 * time.Millisecond), End: f.t.Add(200 * time.Millisecond), StatusCode: 0, Service: "conf-svc",
			Attrs: map[string]any{"db.system": "postgresql"}, Resource: map[string]any{"service.name": "conf-svc"}},
	}
	for _, sp := range []string{f.invokeID(), f.appID()} {
		b.Records = append(b.Records, obsdb.Record{
			Time: f.t.Add(150 * time.Millisecond), TraceID: trace, SpanID: sp, Severity: 9,
			Body: "app line of " + f.id, Service: "conf-svc", Resource: map[string]any{"service.name": "conf-svc"},
		})
	}
	return b
}

func writeDel(t *testing.T, db obsdb.DB, root string, runs ...delRun) {
	t.Helper()
	for _, r := range runs {
		if err := db.Write(ctx(), r.batch(root)); err != nil {
			t.Fatal(err)
		}
	}
}

// gone asserts every read of a run answers ErrNotFound and its trace
// holds none of its spans.
func gone(t *testing.T, db obsdb.DB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := db.Run(ctx(), id); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Run(%s) after delete = %v, want ErrNotFound", id, err)
		}
		if _, err := db.Events(ctx(), id, -1, 10); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Events(%s) after delete = %v, want ErrNotFound", id, err)
		}
		if spans, err := db.RunSpans(ctx(), id); err == nil && len(spans) > 0 {
			t.Errorf("RunSpans(%s) after delete = %d spans", id, len(spans))
		}
		if b, err := db.TranscriptBatches(ctx(), id); err == nil && len(b) > 0 {
			t.Errorf("TranscriptBatches(%s) after delete = %d batches", id, len(b))
		}
	}
}

// kept asserts a run is whole: its row, events, spans and app logs.
func kept(t *testing.T, db obsdb.DB, r delRun) {
	t.Helper()
	if _, err := db.Run(ctx(), r.id); err != nil {
		t.Errorf("Run(%s) = %v, want kept", r.id, err)
		return
	}
	ev, err := db.Events(ctx(), r.id, -1, 10)
	if err != nil || len(ev.Events) == 0 {
		t.Errorf("Events(%s) = %d, %v; want kept", r.id, len(ev.Events), err)
	}
	if r.end == "" {
		return
	}
	spans, err := db.RunSpans(ctx(), r.id)
	if err != nil || len(spans) != 1 {
		t.Errorf("RunSpans(%s) = %d, %v; want its invoke span", r.id, len(spans), err)
	}
	logs, err := db.OtherLogs(ctx(), r.id, obsdb.LogQuery{})
	if err != nil || len(logs.Logs) != 2 {
		t.Errorf("OtherLogs(%s) = %d, %v; want its 2 lines", r.id, len(logs.Logs), err)
	}
}

// alive writes a heartbeat stamped now for each run: it reads running
// for the next obsdb.InterruptedAfter.
func alive(t *testing.T, db obsdb.DB, ids ...string) {
	t.Helper()
	var b obsdb.Batch
	for _, id := range ids {
		hb := record(id, "heartbeat", "", 0, "", nil)
		hb.Time = time.Now().UTC()
		b.Records = append(b.Records, hb)
	}
	if err := db.Write(ctx(), b); err != nil {
		t.Fatal(err)
	}
}

func allIDs(t *testing.T, db obsdb.DB) []string {
	t.Helper()
	page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	ids := idsOf(page.Runs)
	sort.Strings(ids)
	return ids
}

func searchIDs(t *testing.T, db obsdb.DB) []string {
	t.Helper()
	s, ok := db.(obsdb.Searcher)
	if !ok {
		return nil
	}
	ids := []string{}
	for _, q := range []obsdb.SearchQuery{
		{RunQuery: obsdb.RunQuery{ParentRunID: "*"}, Text: "zebracrossing"},
		{RunQuery: obsdb.RunQuery{ParentRunID: "*"}, Tool: "del_tool"},
	} {
		page, err := s.Search(ctx(), q)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, idsOf(page.Runs)...)
	}
	sort.Strings(ids)
	return ids
}

// deleteRun: DeleteRun takes the run, its children and grandchildren —
// rows, records, search index, spans (the non-weft span below each
// invoke span included) and the app log lines under them — and leaves
// every other run whole; deleting a child leaves its parent.
func deleteRun(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		t0 := time.Now().UTC().Add(-time.Hour)
		parent := delRun{id: "del_p", session: "s_del", t: t0, end: "ok"}
		kid := delRun{id: "del_p/0/c1", session: "s_del", parent: "del_p", t: t0.Add(10 * time.Millisecond), end: "ok"}
		grand := delRun{id: "del_p/0/c1/0/c2", session: "s_del", parent: "del_p/0/c1", t: t0.Add(20 * time.Millisecond), end: "fail"}
		other := delRun{id: "del_other", session: "s_keep", t: t0, end: "ok"}
		writeDel(t, db, "del_p", parent, kid, grand)
		writeDel(t, db, "del_other", other)
		trace := parent.trace("del_p")
		if spans, _ := db.Trace(ctx(), trace); len(spans) != 6 {
			t.Fatalf("fixture trace holds %d spans, want 6", len(spans))
		}
		if _, ok := db.(obsdb.Searcher); ok {
			if got := searchIDs(t, db); len(got) != 8 {
				t.Fatalf("fixture search = %v, want each of 4 runs twice", got)
			}
		}

		if err := db.DeleteRun(ctx(), "del_p/0/c1/0/c2"); err != nil {
			t.Fatal(err)
		}
		gone(t, db, grand.id)
		kept(t, db, kid)
		det, err := db.Run(ctx(), kid.id)
		if err != nil || len(det.Children) != 0 {
			t.Errorf("the kid's children after deleting the grandchild = %+v, %v", det.Children, err)
		}

		if err := db.DeleteRun(ctx(), "del_p"); err != nil {
			t.Fatal(err)
		}
		gone(t, db, parent.id, kid.id)
		if spans, err := db.Trace(ctx(), trace); err != nil || len(spans) != 0 {
			t.Errorf("Trace after delete = %d spans, %v; want none (the non-weft spans below went too)", len(spans), err)
		}
		if got := allIDs(t, db); len(got) != 1 || got[0] != "del_other" {
			t.Errorf("runs after delete = %v, want [del_other]", got)
		}
		if _, ok := db.(obsdb.Searcher); ok {
			if got := searchIDs(t, db); fmt.Sprint(got) != "[del_other del_other]" {
				t.Errorf("search after delete = %v, want del_other alone", got)
			}
		}
		kept(t, db, other)
		if _, err := db.Session(ctx(), "s_del"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Session(s_del) after its runs went = %v, want ErrNotFound", err)
		}

		if err := db.DeleteRun(ctx(), "del_p"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("DeleteRun of a deleted run = %v, want ErrNotFound", err)
		}
		if err := db.DeleteRun(ctx(), "no_such_run"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("DeleteRun(unknown) = %v, want ErrNotFound", err)
		}
		// A deleted run written again starts a fresh row.
		writeDel(t, db, "del_p", parent)
		kept(t, db, parent)
	}
}

// deleteSession: every run of the session goes, its children with
// them; another session's runs stay; an unknown id is ErrNotFound.
func deleteSession(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		t0 := time.Now().UTC().Add(-time.Hour)
		t1 := delRun{id: "ds_t1", session: "s_gone", t: t0, end: "ok"}
		t1kid := delRun{id: "ds_t1/0/c1", session: "", parent: "ds_t1", t: t0.Add(time.Millisecond), end: "ok"}
		t2 := delRun{id: "ds_t2", session: "s_gone", t: t0.Add(time.Minute), end: "fail"}
		keep := delRun{id: "ds_keep", session: "s_stays", t: t0, end: "ok"}
		writeDel(t, db, "ds_t1", t1, t1kid)
		writeDel(t, db, "ds_t2", t2)
		writeDel(t, db, "ds_keep", keep)
		if err := db.DeleteSession(ctx(), "s_gone"); err != nil {
			t.Fatal(err)
		}
		gone(t, db, t1.id, t1kid.id, t2.id)
		kept(t, db, keep)
		if _, err := db.Session(ctx(), "s_gone"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Session after DeleteSession = %v, want ErrNotFound", err)
		}
		if _, err := db.Session(ctx(), "s_stays"); err != nil {
			t.Errorf("the other session = %v", err)
		}
		for _, id := range []string{"s_gone", "no_such_session", ""} {
			if err := db.DeleteSession(ctx(), id); !errors.Is(err, obsdb.ErrNotFound) {
				t.Errorf("DeleteSession(%q) = %v, want ErrNotFound", id, err)
			}
		}
	}
}

// sweep: runs not running whose LastSeen is before the cutoff go with
// their not-running children; a running run never does, whatever the
// cutoff — nor does a running child of a swept parent.
func sweep(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		old := time.Now().UTC().Add(-2 * time.Hour)
		now := time.Now().UTC()
		oldOK := delRun{id: "sw_ok", session: "s_sw", t: old, end: "ok"}
		oldOKKid := delRun{id: "sw_ok/0/c1", parent: "sw_ok", t: old.Add(time.Millisecond), end: "ok"}
		oldFail := delRun{id: "sw_fail", session: "s_sw", t: old, end: "fail"}
		oldInterrupted := delRun{id: "sw_int", session: "s_sw", t: old}
		oldParent := delRun{id: "sw_parent", t: old, end: "ok"}
		runningKid := delRun{id: "sw_parent/0/c1", parent: "sw_parent", t: now}
		recentOK := delRun{id: "sw_recent", t: now, end: "ok"}
		running := delRun{id: "sw_running", t: now}
		writeDel(t, db, "sw_ok", oldOK, oldOKKid)
		writeDel(t, db, "sw_fail", oldFail)
		writeDel(t, db, "sw_int", oldInterrupted)
		writeDel(t, db, "sw_parent", oldParent, runningKid)
		writeDel(t, db, "sw_recent", recentOK)
		writeDel(t, db, "sw_running", running)

		n, err := db.Sweep(ctx(), now.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 5 {
			t.Errorf("Sweep deleted %d runs, want 5 (sw_ok, its child, sw_fail, sw_int, sw_parent)", n)
		}
		gone(t, db, oldOK.id, oldOKKid.id, oldFail.id, oldInterrupted.id, oldParent.id)
		kept(t, db, runningKid)
		kept(t, db, recentOK)
		kept(t, db, running)

		// A cutoff in the future takes every run that is not running.
		// The two running runs heartbeat first, as live runs do: a run
		// silent past obsdb.InterruptedAfter is interrupted and would go
		// too, were this test slower than that.
		alive(t, db, runningKid.id, running.id)
		n, err = db.Sweep(ctx(), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("the second sweep deleted %d runs, want 1 (sw_recent)", n)
		}
		if got := allIDs(t, db); fmt.Sprint(got) != "[sw_parent/0/c1 sw_running]" {
			t.Errorf("runs after the sweeps = %v, want the two running runs", got)
		}
		alive(t, db, runningKid.id, running.id)
		if n, err := db.Sweep(ctx(), time.Now().Add(time.Hour)); err != nil || n != 0 {
			t.Errorf("a sweep with nothing to take = %d, %v", n, err)
		}
	}
}

// sweepBatched: a backlog of more roots than one pass takes
// (obsdb.SweepBatch) is swept whole — every root and its child, the
// count over every pass — and a running run among them stays. The
// cutoff sits between the backlog (2 h old) and the running run
// (started now), so the run stays whether the sweep reads it running
// or, the write and the sweep having taken longer than
// obsdb.InterruptedAfter on a slow machine, interrupted.
func sweepBatched(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		now := time.Now().UTC()
		old := now.Add(-2 * time.Hour)
		const roots = obsdb.SweepBatch + 37
		var b obsdb.Batch
		for i := range roots {
			id := fmt.Sprintf("swb_%04d", i)
			for _, r := range []delRun{
				{id: id, t: old, end: "ok"},
				{id: id + "/0/c1", parent: id, t: old.Add(time.Millisecond), end: "ok"},
			} {
				rb := r.batch(id)
				b.Records = append(b.Records, rb.Records...)
				b.Spans = append(b.Spans, rb.Spans...)
			}
		}
		running := delRun{id: "swb_running", t: now}
		rb := running.batch(running.id)
		b.Records = append(b.Records, rb.Records...)
		if err := db.Write(ctx(), b); err != nil {
			t.Fatal(err)
		}
		n, err := db.Sweep(ctx(), now.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 2*roots {
			t.Errorf("Sweep deleted %d runs, want %d (%d roots, each with its child, over more than one pass)", n, 2*roots, roots)
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*", Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		if got := idsOf(page.Runs); fmt.Sprint(got) != "[swb_running]" {
			t.Errorf("runs after a batched sweep = %v, want the running run alone", got)
		}
		gone(t, db, "swb_0000", "swb_0000/0/c1", fmt.Sprintf("swb_%04d", roots-1))
	}
}

// vacuum: Vacuum succeeds after deletions and leaves what was kept
// readable.
func vacuum(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		t0 := time.Now().UTC().Add(-time.Hour)
		a := delRun{id: "vac_a", session: "s_vac", t: t0, end: "ok"}
		b := delRun{id: "vac_b", session: "s_vac", t: t0, end: "ok"}
		writeDel(t, db, "vac_a", a)
		writeDel(t, db, "vac_b", b)
		if err := db.DeleteRun(ctx(), "vac_a"); err != nil {
			t.Fatal(err)
		}
		if err := db.Vacuum(ctx()); err != nil {
			t.Fatal(err)
		}
		gone(t, db, a.id)
		kept(t, db, b)
		if err := db.Vacuum(ctx()); err != nil {
			t.Errorf("a second Vacuum = %v", err)
		}
	}
}

// lastSeenAfter: RunQuery.LastSeenAfter keeps the runs last seen at or
// after it — a run started long ago and heartbeated now included,
// children too under ParentRunID "*" — through Runs and, where the
// backend searches, Search.
func lastSeenAfter(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		now := time.Now().UTC()
		old := now.Add(-3 * time.Hour)
		writeDel(t, db, "ls_old", delRun{id: "ls_old", t: old, end: "ok"})
		writeDel(t, db, "ls_old_int", delRun{id: "ls_old_int", t: old})
		writeDel(t, db, "ls_new", delRun{id: "ls_new", t: now.Add(-10 * time.Minute), end: "ok"},
			delRun{id: "ls_new/0/c1", parent: "ls_new", t: now.Add(-10 * time.Minute), end: "ok"})
		writeDel(t, db, "ls_alive", delRun{id: "ls_alive", t: old})
		hb := record("ls_alive", "heartbeat", "", 0, "", nil)
		hb.Time = now
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{hb}}); err != nil {
			t.Fatal(err)
		}
		q := obsdb.RunQuery{ParentRunID: "*", LastSeenAfter: now.Add(-time.Hour), Limit: 500}
		page, err := db.Runs(ctx(), q)
		if err != nil {
			t.Fatal(err)
		}
		ids := idsOf(page.Runs)
		sort.Strings(ids)
		if fmt.Sprint(ids) != "[ls_alive ls_new ls_new/0/c1]" {
			t.Errorf("Runs(LastSeenAfter 1h ago) = %v, want the recent run, its child and the heartbeated old one", ids)
		}
		if page.Total != 3 {
			t.Errorf("Total = %d, want 3", page.Total)
		}
		if got := allIDs(t, db); len(got) != 5 {
			t.Errorf("unbounded Runs = %v, want all five", got)
		}
		if s, ok := db.(obsdb.Searcher); ok {
			sp, err := s.Search(ctx(), obsdb.SearchQuery{RunQuery: q, Text: "zebracrossing"})
			if err != nil {
				t.Fatal(err)
			}
			got := idsOf(sp.Runs)
			sort.Strings(got)
			if fmt.Sprint(got) != "[ls_alive ls_new ls_new/0/c1]" {
				t.Errorf("Search(LastSeenAfter 1h ago) = %v", got)
			}
		}
	}
}
