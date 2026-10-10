package clickhouse

import (
	"fmt"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// The key loop never deletes a run's own span: those go last, through
// the RunId IN … statement, so a deletion cut short leaves the run's own
// spans and a retry's trace lookup still reaches the non-weft spans
// below them. Every key's app log lines are deleted in the loop, and the
// chunks cover every key once.
func TestPlanSpanDeletesKeepsOwnSpansForLast(t *testing.T) {
	var links []obsdb.SpanLink
	for i := range 450 {
		l := obsdb.SpanLink{TraceID: "t", SpanID: fmt.Sprintf("s%04d", i)}
		if i%3 == 0 {
			l.RunID = "run_a"
		} else {
			l.ParentSpanID = fmt.Sprintf("s%04d", i-i%3)
		}
		links = append(links, l)
	}
	keys := obsdb.DeletedSpans(links, map[string]bool{"run_a": true})
	if len(keys) != 450 {
		t.Fatalf("DeletedSpans = %d keys, want 450", len(keys))
	}
	chunks := planSpanDeletes(links, keys)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3 (deleteChunk/2 = %d keys each)", len(chunks), deleteChunk/2)
	}
	logs, spans := 0, 0
	for _, c := range chunks {
		if len(c.logs) > deleteChunk/2 {
			t.Errorf("a chunk of %d keys", len(c.logs))
		}
		logs += len(c.logs)
		for _, k := range c.spans {
			var n int
			_, _ = fmt.Sscanf(k.SpanID, "s%d", &n)
			if n%3 == 0 {
				t.Errorf("the key loop deletes run_a's own span %s", k.SpanID)
			}
		}
		spans += len(c.spans)
	}
	if logs != 450 || spans != 300 {
		t.Errorf("logs keys = %d (want 450), span keys = %d (want 300, the non-weft ones)", logs, spans)
	}
	if got := planSpanDeletes(nil, nil); len(got) != 0 {
		t.Errorf("no keys = %d chunks", len(got))
	}
}
