package sqlite_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// BenchmarkSessionsTurnFacts reads one /api/sessions page of 50
// sessions of 1,000 turns each (U16-fix): the turn facts are counted
// in SQL and the strip reads the newest obsdb.MaxOutcomes turns per
// session, so the page reads 50 × 40 narrow rows, never 50,000 run
// rows. Each turn is one invoke_agent span; the last of each session
// parks, every hundredth (t50, t150, …) fails.
//
//	go test ./obsdb/sqlite -run '^$' -bench SessionsTurnFacts -benchtime 20x
func BenchmarkSessionsTurnFacts(b *testing.B) {
	const sessions, turns = 50, 1000
	db, err := sqlite.Open(b.TempDir() + "/bench.db")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()
	k := 0
	for s := range sessions {
		var batch obsdb.Batch
		sid := fmt.Sprintf("s_bench%02d", s)
		for n := 1; n <= turns; n++ {
			k++
			id := fmt.Sprintf("%s-t%d", sid, n)
			attrs := map[string]any{
				"gen_ai.operation.name": "invoke_agent", "weft.run.id": id, "gen_ai.agent.name": "bench",
				"weft.session.id": sid, "weft.public_id": "pub_" + sid, "weft.turn": int64(n),
			}
			status := 1
			if n%100 == 50 {
				status, attrs["error.type"] = 2, "model_error"
			}
			if n == turns {
				attrs["weft.run.pending"] = int64(1)
			}
			start := base.Add(time.Duration(k) * time.Millisecond)
			batch.Spans = append(batch.Spans, obsdb.Span{
				TraceID: fmt.Sprintf("be%030x", k), SpanID: fmt.Sprintf("be%014x", k),
				Name: "invoke_agent bench", Kind: 1, Start: start, End: start.Add(time.Millisecond),
				StatusCode: status, Service: "bench", Attrs: attrs,
				Resource: map[string]any{"service.name": "bench"},
			})
		}
		if err := db.Write(ctx, batch); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		page, err := db.Sessions(ctx, obsdb.SessionQuery{Limit: sessions})
		if err != nil {
			b.Fatal(err)
		}
		if len(page.Sessions) != sessions {
			b.Fatalf("listed %d sessions, want %d", len(page.Sessions), sessions)
		}
		r := page.Sessions[0]
		if r.Turns != turns || r.FailedTurns != turns/100 || r.Pending != 1 || len(r.Outcomes) != obsdb.MaxOutcomes {
			b.Fatalf("row %s: turns %d failed %d pending %d strip %d", r.ID, r.Turns, r.FailedTurns, r.Pending, len(r.Outcomes))
		}
	}
}
