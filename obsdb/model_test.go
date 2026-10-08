package obsdb

import (
	"encoding/json"
	"testing"
	"time"
)

// The attribute names DeriveSpan/DeriveRecord read are the record
// contract (ADR 0024): pinned here so a rename cannot pass silently.
func TestDeriveSpanAttributes(t *testing.T) {
	s := Span{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7",
		Name: "invoke_agent support", Kind: 1,
		Start: time.Now(), End: time.Now().Add(time.Second),
		Attrs: map[string]any{
			"weft.run.id":            "s_01M3-t3",
			"weft.parent.run.id":     "s_01M3-t2",
			"weft.parent.call.id":    "call_9",
			"weft.session.id":        "s_01M3",
			"weft.public_id":         "pub_7Hk2",
			"weft.turn":              int64(3),
			"gen_ai.agent.name":      "support",
			"weft.step.index":        int64(2),
			"weft.tool.seq":          int64(7),
			"weft.playground":        true,
			"weft.experiment.id":     "exp_1",
			"weft.forked_from":       "s_01M3-t2#4",
			"gen_ai.operation.name":  "invoke_agent",
			"gen_ai.conversation.id": "s_01M3", // mirrors are not identity sources
			"session.id":             "s_01M3",
		},
	}
	w := DeriveSpan(s)
	want := Weft{
		RunID: "s_01M3-t3", ParentRunID: "s_01M3-t2", ParentCallID: "call_9",
		SessionID: "s_01M3", PublicID: "pub_7Hk2", Turn: 3, Agent: "support",
		Step: 2, ToolSeq: 7, Playground: true,
		ExperimentID: "exp_1", ForkedFrom: "s_01M3-t2#4",
	}
	if w != want {
		t.Fatalf("DeriveSpan = %+v, want %+v", w, want)
	}
}

// A span without weft.run.id is non-weft: no run identity, no session,
// no positions. The agent name alone is still derived (a polyglot
// GenAI span gets its label), but OTel ids and semconv mirrors must
// never fabricate run identity (rule I1).
func TestDeriveSpanNonWeft(t *testing.T) {
	w := DeriveSpan(Span{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		Name:    "chat glm-5.3-flash",
		Attrs:   map[string]any{"gen_ai.operation.name": "chat", "gen_ai.agent.name": "stock", "gen_ai.conversation.id": "conv9", "session.id": "conv9"},
	})
	if w.RunID != "" || w.SessionID != "" || w.PublicID != "" || w.ParentRunID != "" || w.Step != -1 || w.ToolSeq != -1 {
		t.Fatalf("non-weft span derived run identity: %+v", w)
	}
	if w.Agent != "stock" {
		t.Errorf("agent = %q, want the derivable gen_ai.agent.name", w.Agent)
	}
}

// Record derivation: the kind from weft.record, the position from the
// counter that belongs to the kind, the mirrors ignored.
func TestDeriveRecordKindAndPosition(t *testing.T) {
	base := map[string]any{
		"weft.run.id": "r1", "weft.session.id": "s1", "weft.public_id": "pub1",
		"weft.turn": int64(1), "gen_ai.agent.name": "a",
	}
	for _, tc := range []struct {
		name   string
		attrs  map[string]any
		record string
		pos    int64
	}{
		{"event", map[string]any{"weft.record": "event", "weft.event.type": "run_start", "weft.event.pos": int64(0)}, "event", 0},
		{"messages", map[string]any{"weft.record": "messages", "weft.messages.index": int64(2), "weft.messages.count": int64(1)}, "messages", 2},
		{"delta", map[string]any{"weft.record": "delta", "weft.event.type": "text_delta", "weft.delta.pos": int64(41)}, "delta", 41},
		{"heartbeat", map[string]any{"weft.record": "heartbeat"}, "heartbeat", 0},
		// ADR 0028's kinds, each on its own per-run index.
		{"request", map[string]any{"weft.record": "request", "weft.request.index": int64(3), "weft.step.index": int64(2)}, "request", 3},
		{"prompt", map[string]any{"weft.record": "prompt", "weft.prompt.index": int64(1)}, "prompt", 1},
		{"tools", map[string]any{"weft.record": "tools", "weft.tools.index": int64(4)}, "tools", 4},
		// A malformed producer: one of the new kinds without its index.
		{"request without index", map[string]any{"weft.record": "request"}, "request", -1},
		{"prompt without index", map[string]any{"weft.record": "prompt"}, "prompt", -1},
		{"tools without index", map[string]any{"weft.record": "tools"}, "tools", -1},
		// An index present but empty is absent, the reading ClickHouse's
		// view gives (`!= ''`): -1, never 0.
		{"request with empty index", map[string]any{"weft.record": "request", "weft.request.index": ""}, "request", -1},
		// A messages record without its index: a view or a growth
		// record, -1 on both backends (never the input's position 0).
		{"view without index", map[string]any{"weft.record": "messages", "weft.messages.reason": "compacted"}, "messages", -1},
		{"growth without index", map[string]any{"weft.record": "messages"}, "messages", -1},
	} {
		attrs := map[string]any{}
		for k, v := range base {
			attrs[k] = v
		}
		for k, v := range tc.attrs {
			attrs[k] = v
		}
		w := DeriveRecord(Record{Attrs: attrs})
		if w.Record != tc.record || w.Pos != tc.pos {
			t.Errorf("%s: record=%q pos=%d, want %q/%d", tc.name, w.Record, w.Pos, tc.record, tc.pos)
		}
		if w.RunID != "r1" || w.SessionID != "s1" || w.PublicID != "pub1" || w.Turn != 1 || w.Agent != "a" {
			t.Errorf("%s: identity = %+v", tc.name, w)
		}
	}
	// A slog line: no weft attributes at all — everything absent
	// (Step/ToolSeq at their -1 "absent" sentinels).
	if w := DeriveRecord(Record{EventName: "", Body: "starting"}); w.RunID != "" || w.Record != "" || w.Step != -1 || w.ToolSeq != -1 {
		t.Errorf("non-weft record derived %+v, want everything absent", w)
	}
}

// Attr values arrive from OTLP as int64/double/bool/string; Derive* must
// read the numeric ones without caring which numeric type JSON or a
// vendor handed over.
func TestDeriveNumericTolerance(t *testing.T) {
	w := DeriveRecord(Record{Attrs: map[string]any{
		"weft.run.id": "r", "weft.record": "event", "weft.event.pos": float64(12), "weft.turn": float64(2),
	}})
	if w.Pos != 12 || w.Turn != 2 {
		t.Fatalf("float attrs: pos=%d turn=%d, want 12/2", w.Pos, w.Turn)
	}
	// Attrs as JSON (the sqlite path decodes stored JSON): numbers are
	// float64 there.
	var attrs map[string]any
	if err := json.Unmarshal([]byte(`{"weft.run.id":"r","weft.record":"event","weft.event.pos":7}`), &attrs); err != nil {
		t.Fatal(err)
	}
	if w := DeriveRecord(Record{Attrs: attrs}); w.Pos != 7 {
		t.Fatalf("json-decoded pos = %d, want 7", w.Pos)
	}
}

// The string spelling of weft.turn is the one the real chain
// produces: thread mints it as metadata (strconv.Itoa in turn.go) and
// the core stamps every metadata value as attribute.String
// (observe.go) — the whole programme read turn 0 off every studio run
// row because attrIntOr accepted only the numeric spellings OTLP
// fixtures hand-build. Derive* must read "3" as 3; a non-numeric
// string stays the default (never a fabrication).
func TestDeriveTurnAsStringAttr(t *testing.T) {
	if w := DeriveRecord(Record{Attrs: map[string]any{
		"weft.run.id": "s_01M3-t3", "weft.record": "event",
		"weft.session.id": "s_01M3", "weft.turn": "3",
	}}); w.Turn != 3 {
		t.Fatalf("DeriveRecord turn = %d, want 3 (string attr)", w.Turn)
	}
	if w := DeriveSpan(Span{Attrs: map[string]any{
		"weft.run.id": "s_01M3-t3", "weft.turn": "3",
	}}); w.Turn != 3 {
		t.Fatalf("DeriveSpan turn = %d, want 3 (string attr)", w.Turn)
	}
	if w := DeriveSpan(Span{Attrs: map[string]any{
		"weft.run.id": "s_01M3-t3", "weft.turn": "third",
	}}); w.Turn != 0 {
		t.Fatalf("non-numeric turn = %d, want the 0 default", w.Turn)
	}
}

// Step and ToolSeq default to -1 (absent), never 0.
func TestDeriveAbsentStepAndToolSeq(t *testing.T) {
	if w := DeriveSpan(Span{Attrs: map[string]any{"weft.run.id": "r"}}); w.Step != -1 || w.ToolSeq != -1 {
		t.Fatalf("absent step/toolseq = %d/%d, want -1/-1", w.Step, w.ToolSeq)
	}
	if w := DeriveRecord(Record{Attrs: map[string]any{"weft.run.id": "r", "weft.record": "event", "weft.event.pos": int64(0)}}); w.Step != -1 || w.ToolSeq != -1 {
		t.Fatalf("absent step/toolseq = %d/%d, want -1/-1", w.Step, w.ToolSeq)
	}
}

// The string spelling of weft.playground is the one the real chain
// produces: weft/runtime stamps it as run metadata
// ("weft.playground": "true" in executor.go) and the core renders every
// metadata value as attribute.String — the turn-as-string bug's
// sibling. Derive* must read "true" as true, or every playground run
// lands in the session lists and RunQuery.Playground filters nothing.
// Anything but true/"true" stays false.
func TestDerivePlaygroundAsStringAttr(t *testing.T) {
	if w := DeriveRecord(Record{Attrs: map[string]any{
		"weft.run.id": "r_pg", "weft.record": "event", "weft.playground": "true",
	}}); !w.Playground {
		t.Fatal("DeriveRecord playground = false, want true (string attr)")
	}
	if w := DeriveSpan(Span{Attrs: map[string]any{
		"weft.run.id": "r_pg", "weft.playground": "true",
	}}); !w.Playground {
		t.Fatal("DeriveSpan playground = false, want true (string attr)")
	}
	for _, v := range []any{"false", "", "yes", int64(1), false} {
		if w := DeriveSpan(Span{Attrs: map[string]any{
			"weft.run.id": "r", "weft.playground": v,
		}}); w.Playground {
			t.Fatalf("playground %#v derived true, want false", v)
		}
	}
}

// ADR 0028's keys are the record contract, never run metadata: a
// request record's (and a compaction record's) attributes leave MetaOf
// with only the caller's own pairs.
func TestMetaOfExcludesRequestRecordKeys(t *testing.T) {
	attrs := map[string]any{
		"weft.record": "request", "weft.run.id": "r1",
		"weft.request.index": "0", "weft.prompt.index": "0", "weft.tools.index": "0",
		"weft.step.index": "1", "weft.attempt.index": "1",
		"weft.system.hash": "aa", "weft.catalog.hash": "bb", "weft.instructions.hash": "cc",
		"weft.messages.reason": "compacted", "weft.messages.from_seq": "1", "weft.messages.to_seq": "3",
		"weft.compaction.hash": "dd", "weft.compaction.scope": "run",
		// Plan A4: the string-valued answering model, and the timing
		// keys (ints on the wire, strings here to prove the exclusion).
		"gen_ai.response.model": "glm-b", "weft.stream": "true",
		"weft.ttft_ms": "140", "weft.latency_ms": "812",
		// The attempt span's retry-after ask and the fingerprint's
		// fields (strings here; ints and bools on the wire, which
		// SQLite's MetaOf would drop and ClickHouse's stringify —
		// excluded on both, the backends agree).
		"weft.attempt.retry_after_ms": "250",
		"weft.override.instructions":  "true", "weft.override.max_steps": "3",
		"weft.override.model": "glm", "weft.override.parallelism": "1",
		"weft.override.params": "{}", "weft.override.park_all_except": "lookup",
		"weft.override.park_on": "refund", "weft.override.thinking": "high",
		"weft.override.tool_choice": "any", "weft.override.tools": "lookup,refund",
		"tenant": "acme",
	}
	meta := MetaOf(attrs)
	if len(meta) != 1 || meta["tenant"] != "acme" {
		t.Fatalf("MetaOf = %v, want only the caller's tenant", meta)
	}
}

// markerPos: the first 60 bits of a hex hash, a 60-bit FNV of anything
// else, 0 only for the empty string — never negative, and distinct for
// distinct non-hex hashes (ClickHouse dedupes markers by the hash
// string; SQLite by this position).
func TestMarkerPos(t *testing.T) {
	if markerPos("") != 0 {
		t.Fatal("empty hash: want 0")
	}
	if got := markerPos("0123456789abcdef0123"); got != 0x0123456789abcde {
		t.Fatalf("hex hash: pos = %#x, want the first 15 digits", got)
	}
	a, b := markerPos("h_sess"), markerPos("h_sess2")
	if a == 0 || b == 0 || a == b || a < 0 || b < 0 {
		t.Fatalf("non-hex hashes: %d, %d; want distinct, non-zero, non-negative", a, b)
	}
	if markerPos("zz0123456789abcdef") < 0 || markerPos("ffffffffffffffffffff") < 0 {
		t.Fatal("a position must never be negative")
	}
}
