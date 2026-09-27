package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/store/storetest"
	"github.com/weftgo/weft/wefttest"
)

// Memory runs the shared conformance table (the durable backends run
// the same one — agreement with Memory is the reference behaviour).
func TestMemoryConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return store.Memory() })
}

// The result document's explicit tags are the wire contract (ADR 0010
// §2.2): the envelope integer is present, and the field names are
// pinned so a rename is a deliberate version bump, not drift.
func TestResultDocWire(t *testing.T) {
	res := &weft.RunResult{
		ID:         "r1",
		StopReason: weft.StopToolCalls,
		Messages:   []weft.Message{weft.User("hi")},
		Steps: []weft.StepRecord{{
			Index:      0,
			StopReason: weft.StopToolCalls,
			Usage:      weft.Usage{InputTokens: 1, OutputTokens: 2, CachedInputTokens: 1},
			ToolCalls:  []weft.ToolCallPart{{ID: "c1", Name: "t", Args: json.RawMessage(`{}`)}},
			Results:    []weft.ToolResultPart{{CallID: "c1", Name: "t", Content: "ok"}},
		}},
		Usage:   weft.Usage{InputTokens: 1, OutputTokens: 2},
		Pending: []weft.ToolCallPart{{ID: "p1", Name: "gate"}},
	}
	b, err := store.MarshalResult(res)
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Weft   int `json:"weft"`
		Result struct {
			StopReason string `json:"stop_reason"`
			Steps      []struct {
				StopReason    string `json:"stop_reason"`
				RawStopReason string `json:"raw_stop_reason"`
				ToolCalls     []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"steps"`
			Pending []struct {
				ID string `json:"id"`
			} `json:"pending"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	if head.Weft != store.FormatVersion {
		t.Errorf("envelope weft = %d, want %d", head.Weft, store.FormatVersion)
	}
	for _, key := range []string{`"stop_reason"`, `"messages"`, `"steps"`, `"usage"`, `"pending"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("result doc missing key %s: %s", key, b)
		}
	}
	back, err := store.UnmarshalResult(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != res.ID || back.StopReason != res.StopReason || back.NumSteps() != 1 ||
		len(back.Pending) != 1 || back.Pending[0].ID != "p1" ||
		back.Steps[0].ToolCalls[0].ID != "c1" || back.Steps[0].Usage.CachedInputTokens != 1 {
		t.Errorf("result doc did not round trip: %+v", back)
	}
	nb, _ := store.MarshalResult(nil)
	if string(nb) != "null" {
		t.Errorf("MarshalResult(nil) = %s, want null", nb)
	}
	if n, err := store.UnmarshalResult(nb); err != nil || n != nil {
		t.Errorf("UnmarshalResult(null) = %v, %v; want nil, nil", n, err)
	}
}

// The envelope integer is a gate, not a decoration (ADR 0010 §2.3): a
// result document from a newer format fails with ErrNewerFormat, and
// one with a result but no matching envelope was never written by
// this format — neither decodes with today's tags.
func TestResultDocVersionLoud(t *testing.T) {
	_, err := store.UnmarshalResult([]byte(`{"weft":2,"result":{"id":"r1"}}`))
	if !errors.Is(err, store.ErrNewerFormat) {
		t.Errorf("newer envelope: err = %v, want ErrNewerFormat", err)
	}
	if err == nil || !strings.Contains(err.Error(), "weft=2") {
		t.Errorf("newer envelope: err = %v, want both version numbers named", err)
	}
	if _, err := store.UnmarshalResult([]byte(`{"result":{"id":"r1"}}`)); err == nil {
		t.Error("missing envelope: err = nil, want a loud decode error")
	}
	// The version this build writes still reads.
	b, err := store.MarshalResult(&weft.RunResult{ID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UnmarshalResult(b); err != nil {
		t.Errorf("current envelope: %v", err)
	}
}

// oneRun generates a script run with a subagent and a pending approval
// — reasoning, tool calls, Nested events, a pending gate — recorded
// into a fresh Memory store, with the emitted stream captured beside
// it by a second tap.
func oneRun(t *testing.T) (s store.Store, stream []weft.Event) {
	t.Helper()
	s = store.Memory()
	gate := weft.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "g", nil
	}, weft.RequireApproval())
	kid := weft.New(wefttest.Script(wefttest.Say("child done")), weft.Name("kid"),
		store.Record(s, store.Tags(map[string]string{"kind": "child"})))
	agt := weft.New(
		wefttest.Script(
			wefttest.Think("plan", wefttest.ToolCalls(
				wefttest.Call{Name: "delegate"},
				wefttest.Call{Name: "gate"},
			)),
			wefttest.Say("all done"),
		),
		weft.Name("parent"),
		store.Record(s, store.Tags(map[string]string{"cwd": "/tmp"})),
		weft.Tap(func(_ context.Context, ev weft.Event) { stream = append(stream, ev) }),
		weft.Subagent("delegate", "Runs the child.", kid),
		gate,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("expected the gated call pending, got %d", len(res.Pending))
	}
	return s, stream
}

func rootRecord(t *testing.T, s store.Store) store.RunRecord {
	t.Helper()
	p, err := s.List(context.Background(), store.Query{}) // top-level only
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 {
		t.Fatalf("top-level records = %d, want 1 (the parent; the child is not top-level)", p.Total)
	}
	got, err := s.Get(context.Background(), p.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRecordRoundTripByteForByte(t *testing.T) {
	s, stream := oneRun(t)
	parent := rootRecord(t, s)
	if parent.Status != store.Succeeded {
		t.Errorf("parent status = %q, want succeeded", parent.Status)
	}
	if parent.Agent != "parent" || parent.ManifestHash == "" || parent.WeftVersion == "" {
		t.Errorf("identity incomplete: agent=%q hash=%q version=%q", parent.Agent, parent.ManifestHash, parent.WeftVersion)
	}
	if parent.Tags["cwd"] != "/tmp" {
		t.Errorf("tags = %v, want cwd from Tags()", parent.Tags)
	}
	// The run ends at the approval boundary: the result carries the
	// pending call and no final text — the record keeps that shape.
	if parent.Result == nil || len(parent.Result.Pending) != 1 || parent.Result.Pending[0].Name != "gate" {
		t.Errorf("result incomplete: %+v", parent.Result)
	}
	if len(parent.Events) != len(stream) {
		t.Fatalf("recorded %d events, stream had %d", len(parent.Events), len(stream))
	}
	for i := range stream {
		want, _ := json.Marshal(stream[i])
		have, _ := json.Marshal(parent.Events[i])
		if string(want) != string(have) {
			t.Errorf("event %d: recorded %s, stream had %s", i, have, want)
		}
	}
	var nested int
	for _, ev := range parent.Events {
		if _, ok := ev.(weft.Nested); ok {
			nested++
		}
	}
	if nested == 0 {
		t.Error("no Nested events in the parent's stream")
	}
}

// A child run owns its record: ParentID and ParentCallID link it, its
// events are its own unwrapped stream, its result is its own — and the
// default Query hides it from the top-level list.
func TestRecordChildRun(t *testing.T) {
	s, _ := oneRun(t)
	all, err := s.List(context.Background(), store.Query{ParentID: "*"})
	if err != nil {
		t.Fatal(err)
	}
	var child store.RunRecord
	for _, rec := range all.Runs {
		if rec.Agent == "kid" {
			child, _ = s.Get(context.Background(), rec.ID)
		}
	}
	if child.ID == "" {
		t.Fatal("no child record found")
	}
	if child.ParentID == "" || child.ParentCallID != "call_1" {
		t.Errorf("child linkage = {parent:%q call:%q}, want the parent run and call_1", child.ParentID, child.ParentCallID)
	}
	if child.Result == nil || child.Result.Text() != "child done" {
		t.Errorf("child result = %+v, want its own final text", child.Result)
	}
	if child.Tags["kind"] != "child" {
		t.Errorf("child tags = %v, want its own Record option's", child.Tags)
	}
	var starts int
	for _, ev := range child.Events {
		if _, ok := ev.(weft.RunStart); ok {
			starts++
		}
		if _, ok := ev.(weft.Nested); ok {
			t.Error("child stream contains a Nested wrapper: the child records its own unwrapped events")
		}
	}
	if starts != 1 {
		t.Errorf("child stream has %d RunStart events, want 1", starts)
	}
	top, _ := s.List(context.Background(), store.Query{})
	for _, rec := range top.Runs {
		if rec.ID == child.ID {
			t.Error("default Query listed the child; ParentID:\"\" means top-level only")
		}
	}
}

// A failed run keeps its partial transcript and the error text — via
// OnRunEnd, the signal the tap cannot carry.
func TestRecordFailureKeepsPartial(t *testing.T) {
	s := store.Memory()
	boom := errors.New("provider down")
	work := weft.Tool("work", "", func(_ context.Context, _ struct{}) (string, error) {
		return "done", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "work"}),
		wefttest.Fail(boom),
	), weft.Name("failing"), store.Record(s), work)
	_, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err == nil {
		t.Fatal("run should have failed")
	}
	p, _ := s.List(context.Background(), store.Query{})
	if p.Total != 1 {
		t.Fatalf("records = %d, want 1", p.Total)
	}
	rec, err := s.Get(context.Background(), p.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.Failed {
		t.Errorf("status = %q, want failed", rec.Status)
	}
	if rec.Err == "" || !strings.Contains(rec.Err, "provider down") {
		t.Errorf("Err = %q, want the run error text", rec.Err)
	}
	if rec.Result == nil || len(rec.Result.Messages) == 0 {
		t.Errorf("Result = %+v, want the partial transcript", rec.Result)
	}
	if len(rec.Events) == 0 {
		t.Error("no events recorded before the failure")
	}
	if rec.Finished.IsZero() {
		t.Error("Finished not set on the failed record")
	}
}

// A live run is tail-able: mid-run, Get returns the events so far and
// a running status with a fresh heartbeat — the point of per-event
// append.
func TestRecordLiveTail(t *testing.T) {
	s := store.Memory()
	started := make(chan struct{})
	slow := weft.Tool("slow", "", func(_ context.Context, _ struct{}) (string, error) {
		close(started)
		time.Sleep(50 * time.Millisecond)
		return "done", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("finished"),
	), weft.Name("tail"), store.Record(s), slow)
	go func() { _, _ = agt.Generate(context.Background(), weft.Prompt("go")) }()
	<-started
	var rec store.RunRecord
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p, err := s.List(context.Background(), store.Query{})
		if err != nil || p.Total == 0 {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		rec, _ = s.Get(context.Background(), p.Runs[0].ID)
		break
	}
	if rec.ID == "" {
		t.Fatal("no running row appeared mid-run")
	}
	if rec.Status != store.Running {
		t.Errorf("mid-run status = %q, want running", rec.Status)
	}
	if len(rec.Events) == 0 {
		t.Error("mid-run Get returned no events")
	}
	if time.Since(rec.Heartbeat) > store.HeartbeatTimeout {
		t.Error("mid-run heartbeat is stale")
	}
	time.Sleep(150 * time.Millisecond) // let the run finish
	p, _ := s.List(context.Background(), store.Query{})
	final, _ := s.Get(context.Background(), p.Runs[0].ID)
	if final.Status != store.Succeeded {
		t.Errorf("final status = %q, want succeeded", final.Status)
	}
}

// The manifest hash separates agents by their definitions: different
// tool sets → different hashes; the same definition → one hash; an
// unnamed agent → "".
func TestRecordManifestHash(t *testing.T) {
	s := store.Memory()
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "e", nil })
	plus := weft.Tool("plus", "", func(_ context.Context, _ struct{}) (string, error) { return "p", nil })
	run := func(agt *weft.Agent) string {
		t.Helper()
		if _, err := agt.Generate(context.Background(), weft.Prompt("hi")); err != nil {
			t.Fatal(err)
		}
		p, _ := s.List(context.Background(), store.Query{})
		rec, err := s.Get(context.Background(), p.Runs[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return rec.ManifestHash
	}
	a1 := run(weft.New(wefttest.Script(wefttest.Say("x")), weft.Name("a"), echo, store.Record(s)))
	a1b := run(weft.New(wefttest.Script(wefttest.Say("x")), weft.Name("a"), echo, store.Record(s)))
	b := run(weft.New(wefttest.Script(wefttest.Say("x")), weft.Name("a"), echo, plus, store.Record(s)))
	if a1 == "" || a1 != a1b {
		t.Errorf("same definition: %q vs %q, want equal and non-empty", a1, a1b)
	}
	if a1 == b {
		t.Error("different tool sets produced the same manifest hash")
	}
	if h := run(weft.New(wefttest.Script(wefttest.Say("x")), store.Record(s))); h != "" {
		t.Errorf("unnamed agent hash = %q, want \"\" (no manifest)", h)
	}
}

// captureBuf is a synchronized buffer behind a slog text handler.
type captureBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (c *captureBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

func (c *captureBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.Write(p)
}

func captureLogger(buf *captureBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// flakyStore fails every operation while fail is set.
type flakyStore struct {
	inner store.Store
	fail  func() bool
}

func (f *flakyStore) Save(ctx context.Context, r store.RunRecord) error {
	if f.fail() {
		return errors.New("disk on fire")
	}
	return f.inner.Save(ctx, r)
}

func (f *flakyStore) Append(ctx context.Context, id string, ev ...weft.Event) error {
	if f.fail() {
		return errors.New("disk on fire")
	}
	return f.inner.Append(ctx, id, ev...)
}

func (f *flakyStore) Get(ctx context.Context, id string) (store.RunRecord, error) {
	return f.inner.Get(ctx, id)
}

func (f *flakyStore) List(ctx context.Context, q store.Query) (store.Page, error) {
	return f.inner.List(ctx, q)
}

func (f *flakyStore) Delete(ctx context.Context, id string) error {
	return f.inner.Delete(ctx, id)
}

// A failing store never fails the run: the error is logged through the
// agent's logger, and nothing the run produced is lost to the caller.
func TestRecordStoreFailureDoesNotFailRun(t *testing.T) {
	var fail bool
	s := &flakyStore{inner: store.Memory(), fail: func() bool { return fail }}
	buf := &captureBuf{}
	agt := weft.New(wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok again")), weft.Name("degraded"),
		weft.Logger(captureLogger(buf)), store.Record(s))
	fail = true // every store call errors
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatalf("a broken store must not fail the run: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "store: append failed") && !strings.Contains(out, "store: save failed") {
		t.Errorf("store errors not logged: %q", out)
	}
	fail = false
	if _, err := agt.Generate(context.Background(), weft.Prompt("again")); err != nil {
		t.Fatal(err)
	}
	p, _ := s.List(context.Background(), store.Query{})
	if p.Total != 1 {
		t.Fatalf("records = %d, want 1 (the first run's row never landed)", p.Total)
	}
}

// halfFlakyStore fails Append only; Save works — the shape that shows
// the degraded marker in a record that exists.
type halfFlakyStore struct {
	inner      store.Store
	failAppend bool
}

func (f *halfFlakyStore) Save(ctx context.Context, r store.RunRecord) error {
	return f.inner.Save(ctx, r)
}

func (f *halfFlakyStore) Append(ctx context.Context, id string, ev ...weft.Event) error {
	if f.failAppend {
		return errors.New("append broken")
	}
	return f.inner.Append(ctx, id, ev...)
}

func (f *halfFlakyStore) Get(ctx context.Context, id string) (store.RunRecord, error) {
	return f.inner.Get(ctx, id)
}

func (f *halfFlakyStore) List(ctx context.Context, q store.Query) (store.Page, error) {
	return f.inner.List(ctx, q)
}

func (f *halfFlakyStore) Delete(ctx context.Context, id string) error {
	return f.inner.Delete(ctx, id)
}

// The degraded marker: Append fails, the closing Save succeeds — the
// record exists, the run reads succeeded, and Err says the stream has
// a gap.
func TestRecordDegradedMarkerInErr(t *testing.T) {
	inner := store.Memory()
	s := &halfFlakyStore{inner: inner, failAppend: true}
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("gap"), store.Record(s))
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	p, _ := inner.List(context.Background(), store.Query{})
	if p.Total != 1 {
		t.Fatalf("records = %d, want 1", p.Total)
	}
	rec, _ := inner.Get(context.Background(), p.Runs[0].ID)
	if rec.Status != store.Succeeded {
		t.Errorf("status = %q, want succeeded (the run worked)", rec.Status)
	}
	if !strings.HasPrefix(rec.Err, "store degraded:") {
		t.Errorf("Err = %q, want the degraded marker naming the gap", rec.Err)
	}
	if len(rec.Events) != 0 {
		t.Errorf("events = %d, want 0 — every Append failed", len(rec.Events))
	}
}
