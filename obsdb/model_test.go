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

// Step and ToolSeq default to -1 (absent), never 0.
func TestDeriveAbsentStepAndToolSeq(t *testing.T) {
	if w := DeriveSpan(Span{Attrs: map[string]any{"weft.run.id": "r"}}); w.Step != -1 || w.ToolSeq != -1 {
		t.Fatalf("absent step/toolseq = %d/%d, want -1/-1", w.Step, w.ToolSeq)
	}
	if w := DeriveRecord(Record{Attrs: map[string]any{"weft.run.id": "r", "weft.record": "event", "weft.event.pos": int64(0)}}); w.Step != -1 || w.ToolSeq != -1 {
		t.Fatalf("absent step/toolseq = %d/%d, want -1/-1", w.Step, w.ToolSeq)
	}
}
