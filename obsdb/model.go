package obsdb

import "time"

// Span is one finished span, OTLP-shaped.
type Span struct {
	TraceID, SpanID, ParentSpanID string // lowercase hex
	Name                          string
	Kind                          int // OTLP SpanKind
	Start, End                    time.Time
	StatusCode                    int // 0 unset, 1 ok, 2 error
	StatusMessage                 string
	Service                       string         // resource service.name
	Attrs                         map[string]any // string, bool, int64, float64, []any
	Resource                      map[string]any
	Events                        []SpanEvent // exceptions etc.
}

// SpanEvent is one event recorded on a span (an exception, a marker).
type SpanEvent struct {
	Time  time.Time
	Name  string
	Attrs map[string]any
}

// Record is one log record, OTLP-shaped.
type Record struct {
	Time, Observed  time.Time
	TraceID, SpanID string
	Severity        int
	EventName       string
	Body            string
	Service         string
	Attrs           map[string]any
	Resource        map[string]any
}

// Batch is one write: spans and records as ingested, in arrival order.
// Write is idempotent on (run, record kind, pos) and (trace, span), so
// a retried transport's duplicate is a no-op.
type Batch struct {
	Spans   []Span
	Records []Record
}

// Weft is what obsdb derives from a span or record's attributes, stored
// as indexed columns. Zero values mean absent.
type Weft struct {
	RunID, ParentRunID, ParentCallID string
	SessionID, PublicID              string
	Turn                             int
	Agent                            string
	Record                           string // event | delta | messages | heartbeat | "" (non-weft)
	EventType                        string
	Pos                              int64 // event pos, messages index, or delta pos
	Step                             int   // -1 when absent
	ToolSeq                          int64 // -1 when absent
	Playground                       bool
	ExperimentID, ForkedFrom         string
}

// The identity-chain and record attribute names (ADR 0024's record
// contract and §5's chain). Pinned by tests; changing one is a schema
// change, versioned by obsdb_migrations.
const (
	attrRunID        = "weft.run.id"
	attrParentRunID  = "weft.parent.run.id"
	attrParentCallID = "weft.parent.call.id"
	attrSessionID    = "weft.session.id"
	attrPublicID     = "weft.public_id"
	attrTurn         = "weft.turn"
	attrAgentName    = "gen_ai.agent.name"
	attrRecord       = "weft.record"
	attrEventType    = "weft.event.type"
	attrEventPos     = "weft.event.pos"
	attrDeltaPos     = "weft.delta.pos"
	attrMessagesIdx  = "weft.messages.index"
	attrStepIndex    = "weft.step.index"
	attrToolSeq      = "weft.tool.seq"
	attrPlayground   = "weft.playground"
	attrExperimentID = "weft.experiment.id"
	attrForkedFrom   = "weft.forked_from"
)

// DeriveSpan returns the weft identity a span carries, from its
// attributes. A span with no weft.run.id is non-weft (a stock OTel
// application's span): every field reads absent.
func DeriveSpan(s Span) Weft {
	return Weft{
		RunID:        attr(s.Attrs, attrRunID),
		ParentRunID:  attr(s.Attrs, attrParentRunID),
		ParentCallID: attr(s.Attrs, attrParentCallID),
		SessionID:    attr(s.Attrs, attrSessionID),
		PublicID:     attr(s.Attrs, attrPublicID),
		Turn:         attrInt(s.Attrs, attrTurn),
		Agent:        attr(s.Attrs, attrAgentName),
		Step:         attrIntOr(s.Attrs, attrStepIndex, -1),
		ToolSeq:      int64(attrIntOr(s.Attrs, attrToolSeq, -1)),
		Playground:   attrBool(s.Attrs, attrPlayground),
		ExperimentID: attr(s.Attrs, attrExperimentID),
		ForkedFrom:   attr(s.Attrs, attrForkedFrom),
	}
}

// DeriveRecord returns the weft identity a log record carries. Pos is
// the event position for event records, the messages index for
// transcript records, and the delta position for delta records — the
// value the (run, kind, pos) key and the live lane's dedup need. A
// record with no weft.run.id is non-weft and lands in other_logs.
func DeriveRecord(r Record) Weft {
	w := Weft{
		RunID:        attr(r.Attrs, attrRunID),
		ParentRunID:  attr(r.Attrs, attrParentRunID),
		ParentCallID: attr(r.Attrs, attrParentCallID),
		SessionID:    attr(r.Attrs, attrSessionID),
		PublicID:     attr(r.Attrs, attrPublicID),
		Turn:         attrInt(r.Attrs, attrTurn),
		Agent:        attr(r.Attrs, attrAgentName),
		Record:       attr(r.Attrs, attrRecord),
		EventType:    attr(r.Attrs, attrEventType),
		Step:         attrIntOr(r.Attrs, attrStepIndex, -1),
		ToolSeq:      int64(attrIntOr(r.Attrs, attrToolSeq, -1)),
		Playground:   attrBool(r.Attrs, attrPlayground),
		ExperimentID: attr(r.Attrs, attrExperimentID),
		ForkedFrom:   attr(r.Attrs, attrForkedFrom),
	}
	switch {
	case has(r.Attrs, attrEventPos):
		w.Pos = int64(attrInt(r.Attrs, attrEventPos))
	case has(r.Attrs, attrMessagesIdx):
		w.Pos = int64(attrInt(r.Attrs, attrMessagesIdx))
	case has(r.Attrs, attrDeltaPos):
		w.Pos = int64(attrInt(r.Attrs, attrDeltaPos))
	}
	return w
}

// attr reads a string attribute; missing and non-string read as "".
func attr(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// has reports whether k is present, whatever its value's type.
func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }

// attrInt reads an integer attribute; missing and non-numeric read 0.
func attrInt(m map[string]any, k string) int { return attrIntOr(m, k, 0) }

// attrIntOr reads an integer attribute with a default for absent.
func attrIntOr(m map[string]any, k string, def int) int {
	switch v := m[k].(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}

// attrBool reads a boolean attribute; anything but a present true is
// false.
func attrBool(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

// nonMetaAttr names every attribute key that is part of the record and
// span contract (the identity chain, the counters, the semconv fields
// and the mirrors the core adds) rather than caller metadata. Backends
// use it through MetaOf to decide what lands in a run row's meta
// column. Mirrors (session.id, user.id, gen_ai.conversation.id) are
// excluded because their originals (weft.session.id, enduser.id) are
// identity or metadata already.
var nonMetaAttr = map[string]struct{}{
	attrRunID: {}, attrParentRunID: {}, attrParentCallID: {},
	attrSessionID: {}, attrPublicID: {}, attrTurn: {}, attrAgentName: {},
	attrRecord: {}, attrEventType: {}, attrEventPos: {}, attrDeltaPos: {},
	attrMessagesIdx: {}, attrStepIndex: {}, attrToolSeq: {},
	attrPlayground: {}, attrExperimentID: {}, attrForkedFrom: {},
	"weft.messages.count":                      {},
	"weft.messages.input":                      {},
	"weft.content":                             {},
	"weft.content.truncated_bytes":             {},
	"weft.version":                             {},
	"weft.manifest.hash":                       {},
	"weft.run.steps":                           {},
	"weft.run.pending":                         {},
	"weft.run.stop_reason":                     {},
	"weft.stop.raw":                            {},
	"weft.model.tool_calls":                    {},
	"weft.tool.approved":                       {},
	"weft.tool.pending":                        {},
	"weft.tool.result_bytes":                   {},
	"weft.metadata.dropped":                    {},
	"weft.override.hash":                       {},
	"gen_ai.operation.name":                    {},
	"gen_ai.provider.name":                     {},
	"gen_ai.request.model":                     {},
	"gen_ai.response.finish_reasons":           {},
	"gen_ai.usage.input_tokens":                {},
	"gen_ai.usage.output_tokens":               {},
	"gen_ai.usage.cache_read.input_tokens":     {},
	"gen_ai.usage.cache_creation.input_tokens": {},
	"gen_ai.usage.reasoning.output_tokens":     {},
	"gen_ai.tool.name":                         {},
	"gen_ai.tool.call.id":                      {},
	"gen_ai.conversation.id":                   {},
	"session.id":                               {},
	"user.id":                                  {},
	"error.type":                               {},
}

// MetaOf returns the caller metadata a set of attributes carries: every
// string value under a key outside the record/span contract (what
// weft.Metadata put there, enduser.id included). Keys under "weft."
// that are not part of the contract — thread's weft.session.parent, a
// future module's own — are metadata and are kept.
func MetaOf(attrs map[string]any) map[string]string {
	var meta map[string]string
	for k, v := range attrs {
		if _, contract := nonMetaAttr[k]; contract {
			continue
		}
		if s, ok := v.(string); ok {
			if meta == nil {
				meta = map[string]string{}
			}
			meta[k] = s
		}
	}
	return meta
}
