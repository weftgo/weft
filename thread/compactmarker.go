package thread

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
)

// The session compaction marker (ADR 0028 §8, session scope). The core
// knows nothing of a thread compaction: the run that starts on a
// compacted context carries it in its input record, literal, and the
// core emits no run-scope view for it. What tells a reader "the
// session compacted here" is this marker: one OTel log record of kind
// compaction, emitted through the agent's LoggerProvider (the provider
// the runs' own records use) when the compaction lands, under the id
// of the last run that produced the compacted context — so it is in
// the sink at once, and every compaction emits one. A compaction of a
// context no run produced (entries appended by hand) waits for the
// next run this Session drives and is emitted under it; a Close before
// that drops it with a Debug line. It is informational: counts and the
// compaction's hash, never messages, and no reader applies it.
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
// estimator over the context after (0 when the estimator failed).
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

// runSight is what the Session last saw of a run it drove: its id, the
// span context of its invoke_agent span and the run's merged metadata
// (the session identity and the caller's thread.RunOptions metadata).
type runSight struct {
	id string
	sc trace.SpanContext
	md map[string]string
}

// seeRun records the run reporting through the Session's observer and
// emits, under it, the markers that were waiting for a run. Called on
// every observed batch; cheap when nothing waits.
func (s *Session) seeRun(rctx context.Context, runID string) {
	var pending []*compactionMarker
	s.locked(func() {
		if s.lastRun.id != runID {
			s.lastRun = runSight{id: runID, sc: trace.SpanContextFromContext(rctx), md: core.MetadataFromContext(rctx)}
		}
		pending, s.pendingMarkers = s.pendingMarkers, nil
	})
	for _, m := range pending {
		s.emitMarker(rctx, runID, nil, m)
	}
}

// lastRunIDLocked is the id of the last run that produced the leaf's
// context: the newest message or turn entry on the path carrying one.
// "" when none does. Callers hold s.mu.
func (s *Session) lastRunIDLocked() string {
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return ""
	}
	for i := len(path) - 1; i >= 0; i-- {
		switch e := path[i].(type) {
		case MessageEntry:
			if e.RunID != "" {
				return e.RunID
			}
		case TurnEntry:
			if e.RunID != "" {
				return e.RunID
			}
		}
	}
	return ""
}

// reportCompaction builds and emits the marker of the compaction e
// under runID (the last run that produced the context), or holds it
// for the next run when there is none. Nothing is built — the
// estimator not called — unless a destination wants the record.
// Callers must not hold s.mu. Observation only: a logger or estimator
// that panics changes nothing.
func (s *Session) reportCompaction(ctx context.Context, e CompactionEntry, before, after []core.Message, runID string) {
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Debug("thread: compaction marker dropped", "session", s.header.ID, "panic", fmt.Sprint(p))
		}
	}()
	l := s.markerLogger()
	if l == nil || !l.Enabled(ctx, log.EnabledParameters{EventName: markerEventName}) {
		return
	}
	m := &compactionMarker{
		Scope:          "session",
		Hash:           compactionEntryHash(e),
		Entry:          e.ID,
		Reason:         e.Reason,
		MessagesBefore: len(before),
		MessagesAfter:  len(after),
		TokensBefore:   e.TokensBefore,
		TokensAfter:    s.safeEstimate(after),
	}
	m.Replaced, m.Entries = contextChange(before, after)
	if runID == "" {
		s.locked(func() { s.pendingMarkers = append(s.pendingMarkers, m) })
		return
	}
	var sight runSight
	s.locked(func() { sight = s.lastRun })
	ectx := trace.ContextWithSpanContext(ctx, trace.SpanContext{}) // never the caller's span
	md := map[string]string{"weft.session.id": s.header.ID}
	if sight.id == runID {
		ectx = trace.ContextWithSpanContext(ctx, sight.sc)
		md = sight.md
	}
	s.emitMarker(ectx, runID, md, m)
}

// safeEstimate is the estimator over msgs with its panic contained: 0
// and a Debug line.
func (s *Session) safeEstimate(msgs []core.Message) (n int64) {
	defer func() {
		if p := recover(); p != nil {
			n = 0
			s.agent.Logger().Debug("thread: estimator panicked; the compaction marker reports no tokens after",
				"session", s.header.ID, "panic", fmt.Sprint(p))
		}
	}()
	return s.estimateAll(msgs)
}

func (s *Session) markerLogger() log.Logger {
	lp := s.agent.LoggerProvider()
	if lp == nil {
		return nil
	}
	return lp.Logger(markerScopeName)
}

// emitMarker writes one marker record under runID on ctx. md is the
// metadata to stamp; nil reads the run's own from ctx (a run's
// observer context).
func (s *Session) emitMarker(ctx context.Context, runID string, md map[string]string, m *compactionMarker) {
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Debug("thread: compaction marker dropped", "session", s.header.ID, "run", runID, "panic", fmt.Sprint(p))
		}
	}()
	l := s.markerLogger()
	if l == nil || !l.Enabled(ctx, log.EnabledParameters{EventName: markerEventName}) {
		return
	}
	if md == nil {
		md = core.MetadataFromContext(ctx)
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
		switch k {
		case "weft.record", "weft.run.id", "weft.compaction.scope", "weft.compaction.hash", "weft.content", "gen_ai.agent.name":
			continue // the record's own attributes win
		}
		attrs = append(attrs, attribute.String(k, md[k]))
	}
	rec.AddAttributes(attrs...)
	l.Emit(ctx, rec)
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
	n, k := len(before), len(after)
	p := 0
	for p < n && p < k && bytes.Equal(wireOf(before[p]), wireOf(after[p])) {
		p++
	}
	sfx := 0
	for sfx < n-p && sfx < k-p && bytes.Equal(wireOf(before[n-1-sfx]), wireOf(after[k-1-sfx])) {
		sfx++
	}
	return n - p - sfx, k - p - sfx
}

// wireOf is a message's wire JSON; a message whose tool-call arguments
// are not JSON (the one way the encoding fails) is encoded with those
// arguments quoted as a JSON string, the core records' rule — never
// nil, so two such messages are not taken for equal.
func wireOf(m core.Message) []byte {
	b, err := json.Marshal(m)
	if err == nil {
		return b
	}
	q := m
	q.Content = make([]core.Part, len(m.Content))
	for i, p := range m.Content {
		if c, ok := p.(core.ToolCallPart); ok {
			switch {
			case len(bytes.TrimSpace(c.Args)) == 0:
				c.Args = nil
			case !json.Valid(c.Args):
				c.Args, _ = json.Marshal(string(c.Args))
			}
			p = c
		}
		q.Content[i] = p
	}
	b, _ = json.Marshal(q)
	return b
}
