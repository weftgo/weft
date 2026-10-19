package obsdb

import (
	"fmt"
	"hash/fnv"
	"slices"
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
	// ResumedFrom is a resuming run's weft.resumed_from (core.ResumeOf,
	// UX-E-17): the parked run it resumes. On its invoke_agent span and
	// run_start record only.
	ResumedFrom string
	// CallerRunID / CallerCallID are a run's weft.caller.run.id /
	// weft.caller.call.id (O11, U14): the tool call another run was
	// started from when it is no core Subagent child — a thread/pool
	// child session's turn, a Generate inside a handler. A link, never
	// parenthood (ParentRunID is the Subagent child's alone). On the
	// invoke_agent span and run_start record only.
	CallerRunID, CallerCallID string
	// Reason is a messages record's weft.messages.reason: "" for
	// transcript growth, "compacted" for a compaction view (ADR 0028
	// §8), which is never counted or concatenated as transcript. A
	// non-string reason reads as its text (never "": the record is not
	// growth), as ClickHouse's stringified attribute column reads it.
	Reason string
	// Input is a messages record's weft.messages.input: the run's input
	// record (ADR 0028 §8) — what search reads the turn's prompt from.
	Input bool
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
	attrRawIdx       = "weft.raw.index" // ADR 0028 §12: the raw record's own per-run index
	attrStepIndex    = "weft.step.index"
	attrToolSeq      = "weft.tool.seq"
	attrPlayground   = "weft.playground"
	attrExperimentID = "weft.experiment.id"
	attrForkedFrom   = "weft.forked_from"
	attrResumedFrom  = "weft.resumed_from"
	attrCallerRunID  = "weft.caller.run.id"
	attrCallerCallID = "weft.caller.call.id"

	// ADR 0028 §8: the compaction view and the session marker.
	attrMessagesReason  = "weft.messages.reason"
	attrMessagesInput   = "weft.messages.input"
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
		ResumedFrom:  attr(s.Attrs, attrResumedFrom),
		CallerRunID:  attr(s.Attrs, attrCallerRunID),
		CallerCallID: attr(s.Attrs, attrCallerCallID),
	}
}

// DeriveRecord returns the weft identity a log record carries. Pos is
// the event position for event records, the messages index for
// transcript records, the delta position for delta records, and the
// request, prompt, tools or raw index for those kinds (ADR 0028) — the
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
		ResumedFrom:  attr(r.Attrs, attrResumedFrom),
		CallerRunID:  attr(r.Attrs, attrCallerRunID),
		CallerCallID: attr(r.Attrs, attrCallerCallID),
		Reason:       attrReason(r.Attrs),
		Input:        attrBool(r.Attrs, attrMessagesInput),
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
	case hasIndex(r.Attrs, attrRawIdx):
		w.Pos = int64(attrInt(r.Attrs, attrRawIdx))
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
	case w.Record == "event":
		// An event without its position: only a malformed producer
		// gets here. -1 — ClickHouse's records view has always read it
		// so — never position 0, where it would collide with the run's
		// run_start and one of the two be dropped. Events reads from
		// position 0, so it is stored, counted and never shown. (A
		// delta without its position stays at 0: it is never stored,
		// its only effect is the delta high-water mark, and the
		// ClickHouse runs view counts it at 0 too.)
		w.Pos = -1
	case w.Record == RecordCompaction:
		// thread's session marker has no counter (ADR 0028 §8): its
		// position is derived from its hash, so a retried batch lands on
		// the same key and two compactions filed under one run do not.
		w.Pos = markerPos(attr(r.Attrs, attrCompactionHash))
	case w.Record == RecordThread:
		// thread's session records (U12.1) have no counter either: the
		// position is derived from the body — every record names its
		// entry, kind and time, so two facts never share one and a
		// retried batch lands on the same key. Stored as a plain record;
		// no reader applies it to the run.
		w.Pos = markerPos(r.Body)
	case w.Record == "request" || w.Record == "prompt" || w.Record == "tools" || w.Record == RecordRaw:
		// One of ADR 0028's kinds without its index: only a malformed
		// producer gets here. -1 on both backends; duplicates collapse.
		w.Pos = -1
	}
	return w
}

// attrReason reads weft.messages.reason: missing is "" (growth), a
// string is itself, any other type its text — not growth, the reading
// the SQLite transcript reader (messagesFlags) and ClickHouse's
// stringified column give, so a malformed producer's view is never
// counted or indexed as transcript on one backend only.
func attrReason(m map[string]any) string {
	switch v := m[attrMessagesReason].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
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
	attrResumedFrom:                {}, // U13.1: the run row's resumed_from column
	"weft.resume.calls":            {}, // U13.1: a resume's decisions per parked call (on the spans)
	"weft.tool.denied":             {}, // U13.1: a denied call's execute_tool span
	"weft.tool.denied_kind":        {}, // U13.1: denied | no_decision
	attrCallerRunID:                {}, // U14 (O11): the run row's caller_run_id column
	attrCallerCallID:               {}, // U14 (O11): the run row's caller_call_id column
	"weft.messages.count":          {},
	"weft.messages.input":          {},
	"weft.content":                 {},
	"weft.content.truncated_bytes": {},
	"weft.version":                 {},
	"weft.manifest.hash":           {},
	"weft.run.steps":               {},
	"weft.run.pending":             {},
	"weft.run.stop_reason":         {},
	"weft.run.stopped_by":          {}, // U3.2: the run row's stopped_by column
	"weft.cancel.cause":            {}, // U3.2: the run row's cancel_cause column
	"weft.run.events":              {}, // U5: the run row's events_emitted column
	"weft.run.messages":            {}, // U5: the run row's messages_emitted column
	"weft.records.dropped":         {}, // U5-fix: a destination's loss count on run_start/run_finish (ingest health)
	"weft.run.denied":              {}, // U4: the run row's denied counter (Problems)
	"weft.tool.truncated_bytes":    {}, // U4: the truncated counter, the call's badge
	"weft.tool.input_error":        {}, // U4: which input rule an INVALID_INPUT broke
	"weft.panic.layer":             {}, // U4: a panic's layer, handler or middleware
	"weft.attempt.layer":           {}, // U4-fix: a fallback layer's attempt (the fallbacks counter)
	"weft.logs.bridged":            {}, // U11.1: the slog bridge's count on the heartbeat and the root span (the logs route's hint)
	"weft.tool.retry":              {}, // U32.1: a RETRY's consecutive count (the story's pill)
	"weft.tool.retry_budget":       {}, // U32.1: the run's MaxModelRetries beside it
	// U25: a card's facts — an attempt's wait and HTTP status, a call's
	// park source, deadline, queue wait and a parked child's calls, an
	// Output run's submission. Span attributes, not caller metadata.
	"weft.attempt.wait_ms":      {},
	"http.response.status_code": {},
	"weft.tool.pending.source":  {},
	"weft.tool.timeout_ms":      {},
	"weft.tool.queue_ms":        {},
	"weft.subagent.pending":     {},
	"weft.output.submitted":     {},
	// U12.1 (audit O15): weft/thread's summarizer chat spans' role.
	"weft.chat.role":         {},
	"weft.tap.panics":        {}, // U32.1: the run's contained observer panics (Problems)
	"weft.stop.raw":          {},
	"weft.model.tool_calls":  {},
	"weft.tool.approved":     {},
	"weft.tool.pending":      {},
	"weft.tool.result_bytes": {},
	"weft.metadata.dropped":  {},
	"weft.override.hash":     {},
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
	"weft.attempt.retry_after_ms":   {},
	"weft.override.instructions":    {},
	"weft.override.max_steps":       {},
	"weft.override.model":           {},
	"weft.override.parallelism":     {},
	"weft.override.params":          {},
	"weft.override.park_all_except": {},
	"weft.override.park_on":         {},
	"weft.override.thinking":        {},
	"weft.override.tool_choice":     {},
	"weft.override.tools":           {},
	// ADR 0028 §12: the raw record's position.
	"weft.raw.index":                           {},
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

// NonMetaKeys lists, sorted, the record/span contract keys MetaOf
// excludes from a run row's metadata — for a backend that must copy the
// set into its own query language (the ClickHouse views' filter) and
// test its copy whole.
func NonMetaKeys() []string {
	out := make([]string, 0, len(nonMetaAttr))
	for k := range nonMetaAttr {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// MetaOf returns the caller metadata a set of attributes carries: every
// string value under a key outside the record/span contract (what
// core.Metadata put there, enduser.id included). Keys under "weft."
// that are not part of the contract — thread's weft.session.parent, a
// future module's own — are metadata and are kept. An empty string is
// "none" (core.Metadata's rule: a session stamps its own identity keys,
// "" when it has none) and is never kept: a weft.session.parent = ""
// names no parent.
func MetaOf(attrs map[string]any) map[string]string {
	var meta map[string]string
	for k, v := range attrs {
		if _, contract := nonMetaAttr[k]; contract {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
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
