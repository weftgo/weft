package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestSweepTakesOrphanSummarizerSpans (P9-pre-go-fix3): a summarizer
// span landing after DeleteRun is stored under the gone run id — Write
// cannot tell a deleted run from one whose records are still on their
// way, and it recreates no row — and Sweep takes it once it is older
// than the cutoff. A younger orphan waits for its own cutoff; a kept
// run's summarizer span stays with its run.
func TestSweepTakesOrphanSummarizerSpans(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	d := db.(*DB)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-3 * time.Hour)
	delFixture(t, db, "gone", "s_gone", "", old)
	delFixture(t, db, "kept", "s_kept", "", now.Add(-time.Minute))
	if err := db.DeleteRun(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	summarizer := func(runID, spanID string, start time.Time) obsdb.Span {
		return obsdb.Span{TraceID: strings.Repeat("cd", 16), SpanID: spanID, Name: "chat summary-mini", Kind: 3,
			Start: start, End: start.Add(time.Second),
			Attrs: map[string]any{"gen_ai.operation.name": "chat", "weft.run.id": runID, obsdb.AttrChatRole: obsdb.ChatRoleSummarizer,
				"gen_ai.usage.input_tokens": int64(10)}}
	}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{
		summarizer("gone", "5ba0000000000101", old.Add(time.Minute)), // late, filed under the deleted run
		summarizer("young_gone", "5ba0000000000102", now),            // an orphan inside the window
		summarizer("kept", "5ba0000000000103", old),                  // a kept run's
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Run(ctx, "gone"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Fatalf("Run(gone) after a late summarizer span = %v, want not found", err)
	}
	spans := func(runID string) int {
		t.Helper()
		var n int
		if err := d.reads.QueryRow(`SELECT count(*) FROM spans WHERE run_id = ?`, runID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if spans("gone") != 1 {
		t.Fatalf("the late summarizer span is not stored (%d)", spans("gone"))
	}
	if _, err := db.Sweep(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := spans("gone"); n != 0 {
		t.Errorf("after Sweep the deleted run's orphan summarizer span is still stored (%d)", n)
	}
	if n := spans("young_gone"); n != 1 {
		t.Errorf("an orphan younger than the cutoff was swept (%d left): it may be a run whose records are on their way", n)
	}
	if n := spans("kept"); n < 2 {
		t.Errorf("the kept run's spans = %d, want its own and its summarizer span", n)
	}
}
