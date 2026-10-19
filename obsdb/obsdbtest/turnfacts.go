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
// alike. Session (the one-session read) carries the same facts. A
// cancel for another cause (thread.closed) is cancelled, counted apart
// and no failure; a session past MaxOutcomes counts every turn and
// strips the newest (U16-fix).
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
			// Cancelled for another reason (thread.closed): counted apart,
			// never a failure — the Failed filter skips it (U16-fix).
			{sid: "s_tf_cancel", pub: "pub_tfx", n: 1, status: 2, extra: failed("thread.closed")},
			// A newest turn that failed for real: the one Status failed
			// keeps (P9-pre-web-fix).
			{sid: "s_tf_bad", pub: "pub_tfb", n: 1, status: 1},
			{sid: "s_tf_bad", pub: "pub_tfb", n: 2, status: 2, extra: map[string]any{"error.type": "model_error"}},
		} {
			add(tr)
		}
		// A long session (U16-fix): the counts are every turn's, the strip
		// and the open park the newest MaxOutcomes' — t1 failed, out of
		// the strip and still counted; t60 parked.
		const long = obsdb.MaxOutcomes + 20
		for n := 1; n <= long; n++ {
			tr := turn{sid: "s_tf_long", pub: "pub_tfl", n: int64(n), status: 1}
			switch n {
			case 1:
				tr.status, tr.extra = 2, map[string]any{"error.type": "model_error"}
			case long:
				tr.extra = map[string]any{"weft.run.pending": int64(3)}
			}
			add(tr)
		}
		longStrip := make([]obsdb.TurnOutcome, obsdb.MaxOutcomes)
		for i := range longStrip {
			longStrip[i] = obsdb.OutcomeSucceeded
		}
		longStrip[obsdb.MaxOutcomes-1] = obsdb.OutcomeParked
		if err := db.Write(ctx(), b); err != nil {
			t.Fatal(err)
		}
		type facts struct {
			pending, failed, ended, cancelled, resumes int
			outcomes                                   []obsdb.TurnOutcome
			fork                                       string
		}
		want := map[string]facts{
			"s_tf": {pending: 1, failed: 1, ended: 1, cancelled: 1, resumes: 1, outcomes: []obsdb.TurnOutcome{
				obsdb.OutcomeSucceeded, obsdb.OutcomeResumed, obsdb.OutcomeSucceeded, obsdb.OutcomeFailed,
				obsdb.OutcomeEnded, obsdb.OutcomeCancelled, obsdb.OutcomeParked}},
			"s_tf_fork":   {outcomes: []obsdb.TurnOutcome{obsdb.OutcomeSucceeded}, fork: "s_tf#e9"},
			"s_tf_child":  {ended: 1, outcomes: []obsdb.TurnOutcome{obsdb.OutcomeSucceeded, obsdb.OutcomeEnded}},
			"s_tf_cancel": {cancelled: 1, outcomes: []obsdb.TurnOutcome{obsdb.OutcomeCancelled}},
			"s_tf_long":   {pending: 3, failed: 1, outcomes: longStrip},
			"s_tf_bad":    {failed: 1, outcomes: []obsdb.TurnOutcome{obsdb.OutcomeSucceeded, obsdb.OutcomeFailed}},
		}
		check := func(where string, r obsdb.SessionRow) {
			t.Helper()
			w, ok := want[r.ID]
			if !ok {
				return
			}
			got := facts{r.Pending, r.FailedTurns, r.EndedTurns, r.CancelledTurns, r.Resumes, r.Outcomes, r.ForkedFrom}
			if got.pending != w.pending || got.failed != w.failed || got.ended != w.ended || got.cancelled != w.cancelled || got.resumes != w.resumes ||
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
			{"awaiting", obsdb.SessionQuery{Awaiting: true}, []string{"s_tf", "s_tf_long"}},
			{"awaiting scoped", obsdb.SessionQuery{Awaiting: true, PublicID: "pub_tf"}, []string{"s_tf"}},
			{"awaiting other scope", obsdb.SessionQuery{Awaiting: true, PublicID: "pub_tfc"}, nil},
			{"failed", obsdb.SessionQuery{Failed: true}, []string{"s_tf", "s_tf_bad", "s_tf_long"}},
			{"child", obsdb.SessionQuery{Lineage: obsdb.LineageChild}, []string{"s_tf_child"}},
			{"fork", obsdb.SessionQuery{Lineage: obsdb.LineageFork}, []string{"s_tf_fork"}},
			{"unknown lineage", obsdb.SessionQuery{Lineage: "cousin"}, nil},
			{"since", obsdb.SessionQuery{Since: time.Now().Add(time.Hour)}, nil},
			{"since old", obsdb.SessionQuery{Since: old}, []string{"s_tf", "s_tf_bad", "s_tf_cancel", "s_tf_child", "s_tf_fork", "s_tf_long"}},
			{"compacted", obsdb.SessionQuery{Compacted: true}, nil},
			// Status: the newest turn's outcome, the session page's word —
			// a parked newest turn (s_tf, s_tf_long) is not succeeded, a
			// cancelled (s_tf_cancel) or busy-ended (s_tf_child) one not
			// failed (P9-pre-web-fix).
			{"status succeeded", obsdb.SessionQuery{Status: obsdb.StatusSucceeded}, []string{"s_tf_fork"}},
			{"status failed", obsdb.SessionQuery{Status: obsdb.StatusFailed}, []string{"s_tf_bad"}},
			{"status failed scoped", obsdb.SessionQuery{Status: obsdb.StatusFailed, PublicID: "pub_tfb"}, []string{"s_tf_bad"}},
			{"status failed other scope", obsdb.SessionQuery{Status: obsdb.StatusFailed, PublicID: "pub_tfc"}, nil},
			{"status cancelled keeps none", obsdb.SessionQuery{Status: obsdb.StatusCancelled}, nil},
			{"status running", obsdb.SessionQuery{Status: obsdb.StatusRunning}, nil},
			{"status parked keeps none", obsdb.SessionQuery{Status: obsdb.StatusParked}, nil},
			{"failed and fork", obsdb.SessionQuery{Failed: true, Lineage: obsdb.LineageFork}, nil},
		} {
			if got := ids(c.q); !slices.Equal(got, c.want) {
				t.Errorf("%s: %v; want %v", c.name, got, c.want)
			}
		}
		CheckTurnFactFilters(t, db)
	}
}

// CheckTurnFactFilters pins the SQL against Go over whatever db holds
// (U16-fix; every conformance fixture runs it, TestContract every
// contract fixture): each listed session's turn facts as the list's
// SQL counts them equal obsdb.SessionTurnFacts over the session's runs
// in Go (Session's), and SessionQuery's Awaiting and Failed filters
// keep exactly the sessions whose Go facts say Pending > 0 and
// FailedTurns > 0, and its Status filter those whose newest outcome is
// the word. Every read sees one view of the session's turns —
// the same retention cut (ClickHouse's weft_runs TTL) on each: the
// list's Turns is the Session's, its len(Runs) and Runs{SessionID}'s
// Total, so a read that kept a turn another cut is caught.
func CheckTurnFactFilters(t *testing.T, db obsdb.DB) {
	t.Helper()
	all, err := db.Sessions(ctx(), obsdb.SessionQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var awaiting, failed []string
	byStatus := map[obsdb.Status][]string{}
	no := false
	for _, r := range all.Sessions {
		det, err := db.Session(ctx(), r.ID)
		if err != nil {
			t.Fatalf("Session(%s): %v", r.ID, err)
		}
		turns, err := db.Runs(ctx(), obsdb.RunQuery{SessionID: r.ID, Playground: &no, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if r.Turns != det.Turns || det.Turns != len(det.Runs) || turns.Total != len(det.Runs) {
			t.Errorf("session %s: the list's %d turns, Session's %d over %d runs, Runs{SessionID}'s Total %d: one view of its turns on every read",
				r.ID, r.Turns, det.Turns, len(det.Runs), turns.Total)
		}
		var g obsdb.SessionRow
		obsdb.SessionTurnFacts(&g, det.Runs)
		if g.Pending != r.Pending || g.FailedTurns != r.FailedTurns || g.EndedTurns != r.EndedTurns ||
			g.CancelledTurns != r.CancelledTurns || g.Resumes != r.Resumes || g.ForkedFrom != r.ForkedFrom ||
			!slices.Equal(g.Outcomes, r.Outcomes) {
			t.Errorf("session %s: the list's facts %d/%d/%d/%d/%d %q %v, Go's over its runs %d/%d/%d/%d/%d %q %v", r.ID,
				r.Pending, r.FailedTurns, r.EndedTurns, r.CancelledTurns, r.Resumes, r.ForkedFrom, r.Outcomes,
				g.Pending, g.FailedTurns, g.EndedTurns, g.CancelledTurns, g.Resumes, g.ForkedFrom, g.Outcomes)
		}
		if g.Pending > 0 {
			awaiting = append(awaiting, r.ID)
		}
		if g.FailedTurns > 0 {
			failed = append(failed, r.ID)
		}
		if n := len(g.Outcomes); n > 0 {
			o := obsdb.Status(g.Outcomes[n-1])
			byStatus[o] = append(byStatus[o], r.ID)
		}
	}
	for _, c := range []struct {
		name string
		q    obsdb.SessionQuery
		want []string
	}{
		{"Awaiting", obsdb.SessionQuery{Awaiting: true, Limit: 500}, awaiting},
		{"Failed", obsdb.SessionQuery{Failed: true, Limit: 500}, failed},
		// Status: the newest turn's outcome (P9-pre-web-fix).
		{"Status succeeded", obsdb.SessionQuery{Status: obsdb.StatusSucceeded, Limit: 500}, byStatus[obsdb.StatusSucceeded]},
		{"Status failed", obsdb.SessionQuery{Status: obsdb.StatusFailed, Limit: 500}, byStatus[obsdb.StatusFailed]},
		{"Status interrupted", obsdb.SessionQuery{Status: obsdb.StatusInterrupted, Limit: 500}, byStatus[obsdb.StatusInterrupted]},
	} {
		p, err := db.Sessions(ctx(), c.q)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range p.Sessions {
			got = append(got, r.ID)
		}
		slices.Sort(got)
		want := slices.Sorted(slices.Values(c.want))
		if !slices.Equal(got, want) {
			t.Errorf("Sessions(%s) = %v; Go's facts say %v", c.name, got, want)
		}
	}
}
