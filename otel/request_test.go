package otel

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func recordsOfKind(recs []sdklog.Record, kind string) []sdklog.Record {
	var out []sdklog.Record
	for _, r := range recs {
		if attrOf(r, "weft.record") == kind {
			out = append(out, r)
		}
	}
	return out
}

func intAttrOf(r sdklog.Record, key string) (int64, bool) {
	var v int64
	var ok bool
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == key && kv.Value.Type() == attribute.INT64 {
			v, ok = kv.Value.AsInt64(), true
			return false
		}
		return true
	})
	return v, ok
}

// ADR 0028 §6 through the real pipeline: a content-on destination gets
// the prompt with Redact applied (core.ContentPrompt) and capped with
// weft.content.truncated_bytes, the tools record capped to whole
// entries, and the request with each stop sequence redacted
// (core.ContentStop); a content-off destination gets neither text nor
// schema — no prompt, no tools, the request kept with params.stop
// emptied and weft.content=stripped, its hashes intact. The Local sink
// takes the new kinds beside the transcript, which still reads back.
func TestRequestRecordsContentPolicyPerDestination(t *testing.T) {
	dir := t.TempDir()
	on, off := newMemExporter(), newMemExporter()
	seen := map[core.ContentKind]bool{}
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Local(filepath.Join(dir, "weft.db")),
		Exporters(nil, on, WithContent(ContentConfig{Redact: maskCard(seen), MaxBytes: 300})),
		Exporters(nil, off),
	)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Be careful. ", 40) // > MaxBytes
	tools := []core.Option{}
	for _, n := range []string{"alpha", "beta", "gamma", "delta"} {
		tools = append(tools, core.Tool(n, "Tool "+n+" with a description long enough to matter.", func(context.Context, struct {
			Q string `json:"q"`
		}) (string, error) {
			return "ok", nil
		}))
	}
	agt := core.New(wefttest.Script(wefttest.Say("done")), append(tools,
		core.Instructions("card "+testCard+". "+long),
		core.Params(core.RequestParams{Stop: []string{"stop " + testCard, "END"}}),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))...)
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Content on.
	onRecs := on.snapshot()
	prompts := recordsOfKind(onRecs, "prompt")
	if len(prompts) != 1 {
		t.Fatalf("content-on prompt records = %d, want 1", len(prompts))
	}
	var pr struct{ Hash, Text string }
	if err := json.Unmarshal([]byte(prompts[0].Body().AsString()), &pr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pr.Text, testCard) || !strings.HasPrefix(pr.Text, "card [CARD]") || len(pr.Text) != 300 {
		t.Errorf("prompt text not redacted and capped: %d bytes %q", len(pr.Text), pr.Text[:min(40, len(pr.Text))])
	}
	if cut, ok := intAttrOf(prompts[0], "weft.content.truncated_bytes"); !ok || cut <= 0 {
		t.Errorf("prompt cap not marked: %d,%v", cut, ok)
	}
	if want := attrOf(prompts[0], "weft.system.hash"); pr.Hash != want || len(want) != 64 {
		t.Errorf("prompt hash changed by shaping: %q vs %q", pr.Hash, want)
	}
	if !seen[core.ContentPrompt] || !seen[core.ContentStop] {
		t.Errorf("Redact did not see the new kinds: %v", seen)
	}
	tr := recordsOfKind(onRecs, "tools")
	if len(tr) != 1 {
		t.Fatalf("content-on tools records = %d, want 1", len(tr))
	}
	body := tr[0].Body().AsString()
	var tb struct {
		Hash  string            `json:"hash"`
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &tb); err != nil {
		t.Fatalf("capped tools body is not JSON: %v", err)
	}
	if len(body) > 300 || len(tb.Tools) >= 4 || tb.Hash != attrOf(tr[0], "weft.catalog.hash") {
		t.Errorf("tools body not capped to whole entries: %d bytes, %d tools", len(body), len(tb.Tools))
	}
	if cut, ok := intAttrOf(tr[0], "weft.content.truncated_bytes"); !ok || cut <= 0 {
		t.Errorf("tools cap not marked: %d,%v", cut, ok)
	}
	reqs := recordsOfKind(onRecs, "request")
	if len(reqs) != 1 {
		t.Fatalf("content-on request records = %d", len(reqs))
	}
	rb := reqs[0].Body().AsString()
	if strings.Contains(rb, testCard) || !strings.Contains(rb, `"stop [CARD]"`) || !strings.Contains(rb, `"END"`) {
		t.Errorf("content-on request stop not redacted: %s", rb)
	}

	// Content off: no text, no schema anywhere; the request survives.
	offRecs := off.snapshot()
	if n := len(recordsOfKind(offRecs, "prompt")) + len(recordsOfKind(offRecs, "tools")) + len(recordsOfKind(offRecs, "messages")); n != 0 {
		t.Errorf("content-off destination received %d content records", n)
	}
	for _, r := range offRecs {
		b := r.Body().AsString()
		if strings.Contains(b, testCard) || strings.Contains(b, "Be careful") || strings.Contains(b, `"properties"`) {
			t.Errorf("content-off record %s carries content: %s", attrOf(r, "weft.record"), b)
		}
	}
	offReqs := recordsOfKind(offRecs, "request")
	if len(offReqs) != 1 {
		t.Fatalf("content-off request records = %d, want 1", len(offReqs))
	}
	if attrOf(offReqs[0], "weft.content") != "stripped" {
		t.Errorf("content-off request weft.content = %q", attrOf(offReqs[0], "weft.content"))
	}
	var ob struct {
		SystemHash string `json:"system_hash"`
		Tools      struct {
			CatalogHash string   `json:"catalog_hash"`
			Names       []string `json:"names"`
		} `json:"tools"`
		Params      map[string]any `json:"params"`
		MessagesRef map[string]any `json:"messages_ref"`
	}
	if err := json.Unmarshal([]byte(offReqs[0].Body().AsString()), &ob); err != nil {
		t.Fatal(err)
	}
	if ob.SystemHash != pr.Hash || ob.Tools.CatalogHash != tb.Hash || len(ob.Tools.Names) != 4 {
		t.Errorf("content-off request lost its hashes or names: %+v", ob)
	}
	if _, has := ob.Params["stop"]; has {
		t.Errorf("content-off request keeps params.stop: %v", ob.Params)
	}
	// It never received the messages records: the index is gone, the
	// count stays; the content-on destination keeps both.
	if _, has := ob.MessagesRef["index"]; has || ob.MessagesRef["count"] != float64(1) {
		t.Errorf("content-off request messages_ref = %v, want the count alone", ob.MessagesRef)
	}
	if !strings.Contains(rb, `"messages_ref":{"index":0,"count":1}`) {
		t.Errorf("content-on request lost its messages_ref: %s", rb)
	}
	if attrOf(offReqs[0], "weft.system.hash") != pr.Hash || attrOf(offReqs[0], "weft.catalog.hash") != tb.Hash {
		t.Error("content-off request lost its hash attributes")
	}

	// The Local sink took the new kinds beside the transcript.
	db, err := sqliteOpen(filepath.Join(dir, "weft.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if got, err := db.Transcript(context.Background(), res.ID); err != nil || len(got) == 0 {
		t.Errorf("local transcript = %d batches, %v", len(got), err)
	}
}

// The pipeline's Enabled rule covers the new pure-content kinds: a
// content-off-only pipeline answers false for weft.prompt and
// weft.tools (and true for weft.request); one content-on destination
// flips them.
func TestPipelineEnabledRuleRequestKinds(t *testing.T) {
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0), Exporters(nil, newMemExporter()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	lg := p.LoggerProvider().Logger(instrumentationName)
	for _, name := range []string{"weft.prompt", "weft.tools"} {
		if lg.Enabled(testCtx(t), enabledParams(name)) {
			t.Errorf("content-off-only pipeline answers Enabled(%s) true", name)
		}
	}
	if !lg.Enabled(testCtx(t), enabledParams("weft.request")) {
		t.Error("content-off pipeline answers Enabled(weft.request) false")
	}
	p2, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0), Exporters(nil, newMemExporter(), WithContent()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p2.Shutdown(testCtx(t)) }()
	for _, name := range []string{"weft.prompt", "weft.tools"} {
		if !p2.LoggerProvider().Logger(instrumentationName).Enabled(testCtx(t), enabledParams(name)) {
			t.Errorf("a content-on destination must make Enabled(%s) true", name)
		}
	}
}

// A Redact that panics on a prompt drops the prompt record (never sent
// unredacted) and sends the request record stripped.
func TestRequestRecordsRedactPanicNeverLeaks(t *testing.T) {
	mem := newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Exporters(nil, mem, WithContent(ContentConfig{Redact: func(kind core.ContentKind, s string) string {
			if kind == core.ContentPrompt || kind == core.ContentStop {
				panic("redactor broke on " + s)
			}
			return s
		}})))
	if err != nil {
		t.Fatal(err)
	}
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		core.Instructions("secret "+testCard), core.Params(core.RequestParams{Stop: []string{testCard}}),
		core.LoggerProvider(p.LoggerProvider()))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	recs := mem.snapshot()
	if n := len(recordsOfKind(recs, "prompt")); n != 0 {
		t.Errorf("%d prompt records survived a panicking Redact", n)
	}
	reqs := recordsOfKind(recs, "request")
	if len(reqs) != 1 || attrOf(reqs[0], "weft.content") != "stripped" || strings.Contains(reqs[0].Body().AsString(), testCard) {
		t.Errorf("request after a panicking Redact: %d records %v", len(reqs), reqs)
	}
}
