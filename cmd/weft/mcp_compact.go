package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Compact rows (I5, UX-H-26): the list tools — runs, search, sessions,
// session, live — answer a projection of the route's JSON, not the
// JSON itself, because a coding agent pays for every byte: a run row
// as served carries its trace id, three content hashes, empty parent
// and experiment fields and a cost reason in prose, ~1.5 KB a row.
// The projection is the MCP side's alone (no server logic, I4.1's
// rule) and says what it did in the tool's description and the
// instructions; full=true on the call answers the route's JSON as
// served.
//
//   - a field that is empty, zero, false or null is omitted (absent
//     reads as that); an array or object left empty is omitted too;
//   - a list row keeps its identity, outcome and size — id, parent,
//     agent, model as "provider/name", session and turn, status, err
//     (200 runes), steps, pending, stop_reason / stopped_by, the
//     resume links, usage, cost and its badge, the problem counters,
//     children counts, prompt_preview, started and duration_ms — and
//     drops the trace id, the manifest / instructions / catalog hashes,
//     the record counts, last_seen and meta (the run tool has them);
//   - a hash a row keeps (any *_hash) is cut to its first 12 characters;
//   - every cost reason (cost_reason, delegated_cost_reason, …) and
//     unpriced-model list is lifted out of the rows into one
//     cost_reasons list at the top of the answer, each reason once with
//     the number of rows that gave it — never one per row;
//   - in a session answer, a turn's agent, session_id and public_id
//     are omitted when they are the session's.

// rowDrop is the run-row fields a compact row leaves out.
var rowDrop = map[string]bool{
	"trace_id": true, "manifest_hash": true, "instructions_hash": true, "catalog_hash": true,
	"weft_version": true, "service": true, "last_seen": true, "meta": true,
	"event_count": true, "message_count": true, "delta_count": true,
	"events_emitted": true, "messages_emitted": true, "request_count": true,
	"cache": true, "cost_known": true, "delegated_cost_known": true, "finished": true,
}

// rowErrRunes bounds a compact row's err.
const rowErrRunes = 200

// hashRunes is a kept hash's length.
const hashRunes = 12

// compactList is the list answer b projected: rows under list (each a
// run row) compacted, the rest pruned, the cost reasons lifted to one
// cost_reasons list. A body that is not a JSON object comes back as it
// came. sameAs names top-level fields a row omits when equal (the
// session's agent, id → session_id, public_id).
func compactList(b []byte, list string, sameAs map[string]string) []byte {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return b
	}
	reasons := &costReasons{}
	same := map[string]any{}
	for rowKey, docKey := range sameAs {
		if v, ok := doc[docKey]; ok {
			same[rowKey] = v
		}
	}
	if rows, ok := doc[list].([]any); ok {
		for i, r := range rows {
			if m, ok := r.(map[string]any); ok {
				rows[i] = compactRow(m, same, reasons)
			}
		}
	}
	out := prune(doc, reasons)
	m, _ := out.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	if _, ok := m[list]; !ok {
		m[list] = []any{} // an empty page says so, never {}
	}
	if len(reasons.order) > 0 {
		m["cost_reasons"] = reasons.list()
	}
	return marshalCompact(m)
}

// compactRow is one run row compacted (see the top of this file).
func compactRow(m map[string]any, same map[string]any, reasons *costReasons) map[string]any {
	if st, ok := m["started"].(string); ok {
		if fin, ok := m["finished"].(string); ok {
			a, err1 := time.Parse(time.RFC3339Nano, st)
			z, err2 := time.Parse(time.RFC3339Nano, fin)
			if err1 == nil && err2 == nil && !z.Before(a) {
				m["duration_ms"] = json.Number(fmt.Sprint(z.Sub(a).Milliseconds()))
			}
		}
	}
	_, isRun := m["started"] // a session row has first_seen / last_seen, its only times: kept
	for k := range rowDrop {
		if k == "last_seen" && !isRun {
			continue
		}
		delete(m, k)
	}
	for k, v := range same {
		if fmt.Sprint(m[k]) == fmt.Sprint(v) {
			delete(m, k)
		}
	}
	if model, ok := m["model"].(map[string]any); ok {
		p, _ := model["provider"].(string)
		n, _ := model["name"].(string)
		switch {
		case p != "" && n != "":
			m["model"] = p + "/" + n
		case n != "":
			m["model"] = n
		default:
			delete(m, "model")
		}
	}
	if e, ok := m["err"].(string); ok {
		m["err"] = cutRunes(e, rowErrRunes)
	}
	pruned, _ := prune(m, reasons).(map[string]any)
	if pruned == nil {
		return map[string]any{}
	}
	return pruned
}

// prune drops empty, zero, false and null values (and the arrays and
// objects they leave empty), cuts kept hashes and lifts cost reasons
// into reasons. It returns nil for a value that is itself empty.
func prune(v any, reasons *costReasons) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool:
		if !x {
			return nil
		}
		return x
	case string:
		if x == "" {
			return nil
		}
		return x
	case json.Number:
		if f, err := x.Float64(); err == nil && f == 0 {
			return nil
		}
		return x
	case float64:
		if x == 0 {
			return nil
		}
		return x
	case int64:
		if x == 0 {
			return nil
		}
		return x
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if p := prune(e, reasons); p != nil {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		for k, e := range x {
			if isCostReason(k) {
				if s, ok := e.(string); ok && s != "" {
					reasons.add(s)
				}
				delete(x, k)
				continue
			}
			if strings.HasSuffix(k, "unpriced_models") {
				delete(x, k) // the reason names them
				continue
			}
			p := prune(e, reasons)
			if p == nil {
				delete(x, k)
				continue
			}
			if s, ok := p.(string); ok && strings.HasSuffix(k, "_hash") && utf8.RuneCountInString(s) > hashRunes {
				p = string([]rune(s)[:hashRunes])
			}
			x[k] = p
		}
		if len(x) == 0 {
			return nil
		}
		return x
	}
	return v
}

// isCostReason says a key is a cost reason the answer says once.
func isCostReason(k string) bool { return strings.HasSuffix(k, "cost_reason") }

// costReasons is the cost reasons an answer's rows gave, each once, in
// first-seen order, with how many rows gave it.
type costReasons struct {
	order []string
	n     map[string]int
}

func (c *costReasons) add(s string) {
	if c.n == nil {
		c.n = map[string]int{}
	}
	if c.n[s] == 0 {
		c.order = append(c.order, s)
	}
	c.n[s]++
}

// costReason is one lifted reason and the rows that gave it.
type costReason struct {
	Reason string `json:"reason"`
	Rows   int    `json:"rows"`
}

func (c *costReasons) list() []costReason {
	out := make([]costReason, 0, len(c.order))
	for _, s := range c.order {
		out = append(out, costReason{Reason: s, Rows: c.n[s]})
	}
	return out
}

// cutRunes is s cut to n runes, "…" marking a cut.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// marshalCompact is v as compact JSON with its keys sorted (Go's map
// order) and no HTML escaping.
func marshalCompact(v any) []byte {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if e.Encode(v) != nil {
		return nil
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// Compact spans (I5, UX-H-27): spans and trace answer each span as its
// name, run, ids, start offset and duration in ms from the answer's
// first span, status and the attributes that say what it was (model,
// tokens, tool, step, attempt, error, HTTP status, what stopped it),
// strings cut to 200 runes; content attributes (messages, system
// instructions, tool definitions, arguments and results) and span
// events are left out — full=true answers them.

// spanAttrKeep is the attribute keys (exact, or a prefix ending ".")
// a compact span keeps.
var spanAttrKeep = []string{
	"gen_ai.operation.name", "gen_ai.request.model", "gen_ai.response.model", "gen_ai.provider.name",
	"gen_ai.usage.", "gen_ai.tool.name", "gen_ai.tool.call.id", "gen_ai.agent.name",
	"gen_ai.response.finish_reasons", "error.type", "http.response.status_code",
	"weft.step", "weft.attempt.", "weft.run.stopped_by", "weft.cancel.cause", "weft.tool.approved_by",
	"weft.tool.denied", "weft.tool.denied_kind", "weft.tool.is_error", "weft.chat.role", "weft.parent.run.id",
}

// spanAttrDrop is the content attributes a compact span never keeps,
// whatever spanAttrKeep says.
var spanAttrDrop = []string{"gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions",
	"gen_ai.tool.definitions", "gen_ai.tool.call.arguments", "gen_ai.tool.call.result"}

const spanStrRunes = 200

// compactSpans is a spans document (spans, and any holes) with each
// span compacted; a body that does not decode comes back as it came.
// runID, when set, is the run the spans were asked for: a span's run_id
// is kept only when it is another run's (a tree's descendants).
func compactSpans(b []byte, runID string) []byte {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return b
	}
	list, _ := doc["spans"].([]any)
	var t0 time.Time
	for _, s := range list {
		if m, ok := s.(map[string]any); ok {
			if st, err := time.Parse(time.RFC3339Nano, fmt.Sprint(m["start"])); err == nil && (t0.IsZero() || st.Before(t0)) {
				t0 = st
			}
		}
	}
	out := make([]any, 0, len(list))
	for _, s := range list {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		c := map[string]any{"name": m["name"], "span_id": m["span_id"], "parent_span_id": m["parent_span_id"]}
		if r, _ := m["run_id"].(string); r != "" && r != runID {
			c["run_id"] = r
		}
		st, err1 := time.Parse(time.RFC3339Nano, fmt.Sprint(m["start"]))
		en, err2 := time.Parse(time.RFC3339Nano, fmt.Sprint(m["end"]))
		if err1 == nil {
			c["start_ms"] = st.Sub(t0).Milliseconds()
			if err2 == nil && !en.Before(st) {
				c["dur_ms"] = en.Sub(st).Milliseconds()
			}
		}
		if s, _ := m["status"].(string); s != "" && s != "unset" {
			c["status"] = s
		}
		if s, _ := m["status_message"].(string); s != "" {
			c["status_message"] = cutRunes(s, spanStrRunes)
		}
		if attrs, ok := m["attrs"].(map[string]any); ok {
			keep := map[string]any{}
			for k, v := range attrs {
				if !spanAttrKept(k) {
					continue
				}
				if s, ok := v.(string); ok {
					v = cutRunes(s, spanStrRunes)
				}
				keep[k] = v
			}
			if len(keep) > 0 {
				c["attrs"] = keep
			}
		}
		if ev, ok := m["events"].([]any); ok && len(ev) > 0 {
			c["events"] = len(ev)
		}
		if p := prune(c, &costReasons{}); p != nil {
			out = append(out, p)
		}
	}
	doc["spans"] = out
	if !t0.IsZero() {
		doc["t0"] = t0.UTC().Format(time.RFC3339Nano)
	}
	if h, ok := doc["holes"].([]any); ok && len(h) == 0 {
		delete(doc, "holes")
	}
	return marshalCompact(doc)
}

func spanAttrKept(k string) bool {
	if slices.Contains(spanAttrDrop, k) {
		return false
	}
	for _, p := range spanAttrKeep {
		if k == p || (strings.HasSuffix(p, ".") && strings.HasPrefix(k, p)) {
			return true
		}
	}
	return false
}
