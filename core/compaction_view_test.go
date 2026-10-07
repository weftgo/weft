package core_test

// The run-scope compaction view (ADR 0028 §8): a PrepareStep that
// rewrites a request's messages leaves one messages record with
// weft.messages.reason = compacted right before that request's record,
// carrying the replaced half-open range and the replacement; the
// request's messages_ref names it, the next request names the growth
// records again, and the growth records alone still concatenate to the
// transcript.

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// trimMiddleAt returns a PrepareStep that, at step at, keeps the first
// message and the last two and puts one summary message between them.
func trimMiddleAt(at int) core.Option {
	return core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
		if step != at || len(req.Messages) < 4 {
			return req, nil
		}
		m := req.Messages
		req.Messages = []core.Message{m[0], core.User("summary of the middle"), m[len(m)-2], m[len(m)-1]}
		return req, nil
	})
}

// views returns the run's compaction view records.
func views(t *testing.T, lp *recLogProvider) []recLogRecord {
	t.Helper()
	var out []recLogRecord
	for _, m := range lp.ofKind(t, "messages") {
		if m.attr("weft.messages.reason") != "" {
			out = append(out, m)
		}
	}
	return out
}

// wantCompactionHash is ADR 0028 §8's procedure, restated: sha256 over
// the canonical JSON of {"entries", "from_seq", "to_seq"}.
func wantCompactionHash(t *testing.T, from, to int64, body string) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	dec.UseNumber()
	var entries any
	if err := dec.Decode(&entries); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"entries": entries, "from_seq": from, "to_seq": to}); err != nil {
		t.Fatal(err)
	}
	return sha(string(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))))
}

// A PrepareStep that trims the middle at step 3 yields exactly one
// compacted record, at step 3, with the half-open range of the
// transcript it replaced and its replacement; the step-3 request points
// at it and resolves to exactly what the model was sent; the step-4
// request points at the growth records again (a run-scope rewrite is
// never cumulative); and the growth records concatenate to the
// transcript, the view excluded.
func TestRequestRecordsRefCompactedView(t *testing.T) {
	lp := newRecLogProvider()
	model := wefttest.Script(toolTurns(5)...)
	agt := core.New(model, reqEcho("echo"), trimMiddleAt(3), core.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), core.RunID("cv"), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	vs := views(t, lp)
	if len(vs) != 1 {
		t.Fatalf("compacted records = %d, want 1", len(vs))
	}
	v := vs[0]
	idx, _ := v.intAttr("weft.messages.index")
	step, _ := v.intAttr("weft.step.index")
	from, _ := v.intAttr("weft.messages.from_seq")
	to, _ := v.intAttr("weft.messages.to_seq")
	count, _ := v.intAttr("weft.messages.count")
	// Input u (0), then two growth records per step: steps 0–2 are
	// indices 1–6, so the view is 7. The transcript at step 3 is 7
	// messages; the request kept [0] and [5, 7): the range is [1, 5).
	if idx != 7 || step != 3 || from != 1 || to != 5 || count != 1 {
		t.Errorf("view = index %d step %d range [%d, %d) count %d, want 7, 3, [1, 5), 1", idx, step, from, to, count)
	}
	if v.attr("weft.messages.reason") != "compacted" || v.attr("weft.compaction.scope") != "run" || v.attr("weft.content") != "full" {
		t.Errorf("view attrs: reason %q scope %q content %q", v.attr("weft.messages.reason"), v.attr("weft.compaction.scope"), v.attr("weft.content"))
	}
	if v.hasAttr("weft.messages.input") {
		t.Error("a view carries weft.messages.input")
	}
	if got, want := v.attr("weft.compaction.hash"), wantCompactionHash(t, from, to, v.body); got != want {
		t.Errorf("weft.compaction.hash = %s, want %s", got, want)
	}
	var body []core.Message
	if err := json.Unmarshal([]byte(v.body), &body); err != nil || len(body) != 1 || body[0].Text() != "summary of the middle" {
		t.Fatalf("view body = %s (%v)", v.body, err)
	}

	reqs := lp.ofKind(t, "request")
	if len(reqs) != 6 {
		t.Fatalf("request records = %d, want 6", len(reqs))
	}
	sent := model.Requests()
	for i, r := range reqs {
		b := decodeRequest(t, r)
		if b.MessagesRef.Index == nil {
			t.Fatalf("step %d: no messages_ref.index with capture on", i)
		}
		if i == 3 && (*b.MessagesRef.Index != 7 || b.MessagesRef.Count != 4) {
			t.Errorf("step 3 messages_ref = {%d, %d}, want {7, 4}", *b.MessagesRef.Index, b.MessagesRef.Count)
		}
		if i == 4 && (*b.MessagesRef.Index != 9 || b.MessagesRef.Count != 9) {
			t.Errorf("step 4 messages_ref = {%d, %d}, want {9, 9} (the growth records, not the view)", *b.MessagesRef.Index, b.MessagesRef.Count)
		}
		// Every request's ref resolves to exactly what the model was sent.
		got := resolveRef(t, lp, "cv", *b.MessagesRef.Index)
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(sent[i].Messages)
		if !bytes.Equal(gb, wb) || len(got) != b.MessagesRef.Count {
			t.Errorf("step %d: ref resolves to\n%s\nthe model was sent\n%s", i, gb, wb)
		}
	}
	// The plain transcript: growth records only.
	all := resolveRef(t, lp, "cv", 1<<30)
	ab, _ := json.Marshal(all)
	tb, _ := json.Marshal(res.Messages)
	if !bytes.Equal(ab, tb) {
		t.Errorf("growth records rebuild\n%s\ntranscript\n%s", ab, tb)
	}
	// The view takes its index on the one counter: contiguous.
	for i, m := range lp.ofKind(t, "messages") {
		if n, _ := m.intAttr("weft.messages.index"); n != int64(i) {
			t.Errorf("messages record %d has index %d", i, n)
		}
	}
	// Emission order: the view sits immediately before its request.
	lp.mu.Lock()
	recs := slicesClone(lp.records)
	lp.mu.Unlock()
	for i, r := range recs {
		if r.attr("weft.messages.reason") == "compacted" {
			if i+1 >= len(recs) || recs[i+1].attr("weft.record") != "request" {
				t.Errorf("the view is not immediately followed by its request record")
			}
		}
	}
}

func slicesClone(in []recLogRecord) []recLogRecord { return append([]recLogRecord(nil), in...) }

// A PrepareStep that leaves the messages alone — it rewrites only the
// system text, on a deep copy — records no view: equality is by wire
// bytes, not identity.
func TestCompactionViewNotRecordedWhenUnchanged(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(toolTurns(2)...), reqEcho("echo"), core.LoggerProvider(lp),
		core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
			req.System = "rewritten"
			return req, nil
		}))
	if _, err := agt.Generate(context.Background(), core.RunID("same"), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if vs := views(t, lp); len(vs) != 0 {
		t.Fatalf("compacted records = %d, want 0", len(vs))
	}
	growth := len(lp.ofKind(t, "messages"))
	last := decodeRequest(t, lp.ofKind(t, "request")[2])
	if last.MessagesRef.Index == nil || *last.MessagesRef.Index != int64(growth-2) {
		t.Errorf("last request ref = %v, want the latest growth record before it (%d)", last.MessagesRef.Index, growth-2)
	}
}

// Capture off: no view record (it is content), the request keeps only
// its count — what the model was sent — and the model still receives
// exactly PrepareStep's output.
func TestCompactionViewCaptureOff(t *testing.T) {
	lp := newRecLogProvider()
	model := wefttest.Script(toolTurns(3)...)
	agt := core.New(model, reqEcho("echo"), trimMiddleAt(2), core.Content(false), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.RunID("off"), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if n := len(lp.ofKind(t, "messages")); n != 0 {
		t.Fatalf("messages records with capture off = %d", n)
	}
	b := decodeRequest(t, lp.ofKind(t, "request")[2])
	if b.MessagesRef.Index != nil || b.MessagesRef.Count != 4 {
		t.Errorf("step 2 ref = %v/%d, want no index, count 4", b.MessagesRef.Index, b.MessagesRef.Count)
	}
	if got := len(model.Requests()[2].Messages); got != 4 {
		t.Errorf("the model was sent %d messages, want PrepareStep's 4", got)
	}
}

// The view and no view: the model receives PrepareStep's output either
// way — recording it changes nothing model-visible.
func TestCompactionViewChangesNothingModelVisible(t *testing.T) {
	run := func(lp *recLogProvider) []core.ModelRequest {
		model := wefttest.Script(toolTurns(4)...)
		opts := []core.Option{reqEcho("echo"), trimMiddleAt(3)}
		if lp != nil {
			opts = append(opts, core.LoggerProvider(lp))
		} else {
			opts = append(opts, core.Content(false))
		}
		if _, err := core.New(model, opts...).Generate(context.Background(), core.Prompt("go")); err != nil {
			t.Fatal(err)
		}
		return model.Requests()
	}
	on, off := run(newRecLogProvider()), run(nil)
	ob, _ := json.Marshal(on)
	fb, _ := json.Marshal(off)
	if !bytes.Equal(ob, fb) {
		t.Errorf("recording the view changed what the model was sent")
	}
}

// The view record's shape, pinned: attributes and body.
func TestCompactionViewGoldenShape(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(toolTurns(2)...), reqEcho("echo"), trimMiddleAt(2),
		core.Name("support"), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.RunID("run-compacted"), core.Prompt("where is 42?")); err != nil {
		t.Fatal(err)
	}
	vs := views(t, lp)
	if len(vs) != 1 {
		t.Fatalf("compacted records = %d, want 1", len(vs))
	}
	r := vs[0]
	attrs := map[string]string{}
	for _, kv := range r.attrs {
		attrs[string(kv.Key)] = kv.Value.Type().String() + ":" + kv.Value.String()
	}
	var body any
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatal(err)
	}
	doc, err := json.MarshalIndent(map[string]any{
		"event_name": r.eventName,
		"severity":   r.severity.String(),
		"attributes": attrs,
		"body":       body,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wefttest.Golden(t, "testdata/records/compacted.golden.json", append(doc, '\n'))
}
