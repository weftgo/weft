package otel

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const testCard = "4111111111111111"

// maskCard is the masking redactor the tests use: every kind it is
// handed, the card number becomes [CARD]; it records the kinds it saw.
func maskCard(seen map[core.ContentKind]bool) func(core.ContentKind, string) string {
	return func(kind core.ContentKind, s string) string {
		if strings.Contains(s, testCard) && seen != nil {
			seen[kind] = true
		}
		return strings.ReplaceAll(s, testCard, "[CARD]")
	}
}

// Redact applies to weft.messages records, part by part: the user's
// prompt, the assistant's text and reasoning, tool-call args (a JSON
// document and the model's non-JSON bytes) and tool results come out
// masked; the batch still decodes as []core.Message with the same
// messages, roles and part types; the record attributes (index, count)
// are untouched; the Local sink stores the redacted batch and
// obsdb.Transcript — Studio's transcript route — reads it back. Pre-fix
// the transcript left unredacted: the card number was in the stored
// batches.
func TestRedactAppliesToMessagesRecords(t *testing.T) {
	dir := t.TempDir()
	redacted, raw := filepath.Join(dir, "redacted.db"), filepath.Join(dir, "raw.db")
	mem, off := newMemExporter(), newMemExporter()
	seen := map[core.ContentKind]bool{}
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Content(ContentConfig{Redact: maskCard(seen)}),
		Local(redacted),
		Local(raw, WithContent(ContentConfig{})), // the unredacted reference
		Exporters(nil, mem, WithContent()),
		Exporters(nil, off), // content off: messages records dropped, as before
	)
	if err != nil {
		t.Fatal(err)
	}
	type payIn struct {
		Card string `json:"card"`
	}
	pay := core.Tool("pay", "Charge a card.", func(_ context.Context, in payIn) (string, error) {
		return "charged card " + testCard, nil
	})
	agt := core.New(wefttest.Script(
		wefttest.Think("the card is "+testCard, wefttest.ToolCalls(
			wefttest.Call{Name: "pay", Args: `{"card":"` + testCard + `"}`},
			wefttest.Call{Name: "pay", Args: `card ` + testCard + ` oops`}, // not JSON
		)),
		wefttest.Say("charged "+testCard),
	), pay, core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(context.Background(), core.Prompt("pay with "+testCard))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	read := func(path string) []json.RawMessage {
		t.Helper()
		db, err := sqliteOpen(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		tr, err := db.Transcript(context.Background(), res.ID)
		if err != nil || len(tr) == 0 {
			t.Fatalf("%s transcript = %d bodies, %v", path, len(tr), err)
		}
		return tr
	}
	got, ref := read(redacted), read(raw)
	if len(got) != len(ref) {
		t.Fatalf("redacted transcript has %d batches, the reference %d", len(got), len(ref))
	}
	var refAll strings.Builder
	for _, b := range ref {
		refAll.Write(b)
	}
	if !strings.Contains(refAll.String(), testCard) {
		t.Fatal("the reference transcript lacks the card number: the test does not exercise redaction")
	}
	masked := map[string]bool{}
	for i := range got {
		if strings.Contains(string(got[i]), testCard) {
			t.Errorf("batch %d leaks the card number through Redact: %s", i, got[i])
		}
		var gm, rm []core.Message
		if err := json.Unmarshal(got[i], &gm); err != nil {
			t.Fatalf("batch %d no longer decodes as []core.Message: %v (%s)", i, err, got[i])
		}
		if err := json.Unmarshal(ref[i], &rm); err != nil {
			t.Fatal(err)
		}
		if len(gm) != len(rm) {
			t.Fatalf("batch %d: %d messages, the reference %d", i, len(gm), len(rm))
		}
		for j := range gm {
			if gm[j].Role != rm[j].Role || len(gm[j].Content) != len(rm[j].Content) {
				t.Fatalf("batch %d message %d changed shape: %+v vs %+v", i, j, gm[j], rm[j])
			}
			for k, part := range gm[j].Content {
				switch pt := part.(type) {
				case core.TextPart:
					if strings.Contains(pt.Text, "[CARD]") {
						masked[string(gm[j].Role)+" text"] = true
					}
				case core.ReasoningPart:
					if strings.Contains(pt.Text, "[CARD]") {
						masked["reasoning"] = true
					}
				case core.ToolCallPart:
					rc := rm[j].Content[k].(core.ToolCallPart)
					if pt.ID != rc.ID || pt.Name != rc.Name {
						t.Errorf("tool call ids/names changed: %+v vs %+v", pt, rc)
					}
					if !json.Valid(pt.Args) {
						t.Errorf("redacted args are not JSON: %s", pt.Args)
					}
					if strings.Contains(string(pt.Args), "[CARD]") {
						if strings.HasPrefix(string(pt.Args), "{") {
							masked["json args"] = true
						} else {
							masked["non-json args"] = true
						}
					}
				case core.ToolResultPart:
					rr := rm[j].Content[k].(core.ToolResultPart)
					if pt.CallID != rr.CallID || pt.IsError != rr.IsError {
						t.Errorf("tool result ids changed: %+v vs %+v", pt, rr)
					}
					if strings.Contains(pt.Content, "[CARD]") {
						masked["result"] = true
					}
				}
			}
		}
	}
	for _, want := range []string{"user text", "assistant text", "reasoning", "json args", "non-json args", "result"} {
		if !masked[want] {
			t.Errorf("no masked %s in the stored transcript (masked: %v)", want, masked)
		}
	}
	for _, k := range []core.ContentKind{core.ContentText, core.ContentReasoning, core.ContentArgs, core.ContentResult} {
		if !seen[k] {
			t.Errorf("Redact never saw the card under kind %q", k)
		}
	}

	// The records' own attributes are unchanged: indexes 0..n-1, each
	// count the batch's length.
	var idx int64
	for _, r := range mem.snapshot() {
		if recordKind(&r) != "messages" {
			continue
		}
		var n int64
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
			switch string(kv.Key) {
			case "weft.messages.index":
				if kv.Value.AsInt64() != idx {
					t.Errorf("messages index %d, want %d", kv.Value.AsInt64(), idx)
				}
			case "weft.messages.count":
				n = kv.Value.AsInt64()
			}
			return true
		})
		var msgs []core.Message
		if err := json.Unmarshal([]byte(r.Body().AsString()), &msgs); err != nil || int64(len(msgs)) != n {
			t.Errorf("record %d: count %d, body %d messages (%v)", idx, n, len(msgs), err)
		}
		idx++
	}
	if idx != int64(len(got)) {
		t.Errorf("exporter saw %d messages records, the sink stored %d", idx, len(got))
	}

	// Content off: still no messages records at all.
	for _, r := range off.snapshot() {
		if recordKind(&r) == "messages" {
			t.Errorf("a content-off destination received a messages record: %s", r.Body().AsString())
		}
	}
}

// messagesRecord builds one weft.messages record with body.
func messagesRecord(t *testing.T, body string) *sdklog.Record {
	t.Helper()
	return sdkRecordWith(t, "weft.messages", body,
		attribute.String("weft.record", "messages"), attribute.String("weft.content", "full"),
		attribute.Int64("weft.messages.index", 3), attribute.Int("weft.messages.count", 1))
}

// A redactor returning non-JSON for args (the natural "[REDACTED]") makes
// them a JSON string, so the batch still encodes; an unchanged batch
// keeps its bytes exactly; no Redact leaves the record alone.
func TestRedactMessagesArgsFallbackAndIdentity(t *testing.T) {
	body := `[{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"pay","args":{"card":"` + testCard + `"}}]}]`
	mem := newMemExporter()
	p := &destProc{name: "t", inner: sdklog.NewSimpleProcessor(mem), content: true, drops: newDropCounter("t"),
		contentC: ContentConfig{MaxBytes: 4, Redact: func(kind core.ContentKind, s string) string {
			if kind == core.ContentArgs {
				return "[REDACTED]"
			}
			return s
		}}}
	if err := p.OnEmit(context.Background(), messagesRecord(t, body)); err != nil {
		t.Fatal(err)
	}
	unchanged := `[{"role":"user","content":[{"type":"text","text":"a long text well past the four byte cap"}]}]`
	if err := p.OnEmit(context.Background(), messagesRecord(t, unchanged)); err != nil {
		t.Fatal(err)
	}
	p.contentC.Redact = nil
	if err := p.OnEmit(context.Background(), messagesRecord(t, body)); err != nil {
		t.Fatal(err)
	}
	recs := mem.snapshot()
	if len(recs) != 3 {
		t.Fatalf("exported %d records, want 3", len(recs))
	}
	var msgs []core.Message
	if err := json.Unmarshal([]byte(recs[0].Body().AsString()), &msgs); err != nil {
		t.Fatalf("redacted batch does not decode: %v (%s)", err, recs[0].Body().AsString())
	}
	if args := string(msgs[0].Content[0].(core.ToolCallPart).Args); args != `"[REDACTED]"` {
		t.Errorf("non-JSON redactor output = %s, want the JSON string", args)
	}
	if got := recs[1].Body().AsString(); got != unchanged {
		t.Errorf("an unchanged batch was rewritten or capped: %s", got)
	}
	if got := recs[2].Body().AsString(); got != body {
		t.Errorf("no Redact: the batch changed: %s", got)
	}
	for i, r := range recs {
		if attrOf(r, "weft.record") != "messages" || attrOf(r, "weft.content") != "full" {
			t.Errorf("record %d lost its attributes", i)
		}
	}
}

// A panicking Redact on a messages record is contained as on the event
// path — the run's goroutine does not unwind — and the batch is dropped
// (a messages record's stripped form is none), counted, and the WARN
// carries no content. So is a batch that does not decode: never sent
// unredacted.
func TestRedactMessagesFailureNeverLeaks(t *testing.T) {
	buf := &threadSafeBuffer{}
	drops := newDropCounter("t")
	drops.log = slog.New(slog.NewTextHandler(buf, nil))
	mem := newMemExporter()
	p := &destProc{name: "t", inner: sdklog.NewSimpleProcessor(mem), content: true, drops: drops,
		contentC: ContentConfig{Redact: func(_ core.ContentKind, s string) string { panic("cannot redact " + s) }}}
	body := `[{"role":"user","content":[{"type":"text","text":"card ` + testCard + `"}]}]`
	if err := p.OnEmit(context.Background(), messagesRecord(t, body)); err != nil {
		t.Fatal(err)
	}
	p.contentC.Redact = maskCard(nil)
	if err := p.OnEmit(context.Background(), messagesRecord(t, `[{"role":"user","content":"card `+testCard+`"`)); err != nil {
		t.Fatal(err)
	}
	if n := len(mem.snapshot()); n != 0 {
		t.Errorf("%d messages records left after a failed redaction: %s", n, mem.snapshot()[0].Body().AsString())
	}
	if got := drops.count.Load(); got != 2 {
		t.Errorf("dropped count = %d, want 2", got)
	}
	if drops.policy.Load() != 0 {
		t.Error("a failed redaction was counted as policy filtering, not a loss")
	}
	out := buf.String()
	if strings.Contains(out, testCard) {
		t.Errorf("the drop WARN carries content: %s", out)
	}
	if !strings.Contains(out, "redaction panicked") {
		t.Errorf("no WARN for the panicking redactor: %q", out)
	}
}
