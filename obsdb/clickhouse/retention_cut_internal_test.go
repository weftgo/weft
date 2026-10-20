package clickhouse

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestRunsTTLFailedReadIsHeld (P9-pre-go-fix3): a failed system.tables
// read is held for retentionEvery like a good one — not retried on
// every call under the mutex — and says so in one Debug line. With no
// window ever read the reads cut nothing; a later good read cuts.
func TestRunsTTLFailedReadIsHeld(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	var reads atomic.Int64
	fail := true
	d := &DB{}
	d.retention.read = func(context.Context) (string, error) {
		reads.Add(1)
		if fail {
			return "", errors.New("system.tables: boom")
		}
		return "MergeTree ORDER BY RunId TTL LastSeen + toIntervalSecond(3600) SETTINGS index_granularity = 8192", nil
	}
	ctx := context.Background()
	for range 5 {
		if c := d.runsCut(ctx); c.on || c.where() != "" {
			t.Fatalf("runsCut with no window known = %+v (%q), want no cut", c, c.where())
		}
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("five calls after a failed read made %d reads, want 1 (held for a minute)", n)
	}
	if got := buf.String(); strings.Count(got, "weft_runs' TTL could not be read") != 1 || !strings.Contains(got, "level=DEBUG") || !strings.Contains(got, "boom") {
		t.Errorf("log = %q, want one Debug line naming the failure", got)
	}
	// A minute on, the read is tried again; a good one cuts.
	fail = false
	d.retention.at = time.Now().Add(-retentionEvery - time.Second)
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	d.retention.now = func() time.Time { return at }
	c := d.runsCut(ctx)
	if reads.Load() != 2 || !c.on || c.afterNs != at.Add(-time.Hour).UnixNano() {
		t.Errorf("after a minute: %d reads, cut %+v; want 2 and the boundary now − 1h", reads.Load(), c)
	}
	if want := " AND toUnixTimestamp64Nano(LastSeen) > 1792494000000000000"; c.where() != want {
		t.Errorf("where() = %q, want %q", c.where(), want)
	}
	// A failed read after a good one keeps the good window.
	fail = true
	d.retention.at = time.Now().Add(-retentionEvery - time.Second)
	if c := d.runsCut(ctx); !c.on {
		t.Error("a failed read after a good one dropped the window it knew")
	}
}

// TestRunsCutOncePerCall (P9-pre-go-fix3): the cut's boundary is taken
// once per obsdb call — one clock read, one window — and every
// statement of the call compares against it. The clock here jumps a
// whole window on every read, so a call that read it twice would cut
// its count and its page (or its turn facts) at different instants and
// disagree; each call reads it once and agrees with itself.
func TestRunsCutOncePerCall(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	stopMerges(t, db)
	at := time.Now().UTC().Add(-time.Minute)
	writeSessionTurn(t, db, "s_once", 1, at)
	var reads atomic.Int64
	// Read 1 at the real now (the run kept), read 2 a window and a day
	// on (cut), and so on alternating.
	db.retention.now = func() time.Time {
		if reads.Add(1)%2 == 1 {
			return time.Now()
		}
		return time.Now().Add(DefaultMetaTTL + 24*time.Hour)
	}
	page, err := db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("Sessions read the cut's clock %d times, want 1", n)
	}
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].Turns != 1 || len(page.Sessions[0].Outcomes) != 1 {
		t.Errorf("Sessions = Total %d over %+v; want one session of one turn, every statement at one boundary", page.Total, page.Sessions)
	}
	// The second call reads the jumped clock: the run is cut throughout.
	page, err = db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Sessions) != 0 {
		t.Errorf("Sessions past the window = Total %d, %d rows; want none", page.Total, len(page.Sessions))
	}
	reads.Store(0)
	if _, err := db.Session(ctx, "s_once"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Runs(ctx, obsdb.RunQuery{SessionID: "s_once"}); err != nil {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 2 {
		t.Errorf("Session then Runs read the clock %d times, want 2 (once each)", n)
	}
}

// TestRunTreeCutAgreesWithChildren (P9-pre-go-fix3): in the window
// between a child's cut and its parent's — the child's LastSeen past
// weft_runs' TTL, the parent's not, merges stopped so the child's rows
// are still on disk — Run's children, ChildRuns and the run tree all
// leave the child out (and its spans, and a grandchild under it); with
// the clock back, all three hold it. One rule for every run listing.
func TestRunTreeCutAgreesWithChildren(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	stopMerges(t, db)
	now := time.Now().UTC()
	const trace = "0102030405060708090a0b0c0d0e0fc7"
	span := func(spanID, runID, parent string, start time.Time) obsdb.Span {
		attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": runID}
		if parent != "" {
			attrs["weft.parent.run.id"] = parent
		}
		return obsdb.Span{TraceID: trace, SpanID: spanID, Name: "invoke_agent cut", Kind: 1,
			Start: start, End: start.Add(time.Second), StatusCode: 1, Service: "cut-svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "cut-svc"}}
	}
	const parent, child, grand = "tr_cut", "tr_cut/1/call_a", "tr_cut/1/call_a/0/call_b"
	old := now.Add(-DefaultMetaTTL + 24*time.Hour) // a day inside the window now
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{
		span("c700000000000001", parent, "", now.Add(-time.Minute)),
		span("c700000000000002", child, parent, old),
		span("c700000000000003", grand, child, old.Add(time.Millisecond)),
	}}); err != nil {
		t.Fatal(err)
	}
	check := func(label string, want int) {
		t.Helper()
		det, err := db.Run(ctx, parent)
		if err != nil {
			t.Fatal(err)
		}
		kids, err := db.ChildRuns(ctx, []string{parent})
		if err != nil {
			t.Fatal(err)
		}
		tree, err := db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: parent})
		if err != nil {
			t.Fatal(err)
		}
		runs := map[string]bool{}
		for _, s := range tree.Spans {
			runs[s.RunID] = true
		}
		if len(det.Children) != min(want, 1) || len(kids[parent]) != min(want, 1) || len(runs) != 1+want {
			t.Errorf("%s: Run children %d, ChildRuns %d, tree runs %v; want %d, %d and the parent + %d", label,
				len(det.Children), len(kids[parent]), runs, min(want, 1), min(want, 1), want)
		}
		_, err = db.Run(ctx, child)
		if want == 0 && !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("%s: Run(child) = %v, want not found", label, err)
		}
		_, err = db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: child})
		if want == 0 && !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("%s: RunTreeSpans(child) = %v, want not found as Run's", label, err)
		}
	}
	check("inside the window", 2)
	db.retention.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	check("the child past it, the parent not", 0)
	db.retention.now = nil
	check("the clock back", 2)
}

// stopMerges stops weft_runs' merges (and TTL merges) for t: expired
// rows stay on disk, so only the read's cut leaves them out.
func stopMerges(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{"SYSTEM STOP MERGES weft_runs", "SYSTEM STOP TTL MERGES weft_runs"} {
		if err := db.conn.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = db.conn.Exec(context.Background(), "SYSTEM START MERGES weft_runs")
		_ = db.conn.Exec(context.Background(), "SYSTEM START TTL MERGES weft_runs")
	})
}

// writeSessionTurn writes one finished turn of sid at at.
func writeSessionTurn(t *testing.T, db *DB, sid string, turn int64, at time.Time) {
	t.Helper()
	id := sid + "-t" + string(rune('0'+turn))
	attrs := func(ev string, pos int64) map[string]any {
		return map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.turn": turn,
			"weft.event.type": ev, "weft.event.pos": pos}
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		{Time: at, EventName: "weft.event", Severity: 9, Attrs: attrs("run_start", 0), Body: `{"type":"run_start","id":"` + id + `"}`},
		{Time: at.Add(time.Second), EventName: "weft.event", Severity: 9, Attrs: attrs("run_finish", 1),
			Body: `{"type":"run_finish","run_id":"` + id + `","steps":1}`},
	}}); err != nil {
		t.Fatal(err)
	}
}
