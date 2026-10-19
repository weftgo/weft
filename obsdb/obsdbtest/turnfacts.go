package obsdbtest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// sessionTurnFacts pins a session row's turn facts on every backend
// (U16; obsdb.SessionTurnFacts, record contract §6): the outcome strip
// in turn order — a decided park read resumed, an open one parked, a
// busy-policy cancel (weft.cancel.cause thread.interrupted) ended and
// never failed — the failed and ended counts apart, the resumes (a
// stored weft.resumed_from), the newest turn's open park as Pending,
// the fork lineage; and SessionQuery's Awaiting, Failed, Lineage and
// Since filters on the same rule, on Sessions and on the scoped read
// alike. Session (the one-session read) carries the same facts.
func sessionTurnFacts(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		old := time.Now().UTC().Add(-2 * obsdb.InterruptedAfter)
		type turn struct {
			sid, pub string
			n        int64
			status   int // the root span's: 1 ok, 2 error
			extra    map[string]any
		}
		var b obsdb.Batch
		k := 0
		add := func(tr turn) {
			k++
			id := fmt.Sprintf("%s-t%d", tr.sid, tr.n)
			ident := map[string]any{"weft.session.id": tr.sid, "weft.public_id": tr.pub, "weft.turn": tr.n}
			attrs := map[string]any{}
			for kk, v := range ident {
				attrs[kk] = v
			}
			for kk, v := range tr.extra {
				attrs[kk] = v
			}
			s := invokeSpan(id, tr.status, attrs)
			s.TraceID = fmt.Sprintf("d7%030x", k)
			s.SpanID = fmt.Sprintf("d7%014x", k)
			start := old.Add(time.Duration(k) * time.Second)
			s.Start, s.End = start, start.Add(time.Second/2)
			r := record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, ident)
			r.Time = start
			if from, ok := tr.extra["weft.resumed_from"]; ok {
				r.Attrs["weft.resumed_from"] = from
			}
			b.Spans = append(b.Spans, s)
			b.Records = append(b.Records, r)
		}
		failed := func(cause string) map[string]any {
			return map[string]any{"error.type": "context.Canceled", "weft.run.stopped_by": "canceled", "weft.cancel.cause": cause}
		}
		for _, tr := range []turn{
			{sid: "s_tf", pub: "pub_tf", n: 1, status: 1},
			{sid: "s_tf", pub: "pub_tf", n: 2, status: 1, extra: map[string]any{"weft.run.pending": int64(2)}},
			{sid: "s_tf", pub: "pub_tf", n: 3, status: 1, extra: map[string]any{"weft.resumed_from": "s_tf-t2"}},
			{sid: "s_tf", pub: "pub_tf", n: 4, status: 2, extra: map[string]any{"error.type": "model_error"}},
			{sid: "s_tf", pub: "pub_tf", n: 5, status: 2, extra: failed(obsdb.BusyCancelCause)},
			{sid: "s_tf", pub: "pub_tf", n: 6, status: 2, extra: failed("studio.cancel")},
			{sid: "s_tf", pub: "pub_tf", n: 7, status: 1, extra: map[string]any{"weft.run.pending": int64(1)}},
			// A fork: its turns carry weft.session.forked_from.
			{sid: "s_tf_fork", pub: "pub_tf", n: 1, status: 1, extra: map[string]any{obsdb.SessionForkedFromKey: "s_tf#e9"}},
			// A delegated session, and an ended-only one: an interrupt is
			// no failure.
			{sid: "s_tf_child", pub: "pub_tfc", n: 1, status: 1, extra: map[string]any{"weft.session.parent": "s_tf"}},
			{sid: "s_tf_child", pub: "pub_tfc", n: 2, status: 2, extra: failed(obsdb.BusyCancelCause)},
		} {
			add(tr)
		}
		if err := db.Write(ctx(), b); err != nil {
			t.Fatal(err)
		}
		type facts struct {
			pending, failed, ended, resumes int
			outcomes                        []obsdb.TurnOutcome
			fork                            string
		}
		want := map[string]facts{
			"s_tf": {pending: 1, failed: 2, ended: 1, resumes: 1, outcomes: []obsdb.TurnOutcome{
				obsdb.OutcomeSucceeded, obsdb.OutcomeResumed, obsdb.OutcomeSucceeded, obsdb.OutcomeFailed,
				obsdb.OutcomeEnded, obsdb.OutcomeCancelled, obsdb.OutcomeParked}},
			"s_tf_fork":  {outcomes: []obsdb.TurnOutcome{obsdb.OutcomeSucceeded}, fork: "s_tf#e9"},
			"s_tf_child": {ended: 1, outcomes: []obsdb.TurnOutcome{obsdb.OutcomeSucceeded, obsdb.OutcomeEnded}},
		}
		check := func(where string, r obsdb.SessionRow) {
			t.Helper()
			w, ok := want[r.ID]
			if !ok {
				return
			}
			got := facts{r.Pending, r.FailedTurns, r.EndedTurns, r.Resumes, r.Outcomes, r.ForkedFrom}
			if got.pending != w.pending || got.failed != w.failed || got.ended != w.ended || got.resumes != w.resumes ||
				!slices.Equal(got.outcomes, w.outcomes) || got.fork != w.fork {
				t.Errorf("%s %s: facts %+v; want %+v", where, r.ID, got, w)
			}
		}
		page, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, r := range page.Sessions {
			if _, ok := want[r.ID]; ok {
				seen++
			}
			check("Sessions", r)
		}
		if seen != len(want) {
			t.Fatalf("Sessions listed %d of the %d sessions: %+v", seen, len(want), page.Sessions)
		}
		for id := range want {
			det, err := db.Session(ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			check("Session", det.SessionRow)
		}
		// Scoped to a public id: the same facts over its turns.
		scoped, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: "pub_tf"})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range scoped.Sessions {
			check("Sessions(public id)", r)
		}
		ids := func(q obsdb.SessionQuery) []string {
			t.Helper()
			p, err := db.Sessions(ctx(), q)
			if err != nil {
				t.Fatalf("%+v: %v", q, err)
			}
			var out []string
			for _, r := range p.Sessions {
				if _, ok := want[r.ID]; ok {
					out = append(out, r.ID)
				}
			}
			slices.Sort(out)
			return out
		}
		for _, c := range []struct {
			name string
			q    obsdb.SessionQuery
			want []string
		}{
			{"awaiting", obsdb.SessionQuery{Awaiting: true}, []string{"s_tf"}},
			{"awaiting scoped", obsdb.SessionQuery{Awaiting: true, PublicID: "pub_tf"}, []string{"s_tf"}},
			{"awaiting other scope", obsdb.SessionQuery{Awaiting: true, PublicID: "pub_tfc"}, nil},
			{"failed", obsdb.SessionQuery{Failed: true}, []string{"s_tf"}},
			{"child", obsdb.SessionQuery{Lineage: obsdb.LineageChild}, []string{"s_tf_child"}},
			{"fork", obsdb.SessionQuery{Lineage: obsdb.LineageFork}, []string{"s_tf_fork"}},
			{"unknown lineage", obsdb.SessionQuery{Lineage: "cousin"}, nil},
			{"since", obsdb.SessionQuery{Since: time.Now().Add(time.Hour)}, nil},
			{"since old", obsdb.SessionQuery{Since: old}, []string{"s_tf", "s_tf_child", "s_tf_fork"}},
			{"failed and fork", obsdb.SessionQuery{Failed: true, Lineage: obsdb.LineageFork}, nil},
		} {
			if got := ids(c.q); !slices.Equal(got, c.want) {
				t.Errorf("%s: %v; want %v", c.name, got, c.want)
			}
		}
	}
}
