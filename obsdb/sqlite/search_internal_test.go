package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// A transport's re-delivery of a batch changes nothing the search index
// holds: run_search, run_tools and runs_fts keep their rows and their
// content (I4's rule, extended to the index).
func TestSearchIndexIgnoresARedeliveredBatch(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	d := db.(*DB)
	ctx := context.Background()
	t0 := time.Unix(0, 1790845923120000000).UTC()
	rec := func(kind string, pos int64, body string, extra map[string]any) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": "r1"}
		for k, v := range extra {
			a[k] = v
		}
		return obsdb.Record{Time: t0.Add(time.Duration(pos) * time.Millisecond), Body: body, Attrs: a}
	}
	batch := obsdb.Batch{
		Records: []obsdb.Record{
			rec("event", 0, `{"type":"run_start","id":"r1"}`, map[string]any{"weft.event.type": "run_start", "weft.event.pos": int64(0)}),
			rec("event", 1, `{"type":"tool_start","name":"issue_refund"}`, map[string]any{"weft.event.type": "tool_start", "weft.event.pos": int64(1)}),
			rec("messages", 0, `[{"role":"user","content":[{"type":"text","text":"refund order 7"}]}]`,
				map[string]any{"weft.messages.index": int64(0), "weft.messages.input": true}),
			rec("messages", 1, `[{"role":"assistant","content":[{"type":"text","text":"working"}]}]`,
				map[string]any{"weft.messages.index": int64(1)}),
		},
		Spans: []obsdb.Span{{TraceID: "01", SpanID: "02", Name: "invoke_agent a", Start: t0, End: t0.Add(time.Second),
			StatusCode: 2, StatusMessage: "gateway failed",
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "r1"}}},
	}
	snapshot := func() string {
		t.Helper()
		var b strings.Builder
		for _, q := range []string{
			`SELECT id, run_id, prompt, prompt_idx, answer, answer_idx FROM run_search ORDER BY id`,
			`SELECT run_id, name, '', '', '', '' FROM run_tools ORDER BY run_id, name`,
			`SELECT rowid, prompt, answer, err, tools, '' FROM runs_fts ORDER BY rowid`,
		} {
			rows, err := d.reads.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var c [6]any
				if err := rows.Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5]); err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&b, "%v\n", c)
			}
			_ = rows.Close()
			b.WriteString("--\n")
		}
		return b.String()
	}
	if err := db.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	first := snapshot()
	if !strings.Contains(first, "refund order 7") || !strings.Contains(first, "issue_refund") || !strings.Contains(first, "gateway failed") {
		t.Fatalf("index after the first delivery:\n%s", first)
	}
	for i := 0; i < 2; i++ {
		if err := db.Write(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if again := snapshot(); again != first {
			t.Errorf("re-delivery %d changed the index:\n%s\nwant\n%s", i+1, again, first)
		}
	}
}
