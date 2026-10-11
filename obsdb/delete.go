package obsdb

import "sort"

// SpanLink is one stored span's place in its trace, what DeletedSpans
// walks: its key, its parent and the weft.run.id it carries ("" for a
// non-weft span).
type SpanLink struct {
	TraceID, SpanID, ParentSpanID, RunID string
}

// SpanKey names one span.
type SpanKey struct {
	TraceID, SpanID string
}

// DeletedSpans is the rule both backends delete spans by (DB.DeleteRun,
// DeleteSession, Sweep): the deleted runs' own spans and, transitively,
// the non-weft spans below them — a span carrying a run id stops the
// walk, so a run that is kept keeps its spans. A derived run (§9 of
// the record contract: no run id on any span) is named by its id: its
// root span seeds the walk, which takes its subtree — a nested root's
// too, which is its child run, deleted with it (StockTraces names the
// traces to read). It is the rule a run's
// app logs are attributed by (DB.OtherLogs), so a deleted run takes
// exactly the log records its logs page showed: the backends delete the
// non-weft log records emitted under the returned spans. all holds
// every span of the traces the deleted runs' spans are in; deleted is
// the set of run ids being deleted. The keys come back sorted.
func DeletedSpans(all []SpanLink, deleted map[string]bool) []SpanKey {
	children := map[SpanKey][]SpanLink{}
	for _, s := range all {
		if s.ParentSpanID != "" {
			p := SpanKey{s.TraceID, s.ParentSpanID}
			children[p] = append(children[p], s)
		}
	}
	set := map[SpanKey]bool{}
	var queue []SpanKey
	for _, s := range all {
		// A derived run's root carries no run id (docs/record-contract.md
		// §9): it is named by its derived id, otel-<trace>-<span>.
		if (s.RunID != "" && deleted[s.RunID]) || (s.RunID == "" && deleted[StockRunID(s.TraceID, s.SpanID)]) {
			k := SpanKey{s.TraceID, s.SpanID}
			if !set[k] {
				set[k] = true
				queue = append(queue, k)
			}
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, c := range children[k] {
			ck := SpanKey{c.TraceID, c.SpanID}
			if set[ck] || c.RunID != "" {
				continue
			}
			set[ck] = true
			queue = append(queue, ck)
		}
	}
	out := make([]SpanKey, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TraceID != out[j].TraceID {
			return out[i].TraceID < out[j].TraceID
		}
		return out[i].SpanID < out[j].SpanID
	})
	return out
}
