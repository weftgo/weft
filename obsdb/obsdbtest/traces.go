package obsdbtest

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The Traces fixture: four traces, one second-resolution start each.
//
//   - trA (at 0): run tr_parent's invoke_agent root, its chat and
//     execute_tool spans, and the subagent child tr_parent/0/call_1's
//     invoke_agent span under the tool span — one trace, two runs; the
//     child's span ended in error. Every span carries pub_conf.
//   - trB (at 10s): a non-weft root ("POST /chat", service edge-svc,
//     no weft attribute — what an OTLP-instrumented HTTP server sends)
//     with run tr_web's invoke_agent span under it (pub_conf).
//   - trC (at 20s): two spans whose parent never arrived — no root —
//     service orphan-svc, one ended in error.
//   - trD (at 30s): run tr_other, public id pub_other.
const (
	trA = "a1000000000000000000000000000000"
	trB = "b2000000000000000000000000000000"
	trC = "c3000000000000000000000000000000"
	trD = "d4000000000000000000000000000000"
)

func traceSpan(trace, span, parent, name, service string, start, end time.Duration, status int, attrs map[string]any) obsdb.Span {
	return obsdb.Span{
		TraceID: trace, SpanID: span, ParentSpanID: parent, Name: name, Kind: 1,
		Start: at(start), End: at(end), StatusCode: status, Service: service, Attrs: attrs,
		Resource: map[string]any{"service.name": service},
	}
}

func weftAttrs(run, public, op string) map[string]any {
	return map[string]any{"gen_ai.operation.name": op, "weft.run.id": run, "weft.public_id": public, "gen_ai.agent.name": "conf"}
}

func traceFixture() []obsdb.Span {
	const s = time.Second
	return []obsdb.Span{
		traceSpan(trA, "a100000000000001", "", "invoke_agent conf", "conf-svc", 0, 5*s, 1, weftAttrs("tr_parent", "pub_conf", "invoke_agent")),
		traceSpan(trA, "a100000000000002", "a100000000000001", "chat script", "conf-svc", s/2, s, 0, weftAttrs("tr_parent", "pub_conf", "chat")),
		traceSpan(trA, "a100000000000003", "a100000000000001", "execute_tool research", "conf-svc", s, 4*s, 0, weftAttrs("tr_parent", "pub_conf", "execute_tool")),
		traceSpan(trA, "a100000000000004", "a100000000000003", "invoke_agent researcher", "conf-svc", 2*s, 3*s, 2,
			map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "tr_parent/0/call_1", "weft.public_id": "pub_conf",
				"weft.parent.run.id": "tr_parent", "weft.parent.call.id": "call_1", "gen_ai.agent.name": "researcher"}),
		traceSpan(trB, "b200000000000001", "", "POST /chat", "edge-svc", 10*s, 13*s, 0, map[string]any{"http.method": "POST"}),
		traceSpan(trB, "b200000000000002", "b200000000000001", "invoke_agent conf", "conf-svc", 10*s+s/2, 12*s, 1, weftAttrs("tr_web", "pub_conf", "invoke_agent")),
		traceSpan(trC, "c300000000000002", "c3000000000000ff", "work", "orphan-svc", 21*s, 22*s, 2, map[string]any{"job": "x"}),
		traceSpan(trC, "c300000000000001", "c3000000000000ff", "work", "orphan-svc", 20*s, 23*s, 0, map[string]any{"job": "x"}),
		traceSpan(trD, "d400000000000001", "", "invoke_agent conf", "conf-svc", 30*s, 31*s, 1, weftAttrs("tr_other", "pub_other", "invoke_agent")),
	}
}

// traces: the trace list (obsdb.DB.Traces) — one row per trace from its
// spans, newest first; span counts once per (trace, span) whatever a
// retried batch repeated; the runs a trace holds (a subagent child
// beside its parent); a missing root is nil with the earliest span's
// service; error from any span; the since/until/service/public-id
// filters and the (Before, BeforeID) cursor.
func traces(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		spans := traceFixture()
		for range 2 { // the second write is a retried batch: a no-op
			if err := db.Write(ctx(), obsdb.Batch{Spans: spans}); err != nil {
				t.Fatal(err)
			}
		}
		ids := func(rows []obsdb.TraceRow) []string {
			out := []string{}
			for _, r := range rows {
				out = append(out, r.TraceID)
			}
			return out
		}
		page, err := db.Traces(ctx(), obsdb.TraceQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ids(page.Traces), []string{trD, trC, trB, trA}; !reflect.DeepEqual(got, want) || page.Total != 4 || page.NextBefore != nil {
			t.Fatalf("Traces() = %v total %d next %v, want %v total 4, last page", got, page.Total, page.NextBefore, want)
		}
		const s = time.Second
		want := map[string]obsdb.TraceRow{
			trA: {TraceID: trA, Root: &obsdb.TraceRoot{SpanID: "a100000000000001", Name: "invoke_agent conf"}, Service: "conf-svc",
				Start: at(0), End: at(5 * s), SpanCount: 4, Error: true, RunIDs: []string{"tr_parent", "tr_parent/0/call_1"}},
			trB: {TraceID: trB, Root: &obsdb.TraceRoot{SpanID: "b200000000000001", Name: "POST /chat"}, Service: "edge-svc",
				Start: at(10 * s), End: at(13 * s), SpanCount: 2, RunIDs: []string{"tr_web"}},
			trC: {TraceID: trC, Service: "orphan-svc", Start: at(20 * s), End: at(23 * s), SpanCount: 2, Error: true},
			trD: {TraceID: trD, Root: &obsdb.TraceRoot{SpanID: "d400000000000001", Name: "invoke_agent conf"}, Service: "conf-svc",
				Start: at(30 * s), End: at(31 * s), SpanCount: 1, RunIDs: []string{"tr_other"}},
		}
		for _, r := range page.Traces {
			w := want[r.TraceID]
			if !r.Start.Equal(w.Start) || !r.End.Equal(w.End) {
				t.Errorf("%s: start %v end %v, want %v %v", r.TraceID, r.Start, r.End, w.Start, w.End)
			}
			r.Start, r.End, w.Start, w.End = time.Time{}, time.Time{}, time.Time{}, time.Time{}
			if !reflect.DeepEqual(r, w) {
				t.Errorf("row %s = %+v (root %+v), want %+v (root %+v)", r.TraceID, r, r.Root, w, w.Root)
			}
		}

		for _, c := range []struct {
			name  string
			q     obsdb.TraceQuery
			want  []string
			total int
		}{
			{"since", obsdb.TraceQuery{Since: at(15 * s)}, []string{trD, trC}, 2},
			// A trace's time is its earliest span: trA's spans at 1s..2s
			// do not make it start after 1s.
			{"since inside a trace", obsdb.TraceQuery{Since: at(s)}, []string{trD, trC, trB}, 3},
			{"since is inclusive", obsdb.TraceQuery{Since: at(20 * s)}, []string{trD, trC}, 2},
			{"until", obsdb.TraceQuery{Until: at(15 * s)}, []string{trB, trA}, 2},
			{"until is exclusive", obsdb.TraceQuery{Until: at(10 * s)}, []string{trA}, 1},
			{"since and until", obsdb.TraceQuery{Since: at(5 * s), Until: at(25 * s)}, []string{trC, trB}, 2},
			// Any span of the service: trB's root is edge-svc, its run's
			// span conf-svc.
			{"service", obsdb.TraceQuery{Service: "conf-svc"}, []string{trD, trB, trA}, 3},
			{"service of a root", obsdb.TraceQuery{Service: "edge-svc"}, []string{trB}, 1},
			{"service is exact", obsdb.TraceQuery{Service: "conf"}, []string{}, 0},
			{"service and since", obsdb.TraceQuery{Service: "conf-svc", Since: at(5 * s)}, []string{trD, trB}, 2},
			// Every span carries the public id: trB's non-weft root does
			// not, so trB is out (its detail would refuse a panel token).
			{"public id", obsdb.TraceQuery{PublicID: "pub_conf"}, []string{trA}, 1},
			{"another public id", obsdb.TraceQuery{PublicID: "pub_other"}, []string{trD}, 1},
			{"an unknown public id", obsdb.TraceQuery{PublicID: "pub_nobody"}, []string{}, 0},
		} {
			got, err := db.Traces(ctx(), c.q)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if !reflect.DeepEqual(ids(got.Traces), c.want) || got.Total != c.total {
				t.Errorf("%s: %v total %d, want %v total %d", c.name, ids(got.Traces), got.Total, c.want, c.total)
			}
		}

		// The cursor: two at a time, the pair resuming exactly.
		var walked []string
		q := obsdb.TraceQuery{Limit: 2}
		for i := 0; i < 4; i++ {
			p, err := db.Traces(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != 4 {
				t.Errorf("page %d total = %d, want 4 (the cursor aside)", i, p.Total)
			}
			walked = append(walked, ids(p.Traces)...)
			if p.NextBefore == nil {
				break
			}
			q.Before, q.BeforeID = *p.NextBefore, p.NextBeforeID
		}
		if want := []string{trD, trC, trB, trA}; !reflect.DeepEqual(walked, want) {
			t.Errorf("paged = %v, want %v", walked, want)
		}

		// A tie on Start rides along: trE starts with trC.
		trE := "e5000000000000000000000000000000"
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{
			traceSpan(trE, "e500000000000001", "", "tick", "cron-svc", 20*s, 20*s+s/2, 1, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		p, err := db.Traces(ctx(), obsdb.TraceQuery{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ids(p.Traces), []string{trD, trE, trC}; !reflect.DeepEqual(got, want) || p.NextBeforeID != trC {
			t.Errorf("tied page = %v next %q, want %v next %q (the tie rides along)", got, p.NextBeforeID, want, trC)
		}
		p, err = db.Traces(ctx(), obsdb.TraceQuery{Limit: 2, Before: *p.NextBefore})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ids(p.Traces), []string{trB, trA}; !reflect.DeepEqual(got, want) {
			t.Errorf("after the tie on Before alone = %v, want %v", got, want)
		}

		// An empty database lists nothing, without error.
		empty, err := open(t).Traces(ctx(), obsdb.TraceQuery{})
		if err != nil || len(empty.Traces) != 0 || empty.Total != 0 || empty.NextBefore != nil {
			t.Errorf("empty = %+v, %v", empty, err)
		}
	}
}

// walkTraces pages q with the (Before, BeforeID) pair until the last
// page, returning every listed id in order and each page's length.
func walkTraces(t *testing.T, db obsdb.DB, q obsdb.TraceQuery, total int) (ids []string, pages []int) {
	t.Helper()
	for i := 0; i < 10; i++ {
		p, err := db.Traces(ctx(), q)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != total {
			t.Errorf("page %d total = %d, want %d (the cursor aside)", i, p.Total, total)
		}
		for _, r := range p.Traces {
			ids = append(ids, r.TraceID)
		}
		pages = append(pages, len(p.Traces))
		if p.NextBefore == nil {
			return ids, pages
		}
		q.Before, q.BeforeID = *p.NextBefore, p.NextBeforeID
	}
	t.Fatal("the cursor never reached the last page")
	return nil, nil
}

// tracesBigTie: a tie longer than MaxTies is cut there, and the
// (Before, BeforeID) pair resumes inside it — every trace listed once.
func tracesBigTie(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const tied = obsdb.MaxTies + 3
		spans := []obsdb.Span{
			traceSpan("f0000000000000000000000000000000", "f000000000000001", "", "newer", "tie-svc", 20*time.Second, 21*time.Second, 1, nil),
			traceSpan("00000000000000000000000000000001", "0000000000000001", "", "older", "tie-svc", 0, time.Second, 1, nil),
		}
		for i := range tied {
			spans = append(spans, traceSpan(fmt.Sprintf("e%031x", i), fmt.Sprintf("e%015x", i), "", "tied", "tie-svc",
				10*time.Second, 11*time.Second, 1, nil))
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: spans}); err != nil {
			t.Fatal(err)
		}
		ids, pages := walkTraces(t, db, obsdb.TraceQuery{Limit: 2}, tied+2)
		// Page one: the newer trace, one tied by the limit, MaxTies more
		// riding along; the pair resumes the last two tied, then the older.
		if len(pages) < 2 || pages[0] != 2+obsdb.MaxTies {
			t.Errorf("pages = %v, want the first cut at %d (limit 2 + MaxTies)", pages, 2+obsdb.MaxTies)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Errorf("%s listed twice", id)
			}
			seen[id] = true
		}
		if len(seen) != tied+2 || ids[0] != spans[0].TraceID || ids[len(ids)-1] != spans[1].TraceID {
			t.Errorf("walked %d distinct (first %s, last %s), want %d from the newer to the older", len(seen), ids[0], ids[len(ids)-1], tied+2)
		}
		// Before alone after the cut skips the rest of the tie (strictly
		// older): only the pair resumes inside it.
		p, err := db.Traces(ctx(), obsdb.TraceQuery{Limit: 2, Before: at(10 * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Traces) != 1 || p.Traces[0].TraceID != spans[1].TraceID {
			t.Errorf("Before alone at the tie = %d rows, want the older trace only", len(p.Traces))
		}
	}
}

// tracesScopedPaging: the public-id filter under the cursor — every
// page holds only traces every span of which carries the id, Total
// counts that match on every page, and the walk lists each once.
func tracesScopedPaging(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const s = time.Second
		spans := traceFixture()
		for i, tr := range []string{"f6000000000000000000000000000006", "f7000000000000000000000000000007", "f8000000000000000000000000000008"} {
			start := time.Duration(40+10*i) * s
			spans = append(spans, traceSpan(tr, fmt.Sprintf("f%d00000000000001", 6+i), "", "invoke_agent conf", "conf-svc", start, start+s, 1,
				weftAttrs(fmt.Sprintf("tr_scoped_%d", i), "pub_conf", "invoke_agent")))
		}
		// A mixed trace at 45s: a pub_conf run beside a pub_other span.
		const mixed = "f9000000000000000000000000000009"
		spans = append(spans,
			traceSpan(mixed, "f900000000000001", "", "invoke_agent conf", "conf-svc", 45*s, 46*s, 1, weftAttrs("tr_mixed", "pub_conf", "invoke_agent")),
			traceSpan(mixed, "f900000000000002", "f900000000000001", "invoke_agent other", "conf-svc", 45*s+s/2, 46*s, 1, weftAttrs("tr_mixed_kid", "pub_other", "invoke_agent")))
		if err := db.Write(ctx(), obsdb.Batch{Spans: spans}); err != nil {
			t.Fatal(err)
		}
		ids, pages := walkTraces(t, db, obsdb.TraceQuery{PublicID: "pub_conf", Limit: 2}, 4)
		want := []string{"f8000000000000000000000000000008", "f7000000000000000000000000000007", "f6000000000000000000000000000006", trA}
		if !reflect.DeepEqual(ids, want) || pages[0] != 2 {
			t.Errorf("scoped walk = %v (pages %v), want %v two at a time (no %s, no %s)", ids, pages, want, trB, mixed)
		}
	}
}
