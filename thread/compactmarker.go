package thread

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"

	"github.com/weftgo/weft/core"
)

// The session compaction marker (ADR 0028 §8, session scope). The core
// knows nothing of a thread compaction: the run that starts on a
// compacted context carries it in its input record, literal, and the
// core emits no run-scope view for it. What tells a reader "the
// session compacted before this run" is this marker: one OTel log
// record of kind compaction, emitted by the session through the
// agent's LoggerProvider (the provider the run's own records use) when
// the first run after a compaction reports RunStart, under that run's
// id. It is informational: it carries counts and the compaction's
// hash, never messages, and no reader applies it.
const (
	markerEventName = "weft.compaction"
	markerRecord    = "compaction"
	markerScopeName = "github.com/weftgo/weft/thread"
)

// compactionMarker is the marker's body. Replaced and Entries are the
// compacted context's change in messages — the longest common prefix
// and suffix of the context before and after, by wire bytes (ADR 0028
// §8's rule, over the session's context instead of a run's transcript):
// Replaced messages of the old context gave way to Entries messages
// (the summary, and on a trim the stubbed results). The token counts
// are the session's estimates: the compaction's TokensBefore and the
// estimator over the context after.
type compactionMarker struct {
	Scope          string `json:"scope"`
	Hash           string `json:"hash"`
	Entry          string `json:"entry"`
	Reason         Reason `json:"reason"`
	Replaced       int    `json:"replaced"`
	Entries        int    `json:"entries"`
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
	TokensBefore   int64  `json:"tokens_before"`
	TokensAfter    int64  `json:"tokens_after"`
}

// newCompactionMarker builds the marker of the compaction e, given the
// session's context before and after it. Callers must not hold s.mu
// (the estimator is the caller's code).
func (s *Session) newCompactionMarker(e CompactionEntry, before, after []core.Message) *compactionMarker {
	m := &compactionMarker{
		Scope:          "session",
		Hash:           compactionEntryHash(e),
		Entry:          e.ID,
		Reason:         e.Reason,
		MessagesBefore: len(before),
		MessagesAfter:  len(after),
		TokensBefore:   e.TokensBefore,
		TokensAfter:    s.estimateAll(after),
	}
	m.Replaced, m.Entries = contextChange(before, after)
	return m
}

// compactionEntryHash is the session marker's weft.compaction.hash:
// sha256, lowercase hex, of the compaction entry's JSON encoding — the
// entry the session wrote, its id included, so two compactions never
// share a hash.
func compactionEntryHash(e CompactionEntry) string {
	b, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// contextChange counts what a compaction changed between two contexts:
// the messages between the longest common prefix and the longest
// common suffix (not overlapping it) of before, and of after.
func contextChange(before, after []core.Message) (replaced, entries int) {
	enc := func(m core.Message) []byte {
		b, _ := json.Marshal(m)
		return b
	}
	n, k := len(before), len(after)
	p := 0
	for p < n && p < k && bytes.Equal(enc(before[p]), enc(after[p])) {
		p++
	}
	sfx := 0
	for sfx < n-p && sfx < k-p && bytes.Equal(enc(before[n-1-sfx]), enc(after[k-1-sfx])) {
		sfx++
	}
	return n - p - sfx, k - p - sfx
}

// reportCompaction emits the pending marker, if any, under runID: the
// first run after a compaction calls it when its RunStart arrives, so
// the marker never names a run that did not start. md is the session
// identity the run carries (runMetadata). Observation only: a logger
// that panics or refuses changes nothing, and the marker is consumed
// either way.
func (s *Session) reportCompaction(ctx context.Context, runID string, md map[string]string) {
	var m *compactionMarker
	s.locked(func() { m, s.compactMarker = s.compactMarker, nil })
	if m == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Warn("thread: compaction marker dropped", "session", s.header.ID, "run", runID, "panic", p)
		}
	}()
	lp := s.agent.LoggerProvider()
	if lp == nil {
		return
	}
	l := lp.Logger(markerScopeName)
	if !l.Enabled(ctx, log.EnabledParameters{EventName: markerEventName}) {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetEventName(markerEventName)
	rec.SetSeverity(log.SeverityInfo)
	rec.SetBody(attribute.StringValue(string(b)))
	attrs := []attribute.KeyValue{
		attribute.String("weft.record", markerRecord),
		attribute.String("weft.run.id", runID),
		attribute.String("weft.compaction.scope", m.Scope),
		attribute.String("weft.compaction.hash", m.Hash),
		attribute.String("weft.content", "none"),
	}
	if name := s.agent.Name(); name != "" {
		attrs = append(attrs, attribute.String("gen_ai.agent.name", name))
	}
	for _, k := range slices.Sorted(maps.Keys(md)) {
		attrs = append(attrs, attribute.String(k, md[k]))
	}
	rec.AddAttributes(attrs...)
	l.Emit(ctx, rec)
}
