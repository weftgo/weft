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

// attrOperation is the GenAI conventions' operation attribute.
const attrOperation = "gen_ai.operation.name"

// The GenAI operation names a derivation reads.
const (
	opInvokeAgent = "invoke_agent"
	opChat        = "chat"
	opExecuteTool = "execute_tool"
)

// attrConversationID is the GenAI conventions' conversation id: on a
// stock root it is the run's session (none without it).
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

// IsStockRoot reports whether a span opens a derived run on its own
// (§9): an invoke_agent span (by gen_ai.operation.name, or without it
// by a name starting with invoke_agent) with no weft.run.id and a
// gen_ai.conversation.id — the run's session. An invoke_agent span
// without a conversation id opens a run (with no session) only when no
// other invoke_agent span is stored above it in its trace — a
// top-level agent — which DeriveStockRuns decides from the trace; a
// nested one folds into the enclosing run.
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
// Service, Agent and SessionID (the conversation id, "" when the root carries none) the root's;
// Provider and Model the root's gen_ai.provider.name and
// gen_ai.request.model, else the first chat span's; Started and
// Finished the root's start and end; LastSeen the latest time in the
// subtree; Status StatusFailed with the root's status description as
// Err (StockErrNoDescription when it has none) when the root's status
// is Error, else StatusSucceeded — never
// running or interrupted: a span is exported only once it ended, so a
// stored root is a finished run, and there is no heartbeat to read a
// liveness from; Steps the subtree's chat span count; MessageCount the
// subtree's chat spans that carry the conventions' message content
// (StockHasMessages: each a batch of the messages the model was sent and
// answered, as a messages record is a weft run's); Usage the root's
// gen_ai.usage.* when it carries any (verbatim, taken as its own
// figure), else the sum over the subtree's chat spans; ParentRunID and
// ParentCallID, for a root nested in another root's subtree, that
// root's derived id and the gen_ai.tool.call.id of the nearest
// execute_tool span above it. Turn, PublicID, StopReason, Pending,
// Meta, the other counts and the hashes are not derived (zero).
//
// Spans is the run's subtree — the root and every span under it by
// parent span id, down to (not into) a nested root or a span carrying
// weft.run.id — in start order (ties by span id), copied. The chat
// spans carry weft.step.index (their 0-based ordinal by start) and the
// execute_tool spans weft.tool.seq (their 1-based ordinal) and
// weft.step.index (the latest chat span that started before them;
// absent when none did): the reader's positions under weft's own keys,
// so every consumer of a run's spans (the waterfall, the tools list)
// reads one shape — and gen_ai.operation.name on both, the operation
// stockOp resolved (from the span name when the producer set none or
// set it empty), so a reader that switches on the attribute sees what
// the derivation saw. The producer's attributes are otherwise as stored;
// the row's Derived flag (the derived badge) says who numbered them.
type StockRun struct {
	Row   RunRow
	Spans []Span
}

// DeriveStockRuns derives every run the spans hold (§9): one per stock
// root (IsStockRoot, or a top-level invoke_agent span without a
// conversation id), its subtree the spans under it down to a nested
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

// stockWalker answers walkUp for one trace's spans, each span's walk
// memoised: a span's walk is its parent's plus one hop, so a trace is
// walked in O(n) whatever its depth — a deep chain re-derived on every
// ingest batch (inside SQLite's write transaction) would otherwise cost
// O(n·depth). steps counts the parent hops taken (the tests' bound).
type stockWalker struct {
	idx   map[string]*Span
	memo  map[*Span]stockWalk
	busy  map[*Span]bool
	above map[*Span]bool
	steps int
}

func newStockWalker(idx map[string]*Span) *stockWalker {
	return &stockWalker{idx: idx, memo: make(map[*Span]stockWalk, len(idx)), busy: map[*Span]bool{},
		above: map[*Span]bool{}}
}

// isRoot reports whether s opens a derived run in this trace (§9):
// IsStockRoot's, or a non-weft invoke_agent span without a conversation
// id that has no invoke_agent span stored above it (agentAbove) — a
// top-level agent, a run with no session. Its parent not stored (yet),
// such an agent is top-level for now: should an enclosing agent arrive
// later, the agent folds into its run and the writers drop the row it
// had (the trace re-derived whole).
func (w *stockWalker) isRoot(s *Span) bool {
	if stockOp(*s) != opInvokeAgent || attr(s.Attrs, attrRunID) != "" {
		return false
	}
	return attr(s.Attrs, attrConversationID) != "" || !w.agentAbove(s)
}

// agentAbove reports whether a non-weft invoke_agent span is among s's
// stored ancestors. The climb stops at a missing parent, a span with no
// parent, a span carrying weft.run.id (weft's own rows win: a stock
// agent under a weft run's tool is top-level) or a cycle; every span on
// the path shares the answer (its nearest agent above is the same), so
// each is memoised and a trace costs O(n) hops.
func (w *stockWalker) agentAbove(s *Span) bool {
	if r, ok := w.above[s]; ok {
		return r
	}
	path := []*Span{s}
	seen := map[*Span]bool{s: true}
	found := false
	for cur := s; ; {
		if cur.ParentSpanID == "" {
			break
		}
		p, ok := w.idx[cur.ParentSpanID]
		if !ok || attr(p.Attrs, attrRunID) != "" {
			break
		}
		w.steps++
		if stockOp(*p) == opInvokeAgent {
			found = true
			break
		}
		if r, ok := w.above[p]; ok {
			found = r
			break
		}
		if seen[p] {
			break // a cycle: no agent above
		}
		seen[p] = true
		path = append(path, p)
		cur = p
	}
	for _, sp := range path {
		w.above[sp] = found
	}
	return found
}

// walkUp follows s's parents to the nearest root. It stops, with no
// root, at a missing parent, a span with no parent or a span carrying
// weft.run.id; a cycle a malformed producer made reaches no root either
// (a root ends a walk, so a cycle that reaches one is no cycle walked).
func (w *stockWalker) walkUp(s *Span) stockWalk {
	if r, ok := w.memo[s]; ok {
		return r
	}
	// Climb to the first span whose walk is known or decided by its
	// parent alone, then unwind: each span's walk is its parent's,
	// with the parent as the nearest execute_tool when it is one.
	path := []*Span{s}
	w.busy[s] = true
	var last stockWalk
	for cur := s; ; {
		if cur.ParentSpanID == "" {
			break // last: no root
		}
		p, ok := w.idx[cur.ParentSpanID]
		if !ok || attr(p.Attrs, attrRunID) != "" {
			break
		}
		w.steps++
		if w.isRoot(p) {
			last = stockWalk{root: p}
			break
		}
		if r, ok := w.memo[p]; ok {
			last = hop(p, r)
			break
		}
		if w.busy[p] {
			break // a cycle: no root
		}
		w.busy[p] = true
		path = append(path, p)
		cur = p
	}
	w.memo[path[len(path)-1]] = last
	for i := len(path) - 2; i >= 0; i-- {
		w.memo[path[i]] = hop(path[i+1], w.memo[path[i+1]])
	}
	for _, sp := range path {
		delete(w.busy, sp)
	}
	return w.memo[s]
}

// hop is the walk of a span whose parent p (not a root) walks r.
func hop(p *Span, r stockWalk) stockWalk {
	if r.root == nil {
		return stockWalk{}
	}
	if stockOp(*p) == opExecuteTool {
		r.tool = p
	}
	return r
}

// deriveTrace derives one trace's runs from its spans by span id.
func deriveTrace(idx map[string]*Span) []StockRun {
	out, _ := deriveTraceCounted(idx)
	return out
}

// deriveTraceCounted is deriveTrace with the parent hops its walks took.
func deriveTraceCounted(idx map[string]*Span) ([]StockRun, int) {
	w := newStockWalker(idx)
	members := map[*Span][]*Span{}
	var roots []*Span
	for _, s := range idx {
		if w.isRoot(s) {
			roots = append(roots, s)
			continue
		}
		if attr(s.Attrs, attrRunID) != "" {
			continue
		}
		if r := w.walkUp(s); r.root != nil {
			members[r.root] = append(members[r.root], s)
		}
	}
	out := make([]StockRun, 0, len(roots))
	for _, root := range roots {
		out = append(out, deriveRun(w, root, members[root]))
	}
	return out, w.steps
}

// deriveRun builds one derived run from its root and its members.
func deriveRun(w *stockWalker, root *Span, members []*Span) StockRun {
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
	if up := w.walkUp(root); up.root != nil {
		row.ParentRunID = StockRunID(up.root.TraceID, up.root.SpanID)
		if up.tool != nil {
			row.ParentCallID = attr(up.tool.Attrs, "gen_ai.tool.call.id")
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
			c.Attrs = withAttrs(s.Attrs, map[string]any{attrStepIndex: int64(len(chats)), attrOperation: opChat})
			chats = append(chats, s.Start)
			addUsage(&sum, s.Attrs)
			if StockHasMessages(s.Attrs) {
				row.MessageCount++
			}
		case opExecuteTool:
			tools++
			stamp := map[string]any{attrToolSeq: tools, attrOperation: opExecuteTool}
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
