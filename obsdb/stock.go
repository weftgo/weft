package obsdb

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
)

// Deriving a run from stock spans (docs/record-contract.md §9): an
// application that exports stock OTel GenAI spans — a Python or
// TypeScript agent, no weft.* attribute anywhere — gets run rows too.
// DeriveSpan keeps reading such a span as non-weft for the columns it
// fills (every field absent: the span carries no weft identity); the
// stock facts are a trace-level reading instead, because a derived
// run's every field (its steps, its usage fallback, its parent) depends
// on the span tree, never on one span alone. Both backends call
// DeriveStockRuns on a trace's stored spans when a batch brings a stock
// candidate (StockCandidate) — re-deriving the whole trace, so a run
// whose spans arrive over several batches converges on the same row —
// and again at read time for a derived run's spans (StockRunSpans).

// StockErrNoDescription is a failed derived run's error when its root
// span's status is Error with no description (§9): a failed run never
// reads an empty error.
const StockErrNoDescription = "the root span's status is Error (no description)"

// stockPrefix opens every derived run id: otel-<trace id>-<root span id>.
const stockPrefix = "otel-"

// The GenAI operation names a derivation reads.
const (
	opInvokeAgent = "invoke_agent"
	opChat        = "chat"
	opExecuteTool = "execute_tool"
)

// attrConversationID is the GenAI conventions' conversation id: on a
// stock root it is what makes the span a run, and it is the run's
// session.
const attrConversationID = "gen_ai.conversation.id"

// stockIDPattern is a derived run id's exact shape: 32 and 16 lowercase
// hex digits, OTLP's trace and span ids.
var stockIDPattern = regexp.MustCompile(`^otel-([0-9a-f]{32})-([0-9a-f]{16})$`)

// StockRunID is the id of the run derived from the stock root span
// spanID of trace traceID: otel-<trace id>-<root span id>, lowercase
// hex — deterministic from the spans, so a retried export lands on the
// same row. It never contains "/" and cannot collide with an id the
// core generates (32 hex digits, a thread turn's -t<n>, a Subagent
// child's /); a caller-chosen core.RunID must not use the prefix (the
// record contract's §2 says so; nothing enforces it).
func StockRunID(traceID, spanID string) string {
	return stockPrefix + strings.ToLower(traceID) + "-" + strings.ToLower(spanID)
}

// ParseStockRunID splits a derived run id into its trace and root span
// ids; ok is false for any id not of StockRunID's exact shape — every
// weft run id among them.
func ParseStockRunID(id string) (traceID, spanID string, ok bool) {
	m := stockIDPattern.FindStringSubmatch(id)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// StockTraces is the trace ids the derived run ids among ids name, in
// order, once each: a deletion reads a derived run's spans by its
// trace, since none of them carries a run id (DeletedSpans).
func StockTraces(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if t, _, ok := ParseStockRunID(id); ok && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// stockOp is a span's GenAI operation: gen_ai.operation.name when set
// (an empty value is unset),
// else read from the name — a name starting with invoke_agent or
// execute_tool, or a name that is "chat" or starts with "chat " (the
// conventions' "{operation} {model}" span name; a bare prefix would
// take "chatbot" for a model call). "" for any other span.
func stockOp(s Span) string {
	if v, ok := s.Attrs["gen_ai.operation.name"].(string); ok && v != "" {
		return v
	}
	switch {
	case strings.HasPrefix(s.Name, opInvokeAgent):
		return opInvokeAgent
	case strings.HasPrefix(s.Name, opExecuteTool):
		return opExecuteTool
	case s.Name == opChat || strings.HasPrefix(s.Name, opChat+" "):
		return opChat
	}
	return ""
}

// IsStockRoot reports whether a span opens a derived run (§9): an
// invoke_agent span (by gen_ai.operation.name, or without it by a name
// starting with invoke_agent) with no weft.run.id and a
// gen_ai.conversation.id. An invoke_agent span without a conversation
// id is not a root: its spans count toward the enclosing root's run.
func IsStockRoot(s Span) bool {
	return stockOp(s) == opInvokeAgent && attr(s.Attrs, attrRunID) == "" && attr(s.Attrs, attrConversationID) != ""
}

// StockCandidate reports whether a span may take part in a derived run:
// it carries no weft.run.id (weft's own rows always win) and is one of
// the GenAI operations a derivation reads (invoke_agent, chat,
// execute_tool, by stockOp's rule). The writers re-derive a trace when
// a batch brings one: a trace of plain HTTP spans costs no read. A
// generic span between a root and its calls is part of the run's tree
// all the same; it arrives, like every child, before the root it sits
// under ends, and the root's batch re-derives the trace.
func StockCandidate(s Span) bool {
	if attr(s.Attrs, attrRunID) != "" {
		return false
	}
	switch stockOp(s) {
	case opInvokeAgent, opChat, opExecuteTool:
		return true
	}
	return false
}

// StockRun is one run derived from stock spans: its row and its spans.
//
// Row is the run row of §9: Derived true; ID StockRunID's; TraceID,
// Service, Agent and SessionID (the conversation id) the root's;
// Provider and Model the root's gen_ai.provider.name and
// gen_ai.request.model, else the first chat span's; Started and
// Finished the root's start and end; LastSeen the latest time in the
// subtree; Status StatusFailed with the root's status description as
// Err (StockErrNoDescription when it has none) when the root's status
// is Error, else StatusSucceeded — never
// running or interrupted: a span is exported only once it ended, so a
// stored root is a finished run, and there is no heartbeat to read a
// liveness from; Steps the subtree's chat span count; Usage the root's
// gen_ai.usage.* when it carries any (verbatim, taken as its own
// figure), else the sum over the subtree's chat spans; ParentRunID and
// ParentCallID, for a root nested in another root's subtree, that
// root's derived id and the gen_ai.tool.call.id of the nearest
// execute_tool span above it. Turn, PublicID, StopReason, Pending,
// Meta, the counts and the hashes are not derived (zero).
//
// Spans is the run's subtree — the root and every span under it by
// parent span id, down to (not into) a nested root or a span carrying
// weft.run.id — in start order (ties by span id), copied. The chat
// spans carry weft.step.index (their 0-based ordinal by start) and the
// execute_tool spans weft.tool.seq (their 1-based ordinal) and
// weft.step.index (the latest chat span that started before them;
// absent when none did): the reader's positions under weft's own keys,
// so every consumer of a run's spans (the waterfall, the tools list)
// reads one shape. The producer's attributes are otherwise as stored;
// the row's Derived flag (the derived badge) says who numbered them.
type StockRun struct {
	Row   RunRow
	Spans []Span
}

// DeriveStockRuns derives every run the spans hold (§9): one per stock
// root (IsStockRoot), its subtree the spans under it down to a nested
// root. A span is counted toward a root only through a complete chain
// of stored parents: one whose chain reaches a span not (yet) stored,
// a span with no parent, or a span carrying weft.run.id belongs to no
// derived run — so as later batches complete the tree a run's steps and
// usage only grow, and the whole set converges on the full tree's
// answer whatever order the spans arrived in. The spans may hold
// several traces; a duplicate (trace, span) is read once. The runs come
// back ordered by start, then id.
func DeriveStockRuns(spans []Span) []StockRun {
	byTrace := map[string]map[string]*Span{}
	var traces []string
	for i := range spans {
		s := &spans[i]
		idx, ok := byTrace[s.TraceID]
		if !ok {
			idx = map[string]*Span{}
			byTrace[s.TraceID] = idx
			traces = append(traces, s.TraceID)
		}
		if _, dup := idx[s.SpanID]; !dup {
			idx[s.SpanID] = s
		}
	}
	var out []StockRun
	for _, t := range traces {
		out = append(out, deriveTrace(byTrace[t])...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Row, out[j].Row
		if !a.Started.Equal(b.Started) {
			return a.Started.Before(b.Started)
		}
		return a.ID < b.ID
	})
	return out
}

// StockRunSpans is the spans of the derived run id among spans (one
// trace's, as DB.Trace reads them): StockRun.Spans, or nil when no root
// of that id is among them.
func StockRunSpans(spans []Span, id string) []Span {
	for _, r := range DeriveStockRuns(spans) {
		if r.Row.ID == id {
			return r.Spans
		}
	}
	return nil
}

// stockWalk is where a span's chain of stored parents leads: the
// nearest root above it (nil when the chain breaks first) and, on the
// way, the nearest execute_tool span.
type stockWalk struct {
	root *Span
	tool *Span
}

// walkUp follows s's parents to the nearest root. It stops, with no
// root, at a missing parent, a span with no parent, a span carrying
// weft.run.id, or after len(idx) steps (a cycle a malformed producer
// made).
func walkUp(idx map[string]*Span, s *Span) stockWalk {
	var w stockWalk
	cur := s
	for range len(idx) {
		if cur.ParentSpanID == "" {
			return stockWalk{}
		}
		p, ok := idx[cur.ParentSpanID]
		if !ok || attr(p.Attrs, attrRunID) != "" {
			return stockWalk{}
		}
		if IsStockRoot(*p) {
			w.root = p
			return w
		}
		if w.tool == nil && stockOp(*p) == opExecuteTool {
			w.tool = p
		}
		cur = p
	}
	return stockWalk{}
}

// deriveTrace derives one trace's runs from its spans by span id.
func deriveTrace(idx map[string]*Span) []StockRun {
	members := map[*Span][]*Span{}
	var roots []*Span
	for _, s := range idx {
		if IsStockRoot(*s) {
			roots = append(roots, s)
			continue
		}
		if attr(s.Attrs, attrRunID) != "" {
			continue
		}
		if w := walkUp(idx, s); w.root != nil {
			members[w.root] = append(members[w.root], s)
		}
	}
	out := make([]StockRun, 0, len(roots))
	for _, root := range roots {
		out = append(out, deriveRun(idx, root, members[root]))
	}
	return out
}

// deriveRun builds one derived run from its root and its members.
func deriveRun(idx map[string]*Span, root *Span, members []*Span) StockRun {
	all := append([]*Span{root}, members...)
	sort.Slice(all, func(i, j int) bool {
		if !all[i].Start.Equal(all[j].Start) {
			return all[i].Start.Before(all[j].Start)
		}
		return all[i].SpanID < all[j].SpanID
	})
	row := RunRow{
		ID:        StockRunID(root.TraceID, root.SpanID),
		Derived:   true,
		TraceID:   root.TraceID,
		Service:   root.Service,
		Agent:     attr(root.Attrs, attrAgentName),
		Provider:  attr(root.Attrs, "gen_ai.provider.name"),
		Model:     attr(root.Attrs, "gen_ai.request.model"),
		SessionID: attr(root.Attrs, attrConversationID),
		Started:   root.Start,
		Status:    StatusSucceeded,
	}
	if !root.End.IsZero() {
		end := root.End
		row.Finished = &end
	}
	if root.StatusCode == 2 {
		row.Status, row.Err = StatusFailed, root.StatusMessage
		if row.Err == "" {
			// A failed run always says why; a producer that set no
			// description says so in the reader's words.
			row.Err = StockErrNoDescription
		}
	}
	if w := walkUp(idx, root); w.root != nil {
		row.ParentRunID = StockRunID(w.root.TraceID, w.root.SpanID)
		if w.tool != nil {
			row.ParentCallID = attr(w.tool.Attrs, "gen_ai.tool.call.id")
		}
	}
	var chats []time.Time
	var tools int64
	var sum core.Usage
	spans := make([]Span, 0, len(all))
	for _, s := range all {
		for _, t := range []time.Time{s.Start, s.End} {
			if t.After(row.LastSeen) {
				row.LastSeen = t
			}
		}
		c := *s
		switch stockOp(*s) {
		case opChat:
			if len(chats) == 0 {
				// Field by field: the root's when it carries one.
				row.Provider = firstSet(row.Provider, attr(s.Attrs, "gen_ai.provider.name"))
				row.Model = firstSet(row.Model, attr(s.Attrs, "gen_ai.request.model"))
			}
			c.Attrs = withAttrs(s.Attrs, map[string]any{attrStepIndex: int64(len(chats))})
			chats = append(chats, s.Start)
			addUsage(&sum, s.Attrs)
		case opExecuteTool:
			tools++
			stamp := map[string]any{attrToolSeq: tools}
			// The latest chat span that started before it: chats holds
			// the starts in order.
			for i := len(chats) - 1; i >= 0; i-- {
				if chats[i].Before(s.Start) {
					stamp[attrStepIndex] = int64(i)
					break
				}
			}
			c.Attrs = withAttrs(s.Attrs, stamp)
		}
		spans = append(spans, c)
	}
	row.Steps = len(chats)
	if u, ok := usageOf(root.Attrs); ok {
		row.Usage = u
	} else {
		row.Usage = sum
	}
	return StockRun{Row: row, Spans: spans}
}

// withAttrs is a copy of attrs with add set over it.
func withAttrs(attrs, add map[string]any) map[string]any {
	out := make(map[string]any, len(attrs)+len(add))
	for k, v := range attrs {
		out[k] = v
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}

// The GenAI usage keys, in core.Usage's field order.
var usageKeys = [...]string{
	"gen_ai.usage.input_tokens",
	"gen_ai.usage.output_tokens",
	"gen_ai.usage.cache_read.input_tokens",
	"gen_ai.usage.cache_creation.input_tokens",
	"gen_ai.usage.reasoning.output_tokens",
}

// usageOf reads a span's gen_ai.usage.* keys; ok is false when it
// carries none (a numeric string counts, as attrIntOr reads it).
func usageOf(attrs map[string]any) (core.Usage, bool) {
	var vals [len(usageKeys)]int64
	ok := false
	for i, k := range usageKeys {
		if _, set := attrs[k]; set {
			ok = true
			vals[i] = int64(attrIntOr(attrs, k, 0))
		}
	}
	return core.Usage{
		InputTokens: vals[0], OutputTokens: vals[1],
		CachedInputTokens: vals[2], CacheWriteTokens: vals[3], ReasoningTokens: vals[4],
	}, ok
}

// addUsage adds a chat span's usage to sum.
func addUsage(sum *core.Usage, attrs map[string]any) {
	u, _ := usageOf(attrs)
	sum.InputTokens += u.InputTokens
	sum.OutputTokens += u.OutputTokens
	sum.CachedInputTokens += u.CachedInputTokens
	sum.CacheWriteTokens += u.CacheWriteTokens
	sum.ReasoningTokens += u.ReasoningTokens
}
