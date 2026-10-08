package obsdb

import (
	"hash/fnv"
	"strconv"
	"time"
)

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
	Pos                              int64 // event pos, messages index, delta pos, or request/prompt/tools index
	Step                             int   // -1 when absent
	ToolSeq                          int64 // -1 when absent
	Playground                       bool
	ExperimentID, ForkedFrom         string
	// Reason is a messages record's weft.messages.reason: "" for
	// transcript growth, "compacted" for a compaction view (ADR 0028
	// §8), which is never counted or concatenated as transcript.
	Reason string
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
	attrRequestIdx   = "weft.request.index" // ADR 0028: request, prompt and tools records
	attrPromptIdx    = "weft.prompt.index"  // are positioned by their own per-run index
	attrToolsIdx     = "weft.tools.index"
	attrStepIndex    = "weft.step.index"
	attrToolSeq      = "weft.tool.seq"
	attrPlayground   = "weft.playground"
	attrExperimentID = "weft.experiment.id"
	attrForkedFrom   = "weft.forked_from"

	// ADR 0028 §8: the compaction view and the session marker.
	attrMessagesReason  = "weft.messages.reason"
	attrMessagesCount   = "weft.messages.count"
	attrFromSeq         = "weft.messages.from_seq"
	attrToSeq           = "weft.messages.to_seq"
	attrCompactionHash  = "weft.compaction.hash"
	attrCompactionScope = "weft.compaction.scope"
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
// transcript records, the delta position for delta records, and the
// request, prompt or tools index for those kinds (ADR 0028) — the
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
		Reason:       attr(r.Attrs, attrMessagesReason),
	}
	switch {
	case hasIndex(r.Attrs, attrEventPos):
		w.Pos = int64(attrInt(r.Attrs, attrEventPos))
	case hasIndex(r.Attrs, attrMessagesIdx):
		w.Pos = int64(attrInt(r.Attrs, attrMessagesIdx))
	case hasIndex(r.Attrs, attrDeltaPos):
		w.Pos = int64(attrInt(r.Attrs, attrDeltaPos))
	case hasIndex(r.Attrs, attrRequestIdx):
		w.Pos = int64(attrInt(r.Attrs, attrRequestIdx))
	case hasIndex(r.Attrs, attrPromptIdx):
		w.Pos = int64(attrInt(r.Attrs, attrPromptIdx))
	case hasIndex(r.Attrs, attrToolsIdx):
		w.Pos = int64(attrInt(r.Attrs, attrToolsIdx))
	case w.Record == "messages":
		// A messages record without its index — a compaction view or a
		// growth record (ADR 0028 §8): only a malformed producer gets
		// here. -1 on both backends, as for ADR 0028's kinds — never
		// position 0, where it would collide with the input record and
		// one backend would keep it as the transcript's first batch
		// while the other dropped it. The transcript readers and the
		// messages count skip it; Compactions reports a view at -1 as
		// an error.
		w.Pos = -1
	case w.Record == RecordCompaction:
		// thread's session marker has no counter (ADR 0028 §8): its
		// position is derived from its hash, so a retried batch lands on
		// the same key and two compactions filed under one run do not.
		w.Pos = markerPos(attr(r.Attrs, attrCompactionHash))
	case w.Record == "request" || w.Record == "prompt" || w.Record == "tools":
		// One of ADR 0028's kinds without its index: only a malformed
		// producer gets here. -1 on both backends; duplicates collapse.
		w.Pos = -1
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

// hasIndex reports whether an index attribute is present and names a
// position: present with any non-string value, or a non-empty string.
// An empty string is absent — the reading ClickHouse's records view
// gives it (`!= ”`), so a malformed producer's record lands at -1 on
// both backends rather than at 0 on one.
func hasIndex(m map[string]any, k string) bool {
	v, ok := m[k]
	if !ok {
		return false
	}
	s, isString := v.(string)
	return !isString || s != ""
}

// attrInt reads an integer attribute; missing and non-numeric read 0.
func attrInt(m map[string]any, k string) int { return attrIntOr(m, k, 0) }

// attrIntOr reads an integer attribute with a default for absent. A
// numeric string counts as the number it spells: the core renders
// every metadata value as a string attribute (observe.go's
// attribute.String), and thread mints weft.turn through that metadata
// path (strconv.Itoa in turn.go) — so the string spelling is the one
// the pipeline actually produces, and the numeric spellings are what
// OTLP hand-built fixtures carry. ClickHouse's views read the same
// tolerance (toInt32OrZero over the stringified attrs).
func attrIntOr(m map[string]any, k string, def int) int {
	switch v := m[k].(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return int(n)
		}
	}
	return def
}

// attrBool reads a boolean attribute; anything but a present true is
// false. The string "true" counts as true for attrIntOr's reason: the
// flags weft's own modules set travel as run metadata (weft/runtime
// stamps "weft.playground": "true"), and the core renders every
// metadata value as a string attribute — so the string spelling is the
// one the pipeline produces and the bool the one fixtures hand-build.
// ClickHouse's views read the same pair (the stringified attr = 'true').
func attrBool(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
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
	"weft.messages.count":          {},
	"weft.messages.input":          {},
	"weft.content":                 {},
	"weft.content.truncated_bytes": {},
	"weft.version":                 {},
	"weft.manifest.hash":           {},
	"weft.run.steps":               {},
	"weft.run.pending":             {},
	"weft.run.stop_reason":         {},
	"weft.stop.raw":                {},
	"weft.model.tool_calls":        {},
	"weft.tool.approved":           {},
	"weft.tool.pending":            {},
	"weft.tool.result_bytes":       {},
	"weft.metadata.dropped":        {},
	"weft.override.hash":           {},
	// ADR 0028: the request record's keys.
	"weft.request.index":     {},
	"weft.prompt.index":      {},
	"weft.tools.index":       {},
	"weft.system.hash":       {},
	"weft.catalog.hash":      {},
	"weft.attempt.index":     {},
	"weft.instructions.hash": {},
	"weft.messages.reason":   {},
	"weft.messages.from_seq": {},
	"weft.messages.to_seq":   {},
	"weft.compaction.hash":   {},
	"weft.compaction.scope":  {},
	// Plan A4 (ADR 0016's A4 note): the step timing and the answering
	// model on the chat span and the step_finish record.
	"gen_ai.response.model": {},
	"weft.stream":           {},
	"weft.ttft_ms":          {},
	"weft.latency_ms":       {},
	// The attempt span's retry-after ask (ADR 0016's A8 note) and the
	// experiment fingerprint's fields (weft.override.*, ADR 0024 S1):
	// contract, never caller metadata — and not strings only, so left
	// out they would land in ClickHouse's meta (stringified) and not in
	// SQLite's (MetaOf keeps strings), the two backends disagreeing.
	"weft.attempt.retry_after_ms":              {},
	"weft.override.instructions":               {},
	"weft.override.max_steps":                  {},
	"weft.override.model":                      {},
	"weft.override.parallelism":                {},
	"weft.override.params":                     {},
	"weft.override.park_all_except":            {},
	"weft.override.park_on":                    {},
	"weft.override.thinking":                   {},
	"weft.override.tool_choice":                {},
	"weft.override.tools":                      {},
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
// core.Metadata put there, enduser.id included). Keys under "weft."
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

// markerPos is a session compaction marker's position: the first 60
// bits of its hash (lowercase hex). A hash that is absent or not hex
// gets the low 60 bits of its FNV-1a hash instead (0 only for the empty
// string), so two markers with distinct non-hex hashes keep distinct
// keys on SQLite as they do on ClickHouse, which dedupes by the hash
// string; 15 hex digits never exceed 2^60-1, so the position is never
// negative.
func markerPos(hash string) int64 {
	if hash == "" {
		return 0
	}
	if len(hash) >= 15 {
		if n, err := strconv.ParseInt(hash[:15], 16, 64); err == nil {
			return n
		}
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(hash))
	return int64(h.Sum64() & (1<<60 - 1))
}
