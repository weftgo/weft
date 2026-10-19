package otel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/weftgo/weft/core"
)

func threadRecord(t *testing.T, body, content string) *sdklog.Record {
	t.Helper()
	return sdkRecordWith(t, "weft.thread.approval.decision", body,
		attribute.String("weft.record", "thread"), attribute.String("weft.content", content),
		attribute.String("weft.run.id", "s_x-t1"))
}

// A thread record (record contract §4, §5): a content-off chain removes
// its content object and marks it stripped, keeping every other field —
// the ids, enums and an app-chosen name; a record without content passes
// unchanged; a body it cannot read that claims content is never sent.
func TestThreadRecordContentOff(t *testing.T) {
	mem := newMemExporter()
	p := &destProc{name: "otlp", inner: sdklog.NewSimpleProcessor(mem), content: false, drops: newDropCounter("otlp")}
	full := `{"call_id":"r1","content":{"reason":"need a photo","who":"agent-7"},"kind":"approval.decision","name":"case-closed","outcome":"deny","session":"s_x","via":"user"}`
	none := `{"call_id":"r1","kind":"approval.request","session":"s_x","tool":"issue_refund"}`
	for _, r := range []*sdklog.Record{threadRecord(t, full, "full"), threadRecord(t, none, "none"), threadRecord(t, "not json", "full")} {
		if err := p.OnEmit(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	recs := mem.snapshot()
	if len(recs) != 2 {
		t.Fatalf("exported %d records, want 2 (the unreadable one dropped)", len(recs))
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(recs[0].Body().AsString()), &b); err != nil {
		t.Fatal(err)
	}
	if _, ok := b["content"]; ok || b["name"] != "case-closed" || b["via"] != "user" || b["outcome"] != "deny" {
		t.Errorf("stripped body = %v", b)
	}
	if attrOf(recs[0], "weft.content") != "stripped" {
		t.Errorf("weft.content = %q, want stripped", attrOf(recs[0], "weft.content"))
	}
	if recs[1].Body().AsString() != none || attrOf(recs[1], "weft.content") != "none" {
		t.Errorf("a record without content changed: %s", recs[1].Body().AsString())
	}
}

// A content-on chain redacts each content string with ContentThread and
// caps it, recording the cut; a panicking Redact falls back to the
// stripped shape — never the original.
func TestThreadRecordContentOn(t *testing.T) {
	mem := newMemExporter()
	p := &destProc{name: "studio", inner: sdklog.NewSimpleProcessor(mem), content: true, drops: newDropCounter("studio"),
		contentC: ContentConfig{MaxBytes: 8, Redact: func(kind core.ContentKind, s string) string {
			if kind != ContentThread {
				t.Errorf("Redact saw kind %q, want %q", kind, ContentThread)
			}
			return strings.ReplaceAll(s, "agent-7", "[who]")
		}}}
	body := `{"content":{"reason":"need a photo of the damage","who":"agent-7"},"kind":"approval.decision","session":"s_x"}`
	if err := p.OnEmit(context.Background(), threadRecord(t, body, "full")); err != nil {
		t.Fatal(err)
	}
	p.contentC.Redact = func(core.ContentKind, string) string { panic("boom") }
	if err := p.OnEmit(context.Background(), threadRecord(t, body, "full")); err != nil {
		t.Fatal(err)
	}
	recs := mem.snapshot()
	if len(recs) != 2 {
		t.Fatalf("exported %d records, want 2", len(recs))
	}
	var b struct {
		Content map[string]string `json:"content"`
	}
	if err := json.Unmarshal([]byte(recs[0].Body().AsString()), &b); err != nil {
		t.Fatal(err)
	}
	if b.Content["who"] != "[who]" || b.Content["reason"] != "need a p" {
		t.Errorf("shaped content = %v", b.Content)
	}
	var cut int64
	recs[0].WalkAttributes(func(kv attribute.KeyValue) bool {
		if kv.Key == "weft.content.truncated_bytes" {
			cut = kv.Value.AsInt64()
		}
		return true
	})
	if cut != int64(len("need a photo of the damage")-8) {
		t.Errorf("weft.content.truncated_bytes = %d, want the cap's cut", cut)
	}
	if strings.Contains(recs[1].Body().AsString(), "agent-7") || attrOf(recs[1], "weft.content") != "stripped" {
		t.Errorf("a panicking Redact let content through: %s", recs[1].Body().AsString())
	}
}
